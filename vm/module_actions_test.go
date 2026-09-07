package vm

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// dispatchCases parses a source file and returns, for the named function, the
// string-literal cases of the switch on `action`, plus — for the top-level
// dispatcher — the module name → handler function mapping from its switch on
// mc.Module. Reading the source rather than probing behaviour means a new
// `case` is caught even when its handler needs state this test cannot build.
func dispatchCases(t *testing.T, file string) (modules map[string]string, actionCases map[string][]string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	modules = map[string]string{}
	actionCases = map[string][]string{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			tag := ""
			switch tg := sw.Tag.(type) {
			case *ast.Ident:
				tag = tg.Name
			case *ast.SelectorExpr:
				tag = tg.Sel.Name
			}
			for _, c := range sw.Body.List {
				cc := c.(*ast.CaseClause)
				for _, e := range cc.List {
					lit, ok := e.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					s, _ := strconv.Unquote(lit.Value)
					switch tag {
					case "Module":
						// case "qrc20": return dispatchQRC20(...)
						for _, st := range cc.Body {
							ret, ok := st.(*ast.ReturnStmt)
							if !ok {
								continue
							}
							for _, r := range ret.Results {
								if call, ok := r.(*ast.CallExpr); ok {
									if id, ok := call.Fun.(*ast.Ident); ok {
										modules[s] = id.Name
									}
								}
							}
						}
					case "action":
						actionCases[fn.Name.Name] = append(actionCases[fn.Name.Name], s)
					}
				}
			}
			return true
		})
	}
	return modules, actionCases
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Every `case` in the write and read dispatch switches must be listed in
// KnownModuleActions, on the right side, and nothing may be listed that no
// switch serves. Admission trusts this table: an action missing from it is
// refused at /tx/submit, and an action listed on the wrong side is either
// served by /call without a signature or lets a read into a TxCall where the
// dispatch treats it as HARD and jams the sender.
func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return in
	}
	cp := append([]string{}, in...)
	sort.Strings(cp)
	out := cp[:0]
	var prev string
	for i, s := range cp {
		if i == 0 || s != prev {
			out = append(out, s)
		}
		prev = s
	}
	return out
}

func mergeActionCases(dst map[string][]string, extra map[string][]string) {
	for k, v := range extra {
		dst[k] = append(dst[k], v...)
	}
}

func TestKnownModuleActionsCoverEveryDispatchCase(t *testing.T) {
	writeModules, writeCases := dispatchCases(t, "module_router.go")
	readModules, readCases := dispatchCases(t, "module_router_read.go")
	_, extraWrite := dispatchCases(t, "extra_dispatch.go")
	_, extraRead := dispatchCases(t, "extra_dispatch.go")
	_, verifyWrite := dispatchCases(t, "verify.go")
	_, verifyRead := dispatchCases(t, "verify.go")
	mergeActionCases(writeCases, extraWrite)
	mergeActionCases(writeCases, verifyWrite)
	mergeActionCases(readCases, extraRead)
	mergeActionCases(readCases, verifyRead)
	for k, v := range writeCases {
		writeCases[k] = uniqueSorted(v)
	}
	for k, v := range readCases {
		readCases[k] = uniqueSorted(v)
	}

	if got, want := sortedKeys(writeModules), sortedKeys(KnownModuleActions); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DispatchModuleCall routes modules %v, KnownModuleActions lists %v", got, want)
	}
	if got, want := sortedKeys(readModules), sortedKeys(KnownModuleActions); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DispatchModuleReadCall routes modules %v, KnownModuleActions lists %v", got, want)
	}

	for module, acts := range KnownModuleActions {
		gotW := append([]string{}, writeCases[writeModules[module]]...)
		sort.Strings(gotW)
		if want := sortedKeys(acts.Writes); strings.Join(gotW, ",") != strings.Join(want, ",") {
			t.Errorf("%s: write dispatch cases %v, KnownModuleActions.Writes %v", module, gotW, want)
		}
		gotR := append([]string{}, readCases[readModules[module]]...)
		sort.Strings(gotR)
		if want := sortedKeys(acts.Reads); strings.Join(gotR, ",") != strings.Join(want, ",") {
			t.Errorf("%s: read dispatch cases %v, KnownModuleActions.Reads %v", module, gotR, want)
		}
		for a := range acts.Writes {
			if acts.Reads[a] {
				t.Errorf("%s: %q is listed as both a write and a read", module, a)
			}
		}
		if len(gotW) == 0 || len(gotR) == 0 {
			t.Errorf("%s: could not find its dispatch switches (write=%v read=%v)", module, writeModules[module], readModules[module])
		}
	}
}

// The executor tells a HARD failure (unappliable, block rejected) from a SOFT
// one (failed receipt) by the wrapper alone, so the four static classes must
// carry it and nothing an action returns may.
func TestHardModuleErrorClassification(t *testing.T) {
	sdb := newTestState(t)
	caller := addr(0x33)

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"unparseable envelope", []byte("nonsense")},
		{"unknown module", moduleData(t, "nope", "x", struct{}{})},
		{"unknown qrc20 action", moduleData(t, "qrc20", "explode", struct{}{})},
		{"unknown dex action", moduleData(t, "dex", "explode", struct{}{})},
		{"read action in a write call", moduleData(t, "qrc20", "balance_of", map[string]string{"token": addr(0x44).String(), "owner": caller.String()})},
	} {
		err := DispatchModuleCall(sdb, ModuleContext{Caller: caller}, tc.data, &types.Receipt{})
		if err == nil {
			t.Fatalf("%s: expected an error", tc.name)
		}
		if !IsHardModuleError(err) {
			t.Errorf("%s: %v is not HARD", tc.name, err)
		}
	}

	// What an action itself returns is SOFT: a transfer from an empty balance.
	soft := DispatchModuleCall(sdb, ModuleContext{Caller: caller}, moduleData(t, "qrc20", "transfer", qrc20TokenAction{
		Token: addr(0x44).String(), To: addr(0x55).String(), Amount: "1",
	}), &types.Receipt{})
	if soft == nil {
		t.Fatal("transfer from an empty balance should fail")
	}
	if IsHardModuleError(soft) {
		t.Errorf("an action's own error must be SOFT, got HARD: %v", soft)
	}
	// Bad params are the action's own complaint too, not a static class.
	badParams := DispatchModuleCall(sdb, ModuleContext{Caller: caller}, []byte(`{"module":"qrc20","action":"transfer","params":"[]"}`), &types.Receipt{})
	if badParams == nil || IsHardModuleError(badParams) {
		t.Errorf("bad params must be a SOFT error, got %v", badParams)
	}
	if IsHardModuleError(nil) {
		t.Error("nil is not HARD")
	}
	if hard(nil) != nil {
		t.Error("hard(nil) must stay nil")
	}
}

// requireFork is HARD and carries the sentinel, so an action reached before
// its height is unappliable rather than a failed receipt: a follower that
// lacks the fork must reject the block, never apply it differently.
func TestRequireForkGate(t *testing.T) {
	const name = "probe_fork"
	if err := requireFork(ModuleContext{Height: 5}, name); err == nil {
		t.Fatal("nil schedule must read never")
	} else if !errors.Is(err, ErrForkInactive) || !IsHardModuleError(err) {
		t.Fatalf("want HARD ErrForkInactive, got %v", err)
	}
	sched := forks.Schedule{name: 10}
	if err := requireFork(ModuleContext{Height: 9, Forks: sched}, name); err == nil {
		t.Fatal("one block early must be inactive")
	} else if !strings.Contains(err.Error(), "height 9") {
		t.Fatalf("message must carry the block height: %v", err)
	}
	if err := requireFork(ModuleContext{Height: 10, Forks: sched}, name); err != nil {
		t.Fatalf("at the scheduled height: %v", err)
	}
}

// PreflightModuleCall mirrors the fork gate for admission, nil schedule
// included: consensus reads nil as "never" (requireFork), so a schedule-less
// executor would drop the tx every block and admitting it would pin the
// sender's nonce. Only height 0 — the executor-less /estimate path — skips the
// gate. The probe is a temporary module entry so the test does not depend on
// which shipped actions are gated.
func TestPreflightModuleCallForkGate(t *testing.T) {
	KnownModuleActions["probe"] = ModuleActions{
		Writes: map[string]string{"gated": "probe_fork", "open": ""},
		Reads:  map[string]bool{},
	}
	t.Cleanup(func() { delete(KnownModuleActions, "probe") })

	gated := moduleData(t, "probe", "gated", struct{}{})
	open := moduleData(t, "probe", "open", struct{}{})
	sched := forks.Schedule{"probe_fork": 10}

	if _, err := PreflightModuleCall(gated, nil, 1); err == nil || !errors.Is(err, ErrForkInactive) {
		t.Fatalf("nil schedule reads never, like consensus: want ErrForkInactive, got %v", err)
	}
	if _, err := PreflightModuleCall(gated, sched, 0); err != nil {
		t.Fatalf("height 0 (no executor) must not gate: %v", err)
	}
	if _, err := PreflightModuleCall(gated, nil, 0); err != nil {
		t.Fatalf("height 0 with a nil schedule must not gate: %v", err)
	}
	if _, err := PreflightModuleCall(gated, sched, 9); err == nil || !errors.Is(err, ErrForkInactive) {
		t.Fatalf("one block early: want ErrForkInactive, got %v", err)
	}
	if _, err := PreflightModuleCall(gated, sched, 10); err != nil {
		t.Fatalf("at the scheduled height: %v", err)
	}
	if _, err := PreflightModuleCall(open, sched, 1); err != nil {
		t.Fatalf("an ungated action must pass under any schedule: %v", err)
	}
}
