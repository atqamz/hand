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
	for _, name := range []string{Grok, Pi} {
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
				if err := AppendPromptToBrief(name, o); err != nil {
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
				want := string(body) + fmt.Sprintf("\n\n---\n\n%s\n\n%s\n", brief.AppendMarker, statement)
				after, err = os.ReadFile(o.Brief)
				if err != nil || string(after) != want {
					t.Fatalf("recovered appendix is not the exact current contract: %v\n%s", err, after)
				}
			})
		}
	}
}

func TestAppendPromptToBriefRejectsEditedAndDuplicateAppendices(t *testing.T) {
	for _, name := range []string{Grok, Pi} {
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
