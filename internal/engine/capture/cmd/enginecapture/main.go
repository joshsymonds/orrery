// Command enginecapture records, sanitizes, and checks the stream-json probes of Claude Code.
package main

import (
	"os"
	"os/exec"

	"github.com/joshsymonds/orrery/internal/engine/capture"
)

func main() {
	os.Exit(capture.Main(os.Args[1:], capture.Config{
		Environ:  os.Environ(),
		LookPath: exec.LookPath,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}))
}
