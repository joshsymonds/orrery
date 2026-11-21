import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { epicRowWords } from "./epicrow";
import { blockOf } from "./epics";
import { epics, gets } from "./fixtures/epics";
import type { Epic } from "./gen/orrery/v1/epics_pb";
import { type NoticeKey, NoticeBlockSchema, NoticeKeySchema } from "./gen/orrery/v1/notice_pb";
import type { Row } from "./rows";
import type { EpicNode, Hung, Project } from "./tree";
import {
  bringBackRowOf,
  type NoticeFields,
  noticeAct,
  noticeAge,
  noticeClock,
  noticeDays,
  noticeHead,
  noticeKey,
} from "./noticewords";

// secs is an ISO instant as unix seconds, the form the notice times and now take.
function secs(iso: string): number {
  return Date.parse(iso) / 1000;
}

// stamp is an ISO instant as the bigint a notice's time is.
function stamp(iso: string): bigint {
  return BigInt(secs(iso));
}

// now is a Monday, 16 November 2026, at 14:41 UTC, so the day heads are fixed.
const now = secs("2026-11-16T14:41:00Z");

// listedEpic is the Epics.List row of an id in the fixtures.
function listedEpic(id: string): Epic {
  const found = epics.epics.find((epic) => epic.id === id);
  if (found === undefined) {
    throw new Error(`the fixture epics hold no row for ${id}`);
  }
  return found;
}

// epicNode hangs a fixture epic under a conversation as the tree does, its block read from its detail.
function epicNode(id: string): EpicNode {
  const listed = listedEpic(id);
  return { kind: "epic", key: `epic:${id}`, block: blockOf(listed, gets.get(id)), listed, contract: null };
}

// project holds the given hung items under one conversation in the tree and the ended ones behind the ended line.
function project(hung: Hung[] = [], ended: Hung[] = []): Project {
  return {
    key: "host|/home/owner/code/orrery",
    dir: "/home/owner/code/orrery",
    name: "orrery",
    path: "~/code/orrery",
    host: "",
    convos: [{ key: "convo", row: null, standIn: "", again: false, hung }],
    ended: [{ key: "ended", row: null, standIn: "", again: false, hung: ended }],
  };
}

// paneRow is a listed pane row in the directory given.
function row(fields: Partial<Row>): Row {
  return {
    key: "",
    id: "",
    archiveId: "",
    kind: "pane",
    label: "",
    project: "",
    host: "",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    tool: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    putOff: false,
    ...fields,
  };
}

// noticeKeyOf is a notice key that sets the fields given and no others.
function noticeKeyOf(fields: MessageInitShape<typeof NoticeKeySchema>): NoticeKey {
  return create(NoticeKeySchema, fields);
}

// notice is a waiting notice with the fields given.
function notice(fields: Partial<NoticeFields>): NoticeFields {
  return { state: "waiting", title: "", ...fields };
}

// block is a notice block of the kind given.
function block(kind: string) {
  return create(NoticeBlockSchema, { kind, ref: "" });
}

test("NoticeStack: a listed epic leads with the gauge its inbox row draws, names its slug and what it waits on", () => {
  const node = epicNode("orrery/notices-usable");
  const head = noticeHead(
    notice({ key: noticeKeyOf({ epic: node.listed.id }), block: block("question") }),
    [project([node])],
    [],
    now,
  );
  assert.deepEqual(head, {
    lead: { kind: "gauge", gauge: epicRowWords(node, now).gauge },
    dir: "",
    what: "notices-usable asks you a question",
  });
});

test("NoticeStack: an epic's block kind sets its words, and a block of any other kind or none reads as waiting on you", () => {
  const node = epicNode("orrery/notices-usable");
  const words: [string | undefined, string][] = [
    ["question", "asks you a question"],
    ["release_step", "a release step failed"],
    ["report", "finished"],
    ["contract", "has a contract for you"],
    ["something-else", "waits on you"],
    [undefined, "waits on you"],
  ];
  for (const [kind, want] of words) {
    const head = noticeHead(
      notice({ key: noticeKeyOf({ epic: node.listed.id }), block: kind === undefined ? undefined : block(kind) }),
      [project([node])],
      [],
      now,
    );
    assert.equal(head.what, `notices-usable ${want}`, `block kind ${kind ?? "none"}`);
  }
});

test("NoticeStack: an epic under a project's ended line is found as listed and leads with its gauge", () => {
  const node = epicNode("orrery/holder-scopes");
  const head = noticeHead(
    notice({ key: noticeKeyOf({ epic: node.listed.id }), block: block("report") }),
    [project([], [node])],
    [],
    now,
  );
  assert.deepEqual(head, {
    lead: { kind: "gauge", gauge: epicRowWords(node, now).gauge },
    dir: "",
    what: "holder-scopes finished",
  });
});

test("NoticeStack: an epic no listed project holds leads with a bell and the text after the last slash of its id", () => {
  const head = noticeHead(
    notice({ key: noticeKeyOf({ epic: "orrery/gone-epic" }), block: block("contract") }),
    [],
    [],
    now,
  );
  assert.deepEqual(head, { lead: { kind: "icon", icon: "bell" }, dir: "", what: "gone-epic has a contract for you" });
});

test("NoticeStack: a pane leads with its state glyph, names its directory's last element and its state in words", () => {
  const rows = [row({ kind: "pane", id: "%3", project: "/home/owner/code/orrery", state: "waiting" })];
  const head = noticeHead(
    notice({ key: noticeKeyOf({ pane: "%3" }), state: "waiting", title: "orrery · fix the build" }),
    [],
    rows,
    now,
  );
  assert.deepEqual(head, { lead: { kind: "glyph", state: "waiting" }, dir: "orrery", what: "waiting on you" });
});

test("NoticeStack: a session names its listed directory and never its id", () => {
  const rows = [row({ kind: "session", id: "sess-9c", project: "/home/owner/code/tidepool" })];
  const head = noticeHead(
    notice({ key: noticeKeyOf({ session: "sess-9c" }), state: "done", title: "sess-9c · wrapped up" }),
    [],
    rows,
    now,
  );
  assert.deepEqual(head, { lead: { kind: "glyph", state: "done" }, dir: "tidepool", what: "done" });
});

test("NoticeStack: a pane's notice words its failed, cut off and stopped states beside waiting and done", () => {
  const states: [string, string][] = [
    ["waiting", "waiting on you"],
    ["done", "done"],
    ["failed", "failed"],
    ["interrupted", "cut off"],
    ["stopped", "stopped"],
  ];
  for (const [state, want] of states) {
    const head = noticeHead(
      notice({ key: noticeKeyOf({ pane: "%3" }), state, title: "orrery · fix the build" }),
      [],
      [],
      now,
    );
    assert.equal(head.what, want, `state ${state}`);
  }
});

test("NoticeStack: a row that is not listed takes its directory from the title before the separator, or the whole title", () => {
  const withSeparator = noticeHead(
    notice({ key: noticeKeyOf({ pane: "%9" }), state: "working", title: "orrery · tidying the tests" }),
    [],
    [],
    now,
  );
  assert.deepEqual(withSeparator, { lead: { kind: "glyph", state: "working" }, dir: "orrery", what: "working" });

  const withoutSeparator = noticeHead(
    notice({ key: noticeKeyOf({ session: "sess-2" }), state: "working", title: "scratch" }),
    [],
    [],
    now,
  );
  assert.deepEqual(withoutSeparator, { lead: { kind: "glyph", state: "working" }, dir: "scratch", what: "working" });
});

test("NoticeStack: a limit leads with a clock and reads its title, and an alert leads with a bell and reads its title", () => {
  const limit = noticeHead(
    notice({ key: noticeKeyOf({ limit: "weekly@1794855660" }), state: "limited", title: "weekly limit reached" }),
    [],
    [],
    now,
  );
  assert.deepEqual(limit, { lead: { kind: "icon", icon: "clock" }, dir: "", what: "weekly limit reached" });

  const alert = noticeHead(
    notice({ key: noticeKeyOf({ alert: "disk-full" }), state: "failed", title: "disk nearly full" }),
    [],
    [],
    now,
  );
  assert.deepEqual(alert, { lead: { kind: "icon", icon: "bell" }, dir: "", what: "disk nearly full" });
});

test("NoticeStack: an epic's question is answered, a waiting pane or session is answered, and anything else is opened", () => {
  const node = epicNode("orrery/notices-usable");
  assert.equal(noticeAct(notice({ key: noticeKeyOf({ epic: node.listed.id }), block: block("question") }), []), "Answer");
  assert.equal(noticeAct(notice({ key: noticeKeyOf({ epic: node.listed.id }), block: block("report") }), []), "Open");
  assert.equal(noticeAct(notice({ key: noticeKeyOf({ pane: "%3" }), state: "waiting" }), []), "Answer");
  assert.equal(noticeAct(notice({ key: noticeKeyOf({ session: "sess-9c" }), state: "waiting" }), []), "Answer");
  assert.equal(noticeAct(notice({ key: noticeKeyOf({ pane: "%3" }), state: "working" }), []), "Open");
  assert.equal(noticeAct(notice({ key: noticeKeyOf({ session: "sess-9c" }), state: "done" }), []), "Open");
  assert.equal(noticeAct(notice({ key: noticeKeyOf({ limit: "weekly@1794855660" }) }), []), null);
  assert.equal(noticeAct(notice({ key: noticeKeyOf({ alert: "disk-full" }) }), []), null);
});

test("NoticeStack: a waiting pane notice whose listed row is put off leads with idle, says so in words, and is opened", () => {
  const rows = [row({ kind: "pane", id: "%3", project: "/home/owner/code/orrery", state: "waiting", putOff: true })];
  const fields = notice({ key: noticeKeyOf({ pane: "%3" }), state: "waiting", title: "orrery · fix the build" });
  assert.deepEqual(noticeHead(fields, [], rows, now), {
    lead: { kind: "glyph", state: "idle" },
    dir: "orrery",
    what: "waiting on a question you put off",
  });
  assert.equal(noticeAct(fields, rows), "Open");
});

test("NoticeStack: a waiting pane notice whose listed row is archived leads with idle, says idle, and is opened", () => {
  const rows = [row({ kind: "pane", id: "%3", project: "/home/owner/code/orrery", state: "waiting", archived: true })];
  const fields = notice({ key: noticeKeyOf({ pane: "%3" }), state: "waiting", title: "orrery · fix the build" });
  assert.deepEqual(noticeHead(fields, [], rows, now), {
    lead: { kind: "glyph", state: "idle" },
    dir: "orrery",
    what: "idle",
  });
  assert.equal(noticeAct(fields, rows), "Open");
});

test("NoticeStack: a waiting row both archived and put off reads put off, and is opened", () => {
  const rows = [
    row({ kind: "pane", id: "%3", project: "/home/owner/code/orrery", state: "waiting", archived: true, putOff: true }),
  ];
  const fields = notice({ key: noticeKeyOf({ pane: "%3" }), state: "waiting", title: "orrery · fix the build" });
  assert.deepEqual(noticeHead(fields, [], rows, now), {
    lead: { kind: "glyph", state: "idle" },
    dir: "orrery",
    what: "waiting on a question you put off",
  });
  assert.equal(noticeAct(fields, rows), "Open");
});

test("NoticeStack: a done notice keeps its done glyph and word when its listed pane is waiting and put off, and is opened", () => {
  const rows = [row({ kind: "pane", id: "%3", project: "/home/owner/code/orrery", state: "waiting", putOff: true })];
  const fields = notice({ key: noticeKeyOf({ pane: "%3" }), state: "done", title: "orrery · fix the build" });
  assert.deepEqual(noticeHead(fields, [], rows, now), {
    lead: { kind: "glyph", state: "done" },
    dir: "orrery",
    what: "done",
  });
  assert.equal(noticeAct(fields, rows), "Open");
});

test("NoticeStack: a waiting row that waits on you keeps waiting on you and is answered", () => {
  const rows = [row({ kind: "pane", id: "%3", project: "/home/owner/code/orrery", state: "waiting" })];
  const fields = notice({ key: noticeKeyOf({ pane: "%3" }), state: "waiting", title: "orrery · fix the build" });
  assert.deepEqual(noticeHead(fields, [], rows, now), {
    lead: { kind: "glyph", state: "waiting" },
    dir: "orrery",
    what: "waiting on you",
  });
  assert.equal(noticeAct(fields, rows), "Answer");
});

test("NoticeStack: Bring it back is drawn for a put-off waiting pane only, and names that pane's row", () => {
  const putOff = row({ kind: "pane", id: "%3", state: "waiting", putOff: true, questionHash: "hash-3", changed: 300 });
  const notified = notice({ key: noticeKeyOf({ pane: "%3" }), state: "waiting" });
  assert.equal(bringBackRowOf(notified, [putOff]), putOff);
  const archived = row({ kind: "pane", id: "%3", state: "waiting", archived: true });
  assert.equal(bringBackRowOf(notified, [archived]), undefined, "an archived row is not put off");
  const waiting = row({ kind: "pane", id: "%3", state: "waiting" });
  assert.equal(bringBackRowOf(notified, [waiting]), undefined, "a row that waits on you is not put off");
  const headless = row({ kind: "session", id: "sess-9c", state: "waiting", putOff: true });
  const headlessNotice = notice({ key: noticeKeyOf({ session: "sess-9c" }), state: "waiting" });
  assert.equal(bringBackRowOf(headlessNotice, [headless]), undefined, "a headless session has no open request id");
  assert.equal(bringBackRowOf(notice({ key: noticeKeyOf({ pane: "%9" }), state: "waiting" }), [putOff]), undefined);
});

test("NoticeStack: Bring it back is drawn for a waiting pane that is both archived and put off, and names that pane's row", () => {
  const both = row({ kind: "pane", id: "%3", state: "waiting", archived: true, putOff: true });
  assert.equal(bringBackRowOf(notice({ key: noticeKeyOf({ pane: "%3" }), state: "waiting" }), [both]), both);
  const headless = row({ kind: "session", id: "sess-9c", state: "waiting", archived: true, putOff: true });
  assert.equal(
    bringBackRowOf(notice({ key: noticeKeyOf({ session: "sess-9c" }), state: "waiting" }), [headless]),
    undefined,
    "a headless session has no open request id",
  );
});

test("NoticeStack: a notice's age is now under a minute, then whole minutes, hours and days", () => {
  const ages: [number, string][] = [
    [0, "now"],
    [59, "now"],
    [-30, "now"],
    [60, "1m"],
    [119, "1m"],
    [3600, "1h"],
    [3 * 3600 + 1800, "3h"],
    [86400, "1d"],
    [5 * 86400, "5d"],
  ];
  for (const [elapsed, want] of ages) {
    assert.equal(noticeAge(BigInt(now - elapsed), now), want, `${elapsed} s old`);
  }
});

test("NoticeStack: a notice key names all five fields, so a key that sets another field is another notice", () => {
  assert.equal(noticeKey(noticeKeyOf({ pane: "%3" })), noticeKey(noticeKeyOf({ pane: "%3" })));
  assert.notEqual(noticeKey(noticeKeyOf({ pane: "%3" })), noticeKey(noticeKeyOf({ session: "%3" })));
  assert.notEqual(noticeKey(noticeKeyOf({ epic: "orrery/a" })), noticeKey(noticeKeyOf({ limit: "orrery/a" })));
});

test("NoticeCentre: the clock is 24-hour with two digits, read in the zone given", () => {
  assert.equal(noticeClock(stamp("2026-11-16T00:05:00Z"), "UTC"), "00:05");
  assert.equal(noticeClock(stamp("2026-11-15T23:30:00Z"), "UTC"), "23:30");
  assert.equal(noticeClock(stamp("2026-11-15T23:30:00Z"), "Asia/Tokyo"), "08:30");
});

test("NoticeCentre: rows group by calendar day in the zone, newest first, under Today, Yesterday, then the weekday and date", () => {
  const rows = [
    { id: "a", time: stamp("2026-11-16T09:00:00Z") },
    { id: "b", time: stamp("2026-11-15T23:30:00Z") },
    { id: "c", time: stamp("2026-11-15T08:00:00Z") },
    { id: "d", time: stamp("2026-11-13T08:00:00Z") },
  ];
  assert.deepEqual(noticeDays(rows, now, "UTC"), [
    { head: "Today", rows: [rows[0]] },
    { head: "Yesterday", rows: [rows[1], rows[2]] },
    { head: "Friday 13 November", rows: [rows[3]] },
  ]);
});

test("NoticeCentre: the zone moves a row to the day it falls on there", () => {
  const rows = [
    { id: "a", time: stamp("2026-11-16T09:00:00Z") },
    { id: "b", time: stamp("2026-11-15T23:30:00Z") },
    { id: "c", time: stamp("2026-11-15T08:00:00Z") },
  ];
  assert.deepEqual(noticeDays(rows, now, "Asia/Tokyo"), [
    { head: "Today", rows: [rows[0], rows[1]] },
    { head: "Yesterday", rows: [rows[2]] },
  ]);
});
