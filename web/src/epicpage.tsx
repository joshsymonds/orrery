import type { ComponentChildren, JSX } from "preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";

import { releaseOf, replacementsOf, reportOf, reviewOf } from "./ending";
import { EpicUsageSection } from "./epicusage";
import type { LoadArtifact } from "./evidence";
import type { Epic, EpicHistoryEntry, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { UsageEpicTotalsResponse } from "./gen/orrery/v1/usage_pb";
import { Icon } from "./glyphs";
import { JustHappened } from "./happenings";
import { NowSection } from "./nowsection";
import type { CallOutcome } from "./ownercall";
import { Orrery } from "./orrery";
import { durationText, type Phase, provenOf } from "./phases";
import { recordTasksOf } from "./record";
import { blockerPillOf, contractDoorOf, ownerParked, reachedOf, statePillOf, trackOf } from "./recordwords";
import { ReleaseTable } from "./release";
import { EpicReport } from "./report";
import { RequirementTable } from "./reqtable";
import { EpicReview } from "./review";
import type { Row } from "./rows";
import { Sheet } from "./sheet";
import { type Blocker, controlsOf, stateOf } from "./status";
import { steererRowOf } from "./steer";
import { TaskTable } from "./tasktable";
import { listedTitle, TitleParts } from "./titleparts";
import { TitleRow } from "./titlerow";
import { Track } from "./track";

// EpicCommands are the epic's commands: pause, resume, stop and retry, and the answer to one open question: the choice
// the owner picked, which is a declared choice's id or other, and its text. release runs the epic's release, with the
// undo-none steps named by their positions run again; close closes the epic as chosen, superseded by the epic named.
// Each of release and close is sent once and resolves its outcome. reread reads the epic again and resolves true when
// the read answers.
export interface EpicCommands {
  pause: () => void;
  resume: () => void;
  stop: () => void;
  retry: () => void;
  answer: (questionId: string, choice: string, text: string) => Promise<string>;
  release: (undoNone: number[]) => Promise<CallOutcome>;
  close: (kind: "superseded" | "shipped" | "abandoned", by: string) => Promise<CallOutcome>;
  reread: () => Promise<boolean>;
}

// SessionTarget is what a session opens: a pane, or a session by its id, with the served label that names it when no
// listed row does.
export interface SessionTarget {
  kind: "pane" | "session";
  id: string;
  label: string;
}

// SteerSheet is the phone's steering sheet as the epic page holds it: whether it is open, and its closing.
export interface SteerSheet {
  open: boolean;
  onClose: () => void;
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
// one included, brings the Report into view when the page draws one, and else the page's top.
const landingTargets: Partial<Record<string, string>> = {
  question: "section.now",
  release_step: "section.now",
};

// SteeredFrom is the Steered from door: the session that steers the epic, titled from its listed row as steererRowOf finds
// that row, and the opening of that conversation. A steerer no row lists is opened by its id, and its
// title is the word session.
function SteeredFrom({
  steering,
  rows,
  onOpen,
}: {
  steering: string;
  rows: Row[];
  onOpen: (target: SessionTarget) => void;
}): JSX.Element {
  const row = steererRowOf(rows, steering);
  const target: SessionTarget =
    row === null ? { kind: "session", id: steering, label: "" } : { kind: row.kind, id: row.id, label: row.label };
  const title = listedTitle(rows, target.kind, target.id, target.label);
  return (
    <button type="button" class="door steered" onClick={() => onOpen(target)}>
      <Icon name="chat" />
      <span>Steered from</span> <TitleParts dir={title.dir} line={title.line} />
      <Icon name="chevronRight" />
    </button>
  );
}

// EpicPage is the one epic page, drawn section by section in the order of the record: the title row with the epic's
// state and blocker pills and its commands, and under it the did-not-land line of a failed command. Then its summary
// with the Contract door and the Steered from door, its track, Now, Just happened, the tasks with their chart and
// table, and the requirements. A section the epic has not reached is not drawn. The commands are the ones controlsOf
// shows for the epic. Each phase click reaches onOpenPhase. After the requirements, the Review, Release and Report
// sections draw when their views are non-null, in that order, and then the Usage section, when loadUsage is given and its
// answer has sessions. loadUsage is read when the page opens and again each time the count of its steps that have ended
// grows; an error or an empty answer draws no section. Just happened's step lines and evidence runs' judge steps reach
// onOpenStep. A landing brings its part of the page into view once the page is drawn, and a page that draws its
// Report brings the Report into view once when no landing names another block. numberKeys is whether a digit key picks
// an option of the open question. steer draws the steering control: in the title row at the desk, and under narrow the
// sheet that More's Steer item opens. narrow moves the commands off the title row into a foot bar: its Talk button,
// given only when a steerer is known, calls onTalk, and its More button opens a sheet of the commands, with the Steer
// item when steer is given. foot, drawn above that bar under narrow, is the ended bar an epic no running conversation
// steers stands on. Stop asks first in place: its confirm replaces the title row's commands on the desk, and it is a
// sheet of its own on the phone.
export function EpicPage(props: {
  detail: EpicsGetResponse;
  history: EpicHistoryEntry[];
  now: number;
  loadArtifact: LoadArtifact;
  commands: EpicCommands;
  numberKeys: boolean;
  commandError: string;
  onOpenContract: () => void;
  onOpenSession: (target: SessionTarget) => void;
  onBack?: () => void;
  onOpenPhase?: (task: string, phase: Phase) => void;
  onOpenStep?: (step: string) => void;
  landing?: Landing;
  strip?: ComponentChildren;
  rows?: Row[];
  narrow?: boolean;
  onTalk?: () => void;
  steer?: (sheet: SteerSheet) => ComponentChildren;
  foot?: ComponentChildren;
  listed?: Epic[];
  loadUsage?: (epic: string) => Promise<UsageEpicTotalsResponse>;
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
    onOpenStep,
    landing,
    strip,
    rows = [],
    narrow = false,
    onTalk,
    steer,
    foot,
    listed = [],
    loadUsage,
  } = props;
  const { epic, contract, requirements, steps } = detail;
  const reached = reachedOf(detail, history);
  const tasks = recordTasksOf(detail);
  const doneTasks = tasks.filter((task) => task.place === "integrated").length;
  const provenIds = new Set(
    [...requirements.map((requirement) => requirement.id), ...tasks.flatMap((task) => task.requirements)].filter((id) =>
      provenOf(id, tasks, requirements),
    ),
  );
  const slug = epic?.slug ?? "";
  const controls = controlsOf(detail);
  const state = statePillOf(detail, now);
  const blocker = blockerPillOf(detail, now);
  const summary = detail.summary !== "" ? detail.summary : (contract?.title ?? "");
  const steering = epic?.steering ?? "";
  const provenCount = requirements.filter((requirement) => provenIds.has(requirement.id)).length;
  const reviewView = reviewOf(detail);
  const releaseView = releaseOf(detail);
  const reportView = reportOf(detail);
  const [moreOpen, setMoreOpen] = useState(false);
  const [stopOpen, setStopOpen] = useState(false);
  // stopAsking is whether Stop's confirm is open: it is only while the epic can be stopped.
  const stopAsking = stopOpen && controls.stop;
  // pausing is whether Pause is offered: an owner's park holds the epic until its owner answers, so nothing runs.
  const pausing = controls.pause && !ownerParked(detail);
  const [steerOpen, setSteerOpen] = useState(false);
  const steerSheet: SteerSheet = { open: steerOpen, onClose: () => setSteerOpen(false) };
  const pageRef = useRef<HTMLDivElement>(null);
  // The page brings one block into view when it is first drawn and when its landing changes, never on a later read: the
  // block the landing's kind names, else the Report when the page draws one, else the page's top for a landing.
  useLayoutEffect(() => {
    const page = pageRef.current;
    if (page === null) {
      return;
    }
    const target = landing === undefined ? undefined : landingTargets[landing.kind];
    if (target !== undefined) {
      page.querySelector<HTMLElement>(target)?.scrollIntoView();
      return;
    }
    const report = page.querySelector<HTMLElement>('section[aria-label="Report"]');
    if (report !== null) {
      report.scrollIntoView();
    } else if (landing !== undefined) {
      page.scrollIntoView();
    }
  }, [landing]);
  // The Usage section's answer. Each read is numbered, and only the answer of the latest read is drawn. last is the epic and
  // the count of ended steps the page last read for, so a read is made when the page opens and when that count grows.
  const [usage, setUsage] = useState<UsageEpicTotalsResponse | null>(null);
  const usageReads = useRef<{ count: number; last: { epic: string; ended: number } | null }>({ count: 0, last: null });
  const epicId = epic?.id ?? "";
  const endedSteps = steps.filter((step) => step.ended !== 0n).length;
  useEffect(() => {
    if (loadUsage === undefined) {
      return;
    }
    const last = usageReads.current.last;
    usageReads.current.last = { epic: epicId, ended: endedSteps };
    if (last !== null && last.epic === epicId && endedSteps <= last.ended) {
      return;
    }
    usageReads.current.count += 1;
    const read = usageReads.current.count;
    loadUsage(epicId).then(
      (answer) => {
        if (read === usageReads.current.count) {
          setUsage(answer);
        }
      },
      () => {
        if (read === usageReads.current.count) {
          setUsage(null);
        }
      },
    );
  }, [epicId, endedSteps]);
  return (
    <div class="epic-page" ref={pageRef}>
      <TitleRow
        crumbs={[{ label: slug }]}
        onBack={onBack}
        state={state.word}
        stateTone={state.tone}
        blocker={blocker?.word}
        blockerTone={blocker?.tone}
        strip={narrow ? undefined : strip}
        note={commandError}
      >
        {!narrow && !stopAsking && steer?.(steerSheet)}
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
        {!narrow && !stopAsking && pausing && (
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
      {reached.summary && (
        <section class="summary" aria-label="Summary">
          <p class="summary-text">{summary}</p>
          <div class="doors">
            {contract !== undefined && (
              <button type="button" class="door contract" onClick={() => onOpenContract()}>
                <Icon name="doc" />
                <span>Contract</span> <span>{contractDoorOf(detail)}</span>
                <Icon name="chevronRight" />
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
        narrow={narrow}
        numberKeys={numberKeys}
        onAnswer={commands.answer}
        onRetry={() => commands.retry()}
        onStop={() => commands.stop()}
        onResume={() => commands.resume()}
      />
      {reached.history && <JustHappened entries={history} steps={steps} onOpenStep={onOpenStep} />}
      {reached.tasks && (
        <section class="tasks" aria-label="Tasks">
          <div class="sec" data-hue="4">
            <Icon name="pools" />
            <span class="name">Tasks</span>
            <span class="n">{`${doneTasks} of ${tasks.length} done`}</span>
          </div>
          <Orrery tasks={tasks} requirements={requirements} proven={provenIds} onOpenPhase={onOpenPhase} />
          <TaskTable tasks={tasks} now={now} onOpenPhase={onOpenPhase} />
        </section>
      )}
      {reached.requirements && (
        <section class="requirements" aria-label="Requirements">
          <div class="sec" data-hue="ok">
            <Icon name="check" />
            <span class="name">Requirements</span>
            <span class="n">{`${provenCount} of ${requirements.length} proven`}</span>
          </div>
          <RequirementTable requirements={requirements} loadArtifact={loadArtifact} proven={provenIds} />
        </section>
      )}
      {reviewView !== null && <EpicReview view={reviewView} />}
      {releaseView !== null && (
        <section class="release" aria-label="Release">
          <div class="sec" data-hue="engine">
            <Icon name="open" />
            <span class="name">Release</span>
            <span class="n">{releaseView.words}</span>
          </div>
          <ReleaseTable
            view={releaseView}
            narrow={narrow}
            onRelease={commands.release}
            reread={commands.reread}
          />
        </section>
      )}
      {reportView !== null && (
        <EpicReport
          view={reportView}
          slug={slug}
          narrow={narrow}
          replacements={replacementsOf(detail, listed)}
          onRelease={commands.release}
          onClose={commands.close}
          reread={commands.reread}
        />
      )}
      {usage !== null && (
        <EpicUsageSection
          sessions={usage.sessions}
          steps={steps}
          epicSessions={detail.sessions}
          requirements={requirements}
          narrow={narrow}
        />
      )}
      {narrow && foot}
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
          {pausing && (
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
          {steer !== undefined && (
            <button
              type="button"
              class="act"
              onClick={() => {
                setMoreOpen(false);
                setSteerOpen(true);
              }}
            >
              Steer it from another conversation
            </button>
          )}
        </Sheet>
      )}
      {narrow && steer?.(steerSheet)}
      {narrow && stopAsking && (
        <Sheet title={`Stop ${slug}?`} onClose={() => setStopOpen(false)}>
          <p>Its running steps are stopped. Nothing is released.</p>
          <div class="foot">
            <button type="button" class="act" onClick={() => setStopOpen(false)}>
              Keep it running
            </button>
            <button
              type="button"
              class="act danger"
              onClick={() => {
                commands.stop();
                setStopOpen(false);
                setMoreOpen(false);
              }}
            >
              {`Stop ${slug}`}
            </button>
          </div>
        </Sheet>
      )}
    </div>
  );
}
