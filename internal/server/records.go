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
	"sync"
	"time"
)

// liveWindow is how long after its head state.json last changed an epic stays live without a running step.
const liveWindow = 24 * time.Hour

// epicRecord is one epic under the gambit record root, read from its head state.json.
type epicRecord struct {
	ID, Repository, Slug string
	Dir                  string    // absolute record directory
	Outcome              string    // recordText of the head's "outcome"
	OutcomeDetail        string    // recordText of the head's "outcome_detail"
	Changed              time.Time // head state.json mtime
	Problem              string    // empty when the head parsed
}

// readEpics lists every epic under root, sorted by ID. A missing root lists nothing. Records are only read, through
// a read-only view of root; a head that does not parse is listed with a Problem, and only an unexpected I/O failure
// is an error.
func readEpics(root string, heads *headCache) ([]epicRecord, error) {
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
		found, repoErr := readRepo(root, fsys, repo.Name(), heads)
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
func findEpic(root, id string, heads *headCache) (epicRecord, bool, error) {
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
	return readHead(root, fsys, repo, slug, heads)
}

// epicLive reports whether an epic is live: never when its outcome's leading letters are "released", otherwise when a
// labeled step of it runs or its head state.json changed within liveWindow before now.
func epicLive(rec epicRecord, now time.Time, running bool) bool {
	if leadingLetters(rec.Outcome) == "released" {
		return false
	}
	if running {
		return true
	}
	age := now.Sub(rec.Changed)
	return age >= 0 && age <= liveWindow
}

// leadingLetters returns the run of ASCII letters at the start of s, which is empty when s does not start with one.
func leadingLetters(s string) string {
	end := 0
	for end < len(s) && (s[end] >= 'a' && s[end] <= 'z' || s[end] >= 'A' && s[end] <= 'Z') {
		end++
	}
	return s[:end]
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
func readRepo(root string, fsys fs.FS, repo string, heads *headCache) ([]epicRecord, error) {
	slugs, err := fs.ReadDir(fsys, repo)
	if err != nil {
		return nil, fmt.Errorf("read record repository: %w", err)
	}
	recs := []epicRecord{}
	for _, slug := range slugs {
		if !listed(slug) {
			continue
		}
		rec, ok, headErr := readHead(root, fsys, repo, slug.Name(), heads)
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
// state.json. The head is stat'd on every call, and decoded through heads only when its size or mtime changed.
func readHead(root string, fsys fs.FS, repo, slug string, heads *headCache) (epicRecord, bool, error) {
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
	key := filepath.Join(rec.Dir, "state.json")
	entry, ok := heads.lookup(key, info)
	if !ok {
		raw, readErr := fs.ReadFile(fsys, headPath)
		if readErr != nil {
			return epicRecord{}, false, fmt.Errorf("read record head: %w", readErr)
		}
		entry = decodeHead(raw)
		heads.store(key, info, entry)
	}
	if !entry.parsed {
		rec.Problem = headPath + ": does not parse"
		return rec, true, nil
	}
	rec.Outcome = entry.outcome
	rec.OutcomeDetail = entry.outcomeDetail
	return rec, true, nil
}

// headFields returns the raw top-level fields of a head state.json, and false when raw does not parse as a JSON object.
func headFields(raw []byte) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, false
	}
	return fields, true
}

// headCache holds the decode of each record state.json the epics server has read, keyed by path, so that a read
// decodes a file again only after its size or mtime changed. It is shared by every call of an epicsServer, which
// serves concurrent RPCs, so its methods hold mu.
type headCache struct {
	mu      sync.Mutex
	entries map[string]headEntry
}

// headEntry is the decode of one state.json, parse failures included, with the size and mtime the file had when it was
// decoded. A head fills outcome and outcomeDetail; an effort state fills tasks. parsed is false when the content did
// not parse.
type headEntry struct {
	size          int64
	modTime       time.Time
	parsed        bool
	outcome       string
	outcomeDetail string
	tasks         []taskRecord
}

// newHeadCache returns an empty headCache.
func newHeadCache() *headCache {
	return &headCache{entries: make(map[string]headEntry)}
}

// lookup returns the entry of the file at key when the entry was decoded at the size and mtime info reports. It
// reports false when the file has no entry or has changed since.
func (c *headCache) lookup(key string, info fs.FileInfo) (headEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || entry.size != info.Size() || !entry.modTime.Equal(info.ModTime()) {
		return headEntry{}, false
	}
	return entry, true
}

// store records entry as the decode of the file at key, which info describes. info must be the stat taken before the
// file was read, so that the entry is never newer than its content.
func (c *headCache) store(key string, info fs.FileInfo, entry headEntry) {
	entry.size = info.Size()
	entry.modTime = info.ModTime()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry
}

// decodeHead decodes the content of a head state.json into the entry readHead keeps.
func decodeHead(raw []byte) headEntry {
	fields, ok := headFields(raw)
	return headEntry{
		parsed:        ok,
		outcome:       recordText(fields["outcome"]),
		outcomeDetail: recordText(fields["outcome_detail"]),
	}
}
