package bridge

import (
	"bytes"
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
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

	// outboundRanges records every Outbound [from, to] range, so tests can
	// assert the relayer never hands the adapter an inverted range.
	outboundRanges [][2]uint64
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
	m.outboundRanges = append(m.outboundRanges, [2]uint64{from, to})
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

// TestRelayerIdlesWhenCursorPastSafeTip pins the idle-pass behaviour: once the
// cursor is already at/past the safe tip, RunOnce must not call Outbound with
// an inverted [from, to] range (eth_getLogs rejects fromBlock > toBlock).
func TestRelayerIdlesWhenCursorPastSafeTip(t *testing.T) {
	src := newMockChain("src", 10, 2) // safe tip = 8
	dst := newMockChain("dst", 0, 0)
	r := NewRelayer(src, dst)
	r.SetCursor(src, 9) // already past the safe tip 8

	got, err := r.RunOnce(context.Background(), LeftToRight)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("settled %v, want none", got)
	}
	if len(src.outboundRanges) != 0 {
		t.Fatalf("Outbound called with %v, want no call (inverted range must be skipped)", src.outboundRanges)
	}
	if r.Cursor(src) != 9 {
		t.Fatalf("cursor = %d, want unchanged 9", r.Cursor(src))
	}
}

// TestRelayerFailsClosedOnEmptyID pins that a source event without an
// idempotency key aborts the pass and holds the cursor, rather than being
// silently skipped and advanced past (which would drop it forever).
func TestRelayerFailsClosedOnEmptyID(t *testing.T) {
	src := newMockChain("src", 10, 2) // safe tip = 8
	dst := newMockChain("dst", 0, 0)
	src.events = []OutboundEvent{
		{ID: "", Asset: AssetNative, Amount: big.NewInt(100), Height: 5, Recipient: "SQ:a"},
	}
	r := NewRelayer(src, dst)
	if _, err := r.RunOnce(context.Background(), LeftToRight); err == nil {
		t.Fatal("RunOnce accepted an empty-ID event, want an error")
	}
	if r.Cursor(src) != 0 {
		t.Fatalf("cursor = %d, want 0 (held on the empty-ID event)", r.Cursor(src))
	}
	if len(dst.settles) != 0 {
		t.Fatalf("dst settled %d events, want 0", len(dst.settles))
	}
}

// TestBalanceOfSelector pins the canonical ERC-20 balanceOf(address) selector.
func TestBalanceOfSelector(t *testing.T) {
	const want = "70a08231"
	if got := keccak256("balanceOf(address)")[:8]; got != want {
		t.Fatalf("balanceOf selector = %s, want %s", got, want)
	}
}

// An oversized RPC response must fail the call, not balloon memory: the
// endpoint is inside the operator trust boundary, but a misbehaving node must
// fail the pass loudly rather than allocate unbounded.
func TestEVMChainBoundsOversizedResponse(t *testing.T) {
	old := maxRPCResponseBytes
	maxRPCResponseBytes = 256
	t.Cleanup(func() { maxRPCResponseBytes = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'a'}, 1024))
	}))
	t.Cleanup(srv.Close)
	c := NewEVMChain(EVMConfig{RPCURL: srv.URL, ChainIDValue: 2691})
	if _, err := c.Height(context.Background()); err == nil {
		t.Fatal("oversized EVM response accepted")
	}
}

// The same bound on the quantum side.
func TestQuantumChainBoundsOversizedResponse(t *testing.T) {
	old := maxRPCResponseBytes
	maxRPCResponseBytes = 256
	t.Cleanup(func() { maxRPCResponseBytes = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'a'}, 1024))
	}))
	t.Cleanup(srv.Close)
	c := NewQuantumChain(QuantumConfig{BaseURL: srv.URL})
	if _, err := c.Height(context.Background()); err == nil {
		t.Fatal("oversized quantum response accepted")
	}
}
