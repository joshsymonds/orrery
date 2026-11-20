import type { ComponentChildren, JSX } from "preact";
import { useState } from "preact/hooks";

import type { Conversation } from "./conversation";
import { Ctl } from "./ctl";
import { elapsed } from "./dockwords";
import { EvidenceRuns, type LoadArtifact } from "./evidence";
import type { EpicsGetResponse, EpicStep, EpicStepCheck } from "./gen/orrery/v1/epics_pb";
import type { Phase, Tone } from "./phases";
import { type RecordTask, type RingPlace, recordTasksOf } from "./record";
import type { Pill, WordTone } from "./recordwords";
import { CheckBlock } from "./reader";
import { outcomeOf } from "./refusal";
import { Sheet } from "./sheet";
import {
  type ImplementBody,
  type ReviewBody,
  type RunBody,
  type StageBodies,
  stageBodiesOf,
  type Words,
} from "./stagebodies";
import { ContextBand } from "./taskband";
import { type StageRow, stagesOf } from "./taskstages";
import { type Crumb, type InboxMark, TitleRow } from "./titlerow";

// PhaseOutputPage is one task's record: its title row with Interrupt and Stop… at the desk, the epic's band, the task's
// requirement lines and the planner's brief, then its five stage rows, the one open row drawing its body. Under narrow
// the commands are a foot bar, and Stop's confirm a sheet. Interrupt holds the running step and Stop stops it, each as one
// call; a refused or unknown call shows one line under the title row until a call lands. Every text is a child of its
// element, so no markup in a log, reason or claim becomes an element.
export function PhaseOutputPage(props: {
  detail: EpicsGetResponse;
  task: string;
  now: number;
  narrow: boolean;
  conversation: Conversation | null;
  loadArtifact: LoadArtifact;
  onBack: () => void;
  onOpenEpic: () => void;
  onOpenTask: (task: string, phase: Phase) => void;
  onOpenSession: (step: string) => void;
  hold: (step: string) => Promise<void>;
  stop: (step: string) => Promise<void>;
  mark?: InboxMark;
  strip?: ComponentChildren;
}): JSX.Element {
  const {
    detail,
    task,
    now,
    narrow,
    conversation,
    loadArtifact,
    onBack,
    onOpenEpic,
    onOpenTask,
    onOpenSession,
    hold,
    stop,
    mark,
    strip,
  } = props;
  const record = recordTasksOf(detail).find((candidate) => candidate.task === task);
  const [open, setOpen] = useState<RingPlace | undefined>(() =>
    record === undefined ? undefined : stagesOf(detail, record, now).open,
  );
  const [asking, setAsking] = useState(false);
  const [notice, setNotice] = useState("");
  const running = runningOf(detail, task);
  const title = record?.title ?? task;
  const pill = pillOf(record, now);
  const crumbs: Crumb[] = [{ label: detail.epic?.slug ?? "", onGo: onOpenEpic }, { label: title }];

  // sendCall sends one Interrupt or Stop. A refusal or an unknown outcome shows its line; a call that lands clears it.
  async function sendCall(name: string, send: () => Promise<void>): Promise<void> {
    try {
      await send();
      setNotice("");
    } catch (error) {
      setNotice(refusalOf(name, error));
    }
  }

  // stopStep closes the confirm and sends the one Stop of the step.
  function stopStep(step: string): void {
    setAsking(false);
    void sendCall("Stop", () => stop(step));
  }

  return (
    <div class="phase-output">
      <TitleRow
        crumbs={crumbs}
        onBack={onBack}
        state={narrow ? undefined : pill.word}
        stateTone={pill.tone}
        mark={mark}
        strip={strip}
        note={notice}
      >
        {!narrow && running !== undefined && !asking && (
          <>
            {!running.held && (
              <Ctl
                label="Interrupt"
                icon="stop"
                tone="bad"
                tip="Hold this step where it is"
                onClick={() => void sendCall("Interrupt", () => hold(running.id))}
              />
            )}
            <Ctl label="Stop…" tone="bad" onClick={() => setAsking(true)} />
          </>
        )}
        {!narrow && running !== undefined && asking && (
          <>
            <span class="sure">{`Stop ${title}?`}</span>
            <button type="button" class="act primary" onClick={() => stopStep(running.id)}>
              {`Stop ${title}`}
            </button>
            <button type="button" class="act" onClick={() => setAsking(false)}>
              Keep it running
            </button>
          </>
        )}
      </TitleRow>
      <ContextBand detail={detail} now={now} current={task} onOpenEpic={onOpenEpic} onOpenTask={onOpenTask} />
      {record !== undefined && (
        <TaskRecord
          detail={detail}
          record={record}
          now={now}
          open={open}
          onOpen={setOpen}
          conversation={conversation}
          loadArtifact={loadArtifact}
          onOpenSession={onOpenSession}
        />
      )}
      {narrow && (
        <div class="cmdbar">
          <span class={`state t-${pill.tone}`}>{pill.word}</span>
          {running !== undefined && (
            <>
              {!running.held && (
                <Ctl
                  label="Interrupt"
                  icon="stop"
                  tone="bad"
                  tip="Hold this step where it is"
                  onClick={() => void sendCall("Interrupt", () => hold(running.id))}
                />
              )}
              <Ctl only label="Stop this step…" icon="more" onClick={() => setAsking(true)} />
            </>
          )}
        </div>
      )}
      {narrow && asking && running !== undefined && (
        <Sheet title={`Stop ${title}?`} onClose={() => setAsking(false)}>
          <p>The task ends short and the planner decides what to do next.</p>
          <div class="foot">
            <button type="button" class="act" onClick={() => setAsking(false)}>
              Keep it running
            </button>
            <button type="button" class="act danger" onClick={() => stopStep(running.id)}>
              {`Stop ${title}`}
            </button>
          </div>
        </Sheet>
      )}
    </div>
  );
}

// TaskRecord is the part of a task's record under its band: the requirement lines, the planner's brief when its
// conversation holds one, and the five stage rows with the open row's body.
function TaskRecord(props: {
  detail: EpicsGetResponse;
  record: RecordTask;
  now: number;
  open: RingPlace | undefined;
  onOpen: (stage: RingPlace | undefined) => void;
  conversation: Conversation | null;
  loadArtifact: LoadArtifact;
  onOpenSession: (step: string) => void;
}): JSX.Element | null {
  const { detail, record, now, open, onOpen, conversation, loadArtifact, onOpenSession } = props;
  const bodies = stageBodiesOf(detail, record.task);
  if (bodies === null) {
    return null;
  }
  const stages = stagesOf(detail, record, now);
  const brief = briefOf(conversation);
  const latestChecks = detail.steps.find((step) => step.id === bodies.implement.session)?.checks ?? [];
  return (
    <>
      {record.requirements.map((id) => (
        <RequirementLine key={id} id={id} text={requirementTextOf(detail, id)} />
      ))}
      {brief !== undefined && (
        <div class="brief">
          <span class="lbl">The planner’s brief</span>
          <p>{brief}</p>
        </div>
      )}
      {stages.rows.map((row) => (
        <StageItem
          key={row.stage}
          row={row}
          open={open === row.stage}
          onToggle={() => onOpen(open === row.stage ? undefined : row.stage)}
        >
          <StageBody
            stage={row.stage}
            bodies={bodies}
            checks={latestChecks}
            loadArtifact={loadArtifact}
            onOpenSession={onOpenSession}
          />
        </StageItem>
      ))}
    </>
  );
}

// requirementTextOf is a requirement's text as the epic lists it, undefined when it is not listed.
function requirementTextOf(detail: EpicsGetResponse, id: string): string | undefined {
  return detail.requirements.find((requirement) => requirement.id === id)?.text;
}

// RequirementLine is one requirement the task proves: its id, then its text when the epic lists it.
function RequirementLine({ id, text }: { id: string; text: string | undefined }): JSX.Element {
  return (
    <p class="now-sentence">
      <span class="id">{id}</span>
      {text !== undefined && ` ${text}`}
    </p>
  );
}

// StageItem is one stage row: its head, which opens the row and is disabled while the row is ahead, and the row's body
// while it is open.
function StageItem({
  row,
  open,
  onToggle,
  children,
}: {
  row: StageRow;
  open: boolean;
  onToggle: () => void;
  children: ComponentChildren;
}): JSX.Element {
  return (
    <div class={row.tone === undefined ? `phase ${row.state}` : `phase ${row.state} ${row.tone}`}>
      <button
        type="button"
        class="phase-head"
        aria-expanded={open}
        disabled={row.state === "ahead"}
        onClick={() => {
          if (row.state !== "ahead") {
            onToggle();
          }
        }}
      >
        <span class="nm">{row.stage}</span>
        <span class="sum">{row.summary}</span>
        <span class="end">{row.end}</span>
      </button>
      {open && <div class="phase-body">{children}</div>}
    </div>
  );
}

// StageBody is one stage's body, as the stage's bodies draw it.
function StageBody({
  stage,
  bodies,
  checks,
  loadArtifact,
  onOpenSession,
}: {
  stage: RingPlace;
  bodies: StageBodies;
  checks: EpicStepCheck[];
  loadArtifact: LoadArtifact;
  onOpenSession: (step: string) => void;
}): JSX.Element {
  switch (stage) {
    case "red":
      return <RunView body={bodies.red} loadArtifact={loadArtifact} />;
    case "implement":
      return <ImplementView body={bodies.implement} checks={checks} onOpenSession={onOpenSession} />;
    case "green":
      return (
        <>
          <span class="sub">Reproduction</span>
          <WordsLine words={bodies.green.reproduction} />
          <span class="sub">Green run</span>
          <RunView body={bodies.green.run} loadArtifact={loadArtifact} />
        </>
      );
    case "review":
      return <ReviewView body={bodies.review} onOpenSession={onOpenSession} />;
    case "integrate":
      return <WordsLine words={bodies.integrate} />;
  }
}

// RunView draws an evidence run, or the quiet line that says there is none.
function RunView({ body, loadArtifact }: { body: RunBody; loadArtifact: LoadArtifact }): JSX.Element {
  return body.kind === "run" ? (
    <EvidenceRuns runs={[body.run]} loadArtifact={loadArtifact} />
  ) : (
    <WordsLine words={body.line} />
  );
}

// WordsLine is a line of words in its tone.
function WordsLine({ words }: { words: Words }): JSX.Element {
  return <p class={`inote ${words.tone}`}>{words.text}</p>;
}

// ImplementView is the implement body: each attempt, then the latest attempt's check rounds, its gate's log and its judge,
// then the door to its session.
function ImplementView({
  body,
  checks,
  onOpenSession,
}: {
  body: ImplementBody;
  checks: EpicStepCheck[];
  onOpenSession: (step: string) => void;
}): JSX.Element {
  const { latest } = body;
  return (
    <>
      <span class="sub">Attempts</span>
      <ol class="attempts">
        {body.attempts.map((attempt) => (
          <li key={attempt.step} class="attempt">
            <span class="n">{attempt.attempt}</span>
            <span class="words">{attempt.words}</span>
            <span class={`chip ${attempt.chip.tone}`}>{attempt.chip.text}</span>
          </li>
        ))}
      </ol>
      {latest !== null && (
        <>
          <span class="sub">{`Attempt ${latest.attempt} · check rounds`}</span>
          {latest.rounds.map((round) => (
            <CheckBlock key={round.round} text={round.text} round={round.round} checks={checks} />
          ))}
          {latest.gate !== null && (
            <>
              <span class="sub">{`Gate · ${latest.gate.name}`}</span>
              {latest.gate.log !== "" && <div class="log">{latest.gate.log}</div>}
            </>
          )}
          <span class="sub">Judge</span>
          <WordsLine words={latest.judge} />
        </>
      )}
      {body.session !== null && <SessionDoor step={body.session} onOpenSession={onOpenSession} />}
    </>
  );
}

// ReviewView is the review body: a table of each review's findings, or the line that none has run, then the door to the
// latest reading step's session.
function ReviewView({
  body,
  onOpenSession,
}: {
  body: ReviewBody;
  onOpenSession: (step: string) => void;
}): JSX.Element {
  return (
    <>
      {body.empty !== null ? (
        <WordsLine words={body.empty} />
      ) : (
        <div class="tbl">
          <div class="tr head">
            <span>gate</span>
            <span>finding</span>
            <span>where</span>
            <span>what happened</span>
          </div>
          {body.rows.map((row, index) => (
            <div key={index} class="tr">
              <span class="gate">{row.gate}</span>
              <span class="what wide">{row.what}</span>
              <span class="id">{row.where}</span>
              <span class="res wide">
                <span class={`chip ${row.result.tone}`}>{row.result.text}</span> {row.reason}
              </span>
            </div>
          ))}
        </div>
      )}
      {body.session !== null && <SessionDoor step={body.session} onOpenSession={onOpenSession} />}
    </>
  );
}

// SessionDoor opens a step's worker session.
function SessionDoor({ step, onOpenSession }: { step: string; onOpenSession: (step: string) => void }): JSX.Element {
  return (
    <button type="button" class="door" onClick={() => onOpenSession(step)}>
      <span>Open the session</span>
      <span class="chev" aria-hidden="true">
        ›
      </span>
    </button>
  );
}

// briefStepOf is the step whose conversation holds the task's planner's brief: the task's first implementer step in
// started order, else its first step that is not read-only. Undefined when the task has no step.
export function briefStepOf(detail: EpicsGetResponse, task: string): EpicStep | undefined {
  const steps = stepsOf(detail, task);
  return steps.find((step) => step.role === "implementer") ?? steps.find((step) => !step.readOnly);
}

// briefOf is the planner's brief: the text of the conversation's first prompt sent as the brief, undefined while the
// conversation holds none.
function briefOf(conversation: Conversation | null): string | undefined {
  return conversation?.items.find((item) => item.kind === "prompt" && item.sender === "brief")?.text;
}

// stepsOf is a task's steps in started order.
function stepsOf(detail: EpicsGetResponse, task: string): EpicStep[] {
  return detail.steps
    .filter((step) => step.task === task)
    .sort((left, right) => Number(left.started - right.started));
}

// runningOf is a task's running step: its latest step, when that step has not ended.
function runningOf(detail: EpicsGetResponse, task: string): EpicStep | undefined {
  const latest = stepsOf(detail, task).at(-1);
  return latest !== undefined && latest.end === "" ? latest : undefined;
}

// pillOf is the state pill: while a step runs, the task's place and its clock; else the task's words. Both are in the task's
// tone. A task that is not in the epic has no words.
function pillOf(record: RecordTask | undefined, now: number): Pill {
  if (record === undefined) {
    return { word: "", tone: "quiet" };
  }
  const tone = wordToneOf(record.tone);
  if (record.since > 0) {
    return { word: `${record.place} · ${elapsed(now - record.since)}`, tone };
  }
  return { word: record.words, tone };
}

// wordToneOf is a task's tone as a state pill's word tone: a running step live, a held one attention, an accepted task
// engine, and a planned or limited task quiet.
function wordToneOf(tone: Tone): WordTone {
  switch (tone) {
    case "run":
      return "live";
    case "mine":
      return "attention";
    case "acc":
      return "engine";
    case "ok":
      return "ok";
    case "bad":
      return "bad";
    case "plan":
    case "limited":
      return "quiet";
  }
}

// refusalOf is the line a refused or unknown Interrupt or Stop shows: the server's reason with a full stop when it has none,
// else that the call may have landed.
function refusalOf(call: string, error: unknown): string {
  const outcome = outcomeOf(error);
  if (outcome.outcome === "refused") {
    const reason = outcome.message.endsWith(".") ? outcome.message : `${outcome.message}.`;
    return `${call} was refused: ${reason}`;
  }
  return `${call} may have landed: no answer came.`;
}
