package compiler

import (
	"strings"
	"testing"
)

// unknownStmt stands for a statement form added later. It exists only to prove
// the walker refuses what it does not understand.
type unknownStmt struct{}

func (*unknownStmt) isStmt() {}

// unknownExpr is the same for expressions.
type unknownExpr struct{}

func (*unknownExpr) isExpr() {}

// The call-graph walk decides whether a contract is recursive, and a recursive
// contract is what the generation budget exists to stop before code generation
// expands it. A statement form the walk does not descend into hides every call
// inside it, so the check would pass a contract it has not actually examined.
//
// addExpr already failed closed on an unknown expression. addStmts did not: it
// had no default, so a statement type added later would have been skipped in
// silence. Both now refuse.
func TestCallGraphWalkRefusesFormsItDoesNotUnderstand(t *testing.T) {
	t.Run("unknown statement", func(t *testing.T) {
		c := &contractNode{funcs: []*funcNode{
			{name: "f", isInternal: true, body: []stmt{&unknownStmt{}}},
		}}
		err := checkCallGraph(c)
		if err == nil {
			t.Fatal("the walk accepted a statement form it does not handle, so any call inside one would " +
				"never reach the cycle check")
		}
		if !strings.Contains(err.Error(), "does not handle statement") {
			t.Fatalf("error %q does not name the unhandled statement", err)
		}
		if !strings.Contains(err.Error(), "unknownStmt") {
			t.Fatalf("error %q does not name the type, so the next person cannot tell what to add", err)
		}
	})

	t.Run("unknown expression", func(t *testing.T) {
		c := &contractNode{funcs: []*funcNode{
			{name: "f", isInternal: true, body: []stmt{&returnStmt{value: &unknownExpr{}}}},
		}}
		err := checkCallGraph(c)
		if err == nil {
			t.Fatal("the walk accepted an expression form it does not handle")
		}
		if !strings.Contains(err.Error(), "does not handle") {
			t.Fatalf("error %q does not name the unhandled form", err)
		}
	})

	t.Run("break and continue are leaves, not unknowns", func(t *testing.T) {
		// They carry no expression, so skipping them is correct — but they must
		// be listed rather than defaulted, or this would fail.
		c := &contractNode{funcs: []*funcNode{
			{name: "f", isInternal: true, body: []stmt{
				&whileStmt{cond: &numberExpr{}, body: []stmt{&breakStmt{}, &continueStmt{}}},
			}},
		}}
		if err := checkCallGraph(c); err != nil {
			t.Fatalf("break/continue were treated as unknown forms: %v", err)
		}
	})
}
