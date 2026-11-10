package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// ErrUsage matches every command line error that orrery reports with its usage text.
var ErrUsage = errors.New("usage")

// usageError is a command line refused before any work. It matches ErrUsage.
type usageError struct {
	msg string
}

// Error returns the message that names the refused command line.
func (e usageError) Error() string {
	return e.msg
}

// Is reports whether target is ErrUsage.
func (usageError) Is(target error) bool {
	return target == ErrUsage
}

// serveClientHeader is the header every request carries so the serve guard admits a Connect request. Its value only
// has to be non-empty.
const (
	serveClientHeader = "X-Orrery-Client"
	serveClientValue  = "orrery events"
)

// clientTransport adds serveClientHeader to every request it sends.
type clientTransport struct {
	base http.RoundTripper
}

// RoundTrip sends a copy of req with serveClientHeader set.
func (t clientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(serveClientHeader, serveClientValue)
	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, fmt.Errorf("orrery events: request: %w", err)
	}
	return resp, nil
}

// Events writes the event stream of the serve at --url to out, one compact JSON object per line. It starts after
// --after, or after the newest seq of the serve when --after is absent. Without --follow it writes the events up to
// the newest seq it saw at start and returns nil. With --follow it writes new events until the stream ends, and
// returns the error that ended the stream.
func Events(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("events", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	rawURL := flags.String("url", "", "base URL of the serve to read")
	after := flags.Int64("after", 0, "seq to start after, in place of the newest seq")
	follow := flags.Bool("follow", false, "keep writing new events until the stream ends")
	if err := flags.Parse(args); err != nil {
		return usageError{msg: fmt.Sprintf("orrery events: %v", err)}
	}
	if flags.NArg() > 0 {
		return usageError{msg: fmt.Sprintf("orrery events: unexpected argument %q", flags.Arg(0))}
	}
	if *rawURL == "" {
		return usageError{msg: "orrery events: --url is required"}
	}
	afterSet := false
	flags.Visit(func(f *flag.Flag) {
		afterSet = afterSet || f.Name == "after"
	})

	httpClient := &http.Client{Transport: clientTransport{base: http.DefaultTransport}}
	sessions := orreryv1connect.NewSessionsClient(httpClient, *rawURL)
	listed, err := sessions.List(ctx, connect.NewRequest(&orreryv1.ListRequest{}))
	if err != nil {
		return fmt.Errorf("orrery events: list: %w", err)
	}
	newest := listed.Msg.GetSeq()
	cursor := newest
	if afterSet {
		cursor = *after
	}
	if cursor > newest {
		return fmt.Errorf("orrery events: cursor %d is past the end of the log (newest seq %d)", cursor, newest)
	}
	if !*follow && cursor >= newest {
		return nil
	}

	events := orreryv1connect.NewEventsClient(httpClient, *rawURL)
	stream, err := events.Watch(ctx, connect.NewRequest(&orreryv1.WatchRequest{After: cursor}))
	if err != nil {
		return fmt.Errorf("orrery events: watch: %w", err)
	}
	return errors.Join(writeEvents(stream, out, *follow, newest), stream.Close())
}

// writeEvents writes each event the stream delivers as one line. Without follow it returns nil once it has written
// the event at newest, and it returns an error when the stream ends before that. With follow it returns the error
// the stream ended with.
func writeEvents(
	stream *connect.ServerStreamForClient[orreryv1.WatchResponse],
	out io.Writer,
	follow bool,
	newest int64,
) error {
	for stream.Receive() {
		msg := stream.Msg()
		if err := writeLine(out, msg); err != nil {
			return err
		}
		if !follow && msg.GetSeq() >= newest {
			return nil
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("orrery events: %w", err)
	}
	if !follow {
		return fmt.Errorf("orrery events: stream ended before seq %d", newest)
	}
	return nil
}

// writeLine writes msg as one line of compact JSON.
func writeLine(out io.Writer, msg *orreryv1.WatchResponse) error {
	encoded, err := protojson.Marshal(msg)
	if err != nil {
		return fmt.Errorf("orrery events: encode seq %d: %w", msg.GetSeq(), err)
	}
	var line bytes.Buffer
	if err = json.Compact(&line, encoded); err != nil {
		return fmt.Errorf("orrery events: compact seq %d: %w", msg.GetSeq(), err)
	}
	line.WriteByte('\n')
	if _, err = out.Write(line.Bytes()); err != nil {
		return fmt.Errorf("orrery events: write: %w", err)
	}
	return nil
}
