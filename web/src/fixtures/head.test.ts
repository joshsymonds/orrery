// The fixture slice variants of the head's page stories: each is a copy of the shipped slices with what its story draws, and
// importing them leaves the shipped slices as they are.

import assert from "node:assert/strict";
import { test } from "node:test";

import type { Epic, EpicsGetResponse } from "../gen/orrery/v1/epics_pb";
import type { ConversationContext, ConversationState, Pane, SessionsWatchConversationResponse } from "../gen/orrery/v1/orrery_pb";
import { epicNeeds } from "../needs";
import { epicSlices, fullSlices, hotSlices, manySlices, scrolledSlices, warmupSession } from "./head";
import { shippedSlices, type Slices } from "./slices";

// epicOf is the epic of the slices with the slug. It fails the test when the slices hold none.
function epicOf(slices: Slices, slug: string): Epic {
  const epic = slices.epics.epics.find((candidate) => candidate.slug === slug);
  assert.ok(epic !== undefined, `the slices hold the epic ${slug}`);
  return epic;
}

// detailOf is the Epics.Get answer of the epic with the slug. It fails the test when the slices hold none.
function detailOf(slices: Slices, slug: string): EpicsGetResponse {
  const detail = slices.gets.get(epicOf(slices, slug).id);
  assert.ok(detail !== undefined, `the slices hold an Epics.Get answer for ${slug}`);
  return detail;
}

// paneOf is the pane of the slices with the id. It fails the test when the slices hold none.
function paneOf(slices: Slices, id: string): Pane {
  const pane = slices.panes.panes.find((candidate) => candidate.id === id);
  assert.ok(pane !== undefined, `the slices hold the pane ${id}`);
  return pane;
}

// warmupStream is the warm-up session's stream of the slices. It fails the test when the slices hold none.
function warmupStream(slices: Slices): SessionsWatchConversationResponse {
  const stream = slices.sessionStreams.get(warmupSession);
  assert.ok(stream !== undefined, "the slices hold the warm-up session's stream");
  return stream;
}

// warmupState is the state of the warm-up session's stream. It fails the test when the stream holds none.
function warmupState(slices: Slices): ConversationState {
  const state = warmupStream(slices).state;
  assert.ok(state !== undefined, "the warm-up session's stream holds its state");
  return state;
}

// contextOf is the context of the pane's stream. It fails the test when the stream holds none.
function contextOf(slices: Slices, paneId: string): ConversationContext {
  const context = slices.paneStreams.get(paneId)?.state?.context;
  assert.ok(context !== undefined, `the pane ${paneId}'s stream holds a context`);
  return context;
}

test("fixtures/head: importing head.ts leaves the shipped slices as they are", () => {
  assert.deepEqual(shippedSlices.epics.epics.filter((epic) => epic.steering !== ""), []);
  assert.equal(paneOf(shippedSlices, "%14").orreryId, "");
  assert.equal(paneOf(shippedSlices, "%22").orreryId, "");
  assert.deepEqual(shippedSlices.host.stats.map((stat) => stat.level), ["ok", "ok", "ok", "ok"]);
  assert.equal(warmupState(shippedSlices).goal, "");
  assert.deepEqual(warmupState(shippedSlices).background, []);
  assert.equal(warmupState(shippedSlices).context?.tokens, 152000n);
  assert.deepEqual(detailOf(shippedSlices, "rota-export").questions.map((question) => question.id), ["Q1"]);
});

test("fixtures/head: hotSlices' host has cpu warn, ram crit, disk and net ok", () => {
  assert.deepEqual(
    hotSlices.host.stats.map((stat) => [stat.label, stat.level]),
    [["cpu", "warn"], ["ram", "crit"], ["disk", "ok"], ["net", "ok"]],
  );
});

test("fixtures/head: hotSlices' warm-up context is 174000 of 200000", () => {
  const context = warmupState(hotSlices).context;
  assert.ok(context !== undefined, "the warm-up session's state holds its context");
  assert.deepEqual([context.tokens, context.window], [174000n, 200000n]);
});

test("fixtures/head: fullSlices' warm-up has the goal, the two running agents and the running shell", () => {
  const state = warmupState(fullSlices);
  assert.equal(state.goal, "Every port’s tide table is cached through December 17 and the warm-up is one command.");
  assert.deepEqual(
    state.background.map((task) => [task.type, task.subagentType, task.description, task.status]),
    [
      ["agent", "Explore", "Find every caller of keeperFor", "running"],
      ["agent", "reviewer", "Review the query change", "running"],
      ["shell", "", "pnpm dev --port 5173", "running"],
    ],
  );
});

test("fixtures/head: fullSlices' warm-up transcript is long enough to scroll, at least 30 items", () => {
  assert.ok(warmupStream(fullSlices).items.length >= 30, "the transcript holds at least 30 items");
});

test("fixtures/head: fullSlices' rota-export is steered by the warm-up session, and its question is open", () => {
  assert.equal(epicOf(fullSlices, "rota-export").steering, warmupSession);
  assert.deepEqual(detailOf(fullSlices, "rota-export").questions.map((question) => question.id), ["Q1"]);
});

test("fixtures/head: scrolledSlices' rota-export has no question and no need", () => {
  assert.deepEqual(detailOf(scrolledSlices, "rota-export").questions, []);
  const rota = epicOf(scrolledSlices, "rota-export");
  assert.deepEqual(epicNeeds([rota], new Map([[rota.id, detailOf(scrolledSlices, "rota-export")]])), []);
});

test("fixtures/head: epicSlices' pane %14 has orreryId rotaplan, which rota-export's steering names", () => {
  assert.equal(paneOf(epicSlices, "%14").orreryId, "rotaplan");
  assert.equal(epicOf(epicSlices, "rota-export").steering, "rotaplan");
});

test("fixtures/head: epicSlices' pane %14 holds the mid context, 640000 of 1000000", () => {
  const context = contextOf(epicSlices, "%14");
  assert.deepEqual([context.tokens, context.window], [640000n, 1000000n]);
});

test("fixtures/head: manySlices' pane %22 holds the mid context, 640000 of 1000000", () => {
  const context = contextOf(manySlices, "%22");
  assert.deepEqual([context.tokens, context.window], [640000n, 1000000n]);
});

test("fixtures/head: manySlices' four epics name followplan, and pane %22 has that orreryId", () => {
  const steered = manySlices.epics.epics.filter((epic) => epic.steering === "followplan").map((epic) => epic.slug);
  assert.deepEqual(steered, ["orrery-followups", "orbit-labels", "notices-usable", "harbour-map"]);
  assert.equal(paneOf(manySlices, "%22").orreryId, "followplan");
});

test("fixtures/head: manySlices' four epics raise needs only for notices-usable, a park among them", () => {
  const four = manySlices.epics.epics.filter((epic) => epic.steering === "followplan");
  const details = new Map(four.map((epic) => [epic.id, detailOf(manySlices, epic.slug)] as const));
  const needs = epicNeeds(four, details);
  assert.deepEqual(needs.map((need) => [need.epic, need.kind]), [
    ["orrery/notices-usable", "question"],
    ["orrery/notices-usable", "park"],
  ]);
});
