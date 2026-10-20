package vm

import (
	"errors"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

func alwaysCallV1(uint64) bool { return true }

func neverCallV1(uint64) bool { return false }

func createTwiceLoop() (code []byte, createPC int) {
	code = []byte{OpPUSH1, 0x00, OpPUSH1, 0x00, OpMSTORE, OpPUSH1, 0x02}
	loop := len(code)
	code = append(code, OpJUMPDEST)
	code = append(code, OpPUSH1, 0x01, OpPUSH1, 0x1F, OpPUSH1, 0x00)
	createPC = len(code)
	code = append(code, OpCREATE, OpPOP)
	code = append(code, OpPUSH1, 0x01, OpSUB, OpDUP1)
	code = append(code, OpPUSH1, byte(loop), OpSWAP1, OpJUMPI, OpSTOP)
	return code, createPC
}

func create2Stop() []byte {
	return []byte{
		OpPUSH1, 0x00, OpPUSH1, 0x00, OpMSTORE,
		OpPUSH1, 0x00, // value
		OpPUSH1, 0x01, // size
		OpPUSH1, 0x1F, // offset
		OpPUSH1, 0x01, // salt
		OpCREATE2,
		OpSTOP,
	}
}

func countContracts(t *testing.T, sdb *state.StateDB, addrs ...qcrypto.Address) int {
	t.Helper()
	n := 0
	for _, a := range addrs {
		acc, err := sdb.GetAccount(a)
		if err != nil {
			t.Fatalf("GetAccount %s: %v", a, err)
		}
		if acc.IsContract {
			n++
		}
	}
	return n
}

// Under vm_call_v1, DELEGATECALL inherits the enclosing CallValue so a library
// that reads CALLVALUE sees the same attached funds as the proxy.
func TestDelegateCallInheritsValueUnderCallV1(t *testing.T) {
	sdb := newTestState(t)
	proxy := addr(0x70)
	library := addr(0x71)

	installContract(t, sdb, library, concat(sstoreSlot(1), []byte{OpCALLVALUE, OpSSTORE}), 0)
	installContract(t, sdb, proxy, callCode(OpDELEGATECALL, library, 0, 200_000), 0)

	e := NewEngine(sdb)
	e.SetCallRules(alwaysCallV1)
	res, _ := e.CallWithContext(addr(0x01), addr(0x01), proxy, nil, big.NewInt(777), 1, 0, false)
	if res.Err != nil {
		t.Fatalf("proxy call failed: %v", res.Err)
	}
	raw, _ := sdb.GetStorageValue(proxy, bigToSlot(big.NewInt(1)))
	if got := new(big.Int).SetBytes(raw); got.Cmp(big.NewInt(777)) != 0 {
		t.Fatalf("proxy slot 1 = %s, want 777 (DELEGATECALL must inherit CallValue)", got)
	}
}

func TestDelegateCallValueIsZeroWithoutCallV1(t *testing.T) {
	sdb := newTestState(t)
	proxy := addr(0x72)
	library := addr(0x73)

	installContract(t, sdb, library, concat(sstoreSlot(1), []byte{OpCALLVALUE, OpSSTORE}), 0)
	installContract(t, sdb, proxy, callCode(OpDELEGATECALL, library, 0, 200_000), 0)

	e := NewEngine(sdb)
	e.SetCallRules(neverCallV1)
	res, _ := e.CallWithContext(addr(0x01), addr(0x01), proxy, nil, big.NewInt(777), 1, 0, false)
	if res.Err != nil {
		t.Fatalf("proxy call failed: %v", res.Err)
	}
	raw, _ := sdb.GetStorageValue(proxy, bigToSlot(big.NewInt(1)))
	if got := new(big.Int).SetBytes(raw); got.Sign() != 0 {
		t.Fatalf("pre-fork DELEGATECALL CALLVALUE = %s, want 0", got)
	}
}

// A factory that hits the same CREATE opcode twice must derive two children
// once the creator nonce is persistent. Pre-fork both uses share the PC.
func TestCreateLoopTwoChildrenUnderCallV1(t *testing.T) {
	sdb := newTestState(t)
	factory := addr(0x74)
	code, createPC := createTwiceLoop()
	installContract(t, sdb, factory, code, 0)

	e := NewEngine(sdb)
	e.SetCallRules(alwaysCallV1)
	res, _ := e.CallWithContext(addr(0x01), addr(0x01), factory, nil, big.NewInt(0), 1, 0, false)
	if res.Err != nil {
		t.Fatalf("factory call failed: %v", res.Err)
	}

	first := derivedDeployAddr(factory, 0)
	second := derivedDeployAddr(factory, 1)
	// Execute increments pc before the opcode body, so the pre-fork nonce is createPC+1.
	pcChild := derivedDeployAddr(factory, uint64(createPC+1))
	if countContracts(t, sdb, first, second) != 2 {
		t.Fatalf("want two children at nonce 0 and 1; pc-derived occupied=%v",
			countContracts(t, sdb, pcChild) == 1)
	}
	fac, err := sdb.GetAccount(factory)
	if err != nil {
		t.Fatalf("factory account: %v", err)
	}
	if fac.Nonce != 2 {
		t.Fatalf("factory nonce = %d, want 2", fac.Nonce)
	}
}

func TestCreateLoopSamePCWithoutCallV1(t *testing.T) {
	sdb := newTestState(t)
	factory := addr(0x75)
	code, createPC := createTwiceLoop()
	installContract(t, sdb, factory, code, 0)

	e := NewEngine(sdb)
	res, _ := e.CallWithContext(addr(0x01), addr(0x01), factory, nil, big.NewInt(0), 1, 0, false)
	if res.Err != nil {
		t.Fatalf("factory call failed: %v", res.Err)
	}
	pcChild := derivedDeployAddr(factory, uint64(createPC+1))
	nonce0 := derivedDeployAddr(factory, 0)
	if countContracts(t, sdb, pcChild) != 1 {
		t.Fatalf("pre-fork CREATE must deploy exactly once at pc+1=%d", createPC+1)
	}
	if countContracts(t, sdb, nonce0) != 0 {
		t.Fatal("pre-fork CREATE must not use the account nonce")
	}
}

func TestDeployFailClosedOnAccountReadUnderCallV1(t *testing.T) {
	sdb := newTestState(t)
	e := NewEngine(sdb)
	e.SetCallRules(alwaysCallV1)
	sdb.FailNextAccountRead(errors.New("injected account read"))
	if _, err := e.DeployAt(addr(0xDE), []byte{OpSTOP}, 1, 1); err == nil {
		t.Fatal("DeployAt under vm_call_v1 succeeded despite account-read failure")
	}
}

func TestDeployFailOpenOnAccountReadWithoutCallV1(t *testing.T) {
	sdb := newTestState(t)
	e := NewEngine(sdb)
	sdb.FailNextAccountRead(errors.New("injected account read"))
	if _, err := e.Deploy(addr(0xDE), []byte{OpSTOP}, 3); err != nil {
		t.Fatalf("pre-fork Deploy should continue on account-read error, got %v", err)
	}
}

func TestCreate2FailClosedOnAccountReadUnderCallV1(t *testing.T) {
	sdb := newTestState(t)
	factory := addr(0x76)
	installContract(t, sdb, factory, create2Stop(), 0)

	e := NewEngine(sdb)
	e.SetCallRules(alwaysCallV1)
	sdb.FailNextAccountRead(errors.New("injected account read"))
	res, err := e.CallWithContext(addr(0x01), addr(0x01), factory, nil, big.NewInt(0), 1, 0, false)
	if err == nil && (res == nil || res.Err == nil) {
		t.Fatal("CREATE2 under vm_call_v1 succeeded despite account-read failure")
	}
}
