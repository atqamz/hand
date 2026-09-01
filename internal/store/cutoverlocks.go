package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/atqamz/hand/internal/filelock"
)

var errLegacyV18CutoverLocksUnsafe = errors.New("legacy v18 cutover Fleet-local lock graph is not quiescent")

type legacyV18CutoverHeldLock struct {
	path string
	file *os.File
	info os.FileInfo
}

type legacyV18CutoverLocks struct {
	held []*legacyV18CutoverHeldLock
}

func acquireLegacyV18CutoverLocks(ctx context.Context, homeDir string, gate *legacyV18CutoverGate) (*legacyV18CutoverLocks, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if gate == nil || gate.conn == nil || gate.releaseMigration == nil {
		return nil, fmt.Errorf("%w: 5A2 EXCLUSIVE source gate is not held", errLegacyV18CutoverLocksUnsafe)
	}

	var queryOnly int
	if err := gate.conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		return nil, fmt.Errorf("inspect legacy v18 cutover EXCLUSIVE query_only: %w", err)
	}
	if queryOnly != 1 {
		return nil, fmt.Errorf("%w: EXCLUSIVE source gate query_only = %d, want 1", errLegacyV18CutoverLocksUnsafe, queryOnly)
	}

	logicalKeys, err := legacyV18CutoverLogicalLockKeys(sqliteConnQueryer{ctx: ctx, conn: gate.conn})
	if err != nil {
		return nil, err
	}

	locks := &legacyV18CutoverLocks{}
	keep := false
	defer func() {
		if !keep {
			_ = locks.Close()
		}
	}()

	ownedHashed := map[string]struct{}{
		legacyV18CutoverHashedLockPath(homeDir, MigrationLock): {},
	}
	for _, key := range logicalKeys {
		path := legacyV18CutoverHashedLockPath(homeDir, key)
		held, err := acquireLegacyV18CutoverPathLock(path, 0o600)
		if err != nil {
			return nil, fmt.Errorf("%w: acquire logical lock %q: %v", errLegacyV18CutoverLocksUnsafe, key, err)
		}
		locks.held = append(locks.held, held)
		ownedHashed[path] = struct{}{}
	}

	watchPath := filepath.Join(Dir(homeDir), "watch.pid.lock")
	watchLock, err := acquireLegacyV18CutoverPathLock(watchPath, 0o644)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire watcher ownership lock: %v", errLegacyV18CutoverLocksUnsafe, err)
	}
	locks.held = append(locks.held, watchLock)

	registryPath := filepath.Join(homeDir, "data", "projects.md.lock")
	if err := ensureLegacyV18CutoverLockParent(registryPath); err != nil {
		return nil, fmt.Errorf("%w: prepare project registry lock: %v", errLegacyV18CutoverLocksUnsafe, err)
	}
	registryLock, err := acquireLegacyV18CutoverPathLock(registryPath, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire project registry lock: %v", errLegacyV18CutoverLocksUnsafe, err)
	}
	locks.held = append(locks.held, registryLock)

	migrationPath := legacyV18CutoverHashedLockPath(homeDir, MigrationLock)
	if err := validateLegacyV18CutoverRendezvousPath(migrationPath); err != nil {
		return nil, fmt.Errorf("%w: validate held MigrationLock pathname: %v", errLegacyV18CutoverLocksUnsafe, err)
	}

	before, err := legacyV18CutoverHashedLockNames(Dir(homeDir))
	if err != nil {
		return nil, fmt.Errorf("%w: enumerate hashed lock namespace: %v", errLegacyV18CutoverLocksUnsafe, err)
	}
	for _, name := range before {
		path := filepath.Join(Dir(homeDir), name)
		if _, alreadyHeld := ownedHashed[path]; alreadyHeld {
			continue
		}
		held, err := acquireLegacyV18CutoverPathLock(path, 0o600)
		if err != nil {
			return nil, fmt.Errorf("%w: acquire existing hashed rendezvous %s: %v", errLegacyV18CutoverLocksUnsafe, name, err)
		}
		locks.held = append(locks.held, held)
		ownedHashed[path] = struct{}{}
	}

	after, err := legacyV18CutoverHashedLockNames(Dir(homeDir))
	if err != nil {
		return nil, fmt.Errorf("%w: re-enumerate hashed lock namespace: %v", errLegacyV18CutoverLocksUnsafe, err)
	}
	if !equalLegacyV18CutoverLockNames(before, after) {
		return nil, fmt.Errorf("%w: hashed lock namespace changed during quiescence proof: before=%v after=%v", errLegacyV18CutoverLocksUnsafe, before, after)
	}
	for _, held := range locks.held {
		if err := held.verifyPathIdentity(); err != nil {
			return nil, fmt.Errorf("%w: %v", errLegacyV18CutoverLocksUnsafe, err)
		}
	}
	if err := validateLegacyV18CutoverRendezvousPath(migrationPath); err != nil {
		return nil, fmt.Errorf("%w: revalidate held MigrationLock pathname: %v", errLegacyV18CutoverLocksUnsafe, err)
	}

	keep = true
	return locks, nil
}

func legacyV18CutoverLogicalLockKeys(q sqliteQueryer) ([]string, error) {
	keys := map[string]struct{}{
		"config:routing": {},
		"completions":    {},
		SchemaLock:        {},
	}

	taskRows, err := q.Query(`SELECT id, project FROM task ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read legacy v18 task lock identities: %w", err)
	}
	for taskRows.Next() {
		var id, project string
		if err := taskRows.Scan(&id, &project); err != nil {
			_ = taskRows.Close()
			return nil, fmt.Errorf("read legacy v18 task lock identities: %w", err)
		}
		keys["task:"+id] = struct{}{}
		keys["send:"+id] = struct{}{}
		if project != "" {
			keys["project:"+project] = struct{}{}
		}
	}
	if err := taskRows.Err(); err != nil {
		_ = taskRows.Close()
		return nil, fmt.Errorf("read legacy v18 task lock identities: %w", err)
	}
	if err := taskRows.Close(); err != nil {
		return nil, fmt.Errorf("close legacy v18 task lock identities: %w", err)
	}

	projectRows, err := q.Query(`SELECT name FROM project ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("read legacy v18 project lock identities: %w", err)
	}
	for projectRows.Next() {
		var name string
		if err := projectRows.Scan(&name); err != nil {
			_ = projectRows.Close()
			return nil, fmt.Errorf("read legacy v18 project lock identities: %w", err)
		}
		keys["project:"+name] = struct{}{}
	}
	if err := projectRows.Err(); err != nil {
		_ = projectRows.Close()
		return nil, fmt.Errorf("read legacy v18 project lock identities: %w", err)
	}
	if err := projectRows.Close(); err != nil {
		return nil, fmt.Errorf("close legacy v18 project lock identities: %w", err)
	}

	worktreeRows, err := q.Query(`SELECT DISTINCT worktree FROM attempt WHERE worktree <> '' ORDER BY worktree`)
	if err != nil {
		return nil, fmt.Errorf("read legacy v18 worktree lock identities: %w", err)
	}
	for worktreeRows.Next() {
		var path string
		if err := worktreeRows.Scan(&path); err != nil {
			_ = worktreeRows.Close()
			return nil, fmt.Errorf("read legacy v18 worktree lock identities: %w", err)
		}
		keys["worktree:"+path] = struct{}{}
	}
	if err := worktreeRows.Err(); err != nil {
		_ = worktreeRows.Close()
		return nil, fmt.Errorf("read legacy v18 worktree lock identities: %w", err)
	}
	if err := worktreeRows.Close(); err != nil {
		return nil, fmt.Errorf("close legacy v18 worktree lock identities: %w", err)
	}

	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out, nil
}

func legacyV18CutoverHashedLockPath(homeDir, key string) string {
	return filepath.Join(Dir(homeDir), fmt.Sprintf(".%x.lock", sha256.Sum256([]byte(key))))
}

func legacyV18CutoverHashedLockNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if isLegacyV18HashedLockName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func isLegacyV18HashedLockName(name string) bool {
	if len(name) != 70 || !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".lock") {
		return false
	}
	encoded := name[1 : len(name)-len(".lock")]
	if len(encoded) != 64 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func equalLegacyV18CutoverLockNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func ensureLegacyV18CutoverLockParent(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("lock parent %s is not a direct directory", parent)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(parent, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	info, err = os.Lstat(parent)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("lock parent %s is not a direct directory", parent)
	}
	return nil
}

func acquireLegacyV18CutoverPathLock(path string, perm os.FileMode) (*legacyV18CutoverHeldLock, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("rendezvous %s is not a direct regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect rendezvous %s: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, perm)
	if err != nil {
		return nil, fmt.Errorf("open rendezvous %s: %w", path, err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()

	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened rendezvous %s: %w", path, err)
	}
	if !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("opened rendezvous %s is not a regular file", path)
	}
	if err := verifyLegacyV18CutoverPathIdentity(path, opened); err != nil {
		return nil, err
	}
	if err := filelock.Lock(file, false); err != nil {
		return nil, fmt.Errorf("lock rendezvous %s: %w", path, err)
	}
	if err := verifyLegacyV18CutoverPathIdentity(path, opened); err != nil {
		_ = filelock.Unlock(file)
		return nil, err
	}

	closeOnError = false
	return &legacyV18CutoverHeldLock{path: path, file: file, info: opened}, nil
}

func verifyLegacyV18CutoverPathIdentity(path string, opened os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect rendezvous %s: %w", path, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return fmt.Errorf("rendezvous %s changed away from a direct regular file", path)
	}
	if !os.SameFile(opened, current) {
		return fmt.Errorf("rendezvous %s changed identity while being locked", path)
	}
	return nil
}

func validateLegacyV18CutoverRendezvousPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("rendezvous %s is not a direct regular file", path)
	}
	return nil
}

func (h *legacyV18CutoverHeldLock) verifyPathIdentity() error {
	if h == nil || h.file == nil || h.info == nil {
		return fmt.Errorf("cutover lock holder is not live")
	}
	return verifyLegacyV18CutoverPathIdentity(h.path, h.info)
}

func (l *legacyV18CutoverLocks) Close() error {
	if l == nil {
		return nil
	}
	var firstErr error
	for i := len(l.held) - 1; i >= 0; i-- {
		held := l.held[i]
		if held == nil || held.file == nil {
			continue
		}
		if err := filelock.Unlock(held.file); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("unlock legacy v18 cutover rendezvous %s: %w", held.path, err)
		}
		if err := held.file.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close legacy v18 cutover rendezvous %s: %w", held.path, err)
		}
		held.file = nil
	}
	l.held = nil
	return firstErr
}
