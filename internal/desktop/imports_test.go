package desktop

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNoBoardOwnerImports(t *testing.T) {
	forbidden := []string{
		"github.com/atqamz/hand/internal/cli",
		"github.com/atqamz/hand/internal/board",
		"github.com/atqamz/hand/internal/state",
	}
	const rootMyGo = "github.com/egoist/mygo"
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, root := range []string{".", filepath.Join("..", "..", "cmd", "hand-desktop")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			seen[filepath.ToSlash(filepath.Dir(path))] = true
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range f.Imports {
				p, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if root == "." && p == rootMyGo {
					t.Errorf("%s imports the root mygo package, which only cmd/hand-desktop may", filepath.ToSlash(path))
				}
				for _, bad := range forbidden {
					if p == bad || strings.HasPrefix(p, bad+"/") {
						t.Errorf("%s imports %s", filepath.ToSlash(path), p)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{".", "kit", "remote", "webhost", "../../cmd/hand-desktop"} {
		if !seen[dir] {
			t.Errorf("the import check read no Go file in %s", dir)
		}
	}
}
