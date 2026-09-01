package x402

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// x402 payloads are built in browsers: the amount must be accepted as a decimal
// string, and the signed canonical body must not change when it is.
func TestPaymentPayloadAcceptsStringAmount(t *testing.T) {
	var nonce [32]byte
	nonce[0] = 0xAA
	want := &PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: qcrypto.Address{0x11}, To: qcrypto.Address{0x22},
		Amount: big.NewInt(500), ValidAfter: 1, ValidBefore: 2, Nonce: nonce,
	}
	raw, _ := json.Marshal(want)
	if !bytes.Contains(raw, []byte(`"amount":500,`)) {
		t.Fatalf("canonical payload encoding must stay numeric: %s", raw)
	}
	quoted := bytes.Replace(raw, []byte(`"amount":500,`), []byte(`"amount":"500",`), 1)

	var got PaymentPayload
	if err := json.Unmarshal(quoted, &got); err != nil {
		t.Fatalf("string amount rejected: %v", err)
	}
	if got.Amount == nil || got.Amount.Cmp(want.Amount) != 0 {
		t.Fatalf("amount: got %v", got.Amount)
	}
	if got.Nonce != want.Nonce || got.From != want.From || got.To != want.To || got.ChainID != want.ChainID {
		t.Fatalf("other fields lost: %+v", got)
	}
	hw, _ := want.Hash()
	hg, _ := got.Hash()
	if hw != hg {
		t.Fatalf("payload hash differs between numeric and string amount")
	}
}

func TestPaymentRequirementsAcceptStringAmount(t *testing.T) {
	want := &PaymentRequirements{PayTo: qcrypto.Address{0x22}, Amount: big.NewInt(500), ValidAfter: 1, ValidBefore: 2}
	raw, _ := json.Marshal(want)
	quoted := bytes.Replace(raw, []byte(`"amount":500,`), []byte(`"amount":"500",`), 1)
	if bytes.Equal(raw, quoted) {
		t.Fatalf("test setup: %s", raw)
	}
	var got PaymentRequirements
	if err := json.Unmarshal(quoted, &got); err != nil {
		t.Fatalf("string amount rejected: %v", err)
	}
	if got.Amount == nil || got.Amount.Cmp(want.Amount) != 0 || got.PayTo != want.PayTo {
		t.Fatalf("decoded %+v", got)
	}
	var bad PaymentRequirements
	if err := json.Unmarshal(bytes.Replace(raw, []byte(`"amount":500,`), []byte(`"amount":"5.0",`), 1), &bad); err == nil {
		t.Fatalf("non-integer amount string accepted")
	}
}
