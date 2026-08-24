package runtime

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
)

func TestConfirmOneShotDoesNotTreatDisappearanceOrPlainResponseAsSuccess(t *testing.T) {
	useFastLaunchPolling(t)
	expectLaunchTimeout()
	fakeLaunchPane(t, exited("agy response without machine-readable init"))

	err := confirmLaunch(herdr.NewClient(), "wA:pC", harness.Antigravity, launchSpec{Executable: "agy", Cwd: "/tmp/worktree"})
	if err == nil || !strings.Contains(err.Error(), "not confirmed started") {
		t.Fatalf("confirmLaunch() = %v, want fail-closed unconfirmed launch", err)
	}
}

func TestConfirmOneShotRecoversFastExitFromTypedInitEvidence(t *testing.T) {
	useFastLaunchPolling(t)
	fakeLaunchPane(t, exited("{\"event\":\"init\",\"conversation_id\":\"c1\",\"init\":{\"cwd\":\"/tmp/worktree\"}}\n{\"event\":\"result\",\"result\":{\"status\":\"ERROR\"}}"))

	if err := confirmLaunch(herdr.NewClient(), "wA:pC", harness.Antigravity, launchSpec{Executable: "agy", Cwd: "/tmp/worktree"}); err != nil {
		t.Fatalf("confirmLaunch() = %v, typed init must prove startup even though provider outcome is separate", err)
	}
}
