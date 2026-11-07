package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const generationFile = "board.generation"

type requester struct {
	caller string
	pid    int
	marker string
	luvus  string
}

func (q requester) String() string {
	caller := q.caller
	if caller == "" {
		caller = "unknown"
	}
	marker := q.marker
	if marker == "" {
		marker = "unknown"
	}
	s := fmt.Sprintf("%s pid %d start %s", caller, q.pid, marker)
	if q.luvus != "" {
		s += " luvus " + q.luvus
	}
	return s
}

func (q requester) note(reason string) string { return reason + "; requested by " + q.String() }

func (r *runner) requester() requester {
	q := requester{caller: r.caller, pid: os.Getpid(), marker: "unknown"}
	if m, err := luvus.ProcStartMarker(q.pid); err == nil {
		q.marker = m
	}
	return q
}

func (r *runner) recordGeneration() {
	if r.generation != "" {
		return
	}
	if root, err := r.rootDir(); err == nil {
		r.generation = readGeneration(root)
	}
}

func readGeneration(root string) string {
	b, err := os.ReadFile(filepath.Join(root, generationFile))
	if err != nil {
		return "none"
	}
	if g := strings.TrimSpace(string(b)); g != "" {
		return g
	}
	return "none"
}

func recordGeneration(root string) (string, func(), error) {
	path := filepath.Join(root, generationFile)
	unlock, err := lockPath(path + ".lock")
	if err != nil {
		return "", nil, err
	}
	defer unlock()
	pid := os.Getpid()
	marker, err := luvus.ProcStartMarker(pid)
	if err != nil {
		return "", nil, err
	}
	generation := fmt.Sprintf("%d %s", pid, marker)
	line := generation + "\n"
	if err := writeFile(root, path, line); err != nil {
		return "", nil, err
	}
	forget := func() {
		unlock, err := lockPath(path + ".lock")
		if err != nil {
			return
		}
		defer unlock()
		if b, err := os.ReadFile(path); err == nil && string(b) == line {
			_ = os.Remove(path)
		}
	}
	return generation, forget, nil
}

func (r *runner) checkGeneration(ctx context.Context, st *state.Store, sup state.Supervisor) (bool, error) {
	if r.generation == "" {
		return true, nil
	}
	current := readGeneration(r.root)
	if current == r.generation {
		return true, nil
	}
	r.genMu.Lock()
	already := r.refused == current
	r.refused = current
	r.genMu.Unlock()
	if already {
		return false, nil
	}
	detail := "held generation " + r.generation + ", current " + current + ", requested by " + r.requester().String()
	if err := st.NoteSupervisor(ctx, sup.ID, "refused", detail); err != nil {
		return false, err
	}
	return false, nil
}

func (r *runner) staleGeneration(sup state.Supervisor) error {
	return fmt.Errorf("%w: supervisor %s belongs to board generation %s, this process started under %s; nothing was closed", state.ErrConflict, state.SupervisorRef(sup.ID), readGeneration(r.root), r.generation)
}
