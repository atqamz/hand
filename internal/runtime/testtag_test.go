package runtime

import (
	"testing"

	"github.com/atqamz/hand/internal/testtag"
)

func TestMain(m *testing.M) {
	testtag.Main(m.Run)
}
