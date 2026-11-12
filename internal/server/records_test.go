package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeHead writes a synthetic head state.json for the epic <repo>/<slug> under root and sets its mtime.
func writeHead(t *testing.T, root, repo, slug, body string, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(root, repo, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestReadEpicsListsDirectoriesHoldingState(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	writeHead(t, root, "repoa", "slug-b", `{"outcome":"released"}`, now)
	writeHead(t, root, "repoa", "slug-a", `{"outcome":"shipped"}`, now)
	writeHead(t, root, "repob", "x", `{}`, now)
	// A directory without state.json, a plain file in a repo directory, and a dot-named epic are not epics.
	if err := os.MkdirAll(filepath.Join(root, "repoa", "nohead"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "repoa", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeHead(t, root, "repoa", ".hidden", `{}`, now)
	// A dot-named repo directory and a plain file at the root are not repositories.
	writeHead(t, root, ".dotrepo", "y", `{}`, now)
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readEpics(root)
	if err != nil {
		t.Fatalf("readEpics: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, rec := range got {
		ids = append(ids, rec.ID)
	}
	want := []string{"repoa/slug-a", "repoa/slug-b", "repob/x"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	first := got[0]
	if first.Repository != "repoa" || first.Slug != "slug-a" {
		t.Fatalf("repository/slug = %q/%q, want repoa/slug-a", first.Repository, first.Slug)
	}
	if first.Dir != filepath.Join(root, "repoa", "slug-a") {
		t.Fatalf("Dir = %q, want the absolute record directory", first.Dir)
	}
	if first.Outcome != "shipped" {
		t.Fatalf("Outcome = %q, want shipped", first.Outcome)
	}
	if first.Problem != "" {
		t.Fatalf("Problem = %q, want empty for a parsed head", first.Problem)
	}
}

func TestReadEpicsMissingRootIsEmpty(t *testing.T) {
	got, err := readEpics(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("readEpics on a missing root: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("readEpics on a missing root = %v, want empty", got)
	}
}

func TestReadEpicsOutcomeIsRecordText(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"string", `{"outcome":"ended_with_gaps"}`, "ended_with_gaps"},
		{"null", `{"outcome":null}`, ""},
		{"absent", `{"tasks":[]}`, ""},
		{"number", `{"outcome":5}`, "5"},
		{"object", `{"outcome": {"a" : [1, 2]}}`, `{"a":[1,2]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeHead(t, root, "repo", "slug", tc.body, now)
			got, err := readEpics(root)
			if err != nil {
				t.Fatalf("readEpics: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("listed %d epics, want 1", len(got))
			}
			if got[0].Outcome != tc.want {
				t.Fatalf("Outcome = %q, want %q", got[0].Outcome, tc.want)
			}
		})
	}
}

func TestReadEpicsUnparseableHeadIsListedWithProblem(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"truncated", `{"outcome": `},
		{"array", `[1,2]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeHead(t, root, "repo", "slug", tc.body, time.Unix(1_700_000_000, 0))
			got, err := readEpics(root)
			if err != nil {
				t.Fatalf("readEpics: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("listed %d epics, want 1", len(got))
			}
			if got[0].Problem != filepath.Join("repo", "slug", "state.json")+": does not parse" {
				t.Fatalf("Problem = %q", got[0].Problem)
			}
			if got[0].Outcome != "" {
				t.Fatalf("Outcome = %q, want empty", got[0].Outcome)
			}
		})
	}
}

func TestReadEpicsChangedIsHeadMtime(t *testing.T) {
	root := t.TempDir()
	mtime := time.Unix(1_699_000_000, 0)
	writeHead(t, root, "repo", "slug", `{}`, mtime)
	got, err := readEpics(root)
	if err != nil {
		t.Fatalf("readEpics: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d epics, want 1", len(got))
	}
	if !got[0].Changed.Equal(mtime) {
		t.Fatalf("Changed = %v, want %v", got[0].Changed, mtime)
	}
}

func TestRecordTextForms(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{"nil", nil, ""},
		{"null", json.RawMessage(`null`), ""},
		{"string", json.RawMessage(`"released"`), "released"},
		{"empty string", json.RawMessage(`""`), ""},
		{"bool", json.RawMessage(`true`), "true"},
		{"array", json.RawMessage("[ 1,\n 2 ]"), "[1,2]"},
		{"object", json.RawMessage(`{ "k" : "v" }`), `{"k":"v"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recordText(tc.raw); got != tc.want {
				t.Fatalf("recordText(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestEpicLive(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name    string
		outcome string
		changed time.Time
		running bool
		want    bool
	}{
		{"released is folded even when running", "released", now, true, false},
		{"released is folded even when recent", "released", now, false, false},
		{"running is live without a recent change", "", now.Add(-72 * time.Hour), true, true},
		{"recent change is live", "shipped", now.Add(-time.Hour), false, true},
		{"change inside 24h is live", "", now.Add(-24*time.Hour + time.Minute), false, true},
		{"change at exactly 24h is live", "", now.Add(-24 * time.Hour), false, true},
		{"old change and not running is folded", "ended_with_gaps", now.Add(-25 * time.Hour), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := epicRecord{Outcome: tc.outcome, Changed: tc.changed}
			if got := epicLive(rec, now, tc.running); got != tc.want {
				t.Fatalf("epicLive = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindEpic(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	writeHead(t, root, "repoa", "slug", `{"outcome":"shipped"}`, now)

	rec, ok, err := findEpic(root, "repoa/slug")
	if err != nil {
		t.Fatalf("findEpic: %v", err)
	}
	if !ok || rec.ID != "repoa/slug" || rec.Dir != filepath.Join(root, "repoa", "slug") {
		t.Fatalf("findEpic listed id = (%+v, %v), want repoa/slug", rec, ok)
	}

	for _, id := range []string{"../x", "a/b/c", "repoa/missing", "repoa", ""} {
		_, ok, err := findEpic(root, id)
		if err != nil {
			t.Fatalf("findEpic(%q): %v", id, err)
		}
		if ok {
			t.Fatalf("findEpic(%q) found an unlisted id", id)
		}
	}

	_, ok, err = findEpic(filepath.Join(t.TempDir(), "missing"), "repoa/slug")
	if err != nil || ok {
		t.Fatalf("findEpic on a missing root = (%v, %v), want (false, nil)", ok, err)
	}
}
