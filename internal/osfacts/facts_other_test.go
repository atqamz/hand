//go:build !linux && !windows

package osfacts

import (
	"errors"
	"testing"
)

func TestReadIncarnationRefusesOnUnsupportedPlatforms(t *testing.T) {
	if _, err := ReadIncarnation(1); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ReadIncarnation err = %v, want errors.ErrUnsupported", err)
	}
}

func TestZeroIncarnationIsUnknown(t *testing.T) {
	if got := Observe(Incarnation{}); got != Unknown {
		t.Fatalf("Observe(zero value) = %s, want unknown", got)
	}
}
