package faketool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// GHReleaseAsset is one asset attached to a release in the mutable store.
type GHReleaseAsset struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// GHReleaseRecord is one release in the mutable store, draft included: the
// release list endpoint answers with drafts and the tag endpoint does not.
type GHReleaseRecord struct {
	ID         int              `json:"id"`
	TagName    string           `json:"tag_name"`
	Draft      bool             `json:"draft"`
	Prerelease bool             `json:"prerelease"`
	Target     string           `json:"target_commitish"`
	Assets     []GHReleaseAsset `json:"assets"`
}

// GHReleaseStore models the release and asset write calls that
// .github/scripts/edge-publish.sh makes. State survives across invocations of
// the fake, so a test asserts the asset set the script actually leaves behind.
type GHReleaseStore struct {
	Repo     string
	Releases []GHReleaseRecord
	NextID   int
}

func (s GHReleaseStore) repo() string {
	if s.Repo != "" {
		return s.Repo
	}
	return defaultGHReleaseRepo
}

func (s GHReleaseStore) install(t *testing.T, state string) string {
	t.Helper()
	path := ghReleaseStorePath(state)
	if _, err := os.Stat(path); err == nil {
		return path
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if s.NextID == 0 {
		s.NextID = 1
		for _, release := range s.Releases {
			for _, asset := range release.Assets {
				if asset.ID >= s.NextID {
					s.NextID = asset.ID + 1
				}
			}
			if release.ID >= s.NextID {
				s.NextID = release.ID + 1
			}
		}
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal gh release store: %v", err)
	}
	writeFile(t, path, string(data))
	return path
}

// GHReleases reads back the store the gh fake installed on bin, which is how a
// test asserts what a publish run left on the release.
func GHReleases(t *testing.T, bin string) GHReleaseStore {
	t.Helper()
	data, err := os.ReadFile(ghReleaseStorePath(stateDir(t, bin, "gh")))
	if err != nil {
		t.Fatalf("read gh release store: %v", err)
	}
	var store GHReleaseStore
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("decode gh release store: %v", err)
	}
	return store
}

// Release returns the release carrying tag, so a caller can assert its assets
// without hand-searching the store.
func (s GHReleaseStore) Release(tag string) (GHReleaseRecord, bool) {
	for _, release := range s.Releases {
		if release.TagName == tag {
			return release, true
		}
	}
	return GHReleaseRecord{}, false
}

// AssetNames lists the asset names of the release carrying tag, sorted the way
// the store holds them, or nil when no such release exists.
func (s GHReleaseStore) AssetNames(tag string) []string {
	release, ok := s.Release(tag)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(release.Assets))
	for _, asset := range release.Assets {
		names = append(names, asset.Name)
	}
	return names
}

func ghReleaseStorePath(state string) string {
	return filepath.Join(state, "releases.json")
}

func loadGHReleaseStore(path string) (*GHReleaseStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var store GHReleaseStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	return &store, nil
}

func (s *GHReleaseStore) save(path string) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWrite(path, string(data))
}

func (s *GHReleaseStore) take() int {
	id := s.NextID
	s.NextID++
	return id
}

func (s *GHReleaseStore) byTag(tag string) *GHReleaseRecord {
	for i := range s.Releases {
		if s.Releases[i].TagName == tag {
			return &s.Releases[i]
		}
	}
	return nil
}

func (s *GHReleaseStore) byID(id int) *GHReleaseRecord {
	for i := range s.Releases {
		if s.Releases[i].ID == id {
			return &s.Releases[i]
		}
	}
	return nil
}

func (s *GHReleaseStore) dropRelease(id int) bool {
	for i := range s.Releases {
		if s.Releases[i].ID != id {
			continue
		}
		s.Releases = append(s.Releases[:i], s.Releases[i+1:]...)
		return true
	}
	return false
}

func (s *GHReleaseStore) findAsset(id int) (*GHReleaseRecord, int) {
	for i := range s.Releases {
		for j := range s.Releases[i].Assets {
			if s.Releases[i].Assets[j].ID == id {
				return &s.Releases[i], j
			}
		}
	}
	return nil, -1
}

// Real gh renders --jq with jq's own semantics, so the fake shells out to jq
// rather than reimplementing the programs the publish script relies on.
func ghJQ(program string, doc any) (string, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("jq", "-r", program)
	cmd.Stdin = strings.NewReader(string(data))
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("jq %s: %w: %s", program, err, errOut.String())
	}
	return out.String(), nil
}

func ghEmit(args []string, doc any) int {
	program := flagValue(args, "--jq")
	if program == "" {
		data, err := json.Marshal(doc)
		if err != nil {
			return fail("encode gh response: %v", err)
		}
		_, _ = fmt.Fprintln(os.Stdout, string(data))
		return 0
	}
	rendered, err := ghJQ(program, doc)
	if err != nil {
		return fail("%v", err)
	}
	_, _ = io.WriteString(os.Stdout, rendered)
	return 0
}

// Reports whether the store answers this api endpoint, so an unmodeled call
// still reaches the existing unexpected-invocation failure.
func ghReleaseAPI(spec ghSpec, args []string) (int, bool) {
	if spec.ReleaseStorePath == "" {
		return 0, false
	}
	endpoint, _, _ := strings.Cut(ghAPIEndpoint(args), "?")
	prefix := "repos/" + spec.ReleaseStoreRepo + "/releases"
	if endpoint != prefix && !strings.HasPrefix(endpoint, prefix+"/") {
		return 0, false
	}
	store, err := loadGHReleaseStore(spec.ReleaseStorePath)
	if err != nil {
		return fail("read gh release store: %v", err), true
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(endpoint, prefix), "/")
	method := flagValue(args, "--method")
	if method == "" {
		method = "GET"
	}
	switch {
	case rest == "" && method == "GET":
		if store.Releases == nil {
			return ghEmit(args, []GHReleaseRecord{}), true
		}
		return ghEmit(args, store.Releases), true
	case strings.HasSuffix(rest, "/assets") && method == "GET":
		id, err := strconv.Atoi(strings.TrimSuffix(rest, "/assets"))
		if err != nil {
			return fail("unexpected gh invocation: %s", strings.Join(args, " ")), true
		}
		release := store.byID(id)
		if release == nil {
			return fail("release not found: %d", id), true
		}
		if release.Assets == nil {
			return ghEmit(args, []GHReleaseAsset{}), true
		}
		return ghEmit(args, release.Assets), true
	case strings.HasPrefix(rest, "assets/"):
		return ghReleaseAssetAPI(spec, args, store, method, strings.TrimPrefix(rest, "assets/")), true
	case method == "DELETE":
		id, err := strconv.Atoi(rest)
		if err != nil {
			return fail("unexpected gh invocation: %s", strings.Join(args, " ")), true
		}
		if !store.dropRelease(id) {
			return fail("release not found: %d", id), true
		}
		return ghReleaseSave(spec, store), true
	}
	return fail("unexpected gh invocation: %s", strings.Join(args, " ")), true
}

func ghReleaseAssetAPI(spec ghSpec, args []string, store *GHReleaseStore, method, raw string) int {
	id, err := strconv.Atoi(raw)
	if err != nil {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	release, index := store.findAsset(id)
	if release == nil {
		return fail("asset not found: %d", id)
	}
	switch method {
	case "DELETE":
		release.Assets = append(release.Assets[:index], release.Assets[index+1:]...)
		return ghReleaseSave(spec, store)
	case "PATCH":
		name := strings.TrimPrefix(flagValue(args, "-f"), "name=")
		if name == "" || name == flagValue(args, "-f") {
			return fail("unexpected gh invocation: %s", strings.Join(args, " "))
		}
		for i, asset := range release.Assets {
			if i != index && asset.Name == name {
				_, _ = fmt.Fprintf(os.Stderr,
					"gh: Validation Failed (HTTP 422)\nname already_exists: %s\n", name)
				return 1
			}
		}
		release.Assets[index].Name = name
		if code := ghReleaseSave(spec, store); code != 0 {
			return code
		}
		return ghEmit(args, release.Assets[index])
	}
	return fail("unexpected gh invocation: %s", strings.Join(args, " "))
}

func ghReleaseSave(spec ghSpec, store *GHReleaseStore) int {
	if err := store.save(spec.ReleaseStorePath); err != nil {
		return fail("write gh release store: %v", err)
	}
	return 0
}

// Reports whether the store answers this release subcommand, so a suite that
// never installs one keeps the existing read-only release behavior.
func ghReleaseCommand(spec ghSpec, command string, args []string) (int, bool) {
	if spec.ReleaseStorePath == "" {
		return 0, false
	}
	if repo := flagValue(args, "--repo"); repo != "" && !strings.EqualFold(repo, spec.ReleaseStoreRepo) {
		return 0, false
	}
	store, err := loadGHReleaseStore(spec.ReleaseStorePath)
	if err != nil {
		return fail("read gh release store: %v", err), true
	}
	tag := ghReleaseTag(args)
	switch command {
	case "release create":
		return ghReleaseCreate(spec, args, store, tag), true
	case "release upload":
		return ghReleaseUpload(spec, args, store, tag), true
	case "release edit":
		return ghReleaseEdit(spec, args, store, tag), true
	case "release view":
		if flagValue(args, "--json") != "databaseId" {
			return 0, false
		}
		release := store.byTag(tag)
		if release == nil {
			return fail("release not found: %s", tag), true
		}
		return ghEmit(args, map[string]int{"databaseId": release.ID}), true
	}
	return 0, false
}

func ghReleaseCreate(spec ghSpec, args []string, store *GHReleaseStore, tag string) int {
	if tag == "" {
		return fail("unexpected gh invocation: %s", strings.Join(args, " "))
	}
	if store.byTag(tag) != nil {
		return fail("a release with tag %s already exists", tag)
	}
	store.Releases = append(store.Releases, GHReleaseRecord{
		ID:         store.take(),
		TagName:    tag,
		Draft:      hasFlag(args, "--draft"),
		Prerelease: hasFlag(args, "--prerelease"),
		Target:     flagValue(args, "--target"),
	})
	if code := ghReleaseSave(spec, store); code != 0 {
		return code
	}
	_, _ = fmt.Fprintf(os.Stdout, "https://github.com/%s/releases/tag/%s\n", spec.ReleaseStoreRepo, tag)
	return 0
}

func ghReleaseUpload(spec ghSpec, args []string, store *GHReleaseStore, tag string) int {
	release := store.byTag(tag)
	if release == nil {
		return fail("release not found: %s", tag)
	}
	for i := 3; i < len(args); i++ {
		if args[i] == "--repo" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		if _, err := os.Stat(args[i]); err != nil {
			return fail("open %s: %v", args[i], err)
		}
		name := filepath.Base(args[i])
		for _, asset := range release.Assets {
			if asset.Name == name {
				return fail("an asset called %s already exists", name)
			}
		}
		release.Assets = append(release.Assets, GHReleaseAsset{ID: store.take(), Name: name})
	}
	return ghReleaseSave(spec, store)
}

func ghReleaseEdit(spec ghSpec, args []string, store *GHReleaseStore, tag string) int {
	release := store.byTag(tag)
	if release == nil {
		return fail("release not found: %s", tag)
	}
	if renamed := flagValue(args, "--tag"); renamed != "" && renamed != tag {
		if store.byTag(renamed) != nil {
			return fail("a release with tag %s already exists", renamed)
		}
		release.TagName = renamed
	}
	if hasFlag(args, "--draft=false") {
		release.Draft = false
	}
	release.Prerelease = release.Prerelease || hasFlag(args, "--prerelease")
	return ghReleaseSave(spec, store)
}

// The first positional argument after `api`, which is the endpoint however many
// flags gh was handed before it.
func ghAPIEndpoint(args []string) string {
	takesValue := map[string]bool{
		"--method": true, "-X": true, "--jq": true, "-q": true,
		"-f": true, "-F": true, "-H": true, "--template": true, "--input": true,
	}
	for i := 1; i < len(args); i++ {
		if takesValue[args[i]] {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i]
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}
