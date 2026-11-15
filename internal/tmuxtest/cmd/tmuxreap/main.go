// Command tmuxreap stops the tmux servers that tests started and that outlived their test binaries (see
// tmuxtest.Reaper). The check recipes run it first. It prints each server it stops, and exits 1 when a stop fails.
package main

import (
	"fmt"
	"os"

	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

func main() {
	reaped, err := tmuxtest.DefaultReaper().Reap()
	for _, orphan := range reaped {
		fmt.Fprintf(os.Stderr, "tmuxreap: stopped tmux server %d (-L %s) of gone test binary %d\n",
			orphan.PID, orphan.Name, orphan.Owner)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmuxreap: %v\n", err)
		os.Exit(1)
	}
}
