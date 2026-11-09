package desktopasset_test

import (
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/desktopasset"
)

func TestDefaultStampMatchesHand(t *testing.T) {
	if desktopasset.Version != cli.Version {
		t.Fatalf("hand-desktop defaults to %s, hand to %s", desktopasset.Version, cli.Version)
	}
}
