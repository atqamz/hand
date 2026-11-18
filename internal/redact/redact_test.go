package redact

import (
	"strings"
	"testing"
	"time"
)

func TestTextRemovesEachKnownShape(t *testing.T) {
	cases := []struct{ name, in, kind string }{
		{"ghp", "ghp_" + strings.Repeat("a", 36), "github-token"},
		{"gho long", "gho_" + strings.Repeat("B1", 30), "github-token"},
		{"pat", "github_pat_" + strings.Repeat("c", 22) + "_x", "github-token"},
		{"gitlab", "glpat-" + strings.Repeat("d", 20), "gitlab-token"},
		{"sk", "sk-" + strings.Repeat("e", 24), "api-key"},
		{"slack", "xoxb-" + "123456789-abcdefghij", "slack-token"},
		{"aws", "AKIA" + strings.Repeat("Q", 16), "aws-key"},
		{"asia", "ASIA" + strings.Repeat("7", 16), "aws-key"},
		{"google", "AIza" + strings.Repeat("f", 35), "google-key"},
		{"jwt", "eyJ" + strings.Repeat("a", 10) + ".eyJ" + strings.Repeat("b", 10) + "." + strings.Repeat("c", 10), "jwt"},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("Zm9v", 20) + "\n-----END RSA PRIVATE KEY-----", "private-key"},
		{"bearer", "Bearer " + strings.Repeat("g", 24), "bearer"},
	}
	for _, c := range cases {
		got := Text("before " + c.in + " after")
		if got != "before [redacted:"+c.kind+"] after" {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}

func TestTextRemovesAPrivateKeyWithoutAnEndLineToTheEnd(t *testing.T) {
	got := Text("see\n-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("Zm9v", 20) + "\nmore")
	if got != "see\n[redacted:private-key]" {
		t.Fatalf("%q", got)
	}
}

func TestTextKeepsTheSchemeHostAndNameButNotTheSecret(t *testing.T) {
	cases := map[string]string{
		"git clone https://bob:" + strings.Repeat("p", 14) + "@example.com/r.git": "git clone https://[redacted:url-credentials]@example.com/r.git",
		"api_key = " + strings.Repeat("k", 20):                                    "api_key = [redacted:assignment]",
		`"password": "` + strings.Repeat("w", 16) + `"`:                           `"password": "[redacted:assignment]"`,
		"MY_SECRET=" + strings.Repeat("s", 12):                                    "MY_SECRET=[redacted:assignment]",
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestTextLeavesNearMissesAlone(t *testing.T) {
	for _, in := range []string{
		"0a7311bc9f1e2d3c4b5a69788776655443322110",
		"123e4567-e89b-12d3-a456-426614174000",
		"the task-force-" + strings.Repeat("x", 30),
		"desk-" + strings.Repeat("y", 30),
		"tokens: 131776",
		"token: 1234567890123",
		"Bearer token",
		"/home/someone/fleet/memory/last-turns.md",
		"password: short",
		"[redacted:jwt]",
	} {
		if got := Text(in); got != in {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestTextIsIdempotent(t *testing.T) {
	in := "token: eyJaaaaaaaaaa.bbbbbbbbbbb.ccccccccccc and ghp_" + strings.Repeat("a", 36) + " https://u:pppppp@h/ secret=" + strings.Repeat("z", 20)
	once := Text(in)
	if twice := Text(once); twice != once {
		t.Fatalf("%q then %q", once, twice)
	}
	if strings.Contains(once, "ghp_") || strings.Contains(once, "pppppp") {
		t.Fatalf("%q", once)
	}
}

func TestTextHandlesALargeInputInBoundedTime(t *testing.T) {
	in := strings.Repeat("token=short and plain words, ghp_ x -----BEGIN not a key\n", 18000)
	start := time.Now()
	Text(in)
	if d := time.Since(start); d > time.Minute {
		t.Fatalf("took %s", d)
	}
}
