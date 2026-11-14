package evidence

// Phases a judged run belongs to: red shows the conclusion does not hold yet, green shows it does.
const (
	PhaseRed   = "red"
	PhaseGreen = "green"
)

// Verdicts a judge returns on whether a run's conclusion holds.
const (
	VerdictHolds = "holds"
	VerdictFails = "fails"
)

// Sources a run's evidence comes from: a capture the engine executes, or files a person supplies.
const (
	SourceCaptured = "captured"
	SourceSupplied = "supplied"
)

// Passed reports whether a judged run passes its phase. A run that was not captured passes neither phase whatever its
// verdict: notCaptured names why the capture could not start, was killed at its limit or was lost, and is empty when
// the capture ran. Red passes when the judge finds the conclusion fails. Green passes when the judge finds it holds
// and the capture exited 0. Any other phase or verdict passes nothing.
func Passed(phase, notCaptured string, exit int, verdict string) bool {
	if notCaptured != "" {
		return false
	}
	switch phase {
	case PhaseRed:
		return verdict == VerdictFails
	case PhaseGreen:
		return verdict == VerdictHolds && exit == 0
	}
	return false
}
