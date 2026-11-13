package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// interruptFormat is an interrupt control request, with one %s where its random request id goes.
const interruptFormat = `{"type":"control_request","request_id":"%s","request":{"subtype":"interrupt"}}`

// randomIDPattern matches a random request id: orrery- and 32 lowercase hex digits.
var randomIDPattern = regexp.MustCompile(`^orrery-[0-9a-f]{32}$`)

// randomIDInLine matches the request id a control request line carries.
var randomIDInLine = regexp.MustCompile(`"request_id":"(orrery-[0-9a-f]{32})"`)

// idWrite is one write to a process's stdin: the write id it carried and its bytes. A plain Write records an empty id.
type idWrite struct {
	id   string
	data string
}

// idStdin is a process's stdin that records each write with its id. WriteWithID records the id it is given, and a plain
// Write records an empty id, which no test expects. Once closed, it refuses every write as a closed pipe does.
type idStdin struct {
	mu     sync.Mutex
	closed bool
	writes []idWrite
}

func (s *idStdin) WriteWithID(id string, data []byte) (int, error) {
	return s.record(id, data)
}

func (s *idStdin) Write(data []byte) (int, error) {
	return s.record("", data)
}

func (s *idStdin) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// record stores a write under id and returns its length, or refuses it when the stdin is closed.
func (s *idStdin) record(id string, data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	s.writes = append(s.writes, idWrite{id: id, data: string(data)})
	return len(data), nil
}

// all returns a copy of the writes so far.
func (s *idStdin) all() []idWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.writes)
}

// idProcess is a fake process whose stdin records the write ids of its writes. Its stdout and exit come from fakeProcess.
type idProcess struct {
	*fakeProcess
	input *idStdin
}

func (p *idProcess) Stdin() io.WriteCloser { return p.input }

// startIDSession starts a session on an idProcess and stops it when the test ends.
func startIDSession(t *testing.T) (*Session, *idProcess) {
	t.Helper()
	process := &idProcess{fakeProcess: newFakeProcess(), input: &idStdin{}}
	session, err := New(process)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := stopWithin(t, session); stopErr != nil {
			t.Errorf("Stop in cleanup: %v", stopErr)
		}
	})
	return session, process
}

// requireIDWrites fails the test unless the process's stdin holds exactly the initialize write, then want, each with its
// write id and bytes.
func requireIDWrites(t *testing.T, process *idProcess, want ...idWrite) {
	t.Helper()
	wantAll := append([]idWrite{{id: "initialize", data: wantInitialize + "\n"}}, want...)
	if got := process.input.all(); !slices.Equal(got, wantAll) {
		t.Fatalf("stdin writes = %#v, want %#v", got, wantAll)
	}
}

// requireRandomRequest fails the test unless line is the control request of format, a format with one %s where its
// random request id goes. It returns that id.
func requireRandomRequest(t *testing.T, line, format string) string {
	t.Helper()
	match := randomIDInLine.FindStringSubmatch(line)
	if match == nil {
		t.Fatalf("line %q carries no random request id", line)
	}
	if want := fmt.Sprintf(format, match[1]); line != want {
		t.Fatalf("line = %q, want %q", line, want)
	}
	return match[1]
}

func TestInitializeWritesUnderInitializeIDWithItsRequestID(t *testing.T) {
	t.Parallel()
	_, process := startIDSession(t)
	requireIDWrites(t, process)
}

func TestPromptWritesUnderTheIDItIsGiven(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)
	if err := session.Prompt("write-1", "first prompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	writes := process.input.all()
	if len(writes) != 2 || writes[1].id != "write-1" {
		t.Fatalf("stdin writes = %#v, want the prompt under write id write-1", writes)
	}
	requireUserEnvelope(t, strings.TrimSuffix(writes[1].data, "\n"), "first prompt")
}

func TestInterruptWritesUnderARandomIDUsedAsItsRequestID(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)
	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	writes := process.input.all()
	if len(writes) != 2 {
		t.Fatalf("stdin writes = %#v, want the initialize write and the interrupt", writes)
	}
	id := requireRandomRequest(t, strings.TrimSuffix(writes[1].data, "\n"), interruptFormat)
	if writes[1].id != id {
		t.Fatalf("interrupt write id = %q, want its request id %q", writes[1].id, id)
	}
}

func TestSetPermissionModeWritesUnderARandomIDUsedAsItsRequestID(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)
	if err := session.SetPermissionMode("acceptEdits"); err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	writes := process.input.all()
	if len(writes) != 2 {
		t.Fatalf("stdin writes = %#v, want the initialize write and the mode change", writes)
	}
	id := requireRandomRequest(t, strings.TrimSuffix(writes[1].data, "\n"),
		`{"type":"control_request","request_id":"%s","request":{"subtype":"set_permission_mode","mode":"acceptEdits"}}`)
	if writes[1].id != id {
		t.Fatalf("mode change write id = %q, want its request id %q", writes[1].id, id)
	}
}

func TestInterruptAndModeChangeGetFreshRandomIDs(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)
	for range 2 {
		if err := session.Interrupt(); err != nil {
			t.Fatalf("Interrupt: %v", err)
		}
	}
	if err := session.SetPermissionMode("acceptEdits"); err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	writes := process.input.all()
	if len(writes) != 4 {
		t.Fatalf("stdin writes = %d, want 4", len(writes))
	}
	seen := make(map[string]bool)
	for _, write := range writes[1:] {
		if !randomIDPattern.MatchString(write.id) {
			t.Fatalf("write id = %q, want orrery- plus 32 lowercase hex digits", write.id)
		}
		if seen[write.id] {
			t.Fatalf("write id %q is used twice, want a fresh id per write", write.id)
		}
		seen[write.id] = true
	}
}

func TestHostAnswersWriteUnderTheResponseIDOfClaudesRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		line   []byte
		kind   RequestKind
		id     string
		answer func(session *Session) error
	}{
		{
			name: "allow", line: canUseTool("req_write_1", "Write", "toolu_write_1", writeInput),
			kind: RequestPermission, id: "req_write_1",
			answer: func(session *Session) error { return session.Allow("req_write_1", nil) },
		},
		{
			name: "deny", line: canUseTool("req_write_1", "Write", "toolu_write_1", writeInput),
			kind: RequestPermission, id: "req_write_1",
			answer: func(session *Session) error { return session.Deny("req_write_1", "Not today.") },
		},
		{
			name: "refuse", line: canUseTool("req_write_1", "Write", "toolu_write_1", writeInput),
			kind: RequestPermission, id: "req_write_1",
			answer: func(session *Session) error { return session.Refuse("req_write_1", "Refused by the host.") },
		},
		{
			name: "answer question", line: canUseTool("req_color_1", "AskUserQuestion", "toolu_color_1", colorInput),
			kind: RequestQuestion, id: "req_color_1",
			answer: func(session *Session) error { return session.AnswerQuestion("req_color_1", "Blue") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session, process := startIDSession(t)
			emit(t, process.fakeProcess, tc.line)
			requireRequest(t, session, tc.kind, tc.id)
			if err := tc.answer(session); err != nil {
				t.Fatalf("answer: %v", err)
			}
			writes := process.input.all()
			if got, want := writes[len(writes)-1].id, "response:"+tc.id; got != want {
				t.Fatalf("answer write id = %q, want %q", got, want)
			}
		})
	}
}

func TestLongRequestIDAnswersUnderItsHashedResponseID(t *testing.T) {
	t.Parallel()
	// "response:" is 9 bytes, so a 247-byte request id keeps its plain response id at exactly 256 bytes, and a 248-byte
	// one passes 256 bytes and is hashed.
	for _, size := range []int{247, 248} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			t.Parallel()
			requestID := "req_" + strings.Repeat("x", size-len("req_"))
			session, process := startIDSession(t)
			emit(t, process.fakeProcess, canUseTool(requestID, "Write", "toolu_long_1", writeInput))
			requireRequest(t, session, RequestPermission, requestID)
			if err := session.Deny(requestID, "Not today."); err != nil {
				t.Fatalf("Deny: %v", err)
			}
			want := "response:" + requestID
			if size > 247 {
				sum := sha256.Sum256([]byte(requestID))
				want = "response:sha256:" + hex.EncodeToString(sum[:])
			}
			writes := process.input.all()
			if got := writes[len(writes)-1].id; got != want {
				t.Fatalf("deny write id = %q, want %q", got, want)
			}
		})
	}
}

func TestRefusedRequestIsRefusedUnderItsEventID(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)
	emit(t, process.fakeProcess, canUseTool("req_write_1", "Write", "toolu_write_1", writeInput))
	requireRequest(t, session, RequestPermission, "req_write_1")
	emit(t, process.fakeProcess, []byte(elicitationLine))
	line := nextLine(t, session)
	if len(line.Events) != 1 {
		t.Fatalf("line events = %#v, want the one refused request", line.Events)
	}
	request, ok := line.Events[0].(Request)
	if !ok || !request.Refused {
		t.Fatalf("event = %#v, want a refused request", line.Events[0])
	}
	wantID := fmt.Sprintf("event:%d.0", line.Start)
	if request.WriteID != wantID {
		t.Fatalf("request write id = %q, want %q", request.WriteID, wantID)
	}
	requireIDWrites(t, process, idWrite{id: wantID, data: elicitationRefusal + "\n"})
}

// A stdout line carries one request at most, so an event index above 0 is reached by observing a line of events
// directly: the first event is a block and the request follows it at index 1.

func TestRefusedRequestAtIndexAboveZeroIsRefusedUnderItsEventID(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)
	events := []Event{
		Block{Kind: BlockText, Role: "assistant", MessageID: "msg_1", Text: "before the request"},
		Request{Kind: RequestOther, RequestID: "req_elicit_1", Subtype: "elicitation", Refused: true},
	}
	session.observeLine(42, events)
	request, ok := events[1].(Request)
	if !ok || !request.Refused {
		t.Fatalf("event = %#v, want a refused request", events[1])
	}
	const wantID = "event:42.1"
	if request.WriteID != wantID {
		t.Fatalf("request write id = %q, want %q", request.WriteID, wantID)
	}
	requireIDWrites(t, process, idWrite{id: wantID, data: elicitationRefusal + "\n"})
}

func TestDenyEventAtIndexAboveZeroDeniesUnderItsEventID(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)
	events := []Event{
		Block{Kind: BlockText, Role: "assistant", MessageID: "msg_1", Text: "before the request"},
		Request{
			Kind: RequestPermission, RequestID: "req_write_1", ToolUseID: "toolu_write_1", ToolName: "Write",
			Subtype: "can_use_tool", Input: json.RawMessage(writeInput),
		},
	}
	session.observeLine(42, events)
	request, ok := events[1].(Request)
	if !ok || request.Refused {
		t.Fatalf("event = %#v, want an open request", events[1])
	}
	const wantID = "event:42.1"
	if request.WriteID != wantID {
		t.Fatalf("request write id = %q, want %q", request.WriteID, wantID)
	}
	if err := session.DenyEvent(request, "Not today."); err != nil {
		t.Fatalf("DenyEvent: %v", err)
	}
	requireIDWrites(t, process, idWrite{id: wantID, data: writeDeny + "\n"})
}

func TestDenyEventDeniesUnderTheRequestEventIDOnce(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)
	emit(t, process.fakeProcess, canUseTool("req_write_1", "Write", "toolu_write_1", writeInput))
	line := nextLine(t, session)
	if len(line.Events) != 1 {
		t.Fatalf("line events = %#v, want the one request", line.Events)
	}
	request, ok := line.Events[0].(Request)
	if !ok {
		t.Fatalf("event = %#v, want a request", line.Events[0])
	}
	wantID := fmt.Sprintf("event:%d.0", line.Start)
	if err := session.DenyEvent(request, "Not today."); err != nil {
		t.Fatalf("DenyEvent: %v", err)
	}
	requireIDWrites(t, process, idWrite{id: wantID, data: writeDeny + "\n"})
	if err := session.DenyEvent(request, "Not today."); !errors.Is(err, ErrNoPendingRequest) {
		t.Fatalf("second DenyEvent error = %v, want %v", err, ErrNoPendingRequest)
	}
	requireIDWrites(t, process, idWrite{id: wantID, data: writeDeny + "\n"})
}
