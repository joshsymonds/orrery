package stepmcp

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// evidenceGetDefaultLines is the number of lines evidence_get returns when lines is 0 or absent.
const evidenceGetDefaultLines = 40

// evidenceGetText returns the text evidence_get answers with for args and got. Bytes that are not UTF-8 come back as
// base64 under a header, unsliced. UTF-8 bytes come back under a header as numbered lines: the last lines of those
// numbered below before that contain match, and a last line names the first shown line when lines remain before it.
func evidenceGetText(args evidenceGetArgs, got *v1.GetEvidenceArtifactResponse) string {
	data := got.GetData()
	head := fmt.Sprintf("%s (%d bytes, sha256 %s)", evidenceGetWhat(args), len(data), got.GetHash())
	switch {
	case len(data) == 0:
		return head + ": empty"
	case !utf8.Valid(data):
		return head + ": not UTF-8, base64 follows\n" + base64.StdEncoding.EncodeToString(data)
	}
	return evidenceLinesText(head, args, string(data))
}

// evidenceGetWhat names the bytes in the header: the artifact's path, the stderr log, or the stdout log.
func evidenceGetWhat(args evidenceGetArgs) string {
	switch {
	case args.Name != "":
		return "artifact " + args.Name
	case args.Stderr:
		return "stderr"
	}
	return "log"
}

// evidenceLinesText returns head, with the count of the page of text's lines that args selects, then one line per page
// line, numbered from 1. A trailing newline ends the last line and adds none.
func evidenceLinesText(head string, args evidenceGetArgs, text string) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	var kept []int
	for number := 1; number <= len(lines); number++ {
		if args.Before != 0 && number >= args.Before {
			break
		}
		if args.Match != "" && !strings.Contains(lines[number-1], args.Match) {
			continue
		}
		kept = append(kept, number)
	}
	size := args.Lines
	if size == 0 {
		size = evidenceGetDefaultLines
	}
	page := kept
	if len(page) > size {
		page = page[len(page)-size:]
	}

	header := fmt.Sprintf("%s: %d of %d lines", head, len(page), len(lines))
	if args.Match != "" {
		header += fmt.Sprintf(" matching \"%s\"", args.Match)
	}
	if args.Before != 0 {
		header += fmt.Sprintf(" before line %d", args.Before)
	}
	out := []string{header}
	for _, number := range page {
		out = append(out, fmt.Sprintf("%d: %s", number, lines[number-1]))
	}
	if len(kept) > len(page) {
		out = append(out, fmt.Sprintf("before %d continues", page[0]))
	}
	return strings.Join(out, "\n")
}
