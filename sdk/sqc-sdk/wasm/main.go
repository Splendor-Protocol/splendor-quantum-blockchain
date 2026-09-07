//go:build js && wasm

// Package main exposes ML-DSA-65 derive/sign helpers to JavaScript via syscall/js.
// Private keys stay in WASM memory and are never returned to JS.
// Multiple sessions are supported so concurrent bridge instances do not clobber keys.
package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"syscall/js"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/sqc-sdk/signer"
)

const defaultSessionID = "default"

var (
	mu       sync.Mutex
	wallets  = map[string]*signer.DerivedWallet{}
	slhKeys  = map[string]*qcrypto.SLHPrivateKey{}
	nextSess uint64
)

func main() {
	js.Global().Set("sqCreateSession", js.FuncOf(sqCreateSession))
	js.Global().Set("sqDestroySession", js.FuncOf(sqDestroySession))
	js.Global().Set("sqDeriveFromSeedHex", js.FuncOf(sqDeriveFromSeedHex))
	js.Global().Set("sqDeriveFromSigHex", js.FuncOf(sqDeriveFromSigHex))
	js.Global().Set("sqGetAddress", js.FuncOf(sqGetAddress))
	js.Global().Set("sqGetPublicKeyB64", js.FuncOf(sqGetPublicKeyB64))
	js.Global().Set("sqSignTransfer", js.FuncOf(sqSignTransfer))
	js.Global().Set("sqSignCall", js.FuncOf(sqSignCall))
	js.Global().Set("sqSignDeploy", js.FuncOf(sqSignDeploy))
	js.Global().Set("sqSignX402", js.FuncOf(sqSignX402))
	js.Global().Set("sqSignX402Settlement", js.FuncOf(sqSignX402Settlement))
	js.Global().Set("sqSignMessage", js.FuncOf(sqSignMessage))
	js.Global().Set("sqVerifyMessage", js.FuncOf(sqVerifyMessage))
	js.Global().Set("sqLock", js.FuncOf(sqLock))
	js.Global().Set("sqIsUnlocked", js.FuncOf(sqIsUnlocked))
	js.Global().Set("sqDomainMessage", js.FuncOf(sqDomainMessage))
	js.Global().Set("sqSetChainID", js.FuncOf(sqSetChainID))
	js.Global().Set("sqSignPermit", js.FuncOf(sqSignPermit))
	js.Global().Set("sqSignOrder", js.FuncOf(sqSignOrder))
	js.Global().Set("sqSignCancel", js.FuncOf(sqSignCancel))
	js.Global().Set("sqSignIncrementNonce", js.FuncOf(sqSignIncrementNonce))
	js.Global().Set("sqSignBridgeClaim", js.FuncOf(sqSignBridgeClaim))
	js.Global().Set("sqGetModuleRouterAddress", js.FuncOf(sqGetModuleRouterAddress))
	js.Global().Set("sqGenerateSLH", js.FuncOf(sqGenerateSLH))
	js.Global().Set("sqSignVerifyInstruction", js.FuncOf(sqSignVerifyInstruction))

	select {}
}

func errVal(msg string) interface{} {
	return map[string]interface{}{"error": msg}
}

func okJSON(b []byte) interface{} {
	return string(b)
}

func sessionID(args []js.Value, idx int) string {
	if len(args) > idx && args[idx].Type() == js.TypeString {
		s := args[idx].String()
		if s != "" {
			return s
		}
	}
	return defaultSessionID
}

func getWallet(id string) (*signer.DerivedWallet, bool) {
	w, ok := wallets[id]
	return w, ok && w != nil
}

func sqCreateSession(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	defer mu.Unlock()
	nextSess++
	id := fmt.Sprintf("s%d", nextSess)
	wallets[id] = nil
	return id
}

func sqDestroySession(this js.Value, args []js.Value) interface{} {
	id := sessionID(args, 0)
	mu.Lock()
	if w, ok := wallets[id]; ok && w != nil {
		w.Zeroize()
	}
	delete(wallets, id)
	delete(slhKeys, id)
	mu.Unlock()
	return map[string]interface{}{"ok": true}
}

func sqDomainMessage(this js.Value, args []js.Value) interface{} {
	return signer.DerivationDomainMessage
}

// Args: [sessionId?, seedHex]  — if one arg, treated as seedHex on default session.
func sqDeriveFromSeedHex(this js.Value, args []js.Value) interface{} {
	id, seedHex, err := parseSessionAndString(args, "seed hex")
	if err != "" {
		return errVal(err)
	}
	seed, e := signer.SeedFromHex(seedHex)
	if e != nil {
		return errVal(e.Error())
	}
	w, e := signer.DeriveFromSeed(seed)
	if e != nil {
		return errVal(e.Error())
	}
	mu.Lock()
	if old, ok := wallets[id]; ok && old != nil {
		old.Zeroize()
	}
	wallets[id] = w
	mu.Unlock()
	b, _ := json.Marshal(w.PublicResult())
	return okJSON(b)
}

func sqDeriveFromSigHex(this js.Value, args []js.Value) interface{} {
	id, sigHex, err := parseSessionAndString(args, "signature hex")
	if err != "" {
		return errVal(err)
	}
	sig, e := hex.DecodeString(strip0x(sigHex))
	if e != nil {
		return errVal("invalid signature hex")
	}
	w, e := signer.DeriveFromEVMSignature(sig)
	if e != nil {
		return errVal(e.Error())
	}
	mu.Lock()
	if old, ok := wallets[id]; ok && old != nil {
		old.Zeroize()
	}
	wallets[id] = w
	mu.Unlock()
	b, _ := json.Marshal(w.PublicResult())
	return okJSON(b)
}

func sqGetAddress(this js.Value, args []js.Value) interface{} {
	id := sessionID(args, 0)
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	return w.Address.String()
}

func sqGetPublicKeyB64(this js.Value, args []js.Value) interface{} {
	id := sessionID(args, 0)
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	return base64.StdEncoding.EncodeToString(w.PublicKey)
}

// sqSignMessage signs an ARBITRARY message with the session's ML-DSA-65 key —
// the generic signer that Splendor Connect v2 needs to authenticate a quantum
// (SQ:) identity (the on-chain signers only sign structured txs). The signature
// is over the raw message bytes (qcrypto.Sign/Verify do not hash internally), so
// a gateway rebuilds the exact challenge string and calls sqVerifyMessage.
//
// Args: [sessionId, message] or [message] for the default session.
// Returns: { address, publicKeyB64, signatureB64 } or { error }.
func sqSignMessage(this js.Value, args []js.Value) interface{} {
	id, message, err := parseSessionAndString(args, "message")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	sig, e := qcrypto.Sign(w.Private, []byte(message), qcrypto.CtxMessage)
	if e != nil {
		return errVal("sign: " + e.Error())
	}
	return map[string]interface{}{
		"address":      w.Address.String(),
		"publicKeyB64": base64.StdEncoding.EncodeToString(w.PublicKey),
		"signatureB64": base64.StdEncoding.EncodeToString(sig),
	}
}

// sqVerifyMessage verifies an ML-DSA-65 signature over a message given a
// base64 public key — a PURE function (no session/key). This is what a Node
// gateway calls to authenticate a Connect v2 challenge server-side, since there
// is no native-JS CIRCL Mode-3 verifier.
//
// Args: [publicKeyB64, message, signatureB64].
// Returns: { valid: bool } or { error }.
func sqVerifyMessage(this js.Value, args []js.Value) interface{} {
	if len(args) < 3 {
		return errVal("verify: expected [publicKeyB64, message, signatureB64]")
	}
	pubB64 := args[0].String()
	message := args[1].String()
	sigB64 := args[2].String()

	pubBytes, e := base64.StdEncoding.DecodeString(pubB64)
	if e != nil {
		return errVal("verify: bad publicKeyB64: " + e.Error())
	}
	pk, e := qcrypto.PublicKeyFromBytes(pubBytes)
	if e != nil {
		return errVal("verify: " + e.Error())
	}
	sig, e := base64.StdEncoding.DecodeString(sigB64)
	if e != nil {
		return errVal("verify: bad signatureB64: " + e.Error())
	}
	return map[string]interface{}{"valid": qcrypto.Verify(pk, []byte(message), qcrypto.CtxMessage, sig)}
}

// Args: [sessionId, paramsJson] or [paramsJson] for default session.
func sqSignTransfer(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var p signer.TransferParams
	if e := json.Unmarshal([]byte(paramsJSON), &p); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	wire, e := w.SignTransfer(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

func sqSignCall(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var raw map[string]interface{}
	if e := json.Unmarshal([]byte(paramsJSON), &raw); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	p := signer.CallParams{
		Contract:  str(raw, "contract"),
		Value:     str(raw, "value"),
		Fee:       str(raw, "fee"),
		Nonce:     uint64(num(raw, "nonce")),
		Timestamp: int64(num(raw, "timestamp")),
	}
	if cd, ok := raw["calldata_b64"].(string); ok && cd != "" {
		b, e := base64.StdEncoding.DecodeString(cd)
		if e != nil {
			return errVal("invalid calldata_b64")
		}
		p.Calldata = b
	} else if cd, ok := raw["calldata_hex"].(string); ok && cd != "" {
		b, e := hex.DecodeString(strip0x(cd))
		if e != nil {
			return errVal("invalid calldata_hex")
		}
		p.Calldata = b
	}
	wire, e := w.SignCall(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

func sqSignDeploy(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var raw map[string]interface{}
	if e := json.Unmarshal([]byte(paramsJSON), &raw); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	p := signer.DeployParams{
		Fee:       str(raw, "fee"),
		Nonce:     uint64(num(raw, "nonce")),
		Timestamp: int64(num(raw, "timestamp")),
	}
	if b64, ok := raw["bytecode_b64"].(string); ok && b64 != "" {
		b, e := base64.StdEncoding.DecodeString(b64)
		if e != nil {
			return errVal("invalid bytecode_b64")
		}
		p.Bytecode = b
	} else if hx, ok := raw["bytecode_hex"].(string); ok && hx != "" {
		b, e := hex.DecodeString(strip0x(hx))
		if e != nil {
			return errVal("invalid bytecode_hex")
		}
		p.Bytecode = b
	}
	wire, e := w.SignDeploy(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

func sqSignX402(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var p signer.X402Params
	if e := json.Unmarshal([]byte(paramsJSON), &p); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	wire, e := w.SignX402(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

// sqSignX402Settlement signs the on-chain TxX402 that settles a payment.
// params.nonce_hex must match the nonce used for the payment payload.
func sqSignX402Settlement(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var p signer.X402Params
	if e := json.Unmarshal([]byte(paramsJSON), &p); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	wire, e := w.SignX402Settlement(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

// ── Off-chain authorisations (no transaction, no fee) ──────────────────────
//
// Each takes [sessionId?, paramsJson] like sqSignX402 and returns the JSON
// params object the matching module action accepts verbatim (or {error}).
// The digests are bound to the chain id set by sqSetChainID.

// sqSignPermit signs a QRC-20 permit (signer.PermitParams) → qrc20.permit
// params. The session key is the owner.
func sqSignPermit(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var p signer.PermitParams
	if e := json.Unmarshal([]byte(paramsJSON), &p); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	wire, e := w.SignPermit(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

// sqSignOrder signs an exchange order (signer.OrderParams) → the order object
// exchange.match / cancel_order accept. The session key is the maker.
func sqSignOrder(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var p signer.OrderParams
	if e := json.Unmarshal([]byte(paramsJSON), &p); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	wire, e := w.SignOrder(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

// sqSignCancel signs the cancellation of one of the session's orders
// (signer.OrderParams, salt required) → exchange.cancel_order params that
// anyone may submit.
func sqSignCancel(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var p signer.OrderParams
	if e := json.Unmarshal([]byte(paramsJSON), &p); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	wire, e := w.SignCancel(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

// sqSignIncrementNonce signs a bump of the session's order nonce at an
// exchange (signer.IncrementNonceParams) → exchange.increment_nonce params
// that anyone may submit.
func sqSignIncrementNonce(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var p signer.IncrementNonceParams
	if e := json.Unmarshal([]byte(paramsJSON), &p); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	wire, e := w.SignIncrementNonce(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

// sqSignBridgeClaim signs a migration-bridge claim attestation
// (signer.BridgeClaimParams) under CtxBridge → {pubkey, signature, …}.
func sqSignBridgeClaim(this js.Value, args []js.Value) interface{} {
	id, paramsJSON, err := parseSessionAndString(args, "params JSON")
	if err != "" {
		return errVal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	w, ok := getWallet(id)
	if !ok {
		return errVal("wallet locked")
	}
	var p signer.BridgeClaimParams
	if e := json.Unmarshal([]byte(paramsJSON), &p); e != nil {
		return errVal("invalid params: " + e.Error())
	}
	wire, e := w.SignBridgeClaim(p)
	if e != nil {
		return errVal(e.Error())
	}
	return okJSON(wire)
}

// sqGetModuleRouterAddress returns the bare "SQ:…" address every native
// module call is sent to (the `contract` of sqSignCall carrying a module
// envelope). No arguments, no session — like sqDomainMessage.
func sqGetModuleRouterAddress(this js.Value, args []js.Value) interface{} {
	return signer.ModuleRouterAddress().String()
}

func sqLock(this js.Value, args []js.Value) interface{} {
	id := sessionID(args, 0)
	mu.Lock()
	if w, ok := wallets[id]; ok && w != nil {
		w.Zeroize()
	}
	wallets[id] = nil
	mu.Unlock()
	return map[string]interface{}{"ok": true}
}

func sqIsUnlocked(this js.Value, args []js.Value) interface{} {
	id := sessionID(args, 0)
	mu.Lock()
	defer mu.Unlock()
	_, ok := getWallet(id)
	return ok
}

// parseSessionAndString supports:
//
//	[value]              → default session + value
//	[sessionId, value]   → named session + value
func parseSessionAndString(args []js.Value, valueName string) (id, value, errMsg string) {
	if len(args) < 1 {
		return "", "", valueName + " required"
	}
	if len(args) == 1 {
		return defaultSessionID, args[0].String(), ""
	}
	return args[0].String(), args[1].String(), ""
}

func str(m map[string]interface{}, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func num(m map[string]interface{}, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return 0
}

func sqGenerateSLH(this js.Value, args []js.Value) interface{} {
	id := sessionID(args, 0)
	pub, priv, err := qcrypto.GenerateSLHKeypair()
	if err != nil {
		return errVal(err.Error())
	}
	mu.Lock()
	slhKeys[id] = priv
	mu.Unlock()
	return map[string]interface{}{
		"pubkey_hex": hex.EncodeToString(pub.Bytes()),
	}
}

func sqSignVerifyInstruction(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return errVal("instruction JSON required")
	}
	id := defaultSessionID
	raw := args[0].String()
	if len(args) >= 2 {
		id = args[0].String()
		raw = args[1].String()
	}
	var ins types.VerifyInstruction
	if err := json.Unmarshal([]byte(raw), &ins); err != nil {
		return errVal(err.Error())
	}
	mu.Lock()
	w, ok := getWallet(id)
	slh := slhKeys[id]
	mu.Unlock()
	if !ok || w == nil {
		return errVal("wallet locked")
	}
	if slh == nil {
		return errVal("call sqGenerateSLH first")
	}
	if err := types.SignVerifyInstruction(types.ChainID, &ins, w.Private, slh); err != nil {
		return errVal(err.Error())
	}
	b, err := json.Marshal(&ins)
	if err != nil {
		return errVal(err.Error())
	}
	return okJSON(b)
}

func strip0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}

// sqSetChainID points this signer at a network.
//
// Every signing path stamps types.ChainID into the transaction body, and the
// chain rejects a body carrying another network's id. Until the tiers were
// given separate ids that did not matter — one value was correct everywhere.
// Now a signer left on the default silently produces transactions that testnet
// and devnet refuse, so a caller that targets anything other than mainnet must
// set this first.
//
// Takes the chain id as a string, e.g. "splendor-quantum-testnet-1". Returns
// {"ok":true} or {"error":...}.
func sqSetChainID(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || args[0].Type() != js.TypeString {
		return errVal("sqSetChainID(chainID string)")
	}
	if err := types.SetClientChainID(args[0].String()); err != nil {
		return errVal(err.Error())
	}
	return map[string]interface{}{"ok": true}
}
