package releasestep

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// BuildCommitFile is where a build of orrery records the commit it was built from, under the build's output: one line,
// the full commit id, or an empty file for a build from a tree with no commit. flake.nix writes it, and an update step
// reads it two directories above a unit's exe, so a running binary names the commit it holds.
const BuildCommitFile = "share/orrery/commit"

// commitPattern is the form of a full commit id, SHA-1 or SHA-256.
var commitPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// The unit properties an update step reads, as systemctl show prints them.
const (
	propActiveState = "ActiveState"
	propMainPID     = "MainPID"
	stateActive     = "active"
)

// update is an update step: it brings the flake repository's checkout up to its origin branch and runs the command
// that switches the machine to it.
type update struct {
	s         *step
	candidate string
	branch    string
	flake     string
	units     []string
	argv      []string
}

// check reads each unit. The postcondition holds when every unit is active and its main process runs a build whose
// commit contains the candidate: the candidate is an ancestor of it or is it. The facts name each unit's state, exe
// and build commit.
func (u *update) check(ctx context.Context) (verdict, error) {
	held := verdict{held: true}
	var whys []string
	for _, unit := range u.units {
		fact, why, err := u.checkUnit(ctx, unit)
		if err != nil {
			return verdict{}, err
		}
		held.facts = append(held.facts, fact)
		if why != "" {
			whys = append(whys, why)
		}
	}
	if len(whys) > 0 {
		return verdict{facts: held.facts, why: strings.Join(whys, "; ")}, nil
	}
	return held, nil
}

// checkUnit reads unit with systemctl, the exe of its main process, and the commit file of that exe's build, and
// returns the fact that names them, and why the unit does not meet the postcondition, or "" when it does. A build
// commit the repository does not hold is fetched from origin first.
func (u *update) checkUnit(ctx context.Context, unit string) (fact, why string, err error) {
	var out bytes.Buffer
	argv := []string{"systemctl", "--user", "show", "--property=" + propActiveState, "--property=" + propMainPID, unit}
	if err = u.s.machine.Run(ctx, u.s.repo, argv, &out, u.s.stderr); err != nil {
		return "", "", fmt.Errorf("read unit %s: %w", unit, err)
	}
	props := unitProperties(out.String())
	state := props[propActiveState]
	pid := mainPID(props[propMainPID])
	if state != stateActive || pid <= 0 {
		fact = fmt.Sprintf("unit %s is %s with main pid %s", unit, state, props[propMainPID])
		return fact, fmt.Sprintf("unit %s is not active with a main process", unit), nil
	}
	exe, err := u.s.machine.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		fact = fmt.Sprintf("unit %s is active, pid %d, exe unreadable: %v", unit, pid, err)
		return fact, fmt.Sprintf("the exe of unit %s cannot be read", unit), nil
	}
	fact = fmt.Sprintf("unit %s is active, pid %d, exe %s", unit, pid, exe)
	commit, why := u.buildCommit(exe)
	if why != "" {
		return fact, fmt.Sprintf("unit %s: %s", unit, why), nil
	}
	fact += ", built from " + commit
	why, err = u.contains(ctx, commit)
	if err != nil || why != "" {
		return fact, fmt.Sprintf("unit %s: %s", unit, why), err
	}
	return fact + ", which contains candidate " + u.candidate, "", nil
}

// buildCommit returns the commit the build of exe records in its commit file, two directories above exe, and why it
// names none, or "" when it does.
func (u *update) buildCommit(exe string) (commit, why string) {
	path := filepath.Join(filepath.Dir(filepath.Dir(exe)), BuildCommitFile)
	data, err := u.s.machine.ReadFile(path)
	if err != nil {
		return "", fmt.Sprintf("its build records no commit: %v", err)
	}
	commit = strings.TrimSpace(string(data))
	if !commitPattern.MatchString(commit) {
		return "", fmt.Sprintf("%s holds %q, not a full commit id", path, commit)
	}
	return commit, ""
}

// contains returns why commit does not contain the candidate, or "" when it does. A commit the repository does not
// hold is fetched from origin first; one origin does not give is not the candidate's.
func (u *update) contains(ctx context.Context, commit string) (string, error) {
	held, err := u.s.hasCommit(ctx, u.s.repo, commit)
	if err != nil {
		return "", err
	}
	if !held {
		_, fetchErr := u.s.remoteOut(ctx, u.s.repo, "fetch", "--no-tags", "--no-write-fetch-head", Remote, commit)
		if fetchErr != nil {
			return fmt.Sprintf("build commit %s is not in the repository or on %s: %v", commit, Remote, fetchErr), nil
		}
		if held, err = u.s.hasCommit(ctx, u.s.repo, commit); err != nil || !held {
			return fmt.Sprintf("build commit %s is not a commit %s gives", commit, Remote), err
		}
	}
	contains, err := u.s.isAncestor(ctx, u.s.repo, u.candidate, commit)
	if err != nil {
		return "", err
	}
	if !contains {
		return fmt.Sprintf("build commit %s does not contain candidate %s", commit, u.candidate), nil
	}
	return "", nil
}

// mainPID returns the pid a unit's MainPID property names, or 0 when it names none.
func mainPID(value string) int {
	pid, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return pid
}

// unitProperties reads the KEY=VALUE lines systemctl show prints.
func unitProperties(out string) map[string]string {
	props := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		if key, value, found := strings.Cut(strings.TrimSpace(line), "="); found {
			props[key] = value
		}
	}
	return props
}

// act brings the flake repository's checkout up to its origin branch without discarding anything, and then runs the
// step's argv in it, with its output on stderr. The checkout must be on the branch. One that already holds origin's
// branch is left as it is, one behind it is fast-forwarded, which git refuses when a changed file is in the way, and
// one that has diverged from it fails the step.
func (u *update) act(ctx context.Context) error {
	ref, err := u.s.gitOut(ctx, u.flake, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || ref != "refs/heads/"+u.branch {
		return fmt.Errorf("the checkout of %s is not on branch %s", u.flake, u.branch)
	}
	origin, err := u.s.fetchBranch(ctx, u.flake, u.branch)
	if err != nil {
		return err
	}
	head, err := u.s.gitOut(ctx, u.flake, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if upErr := u.bringUp(ctx, head, origin); upErr != nil {
		return upErr
	}
	if err = u.s.machine.Run(ctx, u.flake, u.argv, u.s.stderr, u.s.stderr); err != nil {
		return fmt.Errorf("run in %s: %w", u.flake, err)
	}
	u.s.say(fmt.Sprintf("ran %s in %s", strings.Join(u.argv, " "), u.flake))
	return nil
}

// bringUp moves the checkout's branch, at head, up to origin's branch, at origin, when it does not already hold it.
func (u *update) bringUp(ctx context.Context, head, origin string) error {
	holds, err := u.s.isAncestor(ctx, u.flake, origin, head)
	if err != nil {
		return err
	}
	if holds {
		u.s.say(fmt.Sprintf("%s %s is %s, which holds %s %s at %s", u.flake, u.branch, head, Remote, u.branch, origin))
		return nil
	}
	behind, err := u.s.isAncestor(ctx, u.flake, head, origin)
	if err != nil {
		return err
	}
	if !behind {
		return fmt.Errorf("%s %s at %s has diverged from %s %s at %s: the checkout must be rebased onto %s %s first",
			u.flake, u.branch, head, Remote, u.branch, origin, Remote, u.branch)
	}
	if _, err = u.s.gitOut(ctx, u.flake, "merge", "--ff-only", trackingRef(u.branch)); err != nil {
		return fmt.Errorf("fast-forward %s %s to %s: %w", u.flake, u.branch, origin, err)
	}
	u.s.say(fmt.Sprintf("fast-forwarded %s %s from %s to %s", u.flake, u.branch, head, origin))
	return nil
}
