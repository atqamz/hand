package store

import (
	"context"
	"errors"
)

// LegacyV18CutoverProjectObservation is one registered legacy Project that provider quiescence must verify.
type LegacyV18CutoverProjectObservation struct {
	ProjectID string
	Name      string
	URL       string
	Mode      string
	Upstream  string
	ClonePath string
}

// LegacyV18CutoverWorktreeObservation is one terminal legacy worktree whose provider state must be resolved.
type LegacyV18CutoverWorktreeObservation struct {
	TaskID        string
	AttemptID     int64
	ProjectID     string
	ProjectName   string
	ClonePath     string
	WorktreePath  string
	LeaseID       string
	TeardownState string
}

// LegacyV18CutoverHerdrObservation is one terminal legacy Herdr binding whose provider state must be resolved.
type LegacyV18CutoverHerdrObservation struct {
	TaskID        string
	AttemptID     int64
	ProjectID     string
	ProjectName   string
	Session       string
	WorkspaceID   string
	TabID         string
	PaneID        string
	TeardownState string
}

// LegacyV18CutoverObservationPlan is the immutable provider-observation input derived under the held source gate.
type LegacyV18CutoverObservationPlan struct {
	FleetID   string
	Projects  []LegacyV18CutoverProjectObservation
	Worktrees []LegacyV18CutoverWorktreeObservation
	Herdr     []LegacyV18CutoverHerdrObservation
}

// LegacyV18CutoverGuard holds the source EXCLUSIVE gate and Fleet-local lock closure while providers are observed.
type LegacyV18CutoverGuard struct {
	gate  *legacyV18CutoverGate
	locks *legacyV18CutoverLocks
	plan  LegacyV18CutoverObservationPlan
}

// AcquireLegacyV18CutoverGuard acquires the landed 5A2/5A3 source guards and derives the durable observation plan.
func AcquireLegacyV18CutoverGuard(ctx context.Context, homeDir string) (*LegacyV18CutoverGuard, error) {
	gate, err := acquireLegacyV18CutoverGate(ctx, homeDir)
	if err != nil {
		return nil, err
	}
	keepGate := false
	defer func() {
		if !keepGate {
			_ = gate.Close()
		}
	}()

	locks, err := acquireLegacyV18CutoverLocks(ctx, homeDir, gate)
	if err != nil {
		return nil, err
	}
	keepLocks := false
	defer func() {
		if !keepLocks {
			_ = locks.Close()
		}
	}()

	plan, err := planLegacyV18CutoverObservations(ctx, homeDir, gate, locks)
	if err != nil {
		return nil, err
	}
	keepGate = true
	keepLocks = true
	return &LegacyV18CutoverGuard{gate: gate, locks: locks, plan: exportLegacyV18CutoverObservationPlan(plan)}, nil
}

// ObservationPlan returns a copy so a provider observer cannot mutate the guard's source-derived evidence.
func (g *LegacyV18CutoverGuard) ObservationPlan() LegacyV18CutoverObservationPlan {
	if g == nil {
		return LegacyV18CutoverObservationPlan{}
	}
	plan := g.plan
	plan.Projects = append([]LegacyV18CutoverProjectObservation(nil), g.plan.Projects...)
	plan.Worktrees = append([]LegacyV18CutoverWorktreeObservation(nil), g.plan.Worktrees...)
	plan.Herdr = append([]LegacyV18CutoverHerdrObservation(nil), g.plan.Herdr...)
	return plan
}

// Close releases Fleet-local locks before the EXCLUSIVE source gate and MigrationLock.
func (g *LegacyV18CutoverGuard) Close() error {
	if g == nil {
		return nil
	}
	var errs []error
	if g.locks != nil {
		errs = append(errs, g.locks.Close())
		g.locks = nil
	}
	if g.gate != nil {
		errs = append(errs, g.gate.Close())
		g.gate = nil
	}
	return errors.Join(errs...)
}

func exportLegacyV18CutoverObservationPlan(in legacyV18CutoverObservationPlan) LegacyV18CutoverObservationPlan {
	out := LegacyV18CutoverObservationPlan{FleetID: in.FleetID}
	out.Projects = make([]LegacyV18CutoverProjectObservation, 0, len(in.Projects))
	for _, project := range in.Projects {
		out.Projects = append(out.Projects, LegacyV18CutoverProjectObservation(project))
	}
	out.Worktrees = make([]LegacyV18CutoverWorktreeObservation, 0, len(in.Worktrees))
	for _, worktree := range in.Worktrees {
		out.Worktrees = append(out.Worktrees, LegacyV18CutoverWorktreeObservation(worktree))
	}
	out.Herdr = make([]LegacyV18CutoverHerdrObservation, 0, len(in.Herdr))
	for _, herdr := range in.Herdr {
		out.Herdr = append(out.Herdr, LegacyV18CutoverHerdrObservation(herdr))
	}
	return out
}
