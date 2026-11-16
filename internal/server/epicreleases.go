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

// epicReleaseProofMessages converts each release step of an orreryd epic's release proof to its EpicReleaseProof, in the
// order the stored epic lists them, with its evidence and reason redacted as a release run's are.
func epicReleaseProofMessages(proofs []*enginev1.EpicReleaseProof) []*orreryv1.EpicReleaseProof {
	out := make([]*orreryv1.EpicReleaseProof, 0, len(proofs))
	for _, proof := range proofs {
		out = append(out, &orreryv1.EpicReleaseProof{
			Position: proof.GetPosition(),
			Step:     proof.GetStep(),
			Target:   proof.GetTarget(),
			Undo:     proof.GetUndo(),
			State:    proof.GetState(),
			Evidence: redact.Text(proof.GetEvidence()),
			Reason:   redact.Text(proof.GetReason()),
			Attempt:  proof.GetAttempt(),
			At:       proof.GetAt(),
		})
	}
	return out
}

// epicReportMessage converts report to its EpicReport: its outcome, its shipped and not-shipped Requirement ids, and its
// why, redacted as a release run's reason is. A stored epic with no report has none, and so answers nil.
func epicReportMessage(report *enginev1.EpicReport) *orreryv1.EpicReport {
	if report == nil {
		return nil
	}
	return &orreryv1.EpicReport{
		Outcome:    report.GetOutcome(),
		Shipped:    report.GetShipped(),
		NotShipped: report.GetNotShipped(),
		Why:        redact.Text(report.GetWhy()),
	}
}
