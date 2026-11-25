import type { EpicsGetResponse, EpicStep, EpicStepCheck, EpicTaskReview, EvidenceRun } from "./gen/orrery/v1/epics_pb";
import { tasksOf } from "./phases";

// The tone of a line or a chip: live while a step runs, attention when it is held, ok, bad, neutral, accepted, or quiet.
export type BodyTone = "live" | "attention" | "ok" | "bad" | "neutral" | "accepted" | "quiet";

// A line of words, or a chip's word, in its tone.
export interface Words {
  text: string;
  tone: BodyTone;
}

// A stage's run as the record draws it, or the one quiet line that says there is none.
export type RunBody = { kind: "run"; run: EvidenceRun } | { kind: "none"; line: Words };

// One check round of the latest attempt: its checks in step order, and the text of its failed checks, one per line.
export interface RoundBody {
  round: number;
  checks: { name: string; ok: boolean }[];
  text: string;
}

// The gate of the latest attempt's last round: its name, and its log, the tail stdout then the tail stderr, each only
// when it has text. The log is empty when neither has text.
export interface GateBody {
  name: string;
  log: string;
}

// The latest attempt: its number, its check rounds, its gate when its last round has one, and its judge's line.
export interface LatestBody {
  attempt: number;
  rounds: RoundBody[];
  gate: GateBody | null;
  judge: Words;
}

// One implementer step of the task: its attempt number, its words and its chip.
export interface AttemptRow {
  step: string;
  attempt: number;
  words: string;
  chip: Words;
}

// The implement stage: every attempt in started order, the latest of them, and the step its session door opens.
export interface ImplementBody {
  attempts: AttemptRow[];
  latest: LatestBody | null;
  session: string | null;
}

// One row of the review table: a gate's no-findings verdict, or one finding of a gate. The gate reads on the first row
// of its review only. A row's result is its verdict or the finding's result, and reason is the text after it.
export interface ReviewRow {
  gate: string;
  what: string;
  where: string;
  result: Words;
  reason: string;
}

// The review stage: the table of every review in order, or the quiet line when none has run, and the step its session
// door opens.
export interface ReviewBody {
  rows: ReviewRow[];
  empty: Words | null;
  session: string | null;
}

// The green stage: the line that says what the red test does on the change, then the green run or its quiet line.
export interface GreenBody {
  reproduction: Words;
  run: RunBody;
}

// What each of a task's five stages shows when opened, keyed by the stage's name.
export interface StageBodies {
  red: RunBody;
  implement: ImplementBody;
  green: GreenBody;
  review: ReviewBody;
  integrate: Words;
}

const IMPLEMENTER = "implementer";

function quiet(text: string): Words {
  return { text, tone: "quiet" };
}

function runBodyOf(run: EvidenceRun | undefined, absent: string): RunBody {
  return run === undefined ? { kind: "none", line: quiet(absent) } : { kind: "run", run };
}

// The chip of an attempt: held while the owner holds a running step, running while it runs, done when it ended done,
// and spent for any other end.
function chipOf(step: EpicStep): Words {
  if (step.end === "") {
    return step.held ? { text: "held", tone: "attention" } : { text: "running", tone: "live" };
  }
  return step.end === "done" ? { text: "done", tone: "ok" } : { text: "spent", tone: "bad" };
}

// The words of an attempt: its summary once it ended, else its reason, else what it is doing.
function wordsOf(step: EpicStep): string {
  if (step.end !== "" && step.summary !== "") {
    return step.summary;
  }
  if (step.reason !== "") {
    return step.reason;
  }
  return step.doing;
}

// The check rounds of a writing step, ascending.
function roundsOf(step: EpicStep): number[] {
  return [...new Set(step.checks.map((check) => check.round))].sort((a, b) => a - b);
}

// The text of a round's checks: each failed check as its name and detail, one per line, or All checks pass.
function textOf(checks: EpicStepCheck[]): string {
  const failed = checks.filter((check) => !check.ok);
  if (failed.length === 0) {
    return "All checks pass.";
  }
  return failed.map((check) => (check.detail === "" ? `${check.name} failed` : `${check.name}: ${check.detail}`)).join("\n");
}

function roundBodyOf(step: EpicStep, round: number): RoundBody {
  const checks = step.checks.filter((check) => check.round === round);
  return { round, checks: checks.map((check) => ({ name: check.name, ok: check.ok })), text: textOf(checks) };
}

// The gate of the last round, when that round has a gate check: its name and the log of its tails.
function gateOf(step: EpicStep, lastRound: number | undefined): GateBody | null {
  const gate = step.checks.find((check) => check.round === lastRound && check.name === "gate");
  if (gate === undefined) {
    return null;
  }
  return { name: step.gateName, log: [gate.tailStdout, gate.tailStderr].filter((tail) => tail !== "").join("\n") };
}

// The judge's line: the step's last verdict, met, unmet with its reason, or quiet while there is none.
function judgeOf(step: EpicStep): Words {
  const verdict = step.verdicts.at(-1);
  if (verdict === undefined) {
    return quiet("The judge reads the change once the gate passes.");
  }
  if (verdict.verdict === "met") {
    return { text: "Judged: the goal is met.", tone: "ok" };
  }
  const text = verdict.reason === "" ? `Judged: ${verdict.verdict}.` : `Judged: ${verdict.verdict}. ${verdict.reason}`;
  return { text, tone: "bad" };
}

function latestOf(step: EpicStep, attempt: number): LatestBody {
  const rounds = roundsOf(step);
  return {
    attempt,
    rounds: rounds.map((round) => roundBodyOf(step, round)),
    gate: gateOf(step, rounds.at(-1)),
    judge: judgeOf(step),
  };
}

function implementOf(steps: EpicStep[]): ImplementBody {
  const attempts = steps.filter((step) => step.role === IMPLEMENTER);
  const latest = attempts.at(-1);
  return {
    attempts: attempts.map((step, index) => ({
      step: step.id,
      attempt: index + 1,
      words: wordsOf(step),
      chip: chipOf(step),
    })),
    latest: latest === undefined ? null : latestOf(latest, attempts.length),
    session: latest?.id ?? null,
  };
}

// The verdict's chip: holds ok, fails bad, anything else neutral in its own word.
function verdictWordsOf(verdict: string): Words {
  switch (verdict) {
    case "holds":
      return { text: "holds", tone: "ok" };
    case "fails":
      return { text: "fails", tone: "bad" };
    default:
      return { text: verdict, tone: "neutral" };
  }
}

// The result chip of a finding: confirmed bad, dropped and undecided neutral, and verifying live while none is in.
function resultWordsOf(result: string): Words {
  switch (result) {
    case "confirmed":
      return { text: "confirmed", tone: "bad" };
    case "dropped":
      return { text: "dropped", tone: "neutral" };
    case "undecided":
    case "undetermined":
      return { text: "undecided", tone: "neutral" };
    case "":
      return { text: "verifying", tone: "live" };
    default:
      return { text: result, tone: "neutral" };
  }
}

// The rows of one review: a row for its no-findings verdict, else one row per finding. The gate reads with its attempt
// when the reviews span more than one attempt.
function reviewRowsOf(review: EpicTaskReview, withAttempt: boolean): ReviewRow[] {
  const gate = withAttempt ? `${review.gate} · attempt ${review.attempt}` : review.gate;
  if (review.findings.length === 0) {
    return [{ gate, what: "no findings", where: "", result: verdictWordsOf(review.verdict), reason: review.reason }];
  }
  return review.findings.map((finding, index) => ({
    gate: index === 0 ? gate : "",
    what: finding.claim,
    where: finding.location,
    result: resultWordsOf(finding.result),
    reason: finding.result === "" ? "a verifier is checking it" : finding.reason,
  }));
}

function reviewOf(reviews: EpicTaskReview[], steps: EpicStep[]): ReviewBody {
  const withAttempt = new Set(reviews.map((review) => review.attempt)).size > 1;
  const reader = steps.filter((step) => step.readOnly).at(-1);
  return {
    rows: reviews.flatMap((review) => reviewRowsOf(review, withAttempt)),
    empty: reviews.length === 0 ? quiet("No review has run yet.") : null,
    session: reader?.id ?? null,
  };
}

// The green stage's line: what the red test does on the change, from the green run.
function reproductionOf(run: EvidenceRun | undefined): Words {
  if (run === undefined) {
    return quiet("The red test has not run on the change yet.");
  }
  if (run.state !== "ended") {
    return quiet("The red test is running on the change.");
  }
  return run.passed
    ? { text: "The red test, run on the change, now passes.", tone: "ok" }
    : { text: "The red test, run on the change, still fails.", tone: "bad" };
}

// A task's stage bodies, as its opened stages show them, from one Get answer. Null when the epic has no task with that
// id.
export function stageBodiesOf(detail: EpicsGetResponse, task: string): StageBodies | null {
  const record = tasksOf(detail).find((candidate) => candidate.task === task);
  if (record === undefined) {
    return null;
  }
  const build = detail.buildTasks.find((candidate) => candidate.id === task);
  const steps = detail.steps.filter((step) => step.task === task);
  return {
    red: runBodyOf(build?.red, "No red run yet."),
    implement: implementOf(steps),
    green: {
      reproduction: reproductionOf(build?.green),
      run: runBodyOf(build?.green, "No green run yet."),
    },
    review: reviewOf(build?.reviews ?? [], steps),
    integrate:
      record.place === "integrated"
        ? { text: "Merged.", tone: "ok" }
        : { text: "Every gate took it. It waits for its merge.", tone: "accepted" },
  };
}
