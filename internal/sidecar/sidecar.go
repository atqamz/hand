package sidecar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/proc"
)

const (
	Name    = "hand-iroh"
	Version = "0.1.0"
)

const (
	Starting = "starting"
	Ready    = "ready"
	Failed   = "failed"
	Stopped  = "stopped"
)

const (
	lineMax    = 1024
	tailMax    = 1024
	reasonMax  = 200
	versionMax = 4096
)

var (
	ErrNotFound   = errors.New("hand-iroh not found")
	ErrNotSidecar = errors.New("not a hand-iroh binary")
	ErrVersion    = errors.New("hand-iroh version")
)

var (
	versionLine = regexp.MustCompile(`^hand-iroh (\d+\.\d+\.\d+) iroh \S+$`)
	readyLine   = regexp.MustCompile(`^ready id=([0-9a-f]{64}) relay=(none|https?://\S{1,200})$`)
)

type Status struct{ State, ID, Relay, Reason string }

func exeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func Locate(self, path string) (string, error) {
	if self != "" {
		next := filepath.Join(filepath.Dir(self), Name+exeExt())
		if fi, err := os.Stat(next); err == nil && fi.Mode().IsRegular() && (runtime.GOOS == "windows" || fi.Mode()&0o111 != 0) {
			return next, nil
		}
	}
	if bin, err := harness.LookPath(Name, path); err == nil {
		return bin, nil
	}
	if self == "" {
		return "", fmt.Errorf("%w: it is not on PATH", ErrNotFound)
	}
	return "", fmt.Errorf("%w: it is not next to hand (%s) or on PATH", ErrNotFound, filepath.Dir(self))
}

type capped struct{ b []byte }

func (c *capped) Write(p []byte) (int, error) {
	if room := versionMax - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func BinaryVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env, cmd.WaitDelay = []string{}, time.Second
	var out capped
	cmd.Stdout = &out
	proc.NoWindow(cmd)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s --version failed: %v", ErrNotSidecar, bin, err)
	}
	first, _, _ := strings.Cut(string(out.b), "\n")
	m := versionLine.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return "", fmt.Errorf("%w: %s printed %q, not `hand-iroh X.Y.Z iroh X.Y.Z`", ErrNotSidecar, bin, clean(first))
	}
	return m[1], nil
}

func Verify(ctx context.Context, bin string) (string, error) {
	version, err := BinaryVersion(ctx, bin)
	if err != nil {
		return "", err
	}
	if version != Version {
		return version, fmt.Errorf("%w: %s is %s, but this hand needs %s", ErrVersion, bin, version, Version)
	}
	return version, nil
}

func Resolve(self, path string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		bin, err := Locate(self, path)
		if err == nil {
			_, err = Verify(ctx, bin)
		}
		return bin, err
	}
}

func parseReady(line string) (Status, bool) {
	m := readyLine.FindStringSubmatch(line)
	if m == nil {
		return Status{}, false
	}
	return Status{State: Ready, ID: m[1], Relay: m[2]}, true
}

func clean(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s))
	if r := []rune(s); len(r) > reasonMax {
		s = string(r[:reasonMax]) + "..."
	}
	return s
}

type lines struct {
	buf   []byte
	skip  bool
	ready chan Status
}

func (l *lines) Write(p []byte) (int, error) {
	for _, b := range p {
		switch {
		case b == '\n':
			if st, ok := parseReady(strings.TrimSuffix(string(l.buf), "\r")); ok {
				select {
				case l.ready <- st:
				default:
				}
			}
			l.buf, l.skip = l.buf[:0], false
		case l.skip:
		case len(l.buf) >= lineMax:
			l.buf, l.skip = l.buf[:0], true
		default:
			l.buf = append(l.buf, b)
		}
	}
	return len(p), nil
}

type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > tailMax {
		t.b = t.b[len(t.b)-tailMax:]
	}
	return len(p), nil
}

func (t *tail) last() string {
	rows := strings.Split(strings.TrimSpace(string(t.b)), "\n")
	return clean(rows[len(rows)-1])
}

type Options struct {
	Resolve    func(context.Context) (string, error)
	Board      string
	SecretFile string
	KeyDir     string
	Dir        string
	Args       []string
	ReadyWait  time.Duration
	StopWait   time.Duration
	Stable     time.Duration
	Attempts   int
	Backoff    func(failures int) time.Duration
	OnChange   func(Status)
}

type Sidecar struct {
	o  Options
	mu sync.Mutex
	st Status
}

func New(o Options) *Sidecar {
	if o.ReadyWait <= 0 {
		o.ReadyWait = 30 * time.Second
	}
	if o.StopWait <= 0 {
		o.StopWait = 5 * time.Second
	}
	if o.Stable <= 0 {
		o.Stable = time.Minute
	}
	if o.Attempts <= 0 {
		o.Attempts = 5
	}
	if o.Backoff == nil {
		o.Backoff = func(failures int) time.Duration { return min(time.Second<<(failures-1), 30*time.Second) }
	}
	return &Sidecar{o: o}
}

func (s *Sidecar) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

func (s *Sidecar) set(st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st = st
	if s.o.OnChange != nil {
		s.o.OnChange(st)
	}
}

func (s *Sidecar) Run(ctx context.Context) {
	last := ""
	for failures := 0; ; {
		s.set(Status{State: Starting, Reason: last})
		bin, err := s.o.Resolve(ctx)
		if err != nil {
			s.finish(ctx, Status{State: Failed, Reason: clean(err.Error())})
			return
		}
		began := time.Now()
		last = s.once(ctx, bin)
		if time.Since(began) >= s.o.Stable {
			failures = 0
		}
		if failures++; failures >= s.o.Attempts && ctx.Err() == nil {
			s.finish(ctx, Status{State: Failed, Reason: fmt.Sprintf("gave up after %d quick exits: %s", failures, last)})
			return
		}
		select {
		case <-ctx.Done():
			s.set(Status{State: Stopped})
			return
		case <-time.After(s.o.Backoff(failures)):
		}
	}
}

func (s *Sidecar) finish(ctx context.Context, st Status) {
	if ctx.Err() != nil {
		st = Status{State: Stopped}
	}
	s.set(st)
}

func (s *Sidecar) once(ctx context.Context, bin string) string {
	o := s.o
	cmd := exec.Command(bin, append([]string{"--board", o.Board, "--secret-file", o.SecretFile, "--key-dir", o.KeyDir}, o.Args...)...)
	cmd.Env, cmd.Dir, cmd.WaitDelay = []string{}, o.Dir, 2*time.Second
	proc.NoWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return clean("cannot start: " + err.Error())
	}
	out, errs := &lines{ready: make(chan Status, 1)}, &tail{}
	cmd.Stdout, cmd.Stderr = out, errs
	if err := cmd.Start(); err != nil {
		return clean("cannot start: " + err.Error())
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	reason := func(err error) string {
		msg := "exit status 0"
		if err != nil {
			msg = err.Error()
		}
		if line := errs.last(); line != "" {
			msg += ": " + line
		}
		return clean(msg)
	}
	stop := func() string {
		_ = stdin.Close()
		select {
		case <-exited:
		case <-time.After(o.StopWait):
			_ = cmd.Process.Kill()
			<-exited
		}
		return ""
	}
	wait := time.NewTimer(o.ReadyWait)
	defer wait.Stop()
	select {
	case st := <-out.ready:
		s.set(st)
	case err := <-exited:
		return reason(err)
	case <-ctx.Done():
		return stop()
	case <-wait.C:
		_ = cmd.Process.Kill()
		<-exited
		return fmt.Sprintf("no ready line within %s", o.ReadyWait)
	}
	select {
	case err := <-exited:
		return reason(err)
	case <-ctx.Done():
		return stop()
	}
}
