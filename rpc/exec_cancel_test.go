package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

func installLoopContract(t *testing.T, sdb *state.StateDB) qcrypto.Address {
	t.Helper()
	code := []byte{vm.OpJUMPDEST, vm.OpPUSH1, 0x00, vm.OpPUSH1, 0x01, vm.OpJUMPI}
	h := qcrypto.Hash32(code)
	sdb.SetCode(h, code)
	var addr qcrypto.Address
	addr[31] = 0xC5
	acc := types.NewAccount(addr)
	acc.IsContract = true
	acc.CodeHash = h
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestCancelledCallReleasesSlotBeforeTimeout(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	to := installLoopContract(t, sdb)

	guard := NewExecGuard(0, 0, 1, 30*time.Second, ClientIPPolicy{})
	h := guard.Wrap(makeCallHandler(sdb, nil))
	body, _ := json.Marshal(map[string]string{
		"to": to.String(), "data": "", "encoding": "hex",
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(body)).WithContext(ctx)
	req.RemoteAddr = "203.0.113.8:1"
	started := make(chan struct{})
	done := make(chan int, 1)
	go func() {
		close(started)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		done <- rec.Code
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case code := <-done:
		if code != http.StatusRequestTimeout && code != http.StatusGatewayTimeout {
			t.Fatalf("cancelled /call status %d, want 408/504", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled /call held the slot toward the 30s guard timeout")
	}

	// Slot must be free for the next caller.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	req2 := httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(body)).WithContext(ctx2)
	req2.RemoteAddr = "203.0.113.8:2"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusServiceUnavailable {
		t.Fatal("slot still held after the cancelled /call returned")
	}
}

func TestStateProofIsGuardedAndHonoursCancel(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	var addr qcrypto.Address
	addr[31] = 0x42
	acc := types.NewAccount(addr)
	acc.AddBalance(big.NewInt(1))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}

	guard := NewExecGuard(0, 0, 1, 30*time.Second, ClientIPPolicy{})
	proof := guard.Wrap(merkleProofHandler(sdb, nil))

	entered := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h := guard.Wrap(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodPost, "/hold", nil)
		req.RemoteAddr = "203.0.113.1:1"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("guard slot was never taken")
	}

	proofReq := httptest.NewRequest(http.MethodGet, "/state/proof/"+addr.String(), nil)
	proofReq.RemoteAddr = "203.0.113.2:1"
	proofRec := httptest.NewRecorder()
	proof.ServeHTTP(proofRec, proofReq)
	if proofRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/state/proof status %d while another request holds the slot, want 503", proofRec.Code)
	}

	close(release)
	wg.Wait()

	// Already-cancelled proof must return without the 30s timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/state/proof/"+addr.String(), nil).WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"address": addr.String()})
	req.RemoteAddr = "203.0.113.3:1"
	start := time.Now()
	rec := httptest.NewRecorder()
	proof.ServeHTTP(rec, req)
	if time.Since(start) > time.Second {
		t.Fatal("cancelled /state/proof ran toward the guard timeout")
	}
	if rec.Code != http.StatusRequestTimeout && rec.Code != http.StatusOK {
		// 408 if generation saw the cancel; 200 if the tree was tiny and
		// finished before the check. Either way it must not 503.
		t.Fatalf("cancelled proof status %d", rec.Code)
	}
}

func TestCompileContextHonoursCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Imported via verify path; compiler package is tested there. This
	// asserts the RPC wrapper refuses a cancelled verify without compiling.
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	var addr qcrypto.Address
	addr[31] = 0x77
	acc := types.NewAccount(addr)
	acc.IsContract = true
	acc.CodeHash = [32]byte{1}
	sdb.SetAccount(acc)
	sdb.SetCode(acc.CodeHash, []byte{0x00})
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}

	h := makeVerifyHandler(sdb)
	body, _ := json.Marshal(map[string]string{
		"address": addr.String(),
		"source":  "contract C { function f() {} }",
	})
	req := httptest.NewRequest(http.MethodPost, "/contract/verify", bytes.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusRequestTimeout {
		t.Fatalf("cancelled verify status %d, want 408", rec.Code)
	}
}
