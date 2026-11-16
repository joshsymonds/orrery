import type { EpicStep, EpicTaskReview, EpicsGetResponse, EvidenceRun } from "./gen/orrery/v1/epics_pb";
import { wedgesOf, type Phase, type SliceState } from "./phases";

// A phase of a task: its wedge's slice, and the step "Open the session" opens for it.
export interface PhaseRow {
  phase: Phase;
  state: SliceState;
  summary: string;
  since: number;
  seconds: number;
  session: string;
}

// A step of a task and how it went.
export interface Attempt {
  step: string;
  end: string;
  reason: string;
  summary: string;
  doing: string;
  held: boolean;
}

// One check round of a writing step: its checks other than the gate, the gate, and the judge's verdict.
export interface CheckRound {
  step: string;
  round: number;
  checks: { name: string; ok: boolean; detail: string }[];
  gate: { name: string; ok: boolean; detail: string; stdout: string; stderr: string } | null;
  judge: { verdict: string; reason: string } | null;
  ran: string[];
}

export interface PhaseOutput {
  task: string;
  title: string;
  requirements: string[];
  rows: PhaseRow[];
  red: EvidenceRun | undefined;
  green: EvidenceRun | undefined;
  attempts: Attempt[];
  rounds: CheckRound[];
  reviews: EpicTaskReview[];
  reviewers: Attempt[];
  integrated: boolean;
  running: EpicStep | undefined;
}

// The step a phase's session opens: the latest writing step for implementing and checking, the latest read-only step
// for reviewing, none for integrating.
function sessionOf(phase: Phase, latestWriting: string, latestReading: string): string {
  switch (phase) {
    case "implementing":
    case "checking":
      return latestWriting;
    case "reviewing":
      return latestReading;
    case "integrating":
      return "";
  }
}

function attemptOf(step: EpicStep): Attempt {
  return { step: step.id, end: step.end, reason: step.reason, summary: step.summary, doing: step.doing, held: step.held };
}

// The round of a writing step: its checks, its gate and its verdict, each only when the round has it.
function roundOf(step: EpicStep, round: number): CheckRound {
  const checks = step.checks.filter((check) => check.round === round);
  const gateCheck = checks.find((check) => check.name === "gate");
  const others = checks
    .filter((check) => check.name !== "gate")
    .map((check) => ({ name: check.name, ok: check.ok, detail: check.detail }));
  const verdict = step.verdicts.find((candidate) => candidate.round === round);
  const gate =
    gateCheck === undefined
      ? null
      : {
          name: step.gateName,
          ok: gateCheck.ok,
          detail: gateCheck.detail,
          stdout: gateCheck.tailStdout,
          stderr: gateCheck.tailStderr,
        };
  const judge = verdict === undefined ? null : { verdict: verdict.verdict, reason: verdict.reason };
  const ran: string[] = [];
  if (others.length > 0) {
    ran.push("checks");
  }
  if (gate !== null) {
    ran.push("gate");
  }
  if (judge !== null) {
    ran.push("judge");
  }
  return { step: step.id, round, checks: others, gate, judge, ran };
}

// The rounds of a writing step, ascending: each round a check or a verdict names.
function roundsOf(step: EpicStep): CheckRound[] {
  const rounds = new Set([...step.checks.map((check) => check.round), ...step.verdicts.map((verdict) => verdict.round)]);
  return [...rounds].sort((a, b) => a - b).map((round) => roundOf(step, round));
}

// A task's phase output as the page reads it, from one Get answer: its own steps, runs, reviews and rounds. Null when
// the epic has no task with that id.
export function phaseOutputOf(detail: EpicsGetResponse, task: string): PhaseOutput | null {
  const wedge = wedgesOf(detail).find((candidate) => candidate.task === task);
  if (wedge === undefined) {
    return null;
  }
  const steps = detail.steps.filter((step) => step.task === task);
  const writing = steps.filter((step) => !step.readOnly);
  const reading = steps.filter((step) => step.readOnly);
  const latestWriting = writing.at(-1)?.id ?? "";
  const latestReading = reading.at(-1)?.id ?? "";
  const build = detail.buildTasks.find((candidate) => candidate.id === task);
  return {
    task,
    title: wedge.title,
    requirements: wedge.requirements,
    rows: wedge.slices.map((slice) => ({
      phase: slice.phase,
      state: slice.state,
      summary: slice.summary,
      since: slice.since,
      seconds: slice.seconds,
      session: sessionOf(slice.phase, latestWriting, latestReading),
    })),
    red: build?.red,
    green: build?.green,
    attempts: writing.map(attemptOf),
    rounds: writing.flatMap(roundsOf),
    reviews: build?.reviews ?? [],
    reviewers: reading.map(attemptOf),
    integrated: wedge.place === "integrated",
    running: steps.filter((step) => step.end === "").at(-1),
  };
}
