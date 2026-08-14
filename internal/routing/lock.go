package routing

import (
	"fmt"

	"github.com/atqamz/hand/internal/store"
)

// Routing reads and mutations share this advisory lock namespace.
// Runtime callers release it before taking task, project, worktree, or launch locks.
const ConfigLockName = "config:routing"

func Lock(home string) (func(), error) {
	release, err := store.Lock(home, ConfigLockName, false)
	if err != nil {
		return nil, fmt.Errorf("lock routing configuration: %w", err)
	}
	return release, nil
}

func withLock(home string, fn func() error) error {
	release, err := Lock(home)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}
