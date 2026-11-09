package palette_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/palette"
	"github.com/joshsymonds/orrery/internal/protocol"
)

func TestColourValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  palette.Color
		want palette.Color
	}{
		{"Base", palette.Base, "#1e1e2e"},
		{"Surface0", palette.Surface0, "#313244"},
		{"Overlay0", palette.Overlay0, "#6c7086"},
		{"Text", palette.Text, "#cdd6f4"},
		{"Lavender", palette.Lavender, "#b4befe"},
		{"Mauve", palette.Mauve, "#cba6f7"},
		{"Peach", palette.Peach, "#fab387"},
		{"Yellow", palette.Yellow, "#f9e2af"},
		{"Green", palette.Green, "#a6e3a1"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		state protocol.State
		want  palette.Color
	}{
		{"working", protocol.StateWorking, palette.Yellow},
		{"waiting", protocol.StateWaiting, palette.Peach},
		{"done", protocol.StateDone, palette.Green},
		{"unset", protocol.State(""), ""},
		{"unknown", protocol.State("bogus"), ""},
	}
	for _, tc := range tests {
		if got := palette.State(tc.state); got != tc.want {
			t.Errorf("State(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTmux(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    palette.Color
		text string
		want string
	}{
		{"color", palette.Peach, "w", "#[fg=#fab387]w#[default]"},
		{"empty color", "", "w", "w"},
		{"empty text", palette.Peach, "", "#[fg=#fab387]#[default]"},
	}
	for _, tc := range tests {
		if got := palette.Tmux(tc.c, tc.text); got != tc.want {
			t.Errorf("Tmux(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFg(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    palette.Color
		text string
		want string
	}{
		{"peach", palette.Peach, "w", "\x1b[38;2;250;179;135mw\x1b[39m"},
		{"base", palette.Base, "w", "\x1b[38;2;30;30;46mw\x1b[39m"},
		{"empty color", "", "w", "w"},
		{"missing hash", "fab387", "w", "w"},
		{"short hex", "#fab38", "w", "w"},
		{"non-hex digit", "#fab38z", "w", "w"},
	}
	for _, tc := range tests {
		if got := palette.Fg(tc.c, tc.text); got != tc.want {
			t.Errorf("Fg(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBg(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    palette.Color
		text string
		want string
	}{
		{"surface0", palette.Surface0, "x", "\x1b[48;2;49;50;68mx\x1b[49m"},
		{"empty color", "", "x", "x"},
		{"non-hex digit", "#31324z", "x", "x"},
	}
	for _, tc := range tests {
		if got := palette.Bg(tc.c, tc.text); got != tc.want {
			t.Errorf("Bg(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
