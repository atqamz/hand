package board

import (
	"slices"
	"testing"
)

func TestShotsTakeOnlyThisFleetsInbox(t *testing.T) {
	text := "look\n[image: /f/inbox/20261001-120000-1.png]\n[image: /g/inbox/20261001-120000-2.png]\n[image: /etc/x.png]"
	rest, names := shots(text, "/f")
	if rest != "look\n[image: /g/inbox/20261001-120000-2.png]\n[image: /etc/x.png]" || !slices.Equal(names, []string{"20261001-120000-1.png"}) {
		t.Fatalf("rest %q names %q", rest, names)
	}
	if rest, names := shots("[image: /f/inbox/20261001-120000-1.png]\n", "/f"); rest != "" || len(names) != 1 {
		t.Fatalf("only an image: rest %q names %q", rest, names)
	}
}
