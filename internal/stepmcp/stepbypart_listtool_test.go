package stepmcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// listingAsk is one CallerListings call the listing daemon received, with the caller and epic it named.
type listingAsk struct {
	caller store.EpicCaller
	epic   string
}

// listingSteps is a daemon whose caller listing answers listings and records each call. Every other command is the
// embedded StepCommands', which the tool never reaches.
type listingSteps struct {
	enginehost.StepCommands
	listings []store.StepListing
	mu       sync.Mutex
	asked    []listingAsk
}

// Held reports that no listed step is held.
func (f *listingSteps) Held(string) bool {
	return false
}

// CallerListings answers the configured listings and records the caller and epic it was asked with.
func (f *listingSteps) CallerListings(
	_ context.Context, caller store.EpicCaller, epic string,
) ([]store.StepListing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, listingAsk{caller: caller, epic: epic})
	return f.listings, nil
}

// askedWith returns a copy of the calls the daemon received.
func (f *listingSteps) askedWith() []listingAsk {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// listTool calls step_list with arguments over MCP against a daemon that answers listings, and returns the daemon
// with the call's outcome.
func listTool(t *testing.T, listings []store.StepListing, arguments string) (*listingSteps, toolOutcome) {
	t.Helper()
	fake := &listingSteps{listings: listings}
	p := newPeer(t, startDaemon(t, fake), nil)
	return fake, p.tool("step_list", arguments)
}

// expectListText fails t unless outcome is a result, not a tool error, whose text is want.
func expectListText(t *testing.T, arguments string, outcome toolOutcome, want string) {
	t.Helper()
	if outcome.isError {
		t.Fatalf("step_list %s reported an error: %s", arguments, outcome.text)
	}
	if outcome.text != want {
		t.Fatalf("step_list %s = %q, want %q", arguments, outcome.text, want)
	}
}

// expectAsked fails t unless the daemon was asked once, for the calling session's steps in epic.
func expectAsked(t *testing.T, fake *listingSteps, epic string) {
	t.Helper()
	want := []listingAsk{{caller: store.EpicCaller{Session: daemonSession}, epic: epic}}
	if got := fake.askedWith(); !slices.Equal(got, want) {
		t.Fatalf("step_list reached the daemon as %+v, want %+v", got, want)
	}
}

// TestStepByPartListTool checks step_list over MCP against a daemon whose caller listing answers invented steps. The
// tool asks for the calling session's steps, narrowed to the epic it names, lists each step on one line, and keeps
// its text within 4000 bytes by dropping the earliest steps under a count of them. A session with no steps reads as
// having started none, and a call without caller headers is a tool error.
func TestStepByPartListTool(t *testing.T) {
	t.Run("each step is one line: id, agent, epic, task, end, status; end=running while running", func(t *testing.T) {
		listings := []store.StepListing{
			{ID: "s1", Agent: "reviewer", Epic: "atlas", Task: "t7", Status: "pass", End: "done"},
			{ID: "s2", Agent: "builder", Epic: "atlas", Task: "t8"},
			{ID: "s3", Agent: "scout", Status: "fail", End: "checks_failed"},
		}
		args := `{}`
		fake, outcome := listTool(t, listings, args)
		expectListText(t, args, outcome, strings.Join([]string{
			"s1 agent=reviewer epic=atlas task=t7 end=done status=pass",
			"s2 agent=builder epic=atlas task=t8 end=running status=-",
			"s3 agent=scout epic=- task=- end=checks_failed status=fail",
		}, "\n"))
		expectAsked(t, fake, "")
	})

	t.Run("epic is passed through to the daemon", func(t *testing.T) {
		listings := []store.StepListing{{ID: "s1", Agent: "reviewer", Epic: "atlas"}}
		args := `{"epic":"atlas"}`
		fake, outcome := listTool(t, listings, args)
		expectListText(t, args, outcome, "s1 agent=reviewer epic=atlas task=- end=running status=-")
		expectAsked(t, fake, "atlas")
	})

	t.Run("500 long steps keep their text within 4000 bytes and count the earlier steps cut", func(t *testing.T) {
		const total = 500
		// The epic is multi-byte, so each cut is by rune: 39 runes and the ellipsis.
		listings := make([]store.StepListing, 0, total)
		for i := 1; i <= total; i++ {
			listings = append(listings, store.StepListing{
				ID:     fmt.Sprintf("step-%03d", i),
				Agent:  "reviewer",
				Epic:   strings.Repeat("é", 300),
				Task:   strings.Repeat("t", 300),
				Status: strings.Repeat("s", 300),
				End:    "done",
			})
		}
		shortLine := func(i int) string {
			return fmt.Sprintf("step-%03d agent=reviewer epic=%s task=%s end=done status=%s", i,
				strings.Repeat("é", 39)+"…", strings.Repeat("t", 39)+"…", strings.Repeat("s", 39)+"…")
		}

		args := `{}`
		_, outcome := listTool(t, listings, args)
		if outcome.isError {
			t.Fatalf("step_list %s reported an error: %s", args, outcome.text)
		}
		text := outcome.text
		if len(text) > 4000 {
			t.Fatalf("step_list text is %d bytes, want at most 4000", len(text))
		}
		lines := strings.Split(text, "\n")
		header, kept := lines[0], lines[1:]
		dropped := total - len(kept)
		if want := fmt.Sprintf("[%d earlier steps not shown; pass epic to narrow]", dropped); header != want {
			t.Fatalf("step_list first line = %q, want %q", header, want)
		}
		// The kept lines are the newest steps, the last of them the newest step's.
		want := make([]string, 0, len(kept))
		for i := dropped + 1; i <= total; i++ {
			want = append(want, shortLine(i))
		}
		if !slices.Equal(kept, want) {
			t.Fatalf("step_list kept %d lines, not the newest %d steps as one line each", len(kept), len(want))
		}
		// Keeping as many as fit: one more step with its count one lower would exceed 4000 bytes.
		more := fmt.Sprintf("[%d earlier steps not shown; pass epic to narrow]", dropped-1)
		rest := len(text) - len(header)
		if grown := len(more) + 1 + len(shortLine(dropped)) + rest; grown <= 4000 {
			t.Fatalf("step_list text is %d bytes and could keep one more step within 4000", len(text))
		}
	})

	t.Run("a session with no steps reads as having started none", func(t *testing.T) {
		args := `{}`
		fake, outcome := listTool(t, nil, args)
		expectListText(t, args, outcome, "This session started no steps.")
		expectAsked(t, fake, "")

		args = `{"epic":"atlas"}`
		fake, outcome = listTool(t, nil, args)
		expectListText(t, args, outcome, "This session started no steps in epic atlas.")
		expectAsked(t, fake, "atlas")
	})

	t.Run("a call without caller headers is a tool error", func(t *testing.T) {
		fake := &listingSteps{}
		p := newPeer(t, startDaemonWithEnv(t, fake, func(string) string { return "" }), nil)

		outcome := p.tool("step_list", `{}`)
		if !outcome.isError {
			t.Fatalf("step_list = %q, want a tool error for a call with no caller", outcome.text)
		}
		if !strings.Contains(outcome.text, "carries no caller headers") {
			t.Fatalf("step_list error = %q, want it to name the missing caller headers", outcome.text)
		}
		if got := fake.askedWith(); len(got) != 0 {
			t.Fatalf("refused step_list reached the daemon as %+v, want no listing", got)
		}
	})
}
