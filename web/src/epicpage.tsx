import type { ComponentChildren, JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import { AskFirst, NowBlock, nowSentence } from "./blocker";
import type { LoadArtifact } from "./evidence";
import type { EpicHistoryEntry, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { JustHappened, NextRows, NowRows } from "./happenings";
import { Orrery } from "./orrery";
import { durationText, type Phase, requirementArcs, wedgesOf } from "./phases";
import { GreenProof, RedProof, Report, ReleaseProof } from "./proof";
import { type Blocker, blockerOf, stateOf } from "./status";
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

// EpicPage is the one epic page: its title row with the epic's commands, the summary of its contract, the now sentence
// and blocker, the red proof, the implementation (the orrery, Now, Next, Just happened and the workflow), the green
// proof, the handover when given, the release proof and the report. A section the epic has not reached is dim. The
// commands are live only while the epic is live and has no outcome. Each phase click reaches onOpenPhase. A landing
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
  onBack: () => void;
  onOpenPhase?: (task: string, phase: Phase) => void;
  actions?: ComponentChildren;
  handover?: ComponentChildren;
  landing?: Landing;
  mark?: InboxMark;
  strip?: ComponentChildren;
}): JSX.Element {
  const {
    detail,
    history,
    now,
    loadArtifact,
    commands,
    numberKeys,
    onOpenContract,
    onBack,
    onOpenPhase,
    actions,
    handover,
    landing,
    mark,
    strip,
  } = props;
  const { epic, contract, requirements, steps, nodes, releaseSteps, report } = detail;
  const wedges = wedgesOf(detail);
  const blocker = blockerOf(detail);
  const slug = epic?.slug ?? "";
  const live = epic?.live === true && epic.outcome === "";
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
        {live &&
          (detail.paused ? (
            <button type="button" class="act" onClick={() => commands.resume()}>
              Resume
            </button>
          ) : (
            <button type="button" class="act" onClick={() => commands.pause()}>
              Pause
            </button>
          ))}
        {live && (
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
        <JustHappened entries={history} steps={steps} />
        {nodes.length > 0 && <Workflow nodes={nodes} loadArtifact={loadArtifact} />}
      </section>
      <GreenProof requirements={requirements} wedges={wedges} loadArtifact={loadArtifact} onOpenPhase={onOpenPhase} />
      {handover !== undefined && handover !== null && <section class="handover">{handover}</section>}
      <ReleaseProof contract={contract} releaseSteps={releaseSteps} released={released} />
      <Report report={report} />
    </div>
  );
}
