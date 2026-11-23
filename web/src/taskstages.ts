import { elapsed } from "./dockwords";
import type { EpicBuildTask, EpicsGetResponse, EpicStep, EpicTaskReview, EvidenceRun } from "./gen/orrery/v1/epics_pb";
import { durationText, PLACES, type RingPlace, type SliceState, type Tone } from "./phases";
import type { RecordTask } from "./record";

// A task's five stage rows, as the record's stages read them: each row's state, its summary, its end (a passed row's
// time or the now row's clock) and, on the now row, its tone. Times are unix seconds.

export interface StageRow {
  stage: RingPlace;
  state: SliceState;
  tone: Tone | undefined;
  summary: string;
  end: string;
}

export interface TaskStages {
  rows: StageRow[];
  // The row the record opens on: the now row, the integrate row once integrated, none when planned.
  open: RingPlace | undefined;
}

// The words an evidence run says while it runs and once it has ended.
interface RunWords {
  waiting: string;
  gathering: string;
  capturing: string;
  judging: string;
  passed: string;
  failed: string;
}

const RED_WORDS: RunWords = {
  waiting: "waiting for the gatherer",
  gathering: "being gathered",
  capturing: "capturing the failing test",
  judging: "a judge is reading the red run",
  passed: "the test fails before any change",
  failed: "the test passes before any change",
};

const GREEN_WORDS: RunWords = {
  waiting: "waiting for the gatherer",
  gathering: "being gathered",
  capturing: "capturing the green run",
  judging: "a judge is reading the green run",
  passed: "the test passes on the change",
  failed: "the test still fails on the change",
};

// The words of a running step's stage.
const STAGE_WORDS = new Map<string, string>([
  ["turn", "the worker is writing"],
  ["checks", "the checks are running"],
  ["gate", "the gate is running"],
  ["judge", "a judge is reading the change"],
  ["held", "held"],
  ["limited", "waits on a usage limit"],
]);

// What a stage reads its task from: the build task, its implementer attempts, and its read-only steps, in started order.
interface Facts {
  build: EpicBuildTask | undefined;
  attempts: EpicStep[];
  reading: EpicStep[];
}

// A count with its noun, plural past one.
function countOf(count: number, noun: string): string {
  return `${count} ${count === 1 ? noun : `${noun}s`}`;
}

// A duration as a row's end: its text, or none when it is 0 seconds or less, which is no data.
function timeText(seconds: number): string {
  return seconds > 0 ? durationText(seconds) : "";
}

// The state of a row, from the record's cell for its ring: ok before the task's place, its tone at it, none after.
function stateOf(cell: string): SliceState {
  if (cell === "ok") {
    return "passed";
  }
  return cell === "none" ? "ahead" : "now";
}

// The summary of an evidence run: what it is doing (waiting for the gatherer, being gathered, a judge reading it), and
// once ended what it found, with judged to hold when its judge held.
function runSummaryOf(words: RunWords, run: EvidenceRun | undefined): string {
  if (run?.state === "waiting") {
    return words.waiting;
  }
  if (run?.state === "gathering") {
    return words.gathering;
  }
  if (run?.state === "judging") {
    return words.judging;
  }
  if (run?.state !== "ended") {
    return words.capturing;
  }
  const outcome = run.passed ? words.passed : words.failed;
  return run.verdict === "holds" ? `${outcome} · judged to hold` : outcome;
}

// The last round of a step's checks, and whether every check of that round passed; undefined when it has no checks.
function lastRoundOf(step: EpicStep): { round: number; passed: boolean } | undefined {
  if (step.checks.length === 0) {
    return undefined;
  }
  const round = Math.max(...step.checks.map((check) => check.round));
  return { round, passed: step.checks.filter((check) => check.round === round).every((check) => check.ok) };
}

// The summary of the implement row: the latest attempt and what it is doing, or how it ended.
function implementSummaryOf(build: EpicBuildTask | undefined, attempts: EpicStep[]): string {
  if (build?.stage === "orchestrate") {
    return "an orchestrator is deciding on the implementer’s request";
  }
  const latest = attempts.at(-1);
  if (latest === undefined) {
    return "not started";
  }
  const head = `attempt ${attempts.length}`;
  if (latest.end !== "") {
    if (latest.end !== "done") {
      return `${head} · ended: ${latest.end}`;
    }
    const parts = [head];
    if (latest.checks.filter((check) => check.name === "gate").at(-1)?.ok === true) {
      parts.push("the gate passed");
    }
    if (latest.verdicts.at(-1)?.verdict === "met") {
      parts.push("judged met");
    }
    return parts.join(" · ");
  }
  const parts = [head];
  const round = lastRoundOf(latest);
  if (round !== undefined) {
    parts.push(`round ${round.round} ${round.passed ? "passed" : "failed"}`);
  }
  const stage = STAGE_WORDS.get(latest.stage);
  if (stage !== undefined) {
    parts.push(stage);
  }
  return parts.join(" · ");
}

// The summary of one review: its gate and verdict when it raised no findings, else its gate and finding count, with its
// confirmed count when there is one.
function reviewWordOf(review: EpicTaskReview): string {
  if (review.findings.length === 0) {
    return `${review.gate} ${review.verdict}`;
  }
  const confirmed = review.findings.filter((finding) => finding.result === "confirmed").length;
  const words = `${review.gate}: ${countOf(review.findings.length, "finding")}`;
  return confirmed > 0 ? `${words}, ${confirmed} confirmed` : words;
}

// The summary of the review row: the verifier's open findings in verify, else each review's words in order.
function reviewSummaryOf(build: EpicBuildTask | undefined): string {
  const reviews = build?.reviews ?? [];
  if (build?.stage === "verify") {
    const open = reviews.flatMap((review) => review.findings).filter((finding) => finding.result === "").length;
    return open === 0
      ? "a verifier is checking its findings"
      : `a verifier is checking ${countOf(open, "finding")}`;
  }
  if (reviews.length === 0) {
    return "the review gates are reading the change";
  }
  return reviews.map(reviewWordOf).join(" · ");
}

// The summary of a row, for a row that is not ahead.
function summaryOf(stage: RingPlace, facts: Facts, integrated: boolean): string {
  switch (stage) {
    case "red":
      return runSummaryOf(RED_WORDS, facts.build?.red);
    case "implement":
      return implementSummaryOf(facts.build, facts.attempts);
    case "green":
      return runSummaryOf(GREEN_WORDS, facts.build?.green);
    case "review":
      return reviewSummaryOf(facts.build);
    case "integrate":
      return integrated ? "merged" : "accepted · waits for its merge";
  }
}

// The time a passed row took: its run's duration, or from its first step's start to its last step's end.
function timeOf(stage: RingPlace, facts: Facts): string {
  switch (stage) {
    case "red":
      return timeText(runSecondsOf(facts.build?.red));
    case "green":
      return timeText(runSecondsOf(facts.build?.green));
    case "implement":
      return spanTextOf(facts.attempts);
    case "review":
      return spanTextOf(facts.reading);
    case "integrate":
      return "";
  }
}

// The seconds an evidence run took, from its creation to its end.
function runSecondsOf(run: EvidenceRun | undefined): number {
  return run === undefined ? 0 : Number(run.ended - run.created);
}

// The duration of a set of steps, from the first one's start to the last one's end.
function spanTextOf(steps: EpicStep[]): string {
  const first = steps[0];
  const last = steps.at(-1);
  if (first === undefined || last === undefined) {
    return "";
  }
  return timeText(Number(last.ended - first.started));
}

// The row the record opens on: the task's place, the integrate row once integrated, none when planned.
function openOf(place: RecordTask["place"]): RingPlace | undefined {
  if (place === "planned") {
    return undefined;
  }
  return place === "integrated" ? "integrate" : place;
}

// The five stage rows of a task, in ring order, read at now.
export function stagesOf(detail: EpicsGetResponse, task: RecordTask, now: number): TaskStages {
  const build = detail.buildTasks.find((candidate) => candidate.id === task.task);
  const steps = detail.steps
    .filter((step) => step.task === task.task)
    .sort((left, right) => Number(left.started - right.started));
  const facts: Facts = {
    build,
    attempts: steps.filter((step) => step.role === "implementer"),
    reading: steps.filter((step) => step.readOnly),
  };
  const integrated = task.place === "integrated";
  const rows = PLACES.map((stage, index): StageRow => {
    const state = stateOf(task.cells[index]);
    if (state === "ahead") {
      return { stage, state, tone: undefined, summary: "not reached", end: "" };
    }
    if (state === "now") {
      const clock = task.since > 0 ? elapsed(now - task.since) : "";
      return { stage, state, tone: task.tone, summary: summaryOf(stage, facts, integrated), end: clock };
    }
    return { stage, state, tone: undefined, summary: summaryOf(stage, facts, integrated), end: timeOf(stage, facts) };
  });
  return { rows, open: openOf(task.place) };
}
