package rpc

import (
	"encoding/json"
	"fmt"
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

func hex32(b [32]byte) string { return fmt.Sprintf("%x", b[:]) }

func amountField(v *big.Int, asString bool) interface{} {
	if asString {
		return amountJSON(v)
	}
	return v
}

func publicLogs(logs []*types.Log) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(logs))
	for _, lg := range logs {
		topics := make([]string, len(lg.Topics))
		for i, t := range lg.Topics {
			topics[i] = hex32(t)
		}
		item := map[string]interface{}{
			"address":      lg.Address.String(),
			"topics":       topics,
			"block_height": lg.BlockHeight,
			"tx_hash":      hex32(lg.TxHash),
			"tx_index":     lg.TxIndex,
			"log_index":    lg.LogIndex,
		}
		if len(lg.Data) > 0 {
			item["data"] = fmt.Sprintf("%x", lg.Data)
		}
		out = append(out, item)
	}
	return out
}

func publicReceipt(rc *types.Receipt, stringAmounts bool) map[string]interface{} {
	out := map[string]interface{}{
		"tx_hash":      hex32(rc.TxHash),
		"tx_index":     rc.TxIndex,
		"block_height": rc.BlockHeight,
		"block_hash":   hex32(rc.BlockHash),
		"status":       rc.Status,
		"gas_used":     rc.GasUsed,
		"from":         rc.From.String(),
		"to":           rc.To.String(),
		"amount":       amountField(rc.Amount, stringAmounts),
		"fee":          amountField(rc.Fee, stringAmounts),
		"type":         rc.Type,
		"logs":         publicLogs(rc.Logs),
	}
	if rc.Error != "" {
		out["error"] = rc.Error
	}
	if rc.ContractAddress != nil {
		out["contract_address"] = rc.ContractAddress.String()
	}
	return out
}

func publicTx(tx *types.Transaction, stringAmounts bool) map[string]interface{} {
	out := map[string]interface{}{
		"version":   tx.Version,
		"chain_id":  tx.ChainID,
		"type":      tx.Type,
		"alg_id":    tx.AlgID,
		"from":      tx.From.String(),
		"to":        tx.To.String(),
		"amount":    amountField(tx.Amount, stringAmounts),
		"fee":       amountField(tx.Fee, stringAmounts),
		"nonce":     tx.Nonce,
		"timestamp": tx.Timestamp,
	}
	if len(tx.Data) > 0 {
		out["data"] = fmt.Sprintf("%x", tx.Data)
	}
	return out
}

// receiptResponse and blockResponse pick the encoding the request asked for.
func receiptResponse(r *http.Request, rc *types.Receipt) interface{} {
	return publicReceipt(rc, wantStringAmounts(r))
}

func blockResponse(r *http.Request, b *types.Block) interface{} {
	if wantStringAmounts(r) {
		return blockView(b)
	}
	return b
}
