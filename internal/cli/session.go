package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// sessionBaseURL is the URL the session client sends its RPCs to. The transport dials the session socket for every
// request, so the host name is never resolved.
const sessionBaseURL = "http://orreryd"

// Argument counts of the session verbs that take arguments after the verb.
const (
	sessionOneArg    = 1
	sessionTwoArgs   = 2
	sessionThreeArgs = 3
)

// sessionDialError reports that the session socket cannot be dialed.
type sessionDialError struct {
	err error
}

// Error returns the dial failure.
func (e sessionDialError) Error() string {
	return e.err.Error()
}

// Unwrap returns the dial failure.
func (e sessionDialError) Unwrap() error {
	return e.err
}

// Session implements `orrery session VERB ARGS`. Every verb but watch is one RPC over the Engine service on the
// private session socket at socketPath. Watch reads the session's events from the local store and needs no socket.
// Start writes the new session's id to out and nothing else.
func Session(ctx context.Context, socketPath string, args []string, out io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return usageError{msg: "session: want a verb: start, say, answer, interrupt, stop, seen, or watch"}
	}
	verb, rest := args[0], args[1:]
	if verb == "watch" {
		return SessionWatch(ctx, rest, out, getenv)
	}
	client := enginev1connect.NewEngineClient(sessionHTTPClient(socketPath), sessionBaseURL)
	switch verb {
	case "start":
		return sessionStart(ctx, client, socketPath, rest, out)
	case "say":
		return sessionSay(ctx, client, socketPath, rest)
	case "answer":
		return sessionAnswer(ctx, client, socketPath, rest)
	case "interrupt":
		return sessionInterrupt(ctx, client, socketPath, rest)
	case "stop":
		return sessionStop(ctx, client, socketPath, rest)
	case "seen":
		return sessionSeen(ctx, client, socketPath, rest)
	default:
		return usageError{msg: fmt.Sprintf("session: unknown verb %q", verb)}
	}
}

// sessionStart sends the start RPC for `session start DIR PROMPT` and writes the new id to out.
func sessionStart(
	ctx context.Context, client enginev1connect.EngineClient, socketPath string, args []string, out io.Writer,
) error {
	if len(args) != sessionTwoArgs {
		return sessionForm("start", "DIR PROMPT")
	}
	dir, err := filepath.Abs(args[0])
	if err != nil {
		return sessionFailure("start", socketPath, err)
	}
	resp, err := client.Start(ctx, connect.NewRequest(&v1.StartRequest{Dir: dir, Prompt: args[1]}))
	if err != nil {
		return sessionFailure("start", socketPath, err)
	}
	if _, err = fmt.Fprintln(out, resp.Msg.GetId()); err != nil {
		return sessionFailure("start", socketPath, fmt.Errorf("write id: %w", err))
	}
	return nil
}

// sessionSay sends the say RPC for `session say ID TEXT`.
func sessionSay(ctx context.Context, client enginev1connect.EngineClient, socketPath string, args []string) error {
	if len(args) != sessionTwoArgs {
		return sessionForm("say", "ID TEXT")
	}
	_, err := client.Say(ctx, connect.NewRequest(&v1.SayRequest{Id: args[0], Text: args[1]}))
	return sessionFailure("say", socketPath, err)
}

// sessionAnswer sends the answer RPC for `session answer ID REQUEST_ID ANSWER`.
func sessionAnswer(ctx context.Context, client enginev1connect.EngineClient, socketPath string, args []string) error {
	if len(args) != sessionThreeArgs {
		return sessionForm("answer", "ID REQUEST_ID ANSWER")
	}
	_, err := client.Answer(ctx, connect.NewRequest(&v1.AnswerRequest{
		Id:        args[0],
		RequestId: args[1],
		Answer:    args[2],
	}))
	return sessionFailure("answer", socketPath, err)
}

// sessionInterrupt sends the interrupt RPC for `session interrupt ID`.
func sessionInterrupt(
	ctx context.Context,
	client enginev1connect.EngineClient,
	socketPath string,
	args []string,
) error {
	if len(args) != sessionOneArg {
		return sessionForm("interrupt", "ID")
	}
	_, err := client.Interrupt(ctx, connect.NewRequest(&v1.InterruptRequest{Id: args[0]}))
	return sessionFailure("interrupt", socketPath, err)
}

// sessionStop sends the stop RPC for `session stop ID`.
func sessionStop(ctx context.Context, client enginev1connect.EngineClient, socketPath string, args []string) error {
	if len(args) != sessionOneArg {
		return sessionForm("stop", "ID")
	}
	_, err := client.Stop(ctx, connect.NewRequest(&v1.StopRequest{Id: args[0]}))
	return sessionFailure("stop", socketPath, err)
}

// sessionSeen sends the seen RPC for `session seen ID`.
func sessionSeen(ctx context.Context, client enginev1connect.EngineClient, socketPath string, args []string) error {
	if len(args) != sessionOneArg {
		return sessionForm("seen", "ID")
	}
	_, err := client.Seen(ctx, connect.NewRequest(&v1.SeenRequest{Id: args[0]}))
	return sessionFailure("seen", socketPath, err)
}

// sessionHTTPClient returns an HTTP client whose every connection dials the unix socket at socketPath.
func sessionHTTPClient(socketPath string) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, "unix", socketPath)
			if err != nil {
				return nil, sessionDialError{err: err}
			}
			return conn, nil
		},
	}
	return &http.Client{Transport: transport}
}

// sessionForm returns the usage error for verb given the wrong arguments. form names the arguments it takes.
func sessionForm(verb, form string) error {
	return usageError{msg: fmt.Sprintf("session %s: want %s", verb, form)}
}

// sessionFailure returns the error for verb's failure err, or nil when err is nil. A socket that cannot be dialed
// means orreryd is not taking session commands, and the error names the --sessions flag that enables them.
func sessionFailure(verb, socketPath string, err error) error {
	if err == nil {
		return nil
	}
	var dialErr sessionDialError
	if errors.As(err, &dialErr) {
		return fmt.Errorf("session %s: orreryd takes no session commands on %s; start it with --sessions",
			verb, socketPath)
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return fmt.Errorf("session %s: %s", verb, connectErr.Message())
	}
	return fmt.Errorf("session %s: %w", verb, err)
}
