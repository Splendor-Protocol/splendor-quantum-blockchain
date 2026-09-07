package types

import (
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// TxStatus indicates whether a transaction succeeded or failed during execution.
type TxStatus uint8

const (
	TxStatusSuccess TxStatus = 1
	TxStatusFailed  TxStatus = 0
)

// Log represents a single event emitted by a smart contract during execution.
// Analogous to Ethereum's Log / EVM event, but using SHA-3-256 for topic hashing.
type Log struct {
	// Address of the contract that emitted this log.
	Address qcrypto.Address `json:"address"`

	// Topics are SHA3-256 hashes of the event signature and indexed parameters.
	// Topic[0] is always SHA3-256(EventName(paramType,...)).
	Topics []([32]byte) `json:"topics"`

	// Data contains the ABI-encoded non-indexed event parameters.
	Data []byte `json:"data,omitempty"`

	// Block context
	BlockHeight uint64   `json:"block_height"`
	TxHash      [32]byte `json:"tx_hash"`
	TxIndex     uint32   `json:"tx_index"`
	LogIndex    uint32   `json:"log_index"`
}

// Receipt is the result of executing a single transaction.
// It is stored in the chain database alongside the block.
type Receipt struct {
	// TxHash is the full SHA3-256 hash of the signed transaction.
	TxHash [32]byte `json:"tx_hash"`

	// TxIndex is the position of this transaction in its block.
	TxIndex uint32 `json:"tx_index"`

	// BlockHeight is the height of the block containing this transaction.
	BlockHeight uint64 `json:"block_height"`

	// BlockHash is the SHA3-256 hash of the containing block header.
	BlockHash [32]byte `json:"block_hash"`

	// Status: 1 = success, 0 = reverted/failed.
	Status TxStatus `json:"status"`

	// GasUsed is the number of VM opcodes executed (for contract txs).
	GasUsed uint64 `json:"gas_used"`

	// Error message if Status == TxStatusFailed.
	Error string `json:"error,omitempty"`

	// From / To for easy lookup.
	From qcrypto.Address `json:"from"`
	To   qcrypto.Address `json:"to"`

	// Amount transferred.
	Amount *big.Int `json:"amount"`

	// Fee paid.
	Fee *big.Int `json:"fee"`

	// ContractAddress is set when the transaction created something with an
	// address: a TxDeploy (the new bytecode contract) or a module-router
	// TxCall whose action deploys — qrc20.deploy, qrc1155.deploy,
	// dex.deploy_pool and exchange.deploy (vm/module_router.go). Nil otherwise.
	ContractAddress *qcrypto.Address `json:"contract_address,omitempty"`

	// Logs are the events emitted by this transaction's execution.
	Logs []*Log `json:"logs"`

	// Type of transaction.
	Type TxType `json:"type"`
}

// NewReceipt creates a basic receipt from a transaction, to be filled in by the executor.
func NewReceipt(tx *Transaction, txIndex uint32, blockHeight uint64, blockHash [32]byte) (*Receipt, error) {
	hash, err := tx.FullHash()
	if err != nil {
		return nil, err
	}
	return &Receipt{
		TxHash:      hash,
		TxIndex:     txIndex,
		BlockHeight: blockHeight,
		BlockHash:   blockHash,
		Status:      TxStatusSuccess,
		From:        tx.From,
		To:          tx.To,
		Amount:      new(big.Int).Set(tx.Amount),
		Fee:         new(big.Int).Set(tx.Fee),
		Type:        tx.Type,
		Logs:        []*Log{},
	}, nil
}

// SetFailed marks a receipt as failed with the given error message.
func (r *Receipt) SetFailed(errMsg string) {
	r.Status = TxStatusFailed
	r.Error = errMsg
}

// AddLog appends an event log to the receipt.
func (r *Receipt) AddLog(log *Log) {
	log.TxHash = r.TxHash
	log.TxIndex = r.TxIndex
	log.BlockHeight = r.BlockHeight
	log.LogIndex = uint32(len(r.Logs))
	r.Logs = append(r.Logs, log)
}
