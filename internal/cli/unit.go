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

var units = map[string]string{
	"watch": "Hand watcher",
	"board": "Hand board",
}

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
	description, ok := units[pos[0]]
	if !ok {
		return usageError{"unit: want watch or board, got " + pos[0]}
	}
	command := pos[0]
	switch {
	case pos[0] == "board" && strings.ContainsFunc(*addr, unicode.IsSpace):
		return usageError{"unit: --addr must not contain spaces"}
	case pos[0] == "board":
		command += " --addr " + *addr
	case *addr != set.Lookup("addr").DefValue:
		return usageError{"unit: --addr is only for the board"}
	}
	vals := []string{"", "", command}
	if pos[0] == "watch" {
		st, err := r.store()
		if err != nil {
			return err
		}
		if err := st.Close(); err != nil {
			return err
		}
		vals[0], vals[1] = r.fleet.Name, r.home
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
	vals = append(vals, exe, root, path)
	for i, v := range vals {
		if vals[i], err = unitValue(v); err != nil {
			return err
		}
	}
	name, home, command, exe, root := vals[0], vals[1], vals[2], vals[3], vals[4]
	path = vals[5]
	service := ""
	if home != "" {
		description += " for " + name + " (" + home + ")"
		service = `Environment="HAND_HOME=` + home + "\"\n"
	}
	if root != "" {
		service += `Environment="SECONDHAND_HOME=` + root + "\"\n"
	}
	if path != "" {
		service += `Environment="PATH=` + path + "\"\n"
	}
	_, err = io.WriteString(r.env.Stdout, "[Unit]\n"+
		"Description="+description+"\n"+
		"After=graphical-session.target\n\n"+
		"[Service]\n"+
		service+
		`ExecStart="`+exe+`" `+command+"\n"+
		"Restart=on-failure\n"+
		"RestartSec=5\n\n"+
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
