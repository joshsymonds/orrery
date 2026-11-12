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
// waiting notice is critical; any other is normal. An empty sound means no sound: pw-play does not
// run. Both programs run from PATH with no shell, and a sound plays even when the notice fails.
// Popup returns both failures joined.
func Popup(ctx context.Context, title, body string, waiting bool, sound string) error {
	urgency := "normal"
	if waiting {
		urgency = "critical"
	}

	notifyCtx, cancelNotify := context.WithTimeout(ctx, commandTimeout)
	defer cancelNotify()
	notify := exec.CommandContext(notifyCtx, "notify-send")
	notify.Args = append(notify.Args, "-a", "Coding Agent", "-u", urgency,
		"-h", "boolean:suppress-sound:true", "--", title, body)
	notifyErr := notify.Run()

	var playErr error
	if sound != "" {
		playCtx, cancelPlay := context.WithTimeout(ctx, commandTimeout)
		defer cancelPlay()
		play := exec.CommandContext(playCtx, "pw-play")
		play.Args = append(play.Args, sound)
		playErr = play.Run()
	}

	return errors.Join(named("notify-send", notifyErr), named("pw-play", playErr))
}

// named wraps a non-nil err with the program name it came from.
func named(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
