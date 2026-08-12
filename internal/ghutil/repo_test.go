package ghutil

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

func TestResolveCanonicalRepo(t *testing.T) {
	faketool.GH{Repos: []faketool.GHRepo{{
		Requested:     "atqamz/secondhand",
		NameWithOwner: "atqamz/hand",
		URL:           "https://github.com/atqamz/hand",
	}}}.Install(t, faketool.Bin(t))

	got, err := ResolveCanonicalRepo(context.Background(), "atqamz/secondhand")
	if err != nil {
		t.Fatal(err)
	}
	if got.NameWithOwner != "atqamz/hand" || got.URL != "https://github.com/atqamz/hand" {
		t.Fatalf("canonical repo = %+v, want canonical name and URL", got)
	}
}

func TestResolveCanonicalRepoUnchanged(t *testing.T) {
	faketool.GH{Repos: []faketool.GHRepo{{
		Requested:     "atqamz/hand",
		NameWithOwner: "atqamz/hand",
		URL:           "https://github.com/atqamz/hand",
	}}}.Install(t, faketool.Bin(t))

	got, err := ResolveCanonicalRepo(context.Background(), "atqamz/hand")
	if err != nil {
		t.Fatal(err)
	}
	if got.NameWithOwner != "atqamz/hand" {
		t.Fatalf("canonical repo = %+v, want unchanged slug", got)
	}
}

func TestResolveCanonicalRepoReportsGHFailure(t *testing.T) {
	faketool.GH{}.Install(t, faketool.Bin(t))

	_, err := ResolveCanonicalRepo(context.Background(), "owner/missing")
	if err == nil || !strings.Contains(err.Error(), "gh repo view failed") {
		t.Fatalf("error = %v, want gh failure", err)
	}
}

func TestResolveCanonicalRepoRejectsMalformedJSON(t *testing.T) {
	faketool.GH{Repos: []faketool.GHRepo{{Requested: "owner/repo", Raw: "{"}}}.Install(t, faketool.Bin(t))

	_, err := ResolveCanonicalRepo(context.Background(), "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "parse gh repo view output") {
		t.Fatalf("error = %v, want malformed JSON failure", err)
	}
}

func TestResolveCanonicalRepoRequiresFields(t *testing.T) {
	faketool.GH{Repos: []faketool.GHRepo{{
		Requested: "owner/repo",
		Raw:       `{"nameWithOwner":"owner/repo"}`,
	}}}.Install(t, faketool.Bin(t))

	_, err := ResolveCanonicalRepo(context.Background(), "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "missing nameWithOwner or url") {
		t.Fatalf("error = %v, want missing-field failure", err)
	}
}

func TestRewriteGitHubRemotePreservesTransportAndSuffix(t *testing.T) {
	for _, test := range []struct {
		name string
		old  string
		want string
	}{
		{name: "https", old: "https://github.com/old/repo", want: "https://github.com/new/repo"},
		{name: "https git suffix", old: "https://github.com/old/repo.git", want: "https://github.com/new/repo.git"},
		{name: "scp ssh", old: "git@github.com:old/repo.git", want: "git@github.com:new/repo.git"},
		{name: "ssh URL", old: "ssh://git@github.com/old/repo.git", want: "ssh://git@github.com/new/repo.git"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := RewriteGitHubRemote(test.old, "new/repo")
			if !ok || got != test.want {
				t.Fatalf("RewriteGitHubRemote = %q, %v, want %q, true", got, ok, test.want)
			}
		})
	}
}

func TestRewriteGitHubRemoteRefusesUnsupportedRemote(t *testing.T) {
	for _, remote := range []string{
		"git://github.com/old/repo.git",
		"https://gitlab.com/old/repo.git",
		"/tmp/old-repo",
	} {
		if got, ok := RewriteGitHubRemote(remote, "new/repo"); ok || got != "" {
			t.Errorf("RewriteGitHubRemote(%q) = %q, %v, want empty false", remote, got, ok)
		}
	}
}
