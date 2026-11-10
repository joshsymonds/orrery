package transport

import (
	"encoding/json"
	"errors"
	"testing"
)

const (
	setPermissionModeLine = `{"type":"control_request","request_id":"00000000-0000-4000-8000-000000000001",` +
		`"request":{"subtype":"set_permission_mode","mode":"plan"}}` + "\n"
	permissionAllowLine = `{"type":"control_response","response":{"subtype":"success",` +
		`"request_id":"00000000-0000-4000-8000-000000000002","response":` +
		`{"behavior":"allow","updatedInput":{"path":"/home/user/notes.txt"}}}}` + "\n"
	permissionDenyLine = `{"type":"control_response","response":{"subtype":"success",` +
		`"request_id":"00000000-0000-4000-8000-000000000002","response":` +
		`{"behavior":"deny","message":"not allowed"}}}` + "\n"
)

func TestPermissionSendsWriteExactEnvelopes(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake)
	lines := stdinLines(fake, 3)

	if err := transport.SendSetPermissionMode(fixedUUIDOne, "plan"); err != nil {
		t.Fatalf("SendSetPermissionMode() error = %v", err)
	}
	if err := transport.SendPermissionAllow(
		fixedUUIDTwo,
		json.RawMessage(`{"path":"/home/user/notes.txt"}`),
	); err != nil {
		t.Fatalf("SendPermissionAllow() error = %v", err)
	}
	if err := transport.SendPermissionDeny(fixedUUIDTwo, "not allowed"); err != nil {
		t.Fatalf("SendPermissionDeny() error = %v", err)
	}

	want := []string{setPermissionModeLine, permissionAllowLine, permissionDenyLine}
	for i, expected := range want {
		if got := string(receive(t, lines)); got != expected {
			t.Fatalf("envelope %d = %q, want %q", i, got, expected)
		}
	}
}

func TestPermissionAllowRejectsInvalidInputWithoutWriting(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake)
	lines := stdinLines(fake, 1)

	if err := transport.SendPermissionAllow(
		fixedUUIDTwo,
		json.RawMessage(`{"path":`),
	); err == nil {
		t.Fatal("SendPermissionAllow() with invalid updatedInput error = nil, want error")
	}
	if err := transport.SendSetPermissionMode(fixedUUIDOne, "plan"); err != nil {
		t.Fatalf("SendSetPermissionMode() error = %v", err)
	}
	if got := string(receive(t, lines)); got != setPermissionModeLine {
		t.Fatalf("first envelope after rejected allow = %q, want the mode request only", got)
	}
}

func TestPermissionSendsAfterCloseReturnTerminalError(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake)
	_ = transport.Close()

	sends := []struct {
		name string
		send func() error
	}{
		{name: "SendSetPermissionMode", send: func() error {
			return transport.SendSetPermissionMode(fixedUUIDOne, "plan")
		}},
		{name: "SendPermissionAllow", send: func() error {
			return transport.SendPermissionAllow(fixedUUIDTwo, json.RawMessage(`{}`))
		}},
		{name: "SendPermissionDeny", send: func() error {
			return transport.SendPermissionDeny(fixedUUIDTwo, "not allowed")
		}},
	}
	for _, testCase := range sends {
		if err := testCase.send(); !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("%s() after Close error = %v, want ErrTransportClosed", testCase.name, err)
		}
	}
}
