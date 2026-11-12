import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { PaneSchema, SessionSchema, type Pane, type Session } from "./gen/orrery/v1/orrery_pb";
import { byAttention, byProject, rowsFrom, type Row, type Section } from "./rows";

function makePane(init: MessageInitShape<typeof PaneSchema>): Pane {
  return create(PaneSchema, { host: "box", session: "work", cwd: "/work/app", ...init });
}

function makeSession(init: MessageInitShape<typeof SessionSchema>): Session {
  return create(SessionSchema, init);
}

function keysOf(sections: Section[]): [string, string[]][] {
  return sections.map((section) => [section.title, section.rows.map((row: Row) => row.key)]);
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
      kind: "pane",
      label: "api",
      project: "/work/api",
      host: "box",
      state: "working",
      summary: "building",
      question: "",
      questionHash: "",
      changed: 40,
      seen: 10,
      unseen: true,
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

test("a pane row without a label is named by its tmux session", () => {
  const [row] = rowsFrom([makePane({ id: "%4", session: "deploy", label: "" })], []);
  assert.equal(row?.label, "deploy");
});

test("a session row is keyed by its id, named by its first eight characters, and has no project or host", () => {
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
      kind: "session",
      label: "01234567",
      project: "",
      host: "",
      state: "done",
      summary: "finished",
      question: "",
      questionHash: "",
      changed: 42,
      seen: 42,
      unseen: false,
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

test("byAttention arranges rows into the four sections in order, newest first, and omits empty sections", () => {
  const rows = rowsFrom(
    [
      makePane({ id: "%1", state: "waiting", changed: 100n, seen: 50n }),
      makePane({ id: "%2", state: "waiting", changed: 300n, seen: 300n }),
      makePane({ id: "%3", state: "working", changed: 200n, seen: 10n }),
      makePane({ id: "%4", state: "done", changed: 400n, seen: 1n }),
      makePane({ id: "%5", state: "working", changed: 90n, seen: 90n }),
      makePane({ id: "%6", state: "done", changed: 80n, seen: 90n }),
      makePane({ id: "%7", state: "", changed: 500n, seen: 0n }),
      makePane({ id: "%8", session: "_orrery-shell-2", state: "waiting", changed: 999n }),
    ],
    [
      makeSession({ id: "abcdefghijk", pane: "", state: "working", changed: "150", seen: "150" }),
      makeSession({ id: "x", pane: "", state: "", changed: "x", seen: "0" }),
    ],
  );

  assert.deepEqual(keysOf(byAttention(rows)), [
    ["Needs you", ["pane:box:%2", "pane:box:%1"]],
    ["New since you looked", ["pane:box:%4", "pane:box:%3"]],
    ["Working", ["session:abcdefghijk", "pane:box:%5"]],
    ["Seen", ["pane:box:%7", "pane:box:%6", "session:x"]],
  ]);
});

test("byAttention omits sections with no rows", () => {
  const rows = rowsFrom([makePane({ id: "%1", state: "done", changed: 1n, seen: 1n })], []);
  assert.deepEqual(keysOf(byAttention(rows)), [["Seen", ["pane:box:%1"]]]);
});

test("byAttention and byProject leave the input rows in their original order", () => {
  const rows = rowsFrom(
    [
      makePane({ id: "%1", state: "done", changed: 1n, seen: 1n }),
      makePane({ id: "%2", state: "waiting", changed: 5n, seen: 5n }),
    ],
    [],
  );
  const before = rows.map((row) => row.key);

  byAttention(rows);
  byProject(rows);

  assert.deepEqual(
    rows.map((row) => row.key),
    before,
  );
});

test("byProject groups panes by directory and session rows under Sessions, in rank order", () => {
  const rows = rowsFrom(
    [
      makePane({ id: "%1", host: "box", cwd: "/b/proj", label: "b-done", state: "done", changed: 1n, seen: 1n }),
      makePane({ id: "%2", host: "box", cwd: "/a/proj", label: "a-wait", state: "waiting", changed: 1n, seen: 1n }),
      makePane({ id: "%3", host: "box", cwd: "/b/proj", state: "working", changed: 9n, seen: 1n }),
      makePane({ id: "%4", host: "box", cwd: "/c/quiet", state: "done", changed: 1n, seen: 1n }),
      makePane({ id: "%5", host: "alpha", cwd: "/a/proj", label: "a-wait", state: "waiting", changed: 1n, seen: 1n }),
      makePane({ id: "%6", host: "box", cwd: "/b/idle", state: "", changed: 0n, seen: 0n }),
      makePane({ id: "%7", host: "box", cwd: "/a/proj", label: "a-wait", state: "waiting", changed: 1n, seen: 1n }),
    ],
    [makeSession({ id: "s-one", pane: "", state: "working", changed: "3", seen: "3" })],
  );

  assert.deepEqual(keysOf(byProject(rows)), [
    ["/a/proj", ["pane:alpha:%5", "pane:box:%2", "pane:box:%7"]],
    ["/b/proj", ["pane:box:%3", "pane:box:%1"]],
    ["Sessions", ["session:s-one"]],
    ["/b/idle", ["pane:box:%6"]],
    ["/c/quiet", ["pane:box:%4"]],
  ]);
});
