import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  EpicBuildTaskSchema,
  EpicContractSchema,
  EpicHistoryEntrySchema,
  EpicQuestionSchema,
  EpicReleaseProofSchema,
  EpicRequirementSchema,
  EpicsGetResponseSchema,
  EpicSchema,
  EpicStepSchema,
  type EpicReleaseProof,
  type EpicsGetResponse,
  type EpicStep,
} from "./gen/orrery/v1/epics_pb";
import { clock } from "./reader";
import {
  allLimited,
  askingOf,
  blockerPillOf,
  contractDoorOf,
  nowSentenceOf,
  ownerParked,
  plannerParked,
  reachedOf,
  statePillOf,
  steererParked,
  stepLinesOf,
  trackOf,
} from "./recordwords";

type DetailInit = MessageInitShape<typeof EpicsGetResponseSchema>;
type StepInit = MessageInitShape<typeof EpicStepSchema>;

// A response as serve sends it. What the init leaves out is false, empty or zero.
function detail(init: DetailInit): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// Synthetic epic identity and clock. Every test passes now, so no test reads the wall clock.
const alpha = { id: "repo-a/alpha", repository: "repo-a", slug: "alpha", source: "orreryd" };
const now = 10_000;
const acceptedAt = 1_700_000_000n;
const resetsAt = 1_700_018_000n;

// An orreryd epic in the given phase, with its contract accepted at acceptedAt.
function orrerydIn(outcome: string, init: DetailInit = {}): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    ...init,
    contract: create(EpicContractSchema, { title: "Parser", accepted: acceptedAt, care: "serious" }),
    epic: create(EpicSchema, { ...alpha, outcome }),
  });
}

// A build task with the given status and no stage, integrated when it is done and planned otherwise, as the server
// stands such a task.
function build(id: string, status: string) {
  const [place, tone] = status === "done" ? ["integrated", "ok"] : ["planned", "plan"];
  return create(EpicBuildTaskSchema, { id, title: `Task ${id}`, status, stage: "", requirements: ["R1"], place, tone });
}

// A running step of a task, limited or held or neither.
function step(init: StepInit): EpicStep {
  return create(EpicStepSchema, { task: "T1", agent: "builder", end: "", stageStarted: 9_000n, started: 8_900n, ...init });
}

// A stage as the track names it, in full.
function stage(name: string, state: string, tone: string, words: string) {
  return { name, state, tone, words };
}

test("EpicRecord: a building epic's track is Contract done, Build now and the rest not reached", () => {
  const d = orrerydIn("built", { buildTasks: [build("T1", "done"), build("T2", "pending")] });
  assert.deepEqual(trackOf(d), [
    stage("Contract", "done", "ok", `accepted ${clock(acceptedAt)}`),
    stage("Build", "now", "live", "1 of 2 tasks done"),
    stage("Review", "ahead", "quiet", "not reached"),
    stage("Release", "ahead", "quiet", "not reached"),
    stage("Report", "ahead", "quiet", "not reached"),
  ]);
});

test("EpicRecord: a planning epic's Build now reads the planner's line", () => {
  const d = orrerydIn("accepted");
  assert.deepEqual(trackOf(d)[1], stage("Build", "now", "live", "the planner is outlining tasks"));
});

test("EpicRecord: a clean epic's track is Build and Review done, Release now as ready to release", () => {
  const d = orrerydIn("clean", { buildTasks: [build("T1", "done"), build("T2", "done")] });
  assert.deepEqual(trackOf(d), [
    stage("Contract", "done", "ok", `accepted ${clock(acceptedAt)}`),
    stage("Build", "done", "ok", "2 of 2 tasks done"),
    stage("Review", "done", "ok", "clean"),
    stage("Release", "now", "quiet", "ready to release"),
    stage("Report", "ahead", "quiet", "not reached"),
  ]);
});

test("EpicRecord: a released epic's track ends at Report now, released", () => {
  const d = orrerydIn("released", { buildTasks: [build("T1", "done")] });
  assert.deepEqual(trackOf(d).slice(3), [
    stage("Release", "done", "ok", "released"),
    stage("Report", "now", "ok", "released"),
  ]);
});

test("EpicRecord: a building epic reaches every section", () => {
  const d = orrerydIn("built", {
    summary: "The parser reads a file.",
    buildTasks: [build("T1", "done")],
    requirements: [create(EpicRequirementSchema, { id: "R1", text: "It parses." })],
  });
  const history = [create(EpicHistoryEntrySchema, { time: 1n, cursor: "c1" })];
  assert.deepEqual(reachedOf(d, history), {
    summary: true,
    track: true,
    now: true,
    history: true,
    tasks: true,
    requirements: true,
  });
});

test("EpicRecord: a planning epic reaches its summary, track, now and requirements, not its tasks or history", () => {
  const d = orrerydIn("accepted", {
    requirements: [create(EpicRequirementSchema, { id: "R1", text: "It parses." })],
  });
  assert.deepEqual(reachedOf(d, []), {
    summary: true,
    track: true,
    now: true,
    history: false,
    tasks: false,
    requirements: true,
  });
});

test("EpicRecord: the contract door reads accepted, care and the requirement count, and is empty with no contract", () => {
  assert.equal(contractDoorOf(orrerydIn("accepted", { requirements: [] })), `accepted ${clock(acceptedAt)} · serious care · 0 requirements`);
  const withTwo = orrerydIn("accepted", {
    requirements: [create(EpicRequirementSchema, { id: "R1" }), create(EpicRequirementSchema, { id: "R2" })],
  });
  assert.equal(contractDoorOf(withTwo), `accepted ${clock(acceptedAt)} · serious care · 2 requirements`);
  assert.equal(contractDoorOf(detail({ epic: alpha })), "");
});

test("EpicWords: each state pill has its SPEC 8 word and tone", () => {
  const cases: [string, number, string, string][] = [
    ["accepted", 0, "planning", "live"],
    ["accepted", 1, "building", "live"],
    ["built", 1, "building", "live"],
    ["reviewing", 1, "reviewing", "live"],
    ["correcting", 1, "reviewing", "live"],
    ["clean", 1, "ready to release", "quiet"],
    ["releasing", 1, "releasing", "engine"],
    ["stopped", 1, "stopped", "bad"],
    ["released", 1, "released", "ok"],
    ["done", 1, "done", "ok"],
    ["met_at_base", 1, "already met", "quiet"],
    ["ended_with_gaps", 1, "ended with gaps", "attention"],
    ["closed_superseded", 1, "superseded", "quiet"],
    ["closed_shipped", 1, "shipped outside orrery", "ok"],
    ["closed_abandoned", 1, "abandoned", "quiet"],
  ];
  for (const [outcome, tasks, word, tone] of cases) {
    const buildTasks = tasks === 0 ? [] : [build("T1", "pending")];
    assert.deepEqual(statePillOf(orrerydIn(outcome, { buildTasks }), now), { word, tone }, outcome);
  }
});

test("EpicWords: paused is grey, an owner's park reads parked, and an epic with every step limited is grey", () => {
  assert.deepEqual(statePillOf(orrerydIn("built", { paused: true }), now), { word: "paused", tone: "quiet" });
  assert.deepEqual(statePillOf(orrerydIn("built", { parked: "budget" }), now), { word: "parked", tone: "attention" });
  const limited = orrerydIn("built", { steps: [step({ id: "s1", limited: true })] });
  assert.equal(allLimited(limited), true);
  assert.deepEqual(statePillOf(limited, now), { word: "building", tone: "quiet" });
});

test("EpicWords: a planner's park does not change the state word", () => {
  const d = orrerydIn("built", { parked: "retry later", plannerParked: true });
  assert.equal(ownerParked(d), false);
  assert.equal(plannerParked(d), true);
  assert.deepEqual(statePillOf(d, now), { word: "building", tone: "live" });
});

test("EpicWords: the state pill carries the time since its phase began", () => {
  const d = detail({ epic: { ...alpha, outcome: "built", phaseAt: 9_880n } });
  assert.deepEqual(statePillOf(d, now), { word: "building · 2 min", tone: "live" });
});

test("EpicWords: the blocker pill names a question, an owner's park or a held step, and nothing for a limit, a pause or a planner's park", () => {
  const asked = create(EpicQuestionSchema, { id: "Q1", text: "Which?", asked: 9_400n });
  assert.deepEqual(blockerPillOf(orrerydIn("built", { questions: [asked] }), now), {
    word: "asks you a question · 10 min",
    tone: "attention",
  });
  assert.deepEqual(blockerPillOf(orrerydIn("built", { parked: "budget" }), now), {
    word: "asks you what to do",
    tone: "attention",
  });
  assert.deepEqual(blockerPillOf(orrerydIn("built", { steps: [step({ task: "T2", held: true })] }), now), {
    word: "T2 held",
    tone: "attention",
  });
  assert.equal(blockerPillOf(orrerydIn("built", { steps: [step({ limited: true })] }), now), null);
  assert.equal(blockerPillOf(orrerydIn("built", { paused: true }), now), null);
  assert.equal(blockerPillOf(orrerydIn("built", { parked: "budget", plannerParked: true }), now), null);
});

test("EpicWords: a limited epic's sentence gives its reset time, and a limit with no reset gives none", () => {
  const one = orrerydIn("built", { steps: [step({ id: "s1", limited: true, limitResets: resetsAt })] });
  assert.equal(
    nowSentenceOf(one),
    `1 step waits on a usage limit, which resets at ${clock(resetsAt)}. It resumes on its own.`,
  );
  const two = orrerydIn("built", {
    steps: [step({ id: "s1", limited: true, limitKind: "weekly" }), step({ id: "s2", task: "T2", limited: true, limitKind: "weekly" })],
  });
  assert.equal(nowSentenceOf(two), "2 steps wait on the weekly limit. They resume on their own.");
});

test("EpicWords: the sentence counts the running tasks, and names the open question and the planner's park", () => {
  const running = orrerydIn("built", {
    buildTasks: [build("T1", "pending"), build("T2", "pending")],
    steps: [step({ id: "s1", task: "T1" }), step({ id: "s2", task: "T2" })],
  });
  assert.equal(nowSentenceOf(running), "2 tasks are being worked on.");
  const asked = create(EpicQuestionSchema, { id: "Q1", text: "Which?", asked: 9_400n });
  const oneAsking = orrerydIn("built", {
    buildTasks: [build("T1", "pending")],
    steps: [step({ id: "s1", task: "T1" })],
    questions: [asked],
  });
  assert.equal(nowSentenceOf(oneAsking), "One task is being worked on, and one waits on you.");
  const parked = orrerydIn("built", { parked: "budget", plannerParked: true });
  assert.equal(nowSentenceOf(parked), "The planner holds a park and is deciding what to do. Nothing is asked of you.");
  assert.equal(nowSentenceOf(orrerydIn("built", { paused: true })), "Paused. The steps that were running finish; nothing new starts until you resume.");
  assert.equal(nowSentenceOf(orrerydIn("released")), "It ended: released.");
});

test("EpicWords: limited rows read limited and held rows read held, each with its reset or reason", () => {
  const d = orrerydIn("built", {
    steps: [
      step({ id: "s1", task: "T1", limited: true, limitKind: "weekly", limitResets: resetsAt, stageStarted: 9_000n }),
      step({ id: "s2", task: "T2", held: true, reason: "owner asked", doing: "reading", stageStarted: 0n, started: 8_900n }),
      step({ id: "s3", agent: "reviewer", task: "", doing: "reading the diff", stageStarted: 9_500n }),
    ],
  });
  assert.deepEqual(stepLinesOf(d), [
    {
      id: "s1",
      glyph: "limited",
      label: "T1",
      task: "T1",
      doing: `waits on the weekly limit · resets ${clock(resetsAt)}`,
      since: 9_000,
    },
    { id: "s2", glyph: "held", label: "T2", task: "T2", doing: "held · owner asked", since: 8_900 },
    { id: "s3", glyph: "working", label: "", task: "", doing: "reading the diff", since: 9_500 },
  ]);
});

test("EpicAsk: a question is asked as the question itself, ahead of a park", () => {
  const asked = create(EpicQuestionSchema, { id: "Q1", text: "Which?", asked: 9_400n });
  const d = orrerydIn("built", { questions: [asked], parked: "budget" });
  const asking = askingOf(d);
  assert.equal(asking?.kind, "question");
  assert.equal(asking?.kind === "question" ? asking.question : undefined, d.questions[0]);
});

test("EpicAsk: an owner's park asks in plain words, its reason capitalised with a full stop", () => {
  assert.deepEqual(askingOf(orrerydIn("built", { parked: "budget ceiling reached" })), {
    kind: "park",
    reason: "Budget ceiling reached.",
  });
});

test("EpicAsk: a planner's park is the quiet line, and asks nothing", () => {
  assert.deepEqual(askingOf(orrerydIn("built", { parked: "budget ceiling reached", plannerParked: true })), {
    kind: "planner",
    line: "The planner holds this park: budget ceiling reached. It will retry, replan, or ask you.",
  });
});

test("EpicAsk: an epic whose every running step is limited asks for Resume now", () => {
  assert.deepEqual(askingOf(orrerydIn("built", { steps: [step({ limited: true })] })), { kind: "limited" });
});

test("EpicAsk: an epic with nothing open and nothing limited asks nothing", () => {
  assert.equal(askingOf(orrerydIn("built", { steps: [step({ limited: false })] })), null);
});

test("SteererHolds: a steerer's park is not the owner's, and does not change the state word", () => {
  const d = orrerydIn("built", { parked: "gate failed", steererParked: true });
  assert.equal(ownerParked(d), false);
  assert.equal(steererParked(d), true);
  assert.equal(plannerParked(d), false);
  assert.deepEqual(statePillOf(d, now), { word: "building", tone: "live" });
});

test("SteererHolds: the blocker pill reads a steerer's park and a steerer-held question quietly, after what asks the owner", () => {
  const held = create(EpicQuestionSchema, { id: "Q1", text: "Retry?", asked: 9_400n, steererHeld: true });
  const mine = create(EpicQuestionSchema, { id: "Q2", text: "Which?", asked: 9_700n });
  assert.deepEqual(blockerPillOf(orrerydIn("built", { parked: "gate failed", steererParked: true, questions: [held] }), now), {
    word: "its steerer holds the park",
    tone: "quiet",
  });
  assert.deepEqual(blockerPillOf(orrerydIn("built", { questions: [held] }), now), {
    word: "asks its steerer a question · 10 min",
    tone: "quiet",
  });
  assert.deepEqual(blockerPillOf(orrerydIn("built", { questions: [create(EpicQuestionSchema, { id: "Q1", steererHeld: true })] }), now), {
    word: "asks its steerer a question",
    tone: "quiet",
  });
  assert.deepEqual(blockerPillOf(orrerydIn("built", { questions: [held, mine] }), now), {
    word: "asks you a question · 5 min",
    tone: "attention",
  });
  assert.deepEqual(
    blockerPillOf(orrerydIn("built", { parked: "gate failed", steererParked: true, steps: [step({ task: "T2", held: true })] }), now),
    { word: "T2 held", tone: "attention" },
  );
  assert.deepEqual(blockerPillOf(orrerydIn("ended_with_gaps", { questions: [held] }), now), {
    word: "release it anyway, or close it",
    tone: "attention",
  });
});

test("SteererHolds: a steerer's park holds a stopped release, so its pill reads the steerer's, not the step with no undo", () => {
  const steps = [create(EpicReleaseProofSchema, { position: 1n, step: "publish", state: "failed", undo: "none" })];
  assert.deepEqual(blockerPillOf(orrerydIn("stopped", { releaseSteps: steps }), now), {
    word: "step 1 failed and cannot be undone",
    tone: "attention",
  });
  assert.deepEqual(blockerPillOf(orrerydIn("stopped", { releaseSteps: steps, parked: "publish failed", steererParked: true }), now), {
    word: "its steerer holds the park",
    tone: "quiet",
  });
});

test("SteererHolds: the sentence names a steerer's park, and a steerer-held question as its steerer's wait", () => {
  const held = create(EpicQuestionSchema, { id: "Q1", text: "Retry?", asked: 9_400n, steererHeld: true });
  assert.equal(
    nowSentenceOf(orrerydIn("built", { parked: "gate failed", steererParked: true, questions: [held] })),
    "Parked. Its steerer holds the question; nothing is asked of you unless it escalates it.",
  );
  assert.equal(nowSentenceOf(orrerydIn("built", { questions: [held] })), "It waits on its steerer's answer.");
  assert.equal(
    nowSentenceOf(
      orrerydIn("built", { buildTasks: [build("T1", "pending")], steps: [step({ id: "s1", task: "T1" })], questions: [held] }),
    ),
    "One task is being worked on, and one waits on its steerer.",
  );
  assert.equal(
    nowSentenceOf(
      orrerydIn("built", {
        buildTasks: [build("T1", "pending"), build("T2", "pending")],
        steps: [step({ id: "s1", task: "T1" }), step({ id: "s2", task: "T2" })],
        questions: [held],
      }),
    ),
    "2 tasks are being worked on, and one waits on its steerer.",
  );
  const mine = create(EpicQuestionSchema, { id: "Q2", text: "Which?", asked: 9_700n });
  assert.equal(nowSentenceOf(orrerydIn("built", { questions: [held, mine] })), "It waits on your answer.");
});

test("SteererHolds: a steerer's park and a steerer-held question are quiet lines, and the owner's question asks ahead of them", () => {
  const held = create(EpicQuestionSchema, { id: "Q1", text: "Retry the gate?", asked: 9_400n, steererHeld: true });
  assert.deepEqual(askingOf(orrerydIn("built", { parked: "gate failed", steererParked: true, questions: [held] })), {
    kind: "steerer",
    line: "Its steerer holds this park, and answers it or escalates it to you: gate failed",
  });
  assert.deepEqual(askingOf(orrerydIn("built", { questions: [held] })), {
    kind: "steerer",
    line: "Its steerer holds this question, and answers it or escalates it to you: Retry the gate?",
  });
  const mine = create(EpicQuestionSchema, { id: "Q2", text: "Which?", asked: 9_700n });
  const d = orrerydIn("built", { questions: [held, mine], parked: "gate failed", steererParked: true });
  const asking = askingOf(d);
  assert.equal(asking?.kind === "question" ? asking.question : undefined, d.questions[1]);
  assert.deepEqual(askingOf(orrerydIn("built", { questions: [held], steps: [step({ limited: true })] })), {
    kind: "steerer",
    line: "Its steerer holds this question, and answers it or escalates it to you: Retry the gate?",
  });
});

test("SteererHolds: a steerer-held question does not make Build ask", () => {
  const held = create(EpicQuestionSchema, { id: "Q1", text: "Retry?", steererHeld: true });
  const d = orrerydIn("built", { buildTasks: [build("T1", "done"), build("T2", "pending")], questions: [held] });
  assert.deepEqual(trackOf(d)[1], stage("Build", "now", "live", "1 of 2 tasks done"));
  const mine = create(EpicQuestionSchema, { id: "Q2", text: "Which?" });
  const asks = orrerydIn("built", { buildTasks: [build("T1", "done"), build("T2", "pending")], questions: [held, mine] });
  assert.deepEqual(trackOf(asks)[1], stage("Build", "now", "attention", "1 of 2 tasks done · 1 waits on you"));
});

test("EpicRecord: a reviewing epic has Build done and Review now, and a correcting epic reads correcting", () => {
  const reviewing = orrerydIn("reviewing", { buildTasks: [build("T1", "done")] });
  assert.deepEqual(trackOf(reviewing), [
    stage("Contract", "done", "ok", `accepted ${clock(acceptedAt)}`),
    stage("Build", "done", "ok", "1 of 1 tasks done"),
    stage("Review", "now", "live", "reviewing"),
    stage("Release", "ahead", "quiet", "not reached"),
    stage("Report", "ahead", "quiet", "not reached"),
  ]);
  assert.deepEqual(trackOf(orrerydIn("correcting", { buildTasks: [build("T1", "done")] }))[2], stage("Review", "now", "live", "correcting"));
});

test("EpicRecord: Build now takes the owner's question or park as attention, and a pause or every limited step as quiet", () => {
  const tasks = [build("T1", "done"), build("T2", "pending")];
  const asked = create(EpicQuestionSchema, { id: "Q1", text: "Which?", asked: 9_400n });
  assert.deepEqual(trackOf(orrerydIn("built", { buildTasks: tasks, questions: [asked] }))[1], {
    name: "Build",
    state: "now",
    tone: "attention",
    words: "1 of 2 tasks done · 1 waits on you",
  });
  assert.deepEqual(trackOf(orrerydIn("built", { buildTasks: tasks, parked: "budget" }))[1], {
    name: "Build",
    state: "now",
    tone: "attention",
    words: "1 of 2 tasks done · 1 waits on you",
  });
  assert.deepEqual(trackOf(orrerydIn("built", { buildTasks: tasks, paused: true, steps: [step({ task: "T2" })] }))[1], {
    name: "Build",
    state: "now",
    tone: "quiet",
    words: "1 of 2 tasks done · paused",
  });
  const limitedTasks = [build("T1", "pending"), build("T2", "pending")];
  assert.deepEqual(trackOf(orrerydIn("built", { buildTasks: limitedTasks, steps: [step({ limited: true })] }))[1], {
    name: "Build",
    state: "now",
    tone: "quiet",
    words: "0 of 2 tasks done · waiting on a limit",
  });
});

test("EpicWords: the now sentence gives an owner's park first, and a pause with every step limited reads paused", () => {
  assert.equal(nowSentenceOf(orrerydIn("built", { parked: "budget" })), "Parked. Nothing runs until you answer.");
  assert.equal(
    nowSentenceOf(orrerydIn("built", { parked: "budget", paused: true })),
    "Parked. Nothing runs until you answer.",
  );
  assert.equal(
    nowSentenceOf(orrerydIn("built", { paused: true, steps: [step({ limited: true })] })),
    "Paused. The steps that were running finish; nothing new starts until you resume.",
  );
});

test("EpicWords: the now sentence says it waits on an answer with none running, that the planner is outlining with no task, nothing is running, and one task is being worked on", () => {
  const asked = create(EpicQuestionSchema, { id: "Q1", text: "Which?", asked: 9_400n });
  assert.equal(nowSentenceOf(orrerydIn("built", { questions: [asked] })), "It waits on your answer.");
  assert.equal(
    nowSentenceOf(orrerydIn("accepted")),
    "The planner is reading the contract and outlining tasks. No task has started.",
  );
  assert.equal(nowSentenceOf(orrerydIn("built", { buildTasks: [build("T1", "pending")] })), "Nothing is running.");
  assert.equal(
    nowSentenceOf(orrerydIn("built", { buildTasks: [build("T1", "pending")], steps: [step({ id: "s1", task: "T1" })] })),
    "One task is being worked on.",
  );
});

test("EpicRecord: Release now reads releasing as engine and stopped as bad, and Report now reads ended with gaps as attention", () => {
  const tasks = [build("T1", "done")];
  assert.deepEqual(trackOf(orrerydIn("releasing", { buildTasks: tasks })).slice(3), [
    stage("Release", "now", "engine", "releasing"),
    stage("Report", "ahead", "quiet", "not reached"),
  ]);
  assert.deepEqual(trackOf(orrerydIn("stopped", { buildTasks: tasks })).slice(3), [
    stage("Release", "now", "bad", "stopped"),
    stage("Report", "ahead", "quiet", "not reached"),
  ]);
  assert.deepEqual(trackOf(orrerydIn("ended_with_gaps", { buildTasks: tasks })).slice(3), [
    stage("Release", "done", "ok", "released"),
    stage("Report", "now", "attention", "ended with gaps"),
  ]);
});

test("EpicRecord: an unknown phase puts Contract now with the phase as written, live", () => {
  const track = trackOf(orrerydIn("waiting_for_mars"));
  assert.deepEqual(track[0], stage("Contract", "now", "live", "waiting_for_mars"));
  assert.deepEqual(track[1], stage("Build", "ahead", "quiet", "not reached"));
});

test("EpicRecord: a contract accepted at zero reads accepted with no time, in the track and the door line", () => {
  const d = create(EpicsGetResponseSchema, {
    contract: create(EpicContractSchema, { title: "Parser", accepted: 0n, care: "serious" }),
    epic: create(EpicSchema, { ...alpha, outcome: "built" }),
    buildTasks: [build("T1", "done")],
  });
  assert.deepEqual(trackOf(d)[0], stage("Contract", "done", "ok", "accepted"));
  assert.equal(contractDoorOf(d), "accepted · serious care · 0 requirements");
});

// A release step at its position, in the given state, with the undo it was given.
function releaseStep(position: number, state: string, undo = "rollback"): EpicReleaseProof {
  return create(EpicReleaseProofSchema, { position: BigInt(position), step: `Step ${position}`, state, undo, target: "" });
}

test("EpicWords: a releasing epic's now sentence names its running release step, or says releasing when none runs", () => {
  const steps = [releaseStep(1, "passed"), releaseStep(2, "running"), releaseStep(3, "waiting")];
  assert.equal(nowSentenceOf(orrerydIn("releasing", { releaseSteps: steps })), "Releasing: step 2 of 3 is running.");
  const none = [releaseStep(1, "passed"), releaseStep(2, "waiting")];
  assert.equal(nowSentenceOf(orrerydIn("releasing", { releaseSteps: none })), "Releasing.");
  assert.equal(nowSentenceOf(orrerydIn("releasing")), "Releasing.");
});

test("EpicWords: a stopped release's now sentence names the step with no undo that waits on you, else the first unpassed step, else none", () => {
  const offered = [releaseStep(1, "passed"), releaseStep(2, "failed", "none"), releaseStep(3, "waiting")];
  assert.equal(
    nowSentenceOf(orrerydIn("stopped", { releaseSteps: offered })),
    "The release stopped at step 2, and that step has no undo. It waits on you.",
  );
  const undone = [releaseStep(1, "passed"), releaseStep(2, "waiting"), releaseStep(3, "failed", "rollback")];
  assert.equal(nowSentenceOf(orrerydIn("stopped", { releaseSteps: undone })), "The release stopped at step 2.");
  assert.equal(nowSentenceOf(orrerydIn("stopped")), "The release stopped.");
});

test("EpicWords: an owner's park or an open question still wins over the release sentences", () => {
  const running = [releaseStep(1, "running")];
  const asked = create(EpicQuestionSchema, { id: "Q1", text: "Which?", asked: 9_400n });
  assert.equal(
    nowSentenceOf(orrerydIn("releasing", { releaseSteps: running, parked: "budget" })),
    "Parked. Nothing runs until you answer.",
  );
  assert.equal(
    nowSentenceOf(orrerydIn("stopped", { releaseSteps: [releaseStep(1, "failed", "none")], questions: [asked] })),
    "It waits on your answer.",
  );
});

test("EpicWords: a stopped release with an undo-none failed step gives the blocker pill its words, and nothing else gives a release pill", () => {
  const offered = [releaseStep(1, "passed"), releaseStep(2, "failed", " None ")];
  assert.deepEqual(blockerPillOf(orrerydIn("stopped", { releaseSteps: offered }), now), {
    word: "step 2 failed and cannot be undone",
    tone: "attention",
  });
  const undone = [releaseStep(1, "passed"), releaseStep(2, "failed", "rollback")];
  assert.equal(blockerPillOf(orrerydIn("stopped", { releaseSteps: undone }), now), null);
  assert.equal(blockerPillOf(orrerydIn("releasing", { releaseSteps: [releaseStep(1, "running")] }), now), null);
});

test("EpicWords: an ended-with-gaps epic's blocker pill names its two ways on", () => {
  assert.deepEqual(blockerPillOf(orrerydIn("ended_with_gaps"), now), {
    word: "release it anyway, or close it",
    tone: "attention",
  });
});

test("EpicWords: an owner's park or an open question still wins over the release and ended-with-gaps pills", () => {
  const offered = [releaseStep(1, "failed", "none")];
  const asked = create(EpicQuestionSchema, { id: "Q1", text: "Which?", asked: 9_400n });
  assert.deepEqual(blockerPillOf(orrerydIn("stopped", { releaseSteps: offered, parked: "budget" }), now), {
    word: "asks you what to do",
    tone: "attention",
  });
  assert.deepEqual(blockerPillOf(orrerydIn("ended_with_gaps", { questions: [asked] }), now), {
    word: "asks you a question · 10 min",
    tone: "attention",
  });
});
