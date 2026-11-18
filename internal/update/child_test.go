package update

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChildCmdCallsNewGroupBeforeKillGroupOnCancel(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "update.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "childCmd" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "proc" {
						calls = append(calls, sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
	if want := []string{"NewGroup", "KillGroupOnCancel"}; !slices.Equal(calls, want) {
		t.Fatalf("childCmd calls proc.%v, want proc.%v: Windows children need CREATE_NEW_PROCESS_GROUP from NewGroup, which KillGroupOnCancel does not set there", calls, want)
	}
}

func TestClipCapsTheBytesOfAnyLine(t *testing.T) {
	long := strings.Repeat("é", 1000)
	got := clip(long)
	if len(got) > 300+len("…") || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("clip kept %d bytes: %q", len(got), got)
	}
	if got := clip("one\ntwo\nthree\nfour"); got != "one\ntwo\nthree\n…" {
		t.Fatalf("clip = %q", got)
	}
}
