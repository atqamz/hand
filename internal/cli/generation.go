package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
		r.generation = savedGeneration(root)
	}
}

func savedGeneration(root string) string {
	if root == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, generationFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readGeneration(root string) string {
	if g := savedGeneration(root); g != "" {
		return g
	}
	return "none"
}

func recordGeneration(env Env, root string) (string, func(), error) {
	path := filepath.Join(root, generationFile)
	unlock, err := lockPath(path + ".lock")
	if err != nil {
		return "", nil, err
	}
	defer unlock()
	pid := os.Getpid()
	generation := strconv.Itoa(pid)
	if marker, err := luvus.ProcStartMarker(pid); err == nil {
		generation += " " + marker
	} else {
		fmt.Fprintf(env.Stderr, "warning: cannot read the start marker of process %d: %v; board.generation records only its PID\n", pid, err)
	}
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

func generationAlive(generation string) bool {
	pidText, marker, marked := strings.Cut(generation, " ")
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return false
	}
	if !marked {
		return true
	}
	return rootAlive(pid, marker)
}

func (r *runner) checkGeneration(ctx context.Context, st *state.Store, sup state.Supervisor) (bool, error) {
	if r.generation == "" {
		return true, nil
	}
	root, err := r.rootDir()
	if err != nil {
		return false, err
	}
	current := readGeneration(root)
	if current == r.generation && generationAlive(r.generation) {
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
	if current == r.generation {
		detail = "held generation " + r.generation + " is no longer alive, requested by " + r.requester().String()
	}
	if err := st.NoteSupervisor(ctx, sup.ID, "refused", detail); err != nil {
		return false, err
	}
	return false, nil
}

func (r *runner) guardGeneration(ctx context.Context, st *state.Store, sup state.Supervisor) (func(), bool, error) {
	if r.generation == "" {
		return func() {}, true, nil
	}
	root, err := r.rootDir()
	if err != nil {
		return nil, false, err
	}
	unlock, err := lockPath(filepath.Join(root, generationFile+".lock"))
	if err != nil {
		return nil, false, err
	}
	ok, err := r.checkGeneration(ctx, st, sup)
	if err != nil || !ok {
		unlock()
		return func() {}, ok, err
	}
	return unlock, true, nil
}

func (r *runner) staleGeneration(sup state.Supervisor) error {
	root, _ := r.rootDir()
	return fmt.Errorf("%w: supervisor %s belongs to board generation %s, this process started under %s; nothing was closed", state.ErrConflict, state.SupervisorRef(sup.ID), readGeneration(root), r.generation)
}
