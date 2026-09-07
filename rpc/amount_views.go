package rpc

import (
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Amounts are integers in smallest units (1 SPLD = 1e18), so nearly every real
// value is above 2^53 — the largest integer a JavaScript Number holds. A browser
// or Node client that JSON.parses a receipt, tx or block therefore rounds the
// amount it displays.
//
// The read routes still emit amounts as JSON numbers BY DEFAULT: peers sync
// blocks from /block/* into *big.Int fields, and a node that predates this
// change cannot decode a quoted amount. A client that asks for
// ?amounts=string gets the same document with amount/fee rendered as decimal
// strings (null stays null). Nothing about the canonical encoding that is
// hashed, signed or stored changes; this is a presentation of the same bytes.

// wantStringAmounts reports whether the client opted into string amounts.
func wantStringAmounts(r *http.Request) bool {
	return r.URL.Query().Get("amounts") == "string"
}

// amountJSON renders a big integer as a quoted decimal, or null when absent.
func amountJSON(v *big.Int) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(`"` + v.String() + `"`)
}

// The view types embed a method-less alias of the original so every other
// field keeps its tag and encoding; the shadowing Amount/Fee at depth 0 win
// over the embedded numeric ones (encoding/json's dominance rule).

type txAlias types.Transaction

type txStringView struct {
	*txAlias
	Amount json.RawMessage `json:"amount"`
	Fee    json.RawMessage `json:"fee"`
}

func txView(tx *types.Transaction) txStringView {
	return txStringView{txAlias: (*txAlias)(tx), Amount: amountJSON(tx.Amount), Fee: amountJSON(tx.Fee)}
}

type receiptAlias types.Receipt

type receiptStringView struct {
	*receiptAlias
	Amount json.RawMessage `json:"amount"`
	Fee    json.RawMessage `json:"fee"`
}

func receiptView(rc *types.Receipt) receiptStringView {
	return receiptStringView{receiptAlias: (*receiptAlias)(rc), Amount: amountJSON(rc.Amount), Fee: amountJSON(rc.Fee)}
}

type blockAlias types.Block

type blockStringView struct {
	*blockAlias
	Transactions []txStringView `json:"transactions"`
}

func blockView(b *types.Block) blockStringView {
	txs := make([]txStringView, len(b.Transactions))
	for i, tx := range b.Transactions {
		txs[i] = txView(tx)
	}
	return blockStringView{blockAlias: (*blockAlias)(b), Transactions: txs}
}

// receiptResponse and blockResponse pick the encoding the request asked for.
func receiptResponse(r *http.Request, rc *types.Receipt) interface{} {
	if wantStringAmounts(r) {
		return receiptView(rc)
	}
	return rc
}

func blockResponse(r *http.Request, b *types.Block) interface{} {
	if wantStringAmounts(r) {
		return blockView(b)
	}
	return b
}
