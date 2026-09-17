// Package filesync is the durable plain-file half of core/persist: fsync a
// directory, replace a file atomically. It deliberately imports nothing but
// the standard library.
//
// It is a separate package because core/persist links goleveldb, and
// goleveldb has no file backend for GOOS=js/GOARCH=wasm. core/crypto needs
// only the atomic file write for wallet files, and core/crypto is linked into
// the SDK's WASM signer (sdk/sqc-sdk/wasm → signer → core/types →
// core/crypto). Importing core/persist from core/crypto therefore broke the
// signer build in CI ("undefined: newFileLock" in leveldb/storage). Keep this
// package leveldb-free so the signer keeps building.
package filesync

import (
	"errors"
	"os"
	"path/filepath"
)

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
