// Package desk shows desktop notices and plays their sound on the desk.
package desk

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const commandTimeout = 10 * time.Second

// Popup shows a desktop notice with title and body, then plays the sound file with pw-play. A
// waiting notice is critical; any other is normal. Both programs run from PATH with no shell, and
// the sound plays even when the notice fails. Popup returns both failures joined.
func Popup(ctx context.Context, title, body string, waiting bool, sound string) error {
	urgency := "normal"
	if waiting {
		urgency = "critical"
	}

	notifyCtx, cancelNotify := context.WithTimeout(ctx, commandTimeout)
	defer cancelNotify()
	notifyErr := exec.CommandContext(notifyCtx, "notify-send", "-a", "Coding Agent", "-u", urgency,
		"-h", "boolean:suppress-sound:true", "--", title, body).Run()

	playCtx, cancelPlay := context.WithTimeout(ctx, commandTimeout)
	defer cancelPlay()
	playErr := exec.CommandContext(playCtx, "pw-play", sound).Run()

	return errors.Join(named("notify-send", notifyErr), named("pw-play", playErr))
}

// named wraps a non-nil err with the program name it came from.
func named(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
