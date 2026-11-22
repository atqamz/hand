package desktop_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func scopes(f *fixture, keep ...string) *fixture {
	withSkills(f)
	var out []desktop.SkillScope
	for _, sc := range f.skills.Scopes {
		if slices.Contains(keep, sc.Scope+"/"+sc.Project) {
			out = append(out, sc)
		}
	}
	f.skills.Scopes = out
	return f
}

func skillsApp(t *testing.T, f *fixture) (*desktop.App, *ui.Tester) {
	t.Helper()
	app := newApp(t, withSkills(f))
	app.Show(desktop.SkillsScreen)
	return app, newTester(app)
}

func (f *fixture) skillCalls() []skillsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.skillsLog)
}

func TestTheSkillsPageListsEachScopeWithSourceCommitAndDrift(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			app, _ := skillsApp(t, goldenFixture(t))
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Fleets / golden / skills", "Skills", "Check again",
				"Fleet", "Project hand", "Project api", "User",
				"secondhand", "current", "unmanaged", "scratch",
				"review", "behind", "own source", "notes", "third-party", "edited", "conflict",
				"https://github.com/atqamz/skills.git#skills/review · main · 111111111111 → 222222222222",
				"https://github.com/example/notes.git · v1.2.0 · 333333333333 → 444444444444",
				"This scope could not be read", "the folder is not a git checkout",
				"error", "the fetch of the source failed",
				"Sync", "Update", "Review diff", "Propose")
		})
	}
}

func TestTheSkillsPageSaysAddAndRemoveStayOnTheCommandLine(t *testing.T) {
	_, tt := skillsApp(t, goldenFixture(t))
	mustText(t, tt, "Add and remove are not in the board's API. Run `hand skills add` or `hand skills remove` in a terminal; the next check lists the change.")
}

func TestTheRailOpensTheSkillsPage(t *testing.T) {
	f := withSkills(goldenFixture(t))
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Skills")
	if app.Screen != desktop.SkillsScreen {
		t.Fatalf("screen = %v", app.Screen)
	}
	mustText(t, tt, "Fleets / golden / skills", "secondhand")
	click(t, tt, "Board")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("screen = %v", app.Screen)
	}
}

func TestSkillsAreReadOnlyWhenThePageOpens(t *testing.T) {
	f := goldenFixture(t)
	skillsApp(t, f)
	if f.skillsReads != 1 || len(f.skillCalls()) != 0 {
		t.Fatalf("reads %d, calls %v", f.skillsReads, f.skillCalls())
	}
}

func TestACheckReadsTheSkillsAgain(t *testing.T) {
	f := goldenFixture(t)
	_, tt := skillsApp(t, f)
	click(t, tt, "Check again")
	if f.skillsReads != 2 {
		t.Fatalf("reads = %d", f.skillsReads)
	}
}

func TestAScopeSyncPostsTheScopeAndShowsEachRowAndTheReceipt(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills sync: 1 bumped, 1 pending, 1 refused",
		Results: []desktop.SkillResult{
			{Name: "review", State: "bumped", Detail: "https://github.com/example/hand/pull/7"},
			{Name: "notes", State: "pending", Detail: "third-party, latest 4444; read the diff first"},
			{Name: "deploy", State: "refused", Detail: "the board is shutting down"},
		},
		Refused: 1,
	}
	_, tt := skillsApp(t, withSkills(f))
	click(t, tt, "Sync")
	got := f.skillCalls()
	if len(got) != 1 || got[0].Verb != "sync" || got[0].Req != (desktop.SkillsRequest{Scope: "project", Project: "hand"}) {
		t.Fatalf("calls = %+v", got)
	}
	mustText(t, tt, "Skills sync: 1 bumped, 1 pending, 1 refused", "bumped",
		"https://github.com/example/hand/pull/7", "pending", "refused",
		"the board is shutting down", "1 refused. A refused skill was not changed.")
	if f.skillsReads != 2 {
		t.Fatalf("the list was read %d times, want a second read after the run", f.skillsReads)
	}
}

func TestAReconcileCommandIsShownAndCopied(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt:   "Skills sync: 1 conflict",
		Results:   []desktop.SkillResult{{Name: "triage", State: "conflict", Detail: "edited here, moved at the source"}},
		Reconcile: []string{`hand task add --goal "reconcile triage"`},
	}
	_, tt := skillsApp(t, f)
	click(t, tt, "Sync")
	mustText(t, tt, `hand task add --goal "reconcile triage"`, "conflict")
	click(t, tt, "Copy")
	if got := tt.Clipboard(); got != `hand task add --goal "reconcile triage"` {
		t.Fatalf("clipboard = %q", got)
	}
}

func TestAnOwnSourceSkillSyncsFromItsRowByName(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	_, tt := skillsApp(t, f)
	name, ok := tt.Find("review")
	update, found := tt.Find("Update")
	if !ok || !found {
		t.Fatal("no review row or Update button")
	}
	tt.ClickAt(update.X+update.W/2, name.Y+name.H/2)
	got := f.skillCalls()
	want := desktop.SkillsRequest{Scope: "project", Project: "hand", Name: "review"}
	if len(got) != 1 || got[0].Verb != "sync" || got[0].Req != want {
		t.Fatalf("calls = %+v", got)
	}
}

func TestAThirdPartyUpdateShowsItsDiffThenNeedsAnExplicitApply(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	_, tt := skillsApp(t, f)
	mustNotText(t, tt, "Apply this update")
	click(t, tt, "Review diff")
	tt.SetSize(windowWidth, 1400)
	mustText(t, tt, "Hide diff", "Apply this update", "+description: Write notes after every task",
		"Apply opens a pull request that moves notes to 444444444444, the commit of the diff above. The checkout is not changed.")
	if len(f.skillCalls()) != 0 {
		t.Fatalf("reviewing the diff sent %v", f.skillCalls())
	}
	click(t, tt, "Apply this update")
	got := f.skillCalls()
	want := desktop.SkillsRequest{Scope: "project", Project: "hand", Name: "notes", Yes: "notes@" + thirdLatest}
	if len(got) != 1 || got[0].Verb != "sync" || got[0].Req != want {
		t.Fatalf("calls = %+v, want sync %+v", got, want)
	}
}

func TestACutOrMissingDiffCannotBeApplied(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	for i := range f.skills.Scopes[0].Skills {
		if s := &f.skills.Scopes[0].Skills[i]; s.Name == "notes" {
			s.DiffCut = true
		}
	}
	_, tt := skillsApp(t, f)
	click(t, tt, "Review diff")
	mustText(t, tt, "The diff is over 64 KiB and was cut. Read the rest at the source, then run `hand skills sync --yes notes@"+thirdLatest+"`.")
	mustNotText(t, tt, "Apply this update")

	g := scopes(goldenFixture(t), "project/hand")
	for i := range g.skills.Scopes[0].Skills {
		if s := &g.skills.Scopes[0].Skills[i]; s.Name == "notes" {
			s.Diff = ""
		}
	}
	_, tt = skillsApp(t, g)
	click(t, tt, "Review diff")
	mustText(t, tt, "The board built no diff for this update, so it cannot be applied from here.")
	mustNotText(t, tt, "Apply this update")
}

func TestAMovedThirdPartyYesIsShownAsTheBoardsRefusal(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills sync: 1 refused",
		Results: []desktop.SkillResult{{Name: "notes", State: "refused", Detail: "notes moved since the diff you read: latest 5555"}},
		Refused: 1,
	}
	_, tt := skillsApp(t, f)
	click(t, tt, "Review diff")
	tt.SetSize(windowWidth, 1400)
	click(t, tt, "Apply this update")
	mustText(t, tt, "refused", "notes moved since the diff you read: latest 5555")
}

func TestUpdateAsksBeforeMovingAThirdPartySkillWithoutItsDiff(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{Receipt: "Skills update: 2 updated"}
	_, tt := skillsApp(t, f)
	click(t, tt, "Update")
	mustText(t, tt, "Update does not wait for a yes", "notes", "Update anyway", "Cancel")
	if len(f.skillCalls()) != 0 {
		t.Fatalf("Update sent %v before the yes", f.skillCalls())
	}
	click(t, tt, "Cancel")
	mustNotText(t, tt, "Update anyway")
	click(t, tt, "Update")
	click(t, tt, "Update anyway")
	got := f.skillCalls()
	if len(got) != 1 || got[0].Verb != "update" || got[0].Req != (desktop.SkillsRequest{Scope: "project", Project: "hand"}) {
		t.Fatalf("calls = %+v", got)
	}
	mustText(t, tt, "Skills update: 2 updated")
}

func TestUpdateAsksForACurrentThirdPartySkillToo(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	for i := range f.skills.Scopes[0].Skills {
		if s := &f.skills.Scopes[0].Skills[i]; s.Name == "notes" {
			s.State, s.Latest = "current", s.Commit
		}
	}
	_, tt := skillsApp(t, f)
	click(t, tt, "Update")
	mustText(t, tt, "Update does not wait for a yes", "notes", "Update anyway")
	if len(f.skillCalls()) != 0 {
		t.Fatalf("Update sent %v before the yes", f.skillCalls())
	}
}

func TestAFleetApplyIsDescribedWithItsBackup(t *testing.T) {
	f := scopes(goldenFixture(t), "fleet/")
	f.skills.Scopes[0].Skills = append(f.skills.Scopes[0].Skills, desktop.Skill{
		Name: "notes", Source: "https://github.com/example/notes.git", Ref: "v1", Commit: thirdCommit, Latest: thirdLatest,
		Managed: true, State: "behind", Diff: thirdDiff})
	_, tt := skillsApp(t, f)
	click(t, tt, "Review diff")
	tt.SetSize(windowWidth, 1400)
	mustText(t, tt, "Apply moves notes to 444444444444, the commit of the diff above, with a backup.")
	mustNotText(t, tt, "pull request")
}

func drainSkills(t *testing.T, tt *ui.Tester, posted chan func(), text string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		tt.Frame()
		if tt.HasText(text) {
			return
		}
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatalf("no %q in %q", text, tt.Texts())
		}
	}
}

func TestReopeningThePageKeepsAPendingActionsOutcome(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills sync: 1 current",
		Results: []desktop.SkillResult{{Name: "review", State: "current", Detail: "already at the source"}},
	}
	app, posted := asyncApp(t, withSkills(f))
	tt := newTester(app)
	app.Show(desktop.SkillsScreen)
	drainSkills(t, tt, posted, "review")
	f.skillsGate, f.skillsEntered = make(chan struct{}), make(chan struct{}, 1)
	click(t, tt, "Sync")
	select {
	case <-f.skillsEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the sync never started")
	}
	app.Show(desktop.SkillsScreen)
	close(f.skillsGate)
	drainSkills(t, tt, posted, "already at the source")
	mustText(t, tt, "Skills sync: 1 current")
	reads := f.skillsReads
	for i := 0; i < 3; i++ {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if reads < 2 {
		t.Fatalf("the list was read %d times, want a read after the action", reads)
	}
}

func TestAPairedHostsSkillsAreReadWithNoActionOffered(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.fleets[0].Host = "hostid"
	_, tt := skillsApp(t, f)
	mustText(t, tt, "review", "notes", "deploy", "This fleet is on a paired host. A paired Companion cannot change skills; use the host's Board.",
		"Review diff")
	for _, gone := range []string{"Sync", "Update", "Propose"} {
		if _, ok := tt.Find(gone); ok {
			t.Errorf("a paired host's skills offer %q", gone)
		}
	}
	click(t, tt, "Review diff")
	tt.SetSize(windowWidth, 1400)
	mustText(t, tt, "+description: Write notes after every task")
	mustNotText(t, tt, "Apply this update")
	if len(f.skillCalls()) != 0 {
		t.Fatalf("calls = %+v", f.skillCalls())
	}
}

func TestUpdateRunsAtOnceWhenNoThirdPartySkillIsBehind(t *testing.T) {
	f := scopes(goldenFixture(t), "fleet/")
	f.skillsRun = desktop.SkillsRun{Receipt: "Skills update: 2 current"}
	_, tt := skillsApp(t, f)
	click(t, tt, "Update")
	got := f.skillCalls()
	if len(got) != 1 || got[0].Verb != "update" || got[0].Req != (desktop.SkillsRequest{Scope: "fleet"}) {
		t.Fatalf("calls = %+v", got)
	}
}

func TestAnEditedSkillIsProposedByName(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills propose: 1 proposed",
		Results: []desktop.SkillResult{{Name: "deploy", State: "proposed", Detail: "https://github.com/atqamz/skills/pull/9"}},
	}
	_, tt := skillsApp(t, f)
	click(t, tt, "Propose")
	got := f.skillCalls()
	want := desktop.SkillsRequest{Scope: "project", Project: "hand", Name: "deploy"}
	if len(got) != 1 || got[0].Verb != "propose" || got[0].Req != want {
		t.Fatalf("calls = %+v", got)
	}
	mustText(t, tt, "proposed", "https://github.com/atqamz/skills/pull/9")
}

func TestAPullRequestAddressOpensInTheBrowser(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRun = desktop.SkillsRun{
		Receipt: "Skills propose: 1 proposed",
		Results: []desktop.SkillResult{{Name: "deploy", State: "proposed", Detail: "https://github.com/atqamz/skills/pull/9"}},
	}
	_, tt := skillsApp(t, f)
	click(t, tt, "Propose")
	click(t, tt, "https://github.com/atqamz/skills/pull/9")
	if got := tt.OpenedURLs(); len(got) != 1 || got[0] != "https://github.com/atqamz/skills/pull/9" {
		t.Fatalf("opened %v", got)
	}
}

func TestARefusedActionShowsTheBoardsSentenceNotACannedOne(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		status string
	}{
		{"companion", &desktop.APIError{Status: 403, Code: "forbidden", Message: "a paired Companion cannot change skills; use the host's Board"}, "a paired Companion cannot change skills; use the host's Board"},
		{"controls", &desktop.APIError{Status: 403, Code: "forbidden", Message: "the board's controls are off"}, "the board's controls are off"},
		{"busy", &desktop.APIError{Status: 409, Code: "conflict", Message: "another skills request is running on this board; try again when it ends"}, "another skills request is running on this board; try again when it ends"},
		{"shutdown", errors.New("the board shut down while this ran"), "the board shut down while this ran"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := scopes(goldenFixture(t), "project/hand")
			f.skillsRunErr = c.err
			_, tt := skillsApp(t, f)
			click(t, tt, "Sync")
			mustText(t, tt, "The board refused the request", c.status)
			click(t, tt, "Dismiss")
			mustNotText(t, tt, "The board refused the request")
		})
	}
}

func TestAnUnreadAnswerReadsTheListAgain(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRunErr = &desktop.AppliedUnread{Err: errors.New("bad json")}
	_, tt := skillsApp(t, f)
	click(t, tt, "Sync")
	mustText(t, tt, "the board took the sync but its answer could not be read; the list below is read again")
	if f.skillsReads != 2 {
		t.Fatalf("reads = %d", f.skillsReads)
	}
}

func TestAFailedReadShowsItsErrorWithRetry(t *testing.T) {
	f := goldenFixture(t)
	f.skillsErr = &desktop.APIError{Status: 409, Code: "conflict", Message: "another skills request is running on this board; try again when it ends"}
	_, tt := skillsApp(t, f)
	mustText(t, tt, "The skills could not be read", "another skills request is running on this board; try again when it ends", "Retry")
	f.mu.Lock()
	f.skillsErr = nil
	f.mu.Unlock()
	click(t, tt, "Retry")
	mustText(t, tt, "secondhand")
	mustNotText(t, tt, "The skills could not be read")
}

func TestAReadThatFailsAfterASuccessKeepsTheList(t *testing.T) {
	f := goldenFixture(t)
	_, tt := skillsApp(t, f)
	f.mu.Lock()
	f.skillsErr = errors.New("the board did not answer")
	f.mu.Unlock()
	click(t, tt, "Check again")
	mustText(t, tt, "secondhand", "Stale · the board did not answer")
}

func TestABoardWithoutTheSkillsFeatureSaysSo(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	app.Show(desktop.SkillsScreen)
	tt := newTester(app)
	mustText(t, tt, "The skills could not be read", "this board has no skills endpoints; update hand and restart the board")
	if f.skillsReads != 0 {
		t.Fatalf("read %d times", f.skillsReads)
	}
}

func TestACompanionReadsSkillsButItsActionsShowTheHostsRefusal(t *testing.T) {
	f := scopes(goldenFixture(t), "project/hand")
	f.skillsRunErr = &desktop.APIError{Status: 403, Code: "forbidden", Message: "a paired Companion cannot change skills; use the host's Board", Remote: true}
	_, tt := skillsApp(t, f)
	mustText(t, tt, "review")
	click(t, tt, "Sync")
	mustText(t, tt, "a paired Companion cannot change skills; use the host's Board")
}

func TestSkillsFollowTheFleetSwitch(t *testing.T) {
	f := withSkills(goldenFixture(t))
	app := newApp(t, f)
	app.Show(desktop.SkillsScreen)
	tt := newTester(app)
	mustText(t, tt, "Fleets / golden / skills")
	click(t, tt, "second")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("screen = %v", app.Screen)
	}
	click(t, tt, "Skills")
	mustText(t, tt, "Fleets / second / skills")
	if f.skillsReads != 2 {
		t.Fatalf("reads = %d", f.skillsReads)
	}
}

func TestTheSkillsPageHoldsAtNarrowAndMinimumSizes(t *testing.T) {
	for _, size := range [][2]int{{700, 800}, {860, 600}} {
		for _, dark := range []bool{false, true} {
			app, _ := skillsApp(t, goldenFixture(t))
			tt := sized(t, app, size[0], size[1], dark)
			mustText(t, tt, "Skills", "Check again", "Fleet", "secondhand")
		}
	}
}

func TestScenesTheSkillsPage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "85-skills-1100x760-" + theme(dark)
		t.Run(name, func(t *testing.T) {
			app, _ := skillsApp(t, goldenFixture(t))
			tt := sized(t, app, windowWidth, windowHeight, dark)
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheSkillsPageAtNarrowWidth(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "86-skills-narrow-700x800-" + theme(dark)
		t.Run(name, func(t *testing.T) {
			app, _ := skillsApp(t, goldenFixture(t))
			tt := sized(t, app, 700, 800, dark)
			shotSized(t, tt, name, 700, 800)
		})
	}
}

func TestScenesAThirdPartyDiff(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "87-skills-diff-1100x760-" + theme(dark)
		t.Run(name, func(t *testing.T) {
			f := scopes(goldenFixture(t), "project/hand")
			app, tt := skillsApp(t, f)
			click(t, tt, "Review diff")
			tt = sized(t, app, windowWidth, windowHeight, dark)
			tt.Scroll(550, 400, 0, 260)
			mustText(t, tt, "Apply this update", "+description: Write notes after every task")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesARunWithARefusal(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "88-skills-run-1100x760-" + theme(dark)
		t.Run(name, func(t *testing.T) {
			f := scopes(goldenFixture(t), "project/hand")
			f.skillsRun = desktop.SkillsRun{
				Receipt: "Skills sync: 1 bumped, 1 pending, 1 refused",
				Results: []desktop.SkillResult{
					{Name: "review", State: "bumped", Detail: "https://github.com/example/hand/pull/7"},
					{Name: "notes", State: "pending", Detail: "third-party, latest 4444; read the diff first"},
					{Name: "deploy", State: "refused", Detail: "the board is shutting down"},
				},
				Refused: 1,
			}
			app, tt := skillsApp(t, f)
			click(t, tt, "Sync")
			tt = sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "1 refused. A refused skill was not changed.")
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}
