package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
)

// DecodeWireAmount decodes a JSON integer that a client may send either as a
// bare number or as a decimal string.
//
// Amounts are in smallest units (1 SPLD = 1e18), so almost every real value is
// above 2^53, the largest integer JavaScript can hold in a Number. A browser or
// Node signer that builds its request with JSON.stringify would round the value
// it is about to submit; sending "12345678901234567891" instead is exact. The
// canonical body that is hashed and signed keeps the bare-number encoding, so
// accepting the string form changes no signature, tx id, block or stored byte.
//
// The string form is strict: base-10 digits only. No sign, whitespace,
// exponent, hex prefix or separators — anything else is a malformed amount, not
// something to guess at. Numbers keep encoding/json's existing behaviour.
// A JSON null yields a nil *big.Int, as before.
func DecodeWireAmount(field string, raw json.RawMessage) (*big.Int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		if !isDecimalDigits(s) {
			return nil, fmt.Errorf("%s: %q is not a decimal integer string", field, s)
		}
		v, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("%s: %q is not a decimal integer string", field, s)
		}
		return v, nil
	}
	var v big.Int
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return &v, nil
}

func isDecimalDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// UnmarshalJSON accepts amount and fee as either JSON numbers or decimal
// strings (see DecodeWireAmount). Every other field decodes exactly as the
// struct tags say. Marshalling is untouched: the numeric form is canonical.
func (tx *Transaction) UnmarshalJSON(data []byte) error {
	type plain Transaction // same fields, no methods: no recursion into this one
	aux := struct {
		*plain
		Amount json.RawMessage `json:"amount"`
		Fee    json.RawMessage `json:"fee"`
	}{plain: (*plain)(tx)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Amount != nil {
		v, err := DecodeWireAmount("amount", aux.Amount)
		if err != nil {
			return err
		}
		tx.Amount = v
	}
	if aux.Fee != nil {
		v, err := DecodeWireAmount("fee", aux.Fee)
		if err != nil {
			return err
		}
		tx.Fee = v
	}
	return nil
}
