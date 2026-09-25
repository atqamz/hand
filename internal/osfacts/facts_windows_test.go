//go:build windows

package osfacts

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestBootTickChangedOnlyWhenTickCountDrops(t *testing.T) {
	cases := []struct {
		current, recorded uint64
		want              bool
	}{
		{current: 1000, recorded: 2000, want: true},
		{current: 2000, recorded: 2000, want: false},
		{current: 3000, recorded: 2000, want: false},
	}
	for _, c := range cases {
		if got := bootTickChanged(c.current, c.recorded); got != c.want {
			t.Errorf("bootTickChanged(%d, %d) = %v, want %v", c.current, c.recorded, got, c.want)
		}
	}
}

func TestZeroIncarnationIsUnknown(t *testing.T) {
	if got := Observe(Incarnation{}); got != Unknown {
		t.Fatalf("Observe(zero value) = %s, want unknown", got)
	}
}

func TestReadIncarnationOfSelf(t *testing.T) {
	self, err := ReadIncarnation(windows.GetCurrentProcessId())
	if err != nil {
		t.Fatalf("ReadIncarnation(self): %v", err)
	}
	if got := Observe(self); got != Alive {
		t.Fatalf("Observe(self) = %s, want alive", got)
	}
}
