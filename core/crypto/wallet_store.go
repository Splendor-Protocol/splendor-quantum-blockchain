package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	"golang.org/x/crypto/argon2"
)

// WalletFile is the on-disk format for an encrypted Splendor QC wallet.
// The ML-DSA-65 private key is encrypted with AES-256-GCM.
// The encryption key is derived from the user's password using Argon2id.
//
// Alg names the signature scheme the stored key belongs to. It exists because
// the version number alone cannot tell the two schemes this chain has used
// apart: round-3 CRYSTALS-Dilithium mode3 and ML-DSA-65 have byte-identical
// key sizes, so a wrong-scheme key deserialises without complaint and only
// shows up later as signatures nobody can verify. Recording the algorithm
// makes the file self-describing for the next migration, whatever it is.
type WalletFile struct {
	Version   int    `json:"version"`
	Alg       string `json:"alg"`
	Address   string `json:"address"`
	PublicKey []byte `json:"public_key"`
	Encrypted struct {
		Ciphertext []byte `json:"ciphertext"`
		Nonce      []byte `json:"nonce"`
		Salt       []byte `json:"salt"`
		// Argon2id parameters
		Time    uint32 `json:"time"`
		Memory  uint32 `json:"memory"`
		Threads uint8  `json:"threads"`
	} `json:"encrypted_private_key"`
}

const (
	// walletVersion is the format version SaveWallet writes and LoadWallet
	// accepts. Version 2 is the first version that holds NIST FIPS-204
	// ML-DSA-65 key material; see walletVersionDilithium3 below for why the
	// bump was necessary rather than cosmetic.
	walletVersion = 2

	// walletVersionDilithium3 is the pre-standardisation format: its keys are
	// round-3 CRYSTALS-Dilithium mode3, which this chain no longer speaks.
	// Crucially, mode3 and ML-DSA-65 share the same key sizes (public 1952,
	// secret 4032), so a v1 file passes every length check and decrypts fine —
	// it fails much later, as a signature no validator will accept, or worse
	// gets treated as a live key. LoadWallet therefore names this case
	// explicitly instead of reporting a generic version mismatch.
	walletVersionDilithium3 = 1

	argon2Time    = 3
	argon2Memory  = 64 * 1024 // 64 MB
	argon2Threads = 4
	argon2KeyLen  = 32 // AES-256
	saltSize      = 32

	// Floors for the KDF parameters read back from a wallet file. The params
	// are stored per-file so they can be raised over time, but without a floor
	// anyone who can rewrite the file could drop them to time=1, memory=8 and
	// turn an offline guess at the passphrase into something trivial.
	minArgon2Time   = 1
	minArgon2Memory = 32 * 1024 // 32 MB
	minSaltSize     = 16
)

// CheckKeyFilePermissions refuses a secret file that other local users can
// read. A validator key is the equivalent of a root CA key: a mode-644 wallet
// hands the chain to anyone with a shell on the box, and it is a mistake that
// is invisible until it is exploited.
//
// Windows does not expose POSIX modes through os.FileMode, so the check is
// skipped there rather than reporting a false result.
func CheckKeyFilePermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat key file: %w", err)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf(
			"key file %s is readable by other users (mode %04o): run 'chmod 600 %s'",
			path, mode, path)
	}
	return nil
}

// SaveWallet encrypts the private key with the given password and writes
// the wallet file to the specified path.
func SaveWallet(path string, pub *PublicKey, priv *PrivateKey, password []byte) error {
	// Generate random salt for Argon2id
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive 256-bit encryption key using Argon2id
	key := argon2.IDKey(password, salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	// Encrypt private key bytes using AES-256-GCM
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, priv.Bytes(), nil)

	addr := DeriveAddress(pub)

	wf := WalletFile{
		Version:   walletVersion,
		Alg:       AlgMLDSA65,
		Address:   addr.String(),
		PublicKey: pub.Bytes(),
	}
	wf.Encrypted.Ciphertext = ciphertext
	wf.Encrypted.Nonce = nonce
	wf.Encrypted.Salt = salt
	wf.Encrypted.Time = argon2Time
	wf.Encrypted.Memory = argon2Memory
	wf.Encrypted.Threads = argon2Threads

	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal wallet: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// LoadWallet reads a wallet file, decrypts the private key using the password,
// and returns the public key and private key.
//
// On a validator this file is the chain's root of trust: whoever holds it can
// sign blocks. It is therefore refused if other local users can read it, if it
// does not hold ML-DSA-65 key material, and if its KDF parameters have been
// weakened below the floors above.
func LoadWallet(path string, password []byte) (*PublicKey, *PrivateKey, error) {
	if err := CheckKeyFilePermissions(path); err != nil {
		return nil, nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read wallet file: %w", err)
	}

	var wf WalletFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, nil, fmt.Errorf("failed to parse wallet file: %w", err)
	}
	if wf.Version == walletVersionDilithium3 {
		return nil, nil, fmt.Errorf(
			"wallet %s is a version %d file: it holds a pre-standard CRYSTALS-Dilithium "+
				"mode3 key, not an ML-DSA-65 (NIST FIPS-204) key. This is a signature-scheme "+
				"change, not a corrupted file — the two schemes have identical key sizes "+
				"(public %d, secret %d), so the file parses and decrypts cleanly while the key "+
				"inside it can no longer sign for this chain. There is no conversion: generate "+
				"a new wallet with 'sqc-wallet generate' and register the new address (a "+
				"validator key must also be re-baked into that network's genesis.json)",
			path, wf.Version, PublicKeySize, PrivateKeySize)
	}
	if wf.Version != walletVersion {
		return nil, nil, fmt.Errorf(
			"wallet %s has unsupported version %d; this build reads version %d",
			path, wf.Version, walletVersion)
	}
	// The algorithm field is a cross-check, not the sole gate: version 2 is
	// defined as ML-DSA-65-only, so a v2 file that predates the field (or that
	// omits it) is still ML-DSA-65. A field that is present and says otherwise
	// is a scheme mismatch and must never be loaded as a live key.
	if wf.Alg != "" && wf.Alg != AlgMLDSA65 {
		return nil, nil, fmt.Errorf(
			"wallet %s declares signature algorithm %q, but this build only signs with %s "+
				"(NIST FIPS-204). Key sizes coincide across these schemes, so the file would "+
				"load without error — it is refused deliberately. Generate a new %s wallet "+
				"with 'sqc-wallet generate'",
			path, wf.Alg, AlgMLDSA65, AlgMLDSA65)
	}
	if wf.Encrypted.Time < minArgon2Time || wf.Encrypted.Memory < minArgon2Memory {
		return nil, nil, fmt.Errorf(
			"wallet %s has weakened key-derivation parameters (time=%d, memory=%dKB; "+
				"minimum is time=%d, memory=%dKB) — re-encrypt it before use",
			path, wf.Encrypted.Time, wf.Encrypted.Memory, minArgon2Time, minArgon2Memory)
	}
	if len(wf.Encrypted.Salt) < minSaltSize {
		return nil, nil, fmt.Errorf("wallet %s has a %d-byte salt, minimum is %d",
			path, len(wf.Encrypted.Salt), minSaltSize)
	}

	// Derive decryption key using Argon2id with stored parameters
	key := argon2.IDKey(
		password,
		wf.Encrypted.Salt,
		wf.Encrypted.Time,
		wf.Encrypted.Memory,
		wf.Encrypted.Threads,
		argon2KeyLen,
	)

	// Decrypt private key
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	privBytes, err := gcm.Open(nil, wf.Encrypted.Nonce, wf.Encrypted.Ciphertext, nil)
	if err != nil {
		return nil, nil, errors.New("decryption failed: wrong password or corrupted wallet")
	}

	pub, err := PublicKeyFromBytes(wf.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load public key: %w", err)
	}
	priv, err := PrivateKeyFromBytes(privBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load private key: %w", err)
	}

	// Zero out key material
	for i := range key {
		key[i] = 0
	}
	for i := range privBytes {
		privBytes[i] = 0
	}

	return pub, priv, nil
}
