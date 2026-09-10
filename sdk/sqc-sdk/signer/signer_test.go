package signer

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
	"github.com/splendor-protocol/splendor-quantum-blockchain/x402"
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
	// ML-DSA-65 signatures are randomized — payloads differ in signature bytes,
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
	// ML-DSA-65 (FIPS-204) address for the all-0x01 seed. Regenerated when the
	// chain migrated off round-3 CRYSTALS-Dilithium mode3; the WASM signer must be rebuilt
	// from this same Go signer to reproduce it.
	want := "SQ:307f4559431e680933fc1c62503f050140e5740068248d7374461e0311a2728e"
	if w.Address.String() != want {
		t.Fatalf("golden address mismatch:\n got %s\nwant %s", w.Address.String(), want)
	}
}

// ── Off-chain authorisations: permit, order, cancel, increment_nonce ────────

const (
	authTestBlockTime = int64(1_000_000)
	authTestDeadline  = int64(1_800_000_000)
)

// goldenWallet is the seed 01×32 wallet every cross-language vector uses.
func goldenWallet(t *testing.T) *DerivedWallet {
	t.Helper()
	seed, err := SeedFromHex(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// bytesAddr is an address of one repeated byte, matching the digest goldens
// in core/types (permitTestAddr).
func bytesAddr(b byte) qcrypto.Address {
	var a qcrypto.Address
	for i := range a {
		a[i] = b
	}
	return a
}

// pinChainID saves the package-level chain identity and restores it after
// the test, so a test that points the signer at another tier leaves no trace.
func pinChainID(t *testing.T) {
	t.Helper()
	id, adopted := types.ChainID, types.ChainIDAdoptedForTest()
	t.Cleanup(func() { types.RestoreChainIDForTest(id, adopted) })
}

func newAuthState(t *testing.T) *state.StateDB {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	return sdb
}

// envelope wraps already-marshalled params in the module-call envelope, so a
// signer result is fed to the router byte-for-byte as returned.
func envelope(t *testing.T, module, action string, params []byte) []byte {
	t.Helper()
	data, err := json.Marshal(vm.ModuleCall{Module: module, Action: action, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func allForks() forks.Schedule {
	return forks.Schedule{forks.ModuleRevertV1: 1, forks.QRC20V2: 1, forks.QRC1155V1: 1, forks.ExchangeV1: 1}
}

func authCtx(caller qcrypto.Address, nonce uint64) vm.ModuleContext {
	return vm.ModuleContext{Caller: caller, Nonce: nonce, Height: 1, Time: authTestBlockTime, Forks: allForks()}
}

func mustUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
}

func TestSignPermitDigestAndContext(t *testing.T) {
	w := goldenWallet(t)
	token, spender := bytesAddr(0xaa), bytesAddr(0xcc)
	out, err := w.SignPermit(PermitParams{
		Token: token.String(), Spender: spender.String(),
		Amount: "1000000000000000000", Nonce: 7, Deadline: authTestDeadline,
	})
	if err != nil {
		t.Fatalf("SignPermit: %v", err)
	}
	var sp SignedPermit
	mustUnmarshal(t, out, &sp)

	amount, _ := new(big.Int).SetString("1000000000000000000", 10)
	want, err := types.PermitDigest(types.ChainID, token, w.Address, spender, amount, 7, authTestDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if sp.DigestHex != hex.EncodeToString(want[:]) {
		t.Fatalf("digest_hex %s != PermitDigest %x", sp.DigestHex, want)
	}
	if sp.Owner != w.Address.String() || sp.Token != token.String() || sp.Spender != spender.String() {
		t.Fatalf("addresses not echoed: %+v", sp)
	}
	if sp.Amount != "1000000000000000000" || sp.Nonce != 7 || sp.Deadline != authTestDeadline || sp.ChainID != types.ChainID {
		t.Fatalf("fields not echoed: %+v", sp)
	}
	if !bytes.Equal(sp.OwnerPubKey, w.PublicKey) {
		t.Fatal("owner_pubkey is not the wallet's key")
	}
	if !qcrypto.Verify(w.Public, want[:], qcrypto.CtxPermit, sp.Signature) {
		t.Fatal("signature does not verify under CtxPermit")
	}
	if qcrypto.Verify(w.Public, want[:], qcrypto.CtxMessage, sp.Signature) {
		t.Fatal("signature must not verify under CtxMessage")
	}
	if qcrypto.Verify(w.Public, want[:], qcrypto.CtxOrder, sp.Signature) {
		t.Fatal("signature must not verify under CtxOrder")
	}
}

// The signer follows SetClientChainID: the digest, and so the signature, is
// bound to the tier the client picked, and a permit signed for devnet never
// matches the mainnet digest of the same fields.
func TestSignPermitHonoursClientChainID(t *testing.T) {
	pinChainID(t)
	w := goldenWallet(t)
	p := PermitParams{Token: bytesAddr(0xaa).String(), Spender: bytesAddr(0xcc).String(), Amount: "5", Nonce: 0, Deadline: authTestDeadline}

	var onMain SignedPermit
	out, err := w.SignPermit(p)
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, out, &onMain)

	if err := types.SetClientChainID("splendor-quantum-devnet-1"); err != nil {
		t.Fatal(err)
	}
	var onDev SignedPermit
	out, err = w.SignPermit(p)
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, out, &onDev)

	if onDev.ChainID != "splendor-quantum-devnet-1" || onMain.ChainID != types.DefaultChainID {
		t.Fatalf("chain_id not honoured: main %q dev %q", onMain.ChainID, onDev.ChainID)
	}
	if onDev.DigestHex == onMain.DigestHex {
		t.Fatal("digest did not move with the chain id")
	}
	want, err := types.PermitDigest("splendor-quantum-devnet-1", bytesAddr(0xaa), w.Address, bytesAddr(0xcc), big.NewInt(5), 0, authTestDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if onDev.DigestHex != hex.EncodeToString(want[:]) {
		t.Fatal("devnet digest is not PermitDigest under the devnet id")
	}
}

// The returned object is fed to qrc20.permit verbatim through the router and
// sets the allowance; a second submission is a replay (nonce advanced).
func TestSignPermitRoundTripsThroughRouter(t *testing.T) {
	w := goldenWallet(t)
	sdb := newAuthState(t)
	dep := &types.Receipt{}
	if err := vm.DispatchModuleCall(sdb, authCtx(w.Address, 1), envelope(t, "qrc20", "deploy",
		[]byte(`{"name":"Permit","symbol":"PRM","decimals":18,"total_supply":"1000000"}`)), dep); err != nil {
		t.Fatalf("qrc20.deploy: %v", err)
	}
	token := *dep.ContractAddress
	spender, relayer := bytesAddr(0x33), bytesAddr(0x44)
	tok := vm.NewQRC20Token(sdb, token)

	out, err := w.SignPermit(PermitParams{Token: token.String(), Spender: spender.String(), Amount: "1234", Nonce: 0, Deadline: authTestBlockTime})
	if err != nil {
		t.Fatalf("SignPermit: %v", err)
	}
	rc := &types.Receipt{}
	if err := vm.DispatchModuleCall(sdb, authCtx(relayer, 0), envelope(t, "qrc20", "permit", out), rc); err != nil {
		t.Fatalf("qrc20.permit: %v", err)
	}
	if got, _ := tok.Allowance(w.Address, spender); got.Cmp(big.NewInt(1234)) != 0 {
		t.Fatalf("allowance %s, want 1234", got)
	}
	if len(rc.Logs) != 1 {
		t.Fatalf("want one Approval log, got %d", len(rc.Logs))
	}
	if err := vm.DispatchModuleCall(sdb, authCtx(relayer, 1), envelope(t, "qrc20", "permit", out), &types.Receipt{}); err == nil || !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("replay must fail on the advanced nonce, got %v", err)
	}
	// One second past the deadline the same permit is refused.
	out2, err := w.SignPermit(PermitParams{Token: token.String(), Spender: spender.String(), Amount: "1", Nonce: 1, Deadline: authTestBlockTime})
	if err != nil {
		t.Fatal(err)
	}
	late := authCtx(relayer, 2)
	late.Time = authTestBlockTime + 1
	if err := vm.DispatchModuleCall(sdb, late, envelope(t, "qrc20", "permit", out2), &types.Receipt{}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired permit must fail, got %v", err)
	}
}

func TestSignPermitRejectsBadInputs(t *testing.T) {
	w := goldenWallet(t)
	ok := PermitParams{Token: bytesAddr(0xaa).String(), Spender: bytesAddr(0xcc).String(), Amount: "1", Nonce: 0, Deadline: authTestDeadline}
	if _, err := w.SignPermit(ok); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	twoPow256 := new(big.Int).Lsh(big.NewInt(1), 256).String()
	cases := map[string]func(p *PermitParams){
		"deadline 0":        func(p *PermitParams) { p.Deadline = 0 },
		"deadline negative": func(p *PermitParams) { p.Deadline = -1 },
		"amount empty":      func(p *PermitParams) { p.Amount = "" },
		"amount negative":   func(p *PermitParams) { p.Amount = "-1" },
		"amount 2^256":      func(p *PermitParams) { p.Amount = twoPow256 },
		"amount leading 0":  func(p *PermitParams) { p.Amount = "01" },
		"amount signed":     func(p *PermitParams) { p.Amount = "+1" },
		"bad token":         func(p *PermitParams) { p.Token = "not-an-address" },
		"bad spender":       func(p *PermitParams) { p.Spender = "SQ:zz" },
	}
	for name, mutate := range cases {
		p := ok
		mutate(&p)
		if _, err := w.SignPermit(p); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func goldenOrderParams(exchange qcrypto.Address) OrderParams {
	var salt [32]byte
	for i := range salt {
		salt[i] = byte(i + 1)
	}
	return OrderParams{
		Exchange: exchange.String(), Salt: hex.EncodeToString(salt[:]),
		Condition: 42, Outcome: 1, Side: types.OrderSideBuy,
		ShareAmount: "1000000000000000000", Price: "600000",
		Expiration: authTestDeadline, Nonce: 3, Recipient: bytesAddr(0xbb).String(),
	}
}

func TestSignOrderDigestAndContext(t *testing.T) {
	w := goldenWallet(t)
	exchange := bytesAddr(0xee)
	out, err := w.SignOrder(goldenOrderParams(exchange))
	if err != nil {
		t.Fatalf("SignOrder: %v", err)
	}
	var so SignedOrder
	mustUnmarshal(t, out, &so)

	shares, _ := new(big.Int).SetString("1000000000000000000", 10)
	var salt [32]byte
	for i := range salt {
		salt[i] = byte(i + 1)
	}
	want, err := types.OrderDigest(types.ChainID, types.Order{
		Salt: salt, Maker: w.Address, Exchange: exchange, Condition: 42, Outcome: 1, Side: types.OrderSideBuy,
		ShareAmount: shares, Price: big.NewInt(600000), Expiration: authTestDeadline, Nonce: 3, Recipient: bytesAddr(0xbb),
	})
	if err != nil {
		t.Fatal(err)
	}
	if so.Hash != hex.EncodeToString(want[:]) {
		t.Fatalf("hash %s != OrderDigest %x", so.Hash, want)
	}
	if so.Maker != w.Address.String() || so.Exchange != exchange.String() || so.Recipient != bytesAddr(0xbb).String() {
		t.Fatalf("addresses not echoed: %+v", so.OrderWire)
	}
	if so.Salt != hex.EncodeToString(salt[:]) || so.Condition != 42 || so.Outcome != 1 || so.Side != types.OrderSideBuy ||
		so.ShareAmount != "1000000000000000000" || so.Price != "600000" || so.Expiration != authTestDeadline || so.Nonce != 3 {
		t.Fatalf("fields not echoed: %+v", so.OrderWire)
	}
	if so.ChainID != types.ChainID || !bytes.Equal(so.MakerPubKey, w.PublicKey) {
		t.Fatalf("chain_id / maker_pubkey wrong: %+v", so)
	}
	if !qcrypto.Verify(w.Public, want[:], qcrypto.CtxOrder, so.Signature) {
		t.Fatal("signature does not verify under CtxOrder")
	}
	if qcrypto.Verify(w.Public, want[:], qcrypto.CtxMessage, so.Signature) {
		t.Fatal("signature must not verify under CtxMessage")
	}
	if qcrypto.Verify(w.Public, want[:], qcrypto.CtxPermit, so.Signature) {
		t.Fatal("signature must not verify under CtxPermit")
	}

	// The wire object must carry exactly the module's field names, nothing
	// renamed: decode it generically and check the keys the module reads.
	var raw map[string]json.RawMessage
	mustUnmarshal(t, out, &raw)
	for _, k := range []string{"salt", "maker", "exchange", "condition", "outcome", "side", "share_amount", "price", "expiration", "nonce", "recipient", "signature", "maker_pubkey", "hash", "chain_id"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("wire object lacks %q", k)
		}
	}
	if len(raw) != 15 {
		t.Errorf("wire object has %d keys, want 15: %s", len(raw), out)
	}
}

// An empty salt is filled randomly (two orders never collide) and an empty
// recipient is omitted from the wire — "pay the maker" — while the digest
// still commits to thirty-two zero bytes.
func TestSignOrderRandomSaltAndZeroRecipient(t *testing.T) {
	w := goldenWallet(t)
	p := goldenOrderParams(bytesAddr(0xee))
	p.Salt, p.Recipient = "", ""
	var a, b SignedOrder
	outA, err := w.SignOrder(p)
	if err != nil {
		t.Fatal(err)
	}
	outB, err := w.SignOrder(p)
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, outA, &a)
	mustUnmarshal(t, outB, &b)
	if len(a.Salt) != 64 || a.Salt == b.Salt || a.Hash == b.Hash {
		t.Fatalf("random salt not applied: %s / %s", a.Salt, b.Salt)
	}
	var raw map[string]json.RawMessage
	mustUnmarshal(t, outA, &raw)
	if _, ok := raw["recipient"]; ok {
		t.Fatal("empty recipient must be omitted from the wire")
	}
	var salt [32]byte
	sb, _ := hex.DecodeString(a.Salt)
	copy(salt[:], sb)
	shares, _ := new(big.Int).SetString(p.ShareAmount, 10)
	want, err := types.OrderDigest(types.ChainID, types.Order{
		Salt: salt, Maker: w.Address, Exchange: bytesAddr(0xee), Condition: 42, Outcome: 1, Side: types.OrderSideBuy,
		ShareAmount: shares, Price: big.NewInt(600000), Expiration: authTestDeadline, Nonce: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash != hex.EncodeToString(want[:]) {
		t.Fatal("hash with a zero recipient is not OrderDigest over zero bytes")
	}
}

func TestSignOrderHonoursClientChainID(t *testing.T) {
	pinChainID(t)
	w := goldenWallet(t)
	p := goldenOrderParams(bytesAddr(0xee))
	var onMain, onDev SignedOrder
	out, err := w.SignOrder(p)
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, out, &onMain)
	if err := types.SetClientChainID("splendor-quantum-devnet-1"); err != nil {
		t.Fatal(err)
	}
	out, err = w.SignOrder(p)
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, out, &onDev)
	if onDev.ChainID != "splendor-quantum-devnet-1" || onDev.Hash == onMain.Hash {
		t.Fatalf("order hash did not follow the chain id: %s vs %s", onMain.Hash, onDev.Hash)
	}
}

func TestSignOrderRejectsBadInputs(t *testing.T) {
	w := goldenWallet(t)
	ok := goldenOrderParams(bytesAddr(0xee))
	if _, err := w.SignOrder(ok); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	cases := map[string]func(p *OrderParams){
		"side 2":               func(p *OrderParams) { p.Side = 2 },
		"price 0":              func(p *OrderParams) { p.Price = "0" },
		"price 1e6":            func(p *OrderParams) { p.Price = "1000000" },
		"price leading zero":   func(p *OrderParams) { p.Price = "0600000" },
		"shares 0":             func(p *OrderParams) { p.ShareAmount = "0" },
		"shares empty":         func(p *OrderParams) { p.ShareAmount = "" },
		"shares 2^256":         func(p *OrderParams) { p.ShareAmount = new(big.Int).Lsh(big.NewInt(1), 256).String() },
		"expiration negative":  func(p *OrderParams) { p.Expiration = -1 },
		"salt short":           func(p *OrderParams) { p.Salt = "abcd" },
		"salt not hex":         func(p *OrderParams) { p.Salt = strings.Repeat("zz", 32) },
		"exchange zero":        func(p *OrderParams) { p.Exchange = qcrypto.Address{}.String() },
		"exchange bad":         func(p *OrderParams) { p.Exchange = "nope" },
		"recipient bad":        func(p *OrderParams) { p.Recipient = "nope" },
		"recipient = exchange": func(p *OrderParams) { p.Recipient = p.Exchange },
		"recipient = router":   func(p *OrderParams) { p.Recipient = ModuleRouterAddress().String() },
	}
	for name, mutate := range cases {
		p := ok
		mutate(&p)
		if _, err := w.SignOrder(p); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// A cancel must name the exact order: no random salt there.
	p := ok
	p.Salt = ""
	if _, err := w.SignCancel(p); err == nil || !strings.Contains(err.Error(), "salt is required") {
		t.Fatalf("SignCancel without a salt: %v", err)
	}
}

// SignX402 attaches the payer's key outside the signed body, so a payer that
// has never sent a transaction verifies at the node; without the key the
// same payload is refused, and the hash is unchanged by its presence.
func TestSignX402AttachesPublicKeyForFreshPayer(t *testing.T) {
	var seed [SeedSize]byte
	copy(seed[:], []byte("x402-fresh-payer-seed-0123456789"))
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	sdb := newAuthState(t)
	acc := types.NewAccount(w.Address)
	acc.AddBalance(big.NewInt(1_000_000))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	payee := bytesAddr(0x55)
	nonceHex := hex.EncodeToString(bytes.Repeat([]byte{0xAA}, 32))
	out, err := w.SignX402(X402Params{To: payee.String(), Amount: "500", ValidAfter: 1, ValidBefore: 9_999_999_999, NonceHex: nonceHex})
	if err != nil {
		t.Fatal(err)
	}
	var payload x402.PaymentPayload
	mustUnmarshal(t, out, &payload)
	if !bytes.Equal(payload.PublicKey, w.PublicKey) {
		t.Fatal("public_key is not the payer's key")
	}
	reqs := &x402.PaymentRequirements{PayTo: payee, Amount: big.NewInt(500), ValidAfter: 1, ValidBefore: 9_999_999_999}
	proc := x402.NewProcessor(sdb)
	if res := proc.Verify(reqs, &payload); !res.Valid {
		t.Fatalf("fresh payer with attached key must verify: %s", res.Reason)
	}
	withKey, err := payload.Hash()
	if err != nil {
		t.Fatal(err)
	}
	payload.PublicKey = nil
	withoutKey, err := payload.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if withKey != withoutKey {
		t.Fatal("public_key must not be part of the signed body")
	}
	if res := proc.Verify(reqs, &payload); res.Valid || !strings.Contains(res.Reason, "public key not registered") {
		t.Fatalf("without the key a fresh payer must not verify: %+v", res)
	}
}

// goldenFile is the cross-language vector file the TypeScript SDK and the
// WASM tests read. The permit/order digests in it are core/types' goldens;
// the fromGoldenSeed values are what sqSignPermit / sqSignOrder / sqSignCancel /
// sqSignIncrementNonce report from the seed 01×32 session with the same inputs.
type goldenFile struct {
	Address string `json:"address"`
	ChainID string `json:"chainId"`
	Permit  struct {
		Context        string `json:"context"`
		ChainID        string `json:"chainId"`
		Contract       string `json:"contract"`
		Owner          string `json:"owner"`
		Spender        string `json:"spender"`
		Amount         string `json:"amount"`
		Nonce          uint64 `json:"nonce"`
		Deadline       int64  `json:"deadline"`
		Digest         string `json:"digest"`
		FromGoldenSeed struct {
			DigestHex string `json:"digestHex"`
		} `json:"fromGoldenSeed"`
	} `json:"permit"`
	Order struct {
		Context              string `json:"context"`
		ChainID              string `json:"chainId"`
		Exchange             string `json:"exchange"`
		Salt                 string `json:"salt"`
		Maker                string `json:"maker"`
		Condition            uint64 `json:"condition"`
		Outcome              uint8  `json:"outcome"`
		Side                 uint8  `json:"side"`
		ShareAmount          string `json:"share_amount"`
		Price                string `json:"price"`
		Expiration           int64  `json:"expiration"`
		Nonce                uint64 `json:"nonce"`
		Recipient            string `json:"recipient"`
		Digest               string `json:"digest"`
		CancelDigest         string `json:"cancelDigest"`
		IncrementNonceDigest string `json:"incrementNonceDigest"`
		FromGoldenSeed       struct {
			Hash                    string `json:"hash"`
			CancelDigestHex         string `json:"cancelDigestHex"`
			IncrementNonceDigestHex string `json:"incrementNonceDigestHex"`
		} `json:"fromGoldenSeed"`
	} `json:"order"`
	Bridge struct {
		Context   string `json:"context"`
		ChainID   string `json:"chainId"`
		Bridge    string `json:"bridge"`
		ID        uint64 `json:"id"`
		Asset     string `json:"asset"`
		Amount    string `json:"amount"`
		Recipient string `json:"recipient"`
		EvmTx     string `json:"evmTx"`
		Digest    string `json:"digest"`
	} `json:"bridge"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	raw, err := os.ReadFile("../testdata/golden-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	mustUnmarshal(t, raw, &g)
	return g
}

func TestGoldenVectorsFilePinsDigests(t *testing.T) {
	pinChainID(t)
	g := loadGolden(t)
	if err := types.SetClientChainID(g.ChainID); err != nil {
		t.Fatal(err)
	}
	if g.Permit.Context != string(qcrypto.CtxPermit) || g.Order.Context != string(qcrypto.CtxOrder) ||
		g.Bridge.Context != string(qcrypto.CtxBridge) {
		t.Fatalf("context strings drifted: permit %q order %q bridge %q", g.Permit.Context, g.Order.Context, g.Bridge.Context)
	}
	if g.Permit.ChainID != g.ChainID || g.Order.ChainID != g.ChainID || g.Bridge.ChainID != g.ChainID {
		t.Fatal("vector chain ids disagree with the file's chainId")
	}

	// Pure digests: the core/types goldens, restated for other languages.
	addr := func(s string) qcrypto.Address {
		a, err := qcrypto.AddressFromString(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		return a
	}
	amount, _ := new(big.Int).SetString(g.Permit.Amount, 10)
	pd, err := types.PermitDigest(g.Permit.ChainID, addr(g.Permit.Contract), addr(g.Permit.Owner), addr(g.Permit.Spender), amount, g.Permit.Nonce, g.Permit.Deadline)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(pd[:]) != g.Permit.Digest {
		t.Fatalf("permit.digest %s != PermitDigest %x", g.Permit.Digest, pd)
	}
	saltBytes, _ := hex.DecodeString(g.Order.Salt)
	var salt [32]byte
	copy(salt[:], saltBytes)
	shares, _ := new(big.Int).SetString(g.Order.ShareAmount, 10)
	price, _ := new(big.Int).SetString(g.Order.Price, 10)
	od, err := types.OrderDigest(g.Order.ChainID, types.Order{
		Salt: salt, Maker: addr(g.Order.Maker), Exchange: addr(g.Order.Exchange), Condition: g.Order.Condition,
		Outcome: g.Order.Outcome, Side: g.Order.Side, ShareAmount: shares, Price: price,
		Expiration: g.Order.Expiration, Nonce: g.Order.Nonce, Recipient: addr(g.Order.Recipient),
	})
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(od[:]) != g.Order.Digest {
		t.Fatalf("order.digest %s != OrderDigest %x", g.Order.Digest, od)
	}
	cd := types.CancelDigest(od)
	if hex.EncodeToString(cd[:]) != g.Order.CancelDigest {
		t.Fatalf("order.cancelDigest %s != CancelDigest %x", g.Order.CancelDigest, cd)
	}
	nd, err := types.IncrementNonceDigest(g.Order.ChainID, addr(g.Order.Exchange), addr(g.Order.Maker), g.Order.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(nd[:]) != g.Order.IncrementNonceDigest {
		t.Fatalf("order.incrementNonceDigest %s != IncrementNonceDigest %x", g.Order.IncrementNonceDigest, nd)
	}

	bridgeAmount, _ := new(big.Int).SetString(g.Bridge.Amount, 10)
	evmBytes, err := hex.DecodeString(strings.TrimPrefix(g.Bridge.EvmTx, "0x"))
	if err != nil || len(evmBytes) != 32 {
		t.Fatalf("bridge.evmTx is not 32 hex bytes: %v", err)
	}
	var evmTx [32]byte
	copy(evmTx[:], evmBytes)
	assetWord := map[string]uint8{"spld": 0, "splv": 1}[g.Bridge.Asset]
	bd, err := types.BridgeClaimDigest(g.Bridge.ChainID, addr(g.Bridge.Bridge), g.Bridge.ID, assetWord, bridgeAmount, addr(g.Bridge.Recipient), evmTx)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(bd[:]) != g.Bridge.Digest {
		t.Fatalf("bridge.digest %s != BridgeClaimDigest %x", g.Bridge.Digest, bd)
	}

	// Signer-level: the golden seed as owner/maker with the same inputs.
	w := goldenWallet(t)
	if w.Address.String() != g.Address {
		t.Fatalf("golden seed derives %s, file says %s", w.Address, g.Address)
	}
	var sp SignedPermit
	out, err := w.SignPermit(PermitParams{Token: g.Permit.Contract, Spender: g.Permit.Spender, Amount: g.Permit.Amount, Nonce: g.Permit.Nonce, Deadline: g.Permit.Deadline})
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, out, &sp)
	if sp.DigestHex != g.Permit.FromGoldenSeed.DigestHex {
		t.Errorf("permit.fromGoldenSeed.digestHex moved:\n got %s\nwant %s", sp.DigestHex, g.Permit.FromGoldenSeed.DigestHex)
	}
	op := OrderParams{
		Exchange: g.Order.Exchange, Salt: g.Order.Salt, Condition: g.Order.Condition, Outcome: g.Order.Outcome, Side: g.Order.Side,
		ShareAmount: g.Order.ShareAmount, Price: g.Order.Price, Expiration: g.Order.Expiration, Nonce: g.Order.Nonce, Recipient: g.Order.Recipient,
	}
	var so SignedOrder
	out, err = w.SignOrder(op)
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, out, &so)
	if so.Hash != g.Order.FromGoldenSeed.Hash {
		t.Errorf("order.fromGoldenSeed.hash moved:\n got %s\nwant %s", so.Hash, g.Order.FromGoldenSeed.Hash)
	}
	var sc SignedCancel
	out, err = w.SignCancel(op)
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, out, &sc)
	if sc.OrderHash != so.Hash || sc.DigestHex != g.Order.FromGoldenSeed.CancelDigestHex {
		t.Errorf("order.fromGoldenSeed.cancelDigestHex moved:\n got %s\nwant %s", sc.DigestHex, g.Order.FromGoldenSeed.CancelDigestHex)
	}
	var sn SignedIncrementNonce
	out, err = w.SignIncrementNonce(IncrementNonceParams{Exchange: g.Order.Exchange, ExpectedNonce: g.Order.Nonce})
	if err != nil {
		t.Fatal(err)
	}
	mustUnmarshal(t, out, &sn)
	if sn.DigestHex != g.Order.FromGoldenSeed.IncrementNonceDigestHex {
		t.Errorf("order.fromGoldenSeed.incrementNonceDigestHex moved:\n got %s\nwant %s", sn.DigestHex, g.Order.FromGoldenSeed.IncrementNonceDigestHex)
	}
}

// The bridge-claim path had no test in either language, which is how a signer
// binary that never exported sqSignBridgeClaim reached main with the whole Go
// suite and 154 Vitest cases green. This pins the digest and its domain
// separation; wasm-bridge-claim.test.ts asserts the WASM build agrees.
func TestSignBridgeClaimDigestAndContext(t *testing.T) {
	w := goldenWallet(t)
	bridge, recipient := bytesAddr(0xbb), bytesAddr(0xdd)
	evmTxHex := strings.Repeat("ab", 32)

	out, err := w.SignBridgeClaim(BridgeClaimParams{
		Bridge: bridge.String(), ID: 42, Asset: "spld",
		Amount: "1000000000000000000", Recipient: recipient.String(),
		EvmTx: "0x" + evmTxHex,
	})
	if err != nil {
		t.Fatalf("SignBridgeClaim: %v", err)
	}
	var sc SignedBridgeClaim
	mustUnmarshal(t, out, &sc)
	t.Logf("bridge claim digest_hex = %s (chain %s)", sc.DigestHex, sc.ChainID)

	amount, _ := new(big.Int).SetString("1000000000000000000", 10)
	evmBytes, _ := hex.DecodeString(evmTxHex)
	var evmTx [32]byte
	copy(evmTx[:], evmBytes)
	want, err := types.BridgeClaimDigest(types.ChainID, bridge, 42, 0, amount, recipient, evmTx)
	if err != nil {
		t.Fatal(err)
	}
	if sc.DigestHex != hex.EncodeToString(want[:]) {
		t.Fatalf("digest_hex %s != BridgeClaimDigest %x", sc.DigestHex, want)
	}
	if sc.Bridge != bridge.String() || sc.Recipient != recipient.String() {
		t.Fatalf("addresses not echoed: %+v", sc)
	}
	if sc.ID != 42 || sc.Asset != "spld" || sc.Amount != "1000000000000000000" ||
		sc.EvmTx != "0x"+evmTxHex || sc.ChainID != types.ChainID {
		t.Fatalf("fields not echoed: %+v", sc)
	}
	if !bytes.Equal(sc.PubKey, w.PublicKey) {
		t.Fatal("pubkey is not the wallet's key")
	}
	if !qcrypto.Verify(w.Public, want[:], qcrypto.CtxBridge, sc.Signature) {
		t.Fatal("signature does not verify under CtxBridge")
	}
	// An attestation must not be replayable as any other authorisation.
	for _, ctx := range [][]byte{qcrypto.CtxMessage, qcrypto.CtxPermit, qcrypto.CtxOrder} {
		if qcrypto.Verify(w.Public, want[:], ctx, sc.Signature) {
			t.Fatalf("signature must not verify under %s", ctx)
		}
	}
}

// "splv" is the other asset word, and the id/evm_tx guards are what stop a
// malformed attestation being signed at all.
func TestSignBridgeClaimRejectsBadInputs(t *testing.T) {
	w := goldenWallet(t)
	ok := BridgeClaimParams{
		Bridge: bytesAddr(0xbb).String(), ID: 1, Asset: "splv", Amount: "1",
		Recipient: bytesAddr(0xdd).String(), EvmTx: "0x" + strings.Repeat("cd", 32),
	}
	if _, err := w.SignBridgeClaim(ok); err != nil {
		t.Fatalf("splv claim should sign: %v", err)
	}
	for name, mutate := range map[string]func(p *BridgeClaimParams){
		"zero id":       func(p *BridgeClaimParams) { p.ID = 0 },
		"unknown asset": func(p *BridgeClaimParams) { p.Asset = "eth" },
		"short evm_tx":  func(p *BridgeClaimParams) { p.EvmTx = "0xdeadbeef" },
		"zero bridge":   func(p *BridgeClaimParams) { p.Bridge = qcrypto.Address{}.String() },
	} {
		p := ok
		mutate(&p)
		if _, err := w.SignBridgeClaim(p); err == nil {
			t.Errorf("%s: signed when it should have been rejected", name)
		}
	}
}
