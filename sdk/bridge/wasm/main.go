//go:build js && wasm

// Package main exposes Dilithium derive/sign helpers to JavaScript via syscall/js.
// Private keys stay in WASM memory and are never returned to JS.
package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"syscall/js"
	"sync"

	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/bridge/signer"
)

var (
	mu     sync.Mutex
	wallet *signer.DerivedWallet
)

func main() {
	js.Global().Set("sqDeriveFromSeedHex", js.FuncOf(sqDeriveFromSeedHex))
	js.Global().Set("sqDeriveFromSigHex", js.FuncOf(sqDeriveFromSigHex))
	js.Global().Set("sqGetAddress", js.FuncOf(sqGetAddress))
	js.Global().Set("sqGetPublicKeyB64", js.FuncOf(sqGetPublicKeyB64))
	js.Global().Set("sqSignTransfer", js.FuncOf(sqSignTransfer))
	js.Global().Set("sqSignCall", js.FuncOf(sqSignCall))
	js.Global().Set("sqSignDeploy", js.FuncOf(sqSignDeploy))
	js.Global().Set("sqSignX402", js.FuncOf(sqSignX402))
	js.Global().Set("sqLock", js.FuncOf(sqLock))
	js.Global().Set("sqIsUnlocked", js.FuncOf(sqIsUnlocked))
	js.Global().Set("sqDomainMessage", js.FuncOf(sqDomainMessage))

	// Keep Go runtime alive
	select {}
}

func errVal(msg string) interface{} {
	return map[string]interface{}{"error": msg}
}

func okJSON(b []byte) interface{} {
	return string(b)
}

func sqDomainMessage(this js.Value, args []js.Value) interface{} {
	return signer.DerivationDomainMessage
}

func sqDeriveFromSeedHex(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return errVal("seed hex required")
	}
	seed, err := signer.SeedFromHex(args[0].String())
	if err != nil {
		return errVal(err.Error())
	}
	w, err := signer.DeriveFromSeed(seed)
	if err != nil {
		return errVal(err.Error())
	}
	mu.Lock()
	wallet = w
	mu.Unlock()
	b, _ := json.Marshal(w.PublicResult())
	return okJSON(b)
}

func sqDeriveFromSigHex(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return errVal("signature hex required")
	}
	sig, err := hex.DecodeString(strip0x(args[0].String()))
	if err != nil {
		return errVal("invalid signature hex")
	}
	w, err := signer.DeriveFromEVMSignature(sig)
	if err != nil {
		return errVal(err.Error())
	}
	mu.Lock()
	wallet = w
	mu.Unlock()
	b, _ := json.Marshal(w.PublicResult())
	return okJSON(b)
}

func sqGetAddress(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	defer mu.Unlock()
	if wallet == nil {
		return errVal("wallet locked")
	}
	return wallet.Address.String()
}

func sqGetPublicKeyB64(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	defer mu.Unlock()
	if wallet == nil {
		return errVal("wallet locked")
	}
	return base64.StdEncoding.EncodeToString(wallet.PublicKey)
}

func sqSignTransfer(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	defer mu.Unlock()
	if wallet == nil {
		return errVal("wallet locked")
	}
	if len(args) < 1 {
		return errVal("params JSON required")
	}
	var p signer.TransferParams
	if err := json.Unmarshal([]byte(args[0].String()), &p); err != nil {
		return errVal("invalid params: " + err.Error())
	}
	wire, err := wallet.SignTransfer(p)
	if err != nil {
		return errVal(err.Error())
	}
	return okJSON(wire)
}

func sqSignCall(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	defer mu.Unlock()
	if wallet == nil {
		return errVal("wallet locked")
	}
	if len(args) < 1 {
		return errVal("params JSON required")
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(args[0].String()), &raw); err != nil {
		return errVal("invalid params: " + err.Error())
	}
	p := signer.CallParams{
		Contract: str(raw, "contract"),
		Value:    str(raw, "value"),
		Fee:      str(raw, "fee"),
		Nonce:    uint64(num(raw, "nonce")),
		Timestamp: int64(num(raw, "timestamp")),
	}
	if cd, ok := raw["calldata_b64"].(string); ok && cd != "" {
		b, err := base64.StdEncoding.DecodeString(cd)
		if err != nil {
			return errVal("invalid calldata_b64")
		}
		p.Calldata = b
	} else if cd, ok := raw["calldata_hex"].(string); ok && cd != "" {
		b, err := hex.DecodeString(strip0x(cd))
		if err != nil {
			return errVal("invalid calldata_hex")
		}
		p.Calldata = b
	}
	wire, err := wallet.SignCall(p)
	if err != nil {
		return errVal(err.Error())
	}
	return okJSON(wire)
}

func sqSignDeploy(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	defer mu.Unlock()
	if wallet == nil {
		return errVal("wallet locked")
	}
	if len(args) < 1 {
		return errVal("params JSON required")
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(args[0].String()), &raw); err != nil {
		return errVal("invalid params: " + err.Error())
	}
	p := signer.DeployParams{
		Fee:       str(raw, "fee"),
		Nonce:     uint64(num(raw, "nonce")),
		Timestamp: int64(num(raw, "timestamp")),
	}
	if b64, ok := raw["bytecode_b64"].(string); ok && b64 != "" {
		b, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return errVal("invalid bytecode_b64")
		}
		p.Bytecode = b
	} else if hx, ok := raw["bytecode_hex"].(string); ok && hx != "" {
		b, err := hex.DecodeString(strip0x(hx))
		if err != nil {
			return errVal("invalid bytecode_hex")
		}
		p.Bytecode = b
	}
	wire, err := wallet.SignDeploy(p)
	if err != nil {
		return errVal(err.Error())
	}
	return okJSON(wire)
}

func sqSignX402(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	defer mu.Unlock()
	if wallet == nil {
		return errVal("wallet locked")
	}
	if len(args) < 1 {
		return errVal("params JSON required")
	}
	var p signer.X402Params
	if err := json.Unmarshal([]byte(args[0].String()), &p); err != nil {
		return errVal("invalid params: " + err.Error())
	}
	wire, err := wallet.SignX402(p)
	if err != nil {
		return errVal(err.Error())
	}
	return okJSON(wire)
}

func sqLock(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	wallet = nil
	mu.Unlock()
	return map[string]interface{}{"ok": true}
}

func sqIsUnlocked(this js.Value, args []js.Value) interface{} {
	mu.Lock()
	defer mu.Unlock()
	return wallet != nil
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
