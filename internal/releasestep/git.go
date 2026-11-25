package releasestep

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// remoteKillGrace bounds how long git that reaches origin is waited for once its context has ended and it was killed,
// so a transport it started that outlives it cannot hold its output open.
const remoteKillGrace = 2 * time.Second

// The exit statuses of git that answer a question rather than fail. merge-base --is-ancestor exits notAncestor for a
// commit that is not an ancestor, and rev-parse --verify --quiet exits noObject for a name that names no object.
const (
	notAncestor = 1
	noObject    = 1
)

// gitOut runs git in dir and returns its stdout with surrounding space trimmed.
func (s *step) gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := s.git.Git(ctx, hostexec.GitCall{Dir: dir, Args: args})
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// remoteOut runs git in dir as gitOut does, for a command that reaches origin. The run is a Remote one: git never
// prompts for a login, so it fails where it would wait on a prompt, and it runs in a process group of its own, which
// ctx's end kills whole. It is waited for at most remoteKillGrace after that.
func (s *step) remoteOut(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := s.git.Git(ctx, hostexec.GitCall{Dir: dir, Args: args, Remote: true, WaitDelay: remoteKillGrace})
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// isAncestor reports whether ancestor is an ancestor of descendant in dir, or is it. A commit that is not one is an
// answer, not a failure.
func (s *step) isAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	_, err := s.gitOut(ctx, dir, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var gitErr *hostexec.GitError
	if errors.As(err, &gitErr) && gitErr.ExitCode == notAncestor {
		return false, nil
	}
	return false, err
}

// hasCommit reports whether dir holds commit as a commit object.
func (s *step) hasCommit(ctx context.Context, dir, commit string) (bool, error) {
	_, err := s.gitOut(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", commit+"^{commit}")
	if err == nil {
		return true, nil
	}
	var gitErr *hostexec.GitError
	if errors.As(err, &gitErr) && gitErr.ExitCode == noObject {
		return false, nil
	}
	return false, err
}

// trackingRef is the standard ref a branch of origin is fetched into.
func trackingRef(branch string) string {
	return "refs/remotes/" + Remote + "/" + branch
}

// fetchBranch fetches branch of origin into its tracking ref in dir, and returns the commit the ref names.
func (s *step) fetchBranch(ctx context.Context, dir, branch string) (string, error) {
	ref := trackingRef(branch)
	if _, err := s.remoteOut(ctx, dir, "fetch", "--no-tags", Remote, "+refs/heads/"+branch+":"+ref); err != nil {
		return "", err
	}
	return s.gitOut(ctx, dir, "rev-parse", "--verify", ref+"^{commit}")
}

// version is a version tag's MAJOR.MINOR.PATCH.
type version struct {
	major, minor, patch int
}

// compare orders v against other: negative when v is older, zero when they are the same, positive when v is newer.
func (v version) compare(other version) int {
	return cmp.Or(
		cmp.Compare(v.major, other.major),
		cmp.Compare(v.minor, other.minor),
		cmp.Compare(v.patch, other.patch),
	)
}

// versionTag is a version tag on origin: its name, its version and the commit it peels to.
type versionTag struct {
	name    string
	version version
	commit  string
}

// versionPattern is the form of a version after its prefix: three plain decimal numbers with no leading zero, short
// enough that each fits an int.
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// parseVersion returns the version of the tag named name, and whether name is a version tag of prefix: prefix then
// MAJOR.MINOR.PATCH.
func parseVersion(prefix, name string) (version, bool) {
	rest, found := strings.CutPrefix(name, prefix)
	if !found {
		return version{}, false
	}
	parts := versionPattern.FindStringSubmatch(rest)
	if parts == nil {
		return version{}, false
	}
	numbers := make([]int, 0, len(parts)-1)
	for _, part := range parts[1:] {
		number, err := strconv.Atoi(part)
		if err != nil {
			return version{}, false
		}
		numbers = append(numbers, number)
	}
	return version{major: numbers[0], minor: numbers[1], patch: numbers[2]}, true
}

// tagName is the name of the version tag of prefix at v.
func tagName(prefix string, v version) string {
	return fmt.Sprintf("%s%d.%d.%d", prefix, v.major, v.minor, v.patch)
}

// originTags lists the version tags of prefix on origin of dir, newest first. It fetches nothing.
func (s *step) originTags(ctx context.Context, dir, prefix string) ([]versionTag, error) {
	listing, err := s.remoteOut(ctx, dir, "ls-remote", "--tags", Remote, "refs/tags/"+prefix+"*")
	if err != nil {
		return nil, err
	}
	return parseTagListing(prefix, listing), nil
}

// parseTagListing reads the version tags of prefix from the output of ls-remote --tags, newest first. An annotated tag
// is listed twice, once for the tag object and once, with ^{} after its name, for the commit it peels to; the peeled
// line gives its commit. A tag whose name is not prefix then MAJOR.MINOR.PATCH is left out.
func parseTagListing(prefix, listing string) []versionTag {
	commits := map[string]string{}
	peeled := map[string]bool{}
	for line := range strings.SplitSeq(listing, "\n") {
		object, ref, found := strings.Cut(strings.TrimSpace(line), "\t")
		name, isTag := strings.CutPrefix(ref, "refs/tags/")
		if !found || !isTag {
			continue
		}
		if base, isPeeled := strings.CutSuffix(name, "^{}"); isPeeled {
			commits[base], peeled[base] = object, true
		} else if !peeled[name] {
			commits[name] = object
		}
	}
	tags := make([]versionTag, 0, len(commits))
	for name, commit := range commits {
		if v, ok := parseVersion(prefix, name); ok {
			tags = append(tags, versionTag{name: name, version: v, commit: commit})
		}
	}
	slices.SortFunc(tags, func(a, b versionTag) int { return b.version.compare(a.version) })
	return tags
}

// containingTags returns the tags of tags, which are newest first, whose commit contains candidate: candidate is an
// ancestor of the commit or is it. Tags are made on the branch in order, so the walk stops at the first tag that does
// not contain candidate. A tag whose commit dir does not hold has its objects fetched first.
func (s *step) containingTags(
	ctx context.Context,
	dir string,
	tags []versionTag,
	candidate string,
) ([]versionTag, error) {
	var containing []versionTag
	for _, tag := range tags {
		if err := s.ensureTagCommit(ctx, dir, tag); err != nil {
			return nil, err
		}
		contains, err := s.isAncestor(ctx, dir, candidate, tag.commit)
		if err != nil {
			return nil, err
		}
		if !contains {
			break
		}
		containing = append(containing, tag)
	}
	return containing, nil
}

// ensureTagCommit fetches tag's objects from origin into dir when dir does not hold its commit. It writes no ref.
func (s *step) ensureTagCommit(ctx context.Context, dir string, tag versionTag) error {
	held, err := s.hasCommit(ctx, dir, tag.commit)
	if err != nil || held {
		return err
	}
	_, err = s.remoteOut(ctx, dir, "fetch", "--no-tags", "--no-write-fetch-head", Remote, "refs/tags/"+tag.name)
	if err != nil {
		return err
	}
	held, err = s.hasCommit(ctx, dir, tag.commit)
	if err != nil {
		return err
	}
	if !held {
		return fmt.Errorf("fetched tag %s from %s and it does not hold commit %s", tag.name, Remote, tag.commit)
	}
	return nil
}

// preferred returns the tag of containing, which is newest first and not empty, that a fact names: the one that peels
// to candidate itself when there is one, and the newest otherwise.
func preferred(containing []versionTag, candidate string) versionTag {
	for _, tag := range containing {
		if tag.commit == candidate {
			return tag
		}
	}
	return containing[0]
}
