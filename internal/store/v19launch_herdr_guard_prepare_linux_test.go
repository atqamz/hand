//go:build linux

package store

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/osfacts"
)

type execGuardLaunchTest struct {
	home    string
	dir     string
	session CanonicalV19SessionAcquireRequest
	key     canonicalV19HerdrSessionProviderKey
	input   CanonicalV19LaunchPrepareInput
}

func newExecGuardLaunchTest(t *testing.T, argv ...string) *execGuardLaunchTest {
	t.Helper()
	return newExecGuardLaunchTestWithFleetID(t, "fleet-1", argv...)
}

func newExecGuardLaunchTestWithFleetID(t *testing.T, fleetID string, argv ...string) *execGuardLaunchTest {
	t.Helper()
	fixture, worktree, session, key := canonicalV19HerdrSessionFixtureWithFleetID(t, fleetID)
	if err := os.MkdirAll(worktree.RequestedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19LaunchPrepareInput(worktree, session, "operation-exec-guard-launch", "executor-binding-1")
	input.Spec = CanonicalV19LaunchSpec{
		Executable: argv[0], Arguments: argv[1:], Cwd: worktree.RequestedPath,
		Environment: map[string]CanonicalV19LaunchEnvironmentValue{
			"HAND_ROLE": {ValueKind: "literal", ValueMaterial: "worker", ValueDigest: launch.EnvironmentValueDigest("worker")},
		},
	}
	dir, err := canonicalV19ExecGuardDir(fixture.Home, input.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	return &execGuardLaunchTest{home: fixture.Home, dir: dir, session: session, key: key, input: input}
}

func TestExecGuardPrepareCommitsOnlyTheVerifierAndHandsSBToTheHandoff(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	current, err := prepareCanonicalV19ExecGuardLaunch(context.Background(), l.home, l.input)
	if err != nil || current.Current.State != "prepared" {
		t.Fatalf("prepare = %q, %v, want prepared", current.Current.State, err)
	}
	var handoff execguard.Handoff
	data, err := os.ReadFile(execguard.Locator(l.dir))
	if err == nil {
		err = json.Unmarshal(data, &handoff)
	}
	if err != nil {
		t.Fatal(err)
	}
	request := current.Current.Request
	row, binding := request.Spec.Environment[execguard.CredentialEnv], request.Spec.Environment[execguard.ExecutorBindingEnv]
	secret := handoff.Values[execguard.CredentialEnv]
	if row.ValueKind != "secret-ref" || row.ValueMaterial != execguard.Protocol ||
		row.ValueDigest != launch.ExecGuardCredentialVerifier(current.FleetID, l.input.BindingID, secret) ||
		binding.ValueKind != "literal" || binding.ValueMaterial != l.input.BindingID {
		t.Fatalf("credential row %+v and binding row %+v, want V_B of the handoff's S_B for B (EG-1)", row, binding)
	}
	if launch.CanonicalSpecDigest(request.Spec) != request.LaunchSpecDigest || handoff.LaunchSpecDigest != request.LaunchSpecDigest ||
		handoff.RequestDigest != request.RequestDigest || handoff.ExecutorBindingID != l.input.BindingID {
		t.Fatalf("handoff digests %q %q, want the Tx A commitment that covers both reserved rows (EG-1)", handoff.RequestDigest, handoff.LaunchSpecDigest)
	}
	self, _ := osfacts.ReadIncarnation(os.Getpid())
	if handoff.BootID != self.BootID || handoff.UID != uint32(os.Getuid()) || handoff.WorktreePath != l.input.Spec.Cwd {
		t.Fatalf("handoff names boot %q uid %d worktree %q, want Hand's own", handoff.BootID, handoff.UID, handoff.WorktreePath)
	}
	for path, want := range map[string]fs.FileMode{execguard.Locator(l.dir): 0o600, l.dir: 0o700, filepath.Dir(l.dir): 0o700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s mode = %v, %v, want %v", path, info.Mode().Perm(), err, want)
		}
	}
	assertExecGuardSecretOnlyIn(t, secret, l.home, execguard.Locator(l.dir))
}

func TestExecGuardPrepareValidationRefusesBeforeAnyOperationRow(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	before := canonicalV19WorktreeCreateOperationCount(t, l.home)
	for _, test := range []struct {
		name   string
		mutate func(*CanonicalV19LaunchPrepareInput)
	}{
		{"relative executable", func(input *CanonicalV19LaunchPrepareInput) { input.Spec.Executable = "sh" }},
		{"reserved ExecutorBinding name", func(input *CanonicalV19LaunchPrepareInput) {
			input.Spec.Environment[execguard.ExecutorBindingEnv] = input.Spec.Environment["HAND_ROLE"]
		}},
		{"reserved credential name", func(input *CanonicalV19LaunchPrepareInput) {
			input.Spec.Environment[execguard.CredentialEnv] = input.Spec.Environment["HAND_ROLE"]
		}},
		{"a name that assigns the credential", func(input *CanonicalV19LaunchPrepareInput) {
			input.Spec.Environment[execguard.CredentialEnv+"=x"] = execGuardLiteral("x")
		}},
		{"a name that assigns the ExecutorBinding", func(input *CanonicalV19LaunchPrepareInput) {
			input.Spec.Environment[execguard.ExecutorBindingEnv+"=eb_other"] = execGuardLiteral("eb_other")
		}},
		{"a name with NUL", func(input *CanonicalV19LaunchPrepareInput) {
			input.Spec.Environment["HAND\x00ROLE"] = execGuardLiteral("worker")
		}},
		{"unresolvable secret-ref", func(input *CanonicalV19LaunchPrepareInput) {
			input.Spec.Environment["TOKEN"] = CanonicalV19LaunchEnvironmentValue{ValueKind: "secret-ref", ValueMaterial: "secret://token", ValueDigest: "digest"}
		}},
		{"literal digest the guard would refuse", func(input *CanonicalV19LaunchPrepareInput) {
			input.Spec.Environment["HAND_ROLE"] = CanonicalV19LaunchEnvironmentValue{ValueKind: "literal", ValueMaterial: "worker", ValueDigest: "digest"}
		}},
		{"an operation ID that leaves the exec-guard directory", func(input *CanonicalV19LaunchPrepareInput) { input.OperationID = "../escape" }},
		{"an operation ID that reaches the terminal", func(input *CanonicalV19LaunchPrepareInput) { input.OperationID = "op\nrm -rf ~" }},
	} {
		input := l.input
		input.Spec = cloneCanonicalV19LaunchSpec(input.Spec)
		test.mutate(&input)
		if _, err := prepareCanonicalV19ExecGuardLaunch(context.Background(), l.home, input); err == nil || canonicalV19WorktreeCreateOperationCount(t, l.home) != before {
			t.Errorf("%s: prepare = %v, want a refusal before any operation row", test.name, err)
		}
	}
	if _, err := os.Stat(l.dir); err == nil {
		t.Fatal("a refused Launch wrote a handoff directory")
	}
}

func TestExecGuardKeyRoundTripsItsObservedAndAttestedForms(t *testing.T) {
	guard := &osfacts.Incarnation{BootID: "0b5c6f62-5a39-4c31-9a4b-2d9e5b8e7c10", PID: 4100, StartTicks: 8812}
	root := &osfacts.Incarnation{BootID: guard.BootID, PID: 4101, StartTicks: 8813}
	object := &execguard.Object{Device: 64769, Inode: 1311, SHA256: strings.Repeat("ab", 32)}
	session := canonicalV19HerdrSessionProviderKey{SessionName: herdr.SessionName("fleet-1"), WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"}
	observed := canonicalV19ExecGuardKey{Session: session, Assoc: "observed", Guard: guard, Root: root, ProcessGroup: 4101, Terminal: 34816, Object: object, ObjectClass: execguard.ClassExact}
	for _, key := range []canonicalV19ExecGuardKey{
		observed,
		{Session: session, Assoc: "mismatch", Guard: guard, Root: root, ProcessGroup: 4101, Object: object, ObjectClass: execguard.ClassSampled},
		{Session: session, Assoc: "unobserved", Guard: guard, Object: object, ObjectClass: canonicalV19ExecGuardUnknown},
		{Session: session, Assoc: "unobserved", ObjectClass: canonicalV19ExecGuardUnknown},
	} {
		encoded, err := encodeCanonicalV19ExecGuardKey(key)
		if err != nil {
			t.Fatalf("encode %+v: %v", key, err)
		}
		parsed, err := parseCanonicalV19ExecGuardKey(encoded)
		if err != nil || formatCanonicalV19ExecGuardKey(parsed) != encoded || !strings.HasPrefix(encoded, "herdr-exec-guard:v1?") {
			t.Fatalf("round trip of %q = %+v, %v", encoded, parsed, err)
		}
	}
	valid, _ := encodeCanonicalV19ExecGuardKey(observed)
	for _, broken := range []string{
		strings.Replace(valid, "herdr-exec-guard:v1?", "herdr-executor:v1?", 1),
		strings.Replace(valid, "assoc=observed", "assoc=assumed", 1),
		strings.Replace(valid, "os=linux", "os=darwin", 1),
		strings.Replace(valid, "p=4101", "p=unknown", 1),
		strings.Replace(valid, "r="+guard.BootID+".4101.8813", "r=unknown", 1),
		strings.Replace(valid, "exe_class=exact", "exe_class=verified", 1),
		strings.Replace(strings.Replace(valid, "exe=64769%3A1311", "exe=unknown", 1), "exe_sha256="+object.SHA256, "exe_sha256=unknown", 1),
		strings.Replace(valid, "g="+guard.BootID+".4100.8812", "g=unknown", 1),
		strings.Replace(valid, "exe_sha256="+object.SHA256, "exe_sha256="+strings.ToUpper(object.SHA256), 1),
		strings.Replace(valid, "exe_sha256="+object.SHA256, "exe_sha256="+strings.Repeat("zz", 32), 1),
		strings.Replace(valid, "&g=", "&g=unknown&g=", 1),
		valid + "&extra=1",
	} {
		if _, err := parseCanonicalV19ExecGuardKey(broken); err == nil {
			t.Errorf("parse accepted %q", broken)
		}
	}
}

func execGuardLiteral(value string) CanonicalV19LaunchEnvironmentValue {
	return CanonicalV19LaunchEnvironmentValue{ValueKind: "literal", ValueMaterial: value, ValueDigest: launch.EnvironmentValueDigest(value)}
}

func assertExecGuardSecretOnlyIn(t *testing.T, secret string, root string, allowed ...string) {
	t.Helper()
	if len(secret) != 43 {
		t.Fatalf("credential %q is not 32 bytes of unpadded base64url", secret)
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		for _, ok := range allowed {
			if path == ok {
				return err
			}
		}
		if err == nil && strings.Contains(string(data), secret) {
			t.Errorf("S_B reached %s (EG-1)", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
