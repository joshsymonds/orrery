package holder

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// scopeUnitPrefix starts the unit name of the user scope a scoped holder runs in. The session follows it, then the
// launch's nonce. systemd-run adds scopeUnitSuffix to that name, and systemctl needs it to stop the scope.
const scopeUnitPrefix = "orrery-hold-"

// scopeUnitSuffix ends the name of the user scope a scoped holder runs in.
const scopeUnitSuffix = ".scope"

// scopeNonceBytes is the number of random bytes a launch's nonce is drawn from. Its hex encoding is scopeNonceLen long.
const scopeNonceBytes = 8

// scopeNonceLen is the length of a launch's nonce as hex. A nonce has no dash, so a session's scopes can be named
// exactly by their length: see scopeGlob.
const scopeNonceLen = 2 * scopeNonceBytes

// systemctlNotLoaded is the status systemctl exits with when the unit it is asked to stop is not loaded.
const systemctlNotLoaded = 5

// newScopeNonce returns a fresh nonce for one launch's scope, as hex.
func newScopeNonce() (string, error) {
	var b [scopeNonceBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("holder: make scope nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// scopeUnit returns the unit name of the user scope of one launch of session's holder, with nonce, the launch's own
// nonce. It is the name systemd-run's --unit takes, without the .scope suffix.
func scopeUnit(session, nonce string) string {
	return scopeUnitPrefix + session + "-" + nonce
}

// scopeGlob returns the glob of every scope of session, whatever its nonce, as a stop names it. The nonce is always
// scopeNonceLen characters, and the name of session-x has a dash and more characters in place of the nonce, so the glob
// cannot match the scopes of another session.
func scopeGlob(session string) string {
	return scopeUnit(session, strings.Repeat("?", scopeNonceLen))
}

// scopeStopper stops the user scope of one launch of a session's holder, or of every scope a glob names. A zero
// scopeStopper, for a holder started plainly, stops nothing.
type scopeStopper struct {
	// systemctl is the systemctl beside the systemd-run that started the scope, or empty for no scope.
	systemctl string
	// unit is the name of the scope to stop, with its .scope suffix, or a glob of the scopes to stop.
	unit string
}

// newScopeStopper returns the stopper of the scope unit, a unit name without its suffix, under scope, the systemd-run
// path a launch takes. It uses the systemctl in scope's directory, where systemd installs both. An empty scope gives the
// zero stopper.
func newScopeStopper(scope, unit string) scopeStopper {
	if scope == "" {
		return scopeStopper{}
	}
	return scopeStopper{
		systemctl: filepath.Join(filepath.Dir(scope), "systemctl"),
		unit:      unit + scopeUnitSuffix,
	}
}

// stop stops the scope with systemctl --user stop, which ends every process left in it and returns once they have
// ended. A literal unit that is not loaded has nothing in it, so its stop is not an error, and a glob that matches no
// loaded scope is not one either. The zero stopper returns nil.
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

// writeScopeNonce creates the session directory dir if it is absent and records nonce in it, where a reattach reads it.
func writeScopeNonce(dir, nonce string) error {
	if err := PrepareDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, scopeNonceName)
	if err := os.WriteFile(path, []byte(nonce), 0o600); err != nil {
		return fmt.Errorf("holder: write scope nonce %s: %w", path, err)
	}
	return nil
}

// readScopeStopper returns the stopper of the scope the holder in dir runs in under scope, the systemd-run path it was
// launched with, or empty when it was started plainly. A holder whose directory holds no nonce has no scope to stop: it
// was started plainly, or before scope names carried a nonce.
func readScopeStopper(dir, scope string) (scopeStopper, error) {
	if scope == "" {
		return scopeStopper{}, nil
	}
	nonce, err := readScopeNonce(dir)
	if err != nil {
		return scopeStopper{}, err
	}
	if nonce == "" {
		return scopeStopper{}, nil
	}
	return newScopeStopper(scope, scopeUnit(filepath.Base(dir), nonce)), nil
}

// readScopeNonce returns the nonce writeScopeNonce recorded in dir, or "" when dir holds none. A nonce that is not
// scopeNonceLen characters long is an error.
func readScopeNonce(dir string) (string, error) {
	path := filepath.Join(dir, scopeNonceName)
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("holder: read scope nonce %s: %w", path, err)
	}
	if len(data) != 0 && len(data) != scopeNonceLen {
		return "", fmt.Errorf("holder: scope nonce %s holds %q, want %d hex digits", path, data, scopeNonceLen)
	}
	return string(data), nil
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
