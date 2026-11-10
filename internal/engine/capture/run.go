package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/proc"
	"github.com/joshsymonds/orrery/internal/engine/transport"
	"github.com/joshsymonds/orrery/internal/engine/wire"
)

const (
	runInitRequestID      = "req_init"
	runModeRequestID      = "req_mode"
	runInterruptRequestID = "req_interrupt"
	runUUIDFormat         = "00000000-0000-4000-8000-%012d"
	runResumeUUIDFormat   = "00000000-0000-4000-8001-%012d"
	runTypeResult         = "result"
	runTypeStreamEvent    = "stream_event"
	runSubtypeCanUseTool  = "can_use_tool"
	runUndecodable        = "undecodable"
)

// Sinks receives copies of the bytes that Run exchanges with the probe. In receives every byte Run
// writes to the probe's stdin, and Out receives every byte read from the probe's stdout. Both must be
// non-nil and must be distinct writers, because each is written from its own goroutine.
type Sinks struct {
	In  io.Writer
	Out io.Writer
}

// Outcome reports what one run of a probe observed. Completed is true when the results seen are at least the
// number of prompts. TimedOut is true when the process did not end on its own within the command's timeout.
// Answered lists each request the probe answered, allowed or denied, in arrival order. Unanswered lists each
// request the probe did not answer, as its subtype, or as can_use_tool with the tool name. Both use the same
// form.
type Outcome struct {
	Completed  bool
	TimedOut   bool
	Answered   []string
	Unanswered []string
	Stderr     string
	ExitErr    error
}

// Run starts cmd, plays probe's script over its stdin and stdout, copies every byte each way into sinks,
// and stops the process before it returns, whether the run completed, timed out, or failed. A request
// that does not decode, or whose answer cannot be built, is listed in Unanswered and the run goes on.
// Run returns an error when the process cannot start, when a send to the probe or a write to a sink
// fails, when the probe sends a control response that does not decode, when the context ends first,
// when the process cannot be stopped, or when the probe's streams cannot be closed. Once the process has
// started, Run returns the Outcome it observed beside any error.
func Run(ctx context.Context, probe Probe, cmd Command, sinks Sinks) (Outcome, error) {
	process, err := proc.Start(proc.Config{Path: cmd.Path, Args: cmd.Args, Dir: cmd.Dir, Env: cmd.Env})
	if err != nil {
		return Outcome{}, fmt.Errorf("capture: start %s: %w", probe.Name, err)
	}
	input := &runInput{writer: io.MultiWriter(process.Stdin(), sinks.In), closer: process.Stdin()}
	output := &runOutput{stream: process.Stdout(), sink: sinks.Out}
	// A resumed session already holds the uuids its source sent, so a resume probe numbers its own from a
	// range its source never uses.
	format := runUUIDFormat
	if probe.ResumeOf != "" {
		format = runResumeUUIDFormat
	}
	uuids := 0
	conn := transport.New(input, output, transport.WithUUIDGenerator(func() (string, error) {
		uuids++
		return fmt.Sprintf(format, uuids), nil
	}))
	session := &runSession{probe: probe, process: process, conn: conn, input: input}
	timer := time.NewTimer(cmd.Timeout)
	defer timer.Stop()

	playErr := session.play(ctx, timer.C)
	stopErr := process.Stop()
	closeErr := conn.Close()
	outcome := Outcome{
		Completed:  session.results >= len(probe.Prompts),
		TimedOut:   session.timedOut,
		Answered:   session.answered,
		Unanswered: session.unanswered,
		Stderr:     process.Stderr(),
		ExitErr:    process.Wait(),
	}
	return outcome, errors.Join(playErr, stopErr, closeErr, output.sinkErr)
}

// runInput writes the probe's stdin and copies every written byte into the In sink. Close closes the pipe
// once, so the session and the transport can both close it.
type runInput struct {
	writer    io.Writer
	closer    io.Closer
	closeOnce sync.Once
	closeErr  error
}

// Write implements io.Writer over the probe's stdin.
func (input *runInput) Write(data []byte) (int, error) {
	n, err := input.writer.Write(data)
	if err != nil {
		return n, fmt.Errorf("capture: write probe input: %w", err)
	}
	return n, nil
}

// Close closes the probe's stdin on the first call and returns that call's error on every call.
func (input *runInput) Close() error {
	input.closeOnce.Do(func() {
		if err := input.closer.Close(); err != nil {
			input.closeErr = fmt.Errorf("capture: close probe input: %w", err)
		}
	})
	return input.closeErr
}

// runOutput reads the probe's stdout and copies every byte it reads into the Out sink. A sink failure
// ends the read, and the failure is kept in sinkErr for Run to report once the transport has closed.
type runOutput struct {
	stream    io.ReadCloser
	sink      io.Writer
	sinkErr   error
	closeOnce sync.Once
	closeErr  error
}

// Read implements io.Reader over the probe's stdout.
func (output *runOutput) Read(data []byte) (int, error) {
	n, err := output.stream.Read(data)
	if n > 0 {
		if _, writeErr := output.sink.Write(data[:n]); writeErr != nil {
			output.sinkErr = fmt.Errorf("capture: write probe output: %w", writeErr)
			return n, output.sinkErr
		}
	}
	if err != nil {
		return n, fmt.Errorf("capture: read probe output: %w", err)
	}
	return n, nil
}

// Close closes the probe's stdout on the first call and returns that call's error on every call.
func (output *runOutput) Close() error {
	output.closeOnce.Do(func() {
		if err := output.stream.Close(); err != nil {
			output.closeErr = fmt.Errorf("capture: close probe output: %w", err)
		}
	})
	return output.closeErr
}

// runSession plays one probe's script over a transport. Run's goroutine is its only driver, so its fields
// need no locks.
type runSession struct {
	probe       Probe
	process     *proc.Process
	conn        *transport.Transport
	input       *runInput
	sent        int
	results     int
	interrupted bool
	timedOut    bool
	linger      <-chan time.Time
	answered    []string
	unanswered  []string
}

// play sends initialize and then answers the probe's events and controls until both streams close, the
// timeout fires, or the context ends. Once the streams close it waits for the process to exit, within the
// same timeout.
func (session *runSession) play(ctx context.Context, timeout <-chan time.Time) error {
	if err := session.conn.SendInitialize(runInitRequestID, nil); err != nil {
		return fmt.Errorf("capture: send initialize: %w", err)
	}
	events := session.conn.Events()
	controls := session.conn.Controls()
	for events != nil || controls != nil {
		select {
		case <-ctx.Done():
			return fmt.Errorf("capture: run %s: %w", session.probe.Name, ctx.Err())
		case <-timeout:
			session.timedOut = true
			return nil
		case <-session.linger:
			if err := session.input.Close(); err != nil {
				return err
			}
		case message, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := session.onEvent(message); err != nil {
				return err
			}
		case control, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			if err := session.onControl(control); err != nil {
				return err
			}
		}
	}
	return session.awaitExit(ctx, timeout)
}

// awaitExit waits for the process to exit on its own, within the timeout.
func (session *runSession) awaitExit(ctx context.Context, timeout <-chan time.Time) error {
	select {
	case <-session.process.Done():
		return nil
	case <-timeout:
		session.timedOut = true
		return nil
	case <-ctx.Done():
		return fmt.Errorf("capture: run %s: %w", session.probe.Name, ctx.Err())
	}
}

// onEvent sends the single interrupt at the first stream event when the probe asks for one, and advances
// the script at each result.
func (session *runSession) onEvent(message wire.SDKMessage) error {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(message.BorrowedJSON(), &envelope); err != nil {
		return fmt.Errorf("capture: decode probe message: %w", err)
	}
	switch envelope.Type {
	case runTypeStreamEvent:
		if !session.probe.Interrupt || session.interrupted {
			return nil
		}
		session.interrupted = true
		if err := session.conn.SendInterrupt(runInterruptRequestID); err != nil {
			return fmt.Errorf("capture: send interrupt: %w", err)
		}
	case runTypeResult:
		session.results++
		return session.advance()
	}
	return nil
}

// onControl answers permission requests and advances the script past the responses it waits for.
func (session *runSession) onControl(control transport.ControlMessage) error {
	switch control.Type {
	case transport.ControlRequest:
		return session.onRequest(control)
	case transport.ControlResponse:
		return session.onResponse(control)
	default:
		return nil
	}
}

// onResponse advances the script past the initialize response and the permission-mode response.
func (session *runSession) onResponse(control transport.ControlMessage) error {
	var response wire.SDKControlResponse
	if err := wire.UnmarshalControlJSON(control.RawJSON(), &response); err != nil {
		return fmt.Errorf("capture: decode control response: %w", err)
	}
	switch response.Response.RequestID {
	case runInitRequestID:
		if session.probe.ModeChange == "" {
			return session.advance()
		}
		if err := session.conn.SendSetPermissionMode(runModeRequestID, session.probe.ModeChange); err != nil {
			return fmt.Errorf("capture: send permission mode: %w", err)
		}
	case runModeRequestID:
		return session.advance()
	}
	return nil
}

// runReply is the probe's response to one control request: the request it answers and the decision.
type runReply struct {
	requestID string
	decision  Decision
}

// onRequest sends the probe's answer to a request it answers and lists the request as answered. Every
// other request is listed as unanswered, and the run goes on.
func (session *runSession) onRequest(control transport.ControlMessage) error {
	reply, listing, answered := runJudge(session.probe.Answer, control)
	if !answered {
		session.unanswered = append(session.unanswered, listing)
		return nil
	}
	if err := session.answer(reply.requestID, reply.decision); err != nil {
		return err
	}
	session.answered = append(session.answered, listing)
	return nil
}

// runJudge decides how the probe responds to one control request. When the probe answers it, the bool is
// true, the reply holds what to send, and the string is the Answered entry for the request. Otherwise the
// string is the Unanswered entry for the request. A request that does not decode, or whose answer cannot
// be built, is listed with its error.
func runJudge(answer Answer, control transport.ControlMessage) (runReply, string, bool) {
	var request wire.SDKControlRequest
	if err := wire.UnmarshalControlJSON(control.RawJSON(), &request); err != nil {
		return runReply{}, runListing(runUndecodable, "", err), false
	}
	var head struct {
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(request.Request, &head); err != nil {
		return runReply{}, runListing(runUndecodable, "", err), false
	}
	if head.Subtype != runSubtypeCanUseTool {
		return runReply{}, runListing(head.Subtype, "", nil), false
	}
	var permission wire.SDKControlPermissionRequest
	if err := wire.UnmarshalControlJSON(request.Request, &permission); err != nil {
		return runReply{}, runListing(runSubtypeCanUseTool, "", err), false
	}
	decision, answered, err := Decide(answer, permission)
	if err != nil {
		return runReply{}, runListing(runSubtypeCanUseTool, permission.ToolName, err), false
	}
	if !answered {
		return runReply{}, runListing(runSubtypeCanUseTool, permission.ToolName, nil), false
	}
	reply := runReply{requestID: request.RequestID, decision: decision}
	return reply, runListing(runSubtypeCanUseTool, permission.ToolName, nil), true
}

// runListing formats an Answered or Unanswered entry: the subtype, then the tool name when one is known, then the
// error when one is known.
func runListing(subtype, tool string, err error) string {
	entry := subtype
	if tool != "" {
		entry += " " + tool
	}
	if err != nil {
		entry += ": " + err.Error()
	}
	return entry
}

// answer sends decision as the reply to the request with requestID.
func (session *runSession) answer(requestID string, decision Decision) error {
	var err error
	if decision.Allow {
		err = session.conn.SendPermissionAllow(requestID, decision.UpdatedInput)
	} else {
		err = session.conn.SendPermissionDeny(requestID, decision.Message)
	}
	if err != nil {
		return fmt.Errorf("capture: send permission decision: %w", err)
	}
	return nil
}

// advance sends the next prompt. Once every prompt has been sent, it ends the prompts, and the probe ends
// after its last result.
func (session *runSession) advance() error {
	if session.sent == len(session.probe.Prompts) {
		return session.endPrompts()
	}
	prompt := session.probe.Prompts[session.sent]
	session.sent++
	if _, err := session.conn.SendUserMessage(prompt); err != nil {
		return fmt.Errorf("capture: send prompt: %w", err)
	}
	return nil
}

// endPrompts closes stdin at once when the probe has no linger. Otherwise it starts the linger, once, and
// play closes stdin when the linger ends. Results that arrive during the linger send nothing.
func (session *runSession) endPrompts() error {
	if session.probe.Linger == 0 {
		return session.input.Close()
	}
	if session.linger == nil {
		session.linger = time.After(session.probe.Linger)
	}
	return nil
}
