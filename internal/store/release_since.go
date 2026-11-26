package store

// LastReplan returns the highest attempt among runs' replan release runs, or -1 when there is none. A replan sends the
// release back to planning, and every release step runs again from the candidate the replan leads to, so only a step
// passed at an attempt above LastReplan counts as passed.
func LastReplan(runs []ReleaseRun) int64 {
	last := int64(-1)
	for _, run := range runs {
		if run.Part == ReleasePartReplan {
			last = max(last, run.Attempt)
		}
	}
	return last
}
