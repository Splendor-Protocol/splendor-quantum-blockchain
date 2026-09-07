package signer

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// The SQVL section of golden-vectors.json, pinned here for Go and checked
// against the WASM build in wasm-verify.test.ts. Same purpose as the permit /
// order / bridge pins: two implementations of one digest cannot drift apart
// silently.
type verifyGolden struct {
	ChainID     string `json:"chainId"`
	Context     string `json:"context"`
	Instruction struct {
		Version          int    `json:"version"`
		InstructionID    string `json:"instruction_id"`
		Purpose          string `json:"purpose"`
		Origin           string `json:"origin"`
		Payload          string `json:"payload"`
		SettlementHint   string `json:"settlement_hint"`
		SettlementAction string `json:"settlement_action"`
		From             string `json:"from"`
		To               string `json:"to"`
		Amount           string `json:"amount"`
	} `json:"instruction"`
	Digest      string `json:"digest"`
	Attestation struct {
		Instance string `json:"instance"`
		Digest   string `json:"digest"`
	} `json:"attestation"`
	FromGoldenSeed struct {
		SLHPublicKeyHex string `json:"slhPublicKeyHex"`
	} `json:"fromGoldenSeed"`
}

func TestGoldenVerifyVectorsPinDigests(t *testing.T) {
	raw, err := os.ReadFile("../testdata/golden-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		SeedHex string       `json:"seedHex"`
		Address string       `json:"address"`
		Verify  verifyGolden `json:"verify"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	g := file.Verify
	if g.Context != string(qcrypto.CtxVerify) {
		t.Fatalf("verify context drifted: golden %q, Go %q", g.Context, qcrypto.CtxVerify)
	}

	id, err := types.ParseInstructionID(g.Instruction.InstructionID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(g.Instruction.Payload)
	if err != nil {
		t.Fatal(err)
	}
	ins := &types.VerifyInstruction{
		Version:          g.Instruction.Version,
		InstructionID:    id,
		Purpose:          g.Instruction.Purpose,
		Origin:           g.Instruction.Origin,
		Payload:          payload,
		SettlementHint:   g.Instruction.SettlementHint,
		SettlementAction: g.Instruction.SettlementAction,
		From:             g.Instruction.From,
		To:               g.Instruction.To,
		Amount:           g.Instruction.Amount,
	}
	digest, err := types.VerifyInstructionDigest(g.ChainID, ins)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(digest[:]); got != g.Digest {
		t.Fatalf("verify.digest %s != VerifyInstructionDigest %s", g.Digest, got)
	}

	instance, err := qcrypto.AddressFromString(g.Attestation.Instance)
	if err != nil {
		t.Fatal(err)
	}
	ad, err := types.VerifyAttestationDigest(g.ChainID, instance, digest)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(ad[:]); got != g.Attestation.Digest {
		t.Fatalf("verify.attestation.digest %s != VerifyAttestationDigest %s", g.Attestation.Digest, got)
	}

	// The seeded SLH-DSA key: the same seed the golden address derives from.
	seed, err := SeedFromHex(file.SeedHex)
	if err != nil {
		t.Fatal(err)
	}
	slhPub, slhPriv, err := qcrypto.SLHKeypairFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(slhPub.Bytes()); got != g.FromGoldenSeed.SLHPublicKeyHex {
		t.Fatalf("verify.fromGoldenSeed.slhPublicKeyHex %s != SLHKeypairFromSeed %s", g.FromGoldenSeed.SLHPublicKeyHex, got)
	}

	// And the whole thing round-trips: a session built from the seed signs
	// the golden instruction, and the chain-side check accepts it with the
	// golden address as signer.
	w, err := DeriveFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := types.SignVerifyInstruction(g.ChainID, ins, w.Private, slhPriv); err != nil {
		t.Fatal(err)
	}
	_, signer, err := types.CheckVerifyInstruction(g.ChainID, ins)
	if err != nil {
		t.Fatalf("golden-seed dual signature refused: %v", err)
	}
	if signer.String() != file.Address {
		t.Fatalf("signer %s, want the golden address %s", signer, file.Address)
	}
}
