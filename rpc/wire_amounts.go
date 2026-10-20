package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Amounts are integers in smallest units (1 SPLD = 1e18), so nearly every real
// value is above 2^53 — the largest integer a JavaScript Number holds. A browser
// or Node client that builds a request with JSON.stringify would round the
// amount it is about to submit; sending "12345678901234567891" is exact.
//
// The lenient decoding lives here, at the RPC ingress, and nowhere deeper:
// core/types is compiled into the pinned signer WASM (sdk/sqc-sdk/wasm), so a
// method added there changes a binary that two other repos pin by digest. The
// ingress rewrites quoted decimal strings under the keys below into the bare
// numbers the core types already decode. The canonical body that is hashed and
// signed keeps its numeric encoding, so what a client signs never changes.
//
// The string form is strict: base-10 digits only. No sign, whitespace, exponent,
// hex prefix or separators — anything else is a malformed amount, not something
// to guess at. Numbers, null and every other key pass through untouched, and a
// body with nothing to rewrite is returned byte for byte.
var wireAmountKeys = map[string]bool{"amount": true, "fee": true}

func normalizeWireAmounts(body []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("invalid character after top-level value")
	}
	changed, err := normalizeAmountsIn(v)
	if err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(v)
}

// decodeWireJSON reads a (size-bounded) request body, normalises amounts and
// decodes it into dst. Bodies with no string amounts decode exactly as before.
func decodeWireJSON(r io.Reader, dst interface{}) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	body, err = normalizeWireAmounts(body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, dst)
}

func normalizeAmountsIn(v interface{}) (bool, error) {
	changed := false
	switch node := v.(type) {
	case map[string]interface{}:
		for k, val := range node {
			if s, ok := val.(string); ok && wireAmountKeys[k] {
				if !isDecimalDigits(s) {
					return false, fmt.Errorf("%s: %q is not a decimal integer string", k, s)
				}
				node[k] = json.Number(s)
				changed = true
				continue
			}
			c, err := normalizeAmountsIn(val)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
	case []interface{}:
		for _, val := range node {
			c, err := normalizeAmountsIn(val)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
	}
	return changed, nil
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
