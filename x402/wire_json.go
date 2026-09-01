package x402

import (
	"encoding/json"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Payment amounts arrive from browsers, where an integer above 2^53 cannot be
// expressed as a JSON number without rounding. Both x402 bodies therefore
// accept "amount" as a decimal string as well as a number
// (types.DecodeWireAmount). The signed canonical payload body keeps the numeric
// encoding, so a string-submitted payment hashes and verifies exactly as the
// payer signed it. Marshalling is unchanged.

// UnmarshalJSON accepts Amount as a JSON number or a decimal string.
func (p *PaymentPayload) UnmarshalJSON(data []byte) error {
	type plain PaymentPayload
	aux := struct {
		*plain
		Amount json.RawMessage `json:"amount"`
	}{plain: (*plain)(p)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Amount != nil {
		v, err := types.DecodeWireAmount("amount", aux.Amount)
		if err != nil {
			return err
		}
		p.Amount = v
	}
	return nil
}

// UnmarshalJSON accepts Amount as a JSON number or a decimal string.
func (r *PaymentRequirements) UnmarshalJSON(data []byte) error {
	type plain PaymentRequirements
	aux := struct {
		*plain
		Amount json.RawMessage `json:"amount"`
	}{plain: (*plain)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Amount != nil {
		v, err := types.DecodeWireAmount("amount", aux.Amount)
		if err != nil {
			return err
		}
		r.Amount = v
	}
	return nil
}
