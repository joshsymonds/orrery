package paneregistry

import (
	"strings"
	"testing"
)

// TestRegistryEntryParsesCLIEntry checks that Parse reads every registry key of an invented cli entry, and that the
// pane of its tmux field is the part after the last '.'.
func TestRegistryEntryParsesCLIEntry(t *testing.T) {
	data := []byte(`{"pid":4242,"sessionId":"sess-invented-1","cwd":"/home/tester/work/app",` +
		`"entrypoint":"cli","status":"waiting","waitingFor":"input needed","tmux":"testsrv:@7.%9"}`)
	entry, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if entry.PID != 4242 {
		t.Errorf("PID = %d, want 4242", entry.PID)
	}
	if entry.SessionID != "sess-invented-1" {
		t.Errorf("SessionID = %q, want sess-invented-1", entry.SessionID)
	}
	if entry.Cwd != "/home/tester/work/app" {
		t.Errorf("Cwd = %q, want /home/tester/work/app", entry.Cwd)
	}
	if entry.Entrypoint != EntrypointCLI {
		t.Errorf("Entrypoint = %q, want %q", entry.Entrypoint, EntrypointCLI)
	}
	if entry.Status != StatusWaiting {
		t.Errorf("Status = %q, want %q", entry.Status, StatusWaiting)
	}
	if entry.WaitingFor != WaitingInput {
		t.Errorf("WaitingFor = %q, want %q", entry.WaitingFor, WaitingInput)
	}
	if entry.Tmux != "testsrv:@7.%9" {
		t.Errorf("Tmux = %q, want testsrv:@7.%%9", entry.Tmux)
	}
	if pane := entry.Pane(nil); pane != "%9" {
		t.Errorf("Pane = %q, want %%9", pane)
	}
}

// TestRegistryEntryWaitingForNullIsEmpty checks that a null waitingFor, as an idle session writes, parses as "".
func TestRegistryEntryWaitingForNullIsEmpty(t *testing.T) {
	data := []byte(`{"pid":7,"sessionId":"sess-invented-2","entrypoint":"sdk-cli","status":"idle","waitingFor":null}`)
	entry, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if entry.WaitingFor != "" {
		t.Errorf("WaitingFor = %q, want empty", entry.WaitingFor)
	}
	if entry.Entrypoint != "sdk-cli" {
		t.Errorf("Entrypoint = %q, want sdk-cli", entry.Entrypoint)
	}
}

// TestRegistryEntryRefusesMalformedEntries checks that Parse refuses data that is not JSON, that lacks sessionId, or
// whose status is not busy, waiting, idle or shell, and that the error names each cause.
func TestRegistryEntryRefusesMalformedEntries(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"not JSON", `{"pid":`, "not a JSON object"},
		{"JSON but not an object", `"just a string"`, "not a JSON object"},
		{"lacks sessionId", `{"pid":1,"status":"idle"}`, "lacks sessionId"},
		{"status outside the set", `{"sessionId":"sess-invented-3","status":"running"}`,
			`status "running" is not busy, waiting, idle or shell`},
		{"status in another case", `{"sessionId":"sess-invented-3","status":"Busy"}`,
			`status "Busy" is not busy, waiting, idle or shell`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			if err == nil {
				t.Fatalf("Parse(%s) = nil error, want an error naming %q", tc.data, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse(%s) error = %q, want it to name %q", tc.data, err, tc.want)
			}
		})
	}
}

// TestRegistryEntryParsesStartingEntry checks that Parse accepts the entry Claude Code writes for a session before its
// first status, which has no status key, and that the entry reads as starting while an entry with a status does not.
func TestRegistryEntryParsesStartingEntry(t *testing.T) {
	data := []byte(`{"pid":4242,"sessionId":"sess-invented-5","cwd":"/home/tester/work/app","kind":"interactive",` +
		`"entrypoint":"sdk-cli","name":"app-a1"}`)
	entry, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse of an entry with no status: %v", err)
	}
	if entry.Status != "" || !entry.Starting() {
		t.Errorf("Status = %q, Starting() = %t, want an empty status that reads as starting", entry.Status,
			entry.Starting())
	}
	if entry.SessionID != "sess-invented-5" || entry.PID != 4242 {
		t.Errorf("entry = %+v, want pid 4242 in session sess-invented-5", entry)
	}
	idle, err := Parse([]byte(`{"sessionId":"sess-invented-5","status":"idle"}`))
	if err != nil {
		t.Fatalf("Parse of an idle entry: %v", err)
	}
	if idle.Starting() {
		t.Error("an idle entry reads as starting, want it not to")
	}
}

// TestRegistryEntryAcceptsEachStatus checks that Parse accepts each of the four statuses.
func TestRegistryEntryAcceptsEachStatus(t *testing.T) {
	for _, status := range []string{StatusBusy, StatusWaiting, StatusIdle, StatusShell} {
		data := []byte(`{"sessionId":"sess-invented-4","status":"` + status + `"}`)
		entry, err := Parse(data)
		if err != nil {
			t.Fatalf("Parse with status %q: %v", status, err)
		}
		if entry.Status != status {
			t.Errorf("Status = %q, want %q", entry.Status, status)
		}
	}
}

// TestRegistryEntryPaneFallsBackToTMUXPANE checks that Pane takes the pane from the tmux field when it names one, and
// otherwise from env's TMUX_PANE, and gives "" when neither names a pane.
func TestRegistryEntryPaneFallsBackToTMUXPANE(t *testing.T) {
	env := map[string]string{"TMUX_PANE": "%3"}
	cases := []struct {
		name  string
		entry Entry
		env   map[string]string
		want  string
	}{
		{"tmux field names the pane", Entry{Tmux: "testsrv:@7.%9"}, env, "%9"},
		{"no tmux field takes TMUX_PANE", Entry{}, env, "%3"},
		{"no tmux field and no TMUX_PANE", Entry{}, map[string]string{}, ""},
		{"tmux field with no dot names no pane", Entry{Tmux: "testsrv:@7"}, env, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.Pane(tc.env); got != tc.want {
				t.Fatalf("Pane() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRegistryEntryTranscriptPath checks that TranscriptPath maps a cwd to its project directory under
// .claude/projects, replacing each rune that is not an ASCII letter or digit with '-', and that it returns "" when home
// or id is empty or id holds a path separator.
func TestRegistryEntryTranscriptPath(t *testing.T) {
	cases := []struct {
		name string
		home string
		cwd  string
		id   string
		want string
	}{
		{"plain cwd", "/home/tester", "/home/tester/work/app", "sess-1",
			"/home/tester/.claude/projects/-home-tester-work-app/sess-1.jsonl"},
		{"dots and underscores", "/home/tester", "/srv/my_app.v2", "sess-1",
			"/home/tester/.claude/projects/-srv-my-app-v2/sess-1.jsonl"},
		{"each non-ASCII rune is one dash", "/home/tester", "/tmp/é x", "sess-1",
			"/home/tester/.claude/projects/-tmp---x/sess-1.jsonl"},
		{"empty home", "", "/home/tester/work/app", "sess-1", ""},
		{"empty id", "/home/tester", "/home/tester/work/app", "", ""},
		{"id with a separator", "/home/tester", "/home/tester/work/app", "nested/sess-1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TranscriptPath(tc.home, tc.cwd, tc.id); got != tc.want {
				t.Fatalf("TranscriptPath(%q, %q, %q) = %q, want %q", tc.home, tc.cwd, tc.id, got, tc.want)
			}
		})
	}
}

// TestRegistryEntryParsesEnvironment checks that parseEnviron reads NUL-separated KEY=VALUE pairs of an invented
// process environment, splits each pair at its first '=', and skips a piece that has no '='.
func TestRegistryEntryParsesEnvironment(t *testing.T) {
	data := []byte("TMUX=/tmp/tmux-1000/default,812,0\x00ANTHROPIC_BASE_URL=http://127.0.0.1:9/v1?x=y\x00" +
		"TMUX_PANE=%9\x00junk\x00")
	env := parseEnviron(data)
	if len(env) != 3 {
		t.Fatalf("parseEnviron has %d entries, want 3: %v", len(env), env)
	}
	if env["TMUX"] != "/tmp/tmux-1000/default,812,0" {
		t.Errorf("TMUX = %q", env["TMUX"])
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:9/v1?x=y" {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want the value with its '=' kept", env["ANTHROPIC_BASE_URL"])
	}
	if env["TMUX_PANE"] != "%9" {
		t.Errorf("TMUX_PANE = %q, want %%9", env["TMUX_PANE"])
	}
}

// TestRegistryEntryProcRefusesNonPositivePID checks that SystemProc reports no live process for a pid that is not
// positive, since signaling 0 or a negative pid would address a process group.
func TestRegistryEntryProcRefusesNonPositivePID(t *testing.T) {
	proc := SystemProc()
	for _, pid := range []int{0, -1} {
		if proc.Alive(pid) {
			t.Errorf("Alive(%d) = true, want false", pid)
		}
	}
}
