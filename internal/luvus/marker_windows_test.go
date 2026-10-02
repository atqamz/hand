package luvus

import (
	"os"
	"testing"
)

func TestProcStartMarkerIsStable(t *testing.T) {
	a, err := ProcStartMarker(os.Getpid())
	if err != nil || a == "" {
		t.Fatalf("marker = %q, %v", a, err)
	}
	if b, _ := ProcStartMarker(os.Getpid()); a != b {
		t.Fatalf("marker changed: %q then %q", a, b)
	}
}
