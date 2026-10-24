package harness

import (
	"fmt"
	"path/filepath"
	"slices"
	"unicode/utf16"

	"github.com/atqamz/hand/internal/state"
)

const MaxWindowsCommandLine = 32766

func CommandLineLen(argv []string) int {
	n := 0
	for i, a := range argv {
		if i > 0 {
			n++
		}
		n += quotedLen(utf16.Encode([]rune(a)))
	}
	return n
}

func quotedLen(arg []uint16) int {
	if len(arg) > 0 && !slices.ContainsFunc(arg, func(u uint16) bool { return u == ' ' || u == '\t' || u == '\n' || u == '\v' || u == '"' }) {
		return len(arg)
	}
	n, slashes := 2, 0
	for _, u := range arg {
		switch u {
		case '\\':
			slashes++
			continue
		case '"':
			n += 2*slashes + 2
		default:
			n += slashes + 1
		}
		slashes = 0
	}
	return n + 2*slashes
}

func checkCommandLine(goos string, argv []string) error {
	if goos != "windows" {
		return nil
	}
	if n := CommandLineLen(argv); n > MaxWindowsCommandLine {
		return fmt.Errorf("%w: starting %s takes a %d character command line once quoted, and Windows allows %d; shorten the prompt, or write the long text to a file and make the prompt say to read it", state.ErrInvalid, filepath.Base(argv[0]), n, MaxWindowsCommandLine)
	}
	return nil
}
