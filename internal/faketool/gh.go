package faketool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// GHPR describes one pull request and the checks its fake reports.
type GHPR struct {
	Number     int
	URL        string
	Branch     string
	State      string
	Repo       string
	HeadRepo   string
	HeadRefOid string
	Checks     []string
}

type GHRepo struct {
	Requested     string
	NameWithOwner string
	URL           string
	Raw           string
}

// GHRelease describes the read and download operations used by self-update.
// Tag names the latest stable release; the Edge fields describe the mutable
// edge prerelease and the commit its ref points at.
type GHRelease struct {
	Tag        string
	Notes      string
	FixtureDir string
	Repo       string
	Patterns   []string
	EdgeCommit string
	EdgeNotes  string
	EdgeDir    string
}

// GHResponse provides a deliberately fixed result for one modeled gh command.
type GHResponse struct {
	Command string
	Repo    string
	Stdout  string
	Stderr  string
	Exit    int
	Copy    *GHCopy
}

type GHCopy struct {
	Source string
	Dest   string
}

// GH models the pull-request and release calls made by hand. ReleaseStore adds
// the release and asset writes the edge publish script makes, which no hand
// command performs.
type GH struct {
	PRs                 []GHPR
	Repos               []GHRepo
	Release             GHRelease
	ReleaseStore        *GHReleaseStore
	Responses           []GHResponse
	RejectQualifiedHead bool
	// Commands this fake never answers, for a caller timeout or cancellation path. Same shape and
	// same purpose as Herdr.Hang.
	Hang []string
	Log  string
}

type ghSpec struct {
	PRs                 []GHPR
	Repos               []GHRepo
	Release             GHRelease
	ReleaseStorePath    string
	ReleaseStoreRepo    string
	Responses           []GHResponse
	RejectQualifiedHead bool
	Hang                []string
	StateDir            string
	Log                 string
}

func (g GH) Install(t *testing.T, bin string) {
	t.Helper()
	state := stateDir(t, bin, "gh")
	for i, pr := range g.PRs {
		if pr.State == "" {
			pr.State = "OPEN"
		}
		path := ghStatePath(state, i)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			writeFile(t, path, pr.State+"\n")
		} else if err != nil {
			t.Fatal(err)
		}
	}
	spec := ghSpec{
		PRs: g.PRs, Repos: g.Repos, Release: g.Release, Responses: g.Responses,
		RejectQualifiedHead: g.RejectQualifiedHead, Hang: g.Hang, StateDir: state, Log: g.Log,
	}
	if g.ReleaseStore != nil {
		spec.ReleaseStorePath = g.ReleaseStore.install(t, state)
		spec.ReleaseStoreRepo = g.ReleaseStore.repo()
	}
	installConfig(t, bin, "gh", "gh", spec)
}

func runGHFromPayload(payload json.RawMessage, args []string) int {
	var spec ghSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		return fail("decode gh config: %v", err)
	}
	if spec.Log != "" {
		if err := appendInvocation(spec.Log, "gh", args); err != nil {
			return fail("log gh invocation: %v", err)
		}
	}
	if len(args) < 2 {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	command := args[0] + " " + args[1]
	for _, blocked := range spec.Hang {
		if blocked == command {
			for {
				time.Sleep(time.Hour)
			}
		}
	}
	if command == "pr list" && spec.RejectQualifiedHead && strings.Contains(flagValue(args, "--head"), ":") {
		return fail("qualified head ref matches nothing in real gh: %s", flagValue(args, "--head"))
	}
	for _, response := range spec.Responses {
		if response.Command != command {
			continue
		}
		if response.Repo != "" && !strings.EqualFold(response.Repo, flagValue(args, "--repo")) {
			continue
		}
		if response.Copy != nil {
			data, err := os.ReadFile(response.Copy.Source)
			if err != nil {
				return fail("read gh copy source: %v", err)
			}
			if err := atomicWrite(response.Copy.Dest, string(data)); err != nil {
				return fail("write gh copy destination: %v", err)
			}
		}
		_, _ = io.WriteString(os.Stdout, response.Stdout)
		_, _ = io.WriteString(os.Stderr, response.Stderr)
		return response.Exit
	}
	if args[0] == "api" {
		if code, handled := ghReleaseAPI(spec, args); handled {
			return code
		}
		return ghAPI(spec, args)
	}
	if code, handled := ghReleaseCommand(spec, command, args); handled {
		return code
	}
	switch command {
	case "repo view":
		return ghRepoView(spec, args)
	case "pr view":
		return ghPRView(spec, args)
	case "pr list":
		return ghPRList(spec, args)
	case "pr checks":
		return ghPRChecks(spec, args)
	case "pr merge":
		return ghPRMerge(spec, args)
	case "release view":
		return ghReleaseView(spec, args)
	case "release download":
		return ghReleaseDownload(spec, args)
	default:
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
}

func ghRepoView(spec ghSpec, args []string) int {
	if len(args) != 5 || args[3] != "--json" || args[4] != "nameWithOwner,url" {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	for _, repo := range spec.Repos {
		if !strings.EqualFold(repo.Requested, args[2]) {
			continue
		}
		body := repo.Raw
		if body == "" {
			body = fmt.Sprintf(`{"nameWithOwner":%s,"url":%s}`, jsonQuote(repo.NameWithOwner), jsonQuote(repo.URL))
		}
		_, _ = fmt.Fprintln(os.Stdout, body)
		return 0
	}
	return fail("repository not found: %s", args[2])
}

func ghPRView(spec ghSpec, args []string) int {
	if len(args) < 3 {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	index, ok := ghPRIndex(spec.PRs, args[2])
	if !ok {
		return ghNoSuchPR(args[2])
	}
	switch flagValue(args, "--json") {
	case "headRefOid":
		_, _ = fmt.Fprintf(os.Stdout, "{\"headRefOid\":%s}\n", jsonQuote(spec.PRs[index].HeadRefOid))
		return 0
	default:
		state, err := os.ReadFile(ghStatePath(spec.StateDir, index))
		if err != nil {
			return fail("read gh pull request state: %v", err)
		}
		_, _ = fmt.Fprintf(os.Stdout, "{\"state\":%s}\n", jsonQuote(strings.TrimSpace(string(state))))
		return 0
	}
}

func ghPRList(spec ghSpec, args []string) int {
	branch := flagValue(args, "--head")
	repo := flagValue(args, "--repo")
	var out strings.Builder
	out.WriteByte('[')
	sep := ""
	for i, pr := range spec.PRs {
		if pr.Branch == "" || pr.Branch != branch {
			continue
		}
		prRepo := pr.Repo
		if prRepo == "" {
			prRepo = "owner/repo"
		}
		if repo != "" && !strings.EqualFold(repo, prRepo) {
			continue
		}
		headRepo := pr.HeadRepo
		if headRepo == "" {
			headRepo = prRepo
		}
		state, err := os.ReadFile(ghStatePath(spec.StateDir, i))
		if err != nil {
			return fail("read gh pull request state: %v", err)
		}
		fmt.Fprintf(&out, "%s{\"number\":%d,\"url\":%s,\"state\":%s,\"headRepository\":{\"id\":\"R_1\",\"name\":%s,\"nameWithOwner\":%s}}",
			sep, pr.Number, jsonQuote(pr.URL), jsonQuote(strings.TrimSpace(string(state))),
			jsonQuote(repoName(headRepo)), jsonQuote(headRepo))
		sep = ","
	}
	out.WriteString("]\n")
	_, _ = io.WriteString(os.Stdout, out.String())
	return 0
}

func ghPRChecks(spec ghSpec, args []string) int {
	if len(args) < 3 {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	index, ok := ghPRIndex(spec.PRs, args[2])
	if !ok {
		return ghNoSuchPR(args[2])
	}
	_, _ = fmt.Fprintf(os.Stdout, "%s\n", ghBuckets(spec.PRs[index].Checks))
	return 0
}

func ghPRMerge(spec ghSpec, args []string) int {
	if len(args) < 3 {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	index, ok := ghPRIndex(spec.PRs, args[2])
	if !ok {
		return ghNoSuchPR(args[2])
	}
	path := ghStatePath(spec.StateDir, index)
	state, err := os.ReadFile(path)
	if err != nil {
		return fail("read gh pull request state: %v", err)
	}
	if strings.TrimSpace(string(state)) == "MERGED" {
		_, _ = fmt.Fprintf(os.Stderr, "! Pull request %s was already merged\n", args[2])
		return 0
	}
	if err := atomicWrite(path, "MERGED\n"); err != nil {
		return fail("write gh pull request state: %v", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "%s merged\n", args[2])
	return 0
}

// The mutable edge ref self-update reads for the freshness of an edge build.
func ghAPI(spec ghSpec, args []string) int {
	if !sameArgs(args, []string{"api", "repos/" + ghReleaseRepo(spec.Release) + "/commits/" + edgeTag, "--jq", ".sha"}) {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	if spec.Release.EdgeCommit == "" {
		return fail("ref not found: %s", args[1])
	}
	_, _ = io.WriteString(os.Stdout, spec.Release.EdgeCommit)
	return 0
}

func ghReleaseView(spec ghSpec, args []string) int {
	switch flagValue(args, "--jq") {
	case ".tagName":
		want := []string{"release", "view", "--repo", ghReleaseRepo(spec.Release), "--json", "tagName", "--jq", ".tagName"}
		if spec.Release.Tag != "" && sameArgs(args, want) {
			_, _ = io.WriteString(os.Stdout, spec.Release.Tag)
			return 0
		}
	case ".body":
		tag := ghReleaseTag(args)
		notes, ok := ghReleaseNotes(spec.Release, tag)
		want := []string{"release", "view", tag, "--repo", ghReleaseRepo(spec.Release), "--json", "body", "--jq", ".body"}
		if ok && sameArgs(args, want) {
			_, _ = io.WriteString(os.Stdout, notes)
			return 0
		}
	}
	return fail("unexpected gh invocation: %s", strings.Join(args, " "))
}

func ghReleaseDownload(spec ghSpec, args []string) int {
	tag := ghReleaseTag(args)
	fixtureDir, ok := ghReleaseFixtureDir(spec.Release, tag)
	if !ok {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	dir, ok := ghReleaseDownloadDir(spec.Release, tag, args)
	if !ok {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail("create gh release directory: %v", err)
	}
	for _, name := range ghReleasePatterns(spec.Release) {
		data, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			return fail("read gh release fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return fail("write gh release asset %s: %v", name, err)
		}
	}
	return 0
}

// The positional tag, absent when the caller asks for the latest release.
func ghReleaseTag(args []string) string {
	if len(args) < 3 || strings.HasPrefix(args[2], "-") {
		return ""
	}
	return args[2]
}

func ghReleaseNotes(release GHRelease, tag string) (string, bool) {
	switch {
	case tag == edgeTag && release.EdgeCommit != "":
		return release.EdgeNotes, true
	case tag != "" && tag == release.Tag:
		return release.Notes, true
	}
	return "", false
}

func ghReleaseFixtureDir(release GHRelease, tag string) (string, bool) {
	switch {
	case tag == edgeTag && release.EdgeDir != "":
		return release.EdgeDir, true
	case tag != "" && tag == release.Tag && release.FixtureDir != "":
		return release.FixtureDir, true
	}
	return "", false
}

const (
	defaultGHReleaseRepo = "atqamz/hand"
	edgeTag              = "edge"
)

func ghReleaseRepo(release GHRelease) string {
	if release.Repo != "" {
		return release.Repo
	}
	return defaultGHReleaseRepo
}

func ghReleasePatterns(release GHRelease) []string {
	return ghReleasePatternsFor(release, runtime.GOOS, runtime.GOARCH)
}

func ghReleasePatternsFor(release GHRelease, goos, goarch string) []string {
	if len(release.Patterns) > 0 {
		return append([]string(nil), release.Patterns...)
	}
	return []string{ghReleaseAssetName(goos, goarch), "checksums.txt"}
}

func ghReleaseAssetName(goos, goarch string) string {
	suffix := ".tar.gz"
	if goos == "windows" {
		suffix = ".zip"
	}
	return fmt.Sprintf("hand-%s-%s%s", goos, goarch, suffix)
}

func ghReleaseDownloadDir(release GHRelease, tag string, args []string) (string, bool) {
	patterns := ghReleasePatterns(release)
	if len(args) != 8+2*len(patterns) {
		return "", false
	}
	expected := []string{"release", "download", tag, "--repo", ghReleaseRepo(release), "--dir"}
	for i, want := range expected {
		if args[i] != want {
			return "", false
		}
	}
	dir := args[len(expected)]
	if dir == "" || args[len(expected)+1] != "--clobber" {
		return "", false
	}
	for i, pattern := range patterns {
		index := len(expected) + 2 + 2*i
		if args[index] != "--pattern" || args[index+1] != pattern {
			return "", false
		}
	}
	return dir, true
}

// The one diagnostic that proves a pull request is not there, quoted from real gh (FIDELITY.md):
// an unresolvable repository or a rejected credential answers differently and proves nothing.
func ghNoSuchPR(ref string) int {
	number := ref[strings.LastIndex(ref, "/")+1:]
	return fail("GraphQL: Could not resolve to a PullRequest with the number of %s. (repository.pullRequest)", number)
}

func ghPRIndex(prs []GHPR, ref string) (int, bool) {
	for i, pr := range prs {
		if ref == pr.URL || ref == strconv.Itoa(pr.Number) {
			return i, true
		}
	}
	return 0, false
}

func ghStatePath(dir string, index int) string {
	return filepath.Join(dir, fmt.Sprintf("pr%d", index))
}

func ghBuckets(checks []string) string {
	if len(checks) == 0 {
		checks = []string{"pass"}
	}
	items := make([]string, len(checks))
	for i, bucket := range checks {
		items[i] = "{\"bucket\":" + jsonQuote(bucket) + "}"
	}
	return "[" + strings.Join(items, ",") + "]"
}

func flagValue(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func repoName(slug string) string {
	if _, name, ok := strings.Cut(slug, "/"); ok {
		return name
	}
	return slug
}

func jsonQuote(s string) string {
	return strconv.Quote(s)
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
