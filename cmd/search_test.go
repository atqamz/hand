package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func writeCorpusFile(t *testing.T, home, rel, body string) {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runSearch(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newSearchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestSearchFindsABriefByItsProse(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Rework the merge queue\n\nThe queue drains out of order under load.\n")
	writeCorpusFile(t, home, "data/task-2/brief.md", "# Rename the config loader\n\nNothing to do with queues.\n")

	out, _, err := runSearch(t, "drains", "out", "of", "order")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, filepath.Join("data", "task-1", "brief.md")) {
		t.Fatalf("got %q, want the matching brief's path", out)
	}
	if strings.Contains(out, "task-2") {
		t.Fatalf("got %q, want the unrelated brief left out", out)
	}
	if !strings.Contains(out, "Rework the merge queue") {
		t.Fatalf("got %q, want the document's title alongside its path", out)
	}
}

// Prose carries hyphens and colons that FTS5 reads as operators, so a query a
// supervisor would actually type has to match as the literal text it looks
// like rather than fail as a malformed expression.
func TestSearchTreatsPunctuationInAQueryAsLiteralText(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Gate\n\nDrive the no-mistakes gate with axi respond.\n")

	out, _, err := runSearch(t, "no-mistakes", "gate")
	if err != nil {
		t.Fatalf("got %v, want a hyphenated query treated as text", err)
	}
	if !strings.Contains(out, "task-1") {
		t.Fatalf("got %q, want the hyphenated phrase matched", out)
	}
}

// A search that matched nothing states its zero on stdout: silence there is
// the one answer a search that never ran also produces.
func TestSearchStatesAZeroCountOnStdout(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Gate\n\nDrive the gate.\n")

	out, _, err := runSearch(t, "nothing", "matches", "this")
	if err != nil {
		t.Fatal(err)
	}
	want := "query: nothing matches this\n" +
		"count: 0\n" +
		"hits[0]{path,title,snippet}:\n" +
		"help[2]:\n" +
		"  - Every token has to match: drop one to widen the query\n" +
		"  - Run `hand search --rebuild nothing matches this` if the corpus changed but the index did not\n"
	if out != want {
		t.Fatalf("got stdout %q, want %q", out, want)
	}
}

// The --limit cap and a corpus that genuinely holds no more look identical in
// the rows, so the capped run has to say which one it was.
func TestSearchAtTheLimitSaysThereMayBeMore(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		writeCorpusFile(t, home, filepath.Join("data", id, "brief.md"), "# "+id+"\n\nThe queue drains under load.\n")
	}

	capped, _, err := runSearch(t, "--limit", "2", "queue")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(capped, "count: 2\n") || !strings.Contains(capped, "`hand search --limit 4 queue`") {
		t.Fatalf("got %q, want the cap named alongside the wider query that lifts it", capped)
	}

	under, _, err := runSearch(t, "--limit", "9", "queue")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(under, "--limit 18") {
		t.Fatalf("got %q, want no cap warning when the result came in under the limit", under)
	}
}

func TestSearchFieldsNarrowsTheSchemaHeaderWithTheRows(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Rework the merge queue\n\nThe queue drains under load.\n")

	out, _, err := runSearch(t, "--fields", "path", "drains")
	if err != nil {
		t.Fatal(err)
	}
	want := "hits[1]{path}:\n  " + filepath.Join("data", "task-1", "brief.md") + "\n"
	if !strings.Contains(out, want) {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestSearchFieldsWithJSONIsAUsageError(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	_, _, err := runSearch(t, "--json", "--fields", "path", "drains")
	if got := exitCodeFor(t, err); got != 2 {
		t.Fatalf("exit code = %d, want 2 for --fields alongside --json", got)
	}
}

// A machine consumer that finds nothing must still get a list, since `null`
// makes every caller special-case the empty case before it can iterate.
func TestSearchJSONRendersAnEmptyResultAsAnEmptyList(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Gate\n\nDrive the gate.\n")

	out, _, err := runSearch(t, "--json", "nothing", "matches", "this")
	if err != nil {
		t.Fatal(err)
	}
	var hits []store.Hit
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if hits == nil || len(hits) != 0 {
		t.Fatalf("got %#v from %q, want an empty list", hits, out)
	}
}

func TestSearchJSONCarriesPathTitleAndSnippet(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Rework the merge queue\n\nThe queue drains out of order under load.\n")

	out, _, err := runSearch(t, "--json", "drains")
	if err != nil {
		t.Fatal(err)
	}
	var hits []store.Hit
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits from %q, want exactly the one match", len(hits), out)
	}
	if hits[0].Path != filepath.Join("data", "task-1", "brief.md") {
		t.Fatalf("got path %q, want it relative to the fleet home", hits[0].Path)
	}
	if hits[0].Title != "Rework the merge queue" {
		t.Fatalf("got title %q, want the document's heading", hits[0].Title)
	}
	if !strings.Contains(hits[0].Snippet, "drains") {
		t.Fatalf("got snippet %q, want the matched term in it", hits[0].Snippet)
	}
}

func TestSearchLimitsTheNumberOfHits(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		writeCorpusFile(t, home, filepath.Join("data", id, "brief.md"), "# "+id+"\n\nThe queue drains under load.\n")
	}

	out, _, err := runSearch(t, "--json", "--limit", "2", "queue")
	if err != nil {
		t.Fatal(err)
	}
	var hits []store.Hit
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want the limit honored", len(hits))
	}
}

// Searching a home whose corpus has never been indexed must answer from the
// corpus, not from the absence of an index: the index is derived, so building it
// is the query's job and never a step an operator has to remember.
func TestSearchBuildsTheIndexOnFirstUse(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Gate\n\nThe queue drains under load.\n")

	if _, err := os.Stat(store.IndexPath(home)); !os.IsNotExist(err) {
		t.Fatalf("stat index before search: %v, want it absent", err)
	}
	out, _, err := runSearch(t, "drains")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "task-1") {
		t.Fatalf("got %q, want the match found without a prior index", out)
	}
}

// Deleting the index is the documented recovery, so the next search has to
// rebuild it rather than answer from the hole it left.
func TestSearchAnswersAgainAfterTheIndexIsDeleted(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Gate\n\nThe queue drains under load.\n")

	if _, _, err := runSearch(t, "drains"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.IndexPath(home)); err != nil {
		t.Fatal(err)
	}

	out, _, err := runSearch(t, "drains")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "task-1") {
		t.Fatalf("got %q, want the deleted index rebuilt from the corpus", out)
	}
}

// --rebuild is the recovery for an index that is present but wrong, which is
// exactly what a truncated or overwritten file looks like from the outside.
func TestSearchRebuildRecoversACorruptIndex(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Gate\n\nThe queue drains under load.\n")

	if _, _, err := runSearch(t, "drains"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.IndexPath(home), []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := runSearch(t, "--rebuild", "drains")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "rebuilt index") {
		t.Fatalf("got stderr %q, want the rebuild reported", errOut)
	}
	if !strings.Contains(out, "task-1") {
		t.Fatalf("got %q, want the corpus searchable again after the rebuild", out)
	}
}

// The rebuild reads the corpus and nothing else, so a machine-state database
// that is missing or unreadable cannot stop a supervisor from searching their
// way back to what the fleet was doing.
func TestSearchDoesNotDependOnMachineState(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeCorpusFile(t, home, "data/task-1/brief.md", "# Gate\n\nThe queue drains under load.\n")
	if err := os.WriteFile(store.Path(home), []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runSearch(t, "drains")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "task-1") {
		t.Fatalf("got %q, want the corpus searchable with machine state broken", out)
	}
}

func TestSearchWithoutAQueryIsAUsageError(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	_, _, err := runSearch(t)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("got %v, want ExitError code 2", err)
	}
}
