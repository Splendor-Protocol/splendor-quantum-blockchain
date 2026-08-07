package signer

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestDeriveFromSeedStable(t *testing.T) {
	var seed [SeedSize]byte
	for i := range seed {
		seed[i] = byte(i + 1)
	}

	w1, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatalf("derive 1: %v", err)
	}
	w2, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatalf("derive 2: %v", err)
	}

	if w1.Address != w2.Address {
		t.Fatalf("address not stable: %s vs %s", w1.Address, w2.Address)
	}
	if string(w1.PublicKey) != string(w2.PublicKey) {
		t.Fatal("public key not stable")
	}
	addr := w1.Address.String()
	if len(addr) != 3+64 || addr[:3] != "SQ:" {
		t.Fatalf("unexpected address format: %s", addr)
	}

	// Address must equal DeriveAddress(pubkey)
	derived := qcrypto.DeriveAddress(w1.Public)
	if derived != w1.Address {
		t.Fatal("address does not match DeriveAddress(pubkey)")
	}
}

func TestSeedFromEVMSignature(t *testing.T) {
	sig := make([]byte, 65)
	for i := range sig {
		sig[i] = byte(i)
	}
	s1 := SeedFromEVMSignature(sig)
	s2 := SeedFromEVMSignature(sig)
	if s1 != s2 {
		t.Fatal("seed not deterministic")
	}
	w, err := DeriveFromEVMSignature(sig)
	if err != nil {
		t.Fatal(err)
	}
	if w.Address.IsZero() {
		t.Fatal("zero address")
	}
}

func TestDerivedWalletZeroize(t *testing.T) {
	var seed [SeedSize]byte
	for i := range seed {
		seed[i] = byte(i + 7)
	}
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	if w.Private == nil {
		t.Fatal("expected private key")
	}
	w.Zeroize()
	if w.Private != nil {
		t.Fatal("private key pointer should be nil after Zeroize")
	}
	if w.PublicKey != nil {
		t.Fatal("public key bytes should be nil after Zeroize")
	}
	if !w.Address.IsZero() {
		t.Fatal("address should be zero after Zeroize")
	}
}

func TestSignTransferVerifyAndRoundTrip(t *testing.T) {
	var seed [SeedSize]byte
	copy(seed[:], []byte("splendor-bridge-test-seed-012345")) // 32 bytes

	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}

	// Recipient: another deterministic wallet
	var seed2 [SeedSize]byte
	copy(seed2[:], []byte("splendor-bridge-recv-seed-012345"))
	recv, err := DeriveFromSeed(seed2)
	if err != nil {
		t.Fatal(err)
	}

	wire, err := w.SignTransfer(TransferParams{
		To:        recv.Address.String(),
		Amount:    "1000000000000000000",
		Fee:       "1000",
		Nonce:     0,
		Timestamp: 1700000000,
		Memo:      "test",
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	var tx types.Transaction
	if err := json.Unmarshal(wire, &tx); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}

	if tx.From != w.Address {
		t.Fatalf("from mismatch")
	}
	if tx.To != recv.Address {
		t.Fatalf("to mismatch")
	}
	if len(tx.PublicKey) != qcrypto.PublicKeySize {
		t.Fatalf("expected pubkey on nonce 0, got %d bytes", len(tx.PublicKey))
	}
	if len(tx.Signature) != qcrypto.SignatureSize {
		t.Fatalf("bad sig len %d", len(tx.Signature))
	}

	if err := tx.VerifySignature(w.PublicKey); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	// Remarshal and verify again (JSON stability for hash)
	wire2, err := json.Marshal(&tx)
	if err != nil {
		t.Fatal(err)
	}
	var tx2 types.Transaction
	if err := json.Unmarshal(wire2, &tx2); err != nil {
		t.Fatal(err)
	}
	if err := tx2.VerifySignature(w.PublicKey); err != nil {
		t.Fatalf("verify after remashal failed: %v", err)
	}
}

func TestSignTransferSubsequentNoPubkey(t *testing.T) {
	var seed [SeedSize]byte
	for i := range seed {
		seed[i] = byte(0xAB)
	}
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	to := w.Address // self-send ok for structure

	wire, err := w.SignTransfer(TransferParams{
		To:     to.String(),
		Amount: "1",
		Fee:    "1000",
		Nonce:  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	var tx types.Transaction
	if err := json.Unmarshal(wire, &tx); err != nil {
		t.Fatal(err)
	}
	if len(tx.PublicKey) != 0 {
		t.Fatalf("nonce>0 should omit pubkey, got %d bytes", len(tx.PublicKey))
	}
	if err := tx.VerifySignature(w.PublicKey); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSignX402(t *testing.T) {
	var seed [SeedSize]byte
	copy(seed[:], []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"))
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	nonceHex := hex.EncodeToString(make([]byte, 32))
	wire, err := w.SignX402(X402Params{
		To:          w.Address.String(),
		Amount:      "1000",
		ValidAfter:  1,
		ValidBefore: 9999999999,
		NonceHex:    nonceHex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) < 100 {
		t.Fatalf("payload too short: %s", wire)
	}
}

func TestDomainMessageConstant(t *testing.T) {
	const want = "Splendor Quantum Chain\nKey derivation v1\nchain: splendor-quantum-1"
	if DerivationDomainMessage != want {
		t.Fatalf("domain message mismatch:\n got %q\nwant %q", DerivationDomainMessage, want)
	}
}

func TestSignCallAndDeploy(t *testing.T) {
	var seed [SeedSize]byte
	for i := range seed {
		seed[i] = byte(0x42)
	}
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}

	callWire, err := w.SignCall(CallParams{
		Contract: w.Address.String(),
		Calldata: []byte{0xde, 0xad},
		Value:    "0",
		Fee:      "1000",
		Nonce:    0,
	})
	if err != nil {
		t.Fatalf("SignCall: %v", err)
	}
	var callTx types.Transaction
	if err := json.Unmarshal(callWire, &callTx); err != nil {
		t.Fatal(err)
	}
	if callTx.Type != types.TxCall {
		t.Fatalf("want TxCall, got %v", callTx.Type)
	}
	if err := callTx.VerifySignature(w.PublicKey); err != nil {
		t.Fatalf("call verify: %v", err)
	}

	deployWire, err := w.SignDeploy(DeployParams{
		Bytecode: []byte{0x01, 0x02, 0x03},
		Fee:      "1000",
		Nonce:    1,
	})
	if err != nil {
		t.Fatalf("SignDeploy: %v", err)
	}
	var deployTx types.Transaction
	if err := json.Unmarshal(deployWire, &deployTx); err != nil {
		t.Fatal(err)
	}
	if deployTx.Type != types.TxDeploy {
		t.Fatalf("want TxDeploy, got %v", deployTx.Type)
	}
	if len(deployTx.PublicKey) != 0 {
		t.Fatal("nonce 1 should omit pubkey")
	}
	if err := deployTx.VerifySignature(w.PublicKey); err != nil {
		t.Fatalf("deploy verify: %v", err)
	}
}

func TestInvalidSeedAndAddress(t *testing.T) {
	if _, err := SeedFromHex("dead"); err == nil {
		t.Fatal("expected short seed error")
	}
	if _, err := SeedFromHex("zz"); err == nil {
		t.Fatal("expected bad hex error")
	}

	var seed [SeedSize]byte
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.SignTransfer(TransferParams{
		To:     "not-an-address",
		Amount: "1",
		Fee:    "1000",
		Nonce:  0,
	})
	if err == nil {
		t.Fatal("expected invalid address error")
	}
}

func TestX402Deterministic(t *testing.T) {
	var seed [SeedSize]byte
	copy(seed[:], []byte("yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"))
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	nonceHex := hex.EncodeToString(make([]byte, 32))
	p := X402Params{
		To:          w.Address.String(),
		Amount:      "5000",
		ValidAfter:  100,
		ValidBefore: 200,
		NonceHex:    nonceHex,
	}
	a, err := w.SignX402(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.SignX402(p)
	if err != nil {
		t.Fatal(err)
	}
	// Dilithium signatures are randomized — payloads differ in signature bytes,
	// but signed bodies (everything except signature) must match.
	var pa, pb map[string]interface{}
	if err := json.Unmarshal(a, &pa); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pb); err != nil {
		t.Fatal(err)
	}
	delete(pa, "signature")
	delete(pb, "signature")
	ja, _ := json.Marshal(pa)
	jb, _ := json.Marshal(pb)
	if string(ja) != string(jb) {
		t.Fatalf("x402 bodies differ:\n%s\n%s", ja, jb)
	}
	if pa["amount"] == nil || pa["from"] == nil {
		t.Fatal("missing fields")
	}
}

func TestSignTransferRejectsFeeBelowMin(t *testing.T) {
	var seed [SeedSize]byte
	for i := range seed {
		seed[i] = 1
	}
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.SignTransfer(TransferParams{
		To:     w.Address.String(),
		Amount: "1",
		Fee:    "999",
		Nonce:  0,
	})
	if err == nil {
		t.Fatal("expected fee below minimum error")
	}
	if !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTamperedTransferFailsVerify(t *testing.T) {
	var seed [SeedSize]byte
	copy(seed[:], []byte("tamper-bridge-test-seed-01234567"))
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := w.SignTransfer(TransferParams{
		To:        w.Address.String(),
		Amount:    "1000",
		Fee:       "1000",
		Nonce:     0,
		Timestamp: 1700000001,
	})
	if err != nil {
		t.Fatal(err)
	}

	unmarshal := func() types.Transaction {
		var tx types.Transaction
		if err := json.Unmarshal(wire, &tx); err != nil {
			t.Fatal(err)
		}
		return tx
	}

	tx := unmarshal()
	if err := tx.VerifySignature(w.PublicKey); err != nil {
		t.Fatalf("baseline verify: %v", err)
	}

	tx = unmarshal()
	tx.Amount.SetInt64(999999)
	if err := tx.VerifySignature(w.PublicKey); err == nil {
		t.Fatal("expected verify fail after amount tamper")
	}

	tx = unmarshal()
	tx.Nonce = 99
	if err := tx.VerifySignature(w.PublicKey); err == nil {
		t.Fatal("expected verify fail after nonce tamper")
	}

	tx = unmarshal()
	tx.Signature = tx.Signature[:len(tx.Signature)/2]
	if err := tx.VerifySignature(w.PublicKey); err == nil {
		t.Fatal("expected verify fail on truncated signature")
	}

	tx = unmarshal()
	shortPub := tx.PublicKey[:8]
	if err := tx.VerifySignature(shortPub); err == nil {
		t.Fatal("expected verify fail on truncated pubkey")
	}
}

func TestInvalidAmountStrings(t *testing.T) {
	var seed [SeedSize]byte
	for i := range seed {
		seed[i] = 9
	}
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{"", "-1", "1.5", "abc", "0x10"}
	for _, amount := range cases {
		_, err := w.SignTransfer(TransferParams{
			To:     w.Address.String(),
			Amount: amount,
			Fee:    "1000",
			Nonce:  0,
		})
		if err == nil {
			t.Fatalf("expected error for amount %q", amount)
		}
	}
}

func TestGoldenSeedAddressMatchesTS(t *testing.T) {
	// Must match testdata/golden-vectors.json and wasm-session Vitest golden.
	seedHex := "0101010101010101010101010101010101010101010101010101010101010101"
	seed, err := SeedFromHex(seedHex)
	if err != nil {
		t.Fatal(err)
	}
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	want := "SQ:c3ee306a4d69ecdaf2a43837ae475a54857578533bb919740a5e4dfe9579e44e"
	if w.Address.String() != want {
		t.Fatalf("golden address mismatch:\n got %s\nwant %s", w.Address.String(), want)
	}
}
