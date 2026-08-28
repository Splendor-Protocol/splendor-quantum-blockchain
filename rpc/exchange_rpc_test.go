package rpc

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// exchangeState is a StateDB with one exchange, one condition, an operator
// and a holder with a split position, built through the write router with
// exchange_v1 scheduled — the shape the read routes serve.
type exchangeState struct {
	sdb      *state.StateDB
	owner    qcrypto.Address
	operator qcrypto.Address
	holder   qcrypto.Address
	token    qcrypto.Address
	exchange qcrypto.Address
}

func newExchangeState(t *testing.T, sdb *state.StateDB) *exchangeState {
	t.Helper()
	s := &exchangeState{sdb: sdb, owner: qcrypto.Address{0x11}, operator: qcrypto.Address{0x0e}, holder: qcrypto.Address{0x22}}
	sched := forks.Schedule{forks.ExchangeV1: 1, forks.QRC20V2: 1, forks.QRC1155V1: 1, forks.ModuleRevertV1: 1}
	ctx := func(caller qcrypto.Address, nonce uint64) vm.ModuleContext {
		return vm.ModuleContext{Caller: caller, Nonce: nonce, Height: 1, Time: 1_000_000, Forks: sched}
	}
	write := func(caller qcrypto.Address, nonce uint64, module, action string, params interface{}) *types.Receipt {
		t.Helper()
		rc := &types.Receipt{}
		if err := vm.DispatchModuleCall(sdb, ctx(caller, nonce), moduleEnvelope(t, module, action, params), rc); err != nil {
			t.Fatalf("%s.%s: %v", module, action, err)
		}
		return rc
	}
	dep := write(s.owner, 1, "qrc20", "deploy", map[string]interface{}{
		"name": "Collateral", "symbol": "USDQ", "decimals": 6, "total_supply": "1000000",
	})
	s.token = *dep.ContractAddress
	write(s.owner, 2, "qrc20", "transfer", map[string]interface{}{"token": s.token.String(), "to": s.holder.String(), "amount": "1000"})
	dep = write(s.owner, 3, "exchange", "deploy", map[string]interface{}{"name": "Positions", "redeem_fee_bps": 250})
	s.exchange = *dep.ContractAddress
	ex := map[string]interface{}{"exchange": s.exchange.String()}
	write(s.owner, 4, "exchange", "set_operator", map[string]interface{}{"exchange": ex["exchange"], "operator": s.operator.String(), "active": true})
	write(s.operator, 0, "exchange", "create_condition", map[string]interface{}{
		"exchange": ex["exchange"], "collateral": s.token.String(), "resolver": s.owner.String(),
		"end_time": 2_000_000_000, "outcome_count": 2, "question_hash": strings.Repeat("ab", 32),
	})
	write(s.holder, 0, "exchange", "split", map[string]interface{}{"exchange": ex["exchange"], "condition": 1, "amount": "40"})
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return s
}

// callDecoded runs a module read through POST /call and returns `decoded`.
func callDecoded(t *testing.T, sdb *state.StateDB, module, action string, params interface{}) (int, map[string]interface{}, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"to": vm.ModuleRouterAddress.String(), "data": hexOf(moduleEnvelope(t, module, action, params)),
	})
	rec := httptest.NewRecorder()
	makeCallHandler(sdb, nil)(rec, httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		return rec.Code, nil, rec.Body.String()
	}
	var resp struct {
		Decoded map[string]interface{} `json:"decoded"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode /call: %v (body=%s)", err, rec.Body.String())
	}
	return rec.Code, resp.Decoded, ""
}

// Every GET route returns exactly what the matching /call read returns for
// the same instance — the same keys, values and error texts — because both
// go through one read dispatch.
func TestExchangeRoutesDeepEqualCall(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	s := newExchangeState(t, sdb)
	r := mux.NewRouter()
	RegisterExchangeRoutes(r, sdb, nil)
	ex := s.exchange.String()
	hash := strings.Repeat("ab", 32)

	for _, tc := range []struct {
		path   string
		action string
		params map[string]interface{}
	}{
		{"/exchange/" + ex, "exchange_info", map[string]interface{}{"exchange": ex}},
		{"/exchange/" + ex + "/condition/1", "condition_info", map[string]interface{}{"exchange": ex, "condition": 1}},
		{"/exchange/" + ex + "/order/" + hash, "order_status", map[string]interface{}{"exchange": ex, "hash": hash}},
		{"/exchange/" + ex + "/nonce/" + s.holder.String(), "nonce", map[string]interface{}{"exchange": ex, "maker": s.holder.String()}},
		{"/exchange/" + ex + "/position/1/" + s.holder.String(), "position", map[string]interface{}{"exchange": ex, "condition": 1, "holder": s.holder.String()}},
		// Failures carry the same text: a condition that does not exist, an
		// instance nothing is deployed at, a malformed hash.
		{"/exchange/" + ex + "/condition/7", "condition_info", map[string]interface{}{"exchange": ex, "condition": 7}},
		{"/exchange/" + qcrypto.Address{0x99}.String(), "exchange_info", map[string]interface{}{"exchange": qcrypto.Address{0x99}.String()}},
		{"/exchange/" + ex + "/order/zz", "order_status", map[string]interface{}{"exchange": ex, "hash": "zz"}},
		{"/exchange/nope", "exchange_info", map[string]interface{}{"exchange": "nope"}},
	} {
		t.Run(tc.action+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			callCode, want, callErr := callDecoded(t, sdb, "exchange", tc.action, tc.params)
			if rec.Code != callCode {
				t.Fatalf("GET %s = %d (%s), /call = %d (%s)", tc.path, rec.Code, rec.Body.String(), callCode, callErr)
			}
			if rec.Code != http.StatusOK {
				if rec.Body.String() != callErr {
					t.Fatalf("GET error %s, /call error %s", rec.Body.String(), callErr)
				}
				return
			}
			var got map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode GET: %v (%s)", err, rec.Body.String())
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("GET %s:\n %v\n/call:\n %v", tc.path, got, want)
			}
			if got["exists"] != (tc.params["exchange"] == ex) {
				t.Fatalf("exists = %v", got["exists"])
			}
		})
	}

	// The values are the instance's, not placeholders.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/exchange/"+ex+"/position/1/"+s.holder.String(), nil))
	var pos struct {
		Balances []string `json:"balances"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pos); err != nil || len(pos.Balances) != 2 || pos.Balances[0] != "40" || pos.Balances[1] != "40" {
		t.Fatalf("position = %s (%v)", rec.Body.String(), err)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/exchange/"+ex, nil))
	var info struct {
		Operators      []string `json:"operators"`
		ConditionCount uint64   `json:"condition_count"`
		RedeemFeeBps   uint64   `json:"redeem_fee_bps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil || len(info.Operators) != 1 || info.Operators[0] != s.operator.String() || info.ConditionCount != 1 || info.RedeemFeeBps != 250 {
		t.Fatalf("exchange_info = %s (%v)", rec.Body.String(), err)
	}

	// A non-numeric condition id is refused at the route, before dispatch.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/exchange/"+ex+"/condition/one", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid condition id") {
		t.Fatalf("condition/one = %d %s", rec.Code, rec.Body.String())
	}
	// A registered head func is what the dispatch is handed.
	called := false
	r2 := mux.NewRouter()
	RegisterExchangeRoutes(r2, sdb, func() vm.ReadContext { called = true; return vm.ReadContext{Height: 5, Time: 6} })
	rec = httptest.NewRecorder()
	r2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/exchange/"+ex, nil))
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("head func not consulted (code %d)", rec.Code)
	}
}

// A signed exchange.match from anyone but an operator of the instance is
// refused at /tx/submit and never enters the mempool; the operator's own is
// admitted. Local: the executor's operator gate still runs in consensus.
func TestSubmitRefusesNonOperatorMatch(t *testing.T) {
	s := newTestServer(t, "")
	es := newExchangeState(t, s.stateDB)

	opPub, opPriv, _ := qcrypto.GenerateKeypair()
	operator := qcrypto.DeriveAddress(opPub)
	strPub, strPriv, _ := qcrypto.GenerateKeypair()
	stranger := qcrypto.DeriveAddress(strPub)
	for _, a := range []qcrypto.Address{operator, stranger} {
		acc := types.NewAccount(a)
		acc.AddBalance(big.NewInt(1_000_000))
		s.stateDB.SetAccount(acc)
	}
	if err := vm.NewExchange(s.stateDB, es.exchange).SetOperator(es.owner, operator, true, nil); err != nil {
		t.Fatalf("set_operator: %v", err)
	}
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	matchEnv := moduleEnvelope(t, "exchange", "match", map[string]interface{}{"exchange": es.exchange.String()})
	now := time.Now().Unix()

	rec := postSubmit(t, s, types.NewCall(stranger, vm.ModuleRouterAddress, matchEnv, big.NewInt(0), big.NewInt(mempool.MinFee), 0, now), strPriv)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "is not an operator") {
		t.Fatalf("stranger match: status %d body %s, want 400 not-an-operator", rec.Code, rec.Body.String())
	}
	ghostEnv := moduleEnvelope(t, "exchange", "match", map[string]interface{}{"exchange": qcrypto.Address{0x99}.String()})
	rec = postSubmit(t, s, types.NewCall(operator, vm.ModuleRouterAddress, ghostEnv, big.NewInt(0), big.NewInt(mempool.MinFee), 0, now), opPriv)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no exchange is deployed") {
		t.Fatalf("ghost match: status %d body %s, want 400", rec.Code, rec.Body.String())
	}
	if s.pool.Size() != 0 {
		t.Fatalf("a refused match entered the mempool (size %d)", s.pool.Size())
	}

	// The operator's match is admitted (its content is the executor's problem).
	rec = postSubmit(t, s, types.NewCall(operator, vm.ModuleRouterAddress, matchEnv, big.NewInt(0), big.NewInt(mempool.MinFee), 0, now), opPriv)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("operator match: status %d body %s, want 202", rec.Code, rec.Body.String())
	}
	// Other exchange writes from the stranger are not gated by the preflight.
	cancelEnv := moduleEnvelope(t, "exchange", "cancel_order", map[string]interface{}{"exchange": es.exchange.String()})
	rec = postSubmit(t, s, types.NewCall(stranger, vm.ModuleRouterAddress, cancelEnv, big.NewInt(0), big.NewInt(mempool.MinFee), 0, now), strPriv)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("stranger cancel_order: status %d body %s, want 202", rec.Code, rec.Body.String())
	}
	if s.pool.Size() != 2 {
		t.Fatalf("mempool size %d, want 2", s.pool.Size())
	}
}
