package persist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The recoverable write sites must go through this package. An unsynced
// Write(batch, nil) on those paths is the C3 regression.
func TestDurablePathsUseHelper(t *testing.T) {
	root := filepath.Join("..", "..")
	checks := []struct {
		file    string
		must    []string
		mustNot []string
	}{
		{
			file:    filepath.Join("core", "block", "executor.go"),
			must:    []string{"persist.Write("},
			mustNot: []string{"chainDB.Write(batch, nil)"},
		},
		{
			file:    filepath.Join("core", "state", "statedb.go"),
			must:    []string{"persist.Write("},
			mustNot: []string{"s.db.Write(batch, nil)"},
		},
		{
			file:    filepath.Join("core", "state", "state_snapshot.go"),
			must:    []string{"persist.Write("},
			mustNot: []string{"s.db.Write(batch, nil)"},
		},
		{
			file:    filepath.Join("consensus", "vote_journal.go"),
			must:    []string{"persist.WriteFileAtomic("},
			mustNot: []string{"os.WriteFile(tmp"},
		},
	}
	for _, c := range checks {
		b, err := os.ReadFile(filepath.Join(root, c.file))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		src := string(b)
		for _, want := range c.must {
			if !strings.Contains(src, want) {
				t.Errorf("%s: missing %q", c.file, want)
			}
		}
		for _, ban := range c.mustNot {
			if strings.Contains(src, ban) {
				t.Errorf("%s: still has unsynced write %q", c.file, ban)
			}
		}
	}
}

// Snapshot marker/staging already dirsync; C3 must not rewrite that path.
func TestSnapshotMarkerDirsyncUnchanged(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "core", "block", "state_snapshot.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"func syncDir(dir string) error",
		"if err := syncDir(filepath.Dir(path)); err != nil {",
		"writeSnapshotMarker",
		"writeSnapshotStaging",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("snapshot marker/staging dirsync changed: missing %q", want)
		}
	}
}
