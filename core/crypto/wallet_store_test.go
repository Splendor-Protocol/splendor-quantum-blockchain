package crypto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
