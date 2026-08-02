package state

import (
	"encoding/json"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

const (
	prefixTxIndex    = "txi:"    // txHash → block height + index
	prefixReceipt    = "rcpt:"   // txHash → Receipt
	prefixBlockTxs   = "btxs:"  // blockHeight → []txHash
	prefixLogByAddr  = "log:"   // address+logIdx → Log
)

// TxLocation records where a transaction can be found on chain.
type TxLocation struct {
	BlockHeight uint64 `json:"block_height"`
	BlockHash   [32]byte `json:"block_hash"`
	TxIndex     uint32   `json:"tx_index"`
}

// TxIndexStore is a LevelDB-backed index of all transactions and receipts.
// It allows O(1) lookup of any transaction by its hash.
type TxIndexStore struct {
	db *leveldb.DB
}

// NewTxIndexStore opens (or creates) the transaction index at the given path.
func NewTxIndexStore(path string) (*TxIndexStore, error) {
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open tx index at %s: %w", path, err)
	}
	return &TxIndexStore{db: db}, nil
}

// Close closes the underlying database.
func (s *TxIndexStore) Close() error {
	return s.db.Close()
}

// IndexBlock indexes all transactions in a block.
// It stores:
//   - TxLocation for each tx hash
//   - The list of tx hashes for the block height
func (s *TxIndexStore) IndexBlock(b *types.Block) error {
	batch := new(leveldb.Batch)

	hashes := make([][32]byte, 0, len(b.Transactions))

	for i, tx := range b.Transactions {
		hash, err := tx.FullHash()
		if err != nil {
			return fmt.Errorf("failed to hash tx %d in block %d: %w", i, b.Header.Height, err)
		}

		loc := TxLocation{
			BlockHeight: b.Header.Height,
			BlockHash:   b.Hash,
			TxIndex:     uint32(i),
		}
		locData, err := json.Marshal(loc)
		if err != nil {
			return fmt.Errorf("failed to marshal tx location: %w", err)
		}

		batch.Put(txIndexKey(hash), locData)
		hashes = append(hashes, hash)
	}

	// Store list of tx hashes for this block
	hashData, err := json.Marshal(hashes)
	if err != nil {
		return fmt.Errorf("failed to marshal block tx list: %w", err)
	}
	batch.Put(blockTxsKey(b.Header.Height), hashData)

	return s.db.Write(batch, nil)
}

// GetTxLocation returns the block location of a transaction by hash.
func (s *TxIndexStore) GetTxLocation(txHash [32]byte) (*TxLocation, error) {
	data, err := s.db.Get(txIndexKey(txHash), nil)
	if err == leveldb.ErrNotFound {
		return nil, types.ErrBlockNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tx index read error: %w", err)
	}
	var loc TxLocation
	if err := json.Unmarshal(data, &loc); err != nil {
		return nil, fmt.Errorf("failed to decode tx location: %w", err)
	}
	return &loc, nil
}

// GetBlockTxHashes returns all tx hashes in a block, in order.
func (s *TxIndexStore) GetBlockTxHashes(height uint64) ([][32]byte, error) {
	data, err := s.db.Get(blockTxsKey(height), nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hashes [][32]byte
	if err := json.Unmarshal(data, &hashes); err != nil {
		return nil, err
	}
	return hashes, nil
}

// StoreReceipt stores a transaction receipt.
func (s *TxIndexStore) StoreReceipt(r *types.Receipt) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("failed to marshal receipt: %w", err)
	}
	return s.db.Put(receiptKey(r.TxHash), data, nil)
}

// GetReceipt retrieves a receipt by transaction hash.
func (s *TxIndexStore) GetReceipt(txHash [32]byte) (*types.Receipt, error) {
	data, err := s.db.Get(receiptKey(txHash), nil)
	if err == leveldb.ErrNotFound {
		return nil, fmt.Errorf("receipt not found for tx %x", txHash[:8])
	}
	if err != nil {
		return nil, fmt.Errorf("receipt read error: %w", err)
	}
	var r types.Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("failed to decode receipt: %w", err)
	}
	return &r, nil
}

// StoreBlockReceipts stores all receipts for a block atomically.
func (s *TxIndexStore) StoreBlockReceipts(receipts []*types.Receipt) error {
	batch := new(leveldb.Batch)
	for _, r := range receipts {
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		batch.Put(receiptKey(r.TxHash), data)

		// Index logs by address for filtering
		for _, log := range r.Logs {
			logKey := logByAddrKey(log.Address, r.BlockHeight, log.TxIndex, log.LogIndex)
			logData, err := json.Marshal(log)
			if err != nil {
				return err
			}
			batch.Put(logKey, logData)
		}
	}
	return s.db.Write(batch, nil)
}

// ── Key builders ──────────────────────────────────────────────────────────────

func txIndexKey(hash [32]byte) []byte {
	key := make([]byte, len(prefixTxIndex)+32)
	copy(key, prefixTxIndex)
	copy(key[len(prefixTxIndex):], hash[:])
	return key
}

func receiptKey(hash [32]byte) []byte {
	key := make([]byte, len(prefixReceipt)+32)
	copy(key, prefixReceipt)
	copy(key[len(prefixReceipt):], hash[:])
	return key
}

func blockTxsKey(height uint64) []byte {
	return []byte(fmt.Sprintf("%s%020d", prefixBlockTxs, height))
}

func logByAddrKey(addr interface{ String() string }, blockHeight uint64, txIndex, logIndex uint32) []byte {
	return []byte(fmt.Sprintf("%s%s:%020d:%010d:%010d",
		prefixLogByAddr, addr.String(), blockHeight, txIndex, logIndex))
}
