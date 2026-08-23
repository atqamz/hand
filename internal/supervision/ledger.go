package supervision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/filelock"
)

const (
	ledgerSchema     = "hand.supervision.wake.v1"
	ledgerRel        = "state/runtime/supervision-wake.json"
	ledgerLockSuffix = ".lock"

	// MaxLedgerAge bounds one episode's mechanism memory; entries older than it
	// are pruned whatever their stage. The ledger is disposable evidence, not
	// historical product truth.
	MaxLedgerAge = 24 * time.Hour
	// MaxLedgerEntries caps storm memory; pruning drops the least recently
	// seen entries first.
	MaxLedgerEntries = 512
	// BridgeFailureCooldown bounds how often a deterministic bridge failure
	// surfaces a notice, so a broken mechanism never becomes a per-turn loop.
	BridgeFailureCooldown = time.Hour
)

// Bounded retry windows keeping one unchanged episode from unlimited wake
// attempts while still recovering a wake whose delivery or progress never
// arrived. Neither timeout proves failure; both only allow one more attempt.
type Policy struct {
	// DeliveryTimeout is how long after delivery was requested a bridge has to
	// accept it before another request may be made.
	DeliveryTimeout time.Duration
	// ProgressTimeout is how long after host acceptance `hand orient` progress
	// is awaited before a retry may be requested.
	ProgressTimeout time.Duration
}

func DefaultPolicy() Policy {
	return Policy{DeliveryTimeout: 2 * time.Minute, ProgressTimeout: 15 * time.Minute}
}

type episodeRecord struct {
	Key               string     `json:"key"`
	FirstSeen         time.Time  `json:"first_seen"`
	SeenAt            time.Time  `json:"seen_at"`
	DeliveryRequested *time.Time `json:"delivery_requested,omitempty"`
	HostAccepted      *time.Time `json:"host_accepted,omitempty"`
	Oriented          *time.Time `json:"oriented,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	LastErrorAt       *time.Time `json:"last_error_at,omitempty"`
}

type ledgerFile struct {
	Schema   string                    `json:"schema"`
	Episodes map[string]*episodeRecord `json:"episodes"`
}

// The disposable mechanism store behind wake dedupe and progress
// classification, under state/runtime/ and never canonical hand.db: deleting
// or corrupting it causes bounded duplicate wakes, never workflow-truth harm.
type Ledger struct {
	path   string
	policy Policy
	now    func() time.Time
}

// OpenLedger opens (lazily creating) the fleet home's wake ledger.
func OpenLedger(home string) *Ledger {
	return &Ledger{path: filepath.Join(home, ledgerRel), policy: DefaultPolicy(), now: time.Now}
}

func (l *Ledger) lockPath() string { return l.path + ledgerLockSuffix }

// Atomically decides which current episodes may wake and stamps their delivery
// request in one locked transaction: two waiters can never both claim the same
// exact episode. Transaction faults return as errors, never empty claims.
func (l *Ledger) ClaimEligible(episodes []Episode) ([]Episode, error) {
	var claimed []Episode
	err := l.update(func(file *ledgerFile, now time.Time) {
		for _, episode := range episodes {
			record, exists := file.Episodes[episode.Key()]
			if !exists || l.eligibleRecord(record) {
				stamp := now
				if !exists {
					record = &episodeRecord{Key: episode.Key(), FirstSeen: now}
					file.Episodes[episode.Key()] = record
				}
				record.SeenAt = now
				record.DeliveryRequested = &stamp
				record.HostAccepted = nil
				record.Oriented = nil
				claimed = append(claimed, episode)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// Eligible filters current episodes down to the ones a wake may be requested
// for now. Read-only: claiming goes through ClaimEligible so the stamp lands
// atomically.
func (l *Ledger) Eligible(episodes []Episode) []Episode {
	file := l.read()
	var eligible []Episode
	for _, episode := range episodes {
		record, exists := file.Episodes[episode.Key()]
		if !exists || l.eligibleRecord(record) {
			eligible = append(eligible, episode)
		}
	}
	return eligible
}

func (l *Ledger) eligibleRecord(record *episodeRecord) bool {
	if record.Oriented != nil {
		return false
	}
	now := l.now()
	if record.HostAccepted != nil {
		return now.Sub(*record.HostAccepted) >= l.policy.ProgressTimeout
	}
	if record.DeliveryRequested != nil {
		return now.Sub(*record.DeliveryRequested) >= l.policy.DeliveryTimeout
	}
	return true
}

// MarkRequested records that a wake for these episodes was handed to a host
// bridge, so repeated observation of the same episode does not spam parallel
// wake attempts.
func (l *Ledger) MarkRequested(episodes []Episode) error {
	return l.update(func(file *ledgerFile, now time.Time) {
		for _, episode := range episodes {
			record := ensureRecord(file, episode.Key(), now)
			stamp := now
			record.DeliveryRequested = &stamp
			record.HostAccepted = nil
			record.Oriented = nil
		}
	})
}

// MarkAccepted records host-side acceptance (queue enqueued, prompt submitted,
// rewake armed). Acceptance alone is never reasoning progress: only orient is.
func (l *Ledger) MarkAccepted(keys []string) error {
	return l.update(func(file *ledgerFile, now time.Time) {
		for _, key := range keys {
			record := ensureKey(file, key, now)
			stamp := now
			record.HostAccepted = &stamp
		}
	})
}

// MarkOriented records that a Supervisor reasoning turn re-entered and ran
// `hand orient` while these exact episodes were current. This is the strongest
// progress witness and what stops an unchanged condition from waking forever.
func (l *Ledger) MarkOriented(episodes []Episode) error {
	return l.update(func(file *ledgerFile, now time.Time) {
		for _, episode := range episodes {
			record := ensureRecord(file, episode.Key(), now)
			stamp := now
			record.Oriented = &stamp
		}
	})
}

// MarkError records bounded mechanism failure evidence against episodes, so a
// bridge can avoid turning a deterministic failure into a per-turn loop.
func (l *Ledger) MarkError(episodes []Episode, reason string) error {
	return l.update(func(file *ledgerFile, now time.Time) {
		for _, episode := range episodes {
			record := ensureRecord(file, episode.Key(), now)
			record.LastError = reason
			stamp := now
			record.LastErrorAt = &stamp
		}
	})
}

// LastErrorBefore reports whether every named episode's last recorded error is
// older than window, so a failing bridge may surface one bounded failure
// notice instead of one per turn end. It is true when nothing errored yet.
func (l *Ledger) LastErrorBefore(keys []string, window time.Duration) bool {
	file := l.read()
	cutoff := l.now().Add(-window)
	for _, key := range keys {
		record, exists := file.Episodes[key]
		if !exists || record.LastErrorAt == nil || record.LastErrorAt.Before(cutoff) {
			return true
		}
	}
	return false
}

// MarkErrorByKeys records bounded mechanism failure evidence against claimed
// episode keys, so a failed host delivery can retry boundedly later without
// fabricating Episode values it never held.
func (l *Ledger) MarkErrorByKeys(keys []string, reason string) error {
	return l.update(func(file *ledgerFile, now time.Time) {
		for _, key := range keys {
			record := ensureKey(file, key, now)
			record.LastError = reason
			stamp := now
			record.LastErrorAt = &stamp
		}
	})
}

// BridgeErrorKey namespaces a host bridge's own failure evidence, which is
// not attached to any episode.
func BridgeErrorKey(host string) string {
	return "bridge:" + host
}

// MarkBridgeError stamps one bridge-level mechanism failure (arm failed,
// delivery path broken) without touching any episode record.
func (l *Ledger) MarkBridgeError(host string, reason string) error {
	return l.update(func(f *ledgerFile, now time.Time) {
		record := ensureKey(f, BridgeErrorKey(host), now)
		record.LastError = reason
		stamp := now
		record.LastErrorAt = &stamp
	})
}

// BridgeErroredBefore reports whether the bridge's last recorded failure for
// host is older than window, so a deterministic failure surfaces one bounded
// notice instead of one per turn end. True when nothing has errored yet.
func (l *Ledger) BridgeErroredBefore(host string, window time.Duration) bool {
	return l.LastErrorBefore([]string{BridgeErrorKey(host)}, window)
}

// Progress reports the latest acceptance and orientation stamps across the
// ledger, for the typed diagnostics that must distinguish "a wake was
// accepted" from "a Supervisor actually re-entered and oriented".
func (l *Ledger) Progress() (lastAccepted, lastOriented *time.Time) {
	file := l.read()
	for _, record := range file.Episodes {
		if record.HostAccepted != nil && (lastAccepted == nil || record.HostAccepted.After(*lastAccepted)) {
			stamp := *record.HostAccepted
			lastAccepted = &stamp
		}
		if record.Oriented != nil && (lastOriented == nil || record.Oriented.After(*lastOriented)) {
			stamp := *record.Oriented
			lastOriented = &stamp
		}
	}
	return lastAccepted, lastOriented
}

func ensureRecord(file *ledgerFile, key string, now time.Time) *episodeRecord {
	record, exists := file.Episodes[key]
	if !exists {
		record = &episodeRecord{Key: key, FirstSeen: now}
		file.Episodes[key] = record
	}
	record.SeenAt = now
	return record
}

func ensureKey(file *ledgerFile, key string, now time.Time) *episodeRecord {
	record, exists := file.Episodes[key]
	if !exists {
		record = &episodeRecord{Key: key, FirstSeen: now}
		file.Episodes[key] = record
	}
	return record
}

// Same advisory lock as writers before touching the file: on Windows an atomic
// replacement fails while any reader holds the target open, so unlocked reads
// turn every write into a rename race.
func (l *Ledger) read() ledgerFile {
	file := ledgerFile{Schema: ledgerSchema, Episodes: map[string]*episodeRecord{}}
	lock, err := os.OpenFile(l.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return file
	}
	defer func() { _ = lock.Close() }()
	if err := filelock.Lock(lock, true); err != nil {
		return file
	}
	defer func() { _ = filelock.Unlock(lock) }()

	data, err := os.ReadFile(l.path)
	if err != nil {
		return file
	}
	return l.parse(data)
}

// Decodes already-read ledger bytes; locked callers use this to avoid
// re-entering the lock.
func (l *Ledger) parse(data []byte) ledgerFile {
	var parsed ledgerFile
	if json.Unmarshal(data, &parsed) != nil || parsed.Schema != ledgerSchema || parsed.Episodes == nil {
		// Corruption is the disposable case: bounded duplicate wakes replace
		// canonical-truth risk, and the next write replaces the file wholesale.
		return ledgerFile{Schema: ledgerSchema, Episodes: map[string]*episodeRecord{}}
	}
	now := l.now()
	for key, record := range parsed.Episodes {
		reference := record.SeenAt
		if reference.IsZero() {
			reference = record.FirstSeen
		}
		if now.Sub(reference) > MaxLedgerAge {
			delete(parsed.Episodes, key)
		}
	}
	return parsed
}

// Mutates the ledger under its advisory lock and replaces the file atomically,
// then prunes to the age and count bounds.
func (l *Ledger) update(mutate func(*ledgerFile, time.Time)) error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(ledgerRel), err)
	}
	lock, err := os.OpenFile(l.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", ledgerRel+ledgerLockSuffix, err)
	}
	defer func() { _ = lock.Close() }()
	if err := filelock.Lock(lock, true); err != nil {
		return fmt.Errorf("lock %s: %w", ledgerRel+ledgerLockSuffix, err)
	}
	defer func() { _ = filelock.Unlock(lock) }()

	now := l.now()
	data, readErr := os.ReadFile(l.path)
	var file ledgerFile
	if readErr == nil {
		file = l.parse(data)
	} else {
		file = ledgerFile{Schema: ledgerSchema, Episodes: map[string]*episodeRecord{}}
	}
	mutate(&file, now)
	prune(file.Episodes, now)
	file.Schema = ledgerSchema

	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(l.path, ".supervision-wake-", append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", ledgerRel, err)
	}
	return nil
}

func prune(episodes map[string]*episodeRecord, now time.Time) {
	for key, record := range episodes {
		reference := record.SeenAt
		if reference.IsZero() {
			reference = record.FirstSeen
		}
		if now.Sub(reference) > MaxLedgerAge {
			delete(episodes, key)
		}
	}
	if len(episodes) <= MaxLedgerEntries {
		return
	}
	keys := make([]string, 0, len(episodes))
	for key := range episodes {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := episodes[keys[i]], episodes[keys[j]]
		return left.SeenAt.Before(right.SeenAt)
	})
	for _, key := range keys[:len(episodes)-MaxLedgerEntries] {
		delete(episodes, key)
	}
}
