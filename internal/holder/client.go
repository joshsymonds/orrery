package holder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
)

// VersionError means a holder speaks a wire version this controller does not attach. A controller attaches the
// holder's version and the one before it, so a holder one version older or newer than that is refused.
type VersionError struct {
	// Session names the holder's session.
	Session string
	// Holder is the wire version the holder speaks.
	Holder int
	// Controller is the wire version this controller speaks.
	Controller int
}

// Error names the session, the holder's version and the two versions this controller attaches.
func (e *VersionError) Error() string {
	return fmt.Sprintf("holder for session %s speaks wire version %d; this controller attaches versions %d and %d",
		e.Session, e.Holder, e.Controller, e.Controller-1)
}

// Conn is a controller's connection to one holder.
type Conn struct {
	conn      net.Conn
	frames    *FrameReader
	writeMu   sync.Mutex
	closeOnce sync.Once
	// controllerVersion is the wire version this controller speaks.
	controllerVersion int
	// hello is the holder's hello. It carries the holder's session and wire version.
	hello Frame
}

// Dial connects to the holder on socket and exchanges hello at WireVersion. It accepts a holder of any version, so Stop
// works on every holder. Attach refuses a holder whose version this controller does not attach.
func Dial(ctx context.Context, socket string) (*Conn, error) {
	return dial(ctx, socket, WireVersion)
}

// dial connects to the holder on socket as a controller that speaks version. The holder's first answer must be hello.
func dial(ctx context.Context, socket string, version int) (*Conn, error) {
	var dialer net.Dialer
	raw, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("holder: dial %s: %w", socket, err)
	}
	conn := &Conn{conn: raw, frames: NewFrameReader(raw), controllerVersion: version}
	if err = conn.greet(); err != nil {
		return nil, conn.closeAfter(err)
	}
	return conn, nil
}

// Session returns the session the holder serves.
func (c *Conn) Session() string {
	return c.hello.Session
}

// Version returns the wire version the holder speaks.
func (c *Conn) Version() int {
	return c.hello.Version
}

// Stop sends stop and reads frames until the holder answers stopped, which it sends once the child has stopped and the
// exit file is written. Out, written and exit frames before stopped are skipped. The connection is closed whether or
// not the stop succeeds.
func (c *Conn) Stop() error {
	if err := c.send(Frame{Type: TypeStop}); err != nil {
		return c.closeAfter(err)
	}
	for {
		frame, err := c.frames.Read()
		if err != nil {
			return c.closeAfter(fmt.Errorf("holder: await stopped: %w", err))
		}
		switch frame.Type {
		case TypeStopped:
			return c.Close()
		case TypeOut, TypeWritten, TypeExit:
			// Output and the exit record come before stopped, and the stop does not need them.
		default:
			return c.closeAfter(frameError(frame, TypeStopped))
		}
	}
}

// Attach asks the holder to stream its stdout from replayFrom and returns the attached frame. A holder whose version is
// neither this controller's nor one less is refused with a *VersionError before anything is sent. The connection then
// closes, and the holder keeps running. An error frame from the holder becomes an error carrying its message, and the
// connection closes.
func (c *Conn) Attach(replayFrom int64) (Frame, error) {
	holderVersion := c.Version()
	if holderVersion != c.controllerVersion && holderVersion != c.controllerVersion-1 {
		return Frame{}, c.closeAfter(&VersionError{
			Session:    c.Session(),
			Holder:     holderVersion,
			Controller: c.controllerVersion,
		})
	}
	if err := c.send(Frame{Type: TypeAttach, ReplayFrom: replayFrom}); err != nil {
		return Frame{}, c.closeAfter(err)
	}
	answer, err := c.frames.Read()
	if err != nil {
		return Frame{}, c.closeAfter(fmt.Errorf("holder: await attached: %w", err))
	}
	if answer.Type != TypeAttached {
		return Frame{}, c.closeAfter(frameError(answer, TypeAttached))
	}
	return answer, nil
}

// Read returns the next frame the holder sends.
func (c *Conn) Read() (Frame, error) {
	return c.frames.Read()
}

// Write sends data to the child's stdin under id.
func (c *Conn) Write(id string, data []byte) error {
	return c.send(Frame{Type: TypeWrite, ID: id, Data: data})
}

// Ack tells the holder that the controller has consumed the stdout below consumed and needs no output below replayFrom,
// so the holder may trim its spool to replayFrom.
func (c *Conn) Ack(consumed, replayFrom int64) error {
	return c.send(Frame{Type: TypeAck, Consumed: consumed, ReplayFrom: replayFrom})
}

// Close closes the connection. Only the first call closes it, so later calls return nil.
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		if closeErr := c.conn.Close(); closeErr != nil {
			err = fmt.Errorf("holder: close connection: %w", closeErr)
		}
	})
	return err
}

// greet sends hello at the controller's version and reads the holder's answer, which must be hello.
func (c *Conn) greet() error {
	if err := c.send(Frame{Type: TypeHello, Version: c.controllerVersion}); err != nil {
		return err
	}
	answer, err := c.frames.Read()
	if err != nil {
		return fmt.Errorf("holder: await hello: %w", err)
	}
	if answer.Type != TypeHello {
		return frameError(answer, TypeHello)
	}
	c.hello = answer
	return nil
}

// send writes frame to the holder. Concurrent sends are serialized, so each frame is written whole.
func (c *Conn) send(frame Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.conn, frame)
}

// closeAfter closes the connection and returns err, joined with the close error when closing fails.
func (c *Conn) closeAfter(err error) error {
	if closeErr := c.Close(); closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return err
}

// frameError is the error for a frame that is not the one a call waits for. An error frame carries the holder's message,
// which the holder has already prefixed.
func frameError(frame Frame, want string) error {
	if frame.Type == TypeError {
		return errors.New(frame.Message)
	}
	return fmt.Errorf("holder: got a %q frame, want %q", frame.Type, want)
}
