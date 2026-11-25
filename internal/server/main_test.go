package server_test

import (
	"os"
	"testing"

	"github.com/joshsymonds/orrery/internal/holdertest"
)

// TestMain builds orrery once for the package when a run selects a test whose session runs under orrery holder: the
// speak-label test and the two waits-label tests start a session, which finds orrery beside the test binary or on PATH.
// A run that selects none of them skips the build.
func TestMain(m *testing.M) {
	os.Exit(holdertest.Main(m,
		"TestSpeakLabelOwnerSayEndsWaitsSpeak",
		"TestWaitsLabelFollowsOwnerInterruptSayAndStop",
		"TestWaitsLabelRequestThenSpeakWhileRequestOpen",
	))
}
