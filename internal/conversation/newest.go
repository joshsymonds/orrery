package conversation

// Newest reports the newest reply or question time of transcripts. For each path it remembers how far it has
// read and the time that read left newest, so a later call reads only the bytes appended since. Newest is safe
// for concurrent use.
type Newest struct {
	tracker *fileTracker[int64]
}

// NewNewest returns a Newest that has read no transcript.
func NewNewest() *Newest {
	return &Newest{tracker: newFileTracker[int64]()}
}

// Time returns the unix seconds of the newest reply or question of the transcript at path, or 0 when it has none.
// Tool calls, prompts, results and notices do not count, and neither do sidechain or meta lines.
func (n *Newest) Time(path string) (int64, error) {
	return n.tracker.read(path, applyNewestLine)
}

// applyNewestLine raises the newest time to the reply or question times in one transcript line.
func applyNewestLine(newest *int64, line []byte) {
	for _, item := range lineItems(line) {
		if (item.Kind == kindReply || item.Kind == kindQuestion) && item.Time > *newest {
			*newest = item.Time
		}
	}
}
