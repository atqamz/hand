package redact

import (
	"regexp"
	"strings"
)

type rule struct {
	re   *regexp.Regexp
	repl string
}

func mark(kind string) string { return "[redacted:" + kind + "]" }

var rules = []rule{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`), mark("private-key")},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}`), mark("github-token")},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`), mark("github-token")},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), mark("gitlab-token")},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), mark("api-key")},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]+`), mark("slack-token")},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), mark("aws-key")},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), mark("google-key")},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), mark("jwt")},
	{regexp.MustCompile(`\bBearer[ \t]+[A-Za-z0-9._~+/=-]{20,}`), mark("bearer")},
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/:@]+:[^\s/@]+@`), "${1}" + mark("url-credentials") + "@"},
}

var assignment = regexp.MustCompile(`(?i)((?:secret|token|password|passwd|api_key|private_key|credential)[A-Za-z0-9_.-]*)(["']?[ \t]*[:=][ \t]*)(["']?)([^\s"']{12,})`)

func Text(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return assignment.ReplaceAllStringFunc(s, func(m string) string {
		p := assignment.FindStringSubmatch(m)
		if strings.HasPrefix(p[4], "[redacted:") || strings.Trim(p[4], "0123456789") == "" {
			return m
		}
		return p[1] + p[2] + p[3] + mark("assignment")
	})
}
