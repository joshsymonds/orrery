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

// SessionBaseURL is the URL the session client sends its RPCs to. The transport dials the session socket for every
// request, so the host name is never resolved.
const SessionBaseURL = "http://orreryd"

// sessionURLFlag sends the session verbs to serve at the URL that follows it.
const sessionURLFlag = "--url"

// sessionVerbUsage is the usage error for a session command that names no verb.
const sessionVerbUsage = "session: want a verb: start, say, answer, interrupt, stop, seen, or watch"

// sessionStartForm names the arguments of `session start`.
const sessionStartForm = "[--model MODEL] DIR PROMPT"

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

// Session implements `orrery session [--url URL] VERB ARGS`. Without --url, every verb but watch is one RPC over the
// Engine service on the private session socket at socketPath, and watch reads the session's events from the local
// store. With --url, the verbs go through serve at URL instead, and the socket is not dialed. Start writes the new
// session's id to out and nothing else.
func Session(ctx context.Context, socketPath string, args []string, out io.Writer, getenv func(string) string) error {
	if len(args) > 0 && args[0] == sessionURLFlag {
		if len(args) < sessionTwoArgs || args[1] == "" {
			return usageError{msg: "session: --url needs a URL"}
		}
		return sessionURL(ctx, args[1], args[2:], out)
	}
	if len(args) == 0 {
		return usageError{msg: sessionVerbUsage}
	}
	verb, rest := args[0], args[1:]
	if verb == "watch" {
		return SessionWatch(ctx, rest, out, getenv)
	}
	client := enginev1connect.NewEngineClient(SessionHTTPClient(socketPath), SessionBaseURL)
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

// sessionStartArgs is the parsed arguments of `session start`.
type sessionStartArgs struct {
	model  string
	dir    string
	prompt string
}

// parseStartArgs reads `[--model MODEL] DIR PROMPT`. It returns false when args have another form.
func parseStartArgs(args []string) (sessionStartArgs, bool) {
	var start sessionStartArgs
	if len(args) >= sessionTwoArgs && args[0] == "--model" {
		start.model = args[1]
		args = args[sessionTwoArgs:]
	}
	if len(args) != sessionTwoArgs {
		return sessionStartArgs{}, false
	}
	start.dir, start.prompt = args[0], args[1]
	return start, true
}

// sessionStart sends the start RPC for `session start [--model MODEL] DIR PROMPT` and writes the new id to out.
func sessionStart(
	ctx context.Context, client enginev1connect.EngineClient, socketPath string, args []string, out io.Writer,
) error {
	start, ok := parseStartArgs(args)
	if !ok {
		return sessionForm("start", sessionStartForm)
	}
	dir, err := filepath.Abs(start.dir)
	if err != nil {
		return sessionFailure("start", socketPath, err)
	}
	resp, err := client.Start(ctx, connect.NewRequest(&v1.StartRequest{
		Dir:    dir,
		Prompt: start.prompt,
		Model:  start.model,
	}))
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

// SessionHTTPClient returns an HTTP client whose every connection dials the unix socket at socketPath, so a socket
// nothing listens on fails every request with a dial error.
func SessionHTTPClient(socketPath string) *http.Client {
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
	var dialErr sessionDialError
	if errors.As(err, &dialErr) {
		return fmt.Errorf("session %s: orreryd takes no session commands on %s; start it with --sessions",
			verb, socketPath)
	}
	return sessionRPCFailure(verb, err)
}

// sessionRPCFailure returns the error for verb's RPC failure err, or nil when err is nil. A Connect error reads with
// its message.
func sessionRPCFailure(verb string, err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return fmt.Errorf("session %s: %s", verb, connectErr.Message())
	}
	return fmt.Errorf("session %s: %w", verb, err)
}
