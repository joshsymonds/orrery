package webui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func readIndex(t *testing.T) string {
	t.Helper()
	data, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("FS() has no index.html at its root: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("index.html at the root of FS() is empty")
	}
	return string(data)
}

func TestFSServesIndexAtRoot(t *testing.T) {
	readIndex(t)
}

func TestIndexHasNoInlineScript(t *testing.T) {
	page := strings.ToLower(readIndex(t))
	rest := page
	for {
		at := strings.Index(rest, "<script")
		if at < 0 {
			return
		}
		tag := rest[at:]
		end := strings.Index(tag, ">")
		if end < 0 {
			t.Fatalf("unterminated <script tag in index.html: %q", tag)
		}
		if !strings.Contains(tag[:end], "src=") {
			t.Fatalf("inline <script> in index.html: %q", tag[:end+1])
		}
		rest = tag[end:]
	}
}

func TestIndexHasNoInlineStyle(t *testing.T) {
	page := readIndex(t)
	if regexp.MustCompile(`(?i)\sstyle\s*=`).MatchString(page) {
		t.Fatal("index.html carries a style attribute")
	}
	if regexp.MustCompile(`(?i)<style\b`).MatchString(page) {
		t.Fatal("index.html carries a <style> element")
	}
}

func TestFSRejectsPathsOutsideRoot(t *testing.T) {
	for _, name := range []string{"../dist/index.html", "/index.html", "dist/index.html"} {
		if _, err := fs.ReadFile(FS(), name); err == nil {
			t.Errorf("fs.ReadFile(FS(), %q) succeeded, want an error", name)
		}
	}
}
