// Package persist is the durable-write helper for chain, state, and signing
// records. LevelDB's default Write options are Sync=false; a process exit
// may still flush the OS page cache, but a machine failure can lose the last
// batches. Consensus recovery (block before state, journal before a vote
// goes on the wire) only holds if those writes are actually on disk.
package persist

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
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
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// WriteFileAtomic replaces path with data: temp file, fsync, close, rename,
// then fsync the parent directory. A crash leaves the old file or the new
// one, never a torn destination, and the directory entry itself survives
// power loss.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if path == "" {
		return errors.New("persist: empty path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return SyncDir(dir)
}
