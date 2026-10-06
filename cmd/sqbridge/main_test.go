package main

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/bridge"
)

// fakeChain is a minimal bridge.Chain used to exercise cursor persistence
// without any network access.
type fakeChain struct{ name string }

func (f fakeChain) Name() string                              { return f.name }
func (f fakeChain) ChainID() string                           { return f.name }
func (f fakeChain) ConfirmDepth() uint64                      { return 0 }
func (f fakeChain) Height(context.Context) (uint64, error)    { return 0, nil }
func (f fakeChain) Assets() map[bridge.Asset]bridge.AssetInfo { return nil }
func (f fakeChain) Balance(context.Context, string, bridge.Asset) (*big.Int, error) {
	return nil, nil
}
func (f fakeChain) Outbound(context.Context, uint64, uint64) ([]bridge.OutboundEvent, error) {
	return nil, nil
}
func (f fakeChain) Settle(context.Context, bridge.OutboundEvent) (string, error) {
	return "", nil
}
func (f fakeChain) Settled(context.Context, string) (bool, error) { return false, nil }

func TestSaveLoadCursorRoundTrip(t *testing.T) {
	r := bridge.NewRelayer(fakeChain{"legacy"}, fakeChain{"quantum"})
	r.SetCursor(r.Left, 123)
	r.SetCursor(r.Right, 456)

	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, r); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	restored := bridge.NewRelayer(fakeChain{"legacy"}, fakeChain{"quantum"})
	if err := loadState(path, restored); err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if got := restored.Cursor(restored.Left); got != 123 {
		t.Fatalf("left cursor = %d, want 123", got)
	}
	if got := restored.Cursor(restored.Right); got != 456 {
		t.Fatalf("right cursor = %d, want 456", got)
	}
}

func TestLoadStateMissingFileIsError(t *testing.T) {
	r := bridge.NewRelayer(fakeChain{"legacy"}, fakeChain{"quantum"})
	err := loadState(filepath.Join(t.TempDir(), "nope.json"), r)
	if err == nil {
		t.Fatal("loadState on a missing file should return an error")
	}
}
