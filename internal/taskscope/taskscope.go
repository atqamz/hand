package taskscope

type Scope struct{ Value, Label string }

var Scopes = []Scope{
	{"", "Default"},
	{"finished", "Done and abandoned"},
	{"all", "Every status"},
	{"done", "Done"},
	{"abandoned", "Abandoned"},
	{"active", "Active"},
	{"inbox", "Inbox"},
}

func Label(value string) string {
	for _, s := range Scopes {
		if s.Value == value {
			return s.Label
		}
	}
	return value
}
