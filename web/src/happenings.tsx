import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { EpicHistoryEntry, EpicStep, EpicStepCheck, EvidenceRun } from "./gen/orrery/v1/epics_pb";
import { clock } from "./reader";

// shownFirst is how many of the newest history entries show before the +n button opens the rest.
const shownFirst = 3;

// runResultOf is an evidence run's result: running until it has ended, then passed or failed.
function runResultOf(run: EvidenceRun): string {
  if (run.state !== "ended") {
    return "running";
  }
  return run.passed ? "passed" : "failed";
}

// whatOf is an entry's text: its kind's label and its detail.
function whatOf(entry: EpicHistoryEntry): string {
  const { item } = entry;
  switch (item.case) {
    case "turn": {
      const { seq, state, outcome } = item.value;
      const head = `turn ${seq} · ${state}`;
      return outcome === "" ? head : `${head} · ${outcome}`;
    }
    case "trigger":
      return `trigger · ${item.value.cause}`;
    case "step": {
      const { task, agent, end } = item.value;
      return `${task !== "" ? task : agent} · ${end !== "" ? end : "started"}`;
    }
    case "evidence":
      return `${item.value.item} ${item.value.phase} run · ${runResultOf(item.value)}`;
    case "decision":
      return `decision · ${item.value.text}`;
    case "tell":
      return item.value.text;
    default:
      return "";
  }
}

// OutputTails is a check's stdout and stderr tails: each non-empty tail under its label, preformatted.
export function OutputTails(props: { stdout: string; stderr: string }): JSX.Element {
  const { stdout, stderr } = props;
  return (
    <div class="tails">
      {stdout !== "" && (
        <>
          <span class="tail-label">stdout</span>
          <pre class="log">{stdout}</pre>
        </>
      )}
      {stderr !== "" && (
        <>
          <span class="tail-label">stderr</span>
          <pre class="log">{stderr}</pre>
        </>
      )}
    </div>
  );
}

// tailed is whether a check failed with output to show. A passed check shows none, whatever it carries.
function tailed(check: EpicStepCheck): boolean {
  return !check.ok && (check.stdout !== "" || check.stderr !== "");
}

// StepTails is the output tails of a step's failed checks, read from the epic's steps by the step's id.
function StepTails(props: { id: string; steps: EpicStep[] }): JSX.Element {
  const { id, steps } = props;
  const checks = steps.find((candidate) => candidate.id === id)?.checks ?? [];
  return (
    <>
      {checks.filter(tailed).map((check) => (
        <OutputTails key={`${check.round}-${check.name}`} stdout={check.stdout} stderr={check.stderr} />
      ))}
    </>
  );
}

// HistoryRow is one entry: its clock time and its text, then a step's output tails.
function HistoryRow(props: { entry: EpicHistoryEntry; steps: EpicStep[] }): JSX.Element {
  const { entry, steps } = props;
  const { item } = entry;
  return (
    <li>
      <span class="at">{clock(entry.time)}</span>
      <span class="what">{whatOf(entry)}</span>
      {item.case === "step" && <StepTails id={item.value.id} steps={steps} />}
    </li>
  );
}

// JustHappened is the newest history entries, newest first: the first three, then a +n earlier button that opens the
// rest in place.
export function JustHappened(props: { entries: EpicHistoryEntry[]; steps: EpicStep[] }): JSX.Element {
  const { entries, steps } = props;
  const [open, setOpen] = useState(false);
  const newest = [...entries].reverse();
  const shown = open ? newest : newest.slice(0, shownFirst);
  const earlier = newest.length - shown.length;
  return (
    <section class="happen just">
      <h3>Just happened</h3>
      <ul>
        {shown.map((entry) => (
          <HistoryRow key={entry.cursor} entry={entry} steps={steps} />
        ))}
      </ul>
      {earlier > 0 && (
        <button type="button" class="more" onClick={() => setOpen(true)}>
          {`+${earlier} earlier`}
        </button>
      )}
    </section>
  );
}
