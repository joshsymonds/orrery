import type { ComponentChildren, JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import { AskFirst, NowBlock, nowSentence } from "./blocker";
import type { LoadArtifact } from "./evidence";
import type { EpicHistoryEntry, EpicSession, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { JustHappened, NextRows, NowRows } from "./happenings";
import { Orrery } from "./orrery";
import { durationText, type Phase, requirementArcs, wedgesOf } from "./phases";
import { GreenProof, RedProof, Report, ReleaseProof } from "./proof";
import type { Row } from "./rows";
import { type Blocker, blockerOf, controlsOf, stateOf } from "./status";
import { steererRowOf } from "./steer";
import { listedTitle, TitleParts } from "./titleparts";
import { type InboxMark, TitleRow } from "./titlerow";
import { Workflow } from "./workflow";

// EpicCommands are the epic's commands: pause, resume, stop and retry, and the answer to one open question: the choice
// the owner picked, which is a declared choice's id or other, and its text.
export interface EpicCommands {
  pause: () => void;
  resume: () => void;
  stop: () => void;
  retry: () => void;
  answer: (questionId: string, choice: string, text: string) => Promise<string>;
}

// SessionTarget is what a session opens: a pane, or a session by its id, with the served label that names it when no
// listed row does.
export interface SessionTarget {
  kind: "pane" | "session";
  id: string;
  label: string;
}

// sessionTarget is the target a session opens: its pane when it has one, else the session. Its label is the session's
// served label alone.
export function sessionTarget(session: EpicSession): SessionTarget {
  return session.pane !== ""
    ? { kind: "pane", id: session.pane, label: session.label }
    : { kind: "session", id: session.id, label: session.label };
}

// blockerLine is the blocker's text for the title row: its text, and its time once it has begun.
export function blockerLine(blocker: Blocker | null, now: number): string | undefined {
  if (blocker === null) {
    return undefined;
  }
  return blocker.since > 0 ? `${blocker.text} · ${durationText(now - blocker.since)}` : blocker.text;
}

// stateLine is the epic's state for the title row: its state word, and its time once its phase has begun, never below
// zero when the browser's clock is behind the server's stamp.
export function stateLine(detail: EpicsGetResponse, now: number): string {
  const state = stateOf(detail);
  const phaseAt = Number(detail.epic?.phaseAt ?? 0);
  return state !== "" && phaseAt > 0 ? `${state} · ${durationText(Math.max(0, now - phaseAt))}` : state;
}

// creatorOf is the session that created the epic, the one its epic_create call made. When more than one is marked, it
// is the one whose newest call is latest. It is undefined when no listed session is the creator.
function creatorOf(sessions: EpicSession[]): EpicSession | undefined {
  let creator: EpicSession | undefined;
  for (const session of sessions) {
    if (session.creator && (creator === undefined || session.at > creator.at)) {
      creator = session;
    }
  }
  return creator;
}

// Landing is the block an opened notice lands the epic page on, by its kind. Each notice makes its own, so the page
// brings the block into view once for each. The empty kind names no block.
export interface Landing {
  kind: string;
}

// landingTargets names the part of the epic page each block kind brings into view. A kind it does not name, the empty
// one included, brings the page's top into view.
const landingTargets: Partial<Record<string, string>> = {
  question: ".now-block",
  release_step: ".now-block",
  report: ".proof.report",
};

// EpicPage is the one epic page: its title row with the epic's commands, the summary of its contract, the Conversation
// line to the session that created the epic, the now sentence and blocker, the red proof, the implementation (the
// orrery, Now, Next, Just happened and the workflow), the green proof, the handover when given, the release proof and
// the report. A section the epic has not reached is dim. The commands are the ones controlsOf shows for the epic. Each
// phase click reaches onOpenPhase. The Conversation line draws the creator's title, its listed row's
// directory and line from rows through TitleParts, and reaches onOpenSession with the creator's target. A landing
// brings its part of the page into view once the page is drawn. numberKeys is whether a digit key picks an option of
// the open question.
export function EpicPage(props: {
  detail: EpicsGetResponse;
  history: EpicHistoryEntry[];
  now: number;
  loadArtifact: LoadArtifact;
  commands: EpicCommands;
  numberKeys: boolean;
  onOpenContract: () => void;
  onOpenSession: (target: SessionTarget) => void;
  onBack: () => void;
  onOpenPhase?: (task: string, phase: Phase) => void;
  actions?: ComponentChildren;
  handover?: ComponentChildren;
  landing?: Landing;
  mark?: InboxMark;
  strip?: ComponentChildren;
  rows?: Row[];
}): JSX.Element {
  const {
    detail,
    history,
    now,
    loadArtifact,
    commands,
    numberKeys,
    onOpenContract,
    onOpenSession,
    onBack,
    onOpenPhase,
    actions,
    handover,
    landing,
    mark,
    strip,
    rows = [],
  } = props;
  const { epic, contract, requirements, steps, nodes, releaseSteps, report, sessions } = detail;
  const wedges = wedgesOf(detail);
  const blocker = blockerOf(detail);
  const slug = epic?.slug ?? "";
  const controls = controlsOf(detail);
  const creator = creatorOf(sessions);
  // titleOf names a session by its listed row's label, for the move lines of the epic's log.
  const titleOf = (session: string) => steererRowOf(rows, session)?.label;
  const creatorTarget = creator === undefined ? undefined : sessionTarget(creator);
  const creatorTitle =
    creatorTarget === undefined ? undefined : listedTitle(rows, creatorTarget.kind, creatorTarget.id, creatorTarget.label);
  const released = epic !== undefined && epic.outcome.startsWith("released");
  const pageRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const page = pageRef.current;
    if (landing === undefined || page === null) {
      return;
    }
    const target = landingTargets[landing.kind];
    const element = target === undefined ? page : page.querySelector<HTMLElement>(target);
    element?.scrollIntoView();
  }, [landing]);
  return (
    <div class="epic-page" ref={pageRef}>
      <TitleRow
        crumbs={[{ label: slug }]}
        onBack={onBack}
        state={stateLine(detail, now)}
        blocker={blockerLine(blocker, now)}
        mark={mark}
        strip={strip}
      >
        {actions}
        {controls.resume && (
          <button type="button" class="act" onClick={() => commands.resume()}>
            Resume
          </button>
        )}
        {controls.pause && (
          <button type="button" class="act" onClick={() => commands.pause()}>
            Pause
          </button>
        )}
        {controls.stop && (
          <AskFirst
            label="Stop…"
            question={`Stop ${slug}?`}
            confirm={`Stop ${slug}`}
            keep="Keep it running"
            onConfirm={() => commands.stop()}
          />
        )}
      </TitleRow>
      {contract !== undefined && (
        <button type="button" class="summary" onClick={() => onOpenContract()}>
          {detail.summary !== "" ? detail.summary : contract.title}
        </button>
      )}
      {creatorTarget !== undefined && creatorTitle !== undefined && (
        <button type="button" class="summary conversation" onClick={() => onOpenSession(creatorTarget)}>
          {"Conversation · "}
          <TitleParts dir={creatorTitle.dir} line={creatorTitle.line} />
        </button>
      )}
      {epic !== undefined && epic.problem !== "" && <p class="problem error">{epic.problem}</p>}
      <NowBlock
        sentence={nowSentence(wedges)}
        blocker={blocker}
        slug={slug}
        numberKeys={numberKeys}
        onAnswer={commands.answer}
        onRetry={() => commands.retry()}
        onStop={() => commands.stop()}
      />
      <RedProof requirements={requirements} wedges={wedges} loadArtifact={loadArtifact} onOpenPhase={onOpenPhase} />
      <section class="implementation">
        <Orrery wedges={wedges} arcs={requirementArcs(wedges, requirements)} now={now} onOpenPhase={onOpenPhase} />
        <NowRows wedges={wedges} now={now} onOpenPhase={onOpenPhase} />
        <NextRows wedges={wedges} onOpenPhase={onOpenPhase} />
        <JustHappened entries={history} steps={steps} titleOf={titleOf} />
        {nodes.length > 0 && <Workflow nodes={nodes} loadArtifact={loadArtifact} />}
      </section>
      <GreenProof requirements={requirements} wedges={wedges} loadArtifact={loadArtifact} onOpenPhase={onOpenPhase} />
      {handover !== undefined && handover !== null && <section class="handover">{handover}</section>}
      <ReleaseProof contract={contract} releaseSteps={releaseSteps} released={released} />
      <Report report={report} />
    </div>
  );
}
