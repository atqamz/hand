package faketool

import (
	"fmt"
	"strings"
	"testing"
)

// One pull request the fake gh knows about. State is where it starts; a merge
// through the fake moves it to MERGED. Checks are the buckets `pr checks`
// reports, defaulting to a single pass.
type GHPR struct {
	Number int
	URL    string
	Branch string
	State  string
	// The repo the PR lives in, which `pr list --repo` matches in any casing.
	Repo string
	// The repo the head branch lives in, for a PR opened from a fork. Defaults to
	// Repo, which is the same-repo case.
	HeadRepo string
	Checks   []string
}

type GHRepo struct {
	Requested     string
	NameWithOwner string
	URL           string
	Raw           string
}

// A fake gh whose pull requests carry their own state, so `pr view` answers
// MERGED after `pr merge` rather than repeating whatever it said before.
// FIDELITY.md records this against the real tool.
type GH struct {
	PRs   []GHPR
	Repos []GHRepo
	Log   string
}

// Writes the fake into bin, which Bin has put on PATH.
func (g GH) Install(t *testing.T, bin string) {
	t.Helper()
	state := stateDir(t, bin, "gh")
	stateFile := func(i int) string { return quote(fmt.Sprintf("%s/pr%d", state, i)) }

	// gh takes a URL or a number here, either of which resolves to the same PR.
	var byRef, checks strings.Builder
	for i, pr := range g.PRs {
		for _, ref := range []string{pr.URL, fmt.Sprintf("%d", pr.Number)} {
			if ref == "" || ref == "0" {
				continue
			}
			fmt.Fprintf(&byRef, "    %s) f=%s ;;\n", quote(ref), stateFile(i))
			fmt.Fprintf(&checks, "    %s) printf '%s\\n' ;;\n", quote(ref), ghBuckets(pr.Checks))
		}
	}

	var repoView strings.Builder
	for _, repo := range g.Repos {
		if repo.Requested == "" {
			continue
		}
		body := repo.Raw
		if body == "" {
			body = fmt.Sprintf(`{"nameWithOwner":%s,"url":%s}`, jsonQuote(repo.NameWithOwner), jsonQuote(repo.URL))
		}
		fmt.Fprintf(&repoView, "    %s) printf '%%s\\n' %s ;;\n", quote(repo.Requested), quote(body))
	}

	// One block per PR rather than one per branch, because --repo narrows the search
	// independently of --head: a fork project searches two repos for the same branch
	// and has to see a different answer from each.
	var list strings.Builder
	list.WriteString("    branch=$(argval --head \"$@\")\n    repo=$(argval --repo \"$@\")\n    printf '['\n    sep=\"\"\n")
	for i, pr := range g.PRs {
		if pr.Branch == "" {
			continue
		}
		repo := pr.Repo
		if repo == "" {
			repo = "owner/repo"
		}
		headRepo := pr.HeadRepo
		if headRepo == "" {
			headRepo = repo
		}
		fmt.Fprintf(&list, `    if [ "$branch" = %[1]s ] && { [ -z "$repo" ] || anycase %[2]s "$repo"; }; then
      read s < %[3]s
      printf '%%s{"number":%[4]d,"url":%[5]s,"state":"%%s","headRepository":{"id":"R_1","name":%[6]s,"nameWithOwner":%[7]s}}' "$sep" "$s"
      sep=","
    fi
`, quote(pr.Branch), quote(anyCasePattern(repo)), stateFile(i), pr.Number,
			jsonQuote(pr.URL), jsonQuote(repoName(headRepo)), jsonQuote(headRepo))
	}
	// An empty result is exit 0 with an empty array, not a failure: a branch with no
	// PR is the ordinary case teardown's landed-work check has to handle.
	list.WriteString("    printf ']\\n'\n")

	prelude := "argval() { flag=\"$1\"; shift; while [ $# -gt 0 ]; do if [ \"$1\" = \"$flag\" ]; then echo \"$2\"; return; fi; shift; done; }\n" +
		// GitHub serves a repo under any casing of its slug, so a case-sensitive match
		// would answer a double search of one repo with one hit and hide the duplicate
		// the real gh returns.
		"anycase() { case \"$2\" in $1) return 0 ;; esac; return 1; }\n"
	body := fmt.Sprintf(`  "repo view")
    case "$3" in
%[1]s    *) echo "repository not found: $3" >&2; exit 1 ;;
    esac
    ;;
  "pr view")
    f=""
    case "$3" in
%[2]s    *) echo "no such pull request: $3" >&2; exit 1 ;;
    esac
    read s < "$f"
    printf '{"state":"%%s"}\n' "$s"
    ;;
  "pr list")
%[3]s    ;;
  "pr checks")
    case "$3" in
%[4]s    *) echo "no such pull request: $3" >&2; exit 1 ;;
    esac
    ;;
  "pr merge")
    f=""
    case "$3" in
%[2]s    *) echo "no such pull request: $3" >&2; exit 1 ;;
    esac
    read s < "$f"
    if [ "$s" = MERGED ]; then
      echo "! Pull request $3 was already merged" >&2
      exit 0
    fi
    echo MERGED > "$f"
    printf '%%s merged\n' "$3"
	    ;;`, repoView.String(), byRef.String(), list.String(), checks.String())

	install(t, bin, "gh", g.Log, prelude, "$1 $2", body)

	for i, pr := range g.PRs {
		s := pr.State
		if s == "" {
			s = "OPEN"
		}
		writeFile(t, fmt.Sprintf("%s/pr%d", state, i), s+"\n")
	}
}

func ghBuckets(checks []string) string {
	if len(checks) == 0 {
		checks = []string{"pass"}
	}
	items := make([]string, len(checks))
	for i, bucket := range checks {
		items[i] = `{"bucket":"` + bucket + `"}`
	}
	return "[" + strings.Join(items, ",") + "]"
}

// A glob matching s in any casing, since POSIX sh has no case conversion and the
// hermetic PATH in tests/e2e has no tr.
func anyCasePattern(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteString("[" + string(r-32) + string(r) + "]")
		case r >= 'A' && r <= 'Z':
			b.WriteString("[" + string(r) + string(r+32) + "]")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func repoName(slug string) string {
	if _, name, ok := strings.Cut(slug, "/"); ok {
		return name
	}
	return slug
}
