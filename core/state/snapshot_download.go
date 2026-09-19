package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/sha3"

	"github.com/syndtr/goleveldb/leveldb"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/persist"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// DefaultMaxSnapshotDiskBytes is the whole-object cap for a chunked snapshot
// staged on disk. Distinct from MaxStateSnapshotBytes (single-shot serve) and
// MaxChunkBytes (one page). Download RAM stays one page; this is the disk bound.
const DefaultMaxSnapshotDiskBytes int64 = 1 << 30

var (
	// ErrSnapshotDownloadMismatch means a peer offered a different authenticated
	// snapshot than the one already staged. The staged object is left intact
	// so another peer can continue it.
	ErrSnapshotDownloadMismatch = errors.New("state: download staging is for a different snapshot")
	// ErrSnapshotDownloadTooLarge means the staged object would exceed the
	// whole-object disk budget.
	ErrSnapshotDownloadTooLarge = errors.New("state: snapshot download exceeds the disk budget")
	// ErrSnapshotDownloadCorrupt means progress and the leaf file disagree.
	ErrSnapshotDownloadCorrupt = errors.New("state: snapshot download staging is corrupt")
	// ErrSnapshotDownloadIncomplete means Assemble/import was asked for a
	// transfer that has not reached its final page.
	ErrSnapshotDownloadIncomplete = errors.New("state: snapshot download is not complete")
)

const (
	snapshotDownloadProgressFile = "progress.json"
	snapshotDownloadHeadFile     = "head.json"
	snapshotDownloadLeavesFile   = "leaves.jsonl"
)

// SnapshotDownloadProgress is the durable identity, cursor and integrity
// metadata for a chunked snapshot download.
type SnapshotDownloadProgress struct {
	Height        uint64   `json:"height"`
	BlockHash     [32]byte `json:"block_hash"`
	StateRoot     [32]byte `json:"state_root"`
	ChainID       string   `json:"chain_id"`
	SessionID     string   `json:"session_id"`
	AfterKey      []byte   `json:"after_key,omitempty"`
	LeafCount     int      `json:"leaf_count"`
	LeafBytes     int64    `json:"leaf_bytes"`
	LeafFileBytes int64    `json:"leaf_file_bytes"`
	PageCount     int      `json:"page_count"`
	LeavesHash    [32]byte `json:"leaves_hash"`
	Done          bool     `json:"done"`
}

// SnapshotDownload is a bounded on-disk staging area for one authenticated
// chunked snapshot: pages are appended as JSONL, progress is fsynced after
// each page, and a crash can resume from AfterKey under the same head.
type SnapshotDownload struct {
	dir      string
	maxBytes int64
	progress SnapshotDownloadProgress
	head     *types.Block
}

// OpenSnapshotDownload creates or reopens dir. maxBytes <= 0 uses
// DefaultMaxSnapshotDiskBytes. A torn last page (file longer than the last
// persisted progress) is truncated back to the last consistent length.
func OpenSnapshotDownload(dir string, maxBytes int64) (*SnapshotDownload, error) {
	if dir == "" {
		return nil, errors.New("state: snapshot download directory is empty")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxSnapshotDiskBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("state: create snapshot download dir: %w", err)
	}
	d := &SnapshotDownload{dir: dir, maxBytes: maxBytes}
	if err := d.load(); err != nil {
		return nil, err
	}
	return d, nil
}

// RemoveSnapshotDownload deletes dir if it exists.
func RemoveSnapshotDownload(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	if parent == "." || parent == string(os.PathSeparator) {
		return nil
	}
	return persist.SyncDir(parent)
}

func (d *SnapshotDownload) Dir() string { return d.dir }

func (d *SnapshotDownload) Progress() SnapshotDownloadProgress { return d.progress }

func (d *SnapshotDownload) HasHead() bool { return d.head != nil }

func (d *SnapshotDownload) Head() *types.Block { return d.head }

func (d *SnapshotDownload) Height() uint64 { return d.progress.Height }

func (d *SnapshotDownload) BlockHash() [32]byte { return d.progress.BlockHash }

func (d *SnapshotDownload) StateRoot() [32]byte { return d.progress.StateRoot }

func (d *SnapshotDownload) SessionID() string { return d.progress.SessionID }

func (d *SnapshotDownload) AfterKey() []byte {
	if len(d.progress.AfterKey) == 0 {
		return nil
	}
	return append([]byte(nil), d.progress.AfterKey...)
}

func (d *SnapshotDownload) Done() bool { return d.progress.Done }

func (d *SnapshotDownload) LeafBytes() int64 { return d.progress.LeafBytes }

func (d *SnapshotDownload) progressPath() string {
	return filepath.Join(d.dir, snapshotDownloadProgressFile)
}
func (d *SnapshotDownload) headPath() string {
	return filepath.Join(d.dir, snapshotDownloadHeadFile)
}
func (d *SnapshotDownload) leavesPath() string {
	return filepath.Join(d.dir, snapshotDownloadLeavesFile)
}

func (d *SnapshotDownload) load() error {
	raw, err := os.ReadFile(d.progressPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: read snapshot download progress: %w", err)
	}
	if err := json.Unmarshal(raw, &d.progress); err != nil {
		return fmt.Errorf("state: decode snapshot download progress: %w", err)
	}
	if err := d.repairLeavesFile(); err != nil {
		return err
	}
	got, err := hashFileSHA3(d.leavesPath())
	if err != nil {
		return err
	}
	if got != d.progress.LeavesHash {
		return fmt.Errorf("%w: leaves hash %x != progress %x",
			ErrSnapshotDownloadCorrupt, got[:8], d.progress.LeavesHash[:8])
	}
	headRaw, err := os.ReadFile(d.headPath())
	if os.IsNotExist(err) {
		if d.progress.Height != 0 || d.progress.LeafCount > 0 {
			return fmt.Errorf("%w: progress present but head file missing", ErrSnapshotDownloadCorrupt)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: read snapshot download head: %w", err)
	}
	var head types.Block
	if err := json.Unmarshal(headRaw, &head); err != nil {
		return fmt.Errorf("state: decode snapshot download head: %w", err)
	}
	if head.Hash != d.progress.BlockHash {
		return fmt.Errorf("%w: head hash %x != progress %x",
			ErrSnapshotDownloadCorrupt, head.Hash[:8], d.progress.BlockHash[:8])
	}
	d.head = &head
	return nil
}

func (d *SnapshotDownload) repairLeavesFile() error {
	path := d.leavesPath()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if d.progress.LeafFileBytes == 0 {
			return nil
		}
		return fmt.Errorf("%w: leaves file missing", ErrSnapshotDownloadCorrupt)
	}
	if err != nil {
		return err
	}
	if info.Size() < d.progress.LeafFileBytes {
		return fmt.Errorf("%w: leaves file truncated (%d < %d)",
			ErrSnapshotDownloadCorrupt, info.Size(), d.progress.LeafFileBytes)
	}
	if info.Size() > d.progress.LeafFileBytes {
		// Crash after appending a page and before progress landed: drop the
		// torn tail so the next page resumes from the last acknowledged cursor.
		if err := os.Truncate(path, d.progress.LeafFileBytes); err != nil {
			return fmt.Errorf("state: truncate torn snapshot download: %w", err)
		}
		if err := persist.SyncDir(d.dir); err != nil {
			return err
		}
	}
	return nil
}

// AcceptHead records the authenticated head. A second call with the same
// identity is a no-op; a different identity is refused without mutating the
// store so a failover peer cannot clobber an in-flight transfer.
func (d *SnapshotDownload) AcceptHead(head *types.Block, height uint64) error {
	if head == nil {
		return errors.New("state: snapshot download head is nil")
	}
	if d.head != nil {
		if d.progress.BlockHash != head.Hash || d.progress.Height != height {
			return fmt.Errorf("%w: staged height %d hash %x, peer offered height %d hash %x",
				ErrSnapshotDownloadMismatch, d.progress.Height, d.progress.BlockHash[:8],
				height, head.Hash[:8])
		}
		return nil
	}
	raw, err := json.Marshal(head)
	if err != nil {
		return fmt.Errorf("state: marshal snapshot download head: %w", err)
	}
	if err := persist.WriteFileAtomic(d.headPath(), raw, 0o600); err != nil {
		return fmt.Errorf("state: write snapshot download head: %w", err)
	}
	d.head = head
	d.progress.Height = height
	d.progress.BlockHash = head.Hash
	d.progress.StateRoot = head.Header.StateRoot
	d.progress.ChainID = head.Header.ChainID
	return d.persistProgress()
}

// SetSession records the serving session id (may change after a peer failover
// starts a new session at the saved cursor).
func (d *SnapshotDownload) SetSession(sessionID string) error {
	if d.progress.SessionID == sessionID {
		return nil
	}
	d.progress.SessionID = sessionID
	return d.persistProgress()
}

// AppendPage writes verified page leaves to disk and advances the cursor.
// Leaves whose keys are at or before the persisted cursor are skipped so a
// restarted session that re-sends an earlier page does not duplicate.
func (d *SnapshotDownload) AppendPage(leaves []types.StateLeaf, nextKey []byte, done bool, sessionID string) error {
	if d.head == nil {
		return errors.New("state: snapshot download AppendPage before AcceptHead")
	}
	filtered := leaves
	if last := d.progress.AfterKey; len(last) > 0 {
		i := 0
		for i < len(filtered) && bytes.Compare(filtered[i].Key, last) <= 0 {
			i++
		}
		filtered = filtered[i:]
	}
	var addedBytes int64
	for _, l := range filtered {
		if len(l.Key) == 0 {
			return errors.New("state: snapshot download page carries an empty key")
		}
		if bytes.HasPrefix(l.Key, []byte(prefixMeta)) {
			return fmt.Errorf("state: snapshot download carries node-local key %q", l.Key)
		}
		addedBytes += int64(len(l.Key) + len(l.Value))
	}
	if d.progress.LeafBytes+addedBytes > d.maxBytes {
		return fmt.Errorf("%w: %d raw leaf bytes would exceed %d",
			ErrSnapshotDownloadTooLarge, d.progress.LeafBytes+addedBytes, d.maxBytes)
	}

	if _, err := d.appendLeavesJSONL(filtered); err != nil {
		return err
	}
	sum, err := hashFileSHA3(d.leavesPath())
	if err != nil {
		return err
	}
	info, err := os.Stat(d.leavesPath())
	var fileBytes int64
	if err == nil {
		fileBytes = info.Size()
	} else if !os.IsNotExist(err) {
		return err
	}

	d.progress.LeafCount += len(filtered)
	d.progress.LeafBytes += addedBytes
	d.progress.LeafFileBytes = fileBytes
	d.progress.PageCount++
	d.progress.LeavesHash = sum
	d.progress.SessionID = sessionID
	d.progress.Done = done
	if done {
		d.progress.AfterKey = nil
	} else if len(nextKey) > 0 {
		d.progress.AfterKey = append([]byte(nil), nextKey...)
	}
	return d.persistProgress()
}

func (d *SnapshotDownload) appendLeavesJSONL(leaves []types.StateLeaf) ([]byte, error) {
	if len(leaves) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, l := range leaves {
		if err := enc.Encode(l); err != nil {
			return nil, fmt.Errorf("state: encode snapshot download leaf: %w", err)
		}
	}
	data := buf.Bytes()
	f, err := os.OpenFile(d.leavesPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("state: open snapshot download leaves: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, fmt.Errorf("state: append snapshot download leaves: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, fmt.Errorf("state: sync snapshot download leaves: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := persist.SyncDir(d.dir); err != nil {
		return nil, err
	}
	return data, nil
}

func (d *SnapshotDownload) persistProgress() error {
	raw, err := json.Marshal(d.progress)
	if err != nil {
		return fmt.Errorf("state: marshal snapshot download progress: %w", err)
	}
	if err := persist.WriteFileAtomic(d.progressPath(), raw, 0o600); err != nil {
		return fmt.Errorf("state: write snapshot download progress: %w", err)
	}
	return nil
}

// VerifyRoot checks the staged leaves hash to the authenticated head's state_root.
func (d *SnapshotDownload) VerifyRoot() error {
	if d.head == nil {
		return errors.New("state: snapshot download has no head")
	}
	got, err := SnapshotRootJSONL(d.leavesPath())
	if err != nil {
		return err
	}
	if got != d.progress.StateRoot {
		return fmt.Errorf("%w: staged leaves hash to %x, head commits to %x",
			ErrSnapshotRootMismatch, got[:8], d.progress.StateRoot[:8])
	}
	return nil
}

// Assemble loads the staged snapshot into memory. Callers that must stay
// within the page RAM budget should import from the JSONL file instead.
func (d *SnapshotDownload) Assemble() (*types.StateSnapshot, error) {
	if d.head == nil {
		return nil, errors.New("state: snapshot download has no head")
	}
	if !d.progress.Done {
		return nil, ErrSnapshotDownloadIncomplete
	}
	if err := d.VerifyRoot(); err != nil {
		return nil, err
	}
	var leaves []types.StateLeaf
	if err := forEachLeafJSONL(d.leavesPath(), func(l types.StateLeaf) error {
		leaves = append(leaves, types.StateLeaf{
			Key:   append([]byte(nil), l.Key...),
			Value: append([]byte(nil), l.Value...),
		})
		return nil
	}); err != nil {
		return nil, err
	}
	return &types.StateSnapshot{Height: d.progress.Height, Block: d.head, Leaves: leaves}, nil
}

// Remove deletes this staging directory.
func (d *SnapshotDownload) Remove() error {
	return RemoveSnapshotDownload(d.dir)
}

func hashFileSHA3(path string) ([32]byte, error) {
	var zero [32]byte
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return zero, nil
	}
	if err != nil {
		return zero, err
	}
	defer f.Close()
	h := sha3.New256()
	if _, err := io.Copy(h, f); err != nil {
		return zero, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// SnapshotRootJSONL is SnapshotRoot over a JSONL leaf file.
func SnapshotRootJSONL(path string) ([32]byte, error) {
	index := make(map[string][32]byte)
	err := forEachLeafJSONL(path, func(l types.StateLeaf) error {
		if len(l.Key) == 0 {
			return errors.New("state: snapshot carries an empty key")
		}
		if _, dup := index[string(l.Key)]; dup {
			return fmt.Errorf("state: snapshot carries key %q twice", l.Key)
		}
		index[string(l.Key)] = qcrypto.Hash32(l.Value)
		return nil
	})
	if err != nil {
		return [32]byte{}, err
	}
	return merkleRoot(index), nil
}

func forEachLeafJSONL(path string, fn func(types.StateLeaf) error) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: open snapshot jsonl: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// A single leaf can approach MaxChunkBytes; JSON/base64 expands it.
	sc.Buffer(make([]byte, 0, 64*1024), MaxChunkBytes*2)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var l types.StateLeaf
		if err := json.Unmarshal(line, &l); err != nil {
			return fmt.Errorf("state: decode snapshot jsonl: %w", err)
		}
		if err := fn(l); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("state: scan snapshot jsonl: %w", err)
	}
	return nil
}

// ImportSnapshotJSONL is ImportSnapshot reading leaves from a JSONL file so
// the importer never holds a second full-snapshot JSON buffer.
func (s *StateDB) ImportSnapshotJSONL(path string, height uint64, wantRoot [32]byte) error {
	return s.importSnapshotJSONL(path, height, wantRoot, false)
}

// ImportSnapshotJSONLResume is ImportSnapshotJSONL for the recovery path: it
// also accepts a state whose only content is a previous, interrupted bounded-
// batch publish (leaf values present but no height marker), because re-writing
// the same verified leaves is idempotent and the height marker is what commits
// the snapshot. It never accepts a state that already has a committed height.
func (s *StateDB) ImportSnapshotJSONLResume(path string, height uint64, wantRoot [32]byte) error {
	return s.importSnapshotJSONL(path, height, wantRoot, true)
}

func (s *StateDB) importSnapshotJSONL(path string, height uint64, wantRoot [32]byte, resume bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sandbox {
		return ErrSandboxCommit
	}
	if !s.importAllowedLocked(resume) {
		return ErrStateNotFresh
	}

	// Pass 1 — verify. Build the leaf-hash index (32 bytes per leaf) and check
	// the root. No leaf VALUES are retained: the memory is bounded by the hash
	// index, not by the snapshot's value bytes.
	index := make(map[string][32]byte)
	if err := forEachLeafJSONL(path, func(l types.StateLeaf) error {
		if len(l.Key) == 0 {
			return errors.New("state: snapshot carries an empty key")
		}
		if bytes.HasPrefix(l.Key, []byte(prefixMeta)) {
			return fmt.Errorf("state: snapshot carries node-local key %q", l.Key)
		}
		if _, dup := index[string(l.Key)]; dup {
			return fmt.Errorf("state: snapshot carries key %q twice", l.Key)
		}
		index[string(l.Key)] = qcrypto.Hash32(l.Value)
		return nil
	}); err != nil {
		return err
	}
	if len(index) == 0 {
		return errors.New("state snapshot carries no leaves")
	}
	if got := merkleRoot(index); got != wantRoot {
		return fmt.Errorf("%w: leaves hash to %x, header says %x", ErrSnapshotRootMismatch, got[:8], wantRoot[:8])
	}

	// Pass 2 — publish in bounded batches. Each batch holds at most
	// snapshotImportBatchLeaves leaf values, so RAM stays flat regardless of
	// snapshot size. The height marker is written last and is what makes the
	// import committed; an interrupted pass leaves values but no marker, which
	// the resume path (and the executor's recovery marker) re-imports.
	const snapshotImportBatchLeaves = 10000
	batch := new(leveldb.Batch)
	n := 0
	flush := func() error {
		if err := persist.Write(s.db, batch); err != nil {
			return fmt.Errorf("state: snapshot import failed: %w", err)
		}
		batch = new(leveldb.Batch)
		n = 0
		return nil
	}
	if err := forEachLeafJSONL(path, func(l types.StateLeaf) error {
		// The file is a local staging artifact, but re-check each value against
		// the pass-1 index so a mutation between passes cannot publish data the
		// root never committed to.
		if qcrypto.Hash32(l.Value) != index[string(l.Key)] {
			return fmt.Errorf("state: snapshot changed during import at key %q", l.Key)
		}
		batch.Put(l.Key, l.Value)
		n++
		if n >= snapshotImportBatchLeaves {
			return flush()
		}
		return nil
	}); err != nil {
		return err
	}
	hb, err := json.Marshal(height)
	if err != nil {
		return fmt.Errorf("marshal height marker: %w", err)
	}
	batch.Put([]byte(keyStateHeight), hb)
	if err := persist.Write(s.db, batch); err != nil {
		return fmt.Errorf("state: snapshot import failed: %w", err)
	}

	s.leaves = index
	s.nativeSum = nil
	s.touchLocked()
	s.appliedHeight = height
	s.heightKnown = true
	return nil
}

// importAllowedLocked reports whether an import may replace the committed
// state. A fresh DB is always importable; with resume, a DB that has leaf
// values but no committed height marker (an interrupted bounded-batch publish)
// is also importable. Any pending overlay or committed height refuses.
func (s *StateDB) importAllowedLocked(resume bool) bool {
	if len(s.dirty) > 0 || len(s.codeCache) > 0 || len(s.pendingRaw) > 0 {
		return false
	}
	if s.heightKnown {
		return false
	}
	if len(s.leaves) == 0 {
		return true
	}
	return resume
}
