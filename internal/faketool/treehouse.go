package faketool

import (
	"fmt"
	"strings"
	"testing"
)

// Version banner written to stderr ahead of a "get" payload, matching the
// treehouse whose behaviour FIDELITY.md records.
const TreehouseBanner = "treehouse 2.1.0"

// A fake treehouse pool: "get" leases slots in the order listed and "return"
// frees them, so it answers differently before and after each command that
// changes it, and every acquisition gets a lease identity of its own.
type Treehouse struct {
	Slots []string
	// Leased before the test began, for a task whose row was seeded not spawned.
	Held []string
	// Models a treehouse older than v2.1.0, which reports no lease identity.
	NoLeaseIdentity bool
	Log             string
}

// Writes the fake into bin, which Bin has put on PATH.
func (th Treehouse) Install(t *testing.T, bin string) {
	t.Helper()
	state := stateDir(t, bin, "treehouse")
	counter := quote(state + "/leases")

	marker := func(slot string) string { return quote(state + "/slot-" + key(slot)) }

	var acquire strings.Builder
	for _, slot := range th.Slots {
		payload := fmt.Sprintf(`'{"path":"%%s","lease_id":"lease-%%s","lease_holder":"","leased_at":"2026-01-01T00:00:00Z"}\n' %s "$n"`, quote(slot))
		if th.NoLeaseIdentity {
			payload = fmt.Sprintf(`'{"path":"%%s"}\n' %s`, quote(slot))
		}
		fmt.Fprintf(&acquire, `    if [ ! -s %[1]s ]; then
      n=0; [ -f %[2]s ] && read n < %[2]s
      n=$((n+1)); echo "$n" > %[2]s
      echo leased > %[1]s
      printf %[3]s
      exit 0
    fi
`, marker(slot), counter, payload)
	}

	var resolve strings.Builder
	seen := map[string]bool{}
	for _, slot := range append(append([]string{}, th.Slots...), th.Held...) {
		if seen[slot] {
			continue
		}
		seen[slot] = true
		fmt.Fprintf(&resolve, "      %s) marker=%s ;;\n", quote(slot), marker(slot))
	}

	// The abort exits 0, which is the whole reason this arm exists: with no answer
	// to its prompt treehouse reports the abort on stderr and still exits 0, so an
	// exit-code-only caller reads a return that never happened as success.
	body := fmt.Sprintf(`  get)
    echo %[1]s >&2
%[2]s    printf 'all %[3]d worktrees are in use or dirty (max_trees = %[3]d). Run '"'"'treehouse status'"'"' to see details, or increase max_trees in treehouse.toml\n' >&2
    exit 1
    ;;
  return)
    path="$2"
    forced=""
    for arg in "$@"; do
      if [ "$arg" = "--force" ]; then forced=1; fi
    done
    marker=""
    case "$path" in
%[4]s    esac
    if [ -z "$marker" ]; then
      echo "worktree $path is not managed by treehouse" >&2
      exit 1
    fi
    dirty=""
    if [ -e "$path/.git" ] && [ -n "$(git -C "$path" status --porcelain)" ]; then dirty=1; fi
    if [ -n "$dirty" ] && [ -z "$forced" ]; then
      echo 'Worktree has uncommitted changes. Clean and return? [Y/n] Aborted.' >&2
      exit 0
    fi
    if [ -n "$dirty" ]; then
      git -C "$path" reset -q --hard HEAD
      git -C "$path" clean -qfd
    fi
    : > "$marker"
    echo 'Worktree returned to pool.' >&2
    ;;
  init)
    if [ -f treehouse.toml ]; then
      echo 'treehouse.toml already exists' >&2
      exit 1
    fi
    echo 'max_trees = 16' > treehouse.toml
    echo "Created $(pwd)/treehouse.toml"
    ;;`, quote(TreehouseBanner), acquire.String(), len(th.Slots), resolve.String())

	install(t, bin, "treehouse", th.Log, "$1", body)

	for _, slot := range th.Held {
		writeFile(t, state+"/slot-"+key(slot), "leased\n")
	}
}
