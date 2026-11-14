package enginehost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestBlobs returns a blob store under a fresh state directory. Its blob directory does not exist yet.
func newTestBlobs(t *testing.T) evidenceBlobs {
	t.Helper()
	return evidenceBlobs{dir: filepath.Join(t.TempDir(), "evidence", "blobs")}
}

// blobHashOf returns the lowercase hex sha256 of data.
func blobHashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestEvidenceBlobPutTwiceKeepsOneFile puts the same bytes twice. Both calls return the sha256 of the bytes and that
// hash with its extension as the name, and the blob directory holds one file.
func TestEvidenceBlobPutTwiceKeepsOneFile(t *testing.T) {
	t.Parallel()
	blobs := newTestBlobs(t)
	data := []byte("capture log\n")
	wantHash := blobHashOf(data)

	hash, name, err := blobs.put(data, ".log")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	again, sameName, err := blobs.put(data, ".log")
	if err != nil {
		t.Fatalf("second put: %v", err)
	}
	if hash != wantHash || again != wantHash {
		t.Fatalf("put hashes = %q, %q, want %q", hash, again, wantHash)
	}
	if name != wantHash+".log" || sameName != name {
		t.Fatalf("put names = %q, %q, want %q", name, sameName, wantHash+".log")
	}
	entries, err := os.ReadDir(blobs.dir)
	if err != nil {
		t.Fatalf("read blob directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("blob directory holds %d entries, want 1", len(entries))
	}
}

// TestEvidenceBlobFileHoldsTheBytesAt0600 checks that put creates the blob directory at 0700 and writes the bytes into
// a file at 0600.
func TestEvidenceBlobFileHoldsTheBytesAt0600(t *testing.T) {
	t.Parallel()
	blobs := newTestBlobs(t)
	data := []byte("artifact bytes")

	_, name, err := blobs.put(data, ".bin")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	file := filepath.Join(blobs.dir, name)
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read blob file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("blob file holds %q, want %q", got, data)
	}
	fileInfo, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat blob file: %v", err)
	}
	if mode := fileInfo.Mode().Perm(); mode != 0o600 {
		t.Fatalf("blob file mode = %o, want 0600", mode)
	}
	dirInfo, err := os.Stat(blobs.dir)
	if err != nil {
		t.Fatalf("stat blob directory: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("blob directory mode = %o, want 0700", mode)
	}
}

// TestEvidenceBlobPutKeepsAnExistingFile overwrites a blob file after put wrote it, then puts the same bytes again. The
// second put keeps the bytes on disk as they are.
func TestEvidenceBlobPutKeepsAnExistingFile(t *testing.T) {
	t.Parallel()
	blobs := newTestBlobs(t)
	data := []byte("kept")
	_, name, err := blobs.put(data, ".txt")
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	file := filepath.Join(blobs.dir, name)
	planted := []byte("planted")
	if err = os.WriteFile(file, planted, 0o600); err != nil {
		t.Fatalf("plant existing file: %v", err)
	}
	if _, _, err = blobs.put(data, ".txt"); err != nil {
		t.Fatalf("second put: %v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read blob file: %v", err)
	}
	if !bytes.Equal(got, planted) {
		t.Fatalf("second put rewrote the existing file to %q, want it kept as %q", got, planted)
	}
}

// TestEvidenceBlobPutRefusesAnExtensionOutsideTheDirectory checks that put refuses an extension that would name a path
// outside the blob directory, and creates nothing.
func TestEvidenceBlobPutRefusesAnExtensionOutsideTheDirectory(t *testing.T) {
	t.Parallel()
	blobs := newTestBlobs(t)
	if _, _, err := blobs.put([]byte("x"), "/../escape"); err == nil {
		t.Fatal("put accepted an extension with a path separator")
	}
	if _, err := os.Stat(blobs.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat blob directory after a refused put = %v, want not exist", err)
	}
}

// TestEvidenceExtGivesTheLowercaseExtensionOfEightOrFewerCharacters checks evidenceExt on paths inside and outside the
// extension rule.
func TestEvidenceExtGivesTheLowercaseExtensionOfEightOrFewerCharacters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want string
	}{
		{"out/Shot.PNG", ".png"},
		{"a.tar.gz", ".gz"},
		{"notes", ""},
		{"x.", ""},
		{"x.verylongext", ""},
		{"x.p-g", ""},
		{"x.abcdefgh", ".abcdefgh"},
		{"x.abcdefghi", ""},
	}
	for _, tc := range cases {
		if got := evidenceExt(tc.path); got != tc.want {
			t.Errorf("evidenceExt(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestEvidenceBlobPathBuildsAHashName checks that path joins the blob directory and a name of a lowercase hash with no
// extension, or with an extension evidenceExt gives or .log.
func TestEvidenceBlobPathBuildsAHashName(t *testing.T) {
	t.Parallel()
	blobs := newTestBlobs(t)
	hash := blobHashOf([]byte("named"))
	for _, name := range []string{hash, hash + ".png", hash + ".log"} {
		got, err := blobs.path(name)
		if err != nil {
			t.Fatalf("path(%q): %v", name, err)
		}
		if want := filepath.Join(blobs.dir, name); got != want {
			t.Errorf("path(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestEvidenceBlobPathRefusesOtherNames checks that path refuses a name with a separator or a dot-dot element, a hash
// that is not 64 lowercase hex digits, and an extension evidenceExt does not give.
func TestEvidenceBlobPathRefusesOtherNames(t *testing.T) {
	t.Parallel()
	blobs := newTestBlobs(t)
	hash := blobHashOf([]byte("named"))
	for _, name := range []string{
		"../x",
		"a/b",
		hash + "/../x",
		strings.Repeat("a", 63),
		strings.ToUpper(hash),
		hash + ".PNG",
		hash + ".abcdefghi",
	} {
		if got, err := blobs.path(name); err == nil {
			t.Errorf("path(%q) = %q, want a refusal", name, got)
		}
	}
}

// TestEvidenceBlobReadReturnsWhatPutStoredAndRefusesOtherNames reads back the bytes put stored, and checks that read
// refuses a name path refuses.
func TestEvidenceBlobReadReturnsWhatPutStoredAndRefusesOtherNames(t *testing.T) {
	t.Parallel()
	blobs := newTestBlobs(t)
	data := []byte("stored")
	_, name, err := blobs.put(data, ".log")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := blobs.read(name)
	if err != nil {
		t.Fatalf("read(%q): %v", name, err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("read(%q) = %q, want %q", name, got, data)
	}
	if _, err = blobs.read("../x"); err == nil {
		t.Fatal("read(\"../x\") succeeded, want a refusal")
	}
}
