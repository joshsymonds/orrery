package open

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/summary"
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

// proposeInstruction states the reply grammar. TEXT and the host lines follow it on stdin.
const proposeInstruction = `Choose the hosts and folders that fit TEXT below and rank them best first.
Reply with one line per proposal, in the form <host name> <folder path>, using only the hosts and folders listed
below. Reply with nothing else.
`

// Propose asks the claude binary at claude which of hosts and their folders fit text, and returns the proposals best
// first. It runs the summary model as Intent does, with the same argv, environment and timeout. A reply line is kept
// when it is exactly one host's name, a space and one of that host's Folders, and a proposal already kept is dropped.
// Blank text or no hosts is an error before the model runs, and so is a reply that keeps no line.
func Propose(ctx context.Context, claude, text string, hosts []HostChoice) ([]Proposal, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("propose: text is blank")
	}
	if len(hosts) == 0 {
		return nil, errors.New("propose: no hosts")
	}
	reply, err := runClaude(ctx, claude, proposeStdin(text, hosts), summary.DefaultTimeout)
	if err != nil {
		return nil, err
	}
	proposals := parsePropose(reply, hosts)
	if len(proposals) == 0 {
		return nil, errors.New("propose: the model chose no valid proposal")
	}
	return proposals, nil
}

// proposeStdin is the instruction, then TEXT, then for each host a line naming it with its load and memory, followed
// by its folders, one per line.
func proposeStdin(text string, hosts []HostChoice) string {
	var out strings.Builder
	out.WriteString(proposeInstruction)
	out.WriteString("\nTEXT:\n" + text + "\n")
	out.WriteString("\nHOSTS:\n")
	for _, host := range hosts {
		fmt.Fprintf(&out, "host %s load %s%% memory %s%%\n",
			host.Name, percentText(host.LoadPercent), percentText(host.MemoryPercent))
		for _, dir := range host.Folders {
			out.WriteString(dir + "\n")
		}
	}
	return out.String()
}

// percentText writes p in decimal notation with no more digits than it needs.
func percentText(p float64) string {
	return strconv.FormatFloat(p, 'f', -1, 64)
}

// parsePropose returns the proposals reply names, in reply order. A line is kept when it is exactly the text
// "<host name> <folder>" for one of hosts and one of that host's Folders; a host and folder pair already kept is
// dropped. When two pairs write the same text, the earlier one in hosts wins.
func parsePropose(reply string, hosts []HostChoice) []Proposal {
	known := make(map[string]Proposal)
	for _, host := range hosts {
		for _, dir := range host.Folders {
			line := host.Name + " " + dir
			if _, ok := known[line]; !ok {
				known[line] = Proposal{Host: host.Name, Dir: dir}
			}
		}
	}
	var proposals []Proposal
	kept := make(map[Proposal]bool)
	for _, line := range strings.Split(reply, "\n") {
		proposal, ok := known[strings.TrimSpace(line)]
		if !ok || kept[proposal] {
			continue
		}
		kept[proposal] = true
		proposals = append(proposals, proposal)
	}
	return proposals
}

// KnownFolders returns the directories that the intent lookup knows, in the order Intent sends them to the model, as
// intentDirs lists them.
func KnownFolders(ctx context.Context, cfg Config) ([]string, error) {
	return intentDirs(ctx, cfg)
}
