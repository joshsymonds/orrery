import assert from "node:assert/strict";
import { test } from "node:test";

import { formatAddress, parseAddress, type Address, type View } from "./router";

const VIEWS: readonly View[] = ["inbox", "archived"];

// An address goes out as its hash and comes back as the same address.
function assertRoundTrip(address: Address): void {
  assert.deepEqual(parseAddress(formatAddress(address)), address);
}

for (const view of VIEWS) {
  test(`HashRoute round-trips the ${view} view with no entry`, () => {
    assertRoundTrip({ view, entry: null });
  });
}

test("HashRoute round-trips a session entry", () => {
  assertRoundTrip({ view: "inbox", entry: { kind: "session", id: "S-1" } });
});

test("SidePanel: round-trips a session entry with the agents panel open", () => {
  assertRoundTrip({ view: "inbox", entry: { kind: "session", id: "S-1", panel: "agents" } });
});

test("SidePanel: round-trips a session entry with the running panel open", () => {
  assertRoundTrip({ view: "inbox", entry: { kind: "session", id: "S-1", panel: "running" } });
});

test("SidePanel: writes the open panel as the last segment of the session hash", () => {
  assert.equal(
    formatAddress({ view: "inbox", entry: { kind: "session", id: "S-1", panel: "running" } }),
    "#/inbox/session/S-1/running",
  );
});

test("SidePanel: parses a session hash with any other third segment to the inbox with no entry", () => {
  for (const segment of ["Agents", "running ", "sessions", "extra", ""]) {
    assert.deepEqual(
      parseAddress(`#/inbox/session/S-1/${segment}`),
      { view: "inbox", entry: null },
      `segment ${JSON.stringify(segment)}`,
    );
  }
});

test("HashRoute round-trips a pane entry", () => {
  assertRoundTrip({ view: "inbox", entry: { kind: "pane", host: "laptop", pane: "pane-2" } });
});

test("HashRoute round-trips a step entry", () => {
  assertRoundTrip({ view: "archived", entry: { kind: "step", id: "step-9" } });
});

test("HashRoute round-trips the epic page", () => {
  assertRoundTrip({ view: "inbox", entry: { kind: "epic", id: "E-1", page: { kind: "epic" } } });
});

test("HashRoute round-trips an epic's contract", () => {
  assertRoundTrip({ view: "inbox", entry: { kind: "epic", id: "E-1", page: { kind: "contract" } } });
});

test("HashRoute round-trips an epic's phase output", () => {
  assertRoundTrip({
    view: "inbox",
    entry: { kind: "epic", id: "E-1", page: { kind: "phase", task: "T2", phase: "checking" } },
  });
});

test("HashRoute round-trips an epic's worker session", () => {
  assertRoundTrip({
    view: "inbox",
    entry: { kind: "epic", id: "E-1", page: { kind: "session", task: "T2", phase: "implementing", step: "step-4" } },
  });
});

test("HashRoute round-trips a pane id of the form %N", () => {
  const address: Address = { view: "inbox", entry: { kind: "pane", host: "laptop", pane: "%3" } };
  assert.equal(formatAddress(address), "#/inbox/pane/laptop/%253");
  assertRoundTrip(address);
});

test("HashRoute round-trips an epic id holding a slash", () => {
  const address: Address = { view: "inbox", entry: { kind: "epic", id: "team/E-2", page: { kind: "contract" } } };
  assert.equal(formatAddress(address), "#/inbox/epic/team%2FE-2/contract");
  assertRoundTrip(address);
});

test("HashRoute writes the documented hash form for an epic's sub-page", () => {
  assert.equal(
    formatAddress({
      view: "inbox",
      entry: { kind: "epic", id: "E-1", page: { kind: "session", task: "T2", phase: "checking", step: "step-4" } },
    }),
    "#/inbox/epic/E-1/session/T2/checking/step-4",
  );
});

test("HashRoute parses a hash with or without its leading #", () => {
  const address: Address = { view: "inbox", entry: { kind: "session", id: "S-1" } };
  assert.deepEqual(parseAddress("#/inbox/session/S-1"), address);
  assert.deepEqual(parseAddress("/inbox/session/S-1"), address);
});

test("HashRoute parses a malformed percent escape to the inbox", () => {
  assert.deepEqual(parseAddress("#/inbox/session/%E0%A4%A"), { view: "inbox", entry: null });
  assert.deepEqual(parseAddress("#/inbox/epic/E-1/phase/%E0%A4%A/checking"), { view: "inbox", entry: null });
});

test("HashRoute parses other unreadable hashes to the inbox with no entry", () => {
  const unreadable = [
    "",
    "#",
    "#/",
    "#/unknown",
    "#/inbox/unknown/x",
    "#/inbox/session",
    "#/inbox/session/S-1/extra",
    "#/inbox/session/",
    "#/inbox/pane/laptop",
    "#/inbox/step",
    "#/inbox/epic",
    "#/inbox/epic/",
    "#/inbox/epic/E-1/epic",
    "#/inbox/epic/E-1/contract/extra",
    "#/inbox/epic/E-1/phase/T2/merging",
    "#/inbox/epic/E-1/session/T2/checking",
    "#/inbox/session/S-1/contract",
  ];
  for (const hash of unreadable) {
    assert.deepEqual(parseAddress(hash), { view: "inbox", entry: null }, `hash ${JSON.stringify(hash)}`);
  }
});

test("InboxTree: the old attention and project addresses open the inbox with the same entry", () => {
  for (const order of ["attention", "project"]) {
    assert.deepEqual(parseAddress(`#/${order}`), { view: "inbox", entry: null }, order);
    assert.deepEqual(
      parseAddress(`#/${order}/session/S-1`),
      { view: "inbox", entry: { kind: "session", id: "S-1" } },
      order,
    );
    assert.deepEqual(
      parseAddress(`#/${order}/pane/laptop/%253`),
      { view: "inbox", entry: { kind: "pane", host: "laptop", pane: "%3" } },
      order,
    );
    assert.deepEqual(
      parseAddress(`#/${order}/epic/E-1/phase/T2/checking`),
      { view: "inbox", entry: { kind: "epic", id: "E-1", page: { kind: "phase", task: "T2", phase: "checking" } } },
      order,
    );
  }
});

test("InboxTree: the address written for the inbox names no order", () => {
  assert.equal(formatAddress({ view: "inbox", entry: null }), "#/inbox");
  assert.equal(
    formatAddress({ view: "inbox", entry: { kind: "session", id: "S-1" } }),
    "#/inbox/session/S-1",
  );
  assert.equal(formatAddress({ view: "archived", entry: null }), "#/archived");
});

const PROJECT_KEYS = ["|/home/owner/code/lighthouse", "brine|/home/owner/code/kelp", "repo:orrery", "nodir"];

test("ProjectPage address: formats a project entry as the view, project, and its encoded key", () => {
  for (const key of PROJECT_KEYS) {
    assert.equal(
      formatAddress({ view: "inbox", entry: { kind: "project", key } }),
      `#/inbox/project/${encodeURIComponent(key)}`,
      key,
    );
  }
  assert.equal(
    formatAddress({ view: "inbox", entry: { kind: "project", key: "|/home/owner/code/lighthouse" } }),
    "#/inbox/project/%7C%2Fhome%2Fowner%2Fcode%2Flighthouse",
  );
});

test("ProjectPage address: round-trips a project entry for keys with a pipe, slashes, a colon and a host", () => {
  for (const key of PROJECT_KEYS) {
    assertRoundTrip({ view: "inbox", entry: { kind: "project", key } });
  }
});

test("ProjectPage address: the archived view keeps its view with a project entry", () => {
  const address: Address = { view: "archived", entry: { kind: "project", key: "repo:orrery" } };
  assert.equal(formatAddress(address), "#/archived/project/repo%3Aorrery");
  assertRoundTrip(address);
});

test("ProjectPage address: a project address with no key reads as the inbox", () => {
  assert.deepEqual(parseAddress("#/inbox/project"), { view: "inbox", entry: null });
  assert.deepEqual(parseAddress("#/inbox/project/"), { view: "inbox", entry: null });
});

test("ProjectPage address: the old project first segment reads a project entry under the inbox", () => {
  assert.deepEqual(parseAddress("#/project/project/repo%3Aorrery"), {
    view: "inbox",
    entry: { kind: "project", key: "repo:orrery" },
  });
});
