import type { JSX } from "preact";
import { useState } from "preact/hooks";

import { AskFirst } from "./blocker";
import { EvidenceRuns, type LoadArtifact } from "./evidence";
import type { EpicTaskReview, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { type Attempt, type CheckRound, type PhaseOutput, type PhaseRow, phaseOutputOf } from "./phaserows";
import { durationText, type Phase } from "./phases";
import { outcomeOf } from "./stepbox";
import { type Crumb, type InboxMark, TitleRow } from "./titlerow";

// PhaseOutputPage is one task's phase output: a title row with Hold and Stop… while a step runs, the task's title and
// requirement ids, and its four phases as rows with the given phase open. The open row shows its output and Open the
// session › when the phase names a session. A refused or unknown Hold or Stop shows one line under the title row. Every
// text is a child of its element, so no markup in a log, reason or claim becomes an element.
export function PhaseOutputPage(props: {
  detail: EpicsGetResponse;
  epic: string;
  task: string;
  phase: Phase;
  now: number;
  loadArtifact: LoadArtifact;
  onBack: () => void;
  onOpenPhase: (task: string, phase: Phase) => void;
  onOpenSession: (step: string) => void;
  hold: (step: string) => Promise<void>;
  stop: (step: string) => Promise<void>;
  mark?: InboxMark;
}): JSX.Element {
  const { detail, epic, task, phase, now, loadArtifact, onBack, onOpenPhase, onOpenSession, hold, stop, mark } = props;
  const [notice, setNotice] = useState("");
  const output = phaseOutputOf(detail, task);
  const running = output?.running;
  const crumbs: Crumb[] = [{ label: epic, onGo: onBack }, { label: `${task} · ${phase}` }];

  async function holdStep(step: string): Promise<void> {
    setNotice("");
    try {
      await hold(step);
    } catch (error) {
      setNotice(refusalOf("hold", error));
    }
  }

  async function stopStep(step: string): Promise<void> {
    setNotice("");
    try {
      await stop(step);
    } catch (error) {
      setNotice(refusalOf("stop", error));
    }
  }

  return (
    <div class="phase-output">
      <TitleRow crumbs={crumbs} onBack={onBack} mark={mark}>
        {running !== undefined && !running.held && (
          <button type="button" class="act" onClick={() => void holdStep(running.id)}>
            Hold
          </button>
        )}
        {running !== undefined && (
          <AskFirst
            label="Stop…"
            question={`Stop ${task}?`}
            confirm={`Stop ${task}`}
            keep="Keep it running"
            onConfirm={() => void stopStep(running.id)}
          />
        )}
      </TitleRow>
      {output !== null && (
        <section class="phase-task">
          <h2 class="task-title">{output.title}</h2>
          <p class="requirements">{output.requirements.join(", ")}</p>
        </section>
      )}
      {notice !== "" && (
        <p class="notice" role="alert">
          {notice}
        </p>
      )}
      {output !== null && (
        <ol class="phases">
          {output.rows.map((row) => (
            <PhaseRowItem
              key={row.phase}
              output={output}
              row={row}
              open={row.phase === phase}
              now={now}
              loadArtifact={loadArtifact}
              onOpenPhase={onOpenPhase}
              onOpenSession={onOpenSession}
            />
          ))}
        </ol>
      )}
    </div>
  );
}

// refusalOf is the line a rejected Hold or Stop shows: the server's message when it refused the call, else the call's
// outcome is not known.
function refusalOf(call: string, error: unknown): string {
  const outcome = outcomeOf(error);
  if (outcome.outcome === "refused") {
    return `${call} refused: ${outcome.message}`;
  }
  return `${call} not known: it may have landed`;
}

// timeOf is a phase row's time: a now row's time from its since to now, a passed row's seconds, and none for a row
// ahead. A now row with since 0 has no start to count from (the integrating row, which no step runs in), so it has none.
function timeOf(row: PhaseRow, now: number): string {
  switch (row.state) {
    case "now":
      return row.since === 0 ? "" : durationText(now - row.since);
    case "passed":
      return durationText(row.seconds);
    case "ahead":
      return "";
  }
}

// PhaseRowItem is one phase: its head, which opens the phase and is disabled while the phase is ahead, and the body of
// the open phase with its output and Open the session › when the phase names a session.
function PhaseRowItem(props: {
  output: PhaseOutput;
  row: PhaseRow;
  open: boolean;
  now: number;
  loadArtifact: LoadArtifact;
  onOpenPhase: (task: string, phase: Phase) => void;
  onOpenSession: (step: string) => void;
}): JSX.Element {
  const { output, row, open, now, loadArtifact, onOpenPhase, onOpenSession } = props;
  return (
    <li class={open ? `phase-row ${row.state} open` : `phase-row ${row.state}`}>
      <button
        type="button"
        class="phase-head"
        aria-expanded={open}
        disabled={row.state === "ahead"}
        onClick={() => onOpenPhase(output.task, row.phase)}
      >
        <span class="phase-name">{row.phase}</span>
        <span class="phase-summary">{row.summary}</span>
        <span class="phase-time">{timeOf(row, now)}</span>
      </button>
      {open && (
        <div class="phase-body">
          <BodyOf phase={row.phase} output={output} loadArtifact={loadArtifact} />
          {row.session !== "" && (
            <button type="button" class="act" onClick={() => onOpenSession(row.session)}>
              Open the session ›
            </button>
          )}
        </div>
      )}
    </li>
  );
}

// BodyOf is the output of one phase: the red run, the attempts and the green run when implementing; the rounds when
// checking; the reviews and reviewers when reviewing; and whether the task is integrated when integrating.
function BodyOf(props: { phase: Phase; output: PhaseOutput; loadArtifact: LoadArtifact }): JSX.Element {
  const { phase, output, loadArtifact } = props;
  switch (phase) {
    case "implementing":
      return <Implementing output={output} loadArtifact={loadArtifact} />;
    case "checking":
      return <Checking rounds={output.rounds} />;
    case "reviewing":
      return <Reviewing reviews={output.reviews} reviewers={output.reviewers} />;
    case "integrating":
      return <p class="integration">{output.integrated ? "integrated" : "waits to integrate"}</p>;
  }
}

function Implementing(props: { output: PhaseOutput; loadArtifact: LoadArtifact }): JSX.Element {
  const { output, loadArtifact } = props;
  return (
    <>
      {output.red !== undefined && <EvidenceRuns runs={[output.red]} loadArtifact={loadArtifact} />}
      <ol class="attempts">
        {output.attempts.map((attempt) => (
          <AttemptItem key={attempt.step} attempt={attempt} />
        ))}
      </ol>
      {output.green !== undefined && <EvidenceRuns runs={[output.green]} loadArtifact={loadArtifact} />}
    </>
  );
}

// AttemptItem is one attempt: its doing while it runs, else its end with its reason, or its summary when it has none.
function AttemptItem({ attempt }: { attempt: Attempt }): JSX.Element {
  if (attempt.end === "") {
    return (
      <li class="attempt">
        <p class="attempt-doing">{attempt.doing}</p>
      </li>
    );
  }
  return (
    <li class="attempt">
      <p class="attempt-end">{attempt.end}</p>
      <p class="attempt-text">{attempt.reason !== "" ? attempt.reason : attempt.summary}</p>
    </li>
  );
}

function Checking({ rounds }: { rounds: CheckRound[] }): JSX.Element {
  return (
    <ol class="rounds">
      {rounds.map((round) => (
        <li key={`${round.step}:${round.round}`} class="round">
          <p class="round-head">
            <span class="round-no">{`round ${round.round}`}</span> <span class="ran">{round.ran.join(", ")}</span>
          </p>
          <ul class="checks">
            {round.checks.map((check, index) => (
              <li key={index} class="check">
                <span class="check-name">{check.name}</span> <span class="check-result">{check.ok ? "✓" : "✗"}</span>
                {!check.ok && <span class="check-detail">{check.detail}</span>}
              </li>
            ))}
          </ul>
          {round.gate !== null && <GateOf gate={round.gate} />}
          {round.judge !== null && (
            <p class="judge">
              <span class="judge-verdict">{round.judge.verdict}</span>{" "}
              <span class="judge-reason">{round.judge.reason}</span>
            </p>
          )}
        </li>
      ))}
    </ol>
  );
}

// GateOf is a round's gate: its name and result, its detail, and the tails of its output that are not empty.
function GateOf({ gate }: { gate: NonNullable<CheckRound["gate"]> }): JSX.Element {
  return (
    <div class="gate">
      <p class="gate-head">
        <span class="gate-name">{gate.name}</span> <span class="gate-result">{gate.ok ? "passed" : "failed"}</span>
      </p>
      {gate.detail !== "" && <p class="gate-detail">{gate.detail}</p>}
      {gate.stdout !== "" && <pre class="gate-stdout">{gate.stdout}</pre>}
      {gate.stderr !== "" && <pre class="gate-stderr">{gate.stderr}</pre>}
    </div>
  );
}

function Reviewing(props: { reviews: EpicTaskReview[]; reviewers: Attempt[] }): JSX.Element {
  const { reviews, reviewers } = props;
  return (
    <>
      <ol class="reviews">
        {reviews.map((review, index) => (
          <li key={index} class="review">
            <p class="review-head">
              <span class="review-attempt">{`attempt ${review.attempt}`}</span>{" "}
              <span class="review-gate">{review.gate}</span> <span class="review-verdict">{review.verdict}</span>
            </p>
            <ul class="findings">
              {review.findings.map((finding) => (
                <li key={finding.id} class="finding">
                  <p class="claim">{finding.claim}</p>
                  <p class="location">{finding.location}</p>
                  <p class="result">{finding.result}</p>
                  <p class="reason">{finding.reason}</p>
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ol>
      <ul class="reviewers">
        {reviewers.map((reviewer) => (
          <li key={reviewer.step} class="reviewer">
            {reviewer.summary}
          </li>
        ))}
      </ul>
    </>
  );
}
