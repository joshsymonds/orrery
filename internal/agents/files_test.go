package agents

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// reviewerMD is an agent file that defines an agent named reviewer.
const reviewerMD = "---\nname: reviewer\ndescription: Reviews code\n---\n"

// writeTree writes each file in files under root, creating its parent folders, and returns root.
func writeTree(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// link makes name a symbolic link to target.
func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Fatalf("symlink %s -> %s: %v", name, target, err)
	}
}

// lock sets path's mode to 000 and restores it when the test ends, before its temp dir is removed.
func lock(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Errorf("restore %s: %v", path, err)
		}
	})
}

func TestFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantName string
		wantDesc string
	}{
		{
			name:     "plain values",
			data:     "---\nname: reviewer\ndescription: Reviews code\n---\nbody\n",
			wantName: "reviewer",
			wantDesc: "Reviews code",
		},
		{
			name:     "single-quoted values",
			data:     "---\nname: 'reviewer'\ndescription: 'It''s a reviewer'\n---\n",
			wantName: "reviewer",
			wantDesc: "It's a reviewer",
		},
		{
			name:     "double-quoted values",
			data:     "---\nname: \"reviewer\"\ndescription: \"Reviews \\\"code\\\"\"\n---\n",
			wantName: "reviewer",
			wantDesc: "Reviews \"code\"",
		},
		{
			name:     "literal block description",
			data:     "---\nname: reviewer\ndescription: |\n  Reviews code.\n  Keeps going.\n---\n",
			wantName: "reviewer",
			wantDesc: "Reviews code.\nKeeps going.",
		},
		{
			name:     "folded block description",
			data:     "---\nname: reviewer\ndescription: >\n  Reviews code\n  in order.\n---\n",
			wantName: "reviewer",
			wantDesc: "Reviews code in order.",
		},
		{
			name:     "key after a block description",
			data:     "---\ndescription: |\n  Reviews code.\nname: reviewer\n---\n",
			wantName: "reviewer",
			wantDesc: "Reviews code.",
		},
		{
			name:     "no frontmatter",
			data:     "# reviewer\nname: reviewer\n",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "frontmatter never closed",
			data:     "---\nname: reviewer\ndescription: Reviews code\n",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "no description",
			data:     "---\nname: reviewer\n---\n",
			wantName: "reviewer",
			wantDesc: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotDesc := frontmatter([]byte(tt.data))
			if gotName != tt.wantName || gotDesc != tt.wantDesc {
				t.Errorf("frontmatter() = %q, %q; want %q, %q", gotName, gotDesc, tt.wantName, tt.wantDesc)
			}
		})
	}
}

func TestDirAgents(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "name is the frontmatter name, not the filename",
			files: map[string]string{"review-code.md": reviewerMD},
			want:  []string{"reviewer"},
		},
		{
			name:  "missing name defines nothing",
			files: map[string]string{"a.md": "---\ndescription: Reviews code\n---\n"},
			want:  nil,
		},
		{
			name:  "missing description defines nothing",
			files: map[string]string{"a.md": "---\nname: reviewer\n---\n"},
			want:  nil,
		},
		{
			name:  "empty description defines nothing",
			files: map[string]string{"a.md": "---\nname: reviewer\ndescription:\n---\n"},
			want:  nil,
		},
		{
			name:  "name with a colon defines nothing",
			files: map[string]string{"a.md": "---\nname: plugin:reviewer\ndescription: Reviews code\n---\n"},
			want:  nil,
		},
		{
			name:  "name starting with a dash defines nothing",
			files: map[string]string{"a.md": "---\nname: -reviewer\ndescription: Reviews code\n---\n"},
			want:  nil,
		},
		{
			name:  "no frontmatter defines nothing",
			files: map[string]string{"reviewer.md": "# reviewer\n"},
			want:  nil,
		},
		{
			name:  "nested subfolder counts",
			files: map[string]string{"team/deep/x.md": "---\nname: deep\ndescription: Reviews code\n---\n"},
			want:  []string{"deep"},
		},
		{
			name: "non-md files are ignored",
			files: map[string]string{
				"reviewer.txt":      reviewerMD,
				"reviewer.markdown": reviewerMD,
			},
			want: nil,
		},
		{
			name: "mixed folder keeps defining files in walk order",
			files: map[string]string{
				"alpha.md":     "---\nname: alpha\ndescription: Alpha agent\n---\n",
				"beta.md":      "---\ndescription: No name\n---\n",
				"sub/gamma.md": "---\nname: gamma\ndescription: Gamma agent\n---\n",
			},
			want: []string{"alpha", "gamma"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeTree(t, t.TempDir(), tt.files)
			got, err := dirAgents(dir)
			if err != nil {
				t.Fatalf("dirAgents: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("dirAgents() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestDirAgentsSymlinks(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) string
		want  []string
	}{
		{
			name: "symlinked agents directory whose md entries are symlinks",
			setup: func(t *testing.T) string {
				t.Helper()
				source := writeTree(t, t.TempDir(), map[string]string{"reviewer.md": reviewerMD})
				entries := t.TempDir()
				link(t, filepath.Join(source, "reviewer.md"), filepath.Join(entries, "reviewer.md"))
				agents := filepath.Join(t.TempDir(), "agents")
				link(t, entries, agents)
				return agents
			},
			want: []string{"reviewer"},
		},
		{
			name: "symlinked subdirectory is followed",
			setup: func(t *testing.T) string {
				t.Helper()
				root := t.TempDir()
				other := writeTree(t, t.TempDir(), map[string]string{
					"x.md": "---\nname: linked\ndescription: Linked agent\n---\n",
				})
				link(t, other, filepath.Join(root, "team"))
				return root
			},
			want: []string{"linked"},
		},
		{
			name: "symlink cycle visits each real directory once",
			setup: func(t *testing.T) string {
				t.Helper()
				root := writeTree(t, t.TempDir(), map[string]string{
					"reviewer.md":  reviewerMD,
					"sub/other.md": "# no frontmatter\n",
				})
				link(t, root, filepath.Join(root, "loop"))
				link(t, root, filepath.Join(root, "sub", "up"))
				return root
			},
			want: []string{"reviewer"},
		},
		{
			name: "dangling non-md symlink is ignored",
			setup: func(t *testing.T) string {
				t.Helper()
				root := writeTree(t, t.TempDir(), map[string]string{"reviewer.md": reviewerMD})
				link(t, filepath.Join(root, "missing.txt"), filepath.Join(root, "notes.txt"))
				return root
			},
			want: []string{"reviewer"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dirAgents(tt.setup(t))
			if err != nil {
				t.Fatalf("dirAgents: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("dirAgents() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestDirAgentsMissingDir(t *testing.T) {
	got, err := dirAgents(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(got) != 0 {
		t.Errorf("dirAgents(missing) = %q, %v; want no names and nil", got, err)
	}
}

func TestDirAgentsUnreadable(t *testing.T) {
	assertErrorNames := func(t *testing.T, root, path string) {
		t.Helper()
		got, err := dirAgents(root)
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("dirAgents() = %q, %v; want an error naming %s", got, err, path)
		}
	}
	t.Run("unreadable file", func(t *testing.T) {
		root := writeTree(t, t.TempDir(), map[string]string{"locked.md": reviewerMD})
		locked := filepath.Join(root, "locked.md")
		lock(t, locked)
		assertErrorNames(t, root, locked)
	})
	t.Run("unreadable subdirectory", func(t *testing.T) {
		root := writeTree(t, t.TempDir(), map[string]string{"locked/reviewer.md": reviewerMD})
		locked := filepath.Join(root, "locked")
		lock(t, locked)
		assertErrorNames(t, root, locked)
	})
}

func TestWalkMarkdown(t *testing.T) {
	root := writeTree(t, t.TempDir(), map[string]string{"a.md": "A", "notes.txt": "skip", "sub/b.md": "B"})
	link(t, root, filepath.Join(root, "sub", "loop"))

	var got []string
	err := walkMarkdown(root, func(rel string, data []byte) error {
		got = append(got, rel+"="+string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("walkMarkdown: %v", err)
	}
	want := []string{"a.md=A", "sub/b.md=B"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("walkMarkdown visited %q; want %q", got, want)
	}

	sentinel := errors.New("visit stopped")
	err = walkMarkdown(root, func(string, []byte) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("walkMarkdown error = %v; want the visit error %v", err, sentinel)
	}
}
