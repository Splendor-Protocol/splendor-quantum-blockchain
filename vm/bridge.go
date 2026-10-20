// Package vm — the one-way legacy-mainnet migration bridge (bridge_v1).
//
// Holders burn SPLD (native) or SPLV (ERC-20) on the legacy Splendor EVM
// mainnet; the burn contract there forwards the funds to a dead address and
// emits `Burned(id, sender, asset, amount, quantumRecipient)`. A relayer
// watches those events past a confirmation depth and submits one
// `bridge.claim` per burn here. The bridge then either
//
//   - releases native SPLD from the escrow pool held at the bridge's own
//     instance address — pre-funded by a plain transfer from the genesis
//     allocation, spendable by nothing but claim, since no ML-DSA key derives
//     to a module-managed address; nothing ever mints native SPLD — or
//   - mints SPLV on the module-managed QRC-20 the bridge deployed for itself
//     (owner == the bridge instance, cap == the legacy supply), the only mint
//     path that can ever satisfy the token's requireOwner.
//
// A claim is idempotent by construction: the burn id gets a claimed slot the
// moment it pays out, so a relayer crash, restart or double-submit is a SOFT
// "already claimed" receipt, never a double credit. Every failure here is the
// action's own complaint (SOFT under module_revert_v1); the module has no
// HARD paths beyond the fork gate, so a claim that fails can never jam the
// relayer's account.
package vm

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

const (
	// MaxBridgeOperators caps how many distinct addresses one instance may
	// ever list as an operator (active or not), so bridge_info's operator
	// walk is bounded. Relayers are few; 16 leaves rotation room.
	MaxBridgeOperators = 16

	// BridgeAssetSPLD / BridgeAssetSPLV are the wire values of a claim's
	// asset field and the 0/1 words the Burned event on the EVM side uses.
	BridgeAssetSPLD = "spld"
	BridgeAssetSPLV = "splv"

	// bridgeSplvDecimals matches the legacy ERC-20 (and native SPLD): 18.
	bridgeSplvDecimals = 18
)

// ── Storage slots ───────────────────────────────────────────────────────────
//
// All under the bridge instance address, next to nothing else: the SPLV token
// lives at its own derived address with ordinary "qrc20:" slots.

var (
	slotBROwner         = qcrypto.Hash32([]byte("bridge:owner"))
	slotBRPaused        = qcrypto.Hash32([]byte("bridge:paused"))
	slotBREvmChainID    = qcrypto.Hash32([]byte("bridge:evm_chain_id"))
	slotBREvmContract   = qcrypto.Hash32([]byte("bridge:evm_contract"))
	slotBRSplvToken     = qcrypto.Hash32([]byte("bridge:splv_token"))
	slotBRMaxClaim      = qcrypto.Hash32([]byte("bridge:max_claim_amount"))
	slotBRClaimedSpld   = qcrypto.Hash32([]byte("bridge:claimed_total_spld"))
	slotBRClaimedSplv   = qcrypto.Hash32([]byte("bridge:claimed_total_splv"))
	slotBROperatorCount = qcrypto.Hash32([]byte("bridge:operator_count"))
	slotBRThreshold     = qcrypto.Hash32([]byte("bridge:threshold"))
)

func brOperatorSlot(a qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("bridge:operator:"), a[:]...))
}

func brOperatorIndexSlot(a qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("bridge:operator_index:"), a[:]...))
}

func brOperatorListSlot(i uint64) [32]byte {
	return qcrypto.Hash32(append([]byte("bridge:operator_list:"), u64be(i)...))
}

// brClaimSlot marks one EVM burn id as claimed. The slot stores
// claim-height + 1 (so a claim at height 0 in a bare-StateDB test is still
// distinguishable from the empty slot).
func brClaimSlot(id uint64) [32]byte {
	return qcrypto.Hash32(append([]byte("bridge:claimed:"), u64be(id)...))
}

// BridgeAddress is where bridge.deploy by `deployer` at tx nonce `nonce` in
// the block at `height` lands: SHA3(deployer ‖ decimal(nonce) ‖ u64be(height)
// ‖ "bridge"). The height is inside — like ExchangeAddress, unlike QRC-20 —
// so devnet, testnet and mainnet instances can never share an address.
func BridgeAddress(deployer qcrypto.Address, nonce, height uint64) qcrypto.Address {
	input := append(deployer[:], []byte(fmt.Sprintf("%d", nonce))...)
	input = append(input, u64be(height)...)
	input = append(input, []byte("bridge")...)
	h := qcrypto.Hash32(input)
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// BridgeSplvAddress is where a bridge instance's module-managed SPLV token
// lands: SHA3(bridge ‖ "bridge:splv"). Derived from the instance address, so
// it inherits the instance's cross-tier uniqueness.
func BridgeSplvAddress(bridge qcrypto.Address) qcrypto.Address {
	h := qcrypto.Hash32(append(bridge[:], []byte("bridge:splv")...))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// ── Wire shapes ─────────────────────────────────────────────────────────────
//
// Amounts are decimal strings through parseUint256. The burn id is a JSON
// integer (it is a uint256 counter on the EVM side, but a chain that has seen
// 2^64 burns has bigger problems; the relayer refuses larger). evm_contract
// is 0x + 40 lowercase hex; evm_tx is 0x + 64 lowercase hex.

// bridgeDeployParams is the wire shape of bridge.deploy.
type bridgeDeployParams struct {
	EvmChainID     uint64 `json:"evm_chain_id"`
	EvmContract    string `json:"evm_contract"`
	SplvName       string `json:"splv_name"`
	SplvSymbol     string `json:"splv_symbol"`
	SplvMaxSupply  string `json:"splv_max_supply"`  // decimal, > 0: legacy SPLV supply
	MaxClaimAmount string `json:"max_claim_amount"` // decimal, > 0: per-claim bound, both assets
	Threshold      uint64 `json:"threshold"`        // >= 1: attestations required per claim
}

// bridgeActionParams is the wire shape of the owner controls.
type bridgeActionParams struct {
	Bridge    string `json:"bridge"`
	Operator  string `json:"operator,omitempty"`  // set_operator
	Active    *bool  `json:"active,omitempty"`    // set_operator (required: true grants, false revokes)
	NewOwner  string `json:"new_owner,omitempty"` // transfer_ownership
	Threshold uint64 `json:"threshold,omitempty"` // set_threshold (>= 1)
}

// bridgeAttestation is one attestor's authorisation of a claim: their ML-DSA
// public key and a signature over types.BridgeClaimDigest under CtxBridge. Go
// []byte, so standard base64 on the wire — the same rule as a tx public_key /
// signature.
type bridgeAttestation struct {
	PubKey    []byte `json:"pubkey"`
	Signature []byte `json:"signature"`
}

// bridgeClaimParams is the wire shape of bridge.claim — one EVM burn, one
// credit, authorised by >= threshold distinct attestors.
type bridgeClaimParams struct {
	Bridge       string              `json:"bridge"`
	ID           uint64              `json:"id"`     // the Burned event's id, ≥ 1
	Asset        string              `json:"asset"`  // "spld" | "splv"
	Amount       string              `json:"amount"` // decimal string, smallest unit
	Recipient    string              `json:"recipient"`
	EvmTx        string              `json:"evm_tx"`       // burn tx hash, in the digest and the event
	Attestations []bridgeAttestation `json:"attestations"` // >= threshold distinct operators
}

// ── Bridge ──────────────────────────────────────────────────────────────────

// Bridge is a handle to one deployed migration bridge instance.
type Bridge struct {
	stateDB *state.StateDB
	addr    qcrypto.Address
}

// NewBridge returns a handle to the instance at addr.
func NewBridge(stateDB *state.StateDB, addr qcrypto.Address) *Bridge {
	return &Bridge{stateDB: stateDB, addr: addr}
}

// Address is the instance address (also the escrow pool account).
func (b *Bridge) Address() qcrypto.Address { return b.addr }

// DeployBridge creates an instance at BridgeAddress(ctx.Caller, ctx.Nonce,
// ctx.Height): initialises the module-managed SPLV QRC-20 (owner == the
// instance, zero supply, capped), writes the EVM-side coordinates and caps,
// and marks the account as a contract. The deployer is the owner; relayers
// are added afterwards with SetOperator. The escrow pool is funded by a plain
// transfer to the instance address.
func DeployBridge(stateDB *state.StateDB, ctx ModuleContext, p bridgeDeployParams) (qcrypto.Address, error) {
	const what = "bridge deploy: "
	if ctx.Caller.IsZero() {
		return qcrypto.Address{}, errors.New(what + "deployer is the zero address")
	}
	if p.EvmChainID == 0 {
		return qcrypto.Address{}, errors.New(what + "evm_chain_id must be positive")
	}
	evmContract, err := parseEvmHex(p.EvmContract, 20)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf(what+"evm_contract: %w", err)
	}
	splvMax, err := parseUint256(p.SplvMaxSupply)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf(what+"splv_max_supply: %w", err)
	}
	if splvMax.Sign() <= 0 {
		return qcrypto.Address{}, errors.New(what + "splv_max_supply must be positive")
	}
	maxClaim, err := parseUint256(p.MaxClaimAmount)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf(what+"max_claim_amount: %w", err)
	}
	if maxClaim.Sign() <= 0 {
		return qcrypto.Address{}, errors.New(what + "max_claim_amount must be positive")
	}
	if p.Threshold < 1 {
		return qcrypto.Address{}, errors.New(what + "threshold must be at least 1")
	}
	if p.Threshold > MaxBridgeOperators {
		return qcrypto.Address{}, fmt.Errorf(what+"threshold %d exceeds the operator cap of %d", p.Threshold, MaxBridgeOperators)
	}
	addr := BridgeAddress(ctx.Caller, ctx.Nonce, ctx.Height)
	splv := BridgeSplvAddress(addr)
	b := NewBridge(stateDB, addr)
	if owner, err := b.Owner(); err != nil {
		return qcrypto.Address{}, err
	} else if !owner.IsZero() {
		return qcrypto.Address{}, fmt.Errorf(what+"bridge %s already deployed", addr)
	}
	if err := b.atomic(nil, func() error {
		// owner == the instance: module-managed, so no key can ever mint.
		if err := initQRC20At(stateDB, splv, addr, p.SplvName, p.SplvSymbol, bridgeSplvDecimals, splvMax); err != nil {
			return fmt.Errorf(what+"%w", err)
		}
		if err := b.setAddrSlot(slotBROwner, ctx.Caller); err != nil {
			return err
		}
		if err := b.setU64Slot(slotBREvmChainID, p.EvmChainID); err != nil {
			return err
		}
		if err := b.stateDB.SetStorageValue(addr, slotBREvmContract, evmContract); err != nil {
			return err
		}
		if err := b.setAddrSlot(slotBRSplvToken, splv); err != nil {
			return err
		}
		if err := b.setBigSlot(slotBRMaxClaim, maxClaim); err != nil {
			return err
		}
		if err := b.setU64Slot(slotBRThreshold, p.Threshold); err != nil {
			return err
		}
		markContract(stateDB, splv)
		markContract(stateDB, addr)
		return nil
	}); err != nil {
		return qcrypto.Address{}, err
	}
	return addr, nil
}

// ── Instance state ──────────────────────────────────────────────────────────

// Owner returns the instance owner (zero when nothing is deployed at addr).
func (b *Bridge) Owner() (qcrypto.Address, error) { return b.getAddrSlot(slotBROwner) }

// IsPaused reports whether claim is blocked.
func (b *Bridge) IsPaused() (bool, error) { return b.getFlagSlot(slotBRPaused) }

// EvmChainID returns the legacy chain id burns are accepted from.
func (b *Bridge) EvmChainID() (uint64, error) { return b.getU64Slot(slotBREvmChainID) }

// EvmContract returns the 20-byte burn contract address on the legacy chain.
func (b *Bridge) EvmContract() ([]byte, error) {
	v, err := b.stateDB.GetStorageValue(b.addr, slotBREvmContract)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// SplvToken returns the module-managed SPLV QRC-20 address.
func (b *Bridge) SplvToken() (qcrypto.Address, error) { return b.getAddrSlot(slotBRSplvToken) }

// MaxClaimAmount returns the per-claim bound (both assets).
func (b *Bridge) MaxClaimAmount() (*big.Int, error) { return b.getBigSlot(slotBRMaxClaim) }

// Threshold returns how many distinct attestations a claim requires.
func (b *Bridge) Threshold() (uint64, error) { return b.getU64Slot(slotBRThreshold) }

// ClaimedTotal returns the cumulative amount credited for one asset.
func (b *Bridge) ClaimedTotal(asset string) (*big.Int, error) {
	slot, err := claimedTotalSlot(asset)
	if err != nil {
		return nil, err
	}
	return b.getBigSlot(slot)
}

func claimedTotalSlot(asset string) ([32]byte, error) {
	switch asset {
	case BridgeAssetSPLD:
		return slotBRClaimedSpld, nil
	case BridgeAssetSPLV:
		return slotBRClaimedSplv, nil
	default:
		return [32]byte{}, fmt.Errorf("unknown asset %q (want %q or %q)", asset, BridgeAssetSPLD, BridgeAssetSPLV)
	}
}

// IsOperator reports whether addr may submit claims.
func (b *Bridge) IsOperator(addr qcrypto.Address) (bool, error) {
	return b.getFlagSlot(brOperatorSlot(addr))
}

// Operators lists the active operators in the order they were first added.
func (b *Bridge) Operators() ([]qcrypto.Address, error) {
	n, err := b.getU64Slot(slotBROperatorCount)
	if err != nil {
		return nil, err
	}
	out := []qcrypto.Address{}
	for i := uint64(0); i < n && i < MaxBridgeOperators; i++ {
		a, err := b.getAddrSlot(brOperatorListSlot(i))
		if err != nil {
			return nil, err
		}
		active, err := b.IsOperator(a)
		if err != nil {
			return nil, err
		}
		if active {
			out = append(out, a)
		}
	}
	return out, nil
}

// ClaimedAt reports whether the EVM burn `id` has been claimed and at which
// height.
func (b *Bridge) ClaimedAt(id uint64) (claimed bool, height uint64, err error) {
	v, err := b.getBigSlot(brClaimSlot(id))
	if err != nil {
		return false, 0, err
	}
	if v.Sign() == 0 {
		return false, 0, nil
	}
	return true, v.Uint64() - 1, nil
}

// PoolBalance returns the native SPLD escrow available for claims.
func (b *Bridge) PoolBalance() (*big.Int, error) {
	return b.stateDB.GetBalance(b.addr)
}

// BridgeInfo is the bridge_info read result.
type BridgeInfo struct {
	Address          string   `json:"address"`
	Owner            string   `json:"owner"`
	Operators        []string `json:"operators"`
	Paused           bool     `json:"paused"`
	Threshold        uint64   `json:"threshold"`
	EvmChainID       uint64   `json:"evm_chain_id"`
	EvmContract      string   `json:"evm_contract"`
	SplvToken        string   `json:"splv_token"`
	MaxClaimAmount   string   `json:"max_claim_amount"`
	ClaimedTotalSpld string   `json:"claimed_total_spld"`
	ClaimedTotalSplv string   `json:"claimed_total_splv"`
	PoolBalance      string   `json:"pool_balance"`
}

// Info returns the instance parameters and running totals.
func (b *Bridge) Info() (*BridgeInfo, error) {
	owner, err := b.Owner()
	if err != nil {
		return nil, err
	}
	ops, err := b.Operators()
	if err != nil {
		return nil, err
	}
	paused, err := b.IsPaused()
	if err != nil {
		return nil, err
	}
	threshold, err := b.Threshold()
	if err != nil {
		return nil, err
	}
	chainID, err := b.EvmChainID()
	if err != nil {
		return nil, err
	}
	contract, err := b.EvmContract()
	if err != nil {
		return nil, err
	}
	splv, err := b.SplvToken()
	if err != nil {
		return nil, err
	}
	maxClaim, err := b.MaxClaimAmount()
	if err != nil {
		return nil, err
	}
	spld, err := b.ClaimedTotal(BridgeAssetSPLD)
	if err != nil {
		return nil, err
	}
	splvTotal, err := b.ClaimedTotal(BridgeAssetSPLV)
	if err != nil {
		return nil, err
	}
	pool, err := b.PoolBalance()
	if err != nil {
		return nil, err
	}
	opStrs := make([]string, len(ops))
	for i, a := range ops {
		opStrs[i] = a.String()
	}
	return &BridgeInfo{
		Address: b.addr.String(), Owner: owner.String(), Operators: opStrs, Paused: paused,
		Threshold:  threshold,
		EvmChainID: chainID, EvmContract: "0x" + hex.EncodeToString(contract),
		SplvToken: splv.String(), MaxClaimAmount: maxClaim.String(),
		ClaimedTotalSpld: spld.String(), ClaimedTotalSplv: splvTotal.String(),
		PoolBalance: pool.String(),
	}, nil
}

// ── Guards ──────────────────────────────────────────────────────────────────

func (b *Bridge) requireDeployed(what string) error {
	owner, err := b.Owner()
	if err != nil {
		return err
	}
	if owner.IsZero() {
		return fmt.Errorf("%sno bridge is deployed at %s", what, b.addr)
	}
	return nil
}

func (b *Bridge) requireOwner(what string, caller qcrypto.Address) error {
	if err := b.requireDeployed(what); err != nil {
		return err
	}
	owner, err := b.Owner()
	if err != nil {
		return err
	}
	if caller != owner {
		return fmt.Errorf("%scaller %s is not the owner", what, caller)
	}
	return nil
}

func (b *Bridge) requireOwnerOrOperator(what string, caller qcrypto.Address) error {
	if err := b.requireDeployed(what); err != nil {
		return err
	}
	owner, err := b.Owner()
	if err != nil {
		return err
	}
	if caller == owner {
		return nil
	}
	ok, err := b.IsOperator(caller)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%scaller %s is neither the owner nor an operator", what, caller)
	}
	return nil
}

func (b *Bridge) requireNotPaused(what string) error {
	paused, err := b.IsPaused()
	if err != nil {
		return err
	}
	if paused {
		return errors.New(what + "bridge is paused")
	}
	return nil
}

// ── Owner controls ──────────────────────────────────────────────────────────

// Pause blocks claim. Owner-only.
func (b *Bridge) Pause(caller qcrypto.Address, receipt *types.Receipt) error {
	const what = "bridge pause: "
	if err := b.requireOwner(what, caller); err != nil {
		return err
	}
	return b.atomic(receipt, func() error {
		if err := b.setFlagSlot(slotBRPaused, true); err != nil {
			return err
		}
		b.log(receipt, PausedEvent(b.addr, caller))
		return nil
	})
}

// Unpause lifts Pause. Owner-only.
func (b *Bridge) Unpause(caller qcrypto.Address, receipt *types.Receipt) error {
	const what = "bridge unpause: "
	if err := b.requireOwner(what, caller); err != nil {
		return err
	}
	return b.atomic(receipt, func() error {
		if err := b.setFlagSlot(slotBRPaused, false); err != nil {
			return err
		}
		b.log(receipt, UnpausedEvent(b.addr, caller))
		return nil
	})
}

// SetOperator grants or revokes the right to submit claims. Same list
// semantics as the exchange: an address joins the bounded list the first time
// it is granted, revoking keeps its slot, revoking a never-listed address is
// a silent no-op. Owner-only.
func (b *Bridge) SetOperator(caller, operator qcrypto.Address, active bool, receipt *types.Receipt) error {
	const what = "bridge set_operator: "
	if err := b.requireOwner(what, caller); err != nil {
		return err
	}
	if operator.IsZero() {
		return errors.New(what + "operator is the zero address")
	}
	if operator == b.addr || operator == ModuleRouterAddress {
		return fmt.Errorf(what+"operator %s cannot be a module address", operator)
	}
	return b.atomic(receipt, func() error {
		idx, err := b.getU64Slot(brOperatorIndexSlot(operator))
		if err != nil {
			return err
		}
		if idx == 0 && !active {
			return nil
		}
		if idx == 0 {
			n, err := b.getU64Slot(slotBROperatorCount)
			if err != nil {
				return err
			}
			if n >= MaxBridgeOperators {
				return fmt.Errorf(what+"operator list is full (%d)", MaxBridgeOperators)
			}
			if err := b.setAddrSlot(brOperatorListSlot(n), operator); err != nil {
				return err
			}
			if err := b.setU64Slot(brOperatorIndexSlot(operator), n+1); err != nil {
				return err
			}
			if err := b.setU64Slot(slotBROperatorCount, n+1); err != nil {
				return err
			}
		}
		if err := b.setFlagSlot(brOperatorSlot(operator), active); err != nil {
			return err
		}
		b.log(receipt, OperatorSetEvent(b.addr, operator, active))
		return nil
	})
}

// TransferOwnership hands the instance to newOwner (non-zero, not the
// instance, the router or any contract — none of those can sign an owner
// action). Single-step by design. Owner-only.
func (b *Bridge) TransferOwnership(caller, newOwner qcrypto.Address, receipt *types.Receipt) error {
	const what = "bridge transfer_ownership: "
	if err := b.requireOwner(what, caller); err != nil {
		return err
	}
	if newOwner.IsZero() {
		return errors.New(what + "new_owner is the zero address")
	}
	if newOwner == b.addr || newOwner == ModuleRouterAddress {
		return fmt.Errorf(what+"new_owner %s cannot be a module address", newOwner)
	}
	acc, err := b.stateDB.GetAccount(newOwner)
	if err != nil {
		return fmt.Errorf(what+"new_owner %s: %w", newOwner, err)
	}
	if acc.IsContract {
		return fmt.Errorf(what+"new_owner %s is a contract and can never act as owner", newOwner)
	}
	return b.atomic(receipt, func() error {
		if err := b.setAddrSlot(slotBROwner, newOwner); err != nil {
			return err
		}
		b.log(receipt, OwnershipTransferredEvent(b.addr, caller, newOwner))
		return nil
	})
}

// SetThreshold changes how many distinct attestations a claim requires
// (>= 1, <= the operator cap). Owner-only. Lowering it below the number of
// active operators is allowed (an M-of-N choice); raising it above the number
// that exists simply means no claim passes until enough operators are added —
// fail-closed, the safe direction.
func (b *Bridge) SetThreshold(caller qcrypto.Address, threshold uint64, receipt *types.Receipt) error {
	const what = "bridge set_threshold: "
	if err := b.requireOwner(what, caller); err != nil {
		return err
	}
	if threshold < 1 {
		return errors.New(what + "threshold must be at least 1")
	}
	if threshold > MaxBridgeOperators {
		return fmt.Errorf(what+"threshold %d exceeds the operator cap of %d", threshold, MaxBridgeOperators)
	}
	return b.atomic(receipt, func() error {
		if err := b.setU64Slot(slotBRThreshold, threshold); err != nil {
			return err
		}
		b.log(receipt, ThresholdUpdatedEvent(b.addr, threshold))
		return nil
	})
}

// requireAttestations verifies that at least the instance threshold of DISTINCT
// active operators have signed digest under CtxBridge. The slice is bounded
// (an over-long one is refused before any signature check, since module calls
// are unmetered); every supplied attestation must be exactly the canonical
// key/signature sizes, well-formed, from an active operator, and carry a valid
// signature; a repeated operator counts once. Iteration is over the slice
// (deterministic order) and the map is used only for de-duplication lookups,
// never iterated.
func (b *Bridge) requireAttestations(what string, digest [32]byte, atts []bridgeAttestation) error {
	threshold, err := b.Threshold()
	if err != nil {
		return err
	}
	if uint64(len(atts)) > MaxBridgeOperators {
		return fmt.Errorf("%s%d attestations exceeds the operator cap of %d", what, len(atts), MaxBridgeOperators)
	}
	seen := make(map[qcrypto.Address]bool, len(atts))
	for i, a := range atts {
		// Refuse wrong-size blobs before any parse: a malformed attestation
		// must fail cheaply and precisely, not through allocation or a
		// confusing deserialisation error.
		if len(a.PubKey) != qcrypto.PublicKeySize {
			return fmt.Errorf("%sattestation %d: pubkey is %d bytes, want %d", what, i, len(a.PubKey), qcrypto.PublicKeySize)
		}
		if len(a.Signature) != qcrypto.SignatureSize {
			return fmt.Errorf("%sattestation %d: signature is %d bytes, want %d", what, i, len(a.Signature), qcrypto.SignatureSize)
		}
		pub, err := qcrypto.PublicKeyFromBytes(a.PubKey)
		if err != nil {
			return fmt.Errorf("%sattestation %d: invalid pubkey: %w", what, i, err)
		}
		attestor := qcrypto.DeriveAddress(pub)
		active, err := b.IsOperator(attestor)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("%sattestation %d: %s is not an active operator", what, i, attestor)
		}
		if !qcrypto.Verify(pub, digest[:], qcrypto.CtxBridge, a.Signature) {
			return fmt.Errorf("%sattestation %d: invalid signature for %s", what, i, attestor)
		}
		seen[attestor] = true
	}
	if uint64(len(seen)) < threshold {
		return fmt.Errorf("%s%d distinct attestations, need %d", what, len(seen), threshold)
	}
	return nil
}

// ── Claim ───────────────────────────────────────────────────────────────────

// Claim credits one EVM burn: native SPLD from the escrow pool, or an SPLV
// mint on the module-managed token. The submitter must be owner-or-operator
// (a spam gate), but the credit is authorised by >= threshold distinct
// attestations, not by the submitter alone. Idempotent per burn id; refused
// while paused. Every refusal is SOFT — the action's own complaint, a failed
// receipt, never a jammed nonce.
func (b *Bridge) Claim(ctx ModuleContext, p bridgeClaimParams, receipt *types.Receipt) error {
	const what = "bridge claim: "
	if err := b.requireOwnerOrOperator(what, ctx.Caller); err != nil {
		return err
	}
	if err := b.requireNotPaused(what); err != nil {
		return err
	}
	if p.ID == 0 {
		return errors.New(what + "id must be positive (burn ids start at 1)")
	}
	assetWord, err := bridgeAssetWord(p.Asset)
	if err != nil {
		return errors.New(what + err.Error())
	}
	amount, err := parseUint256(p.Amount)
	if err != nil {
		return fmt.Errorf(what+"amount: %w", err)
	}
	if amount.Sign() <= 0 {
		return errors.New(what + "amount must be positive")
	}
	maxClaim, err := b.MaxClaimAmount()
	if err != nil {
		return err
	}
	if amount.Cmp(maxClaim) > 0 {
		return fmt.Errorf(what+"amount %s exceeds max_claim_amount %s", amount, maxClaim)
	}
	recipient, err := qcrypto.AddressFromString(p.Recipient)
	if err != nil {
		return fmt.Errorf(what+"recipient: %w", err)
	}
	if recipient.IsZero() {
		return errors.New(what + "recipient is the zero address")
	}
	if recipient == b.addr || recipient == ModuleRouterAddress {
		return fmt.Errorf(what+"recipient %s cannot be a module address", recipient)
	}
	recAcc, err := b.stateDB.GetAccount(recipient)
	if err != nil {
		return fmt.Errorf(what+"recipient %s: %w", recipient, err)
	}
	if recAcc.IsContract {
		return fmt.Errorf(what+"recipient %s is a contract; no key could ever spend the credit", recipient)
	}
	evmTx, err := parseEvmHex(p.EvmTx, 32)
	if err != nil {
		return fmt.Errorf(what+"evm_tx: %w", err)
	}
	if [32]byte(evmTx) == [32]byte{} {
		// The burn's provenance is its legacy transaction. An all-zero hash
		// can never be one, and crediting it would rest the payout on
		// attestations alone, with nothing to audit against the legacy chain.
		// Refuse it — defense in depth beneath the attestation layer.
		return errors.New(what + "evm_tx is zero; a claim must reference a real legacy burn transaction")
	}
	// Threshold attestations authorise the credit — not the submitter alone.
	// A compromised operator key, or a single hostile legacy-chain RPC feeding
	// one honest operator, cannot move funds without ≥ threshold DISTINCT
	// attestors independently signing this exact burn.
	var evmTx32 [32]byte
	copy(evmTx32[:], evmTx)
	digest, err := types.BridgeClaimDigest(types.ChainID, b.addr, p.ID, assetWord, amount, recipient, evmTx32)
	if err != nil {
		return fmt.Errorf(what+"%w", err)
	}
	if err := b.requireAttestations(what, digest, p.Attestations); err != nil {
		return err
	}
	claimed, _, err := b.ClaimedAt(p.ID)
	if err != nil {
		return err
	}
	if claimed {
		// Fast-path refusal; re-checked inside the snapshot below so the
		// claimed slot and the payout are one atomic unit.
		return fmt.Errorf(what+"burn %d is already claimed", p.ID)
	}
	totalSlot, err := claimedTotalSlot(p.Asset)
	if err != nil {
		return errors.New(what + err.Error())
	}
	return b.atomic(receipt, func() error {
		// The idempotency core, enforced where it matters: inside the
		// snapshot, immediately before the payout. No reordering (or a
		// module path that could ever reenter) can credit twice between a
		// check and the claimed-slot write.
		claimed, _, err := b.ClaimedAt(p.ID)
		if err != nil {
			return err
		}
		if claimed {
			return fmt.Errorf(what+"burn %d is already claimed", p.ID)
		}
		switch p.Asset {
		case BridgeAssetSPLD:
			pool, err := b.PoolBalance()
			if err != nil {
				return err
			}
			if pool.Cmp(amount) < 0 {
				return fmt.Errorf(what+"escrow pool holds %s, claim needs %s", pool, amount)
			}
			if err := b.stateDB.Transfer(b.addr, recipient, amount); err != nil {
				return fmt.Errorf(what+"%w", err)
			}
		case BridgeAssetSPLV:
			splv, err := b.SplvToken()
			if err != nil {
				return err
			}
			// caller == owner == the instance address: the only mint path.
			if err := NewQRC20Token(b.stateDB, splv).MintWithEvents(b.addr, recipient, amount, receipt); err != nil {
				return fmt.Errorf(what+"%w", err)
			}
		default:
			// Unreachable: p.Asset is validated by bridgeAssetWord and
			// claimedTotalSlot above. The default exists so a future refactor
			// that drops either check fails loudly instead of marking an id
			// claimed without crediting anything.
			return fmt.Errorf(what+"unhandled asset %q", p.Asset)
		}
		if err := b.setBigSlot(brClaimSlot(p.ID), new(big.Int).SetUint64(ctx.Height+1)); err != nil {
			return err
		}
		total, err := b.getBigSlot(totalSlot)
		if err != nil {
			return err
		}
		if err := b.setBigSlot(totalSlot, new(big.Int).Add(total, amount)); err != nil {
			return err
		}
		b.log(receipt, BridgeClaimedEvent(b.addr, p.ID, assetWord, amount, recipient, evmTx32))
		return nil
	})
}

func bridgeAssetWord(asset string) (uint8, error) {
	switch asset {
	case BridgeAssetSPLD:
		return 0, nil
	case BridgeAssetSPLV:
		return 1, nil
	default:
		return 0, fmt.Errorf("unknown asset %q (want %q or %q)", asset, BridgeAssetSPLD, BridgeAssetSPLV)
	}
}

// parseEvmHex decodes a 0x-prefixed, lowercase EVM hex field of exactly n
// bytes. Lowercase is enforced so the stored bytes and every signed wire
// carry one canonical spelling.
func parseEvmHex(s string, n int) ([]byte, error) {
	if len(s) != 2+2*n || s[0] != '0' || s[1] != 'x' {
		return nil, fmt.Errorf("want 0x + %d lowercase hex chars, got %q", 2*n, s)
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, fmt.Errorf("invalid lowercase hex %q", s)
		}
	}
	return hex.DecodeString(s[2:])
}

// ── Events ──────────────────────────────────────────────────────────────────

// BridgeClaimedEvent: id and recipient indexed; asset, amount and the legacy
// burn tx hash as data words.
func BridgeClaimedEvent(contract qcrypto.Address, id uint64, asset uint8, amount *big.Int, recipient qcrypto.Address, evmTx [32]byte) *types.Log {
	return NewEvent(contract, "BridgeClaimed(uint64,uint8,uint256,sqaddress,bytes32)").
		IndexUint256(new(big.Int).SetUint64(id)).
		IndexAddr(recipient).
		DataUint256(big.NewInt(int64(asset))).
		DataUint256(amount).
		DataHash(evmTx).
		Build()
}

// ThresholdUpdatedEvent: the new attestation threshold as a data word.
func ThresholdUpdatedEvent(contract qcrypto.Address, threshold uint64) *types.Log {
	return NewEvent(contract, "ThresholdUpdated(uint64)").
		DataUint256(new(big.Int).SetUint64(threshold)).
		Build()
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// atomic runs fn inside a StateDB snapshot and reverts it — and any logs fn
// appended to receipt — when fn errors. Same contract as Exchange.atomic.
func (b *Bridge) atomic(receipt *types.Receipt, fn func() error) error {
	snap := b.stateDB.Snapshot()
	logs := 0
	if receipt != nil {
		logs = len(receipt.Logs)
	}
	if err := fn(); err != nil {
		if rerr := b.stateDB.RevertToSnapshot(snap); rerr != nil {
			return hard(fmt.Errorf("bridge: %w", rerr))
		}
		if receipt != nil {
			receipt.Logs = receipt.Logs[:logs]
		}
		return err
	}
	if err := b.stateDB.CommitSnapshot(snap); err != nil {
		return hard(fmt.Errorf("bridge: %w", err))
	}
	return nil
}

func (b *Bridge) log(receipt *types.Receipt, l *types.Log) {
	if receipt != nil {
		receipt.AddLog(l)
	}
}

func (b *Bridge) setBigSlot(slot [32]byte, v *big.Int) error {
	return b.stateDB.SetStorageValue(b.addr, slot, v.Bytes())
}

// The readers return a StateDB read error as an error (the exchange's
// divergence lesson): an empty slot is the zero value, an I/O fault is not.
func (b *Bridge) getBigSlot(slot [32]byte) (*big.Int, error) {
	v, err := b.stateDB.GetStorageValue(b.addr, slot)
	if err != nil {
		return big.NewInt(0), err
	}
	if len(v) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(v), nil
}

func (b *Bridge) setU64Slot(slot [32]byte, v uint64) error {
	return b.setBigSlot(slot, new(big.Int).SetUint64(v))
}

func (b *Bridge) getU64Slot(slot [32]byte) (uint64, error) {
	v, err := b.getBigSlot(slot)
	if err != nil {
		return 0, err
	}
	if v.BitLen() > 64 {
		return 0, fmt.Errorf("bridge: slot %x holds %s, not a uint64", slot[:4], v)
	}
	return v.Uint64(), nil
}

func (b *Bridge) setAddrSlot(slot [32]byte, a qcrypto.Address) error {
	return b.stateDB.SetStorageValue(b.addr, slot, a[:])
}

func (b *Bridge) getAddrSlot(slot [32]byte) (qcrypto.Address, error) {
	v, err := b.stateDB.GetStorageValue(b.addr, slot)
	if err != nil {
		return qcrypto.Address{}, err
	}
	if len(v) < qcrypto.AddressSize {
		return qcrypto.Address{}, nil
	}
	var a qcrypto.Address
	copy(a[:], v)
	return a, nil
}

func (b *Bridge) setFlagSlot(slot [32]byte, v bool) error {
	if v {
		return b.stateDB.SetStorageValue(b.addr, slot, []byte{1})
	}
	return b.stateDB.SetStorageValue(b.addr, slot, []byte{0})
}

func (b *Bridge) getFlagSlot(slot [32]byte) (bool, error) {
	v, err := b.stateDB.GetStorageValue(b.addr, slot)
	if err != nil {
		return false, err
	}
	if len(v) == 0 {
		return false, nil
	}
	return v[0] == 1, nil
}
