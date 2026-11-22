package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
)

func SetSkillFetchLimits(t *testing.T, git, budget time.Duration) {
	t.Helper()
	oldGit, oldBudget := skillGitTimeout, skillFetchBudget
	skillGitTimeout, skillFetchBudget = git, budget
	t.Cleanup(func() { skillGitTimeout, skillFetchBudget = oldGit, oldBudget })
}

func TestTrustFollowsTheFetchedURLAndTheSubscription(t *testing.T) {
	git := func(source, url, sub string) fleet.Managed {
		return fleet.Managed{LockEntry: fleet.LockEntry{Source: source, SourceURL: url, SourceType: "github"}, Subscribed: sub}
	}
	local := func(sub string) fleet.Managed {
		return fleet.Managed{LockEntry: fleet.LockEntry{Source: "/src/tool", SourceType: "local"}, Subscribed: sub}
	}
	own := "https://github.com/" + ownRepo + ".git"
	kelvin := strings.ReplaceAll(ownRepo, "k", "\u212a")
	for name, tc := range map[string]struct {
		scope string
		e     fleet.Managed
		want  bool
	}{
		"own url and subscription":      {"fleet", git(ownRepo, own, ownRepo), true},
		"own url, url subscription":     {"fleet", git(own, "", own), true},
		"trailing slash":                {"fleet", git(ownRepo, "https://github.com/"+ownRepo+"/", ownRepo), true},
		"no subscription":               {"fleet", git(ownRepo, own, ""), false},
		"planted source, other url":     {"fleet", git(ownRepo, "file:///elsewhere/tool.git", ownRepo), false},
		"planted source, no url":        {"fleet", git(ownRepo, "", ownRepo), false},
		"subscription names another":    {"fleet", git(ownRepo, own, "someone/else"), false},
		"kelvin sign":                   {"fleet", git(kelvin, "https://github.com/"+kelvin+".git", kelvin), false},
		"upper case":                    {"fleet", git(ownRepo, "https://github.com/"+strings.ToUpper(ownRepo)+".git", ownRepo), false},
		"longer name":                   {"fleet", git(ownRepo, "https://github.com/"+ownRepo+"-x.git", ownRepo), false},
		"other owner":                   {"fleet", git(ownRepo, "https://github.com/evil/skills.git", ownRepo), false},
		"ssh form":                      {"fleet", git(ownRepo, "git@github.com:"+ownRepo+".git", ownRepo), false},
		"other host":                    {"fleet", git(ownRepo, "https://example.com/"+ownRepo+".git", ownRepo), false},
		"local in the fleet":            {"fleet", local("/src/tool"), true},
		"local in the user scope":       {"user", local("/src/tool"), true},
		"local in a project":            {"project", local("/src/tool"), false},
		"local with no subscription":    {"fleet", local(""), false},
		"local, subscription elsewhere": {"fleet", local("/src/other"), false},
	} {
		if got := trusted(fleet.Skills{Scope: tc.scope}, tc.e); got != tc.want {
			t.Errorf("%s: trusted = %v, want %v", name, got, tc.want)
		}
	}
}

func TestDriftStateNamesTheFourStatuses(t *testing.T) {
	for _, tc := range []struct {
		name           string
		edited, behind bool
		want           string
	}{
		{"hash equals the lock, source unmoved", false, false, "current"},
		{"clean copy, source moved", false, true, "behind"},
		{"hash differs from the lock, source unmoved", true, false, "edited"},
		{"hash differs and the source moved", true, true, "conflict"},
	} {
		if got := driftState(tc.edited, tc.behind); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCommitWordNeverCarriesShellText(t *testing.T) {
	for in, want := range map[string]string{
		"":                     "its content",
		"0123456789abcdef0123": "0123456789ab",
		"$(touch x)":           "??touch?x?",
		"a\"b`c":               "a?b?c",
		"x;y":                  "x?y",
		"short":                "short",
	} {
		if got := commitWord(in); got != want {
			t.Errorf("commitWord(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBumpTitleNamesOldAndNewAndNeverCarriesShellText(t *testing.T) {
	e := fleet.Managed{LockEntry: fleet.LockEntry{Commit: "0123456789abcdef", ComputedHash: "feedface00000000"}}
	if got := bumpTitle("triage", e, "fedcba9876543210", "aaaa"); got != "skills: triage 0123456789ab -> fedcba987654" {
		t.Errorf("title = %q", got)
	}
	local := fleet.Managed{LockEntry: fleet.LockEntry{ComputedHash: "feedface0000000000"}}
	if got := bumpTitle("mine", local, "", "cafe0000cafe0000"); got != "skills: mine feedface0000 -> cafe0000cafe" {
		t.Errorf("title = %q", got)
	}
	evil := fleet.Managed{LockEntry: fleet.LockEntry{Commit: "$(touch x)`id`"}}
	if got := bumpTitle("triage", evil, "abc", ""); got != "skills: triage ??touch?x??i -> abc" {
		t.Errorf("title = %q", got)
	}
}

func TestBumpBodyFencesTheDiffAndCutsItAtALine(t *testing.T) {
	e := fleet.Managed{LockEntry: fleet.LockEntry{Source: "atqamz/skills", SourceURL: skillsRepo, SourceType: "github", SkillPath: "skills/triage/SKILL.md"}}
	e.Ref = "main"
	got := bumpBody("skills: triage a -> b", e, "+```\n+````\n+x\n")
	if !strings.HasPrefix(got, "skills: triage a -> b\n\nSource: "+skillsRepo+"#skills/triage, ref main.") || !strings.Contains(got, "\n`````diff\n+```\n+````\n+x\n`````\n") {
		t.Errorf("body = %q", got)
	}
	long := strings.Repeat("+012345678\n", bumpBodyLimit/11+1000)
	got = bumpBody("t", e, long)
	cut := strings.Index(got, "```diff\n") + len("```diff\n")
	diff := got[cut : strings.Index(got[cut:], "```")+cut]
	if len(diff) > bumpBodyLimit || len(diff) < bumpBodyLimit-11 || len(diff)%11 != 0 || !strings.HasSuffix(diff, "\n") || !strings.Contains(got, "The diff is cut at 60000 bytes; Files changed has all of it.") || len(got) > 65536 {
		t.Errorf("cut diff = %d bytes, body %d bytes", len(diff), len(got))
	}
	if strings.Contains(bumpBody("t", e, "+a\n"), "The diff is cut") {
		t.Error("a short diff carries the cut note")
	}
	if got := bumpBody("t", fleet.Managed{LockEntry: fleet.LockEntry{Source: "o/r", SourceURL: "https://github.com/o/r.git", SourceType: "github"}}, "+a\n"); !strings.Contains(got, ", ref HEAD.") {
		t.Errorf("no ref = %q", got)
	}
}

func TestShownURLDropsWhatCouldHoldASecret(t *testing.T) {
	for in, want := range map[string]string{
		"https://tok:sekrit@example.com/o/r.git":       "https://example.com/o/r.git",
		"https://tok@example.com/o/r.git?token=x#frag": "https://example.com/o/r.git",
		"https://github.com/o/r.git":                   "https://github.com/o/r.git",
		"git@github.com:o/r.git":                       "github.com:o/r.git",
		"user:sekrit@example.com:o/r.git":              "example.com:o/r.git",
		"ssh://git:sekrit@example.com:2222/o/r.git":    "ssh://example.com:2222/o/r.git",
	} {
		if got := shownURL(in); got != want {
			t.Errorf("shownURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBumpBodyDropsTheSourceCredentialFromTheDiff(t *testing.T) {
	raw := "https://tok:s3k&t@example.com/o/r.git"
	e := fleet.Managed{LockEntry: fleet.LockEntry{Source: raw, SourceURL: raw, SourceType: "git"}}
	got := bumpBody("t", e, " \"source\": \"https://tok:s3k\\u0026t@example.com/o/r.git\",\n+\"also\": \""+raw+"\"\n+s3k&t\n")
	if strings.Contains(got, "tok") || strings.Contains(got, "s3k") || !strings.Contains(got, "\"source\": \"https://example.com/o/r.git\"") {
		t.Errorf("body = %q", got)
	}
}

func TestProjectRemoteTakesOnlyGitHubAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/app.git":     "acme/app",
		"https://github.com/acme/app":         "acme/app",
		"https://github.com/acme/app/":        "acme/app",
		"git@github.com:acme/app.git":         "acme/app",
		"ssh://git@github.com/acme/app.git":   "acme/app",
		"https://gitlab.com/acme/app.git":     "",
		"http://github.com/acme/app.git":      "",
		"https://github.com/acme/app/tree/x":  "",
		"https://github.com.evil.io/acme/app": "",
		"git@gitlab.com:acme/app.git":         "",
		"/srv/git/app.git":                    "",
	} {
		got := ""
		if m := projectRemote.FindStringSubmatch(in); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("projectRemote(%q) = %q, want %q", in, got, want)
		}
	}
}
