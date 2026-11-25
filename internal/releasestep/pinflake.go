package releasestep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// pinAttempts bounds how many times a pin-flake step makes its pin when origin refuses each push because its branch
// moved while the pin was made.
const pinAttempts = 3

// The files of a flake a pin reads and commits.
const (
	flakeNix  = "flake.nix"
	flakeLock = "flake.lock"
)

// pinFlake is a pin-flake step: it pins a flake input to the newest version tag that contains the candidate, on the
// flake repository's origin branch.
type pinFlake struct {
	s         *step
	candidate string
	branch    string
	prefix    string
	flake     string
	input     string
	url       string
	// tag is the newest version tag on the orrery origin that contained the candidate at the last check.
	tag versionTag
	// origin is the commit the flake origin's branch named at the last check.
	origin string
}

// check lists the orrery origin's version tags and fetches the flake origin's branch. There must be a tag that contains
// the candidate, or the step fails: the tag step comes first. The postcondition holds when the branch's flake.nix sets
// the input's url to the step's url with a version tag in its slot whose commit contains the candidate, and its
// flake.lock locks the input at that commit. A newer tag than the one the step would pin meets it too.
func (p *pinFlake) check(ctx context.Context) (verdict, error) {
	tags, err := p.s.originTags(ctx, p.s.repo, p.prefix)
	if err != nil {
		return verdict{}, err
	}
	containing, err := p.s.containingTags(ctx, p.s.repo, tags, p.candidate)
	if err != nil {
		return verdict{}, err
	}
	if len(containing) == 0 {
		return verdict{}, fmt.Errorf("no %s* version tag on %s contains candidate %s: the tag step comes first",
			p.prefix, Remote, p.candidate)
	}
	p.tag = containing[0]
	p.origin, err = p.s.fetchBranch(ctx, p.flake, p.branch)
	if err != nil {
		return verdict{}, err
	}
	where := fmt.Sprintf("%s %s of %s", Remote, p.branch, p.flake)
	facts := []string{fmt.Sprintf("%s is %s", where, p.origin)}
	nix, err := p.s.gitOut(ctx, p.flake, "show", trackingRef(p.branch)+":"+flakeNix)
	if err != nil {
		return verdict{}, fmt.Errorf("read %s of %s: %w", flakeNix, where, err)
	}
	url, err := inputURL(nix, p.input)
	if err != nil {
		return verdict{facts: facts, why: fmt.Sprintf("%s of %s: %v", flakeNix, where, err)}, nil
	}
	facts = append(facts, fmt.Sprintf("%s of %s sets %s.url to %q", flakeNix, where, p.input, url))
	pinned, found := p.pinnedTag(url, containing)
	if !found {
		why := fmt.Sprintf("%s.url %q names no %s* version tag that contains candidate %s", p.input, url, p.prefix,
			p.candidate)
		return verdict{facts: facts, why: why}, nil
	}
	facts = append(facts, fmt.Sprintf("tag %s peels to %s, which contains candidate %s", pinned.name, pinned.commit,
		p.candidate))
	lock, err := p.s.gitOut(ctx, p.flake, "show", trackingRef(p.branch)+":"+flakeLock)
	if err != nil {
		return verdict{facts: facts, why: fmt.Sprintf("read %s of %s: %v", flakeLock, where, err)}, nil
	}
	rev, err := lockedRev([]byte(lock), p.input)
	if err != nil {
		return verdict{facts: facts, why: fmt.Sprintf("%s of %s: %v", flakeLock, where, err)}, nil
	}
	if rev != pinned.commit {
		why := fmt.Sprintf("%s of %s locks %s at %s, not at tag %s's commit %s", flakeLock, where, p.input, rev,
			pinned.name, pinned.commit)
		return verdict{facts: facts, why: why}, nil
	}
	facts = append(facts, fmt.Sprintf("%s of %s locks %s at %s", flakeLock, where, p.input, rev))
	return verdict{held: true, facts: facts}, nil
}

// pinnedTag returns the tag of containing that url names in the slot of the step's url, and whether it names one.
func (p *pinFlake) pinnedTag(url string, containing []versionTag) (versionTag, bool) {
	before, after, _ := strings.Cut(p.url, TagSlot)
	name, found := strings.CutPrefix(url, before)
	if !found {
		return versionTag{}, false
	}
	if name, found = strings.CutSuffix(name, after); !found {
		return versionTag{}, false
	}
	for _, tag := range containing {
		if tag.name == name {
			return tag, true
		}
	}
	return versionTag{}, false
}

// act makes the pin on the flake origin's branch without touching the flake repository's checkout. When origin refuses
// the push because its branch moved, the step starts again from the fetch, which may find the pin already holds, at
// most pinAttempts times in all.
func (p *pinFlake) act(ctx context.Context) error {
	for attempt := range pinAttempts {
		if attempt > 0 {
			again, err := p.check(ctx)
			if err != nil {
				return err
			}
			if again.held {
				p.s.say(fmt.Sprintf("%s %s of %s already holds a pin that contains the candidate", Remote, p.branch,
					p.flake))
				return nil
			}
		}
		moved, err := p.pin(ctx)
		if err != nil || !moved {
			return err
		}
	}
	return fmt.Errorf("%s %s of %s moved during each of %d pins", Remote, p.branch, p.flake, pinAttempts)
}

// pin makes the pin once, in a temporary worktree of the flake repository detached at the branch's commit from the
// last check: it rewrites the input's url line to the tag, runs `nix flake update <input>`, checks the new flake.lock
// locks the input at the tag's commit, commits only flake.nix and flake.lock, and pushes the commit to the branch,
// never forced. It reports moved when origin refused the push because its branch had moved. The temporary worktree is
// always removed.
func (p *pinFlake) pin(ctx context.Context) (moved bool, err error) {
	dir, err := os.MkdirTemp(p.s.tempDir, "orrery-pin-")
	if err != nil {
		return false, fmt.Errorf("make the pin's temporary directory: %w", err)
	}
	worktree := filepath.Join(dir, "flake")
	defer func() {
		err = errors.Join(err, p.removeWorktree(context.WithoutCancel(ctx), dir, worktree))
	}()
	if _, err = p.s.gitOut(ctx, p.flake, "worktree", "add", "--detach", worktree, p.origin); err != nil {
		return false, fmt.Errorf("add the pin's temporary worktree: %w", err)
	}
	if rewriteErr := p.rewrite(ctx, worktree); rewriteErr != nil {
		return false, rewriteErr
	}
	message := p.input + ": pin " + p.tag.name
	if _, err = p.s.gitOut(ctx, worktree, "add", "--", flakeNix, flakeLock); err != nil {
		return false, err
	}
	if _, err = p.s.gitOut(ctx, worktree, "commit", "-q", "-m", message, "--", flakeNix, flakeLock); err != nil {
		return false, fmt.Errorf("commit the pin: %w", err)
	}
	commit, err := p.s.gitOut(ctx, worktree, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	_, pushErr := p.s.remoteOut(ctx, worktree, "push", Remote, "HEAD:refs/heads/"+p.branch)
	if pushErr == nil {
		p.s.say(fmt.Sprintf("pushed %s (%s) to %s %s of %s, which was %s", commit, message, Remote, p.branch, p.flake,
			p.origin))
		return false, nil
	}
	now, err := p.s.fetchBranch(ctx, p.flake, p.branch)
	if err != nil {
		return false, err
	}
	if now == p.origin {
		return false, fmt.Errorf("%s refused the push of the pin to %s: %w", Remote, p.branch, pushErr)
	}
	p.s.say(fmt.Sprintf("%s %s of %s moved from %s to %s while the pin was made, so the step starts again",
		Remote, p.branch, p.flake, p.origin, now))
	return true, nil
}

// rewrite sets the input's url line in worktree's flake.nix to the tag, runs `nix flake update <input>` there, and
// checks the flake.lock it writes locks the input at the tag's commit. The files are read and written only inside
// worktree.
func (p *pinFlake) rewrite(ctx context.Context, worktree string) (err error) {
	root, err := os.OpenRoot(worktree)
	if err != nil {
		return fmt.Errorf("open the pin's temporary worktree: %w", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	info, err := root.Stat(flakeNix)
	if err != nil {
		return fmt.Errorf("stat %s: %w", flakeNix, err)
	}
	text, err := root.ReadFile(flakeNix)
	if err != nil {
		return fmt.Errorf("read %s: %w", flakeNix, err)
	}
	rewritten, err := setInputURL(string(text), p.input, strings.Replace(p.url, TagSlot, p.tag.name, 1))
	if err != nil {
		return fmt.Errorf("%s: %w", flakeNix, err)
	}
	if err = root.WriteFile(flakeNix, []byte(rewritten), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write %s: %w", flakeNix, err)
	}
	argv := []string{"nix", "flake", "update", p.input}
	if err = p.s.machine.Run(ctx, worktree, argv, p.s.stderr, p.s.stderr); err != nil {
		return fmt.Errorf("%w", err)
	}
	lock, err := root.ReadFile(flakeLock)
	if err != nil {
		return fmt.Errorf("read %s: %w", flakeLock, err)
	}
	rev, err := lockedRev(lock, p.input)
	if err != nil {
		return fmt.Errorf("%s after %s: %w", flakeLock, strings.Join(argv, " "), err)
	}
	if rev != p.tag.commit {
		return fmt.Errorf("%s locked %s at %s, not at tag %s's commit %s", strings.Join(argv, " "), p.input, rev,
			p.tag.name, p.tag.commit)
	}
	return nil
}

// removeWorktree removes the pin's temporary worktree from the flake repository, and then its directory.
func (p *pinFlake) removeWorktree(ctx context.Context, dir, worktree string) error {
	var removeErr error
	if _, err := os.Stat(worktree); err == nil {
		_, removeErr = p.s.gitOut(ctx, p.flake, "worktree", "remove", "--force", worktree)
	}
	if err := os.RemoveAll(dir); err != nil {
		removeErr = errors.Join(removeErr, fmt.Errorf("remove the pin's temporary directory: %w", err))
	}
	return removeErr
}

// urlLine returns the pattern of the line of flake.nix that sets input's url, `input.url = "...";` or
// `inputs.input.url = "...";`, with the url as its one group.
func urlLine(input string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?m)^[ \t]*(?:inputs\.)?` + regexp.QuoteMeta(input) + `\.url[ \t]*=[ \t]*"([^"]*)"[ \t]*;`,
	)
}

// inputURL returns the url that text, a flake.nix, sets input to. text must set it on exactly one line.
func inputURL(text, input string) (string, error) {
	matches := urlLine(input).FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("%d lines set %s.url, not exactly one", len(matches), input)
	}
	return matches[0][1], nil
}

// setInputURL returns text, a flake.nix, with the url on the one line that sets input's url replaced by url.
func setInputURL(text, input, url string) (string, error) {
	matches := urlLine(input).FindAllStringSubmatchIndex(text, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("%d lines set %s.url, not exactly one", len(matches), input)
	}
	start, end := matches[0][2], matches[0][3]
	return text[:start] + url + text[end:], nil
}

// flakeLockFile is the part of a flake.lock a pin reads: the root node's name and each node's inputs and locked rev.
type flakeLockFile struct {
	Root  string              `json:"root"`
	Nodes map[string]lockNode `json:"nodes"`
}

// lockNode is one node of a flake.lock.
type lockNode struct {
	// Inputs maps each input's name to the node it locks to, a node name, or a follows path, a list.
	Inputs map[string]json.RawMessage `json:"inputs"`
	Locked struct {
		Rev string `json:"rev"`
	} `json:"locked"`
}

// lockedRev returns the commit lock, a flake.lock, locks input at: the locked rev of the node the root node's input
// names.
func lockedRev(lock []byte, input string) (string, error) {
	var file flakeLockFile
	if err := json.Unmarshal(lock, &file); err != nil {
		return "", fmt.Errorf("not a flake lock: %w", err)
	}
	rootName := file.Root
	if rootName == "" {
		rootName = "root"
	}
	root, found := file.Nodes[rootName]
	if !found {
		return "", fmt.Errorf("no root node %q", rootName)
	}
	var nodeName string
	if err := json.Unmarshal(root.Inputs[input], &nodeName); err != nil {
		return "", fmt.Errorf("the root node's input %s names no node: %w", input, err)
	}
	node, found := file.Nodes[nodeName]
	if !found || node.Locked.Rev == "" {
		return "", fmt.Errorf("node %q of input %s locks no rev", nodeName, input)
	}
	return node.Locked.Rev, nil
}
