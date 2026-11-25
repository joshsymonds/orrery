import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import type { Clients } from "./actions";
import type { EpicCalls } from "./card";
import { createClients, createTransport } from "./client";
import type { Conversation } from "./conversation";
import { noEpics } from "./epics";
import { ConversationStateSchema } from "./gen/orrery/v1/orrery_pb";
import { InboxRow, type RowMore } from "./inboxrow";
import { ProjectPage } from "./projectpage";
import { type ColumnHead, ReaderColumn, type ReaderColumnProps } from "./readercolumn";
import type { Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";
import { buildTree } from "./tree";

// Each test's name carries the token InboxWebBroken: a failed conversation and an unbound pane draw the broken icon, and a
// failed conversation's page says its crash reason. The rows are invented, and each check reads the DOM.

const now = 100_000;
const serving = "halyard";
const directory = "/work/widgets";
const crashReason = "panic: index out of range [4] with length 3";

// failedRow is a headless conversation that failed two minutes ago. Its summary is the crash reason, as HeadlessSession
// .summary carries it for a failed session.
function failedRow(): Row {
  return {
    key: "session:s9",
    id: "s9",
    archiveId: "s9",
    kind: "session",
    label: "widgets · fix the parser",
    project: directory,
    host: "",
    state: "failed",
    summary: crashReason,
    question: "",
    questionHash: "",
    tool: "",
    changed: now - 120,
    seen: 0,
    unseen: true,
    archived: false,
    putOff: false,
    archiveKind: "headless",
  };
}

// pane is a pane of the serving host in the directory, idle, and unbound when it says so.
function pane(unbound: boolean): Row {
  return {
    key: "pane:halyard:%41",
    id: "%41",
    archiveId: "",
    kind: "pane",
    label: "orphan shell",
    project: directory,
    host: serving,
    state: "idle",
    summary: "",
    question: "",
    questionHash: "",
    tool: "",
    changed: now - 60,
    seen: now,
    unseen: false,
    archived: false,
    putOff: false,
    archiveKind: "none",
    unbound,
  };
}

const noMore: RowMore = { open: false, items: [], toggle: () => {}, close: () => {} };

// withDom renders the element into a fresh test DOM, runs the check on its container, and removes the DOM again.
async function withDom(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// glyphOf returns the state glyph under root, which the row must draw.
function glyphOf(root: ParentNode): Element {
  const glyph = root.querySelector("svg.glyph");
  assert.ok(glyph !== null, "the row draws a state glyph");
  return glyph;
}

test("InboxWebBroken: a failed conversation's row and an unbound pane's row each draw the broken icon", async () => {
  await withDom(
    <InboxRow row={failedRow()} now={now} cursor={false} more={noMore} onOpen={() => {}} onSeen={() => {}} />,
    (container) => {
      assert.equal(glyphOf(container).getAttribute("data-state"), "broken", "the failed conversation draws the broken icon");
      assert.equal(glyphOf(container).getAttribute("aria-label"), "broken", "the broken icon is named broken");
    },
  );
  await withDom(
    <InboxRow row={pane(true)} now={now} cursor={false} more={noMore} onOpen={() => {}} onSeen={() => {}} />,
    (container) => {
      assert.equal(glyphOf(container).getAttribute("data-state"), "broken", "the unbound pane draws the broken icon");
    },
  );
  await withDom(
    <InboxRow row={pane(false)} now={now} cursor={false} more={noMore} onOpen={() => {}} onSeen={() => {}} />,
    (container) => {
      assert.equal(glyphOf(container).getAttribute("data-state"), "idle", "the same pane bound draws its own state, not the broken icon");
    },
  );
});

// noop is a callback that does nothing.
const noop = () => {};

// bareHead is a column's head with no strip, asks, badges or note, and a More menu that does nothing.
const bareHead: ColumnHead = {
  host: null,
  showStrip: false,
  lines: [],
  askingWords: "",
  badges: [],
  note: "",
  menu: { stripsShown: false, toggleStrips: noop, markSeen: noop, archive: null, keys: noop },
};

// neverFetch is the transport the column runs over. A column makes no call while it draws, so no request is sent.
function neverFetch(): Promise<Response> {
  return new Promise(() => {});
}

const clients: Clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: neverFetch }));

// The epic calls are fakes that do nothing: a column draws without calling them.
const epicCalls: EpicCalls = {
  get: () => new Promise(() => {}),
  answer: () => new Promise(() => {}),
  openEpic: noop,
  openStep: noop,
  stepResult: () => new Promise(() => {}),
  now: 0,
  epics: noEpics,
};

// conversation is a conversation the stream holds no state for and no item of.
function conversation(): Conversation {
  return {
    transcript: "t1",
    seq: 1n,
    items: [],
    oldest: 0n,
    more: false,
    state: create(ConversationStateSchema, {}),
    unfinished: [],
    queued: [],
    unsent: [],
  };
}

// columnProps is the cursor column that shows row, with every callback a fake that does nothing.
function columnProps(row: Row): ReaderColumnProps {
  return {
    column: "cursor",
    clients,
    now,
    row,
    conversation: conversation(),
    rows: [row],
    interrupts: new Set(),
    pageStrip: () => undefined,
    head: () => bareHead,
    narrow: false,
    hidden: false,
    onHide: noop,
    onShow: noop,
    onOpenEpic: noop,
    onBack: noop,
    finding: null,
    matches: [],
    current: -1,
    onFind: noop,
    inputs: { current: new Map() },
    handles: { current: new Map() },
    reader: { current: null },
    older: { current: noop },
    sending: [],
    epicCalls,
    addSending: noop,
    retractSending: noop,
    open: false,
  };
}

test("InboxWebBroken: a failed conversation's page shows its crash reason in its state line, checked in the DOM", async () => {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(<ReaderColumn {...columnProps(failedRow())} />, container);
    });
    const section = container.querySelector("section");
    assert.ok(section, "the column draws its reader section");
    assert.equal(section.querySelector(".statebar .word")?.textContent, "failed", "the state line says failed");
    assert.equal(
      section.querySelector(".statebar .what")?.textContent,
      `· ${crashReason} · 2m ago`,
      "the state line carries the crash reason and its age",
    );
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
});

test("InboxWebBroken: the project page draws a failed conversation's row with the broken icon", async () => {
  const project = buildTree({
    rows: [failedRow()],
    epics: [],
    details: new Map(),
    contracts: [],
    host: serving,
    view: "inbox",
  }).find((candidate) => candidate.dir === directory);
  assert.ok(project !== undefined, "the tree draws the directory");
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(
        <ProjectPage project={project} now={now} narrow={false} onOpen={noop} onNewHere={noop} onBack={noop} />,
        container,
      );
    });
    const line = container.querySelector(".pc-row");
    assert.ok(line !== null, "the page draws the conversation's row");
    assert.equal(glyphOf(line).getAttribute("data-state"), "broken", "the failed conversation's row draws the broken icon");
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
});
