package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"time"
)

const (
	legacyV18CutoverBootEvidenceFormat   = "v1"
	legacyV18CutoverWindowsUptimeFloorMS = 60 * 60 * 1000
)

var errLegacyV18CutoverPreflight = errors.New("offline cutover preflight refused")

var legacyV18CutoverBootTokenKinds = map[string]string{
	"linux":   "linux-boot-id",
	"darwin":  "darwin-boot-session-uuid",
	"windows": "windows-tick-count-ms",
}

var (
	legacyV18CutoverUUIDPattern           = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	legacyV18CutoverLinuxMachineIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// E_F in #348 revision 4: the boot session and machine that the freeze commits.
type legacyV18CutoverBootEvidence struct {
	Format     string `json:"format"`
	Platform   string `json:"platform"`
	MachineID  string `json:"machine_id"`
	TokenKind  string `json:"token_kind"`
	Token      string `json:"token"`
	RecordedAt string `json:"recorded_at"`
}

type legacyV18CutoverWitness string

const (
	legacyV18CutoverWitnessPositive       legacyV18CutoverWitness = "positive"
	legacyV18CutoverWitnessRebootRequired legacyV18CutoverWitness = "reboot-required"
	legacyV18CutoverWitnessUnknown        legacyV18CutoverWitness = "cessation-unknown"
)

type legacyV18CutoverPlatformSource struct {
	bootToken  func() (string, error)
	machineID  func() (string, error)
	filesystem func(dir string) error
}

func legacyV18CutoverNativePlatform() legacyV18CutoverPlatformSource {
	return legacyV18CutoverPlatformSource{
		bootToken:  legacyV18CutoverBootToken,
		machineID:  legacyV18CutoverMachineID,
		filesystem: classifyLegacyV18CutoverFilesystem,
	}
}

// Everything the freeze run must prove before MigrationLock; the returned evidence is the E_F it commits.
func preflightLegacyV18CutoverFreeze(homeDir string, platform legacyV18CutoverPlatformSource) (legacyV18CutoverBootEvidence, error) {
	stateDir := filepath.Join(homeDir, "state")
	if err := checkLegacyV18CutoverFilesystem(stateDir, platform); err != nil {
		return legacyV18CutoverBootEvidence{}, err
	}
	evidence, err := readLegacyV18CutoverBootEvidence(platform)
	if err == nil {
		err = checkLegacyV18CutoverFreezeEvidence(evidence)
	}
	if err == nil {
		err = probeLegacyV18CutoverState(stateDir)
	}
	if err != nil {
		return legacyV18CutoverBootEvidence{}, fmt.Errorf("%w: %v", errLegacyV18CutoverPreflight, err)
	}
	return evidence, nil
}

func checkLegacyV18CutoverFreezeEvidence(e legacyV18CutoverBootEvidence) error {
	if e.Platform != "windows" {
		return nil
	}
	uptime, _ := strconv.ParseUint(e.Token, 10, 64)
	if uptime < legacyV18CutoverWindowsUptimeFloorMS {
		return fmt.Errorf("windows uptime %dms is below the %dms floor; retry after one hour of uptime", uptime, legacyV18CutoverWindowsUptimeFloorMS)
	}
	return nil
}

func checkLegacyV18CutoverFilesystem(stateDir string, platform legacyV18CutoverPlatformSource) error {
	info, err := os.Lstat(stateDir)
	if err != nil {
		return fmt.Errorf("%w: inspect %s: %v", errLegacyV18CutoverPreflight, stateDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a direct directory", errLegacyV18CutoverPreflight, stateDir)
	}
	if err := platform.filesystem(stateDir); err != nil {
		return fmt.Errorf("%w: filesystem of %s: %v", errLegacyV18CutoverPreflight, stateDir, err)
	}
	return nil
}

func readLegacyV18CutoverBootEvidence(platform legacyV18CutoverPlatformSource) (legacyV18CutoverBootEvidence, error) {
	kind, ok := legacyV18CutoverBootTokenKinds[runtime.GOOS]
	if !ok {
		return legacyV18CutoverBootEvidence{}, fmt.Errorf("platform %s has no boot identity witness", runtime.GOOS)
	}
	token, err := platform.bootToken()
	if err != nil {
		return legacyV18CutoverBootEvidence{}, fmt.Errorf("read boot identity: %w", err)
	}
	machine, err := platform.machineID()
	if err != nil {
		return legacyV18CutoverBootEvidence{}, fmt.Errorf("read machine identity: %w", err)
	}
	evidence := legacyV18CutoverBootEvidence{
		Format:     legacyV18CutoverBootEvidenceFormat,
		Platform:   runtime.GOOS,
		MachineID:  machine,
		TokenKind:  kind,
		Token:      token,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return evidence, validateLegacyV18CutoverBootEvidence(evidence)
}

func validateLegacyV18CutoverBootEvidence(e legacyV18CutoverBootEvidence) error {
	if e.Format != legacyV18CutoverBootEvidenceFormat {
		return fmt.Errorf("boot evidence format %q is not %q", e.Format, legacyV18CutoverBootEvidenceFormat)
	}
	kind, ok := legacyV18CutoverBootTokenKinds[e.Platform]
	if !ok || e.TokenKind != kind {
		return fmt.Errorf("boot evidence platform %q with token kind %q is not a supported pair", e.Platform, e.TokenKind)
	}
	machine := legacyV18CutoverUUIDPattern
	if e.Platform == "linux" {
		machine = legacyV18CutoverLinuxMachineIDPattern
	}
	if !machine.MatchString(e.MachineID) {
		return fmt.Errorf("machine identity %q is malformed for %s", e.MachineID, e.Platform)
	}
	if e.Platform == "windows" {
		value, err := strconv.ParseUint(e.Token, 10, 64)
		if err != nil || strconv.FormatUint(value, 10) != e.Token {
			return fmt.Errorf("boot token %q is not a canonical tick count", e.Token)
		}
	} else if !legacyV18CutoverUUIDPattern.MatchString(e.Token) {
		return fmt.Errorf("boot token %q is not a lowercase UUID", e.Token)
	}
	if _, err := time.Parse(time.RFC3339, e.RecordedAt); err != nil {
		return fmt.Errorf("boot evidence time %q: %w", e.RecordedAt, err)
	}
	return nil
}

func evaluateLegacyV18CutoverBootWitness(recorded, current legacyV18CutoverBootEvidence) (legacyV18CutoverWitness, string) {
	if err := validateLegacyV18CutoverBootEvidence(recorded); err != nil {
		return legacyV18CutoverWitnessUnknown, "recorded " + err.Error()
	}
	if err := validateLegacyV18CutoverBootEvidence(current); err != nil {
		return legacyV18CutoverWitnessUnknown, "current " + err.Error()
	}
	if current.Platform != recorded.Platform {
		return legacyV18CutoverWitnessUnknown, fmt.Sprintf("frozen on %s, now on %s", recorded.Platform, current.Platform)
	}
	if current.MachineID != recorded.MachineID {
		return legacyV18CutoverWitnessUnknown, "machine identity differs from the freeze; abort is the way back for a moved home"
	}
	if recorded.Platform == "windows" {
		frozen, _ := strconv.ParseUint(recorded.Token, 10, 64)
		now, _ := strconv.ParseUint(current.Token, 10, 64)
		if now < frozen {
			return legacyV18CutoverWitnessPositive, "uptime restarted since the freeze"
		}
		return legacyV18CutoverWitnessRebootRequired, fmt.Sprintf("uptime %dms has not dropped below the %dms recorded at the freeze; restart, then complete within that uptime", now, frozen)
	}
	if current.Token != recorded.Token {
		return legacyV18CutoverWitnessPositive, "boot session changed since the freeze"
	}
	return legacyV18CutoverWitnessRebootRequired, "the machine has not restarted since the freeze"
}

func probeLegacyV18CutoverState(stateDir string) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("probe nonce: %w", err)
	}
	return probeLegacyV18CutoverStateNamed(stateDir, hex.EncodeToString(nonce[:]))
}

// Publication and abort need hard links and replace-existing rename inside state/.
func probeLegacyV18CutoverStateNamed(stateDir, nonce string) (err error) {
	prefix := filepath.Join(stateDir, ".v19-cutover-probe-"+nonce)
	first, linked, replacement := prefix+"-a", prefix+"-b", prefix+"-c"
	var created []string
	defer func() {
		for _, path := range created {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) && err == nil {
				err = fmt.Errorf("remove probe %s: %w", path, removeErr)
			}
		}
	}()
	firstInfo, err := createLegacyV18CutoverProbe(first)
	if err != nil {
		return err
	}
	created = append(created, first)
	if err := os.Link(first, linked); err != nil {
		return fmt.Errorf("hard link probe: %w", err)
	}
	created = append(created, linked)
	if err := requireLegacyV18CutoverProbeIdentity(linked, firstInfo, true); err != nil {
		return fmt.Errorf("hard link probe: %w", err)
	}
	replacementInfo, err := createLegacyV18CutoverProbe(replacement)
	if err != nil {
		return err
	}
	created = append(created, replacement)
	if err := os.Rename(replacement, linked); err != nil {
		return fmt.Errorf("replace-existing rename probe: %w", err)
	}
	if err := requireLegacyV18CutoverProbeIdentity(linked, replacementInfo, true); err != nil {
		return fmt.Errorf("replace-existing rename probe: %w", err)
	}
	if err := requireLegacyV18CutoverProbeIdentity(first, firstInfo, true); err != nil {
		return fmt.Errorf("replace-existing rename probe: %w", err)
	}
	return requireLegacyV18CutoverProbeIdentity(linked, firstInfo, false)
}

// Identity comes from an open handle: on Windows a path-only FileInfo loses its file ID once the name moves.
func createLegacyV18CutoverProbe(path string) (os.FileInfo, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create probe: %w", err)
	}
	info, statErr := file.Stat()
	if closeErr := file.Close(); statErr == nil {
		statErr = closeErr
	}
	if statErr != nil {
		return nil, fmt.Errorf("stat probe: %w", statErr)
	}
	return info, nil
}

func requireLegacyV18CutoverProbeIdentity(path string, want os.FileInfo, same bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	if closeErr := file.Close(); statErr == nil {
		statErr = closeErr
	}
	if statErr != nil {
		return statErr
	}
	if os.SameFile(info, want) != same {
		return fmt.Errorf("%s has the wrong file identity", path)
	}
	return nil
}
