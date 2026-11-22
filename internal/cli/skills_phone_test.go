package cli_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/fakebin"
)

func (a *skillsAPI) phone(scope, project, name string) board.APISkillsRun {
	a.t.Helper()
	run, err := a.act(context.Background(), board.SkillsAction{Verb: "sync", Scope: scope, Project: project, Name: name, Phone: true})
	if err != nil {
		a.t.Fatalf("a phone's sync = %v", err)
	}
	return run
}

func TestSkillsPhoneSyncLeavesAThirdPartySkillBehindWithItsDiff(t *testing.T) {
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	h.ok("skills", "add", "--fleet", "--ref", "main", "file://"+filepath.ToSlash(tool.bare))
	moved := tool.commit(skillFiles("tool-skill", "v2"))
	before, lockBefore := readSkill(t, skillPath(h.home, ".claude", "tool-skill")), readSkill(t, fleetLock(h.home))
	run := newSkillsAPI(t, h).phone("fleet", "", "")
	if r := resultOf(run, "tool-skill"); r.State != "behind" || !strings.Contains(r.Detail, "tool-skill@"+moved) || run.Refused != 0 || len(run.Diffs) != 1 || !strings.Contains(run.Diffs[0].Diff, "+v2") {
		t.Fatalf("a phone's sync = %+v", run)
	}
	if readSkill(t, skillPath(h.home, ".claude", "tool-skill")) != before || readSkill(t, fleetLock(h.home)) != lockBefore {
		t.Fatal("a phone's sync changed a third-party skill")
	}
}

func TestSkillsPhoneSyncAppliesAnOwnSkillWithABackup(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	old := skillState(t, f.h.home)["triage"].Commit
	moved := f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	run := newSkillsAPI(t, f.h).phone("fleet", "", "")
	if r := resultOf(run, "triage"); r.State != "updated" || r.Detail != old[:12]+" -> "+moved[:12] {
		t.Fatalf("row = %+v", r)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "triage")); !strings.Contains(got, "\nv2\n") {
		t.Fatalf("copy = %q", got)
	}
	if len(backups(t, filepath.Join(f.h.home, ".agents", "skills-backups"), "triage")) != 1 {
		t.Fatal("no backup of the previous copy")
	}
}

func TestSkillsPhoneSyncLeavesAnEditedAndAConflictingCopyAlone(t *testing.T) {
	f := newProposeFixture(t, "")
	f.hub.commit(map[string]string{"skills/edi/SKILL.md": skillFiles("edi", "v1")["SKILL.md"], "skills/clash/SKILL.md": skillFiles("clash", "v1")["SKILL.md"]})
	for _, name := range []string{"edi", "clash"} {
		f.h.ok("skills", "add", "--fleet", name)
	}
	editedText, clashText := f.edit("edi"), f.edit("clash")
	f.hub.commit(map[string]string{"skills/clash/SKILL.md": skillFiles("clash", "v2")["SKILL.md"]})
	run := newSkillsAPI(t, f.h).phone("fleet", "", "")
	if resultOf(run, "edi").State != "edited" || resultOf(run, "clash").State != "conflict" || len(run.Reconcile) != 1 {
		t.Fatalf("sync = %+v", run)
	}
	if readSkill(t, skillPath(f.h.home, ".claude", "edi")) != editedText || readSkill(t, skillPath(f.h.home, ".claude", "clash")) != clashText {
		t.Fatal("an edited copy was overwritten")
	}
}

func TestSkillsPhoneSyncInAProjectOpensOneBumpAndARepeatIsPending(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	a := newSkillsAPI(t, f.ph)
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	lockBefore := readSkill(t, fleetLock(f.repo))
	run := a.phone("project", "app", "")
	if r := resultOf(run, "triage"); r.State != "bumped" || r.Detail != prURL || len(f.ghArgs()) != 1 || len(f.branches("triage")) != 1 {
		t.Fatalf("sync = %+v", run)
	}
	if readSkill(t, fleetLock(f.repo)) != lockBefore || gitIn(t, f.repo, "status", "--porcelain") != "" {
		t.Fatal("the checkout changed")
	}
	run = a.phone("project", "app", "")
	if r := resultOf(run, "triage"); r.State != "pending" || len(f.ghArgs()) != 1 || len(f.branches("triage")) != 1 {
		t.Fatalf("a repeat = %+v", run)
	}
}

func TestSkillsPhoneSyncNeverAppliesAChangedSubscription(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	editJSON(t, filepath.Join(f.h.home, "skills.json"), func(skills map[string]map[string]any) {
		skills["triage"]["source"] = "someone/else"
	})
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	run := newSkillsAPI(t, f.h).phone("fleet", "", "")
	if r := resultOf(run, "triage"); r.State != "behind" || len(run.Diffs) != 1 {
		t.Fatalf("sync = %+v", run)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "triage")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("applied: %q", got)
	}
}

func TestSkillsPhoneSyncDoesNotApplyALocalSourceButTheHostDoes(t *testing.T) {
	h := skillsHarness(t)
	src := skillSource(t, "notes", "v1")
	h.ok("skills", "add", "--fleet", src)
	writeFiles(t, src, skillFiles("notes", "v2"))
	a := newSkillsAPI(t, h)
	run := a.phone("fleet", "", "")
	if r := resultOf(run, "notes"); r.State != "behind" || !strings.Contains(r.Detail, "local source") || strings.Contains(r.Detail, src) || len(run.Diffs) != 0 {
		t.Fatalf("a phone's sync = %+v", run)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "notes")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("a phone applied a local source: %q", got)
	}
	if r := resultOf(a.ok("sync", map[string]string{"scope": "fleet"}), "notes"); r.State != "updated" {
		t.Fatalf("the host's sync = %+v", r)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "notes")); !strings.Contains(got, "\nv2\n") {
		t.Fatalf("the host did not apply it: %q", got)
	}
}

func TestSkillsPhoneSyncDropsALoginFromASourceAndAPathFromAFailingGh(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	editJSON(t, fleetLock(f.h.home), func(skills map[string]map[string]any) {
		skills["triage"]["sourceUrl"] = "https://pat:s3@cret@127.0.0.1:1/acme/skills.git"
	})
	run := newSkillsAPI(t, f.h).phone("fleet", "", "")
	if r := resultOf(run, "triage"); r.State != "refused" || strings.Contains(r.Detail, "cret") || strings.Contains(r.Detail, "pat:") {
		t.Fatalf("sync = %+v", run)
	}
	b := newBumpFixture(t)
	b.install("triage")
	b.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	fakebin.Install(t, b.bin, "gh", "gh", map[string]string{"log": b.log, "stderr": "could not read https://pat:s3@cret@github.com/acme/hub.git and /home/someone/secret/dir/x"})
	a := newSkillsAPI(t, b.ph)
	out, err := a.act(context.Background(), board.SkillsAction{Verb: "sync", Scope: "project", Project: "app", Phone: true})
	text := fmt.Sprint(out, err)
	if !strings.Contains(text, "github.com/acme/hub.git") || strings.Contains(text, "cret") || strings.Contains(text, "pat:") || strings.Contains(text, "someone") {
		t.Fatalf("sync = %+v, %v", out, err)
	}
}
