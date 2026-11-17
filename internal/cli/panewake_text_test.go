package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Synthetic <orrery-event> tags, shaped as enginehost.CallerEventBlock writes them.
const (
	paneWakeOpenTag  = `<orrery-event kind="step_end" id="step:synthetic">`
	paneWakeCloseTag = "</orrery-event>"
)

// paneWakeBlock returns an <orrery-event> block whose body is the given lines.
func paneWakeBlock(body ...string) string {
	return strings.Join(append(append([]string{paneWakeOpenTag}, body...), paneWakeCloseTag), "\n")
}

// paneWakeLongBody returns 300 body lines. The first holds a tab, and the rest are numbered from 1.
func paneWakeLongBody() []string {
	body := make([]string, 300)
	body[0] = "a\tb"
	for i := 1; i < len(body); i++ {
		body[i] = fmt.Sprintf("line %d", i)
	}
	return body
}

// paneWakeCutLongBlock returns the text that WakePane types for paneWakeBlock(paneWakeLongBody()...). The opening tag,
// the cleaned first body line and the body lines numbered 1 to 96 take 195 typed invocations, and the cut line and
// the closing tag take 4 more, which makes 199 of the 200 allowed.
func paneWakeCutLongBlock() string {
	kept := []string{paneWakeOpenTag, "a b"}
	for i := range 96 {
		kept = append(kept, fmt.Sprintf("line %d", i+1))
	}
	return strings.Join(append(kept, cli.PaneWakeCutLine, paneWakeCloseTag), "\n")
}

// TestPaneWakeCallerCleansText requires the text that WakePane types to replace each tab and other control character
// with a space, replace invalid UTF-8, turn CRLF and lone CR into LF, and leave typeable text as it is.
func TestPaneWakeCallerCleansText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "tab", text: "a\tb", want: "a b"},
		{name: "control characters", text: "a\x01b\x7fc\u0085d\x1be", want: "a b c d e"},
		{name: "invalid UTF-8", text: "a\xffb", want: "a\uFFFDb"},
		{name: "line ends", text: "a\r\nb\rc", want: "a\nb\nc"},
		{name: "typeable", text: "first line\nsecond line", want: "first line\nsecond line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cli.PaneWakeText("%1", tc.text); got != tc.want {
				t.Fatalf("PaneWakeText(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestPaneWakeCallerCutsLongBlock requires the text of an <orrery-event> block that needs more than 200 typed
// invocations to be cut to 200 or fewer, and to keep the opening tag, the cut line and the closing tag. A block with
// 300 lines keeps as many body lines as fit. A block whose body is one 200 KB line keeps as many pieces of it as fit.
func TestPaneWakeCallerCutsLongBlock(t *testing.T) {
	t.Parallel()
	t.Run("300 lines", func(t *testing.T) {
		t.Parallel()
		got := cli.PaneWakeText("%1", paneWakeBlock(paneWakeLongBody()...))
		if want := paneWakeCutLongBlock(); got != want {
			t.Fatalf("PaneWakeText = %q, want %q", got, want)
		}
		if !cli.SendTypeable("%1", got) {
			t.Fatal("PaneWakeText result is not typeable")
		}
	})
	t.Run("200 KB line", func(t *testing.T) {
		t.Parallel()
		body := strings.Repeat("0123456789", 20000)
		got := cli.PaneWakeText("%1", paneWakeBlock(body))
		if !cli.SendTypeable("%1", got) {
			t.Fatal("PaneWakeText result is not typeable")
		}
		// The opening tag, a line break, 194 pieces of 400 bytes, a line break, the cut line, a line break and the
		// closing tag take 200 typed invocations.
		lines := strings.Split(got, "\n")
		if len(lines) != 4 || lines[0] != paneWakeOpenTag || lines[2] != cli.PaneWakeCutLine ||
			lines[3] != paneWakeCloseTag {
			t.Fatalf("PaneWakeText = %q, want the opening tag, a cut body, the cut line and the closing tag", got)
		}
		if want := 194 * 400; len(lines[1]) != want || !strings.HasPrefix(body, lines[1]) {
			t.Fatalf("PaneWakeText kept %d bytes of the body, want the first %d", len(lines[1]), want)
		}
	})
}

// TestPaneWakeCallerTypesCutBlockIntoDonePane requires WakePane, given the "-L <name>" column of a done pane that shows
// its input box, to type the cleaned and cut text of a block whose body holds a tab and 300 lines, press Enter once,
// and return true.
func TestPaneWakeCallerTypesCutBlockIntoDonePane(t *testing.T) {
	t.Parallel()
	client, pane, file, column := paneWakeCallerPane(t, sendBoxScreen())
	sendSetState(t, client, pane, string(protocol.StateDone))

	sent, err := cli.WakePaneWithClock(
		t.Context(), &instantClock{}, column, pane, paneWakeBlock(paneWakeLongBody()...),
	)
	if err != nil || !sent {
		t.Fatalf("WakePane = %v, %v; want true, nil", sent, err)
	}
	sendAwaitInput(t, client, pane, file, paneWakeCutLongBlock()+"\r")
}
