package supervision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/watcher"
)

// Carries one Wait outcome from a goroutine back to its test.
type waitResult struct {
	wake Wake
	err  error
}

// Two concurrent waiters racing on the same exact episode must produce
// exactly ONE successful non-empty wake overall; every loser keeps waiting
// until its checkpoint and never answers an empty success.
func TestConcurrentWaitersClaimExactlyOnce(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("t1", "g1", "blocked")}}
	reader := fixedReader(evidence)
	stub, restore := newWatcherStub(t, false, nil)
	defer restore()

	var wakes, empties, noEventLosers atomic.Int64
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wake, err := Wait(context.Background(), Waiter{Home: t.TempDir(), ReadEvidence: reader, Ledger: ledger}, WaitConfig{
				Host:         "opencode",
				PollInterval: time.Millisecond,
				Timeout:      250 * time.Millisecond,
			})
			switch {
			case err != nil && errors.Is(err, watcher.ErrNoEvent):
				noEventLosers.Add(1)
				if len(wake.Episodes) != 0 {
					t.Errorf("waiter %d: checkpoint carried episodes", i)
				}
			case err != nil:
				t.Errorf("waiter %d: %v", i, err)
			case len(wake.Episodes) == 0:
				t.Errorf("waiter %d answered an empty successful wake", i)
				empties.Add(1)
			default:
				wakes.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if wakes.Load() != 1 {
		t.Fatalf("successful wakes = %d across 10 concurrent waiters, want exactly one", wakes.Load())
	}
	if empties.Load() != 0 {
		t.Fatalf("empty successful wakes = %d, want zero", empties.Load())
	}
	if noEventLosers.Load() != 9 {
		t.Fatalf("losers ending in a clean checkpoint = %d, want all nine", noEventLosers.Load())
	}
	if stub.acquired.Load() > 9 {
		t.Fatalf("ownership acquisitions = %d, want one per losing waiter at most", stub.acquired.Load())
	}
}

// Different exact episodes may coalesce into one wake; changed currentness is
// independently claimable again.
func TestClaimCoalescesDistinctEpisodesAndNewnessReclaims(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{
		actionableEvidence("t1", "g1", "blocked"),
		actionableEvidence("t2", "g1", "failed"),
		actionableEvidence("t3", "g1", "parked"),
	}}
	wake, err := Wait(context.Background(), Waiter{Home: t.TempDir(), ReadEvidence: fixedReader(evidence), Ledger: ledger}, WaitConfig{Host: "pi"})
	if err != nil || len(wake.Episodes) != 3 {
		t.Fatalf("wake = %#v, %v; want three coalesced subjects in ONE wake", wake, err)
	}

	next := evidence
	next.Actionable = []orientation.ActionableEvidence{actionableEvidence("t1", "g2", "blocked")}
	retry, err := Wait(context.Background(), Waiter{Home: t.TempDir(), ReadEvidence: fixedReader(next), Ledger: ledger}, WaitConfig{Host: "pi"})
	if err != nil || len(retry.Episodes) != 1 {
		t.Fatalf("changed-currentness wake = %#v, %v; want independently eligible", retry, err)
	}
}

// Sixteen distinct legitimate episodes each wake exactly once - the storm and
// permanent-disable guard for Stop-hook recursion limits.
func TestSixteenDistinctEpisodesEachWakeOnce(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	ctx := context.Background()
	for i := range 16 {
		evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{
			actionableEvidence(fmt.Sprintf("t%02d", i), fmt.Sprintf("gen%d", i), "blocked"),
		}}
		wake, err := Wait(ctx, Waiter{Home: t.TempDir(), ReadEvidence: fixedReader(evidence), Ledger: ledger}, WaitConfig{Host: "claude"})
		if err != nil || len(wake.Episodes) != 1 {
			t.Fatalf("episode %d: wake = %#v, %v; want exactly one legitimate rewake", i, wake, err)
		}
		if err := ledger.MarkAccepted(Keys(wake.Episodes)); err != nil {
			t.Fatal(err)
		}
		if err := ledger.MarkOriented(wake.Episodes); err != nil {
			t.Fatal(err)
		}
		// The unchanged episode never re-claims afterwards.
		if again, claimErr := ledger.ClaimEligible(FromEvidence(evidence)); claimErr != nil || len(again) != 0 {
			t.Fatalf("episode %d re-claimed after orient: %d, %v", i, len(again), claimErr)
		}
	}
}

// Attachment comes from this runtime's heartbeat record, watcher liveness from
// fleet-home ownership; neither field may imply the other.
func TestAttachmentIsIndependentFromWatcherLiveness(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeAttachmentRecord(t, home, AttachmentRecord{
		Host: "opencode", Runtime: "ses-1", PID: 1, FleetID: "f_1",
		StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	})
	status := Status{Harness: harness.OpenCode}
	status.finishIndependentFields(StatusInput{Home: home})
	if status.Attachment != "attached:ses-1" {
		t.Fatalf("attachment = %q, want attached to the live runtime record", status.Attachment)
	}
	if status.WatcherLiveness != "idle" {
		t.Fatalf("liveness = %q on a home no watcher owns; the two fields are independent answers", status.WatcherLiveness)
	}

	expired := home
	status2 := Status{Harness: harness.OpenCode}
	writeAttachmentRecord(t, expired, AttachmentRecord{
		Host: "opencode", Runtime: "ses-9", PID: 1, FleetID: "f_1",
		StartedAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	})
	status2.finishIndependentFields(StatusInput{Home: expired})
	if status2.Attachment != "detached" {
		t.Fatalf("expired heartbeat reads %q, want detached", status2.Attachment)
	}
}

// A secondary runtime defers instead of stealing a bridge another live
// runtime already holds.
func TestSecondaryRuntimeCannotStealAnAttachedBridge(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeAttachmentRecord(t, home, AttachmentRecord{
		Host: "opencode", Runtime: "primary-session", PID: 424242, FleetID: "f_1",
		StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	})
	evidence := orientation.Evidence{FleetID: "f_1"}
	stub, restore := newWatcherStub(t, true, nil)
	defer restore()

	_, err := Wait(context.Background(), Waiter{
		Home:         home,
		ReadEvidence: func(context.Context) (orientation.Evidence, error) { return evidence, nil },
		Ledger:       OpenLedger(t.TempDir()),
	}, WaitConfig{Host: "opencode", RuntimeSession: "secondary-session", PollInterval: time.Millisecond})
	if !errors.Is(err, ErrBridgeOwned) {
		t.Fatalf("err = %v, want ErrBridgeOwned for the secondary runtime", err)
	}
	if stub.acquired.Load() != 0 {
		t.Fatal("a deferring waiter must not touch watcher ownership either")
	}
}

// Capability honesty: nothing claims supported before live qualification,
// instruction-only bridges included, and non-supervisor harness names are
// outside the registry even if they build workers.
func TestCapabilityVocabularyStaysHonestBeforeLiveQualification(t *testing.T) {
	for _, rule := range SupervisorQualificationMatrix().Rules {
		if rule.Host == "" || rule.RuntimeVersion == "" || rule.Platform == "" || rule.Evidence == "" {
			t.Fatalf("qualification rule is not exact and evidence-backed: %+v", rule)
		}
	}
	home := t.TempDir()
	exe := "/opt/bin/hand"
	if _, err := InstallClaudeStopHook(home, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexHooks(home, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHostAssets(home, harness.OpenCode, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHostAssets(home, harness.Pi, exe); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CodexThreadEnv, "thr_live")
	for _, name := range SupervisorHosts() {
		status, err := IntegrationStatus(context.Background(), StatusInput{Home: home, Detection: harness.Detection{Name: name, Source: "override"}, Exe: exe})
		if err != nil {
			t.Fatal(err)
		}
		if status.WakeDelivery == CapabilitySupported {
			t.Fatalf("%s claimed supported without any live qualification", name)
		}
		if status.WakeDelivery != CapabilityUnqualified && status.WakeDelivery != CapabilityDegraded {
			t.Fatalf("%s = %q with full static integration installed; want available-unqualified or degraded with reason", name, status.WakeDelivery)
		}
	}
	grok, err := IntegrationStatus(context.Background(), StatusInput{Home: home, Detection: harness.Detection{Name: harness.Grok, Source: "override"}, Exe: exe})
	if err != nil {
		t.Fatal(err)
	}
	if grok.Integration != "not-required" && grok.WakeDelivery == CapabilitySupported {
		t.Fatal("grok must not become supported from instructions alone")
	}
}

func TestWorkerCapableNameOutsideRegistryIsRejected(t *testing.T) {
	if IsSupervisorHost("antigravity") {
		t.Fatal("antigravity is worker-execution support only; it must never enter the supervisor registry implicitly")
	}
	for _, name := range []string{"antigravity", "", "claude-code"} {
		if IsSupervisorHost(name) {
			t.Fatalf("%q must not resolve as a supervisor host", name)
		}
	}
}

// Crash after host acceptance but before orient: suppressed through the
// progress window, then bounded recovery makes it eligible again.
func TestAcceptedThenCrashRecoversBounded(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	now := time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)
	ledger.now = func() time.Time { return now }
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("t1", "g1", "unacknowledged")}}
	episodes := FromEvidence(evidence)

	claimed, claimErr := ledger.ClaimEligible(episodes)
	if claimErr != nil || len(claimed) != 1 {
		t.Fatalf("claim = %d, %v; want the episode", len(claimed), claimErr)
	}
	if err := ledger.MarkAccepted(Keys(claimed)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(DefaultPolicy().ProgressTimeout / 2)
	if eligible := ledger.Eligible(episodes); len(eligible) != 0 {
		t.Fatal("inside the window a crashed delivery is still awaited")
	}
	now = now.Add(2 * DefaultPolicy().ProgressTimeout)
	if eligible := ledger.Eligible(episodes); len(eligible) != 1 {
		t.Fatal("past the window bounded recovery re-eligibles the episode")
	}
}

// The one deterministic episode cross-process children and parent both
// derive, so their keys always agree.
func fixedClaimEpisode() Episode {
	return Episode{
		FleetID: "f_fix", TargetID: "t_fix", TargetKind: "task",
		Currentness: orientation.TargetFor("f_fix", orientation.TargetEvidence{ID: "t_fix", Kind: "task", Generation: []string{"gfix"}}).Currentness,
		Kind:        "blocked",
	}
}

// Child body for the cross-process tests: performs exactly one operation and
// writes a JSON verdict, so the parent can count winners across real process
// boundaries where flock - not a mutex - is the only coordination.
func runSupervisionChild(op string) int {
	home := os.Getenv("HAND_SUPERVISION_CHILD_HOME")
	switch op {
	case "claim":
		claimed, err := OpenLedger(home).ClaimEligible([]Episode{fixedClaimEpisode()})
		if err != nil {
			fmt.Fprintln(os.Stderr, "child claim:", err)
			return 3
		}
		return writeChildVerdict(len(claimed))
	case "acquire":
		now := time.Now()
		acquired, err := AcquireAttachment(home, AttachmentRecord{
			Host:    os.Getenv("HAND_SUPERVISION_CHILD_HOST"),
			Runtime: os.Getenv("HAND_SUPERVISION_CHILD_RUNTIME"), PID: os.Getpid(),
			FleetID: "f_fix", StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "child acquire:", err)
			return 3
		}
		return writeChildVerdict(map[bool]int{true: 1, false: 0}[acquired])
	}
	fmt.Fprintln(os.Stderr, "unknown child op")
	return 2
}

func writeChildVerdict(won int) int {
	out := os.Getenv("HAND_SUPERVISION_CHILD_OUT")
	if out == "" {
		return 4
	}
	if err := os.WriteFile(out, []byte(fmt.Sprintf(`{"won":%d}`, won)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "child verdict:", err)
		return 4
	}
	return 0
}

// Spawns eight real processes that each attempt the same single-episode wake
// claim simultaneously. Exactly one may win; the file lock is the only thing
// coordinating them.
func TestClaimIsAtomicAcrossProcesses(t *testing.T) {
	if os.Getenv("HAND_SUPERVISION_CHILD_OP") != "" {
		t.Skip("child mode")
	}
	home := t.TempDir()
	wins := spawnSupervisionChildren(t, home, "claim", []string{"opencode", "opencode", "opencode", "opencode", "opencode", "opencode", "opencode", "opencode"})
	if wins != 1 {
		t.Fatalf("winning claims across 8 processes = %d, want exactly one", wins)
	}
}

// Two runtimes racing to acquire the same host bridge across process
// boundaries: exactly one holds it, the other is told it is owned.
func TestAttachmentAcquireIsAtomicAcrossProcesses(t *testing.T) {
	if os.Getenv("HAND_SUPERVISION_CHILD_OP") != "" {
		t.Skip("child mode")
	}
	home := t.TempDir()
	wins := spawnSupervisionChildren(t, home, "acquire", []string{"opencode", "opencode"})
	if wins != 1 {
		t.Fatalf("winning acquisitions across 2 processes = %d, want exactly one holder", wins)
	}
}

// Harness name selects a delivery mechanism, never an ownership domain: every
// representative pair contends for the SAME Fleet bridge, proven with real
// subprocesses where flock is the only coordination.
func TestAttachmentAcquireIsFleetExclusiveAcrossHosts(t *testing.T) {
	if os.Getenv("HAND_SUPERVISION_CHILD_OP") != "" {
		t.Skip("child mode")
	}
	for _, pair := range [][2]string{
		{"opencode", "claude"},
		{"claude", "codex"},
		{"pi", "grok"},
		{"grok", "opencode"},
	} {
		t.Run(pair[0]+"-vs-"+pair[1], func(t *testing.T) {
			home := t.TempDir()
			wins := spawnSupervisionChildren(t, home, "acquire", pair[:])
			if wins != 1 {
				t.Fatalf("holders for %s vs %s = %d, want exactly ONE Fleet bridge owner", pair[0], pair[1], wins)
			}
		})
	}
}

func spawnSupervisionChildren(t *testing.T, home, op string, hosts []string) int {
	t.Helper()
	n := len(hosts)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	outs := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		out := filepath.Join(t.TempDir(), fmt.Sprintf("verdict-%d.json", i))
		outs[i] = out
		cmd := exec.Command(exe, "-test.run=TestSupervisionChildProcess$", "-test.timeout=60s")
		cmd.Env = append(os.Environ(),
			"HAND_SUPERVISION_CHILD_OP="+op,
			"HAND_SUPERVISION_CHILD_HOME="+home,
			"HAND_SUPERVISION_CHILD_HOST="+hosts[i],
			"HAND_SUPERVISION_CHILD_RUNTIME="+fmt.Sprintf("runtime-%d", i),
			"HAND_SUPERVISION_CHILD_OUT="+out,
		)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if runErr := cmd.Run(); runErr != nil {
				t.Errorf("child %d exited %v", i, runErr)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	won := 0
	for _, out := range outs {
		data, readErr := os.ReadFile(out)
		if readErr != nil {
			t.Fatalf("child verdict %s: %v", out, readErr)
		}
		var v struct {
			Won int `json:"won"`
		}
		if json.Unmarshal(data, &v) != nil {
			t.Fatalf("verdict %s unparsable: %s", out, data)
		}
		won += v.Won
	}
	return won
}

// Guarded entry point the parent re-executes as a child process.
func TestSupervisionChildProcess(t *testing.T) {
	op := os.Getenv("HAND_SUPERVISION_CHILD_OP")
	if op == "" {
		t.Skip("parent mode")
	}
	code := runSupervisionChild(op)
	if code != 0 {
		t.Fatalf("child failed with code %d", code)
	}
}

// In-process twin of the cross-process acquisition proof.
func TestAttachmentAcquireAllowsExactlyOneHolderConcurrently(t *testing.T) {
	home := t.TempDir()
	mkRecord := func(runtimeName string) AttachmentRecord {
		now := time.Now()
		return AttachmentRecord{Host: "pi", Runtime: runtimeName, PID: os.Getpid(), FleetID: "f_1",
			StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute)}
	}
	var holders atomic.Int64
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acquired, err := AcquireAttachment(home, mkRecord(fmt.Sprintf("runtime-%d", i)))
			if err != nil {
				t.Errorf("runtime-%d: %v", i, err)
				return
			}
			if acquired {
				holders.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if holders.Load() != 1 {
		t.Fatalf("holders = %d across 6 concurrent acquirers, want exactly one", holders.Load())
	}
}

func writeAttachmentRecord(t *testing.T, home string, record AttachmentRecord) {
	t.Helper()
	record.Schema = AttachmentSchema
	if err := os.MkdirAll(filepath.Join(home, "state", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "state", "runtime", "supervision-attachment.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Harness name changes HOW a runtime is woken, never WHO owns the Fleet
// bridge: every fresh different-owner combination is rejected, expired
// owners are takeover-eligible, and only the exact owner may refresh.
func TestAttachmentOwnershipIsFleetExclusiveAcrossHosts(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeAttachmentRecord(t, home, AttachmentRecord{
		Host: "opencode", Runtime: "session-a", PID: 1, FleetID: "f_1",
		StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	})

	incoming := func(host, runtimeName string) AttachmentRecord {
		return AttachmentRecord{Host: host, Runtime: runtimeName, PID: 2, FleetID: "f_1",
			StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute)}
	}

	if acquired, err := AcquireAttachment(home, incoming("claude", "session-b")); err != nil || acquired {
		t.Fatalf("claude over fresh opencode owner = %v, %v; want rejected", acquired, err)
	}
	if owner := BridgeOwner(home, "opencode", now); owner != "session-a" {
		t.Fatalf("owner = %q after foreign attempt, want the original untouched", owner)
	}

	if acquired, err := AcquireAttachment(home, incoming("codex", "thr-a")); err != nil || acquired {
		t.Fatalf("codex over fresh opencode owner = %v, %v; want rejected", acquired, err)
	}

	if _, err := AcquireAttachment(home, incoming("opencode", "session-a")); err != nil {
		t.Fatal(err)
	}
	ours, err := RefreshAttachment(home, incoming("opencode", "session-a"), time.Minute)
	if err != nil || !ours {
		t.Fatalf("exact-owner refresh = %v, %v; want allowed", ours, err)
	}
	foreignOurs, err := RefreshAttachment(home, incoming("codex", "thr-a"), time.Minute)
	if err != nil || foreignOurs {
		t.Fatalf("foreign-host refresh = %v, %v; want refused", foreignOurs, err)
	}
}

func TestAttachmentExpiredOwnerAllowsCrossHostTakeover(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeAttachmentRecord(t, home, AttachmentRecord{
		Host: "pi", Runtime: "pi-a", PID: 1, FleetID: "f_1",
		StartedAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second),
	})

	acquired, err := AcquireAttachment(home, AttachmentRecord{
		Host: "claude", Runtime: "claude-b", PID: 2, FleetID: "f_1",
		StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil || !acquired {
		t.Fatalf("takeover over expired owner = %v, %v; want allowed", acquired, err)
	}
	if owner := BridgeOwner(home, "claude", now); owner != "claude-b" {
		t.Fatalf("owner = %q, want the successor", owner)
	}
}

func TestRefreshPreservesStartedAtAndAdvancesLeaseExplicitly(t *testing.T) {
	home := t.TempDir()
	started := time.Now().Add(-time.Hour)
	writeAttachmentRecord(t, home, AttachmentRecord{
		Host: "pi", Runtime: "pi-a", PID: 1, FleetID: "f_1",
		StartedAt: started, HeartbeatAt: started, ExpiresAt: started.Add(3 * time.Second),
	})
	rec := AttachmentRecord{Host: "pi", Runtime: "pi-a", PID: 1, FleetID: "f_1"}

	before := ReadAttachment(home)
	lease := time.Minute
	ours, err := RefreshAttachment(home, rec, lease)
	if err != nil || !ours {
		t.Fatalf("refresh = %v, %v; want owned", ours, err)
	}
	after := ReadAttachment(home)
	if !after.StartedAt.Equal(before.StartedAt) {
		t.Fatalf("StartedAt moved from %v to %v; refresh must preserve it", before.StartedAt, after.StartedAt)
	}
	if remaining := time.Until(after.ExpiresAt); remaining > lease || remaining < lease-time.Second {
		t.Fatalf("lease after refresh = %v, want ~%v regardless of prior staleness", remaining, lease)
	}
}

// Losing bridge ownership mid-wait cancels the active watcher wait and
// forbids any claim: the retired runtime must not deliver as if it still
// owned delivery.
func TestOwnershipLossCancelsActiveWaitAndForbidsClaim(t *testing.T) {
	home := t.TempDir()
	ledger := OpenLedger(home)

	origRun := runWatcherUntilEvent
	stolen := make(chan struct{})
	runWatcherUntilEvent = func(ctx context.Context, cfg watcher.Config, out, errOut io.Writer) error {
		<-ctx.Done() // blocks exactly like a live watcher wait; only guard cancellation ends it
		return context.Cause(ctx)
	}
	origAcquire := acquireWatcherOwnership
	acquireWatcherOwnership = func(context.Context, string, bool) (*watcher.Ownership, error) { return nil, nil }
	restore := func() {
		runWatcherUntilEvent = origRun
		acquireWatcherOwnership = origAcquire
		watcherAttached = func(string) (bool, error) { return false, nil }
	}
	watcherAttached = func(string) (bool, error) { return false, nil }
	defer restore()

	results := make(chan waitResult, 1)
	go func() {
		wake, err := Wait(context.Background(), Waiter{
			Home:         home,
			ReadEvidence: fixedReader(orientation.Evidence{FleetID: "f_1"}),
			Ledger:       ledger,
		}, WaitConfig{Host: "claude", RuntimeSession: "runtime-old", PollInterval: time.Millisecond})
		results <- waitResult{wake, err}
	}()

	waitForAttachmentRuntime(t, home, "runtime-old")
	// Successor takes the Fleet bridge while the old waiter sits blocked.
	writeAttachmentRecord(t, home, AttachmentRecord{
		Host: "claude", Runtime: "runtime-new", PID: 999999, FleetID: "f_1",
		StartedAt: time.Now(), HeartbeatAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute),
	})
	close(stolen)

	select {
	case got := <-results:
		if !errors.Is(got.err, ErrBridgeOwned) {
			t.Fatalf("err = %v, want ErrBridgeOwned from ownership loss", got.err)
		}
		if len(got.wake.Episodes) != 0 {
			t.Fatalf("episodes = %d, want zero claimed by the retired runtime", len(got.wake.Episodes))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("wait was not cancelled after ownership loss; it kept blocking on the watcher")
	}

	// The episode stays available to the true owner: no requested stamp was
	// written by the retired runtime.
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("t1", "g1", "blocked")}}
	claimed, claimErr := ledger.ClaimEligible(FromEvidence(evidence))
	if claimErr != nil || len(claimed) != 1 {
		t.Fatalf("true owner claim = %d, %v; want the episode unsuppressed", len(claimed), claimErr)
	}
}

func waitForAttachmentRuntime(t *testing.T, home, runtimeName string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if record := ReadAttachment(home); record != nil && record.Runtime == runtimeName && record.Fresh(time.Now()) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("attachment for %q never appeared", runtimeName)
}

// Takeover racing the watcher event: the stale runtime fails its pre-claim
// proof, consumes nothing, and the episode stays claimable by the new owner.
func TestClaimBoundaryTakeoverKeepsEpisodeAvailable(t *testing.T) {
	home := t.TempDir()
	ledger := OpenLedger(home)
	evidence := orientation.Evidence{FleetID: "f_1"}
	eventReady := make(chan struct{})
	release := make(chan struct{})

	origRun := runWatcherUntilEvent
	var once sync.Once
	runWatcherUntilEvent = func(ctx context.Context, cfg watcher.Config, out, errOut io.Writer) error {
		once.Do(func() {
			evidence.Actionable = append(evidence.Actionable, actionableEvidence("t9", "g1", "parked"))
			close(eventReady)
		})
		<-release
		return nil
	}
	origAcquire := acquireWatcherOwnership
	acquireWatcherOwnership = func(context.Context, string, bool) (*watcher.Ownership, error) { return nil, nil }
	watcherAttached = func(string) (bool, error) { return false, nil }
	defer func() {
		runWatcherUntilEvent = origRun
		acquireWatcherOwnership = origAcquire
	}()

	results := make(chan waitResult, 1)
	go func() {
		wake, err := Wait(context.Background(), Waiter{
			Home:         home,
			ReadEvidence: fixedReader(evidence),
			Ledger:       ledger,
		}, WaitConfig{Host: "claude", RuntimeSession: "runtime-old", PollInterval: time.Millisecond})
		results <- waitResult{wake, err}
	}()

	<-eventReady
	writeAttachmentRecord(t, home, AttachmentRecord{
		Host: "claude", Runtime: "runtime-new", PID: 999999, FleetID: "f_1",
		StartedAt: time.Now(), HeartbeatAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute),
	})
	close(release)

	select {
	case got := <-results:
		if !errors.Is(got.err, ErrBridgeOwned) {
			t.Fatalf("err = %v, want the pre-claim ownership proof to reject the stale runtime", got.err)
		}
		if len(got.wake.Episodes) != 0 {
			t.Fatalf("stale runtime claimed %d episodes across the handover", len(got.wake.Episodes))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("wait did not return after the handover")
	}

	raw, readErr := os.ReadFile(filepath.Join(home, "state", "runtime", "supervision-wake.json"))
	if readErr == nil && strings.Contains(string(raw), `"delivery_requested"`) {
		t.Fatal("episode was durably consumed by the stale runtime; it must stay available to the new owner")
	}
	claimed, claimErr := ledger.ClaimEligible(FromEvidence(evidence))
	if claimErr != nil || len(claimed) != 1 {
		t.Fatalf("new-owner claim = %d, %v; want the episode fully available", len(claimed), claimErr)
	}
}
