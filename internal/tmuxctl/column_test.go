package tmuxctl_test

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// TestPaneWakeColumnRoundTripsEachServerKind checks that Column and ServerOfColumn invert each other for a named
// server, a socket server and the default server, and that each column has the form the store records. The socket
// case is the "-S <socket>" form that enginehost.epicCallerOf stores for a pane caller.
func TestPaneWakeColumnRoundTripsEachServerKind(t *testing.T) {
	cases := []struct {
		name   string
		server tmuxctl.Server
		column string
	}{
		{"named", tmuxctl.Server{Name: "orrery"}, "-L orrery"},
		{"socket", tmuxctl.Server{Socket: "/tmp/tmux-1000/work"}, "-S /tmp/tmux-1000/work"},
		{"default", tmuxctl.Server{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.server.Column(); got != tc.column {
				t.Fatalf("Column() = %q, want %q", got, tc.column)
			}
			got, err := tmuxctl.ServerOfColumn(tc.column)
			if err != nil {
				t.Fatalf("ServerOfColumn(%q): %v", tc.column, err)
			}
			if got != tc.server {
				t.Fatalf("ServerOfColumn(%q) = %+v, want %+v", tc.column, got, tc.server)
			}
		})
	}
}

// TestPaneWakeColumnRejectsMalformedColumns checks that a column which is neither empty, "-L <name>" nor "-S <path>",
// or which is a flag with an empty argument, is an error that names the column.
func TestPaneWakeColumnRejectsMalformedColumns(t *testing.T) {
	malformed := []string{
		"default",
		"orrery",
		"-L",
		"-S",
		"-L ",
		"-S ",
		"-x orrery",
		"--L orrery",
		"-Lorrery",
		"-Sx",
	}
	for _, column := range malformed {
		t.Run(column, func(t *testing.T) {
			server, err := tmuxctl.ServerOfColumn(column)
			if err == nil {
				t.Fatalf("ServerOfColumn(%q) = %+v, want error", column, server)
			}
			if !strings.Contains(err.Error(), column) {
				t.Fatalf("ServerOfColumn(%q) error %q does not name the column", column, err)
			}
		})
	}
}
