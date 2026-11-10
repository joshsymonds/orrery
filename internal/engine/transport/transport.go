package transport

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

const (
	defaultBufferSize     = 256
	uuidVersionMask       = byte(0x0f)
	uuidVersionFour       = byte(0x40)
	uuidVariantMask       = byte(0x3f)
	uuidVariantRFC4122    = byte(0x80)
	encodedUUIDByteCount  = 36
	userRole              = "user"
	controlSuccessSubtype = "success"
)

var (
	// ErrTransportClosed marks an intentionally closed transport.
	ErrTransportClosed = errors.New("transport: closed")
	errUnexpectedEOF   = errors.New("transport: unexpected EOF")
)

// TransportError reports the operation and line where transport processing failed.
type TransportError struct {
	Operation string
	Line      uint64
	Err       error
}

// Error implements error.
func (transportError *TransportError) Error() string {
	if transportError.Line == 0 {
		return fmt.Sprintf("transport: %s: %v", transportError.Operation, transportError.Err)
	}
	return fmt.Sprintf(
		"transport: %s line %d: %v",
		transportError.Operation,
		transportError.Line,
		transportError.Err,
	)
}

// Unwrap returns the underlying transport failure.
func (transportError *TransportError) Unwrap() error {
	return transportError.Err
}

// ControlType identifies a non-SDK control envelope.
type ControlType string

const (
	// ControlRequest identifies a control_request envelope.
	ControlRequest ControlType = "control_request"
	// ControlResponse identifies a control_response envelope, including interrupt receipts.
	ControlResponse ControlType = "control_response"
	// ControlCancelRequest identifies a control_cancel_request envelope.
	ControlCancelRequest ControlType = "control_cancel_request"
)

// ControlMessage preserves one inbound control envelope.
type ControlMessage struct {
	Type ControlType
	raw  json.RawMessage
}

// RawJSON returns a copy of the complete control envelope.
func (message ControlMessage) RawJSON() json.RawMessage {
	return append(json.RawMessage(nil), message.raw...)
}

// Option configures a Transport.
type Option func(*transportOptions)

// WithUUIDGenerator replaces user-message UUID generation.
// It is primarily useful when an owning supervisor already allocates message IDs.
func WithUUIDGenerator(generator func() (string, error)) Option {
	return func(options *transportOptions) {
		if generator != nil {
			options.uuidGenerator = generator
		}
	}
}

// WithBufferSize sets the bounded capacity of each inbound receive path.
// A full path pauses the reader of this transport until its consumer takes a value.
func WithBufferSize(size int) Option {
	return func(options *transportOptions) {
		if size > 0 {
			options.bufferSize = size
		}
	}
}

type transportOptions struct {
	bufferSize    int
	uuidGenerator func() (string, error)
}

// Transport exchanges newline-delimited protocol envelopes over supplied streams.
//
// The reader goroutine is the only sender on events and controls, so it is the only goroutine
// that closes them. Termination closes closing, which wakes a reader waiting on a full channel.
type Transport struct {
	stdin  io.Writer
	stdout io.Reader

	events   chan wire.SDKMessage
	controls chan ControlMessage
	done     chan error

	stateMu  sync.Mutex
	terminal error
	writeMu  sync.Mutex

	closing    chan struct{}
	readerDone chan struct{}
	finishOnce sync.Once
	closeOnce  sync.Once
	closeMu    sync.Mutex
	closeErr   error

	uuidGenerator func() (string, error)
}

// New starts the sole reader goroutine for the supplied Claude stream-json pipes.
// Close closes streams that implement io.Closer so blocked pipe I/O is interrupted.
func New(stdin io.Writer, stdout io.Reader, options ...Option) *Transport {
	configuration := transportOptions{
		bufferSize:    defaultBufferSize,
		uuidGenerator: randomUUID,
	}
	for _, apply := range options {
		if apply != nil {
			apply(&configuration)
		}
	}
	transport := &Transport{
		stdin:         stdin,
		stdout:        stdout,
		events:        make(chan wire.SDKMessage, configuration.bufferSize),
		controls:      make(chan ControlMessage, configuration.bufferSize),
		done:          make(chan error, 1),
		closing:       make(chan struct{}),
		readerDone:    make(chan struct{}),
		uuidGenerator: configuration.uuidGenerator,
	}
	go transport.readLoop()
	return transport
}

// Events returns the inbound SDKMessage stream. It closes at transport termination.
func (transport *Transport) Events() <-chan wire.SDKMessage {
	return transport.events
}

// Controls returns control requests and responses, including interrupt receipts.
// It closes at transport termination.
func (transport *Transport) Controls() <-chan ControlMessage {
	return transport.controls
}

// Done receives the terminal transport error exactly once, then closes.
func (transport *Transport) Done() <-chan error {
	return transport.done
}

// SendUserMessage writes one UUID-stamped user-message envelope and returns its UUID.
func (transport *Transport) SendUserMessage(text string) (string, error) {
	transport.writeMu.Lock()
	defer transport.writeMu.Unlock()

	messageUUID, err := transport.uuidGenerator()
	if err != nil {
		return "", fmt.Errorf("transport: generate user message UUID: %w", err)
	}
	if messageUUID == "" {
		return "", errors.New("transport: generated empty user message UUID")
	}
	envelope := userEnvelope{
		Type: userRole,
		Message: userMessage{
			Role:    userRole,
			Content: []textContent{{Type: "text", Text: text}},
		},
		UUID: messageUUID,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("transport: marshal user message: %w", err)
	}
	if writeErr := transport.writeLocked(append(encoded, '\n')); writeErr != nil {
		return "", writeErr
	}
	return messageUUID, nil
}

// SendInterrupt writes one caller-correlated interrupt control request.
func (transport *Transport) SendInterrupt(requestID string) error {
	transport.writeMu.Lock()
	defer transport.writeMu.Unlock()

	encodedID, err := json.Marshal(requestID)
	if err != nil {
		return fmt.Errorf("transport: marshal interrupt request ID: %w", err)
	}
	envelope := make([]byte, 0, len(encodedID)+80)
	envelope = append(envelope, `{"type":"control_request","request_id":`...)
	envelope = append(envelope, encodedID...)
	envelope = append(envelope, `,"request":{"subtype":"interrupt"}}`...)
	envelope = append(envelope, '\n')
	return transport.writeLocked(envelope)
}

// SendInitialize advertises the dialog kinds supported by this client.
func (transport *Transport) SendInitialize(
	requestID string,
	supportedDialogKinds []string,
) error {
	transport.writeMu.Lock()
	defer transport.writeMu.Unlock()

	request, err := json.Marshal(wire.SDKControlInitializeRequest{
		Subtype:              "initialize",
		SupportedDialogKinds: append([]string(nil), supportedDialogKinds...),
		Hooks:                map[string]any{},
		SystemPrompt:         []string{},
	})
	if err != nil {
		return fmt.Errorf("transport: marshal initialize request: %w", err)
	}
	encodedID, err := json.Marshal(requestID)
	if err != nil {
		return fmt.Errorf("transport: marshal initialize request ID: %w", err)
	}
	envelope := make([]byte, 0, len(encodedID)+len(request)+64)
	envelope = append(envelope, `{"type":"control_request","request_id":`...)
	envelope = append(envelope, encodedID...)
	envelope = append(envelope, `,"request":`...)
	envelope = append(envelope, request...)
	envelope = append(envelope, '}')
	return transport.writeLocked(append(envelope, '\n'))
}

// SendControlResponse writes a successful response correlated to a control request.
func (transport *Transport) SendControlResponse(
	requestID string,
	response json.RawMessage,
) error {
	transport.writeMu.Lock()
	defer transport.writeMu.Unlock()

	if !json.Valid(response) {
		return errors.New("transport: invalid control response JSON")
	}
	encodedID, err := json.Marshal(requestID)
	if err != nil {
		return fmt.Errorf("transport: marshal control response request ID: %w", err)
	}
	envelope := make([]byte, 0, len(encodedID)+len(response)+96)
	envelope = append(envelope, `{"type":"control_response","response":{"subtype":"`...)
	envelope = append(envelope, controlSuccessSubtype...)
	envelope = append(envelope, `","request_id":`...)
	envelope = append(envelope, encodedID...)
	envelope = append(envelope, `,"response":`...)
	envelope = append(envelope, response...)
	envelope = append(envelope, '}', '}')
	return transport.writeLocked(append(envelope, '\n'))
}

// Close terminates the transport, interrupts closable streams, and joins its reader.
// It returns even when the reader is waiting for a consumer to take a value.
func (transport *Transport) Close() error {
	transport.finish(ErrTransportClosed)
	<-transport.readerDone
	transport.closeMu.Lock()
	defer transport.closeMu.Unlock()
	return transport.closeErr
}

type userEnvelope struct {
	Type    string      `json:"type"`
	Message userMessage `json:"message"`
	UUID    string      `json:"uuid"`
}

type userMessage struct {
	Role    string        `json:"role"`
	Content []textContent `json:"content"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (transport *Transport) readLoop() {
	defer transport.shutdown()
	reader := bufio.NewReaderSize(transport.stdout, initialReadBufferSize)
	var lineNumber uint64
	for {
		line, err := readJSONLine(reader)
		if err != nil {
			lineNumber++
			if errors.Is(err, io.EOF) {
				err = errUnexpectedEOF
			}
			transport.finish(&TransportError{Operation: "read", Line: lineNumber, Err: err})
			return
		}
		lineNumber++
		if demultiplexErr := transport.demultiplex(line, lineNumber); demultiplexErr != nil {
			transport.finish(demultiplexErr)
			return
		}
	}
}

// shutdown runs on the reader goroutine after its last read. It closes both channels, publishes
// the terminal error on Done, and then releases Close.
func (transport *Transport) shutdown() {
	close(transport.events)
	close(transport.controls)
	transport.done <- transport.terminalError()
	close(transport.done)
	close(transport.readerDone)
}

func (transport *Transport) demultiplex(line []byte, lineNumber uint64) error {
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &discriminator); err != nil {
		return &TransportError{Operation: "decode", Line: lineNumber, Err: err}
	}

	switch ControlType(discriminator.Type) {
	case ControlRequest, ControlResponse, ControlCancelRequest:
		control := ControlMessage{
			Type: ControlType(discriminator.Type),
			raw:  append(json.RawMessage(nil), line...),
		}
		return transport.dispatchControl(control)
	default:
		return transport.dispatchSDKMessage(line, lineNumber)
	}
}

func (transport *Transport) dispatchSDKMessage(line []byte, lineNumber uint64) error {
	var message wire.SDKMessage
	if err := json.Unmarshal(line, &message); err != nil {
		return &TransportError{Operation: "decode SDK message", Line: lineNumber, Err: err}
	}
	select {
	case transport.events <- message:
		return nil
	case <-transport.closing:
		return transport.terminalError()
	}
}

func (transport *Transport) dispatchControl(message ControlMessage) error {
	select {
	case transport.controls <- message:
		return nil
	case <-transport.closing:
		return transport.terminalError()
	}
}

func (transport *Transport) terminalError() error {
	transport.stateMu.Lock()
	defer transport.stateMu.Unlock()
	return transport.terminal
}

func (transport *Transport) writeLocked(data []byte) error {
	if terminal := transport.terminalError(); terminal != nil {
		return terminal
	}
	if err := writeAll(transport.stdin, data); err != nil {
		transportError := &TransportError{Operation: "write NDJSON envelope", Err: err}
		transport.finish(transportError)
		return transportError
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return fmt.Errorf("write bytes: %w", err)
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

// finish records the first terminal cause, wakes a reader blocked on a channel send, and
// closes the streams. It never closes events or controls, which only the reader goroutine closes.
func (transport *Transport) finish(cause error) {
	transport.finishOnce.Do(func() {
		transport.stateMu.Lock()
		transport.terminal = cause
		transport.stateMu.Unlock()
		close(transport.closing)
	})
	transport.closeStreams()
}

func (transport *Transport) closeStreams() {
	transport.closeOnce.Do(func() {
		var closeErr error
		if closer, ok := transport.stdin.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				closeErr = errors.Join(closeErr, fmt.Errorf("close transport stdin: %w", err))
			}
		}
		if closer, ok := transport.stdout.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				closeErr = errors.Join(closeErr, fmt.Errorf("close transport stdout: %w", err))
			}
		}
		transport.closeMu.Lock()
		transport.closeErr = closeErr
		transport.closeMu.Unlock()
	})
}

func randomUUID() (string, error) {
	var identifier [16]byte
	if _, err := io.ReadFull(rand.Reader, identifier[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	identifier[6] = (identifier[6] & uuidVersionMask) | uuidVersionFour
	identifier[8] = (identifier[8] & uuidVariantMask) | uuidVariantRFC4122

	var encoded [encodedUUIDByteCount]byte
	hex.Encode(encoded[0:8], identifier[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], identifier[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], identifier[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], identifier[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], identifier[10:16])
	return string(encoded[:]), nil
}
