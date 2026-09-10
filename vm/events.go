// Package vm — Standard event topic helpers for SQC-VM contracts.
//
// Events in SQC use SHA3-256 topic hashing (not keccak256 like Ethereum).
// Topic[0] is always SHA3-256(EventSignature) — the event identifier.
// Subsequent topics are SHA3-256(indexed_parameter_value).
//
// These helpers make it easy to emit correctly-formatted events from
// native Go contract implementations (QRC-20, QRC-721, Staking, Governance).
//
// Standard event signatures used across all SQC token contracts:
//
//	Transfer(sqaddress,sqaddress,uint256)
//	Approval(sqaddress,sqaddress,uint256)
//	ApprovalForAll(sqaddress,sqaddress,bool)
//	Deposit(sqaddress,sqaddress,uint256,uint256)   [QRC-4626]
//	Withdraw(sqaddress,sqaddress,uint256,uint256)  [QRC-4626]
//	ValidatorRegistered(sqaddress,uint256)
//	ValidatorSlashed(sqaddress,uint256,uint256)
//	ProposalSubmitted(uint256,sqaddress,string)
//	VoteCast(uint256,sqaddress,uint8,uint256)
//	ProposalExecuted(uint256)
package vm

import (
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ── Topic computation ─────────────────────────────────────────────────────────

// EventTopic computes SHA3-256(event_signature_string) for use as Topic[0].
// Example: EventTopic("Transfer(sqaddress,sqaddress,uint256)")
func EventTopic(signature string) [32]byte {
	return qcrypto.Hash32([]byte(signature))
}

// AddrTopic converts an address to a 32-byte topic (left-padded with zeros).
func AddrTopic(addr qcrypto.Address) [32]byte {
	var topic [32]byte
	copy(topic[32-qcrypto.AddressSize:], addr[:])
	return topic
}

// Uint256Topic converts a *big.Int to a 32-byte big-endian topic.
func Uint256Topic(v *big.Int) [32]byte {
	return uint256Word(v)
}

// uint256Word is the 32-byte big-endian encoding of v (zero word for nil or
// non-positive). A value that does not fit — reachable through the legacy
// qrc20 actions, which decode with parseBig — is truncated to its low 32
// bytes rather than allowed to slice past the word and panic the executor:
// an event helper must never be able to take a block down.
func uint256Word(v *big.Int) [32]byte {
	var word [32]byte
	if v == nil || v.Sign() <= 0 {
		return word
	}
	b := v.Bytes()
	if len(b) > 32 {
		b = b[len(b)-32:]
	}
	copy(word[32-len(b):], b)
	return word
}

// BoolTopic converts a bool to a 32-byte topic (0x00...01 or 0x00...00).
func BoolTopic(v bool) [32]byte {
	var topic [32]byte
	if v {
		topic[31] = 1
	}
	return topic
}

// BytesTopic converts a byte slice to a 32-byte topic (right-padded).
func BytesTopic(data []byte) [32]byte {
	var topic [32]byte
	n := len(data)
	if n > 32 {
		n = 32
	}
	copy(topic[:n], data)
	return topic
}

// ── Pre-built standard events ─────────────────────────────────────────────────

// Standard topic[0] hashes (computed once at package init time).
var (
	TopicTransfer            = EventTopic("Transfer(sqaddress,sqaddress,uint256)")
	TopicApproval            = EventTopic("Approval(sqaddress,sqaddress,uint256)")
	TopicApprovalForAll      = EventTopic("ApprovalForAll(sqaddress,sqaddress,bool)")
	TopicMint                = EventTopic("Mint(sqaddress,uint256,uint256)")
	TopicBurn                = EventTopic("Burn(sqaddress,uint256,uint256)")
	TopicDeposit             = EventTopic("Deposit(sqaddress,sqaddress,uint256,uint256)")
	TopicWithdraw            = EventTopic("Withdraw(sqaddress,sqaddress,uint256,uint256)")
	TopicYield               = EventTopic("Yield(uint256,uint256)")
	TopicValidatorRegistered = EventTopic("ValidatorRegistered(sqaddress,uint256)")
	TopicValidatorSlashed    = EventTopic("ValidatorSlashed(sqaddress,uint256,uint256)")
	TopicValidatorJailed     = EventTopic("ValidatorJailed(sqaddress)")
	TopicValidatorUnjailed   = EventTopic("ValidatorUnjailed(sqaddress)")
	TopicRewardsClaimed      = EventTopic("RewardsClaimed(sqaddress,uint256)")
	TopicProposalSubmitted   = EventTopic("ProposalSubmitted(uint256,sqaddress)")
	TopicProposalCanceled    = EventTopic("ProposalCanceled(uint256)")
	TopicProposalPassed      = EventTopic("ProposalPassed(uint256)")
	TopicProposalRejected    = EventTopic("ProposalRejected(uint256)")
	TopicProposalExecuted    = EventTopic("ProposalExecuted(uint256)")
	TopicVoteCast            = EventTopic("VoteCast(uint256,sqaddress,uint8,uint256)")
	TopicSwap                = EventTopic("Swap(sqaddress,uint256,uint256,uint256,uint256,sqaddress)")
)

// QRC-20 v2 (qrc20_v2) lifecycle events: pause state and ownership.
var (
	TopicPaused               = EventTopic("Paused(sqaddress)")
	TopicUnpaused             = EventTopic("Unpaused(sqaddress)")
	TopicOwnershipTransferred = EventTopic("OwnershipTransferred(sqaddress,sqaddress)")
)

// QRC-1155 (qrc1155_v1) transfer events. operator, from and to are indexed;
// a mint is a transfer from the zero address and a burn a transfer to it.
// TransferBatch data is n ‖ ids[n] ‖ values[n], every element a 32-byte word,
// so an indexer can decode it without a length prefix per element.
var (
	TopicTransferSingle = EventTopic("TransferSingle(sqaddress,sqaddress,sqaddress,uint256,uint256)")
	TopicTransferBatch  = EventTopic("TransferBatch(sqaddress,sqaddress,sqaddress,uint256[],uint256[])")
)

// Exchange (exchange_v1) settlement events. Every non-indexed value is a
// 32-byte word (uint64/uint8/bool zero-extended, bytes32 verbatim), never a
// string. Indexed fields: ConditionCreated → condition; OrderFilled → hash,
// maker, taker; MintMatched/MergeMatched → taker hash, maker hash;
// OrderCancelled → hash, maker; NonceIncremented, PositionSplit,
// PositionsMerged, Redeemed, RedeemFeeCharged, OperatorSet,
// FeeRecipientUpdated → the address; Resolved → condition.
var (
	TopicConditionCreated    = EventTopic("ConditionCreated(uint64,sqaddress,sqaddress,uint64,uint8,bytes32)")
	TopicOrderFilled         = EventTopic("OrderFilled(bytes32,sqaddress,sqaddress,uint64,uint8,uint8,uint256,uint256)")
	TopicMintMatched         = EventTopic("MintMatched(bytes32,bytes32,uint64,uint256)")
	TopicMergeMatched        = EventTopic("MergeMatched(bytes32,bytes32,uint64,uint256)")
	TopicOrderCancelled      = EventTopic("OrderCancelled(bytes32,sqaddress)")
	TopicNonceIncremented    = EventTopic("NonceIncremented(sqaddress,uint64)")
	TopicPositionSplit       = EventTopic("PositionSplit(sqaddress,uint64,uint256)")
	TopicPositionsMerged     = EventTopic("PositionsMerged(sqaddress,uint64,uint256)")
	TopicResolved            = EventTopic("Resolved(uint64,uint8)")
	TopicRedeemed            = EventTopic("Redeemed(sqaddress,uint64,uint256,uint256)")
	TopicRedeemFeeCharged    = EventTopic("RedeemFeeCharged(sqaddress,uint256)")
	TopicOperatorSet         = EventTopic("OperatorSet(sqaddress,bool)")
	TopicRedeemFeeUpdated    = EventTopic("RedeemFeeUpdated(uint256)")
	TopicFeeRecipientUpdated = EventTopic("FeeRecipientUpdated(sqaddress)")
	TopicBridgeClaimed       = EventTopic("BridgeClaimed(uint64,uint8,uint256,sqaddress,bytes32)")
	TopicThresholdUpdated    = EventTopic("ThresholdUpdated(uint64)")
)

// ── Event builder ─────────────────────────────────────────────────────────────

// EventBuilder builds a *types.Log with the standard topic-first format.
type EventBuilder struct {
	contract qcrypto.Address
	topics   [][32]byte
	data     []byte
}

// NewEvent starts building a new event for the given contract address.
// sig is the event signature string (e.g. "Transfer(sqaddress,sqaddress,uint256)").
func NewEvent(contract qcrypto.Address, sig string) *EventBuilder {
	return &EventBuilder{
		contract: contract,
		topics:   [][32]byte{EventTopic(sig)},
	}
}

// IndexAddr adds an indexed sqaddress parameter (becomes a topic).
func (e *EventBuilder) IndexAddr(addr qcrypto.Address) *EventBuilder {
	e.topics = append(e.topics, AddrTopic(addr))
	return e
}

// IndexUint256 adds an indexed uint256 parameter (becomes a topic).
func (e *EventBuilder) IndexUint256(v *big.Int) *EventBuilder {
	e.topics = append(e.topics, Uint256Topic(v))
	return e
}

// IndexBool adds an indexed bool parameter (becomes a topic).
func (e *EventBuilder) IndexBool(v bool) *EventBuilder {
	e.topics = append(e.topics, BoolTopic(v))
	return e
}

// IndexHash adds an indexed bytes32 parameter (an order hash, a question
// hash) as a topic, verbatim.
func (e *EventBuilder) IndexHash(h [32]byte) *EventBuilder {
	e.topics = append(e.topics, h)
	return e
}

// DataUint256 appends a non-indexed uint256 to the event data (ABI-encoded as 32 bytes).
func (e *EventBuilder) DataUint256(v *big.Int) *EventBuilder {
	word := uint256Word(v)
	e.data = append(e.data, word[:]...)
	return e
}

// DataAddr appends a non-indexed address to the event data.
func (e *EventBuilder) DataAddr(addr qcrypto.Address) *EventBuilder {
	var word [32]byte
	copy(word[32-qcrypto.AddressSize:], addr[:])
	e.data = append(e.data, word[:]...)
	return e
}

// DataHash appends a non-indexed bytes32 to the event data, verbatim.
func (e *EventBuilder) DataHash(h [32]byte) *EventBuilder {
	e.data = append(e.data, h[:]...)
	return e
}

// DataString appends a non-indexed string to the event data (UTF-8 bytes, not ABI-encoded).
func (e *EventBuilder) DataString(s string) *EventBuilder {
	e.data = append(e.data, []byte(s)...)
	return e
}

// Build returns the completed *types.Log.
// BlockHeight, TxHash, TxIndex, LogIndex are filled in by the receipt system.
func (e *EventBuilder) Build() *types.Log {
	return &types.Log{
		Address: e.contract,
		Topics:  e.topics,
		Data:    e.data,
	}
}

// ── Convenience constructors ──────────────────────────────────────────────────

// TransferEvent builds a standard Transfer event log.
func TransferEvent(contract, from, to qcrypto.Address, amount *big.Int) *types.Log {
	return NewEvent(contract, "Transfer(sqaddress,sqaddress,uint256)").
		IndexAddr(from).
		IndexAddr(to).
		DataUint256(amount).
		Build()
}

// ApprovalEvent builds a standard Approval event log.
func ApprovalEvent(contract, owner, spender qcrypto.Address, amount *big.Int) *types.Log {
	return NewEvent(contract, "Approval(sqaddress,sqaddress,uint256)").
		IndexAddr(owner).
		IndexAddr(spender).
		DataUint256(amount).
		Build()
}

// ApprovalForAllEvent builds an ApprovalForAll event log.
func ApprovalForAllEvent(contract, owner, operator qcrypto.Address, approved bool) *types.Log {
	return NewEvent(contract, "ApprovalForAll(sqaddress,sqaddress,bool)").
		IndexAddr(owner).
		IndexAddr(operator).
		IndexBool(approved).
		Build()
}

// PausedEvent builds a Paused event; account is the owner that paused.
func PausedEvent(contract, account qcrypto.Address) *types.Log {
	return NewEvent(contract, "Paused(sqaddress)").
		IndexAddr(account).
		Build()
}

// UnpausedEvent builds an Unpaused event; account is the owner that unpaused.
func UnpausedEvent(contract, account qcrypto.Address) *types.Log {
	return NewEvent(contract, "Unpaused(sqaddress)").
		IndexAddr(account).
		Build()
}

// OwnershipTransferredEvent builds an OwnershipTransferred event
// (previousOwner and newOwner both indexed).
func OwnershipTransferredEvent(contract, previousOwner, newOwner qcrypto.Address) *types.Log {
	return NewEvent(contract, "OwnershipTransferred(sqaddress,sqaddress)").
		IndexAddr(previousOwner).
		IndexAddr(newOwner).
		Build()
}

// OperatorSetEvent builds an OperatorSet event: operator indexed; active as a
// 0/1 data word. Emitted by a module whose owner grants or revokes an
// operator (the bridge).
func OperatorSetEvent(contract, operator qcrypto.Address, active bool) *types.Log {
	v := big.NewInt(0)
	if active {
		v = big.NewInt(1)
	}
	return NewEvent(contract, "OperatorSet(sqaddress,bool)").
		IndexAddr(operator).
		DataUint256(v).
		Build()
}

// TransferSingleEvent builds a QRC-1155 TransferSingle event: operator, from
// and to indexed; id and value as data words.
func TransferSingleEvent(contract, operator, from, to qcrypto.Address, id, value *big.Int) *types.Log {
	return NewEvent(contract, "TransferSingle(sqaddress,sqaddress,sqaddress,uint256,uint256)").
		IndexAddr(operator).
		IndexAddr(from).
		IndexAddr(to).
		DataUint256(id).
		DataUint256(value).
		Build()
}

// TransferBatchEvent builds a QRC-1155 TransferBatch event: operator, from
// and to indexed; data = n ‖ ids ‖ values as 32-byte words. len(ids) must
// equal len(values) — the callers check it before any balance moves.
func TransferBatchEvent(contract, operator, from, to qcrypto.Address, ids, values []*big.Int) *types.Log {
	e := NewEvent(contract, "TransferBatch(sqaddress,sqaddress,sqaddress,uint256[],uint256[])").
		IndexAddr(operator).
		IndexAddr(from).
		IndexAddr(to).
		DataUint256(big.NewInt(int64(len(ids))))
	for _, id := range ids {
		e.DataUint256(id)
	}
	for _, v := range values {
		e.DataUint256(v)
	}
	return e.Build()
}

// MintEvent builds a standard Mint event (tokenId optional — 0 for QRC-20).
func MintEvent(contract, to qcrypto.Address, tokenId, amount *big.Int) *types.Log {
	return NewEvent(contract, "Mint(sqaddress,uint256,uint256)").
		IndexAddr(to).
		DataUint256(tokenId).
		DataUint256(amount).
		Build()
}

// BurnEvent builds a standard Burn event.
func BurnEvent(contract, from qcrypto.Address, tokenId, amount *big.Int) *types.Log {
	return NewEvent(contract, "Burn(sqaddress,uint256,uint256)").
		IndexAddr(from).
		DataUint256(tokenId).
		DataUint256(amount).
		Build()
}

// ValidatorRegisteredEvent builds a staking ValidatorRegistered event.
func ValidatorRegisteredEvent(contract, validator qcrypto.Address, stake *big.Int) *types.Log {
	return NewEvent(contract, "ValidatorRegistered(sqaddress,uint256)").
		IndexAddr(validator).
		DataUint256(stake).
		Build()
}

// ValidatorSlashedEvent builds a staking ValidatorSlashed event.
func ValidatorSlashedEvent(contract, validator qcrypto.Address, slashAmount, remaining *big.Int) *types.Log {
	return NewEvent(contract, "ValidatorSlashed(sqaddress,uint256,uint256)").
		IndexAddr(validator).
		DataUint256(slashAmount).
		DataUint256(remaining).
		Build()
}
