package luvus_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
)

func probeRun(t *testing.T, c luvus.Client, work, out string, argv []string) (string, string) {
	t.Helper()
	_ = os.Remove(out)
	ctx := context.Background()
	term, err := c.Create(ctx, work, "hand-probe", argv)
	if err != nil {
		return "create refused", err.Error()
	}
	defer c.Close(ctx, term)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		b, err := os.ReadFile(out)
		if err != nil {
			continue
		}
		var got []string
		if json.Unmarshal(b, &got) != nil {
			continue
		}
		var lens []string
		for _, a := range got {
			lens = append(lens, fmt.Sprint(len(a)))
		}
		same := len(got) == len(argv)-1
		for i := 1; same && i < len(argv); i++ {
			same = got[i-1] == argv[i]
		}
		if same {
			return "exact", "args " + strings.Join(lens, ",")
		}
		return "DIFFERENT", "args " + strings.Join(lens, ",") + " want " + fmt.Sprint(len(argv)-1) + " args"
	}
	s, err := c.Read(ctx, term.PaneID, 60)
	return "never started", fmt.Sprintf("screen %q err %v", s.Text, err)
}

func TestLiveProbeLimits(t *testing.T) {
	_, c, _ := startLive(t, "hand-it-probe")
	dir, work := t.TempDir(), t.TempDir()
	out := filepath.Join(dir, "args.json")
	exe := fakebin.Install(t, dir, "real", "args", map[string]string{"out": out})
	word := func(n int) string { return fill("Lorem ipsum dolor sit amet.\n", n) }
	line := harness.CommandLineLen
	report := func(name string, argv []string) {
		verdict, detail := probeRun(t, c, work, out, argv)
		t.Logf("PROBE exe %-34s ~line %6d: %s %s", name, line(argv), verdict, detail)
	}
	for _, n := range []int{1000, 8190, 8192, 16000, 16384, 16385, 20000, 32000, 40000, 100000} {
		report(fmt.Sprintf("one arg of %d", n), []string{exe, word(n)})
	}
	for _, target := range []int{30000, 32000, 32760, 32764, 32765, 32766, 32767, 32768, 32770} {
		argv := []string{exe, word(16384), word(16384)}
		for n := 16384; harness.CommandLineLen(argv) > target; n-- {
			argv[2] = word(n)
		}
		report(fmt.Sprintf("two args, line %d", target), argv)
	}
	for name, text := range map[string]string{
		"16384 double quotes":      strings.Repeat(`"`, 16384),
		"8192 backslash-quotes":    strings.Repeat(`\"`, 8192),
		"16384 backslashes":        strings.Repeat(`\`, 16384),
		"16384 control chars":      strings.Repeat("\x01", 16384),
		"16384 tabs":               strings.Repeat("\t", 16384),
		"4096 emoji":               strings.Repeat("🌊", 4096),
		"5461 CJK chars":           strings.Repeat("日", 5461),
		"16300 quotes then a word": strings.Repeat(`"`, 16300),
	} {
		report(name, []string{exe, text})
	}
	if runtime.GOOS != "windows" {
		return
	}
	shim := filepath.Join(dir, "codex.cmd")
	if err := os.WriteFile(shim, []byte("@\""+exe+"\" %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{100, 1000, 4000, 7000, 7900, 8000, 8100, 8191, 8300, 9000, 12000, 16384} {
		report(fmt.Sprintf("cmd shim one line %d", n), []string{shim, strings.ReplaceAll(word(n), "\n", " ")})
	}
	for _, n := range []int{100, 1000, 4000, 8000, 16384} {
		report(fmt.Sprintf("cmd shim paragraphs %d", n), []string{shim, word(n)})
	}
	for _, s := range []string{`say "hi"`, `100%`, `%PATH%`, `a & b`, `a | b`, `a ^ b`, `a < b > c`, `!x!`, `C:\dir\ "x"`, "tab\there", "emoji 🌊 é"} {
		report("cmd shim "+fmt.Sprintf("%q", s), []string{shim, s})
	}
	comspec := os.Getenv("ComSpec")
	for _, tc := range []struct {
		name string
		arg  string
	}{
		{"one line 8000", strings.ReplaceAll(word(8000), "\n", " ")},
		{"one line 8300", strings.ReplaceAll(word(8300), "\n", " ")},
		{"paragraphs 100", word(100)},
	} {
		ctx := context.Background()
		term, err := c.Create(ctx, work, "hand-probe", []string{comspec, "/k", shim, tc.arg})
		if err != nil {
			t.Logf("PROBE screen %s: create refused %v", tc.name, err)
			continue
		}
		time.Sleep(4 * time.Second)
		screen, err := c.Read(ctx, term.PaneID, 60)
		t.Logf("PROBE screen %s: %q err %v", tc.name, screen.Text, err)
		_ = c.Close(ctx, term)
	}
}
