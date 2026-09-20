package vm

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// instanceLeaf returns the RAW bytes stored at the instance slot — the state
// leaf itself, not a re-marshalled copy of a decoded struct. Consensus compares
// these bytes through the state root, so they are what a parity test must pin.
func instanceLeaf(t *testing.T, sdb *state.StateDB, addr qcrypto.Address) []byte {
	t.Helper()
	b, err := sdb.GetStorageValue(addr, verifyInstanceSlot(addr))
	if err != nil {
		t.Fatalf("read instance leaf: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("instance slot is empty")
	}
	return b
}

// addrWire renders an address exactly as encoding/json renders a [32]byte: a
// JSON array of decimal numbers. Built from the bytes by hand, so this helper
// cannot drift in step with the types whose encoding the parity test is pinning.
func addrWire(a qcrypto.Address) string {
	parts := make([]string, len(a))
	for i, b := range a {
		parts[i] = strconv.Itoa(int(b))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// The verify module is live on every tier (verify_v1 is scheduled at height 1 in
// both genesis-local.json and genesis-production.json) while verify_identity_v1
// is scheduled on none of them. So the pre-activation path is not hypothetical
// future behaviour: it is the behaviour the whole fleet is executing right now,
// and the instance record it writes is a state leaf hashed into every subsequent
// state root.
//
// This is the same shape of argument as TestX402SettlementRecordIsForkGated in
// core/block, run in the other direction. There, the leaf did NOT exist on the
// deployed build, so writing it needed a height. Here the leaf DOES exist on the
// deployed build, so withholding it needs one — and since no height is scheduled,
// withholding it is simply a fork. The test pins:
//
//   - the pre-activation leaf, byte for byte, against the record the deployed
//     build constructs from the same deploy;
//   - that activating verify_identity_v1 changes that leaf not at all;
//   - that the leaf really is sensitive to the identities, so the parity above
//     is not two empty records agreeing with each other.
func TestVerifyIdentityPreForkInstanceLeafMatchesDeployedBuild(t *testing.T) {
	owner := newVfyOp(t)
	op := newVfyOp(t)
	key := hex.EncodeToString(op.slhPub.Bytes())

	params := map[string]any{
		"threshold": 1,
		"operators": []string{op.addr.String()},
		"operator_identities": []map[string]any{
			{"operator": op.addr.String(), "slh_pubkey": key},
		},
	}

	leafFor := func(identity bool) ([]byte, qcrypto.Address) {
		t.Helper()
		sdb := newTestState(t)
		rc, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, identity), "deploy", params)
		if err != nil {
			t.Fatalf("deploy (identity=%v): %v", identity, err)
		}
		return instanceLeaf(t, sdb, *rc.ContractAddress), *rc.ContractAddress
	}

	preLeaf, addr := leafFor(false)

	// What the deployed build stores, spelled out as literal wire bytes rather
	// than re-marshalled from verifyInstance. Going through the struct would
	// cancel out on both sides: an adversarial review demonstrated that swapping
	// two fields in verifyInstance — which changes the real leaf, and so splits a
	// mixed-version set at the first verify deploy — still passed a struct-derived
	// expectation. Only a literal pins the shape as well as the values.
	deployed := []byte(fmt.Sprintf(
		`{"address":%s,"owner":%s,"threshold":1,"operators":[%s],`+
			`"operator_identities":[{"address":%s,"slh_pubkey":%q}],"paused":false}`,
		addrWire(addr), addrWire(owner.addr), addrWire(op.addr),
		addrWire(op.addr), base64.StdEncoding.EncodeToString(op.slhPub.Bytes()),
	))
	if !bytes.Equal(preLeaf, deployed) {
		t.Fatalf("pre-activation instance leaf differs from the deployed build (v1.1.2), so the same "+
			"deploy yields a different state root and a mixed-version validator set splits on it:\n"+
			" got:  %s\n want: %s", preLeaf, deployed)
	}

	// Activation adds requirements, not state.
	postLeaf, _ := leafFor(true)
	if !bytes.Equal(preLeaf, postLeaf) {
		t.Fatalf("%s changes the instance leaf for an unchanged deploy:\n pre:  %s\n post: %s",
			forks.VerifyIdentityV1, preLeaf, postLeaf)
	}

	// The premise: the identities are what make this leaf distinctive. If the
	// same deploy without them produced the same bytes, the parity check above
	// would be pinning nothing.
	sdb := newTestState(t)
	rc, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, false), "deploy", map[string]any{
		"threshold": 1,
		"operators": []string{op.addr.String()},
	})
	if err != nil {
		t.Fatalf("deploy without identities: %v", err)
	}
	bare := instanceLeaf(t, sdb, *rc.ContractAddress)
	if bytes.Equal(bare, preLeaf) {
		t.Fatal("premise changed: operator_identities no longer reaches the instance leaf, so this " +
			"parity check proves nothing — re-derive it before trusting it")
	}
}

// A malformed slh_pubkey is refused before verify_identity_v1, because the
// deployed build refuses it. Accepting it early would diverge the receipt status
// as well as the state: the fleet fails the transaction, consuming the nonce and
// the fee, while a node that skipped the check succeeds and writes a leaf.
//
// The two entry points reject at different strengths, and that asymmetry is
// deployed behaviour rather than an oversight to tidy up:
//
//   - deploy runs the full parse (parseOperatorIdentities), so a key that decodes
//     but is not a valid SLH-DSA public key is refused at any height;
//   - set_operator pre-activation only decodes the encoding; the well-formedness
//     check is part of what verify_identity_v1 adds.
//
// Tightening set_operator's pre-activation arm would fail a transaction the live
// fleet accepts, which is the very divergence this test exists to prevent.
func TestVerifyIdentityPreForkRejectsMalformedSLHKey(t *testing.T) {
	owner := newVfyOp(t)
	op := newVfyOp(t)

	t.Run("deploy refuses an undecodable key", func(t *testing.T) {
		_, err := callVerify(t, newTestState(t), vfyCtx(owner.addr, 1, false), "deploy", map[string]any{
			"threshold": 1,
			"operator_identities": []map[string]any{
				{"operator": op.addr.String(), "slh_pubkey": "not-a-key"},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "slh_pubkey") {
			t.Fatalf("pre-activation deploy accepted an undecodable slh_pubkey: %v", err)
		}
	})

	t.Run("deploy refuses a decodable non-key", func(t *testing.T) {
		_, err := callVerify(t, newTestState(t), vfyCtx(owner.addr, 1, false), "deploy", map[string]any{
			"threshold": 1,
			"operator_identities": []map[string]any{
				{"operator": op.addr.String(), "slh_pubkey": "00ff"},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "slh_pubkey") {
			t.Fatalf("pre-activation deploy accepted 2 bytes as an SLH-DSA public key: %v", err)
		}
	})

	// set_operator needs a live instance to act on.
	newInstance := func(t *testing.T) (*state.StateDB, qcrypto.Address) {
		t.Helper()
		sdb := newTestState(t)
		rc, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, false), "deploy", map[string]any{
			"threshold": 1,
			"operators": []string{op.addr.String()},
		})
		if err != nil {
			t.Fatalf("deploy: %v", err)
		}
		return sdb, *rc.ContractAddress
	}

	t.Run("set_operator refuses an undecodable key", func(t *testing.T) {
		sdb, inst := newInstance(t)
		next := newVfyOp(t)
		_, err := callVerify(t, sdb, vfyCtx(owner.addr, 2, false), "set_operator", map[string]any{
			"verify":     inst.String(),
			"operator":   next.addr.String(),
			"slh_pubkey": "not-a-key",
			"active":     true,
		})
		if err == nil || !strings.Contains(err.Error(), "slh_pubkey") {
			t.Fatalf("pre-activation set_operator accepted an undecodable slh_pubkey: %v", err)
		}
		loaded, err := loadInstance(sdb, inst)
		if err != nil {
			t.Fatalf("load instance: %v", err)
		}
		if isOperator(loaded, next.addr) {
			t.Fatal("a refused set_operator still added the operator")
		}
	})

	t.Run("set_operator records a decodable non-key pre-activation and refuses it after", func(t *testing.T) {
		sdb, inst := newInstance(t)
		next := newVfyOp(t)
		call := func(identity bool, nonce uint64) error {
			_, err := callVerify(t, sdb, vfyCtx(owner.addr, nonce, identity), "set_operator", map[string]any{
				"verify":     inst.String(),
				"operator":   next.addr.String(),
				"slh_pubkey": "00ff",
				"active":     true,
			})
			return err
		}
		if err := call(false, 2); err != nil {
			t.Fatalf("pre-activation set_operator must accept whatever the fleet accepts: %v", err)
		}
		loaded, err := loadInstance(sdb, inst)
		if err != nil {
			t.Fatalf("load instance: %v", err)
		}
		got, ok := operatorSLH(loaded, next.addr)
		if !ok || !bytes.Equal(got, []byte{0x00, 0xff}) {
			t.Fatalf("pre-activation set_operator recorded %x, want the bytes the fleet records", got)
		}
		if err := call(true, 3); err == nil || !strings.Contains(err.Error(), "slh_pubkey") {
			t.Fatalf("%s must refuse a key that is not a well-formed SLH-DSA public key: %v",
				forks.VerifyIdentityV1, err)
		}
	})
}

// Restoring unconditional recording must not relax anything verify_identity_v1
// adds. These are the requirements that belong to the fork and must still only
// apply once it is active: identities are mandatory, they must cover every
// operator, they can stand in for the operators list, and set_operator must name
// a key.
func TestVerifyIdentityForkRequirementsStillApply(t *testing.T) {
	owner := newVfyOp(t)
	op := newVfyOp(t)
	other := newVfyOp(t)

	t.Run("identities are mandatory", func(t *testing.T) {
		params := map[string]any{"threshold": 1, "operators": []string{op.addr.String()}}
		if _, err := callVerify(t, newTestState(t), vfyCtx(owner.addr, 1, false), "deploy", params); err != nil {
			t.Fatalf("pre-activation deploy without identities must still work: %v", err)
		}
		_, err := callVerify(t, newTestState(t), vfyCtx(owner.addr, 1, true), "deploy", params)
		if err == nil || !strings.Contains(err.Error(), "operator_identities") {
			t.Fatalf("post-activation deploy without identities: %v", err)
		}
	})

	t.Run("identities must cover every operator", func(t *testing.T) {
		params := map[string]any{
			"threshold": 1,
			"operators": []string{op.addr.String(), other.addr.String()},
			"operator_identities": []map[string]any{
				{"operator": op.addr.String(), "slh_pubkey": hex.EncodeToString(op.slhPub.Bytes())},
			},
		}
		if _, err := callVerify(t, newTestState(t), vfyCtx(owner.addr, 1, false), "deploy", params); err != nil {
			t.Fatalf("pre-activation deploy with partial identities must still work: %v", err)
		}
		_, err := callVerify(t, newTestState(t), vfyCtx(owner.addr, 1, true), "deploy", params)
		if err == nil || !strings.Contains(err.Error(), "no registered SLH-DSA key") {
			t.Fatalf("post-activation deploy with an uncovered operator: %v", err)
		}
	})

	t.Run("identities stand in for the operators list", func(t *testing.T) {
		sdb := newTestState(t)
		rc, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, true), "deploy", map[string]any{
			"threshold": 1,
			"operator_identities": []map[string]any{
				{"operator": op.addr.String(), "slh_pubkey": hex.EncodeToString(op.slhPub.Bytes())},
			},
		})
		if err != nil {
			t.Fatalf("deploy: %v", err)
		}
		inst, err := loadInstance(sdb, *rc.ContractAddress)
		if err != nil {
			t.Fatalf("load instance: %v", err)
		}
		if len(inst.Operators) != 1 || inst.Operators[0] != op.addr {
			t.Fatalf("operators were not derived from the identities: %v", inst.Operators)
		}
	})

	t.Run("set_operator must name a key", func(t *testing.T) {
		sdb := newTestState(t)
		inst := deployVerify(t, sdb, owner, op, true)
		_, err := callVerify(t, sdb, vfyCtx(owner.addr, 3, true), "set_operator", map[string]any{
			"verify":   inst.String(),
			"operator": other.addr.String(),
			"active":   true,
		})
		if err == nil || !strings.Contains(err.Error(), "slh_pubkey required") {
			t.Fatalf("post-activation set_operator without a key: %v", err)
		}
	})
}
