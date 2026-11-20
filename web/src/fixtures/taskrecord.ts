// The slices of the task record and worker session page stories: rota-export's export-cli task (T2) at each place its record
// draws, and tz-fixtures' held worker session. Each variant changes the building state of rota-export, or the shipped one
// for the held session, in the way its story names. The shipped and building fixtures stay as they are.

import { clone, create } from "@bufbuild/protobuf";

import {
  type EpicBuildTask,
  EpicBuildTaskSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  type EpicStep,
  EpicStepSchema,
  EpicTaskReviewSchema,
  type EvidenceRun,
  EvidenceRunSchema,
} from "../gen/orrery/v1/epics_pb";
import { SessionsWatchConversationResponseSchema } from "../gen/orrery/v1/orrery_pb";
import { at, commit } from "./clock";
import { rotaBuilding, rotaEvidence } from "./epics";
import { prompt, reply } from "./panes";
import type { Slices } from "./slices";

// TaskRecordScenario names a set of slices the task record and worker session stories are served from.
export type TaskRecordScenario =
  | "implementing"
  | "refusedHold"
  | "unansweredNudge"
  | "green"
  | "review"
  | "integrate"
  | "held";

// TaskRecordBases are the fixtures the variants change: rota-export in the building state, and the shipped slices, whose
// tz-fixtures step is held.
export interface TaskRecordBases {
  readonly building: Slices;
  readonly shipped: Slices;
}

const rotaId = "lighthouse/rota-export";
const implementStep = "st-rota-t2-impl";
const verifyStep = "st-rota-t2-verify";
const heldStep = "st-rota-t1-impl";
const greenLog = "ok  \tlighthouse/cli\t0.44s\nok  \tlighthouse/internal/rota\t1.92s";

// stepNamed is the step of a Get answer with id, which rota-export's fixture holds.
function stepNamed(get: EpicsGetResponse, id: string): EpicStep {
  const step = get.steps.find((candidate) => candidate.id === id);
  if (step === undefined) {
    throw new Error(`the rota-export fixture holds no step ${id}`);
  }
  return step;
}

// judgingRun is R4's green run while a judge reads it, as the evidence scenario draws it.
function judgingRun(): EvidenceRun {
  const requirement = rotaEvidence.get.requirements.find((candidate) => candidate.id === "R4");
  const run = requirement?.runs.find((candidate) => candidate.state === "judging");
  if (run === undefined) {
    throw new Error("the evidence fixture holds no judging run for R4");
  }
  return run;
}

// passedGreen is export-cli's green run ended with its test passing on the change.
const passedGreen = create(EvidenceRunSchema, {
  id: "ev-rota-t2-green",
  phase: "green",
  state: "ended",
  verdict: "holds",
  passed: true,
  exit: 0,
  source: "captured",
  revision: commit.rotaRetry,
  created: at("@14:29:00"),
  ended: at("@14:29:35"),
  logBytes: BigInt(new TextEncoder().encode(greenLog).length),
});

// implementingStep is export-cli's writing attempt while it runs, with no verdict yet: the judge reads the change once the
// gate passes.
function implementingStep(get: EpicsGetResponse): EpicStep {
  const step = clone(EpicStepSchema, stepNamed(get, implementStep));
  step.verdicts = [];
  return step;
}

// judgedStep is export-cli's writing attempt ended done: its gate passed and the judge met the goal.
function judgedStep(get: EpicsGetResponse): EpicStep {
  const step = clone(EpicStepSchema, stepNamed(get, implementStep));
  step.end = "done";
  step.status = "ended";
  step.held = false;
  step.ended = at("@14:40");
  step.stage = "";
  step.stageStarted = 0n;
  step.doing = "";
  step.summary = "The refusal returns ErrExists, and the gate passed.";
  return step;
}

// verifyingStep is the reviewer that checks export-cli's findings while it runs.
const verifyingStep = create(EpicStepSchema, {
  id: verifyStep,
  task: "T2",
  agent: "sonnet",
  role: "reviewer",
  readOnly: true,
  gateName: "check",
  gateLimit: 1800n,
  end: "",
  status: "running",
  held: false,
  started: at("@14:40"),
  ended: 0n,
  stage: "turn",
  stageStarted: at("@14:40"),
  doing: "Checking the path of the finding against export.go:41.",
});

// exportCliBuild is export-cli's build task in the building state, with the fields change names replaced.
function exportCliBuild(change: Partial<Pick<EpicBuildTask, "stage" | "green" | "reviews">>): EpicBuildTask {
  const build = rotaBuilding.get.buildTasks.find((candidate) => candidate.id === "T2");
  if (build === undefined) {
    throw new Error("the rota-export fixture holds no build task T2");
  }
  return Object.assign(clone(EpicBuildTaskSchema, build), change);
}

// rotaWith is rota-export's Get answer in the building state, with export-cli's steps and build task replaced: its other
// tasks keep theirs.
function rotaWith(steps: EpicStep[], build: EpicBuildTask): EpicsGetResponse {
  const get = clone(EpicsGetResponseSchema, rotaBuilding.get);
  get.steps = [...get.steps.filter((step) => step.task !== "T2"), ...steps];
  get.buildTasks = get.buildTasks.map((candidate) => (candidate.id === "T2" ? build : candidate));
  return get;
}

// floorReview is the review gate that found nothing, which holds the change.
const floorReview = create(EpicTaskReviewSchema, {
  attempt: 1n,
  gate: "floor",
  step: "",
  verdict: "holds",
  findings: [],
});

// securityReview is the security gate's review: one finding a verifier is checking, and one it dropped.
const securityReview = create(EpicTaskReviewSchema, {
  attempt: 1n,
  gate: "security",
  step: verifyStep,
  verdict: "fails",
  findings: [
    {
      id: "F1",
      claim: "A name holding “../” writes outside the rota directory.",
      location: "export.go:41",
      step: verifyStep,
      result: "",
      reason: "",
    },
    {
      id: "F2",
      claim: "The error message names the wrong path.",
      location: "export.go:93",
      step: verifyStep,
      result: "dropped",
      reason: "it names the path the user gave",
    },
  ],
});

// heldConversation is tz-fixtures' worker conversation: its brief, and the reply that holds it for the owner's answer.
const heldConversation = create(SessionsWatchConversationResponseSchema, {
  transcript: `${heldStep}.jsonl`,
  seq: 1n,
  reset: true,
  items: [
    prompt(at("@14:22"), "brief", "Brief from planner turn 3. Goal: R5, fixtures cover every port zone."),
    reply(
      at("@14:39"),
      "The pinned tz database lacks the 2026 Lisbon rule. One fixture cannot pass without a go.mod bump. Holding for your answer.",
    ),
  ],
  state: {
    goal: "",
    waiting: [],
    context: { tokens: 40000n, window: 200000n },
    session: {
      id: heldStep, dir: "/home/owner/code/lighthouse", label: "T1", state: "working",
      changed: at("@14:39"), seen: 0n, summary: "", question: "", newestReply: at("@14:39"),
    },
  },
});

// withGet is the slices with rota-export's Get answer replaced by get, and the log of the passed green run added.
function withGet(base: Slices, get: EpicsGetResponse): Slices {
  return {
    ...base,
    gets: new Map([...base.gets, [rotaId, get]]),
    evidenceLogs: new Map([...base.evidenceLogs, [passedGreen.id, greenLog]]),
  };
}

// taskRecordStories are the slices of each scenario, built from the bases. Each epic-level variant keeps the building
// state's other tasks, and the held worker session keeps the shipped answers.
export function taskRecordStories(bases: TaskRecordBases): Record<TaskRecordScenario, Slices> {
  const implementing = withGet(bases.building, rotaWith([implementingStep(rotaBuilding.get)], exportCliBuild({})));
  const judged = judgedStep(rotaBuilding.get);
  return {
    implementing,
    refusedHold: { ...implementing, steps: { hold: "refused" } },
    unansweredNudge: { ...implementing, steps: { nudge: "unanswered" } },
    green: withGet(
      bases.building,
      rotaWith([judged, verifyingStep], exportCliBuild({ stage: "green", green: judgingRun() })),
    ),
    review: withGet(
      bases.building,
      rotaWith(
        [judged, verifyingStep],
        exportCliBuild({ stage: "verify", green: passedGreen, reviews: [floorReview, securityReview] }),
      ),
    ),
    integrate: withGet(
      bases.building,
      rotaWith([judged], exportCliBuild({ stage: "", green: passedGreen, reviews: [floorReview] })),
    ),
    held: {
      ...bases.shipped,
      sessionStreams: new Map([...bases.shipped.sessionStreams, [heldStep, heldConversation]]),
    },
  };
}
