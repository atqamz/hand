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
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----.*?(?:-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----|\z)`), mark("private-key")},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}`), mark("github-token")},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`), mark("github-token")},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), mark("gitlab-token")},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), mark("api-key")},
	{regexp.MustCompile(`\b[rs]k_(?:live|test)_[A-Za-z0-9]{16,}`), mark("api-key")},
	{regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36,}`), mark("npm-token")},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]+`), mark("slack-token")},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), mark("aws-key")},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), mark("google-key")},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), mark("jwt")},
	{regexp.MustCompile(`(?i)\bbearer[ \t]+[A-Za-z0-9._~+/=-]{20,}`), mark("bearer")},
	{regexp.MustCompile(`(?i)(authorization["']?[ \t]*[:=][ \t]*["']?basic[ \t]+)[A-Za-z0-9+/]{8,}={0,2}`), "${1}" + mark("basic-auth")},
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/:@]+:[^\s/@]+@`), "${1}" + mark("url-credentials") + "@"},
}

const secretName = `(?i)((?:secret|token|password|passwd|passphrase|api[_.-]?key|private_key|credential)(?:s|[_.-]?(?:key|id|token|secret|value|string|access|base))*)`

var (
	quotedAssignment = regexp.MustCompile(secretName + `(["']?[ \t]*[:=][ \t]*)("(?:[^"\\\n]|\\.){12,}"|'(?:[^'\\\n]|\\.){12,}')`)
	assignment       = regexp.MustCompile(secretName + `(["']?[ \t]*[:=][ \t]*)(["']?)([^\s"']{12,})`)
)

var marker = regexp.MustCompile(`^\[redacted:[a-z-]+\]$`)

func secretValue(v string) bool {
	return !marker.MatchString(v) && strings.Trim(v, "0123456789") != ""
}

func Text(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	s = quotedAssignment.ReplaceAllStringFunc(s, func(m string) string {
		p := quotedAssignment.FindStringSubmatch(m)
		if !secretValue(p[3][1 : len(p[3])-1]) {
			return m
		}
		return p[1] + p[2] + p[3][:1] + mark("assignment") + p[3][len(p[3])-1:]
	})
	return assignment.ReplaceAllStringFunc(s, func(m string) string {
		p := assignment.FindStringSubmatch(m)
		if !secretValue(p[4]) {
			return m
		}
		return p[1] + p[2] + p[3] + mark("assignment")
	})
}
