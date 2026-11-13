package enginehost

import (
	"os"
	"testing"

	"github.com/joshsymonds/orrery/internal/holdertest"
)

func TestMain(m *testing.M) {
	os.Exit(holdertest.Main(m))
}
