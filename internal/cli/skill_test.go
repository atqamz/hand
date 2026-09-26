package cli_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestInitInstallsTheBootstrapAndTheSkill(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	agents, err := os.ReadFile(filepath.Join(h.home, "AGENTS.md"))
	if err != nil || !strings.Contains(string(agents), "Run `hand orient` at the start of every turn") {
		t.Fatalf("AGENTS.md = %q, %v", agents, err)
	}
	skill, err := os.ReadFile(filepath.Join(h.home, ".claude", "skills", "secondhand", "SKILL.md"))
	if err != nil || !strings.HasPrefix(string(skill), "---\nname: secondhand\n") {
		t.Fatalf("skill = %q, %v", skill, err)
	}
	for _, want := range []string{"hand orient", "hand wait --after", "hand report ack", "hand decision ask", "hand attempt start --profile"} {
		if !strings.Contains(string(skill), want) {
			t.Fatalf("skill missing %q", want)
		}
	}
	if _, _, code := h.run("skill"); code != 2 {
		t.Fatalf("hand skill still exists: code %d", code)
	}
}

func TestInitNextToAForeignAgentsFileWritesNothing(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.home, "AGENTS.md"), []byte("# my repo rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("init"); code != 3 || !strings.Contains(errOut, "AGENTS.md is not Hand's") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	for _, name := range []string{"hand.db", "CLAUDE.md", "memory", "routing.json"} {
		if _, err := os.Stat(filepath.Join(h.home, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("refused init still wrote %s: %v", name, err)
		}
	}
}

func TestSkillMentionsOnlyRealCommands(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Join(h.home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, "state", "hand.db"), []byte("0.7"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.ok("init")
	for _, name := range []string{"secondhand", "secondhand-migrate"} {
		skill, err := os.ReadFile(filepath.Join(h.home, ".claude", "skills", name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		uses := regexp.MustCompile("`hand ([a-z]+)(?: ([a-z]+))?").FindAllStringSubmatch(string(skill), -1)
		if min := map[string]int{"secondhand": 10, "secondhand-migrate": 5}[name]; len(uses) < min {
			t.Fatalf("%s: found only %d commands", name, len(uses))
		}
		for _, u := range uses {
			args := []string{u[1]}
			if u[2] != "" {
				args = append(args, u[2])
			}
			_, errOut, _ := h.run(append(args, "--no-such-flag")...)
			if strings.Contains(errOut, "unknown command") || strings.Contains(errOut, "subcommand") {
				t.Fatalf("%s names %q, which does not exist: %s", name, strings.Join(args, " "), errOut)
			}
		}
	}
}

func TestInitInAnOldFleetLeavesItsDataAlone(t *testing.T) {
	h := newHarness(t)
	old := map[string]string{
		"AGENTS.md":                         "## Secondhand supervisor bootstrap\n\nold rules\n",
		"CLAUDE.md":                         "@AGENTS.md",
		"data/operator.md":                  "# Operator\nNo force pushes.\n",
		"data/145-ship/brief.md":            "Ship it.\n",
		"state/hand.db":                     "a 0.7 database, not this one",
		"state/145-ship.status":             "working: started\n",
		"config/profiles/opus-high/harness": "claude\n",
		"projects/app/README":               "app\n",
	}
	for rel, body := range old {
		path := filepath.Join(h.home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.ok("init")
	for rel, body := range old {
		if rel == "AGENTS.md" || rel == "CLAUDE.md" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(h.home, rel))
		if err != nil || string(got) != body {
			t.Fatalf("init changed %s: %q, %v", rel, got, err)
		}
	}
	if agents, _ := os.ReadFile(filepath.Join(h.home, "AGENTS.md")); !strings.Contains(string(agents), "Run `hand orient`") {
		t.Fatalf("the 0.7 bootstrap was not replaced: %q", agents)
	}
}

func TestInitWarnsAboutLive07Wiring(t *testing.T) {
	h := newHarness(t)
	for rel, body := range map[string]string{
		"state/hand.db":                          "0.7",
		".claude/settings.json":                  `{"hooks":{"Stop":[{"hooks":[{"args":["supervision","claude-stop"],"command":"/usr/local/bin/hand","type":"command"}]}]}}`,
		".pi/extensions/hand-supervisor-wake.ts": "x",
		".opencode/plugins/hand-custom.js":       "x",
		".pi/extensions/mine.ts":                 "x",
	} {
		path := filepath.Join(h.home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := h.ok("init")
	for _, want := range []string{"Hand 0.7 wiring", ".claude/settings.json", ".pi/extensions/hand-supervisor-wake.ts", ".opencode/plugins/hand-custom.js"} {
		if !strings.Contains(out, want) {
			t.Fatalf("init does not warn about %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "mine.ts") {
		t.Fatalf("init warned about a file that is not Hand's:\n%s", out)
	}
	if clean := newHarness(t).ok("init"); strings.Contains(clean, "0.7 wiring") {
		t.Fatalf("a fresh fleet got the 0.7 warning:\n%s", clean)
	}
}
