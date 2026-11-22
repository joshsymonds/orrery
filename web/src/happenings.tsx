import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { EpicHistoryEntry, EpicStep, EpicStepCheck, EpicTurn, EvidenceRun } from "./gen/orrery/v1/epics_pb";
import { Icon } from "./glyphs";
import { clock } from "./reader";

// shownFirst is how many of the newest history entries show before the +n button opens the rest.
const shownFirst = 3;

// nameOf is a session's name in an epic's log line: its conversation's title in quotes when titleOf names it, else its id.
function nameOf(session: string, titleOf: ((session: string) => string | undefined) | undefined): string {
  const title = titleOf?.(session);
  return title === undefined ? `session ${session}` : `“${title}”`;
}

// makerOf names who made a move or a decision: you for the owner, orreryd for orreryd, else the conversation's name.
export function makerOf(by: string, session: string, titleOf: ((session: string) => string | undefined) | undefined): string {
  if (by === "owner") {
    return "you";
  }
  if (by === "orreryd") {
    return "orreryd";
  }
  return nameOf(session, titleOf);
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

// runResultsOf is an evidence run's results: its state while it has not ended, else whether it passed, its exit, and
// whether it timed out or was not captured.
function runResultsOf(run: EvidenceRun): string {
  if (run.state !== "ended") {
    return run.state;
  }
  const parts = [run.passed ? "passed" : "failed", `exit ${run.exit}`];
  if (run.timedOut) {
    parts.push("timed out");
  }
  if (run.notCaptured !== "") {
    parts.push(`not captured: ${run.notCaptured}`);
  }
  return parts.join(" · ");
}

// judgeOf is an evidence run's judge: its verdict, or none, and the reason it gave when it gave one.
function judgeOf(run: EvidenceRun): string {
  const verdict = run.verdict === "" ? "no verdict" : run.verdict;
  return run.judgeReason === "" ? `judge: ${verdict}` : `judge: ${verdict} · ${run.judgeReason}`;
}

// OpenStep is the button that opens the page of the step id names.
function OpenStep(props: { id: string; label: string; onOpenStep: (step: string) => void }): JSX.Element {
  const { id, label, onOpenStep } = props;
  return (
    <button type="button" class="open-step" onClick={() => onOpenStep(id)}>
      {label}
    </button>
  );
}

// CheckDetail is one check a step ran: its round, name and result, its detail, and the output tails of a failed check.
function CheckDetail(props: { check: EpicStepCheck }): JSX.Element {
  const { check } = props;
  return (
    <div class="check">
      <p>{`round ${check.round} · ${check.name} · ${check.ok ? "passed" : "failed"}`}</p>
      {check.detail !== "" && <p>{check.detail}</p>}
      {tailed(check) && <OutputTails stdout={check.stdout} stderr={check.stderr} />}
    </div>
  );
}

// checksOf is a step's checks, read from the epic's steps by the step's id.
function checksOf(id: string, steps: EpicStep[]): EpicStepCheck[] {
  return steps.find((candidate) => candidate.id === id)?.checks ?? [];
}

// StepDetail is a step's checks and the door to the step's page.
function StepDetail(props: {
  id: string;
  checks: EpicStepCheck[];
  onOpenStep: ((step: string) => void) | undefined;
}): JSX.Element {
  const { id, checks, onOpenStep } = props;
  return (
    <>
      {checks.map((check) => (
        <CheckDetail key={`${check.round}-${check.name}`} check={check} />
      ))}
      {onOpenStep !== undefined && <OpenStep id={id} label="Open the step" onOpenStep={onOpenStep} />}
    </>
  );
}

// TurnDetail is a turn's outline, then each decision the turn recorded in its order.
function TurnDetail(props: { turn: EpicTurn }): JSX.Element {
  const { outline, decisions } = props.turn;
  return (
    <>
      {outline !== "" && <p class="outline">{outline}</p>}
      {decisions.map((decision, index) => (
        <p key={index} class="decision">
          {decision}
        </p>
      ))}
    </>
  );
}

// RunDetail is an evidence run's results, its judge's verdict and reason, and the door to its judge step.
function RunDetail(props: { run: EvidenceRun; onOpenStep: ((step: string) => void) | undefined }): JSX.Element {
  const { run, onOpenStep } = props;
  return (
    <div class="run-result">
      <p>{runResultsOf(run)}</p>
      <p>{judgeOf(run)}</p>
      {run.judgeStep !== "" && onOpenStep !== undefined && (
        <OpenStep id={run.judgeStep} label="Open the judge step" onOpenStep={onOpenStep} />
      )}
    </div>
  );
}

// detailOf is an entry's detail, which its line opens to: a turn's outline and decisions, each run an evidence line
// groups, and a step's checks with its door. A decision, tell, trigger or action has no detail past its sentence, and
// neither has a turn with no outline or decisions, an evidence line with no runs, or a step with no checks when nothing
// opens it. Such a line does not open, so detailOf is null for those.
function detailOf(
  entry: EpicHistoryEntry,
  steps: EpicStep[],
  onOpenStep: ((step: string) => void) | undefined,
): JSX.Element | null {
  const { item } = entry;
  switch (item.case) {
    case "turn":
      return item.value.outline === "" && item.value.decisions.length === 0 ? null : <TurnDetail turn={item.value} />;
    case "step": {
      const checks = checksOf(item.value.id, steps);
      return checks.length === 0 && onOpenStep === undefined ? null : (
        <StepDetail id={item.value.id} checks={checks} onOpenStep={onOpenStep} />
      );
    }
    case "evidence":
      if (entry.runs.length === 0) {
        return null;
      }
      return (
        <>
          {entry.runs.map((run) => (
            <RunDetail key={run.id} run={run} onOpenStep={onOpenStep} />
          ))}
        </>
      );
    default:
      return null;
  }
}

// HistoryRow is one entry: its clock time and its server sentence. A line with detail opens in place on a click.
function HistoryRow(props: {
  entry: EpicHistoryEntry;
  steps: EpicStep[];
  onOpenStep: ((step: string) => void) | undefined;
}): JSX.Element {
  const { entry, steps, onOpenStep } = props;
  const [open, setOpen] = useState(false);
  const detail = detailOf(entry, steps, onOpenStep);
  const line = (
    <>
      <span class="at">{clock(entry.time)}</span>
      <span class="what">{entry.sentence}</span>
    </>
  );
  return (
    <li>
      {detail === null ? (
        <div class="happen-line">{line}</div>
      ) : (
        <button
          type="button"
          class="happen-line"
          aria-expanded={open ? "true" : "false"}
          onClick={() => setOpen(!open)}
        >
          {line}
        </button>
      )}
      {open && detail !== null && <div class="detail">{detail}</div>}
    </li>
  );
}

// JustHappened is the newest history entries, newest first: the first three, then a +n earlier button that opens the
// rest in place. A step's door, and an evidence run's judge step's, reach onOpenStep.
export function JustHappened(props: {
  entries: EpicHistoryEntry[];
  steps: EpicStep[];
  onOpenStep?: (step: string) => void;
}): JSX.Element {
  const { entries, steps, onOpenStep } = props;
  const [open, setOpen] = useState(false);
  const newest = [...entries].reverse();
  const shown = open ? newest : newest.slice(0, shownFirst);
  const earlier = newest.length - shown.length;
  return (
    <section class="happen just">
      <div class="sec" data-hue="1">
        <Icon name="retry" />
        <h3 class="name">Just happened</h3>
      </div>
      <ul>
        {shown.map((entry) => (
          <HistoryRow key={entry.cursor} entry={entry} steps={steps} onOpenStep={onOpenStep} />
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
