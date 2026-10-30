package network

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func statusPeer(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func filled(b byte) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = b
	}
	return h
}

// The peer probe is this node's handshake: the block-sync loop reads every
// pooled peer's /status each second. A peer whose genesis differs from this
// node's is on another network or on a reset chain, and is refused there.
// /status has carried genesis_hash since v1.2 on every node that stores block
// 0, so the check needs nothing from peers that they do not send today.
func TestProbePeerRefusesAPeerOnAnotherGenesis(t *testing.T) {
	ours, theirs := filled(0xaa), filled(0xbb)
	known := func() ([32]byte, bool) { return ours, true }
	unknown := func() ([32]byte, bool) { return [32]byte{}, false }
	status := func(g string) string {
		if g == "" {
			return `{"chain_id":"splendor-quantum-devnet-1","height":42}`
		}
		return `{"chain_id":"splendor-quantum-devnet-1","height":42,"genesis_hash":"` + g + `"}`
	}

	for _, tc := range []struct {
		name      string
		peer      string
		local     func() ([32]byte, bool)
		wantChain bool // refused as another chain
		wantErr   bool
	}{
		{"same genesis", status(hex.EncodeToString(ours[:])), known, false, false},
		{"another genesis", status(hex.EncodeToString(theirs[:])), known, true, true},
		{"peer reports no genesis (holds no block 0)", status(""), known, false, false},
		{"this node knows no genesis yet", status(hex.EncodeToString(theirs[:])), unknown, false, false},
		{"no identity to check against", status(hex.EncodeToString(theirs[:])), nil, false, false},
		{"unreadable genesis", status("zz"), known, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := ProbePeer(statusPeer(t, tc.peer), tc.local)
			if got := errors.Is(err, ErrPeerOnAnotherChain); got != tc.wantChain {
				t.Fatalf("ErrPeerOnAnotherChain = %v, want %v (err %v)", got, tc.wantChain, err)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err == nil && h != 42 {
				t.Fatalf("height = %d, want 42", h)
			}
		})
	}

	// DialPeer keeps its old contract: reachability and height only.
	if h, err := DialPeer(statusPeer(t, status(hex.EncodeToString(theirs[:])))); err != nil || h != 42 {
		t.Fatalf("DialPeer = %d, %v; want 42, nil", h, err)
	}
}
