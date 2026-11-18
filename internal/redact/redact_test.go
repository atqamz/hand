package redact

import (
	"strings"
	"testing"
	"time"
)

var dashes = strings.Repeat("-", 5)

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
		{"lowercase bearer", "bearer " + strings.Repeat("g", 24), "bearer"},
		{"stripe live", "sk_live_" + strings.Repeat("h", 24), "api-key"},
		{"stripe restricted test", "rk_test_" + strings.Repeat("H", 24), "api-key"},
		{"npm", "npm_" + strings.Repeat("n", 36), "npm-token"},
		{"pgp block", dashes + "BEGIN PGP PRIVATE KEY BLOCK" + dashes + "\nlQdG\n" + dashes + "END PGP PRIVATE KEY BLOCK" + dashes, "private-key"},
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

func TestTextKeepsTheBasicSchemeButNotTheCredentials(t *testing.T) {
	cred := "dXNlcjpwYXNzd29yZDEy"
	cases := map[string]string{
		"Authorization: Basic " + cred + "==":     "Authorization: Basic [redacted:basic-auth]",
		`-H "authorization: basic ` + cred + `"`:  `-H "authorization: basic [redacted:basic-auth]"`,
		"Proxy-Authorization: Basic " + cred:      "Proxy-Authorization: Basic [redacted:basic-auth]",
		`{"Authorization": "Basic ` + cred + `"}`: `{"Authorization": "Basic [redacted:basic-auth]"}`,
		"Basic " + cred + " outside a header":     "Basic " + cred + " outside a header",
		"Authorization: Basic":                    "Authorization: Basic",
	}
	for in, want := range cases {
		got := Text(in)
		if got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
		if again := Text(got); again != got {
			t.Errorf("%q then %q", got, again)
		}
	}
}

func TestTextRemovesAQuotedMultiWordValue(t *testing.T) {
	cases := map[string]string{
		"password='two words here'":                              `password='[redacted:assignment]'`,
		`password: "correct horse battery staple"`:               `password: "[redacted:assignment]"`,
		`{"passphrase": "a long phrase, with commas"}`:           `{"passphrase": "[redacted:assignment]"}`,
		`"token": "123456789012"`:                                `"token": "123456789012"`,
		`password: "[redacted:jwt]"`:                             `password: "[redacted:jwt]"`,
		"PASSWORD='two words'":                                   "PASSWORD='two words'",
		`{"token": "short", "note": "this is plain prose here"}`: `{"token": "short", "note": "this is plain prose here"}`,
	}
	for in, want := range cases {
		got := Text(in)
		if got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
		if again := Text(got); again != got {
			t.Errorf("%q then %q", got, again)
		}
	}
}

func TestTextOnlyTakesSecretNamesNotWordsThatStartWithOne(t *testing.T) {
	long := strings.Repeat("v", 20)
	for _, in := range []string{
		"secret_santa: organise_the_party_soon",
		"token_expires_at: 2026-11-18T10:00:00Z",
		"credential_helper = osxkeychain-store-long",
		"passwordless: yes_for_the_whole_fleet",
	} {
		if got := Text(in); got != in {
			t.Errorf("%q -> %q", in, got)
		}
	}
	for in, want := range map[string]string{
		"aws_secret_access_key = " + long: "aws_secret_access_key = [redacted:assignment]",
		"AWS_SECRET_ACCESS_KEY=" + long:   "AWS_SECRET_ACCESS_KEY=[redacted:assignment]",
		"SECRET_KEY_BASE: " + long:        "SECRET_KEY_BASE: [redacted:assignment]",
		"client_secret_value=" + long:     "client_secret_value=[redacted:assignment]",
		"api_key_id: " + long:             "api_key_id: [redacted:assignment]",
		"GITHUB_TOKEN=" + long:            "GITHUB_TOKEN=[redacted:assignment]",
		"credentials: " + long:            "credentials: [redacted:assignment]",
		"private_key=" + long:             "private_key=[redacted:assignment]",
	} {
		if got := Text(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
