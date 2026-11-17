package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/state"
)

var specName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const maxSkillLines = 500

func ValidateSpec(dir, name string) error {
	path := filepath.Join(dir, "SKILL.md")
	have, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: a skill folder holds a SKILL.md: %v", state.ErrInvalid, err)
	}
	invalid := func(format string, a ...any) error {
		return fmt.Errorf("%w: SKILL.md: %s", state.ErrInvalid, fmt.Sprintf(format, a...))
	}
	head, _, ok := frontmatter(string(have))
	if !ok {
		return invalid("frontmatter missing; the file starts with `---`, a `name` and a `description`, and a closing `---`")
	}
	fields, meta, err := parseFrontmatter(head)
	if err != nil {
		return invalid("%v", err)
	}
	got, has := fields["name"]
	switch {
	case !has || got == "":
		return invalid("name is required")
	case utf8.RuneCountInString(got) > 64 || !specName.MatchString(got):
		return invalid("name %q must be 1-64 lowercase letters, digits and single hyphens, with no hyphen at either end", got)
	case got != name:
		return invalid("name %q must equal the skill's folder name %q", got, name)
	}
	if n := utf8.RuneCountInString(fields["description"]); n < 1 || n > 1024 {
		return invalid("description is %d characters; it must be 1-1024 and say what the skill does and when to use it", n)
	}
	if c, has := fields["compatibility"]; has && (c == "" || utf8.RuneCountInString(c) > 500) {
		return invalid("compatibility is %d characters; when present it must be 1-500", utf8.RuneCountInString(c))
	}
	if _, has := fields["metadata"]; has && meta == nil {
		return invalid("metadata must be a map of string keys to string values")
	}
	if lines := strings.Count(strings.TrimSuffix(string(have), "\n"), "\n") + 1; lines >= maxSkillLines {
		return invalid("%d lines; keep SKILL.md under %d and move detail to references/", lines, maxSkillLines)
	}
	return nil
}

func parseFrontmatter(head string) (fields, meta map[string]string, err error) {
	fields = map[string]string{}
	key := ""
	for _, line := range strings.Split(head, "\n") {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' && !(key != "" && strings.HasPrefix(line, "- ")) {
			k, v, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(k) == "" {
				return nil, nil, fmt.Errorf("frontmatter line %q is not `key: value`", line)
			}
			key, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if strings.Trim(v, "|>+-") == "" {
				v = ""
			}
			fields[key] = unquote(v)
			if key == "metadata" {
				meta = nil
				if v == "" || strings.HasPrefix(v, "{") {
					meta = map[string]string{}
				}
			}
			continue
		}
		if key == "metadata" && meta != nil {
			k, v, ok := strings.Cut(text, ":")
			if !ok {
				return nil, nil, fmt.Errorf("metadata line %q is not `key: value`", text)
			}
			meta[strings.TrimSpace(k)] = unquote(strings.TrimSpace(v))
			continue
		}
		fields[key] = strings.TrimSpace(fields[key] + " " + text)
	}
	return fields, meta, nil
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
