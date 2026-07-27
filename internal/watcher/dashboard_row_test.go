package watcher

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// kindConstants reads the Kind vocabulary out of events.go rather than repeating
// it here, so adding a constant there cannot leave this enumeration behind.
func kindConstants(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "events.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Kind") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				kinds[name.Name] = value
			}
		}
	}
	if len(kinds) == 0 {
		t.Fatal("no Kind constants found in events.go")
	}
	return kinds
}

// Every event-driven dashboard field is decided in one place, so a new event kind
// cannot silently inherit a neighbour's disposition: the state column and the
// Pending Decisions slot are both answered per kind, and an unhandled kind is an
// error rather than a row that writes the state and leaves a stale question standing.
func TestEveryEventKindHasADashboardRowDisposition(t *testing.T) {
	for name, kind := range kindConstants(t) {
		for _, verified := range []bool{false, true} {
			if _, err := dashboardRowFor(&Event{TaskID: "task-1", Kind: kind, Verified: verified}, "1h"); err != nil {
				t.Errorf("dashboardRowFor(%s, verified=%v) = %v, want an explicit disposition", name, verified, err)
			}
		}
	}
}

func TestDashboardRowRejectsAnUnknownEventKind(t *testing.T) {
	if _, err := dashboardRowFor(&Event{TaskID: "task-1", Kind: "invented-kind"}, "1h"); err == nil {
		t.Fatal("dashboardRowFor(invented-kind) = nil error, want a refusal")
	}
}
