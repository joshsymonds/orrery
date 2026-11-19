// The fixture epics of round 6: the epic list, each epic in its state, and rota-export's tasks and requirements. The
// states are read through the client's own helpers over the shipped slices, and the instant is the fixtures' own.

import assert from "node:assert/strict";
import { test } from "node:test";

import type { Epic, EpicRequirement, EpicsGetResponse } from "../gen/orrery/v1/epics_pb";
import { type Place, type Tone, provenOf, wedgesOf } from "../phases";
import { blockerOf, stateOf } from "../status";
import { liveEpoch } from "./clock";
import { shippedSlices } from "./slices";

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

test("FixtureWorld: the epic list holds the ten epics of round 6, by slug, in list order", () => {
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
  ]);
});

test("FixtureWorld: every epic in the list has an Epics.Get answer that names the same epic, so its address draws", () => {
  for (const epic of shippedSlices.epics.epics) {
    assert.equal(shippedSlices.gets.get(epic.id)?.epic?.slug, epic.slug, `${epic.id} has its Epics.Get answer`);
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
