import type { ComponentChildren, JSX } from "preact";

import { AskFirst, NowBlock, nowSentence } from "./blocker";
import type { LoadArtifact } from "./evidence";
import type { EpicHistoryEntry, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { JustHappened, NextRows, NowRows } from "./happenings";
import { Orrery } from "./orrery";
import { durationText, type Phase, requirementArcs, wedgesOf } from "./phases";
import { GreenProof, RedProof, Report, ReleaseProof } from "./proof";
import { type Blocker, blockerOf, stateOf } from "./status";
import { TitleRow } from "./titlerow";
import { Workflow } from "./workflow";

// EpicCommands are the epic's commands: pause, resume, stop and retry, and the answer to one open question.
export interface EpicCommands {
  pause: () => void;
  resume: () => void;
  stop: () => void;
  retry: () => void;
  answer: (questionId: string, text: string) => Promise<string>;
}

// blockerLine is the blocker's text for the title row: its text, and its time once it has begun.
function blockerLine(blocker: Blocker | null, now: number): string | undefined {
  if (blocker === null) {
    return undefined;
  }
  return blocker.since > 0 ? `${blocker.text} · ${durationText(now - blocker.since)}` : blocker.text;
}

// EpicPage is the one epic page: its title row with the epic's commands, the summary of its contract, the now sentence
// and blocker, the red proof, the implementation (the orrery, Now, Next, Just happened and the workflow), the green
// proof, the handover when given, the release proof and the report. A section the epic has not reached is dim. The
// commands are live only while the epic is live and has no outcome. Each phase click reaches onOpenPhase.
export function EpicPage(props: {
  detail: EpicsGetResponse;
  history: EpicHistoryEntry[];
  now: number;
  loadArtifact: LoadArtifact;
  commands: EpicCommands;
  onOpenContract: () => void;
  onBack: () => void;
  onOpenPhase?: (task: string, phase: Phase) => void;
  actions?: ComponentChildren;
  handover?: ComponentChildren;
}): JSX.Element {
  const { detail, history, now, loadArtifact, commands, onOpenContract, onBack, onOpenPhase, actions, handover } = props;
  const { epic, contract, requirements, steps, nodes } = detail;
  const wedges = wedgesOf(detail);
  const blocker = blockerOf(detail);
  const slug = epic?.slug ?? "";
  const live = epic?.live === true && epic.outcome === "";
  const released = epic !== undefined && epic.outcome.startsWith("released");
  return (
    <div class="epic-page">
      <TitleRow
        crumbs={[{ label: "orrery", onGo: onBack }, { label: slug }]}
        onBack={onBack}
        state={stateOf(detail)}
        blocker={blockerLine(blocker, now)}
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
      <NowBlock
        sentence={nowSentence(wedges)}
        blocker={blocker}
        slug={slug}
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
      <ReleaseProof contract={contract} released={released} />
      <Report />
    </div>
  );
}
