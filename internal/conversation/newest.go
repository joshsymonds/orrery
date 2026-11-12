package conversation

// newestState is what Newest remembers of one transcript: the newest reply or question time and the newest
// interruption time.
type newestState struct {
	reply        int64
	interruption int64
}

// Newest reports the newest reply or question time and the newest interruption time of transcripts. For each path
// it remembers how far it has read and the times that read left newest, so a later call reads only the bytes
// appended since. Newest is safe for concurrent use.
type Newest struct {
	tracker *fileTracker[newestState]
}

// NewNewest returns a Newest that has read no transcript.
func NewNewest() *Newest {
	return &Newest{tracker: newFileTracker[newestState]()}
}

// Time returns the unix seconds of the newest reply or question of the transcript at path, or 0 when it has none.
// Tool calls, prompts, results and notices do not count, and neither do sidechain or meta lines.
func (n *Newest) Time(path string) (int64, error) {
	state, err := n.tracker.read(path, applyNewestLine)
	if err != nil {
		return 0, err
	}
	return state.reply, nil
}

// Interrupted returns the unix seconds of the newest interruption line of the transcript at path, or 0 when it has
// none. An interruption line is one the reader gives as an interrupted notice, and sidechain or meta lines do not
// count.
func (n *Newest) Interrupted(path string) (int64, error) {
	state, err := n.tracker.read(path, applyNewestLine)
	if err != nil {
		return 0, err
	}
	return state.interruption, nil
}

// applyNewestLine raises the newest reply or question time and the newest interruption time to those in one
// transcript line.
func applyNewestLine(state *newestState, line []byte) {
	for _, item := range lineItems(line) {
		if (item.Kind == kindReply || item.Kind == kindQuestion) && item.Time > state.reply {
			state.reply = item.Time
		}
		if item.Kind == kindNotice && item.Notice == noticeInterrupted && item.Time > state.interruption {
			state.interruption = item.Time
		}
	}
}
