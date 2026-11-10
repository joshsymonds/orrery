// Command enginecapture records, sanitizes, and checks the stream-json probes of Claude Code.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/engine/capture"
)

func main() {
	os.Exit(capture.Main(os.Args[1:], capture.Config{
		Environ:  os.Environ(),
		LookPath: exec.LookPath,
		Daemon: func() (capture.Daemon, error) {
			passwd, err := os.ReadFile("/etc/passwd")
			if err != nil {
				return capture.Daemon{}, fmt.Errorf("read /etc/passwd: %w", err)
			}
			return capture.NewDaemon(os.Environ(), exec.LookPath, filepath.EvalSymlinks, passwd, os.Getuid())
		},
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}))
}
