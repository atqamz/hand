package herdr

import (
	"os"
	"testing"

	"github.com/atqamz/hand/internal/testtag"
)

func TestMain(m *testing.M) {
	if os.Getenv("HERDR_TEST_SERVER_CHILD") == "1" {
		os.Exit(runDetachedHerdrFixture())
	}
	testtag.Main(m.Run)
}
