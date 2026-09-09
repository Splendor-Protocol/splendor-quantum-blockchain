package block

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A native-module call recorded zero gas, so the block gas budget never saw
// module work. Under modules_v2 every module call records a deterministic
// charge — base plus per-byte on its envelope — on success and on a SOFT
// failure alike, and a follower on the same schedule records the same figure.
// Below the fork the receipt still says zero, so replay is unchanged.

func moduleGasForks(at uint64) forks.Schedule {
	return forks.Schedule{forks.ModuleRevertV1: 1, forks.ModulesV2: at}
}

func TestModuleCallRecordsDeterministicGasUnderModulesV2(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	sinkA := &receiptSink{}
	exA.WithForks(moduleGasForks(1)).WithTxIndex(sinkA)
	fund(t, sdbA, from, 1_000_000)

	// One SOFT-failing module call (a transfer of a token nobody deployed) and
	// one successful deploy.
	failing := softFailingCall(t, from, priv, 0)
	deploy := signedModuleCall(t, from, priv, envelope(t, "qrc20", "deploy", map[string]any{
		"name": "Gas", "symbol": "GAS", "decimals": 18, "total_supply": "1000",
	}), 1)
	b1, err := exA.ProduceBlock([]*types.Transaction{failing, deploy}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 2 || len(sinkA.receipts) != 2 {
		t.Fatalf("block has %d txs and %d receipts, want 2 and 2", len(b1.Transactions), len(sinkA.receipts))
	}
	for i, rc := range sinkA.receipts {
		want := moduleGas(b1.Transactions[i].Data)
		if rc.GasUsed != want {
			t.Fatalf("receipt %d (status %v) gas = %d, want %d", i, rc.Status, rc.GasUsed, want)
		}
		if rc.GasUsed < ModuleGasBase {
			t.Fatalf("receipt %d gas %d is below the base %d", i, rc.GasUsed, ModuleGasBase)
		}
	}
	if sinkA.receipts[0].Status != types.TxStatusFailed || sinkA.receipts[1].Status != types.TxStatusSuccess {
		t.Fatalf("statuses = %v, %v; want failed then success", sinkA.receipts[0].Status, sinkA.receipts[1].Status)
	}

	// A follower on the same schedule records the same figures.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	sinkB := &receiptSink{}
	exB.WithForks(moduleGasForks(1)).WithTxIndex(sinkB)
	fund(t, sdbB, from, 1_000_000)
	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("follower apply: %v", err)
	}
	for i := range sinkA.receipts {
		if sinkB.receipts[i].GasUsed != sinkA.receipts[i].GasUsed {
			t.Fatalf("follower receipt %d gas %d != producer %d", i, sinkB.receipts[i].GasUsed, sinkA.receipts[i].GasUsed)
		}
	}
}

func TestModuleCallRecordsZeroGasBelowModulesV2(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	sink := &receiptSink{}
	ex.WithForks(moduleGasForks(1000)).WithTxIndex(sink) // scheduled, not yet active
	fund(t, sdb, from, 1_000_000)

	deploy := signedModuleCall(t, from, priv, envelope(t, "qrc20", "deploy", map[string]any{
		"name": "Gas", "symbol": "GAS", "decimals": 18, "total_supply": "1000",
	}), 0)
	if _, err := ex.ProduceBlock([]*types.Transaction{deploy}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(sink.receipts) != 1 || sink.receipts[0].GasUsed != 0 {
		t.Fatalf("below the fork a module call must record 0 gas, got %+v", sink.receipts)
	}
}
