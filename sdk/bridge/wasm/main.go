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
	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/bridge/signer"
)

const defaultSessionID = "default"

var (
	mu       sync.Mutex
	wallets  = map[string]*signer.DerivedWallet{}
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

// sqVerifyMessage verifies a ML-DSA-65 Mode-3 signature over a message given a
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
//   [value]              → default session + value
//   [sessionId, value]   → named session + value
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

func strip0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
