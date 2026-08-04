// Command commentlint enforces the two checkable comment rules CONTRIBUTING states:
// a comment may not open with the identifier it documents, and a comment block may
// not exceed three lines.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxBlockLines = 3

type finding struct {
	pos token.Position
	msg string
}

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"."}
	}
	var findings []finding
	for _, root := range roots {
		f, err := checkTree(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "commentlint:", err)
			os.Exit(2)
		}
		findings = append(findings, f...)
	}
	for _, f := range findings {
		fmt.Printf("%s:%d:%d: %s\n", f.pos.Filename, f.pos.Line, f.pos.Column, f.msg)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "commentlint: %d violations\n", len(findings))
		os.Exit(1)
	}
}

func checkTree(root string) ([]finding, error) {
	fset := token.NewFileSet()
	var findings []finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := checkFile(fset, path)
		if err != nil {
			return err
		}
		findings = append(findings, f...)
		return nil
	})
	return findings, err
}

func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func checkFile(fset *token.FileSet, path string) ([]finding, error) {
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	if ast.IsGenerated(f) {
		return nil, nil
	}
	return check(fset, f, path), nil
}

func check(fset *token.FileSet, f *ast.File, path string) []finding {
	var findings []finding
	inTest := strings.HasSuffix(path, "_test.go")

	// Go's doc convention requires an exported doc comment to open with the identifier,
	// so rule 1 can only apply where godoc does not reach: unexported declarations,
	// everything in _test.go, and comments inside function bodies.
	subject := func(doc *ast.CommentGroup, name *ast.Ident) {
		if doc == nil || name == nil || (!inTest && name.IsExported()) {
			return
		}
		if opensWith(doc, name.Name) {
			findings = append(findings, finding{fset.Position(doc.Pos()), rule1msg(name.Name)})
		}
	}

	declared := map[int][]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			subject(d.Doc, d.Name)
		case *ast.TypeSpec:
			subject(d.Doc, d.Name)
		case *ast.Field:
			for _, name := range d.Names {
				subject(d.Doc, name)
			}
		case *ast.ValueSpec:
			for _, name := range d.Names {
				subject(d.Doc, name)
			}
		case *ast.GenDecl:
			// A single-spec declaration carries its comment on the GenDecl, not the spec.
			if len(d.Specs) != 1 {
				return true
			}
			switch s := d.Specs[0].(type) {
			case *ast.TypeSpec:
				subject(d.Doc, s.Name)
			case *ast.ValueSpec:
				for _, name := range s.Names {
					subject(d.Doc, name)
				}
			}
		case *ast.AssignStmt:
			if d.Tok != token.DEFINE {
				return true
			}
			for _, lhs := range d.Lhs {
				addDeclared(fset, declared, lhs)
			}
		case *ast.RangeStmt:
			addDeclared(fset, declared, d.Key)
			addDeclared(fset, declared, d.Value)
		case *ast.LabeledStmt:
			addDeclared(fset, declared, d.Label)
		}
		return true
	})

	for _, g := range f.Comments {
		// A package doc comment is a documentation surface godoc renders, not the
		// in-body volume the rules target, so neither rule applies to it.
		if g == f.Doc {
			continue
		}
		for _, name := range declared[fset.Position(g.End()).Line+1] {
			if opensWith(g, name) {
				findings = append(findings, finding{fset.Position(g.Pos()), rule1msg(name)})
			}
		}
		if n := blockLines(fset, g); n > maxBlockLines {
			findings = append(findings, finding{
				fset.Position(g.Pos()),
				fmt.Sprintf("rule 2: comment block is %d lines, the limit is %d", n, maxBlockLines),
			})
		}
	}

	// A blank line above a doc comment does not start a new block either: splitting a long
	// block that way leaves the same prose in front of the same declaration, and above an
	// exported one it silently drops the first half out of godoc.
	docs := docGroups(f)
	for i := 1; i < len(f.Comments); i++ {
		g, prev := f.Comments[i], f.Comments[i-1]
		if !docs[g] || docs[prev] || fset.Position(g.Pos()).Column != 1 {
			continue
		}
		if fset.Position(g.Pos()).Line-fset.Position(prev.End()).Line != 2 {
			continue
		}
		if n := blockLines(fset, prev) + blockLines(fset, g); n > maxBlockLines {
			findings = append(findings, finding{
				fset.Position(prev.Pos()),
				fmt.Sprintf("rule 2: two blocks a blank line apart document one declaration, %d lines together, the limit is %d", n, maxBlockLines),
			})
		}
	}
	return findings
}

func docGroups(f *ast.File) map[*ast.CommentGroup]bool {
	docs := map[*ast.CommentGroup]bool{}
	add := func(g *ast.CommentGroup) {
		if g != nil {
			docs[g] = true
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			add(d.Doc)
		case *ast.GenDecl:
			add(d.Doc)
		case *ast.TypeSpec:
			add(d.Doc)
		case *ast.ValueSpec:
			add(d.Doc)
		case *ast.Field:
			add(d.Doc)
		}
		return true
	})
	return docs
}

func rule1msg(name string) string {
	return fmt.Sprintf("rule 1: comment opens with the identifier it documents (%q)", name)
}

func addDeclared(fset *token.FileSet, declared map[int][]string, e ast.Expr) {
	id, ok := e.(*ast.Ident)
	if !ok || id.Name == "_" {
		return
	}
	line := fset.Position(id.Pos()).Line
	declared[line] = append(declared[line], id.Name)
}

func opensWith(g *ast.CommentGroup, name string) bool {
	for _, c := range g.List {
		if isDirective(c.Text) {
			continue
		}
		return leadingIdent(c.Text) == name
	}
	return false
}

var identRun = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)

func leadingIdent(text string) string {
	text = strings.TrimPrefix(strings.TrimPrefix(text, "//"), "/*")
	return identRun.FindString(strings.TrimSpace(text))
}

func blockLines(fset *token.FileSet, g *ast.CommentGroup) int {
	n := 0
	for _, c := range g.List {
		if isDirective(c.Text) {
			continue
		}
		n += fset.Position(c.End()).Line - fset.Position(c.Pos()).Line + 1
	}
	return n
}

// Directives are not prose: the go toolchain's `//word:arg` form plus the linter
// suppressions that conventionally carry a leading space.
var directive = regexp.MustCompile(`^//(\s*#(no|go)sec|\+build |[a-z0-9]+:[^ ]|nolint\b)`)

func isDirective(text string) bool { return directive.MatchString(text) }
