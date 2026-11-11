package open

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func envFrom(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestRootsPath(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "XDG_CONFIG_HOME set",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg/config", "HOME": "/made/up/home"},
			want: "/xdg/config/orrery/roots",
		},
		{
			name: "HOME only",
			env:  map[string]string{"HOME": "/made/up/home"},
			want: "/made/up/home/.config/orrery/roots",
		},
		{
			name: "neither set",
			env:  map[string]string{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RootsPath(envFrom(tt.env)); got != tt.want {
				t.Errorf("RootsPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadRootsMissingOrEmptyPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "roots")
	tests := []struct {
		name string
		path string
	}{
		{name: "empty path", path: ""},
		{name: "missing file", path: missing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots, err := ReadRoots(tt.path, "/made/up/home")
			if err != nil {
				t.Fatalf("ReadRoots() error = %v, want nil", err)
			}
			if roots != nil {
				t.Errorf("ReadRoots() = %+v, want no roots", roots)
			}
		})
	}
}

func TestReadRootsValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roots")
	writeRootsFile(t, path, "2 Work\n\n3 My Notes\n")

	roots, err := ReadRoots(path, "/made/up/home")
	if err != nil {
		t.Fatalf("ReadRoots() error = %v", err)
	}
	want := []Root{
		{Dir: "/made/up/home/Work", Depth: 2},
		{Dir: "/made/up/home/My Notes", Depth: 3},
	}
	if !slices.Equal(roots, want) {
		t.Errorf("ReadRoots() = %+v, want %+v", roots, want)
	}
}

func TestReadRootsMalformedLine(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "zero depth", line: "0 Work"},
		{name: "negative depth", line: "-1 Work"},
		{name: "non-numeric depth", line: "x Work"},
		{name: "plus-signed depth", line: "+2 Work"},
		{name: "no path", line: "2"},
		{name: "absolute path", line: "2 /abs"},
		{name: "escaping path", line: "2 ../out"},
		{name: "empty path after space", line: "2 "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "roots")
			writeRootsFile(t, path, "2 Work\n"+tt.line+"\n")

			_, err := ReadRoots(path, "/made/up/home")
			if err == nil {
				t.Fatalf("ReadRoots() error = nil, want error for %q", tt.line)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name file %q", err, path)
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("error %q does not name line 2", err)
			}
		})
	}
}

func TestReadRootsRelativeHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roots")
	writeRootsFile(t, path, "2 Work\n")

	if _, err := ReadRoots(path, "relative/home"); err == nil {
		t.Fatal("ReadRoots() error = nil, want error for a non-absolute home")
	}
}

func TestReadRootsUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roots")
	writeRootsFile(t, path, "2 Work\n")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Errorf("restore permissions: %v", err)
		}
	})

	if _, err := ReadRoots(path, "/made/up/home"); err == nil {
		t.Fatal("ReadRoots() error = nil, want error for an unreadable file")
	}
}

func writeRootsFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write roots file: %v", err)
	}
}
