import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { Clients } from "./actions";
import type { EpicCalls } from "./card";
import { createClients, createTransport } from "./client";
import type { Conversation } from "./conversation";
import { noEpics, type StepItem } from "./epics";
import { EpicStepSchema } from "./gen/orrery/v1/epics_pb";
import { ConversationItemSchema, ConversationStateSchema, type ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { ReaderColumn, type ReaderColumnProps } from "./readercolumn";
import type { Row } from "./rows";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import type { InboxMark } from "./titlerow";

// neverFetch is the transport the columns run over. A column makes no call while it draws, so no request is sent.
function neverFetch(): Promise<Response> {
  return new Promise(() => {});
}

const clients: Clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: neverFetch }));

// The epic calls and the title row's inbox mark are fakes that do nothing: a column draws without calling them.
const epicCalls: EpicCalls = {
  get: () => new Promise(() => {}),
  pause: () => new Promise(() => {}),
  resume: () => new Promise(() => {}),
  answer: () => new Promise(() => {}),
  openEpic: () => {},
  openStep: () => {},
  stepResult: () => new Promise(() => {}),
  epics: noEpics,
};

const quietMark: InboxMark = { needs: 0, enter: () => {}, leave: () => {}, pin: () => {} };

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

function conversation(init: Partial<Conversation> = {}): Conversation {
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
    ...init,
  };
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "box",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// step is a running step of the cursor's epic, as the list holds it, with the epic's own step record.
function step(): StepItem {
  return {
    kind: "step",
    key: "step:s1",
    id: "s1",
    epic: "e1",
    epicSlug: "orrery-web",
    label: "builder · T4",
    doing: "",
    held: false,
    agent: "builder",
    started: 0,
    round: 2,
    pips: [],
    reason: "",
    step: create(EpicStepSchema, { id: "s1", task: "T4", agent: "builder", doing: "", held: false }),
  };
}

// columnProps are the props of a column, with every callback a fake that does nothing.
function columnProps(init: Pick<ReaderColumnProps, "column"> & Partial<ReaderColumnProps>): ReaderColumnProps {
  return {
    clients,
    now: 1000,
    row: null,
    conversation: null,
    rows: [],
    interrupts: new Set(),
    mark: quietMark,
    pageStrip: () => undefined,
    onBack: () => {},
    finding: null,
    matches: [],
    current: -1,
    onFind: () => {},
    inputs: { current: new Map() },
    handles: { current: new Map() },
    reader: { current: null },
    older: { current: () => {} },
    sending: [],
    epicCalls,
    addSending: () => {},
    retractSending: () => {},
    open: false,
    ...init,
  };
}

// withColumn renders the column into a fresh test DOM, runs the check on its section, and removes the DOM.
async function withColumn(
  props: ReaderColumnProps,
  check: (section: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(<ReaderColumn {...props} />, container);
    });
    const section = container.querySelector("section");
    assert.ok(section, "the column draws its reader section");
    await check(section);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

// rowTexts returns the text of each conversation row the reader draws, in order.
function rowTexts(section: HTMLElement): string[] {
  return [...section.querySelectorAll(".reader > .t")].map((element) => element.textContent ?? "");
}

test("AppSplit: the cursor column draws its row's reader, head, item and reply box", async () => {
  // seen is at the item's time, so the item is read and draws no since-you-looked rule.
  const cursorRow = row({ key: "%3", label: "reviewing the split", project: "/work/orrery", seen: 100 });
  const transcript = conversation({ items: [item({ kind: "reply", time: 100n, text: "hello from the worker" })] });
  await withColumn(
    columnProps({ column: "cursor", row: cursorRow, conversation: transcript, rows: [cursorRow] }),
    (section) => {
      assert.equal(section.getAttribute("class"), "read");
      assert.equal(section.getAttribute("aria-label"), "reader");
      assert.equal(section.hasAttribute("hidden"), false);

      const head = section.querySelector(".title-row");
      assert.ok(head, "the head is drawn");
      assert.ok(head.textContent?.includes("reviewing the split"), "the head names the row's line");

      const rows = rowTexts(section);
      assert.equal(rows.length, 1, "the reader draws the conversation's one item");
      assert.match(rows[0] ?? "", /hello from the worker/);

      const reply = section.querySelector(".send > .reply");
      assert.ok(reply, "the reply box is drawn under the reply class");
      assert.equal(reply.hasAttribute("hidden"), false, "the shown row's reply box is visible");
      assert.ok(reply.querySelector("textarea"), "the reply box holds a text area");
      assertNone(section.querySelector(".send > .pin-reply"), "the cursor column draws no pinned reply box");
    },
  );
});

test("AppSplit: the pinned column draws the pinned reader section, with its reply box under pin-reply", async () => {
  const pinnedRow = row({ key: "%7", label: "watching the epic", project: "/work/orrery", seen: 100 });
  const transcript = conversation({ items: [item({ kind: "reply", time: 100n, text: "pinned transcript" })] });
  const props = columnProps({ column: "pinned", row: pinnedRow, conversation: transcript, rows: [pinnedRow] });
  await withColumn(props, (section) => {
    assert.equal(section.getAttribute("class"), "read pinned");
    assert.equal(section.getAttribute("aria-label"), "pinned reader");
    assert.equal(section.hasAttribute("hidden"), false);

    const rows = rowTexts(section);
    assert.equal(rows.length, 1, "the reader draws the conversation's one item");
    assert.match(rows[0] ?? "", /pinned transcript/);

    const reply = section.querySelector(".send > .pin-reply");
    assert.ok(reply, "the reply box is drawn under the pin-reply class");
    assert.equal(reply.hasAttribute("hidden"), false, "the pinned row's reply box is visible");
    assertNone(section.querySelector(".send > .reply"), "the pinned column draws no cursor reply box");
  });
});

test("AppSplit: a step in the cursor column draws its title-row crumbs and its step box", async () => {
  const cursorStep = step();
  const props = columnProps({
    column: "cursor",
    step: cursorStep,
    steps: [cursorStep],
    stepInputs: { current: new Map() },
  });
  await withColumn(props, (section) => {
    assert.equal(section.getAttribute("class"), "read");
    assert.equal(section.getAttribute("aria-label"), "reader");
    assert.equal(section.hasAttribute("hidden"), false, "a step shows the column");

    const crumbs = [...section.querySelectorAll(".crumb")].map((crumb) => crumb.textContent);
    assert.deepEqual(crumbs, ["orrery-web › builder · step · round 2", "builder · T4"]);

    const box = section.querySelector(".send > .reply textarea");
    assert.ok(box, "the step box is drawn under the reply class");
    assert.equal(box.getAttribute("placeholder"), "Nudge builder · T4: it interrupts the turn, then prompts");
  });
});
