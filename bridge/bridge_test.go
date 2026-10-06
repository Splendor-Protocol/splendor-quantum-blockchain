package bridge

import (
	"context"
	"math/big"
	"strings"
	"testing"
)

type mockChain struct {
	name    string
	height  uint64
	confirm uint64
	assets  map[Asset]AssetInfo
	events  []OutboundEvent
	settled map[string]bool
	settles []OutboundEvent
}

func (m *mockChain) Name() string                           { return m.name }
func (m *mockChain) ChainID() string                        { return "mock" }
func (m *mockChain) ConfirmDepth() uint64                   { return m.confirm }
func (m *mockChain) Height(context.Context) (uint64, error) { return m.height, nil }
func (m *mockChain) Assets() map[Asset]AssetInfo            { return m.assets }
func (m *mockChain) Balance(context.Context, string, Asset) (*big.Int, error) {
	return big.NewInt(0), nil
}
func (m *mockChain) Outbound(_ context.Context, from, to uint64) ([]OutboundEvent, error) {
	var out []OutboundEvent
	for _, e := range m.events {
		if e.Height >= from && e.Height <= to {
			out = append(out, e)
		}
	}
	return out, nil
}
func (m *mockChain) Settle(_ context.Context, e OutboundEvent) (string, error) {
	if m.settled[e.ID] {
		return "", nil
	}
	m.settled[e.ID] = true
	m.settles = append(m.settles, e)
	return "tx-" + e.ID, nil
}
func (m *mockChain) Settled(_ context.Context, id string) (bool, error) {
	return m.settled[id], nil
}

func newMockChain(name string, height, confirm uint64) *mockChain {
	return &mockChain{
		name: name, height: height, confirm: confirm,
		assets:  map[Asset]AssetInfo{AssetNative: {Symbol: "SPLD"}},
		settled: map[string]bool{},
	}
}

func TestRelayerSettlesOnlyFinalEvents(t *testing.T) {
	src := newMockChain("src", 10, 2) // safe tip = 8
	dst := newMockChain("dst", 0, 0)
	src.events = []OutboundEvent{
		{ID: "1", Asset: AssetNative, Amount: big.NewInt(100), Height: 8, Recipient: "SQ:a"},
		{ID: "2", Asset: AssetNative, Amount: big.NewInt(200), Height: 9, Recipient: "SQ:b"},
	}
	r := NewRelayer(src, dst)
	got, err := r.RunOnce(context.Background(), LeftToRight)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("settled %v, want only event 1 (height 9 is above the safe tip 8)", got)
	}
	if r.Cursor(src) != 9 {
		t.Fatalf("cursor = %d, want 9 (next height after safe tip 8)", r.Cursor(src))
	}
}

func TestRelayerSkipsAlreadySettled(t *testing.T) {
	src := newMockChain("src", 10, 2)
	dst := newMockChain("dst", 0, 0)
	dst.settled["1"] = true
	src.events = []OutboundEvent{
		{ID: "1", Asset: AssetNative, Amount: big.NewInt(100), Height: 8, Recipient: "SQ:a"},
		{ID: "2", Asset: AssetNative, Amount: big.NewInt(200), Height: 7, Recipient: "SQ:b"},
	}
	r := NewRelayer(src, dst)
	got, err := r.RunOnce(context.Background(), LeftToRight)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("settled %v, want only event 2 (event 1 already settled)", got)
	}
	if len(dst.settles) != 1 {
		t.Fatalf("dst settled %d times, want 1 (idempotency)", len(dst.settles))
	}
}

func TestDecodeBurnedLog(t *testing.T) {
	log := evmLog{
		Topics:      []string{burnedTopic0, "0x" + strings.Repeat("0", 63) + "1", "0x" + strings.Repeat("0", 24) + strings.Repeat("1", 40), "0x" + strings.Repeat("0", 63) + "0"},
		Data:        "0x" + strings.Repeat("0", 63) + "a" + strings.Repeat("0", 63) + "b",
		BlockNumber: "0x10",
		TxHash:      "0x" + strings.Repeat("1", 64),
	}
	e, err := decodeBurned(log)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "1" || e.Asset != AssetNative {
		t.Fatalf("id=%s asset=%s, want 1/native", e.ID, e.Asset)
	}
	if e.Amount.Cmp(big.NewInt(0xa)) != 0 {
		t.Fatalf("amount=%s, want 0xa", e.Amount)
	}
	if !strings.HasPrefix(e.Recipient, "SQ:") {
		t.Fatalf("recipient=%q, want SQ: prefix", e.Recipient)
	}
	if want := "0x" + strings.Repeat("1", 40); e.Sender != want {
		t.Fatalf("sender=%s, want %s", e.Sender, want)
	}
	if e.Height != 0x10 {
		t.Fatalf("height=%d, want 16", e.Height)
	}
}

// TestBurnedTopic0 pins the canonical Keccak-256 topic for the Burned event.
// The topic0 must be keccak256(sig) — a Write-then-Sum — not hash(sig)+state.
func TestBurnedTopic0(t *testing.T) {
	const want = "0x18fb7d6af55f185ad81a39bf0537706c6c62595e260cf5aaeb3f491a4240cd58"
	if burnedTopic0 != want {
		t.Fatalf("burnedTopic0 = %s, want %s", burnedTopic0, want)
	}
}

// TestBalanceOfSelector pins the canonical ERC-20 balanceOf(address) selector.
func TestBalanceOfSelector(t *testing.T) {
	const want = "70a08231"
	if got := keccak256("balanceOf(address)")[:8]; got != want {
		t.Fatalf("balanceOf selector = %s, want %s", got, want)
	}
}
