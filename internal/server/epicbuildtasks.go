package server

import (
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/redact"
)

// epicBuildTaskMessages converts each task of an orreryd epic's plan to its EpicBuildTask, in order. The title is
// redacted, and so are each finding's id, claim, location and reason. A red or green run is converted by
// epicBuildRunMessage. A run the task does not name is absent.
func epicBuildTaskMessages(tasks []*enginev1.EpicTask) []*orreryv1.EpicBuildTask {
	out := make([]*orreryv1.EpicBuildTask, 0, len(tasks))
	for _, task := range tasks {
		msg := &orreryv1.EpicBuildTask{
			Id:           task.GetId(),
			Title:        redact.Text(task.GetTitle()),
			Status:       task.GetStatus(),
			Stage:        task.GetStage(),
			Requirements: task.GetRequirements(),
			Reviews:      epicTaskReviewMessages(task.GetReviews()),
		}
		if run := task.GetRed(); run != nil {
			msg.Red = epicBuildRunMessage(run)
		}
		if run := task.GetGreen(); run != nil {
			msg.Green = epicBuildRunMessage(run)
		}
		out = append(out, msg)
	}
	return out
}

// epicBuildRunMessage converts a task's red or green run as epicRunMessage converts a requirement's run, with its judge
// reason and not-captured reason redacted.
func epicBuildRunMessage(run *enginev1.EvidenceRun) *orreryv1.EvidenceRun {
	msg := epicRunMessage(run)
	msg.JudgeReason = redact.Text(msg.GetJudgeReason())
	msg.NotCaptured = redact.Text(msg.GetNotCaptured())
	return msg
}

// epicTaskReviewMessages converts each review of a task's build to its EpicTaskReview, in order.
func epicTaskReviewMessages(reviews []*enginev1.EpicTaskReview) []*orreryv1.EpicTaskReview {
	out := make([]*orreryv1.EpicTaskReview, 0, len(reviews))
	for _, review := range reviews {
		findings := make([]*orreryv1.EpicTaskFinding, 0, len(review.GetFindings()))
		for _, finding := range review.GetFindings() {
			findings = append(findings, &orreryv1.EpicTaskFinding{
				Id:       redact.Text(finding.GetId()),
				Claim:    redact.Text(finding.GetClaim()),
				Location: redact.Text(finding.GetLocation()),
				Step:     finding.GetStep(),
				Result:   finding.GetResult(),
				Reason:   redact.Text(finding.GetReason()),
			})
		}
		out = append(out, &orreryv1.EpicTaskReview{
			Attempt:  review.GetAttempt(),
			Gate:     review.GetGate(),
			Step:     review.GetStep(),
			Verdict:  review.GetVerdict(),
			Findings: findings,
			Reason:   redact.Text(review.GetReason()),
		})
	}
	return out
}
