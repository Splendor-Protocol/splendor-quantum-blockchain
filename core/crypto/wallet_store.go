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

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/persist/filesync"
)

// WalletFile is the on-disk format for an encrypted Splendor QC wallet.
// The ML-DSA-65 private key is encrypted with AES-256-GCM.
// The encryption key is derived from the user's password using Argon2id.
//
// Alg names the signature scheme the stored key belongs to, so the file is
// self-describing for whatever migration comes next. It is a label, not a
// guarantee — it is unauthenticated and absent from files written before it
// existed — so LoadWallet uses it only to refuse an explicit mismatch and
// proves the key itself with a sign/verify self-test.
//
// The two schemes this chain has used, round-3 CRYSTALS-Dilithium mode3 and
// ML-DSA-65, share a public key size (1952) but not a secret key size (4000 vs
// 4032), so a whole mode3 keypair cannot masquerade as ML-DSA-65: the private
// key fails its length check. What the label guards against is the subtler
// case of key material that is the right size but the wrong key.
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
	// walletVersion is the format version SaveWallet writes. Version 2 is
	// version 1 plus the self-describing "alg" field below.
	walletVersion = 2

	// walletVersionLegacy is the original format, written before "alg" existed.
	// It is still accepted, because that field is the only thing that changed:
	// the move to ML-DSA-65 never bumped this number, so a v1 file is just as
	// likely to hold FIPS-204 key material as a v2 one — every validator
	// wallet in production is a v1 file holding a perfectly good ML-DSA-65 key.
	// The version therefore says nothing about the scheme and is not treated as
	// if it did; LoadWallet proves the key instead. Refusing v1 wholesale would
	// take every existing wallet offline to fix a labelling problem.
	walletVersionLegacy = 1

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

	// Ceilings so a crafted file cannot turn LoadWallet into an unbounded
	// KDF or GCM panic. SaveWallet still writes the constants above.
	maxArgon2Time       = 8
	maxArgon2Memory     = 256 * 1024 // 256 MB
	maxArgon2Threads    = 16
	maxWalletFileBytes  = 256 << 10
	maxWalletCiphertext = 8 << 10
	walletGCMNonceSize  = 12
	walletGCMTagSize    = 16
)

// KeyFilePermissionsEnforced reports whether CheckKeyFilePermissions can
// actually decide anything on this platform.
//
// It exists so the gap is visible at runtime rather than only in DEPLOY.md. A
// check that quietly returns success where it cannot run reads, from the
// caller's side, exactly like a check that passed — which is how an operator
// ends up believing a key is protected when nothing looked at it.
func KeyFilePermissionsEnforced() bool { return runtime.GOOS != "windows" }

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
	if !KeyFilePermissionsEnforced() {
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
	defer wipeBytes(key)

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
	privBytes := priv.Bytes()
	ciphertext := gcm.Seal(nil, nonce, privBytes, nil)
	wipeBytes(privBytes)

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
	return filesync.WriteFileAtomic(path, data, 0o600)
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
	if len(data) > maxWalletFileBytes {
		return nil, nil, fmt.Errorf("wallet %s is %d bytes, maximum is %d",
			path, len(data), maxWalletFileBytes)
	}

	var wf WalletFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, nil, fmt.Errorf("failed to parse wallet file: %w", err)
	}
	if wf.Version != walletVersion && wf.Version != walletVersionLegacy {
		return nil, nil, fmt.Errorf(
			"wallet %s has unsupported version %d; this build reads versions %d and %d",
			path, wf.Version, walletVersionLegacy, walletVersion)
	}
	// The algorithm field is advisory: it records what the writer believed it
	// stored. Trust it only far enough to refuse an explicit mismatch, which is
	// worth naming early because the message can be precise. It cannot be the
	// gate — it is unauthenticated JSON, and absent from every v1 file. The
	// gate is the self-test further down, which proves the key instead of
	// reading its label.
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
	if wf.Encrypted.Time > maxArgon2Time || wf.Encrypted.Memory > maxArgon2Memory {
		return nil, nil, fmt.Errorf(
			"wallet %s has excessive key-derivation parameters (time=%d, memory=%dKB; "+
				"maximum is time=%d, memory=%dKB)",
			path, wf.Encrypted.Time, wf.Encrypted.Memory, maxArgon2Time, maxArgon2Memory)
	}
	if wf.Encrypted.Threads == 0 || wf.Encrypted.Threads > maxArgon2Threads {
		return nil, nil, fmt.Errorf(
			"wallet %s has invalid key-derivation threads %d (allowed 1..%d)",
			path, wf.Encrypted.Threads, maxArgon2Threads)
	}
	if len(wf.Encrypted.Salt) < minSaltSize {
		return nil, nil, fmt.Errorf("wallet %s has a %d-byte salt, minimum is %d",
			path, len(wf.Encrypted.Salt), minSaltSize)
	}
	if len(wf.Encrypted.Nonce) != walletGCMNonceSize {
		return nil, nil, fmt.Errorf("wallet %s has a %d-byte nonce, want %d",
			path, len(wf.Encrypted.Nonce), walletGCMNonceSize)
	}
	if len(wf.Encrypted.Ciphertext) < walletGCMTagSize || len(wf.Encrypted.Ciphertext) > maxWalletCiphertext {
		return nil, nil, fmt.Errorf("wallet %s ciphertext is %d bytes, allowed %d..%d",
			path, len(wf.Encrypted.Ciphertext), walletGCMTagSize, maxWalletCiphertext)
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
	defer wipeBytes(key)

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
	defer wipeBytes(privBytes)

	pub, err := PublicKeyFromBytes(wf.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load public key: %w", err)
	}
	priv, err := PrivateKeyFromBytes(privBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load private key: %w", err)
	}

	// Prove the key material is the scheme this build signs with, rather than
	// inferring it from a version number or a label. Neither is trustworthy:
	// the ML-DSA-65 migration never bumped the format version, so v1 files hold
	// either scheme, and "alg" is unauthenticated and missing from older files.
	// A sign/verify round trip settles it, and settles something the metadata
	// never could — that this private key is the one belonging to the public
	// key stored beside it. Without that, a mismatched pair yields a
	// live-looking address whose every signature is rejected downstream, which
	// is the worst shape a key-material bug can take.
	if err := selfTestKeypair(pub, priv); err != nil {
		return nil, nil, fmt.Errorf("wallet %s: %w", path, err)
	}

	return pub, priv, nil
}

// selfTestKeypair signs a fixed probe and verifies it, confirming in one step
// that the key material really is ML-DSA-65 and that the private key matches
// the stored public key. Costs one signature per wallet load — microseconds at
// process start, against a failure mode that is otherwise silent.
//
// The probe is signed under CtxMessage and never leaves this function; it is a
// liveness check on the key, not a credential.
func selfTestKeypair(pub *PublicKey, priv *PrivateKey) error {
	probe := []byte("splendor-qc wallet load self-test")

	sig, err := Sign(priv, probe, CtxMessage)
	if err != nil {
		return fmt.Errorf(
			"stored key material cannot sign as %s (NIST FIPS-204): %w", AlgMLDSA65, err)
	}
	if !Verify(pub, probe, CtxMessage, sig) {
		return fmt.Errorf(
			"the stored private key does not match the stored public key under %s "+
				"(NIST FIPS-204). The file is corrupt, or it holds key material from "+
				"another signature scheme: the address would still look valid while "+
				"every signature made with it is rejected. Restore a known-good backup, "+
				"or generate a new wallet with 'sqc-wallet generate'", AlgMLDSA65)
	}
	return nil
}

func wipeBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
