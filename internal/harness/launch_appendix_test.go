package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/state"
)

func TestAppendPromptToBriefRejectsStaleLaunchAppendix(t *testing.T) {
	for _, name := range Names() {
		for _, change := range []struct {
			name  string
			apply func(*Options)
		}{
			{name: "report channel", apply: func(o *Options) { o.ReportPath += ".next" }},
			{name: "revoke ship authority", apply: func(o *Options) { o.Kind = state.KindScout }},
			{name: "remove task authority", apply: func(o *Options) { o.Kind = "" }},
			{name: "execution guidance", apply: func(o *Options) { o.ExecutionClass = brief.ExecutionClassStandard }},
			{name: "front matter guidance", apply: func(o *Options) { o.BriefHasFrontMatter = false }},
		} {
			t.Run(name+"/"+change.name, func(t *testing.T) {
				body := []byte("---\nmodel: unchanged\n---\nInvestigate the problem.\n")
				dir := t.TempDir()
				o := Options{
					Brief: filepath.Join(dir, "brief.md"), ReportPath: filepath.Join(dir, "report.status"),
					Kind: state.KindShip, ExecutionClass: brief.ExecutionClassMechanical, BriefHasFrontMatter: true,
				}
				if err := os.WriteFile(o.Brief, body, 0o600); err != nil {
					t.Fatal(err)
				}
				previous := name
				if name != Grok && name != Pi {
					previous = Pi
				}
				if err := AppendPromptToBrief(previous, o); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(o.Brief)
				if err != nil {
					t.Fatal(err)
				}
				change.apply(&o)
				err = AppendPromptToBrief(name, o)
				if err == nil || !strings.Contains(err.Error(), "rewrite the supervisor brief") {
					t.Fatalf("changed launch accepted stale instructions: %v", err)
				}
				after, err := os.ReadFile(o.Brief)
				if err != nil || !bytes.Equal(after, before) {
					t.Fatalf("refusal changed brief: error=%v before=%q after=%q", err, before, after)
				}

				if err := os.WriteFile(o.Brief, body, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := AppendPromptToBrief(name, o); err != nil {
					t.Fatalf("fresh supervisor brief did not recover: %v", err)
				}
				statement, err := launchStatement(o)
				if err != nil {
					t.Fatal(err)
				}
				want := string(body)
				if name == Grok || name == Pi {
					want += fmt.Sprintf("\n\n---\n\n%s\n\n%s\n", brief.AppendMarker, statement)
				} else {
					spec, err := Build(name, o)
					if err != nil || !strings.Contains(strings.Join(spec.Args, " "), statement) {
						t.Fatalf("recovered argument contract = %+v, error=%v", spec, err)
					}
				}
				after, err = os.ReadFile(o.Brief)
				if err != nil || string(after) != want {
					t.Fatalf("recovered appendix is not the exact current contract: %v\n%s", err, after)
				}
			})
		}
	}
}

func TestAppendPromptToBriefRejectsEditedAndDuplicateAppendices(t *testing.T) {
	for _, name := range Names() {
		for _, duplicate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/duplicate=%t", name, duplicate), func(t *testing.T) {
				dir := t.TempDir()
				o := Options{Brief: filepath.Join(dir, "brief.md"), ReportPath: filepath.Join(dir, "report.status")}
				statement, err := launchStatement(o)
				if err != nil {
					t.Fatal(err)
				}
				appendix := fmt.Sprintf("\n\n---\n\n%s\n\n%s\n", brief.AppendMarker, statement)
				body := "Task instructions." + appendix + "Operator follow-up: preserve this text.\n"
				if duplicate {
					body = "Task instructions." + appendix + appendix
				}
				if err := os.WriteFile(o.Brief, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := AppendPromptToBrief(name, o); err == nil {
					t.Fatal("accepted an edited or duplicated launch appendix")
				}
				got, err := os.ReadFile(o.Brief)
				if err != nil || string(got) != body {
					t.Fatalf("refusal discarded brief text: error=%v body=%q", err, got)
				}
			})
		}
	}
}

func TestAppendPromptToBriefMarkerMentionDoesNotSuppressContract(t *testing.T) {
	for _, name := range []string{Grok, Pi} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			o := Options{Brief: filepath.Join(dir, "brief.md"), ReportPath: filepath.Join(dir, "report.status")}
			body := "Investigate this literal marker: " + brief.AppendMarker + "\n"
			if err := os.WriteFile(o.Brief, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := AppendPromptToBrief(name, o); err != nil {
				t.Fatal(err)
			}
			statement, err := launchStatement(o)
			if err != nil {
				t.Fatal(err)
			}
			want := body + fmt.Sprintf("\n\n---\n\n%s\n\n%s\n", brief.AppendMarker, statement)
			got, err := os.ReadFile(o.Brief)
			if err != nil || string(got) != want {
				t.Fatalf("marker mention suppressed the current launch contract: error=%v body=%q", err, got)
			}
			stamp := time.Unix(1_000_000_000, 0)
			if err := os.Chtimes(o.Brief, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(o.Brief)
			if err != nil {
				t.Fatal(err)
			}
			if err := AppendPromptToBrief(name, o); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(o.Brief)
			if err != nil {
				t.Fatal(err)
			}
			if !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() || after.Size() != before.Size() {
				t.Fatal("exact appendix replay rewrote the brief")
			}
		})
	}
}

func TestAppendPromptToBriefHarnessSwitchPreservesExactAppendix(t *testing.T) {
	for _, previous := range []string{Grok, Pi} {
		for _, next := range Names() {
			t.Run(previous+"/"+next, func(t *testing.T) {
				dir := t.TempDir()
				o := Options{Brief: filepath.Join(dir, "brief.md"), ReportPath: filepath.Join(dir, "report.status"), Kind: state.KindScout}
				if err := os.WriteFile(o.Brief, []byte("Investigate only.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := AppendPromptToBrief(previous, o); err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(1_000_000_000, 0)
				if err := os.Chtimes(o.Brief, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(o.Brief)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(o.Brief)
				if err != nil {
					t.Fatal(err)
				}
				if err := AppendPromptToBrief(next, o); err != nil {
					t.Fatal(err)
				}
				if _, err := Build(next, o); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(o.Brief)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("harness switch changed exact appendix: %v", err)
				}
				got, err := os.Stat(o.Brief)
				if err != nil {
					t.Fatal(err)
				}
				if !got.ModTime().Equal(info.ModTime()) || got.Mode() != info.Mode() {
					t.Fatal("harness switch rewrote exact appendix")
				}
			})
		}
	}
}

func TestAppendPromptToBriefArgumentHarnessRefusesInspectionFailure(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode, Antigravity} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := AppendPromptToBrief(name, Options{Brief: dir}); err == nil {
				t.Fatal("uninspectable brief accepted as having no stale appendix")
			}
			missing := filepath.Join(dir, "missing.md")
			if err := AppendPromptToBrief(name, Options{Brief: missing}); err != nil {
				t.Fatalf("missing argument-only brief changed existing no-append behavior: %v", err)
			}
			if _, err := os.Stat(missing); !os.IsNotExist(err) {
				t.Fatalf("argument-only preparation created a missing brief: %v", err)
			}
		})
	}
}

func TestAppendPromptToBriefRejectsChangedLineEndings(t *testing.T) {
	for _, name := range Names() {
		for _, variant := range []string{"CRLF", "mixed", "CRLF before current LF", "unchanged options"} {
			t.Run(name+"/"+variant, func(t *testing.T) {
				dir := t.TempDir()
				o := Options{Brief: filepath.Join(dir, "brief.md"), ReportPath: filepath.Join(dir, "old.status"), Kind: state.KindShip}
				body := "Préserve the operator's text.\n"
				if err := os.WriteFile(o.Brief, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := AppendPromptToBrief(Grok, o); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(o.Brief)
				if err != nil {
					t.Fatal(err)
				}
				if variant != "unchanged options" {
					o.ReportPath = filepath.Join(dir, "current.status")
					o.Kind = state.KindScout
				}
				before := strings.ReplaceAll(string(data), "\n", "\r\n")
				switch variant {
				case "mixed":
					before = strings.Replace(string(data), "\n\n---\n\n", "\n\r\n---\n\r\n", 1)
				case "CRLF before current LF":
					current, err := launchStatement(o)
					if err != nil {
						t.Fatal(err)
					}
					before += fmt.Sprintf("\n\n---\n\n%s\n\n%s\n", brief.AppendMarker, current)
				}
				if err := os.WriteFile(o.Brief, []byte(before), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := AppendPromptToBrief(name, o); err == nil || !strings.Contains(err.Error(), "rewrite the supervisor brief") {
					t.Fatalf("changed line endings hid a launch appendix: %v", err)
				}
				after, err := os.ReadFile(o.Brief)
				if err != nil || string(after) != before {
					t.Fatalf("refusal changed brief bytes: %v", err)
				}
			})
		}
	}
}
