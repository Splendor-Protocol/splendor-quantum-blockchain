package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// 12345678901234567891 > 2^53: JSON.parse rounds it. Read routes keep emitting
// amounts as JSON numbers by default — peers sync blocks from these routes into
// *big.Int fields and an older node cannot decode a quoted amount — but a client
// that asks for ?amounts=string gets amount/fee as decimal strings, exact in JS.
const hugeAmount = "12345678901234567891"

func hugeBig(t *testing.T) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(hugeAmount, 10)
	if !ok {
		t.Fatal("bad constant")
	}
	return v
}

func hugeTransfer(t *testing.T) *types.Transaction {
	t.Helper()
	var from, to qcrypto.Address
	from[0], to[0] = 0x11, 0x22
	tx := types.NewTransfer(from, to, hugeBig(t), big.NewInt(1000), 0, 1_700_000_000)
	tx.Signature = make([]byte, qcrypto.SignatureSize) // shape only; nothing verifies it here
	return tx
}

func TestReceiptAndTxRoutesRenderAmountsAsStringsOnRequest(t *testing.T) {
	store, err := state.NewTxIndexStore(t.TempDir())
	if err != nil {
		t.Fatalf("txindex: %v", err)
	}
	defer store.Close()

	tx := hugeTransfer(t)
	hash, _ := tx.FullHash()
	blk := &types.Block{Transactions: []*types.Transaction{tx}}
	blk.Header.Height = 5
	if err := store.IndexBlock(blk); err != nil {
		t.Fatalf("index: %v", err)
	}
	rc := &types.Receipt{
		TxHash: hash, BlockHeight: 5, Status: types.TxStatusSuccess,
		From: tx.From, To: tx.To, Amount: hugeBig(t), Fee: big.NewInt(1000),
	}
	if err := store.StoreReceipt(rc); err != nil {
		t.Fatalf("store receipt: %v", err)
	}

	get := func(h http.HandlerFunc, path string) []byte {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = mux.SetURLVars(req, map[string]string{"hash": fmt.Sprintf("%x", hash[:])})
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}

	receipt := makeReceiptHandler(store)
	if body := get(receipt, "/tx/x/receipt"); !bytes.Contains(body, []byte(`"amount":`+hugeAmount+`,`)) {
		t.Fatalf("default receipt encoding must stay numeric: %s", body)
	}
	body := get(receipt, "/tx/x/receipt?amounts=string")
	if !bytes.Contains(body, []byte(`"amount":"`+hugeAmount+`"`)) || !bytes.Contains(body, []byte(`"fee":"1000"`)) {
		t.Fatalf("?amounts=string receipt: %s", body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["block_height"] != float64(5) || decoded["status"] != float64(1) || decoded["tx_hash"] == nil || decoded["from"] == nil {
		t.Fatalf("receipt fields lost in the string view: %s", body)
	}

	txh := makeTxHandler(store)
	if body := get(txh, "/tx/x"); !bytes.Contains(body, []byte(`"amount":`+hugeAmount+`,`)) {
		t.Fatalf("default tx encoding must stay numeric: %s", body)
	}
	if body := get(txh, "/tx/x?amounts=string"); !bytes.Contains(body, []byte(`"amount":"`+hugeAmount+`"`)) || !bytes.Contains(body, []byte(`"block_height":5`)) {
		t.Fatalf("?amounts=string tx: %s", body)
	}
}

func TestBlockResponseRendersTxAmountsAsStringsOnRequest(t *testing.T) {
	tx := hugeTransfer(t)
	blk := &types.Block{Transactions: []*types.Transaction{tx, {Amount: nil, Fee: big.NewInt(1)}}}
	blk.Header.Height = 5

	plain := httptest.NewRequest(http.MethodGet, "/block/5", nil)
	raw, err := json.Marshal(blockResponse(plain, blk))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"amount":`+hugeAmount+`,`)) {
		t.Fatalf("default block encoding must stay numeric: %s", raw)
	}

	quoted := httptest.NewRequest(http.MethodGet, "/block/5?amounts=string", nil)
	raw, err = json.Marshal(blockResponse(quoted, blk))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"amount":"`+hugeAmount+`"`)) || !bytes.Contains(raw, []byte(`"fee":"1000"`)) {
		t.Fatalf("?amounts=string block: %s", raw)
	}
	// A missing amount stays null rather than becoming "0".
	if !bytes.Contains(raw, []byte(`"amount":null`)) || !bytes.Contains(raw, []byte(`"fee":"1"`)) {
		t.Fatalf("nil amount should render as null: %s", raw)
	}
	var decoded struct {
		Header       map[string]any   `json:"header"`
		Hash         any              `json:"hash"`
		Transactions []map[string]any `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Header["height"] != float64(5) || decoded.Hash == nil || len(decoded.Transactions) != 2 {
		t.Fatalf("block fields lost in the string view: %s", raw)
	}
	if decoded.Transactions[0]["from"] == nil || decoded.Transactions[0]["nonce"] != float64(0) || decoded.Transactions[0]["signature"] == nil {
		t.Fatalf("tx fields lost in the string view: %s", raw)
	}
	// A string view fed back through the ingress normaliser decodes exactly
	// (core/types itself stays numeric-only: it is compiled into the signer).
	normalized, err := normalizeWireAmounts(raw)
	if err != nil {
		t.Fatal(err)
	}
	var back types.Block
	if err := json.Unmarshal(normalized, &back); err != nil {
		t.Fatalf("string view does not round-trip through the ingress: %v", err)
	}
	if back.Transactions[0].Amount.Cmp(hugeBig(t)) != 0 || back.Transactions[1].Amount != nil {
		t.Fatalf("round trip changed the amounts: %v %v", back.Transactions[0].Amount, back.Transactions[1].Amount)
	}
}
