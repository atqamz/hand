package viewlink

import "testing"

const fleet = "f0123456789ab"

func TestRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		l    Link
		want string
	}{
		{"fleet", Link{Fleet: fleet}, "handboard://view/f0123456789ab/"},
		{"task", Link{Fleet: fleet, Page: Item, Ref: "t3"}, "handboard://view/f0123456789ab/task/t3"},
		{"decision", Link{Fleet: fleet, Page: Item, Ref: "d3"}, "handboard://view/f0123456789ab/decision/d3"},
		{"attempt", Link{Fleet: fleet, Page: Item, Ref: "a7"}, "handboard://view/f0123456789ab/attempt/a7"},
		{"report", Link{Fleet: fleet, Page: Item, Ref: "t3", Report: "r5"}, "handboard://view/f0123456789ab/task/t3#r5"},
		{"doc", Link{Fleet: fleet, Page: Doc, Path: "reports/t1/design.md"}, "handboard://view/f0123456789ab/doc/reports/t1/design.md"},
		{"doc space", Link{Fleet: fleet, Page: Doc, Path: "my notes/a b.md"}, "handboard://view/f0123456789ab/doc/my%20notes/a%20b.md"},
		{"doc hash", Link{Fleet: fleet, Page: Doc, Path: "a#1/b?c.md"}, "handboard://view/f0123456789ab/doc/a%231/b%3Fc.md"},
		{"doc percent", Link{Fleet: fleet, Page: Doc, Path: "100%/x.md"}, "handboard://view/f0123456789ab/doc/100%25/x.md"},
		{"doc unicode", Link{Fleet: fleet, Page: Doc, Path: "laporan/é.md"}, "handboard://view/f0123456789ab/doc/laporan/%C3%A9.md"},
		{"tasks", Link{Fleet: fleet, Page: Tasks}, "handboard://view/f0123456789ab/tasks"},
		{"tasks query", Link{Fleet: fleet, Page: Tasks, Query: "fix #1 & more?"}, "handboard://view/f0123456789ab/tasks?q=fix+%231+%26+more%3F"},
		{"log", Link{Fleet: fleet, Page: Log}, "handboard://view/f0123456789ab/log"},
		{"companion", Link{Fleet: fleet, Page: Companion}, "handboard://view/f0123456789ab/companion"},
		{"alerts", Link{Fleet: fleet, Page: Alerts}, "handboard://view/f0123456789ab/alerts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Format(tc.l)
			if err != nil || got != tc.want {
				t.Fatalf("Format = %q, %v; want %q", got, err, tc.want)
			}
			back, err := Parse(got)
			if err != nil || back != tc.l {
				t.Fatalf("Parse = %+v, %v; want %+v", back, err, tc.l)
			}
		})
	}
}

func TestRefused(t *testing.T) {
	for _, link := range []string{
		"",
		"handboard://open/f0123456789ab/task/t3",
		"handboard://view/",
		"handboard://view/f0123456789ab",
		"handboard://view/F0123456789ab/",
		"handboard://view/f0123456789ab/screen",
		"handboard://view/f0123456789ab/unknown",
		"handboard://view/f0123456789ab/log/",
		"handboard://view/f0123456789ab/doc/",
		"handboard://view/f0123456789ab/doc/../x.md",
		"handboard://view/f0123456789ab/doc/a/../../x.md",
		"handboard://view/f0123456789ab/doc/a/%2e%2e/x.md",
		"handboard://view/f0123456789ab/doc/a%2F..%2Fx.md",
		"handboard://view/f0123456789ab/doc/./x.md",
		"handboard://view/f0123456789ab/doc//x.md",
		"handboard://view/f0123456789ab/doc//etc/passwd",
		"handboard://view/f0123456789ab/doc/a%5Cb.md",
		"handboard://view/f0123456789ab/doc/a%00b.md",
		"handboard://view/f0123456789ab/doc/a.md#L3",
		"handboard://view/f0123456789ab/doc/a.md?x=1",
		"handboard://view/f0123456789ab/doc/%zz",
		"handboard://view/f0123456789ab/tasks?q=",
		"handboard://view/f0123456789ab/tasks?x=1",
		"handboard://view/f0123456789ab/tasks?q=a&b=c",
		"handboard://view/f0123456789ab/task/t0",
		"handboard://view/f0123456789ab/task/t3#r0",
		"handboard://view/f0123456789ab/decision/d3#r5",
		"handboard://view/f0123456789ab/task/t3#",
	} {
		if l, err := Parse(link); err == nil {
			t.Errorf("Parse(%q) = %+v, want refusal", link, l)
		}
	}
}

func TestFormatRefused(t *testing.T) {
	for _, l := range []Link{
		{Fleet: "x"},
		{Fleet: fleet, Page: "screen"},
		{Fleet: fleet, Page: Doc, Path: "../x"},
		{Fleet: fleet, Page: Doc, Path: "/abs"},
		{Fleet: fleet, Page: Doc, Path: "a//b"},
		{Fleet: fleet, Page: Doc},
		{Fleet: fleet, Page: Item},
		{Fleet: fleet, Page: Item, Ref: "x3"},
		{Fleet: fleet, Page: Item, Ref: "d3", Report: "r1"},
	} {
		if s, err := Format(l); err == nil {
			t.Errorf("Format(%+v) = %q, want refusal", l, s)
		}
	}
}
