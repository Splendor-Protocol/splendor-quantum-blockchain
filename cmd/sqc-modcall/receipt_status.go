package main

import (
	"encoding/json"
	"strconv"
)

const (
	receiptStatusSuccess         = "success"
	receiptStatusExecutionFailed = "execution_failed"
	receiptStatusPending         = "pending"
	receiptStatusRejected        = "rejected"
)

// classifyReceiptStatus maps a /tx/{hash}/receipt JSON object to an operator
// label. Default waitReceipt still prints any receipt and exits 0; this helper
// is what --require-success uses to decide whether to exit 1.
func classifyReceiptStatus(r map[string]any) string {
	if len(r) == 0 {
		return receiptStatusPending
	}
	raw, ok := r["status"]
	if !ok || raw == nil {
		if errText, hasErr := r["error"]; hasErr && errText != nil && errText != "" {
			return receiptStatusRejected
		}
		return receiptStatusPending
	}
	n, ok := receiptStatusNumber(raw)
	if !ok {
		return receiptStatusRejected
	}
	switch n {
	case 1:
		return receiptStatusSuccess
	case 0:
		return receiptStatusExecutionFailed
	default:
		return receiptStatusRejected
	}
}

func receiptStatusNumber(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
