package board_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/state"
)

var frozen = time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)

type scene struct {
	id, name string
	build    func(t *testing.T, fx *fixture)
}

func usage(model string, n int) string {
	return fmt.Sprintf(`,"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}`, n)
}

func rec(role, ts, text string, tokens int) string {
	b, _ := json.Marshal(text)
	text = string(b[1 : len(b)-1])
	if role == "user" {
		return `{"type":"user","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}`
	}
	u := ""
	if tokens > 0 {
		u = usage("", tokens)
	}
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"model":"claude-opus-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"` + text + `"}]` + u + `}}`
}

const longReply = "Here is where the fleet stands.\n\n### Summary\n\nt3 passed review and a2 is merging. d1 is waiting on you: it decides whether the cookie keeps its old name.\n\n- t1 **Fix login** is running on a1 (claude opus high)\n- t2 *Flaky test* failed twice; r3 has the log\n- t4 is queued behind t1 because both touch `internal/board/static/board.css`\n\n| task | state | pr | note |\n|---|---|---:|---|\n| t1 | running | 42 | cookie rename |\n| t2 | failed | - | linker error on arm64 macOS runner after the dependency bump |\n| t3 | done | 40 | merged |\n\n```go\nfunc main() {\n\tfmt.Println(\"a very long line that should scroll horizontally rather than wrap because code blocks never wrap in this board\")\n}\n```\n\n> Heads up: the path /very/long/path/that/never/breaks/internal/board/static/board.css:214 is unbreakable.\n\nSupercalifragilisticexpialidocious_and_a_very_long_identifier_without_any_break_points_at_all_so_it_must_wrap_or_scroll."

func baseTasks(t *testing.T, fx *fixture) {
	ctx := context.Background()
	one := active(t, fx.st, "Fix login")
	a := workerAttempt(t, fx.st, one.ID)
	_, _ = fx.st.SetPlan(ctx, one.ID, "1. reproduce\n2. fix the cookie\n3. add a regression test")
	_, _ = fx.st.AddReport(ctx, a.ID, state.ReportDone, "Cookie fixed\nPR: https://github.com/example/hand/pull/42\n\nAll tests pass.")
	_, _ = fx.st.Ask(ctx, one.ID, "Keep the old cookie name?\n\n1. Yes, keep it\n2. No, rename it")
	two := active(t, fx.st, "Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump")
	attempt(t, fx.st, two.ID, state.AttemptFailed, "launch did not finish: the worktree already exists at a path that is not ours")
	three := active(t, fx.st, "Docs")
	b := attempt(t, fx.st, three.ID, state.AttemptRunning, "")
	_, _ = fx.st.AddReport(ctx, b.ID, state.ReportProgress, "Halfway there")
	for _, title := range []string{"Write docs", "Add dark mode toggle", "Rename the cookie"} {
		_, _ = fx.st.AddTask(ctx, "hand", title, "")
	}
}

func sceneHome(t *testing.T, id string) string {
	home := filepath.Join(os.TempDir(), "hand-scenes", id)
	_ = os.RemoveAll(home)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func openNow(t *testing.T, id string) *state.Store {
	path := filepath.Join(t.TempDir(), "hand.db")
	st, err := state.Open(path, func() time.Time { return frozen.Add(-20 * time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO fleet(only, id, name) VALUES (1, ?, 'test')`, id); err != nil {
		t.Fatal(err)
	}
	return st
}

func ago(m int) string {
	return frozen.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339)
}

const fixReply = "Here is the plan for the login cookie, with what I found.\n\n### What happens\n\nThe redirect after `/login/callback` drops the session cookie on Safari because it is set with `SameSite=Strict` on a cross-site navigation. Chrome keeps it only because of the 2-minute Lax-by-default grace.\n\n#### Options\n\n1. **Rename** the cookie and set `SameSite=Lax` (one deploy, everyone logs in once more)\n2. **Keep** the name and add a second `Lax` cookie for the callback only\n   - smaller blast radius\n   - two cookies to expire together\n3. Move the callback to the same site (needs DNS, out of scope this week)\n\nI opened d1 to ask which one you want. Until then a1 keeps the regression test only.\n\n| browser | before | after (option 1) | after (option 2) |\n|---|---:|---:|---:|\n| Safari 18 | logged out | ok | ok |\n| Chrome 140 | ok (grace) | ok | ok |\n| Firefox 133 | logged out | ok | ok |\n\nThe failing check, from a1's run:\n\n```text\n--- FAIL: TestCallbackKeepsSession (0.02s)\n    session_test.go:88: cookie hand_session missing after 302 to https://example.com/login/callback?state=4f0c9a1e8b7d6c5b4a39281706f5e4d3c2b1a090\n```\n\n> The fix touches internal/auth/cookie.go and /very/long/path/to/the/monorepo/services/web/internal/auth/session_cookie_test.go, so t4 waits behind it.\n\n~~Bump the cookie version~~ not needed: the name change covers it."

const errReply = "a2 failed before it started, so nothing ran on t2.\n\n```text\nhand attempt start: launch did not finish: worktree /very/long/path/worktrees/f00000000e0aa/t2-a2 already exists and is not ours (HEAD 9f8e7d6, branch hand/f00000000e0aa/t2-a2)\n```\n\nI did not retry: removing a worktree that is not ours could lose someone's work. It is in Needs as **FAILED a2**. If the folder is safe to delete, tell me and I will start a3."

func inboxShot(t *testing.T, fx *fixture) string {
	dir := filepath.Join(fx.options.Home, "inbox")
	_ = os.MkdirAll(dir, 0o755)
	img := image.NewRGBA(image.Rect(0, 0, 360, 640))
	fill := func(r image.Rectangle, c color.RGBA) { draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src) }
	fill(img.Bounds(), color.RGBA{246, 247, 249, 255})
	fill(image.Rect(0, 0, 360, 56), color.RGBA{27, 31, 36, 255})
	fill(image.Rect(24, 88, 336, 136), color.RGBA{253, 236, 236, 255})
	fill(image.Rect(24, 160, 336, 184), color.RGBA{213, 219, 226, 255})
	fill(image.Rect(24, 200, 260, 224), color.RGBA{213, 219, 226, 255})
	fill(image.Rect(24, 560, 336, 608), color.RGBA{29, 95, 194, 255})
	name := "20261016-081500-1.png"
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_ = png.Encode(f, img)
	return filepath.Join(dir, name)
}

func sentFiles(t *testing.T, fx *fixture) string {
	dir := filepath.Join(fx.options.Home, "inbox")
	_ = os.MkdirAll(dir, 0o755)
	var lines []string
	for i, f := range []struct {
		name string
		size int
	}{{"q3-export.csv", 18_400}, {"server-2026-10-15.log", 2_411_000}, {"invoice-2026-10.pdf", 312_000}, {"crash-dump.zip", 7_900_000}, {"Q3_review_deck_final_v7_for_the_board_meeting.pptx", 4_200_000}, {"old-notes.md", -1}} {
		name := fmt.Sprintf("20261016-083000-%d-%s", i+1, f.name)
		if f.size >= 0 {
			_ = os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x", f.size)), 0o600)
		}
		lines = append(lines, "[file: "+filepath.Join(dir, name)+"]")
	}
	return strings.Join(lines, "\n")
}

const designDoc = `# Nightly export pipeline: design

Proposal for t287, written by its worker. The open decision on t287 asks whether to build it as written.

## Summary

The export runs once a night, writes one CSV per region, and publishes a manifest last, so a reader never sees half a night. The [API notes](api.md) list the endpoints, and the [export notes from t286](../t286/notes.md) explain why the old export is retired.

## Goals

- [x] One job per night that is safe to retry
- [x] A manifest written last
- [ ] A backfill of the last 30 days
- [ ] An alert when a night is late

## Data flow

![The scheduler starts the worker, the worker writes to the bucket](images/flow.png)

| stage | owner | runtime | output |
|---|---|---:|---|
| schedule | cron | 1 s | a run id |
| extract | export worker | 14 min | rows per region |
| write | export worker | 6 min | ` + "`region-*.csv`" + ` |
| publish | export worker | 2 s | ` + "`manifest.json`" + ` |

### Manifest

` + "```json" + `
{
  "run": "2026-10-19T01:00:00Z",
  "files": ["region-eu.csv", "region-us.csv", "region-apac.csv"],
  "rows": 184220
}
` + "```" + `

` + "```mermaid" + `
graph LR
  cron --> worker --> bucket
` + "```" + `

The diagram above stays as its source: the Board draws no Mermaid.

## Risks

1. A late night overlaps the next run.
   - **Mitigation:** a lock file; each run is logged in HOME/reports/t287/run.log
   - the lock is released on exit, even on a panic
2. A region has no rows. *Mitigation:* write an empty file with a header.

> Out of scope: a streaming export. It needs a queue we do not run.

## Lines that check the viewer

Raw HTML stays text: <script>alert(1)</script> and <img src=x onerror=alert(1)>. A [script link](javascript:alert(1)) and a [protocol-relative link](//example.com/x) stay text. A remote image is not loaded: ![remote](https://example.com/x.png). A link that climbs out of the fleet folder goes nowhere: [the key](../../../.ssh/id_ed25519). Paths outside the fleet stay text: OUTSIDE/notes.md and ` + "`~/.ssh/id_ed25519`" + `.
`

const apiDoc = "# Export API notes\n\nBack to the [design](design.md).\n\n| method | path | what |\n|---|---|---|\n| GET | `/exports/{night}` | the manifest |\n| GET | `/exports/{night}/{region}.csv` | one region |\n\nThe worker reads `EXPORT_BUCKET` from its environment.\n"

func docTree(t *testing.T, home string) string {
	outside := home + "-outside"
	_ = os.RemoveAll(outside)
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	write := func(rel, text string) {
		p := filepath.Join(home, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	picture := func(rel string, w, h int, boxes ...color.RGBA) {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{246, 247, 249, 255}}, image.Point{}, draw.Src)
		step := w / (len(boxes) + 1)
		for i, c := range boxes {
			x := step/2 + i*step + step/8
			draw.Draw(img, image.Rect(x, h/3, x+step*3/4, h*2/3), &image.Uniform{c}, image.Point{}, draw.Src)
		}
		p := filepath.Join(home, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_ = png.Encode(f, img)
	}
	r := strings.NewReplacer("HOME", home, "OUTSIDE", outside)
	write("reports/t287/design.md", r.Replace(designDoc))
	write("reports/t287/api.md", apiDoc)
	picture("reports/t287/images/flow.png", 720, 180, color.RGBA{64, 122, 204, 255}, color.RGBA{115, 106, 215, 255}, color.RGBA{26, 122, 62, 255})
	var log strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&log, "2026-10-19T01:%02d:00Z export run=%d region=%s rows=%d ok\n", i, 1019, []string{"eu", "us", "apac"}[i%3], 4000+i*37)
	}
	write("reports/t287/run.log", log.String())
	write("reports/t287/huge.log", strings.Repeat("2026-10-19T01:00:00Z export worker heartbeat ok, nothing to report in this line\n", 40000))
	write("reports/t287/binary.md", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"+strings.Repeat("\x00", 64))
	write("reports/t287/résumé notes.md", "# Notes with a space and an accent in the name\n\nThe name is `résumé notes.md`.\n")
	write("reports/t286/notes.md", "# Export notes (t286)\n\nThe old export wrote one file for all regions and took 40 minutes.\n\n- it held a lock on the whole table\n- a retry wrote the file twice\n\nSee the new [design](../t287/design.md).\n")
	write("projects/app/README.md", "# a project clone\n")
	write(".secret/token", "fixture-not-a-secret\n")
	write("hand.db", "fixture stand-in, not a database\n")
	_ = os.MkdirAll(filepath.Join(outside, "secrets"), 0o700)
	_ = os.WriteFile(filepath.Join(outside, "secrets", "id_ed25519"), []byte("FIXTURE, NOT A KEY\n"), 0o600)
	_ = os.WriteFile(filepath.Join(outside, "notes.md"), []byte("# outside the fleet\n"), 0o644)
	_ = os.Symlink(filepath.Join(outside, "secrets"), filepath.Join(home, "reports/t287/escape"))
	_ = os.Symlink("../../hand.db", filepath.Join(home, "reports/t287/db-link"))
	_ = os.Symlink("../t286/notes.md", filepath.Join(home, "reports/t287/notes-link.md"))
	for i, c := range []color.RGBA{{64, 122, 204, 255}, {196, 43, 50, 255}, {26, 122, 62, 255}, {138, 91, 0, 255}, {115, 106, 215, 255}, {21, 23, 28, 255}} {
		picture(fmt.Sprintf("shots/t288/export-%d-%s.png", i+1, []string{"light", "dark"}[i%2]), 390, 844, c, c)
	}
	return outside
}

func chatLines(t *testing.T, fx *fixture, tokens int) []string {
	wake := func(m int, lines ...string) string {
		return rec("user", ago(m), "[hand v1 wake]\n"+strings.Join(lines, "\n"), 0)
	}
	return []string{
		rec("user", ago(2905), "morning. what is on the board today?", 0),
		rec("assistant", ago(2904), "Morning. Nothing waits on you. Three tasks are in the inbox:\n\n- t4 Write docs\n- t5 Add dark mode toggle\n- t6 Rename the cookie\n\nWant me to start one?", 0),
		rec("user", ago(2890), "start on the login bug first: people get logged out after the SSO redirect, mostly on Safari", 0),
		rec("assistant", ago(2889), "Captured it as t1 **Fix login** and started a1 (claude sonnet) in its own worktree. I will tell you when it reports.", 0),
		wake(2850, `attempt.reported a1 r1 progress (t1 "Fix login")`),
		rec("assistant", ago(2849), "a1 reproduced it on Safari and is writing a regression test first.", 0),
		wake(2790, `attempt.quiet a1 reported since its last quiet turn (t1 "Fix login")`),
		wake(2788, `attempt.idle a3 (t3 "Docs")`),
		wake(2786, `attempt.reported a3 r2 progress (t3 "Docs")`),
		rec("user", ago(1500), "here is what I see on the phone after the redirect\n[image: "+inboxShot(t, fx)+"]", 0),
		rec("assistant", ago(1498), fixReply, 0),
		rec("user", ago(1460), "the customer's export, last night's server log, the invoice they disputed and the crash dump from the phone. the deck is for context only\n"+sentFiles(t, fx), 0),
		rec("assistant", ago(1459), "Got them. The log shows the 502s start at 02:14, right after the deploy; the export has 3 rows with an empty `region`. I will not unpack the crash dump unless you want me to.", 0),
		wake(1440, `attempt.failed a2 launch did not finish (t2 "Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump")`),
		rec("assistant", ago(1439), errReply, 0),
		rec("user", ago(1400), "[Request interrupted by user]", 0),
		rec("user", ago(1399), "leave a2 for now, I will look at the folder tonight", 0),
		rec("assistant", ago(1398), "Left it. a2 stays in Needs until you decide.", 0),
		wake(1300, `attempt.blocked a3 Esc to cancel (t3 "Docs")`),
		wake(1299, `attempt.reported a3 r2 progress (t3 "Docs")`),
		wake(1298, `attempt.idle a3 (t3 "Docs")`),
		wake(1297, `attempt.quiet a1 no report since its last quiet turn (t1 "Fix login")`),
		rec("user", ago(200), "status?", 0),
		rec("assistant", ago(199), "Short version: t1 waits on d1, t2 waits on you (a2's folder), t3 is half done. Nothing is running into a limit.", 0),
		wake(31, `decision.answered d1 2 (t1 "Fix login")`, `attempt.reported a1 r1 done (t1 "Fix login")`),
		rec("assistant", ago(12), longReply, tokens),
	}
}

func claudeChat(t *testing.T, fx *fixture, tokens int) {
	claudeLog(t, fx, chatLines(t, fx, tokens))
}

func scenes() []scene {
	supervised := func(status, hint string, tokens int, extra func(t *testing.T, fx *fixture)) func(t *testing.T, fx *fixture) {
		return func(t *testing.T, fx *fixture) {
			fx.status, fx.hint = status, hint
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			baseTasks(t, fx)
			claudeChat(t, fx, tokens)
			if extra != nil {
				extra(t, fx)
			}
		}
	}
	return []scene{
		{"f00000000e001", "empty", func(t *testing.T, fx *fixture) {}},
		{"f00000000e002", "ready", supervised("idle", "", 510000, nil)},
		{"f00000000e003", "working", supervised("working", "", 850000, func(t *testing.T, fx *fixture) {
			_, _ = fx.st.AddSupervisorInput(context.Background(), "also check the nightly pipeline")
			_, _ = fx.st.AddSupervisorInput(context.Background(), "and write the summary in the PR")
		})},
		{"f00000000e004", "blocked", func(t *testing.T, fx *fixture) {
			fx.screen = permission
			supervised("blocked", "Esc to cancel · Tab to amend", 940000, func(t *testing.T, fx *fixture) {
				fx.panes = map[string]string{"3": "blocked"}
				a, _ := fx.st.Attempts(context.Background(), 0, 10)
				for _, x := range a {
					if x.Status == state.AttemptRunning {
						_ = fx.st.NoteAttempt(context.Background(), x.ID, "blocked", "Esc to cancel")
					}
				}
			})(t, fx)
		}},
		{"f00000000e005", "stopped", func(t *testing.T, fx *fixture) {
			fx.supervisor(t, state.AttemptInterrupted, "gen-1")
			baseTasks(t, fx)
			claudeChat(t, fx, 300000)
		}},
		{"f00000000e007", "many-tasks-and-a-very-long-fleet-name-for-the-masthead", supervised("idle", "", 100000, func(t *testing.T, fx *fixture) {
			ctx := context.Background()
			for i := 0; i < 30; i++ {
				task := active(t, fx.st, fmt.Sprintf("Task number %d: a long title that goes on and on to see how the row truncates at narrow widths", i))
				if i%3 == 0 {
					a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
					if i%6 == 0 {
						_, _ = fx.st.AddReport(ctx, a.ID, state.ReportDone, fmt.Sprintf("Done %d\nPR: https://github.com/example/hand/pull/%d", i, 100+i))
					}
				}
				if i%7 == 0 {
					_, _ = fx.st.Ask(ctx, task.ID, fmt.Sprintf("Question %d: which of the two approaches do you want?", i))
				}
			}
			for i := 0; i < 14; i++ {
				_, _ = fx.st.AddTask(ctx, "hand", fmt.Sprintf("Inbox item %d", i), "")
			}
		})},
		{"f00000000e008", "network", supervised("idle", "", 510000, nil)},
		{"f00000000e00e", "long-task", func(t *testing.T, fx *fixture) {
			ctx := context.Background()
			fx.status = "idle"
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			_, _ = fx.st.AddProject(ctx, "hand", handRepo, "")
			task, _ := fx.st.AddTask(ctx, "hand", "Investigate why the nightly integration pipeline fails on the arm64 macOS runner with an opaque linker error after the toolchain bump and whether the cache key needs the compiler version too", "Goal: "+strings.Repeat("make the pipeline reliable without disabling the arm64 job, ", 12)+"\n\nhttps://example.com/"+strings.Repeat("very-long-unbreakable-path-segment/", 8))
			_, _ = fx.st.Transition(ctx, task.ID, state.StatusActive)
			_, _ = fx.st.SetPlan(ctx, task.ID, "1. reproduce on the runner\n2. bisect the toolchain bump\n3. fix the cache key\n\n| step | owner | note |\n|---|---|---|\n| 1 | a1 | "+strings.Repeat("long note ", 20)+" |")
			a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
			_, _ = fx.st.AddReport(ctx, a.ID, state.ReportDone, "Fixed the cache key and added the compiler version to it\n\n"+strings.Repeat("Details of the investigation, in a paragraph that keeps going. ", 20)+"\n\nPR: https://github.com/example/hand/pull/9999")
			_, _ = fx.st.Ask(ctx, task.ID, "Which of these do you want?\n\n1. Keep the arm64 job and add a retry\n2. Move the arm64 job to a nightly schedule\n3. Drop the arm64 job until the toolchain settles\n\n"+strings.Repeat("Context paragraph that explains the trade-off at length. ", 12))
		}},
		{"f00000000e00f", "deliverables", func(t *testing.T, fx *fixture) {
			ctx := context.Background()
			fx.status = "idle"
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			claudeChat(t, fx, 510000)
			deliver := func(title, end, status, body string) state.Task {
				task := active(t, fx.st, title)
				a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
				if _, err := fx.st.AddReport(ctx, a.ID, status, body); err != nil {
					t.Fatal(err)
				}
				if end != state.AttemptRunning {
					if _, err := fx.st.EndAttempt(ctx, a.ID, end, ""); err != nil {
						t.Fatal(err)
					}
				}
				return task
			}
			deliver("Phone screenshots for the board audit", state.AttemptStopped, state.ReportDone, "Shots taken.\n\nDelivered: /very/long/absolute/path/fleet-example/shots/t255/phone-390-dark-needs-blocked-after.png")
			deliver("Fix login cookie", state.AttemptRunning, state.ReportProgress, "PR open.\n\nDelivered: https://github.com/example/hand/pull/42")
			deliver("Port the importer", state.AttemptRunning, state.ReportProgress, "MR open.\n\nDelivered: https://gitlab.example.com/group/sub/importer/-/merge_requests/17")
			deliver("Write the design note", state.AttemptExited, state.ReportDone, "Note written.\n\nDelivered: https://docs.example.com/notes/board-design-2026-10")
			deliver("Ship the quarterly board report with every artefact, the PR, the MR and the published note", state.AttemptExited, state.ReportDone, "All four done.\n\n- Delivered: https://github.com/example/hand/pull/43\n- Delivered: https://gitlab.example.com/group/sub/importer/-/merge_requests/18\n- Delivered: https://docs.example.com/notes/quarterly-board-report-2026-q3\n- Delivered: /very/long/absolute/path/fleet-example/research/quarterly-board-report-2026-q3/final-report.md")
			deliver("Windows export", state.AttemptExited, state.ReportDone, "Exported.\n\nDelivered: C:\\Users\\example\\Documents\\reports\\quarterly-board-audit-final-version-2.pdf")
			for _, d := range []string{"/very/long/absolute/path/fleet-example/research/finished-task-output/summary-of-findings.md", "https://github.com/example/hand/pull/44", "https://docs.example.com/notes/finished"} {
				done := deliver("Archive me: finished with "+d[len(d)-12:], state.AttemptExited, state.ReportDone, "Finished.\n\nDelivered: "+d)
				rs, _ := fx.st.Reports(ctx, state.ReportFilter{TaskID: done.ID}, 10)
				for _, r := range rs {
					if _, err := fx.st.AckReport(ctx, r.ID, "operator"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := fx.st.Transition(ctx, done.ID, state.StatusDone); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = fx.st.Ask(ctx, 6, "Keep the Windows export as PDF?\n\n1. Yes\n2. No, also attach the CSV")
			if d, err := fx.st.Ask(ctx, 4, "Publish the design note outside the team?\n\n1. Yes, publish it\n2. No, keep it internal"); err == nil {
				_, _ = fx.st.Answer(ctx, d.ID, "2. Keep it internal until the board redesign lands.", "operator")
			}
			for _, title := range []string{"Inbox item without a deliverable", "Another inbox item"} {
				_, _ = fx.st.AddTask(ctx, "hand", title, "")
			}
		}},
		{"f00000000e010", "busy", func(t *testing.T, fx *fixture) {
			ctx := context.Background()
			fx.status = "working"
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			baseTasks(t, fx)
			work := []string{"Bump the Go toolchain to 1.26.5 and refresh the module cache key", "Port the importer to the new state API", "Add a render check for the board", "Write the release notes for 0.9", "Investigate the slow SSE reconnect on Safari", "Tidy the CLI help for hand task", "Pin the luvus version in CI"}
			for i, title := range work {
				task := active(t, fx.st, title)
				switch i {
				case 3:
					attempt(t, fx.st, task.ID, state.AttemptFailed, "exited 2: go vet ./... found 3 issues in internal/state")
				case 5:
					a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
					_, _ = fx.st.AddReport(ctx, a.ID, state.ReportDone, "CLI help tidied\n\nDelivered: https://github.com/example/hand/pull/51")
				case 6:
					_, _ = fx.st.Ask(ctx, task.ID, "Pin luvus to 0.14.2 or track the latest minor?\n\n1. Pin 0.14.2\n2. Track 0.14.x")
				default:
					a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
					if i == 1 {
						_, _ = fx.st.AddReport(ctx, a.ID, state.ReportProgress, "Halfway: the reader is ported, the writer is next")
					}
				}
			}
			for _, title := range []string{"Retry policy for flaky launches", "Board: search every task", "Archive old worktrees weekly", "Document the wake kinds", "Explain the context gauge in the help", "Rename hand-next to hand in the docs", "Check the Windows pipe address", "Add a dark favicon"} {
				_, _ = fx.st.AddTask(ctx, "hand", title, "")
			}
			for _, title := range []string{"Fix the composer hint wrap", "Move the rail to 1024 px", "Keep select values across refreshes"} {
				done := active(t, fx.st, title)
				a := attempt(t, fx.st, done.ID, state.AttemptRunning, "")
				r, _ := fx.st.AddReport(ctx, a.ID, state.ReportDone, "Done.\n\nDelivered: https://github.com/example/hand/pull/49")
				_, _ = fx.st.AckReport(ctx, r.ID, "operator")
				_, _ = fx.st.EndAttempt(ctx, a.ID, state.AttemptExited, "")
				_, _ = fx.st.Transition(ctx, done.ID, state.StatusDone)
			}
			claudeLog(t, fx, append(chatLines(t, fx, 610000)[:23], rec("user", ago(480), "ok. also bump the toolchain and write the release notes", 0), rec("assistant", ago(478), "Captured both: t7 (toolchain) and t10 (release notes). t10 failed vet on its first run; I will retry after t7 lands since it touches the same module cache.", 0), rec("user", ago(31), "[hand v1 wake]\ndecision.answered d1 2 (t1 \"Fix login\")\nattempt.failed a7 exited 2 (t10 \"Write the release notes for 0.9\")", 0), rec("assistant", ago(12), longReply, 610000)))
			_, _ = fx.st.AddSupervisorInput(ctx, "when t7 lands, rerun t10")
		}},
		{"f00000000e016", "screen", func(t *testing.T, fx *fixture) {
			fx.screen = "Which file should I change?\n◉ internal/board/board.go\n◯ internal/board/supervisor.go\n◯ internal/board/queue.go\nselect · n note · ↑/↓ move · esc cancel"
			supervised("blocked", "Which file should I change?", 640000, nil)(t, fx)
		}},
		{"f00000000e012", "documents", func(t *testing.T, fx *fixture) {
			ctx := context.Background()
			fx.status = "idle"
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			home := fx.options.Home
			outside := docTree(t, home)
			for n := 1; ; n++ {
				task := active(t, fx.st, fmt.Sprintf("Earlier task %d", n))
				_, _ = fx.st.Transition(ctx, task.ID, state.StatusDone)
				if task.ID >= 285 {
					break
				}
			}
			notes := active(t, fx.st, "Write the export notes")
			na := attempt(t, fx.st, notes.ID, state.AttemptRunning, "")
			nr, _ := fx.st.AddReport(ctx, na.ID, state.ReportDone, "Notes written.\n\nDelivered: "+home+"/reports/t286/notes.md")
			_, _ = fx.st.AckReport(ctx, nr.ID, "operator")
			_, _ = fx.st.EndAttempt(ctx, na.ID, state.AttemptExited, "")
			_, _ = fx.st.Transition(ctx, notes.ID, state.StatusDone)
			design := active(t, fx.st, "Design the nightly export pipeline")
			da := attempt(t, fx.st, design.ID, state.AttemptRunning, "")
			_, _ = fx.st.AddReport(ctx, da.ID, state.ReportDone, "Design written\n\nThe design is in `"+home+"/reports/t287/design.md`, with the API notes beside it. The run log of the dry run is "+home+"/reports/t287/run.log.\n\nDelivered: "+home+"/reports/t287/design.md")
			_, _ = fx.st.EndAttempt(ctx, da.ID, state.AttemptExited, "")
			_, _ = fx.st.Ask(ctx, design.ID, "Approve the export design for t287?\n\nSummary of the t287 proposal ("+home+"/reports/t287/design.md):\n\n- one export job per night, safe to retry\n- one CSV per region, and the manifest written last\n- no streaming export\n\n1. Approve the design as written (recommended)\n2. Revise it first: add the 30-day backfill\n3. Other (write your own)")
			shots := active(t, fx.st, "Screenshots of the export screen")
			sa := attempt(t, fx.st, shots.ID, state.AttemptRunning, "")
			_, _ = fx.st.AddReport(ctx, sa.ID, state.ReportDone, "Six screenshots, light and dark.\n\nDelivered: "+home+"/shots/t288/")
			tune := active(t, fx.st, "Tune the export worker")
			ta := attempt(t, fx.st, tune.ID, state.AttemptExited, "")
			_, _ = fx.st.AddReport(ctx, ta.ID, state.ReportDone, "Tuned.\n\nDelivered: /tmp/hand-fixture-gone/worktrees/f00000000e012/t289-a4/docs/tuning.md")
			refused := active(t, fx.st, "Check what the Board refuses")
			_, _ = fx.st.Ask(ctx, refused.ID, "Which of these can the Board open?\n\nEach line names a path; only the ones the Board may serve become links.\n\n- a file outside the fleet: "+outside+"/notes.md\n- a link in reports/ that leads out: "+home+"/reports/t287/escape/id_ed25519\n- a link in reports/ to Hand's database: "+home+"/reports/t287/db-link\n- a project clone: "+home+"/projects/app/README.md\n- a dot folder: "+home+"/.secret/token\n- a 3 MiB log: "+home+"/reports/t287/huge.log\n- a binary file named .md: "+home+"/reports/t287/binary.md\n- a name with a space and an accent: `"+home+"/reports/t287/résumé notes.md`\n- a key in the home folder: `~/.ssh/id_ed25519`\n\n1. Only the ones inside the fleet folder (recommended)\n2. All of them")
			claudeLog(t, fx, append(chatLines(t, fx, 420000)[:23], rec("user", ago(30), "is the t287 design ready?", 0), rec("assistant", ago(29), "Yes. a3 finished it: `"+home+"/reports/t287/design.md`. It covers the nightly job, the manifest and the risks; d1 asks you to approve it. The screenshots for t288 are in "+home+"/shots/t288/ and the old notes are at "+home+"/reports/t286/notes.md.", 420000)))
		}},
		{"f00000000e015", "phone", func(t *testing.T, fx *fixture) {
			fx.options.Iroh = func() sidecar.Status {
				return sidecar.Status{State: sidecar.Ready, ID: strings.Repeat("0123456789abcdef", 4), Relay: "https://relay.example/"}
			}
		}},
		{"f00000000e014", "cleanup", supervised("idle", "", 100000, func(t *testing.T, fx *fixture) {
			ctx := context.Background()
			task := active(t, fx.st, "Port the importer to the new state API")
			a := attempt(t, fx.st, task.ID, state.AttemptStopped, "stopped by operator")
			if err := fx.st.KeepAttempt(ctx, a.ID, "it has uncommitted changes"); err != nil {
				t.Fatal(err)
			}
			policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}},"retention":{"warn_size":"1MB","warn_days":"30d"}}`
			if err := os.WriteFile(filepath.Join(fx.options.Home, harness.PolicyFile), []byte(policy), 0o644); err != nil {
				t.Fatal(err)
			}
			for rel, age := range map[string]int{"reports/t12/design.md": 3, "shots/t9/phone-390-dark.png": 40, "shots/t9/phone-1440-light.png": 41} {
				path := filepath.Join(fx.options.Home, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, make([]byte, 700<<10), 0o644); err != nil {
					t.Fatal(err)
				}
				when := frozen.AddDate(0, 0, -age)
				if err := os.Chtimes(path, when, when); err != nil {
					t.Fatal(err)
				}
			}
		})},
		{"f00000000e013", "ask", supervised("idle", "", 420000, func(t *testing.T, fx *fixture) {
			askAttempt(t, fx, "Clean the build folder")
			fx.screen = capture(t, "long16")
		})},
		{"f00000000e011", "decisions", func(t *testing.T, fx *fixture) {
			ctx := context.Background()
			fx.status = "idle"
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			claudeChat(t, fx, 420000)
			ask := func(title, q string) state.Decision {
				task := active(t, fx.st, title)
				d, err := fx.st.Ask(ctx, task.ID, q)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			ask("Pin the luvus version in CI", "Pin luvus to which version?\n\nCI broke when luvus 0.15 changed the output of `luvus session list`. Pick how CI follows luvus from now on.\n\n1. Pin 0.14.2 — reproducible builds; we bump it by hand (recommended)\n2. Track 0.14.x — patch fixes arrive by themselves\n3. Track the latest release — newest features, and CI may break again\n4. Vendor luvus — full control, 40 MB more in the repository")
			ask("Merge the CLI help PR", "Merge PR 51 now?\n\nCI is green and both reviews are clean on the head.\n\n1. Yes\n2. No")
			ask("Investigate the nightly arm64 failure", "How should the nightly arm64 job run until the toolchain settles?\n\n1. Keep the arm64 job in the nightly run and add one automatic retry with a fresh module cache, so a flaky linker run no longer turns the whole night red, at the cost of up to 25 more minutes per night\n2. Move the arm64 job to a weekly run on Sunday nights and report it as a separate check, so the nightly stays green but an arm64 regression can go unseen for up to six days\n3. Drop the arm64 job from CI until the Go toolchain ships the linker fix, and run it by hand before each release; nothing arm64-specific is checked in between")
			ask("Name the next release", "What should the 0.12 release be called?\n\nThe board redesign is the headline of this release.\n\n1. Desk\n2. Console\n3. Other (write your own)")
			ask("Rotate the board token", "Rotate the board token now?\n\nEvery open board tab logs out and needs a fresh `hand open` link.\n\n1. Rotate now\n2. Wait until Friday")
			if d := ask("Publish the design note", "Publish the design note outside the team?\n\n1. Yes, publish it\n2. No, keep it internal"); d.ID > 0 {
				if _, err := fx.st.Answer(ctx, d.ID, "2. No, keep it internal\n\nKeep it internal until the board redesign lands.", "operator (board)"); err != nil {
					t.Fatal(err)
				}
			}
			for _, title := range []string{"Write the release notes for 0.12", "Check the Windows pipe address"} {
				_, _ = fx.st.AddTask(ctx, "hand", title, "")
			}
		}},
	}
}

func TestRenderScenes(t *testing.T) {
	addr := os.Getenv("SCENES_ADDR")
	if addr == "" {
		t.Skip("SCENES_ADDR not set")
	}
	codex := t.TempDir()
	_ = os.WriteFile(filepath.Join(codex, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"}]},{"slug":"gpt-6","supported_reasoning_levels":[{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]}]}`), 0o644)
	policy, _ := json.Marshal(harness.StarterPolicy)

	handlers := map[string]http.Handler{}
	var links []board.FleetLink
	for _, sc := range scenes() {
		fx := newFixture(t)
		fx.st = openNow(t, sc.id)
		fx.options.Harness.CodexHome = codex
		fx.options.Home = sceneHome(t, sc.id)
		fx.options.Base = "/" + sc.id
		fx.options.Tick = 2 * time.Second
		fx.options.Now = func() time.Time { return frozen }
		fx.options.Controls = sc.name != "network"
		_ = os.WriteFile(filepath.Join(fx.options.Home, harness.PolicyFile), policy, 0o644)
		sc.build(t, fx)
		control := fx.options.Control
		fx.options.Control = func(ctx context.Context, args ...string) (string, error) {
			if len(args) == 4 && args[0] == "supervisor" && args[1] == "send" {
				_, err := fx.st.AddSupervisorInput(ctx, args[3])
				return "", err
			}
			return control(ctx, args...)
		}
		if _, err := fx.st.RenameFleet(context.Background(), sc.name); err != nil {
			t.Fatal(err)
		}
		handlers[sc.id] = board.New(fx.st, token, fx.options)
		links = append(links, board.FleetLink{ID: sc.id, Name: sc.name})
	}
	host := board.NewHost(board.HostOptions{
		Loopback: true,
		Resolve:  func(id string) (string, error) { return "/h/" + id, nil },
		Open: func(id, home string) (http.Handler, io.Closer, error) {
			h, ok := handlers[id]
			if !ok {
				return nil, nil, fmt.Errorf("%w: fleet %s", state.ErrNotFound, id)
			}
			return h, closer(func() error { return nil }), nil
		},
		List: func() ([]board.FleetLink, error) { return links, nil },
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: host}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	if err := os.WriteFile(os.Getenv("SCENES_READY"), []byte(token), 0o644); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(os.Getenv("SCENES_STOP")); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
