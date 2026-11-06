package state

import (
	"slices"
	"testing"
)

func TestRewriteImportTextGrammar(t *testing.T) {
	ids := map[int64]bool{2: true, 29: true}
	cases := []struct {
		text string
		want string
		refs []string
		amb  []string
		left []string
	}{
		{"after t2.", "after t31.", []string{"t2"}, nil, nil},
		{"after t2. Next", "after t31. Next", []string{"t2"}, nil, nil},
		{"see t2.png", "see t2.png", nil, []string{"t2.png"}, nil},
		{"read path/t2", "read path/t2", nil, nil, nil},
		{"head d5fe72c now", "head d5fe72c now", nil, []string{"d5fe72c"}, nil},
		{"see d10 now", "see d10 now", nil, nil, []string{"d10"}},
		{"see t29, then", "see t58, then", []string{"t29"}, nil, nil},
		{"see t99", "see t99", nil, nil, nil},
	}
	for _, c := range cases {
		got, refs, left, amb := rewriteImportText(c.text, ids, 29, "t1", "title")
		if got != c.want {
			t.Errorf("%q = %q, want %q", c.text, got, c.want)
			continue
		}
		var gotRefs, gotLeft, gotAmb []string
		for _, r := range refs {
			gotRefs = append(gotRefs, r.From)
		}
		for _, r := range left {
			gotLeft = append(gotLeft, r.From)
		}
		for _, a := range amb {
			gotAmb = append(gotAmb, a.Token)
		}
		if !slices.Equal(gotRefs, c.refs) || !slices.Equal(gotLeft, c.left) || !slices.Equal(gotAmb, c.amb) {
			t.Errorf("%q refs=%v left=%v ambiguous=%v, want %v %v %v", c.text, gotRefs, gotLeft, gotAmb, c.refs, c.left, c.amb)
		}
	}
}
