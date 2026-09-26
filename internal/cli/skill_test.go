package cli_test

import (
	"regexp"
	"strings"
	"testing"
)

func TestSkillPrintsTheSupervisorSkill(t *testing.T) {
	h := newHarness(t)
	out := h.ok("skill")
	if !strings.HasPrefix(out, "---\nname: hand\n") {
		t.Fatalf("skill does not start with its frontmatter: %q", out[:min(len(out), 80)])
	}
	for _, want := range []string{"hand orient", "hand wait --after", "hand report ack", "hand decision ask", "hand attempt start --profile"} {
		if !strings.Contains(out, want) {
			t.Fatalf("skill missing %q", want)
		}
	}
}

func TestSkillMentionsOnlyRealCommands(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	skill := h.ok("skill")
	uses := regexp.MustCompile("`hand ([a-z]+)(?: ([a-z]+))?").FindAllStringSubmatch(skill, -1)
	if len(uses) < 10 {
		t.Fatalf("found only %d commands in the skill", len(uses))
	}
	for _, u := range uses {
		args := []string{u[1]}
		if u[2] != "" {
			args = append(args, u[2])
		}
		_, errOut, _ := h.run(append(args, "--no-such-flag")...)
		if strings.Contains(errOut, "unknown command") || strings.Contains(errOut, "subcommand") {
			t.Fatalf("skill names %q, which does not exist: %s", strings.Join(args, " "), errOut)
		}
	}
}
