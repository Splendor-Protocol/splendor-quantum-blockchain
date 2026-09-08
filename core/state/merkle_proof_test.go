package state

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// stateWithAccounts commits n funded accounts and returns them with the root.
func stateWithAccounts(t *testing.T, n int) (*StateDB, []qcrypto.Address, [32]byte) {
	t.Helper()
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	addrs := make([]qcrypto.Address, 0, n)
	for i := 0; i < n; i++ {
		a := mkAddr(byte(i + 1))
		acc := types.NewAccount(a)
		acc.AddBalance(big.NewInt(int64(i + 1)))
		sdb.SetAccount(acc)
		addrs = append(addrs, a)
	}
	root, err := sdb.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return sdb, addrs, root
}

// foldByParity is the rule the proof endpoint used to describe: sibling i is on
// the right when bit i of the leaf index is 0. It is wrong once an odd node has
// been carried up without a sibling.
func foldByParity(leaf [32]byte, siblings [][32]byte, leafIndex int) [32]byte {
	node := leaf
	for i, sib := range siblings {
		if (leafIndex>>uint(i))&1 == 0 {
			node = internalHash(node, sib)
		} else {
			node = internalHash(sib, node)
		}
	}
	return node
}

// Every leaf of every small tree proves against the committed root when the
// sides are explicit — even and odd leaf counts alike, and at every level.
func TestMerkleProofPathVerifiesForEveryLeafCount(t *testing.T) {
	for n := 1; n <= 9; n++ {
		sdb, addrs, root := stateWithAccounts(t, n)
		for _, a := range addrs {
			leaf, path, idx, count, err := sdb.MerkleProofPath(a)
			if err != nil {
				t.Fatalf("n=%d %s: %v", n, a, err)
			}
			if count != n {
				t.Fatalf("n=%d: leaf count = %d", n, count)
			}
			if idx < 0 || idx >= n {
				t.Fatalf("n=%d: leaf index = %d", n, idx)
			}
			if !VerifyMerkleProof(root, leaf, path) {
				t.Fatalf("n=%d leaf %d: the path does not reach the root", n, idx)
			}
			// The legacy shape is the same digests, in the same order.
			leaf2, siblings, err := sdb.MerkleProof(a)
			if err != nil || leaf2 != leaf || len(siblings) != len(path) {
				t.Fatalf("n=%d: MerkleProof disagrees with MerkleProofPath (%v)", n, err)
			}
			for i := range path {
				if siblings[i] != path[i].Sibling {
					t.Fatalf("n=%d leaf %d: sibling %d differs", n, idx, i)
				}
			}
			// Flipping any one side must break the proof.
			for i := range path {
				flipped := append([]ProofStep(nil), path...)
				flipped[i].SiblingOnLeft = !flipped[i].SiblingOnLeft
				if VerifyMerkleProof(root, leaf, flipped) {
					t.Fatalf("n=%d leaf %d: a wrong side at step %d still verified", n, idx, i)
				}
			}
		}
	}
}

// Why the sides are in the proof: with three leaves the third is carried up
// alone, then paired on the RIGHT of the (0,1) parent. Its index is even, so
// the parity rule puts it on the left and reconstructs the wrong root.
func TestMerkleProofSidesAreNotLeafIndexParity(t *testing.T) {
	sdb, addrs, root := stateWithAccounts(t, 3)
	var odd qcrypto.Address
	for _, a := range addrs {
		_, _, idx, _, err := sdb.MerkleProofPath(a)
		if err != nil {
			t.Fatal(err)
		}
		if idx == 2 {
			odd = a
		}
	}
	leaf, path, idx, _, err := sdb.MerkleProofPath(odd)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 1 || !path[0].SiblingOnLeft {
		t.Fatalf("path for the carried leaf = %+v, want one step with the sibling on the left", path)
	}
	if !VerifyMerkleProof(root, leaf, path) {
		t.Fatal("explicit sides must verify")
	}
	siblings := [][32]byte{path[0].Sibling}
	if foldByParity(leaf, siblings, idx) == root {
		t.Fatal("the parity rule should NOT reconstruct the root here — if it does, the test tree changed shape")
	}
}

// An account that is not in committed state has no proof.
func TestMerkleProofPathUnknownAccount(t *testing.T) {
	sdb, _, _ := stateWithAccounts(t, 2)
	if _, _, _, _, err := sdb.MerkleProofPath(mkAddr(0xEE)); err == nil {
		t.Fatal("expected an error for an account outside committed state")
	}
}
