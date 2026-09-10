package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRefuseExistingWallet(t *testing.T) {
	fresh := filepath.Join(t.TempDir(), "new.sqwallet")
	if err := refuseExistingWallet(fresh); err != nil {
		t.Fatalf("fresh path refused: %v", err)
	}
	if err := os.WriteFile(fresh, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseExistingWallet(fresh); err == nil {
		t.Fatal("existing wallet was not refused")
	}
}
