package desktop_test

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	notesSource = "https://github.com/example/notes.git"
	reviewSHA   = "5555555555555555555555555555555555555555"
)

const newSkillDiff = `files of this skill (mode, bytes, path):
100644 61 SKILL.md
--- /dev/null
+++ b/SKILL.md
@@ -0,0 +1,4 @@
+---
+name: notes
+description: Write notes after every task
+---
`

func manageApp(t *testing.T, f *fixture) (*desktop.App, *ui.Tester) {
	t.Helper()
	withSkills(f)
	f.features = []string{"skills", "skills_manage"}
	app := newApp(t, f)
	app.Show(desktop.SkillsScreen)
	return app, newTester(app)
}

func reviewRun(commit string) desktop.SkillsRun {
	return desktop.SkillsRun{
		Receipt: "Skills add: 1 review",
		Results: []desktop.SkillResult{{Name: "notes", State: "review", Detail: commit}},
		Diffs:   []desktop.SkillDiff{{Name: "notes", Diff: newSkillDiff}},
	}
}

func addSource(t *testing.T, tt *ui.Tester, source, ref string) {
	t.Helper()
	typeInto(t, tt, "Source", source)
	if ref != "" {
		typeInto(t, tt, "Ref", ref)
	}
}

func TestAnOlderBoardWithoutSkillsManageDrawsNoAddOrRemoveControl(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	_, tt := skillsApp(t, f)
	mustText(t, tt, "This board does not offer add or remove; update hand and restart the board. Until then run hand skills add or hand skills remove in a terminal.",
		"Review diff", "Propose")
	for _, gone := range []string{"Add a skill", "Source", "Remove", "Add"} {
		if _, ok := tt.Find(gone); ok {
			t.Errorf("an older board draws %q", gone)
		}
	}
}

func TestTheAddFormAndRemoveButtonsShowOnALocalFleetWithSkillsManage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			app, _ := manageApp(t, goldenFixture(t))
			tt := sized(t, app, windowWidth, 2400, dark)
			mustText(t, tt, "Add a skill", "Source", "Ref", "Add", "Remove",
				"A bare name adds a skill of Hand's own repository, tracked on main. An https address is third-party: the board shows the whole skill first and installs nothing until you confirm.")
			mustNotText(t, tt, "This board does not offer add or remove")
			mustNotText(t, tt, "The window has no add or remove control")
			if n := textCount(tt, "Add a skill"); n != 3 {
				t.Errorf("add forms = %d, want 3: the fleet, the project hand and the user scope; not the unreadable project api", n)
			}
		})
	}
}

func TestAPairedHostDrawsNoAddFormAndNoRemove(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.fleets[0].Host = "hostid"
	_, tt := manageApp(t, f)
	mustText(t, tt, "This fleet is on a paired host. A paired Companion cannot change skills; use the host's Board.", "review", "notes")
	mustNotText(t, tt, "This board does not offer add or remove")
	for _, gone := range []string{"Add a skill", "Source", "Remove", "Add", "Delete files"} {
		if _, ok := tt.Find(gone); ok {
			t.Errorf("a paired host draws %q", gone)
		}
	}
}

func TestAnOwnSourceAddPostsTheScopeSourceAndRefAndClearsTheForm(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills add: 1 added",
		Results: []desktop.SkillResult{{Name: "triage", State: "added", Detail: ownLatest}},
	}
	_, tt := manageApp(t, f)
	click(t, tt, "Add")
	if len(f.skillCalls()) != 0 {
		t.Fatalf("an empty source sent %+v", f.skillCalls())
	}
	addSource(t, tt, "  triage  ", " v2 ")
	click(t, tt, "Add")
	got := f.skillCalls()
	want := desktop.SkillsRequest{Scope: "project", Project: "hand", Source: "triage", Ref: "v2"}
	if len(got) != 1 || got[0].Verb != "add" || got[0].Req != want {
		t.Fatalf("calls = %+v, want add %+v", got, want)
	}
	mustText(t, tt, "Skills add: 1 added", "triage", "added")
	mustNotText(t, tt, "Review triage")
	click(t, tt, "Add")
	if len(f.skillCalls()) != 1 {
		t.Fatalf("the form kept its source after an add: %+v", f.skillCalls())
	}
	if f.skillsReads != 2 {
		t.Fatalf("reads = %d, want a read after the add", f.skillsReads)
	}
}

func TestAnEditMadeWhileAnAddRunsIsKept(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.features = []string{"skills", "skills_manage"}
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills add: 1 added",
		Results: []desktop.SkillResult{{Name: "triage", State: "added", Detail: ownLatest}},
	}
	app, posted := asyncApp(t, f)
	tt := newTester(app)
	app.Show(desktop.SkillsScreen)
	drainSkills(t, tt, posted, "Add a skill")
	addSource(t, tt, "triage", "")
	f.skillsGate, f.skillsEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Add")
	select {
	case <-f.skillsEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the add never started")
	}
	typeInto(t, tt, "Source", "next-skill")
	close(f.skillsGate)
	drainSkills(t, tt, posted, "Skills add: 1 added")
	f.skillsGate = nil
	for deadline := time.After(5 * time.Second); f.readCount() < 2; {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatalf("the list was read %d times", f.readCount())
		case <-time.After(10 * time.Millisecond):
		}
	}
	for drained := false; !drained; {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(50 * time.Millisecond):
			drained = true
		}
	}
	tt.Frame()
	click(t, tt, "Add")
	for deadline := time.After(5 * time.Second); len(f.skillCalls()) < 2; {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatalf("calls = %+v", f.skillCalls())
		case <-time.After(10 * time.Millisecond):
		}
	}
	want := desktop.SkillsRequest{Scope: "project", Project: "hand", Source: "next-skill"}
	if got := f.skillCalls(); len(got) != 2 || got[1].Req != want {
		t.Fatalf("calls = %+v, want a second add of %+v", got, want)
	}
}

func TestAReviewIsNotConfirmableOnceTheBoardNoLongerListsSkillsManage(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = reviewRun(reviewSHA)
	f.skillsOnRun = func(string, desktop.SkillsRequest) { f.features = []string{"skills"} }
	_, tt := manageApp(t, f)
	addSource(t, tt, notesSource, "")
	click(t, tt, "Add")
	tt.SetSize(windowWidth, 2400)
	mustText(t, tt, "Review notes before it is added", "+description: Write notes after every task", "Close")
	mustNotText(t, tt, "Add this skill")
}

func TestAThirdPartyAddOpensTheReviewCardAndConfirmsWithTheBoardsCommit(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := scopes(goldenFixture(t), "project/hand")
			f.skillsRun = reviewRun(reviewSHA)
			f.skillsOnRun = func(verb string, req desktop.SkillsRequest) {
				if req.Yes != "" {
					f.skillsRun = desktop.SkillsRun{
						Receipt: "Skills add: 1 added",
						Results: []desktop.SkillResult{{Name: "notes", State: "added", Detail: reviewSHA}},
					}
				}
			}
			app, tt := manageApp(t, f)
			addSource(t, tt, notesSource, "v1.2.0")
			click(t, tt, "Add")
			tt = sized(t, app, windowWidth, 2400, dark)
			mustText(t, tt, "Review notes before it is added", "Add this skill", "Close",
				"Nothing is installed yet. It would come from "+notesSource+" at commit 555555555555, the commit of the diff below, into Project hand. A skill steers every worker that reads it.",
				"+description: Write notes after every task", "Skills add: 1 review", "review", reviewSHA)
			got := f.skillCalls()
			if len(got) != 1 || got[0].Req.Yes != "" {
				t.Fatalf("the review step sent %+v", got)
			}

			typeInto(t, tt, "Source", "something-else")
			click(t, tt, "Add this skill")
			got = f.skillCalls()
			want := desktop.SkillsRequest{Scope: "project", Project: "hand", Source: notesSource, Ref: "v1.2.0", Yes: "notes@" + reviewSHA}
			if len(got) != 2 || got[1].Verb != "add" || got[1].Req != want {
				t.Fatalf("calls = %+v, want a second add %+v", got, want)
			}
			mustNotText(t, tt, "Add this skill")
			mustText(t, tt, "Skills add: 1 added")
		})
	}
}

func TestTheReviewShowsTheDiffOfTheSkillItNames(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = reviewRun(reviewSHA)
	f.skillsRun.Diffs = []desktop.SkillDiff{f.skillsRun.Diffs[0], {Name: "other", Diff: "+the other skill\n"}}
	_, tt := manageApp(t, f)
	addSource(t, tt, notesSource, "")
	click(t, tt, "Add")
	tt.SetSize(windowWidth, 2400)
	mustText(t, tt, "+description: Write notes after every task")
	mustNotText(t, tt, "+the other skill")
}

func TestCloseDismissesAReviewWithoutAddingAnything(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = reviewRun(reviewSHA)
	_, tt := manageApp(t, f)
	addSource(t, tt, notesSource, "")
	click(t, tt, "Add")
	tt.SetSize(windowWidth, 2400)
	click(t, tt, "Close")
	mustNotText(t, tt, "Add this skill")
	if len(f.skillCalls()) != 1 {
		t.Fatalf("calls = %+v", f.skillCalls())
	}
}

func TestACutReviewCannotBeAdded(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = reviewRun(reviewSHA)
	f.skillsRun.Diffs[0].Cut = true
	_, tt := manageApp(t, f)
	addSource(t, tt, notesSource, "")
	click(t, tt, "Add")
	tt.SetSize(windowWidth, 2400)
	mustText(t, tt, "The diff is over 64 KiB and was cut, so the board will not add it from here. Read the repository, then run hand skills add in a terminal.", "Close")
	mustNotText(t, tt, "Add this skill")
}

func TestARefusedYesShowsTheReasonAndTheNewDiffWithNoButton(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills add: 1 refused",
		Results: []desktop.SkillResult{{Name: "notes", State: "refused", Detail: "yes notes@" + thirdCommit + " is not what this source is at now"}},
		Diffs:   []desktop.SkillDiff{{Name: "notes", Diff: newSkillDiff}},
		Refused: 1,
	}
	_, tt := manageApp(t, f)
	addSource(t, tt, notesSource, "")
	click(t, tt, "Add")
	tt.SetSize(windowWidth, 2400)
	mustText(t, tt, "yes notes@"+thirdCommit+" is not what this source is at now", "1 refused. A refused skill was not changed.",
		"Not added: notes", "+description: Write notes after every task")
	mustNotText(t, tt, "Add this skill")
}

func TestAnAddTheBoardRefusesShowsItsSentence(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRunErr = &desktop.APIError{Status: 409, Code: "conflict", Message: "skill notes already exists in this scope"}
	_, tt := manageApp(t, f)
	addSource(t, tt, notesSource, "")
	click(t, tt, "Add")
	mustText(t, tt, "The board refused the request", "skill notes already exists in this scope")
}

func TestBoardTextIsDrawnAsTextNeverAsALink(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills add: 1 review, 1 refused",
		Results: []desktop.SkillResult{
			{Name: "[x](https://evil.example/a)", State: "refused", Detail: "https://evil.example/refusal"},
			{Name: "notes", State: "added", Detail: "https://evil.example/added"},
		},
		Diffs:   []desktop.SkillDiff{{Name: "[x](https://evil.example/a)", Diff: "+<a href=\"https://evil.example/d\">https://evil.example/d</a>\n"}},
		Refused: 1,
	}
	_, tt := manageApp(t, f)
	addSource(t, tt, notesSource, "")
	click(t, tt, "Add")
	tt.SetSize(windowWidth, 2400)
	mustText(t, tt, "[x](https://evil.example/a)", "https://evil.example/refusal", "https://evil.example/added",
		`+<a href="https://evil.example/d">https://evil.example/d</a>`)
	for _, link := range []string{"https://evil.example/refusal", "https://evil.example/added"} {
		if err := tt.Click(link); err == nil && len(tt.OpenedURLs()) != 0 {
			t.Errorf("clicking %q opened %v", link, tt.OpenedURLs())
		}
	}
}

func textCount(tt *ui.Tester, text string) int {
	n := 0
	for _, s := range tt.Texts() {
		if s == text {
			n++
		}
	}
	return n
}

func skillCall(t *testing.T, f *fixture, i int) skillsCall {
	t.Helper()
	got := f.skillCalls()
	if len(got) <= i {
		t.Fatalf("calls = %+v, want at least %d", got, i+1)
	}
	return got[i]
}

func TestRemoveAsksBeforeItDeletesAndThenPostsTheName(t *testing.T) {
	f := scopes(goldenFixture(t), "fleet/")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills remove: 1 removed",
		Results: []desktop.SkillResult{{Name: "secondhand-cleanup", State: "removed"}},
	}
	_, tt := manageApp(t, f)
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	mustText(t, tt, "Remove secondhand?", "Delete files", "Cancel",
		"This deletes the skill's files from this scope and unsubscribes it. The board refuses a copy edited here. The skill can be added again from its source.")
	if len(f.skillCalls()) != 0 {
		t.Fatalf("the first click sent %+v", f.skillCalls())
	}
	click(t, tt, "Cancel")
	mustNotText(t, tt, "Delete files")
	if len(f.skillCalls()) != 0 {
		t.Fatalf("cancel sent %+v", f.skillCalls())
	}
	click(t, tt, "Remove")
	click(t, tt, "Delete files")
	got := skillCall(t, f, 0)
	want := desktop.SkillsRequest{Scope: "fleet", Name: "secondhand"}
	if got.Verb != "remove" || got.Req != want {
		t.Fatalf("call = %+v, want remove %+v", got, want)
	}
	mustText(t, tt, "Skills remove: 1 removed", "removed")
	mustNotText(t, tt, "Delete files")
	if f.skillsReads != 2 {
		t.Fatalf("reads = %d, want a read after the remove", f.skillsReads)
	}
}

func TestRemoveIsDisabledForAnEditedOrConflictedSkill(t *testing.T) {
	for _, state := range []string{"edited", "conflict"} {
		t.Run(state, func(t *testing.T) {
			f := scopes(goldenFixture(t), "project/hand")
			f.skills.Scopes[0].Skills = []desktop.Skill{{Name: "deploy", Source: "https://github.com/atqamz/skills.git#skills/deploy", Ref: "main",
				Commit: ownCommit, Latest: ownCommit, Managed: true, Trusted: true, State: state}}
			_, tt := manageApp(t, f)
			mustText(t, tt, "deploy", state, "Remove")
			click(t, tt, "Remove")
			mustNotText(t, tt, "Delete files")
			mustNotText(t, tt, "Remove deploy?")
			if len(f.skillCalls()) != 0 {
				t.Fatalf("calls = %+v", f.skillCalls())
			}
		})
	}
}

func TestRemoveIsOfferedOnlyForACurrentOrBehindManagedSkill(t *testing.T) {
	for _, c := range []struct {
		skill desktop.Skill
		want  bool
	}{
		{desktop.Skill{Name: "a", Managed: true, Trusted: true, State: "current"}, true},
		{desktop.Skill{Name: "a", Managed: true, Trusted: false, State: "behind", Diff: thirdDiff, Latest: thirdLatest}, true},
		{desktop.Skill{Name: "a", Managed: true, Trusted: true, State: "error", Detail: "the fetch failed"}, false},
		{desktop.Skill{Name: "a", Managed: false}, false},
	} {
		f := scopes(goldenFixture(t), "project/hand")
		f.skills.Scopes[0].Skills = []desktop.Skill{c.skill}
		_, tt := manageApp(t, f)
		if _, ok := tt.Find("Remove"); ok != c.want {
			t.Errorf("%+v: remove drawn = %v, want %v", c.skill, ok, c.want)
		}
	}
}

func TestRemoveNamesTheSkillOfItsRow(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skills.Scopes[0].Skills = f.skills.Scopes[0].Skills[1:2]
	_, tt := manageApp(t, f)
	click(t, tt, "Remove")
	mustText(t, tt, "Remove notes?")
	click(t, tt, "Delete files")
	want := desktop.SkillsRequest{Scope: "project", Project: "hand", Name: "notes"}
	if got := skillCall(t, f, 0); got.Verb != "remove" || got.Req != want {
		t.Fatalf("call = %+v, want remove %+v", got, want)
	}
}

func TestARefusedRemoveShowsTheRowsReason(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills remove: 1 refused",
		Results: []desktop.SkillResult{{Name: "review", State: "refused", Detail: "review was edited since it was installed; propose it or restore it first"}},
		Refused: 1,
	}
	f.skills.Scopes[0].Skills = f.skills.Scopes[0].Skills[:1]
	_, tt := manageApp(t, f)
	click(t, tt, "Remove")
	click(t, tt, "Delete files")
	mustText(t, tt, "Skills remove: 1 refused", "1 refused. A refused skill was not changed.",
		"review was edited since it was installed; propose it or restore it first")
	if strings.Contains(strings.Join(tt.Texts(), "\n"), "Remove review?") {
		t.Error("the confirm stays open after the answer")
	}
}

func TestScenesTheAddForm(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "89-skills-add-1100x760-" + theme(dark)
		t.Run(name, func(t *testing.T) {
			f := scopes(goldenFixture(t), "fleet/", "project/hand")
			app, tt := manageApp(t, f)
			addSource(t, tt, notesSource, "v1.2.0")
			tt = sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Add a skill", "Source", "Ref", "Remove")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheReviewCard(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "90-skills-review-1100x760-" + theme(dark)
		t.Run(name, func(t *testing.T) {
			f := scopes(goldenFixture(t), "project/hand")
			f.skillsRun = reviewRun(reviewSHA)
			app, tt := manageApp(t, f)
			addSource(t, tt, notesSource, "v1.2.0")
			click(t, tt, "Add")
			tt = sized(t, app, windowWidth, windowHeight, dark)
			tt.Scroll(550, 400, 0, 250)
			mustText(t, tt, "Review notes before it is added", "Add this skill", "+description: Write notes after every task")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}
