package crypto

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTestWallet(t *testing.T) (path string, pass []byte, addr Address) {
	t.Helper()
	pub, priv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	path = filepath.Join(t.TempDir(), "validator.sqwallet")
	pass = []byte("correct horse battery staple")
	if err := SaveWallet(path, pub, priv, pass); err != nil {
		t.Fatalf("save wallet: %v", err)
	}
	return path, pass, DeriveAddress(pub)
}

// A wallet written by SaveWallet must load back cleanly.
func TestWalletRoundTrip(t *testing.T) {
	path, pass, addr := writeTestWallet(t)

	pub, priv, err := LoadWallet(path, pass)
	if err != nil {
		t.Fatalf("load wallet: %v", err)
	}
	if DeriveAddress(pub) != addr {
		t.Fatal("loaded wallet has a different address")
	}
	if priv == nil {
		t.Fatal("loaded wallet has no private key")
	}
}

// SaveWallet must not leave the key readable by other local users.
func TestSaveWalletWritesOwnerOnlyMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes are not meaningful on Windows")
	}
	path, _, _ := writeTestWallet(t)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("wallet written with mode %04o, want owner-only", mode)
	}
}

// A validator key that other local users can read must be refused outright.
// This is the mistake that quietly hands the chain to anyone with a shell.
func TestLoadWalletRefusesGroupOrWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes are not meaningful on Windows")
	}
	for _, mode := range []os.FileMode{0o640, 0o644, 0o604, 0o660, 0o666} {
		path, pass, _ := writeTestWallet(t)
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		if _, _, err := LoadWallet(path, pass); err == nil {
			t.Errorf("mode %04o was accepted; it must be refused", mode)
		}
	}
}

// Weakened key-derivation parameters must be refused, so rewriting the file
// cannot turn an offline guess at the passphrase into a cheap one.
func TestLoadWalletRefusesWeakenedKDF(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*WalletFile)
	}{
		{"low memory", func(wf *WalletFile) { wf.Encrypted.Memory = 8 }},
		{"zero time", func(wf *WalletFile) { wf.Encrypted.Time = 0 }},
		{"short salt", func(wf *WalletFile) { wf.Encrypted.Salt = wf.Encrypted.Salt[:8] }},
	} {
		t.Run(c.name, func(t *testing.T) {
			path, pass, _ := writeTestWallet(t)

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var wf WalletFile
			if err := json.Unmarshal(raw, &wf); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			c.mutate(&wf)
			out, err := json.Marshal(wf)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}

			if _, _, err := LoadWallet(path, pass); err == nil {
				t.Fatalf("%s was accepted", c.name)
			}
		})
	}
}

// The wrong passphrase must not decrypt the key.
func TestLoadWalletRejectsWrongPassphrase(t *testing.T) {
	path, _, _ := writeTestWallet(t)
	if _, _, err := LoadWallet(path, []byte("not the passphrase")); err == nil {
		t.Fatal("wallet decrypted under the wrong passphrase")
	}
}

// rewriteWallet loads the wallet file at path, applies mutate to the parsed
// struct, and writes it back owner-only. It is how the tests below forge the
// files an operator might actually be holding — a pre-migration v1 wallet, or
// one whose fields have been edited by hand.
func rewriteWallet(t *testing.T, path string, mutate func(*WalletFile)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var wf WalletFile
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mutate(&wf)
	out, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func requireErrContains(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error message is missing %q.\ngot: %v", w, err)
		}
	}
}

// The key that comes back out must be the key that went in — not merely a key
// whose address matches. A round-trip is only proved by signing with the
// loaded private key and verifying under the original public key.
func TestWalletRoundTripPreservesSigningKey(t *testing.T) {
	pub, priv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	path := filepath.Join(t.TempDir(), "wallet.sqwallet")
	pass := []byte("correct horse battery staple")
	if err := SaveWallet(path, pub, priv, pass); err != nil {
		t.Fatalf("save wallet: %v", err)
	}

	loadedPub, loadedPriv, err := LoadWallet(path, pass)
	if err != nil {
		t.Fatalf("load wallet: %v", err)
	}
	if !bytes.Equal(loadedPub.Bytes(), pub.Bytes()) {
		t.Fatal("loaded public key differs from the saved one")
	}
	msg := []byte("the block this validator is about to seal")
	sig, err := Sign(loadedPriv, msg, CtxBlock)
	if err != nil {
		t.Fatalf("sign with loaded key: %v", err)
	}
	if !Verify(pub, msg, CtxBlock, sig) {
		t.Fatal("signature from the loaded private key does not verify under the original public key")
	}
}

// The file SaveWallet writes must say, on its face, both which format version
// it is and which signature scheme the key belongs to. Without the algorithm
// field the file is ambiguous: Dilithium3 and ML-DSA-65 keys are the same size.
func TestSaveWalletRecordsVersionAndAlgorithm(t *testing.T) {
	path, _, _ := writeTestWallet(t)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var wf WalletFile
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wf.Version != 2 {
		t.Errorf("wallet written with version %d, want 2", wf.Version)
	}
	if wf.Alg != AlgMLDSA65 {
		t.Errorf("wallet written with alg %q, want %q", wf.Alg, AlgMLDSA65)
	}
	// The field must be in the JSON itself, not merely in the Go struct: a
	// reader on another machine (or in another language) only sees the bytes.
	if !strings.Contains(string(raw), `"alg": "`+AlgMLDSA65+`"`) {
		t.Errorf("wallet JSON does not carry the alg field:\n%s", raw)
	}
}

// The format version says nothing about the signature scheme: the ML-DSA-65
// migration never bumped it, so the wallets in production are v1 files holding
// FIPS-204 keys. This is the exact shape of every live validator wallet — a
// v1 file with no algorithm field — and refusing it would take the whole fleet
// offline on the next redeploy to fix a labelling problem.
func TestLoadWalletAcceptsLegacyVersion1File(t *testing.T) {
	path, pass, addr := writeTestWallet(t)
	rewriteWallet(t, path, func(wf *WalletFile) {
		wf.Version = 1
		wf.Alg = "" // v1 files predate the algorithm field entirely
	})

	pub, priv, err := LoadWallet(path, pass)
	if err != nil {
		t.Fatalf("legacy v1 wallet holding an ML-DSA-65 key was refused: %v", err)
	}
	if DeriveAddress(pub) != addr {
		t.Fatal("loaded wallet has a different address")
	}
	if priv == nil {
		t.Fatal("loaded wallet has no private key")
	}
}

// Reading a wallet must never write one. An in-place "upgrade" would rewrite
// the chain's root of trust as a side effect of starting a node, and would do
// it to a file the operator has backed up under a hash.
func TestLoadWalletNeverRewritesTheFile(t *testing.T) {
	path, pass, _ := writeTestWallet(t)
	rewriteWallet(t, path, func(wf *WalletFile) {
		wf.Version = 1
		wf.Alg = ""
	})

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, _, err := LoadWallet(path, pass); err != nil {
		t.Fatalf("load: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("LoadWallet rewrote the wallet file; it must be left untouched")
	}
}

// Any version this build does not know is a plain format mismatch and must say
// so, naming both versions it does read.
func TestLoadWalletRefusesUnknownVersion(t *testing.T) {
	path, pass, _ := writeTestWallet(t)
	rewriteWallet(t, path, func(wf *WalletFile) { wf.Version = 99 })

	_, _, err := LoadWallet(path, pass)
	requireErrContains(t, err, "unsupported version 99", "versions 1 and 2")
}

// The gate that actually decides whether a key is usable is a sign/verify round
// trip, not a label. Swapping in an unrelated public key leaves every length
// check satisfied and the metadata untouched, so nothing else in the loader
// would catch it — the node would come up advertising an address whose
// signatures no validator accepts.
func TestLoadWalletRefusesMismatchedKeypair(t *testing.T) {
	path, pass, _ := writeTestWallet(t)

	other, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	rewriteWallet(t, path, func(wf *WalletFile) { wf.PublicKey = other.Bytes() })

	_, _, err = LoadWallet(path, pass)
	requireErrContains(t, err, "does not match", AlgMLDSA65, "generate a new wallet")
}

// The self-test must not be fooled by a file that simply claims to be current:
// relabelling a mismatched key as v2 with the right alg string changes nothing
// about the key material, and unauthenticated metadata must not be able to
// promote it.
func TestLoadWalletSelfTestOutranksTheMetadata(t *testing.T) {
	path, pass, _ := writeTestWallet(t)

	other, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	rewriteWallet(t, path, func(wf *WalletFile) {
		wf.Version = walletVersion
		wf.Alg = AlgMLDSA65
		wf.PublicKey = other.Bytes()
	})

	if _, _, err := LoadWallet(path, pass); err == nil {
		t.Fatal("a mismatched keypair was accepted because the file said it was current")
	}
}

// A file that names a scheme this build does not sign with must be refused on
// the strength of that field alone, even though the key material is the right
// length and would decrypt perfectly.
func TestLoadWalletRefusesWrongAlgorithm(t *testing.T) {
	for _, alg := range []string{"CRYSTALS-Dilithium-mode3", "ML-DSA-44", "SLH-DSA-SHA2-128s", "ed25519"} {
		t.Run(alg, func(t *testing.T) {
			path, pass, _ := writeTestWallet(t)
			rewriteWallet(t, path, func(wf *WalletFile) { wf.Alg = alg })

			_, _, err := LoadWallet(path, pass)
			requireErrContains(t, err, alg, AlgMLDSA65)
		})
	}
}

// A v2 file that omits the algorithm field is still ML-DSA-65 by definition of
// the version, so it must keep loading. This is the one leniency in the gate,
// and it is deliberate — pinning it down here stops it drifting either way.
func TestLoadWalletAcceptsVersion2WithoutAlgorithmField(t *testing.T) {
	path, pass, addr := writeTestWallet(t)
	rewriteWallet(t, path, func(wf *WalletFile) { wf.Alg = "" })

	pub, _, err := LoadWallet(path, pass)
	if err != nil {
		t.Fatalf("v2 wallet without an alg field was refused: %v", err)
	}
	if DeriveAddress(pub) != addr {
		t.Fatal("loaded wallet has a different address")
	}
}

// The KDF floors must survive the version bump: the checks still fire, and
// still say what is wrong. (The scheme gate runs first, so a mutated file must
// not accidentally trip that instead.)
func TestLoadWalletStillReportsWeakenedKDF(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*WalletFile)
		want   string
	}{
		{"low memory", func(wf *WalletFile) { wf.Encrypted.Memory = 8 }, "weakened key-derivation parameters"},
		{"zero time", func(wf *WalletFile) { wf.Encrypted.Time = 0 }, "weakened key-derivation parameters"},
		{"short salt", func(wf *WalletFile) { wf.Encrypted.Salt = wf.Encrypted.Salt[:8] }, "8-byte salt"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path, pass, _ := writeTestWallet(t)
			rewriteWallet(t, path, c.mutate)

			_, _, err := LoadWallet(path, pass)
			requireErrContains(t, err, c.want)
		})
	}
}

// The floors are floors, not the working values: a file sitting exactly on
// minArgon2Time/minArgon2Memory must clear the check. It then fails to decrypt
// (the stored ciphertext was sealed under the stronger parameters), and that
// distinction is the proof — a KDF-parameter error here would mean the floor
// had crept above its documented value.
func TestLoadWalletAcceptsKDFParamsExactlyAtTheFloor(t *testing.T) {
	path, pass, _ := writeTestWallet(t)
	rewriteWallet(t, path, func(wf *WalletFile) {
		wf.Encrypted.Time = minArgon2Time
		wf.Encrypted.Memory = minArgon2Memory
		wf.Encrypted.Salt = wf.Encrypted.Salt[:minSaltSize]
	})

	_, _, err := LoadWallet(path, pass)
	if err == nil {
		t.Fatal("expected decryption to fail under different KDF parameters")
	}
	if strings.Contains(err.Error(), "key-derivation parameters") ||
		strings.Contains(err.Error(), "salt, minimum is") {
		t.Fatalf("parameters at the documented floor were rejected by the floor check: %v", err)
	}
}
