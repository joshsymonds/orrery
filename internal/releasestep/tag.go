package releasestep

import (
	"context"
	"fmt"
	"strings"
)

// tagAttempts bounds how many names a tag step tries when origin refuses each because another release took it first.
const tagAttempts = 5

// tagStep is a tag step: it pushes a signed annotated version tag at the candidate.
type tagStep struct {
	s         *step
	candidate string
	prefix    string
	title     string
}

// check lists origin's version tags. The postcondition holds when one of them peels to a commit that contains the
// candidate; the fact names the one that peels to the candidate itself when there is one, and the newest otherwise.
func (t *tagStep) check(ctx context.Context) (verdict, error) {
	tags, err := t.s.originTags(ctx, t.s.repo, t.prefix)
	if err != nil {
		return verdict{}, err
	}
	containing, err := t.s.containingTags(ctx, t.s.repo, tags, t.candidate)
	if err != nil {
		return verdict{}, err
	}
	if len(containing) == 0 {
		why := fmt.Sprintf("no %s* version tag on %s contains candidate %s", t.prefix, Remote, t.candidate)
		return verdict{why: why}, nil
	}
	return verdict{held: true, facts: tagFacts(preferred(containing, t.candidate), t.candidate)}, nil
}

// tagFacts are the facts that show tag on origin holds candidate.
func tagFacts(tag versionTag, candidate string) []string {
	facts := []string{fmt.Sprintf("tag %s on %s peels to %s", tag.name, Remote, tag.commit)}
	if tag.commit == candidate {
		return append(facts, fmt.Sprintf("tag %s is candidate %s", tag.name, candidate))
	}
	return append(facts, fmt.Sprintf("tag %s contains candidate %s", tag.name, candidate))
}

// act creates a signed annotated tag at the candidate and pushes it. Its name is the next patch above the newest version
// among origin's tags and the repository's own, so a name either already has is skipped. When origin refuses the push
// because the name was taken there meanwhile, the next free name is tried, at most tagAttempts names in all. No tag is
// ever moved or deleted.
func (t *tagStep) act(ctx context.Context) error {
	for range tagAttempts {
		newest, err := t.newestVersion(ctx)
		if err != nil {
			return err
		}
		name := tagName(t.prefix, version{major: newest.major, minor: newest.minor, patch: newest.patch + 1})
		if _, err = t.s.gitOut(ctx, t.s.repo, "tag", "-s", "-a", name, "-m", t.message(name), t.candidate); err != nil {
			return fmt.Errorf("create tag %s at %s: %w", name, t.candidate, err)
		}
		_, pushErr := t.s.remoteOut(ctx, t.s.repo, "push", Remote, "refs/tags/"+name)
		if pushErr == nil {
			t.s.say(fmt.Sprintf("pushed signed annotated tag %s at %s to %s", name, t.candidate, Remote))
			return nil
		}
		taken, err := t.onOrigin(ctx, name)
		if err != nil {
			return err
		}
		if !taken {
			return fmt.Errorf("%s refused the push of tag %s: %w", Remote, name, pushErr)
		}
		t.s.say(fmt.Sprintf("%s took %s while this step ran, so the step takes the next free name", Remote, name))
	}
	return fmt.Errorf("%s took each of the %d names this step tried", Remote, tagAttempts)
}

// message is the message of the tag named name: the name, then the change's title when it has one.
func (t *tagStep) message(name string) string {
	if strings.TrimSpace(t.title) == "" {
		return name
	}
	return name + ": " + t.title
}

// newestVersion returns the newest version among the version tags on origin and in the repository, or the zero
// version when there is none.
func (t *tagStep) newestVersion(ctx context.Context) (version, error) {
	tags, err := t.s.originTags(ctx, t.s.repo, t.prefix)
	if err != nil {
		return version{}, err
	}
	var newest version
	if len(tags) > 0 {
		newest = tags[0].version
	}
	local, err := t.s.gitOut(ctx, t.s.repo, "tag", "--list", t.prefix+"*")
	if err != nil {
		return version{}, err
	}
	for name := range strings.FieldsSeq(local) {
		if v, ok := parseVersion(t.prefix, name); ok && v.compare(newest) > 0 {
			newest = v
		}
	}
	return newest, nil
}

// onOrigin reports whether origin has a tag named name.
func (t *tagStep) onOrigin(ctx context.Context, name string) (bool, error) {
	tags, err := t.s.originTags(ctx, t.s.repo, t.prefix)
	if err != nil {
		return false, err
	}
	for _, tag := range tags {
		if tag.name == name {
			return true, nil
		}
	}
	return false, nil
}
