import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { EpicAction, EpicHistoryEntry, EpicStep, EpicStepCheck, EvidenceRun } from "./gen/orrery/v1/epics_pb";
import { durationText, type Phase, type Wedge } from "./phases";
import { clock } from "./reader";
import { OutputTails } from "./workflow";

// shownFirst is how many of the newest history entries show before the +n button opens the rest.
const shownFirst = 3;

// inFlight is whether a wedge is in a phase and running, or held by the owner.
function inFlight(wedge: Wedge): boolean {
  return (wedge.tone === "run" || wedge.tone === "mine") && wedge.place !== "planned" && wedge.place !== "integrated";
}

// nowText is a row's text: its task and phase, then how long it has been in the phase once the clock has started it.
function nowText(wedge: Wedge, now: number): string {
  const since = wedge.slices.find((slice) => slice.state === "now")?.since ?? 0;
  const head = `${wedge.task} · ${wedge.place}`;
  return since > 0 ? `${head} · ${durationText(now - since)}` : head;
}

// opening is a row's click handler: it opens the row's task at a phase, when the epic page gives a handler and the row
// has a phase. Otherwise the row is not clickable.
function opening(
  onOpenPhase: ((task: string, phase: Phase) => void) | undefined,
  task: string,
  phase: Phase | undefined,
): (() => void) | undefined {
  if (onOpenPhase === undefined || phase === undefined) {
    return undefined;
  }
  return () => onOpenPhase(task, phase);
}

// NowRows is the tasks in flight: each wedge in a phase that runs or is held by the owner, with its phase and how long
// it has been there. A row opens its now phase when onOpenPhase is given.
export function NowRows(props: {
  wedges: Wedge[];
  now: number;
  onOpenPhase?: (task: string, phase: Phase) => void;
}): JSX.Element {
  const { wedges, now, onOpenPhase } = props;
  return (
    <section class="happen now">
      <h3>Now</h3>
      <ul>
        {wedges.filter(inFlight).map((wedge) => {
          const phase = wedge.slices.find((slice) => slice.state === "now")?.phase;
          const onClick = opening(onOpenPhase, wedge.task, phase);
          return (
            <li
              key={wedge.task}
              class={onClick ? `t-${wedge.tone} open` : `t-${wedge.tone}`}
              role={onClick ? "button" : undefined}
              onClick={onClick}
            >
              {nowText(wedge, now)}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

// NextRows is the tasks not started: each planned wedge, with its title. A row opens its task's implementing phase when
// onOpenPhase is given.
export function NextRows(props: { wedges: Wedge[]; onOpenPhase?: (task: string, phase: Phase) => void }): JSX.Element {
  const { wedges, onOpenPhase } = props;
  return (
    <section class="happen next">
      <h3>Next</h3>
      <ul>
        {wedges
          .filter((wedge) => wedge.place === "planned")
          .map((wedge) => {
            const onClick = opening(onOpenPhase, wedge.task, "implementing");
            return (
              <li
                key={wedge.task}
                class={onClick ? "open" : undefined}
                role={onClick ? "button" : undefined}
                onClick={onClick}
              >
                {`${wedge.task} · ${wedge.title}`}
              </li>
            );
          })}
      </ul>
    </section>
  );
}

// runResultOf is an evidence run's result: running until it has ended, then passed or failed.
function runResultOf(run: EvidenceRun): string {
  if (run.state !== "ended") {
    return "running";
  }
  return run.passed ? "passed" : "failed";
}

// nameOf is a session's name in an epic's log line: its conversation's title in quotes when titleOf names it, else its id.
function nameOf(session: string, titleOf: ((session: string) => string | undefined) | undefined): string {
  const title = titleOf?.(session);
  return title === undefined ? `session ${session}` : `“${title}”`;
}

// moverOf names who made a move: you for the owner, orreryd for orreryd, and the conversation's name otherwise.
function moverOf(action: EpicAction, titleOf: ((session: string) => string | undefined) | undefined): string {
  if (action.by === "owner") {
    return "you";
  }
  if (action.by === "orreryd") {
    return "orreryd";
  }
  return nameOf(action.session, titleOf);
}

// moveOf is a move's log line: where the epic went, where it came from when it had a steerer, and who moved it.
function moveOf(action: EpicAction, titleOf: ((session: string) => string | undefined) | undefined): string {
  const from = action.from === "" ? "" : ` from ${nameOf(action.from, titleOf)}`;
  return `moved to ${nameOf(action.to, titleOf)}${from}, by ${moverOf(action, titleOf)}`;
}

// whatOf is an entry's text: its kind's label and its detail.
function whatOf(entry: EpicHistoryEntry, titleOf: ((session: string) => string | undefined) | undefined): string {
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
    case "action":
      return item.value.kind === "move" ? moveOf(item.value, titleOf) : "";
    case "tell":
      return item.value.text;
    default:
      return "";
  }
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
function HistoryRow(props: {
  entry: EpicHistoryEntry;
  steps: EpicStep[];
  titleOf: ((session: string) => string | undefined) | undefined;
}): JSX.Element {
  const { entry, steps, titleOf } = props;
  const { item } = entry;
  return (
    <li>
      <span class="at">{clock(entry.time)}</span>
      <span class="what">{whatOf(entry, titleOf)}</span>
      {item.case === "step" && <StepTails id={item.value.id} steps={steps} />}
    </li>
  );
}

// JustHappened is the newest history entries, newest first: the first three, then a +n earlier button that opens the
// rest in place.
export function JustHappened(props: {
  entries: EpicHistoryEntry[];
  steps: EpicStep[];
  titleOf?: (session: string) => string | undefined;
}): JSX.Element {
  const { entries, steps, titleOf } = props;
  const [open, setOpen] = useState(false);
  const newest = [...entries].reverse();
  const shown = open ? newest : newest.slice(0, shownFirst);
  const earlier = newest.length - shown.length;
  return (
    <section class="happen just">
      <h3>Just happened</h3>
      <ul>
        {shown.map((entry) => (
          <HistoryRow key={entry.cursor} entry={entry} steps={steps} titleOf={titleOf} />
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
