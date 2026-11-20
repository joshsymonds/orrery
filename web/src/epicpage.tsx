import type { ComponentChildren, JSX } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import type { LoadArtifact } from "./evidence";
import type { EpicHistoryEntry, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { JustHappened } from "./happenings";
import { NowSection } from "./nowsection";
import { Orrery } from "./orrery";
import { durationText, type Phase, provenOf, wedgesOf } from "./phases";
import { recordTasksOf } from "./record";
import { blockerPillOf, contractDoorOf, reachedOf, statePillOf, trackOf } from "./recordwords";
import { RequirementTable } from "./reqtable";
import type { Row } from "./rows";
import { Sheet } from "./sheet";
import { type Blocker, controlsOf, stateOf } from "./status";
import { TaskTable } from "./tasktable";
import type { TalkTarget } from "./talk";
import { listedTitle, TitleParts } from "./titleparts";
import { type InboxMark, TitleRow } from "./titlerow";
import { Track } from "./track";

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
  question: "section.now",
  release_step: "section.now",
};

// SteeredFrom is the Steered from door: the session that steers the epic, titled from its listed row the way app's
// steererOf finds that row, and the opening of that conversation. A steerer no row lists is opened by its id, and its
// title is the word session.
function SteeredFrom({
  steering,
  rows,
  onOpen,
}: {
  steering: string;
  rows: Row[];
  onOpen: (target: TalkTarget) => void;
}): JSX.Element {
  const row = rows.find((candidate) => candidate.archiveKind !== "none" && candidate.archiveId === steering);
  const target: TalkTarget =
    row === undefined ? { kind: "session", id: steering, label: "" } : { kind: row.kind, id: row.id, label: row.label };
  const title = listedTitle(rows, target.kind, target.id, target.label);
  return (
    <button type="button" class="door steered" onClick={() => onOpen(target)}>
      <span>Steered from</span> <TitleParts dir={title.dir} line={title.line} />
    </button>
  );
}

// EpicPage is the one epic page, drawn section by section in the order of the record: the title row with the epic's
// state and blocker pills and its commands, and under it the did-not-land line of a failed command. Then, for an
// orreryd epic: its problem, its summary with the Contract door and the Steered from door, its track, Now, Just
// happened, the tasks with their chart and table, and the requirements. A record epic draws only Now and its tasks. A
// section the epic has not reached is not drawn. The commands are the ones controlsOf shows for the epic. Each phase
// click reaches onOpenPhase. A landing brings its part of the page into view once the page is drawn. numberKeys is
// whether a digit key picks an option of the open question. narrow moves the commands off the title row into a foot
// bar: its Talk button, given only when a steerer is known, calls onTalk, and its More button opens a sheet of the
// commands. Stop asks first in place: its confirm replaces the title row's commands on the desk, and it is a sheet of
// its own on the phone.
export function EpicPage(props: {
  detail: EpicsGetResponse;
  history: EpicHistoryEntry[];
  now: number;
  loadArtifact: LoadArtifact;
  commands: EpicCommands;
  numberKeys: boolean;
  commandError: string;
  onOpenContract: () => void;
  onOpenSession: (target: TalkTarget) => void;
  onBack: () => void;
  onOpenPhase?: (task: string, phase: Phase) => void;
  actions?: ComponentChildren;
  landing?: Landing;
  mark?: InboxMark;
  strip?: ComponentChildren;
  rows?: Row[];
  narrow?: boolean;
  onTalk?: () => void;
}): JSX.Element {
  const {
    detail,
    history,
    now,
    loadArtifact,
    commands,
    numberKeys,
    commandError,
    onOpenContract,
    onOpenSession,
    onBack,
    onOpenPhase,
    actions,
    landing,
    mark,
    strip,
    rows = [],
    narrow = false,
    onTalk,
  } = props;
  const { epic, contract, requirements, steps } = detail;
  const reached = reachedOf(detail, history);
  const wedges = wedgesOf(detail);
  const tasks = recordTasksOf(detail);
  const doneTasks = tasks.filter((task) => task.place === "integrated").length;
  const provenIds = new Set(
    [...requirements.map((requirement) => requirement.id), ...wedges.flatMap((wedge) => wedge.requirements)].filter((id) =>
      provenOf(id, wedges, requirements),
    ),
  );
  const slug = epic?.slug ?? "";
  const controls = controlsOf(detail);
  const state = statePillOf(detail, now);
  const blocker = blockerPillOf(detail, now);
  const problem = epic?.source === "orreryd" ? epic.problem : "";
  const summary = detail.summary !== "" ? detail.summary : (contract?.title ?? "");
  const steering = epic?.steering ?? "";
  const provenCount = requirements.filter((requirement) => provenIds.has(requirement.id)).length;
  const [moreOpen, setMoreOpen] = useState(false);
  const [stopOpen, setStopOpen] = useState(false);
  // stopAsking is whether Stop's confirm is open: it is only while the epic can be stopped.
  const stopAsking = stopOpen && controls.stop;
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
        state={state.word}
        stateTone={state.tone}
        blocker={blocker?.word}
        blockerTone={blocker?.tone}
        mark={mark}
        strip={strip}
        note={commandError}
      >
        {actions}
        {!narrow && stopAsking && (
          <>
            <span class="sure">{`Stop ${slug}?`}</span>
            <button
              type="button"
              class="act primary"
              onClick={() => {
                setStopOpen(false);
                commands.stop();
              }}
            >
              {`Stop ${slug}`}
            </button>
            <button type="button" class="act" onClick={() => setStopOpen(false)}>
              Keep it running
            </button>
          </>
        )}
        {!narrow && !stopAsking && controls.resume && (
          <button type="button" class="act" onClick={() => commands.resume()}>
            Resume
          </button>
        )}
        {!narrow && !stopAsking && controls.pause && (
          <button type="button" class="act" onClick={() => commands.pause()}>
            Pause
          </button>
        )}
        {!narrow && !stopAsking && controls.stop && (
          <button type="button" class="act" onClick={() => setStopOpen(true)}>
            Stop…
          </button>
        )}
      </TitleRow>
      {problem !== "" && <p class="problem error">{problem}</p>}
      {reached.summary && (
        <section class="summary" aria-label="Summary">
          <p class="summary-text">{summary}</p>
          <div class="doors">
            {contract !== undefined && (
              <button type="button" class="door contract" onClick={() => onOpenContract()}>
                <span>Contract</span> <span>{contractDoorOf(detail)}</span>
              </button>
            )}
            {steering !== "" && <SteeredFrom steering={steering} rows={rows} onOpen={onOpenSession} />}
          </div>
        </section>
      )}
      {reached.track && <Track stages={trackOf(detail)} />}
      <NowSection
        detail={detail}
        now={now}
        numberKeys={numberKeys}
        onAnswer={commands.answer}
        onRetry={() => commands.retry()}
        onStop={() => commands.stop()}
        onResume={() => commands.resume()}
      />
      {reached.history && <JustHappened entries={history} steps={steps} />}
      {reached.tasks && (
        <section class="tasks" aria-label="Tasks">
          <header class="sec-head">
            <h3>Tasks</h3>
            <span>{`${doneTasks} of ${tasks.length} done`}</span>
          </header>
          <Orrery tasks={tasks} proven={provenIds} onOpenPhase={onOpenPhase} />
          <TaskTable tasks={tasks} now={now} onOpenPhase={onOpenPhase} />
        </section>
      )}
      {reached.requirements && (
        <section class="requirements" aria-label="Requirements">
          <header class="sec-head">
            <h3>Requirements</h3>
            <span>{`${provenCount} of ${requirements.length} proven`}</span>
          </header>
          <RequirementTable requirements={requirements} loadArtifact={loadArtifact} />
        </section>
      )}
      {narrow && (
        <div class="cmdbar">
          {onTalk !== undefined && (
            <button type="button" class="act" onClick={() => onTalk()}>
              Talk to the session that steers it
            </button>
          )}
          <button type="button" class="act" onClick={() => setMoreOpen(true)}>
            More
          </button>
        </div>
      )}
      {narrow && moreOpen && (
        <Sheet title={slug} onClose={() => setMoreOpen(false)}>
          {controls.resume && (
            <button
              type="button"
              class="act"
              onClick={() => {
                commands.resume();
                setMoreOpen(false);
              }}
            >
              Resume
            </button>
          )}
          {controls.pause && (
            <button
              type="button"
              class="act"
              onClick={() => {
                commands.pause();
                setMoreOpen(false);
              }}
            >
              Pause
            </button>
          )}
          {controls.stop && (
            <button
              type="button"
              class="act"
              onClick={() => {
                setMoreOpen(false);
                setStopOpen(true);
              }}
            >
              Stop…
            </button>
          )}
        </Sheet>
      )}
      {narrow && stopAsking && (
        <Sheet title={`Stop ${slug}?`} onClose={() => setStopOpen(false)}>
          <p>Its running steps are stopped. Nothing is released.</p>
          <button
            type="button"
            class="act primary"
            onClick={() => {
              commands.stop();
              setStopOpen(false);
              setMoreOpen(false);
            }}
          >
            {`Stop ${slug}`}
          </button>
          <button type="button" class="act" onClick={() => setStopOpen(false)}>
            Keep it running
          </button>
        </Sheet>
      )}
    </div>
  );
}
