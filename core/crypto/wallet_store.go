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

	"golang.org/x/crypto/argon2"
)

// WalletFile is the on-disk format for an encrypted Splendor QC wallet.
// The Dilithium private key is encrypted with AES-256-GCM.
// The encryption key is derived from the user's password using Argon2id.
type WalletFile struct {
	Version   int    `json:"version"`
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
	walletVersion  = 1
	argon2Time     = 3
	argon2Memory   = 64 * 1024 // 64 MB
	argon2Threads  = 4
	argon2KeyLen   = 32 // AES-256
	saltSize       = 32
)

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
func LoadWallet(path string, password []byte) (*PublicKey, *PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read wallet file: %w", err)
	}

	var wf WalletFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, nil, fmt.Errorf("failed to parse wallet file: %w", err)
	}
	if wf.Version != walletVersion {
		return nil, nil, fmt.Errorf("unsupported wallet version: %d", wf.Version)
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
