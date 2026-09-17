package state

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/persist"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ErrTxIndexIncomplete reports a height whose receipts exist neither in the
// index nor in the chain store, so nothing on this node can complete it and
// the durable watermark cannot pass it. Backfill stops there: heights above
// it were indexed live and stay readable, but /readyz keeps reporting the
// index behind the applied height.
var ErrTxIndexIncomplete = errors.New("tx index: height cannot be completed from this node's stores")

// ErrReceiptNotFound is the GetReceipt error for a hash the index does not
// hold, as opposed to a read or decode failure.
var ErrReceiptNotFound = errors.New("receipt not found")

// backfillFlushEvery is how many heights Backfill walks between durable
// watermark writes. Every write is fsynced. A node upgraded from a build that
// wrote no watermark walks its whole chain on first boot, before it listens
// or seals, and one fsync per historical block turned that walk into minutes
// on a tier. Batching makes it a read-only scan with a handful of writes; a
// crash mid-walk costs at most this many heights of re-reading.
const backfillFlushEvery = 1024

const (
	prefixTxIndex   = "txi:"  // txHash → block height + index
	prefixReceipt   = "rcpt:" // txHash → Receipt
	prefixBlockTxs  = "btxs:" // blockHeight → []txHash
	prefixLogByAddr = "log:"  // address+logIdx → Log

	keyIndexHeight  = "meta:txindex_height"
	keyCoverageFrom = "meta:txindex_coverage_from"
)

// TxLocation records where a transaction can be found on chain.
type TxLocation struct {
	BlockHeight uint64   `json:"block_height"`
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
	if err := appendIndexBlock(batch, b); err != nil {
		return err
	}
	return persist.Write(s.db, batch)
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
		return nil, fmt.Errorf("%w for tx %x", ErrReceiptNotFound, txHash[:8])
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
	if err := appendReceipts(batch, receipts); err != nil {
		return err
	}
	return persist.Write(s.db, batch)
}

func appendReceipts(batch *leveldb.Batch, receipts []*types.Receipt) error {
	for _, r := range receipts {
		if r == nil {
			continue
		}
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		batch.Put(receiptKey(r.TxHash), data)
		for _, lg := range r.Logs {
			logKey := logByAddrKey(lg.Address, r.BlockHeight, lg.TxIndex, lg.LogIndex)
			logData, err := json.Marshal(lg)
			if err != nil {
				return err
			}
			batch.Put(logKey, logData)
		}
	}
	return nil
}

func appendIndexBlock(batch *leveldb.Batch, b *types.Block) error {
	if b == nil {
		return nil
	}
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
	hashData, err := json.Marshal(hashes)
	if err != nil {
		return fmt.Errorf("failed to marshal block tx list: %w", err)
	}
	batch.Put(blockTxsKey(b.Header.Height), hashData)
	return nil
}

// IndexedHeight is the highest block whose txs/receipts were durably indexed.
func (s *TxIndexStore) IndexedHeight() (uint64, bool) {
	h, ok, err := s.readMetaUint64(keyIndexHeight)
	if err != nil || !ok {
		return 0, false
	}
	return h, true
}

// CoverageFrom is the first height this index claims to cover. After a
// snapshot import it equals the snapshot height: earlier receipts do not exist
// on this node.
func (s *TxIndexStore) CoverageFrom() (uint64, bool) {
	h, ok, err := s.readMetaUint64(keyCoverageFrom)
	if err != nil || !ok {
		return 0, false
	}
	return h, true
}

// MarkSnapshotCoverage records that historical receipts below height are
// unavailable (state-sync join). IndexedHeight is set to that point only
// when it is missing or behind — a remake must not clobber later progress.
func (s *TxIndexStore) MarkSnapshotCoverage(height uint64) error {
	batch := new(leveldb.Batch)
	putIndexMeta(batch, keyCoverageFrom, height)
	cur, ok := s.IndexedHeight()
	if !ok || cur < height {
		putIndexMeta(batch, keyIndexHeight, height)
	}
	return persist.Write(s.db, batch)
}

// ClearCoverageFrom drops the coverage mark so reconcile can remake it.
func (s *TxIndexStore) ClearCoverageFrom() error {
	err := s.db.Delete([]byte(keyCoverageFrom), persist.SyncWrite)
	if err == leveldb.ErrNotFound {
		return nil
	}
	return err
}

// RecordBlock indexes txs, stores receipts, and advances IndexedHeight in one
// durable batch when the new height is contiguous. A lost write after state
// commit is repaired by Backfill / ReconcileTxIndex.
func (s *TxIndexStore) RecordBlock(b *types.Block, receipts []*types.Receipt) error {
	if b == nil {
		return nil
	}
	batch := new(leveldb.Batch)
	if err := appendIndexBlock(batch, b); err != nil {
		return err
	}
	if err := appendReceipts(batch, receipts); err != nil {
		return err
	}
	if s.canAdvanceIndexedHeight(b.Header.Height) {
		putIndexMeta(batch, keyIndexHeight, b.Header.Height)
	}
	return persist.Write(s.db, batch)
}

// IndexComplete reports whether this height has its tx list and receipts.
func (s *TxIndexStore) IndexComplete(b *types.Block) bool {
	if b == nil {
		return false
	}
	_, err := s.db.Get(blockTxsKey(b.Header.Height), nil)
	if err != nil {
		return false
	}
	for _, tx := range b.Transactions {
		hash, herr := tx.FullHash()
		if herr != nil {
			return false
		}
		if _, rerr := s.GetReceipt(hash); rerr != nil {
			return false
		}
	}
	return true
}

// Backfill indexes stored blocks in [from, to], restores receipts, and
// advances IndexedHeight only contiguously. load may return a nil block to
// skip a missing ancestor (snapshot gap).
//
// Heights below the persisted watermark are complete by definition and are
// not read again, so a caller may always start at 0 (or the coverage mark)
// and a repeated call over a long chain stays cheap. The watermark itself is
// carried in memory and written every backfillFlushEvery heights and at the
// end. A height that stays incomplete after RecordBlock has receipts nowhere
// on this node; the walk stops there with ErrTxIndexIncomplete instead of
// re-reading every block above it on each later call.
func (s *TxIndexStore) Backfill(from, to uint64, load func(uint64) (*types.Block, []*types.Receipt, error)) error {
	if load == nil || from > to {
		return nil
	}
	mark, haveMark := s.IndexedHeight()
	pending := false
	flush := func() error {
		if !pending {
			return nil
		}
		batch := new(leveldb.Batch)
		putIndexMeta(batch, keyIndexHeight, mark)
		if err := persist.Write(s.db, batch); err != nil {
			return err
		}
		pending = false
		return nil
	}
	var (
		hole     uint64
		holeSeen bool
	)
	walked := uint64(0)
	for h := from; h <= to; h++ {
		if haveMark && h < mark {
			// Below the watermark is complete by definition.
			continue
		}
		b, receipts, err := load(h)
		if err != nil {
			return errors.Join(fmt.Errorf("tx index backfill at %d: %w", h, err), flush())
		}
		if b == nil {
			continue
		}
		if !s.IndexComplete(b) {
			ok, cerr := s.canComplete(b, receipts)
			if cerr != nil {
				return errors.Join(fmt.Errorf("tx index backfill at %d: %w", h, cerr), flush())
			}
			if !ok {
				// Receipts for this height are in neither store. Keep its tx
				// list readable and walk on so later heights are still
				// repaired, but the watermark stops below it: RecordBlock is
				// not used here because it would advance the watermark over
				// a height it was never given receipts for. At the watermark
				// height itself this is expected (a snapshot join's coverage
				// height has no receipts by design) and not reported.
				if err := s.IndexBlock(b); err != nil {
					return errors.Join(fmt.Errorf("tx index backfill list %d: %w", h, err), flush())
				}
				if !(haveMark && h == mark) && !holeSeen {
					hole, holeSeen = h, true
				}
				continue
			}
			if err := s.RecordBlock(b, receipts); err != nil {
				return errors.Join(fmt.Errorf("tx index backfill record %d: %w", h, err), flush())
			}
			if !s.IndexComplete(b) {
				return errors.Join(fmt.Errorf("%w: height %d", ErrTxIndexIncomplete, h), flush())
			}
		}
		if !holeSeen && canAdvanceIndexedHeightFrom(mark, haveMark, s.CoverageFrom, h) {
			mark, haveMark, pending = h, true, true
		}
		walked++
		if walked%backfillFlushEvery == 0 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if holeSeen {
		return fmt.Errorf("%w: height %d", ErrTxIndexIncomplete, hole)
	}
	return nil
}

// canComplete reports whether every transaction in b has a receipt either
// already in the index or among receipts, i.e. whether RecordBlock can make
// the height complete. A read failure is returned, not treated as absence.
func (s *TxIndexStore) canComplete(b *types.Block, receipts []*types.Receipt) (bool, error) {
	given := make(map[[32]byte]struct{}, len(receipts))
	for _, r := range receipts {
		if r != nil {
			given[r.TxHash] = struct{}{}
		}
	}
	for _, tx := range b.Transactions {
		hash, err := tx.FullHash()
		if err != nil {
			return false, nil
		}
		if _, ok := given[hash]; ok {
			continue
		}
		_, err = s.GetReceipt(hash)
		if err == nil {
			continue
		}
		if errors.Is(err, ErrReceiptNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *TxIndexStore) canAdvanceIndexedHeight(h uint64) bool {
	cur, ok := s.IndexedHeight()
	return canAdvanceIndexedHeightFrom(cur, ok, s.CoverageFrom, h)
}

// canAdvanceIndexedHeightFrom is the contiguity rule: the next height after
// the watermark, or the coverage mark when there is no watermark yet, or
// genesis/block 1 on a fresh index.
func canAdvanceIndexedHeightFrom(cur uint64, ok bool, coverage func() (uint64, bool), h uint64) bool {
	if ok {
		return h == cur+1
	}
	if cov, cok := coverage(); cok {
		return h == cov
	}
	return h <= 1
}

func (s *TxIndexStore) readMetaUint64(key string) (uint64, bool, error) {
	raw, err := s.db.Get([]byte(key), nil)
	if err == leveldb.ErrNotFound {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var h uint64
	if err := json.Unmarshal(raw, &h); err != nil {
		return 0, false, err
	}
	return h, true, nil
}

func putIndexMeta(batch *leveldb.Batch, key string, h uint64) {
	raw, _ := json.Marshal(h)
	batch.Put([]byte(key), raw)
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
