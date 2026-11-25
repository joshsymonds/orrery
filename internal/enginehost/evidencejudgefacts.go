package enginehost

import (
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/testfacts"
)

// judgeFactsHeading heads the test facts a judge prompt gives. The facts are orrery's reading of the logs, which the judge
// weighs and may overrule; the verdict stays the judge's.
const judgeFactsHeading = "Test facts orrery read from the logs. This is orrery's own reading of the output, and it may " +
	"miss a runner's format or misread one: where the logs show otherwise, trust the logs and overrule it. The verdict " +
	"is yours."

// judgeFactsLines returns the lines a judge prompt of run puts after judgePrompt's: judgeFactsHeading, then each line
// testfacts gives for the text of run's stdout and stderr logs, as a read of them shows it, and of each kept artifact whose
// path ends in .xml, which a JUnit report is, indented by two spaces. A log cut at its start adds a line that the tests before
// the cut are not counted, and a log or artifact that cannot be read adds a line saying so, so the judge still starts. A run
// with no log and no such artifact gives nil.
func (e *Evidence) judgeFactsLines(run store.EvidenceRun) []string {
	var texts, notes []string
	logs := []struct{ label, hash string }{{"stdout log", run.LogHash}, {"stderr log", run.StderrHash}}
	for _, entry := range logs {
		if entry.hash == "" {
			continue
		}
		raw, err := e.blobs.read(entry.hash + ".log")
		if err != nil {
			notes = append(notes, "The "+entry.label+" could not be read for these facts: "+err.Error())
			continue
		}
		if e.logCut(raw) {
			notes = append(notes, "The "+entry.label+" was cut at its start, so tests before the cut are not counted.")
		}
		texts = append(texts, e.logText(raw))
	}
	for _, artifact := range run.Artifacts {
		if artifact.State != judgeStateKept || !strings.HasSuffix(artifact.Path, ".xml") {
			continue
		}
		data, err := e.blobs.read(artifact.Blob)
		if err != nil {
			notes = append(notes, "The artifact "+artifact.Path+" could not be read for these facts: "+err.Error())
			continue
		}
		texts = append(texts, string(data))
	}
	if len(texts) == 0 && len(notes) == 0 {
		return nil
	}
	lines := []string{judgeFactsHeading}
	for _, line := range append(testfacts.Read(texts...).Lines(), notes...) {
		lines = append(lines, "  "+line)
	}
	return lines
}
