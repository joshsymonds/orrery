import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicSchema, EpicsGetResponseSchema, type Epic, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { epicNeeds, rowNeed, type Need } from "./needs";
import { type Row } from "./rows";

function makeRow(init: Partial<Row>): Row {
  return {
    tool: "",
    key: "pane:box:%3",
    id: "%3",
    archiveId: "",
    kind: "pane",
    label: "api",
    project: "/work/api",
    host: "box",
    state: "waiting",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    ...init,
  };
}

// Synthetic epic identity shared by the fixtures.
const alpha = { id: "repo-a/alpha", repository: "repo-a", slug: "alpha" };

type EpicInit = MessageInitShape<typeof EpicSchema>;
type DetailInit = MessageInitShape<typeof EpicsGetResponseSchema>;

// A live epic unless the init says otherwise.
function makeEpic(init: EpicInit): Epic {
  return create(EpicSchema, { ...alpha, live: true, ...init });
}

// A response as serve sends it. What the init leaves out is false, empty or zero.
function makeDetail(init: DetailInit): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// The needs of one epic that has a detail.
function needsFor(init: DetailInit, epic: Epic = makeEpic({})): Need[] {
  return epicNeeds([epic], new Map([[epic.id, makeDetail(init)]]));
}

function textsOf(needs: Need[]): string[] {
  return needs.map((need) => need.text);
}

const asksAboutDatabase = JSON.stringify({
  questions: [{ question: "Which database?", options: [{ label: "Postgres", description: "" }] }],
});

test("a waiting row with a question reads asks and the question's title, naming neither its directory nor its line, and its summary the why", () => {
  const need = rowNeed(makeRow({ label: "api", question: asksAboutDatabase, summary: "schema change" }));
  assert.equal(need.text, "asks: Which database?");
  assert.equal(need.summary, "schema change");
  assert.equal(need.kind, "row");
});

test("a waiting row with no question reads that it waits on you, naming neither its directory nor its line, and its summary the why", () => {
  const need = rowNeed(makeRow({ label: "web", question: "", summary: "build finished" }));
  assert.equal(need.text, "waits on you");
  assert.equal(need.summary, "build finished");
  assert.equal(need.kind, "row");
});

test("an open question reads its slug asks, as a question need", () => {
  const needs = needsFor({ questions: [{ id: "Q1", text: "Use Postgres?" }] });
  assert.deepEqual(textsOf(needs), ["alpha asks: Use Postgres?"]);
  assert.equal(needs[0].kind, "question");
});

test("two open questions give two needs, in order", () => {
  const needs = needsFor({
    questions: [
      { id: "Q1", text: "Use Postgres?" },
      { id: "Q2", text: "Ship on Friday?" },
    ],
  });
  assert.deepEqual(textsOf(needs), ["alpha asks: Use Postgres?", "alpha asks: Ship on Friday?"]);
  assert.deepEqual(
    needs.map((need) => need.kind),
    ["question", "question"],
  );
  assert.deepEqual(
    needs.map((need) => `${need.epic} ${need.question}`),
    ["repo-a/alpha Q1", "repo-a/alpha Q2"],
  );
});

test("a stopped epic with a failed release step 2 and a park gives the release need and no park need", () => {
  const needs = needsFor({
    parked: "budget",
    releaseSteps: [
      { position: 1n, step: "tag", state: "passed" },
      { position: 2n, step: "publish", state: "failed", reason: "registry 503" },
      { position: 3n, step: "announce", state: "waiting" },
    ],
  });
  assert.deepEqual(textsOf(needs), ["alpha: release step 2 (publish) failed: registry 503"]);
  assert.equal(needs[0].kind, "release");
  assert.equal(needs[0].epic, "repo-a/alpha");
});

test("a failed release step gives one release need, the first in order", () => {
  const needs = needsFor({
    releaseSteps: [
      { position: 1n, step: "tag", state: "failed", reason: "tag exists" },
      { position: 2n, step: "publish", state: "failed", reason: "registry 503" },
    ],
  });
  assert.deepEqual(textsOf(needs), ["alpha: release step 1 (tag) failed: tag exists"]);
});

test("release steps that have not failed give no release need", () => {
  const needs = needsFor({
    releaseSteps: [
      { position: 1n, step: "tag", state: "passed" },
      { position: 2n, step: "publish", state: "running" },
      { position: 3n, step: "announce", state: "waiting" },
    ],
  });
  assert.deepEqual(needs, []);
});

test("a parked epic reads its park, as a park need", () => {
  const needs = needsFor({ parked: "budget" });
  assert.deepEqual(textsOf(needs), ["alpha is parked: budget"]);
  assert.equal(needs[0].kind, "park");
});

test("a held step gives its slug and task held, as an other need", () => {
  const needs = needsFor({ steps: [{ id: "s1", task: "web-line", held: true }] });
  assert.deepEqual(textsOf(needs), ["alpha: web-line held"]);
  assert.equal(needs[0].kind, "other");
});

test("a limited step the client does not name reads its own text, as an other need", () => {
  const needs = needsFor({ steps: [{ id: "s2", task: "build", limited: true, limitKind: "usage" }] });
  assert.deepEqual(textsOf(needs), ["alpha: build waits on a usage limit"]);
  assert.equal(needs[0].kind, "other");
});

test("an open question beside a held step gives both needs", () => {
  const needs = needsFor({
    questions: [{ id: "Q1", text: "Use Postgres?" }],
    steps: [{ id: "s1", task: "web-line", held: true }],
  });
  assert.deepEqual(textsOf(needs), ["alpha asks: Use Postgres?", "alpha: web-line held"]);
});

test("an epic with no blocker gives no need", () => {
  assert.deepEqual(needsFor({}), []);
});

test("an epic that is not live, or has no detail, gives no need", () => {
  const ended = makeEpic({ live: false, outcome: "released", source: "orreryd" });
  assert.deepEqual(needsFor({ questions: [{ id: "Q1", text: "Use Postgres?" }] }, ended), []);
  assert.deepEqual(epicNeeds([makeEpic({})], new Map()), []);
});

test("OrchestratorPark: a planner-parked epic with a park and a failed release step raises only its open question's need", () => {
  const needs = needsFor({
    questions: [{ id: "Q1", text: "Use Postgres?" }],
    parked: "release step 2 (publish) failed",
    plannerParked: true,
    releaseSteps: [{ position: 2n, step: "publish", state: "failed", reason: "registry 503" }],
  });
  assert.deepEqual(needs, [
    { kind: "question", text: "alpha asks: Use Postgres?", summary: "", epic: "repo-a/alpha", question: "Q1" },
  ]);
});
