package kinds

import (
	"encoding/json"
)

// testVerifyStep is the step the verifications the shared results record name.
const testVerifyStep = "verify-step"

// asBefore returns result, an integrate run's result, with a batch verification recorded that names no gate and no
// Requirement: every Done gate runs, since a gate the record does not name runs, and the batch captures its floor
// alone, so the batch decides as it did before a verification choice was made, and the choice adds no line to its
// output. Each of rounds, a task and its round, is recorded too, as roundAsBefore records it.
func asBefore(result string, rounds ...[2]int64) string {
	verified := Verification{Step: testVerifyStep}
	return withRecorded(result, &verified, roundsAsBefore(rounds...))
}

// roundsAsBefore returns the fallback verification of each of rounds, a task and its round: every Done gate runs and
// every Requirement that names an evidence item is captured, as a round did before a verification choice was made. Its
// output line says it fell back.
func roundsAsBefore(rounds ...[2]int64) []RoundVerification {
	out := make([]RoundVerification, 0, len(rounds))
	for _, round := range rounds {
		out = append(out, RoundVerification{
			Task: round[0], Round: round[1],
			Verification: Verification{Step: testVerifyStep, Fallback: "the test runs everything"},
		})
	}
	return out
}

// roundFallbackLine is the output line of a round verification roundsAsBefore records.
const roundFallbackLine = "verification " + testVerifyStep +
	" fell back to every Done gate and the default captures: the test runs everything"

// withRecorded returns result with batch recorded as its verification, when it is not nil, and rounds as its round
// verifications. A result that is not a JSON object is returned as it is.
func withRecorded(result string, batch *Verification, rounds []RoundVerification) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(result), &fields) != nil {
		return result
	}
	if batch != nil {
		encoded, err := json.Marshal(batch)
		if err != nil {
			return result
		}
		fields["verification"] = encoded
	}
	if len(rounds) > 0 {
		encoded, err := json.Marshal(rounds)
		if err != nil {
			return result
		}
		fields["roundVerifications"] = encoded
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return result
	}
	return string(out)
}
