package watcher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

const claudeLimitText = "Claude usage limit reached. Your limit will reset at 3pm (UTC)."

// The harness herdr reports for a pane the usage-limit capability covers. Held as a constant so a test
// asserting the capability's gate reads against the same value the fake serves.
const limitedPaneAgent = "claude"

// Arms the fake herdr with what a usage-limit check reads and where its steers are recorded, and returns
// the log the test asserts against. An empty agent is how a test says "herdr has not classified this
// pane", which is what every test predating the usage-limit check gets by default.
func paneScript(t *testing.T, agent, paneText string) (paneLog string) {
	t.Helper()
	dir := t.TempDir()
	textFile := filepath.Join(dir, "pane-text")
	if err := os.WriteFile(textFile, []byte(paneText), 0o644); err != nil {
		t.Fatal(err)
	}
	paneLog = filepath.Join(dir, "pane-calls")
	t.Setenv("PANE_AGENT", agent)
	t.Setenv("PANE_TEXT_FILE", textFile)
	t.Setenv("PANE_LOG", paneLog)
	return paneLog
}

func paneCalls(t *testing.T, paneLog string) string {
	t.Helper()
	data, err := os.ReadFile(paneLog)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func limitHold(t *testing.T, home, id string) (state.Hold, bool) {
	t.Helper()
	h, exists, err := state.ReadHold(home, id)
	if err != nil {
		t.Fatal(err)
	}
	return h, exists
}

// Rewrites the durable retry stamp, which is how a test makes an attempt due without waiting one out.
// Dropping the tracking map alongside it is the restart the stamp exists for: the watcher that scheduled
// the attempt is gone, and the one that resumes has nothing but this column to learn from.
func setLimitRetryAt(t *testing.T, home, id string, at time.Time) {
	t.Helper()
	_, attempt := readTaskAttempt(t, home, id)
	attempt.UsageLimitRetryAt = at.UTC().Format(time.RFC3339)
	if err := state.UpdateAttempt(home, attempt); err != nil {
		t.Fatal(err)
	}
}

// The behavior atqamz/hand#136 asks for, end to end through tick: a worker whose harness stopped
// on a usage limit is detected, recorded, steered once its schedule comes due, and released the moment
// it is running again. Recognising the message is only the first of the four.
func TestTickResumesALimitedWorkerAndLetsGoWhenItRuns(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	paneLog := paneScript(t, limitedPaneAgent, claudeLimitText)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var buf bytes.Buffer

	tick(ctx, cfg, client, states, &buf, &buf)
	if calls := paneCalls(t, paneLog); calls != "" {
		t.Fatalf("pane calls = %q, want none: a working pane has not stopped on anything", calls)
	}

	setStatus(t, statusFile, "done")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &buf)
	if !strings.Contains(buf.String(), "usage-limit task-1") {
		t.Fatalf("output = %q, want usage-limit task-1 on the stop that carried the limit message", buf.String())
	}
	if calls := paneCalls(t, paneLog); calls != "read\n" {
		t.Fatalf("pane calls = %q, want exactly one read: the limit is recorded on detection, not steered on it", calls)
	}
	held, exists := limitHold(t, home, "task-1")
	if !exists || held.Kind != state.HoldKindLimit {
		t.Fatalf("hold = %+v, exists = %v, want a %s hold", held, exists, state.HoldKindLimit)
	}
	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.UsageLimitRetryAt == "" {
		t.Fatal("usage_limit_retry_at is empty, want the schedule a restart resumes from")
	}
	retryAt, err := time.Parse(time.RFC3339, attempt.UsageLimitRetryAt)
	if err != nil {
		t.Fatal(err)
	}
	if !retryAt.After(time.Now()) {
		t.Fatalf("usage_limit_retry_at = %s, want an instant still ahead: the named reset is 3pm UTC", retryAt)
	}

	// A due attempt, reached the way a real one is: the stamp says the wait is over.
	setLimitRetryAt(t, home, "task-1", time.Now().Add(-time.Minute))
	states = make(map[string]*TaskState)
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &buf)
	tick(ctx, cfg, client, states, &buf, &buf)
	calls := paneCalls(t, paneLog)
	if !strings.Contains(calls, "send-text") || !strings.Contains(calls, "send-keys Enter") {
		t.Fatalf("pane calls = %q, want the due attempt to steer the pane and submit it", calls)
	}
	_, attempt = readTaskAttempt(t, home, "task-1")
	if got := attempt.UsageLimitAttempts; got != 1 {
		t.Fatalf("usage_limit_attempts = %d, want 1", got)
	}

	setStatus(t, statusFile, "working")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &buf)
	if !strings.Contains(buf.String(), "usage-limit-resumed task-1") {
		t.Fatalf("output = %q, want usage-limit-resumed task-1 once the worker is running again", buf.String())
	}
	if _, exists := limitHold(t, home, "task-1"); exists {
		t.Fatal("the limit hold outlived the limit, so hand spawn would refuse this id over quota nobody is waiting on")
	}
	_, attempt = readTaskAttempt(t, home, "task-1")
	if attempt.UsageLimitRetryAt != "" || attempt.UsageLimitAttempts != 0 {
		t.Fatalf("usage-limit columns = %q/%d, want both cleared", attempt.UsageLimitRetryAt, attempt.UsageLimitAttempts)
	}
}

// The other half of the same behavior: a worker that stopped for any other reason must come out of a
// tick untouched. A mechanism that steers on a stop it cannot explain is worse than no mechanism, since
// the steer lands in a pane whose worker had its own reason to be quiet.
func TestTickLeavesAWorkerThatStoppedForAnotherReasonAlone(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	paneLog := paneScript(t, limitedPaneAgent, "$ go test -race ./...\nok  github.com/atqamz/hand/internal/watcher\n")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var buf bytes.Buffer

	tick(ctx, cfg, client, states, &buf, &buf)
	setStatus(t, statusFile, "done")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &buf)
	tick(ctx, cfg, client, states, &buf, &buf)

	if strings.Contains(buf.String(), "usage-limit") {
		t.Fatalf("output = %q, want no usage-limit event: nothing on this pane says the harness ran out of quota", buf.String())
	}
	if calls := paneCalls(t, paneLog); strings.Contains(calls, "send") {
		t.Fatalf("pane calls = %q, want no steer for an unlimited worker", calls)
	}
	if _, exists := limitHold(t, home, "task-1"); exists {
		t.Fatal("a limit hold was set on a worker that never hit a limit")
	}
	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.UsageLimitRetryAt != "" {
		t.Fatalf("usage_limit_retry_at = %q, want empty", attempt.UsageLimitRetryAt)
	}
}

// The capability gate is the whole reason detection is not one more condition on the
// poll loop, so it is worth asserting directly: a harness with no catalogued signature
// costs no pane read at all, however much its pane looks like claude's.
func TestTickReadsNoPaneForAHarnessWithoutTheCapability(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	paneLog := paneScript(t, "codex", claudeLimitText)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var buf bytes.Buffer

	tick(ctx, cfg, client, states, &buf, &buf)
	setStatus(t, statusFile, "done")
	tick(ctx, cfg, client, states, &buf, &buf)

	if calls := paneCalls(t, paneLog); calls != "" {
		t.Fatalf("pane calls = %q, want none: codex declines the usage-limit capability", calls)
	}
}

// A watcher that starts up against a worker already stranded has no transition left to
// detect on - the stop happened before the process existed - so the first sighting has
// to do it instead, or every restart leaves the fleet's limited workers dead.
func TestTickDetectsALimitThatPredatesTheWatcher(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)
	paneScript(t, limitedPaneAgent, claudeLimitText)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var buf bytes.Buffer

	tick(ctx, cfg, client, states, &buf, &buf)
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &buf)

	if !strings.Contains(buf.String(), "usage-limit task-1") {
		t.Fatalf("output = %q, want usage-limit task-1 from the first sighting of an already-stopped worker", buf.String())
	}
}

// A worker that said done or failed is not waiting on quota, whatever is still on its
// screen. Steering one would restart work that is over.
func TestTickDoesNotResumeALimitedWorkerThatReportedDone(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)
	paneLog := paneScript(t, limitedPaneAgent, claudeLimitText)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		LastReportState: state.ReportDone, LastReportNote: "shipped"},
	)
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var buf bytes.Buffer

	tick(ctx, cfg, client, states, &buf, &buf)
	tick(ctx, cfg, client, states, &buf, &buf)

	if calls := paneCalls(t, paneLog); calls != "" {
		t.Fatalf("pane calls = %q, want none: this worker's own last word explains the quiet pane", calls)
	}
	if _, exists := limitHold(t, home, "task-1"); exists {
		t.Fatal("a limit hold was set on a worker that reported done")
	}
}

// A limit that lifts while a task is already scheduled must not keep collecting
// attempts against it, and the durable schedule and its visible hold must go together
// or hand status and hand watch disagree about whether the fleet is waiting on quota.
func TestTickReleasesALimitTheOperatorEndedItself(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)
	paneScript(t, limitedPaneAgent, claudeLimitText)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		UsageLimitRetryAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), UsageLimitAttempts: 2},
	)
	if err := state.SetHold(home, state.Hold{
		ID: "task-1", Kind: state.HoldKindLimit, Reason: "harness stopped on a usage limit",
		SetAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	states := make(map[string]*TaskState)
	ctx := context.Background()
	var buf bytes.Buffer

	tick(ctx, cfg, client, states, &buf, &buf)
	setStatus(t, statusFile, "working")
	buf.Reset()
	tick(ctx, cfg, client, states, &buf, &buf)

	if !strings.Contains(buf.String(), "usage-limit-resumed task-1") {
		t.Fatalf("output = %q, want usage-limit-resumed task-1: a hand send is as good a resume as the watcher's own", buf.String())
	}
	if _, exists := limitHold(t, home, "task-1"); exists {
		t.Fatal("the limit hold survived the worker running again")
	}
	_, attempt := readTaskAttempt(t, home, "task-1")
	if attempt.UsageLimitRetryAt != "" {
		t.Fatalf("usage_limit_retry_at = %q, want empty", attempt.UsageLimitRetryAt)
	}
}

// A submitted automatic resume is observed rather than repeated on later watcher restarts.
func TestUsageLimitDoesNotResendSubmittedResume(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "done")
	writeFakeHerdr(t, statusFile)
	paneLog := paneScript(t, limitedPaneAgent, "Claude usage limit reached.")

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"},
		UsageLimitAttempts: limitStuckAfter - 1},
	)
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	ctx := context.Background()
	var buf bytes.Buffer

	for range 3 {
		setLimitRetryAt(t, home, "task-1", time.Now().Add(-time.Minute))
		states := make(map[string]*TaskState)
		tick(ctx, cfg, client, states, &buf, &buf)
		tick(ctx, cfg, client, states, &buf, &buf)
	}

	if got := strings.Count(paneCalls(t, paneLog), "send-text"); got != 1 {
		t.Fatalf("send-text calls = %d, want one across watcher restarts", got)
	}
	_, attempt := readTaskAttempt(t, home, "task-1")
	if got := attempt.UsageLimitAttempts; got != limitStuckAfter {
		t.Fatalf("usage_limit_attempts = %d, want the one submitted attempt recorded after the prior attempts", got)
	}
}

func TestUsageLimitResumesAgainAfterACompletedEpisode(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status")
	setStatus(t, statusFile, "working")
	writeFakeHerdr(t, statusFile)
	paneLog := paneScript(t, limitedPaneAgent, claudeLimitText)

	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	cfg := Config{Home: home, PollInterval: time.Hour, StaleThreshold: time.Hour}
	client := herdr.NewClient()
	ctx := context.Background()
	var buf bytes.Buffer
	states := make(map[string]*TaskState)

	tick(ctx, cfg, client, states, &buf, &buf)
	setStatus(t, statusFile, "done")
	tick(ctx, cfg, client, states, &buf, &buf)
	states = make(map[string]*TaskState)
	setLimitRetryAt(t, home, "task-1", time.Now().Add(-time.Minute))
	tick(ctx, cfg, client, states, &buf, &buf)
	tick(ctx, cfg, client, states, &buf, &buf)

	setStatus(t, statusFile, "working")
	tick(ctx, cfg, client, states, &buf, &buf)
	setStatus(t, statusFile, "done")
	tick(ctx, cfg, client, states, &buf, &buf)

	states = make(map[string]*TaskState)
	setLimitRetryAt(t, home, "task-1", time.Now().Add(-time.Minute))
	tick(ctx, cfg, client, states, &buf, &buf)
	tick(ctx, cfg, client, states, &buf, &buf)

	if got := strings.Count(paneCalls(t, paneLog), "send-text"); got != 2 {
		t.Fatalf("send-text calls = %d, want one resume per limit episode", got)
	}
}

// usage-limit-stuck is the one kind of the three that reaches config/notify. A limit
// that clears on its own wakes nobody by design, so the other two must stay out.
func TestOnlyTheStuckUsageLimitKindNotifies(t *testing.T) {
	filter := NotifyFilter()
	if !filter.Matches(KindUsageLimitStuck) {
		t.Errorf("%s does not notify, want it to", KindUsageLimitStuck)
	}
	for _, kind := range []string{KindUsageLimit, KindUsageLimitResumed} {
		if filter.Matches(kind) {
			t.Errorf("%s notifies, want it not to: the resume needs no human", kind)
		}
	}
}

func TestNextLimitRetryIsBounded(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		reset    time.Time
		attempts int
		want     time.Duration
	}{
		{
			name:  "no named reset waits the floor",
			reset: time.Time{},
			want:  limitFloor,
		},
		{
			name:  "a named reset is waited out, just past it",
			reset: now.Add(3 * time.Hour),
			want:  3*time.Hour + limitSkew,
		},
		{
			// A reset already behind the message is no schedule at all, so the
			// backoff decides - never an attempt on the spot.
			name:     "a reset in the past falls back to the backoff",
			reset:    now.Add(-time.Hour),
			attempts: 2,
			want:     4 * limitFloor,
		},
		{
			name:     "the backoff caps",
			attempts: 20,
			want:     limitBackoffCap,
		},
		{
			// The bound that keeps a misparsed or absurd prediction from stranding
			// the worker: the wait is capped, and the next attempt re-reads the
			// harness's own fresh refusal to reschedule from.
			name:  "an absurd reset caps at the maximum wait",
			reset: now.AddDate(0, 0, 30),
			want:  limitMaxWait,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextLimitRetry(tt.reset, tt.attempts, now)
			if want := now.Add(tt.want); !got.Equal(want) {
				t.Fatalf("nextLimitRetry = %s, want %s", got, want)
			}
		})
	}
}

// A steer that never landed is one lost attempt, not a task that becomes due again on
// every tick - which is the retry storm the whole mechanism is bounded to avoid.
func TestAFailedSteerStillConsumesItsAttempt(t *testing.T) {
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	ts := &TaskState{LimitRetryAt: time.Now().Add(-time.Minute)}
	client := &steerFailingPane{}
	cfg := Config{Home: home}
	task, attempt := readTaskAttempt(t, home, "task-1")
	pane := herdr.Pane{PaneID: "p1", Agent: limitedPaneAgent}
	var errBuf bytes.Buffer

	now := time.Now()
	if e := classifyUsageLimit(cfg, client, ts, task, attempt, pane, herdr.StatusDone, nil, false, now, &errBuf); e != nil {
		t.Fatalf("event = %+v, want none for an ordinary attempt", e)
	}
	if ts.LimitAttempts != 1 {
		t.Fatalf("LimitAttempts = %d, want 1", ts.LimitAttempts)
	}
	if !ts.LimitRetryAt.After(now) {
		t.Fatalf("LimitRetryAt = %s, want it pushed ahead of %s; err=%q", ts.LimitRetryAt, now, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "no automatic resend") {
		t.Fatalf("errOut = %q, want the ambiguous steer and resend suppression reported", errBuf.String())
	}
}

type steerFailingPane struct{}

func (p *steerFailingPane) PaneGet(string) (herdr.Pane, error) {
	return herdr.Pane{PaneID: "p1", AgentStatus: herdr.StatusDone}, nil
}

func (p *steerFailingPane) PaneRead(string, int) (string, error) { return claudeLimitText, nil }

func (p *steerFailingPane) PaneSendText(string, string) error { return errors.New("pane p1 not found") }

func (p *steerFailingPane) PaneSendKeys(string, ...string) error { return nil }

// An attempt is the same steer hand send performs and takes the same lock, so a send in flight is never
// interleaved into the composer it is already writing. The attempt is deferred rather than spent: the
// schedule stays due, and the next tick either steers or finds the send has ended the limit for it.
func TestAnAttemptYieldsToASendHoldingTheLock(t *testing.T) {
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	release, err := state.TryLock(home, "send:task-1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	due := time.Now().Add(-time.Minute)
	ts := &TaskState{LimitRetryAt: due}
	client := &countingPane{}
	cfg := Config{Home: home}
	task := state.Task{ID: "task-1"}
	attempt := state.Attempt{Herdr: state.Herdr{PaneID: "p1"}}
	pane := herdr.Pane{PaneID: "p1", Agent: limitedPaneAgent}
	var errBuf bytes.Buffer

	if e := classifyUsageLimit(cfg, client, ts, task, attempt, pane, herdr.StatusDone, nil, false, time.Now(), &errBuf); e != nil {
		t.Fatalf("event = %+v, want none", e)
	}
	if client.steers != 0 || client.reads != 0 {
		t.Fatalf("reads = %d, steers = %d, want neither: another writer holds the composer", client.reads, client.steers)
	}
	if ts.LimitAttempts != 0 {
		t.Fatalf("LimitAttempts = %d, want 0: a deferred attempt is not a spent one", ts.LimitAttempts)
	}
	if !ts.LimitRetryAt.Equal(due) {
		t.Fatalf("LimitRetryAt = %s, want it left due at %s", ts.LimitRetryAt, due)
	}
}

func TestAnAttemptYieldsToTaskOwnershipChangeAfterSendLock(t *testing.T) {
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	release, err := state.TryLock(home, "task:task-1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	due := time.Now().Add(-time.Minute)
	ts := &TaskState{LimitRetryAt: due}
	client := &countingPane{}
	cfg := Config{Home: home}
	task, attempt := readTaskAttempt(t, home, "task-1")
	pane := herdr.Pane{PaneID: "p1", Agent: limitedPaneAgent}
	var errBuf bytes.Buffer

	if e := classifyUsageLimit(cfg, client, ts, task, attempt, pane, herdr.StatusDone, nil, false, time.Now(), &errBuf); e != nil {
		t.Fatalf("event = %+v, want none", e)
	}
	if client.reads != 0 || client.steers != 0 {
		t.Fatalf("reads = %d, steers = %d, want neither while task ownership changes", client.reads, client.steers)
	}
}

// A tick reads its Task and Attempt before it takes either lock, so `hand teardown` can terminalize both
// in between. The steer would then reach a pane the fleet has taken back, and the hold write would put a
// limit hold on a terminal id, which refuses `hand reopen` and `hand spawn` until it is cleared by hand.
func TestAnAttemptYieldsWhenTeardownReplacedTheOwnershipItRead(t *testing.T) {
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	task, attempt := readTaskAttempt(t, home, "task-1")
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}

	due := time.Now().Add(-time.Minute)
	ts := &TaskState{LimitRetryAt: due}
	client := &countingPane{}
	cfg := Config{Home: home}
	pane := herdr.Pane{PaneID: "p1", Agent: limitedPaneAgent}
	var errBuf bytes.Buffer

	if e := classifyUsageLimit(cfg, client, ts, task, attempt, pane, herdr.StatusDone, nil, false, time.Now(), &errBuf); e != nil {
		t.Fatalf("event = %+v, want none", e)
	}
	if client.reads != 0 || client.steers != 0 {
		t.Fatalf("reads = %d, steers = %d, want neither: the attempt this tick read is no longer the fleet's", client.reads, client.steers)
	}
	if ts.LimitAttempts != 0 || !ts.LimitRetryAt.Equal(due) {
		t.Fatalf("LimitAttempts = %d, LimitRetryAt = %s, want the schedule left alone", ts.LimitAttempts, ts.LimitRetryAt)
	}
	if h, exists := limitHold(t, home, "task-1"); exists {
		t.Fatalf("hold = %+v, want none: a limit hold on a terminal task blocks hand reopen and hand spawn", h)
	}
}

// The hold is only a projection of the schedule, and an operator's hold on the same id is a question of
// their own. Overwriting it would not merely hide that question: the clear at the end of the limit
// matches on kind, so the row - operator hold and all - would be deleted once the worker ran again.
func TestALimitLeavesAnOperatorHoldOnTheSameIDStanding(t *testing.T) {
	home := setupWatcherHome(t, state.Task{ID: "task-1", Project: "nsr", Kind: state.KindShip}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "p1"}})
	operatorHold := state.Hold{
		ID: "task-1", Kind: state.HoldKindOperator, Reason: "two ways to fix this, needs a call",
		SetAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := state.SetHold(home, operatorHold); err != nil {
		t.Fatal(err)
	}

	ts := &TaskState{}
	client := &countingPane{}
	cfg := Config{Home: home}
	task, attempt := readTaskAttempt(t, home, "task-1")
	pane := herdr.Pane{PaneID: "p1", Agent: limitedPaneAgent}
	var errBuf bytes.Buffer

	e := classifyUsageLimit(cfg, client, ts, task, attempt, pane, herdr.StatusDone, nil, true, time.Now(), &errBuf)
	if e == nil || e.Kind != KindUsageLimit {
		t.Fatalf("event = %+v, want %s: the limit is real whatever else holds the id", e, KindUsageLimit)
	}
	if ts.LimitRetryAt.IsZero() {
		t.Fatal("LimitRetryAt is zero: the schedule, not the hold, is what resumes the worker")
	}
	if !strings.Contains(errBuf.String(), "left unprojected") {
		t.Fatalf("errOut = %q, want the unprojected wait reported", errBuf.String())
	}
	held, exists := limitHold(t, home, "task-1")
	if !exists || held.Kind != state.HoldKindOperator || held.Reason != operatorHold.Reason {
		t.Fatalf("hold = %+v, exists = %v, want the operator's own hold untouched", held, exists)
	}

	if e := classifyUsageLimit(cfg, client, ts, task, attempt, pane, herdr.StatusWorking, nil, false, time.Now(), &errBuf); e == nil || e.Kind != KindUsageLimitResumed {
		t.Fatalf("event = %+v, want %s once the worker runs again", e, KindUsageLimitResumed)
	}
	held, exists = limitHold(t, home, "task-1")
	if !exists || held.Kind != state.HoldKindOperator {
		t.Fatalf("hold = %+v, exists = %v: the end of the limit destroyed the operator's question", held, exists)
	}
}

type countingPane struct {
	reads  int
	steers int
}

func (p *countingPane) PaneGet(string) (herdr.Pane, error) {
	return herdr.Pane{PaneID: "p1", AgentStatus: herdr.StatusDone}, nil
}

func (p *countingPane) PaneRead(string, int) (string, error) {
	p.reads++
	return claudeLimitText, nil
}

func (p *countingPane) PaneSendText(string, string) error {
	p.steers++
	return nil
}

func (p *countingPane) PaneSendKeys(string, ...string) error { return nil }
