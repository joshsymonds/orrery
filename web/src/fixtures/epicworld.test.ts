// The fixture epics of round 6: the epic list, each epic in its state, and rota-export's tasks and requirements. The
// states are read through the client's own helpers over the shipped slices, and the instant is the fixtures' own.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Code } from "@connectrpc/connect";

import type { Epic, EpicRequirement, EpicsGetResponse } from "../gen/orrery/v1/epics_pb";
import { type Place, type Tone, provenOf, wedgesOf } from "../phases";
import { allLimited, askingOf, nowSentenceOf, ownerParked, plannerParked } from "../recordwords";
import { rowsFrom } from "../rows";
import { blockerOf, stateOf } from "../status";
import { at, liveEpoch } from "./clock";
import { type EpicScenario, epicScenarios, type Slices, shippedSlices } from "./slices";
import { createFixtureTransport } from "./transport";

// epicOf is the epic of the list with the slug. It fails the test when the list holds none.
function epicOf(slug: string): Epic {
  const epic = shippedSlices.epics.epics.find((candidate) => candidate.slug === slug);
  assert.ok(epic !== undefined, `the fixtures hold the epic ${slug}`);
  return epic;
}

// detailOf is the Epics.Get answer of the epic with the slug. It fails the test when the fixtures hold none.
function detailOf(slug: string): EpicsGetResponse {
  const detail = shippedSlices.gets.get(epicOf(slug).id);
  assert.ok(detail !== undefined, `the fixtures hold an Epics.Get answer for ${slug}`);
  return detail;
}

// requirementOf is the requirement of the epic with the id. It fails the test when the epic holds none.
function requirementOf(slug: string, id: string): EpicRequirement {
  const requirement = detailOf(slug).requirements.find((candidate) => candidate.id === id);
  assert.ok(requirement !== undefined, `${slug} holds the requirement ${id}`);
  return requirement;
}

// provenIds are the ids of the epic's requirements that are proven, by the client's own rule over its wedges.
function provenIds(slug: string): string[] {
  const detail = detailOf(slug);
  const wedges = wedgesOf(detail);
  return detail.requirements
    .filter((requirement) => provenOf(requirement.id, wedges, detail.requirements))
    .map((requirement) => requirement.id);
}

// assertTask checks rota-export's task with the title: its task id, requirements, place and tone, as the client's wedges
// derive them from the epic's detail.
function assertTask(title: string, task: string, requirements: string[], place: Place, tone: Tone): void {
  const wedge = wedgesOf(detailOf("rota-export")).find((candidate) => candidate.title === title);
  assert.ok(wedge !== undefined, `rota-export holds a task titled ${title}`);
  assert.deepEqual([wedge.task, wedge.requirements, wedge.place, wedge.tone], [task, requirements, place, tone], title);
}

test("FixtureWorld: the epic list holds the ten epics of round 6, then the two archived ones, by slug, in list order", () => {
  assert.deepEqual(shippedSlices.epics.epics.map((epic) => epic.slug), [
    "orrery-followups",
    "rota-export",
    "tide-alerts",
    "offline-tables",
    "orbit-labels",
    "notices-usable",
    "harbour-map",
    "buoy-feeds",
    "holder-scopes",
    "tide-cache",
    "moon-rounding",
    "tide-warmup",
  ]);
});

test("InboxTree: the list holds notices-usable parked and harbour-map paused by its owner, as their Epics.Get answers do", () => {
  const notices = epicOf("notices-usable");
  assert.equal(notices.parked, "R5 is already met at the epic’s base", "the list row names the park");
  assert.equal(notices.parked, detailOf("notices-usable").parked, "the list row names the park its Epics.Get answer names");
  const harbour = epicOf("harbour-map");
  assert.deepEqual([harbour.paused, harbour.pauseSource], [true, "owner"], "the list row says the owner paused it");
  assert.deepEqual([detailOf("harbour-map").paused], [harbour.paused], "its Epics.Get answer says it is paused");
});

test("InboxTree: moon-rounding and tide-warmup are ended and archived in the list and in their Epics.Get answers", () => {
  for (const slug of ["moon-rounding", "tide-warmup"]) {
    const listed = epicOf(slug);
    assert.deepEqual([listed.live, listed.archived], [false, true], `${slug} is ended and archived in the list`);
    assert.deepEqual([detailOf(slug).epic?.live, detailOf(slug).epic?.archived], [false, true], `${slug}'s Epics.Get answer agrees`);
  }
});

test("FixtureWorld: every epic in the list has an Epics.Get answer that names the same epic, so its address draws", () => {
  for (const epic of shippedSlices.epics.epics) {
    assert.equal(shippedSlices.gets.get(epic.id)?.epic?.slug, epic.slug, `${epic.id} has its Epics.Get answer`);
  }
});

// steeringOf is the id of the conversation that steers each epic: round 6's (web/visual/wireframe/round6/js/world.js,
// W.steerers) for the ten listed epics of round 6, the headless session for the buoy-feeds epic, and each pane's orrery id
// for the rest; then the archived almanac conversation for moon-rounding and the stopped warm-up for tide-warmup.
const steeringOf: Record<string, string> = {
  "orrery-followups": "sess-follow-22",
  "rota-export": "sess-rota-14",
  "tide-alerts": "sess-tide-16",
  "offline-tables": "sess-offline-17",
  "orbit-labels": "sess-follow-22",
  "notices-usable": "sess-follow-22",
  "harbour-map": "sess-follow-22",
  "buoy-feeds": "6c2f9e14-3a7b-4d58-9b0e-2f7a1c8d4e55",
  "holder-scopes": "sess-holder-19",
  "tide-cache": "sess-cache-20",
  "moon-rounding": "e7c4b2a1-5d9f-4e3a-9b61-2a8f0c4d7e13",
  "tide-warmup": "9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90",
};

test("FixtureWorld: each listed epic names the steering id of its conversation, and that id is a listed row's id", () => {
  const listed = new Set(
    rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions).map((row) => row.archiveId),
  );
  for (const epic of shippedSlices.epics.epics) {
    const wanted = steeringOf[epic.slug];
    assert.equal(epic.steering, wanted, `${epic.slug} is steered by its conversation`);
    assert.equal(shippedSlices.gets.get(epic.id)?.epic?.steering, wanted, `${epic.slug}'s Epics.Get names the same conversation`);
    assert.ok(listed.has(wanted), `${wanted} is the id of a listed conversation`);
  }
});

test("FixtureWorld: each epic lives in its repository's directory under /home/owner/code, in the list and in its Epics.Get answer", () => {
  for (const epic of shippedSlices.epics.epics) {
    const wanted = `/home/owner/code/${epic.repository}`;
    assert.equal(epic.path, wanted, `${epic.slug} is listed with its path`);
    assert.equal(shippedSlices.gets.get(epic.id)?.epic?.path, wanted, `${epic.slug}'s Epics.Get names its path`);
  }
});

test("FixtureWorld: rota-export is building, and asks Q1 whether to bump the tz database", () => {
  const detail = detailOf("rota-export");
  assert.equal(stateOf(detail), "building");
  const blocker = blockerOf(detail);
  const question = blocker?.kind === "question" ? blocker.question : undefined;
  assert.equal(question?.id, "Q1", "the epic waits on question Q1");
  assert.ok(question?.text.startsWith("Bump the embedded tz database?"), "Q1 asks about the tz database");
});

test("FixtureWorld: orrery-followups is building, with R2 proven and R1 and R3 not", () => {
  assert.equal(stateOf(detailOf("orrery-followups")), "building");
  assert.deepEqual(provenIds("orrery-followups"), ["R2"]);
});

test("FixtureWorld: tide-alerts is a written contract, not yet accepted, and its alert job deploys to halyard", () => {
  const detail = detailOf("tide-alerts");
  assert.equal(stateOf(detail), "contract written");
  assert.equal(detail.contract?.accepted, 0n, "the contract is written but not accepted");
  assert.equal(detail.releaseSteps[1]?.target, "halyard", "the alert job deploys to halyard");
});

test("FixtureWorld: offline-tables is stopped, with release step 2 failed on its CI", () => {
  const detail = detailOf("offline-tables");
  assert.equal(stateOf(detail), "stopped");
  const step = detail.releaseSteps.find((proof) => proof.position === 2n);
  assert.deepEqual([step?.step, step?.state], ["CI green on the tag", "failed"]);
});

test("FixtureWorld: orbit-labels is releasing, with all four of its requirements proven", () => {
  const detail = detailOf("orbit-labels");
  assert.equal(stateOf(detail), "releasing");
  assert.deepEqual(provenIds("orbit-labels"), ["R1", "R2", "R3", "R4"]);
});

test("FixtureWorld: notices-usable is parked, with none of its five requirements proven", () => {
  const detail = detailOf("notices-usable");
  assert.equal(stateOf(detail), "parked");
  assert.equal(detail.requirements.length, 5);
  assert.deepEqual(provenIds("notices-usable"), []);
});

test("FixtureWorld: notices-usable is parked on the engine's question that R5 is already met", () => {
  const blocker = blockerOf(detailOf("notices-usable"));
  assert.ok(
    blocker?.kind === "question" && blocker.question.text.startsWith("Requirement R5 is already met"),
    "the engine asks about R5",
  );
});

test("FixtureWorld: notices-usable has been parked for 29 hours at the fixtures' instant", () => {
  const instant = Date.parse(liveEpoch) / 1000;
  assert.equal(instant - Number(epicOf("notices-usable").phaseAt), 29 * 3600);
});

test("FixtureWorld: harbour-map is paused by you, with 5 of its 9 tasks done", () => {
  const detail = detailOf("harbour-map");
  assert.equal(stateOf(detail), "paused");
  assert.equal(detail.paused, true, "the owner paused it");
  assert.equal(detail.buildTasks.length, 9);
  assert.equal(wedgesOf(detail).filter((wedge) => wedge.place === "integrated").length, 5);
});

test("FixtureWorld: buoy-feeds is planning, with no task yet", () => {
  const detail = detailOf("buoy-feeds");
  assert.equal(stateOf(detail), "planning");
  assert.equal(detail.buildTasks.length, 0);
});

test("FixtureWorld: holder-scopes shipped v0.45.0 on halyard, with all four of its requirements proven", () => {
  const detail = detailOf("holder-scopes");
  assert.equal(stateOf(detail), "released");
  assert.deepEqual(provenIds("holder-scopes"), ["R1", "R2", "R3", "R4"]);
  assert.equal(detail.releaseSteps[2]?.target, "halyard", "v0.45.0 runs on halyard");
  assert.ok(detail.report?.why.includes("v0.45.0"), "the report names v0.45.0");
});

test("FixtureWorld: tide-cache has ended, superseded by offline-tables", () => {
  const detail = detailOf("tide-cache");
  assert.equal(epicOf("tide-cache").live, false, "the epic is not live");
  assert.equal(detail.report?.outcome, "closed_superseded");
  assert.ok(detail.report?.why.includes("superseded by offline-tables"), "the report names offline-tables");
});

test("FixtureWorld: rota-export's task shift-store is integrated, with task T4 proving R1", () => {
  assertTask("shift-store", "T4", ["R1"], "integrated", "ok");
});

test("FixtureWorld: rota-export's task rota-endpoint is integrated, with task T3 proving R2", () => {
  assertTask("rota-endpoint", "T3", ["R2"], "integrated", "ok");
});

test("FixtureWorld: rota-export's task csv-writer is accepted and waits to integrate, with task T5 proving R3", () => {
  assertTask("csv-writer", "T5", ["R3"], "integrating", "acc");
});

test("FixtureWorld: rota-export's task export-cli is implementing R4 under the gate, with task T2", () => {
  assertTask("export-cli", "T2", ["R4"], "implementing", "run");
});

test("FixtureWorld: rota-export's task tz-fixtures is held on your answer, with task T1 for R5", () => {
  assertTask("tz-fixtures", "T1", ["R5"], "implementing", "mine");
});

test("FixtureWorld: rota-export's task audit-log is planned for R6", () => {
  assertTask("audit-log", "T7", ["R6"], "planned", "plan");
});

test("FixtureWorld: rota-export's task readme is planned for R6", () => {
  assertTask("readme", "T6", ["R6"], "planned", "plan");
});

test("FixtureWorld: rota-export proves R1 to R3 and leaves R4 to R6 unproven", () => {
  assert.deepEqual(provenIds("rota-export"), ["R1", "R2", "R3"]);
});

test("FixtureWorld: rota-export's R4 green run is still capturing, with no verdict yet", () => {
  const green = requirementOf("rota-export", "R4").runs.find((run) => run.phase === "green");
  assert.deepEqual([green?.state, green?.verdict, green?.passed], ["capturing", "", false]);
});

test("FixtureWorld: rota-export's R5 red run passed, and R5 has no green run", () => {
  const runs = requirementOf("rota-export", "R5").runs;
  assert.deepEqual(runs.map((run) => [run.phase, run.passed]), [["red", true]]);
});

test("FixtureWorld: rota-export's R6 is a review with no run yet", () => {
  const requirement = requirementOf("rota-export", "R6");
  assert.deepEqual([requirement.kind, requirement.runs.length], ["review", 0]);
});

// detailIn is the Epics.Get answer of the epic with the slug in the slices, or undefined when the slices hold none.
function detailIn(slices: Slices, slug: string): EpicsGetResponse | undefined {
  const epic = slices.epics.epics.find((candidate) => candidate.slug === slug);
  return epic === undefined ? undefined : slices.gets.get(epic.id);
}

// scenarioDetail is the Epics.Get answer of the epic with the slug in the scenario's slices. It fails the test when the
// scenario holds none.
function scenarioDetail(scenario: EpicScenario, slug: string): EpicsGetResponse {
  const detail = detailIn(epicScenarios[scenario], slug);
  assert.ok(detail !== undefined, `the ${scenario} scenario holds an Epics.Get answer for ${slug}`);
  return detail;
}

test("FixtureScenarios: building draws rota-export building, with nothing asked and two steps running, one of them not held", () => {
  const detail = scenarioDetail("building", "rota-export");
  assert.equal(stateOf(detail), "building");
  assert.equal(detail.questions.length, 0, "no question is open");
  assert.equal(blockerOf(detail), null, "nothing holds the epic");
  assert.equal(nowSentenceOf(detail), "2 tasks are being worked on.");
  const running = detail.steps.filter((step) => step.end === "");
  assert.deepEqual(running.map((step) => [step.task, step.held]), [["T2", false], ["T1", false]]);
  assert.deepEqual(
    wedgesOf(detail).filter((wedge) => ["T1", "T2"].includes(wedge.task)).map((wedge) => [wedge.task, wedge.place, wedge.tone]),
    [["T1", "implementing", "run"], ["T2", "implementing", "run"]],
  );
});

test("FixtureScenarios: limited draws rota-export with every running step on the weekly limit, which resets at 15:10", () => {
  const detail = scenarioDetail("limited", "rota-export");
  assert.equal(allLimited(detail), true, "every running step is limited");
  const running = detail.steps.filter((step) => step.end === "");
  assert.deepEqual(
    running.map((step) => [step.limitKind, step.limitResets]),
    [["weekly", at("@15:10")], ["weekly", at("@15:10")]],
    "both wait on the weekly limit, which resets at 15:10",
  );
  // The reset's clock time is written in the local zone, so the sentence is checked for its words, not its digits.
  assert.ok(nowSentenceOf(detail).startsWith("2 steps wait on the weekly limit, which resets at "), "Now names the limit");
});

test("FixtureScenarios: parked draws notices-usable on the owner's park, which asks with no question", () => {
  const detail = scenarioDetail("parked", "notices-usable");
  assert.equal(detail.questions.length, 0, "no question is open");
  assert.equal(askingOf(detail)?.kind, "park", "the owner's park asks");
});

test("FixtureWorld: rota-export's question Q1 offers Keep the pin, recommended, and Allow the bump", () => {
  const [question] = detailOf("rota-export").questions;
  assert.deepEqual(question?.choices.map((choice) => choice.id), ["Keep the pin", "Allow the bump"]);
  assert.equal(question?.recommendation?.choice, "Keep the pin");
});

test("FixtureScenarios: plannerParked draws rota-export under the planner's park, which asks the owner nothing", () => {
  const detail = scenarioDetail("plannerParked", "rota-export");
  assert.equal(plannerParked(detail), true, "the planner holds the park");
  assert.equal(ownerParked(detail), false, "the owner does not hold it");
  assert.equal(askingOf(detail)?.kind, "planner");
  assert.equal(detail.questions.length, 0, "no question is open");
});

test("FixtureScenarios: problem draws rota-export with the engine's problem on the epic it reads", () => {
  const detail = scenarioDetail("problem", "rota-export");
  assert.notEqual(detail.epic?.problem, "", "the epic has a problem");
});

test("FixtureScenarios: unavailable draws rota-export with no Epics.Get answer, so the read fails", async () => {
  const { clients } = createFixtureTransport(epicScenarios.unavailable);
  const id = epicOf("rota-export").id;
  await assert.rejects(clients.epics.get({ id }), { code: Code.NotFound });
});

test("FixtureScenarios: commandError refuses Pause with a reason, so the page's command does not land", async () => {
  const { clients } = createFixtureTransport(epicScenarios.commandError);
  const id = epicOf("rota-export").id;
  await assert.rejects(clients.epics.pause({ id }), { code: Code.Unavailable });
});

test("FixtureScenarios: planning draws buoy-feeds with no question, no task, and the planner's sentence", () => {
  const detail = scenarioDetail("planning", "buoy-feeds");
  assert.equal(detail.questions.length, 0, "no question is open");
  assert.equal(askingOf(detail), null, "nothing asks the owner");
  assert.equal(wedgesOf(detail).length, 0, "no task is outlined");
  assert.equal(nowSentenceOf(detail), "The planner is reading the contract and outlining tasks. No task has started.");
});

test("FixtureScenarios: loading holds the Epics.Get of an epic the first load does not read, so its page waits", () => {
  const listed = epicScenarios.loading.epics.epics.find((epic) => epic.slug === "tide-cache");
  assert.ok(listed !== undefined, "the scenario lists tide-cache");
  assert.equal(listed.live, false, "the first load reads only live epics");
  assert.ok(epicScenarios.loading.heldGets.has(listed.id), "the page's own read of it stays open");
});

test("FixtureScenarios: dense draws deck-pipeline with 120 tasks, 96 of them done", () => {
  const detail = scenarioDetail("dense", "deck-pipeline");
  const wedges = wedgesOf(detail);
  assert.equal(wedges.length, 120);
  assert.equal(wedges.filter((wedge) => wedge.place === "integrated").length, 96);
});
