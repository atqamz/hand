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
