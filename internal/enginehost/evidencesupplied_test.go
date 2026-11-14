package enginehost

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// suppliedTestCap is the largest supplied file kept as a blob: 10 MiB.
const suppliedTestCap = 10 << 20

// suppliedTestSnapshot returns the HEAD id, the status and the index bytes of the repository at dir. The status runs
// with no optional locks, so reading it does not refresh the index.
func suppliedTestSnapshot(t *testing.T, dir string) (head, status string, index []byte) {
	t.Helper()
	ctx := context.Background()
	head, err := stepGitResolve(ctx, dir, "HEAD")
	if err != nil {
		t.Fatalf("resolve HEAD: %v", err)
	}
	status, err = stepGit(ctx, dir, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	index, err = os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	return head, status, index
}

// TestReadSuppliedKeepsTheCommittedBytes commits a file, then edits it in the working tree and adds an uncommitted
// file of another name. The committed file is kept with the committed bytes, and the uncommitted file is missing.
func TestReadSuppliedKeepsTheCommittedBytes(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	commit := stepGitTestCommit(t, repo, map[string]string{"evidence/report.txt": "committed\n"})
	stepGitTestWrite(t, repo, "evidence/report.txt", "edited in the working tree\n")
	stepGitTestWrite(t, repo, "evidence/untracked.txt", "never committed\n")
	blobs := newTestBlobs(t)

	capture := readSupplied(
		context.Background(), repo, commit, []string{"evidence/report.txt", "evidence/untracked.txt"}, blobs,
	)

	if capture.NotCaptured != "" {
		t.Fatalf("NotCaptured = %q, want none", capture.NotCaptured)
	}
	if len(capture.Artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2: %+v", len(capture.Artifacts), capture.Artifacts)
	}
	const committed = "committed\n"
	hash := blobHashOf([]byte(committed))
	wantKept := store.EvidenceArtifact{
		Path: "evidence/report.txt", State: "kept", Hash: hash, Blob: hash + ".txt", Size: int64(len(committed)),
	}
	if got := capture.Artifacts[0]; got != wantKept {
		t.Fatalf("kept artifact = %+v, want %+v", got, wantKept)
	}
	data, err := blobs.read(hash + ".txt")
	if err != nil {
		t.Fatalf("read kept blob: %v", err)
	}
	if string(data) != committed {
		t.Fatalf("kept blob = %q, want the committed bytes %q", data, committed)
	}
	wantMissing := store.EvidenceArtifact{Path: "evidence/untracked.txt", State: "missing"}
	if got := capture.Artifacts[1]; got != wantMissing {
		t.Fatalf("missing artifact = %+v, want %+v", got, wantMissing)
	}
	if capture.Exit != 1 {
		t.Fatalf("Exit = %d with a missing file, want 1", capture.Exit)
	}
}

// TestReadSuppliedExitsZeroOnlyWhenEveryFileIsKept reads two committed files, which exits 0, then reads one of them
// with a name the commit does not hold, which exits 1.
func TestReadSuppliedExitsZeroOnlyWhenEveryFileIsKept(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	commit := stepGitTestCommit(t, repo, map[string]string{"a.txt": "a\n", "b/c.txt": "c\n"})
	blobs := newTestBlobs(t)
	ctx := context.Background()

	all := readSupplied(ctx, repo, commit, []string{"a.txt", "b/c.txt"}, blobs)
	if all.Exit != 0 {
		t.Fatalf("Exit = %d with every file kept, want 0: %+v", all.Exit, all.Artifacts)
	}
	for _, a := range all.Artifacts {
		if a.State != "kept" {
			t.Fatalf("artifact %s is %s, want kept", a.Path, a.State)
		}
	}

	some := readSupplied(ctx, repo, commit, []string{"a.txt", "absent.txt"}, blobs)
	if some.Exit != 1 {
		t.Fatalf("Exit = %d with a missing file, want 1", some.Exit)
	}
}

// TestReadSuppliedOverTenMiBIsOverCapAndNotStored commits one file of exactly 10 MiB and one a byte larger. The first
// is kept. The second is over_cap with its size, and only the first has a blob stored.
func TestReadSuppliedOverTenMiBIsOverCapAndNotStored(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	commit := stepGitTestCommit(t, repo, map[string]string{
		"big/exact.bin": strings.Repeat("e", suppliedTestCap),
		"big/over.bin":  strings.Repeat("o", suppliedTestCap+1),
	})
	blobs := newTestBlobs(t)

	capture := readSupplied(context.Background(), repo, commit, []string{"big/exact.bin", "big/over.bin"}, blobs)

	if len(capture.Artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2: %+v", len(capture.Artifacts), capture.Artifacts)
	}
	if got := capture.Artifacts[0]; got.State != "kept" || got.Size != suppliedTestCap {
		t.Fatalf("exact-cap artifact = %+v, want kept with size %d", got, suppliedTestCap)
	}
	wantOver := store.EvidenceArtifact{Path: "big/over.bin", State: "over_cap", Size: suppliedTestCap + 1}
	if got := capture.Artifacts[1]; got != wantOver {
		t.Fatalf("over-cap artifact = %+v, want %+v", got, wantOver)
	}
	entries, err := os.ReadDir(blobs.dir)
	if err != nil {
		t.Fatalf("read blob directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("blob directory holds %d entries, want 1: the kept file only", len(entries))
	}
	if capture.Exit != 1 {
		t.Fatalf("Exit = %d with an over_cap file, want 1", capture.Exit)
	}
}

// TestReadSuppliedDirectoryAndAbsentPathsAreMissing names a directory of the commit and a path the commit does not
// hold. Both are missing, and the capture exits 1.
func TestReadSuppliedDirectoryAndAbsentPathsAreMissing(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	commit := stepGitTestCommit(t, repo, map[string]string{"dir/a.txt": "a\n"})

	capture := readSupplied(context.Background(), repo, commit, []string{"dir", "absent.txt"}, newTestBlobs(t))

	want := []store.EvidenceArtifact{{Path: "dir", State: "missing"}, {Path: "absent.txt", State: "missing"}}
	if !slices.Equal(capture.Artifacts, want) {
		t.Fatalf("artifacts = %+v, want %+v", capture.Artifacts, want)
	}
	if capture.Exit != 1 {
		t.Fatalf("Exit = %d with missing paths, want 1", capture.Exit)
	}
}

// TestReadSuppliedLeavesTheRepositoryAlone reads over a repository with a committed file edited in the working tree and
// an untracked file. HEAD, the status and the index are the same afterwards.
func TestReadSuppliedLeavesTheRepositoryAlone(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	commit := stepGitTestCommit(t, repo, map[string]string{"evidence/report.txt": "committed\n"})
	stepGitTestWrite(t, repo, "evidence/report.txt", "edited\n")
	stepGitTestWrite(t, repo, "evidence/untracked.txt", "new\n")
	headBefore, statusBefore, indexBefore := suppliedTestSnapshot(t, repo)
	if statusBefore == "" {
		t.Fatal("status is empty before the read, so the comparison would prove nothing")
	}

	capture := readSupplied(
		context.Background(), repo, commit, []string{"evidence/report.txt", "evidence/untracked.txt"}, newTestBlobs(t),
	)
	if capture.Exit != 1 {
		t.Fatalf("Exit = %d, want 1 so the read ran to its end", capture.Exit)
	}

	head, status, index := suppliedTestSnapshot(t, repo)
	if head != headBefore {
		t.Fatalf("HEAD moved from %s to %s", headBefore, head)
	}
	if status != statusBefore {
		t.Fatalf("status changed from %q to %q", statusBefore, status)
	}
	if !bytes.Equal(index, indexBefore) {
		t.Fatal("index bytes changed")
	}
}

// TestReadSuppliedRevisionThatDoesNotResolveIsNotCaptured names revisions the repository does not resolve, one of them
// a dash-led word that must not be read as a git option. Each is not captured with the reason, keeps nothing, and
// exits 1.
func TestReadSuppliedRevisionThatDoesNotResolveIsNotCaptured(t *testing.T) {
	t.Parallel()
	for _, revision := range []string{"no-such-revision", "--all"} {
		t.Run(revision, func(t *testing.T) {
			t.Parallel()
			repo := stepGitTestRepo(t)
			blobs := newTestBlobs(t)

			capture := readSupplied(context.Background(), repo, revision, []string{"README"}, blobs)

			if !strings.HasPrefix(capture.NotCaptured, "could not start: ") {
				t.Fatalf("NotCaptured = %q, want a could not start reason", capture.NotCaptured)
			}
			if len(capture.Artifacts) != 0 {
				t.Fatalf("artifacts = %+v, want none", capture.Artifacts)
			}
			if capture.Exit != 1 {
				t.Fatalf("Exit = %d, want 1", capture.Exit)
			}
			if _, err := os.Stat(blobs.dir); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("blob directory stat = %v, want it absent: nothing is stored", err)
			}
		})
	}
}
