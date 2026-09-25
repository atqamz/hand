package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrLegacyV18CutoverGuardClosed means provider observation no longer owns the required source guards.
var ErrLegacyV18CutoverGuardClosed = errors.New("legacy v18 cutover guard is closed")

var ErrLegacyV18AutomaticCutoverUnavailable = errors.New("automatic v0.7.2 cutover is unavailable: a legacy project writer can mutate after source freeze")

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
	gate       *legacyV18CutoverGate
	locks      *legacyV18CutoverLocks
	plan       LegacyV18CutoverObservationPlan
	evidence   legacyV18CutoverBootEvidence
	sourceHeld bool
}

func AcquireLegacyV18CutoverGuard(_ context.Context, _ string) (*LegacyV18CutoverGuard, error) {
	return nil, ErrLegacyV18AutomaticCutoverUnavailable
}

// The evidence is the preflight read that Freeze commits as E_F.
func acquireLegacyV18CutoverGuardForFixture(ctx context.Context, homeDir string, evidence legacyV18CutoverBootEvidence) (*LegacyV18CutoverGuard, error) {
	if err := validateLegacyV18CutoverBootEvidence(evidence); err != nil {
		return nil, fmt.Errorf("acquire legacy v18 cutover guard: boot evidence: %w", err)
	}
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
	return &LegacyV18CutoverGuard{
		gate:       gate,
		locks:      locks,
		plan:       exportLegacyV18CutoverObservationPlan(plan),
		evidence:   evidence,
		sourceHeld: true,
	}, nil
}

// ObservationPlan returns a copy only while the source gate and Fleet-local lock closure remain held.
func (g *LegacyV18CutoverGuard) ObservationPlan() (LegacyV18CutoverObservationPlan, error) {
	if !g.held() {
		return LegacyV18CutoverObservationPlan{}, ErrLegacyV18CutoverGuardClosed
	}
	plan := g.plan
	plan.Projects = append([]LegacyV18CutoverProjectObservation(nil), g.plan.Projects...)
	plan.Worktrees = append([]LegacyV18CutoverWorktreeObservation(nil), g.plan.Worktrees...)
	plan.Herdr = append([]LegacyV18CutoverHerdrObservation(nil), g.plan.Herdr...)
	return plan, nil
}

// Close releases Fleet-local locks before the EXCLUSIVE source gate and MigrationLock.
func (g *LegacyV18CutoverGuard) Close() error {
	if g == nil {
		return nil
	}
	g.sourceHeld = false
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
