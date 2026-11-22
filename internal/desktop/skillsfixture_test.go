package desktop_test

import (
	"context"

	"github.com/atqamz/hand/internal/desktop"
)

type skillsCall struct {
	Verb string
	Req  desktop.SkillsRequest
}

func (f *fixture) Skills(ctx context.Context, id string) (desktop.Skills, error) {
	f.note("skills")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skillsReads++
	if f.skillsErr != nil {
		return desktop.Skills{}, f.skillsErr
	}
	return f.skills, nil
}

func (f *fixture) SkillsRun(ctx context.Context, id, verb string, req desktop.SkillsRequest) (desktop.SkillsRun, error) {
	f.note("skills:" + verb)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skillsLog = append(f.skillsLog, skillsCall{verb, req})
	if f.skillsRunErr != nil {
		return desktop.SkillsRun{}, f.skillsRunErr
	}
	if f.skillsOnRun != nil {
		f.skillsOnRun(verb, req)
	}
	out := f.skillsRun
	out.Scope, out.Project = req.Scope, req.Project
	return out, nil
}

const (
	ownCommit   = "1111111111111111111111111111111111111111"
	ownLatest   = "2222222222222222222222222222222222222222"
	thirdCommit = "3333333333333333333333333333333333333333"
	thirdLatest = "4444444444444444444444444444444444444444"
)

const thirdDiff = `--- a/notes/SKILL.md
+++ b/notes/SKILL.md
@@ -1,4 +1,5 @@
 ---
 name: notes
-description: Write notes
+description: Write notes after every task
 ---
+Run notes add before you stop.
`

func skillsData() desktop.Skills {
	return desktop.Skills{Scopes: []desktop.SkillScope{
		{Scope: "fleet", Skills: []desktop.Skill{
			{Name: "secondhand", Source: "hand init", Managed: true, Trusted: true, State: "current"},
			{Name: "secondhand-cleanup", Source: "hand init", Managed: true, Trusted: true, State: "current"},
			{Name: "scratch", Managed: false},
		}},
		{Scope: "project", Project: "hand", Skills: []desktop.Skill{
			{Name: "review", Source: "https://github.com/atqamz/skills.git#skills/review", Ref: "main", Commit: ownCommit, Latest: ownLatest, Managed: true, Trusted: true, State: "behind"},
			{Name: "notes", Source: "https://github.com/example/notes.git", Ref: "v1.2.0", Commit: thirdCommit, Latest: thirdLatest, Managed: true, Trusted: false, State: "behind", Diff: thirdDiff},
			{Name: "deploy", Source: "https://github.com/atqamz/skills.git#skills/deploy", Ref: "main", Commit: ownCommit, Latest: ownCommit, Managed: true, Trusted: true, State: "edited"},
			{Name: "triage", Source: "https://github.com/atqamz/skills.git#skills/triage", Ref: "main", Commit: ownCommit, Latest: ownLatest, Managed: true, Trusted: true, State: "conflict"},
		}},
		{Scope: "project", Project: "api", Error: "the folder is not a git checkout"},
		{Scope: "user", Skills: []desktop.Skill{
			{Name: "lint", Source: "https://github.com/atqamz/skills.git#skills/lint", Ref: "main", Commit: ownCommit, Latest: "", Managed: true, Trusted: true, State: "error", Detail: "the fetch of the source failed"},
		}},
	}}
}

func withSkills(f *fixture) *fixture {
	f.features = []string{"skills"}
	if f.skills.Scopes == nil {
		f.skills = skillsData()
	}
	return f
}
