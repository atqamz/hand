package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
)

func tailscaleMain(args []string) int {
	fakebin.Append(fakebin.Params()["log"], strings.Join(args, " "))
	return 0
}

func TestOpenPrintPrintsTheAppLinkNextToTheTokenLink(t *testing.T) {
	fx := newOpenFixture(t)
	for ref, page := range map[string]string{
		"t1":         "task/t1",
		"d1":         "decision/d1",
		"a1":         "attempt/a1",
		"r1":         "task/t1#r1",
		"supervisor": "",
		"":           "",
	} {
		args := []string{"open", "--print"}
		if ref != "" {
			args = append(args, ref)
		}
		out := fx.h.ok(args...)
		if got, want := field(out, "app"), "handboard://open/"+fx.id+"/"+page; got != want {
			t.Fatalf("open --print %q app = %q, want %q", ref, got, want)
		}
		if strings.Contains(field(out, "app"), fx.token(t)) || !strings.HasPrefix(field(out, "url"), fx.base+"/"+fx.id+"/") {
			t.Fatalf("open --print %q = %q", ref, out)
		}
	}
}

func TestNothingRunsTailscale(t *testing.T) {
	fx := newOpenFixture(t)
	log := filepath.Join(t.TempDir(), "tailscale.log")
	fakebin.Install(t, fx.h.vars["PATH"], "tailscale", "tailscale", map[string]string{"log": log})
	fx.h.ok("open", "--print", "t1")
	fx.h.ok("open", "t1")
	fx.h.run("doctor")
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("tailscale ran: %q", b)
	}
}

func TestTheAppLinkReproducesEveryCanonicalVector(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "companion-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Open struct {
			Valid []struct {
				Link   string `json:"link"`
				Parsed struct {
					Fleet  string  `json:"fleet"`
					Page   string  `json:"page"`
					Report *string `json:"report"`
				} `json:"parsed"`
			} `json:"valid"`
		} `json:"open"`
	}
	if err := json.Unmarshal(b, &v); err != nil || len(v.Open.Valid) == 0 {
		t.Fatalf("vectors: %v", err)
	}
	for _, c := range v.Open.Valid {
		frag := ""
		if c.Parsed.Report != nil {
			frag = "#" + *c.Parsed.Report
		}
		if got := cli.AppLink(c.Parsed.Fleet, "/"+c.Parsed.Page, frag); got != c.Link {
			t.Errorf("AppLink = %q, want %q", got, c.Link)
		}
	}
}
