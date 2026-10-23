package gitcfg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/proc"
	"github.com/atqamz/hand/internal/state"
)

const (
	header         = "v1\n"
	worktreeSuffix = " (worktree)"
	attributes     = ".git/info/attributes"
	shown          = 10
)

var Listed = []string{
	"browser.*.cmd",
	"browser.*.path",
	"core.alternateRefsCommand",
	"core.askPass",
	"core.editor",
	"core.fsmonitor",
	"core.gitProxy",
	"core.hooksPath",
	"core.pager",
	"core.sshCommand",
	"credential.helper",
	"credential.*.helper",
	"diff.external",
	"diff.*.command",
	"diff.*.textconv",
	"difftool.*.cmd",
	"difftool.*.path",
	"filter.*.clean",
	"filter.*.process",
	"filter.*.smudge",
	"gc.recentObjectsHook",
	"gpg.program",
	"gpg.*.defaultKeyCommand",
	"gpg.*.program",
	"guitool.*.cmd",
	"help.browser",
	"hook.*.command",
	"hook.*.enabled",
	"hook.*.event",
	"imap.tunnel",
	"include.path",
	"includeIf.*.path",
	"instaweb.browser",
	"instaweb.httpd",
	"interactive.diffFilter",
	"lfs.customtransfer.*.args",
	"lfs.customtransfer.*.path",
	"lfs.standalonetransferagent",
	"man.*.cmd",
	"man.*.path",
	"merge.*.driver",
	"mergetool.*.cmd",
	"mergetool.*.path",
	"pager.*",
	"protocol.allow",
	"protocol.*.allow",
	"remote.*.proxy",
	"remote.*.receivepack",
	"remote.*.uploadpack",
	"remote.*.vcs",
	"sendemail.smtpServer",
	"sendemail.*.smtpServer",
	"sequence.editor",
	"submodule.*.update",
	"trailer.*.cmd",
	"trailer.*.command",
	"web.browser",
}

var patterns = func() []string {
	out := make([]string, len(Listed))
	for i, p := range Listed {
		out[i] = strings.ToLower(p)
	}
	return out
}()

func listed(name string) bool {
	name = strings.ToLower(name)
	return slices.ContainsFunc(patterns, func(p string) bool {
		pre, post, star := strings.Cut(p, "*")
		if !star {
			return name == p
		}
		return len(name) >= len(pre)+len(post) && strings.HasPrefix(name, pre) && strings.HasSuffix(name, post)
	})
}

type Setting struct {
	Name   string
	Values []string
}

func (s Setting) hash() string {
	h := sha256.New()
	for _, v := range s.Values {
		fmt.Fprintf(h, "%d:%s", len(v), v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type Settings []Setting

func attributesPath(ctx context.Context, dir string) (string, error) {
	path, err := run(ctx, dir, "rev-parse", "--git-path", "info/attributes")
	if err != nil {
		return "", err
	}
	file := filepath.FromSlash(strings.TrimSpace(path))
	if !filepath.IsAbs(file) {
		file = filepath.Join(dir, file)
	}
	return file, nil
}

func Read(ctx context.Context, dir string) (Settings, error) {
	file, err := attributesPath(ctx, dir)
	if err != nil {
		return nil, err
	}
	return read(ctx, dir, file)
}

func read(ctx context.Context, dir, file string) (Settings, error) {
	out, err := run(ctx, dir, "config", "--list", "--show-scope", "--includes", "-z")
	if err != nil {
		return nil, err
	}
	found := map[string][]string{}
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		scope := fields[i]
		if scope != "local" && scope != "worktree" {
			continue
		}
		name, value, set := strings.Cut(fields[i+1], "\n")
		if !listed(name) {
			continue
		}
		if !set {
			value = "true"
		}
		if scope == "worktree" {
			name += worktreeSuffix
		}
		found[name] = append(found[name], value)
	}
	switch content, err := os.ReadFile(file); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	case len(content) > 0:
		found[attributes] = strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	}
	s := make(Settings, 0, len(found))
	for _, name := range slices.Sorted(maps.Keys(found)) {
		s = append(s, Setting{Name: name, Values: found[name]})
	}
	return s, nil
}

func (s Settings) Digest() string {
	var b strings.Builder
	b.WriteString(header)
	for _, e := range s {
		b.WriteString(e.hash() + " " + e.Name + "\n")
	}
	return b.String()
}

func (s Settings) Changed(recorded string, linked bool) []string {
	then := map[string]string{}
	if body, ok := strings.CutPrefix(recorded, header); ok {
		for line := range strings.SplitSeq(body, "\n") {
			if hash, name, ok := strings.Cut(line, " "); ok && !(linked && strings.HasSuffix(name, worktreeSuffix)) {
				then[name] = hash
			}
		}
	}
	changed := map[string]bool{}
	for _, e := range s {
		if then[e.Name] != e.hash() {
			changed[e.Name] = true
		}
		delete(then, e.Name)
	}
	for name := range then {
		changed[name] = true
	}
	return slices.Sorted(maps.Keys(changed))
}

type Repo struct {
	Project    string
	Dir        string
	Recorded   string
	Linked     bool
	attributes string
}

func Open(ctx context.Context, project, dir, recorded string) (Repo, error) {
	file, err := attributesPath(ctx, dir)
	return Repo{Project: project, Dir: dir, Recorded: recorded, attributes: file}, err
}

func (r Repo) In(dir string) Repo {
	r.Dir, r.Linked = dir, true
	return r
}

func (r Repo) Check(ctx context.Context) error {
	if r.Recorded == "" {
		return fmt.Errorf("%w: git settings of project %s are not recorded", state.ErrInvalid, r.Project)
	}
	var now Settings
	var err error
	if r.attributes == "" {
		now, err = Read(ctx, r.Dir)
	} else {
		now, err = read(ctx, r.Dir, r.attributes)
	}
	if err != nil {
		return err
	}
	names := now.Changed(r.Recorded, r.Linked)
	if len(names) == 0 {
		return nil
	}
	return r.refusal(names)
}

type ChangedError struct{ msg string }

func (e ChangedError) Error() string { return state.ErrInvalid.Error() + ": " + e.msg }

func (e ChangedError) Unwrap() error { return state.ErrInvalid }

func Initialised(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	_, head := os.Stat(filepath.Join(dir, "HEAD"))
	_, objects := os.Stat(filepath.Join(dir, "objects"))
	return head == nil && objects == nil
}

func (r Repo) refusal(names []string) error {
	quoted := make([]string, 0, shown+1)
	inWorktree := false
	for i, name := range names {
		inWorktree = inWorktree || strings.HasSuffix(name, worktreeSuffix)
		if i < shown {
			quoted = append(quoted, strconv.Quote(name))
		}
	}
	if len(names) > shown {
		quoted = append(quoted, fmt.Sprintf("and %d more", len(names)-shown))
	}
	msg := fmt.Sprintf("git settings of project %s changed since Hand recorded them: %s; Hand ran nothing. Read them in %s, then accept them with `hand project trust %s`", r.Project, strings.Join(quoted, ", "), r.Dir, r.Project)
	if inWorktree {
		msg += "; a setting marked (worktree) is kept in that worktree's own configuration and trust does not accept it: remove it there, or delete the worktree's folder and run the command again"
	}
	return ChangedError{msg}
}

func (r Repo) Git(ctx context.Context, args ...string) (string, error) {
	if err := r.Check(ctx); err != nil {
		return "", err
	}
	return run(ctx, r.Dir, args...)
}

func Toplevel(ctx context.Context, dir string) (string, error) {
	return run(ctx, dir, "rev-parse", "--show-toplevel")
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.longpaths=true", "-C", dir}, args...)...)
	cmd.WaitDelay = time.Second
	proc.NoWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(append(stderr.Bytes(), out...)))
	}
	return string(out), nil
}
