import assert from "node:assert/strict";
import { test } from "node:test";

import { formatAddress, parseAddress, type Address, type Order } from "./router";

const ORDERS: readonly Order[] = ["attention", "project", "archived"];

// An address goes out as its hash and comes back as the same address.
function assertRoundTrip(address: Address): void {
  assert.deepEqual(parseAddress(formatAddress(address)), address);
}

for (const order of ORDERS) {
  test(`HashRoute round-trips the ${order} order with no entry`, () => {
    assertRoundTrip({ order, entry: null });
  });
}

test("HashRoute round-trips a session entry", () => {
  assertRoundTrip({ order: "project", entry: { kind: "session", id: "S-1" } });
});

test("SidePanel: round-trips a session entry with the agents panel open", () => {
  assertRoundTrip({ order: "project", entry: { kind: "session", id: "S-1", panel: "agents" } });
});

test("SidePanel: round-trips a session entry with the running panel open", () => {
  assertRoundTrip({ order: "project", entry: { kind: "session", id: "S-1", panel: "running" } });
});

test("SidePanel: writes the open panel as the last segment of the session hash", () => {
  assert.equal(
    formatAddress({ order: "project", entry: { kind: "session", id: "S-1", panel: "running" } }),
    "#/project/session/S-1/running",
  );
});

test("SidePanel: parses a session hash with any other third segment to the attention inbox with no entry", () => {
  for (const segment of ["Agents", "running ", "sessions", "extra", ""]) {
    assert.deepEqual(
      parseAddress(`#/project/session/S-1/${segment}`),
      { order: "attention", entry: null },
      `segment ${JSON.stringify(segment)}`,
    );
  }
});

test("SidePanel: a session entry with no panel still round-trips with no panel key", () => {
  const parsed = parseAddress("#/project/session/S-1");
  assert.deepEqual(parsed, { order: "project", entry: { kind: "session", id: "S-1" } });
  assert.equal(parsed.entry !== null && "panel" in parsed.entry, false);
  assertRoundTrip({ order: "project", entry: { kind: "session", id: "S-1" } });
});

test("HashRoute round-trips a pane entry", () => {
  assertRoundTrip({ order: "attention", entry: { kind: "pane", host: "laptop", pane: "pane-2" } });
});

test("HashRoute round-trips a step entry", () => {
  assertRoundTrip({ order: "archived", entry: { kind: "step", id: "step-9" } });
});

test("HashRoute round-trips the epic page", () => {
  assertRoundTrip({ order: "attention", entry: { kind: "epic", id: "E-1", page: { kind: "epic" } } });
});

test("HashRoute round-trips an epic's contract", () => {
  assertRoundTrip({ order: "attention", entry: { kind: "epic", id: "E-1", page: { kind: "contract" } } });
});

test("HashRoute round-trips an epic's phase output", () => {
  assertRoundTrip({
    order: "project",
    entry: { kind: "epic", id: "E-1", page: { kind: "phase", task: "T2", phase: "checking" } },
  });
});

test("HashRoute round-trips an epic's worker session", () => {
  assertRoundTrip({
    order: "project",
    entry: { kind: "epic", id: "E-1", page: { kind: "session", task: "T2", phase: "implementing", step: "step-4" } },
  });
});

test("HashRoute round-trips a pane id of the form %N", () => {
  const address: Address = { order: "attention", entry: { kind: "pane", host: "laptop", pane: "%3" } };
  assert.equal(formatAddress(address), "#/attention/pane/laptop/%253");
  assertRoundTrip(address);
});

test("HashRoute round-trips an epic id holding a slash", () => {
  const address: Address = { order: "project", entry: { kind: "epic", id: "team/E-2", page: { kind: "contract" } } };
  assert.equal(formatAddress(address), "#/project/epic/team%2FE-2/contract");
  assertRoundTrip(address);
});

test("HashRoute writes the documented hash form for an epic's sub-page", () => {
  assert.equal(
    formatAddress({
      order: "project",
      entry: { kind: "epic", id: "E-1", page: { kind: "session", task: "T2", phase: "checking", step: "step-4" } },
    }),
    "#/project/epic/E-1/session/T2/checking/step-4",
  );
});

test("HashRoute parses a hash with or without its leading #", () => {
  const address: Address = { order: "project", entry: { kind: "session", id: "S-1" } };
  assert.deepEqual(parseAddress("#/project/session/S-1"), address);
  assert.deepEqual(parseAddress("/project/session/S-1"), address);
});

test("HashRoute parses a malformed percent escape to the attention inbox", () => {
  assert.deepEqual(parseAddress("#/project/session/%E0%A4%A"), { order: "attention", entry: null });
  assert.deepEqual(parseAddress("#/project/epic/E-1/phase/%E0%A4%A/checking"), { order: "attention", entry: null });
});

test("HashRoute parses other unreadable hashes to the attention inbox with no entry", () => {
  const unreadable = [
    "",
    "#",
    "#/",
    "#/unknown",
    "#/project/unknown/x",
    "#/project/session",
    "#/project/session/S-1/extra",
    "#/project/session/",
    "#/project/pane/laptop",
    "#/project/step",
    "#/project/epic",
    "#/project/epic/",
    "#/project/epic/E-1/epic",
    "#/project/epic/E-1/contract/extra",
    "#/project/epic/E-1/phase/T2/merging",
    "#/project/epic/E-1/session/T2/checking",
    "#/project/session/S-1/contract",
  ];
  for (const hash of unreadable) {
    assert.deepEqual(parseAddress(hash), { order: "attention", entry: null }, `hash ${JSON.stringify(hash)}`);
  }
});
