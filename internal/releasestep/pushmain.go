package releasestep

import (
	"context"
	"fmt"
)

// pushMain is a push-main step: it fast-forwards origin's branch to the candidate.
type pushMain struct {
	s         *step
	candidate string
	branch    string
	// origin is the commit origin's branch named at the last check.
	origin string
}

// check fetches origin's branch. The postcondition holds when the candidate is an ancestor of the branch's commit or is
// it, so a branch that has moved past the candidate still meets it.
func (p *pushMain) check(ctx context.Context) (verdict, error) {
	origin, err := p.s.fetchBranch(ctx, p.s.repo, p.branch)
	if err != nil {
		return verdict{}, err
	}
	p.origin = origin
	contains, err := p.s.isAncestor(ctx, p.s.repo, p.candidate, origin)
	if err != nil {
		return verdict{}, err
	}
	branchFact := fmt.Sprintf("%s %s is %s", Remote, p.branch, origin)
	if !contains {
		why := fmt.Sprintf("candidate %s is not in %s %s, which is %s", p.candidate, Remote, p.branch, origin)
		return verdict{facts: []string{branchFact}, why: why}, nil
	}
	candidateFact := fmt.Sprintf("candidate %s is in %s %s", p.candidate, Remote, p.branch)
	return verdict{held: true, facts: []string{branchFact, candidateFact}}, nil
}

// act pushes the candidate to origin's branch, which must be an ancestor of it, so the push is a fast-forward. A branch
// that has moved to a commit the candidate does not hold fails the step, and orreryd prepares the candidate again. The
// push is never forced, and a refused one fails with git's message.
func (p *pushMain) act(ctx context.Context) error {
	fastForward, err := p.s.isAncestor(ctx, p.s.repo, p.origin, p.candidate)
	if err != nil {
		return err
	}
	if !fastForward {
		return fmt.Errorf("%s %s is %s, which candidate %s does not hold: the candidate must be prepared again",
			Remote, p.branch, p.origin, p.candidate)
	}
	refspec := p.candidate + ":refs/heads/" + p.branch
	if _, err = p.s.remoteOut(ctx, p.s.repo, "push", Remote, refspec); err != nil {
		return fmt.Errorf("%s refused the push of %s to %s: %w", Remote, p.candidate, p.branch, err)
	}
	p.s.say(fmt.Sprintf("pushed %s to %s %s, which was %s", p.candidate, Remote, p.branch, p.origin))
	return nil
}
