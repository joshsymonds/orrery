package enginehost

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceMaxKeptBytes is the largest supplied file kept as a blob: 10 MiB. A larger file is over_cap, its size
// recorded and its bytes not stored.
const evidenceMaxKeptBytes = 10 << 20

// The states an artifact of a supplied file can take, as the store records them.
const (
	artifactKept    = "kept"
	artifactMissing = "missing"
	artifactOverCap = "over_cap"
)

// readSupplied keeps the files a supplied evidence item names, as the commit that revision resolves to holds them.
// Each file is read from the commit's objects, never from the working tree. A path the commit does not hold as a blob
// is missing. A blob over evidenceMaxKeptBytes is over_cap with its size. Any other blob's bytes go to blobs under
// their sha256. The capture exits 0 only when every file is kept. A revision that does not resolve, or a read that
// fails, ends the capture as not captured. Nothing is written to the repository.
func readSupplied(
	ctx context.Context, repo, revision string, files []string, blobs evidenceBlobs,
) store.EvidenceCapture {
	commit, err := stepGitResolve(ctx, repo, revision)
	if err != nil {
		return store.EvidenceCapture{Exit: 1, NotCaptured: "could not start: " + err.Error(), At: time.Now()}
	}
	var capture store.EvidenceCapture
	for _, file := range files {
		artifact, keepErr := keepSupplied(ctx, repo, commit, file, blobs)
		if keepErr != nil {
			return store.EvidenceCapture{
				Exit: 1, NotCaptured: fmt.Sprintf("could not keep %s: %v", file, keepErr), At: time.Now(),
			}
		}
		if artifact.State != artifactKept {
			capture.Exit = 1
		}
		capture.Artifacts = append(capture.Artifacts, artifact)
	}
	capture.At = time.Now()
	return capture
}

// keepSupplied reads file from commit and returns the artifact it leaves. A path the commit does not hold as a blob is
// missing. An error is returned when the context is done, a git read fails, or the blob store refuses the bytes.
func keepSupplied(
	ctx context.Context, repo, commit, file string, blobs evidenceBlobs,
) (store.EvidenceArtifact, error) {
	object := commit + ":" + file
	kind, err := stepGit(ctx, repo, "cat-file", "-t", object)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return store.EvidenceArtifact{}, fmt.Errorf("read %s: %w", object, ctxErr)
	}
	// git fails on a path the commit does not hold, so a failed type read means missing as well.
	if err != nil {
		kind = ""
	}
	if kind != "blob" {
		return store.EvidenceArtifact{Path: file, State: artifactMissing}, nil
	}
	sizeText, err := stepGit(ctx, repo, "cat-file", "-s", object)
	if err != nil {
		return store.EvidenceArtifact{}, err
	}
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil {
		return store.EvidenceArtifact{}, fmt.Errorf("size of %s: %w", object, err)
	}
	if size > evidenceMaxKeptBytes {
		return store.EvidenceArtifact{Path: file, State: artifactOverCap, Size: size}, nil
	}
	data, err := suppliedBlob(ctx, repo, object)
	if err != nil {
		return store.EvidenceArtifact{}, err
	}
	hash, name, err := blobs.put(data, evidenceExt(file))
	if err != nil {
		return store.EvidenceArtifact{}, err
	}
	return store.EvidenceArtifact{Path: file, State: artifactKept, Hash: hash, Blob: name, Size: int64(len(data))}, nil
}

// suppliedBlob returns the bytes of object, a <commit>:<path> spec of a blob. It runs git itself because stepGit trims
// its output, which would drop a file's trailing newline.
func suppliedBlob(ctx context.Context, repo, object string) ([]byte, error) {
	// Like stepGit, the argument vector is built here, so git runs with argv and no shell.
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "cat-file", "blob", object)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file blob %s: %w: %s", object, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
