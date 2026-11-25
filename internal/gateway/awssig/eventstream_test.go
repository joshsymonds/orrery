package awssig_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway/awssig"
)

// stringHeaderType is the event-stream header value type for a UTF-8 string, which is the only type Bedrock's own
// headers use.
const stringHeaderType = 7

// frame encodes one event-stream message the way Bedrock does, so the decoder is tested against the framing rules
// rather than against itself.
func frame(headers map[string]string, payload []byte) []byte {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}

	sort.Strings(names)

	var block bytes.Buffer

	for _, name := range names {
		block.WriteByte(byte(len(name)))
		block.WriteString(name)
		block.WriteByte(stringHeaderType)
		binary.Write(&block, binary.BigEndian, uint16(len(headers[name])))
		block.WriteString(headers[name])
	}

	total := uint32(block.Len() + len(payload) + 16)

	var prelude bytes.Buffer

	binary.Write(&prelude, binary.BigEndian, total)
	binary.Write(&prelude, binary.BigEndian, uint32(block.Len()))

	var message bytes.Buffer

	message.Write(prelude.Bytes())
	binary.Write(&message, binary.BigEndian, crc32.ChecksumIEEE(prelude.Bytes()))
	message.Write(block.Bytes())
	message.Write(payload)
	binary.Write(&message, binary.BigEndian, crc32.ChecksumIEEE(message.Bytes()))

	return message.Bytes()
}

// eventFrame is a Bedrock content event: the standard header set and a payload.
func eventFrame(eventType string, payload string) []byte {
	return frame(map[string]string{
		":event-type":   eventType,
		":message-type": "event",
		":content-type": "application/json",
	}, []byte(payload))
}

// decodeAll reads every message from data, returning them and the error that ended the stream.
func decodeAll(t *testing.T, reader io.Reader) ([]awssig.Message, error) {
	t.Helper()

	dec := awssig.NewDecoder(reader)

	var messages []awssig.Message

	for {
		msg, err := dec.Next()
		if err != nil {
			return messages, err
		}

		messages = append(messages, msg)
	}
}

// TestGatewayRoutesSigV4SigningEventstreamReadsEveryMessage decodes a whole Bedrock stream: every message's headers and
// payload come back in order, and the clean end of the stream is io.EOF.
func TestGatewayRoutesSigV4SigningEventstreamReadsEveryMessage(t *testing.T) {
	t.Parallel()

	stream := bytes.NewReader(bytes.Join([][]byte{
		eventFrame("message_start", `{"bytes":"aGk="}`),
		eventFrame("content_block_delta", `{"bytes":"ZGVsdGE="}`),
		eventFrame("message_stop", `{"bytes":"c3RvcA=="}`),
	}, nil))

	messages, err := decodeAll(t, stream)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() ended with %v; want io.EOF", err)
	}

	if len(messages) != 3 {
		t.Fatalf("decoded %d messages; want 3", len(messages))
	}

	wantTypes := []string{"message_start", "content_block_delta", "message_stop"}
	for i, want := range wantTypes {
		if got := messages[i].Headers[":event-type"]; got != want {
			t.Errorf("message %d :event-type = %q; want %q", i, got, want)
		}

		if got := messages[i].Headers[":message-type"]; got != "event" {
			t.Errorf("message %d :message-type = %q; want %q", i, got, "event")
		}
	}

	if got := string(messages[0].Payload); got != `{"bytes":"aGk="}` {
		t.Errorf("first payload = %q; want the encoded event", got)
	}
}

// TestGatewayRoutesSigV4SigningEventstreamReassemblesSplitReads checks that a frame arriving in pieces, the normal case
// over a real connection, is the same frame. One byte at a time is the pathological version of it.
func TestGatewayRoutesSigV4SigningEventstreamReassemblesSplitReads(t *testing.T) {
	t.Parallel()

	stream := bytes.Join([][]byte{
		eventFrame("message_start", `{"bytes":"aGk="}`),
		eventFrame("message_stop", `{"bytes":"c3RvcA=="}`),
	}, nil)

	messages, err := decodeAll(t, chunked(stream))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() ended with %v; want io.EOF", err)
	}

	if len(messages) != 2 {
		t.Fatalf("decoded %d messages; want 2", len(messages))
	}

	if got := messages[1].Headers[":event-type"]; got != "message_stop" {
		t.Errorf("second :event-type = %q; want %q", got, "message_stop")
	}
}

// chunked returns a reader that hands out one byte per Read, so every frame boundary falls mid-field at least once.
func chunked(data []byte) io.Reader {
	return &oneByteReader{data: data, at: 0}
}

// oneByteReader is the smallest possible chunking of a stream.
type oneByteReader struct {
	data []byte
	at   int
}

// Read copies at most one byte per call.
func (r *oneByteReader) Read(out []byte) (int, error) {
	if r.at >= len(r.data) {
		return 0, io.EOF
	}

	if len(out) == 0 {
		return 0, nil
	}

	out[0] = r.data[r.at]
	r.at++

	return 1, nil
}

// TestGatewayRoutesSigV4SigningEventstreamRefusesBadCRC corrupts one byte of each checksum of a sound frame: the one
// over the prelude and the one over the whole message. Each must be refused with a checksum error, never decoded.
func TestGatewayRoutesSigV4SigningEventstreamRefusesBadCRC(t *testing.T) {
	t.Parallel()

	sound := eventFrame("message_start", `{"bytes":"aGk="}`)

	corrupt := func(at int) []byte {
		damaged := bytes.Clone(sound)
		damaged[at] ^= 0xFF

		return damaged
	}

	// The prelude CRC occupies bytes 8 to 11; the message CRC is the last four bytes. Each refusal must name the
	// checksum it failed on: a corrupted prelude CRC also breaks the message CRC, which covers the prelude.
	tests := []struct {
		name   string
		stream []byte
		want   string
	}{
		{name: "prelude CRC", stream: corrupt(8), want: "prelude"},
		{name: "message CRC", stream: corrupt(len(sound) - 1), want: "message"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			messages, err := decodeAll(t, bytes.NewReader(tc.stream))
			if err == nil || errors.Is(err, io.EOF) {
				t.Fatalf("Next() = %v after %d messages; want a checksum refusal", err, len(messages))
			}

			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "checksum") {
				t.Errorf("error = %q; want it to name the %s checksum", err.Error(), tc.want)
			}

			if len(messages) != 0 {
				t.Errorf("decoded %d messages from a frame with a bad CRC; want none", len(messages))
			}
		})
	}
}

// TestGatewayRoutesSigV4SigningEventstreamRefusesTruncation checks that a stream ending mid-frame is an error, not a
// clean end: the last message was never whole.
func TestGatewayRoutesSigV4SigningEventstreamRefusesTruncation(t *testing.T) {
	t.Parallel()

	sound := eventFrame("message_start", `{"bytes":"aGk="}`)

	tests := []struct {
		name   string
		stream []byte
	}{
		{name: "truncated mid-frame", stream: sound[:len(sound)-3]},
		{name: "truncated prelude", stream: sound[:5]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			messages, err := decodeAll(t, bytes.NewReader(tc.stream))
			if err == nil || errors.Is(err, io.EOF) {
				t.Fatalf("Next() = %v after %d messages; want a truncation error", err, len(messages))
			}
		})
	}
}

// TestGatewayRoutesSigV4SigningEventstreamRefusesAnOversizeMessage checks that a message claiming an absurd length is
// refused outright rather than allocated: an upstream cannot be allowed to name the gateway's memory use.
func TestGatewayRoutesSigV4SigningEventstreamRefusesAnOversizeMessage(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer

	var prelude bytes.Buffer

	binary.Write(&prelude, binary.BigEndian, uint32(1<<30))
	binary.Write(&prelude, binary.BigEndian, uint32(0))
	stream.Write(prelude.Bytes())
	binary.Write(&stream, binary.BigEndian, crc32.ChecksumIEEE(prelude.Bytes()))

	if _, err := awssig.NewDecoder(bytes.NewReader(stream.Bytes())).Next(); err == nil {
		t.Fatal("Next() = nil; want an error for an oversize message")
	}
}

// TestGatewayRoutesSigV4SigningEventstreamReadsAnExceptionMessage checks that Bedrock's mid-stream failure, which
// carries :exception-type instead of :event-type, decodes with its headers and body intact.
func TestGatewayRoutesSigV4SigningEventstreamReadsAnExceptionMessage(t *testing.T) {
	t.Parallel()

	stream := frame(map[string]string{
		":exception-type": "modelStreamErrorException",
		":message-type":   "exception",
		":content-type":   "application/json",
	}, []byte(`{"message":"upstream said no"}`))

	msg, err := awssig.NewDecoder(bytes.NewReader(stream)).Next()
	if err != nil {
		t.Fatalf("Next() = %v; want the exception message", err)
	}

	if got := msg.Headers[":exception-type"]; got != "modelStreamErrorException" {
		t.Errorf(":exception-type = %q; want %q", got, "modelStreamErrorException")
	}

	if got := string(msg.Payload); got != `{"message":"upstream said no"}` {
		t.Errorf("payload = %q; want the exception body", got)
	}
}
