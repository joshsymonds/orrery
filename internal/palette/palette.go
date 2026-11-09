// Package palette holds the Catppuccin Mocha colors orrery draws with and the helpers that color text for tmux
// formats and for terminals. It is the only place the hex values live.
package palette

import (
	"fmt"
	"strconv"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Color is a #rrggbb hex color. The empty Color means no color.
type Color string

// Catppuccin Mocha colors.
const (
	Base     Color = "#1e1e2e"
	Surface0 Color = "#313244"
	Overlay0 Color = "#6c7086"
	Text     Color = "#cdd6f4"
	Lavender Color = "#b4befe"
	Mauve    Color = "#cba6f7"
	Peach    Color = "#fab387"
	Yellow   Color = "#f9e2af"
	Green    Color = "#a6e3a1"
)

// State returns the color of a pane in state, or the empty Color when state is not a known pane state.
func State(state protocol.State) Color {
	switch state {
	case protocol.StateWorking:
		return Yellow
	case protocol.StateWaiting:
		return Peach
	case protocol.StateDone:
		return Green
	default:
		return ""
	}
}

// Tmux wraps text in a tmux style that sets its foreground to c and resets it after. It returns text unchanged when
// c is empty.
func Tmux(c Color, text string) string {
	if c == "" {
		return text
	}
	return "#[fg=" + string(c) + "]" + text + "#[default]"
}

// Fg wraps text in a truecolor SGR sequence that sets its foreground to c. It returns text unchanged when c is not a
// valid #rrggbb color.
func Fg(c Color, text string) string {
	return sgr("38", "39", c, text)
}

// Bg wraps text in a truecolor SGR sequence that sets its background to c. It returns text unchanged when c is not a
// valid #rrggbb color.
func Bg(c Color, text string) string {
	return sgr("48", "49", c, text)
}

// sgr wraps text in the set and reset truecolor SGR sequences for c. It returns text unchanged when c is not a valid
// #rrggbb color.
func sgr(set, reset string, c Color, text string) string {
	red, green, blue, ok := rgb(c)
	if !ok {
		return text
	}
	return fmt.Sprintf("\x1b[%s;2;%d;%d;%dm%s\x1b[%sm", set, red, green, blue, text, reset)
}

// Bit layout of a #rrggbb value: each color channel is eight bits wide, and the value is six hex digits after a
// leading #.
const (
	channelBits = 8
	greenShift  = channelBits
	redShift    = 2 * channelBits
	channelMask = 1<<channelBits - 1
	rgbBits     = 3 * channelBits
	rgbLen      = 1 + rgbBits/4
)

// rgb returns the red, green and blue components of c. It reports false when c is not a #rrggbb color.
func rgb(c Color) (red, green, blue uint64, ok bool) {
	if len(c) != rgbLen || c[0] != '#' {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(string(c[1:]), 16, rgbBits)
	if err != nil {
		return 0, 0, 0, false
	}
	return v >> redShift, (v >> greenShift) & channelMask, v & channelMask, true
}
