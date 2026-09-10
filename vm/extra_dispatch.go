package vm

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func parseAmt(s string) (*big.Int, error) {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("invalid amount %q", s)
	}
	return v, nil
}

func dispatchValidators(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if err := requireFork(ctx, forks.ValidatorsV1); err != nil {
		return err
	}
	reg := NewStakingContract(stateDB, DeployStaking(stateDB))
	switch action {
	case "register":
		var p struct {
			Pubkey string `json:"pubkey"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		pk, err := hex.DecodeString(p.Pubkey)
		if err != nil {
			pk, err = b64orHex(p.Pubkey)
		}
		if err != nil {
			return fmt.Errorf("validators register: pubkey: %w", err)
		}
		// validators_admission_v1: only the block's producer or an approved
		// address may register. Judged before the bond so a refused caller
		// keeps its SPLD; the failure is SOFT (a failed receipt under
		// module_revert_v1), not a dropped transaction.
		if ctx.Forks.Active(forks.ValidatorsAdmissionV1, ctx.Height) {
			if err := reg.CheckAdmission(ctx.Caller, ctx.Producer); err != nil {
				return err
			}
		}
		// modules_v2: an Exiting record refuses a new bond until the exit
		// is finalized. Before it, the legacy rule (Active only) applies so
		// already-applied blocks replay unchanged.
		if ctx.Forks.Active(forks.ModulesV2, ctx.Height) {
			return reg.RegisterV2(ctx.Caller, pk, ctx.Height)
		}
		return reg.Register(ctx.Caller, pk, ctx.Height)
	case "unregister":
		return reg.Unregister(ctx.Caller, ctx.Height)
	case "finalize_exit":
		return reg.FinalizeExit(ctx.Caller, ctx.Height)
	case "approve", "revoke":
		if err := requireFork(ctx, forks.ValidatorsAdmissionV1); err != nil {
			return err
		}
		var p struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		addr, err := qcrypto.AddressFromString(p.Address)
		if err != nil {
			return fmt.Errorf("validators %s: address: %w", action, err)
		}
		if action == "approve" {
			return reg.Approve(ctx.Caller, addr)
		}
		return reg.Revoke(ctx.Caller, addr)
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchValidatorsRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	reg := NewStakingContract(stateDB, DeployStaking(nil))
	switch action {
	case "validator":
		var p struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		addr, err := qcrypto.AddressFromString(p.Address)
		if err != nil {
			return nil, err
		}
		return reg.GetValidator(addr)
	case "list":
		return reg.ListActive()
	case "count":
		n, err := reg.ValidatorCount()
		return map[string]uint64{"count": n}, err
	case "approved":
		var p struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		addr, err := qcrypto.AddressFromString(p.Address)
		if err != nil {
			return nil, err
		}
		ok, err := reg.IsApproved(addr)
		return map[string]bool{"approved": ok}, err
	case "bond":
		return map[string]string{"bond": ValidatorBond().String(), "spld": fmt.Sprintf("%d", ValidatorBondSPLD)}, nil
	default:
		return nil, fmt.Errorf("validators: unknown read %q", action)
	}
}

func dispatchQRC721(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if err := requireFork(ctx, forks.QRC721V1); err != nil {
		return err
	}
	switch action {
	case "deploy":
		var p struct {
			Name   string `json:"name"`
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		addr, err := DeployQRC721(stateDB, ctx.Caller, p.Name, p.Symbol, ctx.Nonce)
		if err != nil {
			return err
		}
		receipt.ContractAddress = &addr
		return nil
	case "mint", "transfer", "burn":
		var p struct {
			Collection string `json:"collection"`
			To         string `json:"to"`
			ID         string `json:"id"`
			URI        string `json:"uri"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		col, err := qcrypto.AddressFromString(p.Collection)
		if err != nil {
			return err
		}
		id, err := parseAmt(p.ID)
		if err != nil {
			return err
		}
		nft := NewQRC721Token(stateDB, col)
		if action == "mint" {
			to, err := qcrypto.AddressFromString(p.To)
			if err != nil {
				return err
			}
			return nft.Mint(ctx.Caller, to, id, p.URI)
		}
		if action == "transfer" {
			to, err := qcrypto.AddressFromString(p.To)
			if err != nil {
				return err
			}
			return nft.Transfer(ctx.Caller, to, id)
		}
		return nft.Burn(ctx.Caller, id)
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchQRC721Read(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p struct {
		Collection string `json:"collection"`
		Owner      string `json:"owner"`
		ID         string `json:"id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	col, err := qcrypto.AddressFromString(p.Collection)
	if err != nil {
		return nil, err
	}
	nft := NewQRC721Token(stateDB, col)
	switch action {
	case "owner_of":
		id, err := parseAmt(p.ID)
		if err != nil {
			return nil, err
		}
		return nft.OwnerOf(id)
	case "balance_of":
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, err
		}
		return nft.BalanceOf(owner)
	case "token_info":
		name, _ := nft.Name()
		sym, _ := nft.Symbol()
		owner, _ := nft.Owner()
		supply, _ := nft.TotalSupply()
		return map[string]string{
			"name": name, "symbol": sym,
			"owner": owner.String(), "total_supply": supply.String(),
		}, nil
	default:
		return nil, fmt.Errorf("qrc721: unknown read %q", action)
	}
}
