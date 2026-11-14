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
	if err := r.ensureBoardWithPath(r.ctx(), token, true); err != nil {
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
