package main

import (
	"encoding/json"
	"testing"
)

func TestClassifyReceiptStatus(t *testing.T) {
	cases := []struct {
		name string
		r    map[string]any
		want string
	}{
		{name: "nil is pending", r: nil, want: receiptStatusPending},
		{name: "empty is pending", r: map[string]any{}, want: receiptStatusPending},
		{name: "missing status is pending", r: map[string]any{"tx_hash": "ab"}, want: receiptStatusPending},
		{name: "nil status is pending", r: map[string]any{"status": nil}, want: receiptStatusPending},
		{name: "error without status is rejected", r: map[string]any{"error": "rejected: fee too low"}, want: receiptStatusRejected},
		{name: "empty error without status is pending", r: map[string]any{"error": ""}, want: receiptStatusPending},
		{name: "json number success", r: map[string]any{"status": float64(1)}, want: receiptStatusSuccess},
		{name: "json number failed", r: map[string]any{"status": float64(0), "error": "module revert"}, want: receiptStatusExecutionFailed},
		{name: "int success", r: map[string]any{"status": 1}, want: receiptStatusSuccess},
		{name: "json.Number success", r: map[string]any{"status": json.Number("1")}, want: receiptStatusSuccess},
		{name: "string success", r: map[string]any{"status": "1"}, want: receiptStatusSuccess},
		{name: "unknown numeric status is rejected", r: map[string]any{"status": float64(2)}, want: receiptStatusRejected},
		{name: "non-numeric status is rejected", r: map[string]any{"status": "nope"}, want: receiptStatusRejected},
		{name: "fractional status is rejected", r: map[string]any{"status": 0.5}, want: receiptStatusRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyReceiptStatus(tc.r); got != tc.want {
				t.Fatalf("classifyReceiptStatus(%v) = %q, want %q", tc.r, got, tc.want)
			}
		})
	}
}
