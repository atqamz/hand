package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestEnsureAdoptsDeterministicGenerationWithoutSelection(t *testing.T) {
	store, requests := generationStoreFixture(t)
	first, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Lock.Target("", "")
	if err != nil {
		t.Fatal(err)
	}
	wantName, err := generationBundleName(store.Lock.RuntimeID, first.Target, target)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(first.BundleDir), filepath.Base(wantName); got != want {
		t.Fatalf("generation directory = %q, want deterministic %q", got, want)
	}

	currentPath := filepath.Join(store.Root, "runtime", currentName)
	if err := os.Remove(currentPath); err != nil {
		t.Fatal(err)
	}
	second, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.BundleDir != first.BundleDir {
		t.Fatalf("adopted bundle = %q, want existing exact generation %q", second.BundleDir, first.BundleDir)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("artifact requests = %d, want one materialization", got)
	}
	entries, err := os.ReadDir(filepath.Join(store.Root, "runtime", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("generation count = %d, want one", len(entries))
	}
}

func TestResolveUsesExactGenerationAcrossFleetsWithoutSelection(t *testing.T) {
	owner, _ := generationStoreFixture(t)
	want, err := owner.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewStore(owner.Root, owner.Lock)
	if err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(owner.Root, "runtime", currentName)
	selected, err := owner.readCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(currentPath); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Selected("", ""); err == nil {
		t.Fatal("selection unexpectedly survived pointer removal")
	}
	resolved, err := other.resolve()
	if err != nil || resolved.BundleDir != want.BundleDir {
		t.Fatalf("resolve after another Fleet removes selection = %q, %v; want %q", resolved.BundleDir, err, want.BundleDir)
	}
	if _, err := os.Stat(currentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only resolution changed missing selection: %v", err)
	}
	if status, err := other.Status("", ""); err != nil || !status.Ready || status.Selection != "absent" || status.BundleDir != want.BundleDir {
		t.Fatalf("runtime status without selection = %+v, %v; want ready exact generation and absent selection", status, err)
	}

	alias := filepath.Join("bundles", "pointer-alias")
	copyTree(t, want.BundleDir, filepath.Join(owner.Root, "runtime", alias))
	selected.Bundle = filepath.ToSlash(alias)
	data, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if pointed, err := other.Selected("", ""); err != nil || pointed.BundleDir == want.BundleDir {
		t.Fatalf("alias fixture did not select the alternate path: %q, %v", pointed.BundleDir, err)
	}
	resolved, err = other.resolve()
	if err != nil || resolved.BundleDir != want.BundleDir {
		t.Fatalf("resolve with alias selection = %q, %v; want %q", resolved.BundleDir, err, want.BundleDir)
	}
	if after, err := os.ReadFile(currentPath); err != nil || string(after) != string(data) {
		t.Fatalf("read-only resolution changed alias selection: %q, %v", after, err)
	}
	if status, err := other.Status("", ""); err != nil || !status.Ready || status.Selection != "present" || status.BundleDir != want.BundleDir {
		t.Fatalf("runtime status with alias selection = %+v, %v; want ready exact generation and present selection", status, err)
	}

	if err := os.WriteFile(want.HerdrPath, []byte("corrupt exact generation"), 0o700); err != nil {
		t.Fatal(err)
	}
	if resolved, err := other.resolve(); err == nil {
		t.Fatalf("resolved invalid exact generation %q", resolved.BundleDir)
	}
	if status, err := other.Status("", ""); err != nil || status.Ready {
		t.Fatalf("runtime status for invalid exact generation = %+v, %v; want not ready", status, err)
	}
}

func TestMaterializeGenerationDoesNotChangeSelection(t *testing.T) {
	store, _ := generationStoreFixture(t)
	currentPath := filepath.Join(store.Root, "runtime", currentName)
	if err := os.MkdirAll(filepath.Dir(currentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := []byte("foreign release selection\n")
	if err := os.WriteFile(currentPath, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := store.MaterializeGeneration(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(runtime.BundleDir) == "" {
		t.Fatal("materialized generation has no exact identity")
	}
	after, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(foreign) {
		t.Fatalf("materialization changed selection to %q, want %q", after, foreign)
	}
}

func TestEnsureIgnoresMalformedStaleAndReplacedSelection(t *testing.T) {
	store, requests := generationStoreFixture(t)
	want, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(store.Root, "runtime", currentName)
	original, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	var selected Current
	if err := json.Unmarshal(original, &selected); err != nil {
		t.Fatal(err)
	}

	aliasName := filepath.Join("bundles", "pointer-alias")
	copyTree(t, want.BundleDir, filepath.Join(store.Root, "runtime", aliasName))
	replaced := selected
	replaced.Bundle = filepath.ToSlash(aliasName)
	replacedData, err := json.Marshal(replaced)
	if err != nil {
		t.Fatal(err)
	}

	for name, pointer := range map[string][]byte{
		"malformed": []byte("{truncated"),
		"stale":     []byte(`{"schema":1,"runtime_id":"stale","target":"unknown","bundle":"bundles/missing"}`),
		"replaced":  replacedData,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(currentPath, pointer, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := store.Ensure(context.Background(), "", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.BundleDir != want.BundleDir {
				t.Fatalf("bundle = %q, want deterministic generation %q", got.BundleDir, want.BundleDir)
			}
		})
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("artifact requests = %d, want no rematerialization", got)
	}
}

func TestEnsureRefusesUnknownExactGenerationManifestWithoutRewrite(t *testing.T) {
	store, requests := generationStoreFixture(t)
	runtime, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(runtime.BundleDir, manifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["unknown_ownership"] = "unverified"
	tampered, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Root, "runtime", currentName)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ensure(context.Background(), "", ""); err == nil {
		t.Fatal("unknown exact-generation manifest field was adopted")
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil || string(after) != string(tampered) {
		t.Fatalf("refusal rewrote invalid generation: %q, %v", after, err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("refusal downloaded %d artifacts, want no repair rewrite", got)
	}
}

func TestGenerationRejectsSymlinkedBundleParent(t *testing.T) {
	store, _ := generationStoreFixture(t)
	runtime, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	bundles := filepath.Join(store.Root, "runtime", "bundles")
	external := filepath.Join(t.TempDir(), "external-bundles")
	if err := os.Rename(bundles, external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, bundles); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.Generation(filepath.Base(runtime.BundleDir), "", ""); err == nil {
		t.Fatal("generation through a symlinked bundle parent was accepted")
	}
}

func TestOpenDirectRuntimeRootAllowsConfiguredRootSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "configured-home")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	h, err := openDirectRuntimeRoot(link)
	if err != nil {
		t.Fatalf("open configured root symlink: %v", err)
	}
	defer func() { _ = h.Close() }()
}

func TestRuntimeVerificationStageIgnoresTMPDIR(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(store.Root, "does-not-exist"))
	if _, err := store.Selected("", ""); err != nil {
		t.Fatalf("selected with unusable TMPDIR: %v", err)
	}
}

func TestRuntimeFileAccessRemainsAnchoredAcrossRootReplacement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "secondhand")
	path := filepath.Join(root, "runtime", "current.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootHandle, err := openDirectRuntimeRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rootHandle.Close() }()
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		if runtime.GOOS == "windows" {
			// Windows retains the open rooted directory handle exclusively;
			// denial is the platform's anchored-path guarantee.
			return
		}
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := readRuntimeFile(rootHandle, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("rooted read followed replacement path: %q", data)
	}
}

func TestRuntimeGenerationSelectionRemainsAnchoredAcrossRootReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows denies replacement of an open rooted directory")
	}
	store, _ := generationStoreFixture(t)
	installed, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Lock.Target("", "")
	if err != nil {
		t.Fatal(err)
	}
	bundleName, err := generationBundleName(store.Lock.RuntimeID, installed.Target, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Root, "runtime", currentName)); err != nil {
		t.Fatal(err)
	}
	rootHandle, err := openDirectRuntimeRoot(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rootHandle.Close() }()
	moved := store.Root + "-moved"
	if err := os.Rename(store.Root, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.Root, "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, current, err := store.generationAt(rootHandle, bundleName, installed.Target, target)
	if err != nil {
		t.Fatalf("generation verification followed replacement root: %v", err)
	}
	if err := store.selectGenerationAt(rootHandle, current); err != nil {
		t.Fatalf("generation selection followed replacement root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "runtime", currentName)); err != nil {
		t.Fatalf("selection missing from acquired root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "runtime", currentName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selection written through replacement root: %v", err)
	}
}

func TestRuntimeGenerationSelectionSurvivesConfiguredRootRetarget(t *testing.T) {
	fixture, _ := generationStoreFixture(t)
	originalRoot := t.TempDir()
	configuredRoot := filepath.Join(t.TempDir(), "configured")
	if err := os.Symlink(originalRoot, configuredRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	store, err := NewStore(configuredRoot, fixture.Lock)
	if err != nil {
		t.Fatal(err)
	}
	store.HTTPClient = fixture.HTTPClient
	installed, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Lock.Target("", "")
	if err != nil {
		t.Fatal(err)
	}
	bundleName, err := generationBundleName(store.Lock.RuntimeID, installed.Target, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Root, "runtime", currentName)); err != nil {
		t.Fatal(err)
	}
	rootHandle, err := openDirectRuntimeRoot(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rootHandle.Close() }()
	replacementRoot := t.TempDir()
	if err := os.Remove(configuredRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacementRoot, configuredRoot); err != nil {
		t.Fatal(err)
	}
	_, current, err := store.generationAt(rootHandle, bundleName, installed.Target, target)
	if err != nil {
		t.Fatalf("generation verification followed retargeted root: %v", err)
	}
	if err := store.selectGenerationAt(rootHandle, current); err != nil {
		t.Fatalf("generation selection followed retargeted root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(originalRoot, "runtime", currentName)); err != nil {
		t.Fatalf("selection missing from acquired root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(replacementRoot, "runtime", currentName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selection written through retargeted root: %v", err)
	}
}

func TestInstalledComponentVerificationRemainsAnchoredAcrossRootReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows denies replacement of an open rooted directory")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "secondhand")
	bundle := filepath.Join(root, "runtime", "generations", "runtime-id")
	body := []byte("verified artifact")
	digest := sha256.Sum256(body)
	component := Component{
		Name: "tool", SHA256: hex.EncodeToString(digest[:]), Format: "binary", Root: ".",
		Files: []ExpectedFile{{Path: executableName("tool"), Executable: true, Regular: true}},
	}
	for _, path := range []string{
		filepath.Join(bundle, "artifacts", "tool"),
		filepath.Join(bundle, "tool", executableName("tool")),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rootHandle, err := openDirectRuntimeRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rootHandle.Close() }()
	if err := os.Rename(root, filepath.Join(parent, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := verifyInstalledComponentAgainstArtifact(rootHandle, root, bundle, "tool", component); err != nil {
		t.Fatalf("verification followed the replacement root: %v", err)
	}
}

func TestInstallComponentRemainsAnchoredAcrossRootReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows denies replacement of an open rooted directory")
	}
	store, _ := generationStoreFixture(t)
	target, err := store.Lock.Target("", "")
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(store.Root, "runtime", ".staging-test")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	rootHandle, err := openDirectRuntimeRoot(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rootHandle.Close() }()

	moved := store.Root + "-moved"
	if err := os.Rename(store.Root, moved); err != nil {
		t.Fatal(err)
	}
	if err := store.installComponent(context.Background(), rootHandle, stage, "herdr", target.Components["herdr"]); err != nil {
		t.Fatalf("install component followed the replacement root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "runtime", ".staging-test", "artifacts", "herdr")); err != nil {
		t.Fatalf("retained artifact missing from acquired root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "runtime")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("component setup wrote through replacement root: %v", err)
	}
}

func TestConcurrentEnsureConvergesColdAndWithoutSelection(t *testing.T) {
	store, requests := generationStoreFixture(t)
	ensureTogether := func() [2]Runtime {
		t.Helper()
		var start sync.WaitGroup
		start.Add(1)
		var wg sync.WaitGroup
		wg.Add(2)
		var runtimes [2]Runtime
		var errs [2]error
		for index := range runtimes {
			go func() {
				defer wg.Done()
				start.Wait()
				peer, err := NewStore(store.Root, store.Lock)
				if err == nil {
					peer.HTTPClient = store.HTTPClient
					peer.MaxArtifact = store.MaxArtifact
					runtimes[index], err = peer.Ensure(context.Background(), "", "")
				}
				errs[index] = err
			}()
		}
		start.Done()
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		return runtimes
	}

	cold := ensureTogether()
	if cold[0].BundleDir != cold[1].BundleDir {
		t.Fatalf("cold concurrent bundles differ: %q, %q", cold[0].BundleDir, cold[1].BundleDir)
	}
	if err := os.Remove(filepath.Join(store.Root, "runtime", currentName)); err != nil {
		t.Fatal(err)
	}
	warm := ensureTogether()
	if warm[0].BundleDir != cold[0].BundleDir || warm[1].BundleDir != cold[0].BundleDir {
		t.Fatalf("pointerless concurrent bundles = %q, %q; want %q", warm[0].BundleDir, warm[1].BundleDir, cold[0].BundleDir)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("artifact requests = %d, want one cold materialization", got)
	}
}

func TestBinaryExtractionNeverTruncatesAnExecutingFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY reproduction is Linux-specific")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	installed := filepath.Join(dir, "component", "runtime-test")
	if err := os.MkdirAll(filepath.Dir(installed), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, data, 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(installed, "-test.run=^TestRuntimeGenerationProcessHelper$")
	cmd.Env = append(os.Environ(), "HAND_RUNTIME_GENERATION_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	ready := filepath.Join(dir, "ready")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime helper did not become ready")
		}
		time.Sleep(time.Millisecond)
	}

	artifact := filepath.Join(dir, "artifact")
	if err := os.WriteFile(artifact, data, 0o600); err != nil {
		t.Fatal(err)
	}
	err = extract(artifact, filepath.Join(dir, "component"), Component{
		Format: "binary", Root: ".", Files: []ExpectedFile{{Path: "runtime-test", Regular: true, Executable: true}},
	})
	if err == nil {
		t.Fatal("binary extraction rewrote a published executing inode")
	}
	if errors.Is(err, syscall.ETXTBSY) {
		t.Fatalf("binary extraction attempted a truncating write: %v", err)
	}
}

func TestSecondFleetAdoptsGenerationWhileItsRuntimeIsExecuting(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executableData, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	store, requests := generationStoreFixtureWithHerdr(t, executableData)
	first, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(first.HerdrPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest, err := fileDigest(first.HerdrPath)
	if err != nil {
		t.Fatal(err)
	}

	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(first.HerdrPath, "-test.run=^TestRuntimeGenerationProcessHelper$")
	cmd.Env = append(os.Environ(), "HAND_RUNTIME_GENERATION_HELPER=1", "HAND_RUNTIME_GENERATION_READY="+ready)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	waitForGenerationHelper(t, ready)

	if err := os.Remove(filepath.Join(store.Root, "runtime", currentName)); err != nil {
		t.Fatal(err)
	}
	secondFleet, err := NewStore(store.Root, store.Lock)
	if err != nil {
		t.Fatal(err)
	}
	secondFleet.HTTPClient = store.HTTPClient
	adopted, err := secondFleet.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if adopted.BundleDir != first.BundleDir {
		t.Fatalf("second Fleet selected %q, want executing generation %q", adopted.BundleDir, first.BundleDir)
	}
	afterInfo, err := os.Stat(adopted.HerdrPath)
	if err != nil {
		t.Fatal(err)
	}
	afterDigest, err := fileDigest(adopted.HerdrPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) || afterDigest != beforeDigest {
		t.Fatalf("adoption changed executing inode/content: same=%v digest=%s want=%s", os.SameFile(beforeInfo, afterInfo), afterDigest, beforeDigest)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("artifact requests = %d, want only first Fleet materialization", got)
	}
}

func TestManagedHandGenerationSurvivesSourceReplacementBeforeLaunch(t *testing.T) {
	store, _ := generationStoreFixture(t)
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), executableName("hand"))
	data, err := os.ReadFile(testExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0o700); err != nil {
		t.Fatal(err)
	}
	managed, err := store.MaterializeHandExecutable(source)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := store.MaterializeHandExecutable(source); err != nil || again != managed {
		t.Fatalf("repeat materialization = %q, %v; want %q", again, err, managed)
	}
	generation := filepath.Base(filepath.Dir(managed))
	if len(generation) != sha256.Size*2 {
		t.Fatalf("managed Hand generation = %q, want full SHA-256", generation)
	}
	if _, err := hex.DecodeString(generation); err != nil {
		t.Fatalf("managed Hand generation = %q: %v", generation, err)
	}
	if err := os.WriteFile(source, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(managed, "-test.run=^TestRuntimeGenerationProcessHelper$")
	cmd.Env = append(os.Environ(), "HAND_RUNTIME_GENERATION_HELPER=1", "HAND_RUNTIME_GENERATION_READY="+ready)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("launch immutable managed Hand after source replacement: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = stdin.Close()
			_ = cmd.Wait()
		}
	})
	waitForGenerationHelper(t, ready)
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	got, err := fileDigest(managed)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("managed Hand digest = %s, want source generation %x", got, want)
	}
}

func TestRuntimeGenerationProcessHelper(t *testing.T) {
	if os.Getenv("HAND_RUNTIME_GENERATION_HELPER") != "1" {
		return
	}
	ready := os.Getenv("HAND_RUNTIME_GENERATION_READY")
	if ready == "" {
		ready = filepath.Join(filepath.Dir(filepath.Dir(os.Args[0])), "ready")
	}
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		os.Exit(2)
	}
	_, _ = os.Stdin.Read(make([]byte, 1))
	os.Exit(0)
}

func generationStoreFixture(t *testing.T) (*Store, *atomic.Int64) {
	return generationStoreFixtureWithHerdr(t, nil)
}

func generationStoreFixtureWithHerdr(t *testing.T, herdrData []byte) (*Store, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		body := []byte("fixture-" + filepath.Base(request.URL.Path))
		if filepath.Base(request.URL.Path) == "herdr" && herdrData != nil {
			body = herdrData
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	components := make(map[string]Component, 3)
	for _, name := range []string{"git", "treehouse", "herdr"} {
		body := []byte("fixture-" + name)
		if name == "herdr" && herdrData != nil {
			body = herdrData
		}
		digest := sha256.Sum256(body)
		components[name] = Component{
			Name: name, Version: "test", Revision: "test", URL: server.URL + "/" + name,
			SHA256: hex.EncodeToString(digest[:]), Format: "binary", Root: ".",
			Files: []ExpectedFile{{Path: executableName(name), Executable: true, Regular: true}},
		}
	}
	lock := Lock{Schema: 1, GeneratedBy: "generation-test", Targets: map[string]Target{
		runtime.GOOS + "/" + runtime.GOARCH: {Components: components},
	}}
	var err error
	lock.RuntimeID, err = lock.DeterministicID()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(t.TempDir(), lock)
	if err != nil {
		t.Fatal(err)
	}
	store.HTTPClient = server.Client()
	return store, &requests
}

func waitForGenerationHelper(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime helper did not become ready at %s", path)
		}
		time.Sleep(time.Millisecond)
	}
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o700)
	})
	if err != nil {
		t.Fatal(err)
	}
}
