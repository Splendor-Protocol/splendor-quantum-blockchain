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
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchLendingRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p struct {
		Lending string `json:"lending"`
		Asset   string `json:"asset"`
		Owner   string `json:"owner"`
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
		var owners []MSigOwner
		for _, o := range p.Owners {
			pk, _ := hex.DecodeString(o.Pubkey)
			owners = append(owners, MSigOwner{Address: o.Address, PublicKey: pk, Name: o.Name})
		}
		addr, err := DeployMultiSig(stateDB, ctx.Caller, owners, p.Threshold, ctx.Nonce)
		if err != nil {
			return err
		}
		receipt.ContractAddress = &addr
		return nil
	case "submit":
		var p struct {
			Wallet string `json:"wallet"`
			To     string `json:"to"`
			Value  string `json:"value"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		waddr, err := qcrypto.AddressFromString(p.Wallet)
		if err != nil {
			return err
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return err
		}
		val, err := parseAmt(p.Value)
		if err != nil {
			return err
		}
		_, err = NewMultiSigWallet(stateDB, waddr).Submit(ctx.Caller, MSigTransfer, to, val, nil, "")
		return err
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchMultisigRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p struct {
		Wallet string `json:"wallet"`
		Nonce  uint64 `json:"nonce"`
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
		var p struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Payload     string `json:"payload"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		_, err := g.Submit(ctx.Caller, 0, p.Title, p.Description, p.Payload, ctx.Height)
		return err
	case "vote":
		var p struct {
			ID     uint64 `json:"id"`
			Option uint8  `json:"option"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		return g.Vote(ctx.Caller, p.ID, VoteOption(p.Option), ctx.Height)
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchGovernanceRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	g := NewGovernanceContract(stateDB, DeployGovernance(nil))
	switch action {
	case "proposal":
		var p struct {
			ID uint64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return g.GetProposal(p.ID)
	case "count":
		n, err := g.ProposalCount()
		return map[string]uint64{"count": n}, err
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
	case "deposit":
		var p struct {
			Vault  string `json:"vault"`
			Assets string `json:"assets"`
			To     string `json:"to"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		vaddr, err := qcrypto.AddressFromString(p.Vault)
		if err != nil {
			return err
		}
		to := ctx.Caller
		if p.To != "" {
			to, err = qcrypto.AddressFromString(p.To)
			if err != nil {
				return err
			}
		}
		amt, err := parseAmt(p.Assets)
		if err != nil {
			return err
		}
		_, err = NewQRC4626Vault(stateDB, vaddr).Deposit(ctx.Caller, to, amt)
		return err
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

func dispatchQRC4626Read(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p struct {
		Vault string `json:"vault"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	vaddr, err := qcrypto.AddressFromString(p.Vault)
	if err != nil {
		return nil, err
	}
	v := NewQRC4626Vault(stateDB, vaddr)
	switch action {
	case "total_assets":
		return v.TotalAssets()
	case "name":
		return v.Name()
	default:
		return nil, fmt.Errorf("qrc4626: unknown read %q", action)
	}
}
