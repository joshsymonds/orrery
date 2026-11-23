package enginehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepCheck is one verdict on a step's done return.
type stepCheck struct {
	Name   string
	OK     bool
	Detail string
}

// stepGitPathLimit caps the paths a failed check names in its Detail.
const stepGitPathLimit = 20

// The names of the four checks, in the order commitChecks returns them.
const (
	stepGitHeadName     = "head"
	stepGitAncestryName = "ancestry"
	stepGitCleanName    = "clean"
	stepGitOwnedName    = "owned"
)

// stepGitCheckoutName is the name of the check a read-only step's return stores when its checkout moved.
const stepGitCheckoutName = "checkout"

// stepCheckout is the state of a read-only step's checkout at one moment: its HEAD, and a sha256 over git status and
// git diff HEAD. Ignored files are in neither, so they never change it. The zero value is no recorded state.
type stepCheckout struct {
	head string
	hash string
}

// stepGit runs git in dir on host and returns its trimmed stdout. On failure the error is the host's, which carries
// git's stderr. Like proc and the gate runner, it builds the argument vector itself: git runs with argv and no shell.
func stepGit(ctx context.Context, host hostexec.Host, dir string, args ...string) (string, error) {
	out, err := host.Git(ctx, hostexec.GitCall{Dir: dir, Args: args})
	if err != nil {
		return "", fmt.Errorf("git in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// stepGitResolve returns the full commit id rev names on host. The end-of-options marker keeps a rev that starts with
// a dash from being read as a git option.
func stepGitResolve(ctx context.Context, host hostexec.Host, dir, rev string) (string, error) {
	return stepGit(ctx, host, dir, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
}

// commitChecks returns the four checks on a done return, in order: head, ancestry, clean, owned. Each check only
// reads the repository on host.
func commitChecks(
	ctx context.Context, host hostexec.Host, dir, start, commit string, owned []string,
) []stepCheck {
	return append(
		commitChecksWithoutOwned(ctx, host, dir, start, commit),
		stepGitOwnedCheck(ctx, host, dir, start, commit, owned),
	)
}

// commitChecksWithoutOwned returns the head, ancestry and clean checks on a done return, in order. An unowned writing
// step's done return runs these alone, since its owned check is skipped.
func commitChecksWithoutOwned(ctx context.Context, host hostexec.Host, dir, start, commit string) []stepCheck {
	return []stepCheck{
		stepGitHeadCheck(ctx, host, dir, commit),
		stepGitAncestryCheck(ctx, host, dir, start),
		stepGitCleanCheck(ctx, host, dir),
	}
}

// stepGitHeadCheck passes when HEAD is the commit that commit resolves to.
func stepGitHeadCheck(ctx context.Context, host hostexec.Host, dir, commit string) stepCheck {
	want, err := stepGitResolve(ctx, host, dir, commit)
	if err != nil {
		return stepCheck{Name: stepGitHeadName, Detail: fmt.Sprintf("commit %q does not resolve: %v", commit, err)}
	}
	got, err := stepGitResolve(ctx, host, dir, "HEAD")
	if err != nil {
		return stepCheck{Name: stepGitHeadName, Detail: fmt.Sprintf("HEAD does not resolve: %v", err)}
	}
	if got != want {
		return stepCheck{Name: stepGitHeadName, Detail: fmt.Sprintf("HEAD is %s, want %s", got, want)}
	}
	return stepCheck{Name: stepGitHeadName, OK: true, Detail: "HEAD is " + got}
}

// stepGitAncestryCheck passes when start is an ancestor of HEAD.
func stepGitAncestryCheck(ctx context.Context, host hostexec.Host, dir, start string) stepCheck {
	base, err := stepGitResolve(ctx, host, dir, start)
	if err != nil {
		return stepCheck{Name: stepGitAncestryName, Detail: fmt.Sprintf("start %q does not resolve: %v", start, err)}
	}
	if _, err = stepGit(ctx, host, dir, "merge-base", "--is-ancestor", base, "HEAD"); err != nil {
		detail := fmt.Sprintf("start %s is not an ancestor of HEAD: %v", base, err)
		return stepCheck{Name: stepGitAncestryName, Detail: detail}
	}
	return stepCheck{Name: stepGitAncestryName, OK: true, Detail: "start " + base + " is an ancestor of HEAD"}
}

// stepGitCleanCheck passes when no tracked change, staged or unstaged, and no untracked non-ignored file exists. It takes
// no optional lock, so git does not refresh the index of the step's worktree.
func stepGitCleanCheck(ctx context.Context, host hostexec.Host, dir string) stepCheck {
	out, err := stepGit(
		ctx,
		host,
		dir,
		"--no-optional-locks",
		"status",
		"--porcelain=v1",
		"-z",
		"--untracked-files=all",
		"--no-renames",
	)
	if err != nil {
		return stepCheck{Name: stepGitCleanName, Detail: fmt.Sprintf("status: %v", err)}
	}
	// stepGit trims the whole output, which can eat the leading space of the first record's status column, so each
	// record is kept whole and trimmed rather than sliced at a fixed offset.
	var dirty []string
	for _, rec := range strings.Split(out, "\x00") {
		if rec = strings.TrimSpace(rec); rec != "" {
			dirty = append(dirty, rec)
		}
	}
	if len(dirty) == 0 {
		return stepCheck{Name: stepGitCleanName, OK: true, Detail: "working tree is clean"}
	}
	return stepCheck{Name: stepGitCleanName, Detail: stepGitListPaths("working tree has changes: ", dirty)}
}

// stepGitOwnedCheck passes when every path changed from start to commit is owned: equal to an entry of owned, or under
// an entry that ends in "/", which owns every path below that directory. An entry without the slash owns that exact path
// only. Without rename detection git reports both the old and the new side of a rename, so both are checked.
func stepGitOwnedCheck(
	ctx context.Context, host hostexec.Host, dir, start, commit string, owned []string,
) stepCheck {
	from, err := stepGitResolve(ctx, host, dir, start)
	if err != nil {
		return stepCheck{Name: stepGitOwnedName, Detail: fmt.Sprintf("start %q does not resolve: %v", start, err)}
	}
	to, err := stepGitResolve(ctx, host, dir, commit)
	if err != nil {
		return stepCheck{Name: stepGitOwnedName, Detail: fmt.Sprintf("commit %q does not resolve: %v", commit, err)}
	}
	out, err := stepGit(ctx, host, dir, "diff", "--name-only", "-z", "--no-renames", from, to)
	if err != nil {
		return stepCheck{Name: stepGitOwnedName, Detail: fmt.Sprintf("diff %s %s: %v", from, to, err)}
	}
	allowed := make(map[string]bool, len(owned))
	var dirs []string
	for _, p := range owned {
		if strings.HasSuffix(p, "/") {
			dirs = append(dirs, p)
		} else {
			allowed[p] = true
		}
	}
	var outside []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" && !allowed[p] && !underOwnedDir(p, dirs) {
			outside = append(outside, p)
		}
	}
	if len(outside) == 0 {
		return stepCheck{Name: stepGitOwnedName, OK: true, Detail: "every changed path is owned"}
	}
	detail := stepGitListPaths("changed paths outside owned: ", outside)
	return stepCheck{
		Name:   stepGitOwnedName,
		Detail: detail + "; take those files out of the commit, and the work in the worktree is kept",
	}
}

// underOwnedDir reports whether path lies below one of dirs, each an owned entry ending in "/".
func underOwnedDir(path string, dirs []string) bool {
	for _, dir := range dirs {
		if strings.HasPrefix(path, dir) {
			return true
		}
	}
	return false
}

// stepGitListPaths joins up to stepGitPathLimit entries after prefix and notes how many were left out.
func stepGitListPaths(prefix string, entries []string) string {
	shown := entries
	if len(entries) > stepGitPathLimit {
		shown = entries[:stepGitPathLimit]
	}
	detail := prefix + strings.Join(shown, ", ")
	if left := len(entries) - len(shown); left > 0 {
		detail += fmt.Sprintf(" and %d more", left)
	}
	return detail
}

// stepGitCheckout returns the state of the checkout at dir. It reads with no optional locks, so git does not refresh the
// index of the step's worktree.
func stepGitCheckout(ctx context.Context, host hostexec.Host, dir string) (stepCheckout, error) {
	head, err := stepGitResolve(ctx, host, dir, "HEAD")
	if err != nil {
		return stepCheckout{}, err
	}
	status, err := stepGit(
		ctx,
		host,
		dir,
		"--no-optional-locks",
		"status",
		"--porcelain=v1",
		"-z",
		"--untracked-files=all",
		"--no-renames",
	)
	if err != nil {
		return stepCheckout{}, err
	}
	diff, err := stepGit(ctx, host, dir, "--no-optional-locks", "diff", "HEAD")
	if err != nil {
		return stepCheckout{}, err
	}
	sum := sha256.Sum256([]byte(status + "\x00" + diff))
	return stepCheckout{head: head, hash: hex.EncodeToString(sum[:])}, nil
}

// stepGitCheckoutCheck returns the checkout check of a read-only step's return. It passes when the checkout at dir is still
// the state before records. Otherwise it fails with a detail that names the move, and a git error fails it closed.
func stepGitCheckoutCheck(ctx context.Context, host hostexec.Host, dir string, before stepCheckout) stepCheck {
	now, err := stepGitCheckout(ctx, host, dir)
	if err != nil {
		return stepCheck{Name: stepGitCheckoutName, Detail: fmt.Sprintf("read the checkout: %v", err)}
	}
	if now == before {
		return stepCheck{Name: stepGitCheckoutName, OK: true, Detail: "checkout is as the step started it"}
	}
	detail, err := stepGitCheckoutMove(ctx, host, dir, before, now)
	if err != nil {
		return stepCheck{Name: stepGitCheckoutName, Detail: fmt.Sprintf("read the checkout: %v", err)}
	}
	return stepCheck{Name: stepGitCheckoutName, Detail: detail}
}

// stepGitCheckoutMove returns the detail of a checkout at dir that moved from before to now: HEAD's move when HEAD moved,
// and the paths changed since before. Those are the paths the commits since before changed and the paths that differ from
// HEAD now, sorted and capped by stepGitListPaths.
func stepGitCheckoutMove(
	ctx context.Context, host hostexec.Host, dir string, before, now stepCheckout,
) (string, error) {
	var parts, paths []string
	if now.head != before.head {
		parts = append(parts, fmt.Sprintf("HEAD moved from %s to %s", before.head, now.head))
		committed, err := stepGit(ctx, host, dir, "diff", "--name-only", "-z", "--no-renames", before.head, now.head)
		if err != nil {
			return "", err
		}
		paths = append(paths, stepGitNULRecords(committed)...)
	}
	tracked, err := stepGit(ctx, host, dir, "--no-optional-locks", "diff", "--name-only", "-z", "--no-renames", "HEAD")
	if err != nil {
		return "", err
	}
	untracked, err := stepGit(ctx, host, dir, "--no-optional-locks", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	paths = append(paths, stepGitNULRecords(tracked)...)
	paths = append(paths, stepGitNULRecords(untracked)...)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	if len(paths) > 0 {
		parts = append(parts, stepGitListPaths("changed paths: ", paths))
	}
	if len(parts) == 0 {
		return "working tree differs from the state it started in", nil
	}
	return strings.Join(parts, "; "), nil
}

// stepGitNULRecords returns the NUL-terminated records of out, without the empty ones.
func stepGitNULRecords(out string) []string {
	var records []string
	for _, rec := range strings.Split(out, "\x00") {
		if rec != "" {
			records = append(records, rec)
		}
	}
	return records
}

// stepGitLines returns the line counts of the diff from start to commit in dir: the files it changes, and the lines it
// adds and removes. Each NUL-terminated numstat record is one file, and a binary file's record, with - for both counts,
// is a file with no lines. start and commit resolve as stepGitOwnedCheck resolves them. An empty or unresolvable commit
// is an error, and so is a malformed record.
func stepGitLines(ctx context.Context, host hostexec.Host, dir, start, commit string) (store.StepLines, error) {
	if commit == "" {
		return store.StepLines{}, errors.New("commit is empty")
	}
	from, err := stepGitResolve(ctx, host, dir, start)
	if err != nil {
		return store.StepLines{}, fmt.Errorf("start %q does not resolve: %w", start, err)
	}
	to, err := stepGitResolve(ctx, host, dir, commit)
	if err != nil {
		return store.StepLines{}, fmt.Errorf("commit %q does not resolve: %w", commit, err)
	}
	out, err := stepGit(ctx, host, dir, "diff", "--numstat", "-z", "--no-renames", from, to)
	if err != nil {
		return store.StepLines{}, fmt.Errorf("diff %s %s: %w", from, to, err)
	}
	records := stepGitNULRecords(out)
	lines := store.StepLines{Counted: true, Files: int64(len(records))}
	for _, rec := range records {
		var added, removed int64
		if added, removed, err = stepGitNumstat(rec); err != nil {
			return store.StepLines{}, fmt.Errorf("diff %s %s: %w", from, to, err)
		}
		lines.Added += added
		lines.Removed += removed
	}
	return lines, nil
}

// stepGitNumstatFields is the number of fields in a numstat record: added, removed and the path.
const stepGitNumstatFields = 3

// stepGitNumstat returns the lines a numstat record, added<TAB>removed<TAB>path, adds and removes. A binary file's
// record has - for both counts and adds none. A record of any other shape, or with no path, is malformed.
func stepGitNumstat(rec string) (added, removed int64, err error) {
	fields := strings.SplitN(rec, "\t", stepGitNumstatFields)
	if len(fields) != stepGitNumstatFields || fields[2] == "" {
		return 0, 0, fmt.Errorf("malformed numstat record %q", rec)
	}
	if fields[0] == "-" && fields[1] == "-" {
		return 0, 0, nil
	}
	if added, err = stepGitCount(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("malformed numstat record %q: %w", rec, err)
	}
	if removed, err = stepGitCount(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("malformed numstat record %q: %w", rec, err)
	}
	return added, removed, nil
}

// stepGitCount returns the line count field of a numstat record, which is a non-negative integer.
func stepGitCount(field string) (int64, error) {
	n, err := strconv.ParseInt(field, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a line count", field)
	}
	return n, nil
}
