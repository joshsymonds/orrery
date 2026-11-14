package enginehost

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// evidenceDirMode is the mode of the blob directory when put creates it.
const evidenceDirMode fs.FileMode = 0o700

// evidenceHashLen is the length of a blob name's hash: a sha256 digest in lowercase hex.
const evidenceHashLen = 2 * sha256.Size

// evidenceMaxExt is the most characters an extension evidenceExt gives may have, not counting its dot.
const evidenceMaxExt = 8

// evidenceExtChars are the characters an extension evidenceExt gives may be made of, after lowercasing.
const evidenceExtChars = "abcdefghijklmnopqrstuvwxyz0123456789"

// errBlobName reports a blob name that is not a lowercase sha256 hex digest with an extension evidenceExt gives.
var errBlobName = errors.New("enginehost: evidence blob name is not a sha256 hash with an extension")

// evidenceBlobs stores evidence bytes as files named by their sha256 under dir, the evidence/blobs directory of the
// state directory. A file's name is its hash and an extension, so identical bytes share one file and no file is pruned.
type evidenceBlobs struct{ dir string }

// evidenceExt returns the lowercased extension of path with its dot when it is 1 to 8 characters of [a-z0-9] after
// lowercasing, and "" otherwise.
func evidenceExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	body, dotted := strings.CutPrefix(ext, ".")
	if !dotted || body == "" || len(body) > evidenceMaxExt {
		return ""
	}
	for _, c := range body {
		if !strings.ContainsRune(evidenceExtChars, c) {
			return ""
		}
	}
	return ext
}

// put stores data as a file named by its sha256 and ext, and returns the hash and the name. An existing file of that
// name is kept as it is. A new file is written through a temporary file in the blob directory and renamed into place, and
// the blob directory is created at 0700 when it is missing. Errors name the path.
func (b evidenceBlobs) put(data []byte, ext string) (hash, name string, err error) {
	sum := sha256.Sum256(data)
	hash = hex.EncodeToString(sum[:])
	name = hash + ext
	file, err := b.path(name)
	if err != nil {
		return "", "", err
	}
	_, statErr := os.Stat(file)
	if statErr == nil {
		return hash, name, nil
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", fmt.Errorf("stat evidence blob %s: %w", file, statErr)
	}
	if mkdirErr := os.MkdirAll(b.dir, evidenceDirMode); mkdirErr != nil {
		return "", "", fmt.Errorf("create evidence blob directory %s: %w", b.dir, mkdirErr)
	}
	if writeErr := writeBlobFile(b.dir, file, data); writeErr != nil {
		return "", "", writeErr
	}
	return hash, name, nil
}

// writeBlobFile writes data to a temporary file in dir and renames it to file. CreateTemp makes the temporary file
// 0600, and the renamed file keeps that mode. A failure removes the temporary file.
func writeBlobFile(dir, file string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".blob-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return errors.Join(fmt.Errorf("write evidence blob %s: %w", file, err), os.Remove(tmp.Name()))
	}
	if err = os.Rename(tmp.Name(), file); err != nil {
		return errors.Join(fmt.Errorf("rename evidence blob into place at %s: %w", file, err), os.Remove(tmp.Name()))
	}
	return nil
}

// path returns the file of a blob name inside the blob directory. The name is checked before any path is built from it,
// so a name with a separator, a dot-dot element or a hash that is not lowercase hex is refused.
func (b evidenceBlobs) path(name string) (string, error) {
	if err := checkBlobName(name); err != nil {
		return "", err
	}
	return filepath.Join(b.dir, name), nil
}

// read returns the bytes of the blob named name. The name is checked as path checks it, and the file is read through an
// os.Root of the blob directory, so the read stays inside that directory.
func (b evidenceBlobs) read(name string) ([]byte, error) {
	file, err := b.path(name)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(b.dir)
	if err != nil {
		return nil, fmt.Errorf("open evidence blob directory %s: %w", b.dir, err)
	}
	data, readErr := root.ReadFile(name)
	if err = errors.Join(readErr, root.Close()); err != nil {
		return nil, fmt.Errorf("read evidence blob %s: %w", file, err)
	}
	return data, nil
}

// checkBlobName returns errBlobName unless name is a lowercase sha256 hex digest, optionally followed by an extension
// that evidenceExt gives (".log" among them).
func checkBlobName(name string) error {
	hash, ext := name, ""
	if len(name) > evidenceHashLen {
		hash, ext = name[:evidenceHashLen], name[evidenceHashLen:]
	}
	if len(hash) != evidenceHashLen || !isLowerHex(hash) {
		return fmt.Errorf("%w: %q", errBlobName, name)
	}
	if ext != "" && evidenceExt("x"+ext) != ext {
		return fmt.Errorf("%w: %q", errBlobName, name)
	}
	return nil
}

// isLowerHex reports whether s is hex digits with no uppercase letters.
func isLowerHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}
