package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

func init() {
	commands["unit"] = cmdUnit
}

func cmdUnit(r *runner, args []string) error {
	set := flags("unit")
	addr := set.String("addr", "127.0.0.1:7777", "listen address baked into the board unit")
	pos, err := parse(set, args, 1)
	if err != nil {
		return err
	}
	switch {
	case pos[0] == "watch":
		return usageError{"unit watch was removed: the board keeps watchers alive; run `hand unit board` and see Autostart in the README to migrate"}
	case pos[0] != "board":
		return usageError{"unit: want board, got " + pos[0]}
	case strings.ContainsFunc(*addr, unicode.IsSpace):
		return usageError{"unit: --addr must not contain spaces"}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	root := ""
	if r.env.Getenv("SECONDHAND_HOME") != "" {
		if root, err = fleet.Root(r.env.Getenv); err != nil {
			return err
		}
	}
	path := r.env.Getenv("PATH")
	if _, err := unitValue(path); err != nil {
		return fmt.Errorf("%w: PATH holds a quote, backslash or control character, which a systemd unit cannot hold; fix PATH, then run `hand unit` again", state.ErrInvalid)
	}
	vals := []string{exe, root, path}
	for i, v := range vals {
		if vals[i], err = unitValue(v); err != nil {
			return err
		}
	}
	exe, root, path = vals[0], vals[1], vals[2]
	service := ""
	if root != "" {
		service += `Environment="SECONDHAND_HOME=` + root + "\"\n"
	}
	if path != "" {
		service += `Environment="PATH=` + path + "\"\n"
	}
	_, err = io.WriteString(r.env.Stdout, "[Unit]\n"+
		"Description=Hand board\n"+
		"After=graphical-session.target\n\n"+
		"[Service]\n"+
		service+
		`ExecStart="`+exe+`" board --addr `+*addr+"\n"+
		"Restart=on-failure\n"+
		"RestartSec=5\n"+
		"RestartPreventExitStatus=3\n\n"+
		"[Install]\n"+
		"WantedBy=graphical-session.target\n")
	return err
}

func unitValue(s string) (string, error) {
	if strings.ContainsFunc(s, func(c rune) bool { return c < ' ' || c == 0x7f || strings.ContainsRune(`"'\`, c) }) {
		return "", fmt.Errorf("%w: %q cannot go into a systemd unit; rename it without quotes, backslashes or control characters", state.ErrInvalid, s)
	}
	return strings.ReplaceAll(s, "%", "%%"), nil
}
