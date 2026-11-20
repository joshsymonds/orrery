import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  HeadlessSessionSchema,
  PaneSchema,
  SessionSchema,
  type HeadlessSession,
  type Pane,
  type Session,
} from "./gen/orrery/v1/orrery_pb";
import { rowsFrom, splitArchived, type Row } from "./rows";

function makePane(init: MessageInitShape<typeof PaneSchema>): Pane {
  return create(PaneSchema, { host: "box", session: "work", cwd: "/work/app", ...init });
}

function makeSession(init: MessageInitShape<typeof SessionSchema>): Session {
  return create(SessionSchema, init);
}

function makeHeadless(init: MessageInitShape<typeof HeadlessSessionSchema>): HeadlessSession {
  return create(HeadlessSessionSchema, init);
}


test("a pane row takes its label, project, host, state, text, and times from the pane", () => {
  const rows = rowsFrom(
    [
      makePane({
        id: "%3",
        host: "box",
        label: "api",
        cwd: "/work/api",
        state: "working",
        summary: "building",
        question: "",
        changed: 40n,
        seen: 10n,
      }),
    ],
    [],
  );

  assert.deepEqual(rows, [
    {
      key: "pane:box:%3",
      id: "%3",
      archiveId: "",
      kind: "pane",
      label: "building",
      project: "/work/api",
      host: "box",
      state: "working",
      summary: "building",
      question: "",
      questionHash: "",
      changed: 40,
      seen: 10,
      unseen: true,
      archived: false,
      tool: "",
      archiveKind: "none",
    },
  ]);
});

test("a pane row carries the pane's id and question hash, and a session row carries the session's id with no hash", () => {
  const [paneRow] = rowsFrom(
    [makePane({ id: "%3", host: "box", questionHash: "hash-of-question", state: "waiting" })],
    [],
  );
  const [sessionRow] = rowsFrom([], [makeSession({ id: "sess-7", pane: "", state: "done" })]);

  assert.equal(paneRow?.id, "%3");
  assert.equal(paneRow?.questionHash, "hash-of-question");
  assert.equal(sessionRow?.id, "sess-7");
  assert.equal(sessionRow?.questionHash, "");
});

test("a pane row carries its pane's running tool, and a session or headless row carries an empty tool", () => {
  const tool = JSON.stringify({ id: "toolu_1", name: "Bash", input: "go test ./...", start: 1781000000 });
  const rows = rowsFrom(
    [makePane({ id: "%11", tool })],
    [makeSession({ id: "sess-7", pane: "" })],
    [makeHeadless({ id: "head-1" })],
  );

  assert.deepEqual(
    rows.map((row) => [row.id, row.tool]),
    [
      ["%11", tool],
      ["sess-7", ""],
      ["head-1", ""],
    ],
  );
});

test("TitleParts: a pane with no summary has an empty line, never its tmux session or label, so its title is its directory alone", () => {
  const [row] = rowsFrom([makePane({ id: "%4", session: "deploy", label: "api", cwd: "/work/app", summary: "" })], []);
  assert.equal(row?.label, "");
  assert.equal(row?.project, "/work/app");
});

test("a session row is keyed by its id, reads 'session' as its line when it has no label, and has no project or host", () => {
  const rows = rowsFrom(
    [],
    [
      makeSession({
        id: "0123456789abcdef",
        pane: "",
        label: "",
        state: "done",
        summary: "finished",
        question: "",
        changed: "42",
        seen: "42",
      }),
    ],
  );

  assert.deepEqual(rows, [
    {
      key: "session:0123456789abcdef",
      id: "0123456789abcdef",
      archiveId: "0123456789abcdef",
      kind: "session",
      label: "session",
      project: "",
      host: "",
      state: "done",
      summary: "finished",
      question: "",
      questionHash: "",
      changed: 42,
      seen: 42,
      unseen: false,
      archived: false,
      tool: "",
      archiveKind: "session",
    },
  ]);
});

test("a session row reads a label when one is set and treats non-numeric times as zero", () => {
  const [row] = rowsFrom(
    [],
    [makeSession({ id: "s2", pane: "", label: "nightly", state: "working", changed: "7", seen: "soon" })],
  );

  assert.equal(row?.label, "nightly");
  assert.equal(row?.changed, 7);
  assert.equal(row?.seen, 0);
  assert.equal(row?.unseen, true);
});

test("TitleParts: a headless row's line is its served headline and its project its directory, with no id in either, and an empty headline leaves the line empty", () => {
  const rows = rowsFrom(
    [],
    [],
    [
      makeHeadless({
        id: "0123456789abcdef",
        dir: "/work/app",
        label: "app · 01234567",
        headline: "",
        state: "idle",
        changed: 42n,
        seen: 42n,
      }),
      makeHeadless({
        id: "fedcba9876543210",
        dir: "/work/app",
        label: "app · fedcba98",
        headline: "Billing rounding plan",
        state: "working",
        changed: 7n,
        seen: 0n,
      }),
    ],
  );

  assert.deepEqual(
    rows.map((row) => [row.id, row.label, row.project]),
    [
      ["0123456789abcdef", "", "/work/app"],
      ["fedcba9876543210", "Billing rounding plan", "/work/app"],
    ],
  );
});

test("ArchiveView: rowsFrom marks archived session and headless rows with their archive kinds, and a pane with no orrery_id has no archive kind", () => {
  const rows = rowsFrom(
    [makePane({ id: "%3", orreryId: "" })],
    [makeSession({ id: "sess-archived", pane: "", archived: true }), makeSession({ id: "sess-live", pane: "" })],
    [makeHeadless({ id: "head-archived", dir: "/work/app", archived: true }), makeHeadless({ id: "head-live" })],
  );

  assert.deepEqual(
    rows.map((row) => [row.id, row.archived, row.archiveKind]),
    [
      ["%3", false, "none"],
      ["sess-archived", true, "session"],
      ["sess-live", false, "session"],
      ["head-archived", true, "headless"],
      ["head-live", false, "headless"],
    ],
  );
});

test("ArchivePaneRow: a pane with an orrery_id archives as the session that id names, and reads archived from that session, or not when the session is missing", () => {
  const rows = rowsFrom(
    [
      makePane({ id: "%1", orreryId: "sess-archived" }),
      makePane({ id: "%2", orreryId: "sess-live" }),
      makePane({ id: "%4", orreryId: "sess-missing" }),
    ],
    [
      makeSession({ id: "sess-archived", pane: "%1", archived: true }),
      makeSession({ id: "sess-live", pane: "%2" }),
    ],
  );

  assert.deepEqual(
    rows.map((row) => [row.id, row.archiveId, row.archiveKind, row.archived]),
    [
      ["%1", "sess-archived", "session", true],
      ["%2", "sess-live", "session", false],
      ["%4", "sess-missing", "session", false],
    ],
  );
});

test("ArchiveView: splitArchived leaves archived rows out of the default rows and lists them as archived, each in input order", () => {
  const rows = rowsFrom(
    [makePane({ id: "%1" })],
    [
      makeSession({ id: "sess-z", pane: "", archived: true }),
      makeSession({ id: "sess-y", pane: "" }),
      makeSession({ id: "sess-x", pane: "", archived: true }),
    ],
    [makeHeadless({ id: "head-w" })],
  );

  const split = splitArchived(rows);

  assert.deepEqual(
    split.rows.map((row) => row.key),
    ["pane:box:%1", "session:sess-y", "session:head-w"],
  );
  assert.deepEqual(
    split.archived.map((row) => row.key),
    ["session:sess-z", "session:sess-x"],
  );
});

test("panes in shell sessions are dropped", () => {
  const rows = rowsFrom([makePane({ id: "%9", session: "_orrery-shell-1" })], []);
  assert.deepEqual(rows, []);
});

test("a session entry that names a pane is dropped, since Panes.List already lists that pane", () => {
  const rows = rowsFrom([], [makeSession({ id: "s1", pane: "%3", state: "working" })]);
  assert.deepEqual(rows, []);
});

test("a row is unseen only when it has a state and changed after it was seen", () => {
  const cases: { name: string; state: string; changed: bigint; seen: bigint; unseen: boolean }[] = [
    { name: "a state and changed after seen", state: "working", changed: 2n, seen: 1n, unseen: true },
    { name: "changed equal to seen", state: "working", changed: 1n, seen: 1n, unseen: false },
    { name: "changed after seen but no state", state: "", changed: 5n, seen: 1n, unseen: false },
  ];

  for (const testCase of cases) {
    const [row] = rowsFrom(
      [makePane({ id: "%1", state: testCase.state, changed: testCase.changed, seen: testCase.seen })],
      [],
    );
    assert.equal(row?.unseen, testCase.unseen, testCase.name);
  }
});
