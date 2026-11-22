package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceCaptureLogTail is the number of newest output bytes a capture keeps: 1 MiB.
const evidenceCaptureLogTail = 1 << 20

// evidenceCaptureArtifactCap is the largest artifact a capture keeps, in bytes. A larger one is noted as over the cap and not
// stored.
const evidenceCaptureArtifactCap = 10 << 20

// evidenceCaptureKilled is the reason a capture that ran past its limit captured nothing, as the store records it.
const evidenceCaptureKilled = "killed at its limit"

// evidenceCapture runs an evidence item's capture at a revision in a detached worktree, under the holder and the pool
// grant its gate queues for, and keeps what the capture leaves. worktrees is the directory the worktrees are added under,
// one per run id. gateStateDir is the state directory the holder directories live under, gateScope the systemd-run
// scope the holders run in when it is set. blobs keeps the bytes.
type evidenceCapture struct {
	pools        PoolCommands
	environ      []string
	worktrees    string
	gateStateDir string
	gateScope    string
	blobs        evidenceBlobs
	// host is the Host the capture's git, worktrees and artifact reads run on. It is set with on, and is the local host
	// when it is not set (see hostOf).
	host hostexec.Host
	// logTail is the number of newest output bytes a capture keeps, and the size at which a log read back counts as
	// cut, in place of evidenceCaptureLogTail when it is not zero. Tests set it to keep their logs small.
	logTail int
}

// tail returns the number of newest output bytes a capture keeps: logTail, or evidenceCaptureLogTail when it is zero.
func (c evidenceCapture) tail() int {
	if c.logTail != 0 {
		return c.logTail
	}
	return evidenceCaptureLogTail
}

// on returns the capture that runs its git, worktrees and artifact reads on host.
func (c evidenceCapture) on(host hostexec.Host) evidenceCapture {
	c.host = host
	return c
}

// hostOf returns the Host the capture runs on: its host, or the local host when none is set, as a capture built outside
// a runner runs.
func (c evidenceCapture) hostOf() hostexec.Host {
	if c.host == nil {
		return hostexec.NewLocal()
	}
	return c.host
}

// run captures item at revision of repo as run id. It adds a detached worktree, runs the capture under a grant of the
// default gate pool, and keeps what it leaves. started, when not nil, is called once the grant holds, before the gate
// launches. A capture that did not run to an outcome says why in NotCaptured. When the gate detaches, run returns the zero
// capture and an error matching errGateDetach: the gate runs on under its holder, the worktree stays for readopt, and the
// caller records nothing.
func (c evidenceCapture) run(
	ctx context.Context,
	repo, revision, id string,
	item evidence.Item,
	started func(ctx context.Context, grant, holder string) error,
) (store.EvidenceCapture, error) {
	worktree := filepath.Join(c.worktrees, id)
	if err := addStepWorktree(ctx, c.hostOf(), repo, worktree, revision); err != nil {
		return c.end(ctx, repo, worktree, item, nil, captureNotStarted(err)), nil
	}
	req, err := c.captureRequest(ctx, worktree, id, item)
	if err != nil {
		return c.end(ctx, repo, worktree, item, nil, captureNotStarted(err)), nil
	}
	// launched is set once the grant holds and started returns nil. From then the gate may run, so a gate that ends with its
	// context did not finish, where one that never launched did not start.
	launched := false
	gate := c.captureGate(worktree, id, item, func(gateCtx context.Context, grant, holder string) error {
		if started != nil {
			if startErr := started(gateCtx, grant, holder); startErr != nil {
				return startErr
			}
		}
		launched = true
		return nil
	})
	result, err := runGrantedGate(ctx, c.pools, req, gate)
	if errors.Is(err, errGateDetach) {
		// The gate keeps running under its holder in worktree, so neither is removed: readopt re-adopts the gate.
		return store.EvidenceCapture{}, err
	}
	if err != nil {
		reason := captureNotStarted(err)
		if launched && ctx.Err() != nil {
			reason = captureNotFinished(err)
		}
		return c.end(ctx, repo, worktree, item, nil, reason), nil
	}
	return c.end(ctx, repo, worktree, item, &result, ""), nil
}

// readopt keeps the capture a restart left running as run id under grant. It re-adds the worktree when a restart removed
// it, re-adopts the gate, and keeps what the capture left. A gate that cannot be re-adopted is lost, and its holder is
// ended. As run does, it returns the zero capture and an error matching errGateDetach when the gate detaches again.
func (c evidenceCapture) readopt(
	ctx context.Context,
	repo, revision, id string,
	item evidence.Item,
	grant string,
) (store.EvidenceCapture, error) {
	worktree := filepath.Join(c.worktrees, id)
	req, err := c.readoptRequest(ctx, repo, revision, worktree, id, item)
	var result gateResult
	if err == nil {
		result, err = readoptGrantedGate(ctx, c.pools, req, c.captureGate(worktree, id, item, nil), grant)
	}
	switch {
	case errors.Is(err, errGateDetach):
		// The gate keeps running under its holder in worktree, so neither is removed: the next readopt re-adopts it.
		return store.EvidenceCapture{}, err
	case err == nil:
		return c.end(ctx, repo, worktree, item, &result, ""), nil
	}
	return c.readoptLost(ctx, repo, worktree, item, id), nil
}

// readoptRequest re-adds run id's worktree when a restart removed it, and returns the grant request its capture queues in it.
func (c evidenceCapture) readoptRequest(
	ctx context.Context, repo, revision, worktree, id string, item evidence.Item,
) (grants.Request, error) {
	if err := captureEnsureWorktree(ctx, c.hostOf(), repo, revision, worktree); err != nil {
		return grants.Request{}, err
	}
	return c.captureRequest(ctx, worktree, id, item)
}

// readoptLost ends the capture of a gate that readopt cannot re-adopt, so the gate is lost at a restart. Nothing reads its
// holder again, so the holder is stopped and cleared before the worktree is removed. A failure to do so is kept as not
// finished.
func (c evidenceCapture) readoptLost(
	ctx context.Context, repo, worktree string, item evidence.Item, id string,
) store.EvidenceCapture {
	plan, err := planGate(c.captureGate(worktree, id, item, nil))
	if err == nil {
		err = gateClear(ctx, c.hostOf(), plan.dir)
	}
	return captureFinished(c.end(ctx, repo, worktree, item, nil, store.LostAtRestart), err)
}

// captureRequest returns the grant request run id's capture queues in worktree: one member of the default gate pool.
func (c evidenceCapture) captureRequest(
	ctx context.Context, worktree, id string, item evidence.Item,
) (grants.Request, error) {
	needs := []grants.Need{{Pool: gates.DefaultPool, Count: 1}}
	return stepGrant(ctx, worktree, "evidence-"+id, item.Capture, needs, stepGetenv(c.environ))
}

// captureGate returns the gate that runs item's capture in worktree as run id. Its stdout and stderr tails are 1 MiB
// each, and its limit is the item's limit in seconds.
func (c evidenceCapture) captureGate(
	worktree, id string,
	item evidence.Item,
	started func(ctx context.Context, grant, holder string) error,
) gateRun {
	return gateRun{
		Argv:     item.Capture,
		Dir:      worktree,
		Limit:    time.Duration(item.Limit) * time.Second,
		Environ:  c.environ,
		Host:     c.hostOf(),
		Name:     "evidence-" + id,
		StateDir: c.gateStateDir,
		Scope:    c.gateScope,
		Started:  started,
		Tail:     c.tail(),
	}
}

// end keeps what a gate that ran left, then removes the worktree and clears the gate's holder directory, on every path.
// result is the gate's result, or nil when no gate ran to one; reason is why the capture captured nothing when no gate
// ran to one. A failure to keep or clean up is added to NotCaptured, so it is never silent.
func (c evidenceCapture) end(
	ctx context.Context,
	repo, worktree string,
	item evidence.Item,
	result *gateResult,
	reason string,
) store.EvidenceCapture {
	capture := store.EvidenceCapture{NotCaptured: reason}
	holder := ""
	var keepErr error
	if result != nil {
		holder = result.Holder
		capture.Exit = result.Exit
		capture.TimedOut = result.TimedOut
		switch {
		case result.TimedOut:
			capture.NotCaptured = evidenceCaptureKilled
		case result.Exit == store.EnvironmentExit:
			capture.NotCaptured = store.NotCapturedEnvironment
		}
		var logErr, stderrErr, artifactErr error
		capture.LogHash, capture.LogBytes, logErr = c.keepLog(result.Stdout)
		capture.StderrHash, capture.StderrBytes, stderrErr = c.keepLog(result.Stderr)
		capture.Artifacts, artifactErr = c.keepArtifacts(ctx, worktree, item.Artifacts)
		keepErr = errors.Join(logErr, stderrErr, artifactErr)
	}
	cleanErr := errors.Join(
		removeStepWorktree(context.WithoutCancel(ctx), c.hostOf(), repo, worktree),
		gateClear(ctx, c.hostOf(), holder),
	)
	capture.At = time.Now()
	return captureFinished(capture, errors.Join(keepErr, cleanErr))
}

// keepLog stores one of the capture's output tails as a log blob and returns its hash and size. An empty tail stores no
// blob.
func (c evidenceCapture) keepLog(tail string) (string, int64, error) {
	if tail == "" {
		return "", 0, nil
	}
	hash, _, err := c.blobs.put([]byte(tail), ".log")
	if err != nil {
		return "", 0, err
	}
	return hash, int64(len(tail)), nil
}

// keepArtifacts returns what each path left in worktree, in the order paths gives, as keepArtifact keeps each one. The
// worktree is resolved once, so each path can be checked to lie inside it.
func (c evidenceCapture) keepArtifacts(
	ctx context.Context, worktree string, paths []string,
) ([]store.EvidenceArtifact, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	root, err := c.hostOf().EvalSymlinks(ctx, worktree)
	if err != nil {
		return nil, fmt.Errorf("open worktree %s: %w", worktree, err)
	}
	artifacts := make([]store.EvidenceArtifact, 0, len(paths))
	for _, path := range paths {
		artifact, keepErr := c.keepArtifact(ctx, worktree, root, path)
		if keepErr != nil {
			return nil, keepErr
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

// keepArtifact returns what path left in worktree, whose resolved path is root. A regular file up to the cap is kept as a
// blob, and a larger one is over_cap and not stored. A path that artifactFile refuses is missing, and so is one that is
// swapped for a symbolic link between its stat and its read. The read goes through the host's ReadRooted, so it stays
// inside worktree.
func (c evidenceCapture) keepArtifact(
	ctx context.Context, worktree, root, path string,
) (store.EvidenceArtifact, error) {
	host := c.hostOf()
	info, ok := artifactFile(ctx, host, worktree, root, path)
	if !ok {
		return store.EvidenceArtifact{Path: path, State: artifactMissing}, nil
	}
	// A file over the cap is refused on its size before it is read, so a large one is never held in memory.
	if info.Size() > evidenceCaptureArtifactCap {
		return store.EvidenceArtifact{Path: path, State: artifactOverCap, Size: info.Size()}, nil
	}
	data, err := host.ReadRooted(ctx, worktree, path)
	switch {
	case errors.Is(err, hostexec.ErrNotRegular):
		return store.EvidenceArtifact{Path: path, State: artifactMissing}, nil
	case err != nil:
		return store.EvidenceArtifact{}, fmt.Errorf("read artifact %s: %w", path, err)
	}
	// The file may have grown since it was stat'ed, so its read is checked against the cap as well.
	if int64(len(data)) > evidenceCaptureArtifactCap {
		return store.EvidenceArtifact{Path: path, State: artifactOverCap, Size: int64(len(data))}, nil
	}
	hash, name, err := c.blobs.put(data, evidenceExt(path))
	if err != nil {
		return store.EvidenceArtifact{}, err
	}
	return store.EvidenceArtifact{Path: path, State: artifactKept, Hash: hash, Blob: name, Size: int64(len(data))}, nil
}

// artifactFile returns the stat of path in worktree and true when path is a regular file inside worktree, whose resolved
// path is under root. A symbolic link at path is not followed, so it is not a regular file. A path that is not local to
// worktree, and one that cannot be stat'ed or resolved, is not a regular file inside it either. A directory on the way to
// path may be a symbolic link out of worktree, which the read would refuse, so the resolved path is checked too.
func artifactFile(ctx context.Context, host hostexec.Host, worktree, root, path string) (fs.FileInfo, bool) {
	if !filepath.IsLocal(path) {
		return nil, false
	}
	full := filepath.Join(worktree, path)
	info, err := host.Lstat(ctx, full)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	resolved, err := host.EvalSymlinks(ctx, full)
	if err != nil || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return nil, false
	}
	return info, true
}

// captureEnsureWorktree adds the worktree at path on host, at revision of repo, when a restart removed it. A directory
// removed outside git leaves git's record of the worktree, which is dropped first so the path can be added again.
func captureEnsureWorktree(ctx context.Context, host hostexec.Host, repo, revision, path string) error {
	_, err := host.Lstat(ctx, path)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		if rmErr := removeStepWorktree(ctx, host, repo, path); rmErr != nil {
			return rmErr
		}
		return addStepWorktree(ctx, host, repo, path, revision)
	}
	return fmt.Errorf("stat worktree %s: %w", path, err)
}

// captureNotStarted is the reason a capture captured nothing because its worktree, grant or gate did not start.
func captureNotStarted(err error) string {
	return "could not start: " + err.Error()
}

// captureNotFinished is the reason a capture captured nothing because its gate did not finish: what it left could not be
// kept, its worktree or holder could not be cleared, or its context ended while it ran.
func captureNotFinished(err error) string {
	return "could not finish: " + err.Error()
}

// captureFinished adds to capture's reason that its gate did not finish, when err is not nil. It fails closed: a capture
// with a reason is not captured, so the run records no passing verdict.
func captureFinished(capture store.EvidenceCapture, err error) store.EvidenceCapture {
	if err == nil {
		return capture
	}
	finished := captureNotFinished(err)
	if capture.NotCaptured == "" {
		capture.NotCaptured = finished
	} else {
		capture.NotCaptured += "; " + finished
	}
	return capture
}
