import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";

import { epicsView, noEpics } from "./epics";
import { ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { EpicSchema, EpicStepSchema } from "./gen/orrery/v1/epics_pb";
import { listHolds } from "./listholds";
import type { Address, Entry, Order } from "./router";
import type { Row } from "./rows";

const orders: Order[] = ["attention", "project", "archived"];

// at is the address of order with entry.
function at(order: Order, entry: Entry | null): Address {
  return { order, entry };
}

// rowOf is a pane or session row, archived or not, on host. Only the fields listHolds reads carry a meaning here.
function rowOf(kind: Row["kind"], id: string, archived: boolean, host = "box"): Row {
  return {
    key: `${kind}:${host}:${id}`,
    id,
    archiveId: id,
    kind,
    label: "",
    project: "",
    host,
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived,
    archiveKind: "session",
  };
}

// The epics the lines hold: one ended earlier, one archived, and one live epic whose running step is step-1.
const endedEpic = create(EpicSchema, {
  id: "repo/ended",
  repository: "repo",
  slug: "ended",
  live: false,
  phaseAt: 5n,
  seen: 5n,
});
const archivedEpic = create(EpicSchema, {
  id: "repo/archived",
  repository: "repo",
  slug: "archived",
  live: false,
  archived: true,
});
const liveEpic = create(EpicSchema, {
  id: "repo/live",
  repository: "repo",
  slug: "live",
  live: true,
  runningSteps: [create(EpicStepSchema, { id: "step-1", task: "step one", agent: "builder" })],
});
const epics = epicsView([endedEpic, archivedEpic, liveEpic], new Map());

function epicEntry(id: string): Entry {
  return { kind: "epic", id, page: { kind: "epic" } };
}

test("HashRoute: an address with no entry holds under every order", () => {
  for (const order of orders) {
    assert.equal(listHolds(at(order, null), [], noEpics), true, `${order} did not hold an address with no entry`);
  }
});

test("HashRoute: a session holds under attention and project when its row is not archived", () => {
  const rows = [rowOf("session", "s1", false)];
  const session: Entry = { kind: "session", id: "s1" };
  assert.equal(listHolds(at("attention", session), rows, noEpics), true);
  assert.equal(listHolds(at("project", session), rows, noEpics), true);
});

test("HashRoute: a session under attention or project does not hold when its row is archived", () => {
  const rows = [rowOf("session", "s1", true)];
  const session: Entry = { kind: "session", id: "s1" };
  assert.equal(listHolds(at("attention", session), rows, noEpics), false);
  assert.equal(listHolds(at("project", session), rows, noEpics), false);
});

test("HashRoute: a session holds under archived only when its row is archived", () => {
  const session: Entry = { kind: "session", id: "s1" };
  assert.equal(listHolds(at("archived", session), [rowOf("session", "s1", true)], noEpics), true);
  assert.equal(listHolds(at("archived", session), [rowOf("session", "s1", false)], noEpics), false);
});

test("HashRoute: a pane holds by its host and pane id, under the line its archive state gives", () => {
  const pane: Entry = { kind: "pane", host: "box", pane: "%3" };
  assert.equal(listHolds(at("attention", pane), [rowOf("pane", "%3", false)], noEpics), true);
  assert.equal(listHolds(at("project", pane), [rowOf("pane", "%3", false)], noEpics), true);
  assert.equal(listHolds(at("attention", pane), [rowOf("pane", "%3", true)], noEpics), false);
  assert.equal(listHolds(at("archived", pane), [rowOf("pane", "%3", true)], noEpics), true);
  assert.equal(listHolds(at("archived", pane), [rowOf("pane", "%3", false)], noEpics), false);
  assert.equal(
    listHolds(at("attention", pane), [rowOf("pane", "%3", false, "other")], noEpics),
    false,
    "a pane on another host held the entry",
  );
});

test("HashRoute: an id no row, step or epic carries holds under no order", () => {
  const rows = [rowOf("session", "s1", false), rowOf("pane", "%3", false)];
  const absent: Entry[] = [
    { kind: "session", id: "gone" },
    { kind: "pane", host: "box", pane: "%9" },
    { kind: "step", id: "gone-step" },
    epicEntry("repo/gone"),
  ];
  for (const order of orders) {
    for (const entry of absent) {
      assert.equal(listHolds(at(order, entry), rows, epics), false, `${order} held ${JSON.stringify(entry)}`);
    }
  }
});

test("HashRoute: an epic in ended earlier holds under attention and project, and not under archived", () => {
  const ended = epicEntry("repo/ended");
  assert.equal(listHolds(at("attention", ended), [], epics), true);
  assert.equal(listHolds(at("project", ended), [], epics), true);
  assert.equal(listHolds(at("archived", ended), [], epics), false);
});

test("HashRoute: an epic in the archived line holds under archived only", () => {
  const archived = epicEntry("repo/archived");
  assert.equal(listHolds(at("archived", archived), [], epics), true);
  assert.equal(listHolds(at("attention", archived), [], epics), false);
  assert.equal(listHolds(at("project", archived), [], epics), false);
});

test("HashRoute: an epic in a block holds under attention and project, and not under archived", () => {
  const live = epicEntry("repo/live");
  assert.equal(listHolds(at("attention", live), [], epics), true);
  assert.equal(listHolds(at("project", live), [], epics), true);
  assert.equal(listHolds(at("archived", live), [], epics), false);
});

test("ContractAddress: a contract entry holds under every order when the loaded contracts hold its id", () => {
  const contract: Entry = { kind: "contract", id: "tidepool/tide-alerts" };
  const contracts = [create(ContractSchema, { id: "tidepool/tide-alerts" })];
  for (const order of orders) {
    assert.equal(listHolds(at(order, contract), [], noEpics, contracts), true, `${order} did not hold it`);
    assert.equal(listHolds(at(order, contract), [], noEpics, []), false, `${order} held it with no contracts`);
  }
});

test("HashRoute: a step in an epic block holds under attention and project, and never under archived", () => {
  const step: Entry = { kind: "step", id: "step-1" };
  assert.equal(listHolds(at("attention", step), [], epics), true);
  assert.equal(listHolds(at("project", step), [], epics), true);
  assert.equal(listHolds(at("archived", step), [], epics), false);
});
