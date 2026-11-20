package stepmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"runtime"
	"sync/atomic"
	"testing"
)

// numberedReply is one JSON-RPC response with the id of the request it answers.
type numberedReply struct {
	ID int `json:"id"`
	rpcReply
}

// sendToolCall sends a tools/call of name with no arguments under a request id the peer has not used, and returns
// that id.
func sendToolCall(p *peer, name string) int {
	p.nextRequest++
	p.send(map[string]any{
		"jsonrpc": "2.0", "id": p.nextRequest, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": map[string]any{}},
	})
	return p.nextRequest
}

// readNumberedReply reads the next response the server writes. It has no deadline of its own.
func readNumberedReply(t *testing.T, p *peer) numberedReply {
	t.Helper()
	var reply numberedReply
	if err := p.output.Decode(&reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return reply
}

// blockingAndProbeServer serves block, which holds its call until release is closed and reports each start on
// entered, and probe, which counts its starts and returns at once.
func blockingAndProbeServer(entered chan<- struct{}, release <-chan struct{}, probeStarts *atomic.Int32) *Server {
	return &Server{tools: []Tool{
		{
			Name:        "block",
			Description: "blocks until released",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(ctx context.Context, _ Call) (ToolResult, error) {
				entered <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
				}
				return ToolResult{Content: []Content{{Type: textContentType, Text: "released"}}}, nil
			},
		},
		{
			Name:        "probe",
			Description: "counts its starts",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(context.Context, Call) (ToolResult, error) {
				probeStarts.Add(1)
				return ToolResult{Content: []Content{{Type: textContentType, Text: "ran"}}}, nil
			},
		},
	}}
}

// TestNinthToolCallIsRefusedAtOnceAndNeverStarts checks that with eight tools/call requests in flight on one
// connection, a ninth is refused at once with JSON-RPC error -32000 and its handler never starts, and that a new
// call succeeds once the eight have finished.
func TestNinthToolCallIsRefusedAtOnceAndNeverStarts(t *testing.T) {
	t.Parallel()
	const ceiling = 8
	entered := make(chan struct{}, ceiling)
	release := make(chan struct{})
	var probeStarts atomic.Int32
	p, _ := servePipes(t, blockingAndProbeServer(entered, release, &probeStarts))
	initialize(p)

	blocking := make(map[int]bool, ceiling)
	for range ceiling {
		blocking[sendToolCall(p, "block")] = true
	}
	for range ceiling {
		<-entered
	}

	refusedID := sendToolCall(p, "probe")
	refused := readNumberedReply(t, p)
	if refused.ID != refusedID {
		t.Fatalf("reply id = %d, want %d", refused.ID, refusedID)
	}
	if refused.Error == nil || refused.Error.Code != -32000 ||
		refused.Error.Message != "Server already has 8 tool calls in flight" {
		t.Fatalf("ninth tools/call reply: error %+v, result %s; want -32000 with the ceiling message",
			refused.Error, refused.Result)
	}
	if n := probeStarts.Load(); n != 0 {
		t.Fatalf("the refused call's handler started %d times, want 0", n)
	}

	close(release)
	for range ceiling {
		reply := readNumberedReply(t, p)
		if reply.Error != nil || !blocking[reply.ID] {
			t.Fatalf("reply to call %d: error %+v; want a result for one of the eight blocked calls",
				reply.ID, reply.Error)
		}
		delete(blocking, reply.ID)
	}

	outcome := p.tool("probe", "{}")
	if outcome.isError || outcome.text != "ran" {
		t.Fatalf("call after the eight finished: text %q, isError %v; want ran, false", outcome.text, outcome.isError)
	}
	if n := probeStarts.Load(); n != 1 {
		t.Fatalf("probe handler started %d times, want 1 (the call after the eight)", n)
	}
}

// liveHeap returns the heap in use once garbage is collected.
func liveHeap() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// TestOversizedInboundLineEndsTheSessionWithBoundedMemory checks that one inbound line over 1 MiB ends the session
// with an error, that the server answers nothing for it, that the server reads no more than 1 MiB of it, and that the
// heap grows by far less than the 8 MiB line would take if it were held whole. It reads process-wide heap statistics, so
// it does not run in parallel.
func TestOversizedInboundLineEndsTheSessionWithBoundedMemory(t *testing.T) {
	const (
		lineBytes    = 8 << 20
		maxLineBytes = 1 << 20
		chunkBytes   = 64 << 10
		// maxGrowthBytes is well under the line held whole. Reading up to the 1 MiB frame limit, the SDK's JSON decoder
		// doubles its buffer to about 2 MiB, so the bound leaves room for that buffer and is still half the line.
		maxGrowthBytes = 4 * maxLineBytes
	)
	server, err := NewServer(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	var line bytes.Buffer
	line.WriteString(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"padding":"`)
	line.Write(bytes.Repeat([]byte("p"), lineBytes))
	line.WriteString("\"}}\n")

	serverInput, clientInput := io.Pipe()
	outputRead, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		_ = serverInput.Close()
		_ = clientInput.Close()
		_ = outputRead.Close()
		_ = serverOutput.Close()
	})
	firstAnswer := make(chan struct{})
	answered := make(chan []byte, 1)
	go func() {
		var answers bytes.Buffer
		chunk := make([]byte, 4096)
		for {
			n, err := outputRead.Read(chunk)
			if n > 0 {
				if answers.Len() == 0 {
					close(firstAnswer)
				}
				answers.Write(chunk[:n])
			}
			if err != nil {
				answered <- answers.Bytes()
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, serverInput, serverOutput) }()

	baseline := liveHeap()
	var peak uint64
	var consumed int
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		data := line.Bytes()
		for offset := 0; offset < len(data); offset += chunkBytes {
			end := min(offset+chunkBytes, len(data))
			n, err := clientInput.Write(data[offset:end])
			consumed += n
			if err != nil {
				return
			}
			peak = max(peak, liveHeap())
		}
	}()

	var serveErr error
	select {
	case serveErr = <-done:
	case <-firstAnswer:
		// The server answered the line, so end the input to let Serve return.
		_ = clientInput.Close()
		serveErr = <-done
	}
	_ = serverInput.Close()
	_ = serverOutput.Close()
	answers := <-answered
	<-writeDone

	if serveErr == nil {
		t.Error("Serve returned nil; want an error, because a line over 1 MiB ends the session")
	}
	if len(answers) != 0 {
		t.Errorf("server answered %d bytes after the oversized line: %.200s", len(answers), answers)
	}
	if consumed > maxLineBytes {
		t.Errorf("server read %d bytes of a %d-byte line, want at most %d", consumed, lineBytes, maxLineBytes)
	}
	if growth := int64(peak) - int64(baseline); growth > maxGrowthBytes {
		t.Errorf("heap grew by %d bytes while reading a %d-byte line, want at most %d",
			growth, lineBytes, maxGrowthBytes)
	}
}
