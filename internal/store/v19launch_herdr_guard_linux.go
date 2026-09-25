//go:build linux

package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/herdr"
)

const canonicalV19ExecGuardPoll = 50 * time.Millisecond

// One fresh Launch through `hand exec-guard`: Tx A commits B and V_B, the handoff alone
// carries S_B, and Tx B precedes the one typed invocation. Reachable only with deps.execGuard,
// which production leaves off, so the revision-1 refusal still governs every platform.
func launchCanonicalV19Herdr(
	ctx context.Context,
	homeDir string,
	input CanonicalV19LaunchPrepareInput,
	deps canonicalV19HerdrLaunchDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	hand, err := deps.hand()
	if err != nil || !filepath.IsAbs(hand) || strings.HasSuffix(hand, " (deleted)") {
		return "", fmt.Errorf("launch canonical v19 Herdr: resolve an absolute hand executable that still exists at %q: %v", hand, err)
	}
	current, prepareErr := prepareCanonicalV19ExecGuardLaunch(ctx, homeDir, input)
	if current.Current.State == "" {
		return "", prepareErr
	}
	abandon := func(cause error) (string, error) {
		state, err := reconcileCanonicalV19HerdrLaunch(ctx, homeDir, input.OperationID, deps)
		return state, errors.Join(fmt.Errorf("launch canonical v19 Herdr: %w", cause), err)
	}
	dir, err := canonicalV19ExecGuardDir(homeDir, input.OperationID)
	if err = errors.Join(prepareErr, err); err != nil {
		return abandon(err)
	}
	key, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil || key.SessionName != herdr.SessionName(current.FleetID) {
		return abandon(fmt.Errorf("provider Session key %q does not name this Fleet's Herdr session", current.ProviderSessionKey))
	}
	client := deps.clientFor(key.SessionName)
	if client == nil {
		return abandon(errors.New("provider client is unavailable"))
	}
	observed := observeCanonicalV19HerdrLaunch(ctx, current, client)
	if observed.State != canonicalV19HerdrLaunchReady {
		return abandon(fmt.Errorf("the Session pane is not ready for the exec guard: %s", observed.Reason))
	}
	submittedAt := canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt)
	if _, err := SubmitCanonicalV19Launch(ctx, homeDir, input.OperationID, submittedAt, observed.EvidenceDigest); err != nil {
		return abandon(err)
	}
	current.Current.State, current.Current.StateChangedAt = "submitted", submittedAt
	runErr := client.PaneRunExecGuard(key.PaneID, current.WorktreePath, hand, execguard.Locator(dir))
	if runErr != nil && herdr.IsProcessNotStarted(runErr) {
		return abandon(fmt.Errorf("herdr typed no exec guard invocation: %w", runErr))
	}
	for deadline := time.Now().Add(deps.settle); ; time.Sleep(canonicalV19ExecGuardPoll) {
		verdict, err := observeCanonicalV19ExecGuardLaunch(dir, current, client)
		if err != nil {
			return current.Current.State, fmt.Errorf("launch canonical v19 Herdr: %w", err)
		}
		pending := verdict.State == "" || verdict.State == canonicalV19ExecGuardStarting
		if pending && time.Now().Before(deadline) && ctx.Err() == nil {
			continue
		}
		if pending {
			return abandon(errors.Join(errors.New("the exec guard wrote no decisive record in time"), runErr))
		}
		if runErr != nil {
			verdict.Reason = canonicalV19HerdrLaunchJoinReason(verdict.Reason, "Herdr exec guard invocation failed: "+canonicalV19HerdrSessionErrorText(runErr))
		}
		return applyCanonicalV19ExecGuardVerdict(ctx, homeDir, current, verdict, deps.now)
	}
}
