package state

import "testing"

func TestValidatePRURLAcceptsCanonicalForm(t *testing.T) {
	if !ValidatePRURL("https://github.com/atqamz/secondhand/pull/31") {
		t.Fatal("want canonical PR URL to validate")
	}
}

func TestValidatePRURLAgainstDogfoodDoneLine(t *testing.T) {
	urls := FindPRURLs("done: PR https://github.com/atqamz/secondhand/pull/31 checks green")
	if len(urls) != 1 {
		t.Fatalf("got %+v", urls)
	}
	if !ValidatePRURL(urls[0]) {
		t.Fatalf("got invalid URL %q from dogfood fixture", urls[0])
	}
}

func TestValidatePRURLRejectsMalformed(t *testing.T) {
	cases := []string{
		"http://github.com/atqamz/secondhand/pull/31",
		"https://github.com/atqamz/secondhand/pull/31/files",
		"https://github.com/atqamz/secondhand/pull/",
		"https://github.com/atqamz/secondhand/pull/31 ",
		" https://github.com/atqamz/secondhand/pull/31",
		"https://github.com/atqamz/secondhand/issues/31",
		"https://not-github.com/atqamz/secondhand/pull/31",
		"https://github.com.evil.com/atqamz/secondhand/pull/31",
		"https://github.com/atqamz/secondhand/pull/31; rm -rf /",
		"https://github.com/atqamz/pull/31",
		"",
	}
	for _, c := range cases {
		if ValidatePRURL(c) {
			t.Errorf("want %q to be rejected", c)
		}
	}
}

func TestParsePRURLExtractsSlug(t *testing.T) {
	slug, ok := ParsePRURL("https://github.com/atqamz/secondhand/pull/31")
	if !ok || slug != "atqamz/secondhand" {
		t.Fatalf("got slug=%q ok=%v", slug, ok)
	}
}

func TestParsePRURLRejectsMalformed(t *testing.T) {
	if _, ok := ParsePRURL("not a url"); ok {
		t.Fatal("want malformed URL to be rejected")
	}
}

func TestFindPRURLsIgnoresMultipleURLsAsCallerResponsibility(t *testing.T) {
	urls := FindPRURLs("done: see https://github.com/a/b/pull/1 and https://github.com/c/d/pull/2")
	if len(urls) != 2 {
		t.Fatalf("got %+v, want both URLs returned - filtering to exactly one is the caller's job", urls)
	}
}

func TestFindPRURLsSkipsURLWithAdjacentPunctuation(t *testing.T) {
	urls := FindPRURLs("done: PR (https://github.com/a/b/pull/1) merged")
	if len(urls) != 0 {
		t.Fatalf("got %+v, want no match since the token isn't exactly a PR URL", urls)
	}
}

func TestFindPRURLsAgainstDogfoodDoneLine(t *testing.T) {
	lines := splitDogfoodLines(t)
	urls := FindPRURLs(lines[2])
	if len(urls) != 1 || urls[0] != "https://github.com/atqamz/secondhand/pull/31" {
		t.Fatalf("got %+v, want exactly the one embedded PR URL", urls)
	}
	if urls := FindPRURLs(lines[0]); len(urls) != 0 {
		t.Fatalf("got %+v, want no PR URL in the working line", urls)
	}
}
