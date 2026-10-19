package harness

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
		`{"profiles":{"deep":{"harness":"claude","model":"opus","effort":"high"}}}{`,
		`{"profiles":{"deep":{"harness":"claude","model":"opus","effort":"high"}}} {"profiles":{}}`,
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
	if err != nil || !slices.Equal(p.Names(), []string{"deep", "default", "high", "max", "quick"}) {
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

func TestPolicyAcceptsTheSupervisorBlock(t *testing.T) {
	profiles := `"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}}`
	if p, err := LoadPolicy(writePolicy(t, `{`+profiles+`,"supervisor":{"autoresume":true}}`)); err != nil || !p.Supervisor.Autoresume {
		t.Fatalf("policy = %+v, %v", p, err)
	}
	if p, err := LoadPolicy(writePolicy(t, `{`+profiles+`}`)); err != nil || p.Supervisor.Autoresume {
		t.Fatalf("default policy = %+v, %v", p, err)
	}
	if _, err := LoadPolicy(writePolicy(t, `{`+profiles+`,"supervisor":{"x":1}}`)); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("unknown supervisor field err = %v", err)
	}
}

func TestPolicyAcceptsANotifyCommand(t *testing.T) {
	profiles := `"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}}`
	if p, err := LoadPolicy(writePolicy(t, `{`+profiles+`,"notify":["ntfy","publish","topic"]}`)); err != nil || !slices.Equal(p.Notify, []string{"ntfy", "publish", "topic"}) {
		t.Fatalf("policy = %+v, %v", p, err)
	}
	if p, err := LoadPolicy(writePolicy(t, `{`+profiles+`}`)); err != nil || p.Notify != nil {
		t.Fatalf("default policy = %+v, %v", p, err)
	}
	if _, err := LoadPolicy(writePolicy(t, `{`+profiles+`,"notify":"ntfy publish topic"}`)); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("string notify err = %v", err)
	}
}

func TestPolicyReadsTheStallSetting(t *testing.T) {
	profiles := `"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}}`
	for body, want := range map[string]Stall{
		`{` + profiles + `}`:                                        DefaultStall,
		`{` + profiles + `,"stall":"off"}`:                          {Off: true},
		`{` + profiles + `,"stall":{"after":"10m"}}`:                {After: 10 * time.Minute, Every: time.Hour},
		`{` + profiles + `,"stall":{"after":"5m","every":"30m"}}`:   {After: 5 * time.Minute, Every: 30 * time.Minute},
		`{` + profiles + `,"stall":{"every":"2h","after":"1h30m"}}`: {After: 90 * time.Minute, Every: 2 * time.Hour},
	} {
		if p, err := LoadPolicy(writePolicy(t, body)); err != nil || p.Stall != want {
			t.Fatalf("policy %s: stall = %+v, %v; want %+v", body, p.Stall, err, want)
		}
	}
	for body, want := range map[string]string{
		`"on"`:                         `stall must be "off" or {"after": "20m", "every": "60m"}`,
		`null`:                         `stall must be "off"`,
		`5`:                            `stall must be "off"`,
		`{"after":20}`:                 `stall must be "off"`,
		`{"after":"20m","when":"now"}`: `unknown field "when"`,
		`{"after":"soon"}`:             `stall.after is "soon"; give a duration from 1m to 24h, like "20m"`,
		`{"after":"30s"}`:              `stall.after is "30s"`,
		`{"every":"25h"}`:              `stall.every is "25h"`,
		`{"every":""}`:                 `stall.every is ""`,
	} {
		_, err := LoadPolicy(writePolicy(t, `{`+profiles+`,"stall":`+body+`}`))
		if !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), PolicyFile) || !strings.Contains(err.Error(), want) {
			t.Fatalf("stall %s err = %v, want %q", body, err, want)
		}
	}
}

func TestStallDelaysDoubleUpToEvery(t *testing.T) {
	for _, c := range []struct {
		stall Stall
		want  []time.Duration
	}{
		{DefaultStall, []time.Duration{20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour}},
		{Stall{After: 5 * time.Minute, Every: 30 * time.Minute}, []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 30 * time.Minute, 30 * time.Minute}},
		{Stall{After: 2 * time.Hour, Every: time.Hour}, []time.Duration{2 * time.Hour, time.Hour, time.Hour}},
	} {
		for n, want := range c.want {
			if got := c.stall.Delay(n); got != want {
				t.Fatalf("%+v Delay(%d) = %v, want %v", c.stall, n, got, want)
			}
		}
	}
	if got := (Stall{After: 24 * time.Hour, Every: 24 * time.Hour}).Delay(500); got != 24*time.Hour {
		t.Fatalf("Delay after many wakes = %v", got)
	}
}

func TestPolicyReadsTheInboxSetting(t *testing.T) {
	profiles := `"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}}`
	for body, want := range map[string]time.Duration{
		`{` + profiles + `}`:                         0,
		`{` + profiles + `,"inbox":"off"}`:           0,
		`{` + profiles + `,"inbox":{}}`:              0,
		`{` + profiles + `,"inbox":{"keep":"off"}}`:  0,
		`{` + profiles + `,"inbox":{"keep":"30d"}}`:  30 * 24 * time.Hour,
		`{` + profiles + `,"inbox":{"keep":"1d"}}`:   24 * time.Hour,
		`{` + profiles + `,"inbox":{"keep":"365d"}}`: 365 * 24 * time.Hour,
	} {
		if p, err := LoadPolicy(writePolicy(t, body)); err != nil || p.Inbox.Keep != want {
			t.Fatalf("policy %s: inbox = %+v, %v; want keep %v", body, p.Inbox, err, want)
		}
	}
	for body, want := range map[string]string{
		`"on"`:                        `inbox must be "off" or {"keep": "30d"}`,
		`null`:                        `inbox must be "off"`,
		`30`:                          `inbox must be "off"`,
		`{"keep":30}`:                 `inbox must be "off"`,
		`{"keep":"30d","every":"1d"}`: `unknown field "every"`,
		`{"keep":"30"}`:               `inbox.keep is "30"; give "off" or days from 1d to 9999d, like "30d"`,
		`{"keep":"0d"}`:               `inbox.keep is "0d"`,
		`{"keep":"12h"}`:              `inbox.keep is "12h"`,
		`{"keep":""}`:                 `inbox.keep is ""`,
	} {
		_, err := LoadPolicy(writePolicy(t, `{`+profiles+`,"inbox":`+body+`}`))
		if !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), PolicyFile) || !strings.Contains(err.Error(), want) {
			t.Fatalf("inbox %s err = %v, want %q", body, err, want)
		}
	}
}
