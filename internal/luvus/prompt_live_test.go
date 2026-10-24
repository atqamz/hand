package luvus_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
)

type promptCase struct {
	name, text string
	raw        bool
}

const awkward = "First: \"double\" and 'single' quotes, C:\\Users\\dev\\tides\\ and a trailing one \\\\\n\n" +
	"Second: %PATH% %USERPROFILE% ^caret & ampersand | pipe < less > greater !bang! `tick` $(sub)\n" +
	"\tTabbed é ü ñ 日本語 🌊👍🏽\r\nCRLF line\r\n\r\n"

func fill(seed string, n int) string {
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(seed)
	}
	s := strings.ToValidUTF8(b.String()[:n], "")
	return s + strings.Repeat("x", n-len(s))
}

func promptCases(t *testing.T) []promptCase {
	t.Helper()
	brief, err := os.ReadFile(filepath.Join("testdata", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []promptCase{
		{name: "one word", text: "go"},
		{name: "two paragraphs", text: "Fix the login bug.\n\nThen commit and stop."},
		{name: "crlf paragraphs", text: "Fix the login bug.\r\n\r\nThen commit and stop.\r\n"},
		{name: "double quotes", text: `He said "no" and "yes", then ""doubled"" and a lone " here`},
		{name: "single quotes", text: `It's the operator's 'plan': don't panic`},
		{name: "backslashes", text: `C:\Users\dev\tides\ and \\server\share\dir and \n not a newline`},
		{name: "backslash before a quote", text: `open "C:\Users\dev\tides\" then "D:\x\\" and \\\"`},
		{name: "ends with a backslash", text: `the folder is C:\Users\dev\tides\`},
		{name: "ends with backslashes", text: `the folder is C:\Users\dev\tides\\`},
		{name: "ends with a backslash and a quote", text: `ends with \"`},
		{name: "percent variables", text: `%PATH% %USERPROFILE% %1 %* %~dp0 %%`},
		{name: "cmd metacharacters", text: `a^b & c | d < e > f ! g (h) ; i && j || k`},
		{name: "shell metacharacters", text: "`tick` $(sub) ${VAR} $env:HOME ;; 'x' *.go ~"},
		{name: "unicode", text: "é ü ñ Ω 日本語 العربية 🌊 👍🏽 👨‍👩‍👧 e\u0301"},
		{name: "tabs", text: "a\tb\t\tc\n\td"},
		{name: "whitespace around", text: "  lead\n\ntrail  \n\n"},
		{name: "leading dash", text: "-p is not a flag here", raw: true},
		{name: "leading double dash", text: "--help", raw: true},
		{name: "brief", text: string(brief)},
		{name: "brief with crlf", text: strings.ReplaceAll(string(brief), "\n", "\r\n")},
	}
	for _, n := range []int{1, 100, 1024, 4096, 8190, 8191, 8192, 12288, 16383, harness.MaxPromptBytes} {
		cases = append(cases, promptCase{name: fmt.Sprintf("mixed %d bytes", n), text: fill(awkward, n)})
	}
	for _, n := range []int{8191, 8192, harness.MaxPromptBytes} {
		cases = append(cases, promptCase{name: fmt.Sprintf("plain %d bytes", n), text: fill("The quick brown fox jumps over the lazy dog.\n", n)})
	}
	return cases
}

func recordedArgs(t *testing.T, c luvus.Client, work, out string, argv []string) []string {
	t.Helper()
	_ = os.Remove(out)
	ctx := context.Background()
	term, err := c.Create(ctx, work, "hand-it", argv)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = c.Close(ctx, term) }()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		var got []string
		if b, err := os.ReadFile(out); err == nil && json.Unmarshal(b, &got) == nil {
			return got
		}
		if time.Now().After(deadline) {
			s, err := c.Read(ctx, term.PaneID, 60)
			t.Fatalf("the program never recorded its arguments; screen %q, %v", s.Text, err)
		}
	}
}

func firstDifference(got, want string) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	return fmt.Sprintf("got %d bytes, want %d, first difference at byte %d", len(got), len(want), i)
}

func TestLiveAPromptReachesAnExeIntact(t *testing.T) {
	_, c, _ := startLive(t, "hand-it-prompt")
	dir, work := t.TempDir(), t.TempDir()
	out := filepath.Join(dir, "args.json")
	bin := fakebin.Install(t, dir, "codex", "args", map[string]string{"out": out})
	spec := harness.Spec{Harness: "codex", Model: "gpt-6-luna", Effort: "low"}
	shapes := []struct {
		name  string
		build func(prompt string) ([]string, error)
	}{
		{"worker", func(p string) ([]string, error) { return harness.Argv(bin, spec, p, work) }},
		{"supervisor", func(p string) ([]string, error) { return harness.SupervisorArgv(bin, spec, "", p, work, false) }},
	}
	for _, shape := range shapes {
		for _, tc := range promptCases(t) {
			t.Run(shape.name+"/"+tc.name, func(t *testing.T) {
				argv, err := shape.build(tc.text)
				if tc.raw {
					if err == nil {
						t.Fatal("Hand accepted a prompt that starts with a dash")
					}
					if argv, err = shape.build("x"); err == nil {
						argv[len(argv)-1] = tc.text
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				got := recordedArgs(t, c, work, out, argv)
				if len(got) != len(argv)-1 {
					t.Fatalf("the program got %d arguments, want %d: %q", len(got), len(argv)-1, got)
				}
				for i, want := range argv[1:] {
					if got[i] != want {
						t.Fatalf("argument %d: %s", i, firstDifference(got[i], want))
					}
				}
			})
		}
	}
}
