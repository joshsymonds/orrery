// Package holder runs one child process for any controller: it spools the child's stdout to disk, keeps a stderr
// tail, holds the session's lock and serves a versioned socket.
package holder

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// WireVersion is the protocol version this holder speaks. It is raised only by an incompatible change.
// hello, stop and stopped keep their shape in every version.
const WireVersion = 1

const (
	// MaxFrameBytes bounds one frame, LF included.
	MaxFrameBytes = 32 << 20

	// ChunkBytes bounds the stdout bytes in one out frame.
	ChunkBytes = 64 << 10
)

// Frame types. A controller sends hello, attach, ack, write and stop. A holder sends hello, attached, out,
// written, stopped, exit and error.
const (
	TypeHello    = "hello"
	TypeStop     = "stop"
	TypeStopped  = "stopped"
	TypeAttach   = "attach"
	TypeAttached = "attached"
	TypeOut      = "out"
	TypeAck      = "ack"
	TypeWrite    = "write"
	TypeWritten  = "written"
	TypeExit     = "exit"
	TypeError    = "error"
)

// Reasons an Exit record gives for the child's end.
const (
	// ReasonExited means the child ended on its own.
	ReasonExited = "exited"
	// ReasonStopped means a controller's stop, or a signal to the holder, stopped the child.
	ReasonStopped = "stopped"
	// ReasonIdle means no controller was attached for the idle timeout, so the holder stopped the child.
	ReasonIdle = "idle"
	// ReasonLimit means a gate child ran past its gate limit, so the holder stopped it.
	ReasonLimit = "limit"
)

var (
	// ErrFrameTooLong marks a frame larger than MaxFrameBytes, LF included.
	ErrFrameTooLong = errors.New("holder: frame exceeds limit")

	// ErrBadFrame marks input that is not a frame: invalid JSON, a missing or empty type, or a final line without LF.
	ErrBadFrame = errors.New("holder: bad frame")
)

// Frame is one protocol message. Type is always present; every other field is omitted when empty. Each field's
// comment names the frame types that carry it.
type Frame struct {
	// Type is one of the Type constants. Every frame carries it.
	Type string `json:"type"`
	// Version is the WireVersion the sender speaks. hello carries it, both ways.
	Version int `json:"version,omitempty"`
	// Session names the holder's session. The holder's hello carries it.
	Session string `json:"session,omitempty"`
	// ReplayFrom is a stdout offset. attach carries the offset its out frames start at; ack carries the offset
	// below which the controller needs no output again, so the holder may delete segments wholly below it; attached
	// carries the spool's replay-from offset, the floor a re-attaching controller acks at or above.
	ReplayFrom int64 `json:"replayFrom,omitempty"`
	// Consumed is the stdout offset the controller has consumed. ack carries it, and attached carries the spool's
	// consumed offset. The holder reads the child's stdout no further than the cap above it.
	Consumed int64 `json:"consumed,omitempty"`
	// Start is the first stdout offset the spool still holds. attached carries it.
	Start int64 `json:"start,omitempty"`
	// End is the stdout offset just past the last byte the spool holds. attached carries it.
	End int64 `json:"end,omitempty"`
	// Written lists the IDs of writes already applied to the child's stdin, in the order they were written.
	// attached carries it, so the controller does not resend them.
	Written []string `json:"written,omitempty"`
	// Offset is the stdout offset of Data's first byte. out carries it.
	Offset int64 `json:"offset,omitempty"`
	// Data is the payload, base64 on the wire. out carries stdout bytes and write carries stdin bytes.
	Data []byte `json:"data,omitempty"`
	// ID names a stdin write. write carries the controller's ID; written and error echo it.
	ID string `json:"id,omitempty"`
	// Duplicate is true when written answers a write whose ID was already applied, so nothing was written again.
	Duplicate bool `json:"duplicate,omitempty"`
	// Exit is the exit record. exit carries it.
	Exit *Exit `json:"exit,omitempty"`
	// Message is the reason for an error frame. error carries it.
	Message string `json:"message,omitempty"`
}

// Exit records how the child ended. The holder writes it to the exit file and sends it in the exit frame.
type Exit struct {
	// Reason is one of the Reason constants.
	Reason string `json:"reason"`
	// Code is the child's exit status, or -1 when a signal ended it.
	Code int `json:"code"`
	// Signal names the signal that ended the child, and is empty otherwise.
	Signal string `json:"signal,omitempty"`
	// StdoutEnd is the stdout offset just past the last byte the spool holds.
	StdoutEnd int64 `json:"stdoutEnd"`
	// Stderr is the tail of the child's stderr.
	Stderr string `json:"stderr"`
}

// WriteFrame writes frame as one JSON line with a single Write call. It refuses a frame with no Type
// (ErrBadFrame) and a frame that, LF included, exceeds MaxFrameBytes (ErrFrameTooLong).
func WriteFrame(writer io.Writer, frame Frame) error {
	if frame.Type == "" {
		return fmt.Errorf("%w: empty type", ErrBadFrame)
	}
	line, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("holder: encode frame: %w", err)
	}
	if len(line)+1 > MaxFrameBytes {
		return ErrFrameTooLong
	}
	line = append(line, '\n')
	if _, err = writer.Write(line); err != nil {
		return fmt.Errorf("holder: write frame: %w", err)
	}
	return nil
}

// FrameReader reads frames from a stream.
type FrameReader struct {
	reader *bufio.Reader
}

// NewFrameReader returns a FrameReader over r.
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{reader: bufio.NewReader(r)}
}

// Read returns the next frame. It returns io.EOF at a clean end of stream, ErrFrameTooLong for a frame past
// MaxFrameBytes, and ErrBadFrame, wrapping the cause, for input that is not a frame.
func (r *FrameReader) Read() (Frame, error) {
	line, err := r.readLine()
	if err != nil {
		return Frame{}, err
	}
	var frame Frame
	if err = json.Unmarshal(line, &frame); err != nil {
		return Frame{}, fmt.Errorf("%w: %w", ErrBadFrame, err)
	}
	if frame.Type == "" {
		return Frame{}, fmt.Errorf("%w: empty type", ErrBadFrame)
	}
	return frame, nil
}

// readLine returns the next line without its LF. It buffers no more than MaxFrameBytes, so an endless line
// fails with ErrFrameTooLong rather than growing without bound.
func (r *FrameReader) readLine() ([]byte, error) {
	var line []byte
	for {
		fragment, err := r.reader.ReadSlice('\n')
		if len(line)+len(fragment) > MaxFrameBytes {
			return nil, ErrFrameTooLong
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line[:len(line)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case !errors.Is(err, io.EOF):
			return nil, fmt.Errorf("holder: read frame: %w", err)
		case len(line) == 0:
			return nil, io.EOF
		default:
			return nil, fmt.Errorf("%w: final line has no LF", ErrBadFrame)
		}
	}
}
