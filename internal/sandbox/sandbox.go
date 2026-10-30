package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

const (
	Open  = "open"
	Boxed = "boxed"
)

var Modes = []string{Open, Boxed}

var Prober = Probe

func ValidMode(mode string) bool {
	return mode == Open || mode == Boxed
}

type Rule struct {
	Path  string
	Write bool
}

type Policy struct {
	Mode     string
	Worktree string
	Temp     string
	Rules    []Rule
	Environ  []string
	Dropped  []string
}

type Request struct {
	Mode       string
	Worktree   string
	GitCommon  string
	GitPrivate string
	Remotes    []string
	Harness    string
	Home       string
	Bin        string
	Getenv     func(string) string
	Environ    []string
}

var systemDirs = []string{
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib32",
	"/lib64",
	"/libx32",
	"/etc",
	"/opt",
	"/nix/store",
	"/proc",
	"/sys",
	"/run/systemd/resolve",
}

var devicePaths = []string{
	"/dev/null",
	"/dev/zero",
	"/dev/full",
	"/dev/random",
	"/dev/urandom",
	"/dev/tty",
	"/dev/ptmx",
	"/dev/pts",
	"/dev/shm",
}

var gitWriteDirs = []string{"objects", "refs", "logs"}

var gitWriteFiles = []string{
	"FETCH_HEAD",
	"ORIG_HEAD",
	"COMMIT_EDITMSG",
	"MERGE_HEAD",
	"MERGE_MSG",
	"CHERRY_PICK_HEAD",
	"REVERT_HEAD",
	"packed-refs",
}

func Resolve(req Request) (Policy, error) {
	if !ValidMode(req.Mode) {
		return Policy{}, fmt.Errorf("%w: access must be one of %s", state.ErrInvalid, strings.Join(Modes, ", "))
	}
	if req.Mode == Open {
		return Policy{Mode: Open, Environ: req.Environ}, nil
	}
	if req.Getenv == nil {
		req.Getenv = os.Getenv
	}
	wt, err := existing(req.Worktree)
	if err != nil {
		return Policy{}, fmt.Errorf("%w: worktree %s: %v", state.ErrInvalid, req.Worktree, err)
	}
	if fi, err := os.Stat(wt); err != nil || !fi.IsDir() {
		return Policy{}, fmt.Errorf("%w: worktree %s is not a directory", state.ErrInvalid, req.Worktree)
	}
	home := req.Getenv("HOME")
	if home != "" {
		if real, err := filepath.EvalSymlinks(home); err == nil {
			home = real
		}
	}
	config := xdg(req.Getenv, "XDG_CONFIG_HOME", home, ".config")
	data := xdg(req.Getenv, "XDG_DATA_HOME", home, ".local/share")
	cache := xdg(req.Getenv, "XDG_CACHE_HOME", home, ".cache")
	stateDir := xdg(req.Getenv, "XDG_STATE_HOME", home, ".local/state")
	temp, err := privateTemp(req.Home, wt)
	if err != nil {
		return Policy{}, err
	}
	rules := map[string]bool{}
	add := func(path string, write bool) {
		if path == "" {
			return
		}
		p, err := existing(path)
		if err != nil {
			return
		}
		rules[p] = rules[p] || write
	}

	add(wt, true)
	add(temp, true)
	for _, dir := range systemDirs {
		add(dir, false)
	}
	for _, dir := range devicePaths {
		add(dir, true)
	}
	writeDir := func(path string) {
		if path == "" {
			return
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return
		}
		add(path, true)
	}
	if home != "" {
		for _, dir := range []string{"mise", "uv", "go-build", "pip", "node-gyp"} {
			writeDir(filepath.Join(cache, dir))
		}
		add(filepath.Join(stateDir, "mise"), false)
		writeDir(filepath.Join(stateDir, "mise", "hints"))
		writeDir(filepath.Join(stateDir, "mise", "incomplete-installs"))
		add(filepath.Join(stateDir, "mise", "tool-purgatory.json"), true)
		writeDir(filepath.Join(stateDir, "uv"))
		add(filepath.Join(data, "mise"), false)
		writeDir(filepath.Join(data, "mise", "downloads"))
		add(filepath.Join(data, "uv"), false)
		writeDir(filepath.Join(data, "pnpm", "store"))
		add(filepath.Join(data, "pnpm"), false)
		writeDir(filepath.Join(home, ".npm"))
		add(filepath.Join(home, ".bun"), false)
		writeDir(filepath.Join(home, ".bun", "install", "cache"))
		add(filepath.Join(home, ".cargo", "bin"), false)
		add(filepath.Join(home, ".cargo", "config.toml"), false)
		add(filepath.Join(home, ".cargo", "config"), false)
		writeDir(filepath.Join(home, ".cargo", "registry"))
		writeDir(filepath.Join(home, ".cargo", "git"))
		add(filepath.Join(home, ".rustup"), false)
		writeDir(filepath.Join(home, ".rustup", "downloads"))
		writeDir(filepath.Join(home, ".rustup", "tmp"))
		writeDir(filepath.Join(home, ".rustup", "update-hashes"))
		add(filepath.Join(home, "go"), false)
		writeDir(filepath.Join(home, "go", "pkg"))
		add(filepath.Join(home, ".local/bin"), false)
		add(filepath.Join(config, "git"), false)
		add(filepath.Join(home, ".gitconfig"), false)
		harnessDirs(req.Harness, home, config, data, cache, stateDir, req.Getenv, add, writeDir)
	}
	if common, err := existing(req.GitCommon); err == nil {
		add(common, false)
		for _, dir := range gitWriteDirs {
			add(filepath.Join(common, dir), true)
		}
		for _, file := range gitWriteFiles {
			add(filepath.Join(common, file), true)
		}
	}
	add(req.GitPrivate, true)
	for _, remote := range req.Remotes {
		add(remote, false)
		for _, dir := range gitWriteDirs {
			add(filepath.Join(remote, dir), true)
		}
		for _, file := range gitWriteFiles {
			add(filepath.Join(remote, file), true)
		}
	}
	add(req.Bin, false)
	policy := Policy{Mode: Boxed, Worktree: wt, Temp: temp}
	for path, write := range rules {
		policy.Rules = append(policy.Rules, Rule{Path: path, Write: write})
	}
	sort.Slice(policy.Rules, func(i, j int) bool { return policy.Rules[i].Path < policy.Rules[j].Path })
	policy.Environ, policy.Dropped = Environ(req.Environ, temp)
	return policy, nil
}

func harnessDirs(name, home, config, data, cache, stateDir string, getenv func(string) string, add func(string, bool), writeDir func(string)) {
	switch name {
	case "claude":
		writeDir(filepath.Join(home, ".claude"))
		add(filepath.Join(home, ".claude.json"), true)
	case "codex":
		dir := getenv("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		writeDir(dir)
	case "opencode":
		writeDir(filepath.Join(config, "opencode"))
		writeDir(filepath.Join(data, "opencode"))
		writeDir(filepath.Join(cache, "opencode"))
		writeDir(filepath.Join(stateDir, "opencode"))
	case "agy":
		writeDir(filepath.Join(home, ".gemini"))
		writeDir(filepath.Join(config, "antigravity"))
	}
}

func xdg(getenv func(string) string, name, home, fallback string) string {
	if v := getenv(name); v != "" {
		return v
	}
	return filepath.Join(home, fallback)
}

func privateTemp(home, worktree string) (string, error) {
	base := filepath.Base(worktree)
	var dir string
	if home != "" {
		dir = filepath.Join(home, "tmp", base)
	} else {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("hand-sandbox-%d", os.Geteuid()), base)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	return real, nil
}

func existing(path string) (string, error) {
	if path == "" {
		return "", os.ErrNotExist
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(real); err != nil {
		return "", err
	}
	return real, nil
}

var envDropPrefixes = []string{"LUVUS_", "HAND_LUVUS_"}

var envDropNames = map[string]bool{
	"SSH_AUTH_SOCK":            true,
	"SSH_AGENT_PID":            true,
	"DBUS_SESSION_BUS_ADDRESS": true,
	"DBUS_SYSTEM_BUS_ADDRESS":  true,
	"GH_TOKEN":                 true,
	"GITHUB_TOKEN":             true,
	"GPG_AGENT_INFO":           true,
}

func Environ(in []string, temp string) (env []string, dropped []string) {
	env = make([]string, 0, len(in)+3)
	for _, kv := range in {
		name, _, _ := strings.Cut(kv, "=")
		if droppedName(name) {
			dropped = append(dropped, name)
			continue
		}
		switch name {
		case "TMPDIR", "TMP", "TEMP":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp)
	sort.Strings(dropped)
	return env, dropped
}

func droppedName(name string) bool {
	if envDropNames[name] {
		return true
	}
	for _, prefix := range envDropPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

var ErrUnsupported = errors.New("sandbox: the platform cannot enforce boxed")
