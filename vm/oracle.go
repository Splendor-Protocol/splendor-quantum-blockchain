// Package vm — On-chain Price Oracle for Splendor Quantum Chain.
//
// The oracle aggregates price data from multiple authorised ML-DSA-65-signed
// data providers (feeders). It uses a median of all valid signed prices to
// resist manipulation from any single feeder.
//
// Design:
//   - Governance registers feeder addresses (ML-DSA-65 public keys)
//   - Feeders submit price updates signed with their ML-DSA-65 key
//   - Oracle stores the last N prices per feeder (sliding window)
//   - GetPrice() returns the median of all valid feeder prices
//   - Prices older than maxAge blocks are rejected as stale
//   - A minimum of minFeeders must agree before the price is considered valid
//
// Price format:
//   All prices are stored as uint256 in "price units" where:
//   1 price unit = 1 smallest SPLD / 1 smallest token unit
//   i.e. how many smallest-SPLD you need to buy 1 smallest-token
//
// All storage: SHA3-256 slots. All signatures: ML-DSA-65.
// No ECDSA. No keccak. No Ethereum address format.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// Oracle constants
const (
	// MaxPriceAgeBlocks: price older than this many blocks is stale (~10 minutes at 2s/block)
	MaxPriceAgeBlocks = 300
	// MinFeeders: minimum number of distinct feeders required for a valid price
	MinFeeders = 1
)

// ── Storage slots ─────────────────────────────────────────────────────────────

var (
	slotOracleOwner       = qcrypto.Hash32([]byte("oracle:owner"))
	slotOracleFeederCount = qcrypto.Hash32([]byte("oracle:feeder_count"))
)

func oracleFeederSlot(feeder qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("oracle:feeder:"), feeder[:]...))
}

func oraclePriceSlot(asset qcrypto.Address, feeder qcrypto.Address) [32]byte {
	key := append([]byte("oracle:price:"), asset[:]...)
	key = append(key, feeder[:]...)
	return qcrypto.Hash32(key)
}

// ── Data structures ────────────────────────────────────────────────────────────

// FeederInfo describes an authorised price feeder.
type FeederInfo struct {
	Address   string `json:"address"`
	PublicKey []byte `json:"public_key"` // ML-DSA-65 public key (1952 bytes)
	Name      string `json:"name"`
	Active    bool   `json:"active"`
}

// PriceFeed is a single signed price submission from one feeder.
type PriceFeed struct {
	Asset       string   `json:"asset"`        // asset address (SQ:...)
	Price       *big.Int `json:"price"`        // price in oracle units
	BlockHeight uint64   `json:"block_height"` // when this price was submitted
	Signature   []byte   `json:"signature"`    // ML-DSA-65 sig over message
}

// PriceUpdate is the canonical message a feeder signs.
// msg = SHA3-256("oracle:price:" + asset + price32 + block_height8)
type PriceUpdate struct {
	Asset       qcrypto.Address
	Price       *big.Int
	BlockHeight uint64
}

// PriceUpdateMessage computes the canonical message to be signed.
func PriceUpdateMessage(asset qcrypto.Address, price *big.Int, blockHeight uint64) [32]byte {
	priceBytes := make([]byte, 32)
	pb := price.Bytes()
	copy(priceBytes[32-len(pb):], pb)

	heightBytes := make([]byte, 8)
	h := blockHeight
	for i := 7; i >= 0; i-- {
		heightBytes[i] = byte(h)
		h >>= 8
	}

	msg := append([]byte("oracle:price:"), asset[:]...)
	msg = append(msg, priceBytes...)
	msg = append(msg, heightBytes...)
	return qcrypto.Hash32(msg)
}

// ── PriceOracle ───────────────────────────────────────────────────────────────

// PriceOracle aggregates ML-DSA-65-signed price feeds and provides median prices.
type PriceOracle struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewPriceOracle returns a handle to an existing price oracle contract.
func NewPriceOracle(stateDB *state.StateDB, contract qcrypto.Address) *PriceOracle {
	return &PriceOracle{stateDB: stateDB, contract: contract}
}

// DeployOracle deploys a price oracle at a deterministic address.
func DeployOracle(stateDB *state.StateDB, owner qcrypto.Address) qcrypto.Address {
	hash := qcrypto.Hash32(append(owner[:], []byte("price_oracle_v1")...))
	var addr qcrypto.Address
	copy(addr[:], hash[:])
	stateDB.SetStorageValue(addr, slotOracleOwner, owner[:])
	return addr
}

// ── Feeder management ─────────────────────────────────────────────────────────

// RegisterFeeder adds an authorised price feeder. Only callable by owner.
func (o *PriceOracle) RegisterFeeder(caller qcrypto.Address, feeder qcrypto.Address, pubKey []byte, name string) error {
	if err := o.requireOwner(caller); err != nil {
		return err
	}
	if len(pubKey) != qcrypto.PublicKeySize {
		return fmt.Errorf("oracle: invalid feeder public key size (%d != %d)", len(pubKey), qcrypto.PublicKeySize)
	}

	info := &FeederInfo{
		Address:   feeder.String(),
		PublicKey: pubKey,
		Name:      name,
		Active:    true,
	}
	return o.setFeeder(feeder, info)
}

// DeactivateFeeder removes a feeder's authority to submit prices.
func (o *PriceOracle) DeactivateFeeder(caller, feeder qcrypto.Address) error {
	if err := o.requireOwner(caller); err != nil {
		return err
	}
	info, err := o.GetFeeder(feeder)
	if err != nil || info == nil {
		return fmt.Errorf("oracle: feeder %s not registered", feeder)
	}
	info.Active = false
	return o.setFeeder(feeder, info)
}

// GetFeeder returns feeder information. Returns nil if not registered.
func (o *PriceOracle) GetFeeder(feeder qcrypto.Address) (*FeederInfo, error) {
	b, err := o.stateDB.GetStorageValue(o.contract, oracleFeederSlot(feeder))
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var info FeederInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ── Price submission ──────────────────────────────────────────────────────────

// SubmitPrice allows a registered feeder to post a new signed price.
// The feeder must sign PriceUpdateMessage(asset, price, blockHeight) with their ML-DSA-65 key.
func (o *PriceOracle) SubmitPrice(
	feeder qcrypto.Address,
	asset qcrypto.Address,
	price *big.Int,
	blockHeight uint64,
	signature []byte,
) error {
	if price.Sign() <= 0 {
		return fmt.Errorf("oracle: price must be positive")
	}

	// Verify feeder is registered and active
	info, err := o.GetFeeder(feeder)
	if err != nil || info == nil || !info.Active {
		return fmt.Errorf("oracle: feeder %s not authorised", feeder)
	}

	// Verify ML-DSA-65 signature
	msg := PriceUpdateMessage(asset, price, blockHeight)
	pub, err := qcrypto.PublicKeyFromBytes(info.PublicKey)
	if err != nil {
		return fmt.Errorf("oracle: invalid feeder public key: %w", err)
	}
	if !qcrypto.Verify(pub, msg[:], qcrypto.CtxOracle, signature) {
		return fmt.Errorf("oracle: invalid ML-DSA signature from feeder %s", feeder)
	}

	// Store the price feed
	feed := &PriceFeed{
		Asset:       asset.String(),
		Price:       new(big.Int).Set(price),
		BlockHeight: blockHeight,
		Signature:   signature,
	}
	return o.setPrice(asset, feeder, feed)
}

// ── Price queries ─────────────────────────────────────────────────────────────

// GetPrice returns the median price across all active feeders for an asset.
// Returns ErrOracleStalePrices if no valid (non-stale) prices exist.
// Returns ErrOracleInsufficientFeeders if fewer than MinFeeders have valid prices.
func (o *PriceOracle) GetPrice(asset qcrypto.Address, currentBlock uint64) (*big.Int, error) {
	return o.GetPriceFromFeeders(asset, currentBlock, nil)
}

// GetPriceFromFeeders returns the median price from the given feeder list.
// Pass nil for feeders to auto-discover from all registered feeders.
func (o *PriceOracle) GetPriceFromFeeders(asset qcrypto.Address, currentBlock uint64, feeders []qcrypto.Address) (*big.Int, error) {
	if feeders == nil {
		// If no feeder list provided, caller must pass known feeders
		return nil, fmt.Errorf("oracle: feeder list required")
	}

	var validPrices []*big.Int

	for _, feeder := range feeders {
		info, err := o.GetFeeder(feeder)
		if err != nil || info == nil || !info.Active {
			continue
		}

		feed, err := o.getPrice(asset, feeder)
		if err != nil || feed == nil {
			continue
		}

		// Check freshness
		if currentBlock > feed.BlockHeight+MaxPriceAgeBlocks {
			continue // stale
		}

		validPrices = append(validPrices, new(big.Int).Set(feed.Price))
	}

	if len(validPrices) < MinFeeders {
		return nil, fmt.Errorf("oracle: insufficient valid prices (%d < %d required)", len(validPrices), MinFeeders)
	}

	return median(validPrices), nil
}

// GetRawPrice returns the raw price feed from a specific feeder.
func (o *PriceOracle) GetRawPrice(asset, feeder qcrypto.Address) (*PriceFeed, error) {
	return o.getPrice(asset, feeder)
}

// GetAllPrices returns all price feeds for an asset from all given feeders.
func (o *PriceOracle) GetAllPrices(asset qcrypto.Address, feeders []qcrypto.Address) ([]*PriceFeed, error) {
	var feeds []*PriceFeed
	for _, feeder := range feeders {
		feed, err := o.getPrice(asset, feeder)
		if err != nil || feed == nil {
			continue
		}
		feeds = append(feeds, feed)
	}
	return feeds, nil
}

// ── TWAP (Time-Weighted Average Price) ────────────────────────────────────────

// GetTWAP computes the time-weighted average price from multiple feeders.
// Weights each price by its block proximity to currentBlock (more recent = higher weight).
func (o *PriceOracle) GetTWAP(asset qcrypto.Address, currentBlock uint64, feeders []qcrypto.Address) (*big.Int, error) {
	type weightedPrice struct {
		price  *big.Int
		weight *big.Int
	}
	var wps []weightedPrice
	totalWeight := big.NewInt(0)

	for _, feeder := range feeders {
		info, err := o.GetFeeder(feeder)
		if err != nil || info == nil || !info.Active {
			continue
		}
		feed, err := o.getPrice(asset, feeder)
		if err != nil || feed == nil {
			continue
		}
		if currentBlock > feed.BlockHeight+MaxPriceAgeBlocks {
			continue
		}

		// Weight = (MaxPriceAgeBlocks - age) — newer = higher weight
		age := currentBlock - feed.BlockHeight
		weight := big.NewInt(int64(MaxPriceAgeBlocks - age))
		wps = append(wps, weightedPrice{price: feed.Price, weight: weight})
		totalWeight.Add(totalWeight, weight)
	}

	if len(wps) == 0 || totalWeight.Sign() == 0 {
		return nil, fmt.Errorf("oracle: no valid prices for TWAP")
	}

	// TWAP = sum(price * weight) / totalWeight
	twap := big.NewInt(0)
	for _, wp := range wps {
		contribution := new(big.Int).Mul(wp.price, wp.weight)
		twap.Add(twap, contribution)
	}
	twap.Div(twap, totalWeight)
	return twap, nil
}

// ── Access control ────────────────────────────────────────────────────────────

func (o *PriceOracle) requireOwner(caller qcrypto.Address) error {
	b, err := o.stateDB.GetStorageValue(o.contract, slotOracleOwner)
	if err != nil || len(b) < qcrypto.AddressSize {
		return fmt.Errorf("oracle: owner not set")
	}
	var owner qcrypto.Address
	copy(owner[:], b)
	if caller != owner {
		return fmt.Errorf("oracle: caller %s is not owner", caller)
	}
	return nil
}

// ── Storage helpers ────────────────────────────────────────────────────────────

func (o *PriceOracle) setFeeder(feeder qcrypto.Address, info *FeederInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return o.stateDB.SetStorageValue(o.contract, oracleFeederSlot(feeder), data)
}

func (o *PriceOracle) setPrice(asset, feeder qcrypto.Address, feed *PriceFeed) error {
	data, err := json.Marshal(feed)
	if err != nil {
		return err
	}
	return o.stateDB.SetStorageValue(o.contract, oraclePriceSlot(asset, feeder), data)
}

func (o *PriceOracle) getPrice(asset, feeder qcrypto.Address) (*PriceFeed, error) {
	b, err := o.stateDB.GetStorageValue(o.contract, oraclePriceSlot(asset, feeder))
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var feed PriceFeed
	if err := json.Unmarshal(b, &feed); err != nil {
		return nil, err
	}
	if feed.Price == nil {
		feed.Price = big.NewInt(0)
	}
	return &feed, nil
}

// ── Math helpers ──────────────────────────────────────────────────────────────

// median returns the median of a slice of *big.Int values.
// Sorts in place.
func median(prices []*big.Int) *big.Int {
	if len(prices) == 0 {
		return big.NewInt(0)
	}
	sort.Slice(prices, func(i, j int) bool {
		return prices[i].Cmp(prices[j]) < 0
	})
	mid := len(prices) / 2
	if len(prices)%2 == 1 {
		return new(big.Int).Set(prices[mid])
	}
	// Even count: average of two middle values
	sum := new(big.Int).Add(prices[mid-1], prices[mid])
	return sum.Div(sum, big.NewInt(2))
}
