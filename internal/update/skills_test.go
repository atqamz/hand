package update

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestUpdateSyncsTheFleetSkillsAfterInit(t *testing.T) {
	f := newCycle(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || rep.Status != "updated" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"hand init @ " + f.alpha, "hand skills sync --fleet @ " + f.alpha, "hand skills outdated --all @ " + f.alpha}
	if got := f.log(t, "hand init", "hand skills"); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestUpdateSyncsTheFleetSkillsWhenTheBinaryIsCurrent(t *testing.T) {
	f := newCycle(t)
	f.o.From = Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 24, Luvus: "0.14.4"}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"hand skills sync --fleet @ " + f.alpha, "hand skills outdated --all @ " + f.alpha}
	if got := f.log(t, "hand init", "hand skills"); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestUpdateCheckDoesNotSyncSkills(t *testing.T) {
	f := newCycle(t)
	f.o.Check = true
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if got := f.log(t, "hand skills"); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestUpdateSurvivesASkillsSyncErrorAndReportsWhatIsBehind(t *testing.T) {
	f := newCycle(t)
	f.o.Env = append(f.o.Env, "HAND_FAIL=skills sync", "HAND_OUT=skills[3]{name,state,scope,source,ref,commit,latest,detail}:\n  a,behind,fleet,s,main,c1,c2,\"\"\n  b,current,fleet,s,main,c1,c1,\"\"\n  c,edited,project/app,s,main,c1,c2,\"\"\n")
	f.o.Getenv = lookup(f.o.Env)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || rep.Status != "updated" {
		t.Fatalf("a failing sync failed the update: %+v, %v", rep, err)
	}
	var sync, fleetLine, project bool
	for _, h := range rep.Help {
		sync = sync || strings.Contains(h, "`hand skills sync --fleet` failed in "+f.alpha)
		fleetLine = fleetLine || strings.Contains(h, "1 skill(s) of fleet are behind or edited in "+f.alpha) && strings.Contains(h, "hand skills outdated --fleet")
		project = project || strings.Contains(h, "1 skill(s) of project/app") && strings.Contains(h, "hand skills outdated --project app")
	}
	if !sync || !fleetLine || !project {
		t.Fatalf("help = %q", rep.Help)
	}
}
