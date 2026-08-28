package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/filelock"
	"github.com/atqamz/hand/internal/state"
)

// ErrAttached is wrapped into Acquire's refusal so cmd can render it as a
// precondition failure rather than a general error.
var ErrAttached = errors.New("a watcher is already attached to this fleet home")

// How long Acquire --takeover waits for a cooperatively-requested incumbent to
// release before failing, and how often it re-tries the lock. Variables so a
// test can shrink the wait without sleeping out the default grace.
var (
	takeoverGrace     = 5 * time.Second
	takeoverPoll      = 50 * time.Millisecond
	afterLockAcquired = func() {}
)

// OwnerPath names the advisory file that records the owning watcher's pid.
// Backward-compatible with what operators already read; OwnerRecordPath is the
// coherent routing record #222 routes takeover through, and neither is authority.
func OwnerPath(homeDir string) string {
	return filepath.Join(state.Dir(homeDir), "watch.pid")
}

func stateDir(homeDir string) string {
	return state.Dir(homeDir)
}

func atomicfileWrite(path string, data []byte) error {
	return atomicfile.Write(path, ".atomic-", data, 0o644)
}

// Ownership keeps the files and endpoint that make a watcher the fleet-home owner.
type Ownership struct {
	home     string
	lockFile *os.File
	endpoint *takeoverEndpoint
	record   OwnerRecord
	once     sync.Once
}

// Acquire makes hand watch a singleton per fleet home. The kernel drops the lock when the holder dies.
func Acquire(homeDir string, takeover bool) (*Ownership, error) {
	return AcquireContext(context.Background(), homeDir, takeover)
}

func IsAttached(homeDir string) (bool, error) {
	home := canonicalHome(homeDir)
	lockFile, err := os.OpenFile(OwnerPath(home)+".lock", os.O_RDWR, 0o644)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open watcher lock: %w", err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := lockOwner(lockFile); err == nil {
		releaseLock(lockFile)
		return false, nil
	} else if errors.Is(err, filelock.ErrBusy) {
		return true, nil
	} else {
		return false, fmt.Errorf("inspect watcher lock: %w", err)
	}
}

// Context-aware acquisition stops a takeover wait without changing lock authority.
func AcquireContext(ctx context.Context, homeDir string, takeover bool) (*Ownership, error) {
	return acquireContext(ctx, homeDir, takeover, OwnerKindWatch)
}

// AcquireBridgeContext acquires ownership for an in-process supervision
// bridge cycle (waitOwned). It never requests takeover, and it records
// itself as a bridge holder so a contended refusal names it correctly.
func AcquireBridgeContext(ctx context.Context, homeDir string) (*Ownership, error) {
	return acquireContext(ctx, homeDir, false, OwnerKindBridge)
}

func acquireContext(ctx context.Context, homeDir string, takeover bool, kind string) (*Ownership, error) {
	if err := acquisitionContextError(ctx); err != nil {
		return nil, err
	}
	home := canonicalHome(homeDir)
	if err := os.MkdirAll(stateDir(home), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	lockPath := OwnerPath(home) + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", lockPath, err)
	}

	if err := lockOwner(lockFile); err != nil {
		if !errors.Is(err, filelock.ErrBusy) {
			_ = lockFile.Close()
			return nil, fmt.Errorf("lock %s: %w", lockPath, err)
		}
		if err := contend(ctx, lockFile, home, takeover); err != nil {
			_ = lockFile.Close()
			return nil, err
		}
	}

	afterLockAcquired()
	ownership, err := publishNewOwner(home, lockFile, kind)
	if err != nil {
		_ = lockFile.Close()
		return nil, err
	}
	if err := acquisitionContextError(ctx); err != nil {
		ownership.Release()
		return nil, err
	}
	return ownership, nil
}

// Runs the new-owner publication protocol once while holding the authoritative
// lock: stale record out, fresh generation, endpoint ready, advisory pid, then
// the coherent watch.owner record LAST as the readiness barrier.
func publishNewOwner(home string, lockFile *os.File, kind string) (*Ownership, error) {
	if err := clearRoutingRecord(home); err != nil {
		releaseLock(lockFile)
		return nil, err
	}

	gen, err := newGeneration()
	if err != nil {
		releaseLock(lockFile)
		return nil, err
	}

	endpoint, err := newTakeoverEndpoint(home, gen)
	if err != nil {
		releaseLock(lockFile)
		return nil, err
	}

	if err := publishPID(home, os.Getpid()); err != nil {
		endpoint.Close()
		releaseLock(lockFile)
		return nil, err
	}

	record := OwnerRecord{Version: ownerRecordVersion, Generation: gen, PID: os.Getpid(), Kind: kind}
	if err := publishOwnerRecord(home, record); err != nil {
		endpoint.Close()
		_ = clearPID(home)
		releaseLock(lockFile)
		return nil, err
	}

	return &Ownership{home: home, lockFile: lockFile, endpoint: endpoint, record: record}, nil
}

func publishPID(home string, pid int) error {
	data := []byte(strconv.Itoa(pid) + "\n")
	if err := atomicfileWrite(OwnerPath(home), data); err != nil {
		return fmt.Errorf("record watcher pid in %s: %w", OwnerPath(home), err)
	}
	return nil
}

// Leaves the advisory pid file present but empty, so an operator reading it on
// an unwatched home finds nothing rather than a dead process's number.
func clearPID(home string) error {
	if err := atomicfileWrite(OwnerPath(home), nil); err != nil {
		return fmt.Errorf("clear %s: %w", OwnerPath(home), err)
	}
	return nil
}

// Removes any old watch.owner publication while holding the authoritative lock,
// so a stale generation cannot route a takeover at a stale incumbent during the
// publication window.
func clearRoutingRecord(home string) error {
	if err := os.Remove(OwnerRecordPath(home)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale owner record: %w", err)
	}
	return nil
}

// Release shuts ownership down in the order that keeps stale routing metadata
// non-destructive: routing record unpublished and endpoint closed while the
// authoritative lock is still held, pid cleared, then the lock released last.
func (o *Ownership) Release() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		_ = clearRoutingRecord(o.home)
		o.endpoint.Close()
		_ = clearPID(o.home)
		releaseLock(o.lockFile)
	})
}

// TakeoverRequested returns the one-shot incumbent shutdown notification, set
// only by a valid current-generation request through the handmade endpoint.
func (o *Ownership) TakeoverRequested() <-chan struct{} {
	if o == nil || o.endpoint == nil {
		return nil
	}
	return o.endpoint.Requested()
}

// Generation is the ownership generation this watcher published, for
// diagnostics and tests.
func (o *Ownership) Generation() string {
	if o == nil {
		return ""
	}
	return o.record.Generation
}

// PID is the advisory pid published for operator diagnostics, never authority.
func (o *Ownership) PID() int {
	if o == nil {
		return 0
	}
	return o.record.PID
}

// Handles lock contention, naming the recorded holder either way: an
// immediate refusal without --takeover, or - with it - a fast failure
// against a recorded bridge holder, else the usual generation-based wait.
func contend(ctx context.Context, lockFile *os.File, home string, takeover bool) error {
	if err := acquisitionContextError(ctx); err != nil {
		return err
	}
	if !takeover {
		return refusalError(home)
	}
	if kind, desc := describeHolder(home); kind == holderBridge {
		return fmt.Errorf("%w - %s; --takeover cannot succeed against it because it never observes a takeover request - wait for its current cycle to end and retry without --takeover",
			ErrAttached, desc)
	}

	deadline := time.Now().Add(takeoverGrace)
	requested := make(map[string]bool)
	requestCurrent(home, requested)

	for {
		if err := acquisitionContextError(ctx); err != nil {
			return err
		}
		err := lockOwner(lockFile)
		if err == nil {
			return nil
		}
		if !errors.Is(err, filelock.ErrBusy) {
			return fmt.Errorf("lock %s: %w", lockFile.Name(), err)
		}
		requestCurrent(home, requested)
		if time.Now().After(deadline) {
			return timeoutError(home)
		}
		timer := time.NewTimer(takeoverPoll)
		select {
		case <-ctx.Done():
			if err := acquisitionContextError(ctx); err != nil {
				_ = timer.Stop()
				return err
			}
		case <-timer.C:
		}
	}
}

// Classifies a contended lock's recorded holder: what a refusal should say,
// and whether --takeover is a treatment that can actually work against it.
type holderKind int

const (
	// The busy lock's owner record is absent or malformed: nothing here
	// names a holder, so nothing is guessed.
	holderRecordUnreadable holderKind = iota
	// A readable record whose Kind is watch-shaped or not recorded (legacy
	// or unrecognized) - treated alike since neither is provably a bridge.
	holderOther
	// A readable record naming an in-process supervision bridge cycle,
	// which never observes a takeover request.
	holderBridge
)

// Reads the durable owner record and turns it into the refusal's factual
// core: what to say about who holds the lock. Never asserts an identity
// the record does not carry.
func describeHolder(home string) (holderKind, string) {
	holder, err := readOwnerRecord(home)
	if err != nil {
		return holderRecordUnreadable, "the fleet-home watcher lock is held, but its ownership record could not be read to identify the holder"
	}
	if holder.Kind == OwnerKindBridge {
		return holderBridge, fmt.Sprintf("pid %d holds the fleet-home watcher lock as a background supervision-wait cycle (generation %s), not an interactive watcher",
			holder.PID, holder.Generation)
	}
	return holderOther, fmt.Sprintf("pid %d already holds the fleet-home watcher lock (generation %s)", holder.PID, holder.Generation)
}

// Composes the immediate (non-takeover) contention refusal, matching the
// treatment to the holder read: transient for a bridge, no guess for an
// unreadable record, the ordinary remedy otherwise.
func refusalError(home string) error {
	kind, desc := describeHolder(home)
	switch kind {
	case holderBridge:
		return fmt.Errorf("%w - %s; it is transient and never honors --takeover - wait for its current cycle to end and retry",
			ErrAttached, desc)
	case holderOther:
		return fmt.Errorf("%w - %s; stop it through its owning session, or use --takeover for cooperative replacement",
			ErrAttached, desc)
	default:
		return fmt.Errorf("%w - %s; wait for it to release the lock and retry", ErrAttached, desc)
	}
}

// Composes the refusal for a --takeover contender that never observed the
// kernel lock release within takeoverGrace, naming whatever the owner
// record shows for the holder at that moment.
func timeoutError(home string) error {
	kind, desc := describeHolder(home)
	switch kind {
	case holderBridge:
		return fmt.Errorf("%w - the fleet-home lock did not release within %s after --takeover; %s and it never observes a takeover request - wait for its current cycle to end and retry without --takeover",
			ErrAttached, takeoverGrace, desc)
	case holderOther:
		return fmt.Errorf("%w - the fleet-home lock did not release within %s after --takeover; %s and did not step aside - stop it through its owning session and retry",
			ErrAttached, takeoverGrace, desc)
	default:
		return fmt.Errorf("%w - the fleet-home lock did not release within %s after --takeover, and %s; wait and retry",
			ErrAttached, takeoverGrace, desc)
	}
}

func acquisitionContextError(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	return cancellationError(ctx)
}

// Asks the currently-published owner generation to step aside, exactly once per
// observed generation. A malformed or absent record performs no action; there is
// never a pid-based fallback.
func requestCurrent(home string, requested map[string]bool) {
	rec, err := readOwnerRecord(home)
	if err != nil {
		return
	}
	if rec.PID == os.Getpid() || requested[rec.Generation] {
		return
	}
	if rErr := requestTakeover(home, rec.Generation); rErr != nil {
		// Endpoint-gone or stale-generation here is not proof of ownership: the
		// contender keeps waiting on the kernel lock, which is the only authority.
		return
	}
	requested[rec.Generation] = true
}

// Returns the coherent current routing record, or an error for any malformed,
// partial, or unsupported value - none of which is actionable.
func readOwnerRecord(home string) (OwnerRecord, error) {
	data, err := os.ReadFile(OwnerRecordPath(home))
	if err != nil {
		return OwnerRecord{}, err
	}
	return parseOwnerRecord(data)
}

func lockOwner(file *os.File) error {
	return filelock.Lock(file, false)
}

func releaseLock(file *os.File) {
	_ = filelock.Unlock(file)
	_ = file.Close()
}
