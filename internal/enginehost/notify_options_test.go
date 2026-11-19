package enginehost

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// teeSink records each notice in its fakeSink and routes it through notifier, so a test sees both the notice and the
// push it sends. Its Changed and Seen only record, as the fakeSink does.
type teeSink struct {
	*fakeSink
	notifier *Notifier
}

func (s teeSink) Notify(ctx context.Context, n Notice) {
	s.fakeSink.Notify(ctx, n)
	s.notifier.Notify(ctx, n)
}

// sessionNoticeAndPush runs a headless session of prompt on a manager whose notices reach a phone through a real
// router. It returns the first notice the manager sent and the push that notice sent.
func sessionNoticeAndPush(t *testing.T, prompt string) (Notice, push.Notification) {
	t.Helper()
	sink := &fakeSink{}
	sender := &fakeSender{}
	notices := notice.New(sessionTestHost, sender, fixedClock, slog.New(slog.DiscardHandler))
	router := newSessionRouter(t, notices, &fakeRouteLog{})
	m, _ := newNoticeManager(t, fakeClaudePath(t), teeSink{
		fakeSink: sink,
		notifier: NewNotifier(NotifierConfig{Route: router}),
	})
	if _, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: prompt}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := waitNotices(t, engineOf(m), sink)
	waittest.Until(t, nil, "the first notice's push", func() bool { return len(sender.publishes()) == 1 })
	return got[0], sender.publishes()[0]
}

func TestPushOptionsAskNoticeAndPushCarryLabelsAndRequest(t *testing.T) {
	t.Parallel()
	got, note := sessionNoticeAndPush(t, "ASK")
	want := []string{"red", "blue"}
	if !slices.Equal(got.Options, want) || got.Request != "ask-1" {
		t.Fatalf("notice options = %q and request = %q, want %q and ask-1", got.Options, got.Request, want)
	}
	if !slices.Equal(note.Options, want) || note.Request != "ask-1" {
		t.Fatalf("push options = %q and request = %q, want %q and ask-1", note.Options, note.Request, want)
	}
}

func TestPushOptionsPermitNoticeAndPushCarryAllowDenyAndRequest(t *testing.T) {
	t.Parallel()
	got, note := sessionNoticeAndPush(t, "PERMIT")
	want := []string{"allow", "deny"}
	if !slices.Equal(got.Options, want) || got.Request != "permit-1" {
		t.Fatalf("notice options = %q and request = %q, want %q and permit-1", got.Options, got.Request, want)
	}
	if !slices.Equal(note.Options, want) || note.Request != "permit-1" {
		t.Fatalf("push options = %q and request = %q, want %q and permit-1", note.Options, note.Request, want)
	}
}

func TestPushOptionsDonePushCarriesNoOptionsOrRequest(t *testing.T) {
	t.Parallel()
	_, note := sessionNoticeAndPush(t, "hi")
	if note.State != protocol.StateDone || len(note.Options) != 0 || note.Request != "" {
		t.Fatalf("done push = state %q, options %q, request %q, want done with neither options nor request",
			note.State, note.Options, note.Request)
	}
}

func TestPushOptionsRequestChoicesOffersNone(t *testing.T) {
	t.Parallel()
	single := json.RawMessage(`{"questions":[{"question":"Which shade?","multiSelect":false,` +
		`"options":[{"label":"amber"},{"label":"teal"}]}]}`)
	twoQuestions := json.RawMessage(`{"questions":[{"question":"Which shade?","multiSelect":false,` +
		`"options":[{"label":"amber"}]},{"question":"Which fruit?","multiSelect":false,` +
		`"options":[{"label":"fig"}]}]}`)
	several := json.RawMessage(`{"questions":[{"question":"Which shades?","multiSelect":true,` +
		`"options":[{"label":"teal"},{"label":"ochre"}]}]}`)
	noOptions := json.RawMessage(`{"questions":[{"question":"Anything?","multiSelect":false,"options":[]}]}`)
	undecodable := json.RawMessage(`{"questions":`)
	cases := []struct {
		name  string
		event session.Event
	}{
		{
			name:  "two questions",
			event: session.Request{Kind: session.RequestQuestion, RequestID: "ask-7", Input: twoQuestions},
		},
		{
			name:  "multiSelect question",
			event: session.Request{Kind: session.RequestQuestion, RequestID: "ask-7", Input: several},
		},
		{
			name:  "refused request",
			event: session.Request{Kind: session.RequestQuestion, RequestID: "ask-7", Input: single, Refused: true},
		},
		{
			name:  "answered request",
			event: session.Request{Kind: session.RequestQuestion, RequestID: "ask-7", Input: single, Answered: true},
		},
		{
			name:  "question with no options",
			event: session.Request{Kind: session.RequestQuestion, RequestID: "ask-7", Input: noOptions},
		},
		{
			name:  "input that does not decode",
			event: session.Request{Kind: session.RequestQuestion, RequestID: "ask-7", Input: undecodable},
		},
		{name: "event that is not a request", event: session.TurnEnded{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			options, request := requestChoices(tc.event)
			if options != nil || request != "" {
				t.Fatalf("requestChoices = (%q, %q), want (nil, \"\")", options, request)
			}
		})
	}
}

func TestPushOptionsRestoredPushMatchesNotify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	router := &fakeRouter{}
	event := Notice{
		Session: notifierSession, Dir: notifierDir, State: "waiting", Body: "Which shade?",
		Options: []string{"amber", "teal"}, Request: "ask-7",
	}
	NewNotifier(NotifierConfig{Route: router}).Notify(ctx, event)
	if len(router.notices) != 1 {
		t.Fatalf("Notify calls = %d, want 1", len(router.notices))
	}
	built, err := router.notices[0].Push(ctx)
	if err != nil {
		t.Fatalf("Push of the notified notice: %v", err)
	}
	restored, err := RestoreNotice(ctx, router.notices[0].Source.Data, 0)
	if err != nil {
		t.Fatalf("RestoreNotice: %v", err)
	}
	pushed, err := restored.Push(ctx)
	if err != nil {
		t.Fatalf("Push of the restored notice: %v", err)
	}

	want := []string{"amber", "teal"}
	if !slices.Equal(built.Options, want) || built.Request != "ask-7" {
		t.Fatalf("notified push options = %q and request = %q, want %q and ask-7", built.Options, built.Request, want)
	}
	if !reflect.DeepEqual(pushed, built) {
		t.Fatalf("restored push = %+v, want the notified push %+v", pushed, built)
	}
}

func TestPushOptionsOldRowRestoresWithNoOptionsOrRequest(t *testing.T) {
	t.Parallel()
	old := []byte(`{"session":"0123456789abcdef0123456789abcdef","dir":"/x/proj",` +
		`"state":"waiting","body":"Which shade?"}`)
	restored, err := RestoreNotice(context.Background(), old, 0)
	if err != nil {
		t.Fatalf("RestoreNotice of a row without options: %v", err)
	}
	note, err := restored.Push(context.Background())
	if err != nil {
		t.Fatalf("Push of the restored row: %v", err)
	}
	if note.Question != "Which shade?" || len(note.Options) != 0 || note.Request != "" {
		t.Fatalf("restored push = question %q, options %q, request %q, want the question with neither options "+
			"nor request", note.Question, note.Options, note.Request)
	}
}
