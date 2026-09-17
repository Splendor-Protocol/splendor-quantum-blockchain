package persist

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

type recordDB struct {
	wo *opt.WriteOptions
}

func (r *recordDB) Write(_ *leveldb.Batch, wo *opt.WriteOptions) error {
	r.wo = wo
	return nil
}

func TestWriteUsesSync(t *testing.T) {
	var rec recordDB
	if err := Write(&rec, new(leveldb.Batch)); err != nil {
		t.Fatal(err)
	}
	if rec.wo == nil || !rec.wo.Sync {
		t.Fatalf("Write used Sync=%v, want true", rec.wo)
	}
	if rec.wo != SyncWrite {
		t.Fatal("Write did not use persist.SyncWrite")
	}
}

func TestWriteRejectsNilDB(t *testing.T) {
	if err := Write(nil, new(leveldb.Batch)); err == nil {
		t.Fatal("nil database was accepted")
	}
}

func TestWriteFileAtomicSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal.json")
	if err := WriteFileAtomic(path, []byte(`{"height":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left after atomic write")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != `{"height":1}` {
		t.Fatalf("first write = %q err=%v", got, err)
	}
	if err := WriteFileAtomic(path, []byte(`{"height":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != `{"height":2}` {
		t.Fatalf("reopen after replace = %q err=%v", got, err)
	}
}

func TestSyncDirOnMissingIsError(t *testing.T) {
	if err := SyncDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("SyncDir on a missing directory succeeded")
	}
}
