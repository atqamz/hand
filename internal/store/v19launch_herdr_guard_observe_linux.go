//go:build linux

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/osfacts"
	"golang.org/x/sys/unix"
)

const canonicalV19ExecGuardStarting = "starting"

var canonicalV19ExecGuardRecordKinds = []string{
	execguard.KindClaimed, execguard.KindPinned, execguard.KindRunning, execguard.KindRefused, execguard.KindCeased,
}

type canonicalV19ExecGuardVerdict struct {
	State        string
	Key          string
	TerminalKind string
	Reason       string
	Records      []execguard.Record
}

func reconcileCanonicalV19HerdrGuardLaunch(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrLaunchCurrent,
	deps canonicalV19HerdrLaunchDeps,
) (string, bool, error) {
	credential := current.Current.Request.Spec.Environment[execguard.CredentialEnv]
	if credential.ValueKind != "secret-ref" || credential.ValueMaterial != execguard.Protocol {
		return "", false, nil
	}
	operationID := current.Current.Request.OperationID
	dir, err := canonicalV19ExecGuardDir(homeDir, operationID)
	if err != nil {
		return current.Current.State, true, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w", err)
	}
	if current.Current.State == "prepared" {
		if err := fenceCanonicalV19ExecGuard(dir); err != nil {
			return "prepared", true, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w", err)
		}
		verdict := canonicalV19ExecGuardVerdict{State: "no-effect", Reason: "fenced before submission"}
		state, err := applyCanonicalV19ExecGuardVerdict(ctx, homeDir, current, verdict, deps.now)
		if errors.Is(err, ErrCanonicalV19LaunchTransition) {
			state, err = reconcileCanonicalV19HerdrLaunch(ctx, homeDir, operationID, deps)
		}
		return state, true, err
	}
	var client canonicalV19HerdrLaunchClient
	if key, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey); err == nil {
		client = deps.clientFor(key.SessionName)
	}
	verdict, err := observeCanonicalV19ExecGuardLaunch(dir, current, client)
	if err == nil && verdict.State == "" {
		switch fenceErr := execguard.Fence(dir); {
		case fenceErr == nil:
			verdict.State, verdict.Reason = "uncertain", "fenced before any claim; no-effect also needs the unqualified Herdr property O1"
		case errors.Is(fenceErr, fs.ErrNotExist):
			if verdict, err = observeCanonicalV19ExecGuardLaunch(dir, current, client); err == nil && verdict.State == "" {
				verdict.State, verdict.Reason = "uncertain", "the handoff was claimed or fenced and no decisive guard record exists"
			}
		default:
			err = fenceErr
		}
	}
	if err != nil {
		return current.Current.State, true, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w", err)
	}
	if verdict.State == canonicalV19ExecGuardStarting {
		return current.Current.State, true, fmt.Errorf("reconcile canonical v19 Herdr Launch: %s", verdict.Reason)
	}
	state, err := applyCanonicalV19ExecGuardVerdict(ctx, homeDir, current, verdict, deps.now)
	return state, true, err
}

func settleCanonicalV19ExecGuardFiles(homeDir, operationID string) error {
	dir, err := canonicalV19ExecGuardDir(homeDir, operationID)
	if err != nil {
		// Prepare refuses such an ID before Tx A, so no handoff directory can exist for it.
		return nil
	}
	return errors.Join(fenceCanonicalV19ExecGuard(dir), execguard.Settle(dir))
}

func fenceCanonicalV19ExecGuard(dir string) error {
	if err := execguard.Fence(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// An empty State means no guard has claimed L yet; starting means a live guard claimed it
// and has not written running, which is no reason for any transition or fence.
func observeCanonicalV19ExecGuardLaunch(
	dir string,
	current canonicalV19HerdrLaunchCurrent,
	client canonicalV19HerdrLaunchClient,
) (canonicalV19ExecGuardVerdict, error) {
	request := current.Current.Request
	var verdict canonicalV19ExecGuardVerdict
	records, err := readCanonicalV19ExecGuardRecords(dir, request.OperationID)
	liveness := osfacts.Unknown
	if claimed := records[execguard.KindClaimed]; err == nil && claimed != nil {
		// A guard writes its last record before it exits, so a re-read after seeing it gone is complete.
		if liveness = osfacts.Observe(claimed.Guard); liveness != osfacts.Alive {
			records, err = readCanonicalV19ExecGuardRecords(dir, request.OperationID)
		}
	}
	if err != nil {
		return verdict, err
	}
	for _, kind := range canonicalV19ExecGuardRecordKinds {
		if record := records[kind]; record != nil {
			verdict.Records = append(verdict.Records, *record)
		}
	}
	uncertain := func(reason string) (canonicalV19ExecGuardVerdict, error) {
		verdict.State, verdict.Reason = "uncertain", reason
		return verdict, nil
	}
	claimed, pinned, running, refused, ceased := records[execguard.KindClaimed], records[execguard.KindPinned],
		records[execguard.KindRunning], records[execguard.KindRefused], records[execguard.KindCeased]
	if running == nil {
		switch {
		case refused != nil && (claimed == nil || claimed.Guard == refused.Guard):
			verdict.State, verdict.Reason = "rejected", "the exec guard refused before any harness: "+refused.Reason
			return verdict, nil
		case refused != nil || ceased != nil:
			return uncertain("guard records for the Launch name different guards or cessation without a running harness")
		case claimed != nil && liveness != osfacts.Alive:
			return uncertain("the exec guard claimed the Launch and is " + string(liveness) + " without a running record")
		case claimed != nil:
			verdict.State, verdict.Reason = canonicalV19ExecGuardStarting, "the live exec guard claimed the Launch and has not written running yet"
		}
		return verdict, nil
	}
	guard := running.Guard
	committed := request.Spec.Environment[execguard.CredentialEnv].ValueDigest
	switch {
	case refused != nil:
		return uncertain("the exec guard recorded both a running harness and a refusal")
	case claimed == nil || pinned == nil || claimed.Guard != guard || pinned.Guard != guard:
		return uncertain("claimed, pinned and running records do not name one guard incarnation")
	case pinned.RequestDigest != request.RequestDigest || running.RequestDigest != request.RequestDigest ||
		pinned.LaunchSpecDigest != request.LaunchSpecDigest || running.LaunchSpecDigest != request.LaunchSpecDigest ||
		pinned.CredentialVerifier != committed || running.CredentialVerifier != committed:
		return uncertain("guard records do not name the committed request, launch-spec and credential digests")
	case pinned.Executable == nil || pinned.Executable.Path != request.Spec.Executable ||
		(running.ObjectClass != execguard.ClassExact && running.ObjectClass != execguard.ClassSampled):
		return uncertain("guard records do not pin the Launch executable with a known class")
	case running.Root == nil || running.Root.BootID != guard.BootID || running.Root.StartTicks < guard.StartTicks || running.ProcessGroup != running.Root.PID:
		return uncertain("the running record names no harness root of this guard")
	}
	key := canonicalV19ExecGuardKey{
		Assoc: "unobserved", Guard: &guard, Root: running.Root, ProcessGroup: running.ProcessGroup,
		Terminal: running.Terminal, Object: pinned.Executable, ObjectClass: running.ObjectClass,
	}
	key.Session, _ = parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	switch {
	case ceased != nil:
		if ceased.Guard != guard || ceased.Root == nil || *ceased.Root != *running.Root || ceased.Predicate != "wait4-echild" || ceased.Exit == nil {
			return uncertain("the ceased record does not name this guard's tree and platform predicate")
		}
		verdict.TerminalKind = execguard.TerminalKind(*ceased, "")
	case liveness == osfacts.BootChanged:
		verdict.TerminalKind = "provider-gone"
	case liveness != osfacts.Alive:
		return uncertain("the exec guard is " + string(liveness) + " without a ceased record")
	default:
		if reason := checkCanonicalV19ExecGuardOSFacts(guard, running.Terminal); reason != "" {
			if osfacts.Observe(guard) != osfacts.Alive {
				return verdict, nil
			}
			return uncertain(reason)
		}
		key.Assoc = canonicalV19ExecGuardAssociation(client, key.Session.PaneID, guard, running.ProcessGroup)
	}
	encoded, err := encodeCanonicalV19ExecGuardKey(key)
	if err != nil {
		return uncertain("the guard records do not form a provider executor key: " + err.Error())
	}
	verdict.State, verdict.Key = "succeeded", encoded
	return verdict, nil
}

func readCanonicalV19ExecGuardRecords(dir, operationID string) (map[string]*execguard.Record, error) {
	records := map[string]*execguard.Record{}
	for _, kind := range canonicalV19ExecGuardRecordKinds {
		record, err := execguard.ReadRecord(dir, kind)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if record.LaunchOperationID == operationID {
			records[kind] = &record
		}
	}
	return records, nil
}

func checkCanonicalV19ExecGuardOSFacts(guard osfacts.Incarnation, terminal uint64) string {
	namespace, nsErr := osfacts.PIDNamespace(guard.PID)
	self, selfErr := osfacts.SelfPIDNamespace()
	uid, uidErr := osfacts.RealUID(guard.PID)
	tty, ttyErr := osfacts.ControllingTerminal(guard.PID)
	switch {
	case errors.Join(nsErr, selfErr, uidErr, ttyErr) != nil || osfacts.Observe(guard) != osfacts.Alive:
		return "the exec guard's OS facts could not be read"
	case namespace != self || uid != uint32(os.Getuid()):
		return "the exec guard runs in another PID namespace or as another user"
	case tty != terminal:
		return "the running record names another controlling terminal than the OS reports"
	}
	return ""
}

func applyCanonicalV19ExecGuardVerdict(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrLaunchCurrent,
	verdict canonicalV19ExecGuardVerdict,
	now func() time.Time,
) (string, error) {
	request := current.Current.Request
	if verdict.State == "uncertain" && current.Current.State == "uncertain" {
		return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation remains uncertain: %s", verdict.Reason)
	}
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	evidence := canonicalV19ExecGuardEvidenceDigest(current, verdict)
	var err error
	switch verdict.State {
	case "succeeded":
		err = EstablishCanonicalV19ExecutorBinding(ctx, homeDir, CanonicalV19ExecutorBindingEvidence{
			OperationID: request.OperationID, ProviderExecutorKey: verdict.Key, EstablishedAt: observedAt,
			EvidenceDigest: evidence, TerminalKind: verdict.TerminalKind,
		})
	case "no-effect":
		err = classifyCanonicalV19LaunchPreparedNoEffect(ctx, homeDir, CanonicalV19LaunchTransitionInput{
			OperationID: request.OperationID, State: "no-effect", ObservedAt: observedAt, EvidenceDigest: evidence,
		})
	default:
		err = ClassifyCanonicalV19Launch(ctx, homeDir, CanonicalV19LaunchTransitionInput{
			OperationID: request.OperationID, State: verdict.State, ObservedAt: observedAt, EvidenceDigest: evidence,
		})
	}
	if err != nil {
		return current.Current.State, err
	}
	switch verdict.State {
	case "uncertain":
		return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation is uncertain: %s", verdict.Reason)
	case "rejected":
		return "rejected", errors.Join(fmt.Errorf("reconcile canonical v19 Herdr Launch: %s", verdict.Reason), settleCanonicalV19ExecGuardFiles(homeDir, request.OperationID))
	}
	return verdict.State, settleCanonicalV19ExecGuardFiles(homeDir, request.OperationID)
}

func canonicalV19ExecGuardEvidenceDigest(current canonicalV19HerdrLaunchCurrent, verdict canonicalV19ExecGuardVerdict) string {
	records, _ := json.Marshal(verdict.Records)
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-exec-guard-launch-observation:v1")
	writeCanonicalV19DigestField(hash, "operation_id", current.Current.Request.OperationID)
	writeCanonicalV19DigestField(hash, "request_digest", current.Current.Request.RequestDigest)
	writeCanonicalV19DigestField(hash, "state", verdict.State)
	writeCanonicalV19DigestField(hash, "provider_executor_key", verdict.Key)
	writeCanonicalV19DigestField(hash, "terminal_kind", verdict.TerminalKind)
	writeCanonicalV19DigestField(hash, "reason", verdict.Reason)
	writeCanonicalV19DigestField(hash, "records", string(records))
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19ExecGuardAssociation(client canonicalV19HerdrLaunchClient, paneID string, guard osfacts.Incarnation, group int) string {
	if client == nil {
		return "unobserved"
	}
	info, err := client.PaneProcessInfo(paneID)
	var device unix.Stat_t
	if err != nil || info.PaneID != paneID || info.TTY == "" || unix.Stat(info.TTY, &device) != nil {
		return "unobserved"
	}
	terminal, ttyErr := osfacts.ControllingTerminal(guard.PID)
	foreground, fgErr := osfacts.ForegroundGroup(guard.PID)
	if ttyErr != nil || fgErr != nil || osfacts.Observe(guard) != osfacts.Alive {
		return "unobserved"
	}
	return canonicalV19ExecGuardAssociationOf(uint64(device.Rdev), info.ForegroundProcessGroupID, terminal, foreground, group)
}

func canonicalV19ExecGuardAssociationOf(paneTerminal uint64, paneForeground int, terminal uint64, foreground, group int) string {
	if paneTerminal == terminal && foreground == group && paneForeground == group {
		return "observed"
	}
	return "mismatch"
}
