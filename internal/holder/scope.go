package holder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// scopeUnitPrefix starts the name of the user scope a scoped holder runs in. The session follows it.
const scopeUnitPrefix = "orrery-hold-"

// systemctlNotLoaded is the status systemctl exits with when the unit it is asked to stop is not loaded.
const systemctlNotLoaded = 5

// scopeName returns the name systemd-run gives the user scope of session's holder, without the .scope suffix.
func scopeName(session string) string {
	return scopeUnitPrefix + session
}

// scopeStopper stops the user scope of one session's holder. A zero scopeStopper, for a holder started plainly, stops
// nothing.
type scopeStopper struct {
	// systemctl is the systemctl beside the systemd-run that started the scope, or empty for no scope.
	systemctl string
	// unit is the scope's unit name.
	unit string
}

// newScopeStopper returns the stopper of session's scope under scope, the systemd-run path a launch takes. It uses the
// systemctl in scope's directory, where systemd installs both. An empty scope gives the zero stopper.
func newScopeStopper(scope, session string) scopeStopper {
	if scope == "" {
		return scopeStopper{}
	}
	return scopeStopper{
		systemctl: filepath.Join(filepath.Dir(scope), "systemctl"),
		unit:      scopeName(session) + ".scope",
	}
}

// stop stops the scope with systemctl --user stop, which ends every process left in it and returns once they have
// ended. A scope that is not loaded has nothing in it, so its stop is not an error. The zero stopper returns nil.
func (s scopeStopper) stop(ctx context.Context) error {
	if s.systemctl == "" {
		return nil
	}
	// The command is built as proc builds the holder's: the program is the systemctl in the operator's --holder-scope
	// directory, run with argv and no shell.
	var out bytes.Buffer
	cmd := &exec.Cmd{
		Path:   s.systemctl,
		Args:   []string{s.systemctl, "--user", "stop", s.unit},
		Stdout: &out,
		Stderr: &out,
	}
	err := runBounded(ctx, cmd)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == systemctlNotLoaded {
		return nil
	}
	if err != nil {
		return fmt.Errorf("holder: stop scope %s: %w: %s", s.unit, err, strings.TrimSpace(out.String()))
	}
	return nil
}

// runBounded runs cmd until it exits, and kills it once ctx ends first. A kill that fails other than on a process that
// has already exited is joined to cmd's error.
func runBounded(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	killed := make(chan error, 1)
	stopKill := context.AfterFunc(ctx, func() { killed <- cmd.Process.Kill() })
	err := cmd.Wait()
	if !stopKill() {
		if killErr := <-killed; killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			err = errors.Join(err, killErr)
		}
	}
	return err
}
