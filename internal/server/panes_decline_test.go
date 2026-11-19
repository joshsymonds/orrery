package server_test

import (
	"strconv"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// waitingPaneOptions returns the options of a waiting pane that began waiting at newestUnix0900, asking synthQuestion,
// with its transcript at path.
func waitingPaneOptions(path string) map[string]string {
	options := liveOptions(path)
	options[protocol.OptionState] = string(protocol.StateWaiting)
	options[protocol.OptionChanged] = strconv.FormatInt(newestUnix0900, 10)
	options[protocol.OptionQuestion] = synthQuestion
	return options
}

func TestDeclineListingEndsWaitingPane(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeNewestTranscript(t,
		newestToolLine(newestStamp0900),
		interruptLine(newestStamp0905))
	pane := conversationPane(t, tmux, waitingPaneOptions(path))

	listed := listedPane(t, newHandler(t, tmux), pane)
	if listed.GetState() != string(protocol.StateDone) {
		t.Fatalf("state = %q, want %q", listed.GetState(), protocol.StateDone)
	}
	if listed.GetQuestion() != "" {
		t.Fatalf("question = %q, want empty", listed.GetQuestion())
	}
	if listed.GetQuestionHash() != protocol.QuestionHash("") {
		t.Fatalf("question_hash = %q, want the hash of an empty question", listed.GetQuestionHash())
	}
	if got, _ := paneOption(t, tmux, pane, protocol.OptionState); got != string(protocol.StateDone) {
		t.Fatalf("%s = %q, want %q", protocol.OptionState, got, protocol.StateDone)
	}
	if _, ok := paneOption(t, tmux, pane, protocol.OptionQuestion); ok {
		t.Fatalf("%s is set, want unset", protocol.OptionQuestion)
	}
}

func TestDeclineListingKeepsWaitingPane(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeNewestTranscript(t, newestToolLine(newestStamp0900))
	pane := conversationPane(t, tmux, waitingPaneOptions(path))

	listed := listedPane(t, newHandler(t, tmux), pane)
	if listed.GetState() != string(protocol.StateWaiting) {
		t.Fatalf("state = %q, want %q", listed.GetState(), protocol.StateWaiting)
	}
	if listed.GetQuestion() != synthQuestion {
		t.Fatalf("question = %q, want %q", listed.GetQuestion(), synthQuestion)
	}
	if listed.GetQuestionHash() != protocol.QuestionHash(synthQuestion) {
		t.Fatalf("question_hash = %q, want the hash of the question", listed.GetQuestionHash())
	}
	if got, _ := paneOption(t, tmux, pane, protocol.OptionState); got != string(protocol.StateWaiting) {
		t.Fatalf("%s = %q, want %q", protocol.OptionState, got, protocol.StateWaiting)
	}
	if got, _ := paneOption(t, tmux, pane, protocol.OptionQuestion); got != synthQuestion {
		t.Fatalf("%s = %q, want %q", protocol.OptionQuestion, got, synthQuestion)
	}
}
