package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	testCutoverBootA   = "11111111-2222-4333-8444-555555555555"
	testCutoverBootB   = "66666666-7777-4888-9999-aaaaaaaaaaaa"
	testCutoverMachine = "0123456789abcdef0123456789abcdef"
	testCutoverGUID    = "abcdefab-cdef-4abc-8def-abcdefabcdef"
	testCutoverTime    = "2026-09-25T00:00:00Z"
)

func testCutoverEvidence(platform, machine, token string) legacyV18CutoverBootEvidence {
	return legacyV18CutoverBootEvidence{
		Format:     legacyV18CutoverBootEvidenceFormat,
		Platform:   platform,
		MachineID:  machine,
		TokenKind:  legacyV18CutoverBootTokenKinds[platform],
		Token:      token,
		RecordedAt: testCutoverTime,
	}
}

// #348 revision 4 "Boot witness": only a positively read, same-machine boot change proves cessation.
func TestLegacyV18CutoverBootWitness(t *testing.T) {
	linux := testCutoverEvidence("linux", testCutoverMachine, testCutoverBootA)
	darwin := testCutoverEvidence("darwin", testCutoverGUID, testCutoverBootA)
	windows := testCutoverEvidence("windows", testCutoverGUID, "7200000")
	with := func(e legacyV18CutoverBootEvidence, edit func(*legacyV18CutoverBootEvidence)) legacyV18CutoverBootEvidence {
		edit(&e)
		return e
	}
	for _, tc := range []struct {
		name              string
		recorded, current legacyV18CutoverBootEvidence
		want              legacyV18CutoverWitness
	}{
		{"linux same boot", linux, linux, legacyV18CutoverWitnessRebootRequired},
		{"linux new boot", linux, with(linux, func(e *legacyV18CutoverBootEvidence) { e.Token = testCutoverBootB }), legacyV18CutoverWitnessPositive},
		{"darwin new boot", darwin, with(darwin, func(e *legacyV18CutoverBootEvidence) { e.Token = testCutoverBootB }), legacyV18CutoverWitnessPositive},
		{"windows uptime dropped", windows, with(windows, func(e *legacyV18CutoverBootEvidence) { e.Token = "60000" }), legacyV18CutoverWitnessPositive},
		{"windows uptime equal", windows, windows, legacyV18CutoverWitnessRebootRequired},
		{"windows uptime grew", windows, with(windows, func(e *legacyV18CutoverBootEvidence) { e.Token = "7200001" }), legacyV18CutoverWitnessRebootRequired},
		{"windows non-canonical token", windows, with(windows, func(e *legacyV18CutoverBootEvidence) { e.Token = "060000" }), legacyV18CutoverWitnessUnknown},
		{"uppercase token", linux, with(linux, func(e *legacyV18CutoverBootEvidence) { e.Token = strings.ToUpper(testCutoverBootB) }), legacyV18CutoverWitnessUnknown},
		{"empty current token", linux, with(linux, func(e *legacyV18CutoverBootEvidence) { e.Token = "" }), legacyV18CutoverWitnessUnknown},
		{"malformed recorded", with(linux, func(e *legacyV18CutoverBootEvidence) { e.Format = "v0" }), linux, legacyV18CutoverWitnessUnknown},
		{"token kind mismatch", linux, with(linux, func(e *legacyV18CutoverBootEvidence) { e.TokenKind = "darwin-boot-session-uuid" }), legacyV18CutoverWitnessUnknown},
		{"platform differs", linux, with(darwin, func(e *legacyV18CutoverBootEvidence) { e.Token = testCutoverBootB }), legacyV18CutoverWitnessUnknown},
		{"machine differs", linux, testCutoverEvidence("linux", strings.Repeat("f", 32), testCutoverBootB), legacyV18CutoverWitnessUnknown},
		{"linux machine id with dashes", linux, testCutoverEvidence("linux", testCutoverGUID, testCutoverBootB), legacyV18CutoverWitnessUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := evaluateLegacyV18CutoverBootWitness(tc.recorded, tc.current)
			if got != tc.want || reason == "" {
				t.Fatalf("witness = %s (%q), want %s", got, reason, tc.want)
			}
		})
	}
}

func TestPreflightLegacyV18CutoverFreezeReturnsCommittableEvidence(t *testing.T) {
	home := testCutoverPreflightHome(t)
	evidence, err := preflightLegacyV18CutoverFreeze(home, testCutoverPlatform(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLegacyV18CutoverBootEvidence(evidence); err != nil {
		t.Fatalf("preflight evidence is not committable: %v", err)
	}
	if evidence.Platform != runtime.GOOS || evidence.Token != testCutoverPlatformToken() {
		t.Fatalf("preflight evidence = %+v", evidence)
	}
	requireNoCutoverProbeNames(t, home)
}

func TestPreflightLegacyV18CutoverFreezeRefusesWithoutProbeNames(t *testing.T) {
	for name, platform := range map[string]legacyV18CutoverPlatformSource{
		"remote filesystem": testCutoverPlatform(func(p *legacyV18CutoverPlatformSource) {
			p.filesystem = func(string) error { return errors.New("nfs") }
		}),
		"unreadable machine identity": testCutoverPlatform(func(p *legacyV18CutoverPlatformSource) {
			p.machineID = func() (string, error) { return "", errors.New("uninitialized") }
		}),
		"unreadable boot identity": testCutoverPlatform(func(p *legacyV18CutoverPlatformSource) {
			p.bootToken = func() (string, error) { return "", os.ErrPermission }
		}),
		"malformed boot identity": testCutoverPlatform(func(p *legacyV18CutoverPlatformSource) {
			p.bootToken = func() (string, error) { return "not-a-token", nil }
		}),
	} {
		t.Run(name, func(t *testing.T) {
			home := testCutoverPreflightHome(t)
			if _, err := preflightLegacyV18CutoverFreeze(home, platform); !errors.Is(err, errLegacyV18CutoverPreflight) {
				t.Fatalf("preflight error = %v, want preflight refusal", err)
			}
			requireNoCutoverProbeNames(t, home)
		})
	}
}

func TestPreflightLegacyV18CutoverFreezeRefusesSymlinkedState(t *testing.T) {
	home := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(home, "state")); err != nil {
		t.Skip(err)
	}
	if _, err := preflightLegacyV18CutoverFreeze(home, testCutoverPlatform(nil)); !errors.Is(err, errLegacyV18CutoverPreflight) {
		t.Fatalf("preflight error = %v, want preflight refusal", err)
	}
}

func TestLegacyV18CutoverWindowsUptimeFloor(t *testing.T) {
	for token, wantErr := range map[string]bool{"3599999": true, "3600000": false, "86400000": false} {
		err := checkLegacyV18CutoverFreezeEvidence(testCutoverEvidence("windows", testCutoverGUID, token))
		if (err != nil) != wantErr {
			t.Fatalf("uptime %s: err = %v, want refusal %v", token, err, wantErr)
		}
	}
	if err := checkLegacyV18CutoverFreezeEvidence(testCutoverEvidence("linux", testCutoverMachine, testCutoverBootA)); err != nil {
		t.Fatalf("linux evidence has no uptime floor: %v", err)
	}
}

func TestProbeLegacyV18CutoverStateLeavesOnlyForeignNames(t *testing.T) {
	const nonce = "00112233445566778899aabbccddeeff"
	for _, suffix := range []string{"-a", "-b", "-c"} {
		t.Run("existing"+suffix, func(t *testing.T) {
			state := t.TempDir()
			foreign := filepath.Join(state, ".v19-cutover-probe-"+nonce+suffix)
			if err := os.WriteFile(foreign, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := probeLegacyV18CutoverStateNamed(state, nonce); err == nil {
				t.Fatal("probe accepted an existing name")
			}
			entries, err := os.ReadDir(state)
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(foreign); err != nil || string(data) != "foreign" || len(entries) != 1 {
				t.Fatalf("probe changed names it did not create: entries=%d data=%q err=%v", len(entries), data, err)
			}
		})
	}
}

func TestLegacyV18CutoverPlatformReadsNativeBootToken(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("HAND_TEST_CUTOVER_BOOT_TOKEN") != "" {
		t.Skip("native Linux boot_id only")
	}
	token, err := legacyV18CutoverPlatform().bootToken()
	if err != nil || !legacyV18CutoverUUIDPattern.MatchString(token) {
		t.Fatalf("boot token = %q, %v", token, err)
	}
}

func testCutoverPreflightHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func testCutoverPlatformToken() string {
	if runtime.GOOS == "windows" {
		return "7200000"
	}
	return testCutoverBootA
}

func testCutoverPlatform(edit func(*legacyV18CutoverPlatformSource)) legacyV18CutoverPlatformSource {
	machine := testCutoverGUID
	if runtime.GOOS == "linux" {
		machine = testCutoverMachine
	}
	platform := legacyV18CutoverPlatformSource{
		bootToken:  func() (string, error) { return testCutoverPlatformToken(), nil },
		machineID:  func() (string, error) { return machine, nil },
		filesystem: func(string) error { return nil },
	}
	if edit != nil {
		edit(&platform)
	}
	return platform
}

func requireNoCutoverProbeNames(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".v19-cutover-probe-") {
			t.Fatalf("preflight left probe %s", entry.Name())
		}
	}
}
