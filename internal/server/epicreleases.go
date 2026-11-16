package server

import (
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/redact"
)

// epicReleaseRunMessages converts each release run of an orreryd epic to its EpicReleaseRun, in the order the stored
// epic lists them, with its evidence and reason redacted as the ledger's free text is.
func epicReleaseRunMessages(runs []*enginev1.EpicReleaseRun) []*orreryv1.EpicReleaseRun {
	out := make([]*orreryv1.EpicReleaseRun, 0, len(runs))
	for _, run := range runs {
		out = append(out, &orreryv1.EpicReleaseRun{
			Part:     run.GetPart(),
			Position: run.GetPosition(),
			Attempt:  run.GetAttempt(),
			Step:     run.GetStep(),
			Start:    run.GetStart(),
			Head:     run.GetHead(),
			End:      run.GetEnd(),
			Commits:  run.GetCommits(),
			Evidence: redact.Text(run.GetEvidence()),
			Passed:   run.GetPassed(),
			Reason:   redact.Text(run.GetReason()),
			At:       run.GetAt(),
		})
	}
	return out
}
