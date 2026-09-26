package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const Version = "0.0.0-next"

type Env struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
	Environ func() []string
	Now     func() time.Time
	Context context.Context
}

type handler func(*runner, []string) error

type runner struct {
	env  Env
	home string
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

var commands = map[string]handler{
	"version": func(r *runner, _ []string) error {
		var d toon.Doc
		d.Field("version", Version)
		return r.print(&d)
	},
}

func Run(args []string, env Env) int {
	err := dispatch(args, env)
	if err == nil {
		return 0
	}
	fmt.Fprintf(env.Stderr, "error: %s\n", err)
	return exitCode(err)
}

func exitCode(err error) int {
	var u usageError
	switch {
	case errors.As(err, &u), errors.Is(err, state.ErrInvalid):
		return 2
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrConflict):
		return 3
	}
	return 1
}

func dispatch(args []string, env Env) error {
	home := ""
	if len(args) >= 2 && args[0] == "--home" {
		home, args = args[1], args[2:]
	}
	if len(args) == 0 {
		return usageError{"usage: hand [--home DIR] COMMAND; commands: " + names(commands)}
	}
	h, ok := commands[args[0]]
	if !ok {
		return usageError{fmt.Sprintf("unknown command %q; commands: %s", args[0], names(commands))}
	}
	if home == "" {
		home = env.Getenv("HAND_HOME")
	}
	if home == "" && env.Getenv("HOME") != "" {
		home = filepath.Join(env.Getenv("HOME"), ".hand")
	}
	if home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return err
		}
		home = abs
	}
	return h(&runner{env: env, home: home}, args[1:])
}

func sub(r *runner, args []string, name string, table map[string]handler) error {
	if len(args) == 0 {
		return usageError{fmt.Sprintf("usage: hand %s SUBCOMMAND; subcommands: %s", name, names(table))}
	}
	h, ok := table[args[0]]
	if !ok {
		return usageError{fmt.Sprintf("unknown %s subcommand %q; subcommands: %s", name, args[0], names(table))}
	}
	return h(r, args[1:])
}

func names(table map[string]handler) string {
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parse(fs *flag.FlagSet, args []string, want int) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		return nil, usageError{fmt.Sprintf("%s: %v", fs.Name(), err)}
	}
	if fs.NArg() != want {
		return nil, usageError{fmt.Sprintf("%s: want %d argument(s) after flags, got %d", fs.Name(), want, fs.NArg())}
	}
	return fs.Args(), nil
}

func (r *runner) print(d *toon.Doc) error {
	return d.Render(r.env.Stdout)
}

func (r *runner) ctx() context.Context {
	if r.env.Context != nil {
		return r.env.Context
	}
	return context.Background()
}
