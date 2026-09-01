package types

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// 12345678901234567891 > 2^53: a JS client that parses this as a Number rounds
// it. The value the wallet displays and signs must be the value on chain, so the
// wire accepts the same integer as a decimal string — while the canonical
// (signed) encoding stays numeric so no existing signature or block changes.
const bigAmount = "12345678901234567891"

func bigFromString(t *testing.T, s string) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad test constant %q", s)
	}
	return v
}

func sampleTransfer(t *testing.T) *Transaction {
	t.Helper()
	var from, to qcrypto.Address
	from[0], to[0] = 0x11, 0x22
	return NewTransfer(from, to, bigFromString(t, bigAmount), big.NewInt(1000), 7, 1_700_000_000)
}

// quoteAmounts rewrites the numeric amount/fee of a marshalled tx as strings.
func quoteAmounts(t *testing.T, raw []byte) []byte {
	t.Helper()
	out := bytes.Replace(raw, []byte(`"amount":`+bigAmount+`,`), []byte(`"amount":"`+bigAmount+`",`), 1)
	out = bytes.Replace(out, []byte(`"fee":1000,`), []byte(`"fee":"1000",`), 1)
	if bytes.Equal(out, raw) {
		t.Fatalf("test setup: amounts were not numeric in %s", raw)
	}
	return out
}

func TestTransactionMarshalKeepsNumericAmounts(t *testing.T) {
	raw, err := json.Marshal(sampleTransfer(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"amount":`+bigAmount+`,`)) || !bytes.Contains(raw, []byte(`"fee":1000,`)) {
		t.Fatalf("canonical encoding changed (amounts must stay JSON numbers): %s", raw)
	}
}

func TestTransactionUnmarshalAcceptsStringAmountAndFee(t *testing.T) {
	want := sampleTransfer(t)
	raw, _ := json.Marshal(want)

	var got Transaction
	if err := json.Unmarshal(quoteAmounts(t, raw), &got); err != nil {
		t.Fatalf("string amounts rejected: %v", err)
	}
	if got.Amount == nil || got.Amount.Cmp(want.Amount) != 0 {
		t.Fatalf("amount: got %v, want %s", got.Amount, bigAmount)
	}
	if got.Fee == nil || got.Fee.Cmp(want.Fee) != 0 {
		t.Fatalf("fee: got %v, want 1000", got.Fee)
	}
	if got.Version != want.Version || got.ChainID != want.ChainID || got.Type != want.Type ||
		got.AlgID != want.AlgID || got.From != want.From || got.To != want.To ||
		got.Nonce != want.Nonce || got.Timestamp != want.Timestamp {
		t.Fatalf("other fields did not survive decoding:\n got %+v\nwant %+v", got, *want)
	}
	hw, _ := want.Hash()
	hg, _ := got.Hash()
	if hw != hg {
		t.Fatalf("signing hash differs between numeric and string forms")
	}
}

func TestTransactionUnmarshalStillAcceptsNumericAmounts(t *testing.T) {
	want := sampleTransfer(t)
	raw, _ := json.Marshal(want)
	var got Transaction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("numeric amounts rejected: %v", err)
	}
	if got.Amount.Cmp(want.Amount) != 0 || got.Fee.Cmp(want.Fee) != 0 {
		t.Fatalf("numeric decode changed values: %v %v", got.Amount, got.Fee)
	}
	// null keeps meaning "absent" (Validate rejects it later), not an error.
	nulled := bytes.Replace(raw, []byte(`"amount":`+bigAmount+`,`), []byte(`"amount":null,`), 1)
	var withNull Transaction
	if err := json.Unmarshal(nulled, &withNull); err != nil {
		t.Fatalf("null amount should decode: %v", err)
	}
	if withNull.Amount != nil {
		t.Fatalf("null amount decoded to %v, want nil", withNull.Amount)
	}
}

func TestStringAmountTransactionVerifiesSignerSignature(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	tx := sampleTransfer(t)
	tx.From = qcrypto.DeriveAddress(pub)
	if err := tx.Sign(priv, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(tx)

	var got Transaction
	if err := json.Unmarshal(quoteAmounts(t, raw), &got); err != nil {
		t.Fatal(err)
	}
	if err := got.VerifySignature(pub.Bytes()); err != nil {
		t.Fatalf("a tx submitted with string amounts must verify against the signer's numeric hash: %v", err)
	}
	f1, _ := tx.FullHash()
	f2, _ := got.FullHash()
	if f1 != f2 {
		t.Fatalf("tx id changed by the wire encoding of amounts")
	}
}

func TestTransactionUnmarshalRejectsMalformedAmountStrings(t *testing.T) {
	base, _ := json.Marshal(sampleTransfer(t))
	for _, bad := range []string{`"1.5"`, `"-1"`, `"0x10"`, `"1e3"`, `""`, `" 12"`, `"12 "`, `"12_000"`, `"+1"`, `"abc"`, `true`, `[]`, `{}`} {
		raw := bytes.Replace(base, []byte(`"amount":`+bigAmount+`,`), []byte(`"amount":`+bad+`,`), 1)
		var tx Transaction
		if err := json.Unmarshal(raw, &tx); err == nil {
			t.Errorf("amount %s was accepted", bad)
		}
	}
}
