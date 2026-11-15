package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// liveWindow is how long after its head state.json last changed an epic stays live without a running step.
const liveWindow = 24 * time.Hour

// epicRecord is one epic under the gambit record root, read from its head state.json.
type epicRecord struct {
	ID, Repository, Slug string
	Dir                  string    // absolute record directory
	Outcome              string    // recordText of the head's "outcome"
	Changed              time.Time // head state.json mtime
	Problem              string    // empty when the head parsed
}

// readEpics lists every epic under root, sorted by ID. A missing root lists nothing. Records are only read, through
// a read-only view of root; a head that does not parse is listed with a Problem, and only an unexpected I/O failure
// is an error.
func readEpics(root string) ([]epicRecord, error) {
	fsys := os.DirFS(root)
	repos, err := fs.ReadDir(fsys, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return []epicRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read record root: %w", err)
	}
	recs := []epicRecord{}
	for _, repo := range repos {
		if !listed(repo) {
			continue
		}
		found, repoErr := readRepo(root, fsys, repo.Name())
		if repoErr != nil {
			return nil, repoErr
		}
		recs = append(recs, found...)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
	return recs, nil
}

// listed reports whether a record listing names an epic or repository directory: a directory whose name is not
// dot-led. A symlink is not a directory in a listing, so it is never listed.
func listed(entry fs.DirEntry) bool {
	return entry.IsDir() && !strings.HasPrefix(entry.Name(), ".")
}

// findEpic returns the listed epic whose ID is id, reading only that epic's head. The id must split at "/" into exactly
// two parts: the first must be a repository in the record root's listing and the second an epic in that repository's
// listing, both by readEpics' filter. The id is never joined into a path, so an id the listings do not name, a symlink
// or a file among them, finds nothing and reads nothing.
func findEpic(root, id string) (epicRecord, bool, error) {
	repo, slug, found := strings.Cut(id, "/")
	if !found || strings.Contains(slug, "/") {
		return epicRecord{}, false, nil
	}
	fsys := os.DirFS(root)
	repos, err := fs.ReadDir(fsys, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return epicRecord{}, false, nil
	}
	if err != nil {
		return epicRecord{}, false, fmt.Errorf("read record root: %w", err)
	}
	if !slices.ContainsFunc(repos, func(entry fs.DirEntry) bool { return listed(entry) && entry.Name() == repo }) {
		return epicRecord{}, false, nil
	}
	slugs, err := fs.ReadDir(fsys, repo)
	if err != nil {
		return epicRecord{}, false, fmt.Errorf("read record repository: %w", err)
	}
	if !slices.ContainsFunc(slugs, func(entry fs.DirEntry) bool { return listed(entry) && entry.Name() == slug }) {
		return epicRecord{}, false, nil
	}
	return readHead(root, fsys, repo, slug)
}

// epicLive reports whether an epic is live: never when its outcome is exactly "released", otherwise when a
// labeled step of it runs or its head state.json changed within liveWindow before now.
func epicLive(rec epicRecord, now time.Time, running bool) bool {
	if rec.Outcome == "released" {
		return false
	}
	if running {
		return true
	}
	age := now.Sub(rec.Changed)
	return age >= 0 && age <= liveWindow
}

// recordText returns a record field as text: a JSON string is its value, null or absent is empty, and any other
// value is its compact JSON.
func recordText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return string(trimmed)
	}
	return compact.String()
}

// readRepo lists the epics under one repository directory of root, in directory order.
func readRepo(root string, fsys fs.FS, repo string) ([]epicRecord, error) {
	slugs, err := fs.ReadDir(fsys, repo)
	if err != nil {
		return nil, fmt.Errorf("read record repository: %w", err)
	}
	recs := []epicRecord{}
	for _, slug := range slugs {
		if !listed(slug) {
			continue
		}
		rec, ok, headErr := readHead(root, fsys, repo, slug.Name())
		if headErr != nil {
			return nil, headErr
		}
		if ok {
			recs = append(recs, rec)
		}
	}
	return recs, nil
}

// readHead reads the head state.json of <repo>/<slug> through fsys. It reports false when the directory holds no
// state.json.
func readHead(root string, fsys fs.FS, repo, slug string) (epicRecord, bool, error) {
	headPath := path.Join(repo, slug, "state.json")
	info, err := fs.Stat(fsys, headPath)
	if errors.Is(err, fs.ErrNotExist) {
		return epicRecord{}, false, nil
	}
	if err != nil {
		return epicRecord{}, false, fmt.Errorf("stat record head: %w", err)
	}
	rec := epicRecord{
		ID:         repo + "/" + slug,
		Repository: repo,
		Slug:       slug,
		Dir:        filepath.Join(root, repo, slug),
		Changed:    info.ModTime(),
	}
	raw, err := fs.ReadFile(fsys, headPath)
	if err != nil {
		return epicRecord{}, false, fmt.Errorf("read record head: %w", err)
	}
	outcome, ok := headOutcome(raw)
	if !ok {
		rec.Problem = headPath + ": does not parse"
		return rec, true, nil
	}
	rec.Outcome = recordText(outcome)
	return rec, true, nil
}

// headOutcome returns the raw "outcome" of a head state.json, and false when raw does not parse as a JSON object.
func headOutcome(raw []byte) (json.RawMessage, bool) {
	var head struct {
		Outcome json.RawMessage `json:"outcome"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return nil, false
	}
	return head.Outcome, true
}
