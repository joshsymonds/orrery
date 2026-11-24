package open

import (
	"context"
	"errors"
)

// HostChoice is a host that a proposal may place a session on: its name, its load and memory use as percentages, and
// the folders it knows.
type HostChoice struct {
	Name          string
	LoadPercent   float64
	MemoryPercent float64
	Folders       []string
}

// Proposal is one place that a prompt may start a session: a host and a directory on it.
type Proposal struct {
	Host string
	Dir  string
}

// Propose asks the claude binary at claude which of hosts and their folders fit text, and returns the proposals best
// first.
func Propose(_ context.Context, _, _ string, _ []HostChoice) ([]Proposal, error) {
	return nil, errors.New("open: Propose is not built yet")
}

// KnownFolders returns the directories that the intent lookup knows, in the order Intent sends them to the model, as
// intentDirs lists them.
func KnownFolders(ctx context.Context, cfg Config) ([]string, error) {
	return intentDirs(ctx, cfg)
}
