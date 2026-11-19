package uninstall

import "strings"

func dropPath(list, dir string, expand func(string) string) (string, bool) {
	var kept []string
	found := false
	for _, item := range strings.Split(list, ";") {
		if item == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(expand(item), `\`), strings.TrimRight(dir, `\`)) {
			found = true
			continue
		}
		kept = append(kept, item)
	}
	return strings.Join(kept, ";"), found
}
