package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// lineageEscalatedRung is the highest rung from which --escalated keeps a task.
const lineageEscalatedRung = 3

// lineageConfirmed is the result of a finding that its verifier confirmed.
const lineageConfirmed = "confirmed"

// epicLineage implements `lineage [--epic ID] [--escalated]`. It prints a block for each task of the epics that has
// rungs: its header, its rungs, its versions and the findings its reviews confirmed. --epic ID reads that one epic;
// without it, every epic the service lists is read, in the order it lists them. --escalated keeps only the tasks whose
// highest rung is 3 or more. A wrong form is a usage error and calls nothing. A failed read returns its error and
// prints nothing.
func epicLineage(ctx context.Context, epics enginev1connect.EpicsClient, rest []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("epic lineage", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	epicID := flags.String("epic", "", "the epic whose task ladders to list; omit it to list every epic's")
	escalated := flags.Bool("escalated", false, "list only the tasks whose highest rung is 3 or more")
	if err := flags.Parse(rest); err != nil || flags.NArg() != 0 {
		return epicForm(epicLineageForm)
	}
	given := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if given["epic"] && *epicID == "" {
		return epicForm(epicLineageForm)
	}
	var ids []string
	if given["epic"] {
		ids = []string{*epicID}
	} else {
		listed, err := lineageListedIDs(ctx, epics)
		if err != nil {
			return err
		}
		ids = listed
	}
	var out strings.Builder
	for _, id := range ids {
		read, err := epics.GetEpic(ctx, connect.NewRequest(&v1.GetEpicRequest{Id: id}))
		if err != nil {
			return fmt.Errorf("epic lineage: read epic %s: %w", id, err)
		}
		lineageBlocks(&out, id, read.Msg.GetEpic(), *escalated)
	}
	if _, err := io.WriteString(stdout, out.String()); err != nil {
		return fmt.Errorf("epic lineage: write: %w", err)
	}
	return nil
}

// lineageListedIDs returns the ids of the epics the service lists, in the order it lists them.
func lineageListedIDs(ctx context.Context, epics enginev1connect.EpicsClient) ([]string, error) {
	listed, err := epics.ListEpics(ctx, connect.NewRequest(&v1.ListEpicsRequest{}))
	if err != nil {
		return nil, fmt.Errorf("epic lineage: list epics: %w", err)
	}
	summaries := listed.Msg.GetEpics()
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.GetId())
	}
	return ids, nil
}

// lineageBlocks adds to out the block of each task of epic that has rungs and that escalatedOnly keeps. epicID is the
// id the epic was read under.
func lineageBlocks(out *strings.Builder, epicID string, epic *v1.StoredEpic, escalatedOnly bool) {
	for _, task := range epic.GetTasks() {
		rungs := task.GetRungs()
		if len(rungs) == 0 {
			continue
		}
		highest := lineageHighestRung(rungs)
		if escalatedOnly && highest < lineageEscalatedRung {
			continue
		}
		fmt.Fprintf(out, "%s %s %s (%s, rung %d)\n", epicID, task.GetId(), task.GetTitle(), task.GetStatus(), highest)
		for _, rung := range rungs {
			fmt.Fprintln(out, lineageRungLine(rung))
		}
		for _, version := range task.GetVersions() {
			fmt.Fprintln(out, lineageVersionLine(version))
		}
		for _, review := range task.GetReviews() {
			for _, finding := range review.GetFindings() {
				if finding.GetResult() == lineageConfirmed {
					fmt.Fprintf(out, "  finding attempt %d %s %s: %s\n",
						review.GetAttempt(), review.GetGate(), finding.GetId(), finding.GetClaim())
				}
			}
		}
	}
}

// lineageHighestRung returns the highest rung number among rungs.
func lineageHighestRung(rungs []*v1.EpicTaskRung) int64 {
	var highest int64
	for _, rung := range rungs {
		highest = max(highest, rung.GetRung())
	}
	return highest
}

// lineageRungLine returns the line of a rung: its number and kind, then what moved the task onto the rung, the gate and
// assertion of the finding that did, its outcome and its reason, each part only when it is set.
func lineageRungLine(rung *v1.EpicTaskRung) string {
	var line strings.Builder
	fmt.Fprintf(&line, "  rung %d %s", rung.GetRung(), rung.GetKind())
	if rung.GetMovedBy() != "" {
		fmt.Fprintf(&line, " moved by %s: %s", rung.GetMovedBy(), rung.GetCause())
	}
	if rung.GetGate() != "" {
		fmt.Fprintf(&line, " (gate %s: %s)", rung.GetGate(), rung.GetAssertion())
	}
	if rung.GetOutcome() != "" {
		fmt.Fprintf(&line, " -> %s", rung.GetOutcome())
	}
	if rung.GetReason() != "" {
		fmt.Fprintf(&line, ": %s", rung.GetReason())
	}
	return line.String()
}

// lineageVersionLine returns the line of a version: its number, source, state and reason, then the fields it changes,
// when it changes any.
func lineageVersionLine(version *v1.EpicTaskVersion) string {
	var line strings.Builder
	fmt.Fprintf(&line, "  version %d %s %s: %s",
		version.GetVersion(), version.GetSource(), version.GetState(), version.GetReason())
	if changed := version.GetChanged(); len(changed) > 0 {
		fmt.Fprintf(&line, " [changed %s]", strings.Join(changed, ", "))
	}
	return line.String()
}
