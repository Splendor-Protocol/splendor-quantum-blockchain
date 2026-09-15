package block

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
)

func benchExecutor(tb testing.TB) (*Executor, *state.StateDB) {
	tb.Helper()
	sdb, err := state.NewStateDB(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	ex, err := NewExecutor(sdb, tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	return ex, sdb
}

func benchFund(tb testing.TB, sdb *state.StateDB, addr qcrypto.Address, amt int64) {
	tb.Helper()
	acc := types.NewAccount(addr)
	acc.AddBalance(big.NewInt(amt))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		tb.Fatal(err)
	}
}

func benchTransfer(tb testing.TB, from qcrypto.Address, priv *qcrypto.PrivateKey, to qcrypto.Address, amount int64, nonce uint64) *types.Transaction {
	tb.Helper()
	tx := types.NewTransfer(from, to, big.NewInt(amount), big.NewInt(testFee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		tb.Fatal(err)
	}
	return tx
}

// seedAccounts commits n funded accounts, each carrying a registered ML-DSA
// public key so the stored record has the size a real sender's has.
func seedAccounts(tb testing.TB, sdb *state.StateDB, n int, pk []byte) {
	tb.Helper()
	for i := 0; i < n; i++ {
		var a qcrypto.Address
		a[0] = byte(i >> 16)
		a[1] = byte(i >> 8)
		a[2] = byte(i)
		a[3] = 0xAB
		acc := types.NewAccount(a)
		acc.AddBalance(big.NewInt(1_000_000))
		acc.PublicKey = pk
		sdb.SetAccount(acc)
		if i%2000 == 1999 {
			if _, err := sdb.Commit(); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if _, err := sdb.Commit(); err != nil {
		tb.Fatal(err)
	}
}

// BenchmarkApplyOneTxBlock times the leader's build and the follower's apply
// of a one-transfer block against a state holding N accounts, and reports the
// per-phase cost that dominates each.
func BenchmarkApplyOneTxBlock(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 50_000} {
		b.Run(fmt.Sprintf("accounts=%d", n), func(b *testing.B) {
			pub, priv, _ := qcrypto.GenerateKeypair()
			from := qcrypto.DeriveAddress(pub)
			to := testAddr(0x55)
			vpub, vpriv, _ := qcrypto.GenerateKeypair()
			vaddr := qcrypto.DeriveAddress(vpub)

			exA, sdbA := benchExecutor(b)
			defer exA.Close()
			defer sdbA.Close()
			exB, sdbB := benchExecutor(b)
			defer exB.Close()
			defer sdbB.Close()
			seedAccounts(b, sdbA, n, pub.Bytes())
			seedAccounts(b, sdbB, n, pub.Bytes())
			benchFund(b, sdbA, from, 1_000_000)
			benchFund(b, sdbB, from, 1_000_000)

			var prev [32]byte
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tx := benchTransfer(b, from, priv, to, 1, uint64(i))
				t0 := time.Now()
				blk, err := exA.BuildProposal([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, prev, uint64(i+1))
				if err != nil {
					b.Fatal(err)
				}
				tBuild := time.Since(t0)
				t1 := time.Now()
				if err := exA.ExecuteBlock(blk); err != nil {
					b.Fatal(err)
				}
				tApplyLeader := time.Since(t1)
				t2 := time.Now()
				if err := exB.ValidateProposal(blk); err != nil {
					b.Fatal(err)
				}
				tValidate := time.Since(t2)
				t3 := time.Now()
				if err := exB.ExecuteBlock(blk); err != nil {
					b.Fatal(err)
				}
				tApplyFollower := time.Since(t3)
				prev = blk.Hash
				if i == b.N-1 {
					t4 := time.Now()
					_ = genesis.CheckNativeSupply(sdbB)
					tSupply := time.Since(t4)
					t5 := time.Now()
					_, _ = sdbB.PendingStateRoot()
					tRoot := time.Since(t5)
					b.ReportMetric(float64(tBuild.Milliseconds()), "build_ms")
					b.ReportMetric(float64(tApplyLeader.Milliseconds()), "apply_leader_ms")
					b.ReportMetric(float64(tValidate.Milliseconds()), "validate_ms")
					b.ReportMetric(float64(tApplyFollower.Milliseconds()), "apply_follower_ms")
					b.ReportMetric(float64(tSupply.Milliseconds()), "supply_check_ms")
					b.ReportMetric(float64(tRoot.Milliseconds()), "state_root_ms")
				}
			}
		})
	}
}
