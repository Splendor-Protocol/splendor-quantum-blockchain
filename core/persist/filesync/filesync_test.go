package filesync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.json")
	if err := WriteFileAtomic(path, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left after atomic write")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != `{"v":1}` {
		t.Fatalf("first write = %q err=%v", got, err)
	}
	if err := WriteFileAtomic(path, []byte(`{"v":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != `{"v":2}` {
		t.Fatalf("reopen after replace = %q err=%v", got, err)
	}
}

func TestWriteFileAtomicCreatesParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "f.json")
	if err := WriteFileAtomic(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFileAtomicRejectsEmptyPath(t *testing.T) {
	if err := WriteFileAtomic("", []byte("x"), 0o600); err == nil {
		t.Fatal("empty path was accepted")
	}
}
