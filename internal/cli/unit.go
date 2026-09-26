package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

var units = map[string]struct{ description, args string }{
	"watch": {"Hand watcher", "watch"},
	"board": {"Hand board", "board --addr 127.0.0.1:7777"},
}

func init() {
	commands["unit"] = cmdUnit
}

func cmdUnit(r *runner, args []string) error {
	pos, err := parse(flags("unit"), args, 1)
	if err != nil {
		return err
	}
	u, ok := units[pos[0]]
	if !ok {
		return usageError{"unit: want watch or board, got " + pos[0]}
	}
	if r.home == "" {
		return usageError{"no home: pass --home DIR or set HAND_HOME"}
	}
	if _, err := os.Stat(r.dbPath()); err != nil {
		return fmt.Errorf("%w: no hand home at %s; run `hand init`", state.ErrNotFound, r.home)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := unitValue(r.home)
	if err != nil {
		return err
	}
	if exe, err = unitValue(exe); err != nil {
		return err
	}
	_, err = io.WriteString(r.env.Stdout, "[Unit]\n"+
		"Description="+u.description+" for "+home+"\n\n"+
		"[Service]\n"+
		`Environment="HAND_HOME=`+home+"\"\n"+
		`ExecStart="`+exe+`" `+u.args+"\n"+
		"Restart=on-failure\n"+
		"RestartSec=5\n\n"+
		"[Install]\n"+
		"WantedBy=default.target\n")
	return err
}

func unitValue(s string) (string, error) {
	if strings.ContainsFunc(s, func(c rune) bool { return c < ' ' || c == 0x7f || strings.ContainsRune(`"'\`, c) }) {
		return "", fmt.Errorf("%w: %q cannot go into a systemd unit; rename it without quotes, backslashes or control characters", state.ErrInvalid, s)
	}
	return strings.ReplaceAll(s, "%", "%%"), nil
}
