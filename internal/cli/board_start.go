package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/toon"
)

const boardStartUsage = "usage: hand board start"

func cmdBoardStart(r *runner, args []string) error {
	set := flags("board start")
	if err := set.Parse(args); err != nil {
		return helpOr(set, err, usageError{boardStartUsage})
	}
	if set.NArg() != 0 {
		return usageError{boardStartUsage}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	token, err := boardToken(r.home)
	if err != nil {
		return err
	}
	if err := r.ensureBoard(r.ctx(), token); err != nil {
		return err
	}
	var d toon.Doc
	d.Field("board", "running")
	return r.print(&d)
}

func boardPathFile(root string) string { return filepath.Join(root, "board.path") }

func recordedBoardPath(root string) string {
	b, err := os.ReadFile(boardPathFile(root))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (r *runner) boardStartPath() string {
	path := recordedBoardPath(r.root)
	if r.terminal() {
		if current := r.env.Getenv("PATH"); current != "" {
			path = current
			_ = writeFile(r.root, boardPathFile(r.root), current+"\n")
		}
	}
	if path == "" {
		path = r.env.Getenv("PATH")
	}
	return path
}
