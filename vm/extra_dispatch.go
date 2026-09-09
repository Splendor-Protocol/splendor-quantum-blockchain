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
	case "bond":
		return map[string]string{"bond": ValidatorBond().String(), "spld": fmt.Sprintf("%d", ValidatorBondSPLD)}, nil
	default:
		return nil, fmt.Errorf("validators: unknown read %q", action)
	}
}

func dispatchLending(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if err := requireFork(ctx, forks.LendingV1); err != nil {
		return err
	}
	switch action {
	case "deploy":
		addr := DeployLending(stateDB, ctx.Caller, ctx.Nonce)
		receipt.ContractAddress = &addr
		return nil
	case "enable_asset":
		var p struct {
			Lending string `json:"lending"`
			Asset   string `json:"asset"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		laddr, err := qcrypto.AddressFromString(p.Lending)
		if err != nil {
			return err
		}
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return err
		}
		return NewLendingProtocol(stateDB, laddr).EnableAsset(ctx.Caller, asset, CollateralFactorBPS, LiquidationThresholdBPS)
	case "set_oracle":
		var p struct {
			Lending string `json:"lending"`
			Oracle  string `json:"oracle"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		laddr, err := qcrypto.AddressFromString(p.Lending)
		if err != nil {
			return err
		}
		oaddr, err := qcrypto.AddressFromString(p.Oracle)
		if err != nil {
			return err
		}
		return NewLendingProtocol(stateDB, laddr).SetOracle(ctx.Caller, oaddr)
	case "deposit", "withdraw", "borrow", "repay":
		var p struct {
			Lending    string `json:"lending"`
			Asset      string `json:"asset"`
			Amount     string `json:"amount"`
			Collateral string `json:"collateral"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		laddr, err := qcrypto.AddressFromString(p.Lending)
		if err != nil {
			return err
		}
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return err
		}
		amt, err := parseAmt(p.Amount)
		if err != nil {
			return err
		}
		lp := NewLendingProtocol(stateDB, laddr)
		if action == "deposit" {
			return lp.Deposit(ctx.Caller, asset, amt, ctx.Height)
		}
		if action == "withdraw" {
			_, err = lp.Withdraw(ctx.Caller, asset, amt, ctx.Height)
			return err
		}
		if action == "repay" {
			_, err = lp.Repay(ctx.Caller, asset, amt, ctx.Height)
			return err
		}
		col, err := qcrypto.AddressFromString(p.Collateral)
		if err != nil {
			return err
		}
		return lp.BorrowFromOracle(ctx.Caller, asset, col, amt, ctx.Height)
	case "liquidate":
		// Repays up to half of `borrower`'s debt in `asset` and takes
		// `collateral` plus the liquidation bonus, at oracle prices, only if
		// the borrower's whole position is below its liquidation threshold.
		var p struct {
			Lending    string `json:"lending"`
			Borrower   string `json:"borrower"`
			Asset      string `json:"asset"`      // the debt asset being repaid
			Collateral string `json:"collateral"` // the collateral asset seized
			Amount     string `json:"amount"`     // repay amount, capped at the close factor
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		laddr, err := qcrypto.AddressFromString(p.Lending)
		if err != nil {
			return err
		}
		borrower, err := qcrypto.AddressFromString(p.Borrower)
		if err != nil {
			return fmt.Errorf("lending liquidate: borrower: %w", err)
		}
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return err
		}
		col, err := qcrypto.AddressFromString(p.Collateral)
		if err != nil {
			return err
		}
		amt, err := parseAmt(p.Amount)
		if err != nil {
			return err
		}
		_, err = NewLendingProtocol(stateDB, laddr).LiquidateFromOracle(ctx.Caller, borrower, asset, col, amt, ctx.Height)
		return err
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchLendingRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p struct {
		Lending string `json:"lending"`
		Asset   string `json:"asset,omitempty"`
		Owner   string `json:"owner,omitempty"`
		Height  uint64 `json:"height,omitempty"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	laddr, err := qcrypto.AddressFromString(p.Lending)
	if err != nil {
		return nil, err
	}
	lp := NewLendingProtocol(stateDB, laddr)
	switch action {
	case "pool":
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return nil, err
		}
		return lp.GetPool(asset)
	case "deposit_balance":
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return nil, err
		}
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, err
		}
		return lp.GetDepositBalance(owner, asset)
	case "borrow_balance":
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return nil, err
		}
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, err
		}
		return lp.GetBorrowBalance(owner, asset)
	case "assets":
		assets, err := lp.RegisteredAssets()
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(assets))
		for _, a := range assets {
			out = append(out, a.String())
		}
		return out, nil
	case "health":
		// Advisory read: values the whole position at the CURRENT oracle
		// prices and the stored indexes (interest is not accrued here; a
		// read must not write). Height 0 asks the oracle for its latest.
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, err
		}
		return lp.AccountHealth(owner, p.Height)
	default:
		return nil, fmt.Errorf("lending: unknown read %q", action)
	}
}

func dispatchOracle(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if err := requireFork(ctx, forks.OracleV1); err != nil {
		return err
	}
	switch action {
	case "deploy":
		addr := DeployOracle(stateDB, ctx.Caller)
		receipt.ContractAddress = &addr
		return nil
	case "register_feeder":
		var p struct {
			Oracle string `json:"oracle"`
			Feeder string `json:"feeder"`
			Pubkey string `json:"pubkey"`
			Name   string `json:"name"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		oaddr, err := qcrypto.AddressFromString(p.Oracle)
		if err != nil {
			return err
		}
		feeder, err := qcrypto.AddressFromString(p.Feeder)
		if err != nil {
			return err
		}
		pk, err := hex.DecodeString(p.Pubkey)
		if err != nil {
			return err
		}
		return NewPriceOracle(stateDB, oaddr).RegisterFeeder(ctx.Caller, feeder, pk, p.Name)
	case "submit_price":
		var p struct {
			Oracle    string `json:"oracle"`
			Asset     string `json:"asset"`
			Price     string `json:"price"`
			Signature string `json:"signature"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		oaddr, err := qcrypto.AddressFromString(p.Oracle)
		if err != nil {
			return err
		}
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return err
		}
		price, err := parseAmt(p.Price)
		if err != nil {
			return err
		}
		sig, err := b64orHex(p.Signature)
		if err != nil {
			return err
		}
		return NewPriceOracle(stateDB, oaddr).SubmitPrice(ctx.Caller, asset, price, ctx.Height, sig)
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchOracleRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p struct {
		Oracle string `json:"oracle"`
		Asset  string `json:"asset"`
		Feeder string `json:"feeder"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	oaddr, err := qcrypto.AddressFromString(p.Oracle)
	if err != nil {
		return nil, err
	}
	o := NewPriceOracle(stateDB, oaddr)
	switch action {
	case "price":
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return nil, err
		}
		return o.GetPrice(asset, 0)
	case "feeder":
		feeder, err := qcrypto.AddressFromString(p.Feeder)
		if err != nil {
			return nil, err
		}
		return o.GetFeeder(feeder)
	default:
		return nil, fmt.Errorf("oracle: unknown read %q", action)
	}
}

func dispatchMultisig(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if err := requireFork(ctx, forks.MultisigV1); err != nil {
		return err
	}
	switch action {
	case "deploy":
		var p struct {
			Threshold uint64 `json:"threshold"`
			Owners    []struct {
				Address string `json:"address"`
				Pubkey  string `json:"pubkey"`
				Name    string `json:"name"`
			} `json:"owners"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if len(p.Owners) > MaxMultisigOwners {
			return fmt.Errorf("multisig deploy: %d owners, above the cap of %d", len(p.Owners), MaxMultisigOwners)
		}
		owners := make([]MSigOwner, 0, len(p.Owners))
		for _, o := range p.Owners {
			// A key that does not decode used to become an empty key, which
			// DeployMultiSig then refused with a message about the wrong
			// thing; say what was actually wrong.
			pk, err := b64orHex(o.Pubkey)
			if err != nil {
				return fmt.Errorf("multisig deploy: owner %s pubkey: %w", o.Address, err)
			}
			if err := capString("multisig deploy", "name", o.Name, MaxModuleNameBytes); err != nil {
				return err
			}
			owners = append(owners, MSigOwner{Address: o.Address, PublicKey: pk, Name: o.Name})
		}
		addr, err := DeployMultiSig(stateDB, ctx.Caller, owners, p.Threshold, ctx.Nonce)
		if err != nil {
			return err
		}
		markContract(stateDB, addr)
		receipt.ContractAddress = &addr
		return nil

	case "submit":
		// tx_type selects the payload: "transfer" (default) moves value to
		// `to`; "add_owner" adds {owner, pubkey, name}; "remove_owner" removes
		// {owner}; "change_threshold" sets {threshold}. Contract calls are
		// not offered: the module cannot execute them.
		var p struct {
			Wallet      string `json:"wallet"`
			TxType      string `json:"tx_type,omitempty"`
			To          string `json:"to,omitempty"`
			Value       string `json:"value,omitempty"`
			Owner       string `json:"owner,omitempty"`
			Pubkey      string `json:"pubkey,omitempty"`
			Name        string `json:"name,omitempty"`
			Threshold   uint64 `json:"threshold,omitempty"`
			Description string `json:"description,omitempty"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		waddr, err := qcrypto.AddressFromString(p.Wallet)
		if err != nil {
			return err
		}
		w := NewMultiSigWallet(stateDB, waddr)
		var (
			txType MSigTxType
			to     qcrypto.Address
			val    = new(big.Int)
			data   []byte
		)
		switch p.TxType {
		case "", "transfer":
			txType = MSigTransfer
			if to, err = qcrypto.AddressFromString(p.To); err != nil {
				return fmt.Errorf("multisig submit: to: %w", err)
			}
			if val, err = parseAmt(p.Value); err != nil {
				return fmt.Errorf("multisig submit: value: %w", err)
			}
		case "add_owner":
			txType = MSigAddOwner
			owner, err := qcrypto.AddressFromString(p.Owner)
			if err != nil {
				return fmt.Errorf("multisig submit: owner: %w", err)
			}
			pk, err := b64orHex(p.Pubkey)
			if err != nil {
				return fmt.Errorf("multisig submit: pubkey: %w", err)
			}
			data = EncodeAddOwnerData(owner, pk, p.Name)
		case "remove_owner":
			txType = MSigRemOwner
			owner, err := qcrypto.AddressFromString(p.Owner)
			if err != nil {
				return fmt.Errorf("multisig submit: owner: %w", err)
			}
			data = EncodeRemoveOwnerData(owner)
		case "change_threshold":
			txType = MSigChangeThreshold
			data = EncodeChangeThresholdData(p.Threshold)
		default:
			return fmt.Errorf("multisig submit: unsupported tx_type %q", p.TxType)
		}
		_, err = w.Submit(ctx.Caller, txType, to, val, data, p.Description)
		return err

	case "confirm":
		var p struct {
			Wallet    string `json:"wallet"`
			Nonce     uint64 `json:"nonce"`
			Signature string `json:"signature"` // ML-DSA-65 over ProposalHash, hex or base64
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		waddr, err := qcrypto.AddressFromString(p.Wallet)
		if err != nil {
			return err
		}
		sig, err := b64orHex(p.Signature)
		if err != nil {
			return fmt.Errorf("multisig confirm: signature: %w", err)
		}
		return NewMultiSigWallet(stateDB, waddr).Confirm(ctx.Caller, p.Nonce, sig)

	case "revoke", "execute", "cancel":
		var p struct {
			Wallet string `json:"wallet"`
			Nonce  uint64 `json:"nonce"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		waddr, err := qcrypto.AddressFromString(p.Wallet)
		if err != nil {
			return err
		}
		w := NewMultiSigWallet(stateDB, waddr)
		switch action {
		case "revoke":
			return w.Revoke(ctx.Caller, p.Nonce)
		case "execute":
			return w.Execute(ctx.Caller, p.Nonce, stateDB)
		default:
			return w.Cancel(ctx.Caller, p.Nonce)
		}

	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

// MaxMultisigOwners bounds a wallet's owner list at deploy: each owner is a
// 1952-byte key persisted as state for a flat fee.
const MaxMultisigOwners = 32

func dispatchMultisigRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p struct {
		Wallet  string `json:"wallet"`
		Nonce   uint64 `json:"nonce"`
		Address string `json:"address,omitempty"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	waddr, err := qcrypto.AddressFromString(p.Wallet)
	if err != nil {
		return nil, err
	}
	w := NewMultiSigWallet(stateDB, waddr)
	switch action {
	case "threshold":
		return w.Threshold()
	case "proposal":
		return w.GetProposal(p.Nonce)
	case "owner":
		a, err := qcrypto.AddressFromString(p.Address)
		if err != nil {
			return nil, err
		}
		return w.GetOwner(a)
	case "owner_count":
		return w.OwnerCount()
	case "nonce":
		return w.CurrentNonce()
	case "balance":
		return w.Balance(stateDB)
	case "confirmations":
		return w.ConfirmationCount(p.Nonce)
	case "proposal_hash":
		// What an owner signs for `confirm`: bound to this chain, this wallet,
		// this nonce and this payload.
		prop, err := w.GetProposal(p.Nonce)
		if err != nil || prop == nil {
			return nil, fmt.Errorf("multisig: proposal %d not found", p.Nonce)
		}
		to, err := qcrypto.AddressFromString(prop.To)
		if err != nil {
			return nil, err
		}
		h := ProposalHash(waddr, p.Nonce, prop.TxType, to, prop.Value, prop.Data)
		return map[string]string{"hash": hex.EncodeToString(h[:]), "context": string(qcrypto.CtxMultisig)}, nil
	default:
		return nil, fmt.Errorf("multisig: unknown read %q", action)
	}
}

func dispatchGovernance(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if err := requireFork(ctx, forks.GovernanceV1); err != nil {
		return err
	}
	g := NewGovernanceContract(stateDB, DeployGovernance(stateDB))
	switch action {
	case "submit":
		// Text (signalling) proposals only; see governance.Advisory. Voting
		// power is the caller's active validator bond.
		var p struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Payload     string `json:"payload"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		_, err := g.Submit(ctx.Caller, ProposalTypeText, p.Title, p.Description, p.Payload, ctx.Height)
		return err
	case "vote":
		var p struct {
			ID     uint64 `json:"id"`
			Option uint8  `json:"option"` // 1 yes, 2 no, 3 abstain, 4 no-with-veto
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		return g.Vote(ctx.Caller, p.ID, VoteOption(p.Option), ctx.Height)
	case "tally", "cancel":
		var p struct {
			ID uint64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if action == "tally" {
			// Anyone may close a proposal whose voting period has ended; the
			// quorum is judged against the active set's total bond NOW.
			return g.TallyNow(p.ID, ctx.Height)
		}
		return g.Cancel(ctx.Caller, p.ID, ctx.Height)
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchGovernanceRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	g := NewGovernanceContract(stateDB, DeployGovernance(nil))
	var p struct {
		ID      uint64 `json:"id,omitempty"`
		Address string `json:"address,omitempty"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
	}
	switch action {
	case "proposal":
		prop, err := g.GetProposal(p.ID)
		if err != nil || prop == nil {
			return nil, fmt.Errorf("governance: proposal %d not found", p.ID)
		}
		// The label travels with every read so no client mistakes Passed
		// for an enacted change.
		return map[string]any{
			"proposal": prop, "status_name": ProposalStatusName(prop.Status), "advisory": Advisory,
			"note": "passed proposals are signalling only; nothing executes on-chain in this build",
		}, nil
	case "count":
		n, err := g.ProposalCount()
		return map[string]uint64{"count": n}, err
	case "vote_of":
		a, err := qcrypto.AddressFromString(p.Address)
		if err != nil {
			return nil, err
		}
		opt, err := g.GetVoteOf(p.ID, a)
		return map[string]uint8{"option": uint8(opt)}, err
	case "voting_power":
		a, err := qcrypto.AddressFromString(p.Address)
		if err != nil {
			return nil, err
		}
		return map[string]string{"power": g.votingPowerOf(a).String(), "source": "active validator bond"}, nil
	case "total_voting_power":
		total, err := g.activeVotingPower()
		if err != nil {
			return nil, err
		}
		return map[string]string{"power": total.String(), "source": "sum of active validator bonds"}, nil
	default:
		return nil, fmt.Errorf("governance: unknown read %q", action)
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

func dispatchQRC4626(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if err := requireFork(ctx, forks.QRC4626V1); err != nil {
		return err
	}
	switch action {
	case "deploy":
		var p struct {
			Name   string `json:"name"`
			Symbol string `json:"symbol"`
			Asset  string `json:"asset"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		asset, err := qcrypto.AddressFromString(p.Asset)
		if err != nil {
			return err
		}
		addr, err := DeployQRC4626(stateDB, ctx.Caller, p.Name, p.Symbol, asset, ctx.Nonce)
		if err != nil {
			return err
		}
		receipt.ContractAddress = &addr
		return nil
	case "deposit", "mint", "withdraw", "redeem", "approve":
		// Every share movement in one shape. `to` defaults to the caller;
		// `owner` (withdraw/redeem) defaults to the caller and otherwise
		// spends the caller's share allowance from that owner. The bounds
		// are the slippage guards: a deposit that would mint fewer than
		// min_shares, a mint that would cost more than max_assets, a
		// withdraw that would burn more than max_shares, or a redeem that
		// would return fewer than min_assets is refused before any transfer.
		var p struct {
			Vault     string `json:"vault"`
			Assets    string `json:"assets,omitempty"`
			Shares    string `json:"shares,omitempty"`
			To        string `json:"to,omitempty"`
			Owner     string `json:"owner,omitempty"`
			Spender   string `json:"spender,omitempty"`
			MinShares string `json:"min_shares,omitempty"`
			MaxShares string `json:"max_shares,omitempty"`
			MinAssets string `json:"min_assets,omitempty"`
			MaxAssets string `json:"max_assets,omitempty"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		vaddr, err := qcrypto.AddressFromString(p.Vault)
		if err != nil {
			return fmt.Errorf("qrc4626 %s: vault: %w", action, err)
		}
		v := NewQRC4626Vault(stateDB, vaddr)
		if _, err := v.Asset(); err != nil {
			return fmt.Errorf("qrc4626 %s: %s is not a vault", action, vaddr)
		}
		to := ctx.Caller
		if p.To != "" {
			if to, err = qcrypto.AddressFromString(p.To); err != nil {
				return fmt.Errorf("qrc4626 %s: to: %w", action, err)
			}
		}
		owner := ctx.Caller
		if p.Owner != "" {
			if owner, err = qcrypto.AddressFromString(p.Owner); err != nil {
				return fmt.Errorf("qrc4626 %s: owner: %w", action, err)
			}
		}
		switch action {
		case "deposit":
			assets, err := parseAmt(p.Assets)
			if err != nil {
				return fmt.Errorf("qrc4626 deposit: assets: %w", err)
			}
			if p.MinShares != "" {
				min, err := parseAmt(p.MinShares)
				if err != nil {
					return fmt.Errorf("qrc4626 deposit: min_shares: %w", err)
				}
				got, err := v.PreviewDeposit(assets)
				if err != nil {
					return err
				}
				if got.Cmp(min) < 0 {
					return fmt.Errorf("qrc4626 deposit: would mint %s shares, below min_shares %s", got, min)
				}
			}
			_, err = v.Deposit(ctx.Caller, to, assets)
			return err
		case "mint":
			shares, err := parseAmt(p.Shares)
			if err != nil {
				return fmt.Errorf("qrc4626 mint: shares: %w", err)
			}
			if p.MaxAssets != "" {
				max, err := parseAmt(p.MaxAssets)
				if err != nil {
					return fmt.Errorf("qrc4626 mint: max_assets: %w", err)
				}
				cost, err := v.PreviewMint(shares)
				if err != nil {
					return err
				}
				if cost.Cmp(max) > 0 {
					return fmt.Errorf("qrc4626 mint: would cost %s assets, above max_assets %s", cost, max)
				}
			}
			_, err = v.Mint(ctx.Caller, to, shares)
			return err
		case "withdraw":
			assets, err := parseAmt(p.Assets)
			if err != nil {
				return fmt.Errorf("qrc4626 withdraw: assets: %w", err)
			}
			if p.MaxShares != "" {
				max, err := parseAmt(p.MaxShares)
				if err != nil {
					return fmt.Errorf("qrc4626 withdraw: max_shares: %w", err)
				}
				burn, err := v.PreviewWithdraw(assets)
				if err != nil {
					return err
				}
				if burn.Cmp(max) > 0 {
					return fmt.Errorf("qrc4626 withdraw: would burn %s shares, above max_shares %s", burn, max)
				}
			}
			_, err = v.Withdraw(ctx.Caller, to, owner, assets)
			return err
		case "redeem":
			shares, err := parseAmt(p.Shares)
			if err != nil {
				return fmt.Errorf("qrc4626 redeem: shares: %w", err)
			}
			if p.MinAssets != "" {
				min, err := parseAmt(p.MinAssets)
				if err != nil {
					return fmt.Errorf("qrc4626 redeem: min_assets: %w", err)
				}
				out, err := v.PreviewRedeem(shares)
				if err != nil {
					return err
				}
				if out.Cmp(min) < 0 {
					return fmt.Errorf("qrc4626 redeem: would return %s assets, below min_assets %s", out, min)
				}
			}
			_, err = v.Redeem(ctx.Caller, to, owner, shares)
			return err
		default: // approve
			spender, err := qcrypto.AddressFromString(p.Spender)
			if err != nil {
				return fmt.Errorf("qrc4626 approve: spender: %w", err)
			}
			shares, err := parseAmt(p.Shares)
			if err != nil {
				return fmt.Errorf("qrc4626 approve: shares: %w", err)
			}
			return v.Approve(ctx.Caller, spender, shares)
		}
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchQRC4626Read(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p struct {
		Vault   string `json:"vault"`
		Owner   string `json:"owner,omitempty"`
		Spender string `json:"spender,omitempty"`
		Assets  string `json:"assets,omitempty"`
		Shares  string `json:"shares,omitempty"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	vaddr, err := qcrypto.AddressFromString(p.Vault)
	if err != nil {
		return nil, err
	}
	v := NewQRC4626Vault(stateDB, vaddr)
	amount := func(s, what string) (*big.Int, error) {
		a, err := parseAmt(s)
		if err != nil {
			return nil, fmt.Errorf("qrc4626 %s: %s: %w", action, what, err)
		}
		return a, nil
	}
	switch action {
	case "total_assets":
		return v.TotalAssets()
	case "total_shares":
		return v.TotalShares()
	case "name":
		return v.Name()
	case "symbol":
		return v.Symbol()
	case "asset":
		a, err := v.Asset()
		if err != nil {
			return nil, err
		}
		return a.String(), nil
	case "share_balance":
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, err
		}
		return v.ShareBalance(owner)
	case "allowance":
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, err
		}
		spender, err := qcrypto.AddressFromString(p.Spender)
		if err != nil {
			return nil, err
		}
		return v.Allowance(owner, spender)
	case "exchange_rate":
		num, den, err := v.ExchangeRate()
		if err != nil {
			return nil, err
		}
		return map[string]string{"assets_per_share": num.String(), "denominator": den.String()}, nil
	case "preview_deposit":
		a, err := amount(p.Assets, "assets")
		if err != nil {
			return nil, err
		}
		return v.PreviewDeposit(a)
	case "preview_mint":
		s, err := amount(p.Shares, "shares")
		if err != nil {
			return nil, err
		}
		return v.PreviewMint(s)
	case "preview_withdraw":
		a, err := amount(p.Assets, "assets")
		if err != nil {
			return nil, err
		}
		return v.PreviewWithdraw(a)
	case "preview_redeem":
		s, err := amount(p.Shares, "shares")
		if err != nil {
			return nil, err
		}
		return v.PreviewRedeem(s)
	default:
		return nil, fmt.Errorf("qrc4626: unknown read %q", action)
	}
}
