// Package webui embeds the built web page and exposes it as a filesystem rooted at its index.
package webui

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
)

//go:embed dist
var dist embed.FS

// root serves dist/ as the top of the tree, so index.html opens from the root.
type root struct{}

// Open maps a valid name under the root to the same name under dist/. fs.ValidPath rejects
// names that would climb out of dist/, which path.Join alone would not.
func (root) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	file, err := dist.Open(path.Join("dist", name))
	if err != nil {
		return nil, fmt.Errorf("open web page %s: %w", name, err)
	}
	return file, nil
}

// FS returns the embedded web page with dist/ as its root.
func FS() fs.FS {
	return root{}
}
