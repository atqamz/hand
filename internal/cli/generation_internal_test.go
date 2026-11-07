package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestARequesterWithNoCallerSaysUnknown(t *testing.T) {
	if got := (requester{}).String(); !strings.Contains(got, "unknown") {
		t.Fatalf("requester = %q", got)
	}
}

func TestAStaleGenerationRecordsOneRefusal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, generationFile), []byte("9 stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(dir, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	r := &runner{root: dir, generation: "9 other"}
	for i := range 5 {
		ok, err := r.checkGeneration(ctx, st, sup)
		if err != nil || ok {
			t.Fatalf("check %d = %v, %v", i, ok, err)
		}
	}
	events, err := st.EventsAfter(ctx, 0, []string{"supervisor.refused"}, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("refusals = %+v, %v", events, err)
	}
}
