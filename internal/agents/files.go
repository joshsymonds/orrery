// Package agents reads the agents that Claude Code's user, project and managed agents directories define.
package agents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// quotes is the number of quote marks that wrap a quoted scalar.
const quotes = 2

// dirAgents returns the agent names that the user, project or managed agents directory dir defines. A .md file under
// dir defines the name in its frontmatter, never its filename, when it has a description and the name is not empty,
// has no ':' and does not start with '-'. Names come in walk order, one per defining file. A dir that does not exist
// defines none.
func dirAgents(dir string) ([]string, error) {
	var names []string
	err := walkMarkdown(dir, func(_ string, data []byte) error {
		name, description := frontmatter(data)
		if name != "" && description != "" && !strings.Contains(name, ":") && !strings.HasPrefix(name, "-") {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// walkMarkdown calls visit with the path relative to dir and the contents of each .md file under dir, in lexical
// order, and returns the first error that visit returns. Symlinked directories and .md files are followed, and each
// real directory is walked once, so a symlink cycle ends. A dir that does not exist has no files and is not an error.
// Any other entry that cannot be read is an error that names its path, except a dangling symlink that is not a .md
// file, which has nothing to read and is skipped.
func walkMarkdown(dir string, visit func(rel string, data []byte) error) error {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	w := walk{fsys: os.DirFS(dir), seen: make(map[string]bool), visit: visit}
	return w.folder(dir, ".")
}

// walk is the state of one walkMarkdown. fsys is the file system rooted at the walk's root, seen holds the resolved
// path of each real folder walked so far, and visit receives each .md file.
type walk struct {
	fsys  fs.FS
	seen  map[string]bool
	visit func(rel string, data []byte) error
}

// folder walks the folder at dir, which is the folder rel in w.fsys. A folder whose resolved path is in w.seen has been
// walked already and is skipped.
func (w walk) folder(dir, rel string) error {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("agents: read %s: %w", dir, err)
	}
	if w.seen[resolved] {
		return nil
	}
	w.seen[resolved] = true
	list, err := fs.ReadDir(w.fsys, rel)
	if err != nil {
		return fmt.Errorf("agents: read %s: %w", dir, err)
	}
	return w.entries(dir, rel, list)
}

// entries walks each of list, the entries of the folder at dir, which is rel in w.fsys.
func (w walk) entries(dir, rel string, list []fs.DirEntry) error {
	for _, entry := range list {
		if err := w.entry(dir, rel, entry); err != nil {
			return err
		}
	}
	return nil
}

// entry walks entry, an entry of the folder at dir, which is rel in w.fsys. A folder is walked, and a .md file is
// visited with its path relative to the walk's root.
func (w walk) entry(dir, rel string, entry fs.DirEntry) error {
	name := entry.Name()
	childDir := filepath.Join(dir, name)
	childRel := path.Join(rel, name)
	info, err := fs.Stat(w.fsys, childRel)
	if err != nil {
		if path.Ext(name) != ".md" && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("agents: read %s: %w", childDir, err)
	}
	if info.IsDir() {
		return w.folder(childDir, childRel)
	}
	if path.Ext(name) != ".md" || !info.Mode().IsRegular() {
		return nil
	}
	data, err := fs.ReadFile(w.fsys, childRel)
	if err != nil {
		return fmt.Errorf("agents: read %s: %w", childDir, err)
	}
	return w.visit(childRel, data)
}

// frontmatter returns the name and description in data's frontmatter, the YAML block that a first line `---` opens and
// the next `---` line closes. A value is plain, single-quoted, double-quoted, or a block scalar introduced by `|` or `>`.
// Data without a closed frontmatter, or without the key, gives an empty string for it.
func frontmatter(data []byte) (name, description string) {
	block, ok := frontmatterBlock(data)
	if !ok {
		return "", ""
	}
	for i := 0; i < len(block); i++ {
		key, rest, found := strings.Cut(block[i], ":")
		if !found || (key != "name" && key != "description") {
			continue
		}
		value, last := scalar(block, i, strings.TrimSpace(rest))
		i = last
		if key == "name" {
			name = value
		} else {
			description = value
		}
	}
	return name, description
}

// frontmatterBlock returns the lines between data's opening `---` line and the next `---` line, and whether both exist.
func frontmatterBlock(data []byte) ([]string, bool) {
	lines := strings.Split(string(data), "\n")
	if strings.TrimRight(lines[0], " \t") != "---" {
		return nil, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return lines[1:i], true
		}
	}
	return nil, false
}

// scalar returns the value of the key on block[i], whose text after the colon is rest, and the index of the last line
// that the value spans. A block scalar spans the blank and indented lines that follow it.
func scalar(block []string, i int, rest string) (string, int) {
	if !strings.HasPrefix(rest, "|") && !strings.HasPrefix(rest, ">") {
		return unquote(rest), i
	}
	last := i
	for last+1 < len(block) && (strings.TrimSpace(block[last+1]) == "" || block[last+1][0] == ' ' || block[last+1][0] == '\t') {
		last++
	}
	body := dedent(block[i+1 : last+1])
	if rest[0] == '>' {
		return strings.Join(strings.Fields(strings.Join(body, " ")), " "), last
	}
	return strings.TrimSpace(strings.Join(body, "\n")), last
}

// dedent removes the indentation that the least-indented non-blank line has, and empties the blank lines.
func dedent(lines []string) []string {
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if n := len(line) - len(strings.TrimLeft(line, " \t")); indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			out[i] = line[indent:]
		}
	}
	return out
}

// unquote returns the text of a plain, single-quoted or double-quoted scalar. A double-quoted scalar whose escapes Go
// cannot read has no text, so the key it sets defines nothing.
func unquote(value string) string {
	if len(value) < quotes {
		return value
	}
	switch {
	case value[0] == '\'' && value[len(value)-1] == '\'':
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	case value[0] == '"' && value[len(value)-1] == '"':
		text, err := strconv.Unquote(value)
		if err != nil {
			return ""
		}
		return text
	}
	return value
}
