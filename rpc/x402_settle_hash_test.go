package rpc

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/x402"
)

// A confirmation names the transaction that actually settled the payment.
//
// The used-nonce path echoed back the hash of the settlement transaction the
// CALLER supplied. A client re-signing a different transaction over the same
// payload — different fee, different account nonce, same payment — arrives with
// a hash of its own that never settled anything, and was told it was confirmed
// under that hash. Anyone auditing the payment against the chain would look for
// a transaction that does not exist.
func TestConfirmedSettlementReportsTheSettledTransaction(t *testing.T) {
	payerPub, payerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	payee := qcrypto.Address{0x55}
	payer := qcrypto.DeriveAddress(payerPub)
	s := newTestServer(t, "secret")

	acc := types.NewAccount(payer)
	acc.AddBalance(big.NewInt(1_000_000))
	acc.PublicKey = payerPub.Bytes()
	s.stateDB.SetAccount(acc)

	var nonce [32]byte
	nonce[0] = 0xE7
	now := time.Now().Unix()
	payload := &x402.PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: payer, To: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 3600, Nonce: nonce,
	}
	if err := payload.Sign(payerPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// What the chain recorded: a specific settlement transaction.
	var settled [32]byte
	for i := range settled {
		settled[i] = byte(0xA0 + i%16)
	}
	if err := s.stateDB.MarkX402NonceSettled(x402.NonceKey(payer, nonce), &types.X402Settlement{
		From: payer, To: payee, Amount: big.NewInt(500),
		ChainID: types.ChainID, Height: 3, TxHash: settled,
	}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if _, err := s.stateDB.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// What the caller presents: a differently-signed transaction over the same
	// payment. Its hash is not the settled one.
	reqs := &x402.PaymentRequirements{
		PayTo: payee, Amount: big.NewInt(500),
		ValidAfter: now - 30, ValidBefore: now + 3600,
	}
	settle := x402.NewSettlementTx(payload, big.NewInt(4321), 9)
	if err := settle.Sign(payerPriv, true); err != nil {
		t.Fatalf("sign settlement: %v", err)
	}
	suppliedHash, err := settle.Hash()
	if err != nil {
		t.Fatalf("hash the supplied settlement: %v", err)
	}
	if suppliedHash == settled {
		t.Fatal("test premise: the supplied transaction must differ from the settled one")
	}

	raw, _ := json.Marshal(map[string]any{"requirements": reqs, "payload": payload, "settlement_tx": settle})
	rec := httptest.NewRecorder()
	s.handleX402Settle(rec, httptest.NewRequest(http.MethodPost, "/x402/settle", bytes.NewReader(raw)))

	var out x402.SettleResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || out.Status != x402.SettleConfirmed {
		t.Fatalf("status %d/%s, want 200/confirmed: %s", rec.Code, out.Status, rec.Body.String())
	}

	wantHex := hexOf(settled[:])
	if out.TxHash != wantHex {
		t.Fatalf("confirmation names tx %s, but the settlement recorded on chain is %s%s",
			out.TxHash, wantHex, suppliedHint(out.TxHash, hexOf(suppliedHash[:])))
	}
}

func suppliedHint(got, supplied string) string {
	if got == supplied {
		return " — it echoed the caller's own transaction, which never settled"
	}
	return ""
}
