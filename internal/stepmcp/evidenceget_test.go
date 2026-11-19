package stepmcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// getHash is the invented hash of the artifacts these tests read.
const getHash = "b7a1e0c9"

// evidenceGetDescription is the description evidence_get advertises.
const evidenceGetDescription = "Return one artifact of an evidence run by its path, or the run's stdout log when name is omitted, " +
	"or its stderr log with stderr. UTF-8 bytes come back as numbered text lines: the last lines lines, 40 by default; " +
	"before pages backward to lines numbered below it; match keeps only lines that contain it. Other bytes come back as base64."

// hundredLog is a log of 100 lines, line n reading "line n", each ending in a newline.
func hundredLog() []byte {
	var b strings.Builder
	for n := 1; n <= 100; n++ {
		fmt.Fprintf(&b, "line %d\n", n)
	}
	return []byte(b.String())
}

// logRows returns the rows of hundredLog from line first to line last, each its number then its text, joined by newlines.
func logRows(first, last int) string {
	rows := make([]string, 0, last-first+1)
	for n := first; n <= last; n++ {
		rows = append(rows, fmt.Sprintf("%d: line %d", n, n))
	}
	return strings.Join(rows, "\n")
}

func TestEpicByPartEvidenceGetReturnsTextByLines(t *testing.T) {
	log := hundredLog()
	head := fmt.Sprintf("log (%d bytes, sha256 %s)", len(log), getHash)
	cases := []struct {
		name      string
		arguments string
		data      []byte
		want      string
	}{
		{
			name:      "no slicing shows the last 40 lines",
			arguments: `{"run":"run-1"}`,
			data:      log,
			want:      head + ": 40 of 100 lines\n" + logRows(61, 100) + "\nbefore 61 continues",
		},
		{
			name:      "lines 5 shows the last five",
			arguments: `{"run":"run-1","lines":5}`,
			data:      log,
			want:      head + ": 5 of 100 lines\n" + logRows(96, 100) + "\nbefore 96 continues",
		},
		{
			name:      "lines 5 before 96 pages backward",
			arguments: `{"run":"run-1","lines":5,"before":96}`,
			data:      log,
			want:      head + ": 5 of 100 lines before line 96\n" + logRows(91, 95) + "\nbefore 91 continues",
		},
		{
			name:      "before 3 shows lines 1 and 2 with no continues line",
			arguments: `{"run":"run-1","before":3}`,
			data:      log,
			want:      head + ": 2 of 100 lines before line 3\n" + logRows(1, 2),
		},
		{
			name:      "named UTF-8 artifact",
			arguments: `{"run":"run-1","name":"out/report.txt"}`,
			data:      []byte("ok\n"),
			want:      "artifact out/report.txt (3 bytes, sha256 " + getHash + "): 1 of 1 lines\n1: ok",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEvidence{getReply: &v1.GetEvidenceArtifactResponse{Data: tc.data, Hash: getHash}}
			p := newPeerWithEvidence(t, nil, nil, fake)

			outcome := p.tool("evidence_get", tc.arguments)

			if outcome.isError {
				t.Fatalf("evidence_get returned an error: %s", outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("evidence_get text = %q, want %q", outcome.text, tc.want)
			}
			lines := strings.Split(outcome.text, "\n")
			t.Logf("header: %s", lines[0])
			t.Logf("last line: %s", lines[len(lines)-1])
		})
	}
}

func TestEpicByPartEvidenceGetFiltersLinesByMatch(t *testing.T) {
	var b strings.Builder
	for n := 1; n <= 100; n++ {
		if n%10 == 0 {
			fmt.Fprintf(&b, "FAIL case %d\n", n)
		} else {
			fmt.Fprintf(&b, "ok case %d\n", n)
		}
	}
	log := []byte(b.String())
	head := fmt.Sprintf("log (%d bytes, sha256 %s)", len(log), getHash)
	// failRows returns the FAIL lines from line first to line last, every tenth, each its number then its text.
	failRows := func(first, last int) string {
		var rows []string
		for n := first; n <= last; n += 10 {
			rows = append(rows, fmt.Sprintf("%d: FAIL case %d", n, n))
		}
		return strings.Join(rows, "\n")
	}
	cases := []struct {
		name      string
		arguments string
		want      string
	}{
		{
			name:      "match keeps the ten FAIL lines",
			arguments: `{"run":"run-1","match":"FAIL"}`,
			want:      head + ": 10 of 100 lines matching \"FAIL\"\n" + failRows(10, 100),
		},
		{
			name:      "match with lines 3 keeps the last three and names the first shown",
			arguments: `{"run":"run-1","match":"FAIL","lines":3}`,
			want:      head + ": 3 of 100 lines matching \"FAIL\"\n" + failRows(80, 100) + "\nbefore 80 continues",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEvidence{getReply: &v1.GetEvidenceArtifactResponse{Data: log, Hash: getHash}}
			p := newPeerWithEvidence(t, nil, nil, fake)

			outcome := p.tool("evidence_get", tc.arguments)

			if outcome.isError {
				t.Fatalf("evidence_get returned an error: %s", outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("evidence_get text = %q, want %q", outcome.text, tc.want)
			}
			t.Logf("result:\n%s", outcome.text)
		})
	}
}

func TestEpicByPartEvidenceGetReturnsOtherBytesAsBase64(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		data      []byte
		want      string
	}{
		{
			// The first byte is not UTF-8, so the slicing arguments are ignored and the bytes come back whole.
			name:      "bytes that are not UTF-8 come back as base64",
			arguments: `{"run":"run-1","name":"out/shot.png","lines":5,"before":2,"match":"x"}`,
			data:      []byte{0x89, 'P', 'N', 'G'},
			want:      "artifact out/shot.png (4 bytes, sha256 " + getHash + "): not UTF-8, base64 follows\niVBORw==",
		},
		{
			name:      "empty data is one line",
			arguments: `{"run":"run-1"}`,
			data:      nil,
			want:      "log (0 bytes, sha256 " + getHash + "): empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEvidence{getReply: &v1.GetEvidenceArtifactResponse{Data: tc.data, Hash: getHash}}
			p := newPeerWithEvidence(t, nil, nil, fake)

			outcome := p.tool("evidence_get", tc.arguments)

			if outcome.isError {
				t.Fatalf("evidence_get returned an error: %s", outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("evidence_get text = %q, want %q", outcome.text, tc.want)
			}
			t.Logf("result: %s", outcome.text)
		})
	}
}

// evidenceGetListing is the evidence_get entry of a tools/list reply or of the fixture, reduced to what the test reads.
type evidenceGetListing struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	} `json:"inputSchema"`
}

// findEvidenceGet returns the evidence_get entry of tools, which source names in its failure.
func findEvidenceGet(t *testing.T, tools []evidenceGetListing, source string) evidenceGetListing {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == "evidence_get" {
			return tool
		}
	}
	t.Fatalf("%s does not list evidence_get", source)
	return evidenceGetListing{}
}

// checkEvidenceGetAdvertises reports each way tool, from source, differs from the arguments and description evidence_get
// advertises.
func checkEvidenceGetAdvertises(t *testing.T, source string, tool evidenceGetListing) {
	t.Helper()
	properties := slices.Sorted(maps.Keys(tool.InputSchema.Properties))
	if want := []string{"before", "lines", "match", "name", "run", "stderr"}; !slices.Equal(properties, want) {
		t.Errorf("%s evidence_get properties = %v, want %v", source, properties, want)
	}
	if want := []string{"run"}; !slices.Equal(tool.InputSchema.Required, want) {
		t.Errorf("%s evidence_get required = %v, want %v", source, tool.InputSchema.Required, want)
	}
	if tool.Description != evidenceGetDescription {
		t.Errorf("%s evidence_get description = %q, want %q", source, tool.Description, evidenceGetDescription)
	}
}

func TestEpicByPartEvidenceGetToolsListAdvertisesItsArguments(t *testing.T) {
	p := newPeerWithEvidence(t, nil, nil, nil)
	reply := p.call("tools/list", map[string]any{})
	var listed struct {
		Tools []evidenceGetListing `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	live := findEvidenceGet(t, listed.Tools, "tools/list")
	t.Logf("evidence_get properties: %v", slices.Sorted(maps.Keys(live.InputSchema.Properties)))
	t.Logf("evidence_get required: %v", live.InputSchema.Required)
	t.Logf("evidence_get description: %s", live.Description)
	checkEvidenceGetAdvertises(t, "tools/list", live)

	raw, err := os.ReadFile(toolsListFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture []evidenceGetListing
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	checkEvidenceGetAdvertises(t, toolsListFixture, findEvidenceGet(t, fixture, toolsListFixture))
}

func TestEvidenceGetRefusesNegativeLinesOrBefore(t *testing.T) {
	cases := []struct {
		arguments string
		refusal   string
	}{
		{arguments: `{"run":"run-1","lines":-1}`, refusal: "lines: must not be negative"},
		{arguments: `{"run":"run-1","before":-1}`, refusal: "before: must not be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.refusal, func(t *testing.T) {
			fake := &fakeEvidence{getReply: &v1.GetEvidenceArtifactResponse{Data: []byte("x\n"), Hash: getHash}}
			p := newPeerWithEvidence(t, nil, nil, fake)

			outcome := p.tool("evidence_get", tc.arguments)

			if !outcome.isError || outcome.text != tc.refusal {
				t.Fatalf(
					"evidence_get = %q (isError %t), want the refusal %q",
					outcome.text,
					outcome.isError,
					tc.refusal,
				)
			}
			if n := len(fake.calls().gets); n != 0 {
				t.Fatalf("evidence_get reached the service %d times, want none", n)
			}
		})
	}
}
