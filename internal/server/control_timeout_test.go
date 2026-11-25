package server

import (
	"testing"
	"time"
)

// TestControlZeroTimeoutIsTheDefault checks that a Control service given no timeout bounds each forward by
// defaultControlTimeout, ten seconds, and that a timeout it is given is kept. TestControlDeadlineExceeded checks that
// the bound ends a forward that does not return.
func TestControlZeroTimeoutIsTheDefault(t *testing.T) {
	t.Parallel()
	if defaultControlTimeout != 10*time.Second {
		t.Fatalf("defaultControlTimeout = %v, want 10s", defaultControlTimeout)
	}
	if got := newControlServer(nil, 0, "", nil, "", nil).timeout; got != defaultControlTimeout {
		t.Errorf("timeout with none given = %v, want defaultControlTimeout %v", got, defaultControlTimeout)
	}
	if got := newControlServer(nil, time.Second, "", nil, "", nil).timeout; got != time.Second {
		t.Errorf("timeout given 1s = %v, want 1s", got)
	}
}
