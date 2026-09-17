// Package persist is the durable-write helper for chain, state, and signing
// records. LevelDB's default Write options are Sync=false; a process exit
// may still flush the OS page cache, but a machine failure can lose the last
// batches. Consensus recovery (block before state, journal before a vote
// goes on the wire) only holds if those writes are actually on disk.
package persist

import (
	"errors"
	"os"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/persist/filesync"
)

// SyncWrite is the WriteOptions every recoverable chain/state batch must use.
var SyncWrite = &opt.WriteOptions{Sync: true}

// BatchWriter is the LevelDB write surface persist.Write needs. *leveldb.DB
// implements it; tests substitute a recorder.
type BatchWriter interface {
	Write(batch *leveldb.Batch, wo *opt.WriteOptions) error
}

// Write commits batch with Sync=true. Callers keep the durable order:
// block/head first, then state, then completion/cleanup.
func Write(db BatchWriter, batch *leveldb.Batch) error {
	if db == nil {
		return errors.New("persist: nil database")
	}
	return db.Write(batch, SyncWrite)
}

// SyncDir fsyncs a directory so a rename or unlink inside it is durable.
// The implementation lives in core/persist/filesync, which is leveldb-free
// so packages linked into the WASM signer (core/crypto) can use it.
func SyncDir(dir string) error { return filesync.SyncDir(dir) }

// WriteFileAtomic replaces path with data: temp file, fsync, close, rename,
// then fsync the parent directory. See filesync.WriteFileAtomic.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return filesync.WriteFileAtomic(path, data, perm)
}
