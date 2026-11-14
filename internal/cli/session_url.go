package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// sessionURL implements `orrery session --url URL VERB ARGS`. Start, say, answer, interrupt, stop and seen call
// serve's Control service at rawURL, and watch reads serve's Events stream, retrying on clock. Every request carries
// the client header serve's guard requires.
func sessionURL(ctx context.Context, clock rpcretry.Clock, rawURL string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return usageError{msg: sessionVerbUsage}
	}
	verb, rest := args[0], args[1:]
	httpClient := &http.Client{Transport: clientTransport{base: http.DefaultTransport}}
	if verb == "watch" {
		return sessionWatchURL(ctx, clock, httpClient, rawURL, rest, out)
	}
	control := orreryv1connect.NewControlClient(httpClient, rawURL)
	switch verb {
	case "start":
		return sessionStartURL(ctx, control, rest, out)
	case "say":
		return sessionSayURL(ctx, control, rest)
	case "answer":
		return sessionAnswerURL(ctx, control, rest)
	case "interrupt":
		return sessionInterruptURL(ctx, control, rest)
	case "stop":
		return sessionStopURL(ctx, control, rest)
	case "seen":
		return sessionSeenURL(ctx, control, rest)
	default:
		return usageError{msg: fmt.Sprintf("session: unknown verb %q", verb)}
	}
}

// sessionStartURL calls Control.Start for `session --url URL start [--model MODEL] DIR PROMPT` and writes the new id
// to out. DIR is sent as given.
func sessionStartURL(ctx context.Context, control orreryv1connect.ControlClient, args []string, out io.Writer) error {
	start, ok := parseStartArgs(args)
	if !ok {
		return sessionForm("start", sessionStartForm)
	}
	resp, err := control.Start(ctx, connect.NewRequest(&orreryv1.StartRequest{
		Dir:    start.dir,
		Prompt: start.prompt,
		Model:  start.model,
	}))
	if err != nil {
		return sessionRPCFailure("start", err)
	}
	if _, err = fmt.Fprintln(out, resp.Msg.GetId()); err != nil {
		return sessionRPCFailure("start", fmt.Errorf("write id: %w", err))
	}
	return nil
}

// sessionSayURL calls Control.Say for `session --url URL say ID TEXT`.
func sessionSayURL(ctx context.Context, control orreryv1connect.ControlClient, args []string) error {
	if len(args) != sessionTwoArgs {
		return sessionForm("say", "ID TEXT")
	}
	_, err := control.Say(ctx, connect.NewRequest(&orreryv1.SayRequest{Id: args[0], Text: args[1]}))
	return sessionRPCFailure("say", err)
}

// sessionAnswerURL calls Control.Answer for `session --url URL answer ID REQUEST_ID ANSWER`.
func sessionAnswerURL(ctx context.Context, control orreryv1connect.ControlClient, args []string) error {
	if len(args) != sessionThreeArgs {
		return sessionForm("answer", "ID REQUEST_ID ANSWER")
	}
	_, err := control.Answer(ctx, connect.NewRequest(&orreryv1.AnswerRequest{
		Id:        args[0],
		RequestId: args[1],
		Answer:    args[2],
	}))
	return sessionRPCFailure("answer", err)
}

// sessionInterruptURL calls Control.Interrupt for `session --url URL interrupt ID`.
func sessionInterruptURL(ctx context.Context, control orreryv1connect.ControlClient, args []string) error {
	if len(args) != sessionOneArg {
		return sessionForm("interrupt", "ID")
	}
	_, err := control.Interrupt(ctx, connect.NewRequest(&orreryv1.InterruptRequest{Id: args[0]}))
	return sessionRPCFailure("interrupt", err)
}

// sessionStopURL calls Control.Stop for `session --url URL stop ID`.
func sessionStopURL(ctx context.Context, control orreryv1connect.ControlClient, args []string) error {
	if len(args) != sessionOneArg {
		return sessionForm("stop", "ID")
	}
	_, err := control.Stop(ctx, connect.NewRequest(&orreryv1.StopRequest{Id: args[0]}))
	return sessionRPCFailure("stop", err)
}

// sessionSeenURL calls Control.Seen for `session --url URL seen ID`.
func sessionSeenURL(ctx context.Context, control orreryv1connect.ControlClient, args []string) error {
	if len(args) != sessionOneArg {
		return sessionForm("seen", "ID")
	}
	_, err := control.Seen(ctx, connect.NewRequest(&orreryv1.SeenRequest{Id: args[0]}))
	return sessionRPCFailure("seen", err)
}

// sessionWatchURL implements `session --url URL watch ID`. It lists serve's newest seq, then writes the session's
// messages from the Events stream, from seq 0 on, or from the newest seq when serve answers cursor 0 out of range
// before any message, until it returns. A stream that ends with Unavailable is reopened after the last message it
// delivered, and the retries give up rpcretry.Window after that message.
func sessionWatchURL(
	ctx context.Context,
	clock rpcretry.Clock,
	httpClient *http.Client,
	rawURL string,
	args []string,
	out io.Writer,
) error {
	if len(args) != sessionOneArg {
		return sessionForm("watch", "ID")
	}
	listed, err := orreryv1connect.NewSessionsClient(httpClient, rawURL).List(
		ctx, connect.NewRequest(&orreryv1.ListRequest{}),
	)
	if err != nil {
		return sessionRPCFailure("watch", err)
	}
	newest := listed.Msg.GetSeq()
	events := orreryv1connect.NewEventsClient(httpClient, rawURL)
	watch := &urlWatch{id: args[0], newest: newest, out: out}
	err = watch.run(ctx, clock, events)
	if connect.CodeOf(err) == connect.CodeOutOfRange && watch.messages == 0 {
		watch = &urlWatch{id: args[0], after: newest, newest: newest, out: out}
		err = watch.run(ctx, clock, events)
	}
	return sessionRPCFailure("watch", err)
}

// urlWatch is one watch of the session id over serve's Events stream. It keeps the cursor a reopened stream resumes
// after, the state the session was last seen in, and how many messages have arrived, so that a reopened stream carries
// on where the stream before it ended. newest is the seq serve listed when the watch began.
type urlWatch struct {
	id       string
	newest   int64
	after    int64
	state    string
	messages int
	out      io.Writer
}

// run reads the session's messages until it stops or fails. Each rpcretry.Do call opens the stream and reads it. A
// stream that delivered messages and then ended with Unavailable ends its call, so the next call's window starts from
// the last message. A call whose open or stream fails with Unavailable before any message is retried within
// rpcretry.Window of its start.
func (w *urlWatch) run(ctx context.Context, clock rpcretry.Clock, events orreryv1connect.EventsClient) error {
	for {
		reopen := false
		err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
			var err error
			reopen, err = w.read(callCtx, events)
			return err
		})
		if !reopen {
			return errors.Join(err)
		}
	}
}

// read opens the Events stream after the cursor and writes the session's messages from it. It reports whether the
// stream ended with Unavailable after it delivered a message, which means run opens it again. Otherwise it returns the
// error that ended the watch, or nil when the session stopped.
func (w *urlWatch) read(ctx context.Context, events orreryv1connect.EventsClient) (bool, error) {
	stream, err := events.Watch(ctx, connect.NewRequest(&orreryv1.WatchRequest{After: w.after}))
	if err != nil {
		return false, fmt.Errorf("open stream: %w", err)
	}
	before := w.messages
	err = errors.Join(w.write(stream), stream.Close())
	if rpcretry.Unavailable(err) && w.messages > before {
		return true, nil
	}
	return false, err
}

// write writes each message of the session that stream delivers, as one line, and records each message's seq as the
// cursor. It returns nil once a message at or past newest leaves the session stopped or failed. A stream that ends
// before then is an error.
func (w *urlWatch) write(stream *connect.ServerStreamForClient[orreryv1.WatchResponse]) error {
	for stream.Receive() {
		msg := stream.Msg()
		w.after = msg.GetSeq()
		w.messages++
		if msg.GetSessionId() == w.id {
			if next, ok := engineState(msg); ok {
				w.state = next
			}
			if err := writeLine(w.out, msg); err != nil {
				return err
			}
		}
		if msg.GetSeq() >= w.newest && (w.state == sessionStopped || w.state == sessionFailed) {
			return nil
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("receive: %w", err)
	}
	return fmt.Errorf("stream ended before session %s stopped", w.id)
}

// engineState returns the state that msg sets for a headless session, and whether msg sets one.
func engineState(msg *orreryv1.WatchResponse) (string, bool) {
	if added := msg.GetEngineAdded(); added != nil {
		return added.GetState(), true
	}
	if changed := msg.GetEngineChanged(); changed != nil && changed.State != nil {
		return changed.GetState(), true
	}
	return "", false
}
