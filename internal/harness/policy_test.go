package harness

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, PolicyFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestLoadPolicyAndPickAProfile(t *testing.T) {
	home := writePolicy(t, `{"profiles":{"deep":{"harness":"claude","model":"opus","effort":"xhigh"},"quick":{"harness":"codex","model":"gpt-6-luna","effort":"low"}}}`)
	p, err := LoadPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Names(); !slices.Equal(got, []string{"deep", "quick"}) {
		t.Fatalf("names = %q", got)
	}
	if s, err := p.Profile("deep"); err != nil || s != (Spec{"claude", "opus", "xhigh"}) {
		t.Fatalf("deep = %+v, %v", s, err)
	}
	if _, err := p.Profile("fast"); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "deep, quick") {
		t.Fatalf("unknown profile err = %v", err)
	}
}

func TestLoadPolicyRejectsBadFiles(t *testing.T) {
	for _, body := range []string{
		`{`,
		`{"profiles":{}}`,
		`{"profiles":{"Deep":{"harness":"claude","model":"opus","effort":"high"}}}`,
		`{"profiles":{"deep":{"harness":"claude","model":"opus","effort":"high","temperature":1}}}`,
	} {
		if _, err := LoadPolicy(writePolicy(t, body)); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), PolicyFile) {
			t.Fatalf("policy %q err = %v", body, err)
		}
	}
	if _, err := LoadPolicy(t.TempDir()); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "hand init") {
		t.Fatalf("missing policy err = %v", err)
	}
}

func TestStarterPolicyIsWrittenOnce(t *testing.T) {
	home := t.TempDir()
	if created, err := WriteStarterPolicy(home); err != nil || !created {
		t.Fatalf("first write = %v, %v", created, err)
	}
	p, err := LoadPolicy(home)
	if err != nil || !slices.Equal(p.Names(), []string{"deep", "default", "quick"}) {
		t.Fatalf("starter = %+v, %v", p, err)
	}
	edited := `{"profiles":{"mine":{"harness":"claude","model":"haiku","effort":"low"}}}`
	if err := os.WriteFile(filepath.Join(home, PolicyFile), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if created, err := WriteStarterPolicy(home); err != nil || created {
		t.Fatalf("second write = %v, %v", created, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, PolicyFile)); string(b) != edited {
		t.Fatalf("edited policy overwritten: %s", b)
	}
}
