// bridge-relayer — claim attestations (M-of-N).
//
// A bridge claim is authorised on-chain by >= threshold DISTINCT operator
// attestations over types.BridgeClaimDigest, not by the submitter alone. This
// relayer always produces its own attestation. For threshold 1 (single-org) it
// submits with just that. For threshold > 1 it uses a shared --attestation-dir:
// each attestor writes its attestation for a burn there and reads its peers';
// any node with >= threshold distinct attestations submits (the on-chain
// claimed slot makes concurrent submitters safe). The directory is the whole
// coordination surface — put it on shared storage the attestors trust.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// claimDigest reproduces the module's digest for burn b at this bridge, using
// the canonical helper in core/types (one implementation, both sides).
func claimDigest(bridge qcrypto.Address, b Burn) ([32]byte, error) {
	assetWord, err := assetWord(b.Asset)
	if err != nil {
		return [32]byte{}, err
	}
	recipient, err := qcrypto.AddressFromString(b.Recipient)
	if err != nil {
		return [32]byte{}, fmt.Errorf("recipient: %w", err)
	}
	var evmTx [32]byte
	raw, err := hex.DecodeString(strings.TrimPrefix(b.TxHash, "0x"))
	if err != nil || len(raw) != 32 {
		return [32]byte{}, fmt.Errorf("evm_tx %q is not 32 bytes of hex", b.TxHash)
	}
	copy(evmTx[:], raw)
	return types.BridgeClaimDigest(types.ChainID, bridge, b.ID, assetWord, b.Amount, recipient, evmTx)
}

func assetWord(asset string) (uint8, error) {
	switch asset {
	case "spld":
		return 0, nil
	case "splv":
		return 1, nil
	default:
		return 0, fmt.Errorf("unknown asset %q", asset)
	}
}

// attest produces this relayer's attestation for burn b.
func (r *Relayer) attest(b Burn) (attestation, error) {
	digest, err := claimDigest(r.Bridge, b)
	if err != nil {
		return attestation{}, err
	}
	sig, err := qcrypto.Sign(r.Priv, digest[:], qcrypto.CtxBridge)
	if err != nil {
		return attestation{}, fmt.Errorf("sign attestation: %w", err)
	}
	return attestation{PubKey: r.Priv.Public().Bytes(), Signature: sig}, nil
}

// gatherAttestations returns the attestations available for burn b and whether
// they meet the threshold. For threshold 1 (or no attestation dir) that is just
// this relayer's own. Otherwise it publishes its own to the shared dir and
// collects every peer's, de-duplicated by attestor address.
func (r *Relayer) gatherAttestations(b Burn, threshold uint64) ([]attestation, bool, error) {
	own, err := r.attest(b)
	if err != nil {
		return nil, false, err
	}
	if threshold <= 1 || r.AttestationDir == "" {
		return []attestation{own}, threshold <= 1, nil
	}
	if err := r.publishAttestation(b, own); err != nil {
		return nil, false, err
	}
	atts, err := r.readAttestations(b)
	if err != nil {
		return nil, false, err
	}
	return atts, uint64(len(atts)) >= threshold, nil
}

// burnDir is the shared-directory path holding one burn's attestations.
func (r *Relayer) burnDir(b Burn) string {
	return filepath.Join(r.AttestationDir, r.Bridge.String(), fmt.Sprintf("%d", b.ID))
}

// publishAttestation writes this relayer's attestation atomically, named by the
// attestor address so each attestor owns exactly one file per burn.
func (r *Relayer) publishAttestation(b Burn, a attestation) error {
	dir := r.burnDir(b)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("attestation dir: %w", err)
	}
	self := qcrypto.DeriveAddress(r.Priv.Public())
	path := filepath.Join(dir, self.String()+".json")
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readAttestations loads every *.json in the burn dir, de-duplicating by the
// attestor derived from each pubkey and bounding the count. Malformed files are
// skipped (a peer mid-write); the on-chain check re-verifies every signature,
// so this need only assemble candidates.
func (r *Relayer) readAttestations(b Burn) ([]attestation, error) {
	entries, err := os.ReadDir(r.burnDir(b))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[qcrypto.Address]bool{}
	out := []attestation{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(r.burnDir(b), e.Name()))
		if err != nil {
			continue
		}
		var a attestation
		if err := json.Unmarshal(raw, &a); err != nil {
			continue
		}
		pub, err := qcrypto.PublicKeyFromBytes(a.PubKey)
		if err != nil {
			continue
		}
		addr := qcrypto.DeriveAddress(pub)
		if seen[addr] {
			continue
		}
		seen[addr] = true
		out = append(out, a)
		if len(out) >= MaxAttestations {
			break
		}
	}
	return out, nil
}

// MaxAttestations bounds how many attestations one claim carries, matching the
// module's operator cap so the submitted claim is never refused for size.
const MaxAttestations = 16
