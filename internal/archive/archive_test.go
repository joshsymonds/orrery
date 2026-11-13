package archive_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/archive"
)

func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestPath(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "XDG_STATE_HOME wins over HOME",
			env:  map[string]string{"XDG_STATE_HOME": "/xdg/state", "HOME": "/home/synthetic"},
			want: "/xdg/state/orrery/archive.jsonl",
		},
		{
			name: "HOME fallback when XDG_STATE_HOME is unset",
			env:  map[string]string{"HOME": "/home/synthetic"},
			want: "/home/synthetic/.local/state/orrery/archive.jsonl",
		},
		{
			name: "HOME fallback when XDG_STATE_HOME is empty",
			env:  map[string]string{"XDG_STATE_HOME": "", "HOME": "/home/synthetic"},
			want: "/home/synthetic/.local/state/orrery/archive.jsonl",
		},
	}
	for _, tc := range cases {
		got, err := archive.Path(env(tc.env))
		if err != nil {
			t.Errorf("%s: error %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPathErrorsWhenNoStateLocation(t *testing.T) {
	if got, err := archive.Path(env(nil)); err == nil {
		t.Errorf("Path with XDG_STATE_HOME and HOME unset = %q, want error", got)
	}
}

func TestAppendCreatesDirectoryAndFileAndAppendsLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "orrery", "archive.jsonl")

	first := archive.Record{
		Dir:           "/work/alpha",
		Session:       "s1",
		ClaudeSession: "c1",
		Transcript:    "/transcripts/one.jsonl",
		Label:         "one",
		Summary:       "first summary",
		Closed:        100,
	}
	second := archive.Record{
		Dir:           "/work/beta",
		Session:       "s2",
		ClaudeSession: "c2",
		Transcript:    "/transcripts/two.jsonl",
		Label:         "two",
		Summary:       "second summary",
		Closed:        200,
	}
	if err := archive.Append(path, first); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if err := archive.Append(path, second); err != nil {
		t.Fatalf("Append second: %v", err)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("parent directory not created: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("parent directory perm = %o, want 700", got)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("archive file not created: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("archive file perm = %o, want 600", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	want := `{"dir":"/work/alpha","session":"s1","transcript":"/transcripts/one.jsonl",` +
		`"label":"one","summary":"first summary","closed":100,"claude_session":"c1"}` + "\n" +
		`{"dir":"/work/beta","session":"s2","transcript":"/transcripts/two.jsonl",` +
		`"label":"two","summary":"second summary","closed":200,"claude_session":"c2"}` + "\n"
	if string(data) != want {
		t.Errorf("archive bytes:\n got %q\nwant %q", data, want)
	}
}

func TestForDirFiltersNewestFirstWithTies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	appendRecords(t, path,
		archive.Record{Dir: "/work/alpha", Session: "a", Closed: 100},
		archive.Record{Dir: "/work/beta", Session: "b", Closed: 300},
		archive.Record{Dir: "/work/alpha", Session: "c", Closed: 200},
		archive.Record{Dir: "/work/alpha/sub", Session: "x", Closed: 999},
		archive.Record{Dir: "/work/alpha", Session: "d", Closed: 200},
	)

	got, err := archive.ForDir(path, "/work/alpha")
	if err != nil {
		t.Fatalf("ForDir: %v", err)
	}
	wantSessions := []string{"d", "c", "a"}
	if len(got) != len(wantSessions) {
		t.Fatalf("ForDir returned %d records, want %d: %+v", len(got), len(wantSessions), got)
	}
	for i, want := range wantSessions {
		if got[i].Session != want {
			t.Errorf("record %d session = %q, want %q", i, got[i].Session, want)
		}
	}
}

func TestForDirRoundTripsClaudeSessionWithEscaping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	want := archive.Record{
		Dir:           "/work/alpha",
		Session:       "s1",
		ClaudeSession: `c"1\2`,
		Transcript:    "/transcripts/one.jsonl",
		Label:         "one",
		Summary:       "summary",
		Closed:        100,
	}
	appendRecords(t, path, want)

	got, err := archive.ForDir(path, "/work/alpha")
	if err != nil {
		t.Fatalf("ForDir: %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("ForDir = %+v, want [%+v]", got, want)
	}
}

func TestForDirNonStringClaudeSessionIsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	content := `{"dir":"/work/alpha","session":"a","claude_session":7,"closed":1}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	got, err := archive.ForDir(path, "/work/alpha")
	if err == nil {
		t.Fatalf("ForDir with non-string claude_session returned %+v, want error", got)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error %q does not name line 1", err)
	}
}

func TestForDirLineWithoutClaudeSessionReadsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	content := `{"dir":"/work/alpha","session":"a","closed":1}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	got, err := archive.ForDir(path, "/work/alpha")
	if err != nil {
		t.Fatalf("ForDir: %v", err)
	}
	if len(got) != 1 || got[0].ClaudeSession != "" {
		t.Errorf("ForDir = %+v, want one record with empty ClaudeSession", got)
	}
}

func TestForDirMissingFileYieldsNoRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "archive.jsonl")

	got, err := archive.ForDir(path, "/work/alpha")
	if err != nil {
		t.Fatalf("ForDir on missing file: error %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ForDir on missing file returned %d records, want 0", len(got))
	}
}

func TestForDirMalformedLineErrorNamesLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	content := `{"dir":"/work/alpha","session":"a","closed":1}` + "\n" +
		"not json\n" +
		`{"dir":"/work/alpha","session":"c","closed":3}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	got, err := archive.ForDir(path, "/work/alpha")
	if err == nil {
		t.Fatalf("ForDir with malformed line returned %+v, want error", got)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not name line 2", err)
	}
}

func TestDirsNewestFirstOncePerDirWithTies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	appendRecords(t, path,
		archive.Record{Dir: "/work/alpha", Session: "a1", Closed: 100},
		archive.Record{Dir: "/work/beta", Session: "b1", Closed: 300},
		archive.Record{Dir: "/work/gamma", Session: "g1", Closed: 200},
		archive.Record{Dir: "/work/alpha", Session: "a2", Closed: 200},
		archive.Record{Dir: "/work/gamma", Session: "g2", Closed: 50},
		archive.Record{Dir: "/work/beta", Session: "b2", Closed: 200},
		archive.Record{Dir: "", Session: "none", Closed: 999},
	)

	got, err := archive.Dirs(path)
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	want := []string{"/work/beta", "/work/alpha", "/work/gamma"}
	if !slices.Equal(got, want) {
		t.Errorf("Dirs = %q, want %q", got, want)
	}
}

func TestDirsMissingFileYieldsNoDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "archive.jsonl")

	got, err := archive.Dirs(path)
	if err != nil {
		t.Fatalf("Dirs on missing file: error %v", err)
	}
	if got != nil {
		t.Errorf("Dirs on missing file = %q, want nil", got)
	}
}

func TestDirsMalformedLineErrorNamesLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	content := `{"dir":"/work/alpha","session":"a","closed":1}` + "\n" +
		"not json\n" +
		`{"dir":"/work/beta","session":"b","closed":2}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	got, err := archive.Dirs(path)
	if err == nil {
		t.Fatalf("Dirs with malformed line returned %q, want error", got)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not name line 2", err)
	}
}

func TestDirsLeavesArchiveUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	appendRecords(t, path,
		archive.Record{Dir: "/work/alpha", Session: "a", ClaudeSession: "c", Closed: 100},
		archive.Record{Dir: "/work/beta", Session: "b", Closed: 200},
	)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	if _, err := archive.Dirs(path); err != nil {
		t.Fatalf("Dirs: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive after Dirs: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("Dirs changed the archive:\n got %q\nwant %q", after, before)
	}
}

func appendRecords(t *testing.T, path string, records ...archive.Record) {
	t.Helper()
	for _, record := range records {
		if err := archive.Append(path, record); err != nil {
			t.Fatalf("Append %+v: %v", record, err)
		}
	}
}
