import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import {
  type Epic,
  EpicSchema,
  type EpicStep,
  EpicStepSchema,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn } from "./testwait";

const now = 1000;

// quietFetch answers the app's calls without data: a unary call with an empty object, and a stream with a body that
// stays open until its call aborts, so no conversation arrives and no timer runs.
async function quietFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  if (new URL(request.url).pathname.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

// row is a seen row, so no dwell timer runs on the cursor's row.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
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

// fixture has one row that needs you, one working and one done, in attention order.
function fixture(): Row[] {
  return [
    row({
      key: "a",
      label: "api",
      project: "/work/api",
      state: "waiting",
      question: "Ship it?",
      changed: 300,
      seen: 300,
    }),
    row({ key: "b", label: "web", project: "/work/web", state: "working", changed: 200, seen: 200 }),
    row({ key: "c", label: "docs", project: "/work/docs", state: "done", changed: 150, seen: 150 }),
  ];
}

const widgetsRepo = "widgets-5d0c1a7";

function makeEpic(slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${widgetsRepo}/${slug}`,
    repository: widgetsRepo,
    slug,
    outcome: "",
    live: true,
    ...init,
  });
}

function makeStep(id: string): EpicStep {
  return create(EpicStepSchema, { id, task: id, agent: "builder", doing: "", held: false });
}

// withInbox mounts the app over quietFetch in a fresh DOM and runs check. The app is unmounted and the DOM removed when
// the test ends, or as soon as signal aborts, as app.test.tsx's withApp does.
async function withInbox(
  signal: AbortSignal,
  props: { rows?: Row[]; epics?: EpicsView },
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: quietFetch }));
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(
        <App rows={props.rows ?? fixture()} error="" now={now} clients={clients} epics={props.epics} />,
        container,
      );
    });
    await check(container);
  } finally {
    await teardown();
  }
}

async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// dispatchOn fires an event of the type on the element, as the browser does for a pointer or a click.
async function dispatchOn(element: Element | null, type: string): Promise<void> {
  assert.ok(element, `the page has an element for ${type}`);
  await act(() => {
    element.dispatchEvent(new Event(type, { bubbles: true }));
  });
}

async function clickRow(container: HTMLElement, key: string): Promise<void> {
  await dispatchOn(container.querySelector(`.b-row[data-key="${key}"]`), "click");
}

// replyBox is the cursor's visible reply box. A missing box fails the test.
function replyBox(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) .composer textarea");
  if (box === null) {
    assert.fail("the cursor's reply box is drawn");
  }
  return box;
}

// onCursorRow is whether the focus is on the list's cursor row.
function onCursorRow(container: HTMLElement): boolean {
  return document.activeElement === container.querySelector(".b-row.cur");
}

// shiftTabFrom presses Shift+Tab in the element, as a key pressed in a focused box is, and returns whether the press
// was default-prevented.
async function shiftTabFrom(element: Element): Promise<boolean> {
  const tab = new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true });
  await act(() => {
    element.dispatchEvent(tab);
  });
  return tab.defaultPrevented;
}

// leaveReplyBox leaves the cursor's reply box the way a row's opening leaves it on origin: it focuses the visible reply
// box, then presses Shift+Tab in it. It returns whether the press was default-prevented.
async function leaveReplyBox(container: HTMLElement): Promise<boolean> {
  const box = replyBox(container);
  await act(() => {
    box.focus();
  });
  return shiftTabFrom(box);
}

// paneState is the inbox state the app draws on the panes: docked, pinned, hidden or flyout.
function paneState(container: HTMLElement): string | null {
  return container.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// rowKeys is the key of each row the list draws, in order. An epic entry is drawn as a line with the row class, but it
// is no row.
function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".b-row")]
    .map((element) => element.getAttribute("data-key") ?? "")
    .filter((key) => !key.startsWith("epic:"));
}

// markCount is the count the mark crumb carries, or null when it carries none.
function markCount(container: HTMLElement): string | null {
  return container.querySelector(".mark-crumb .needs")?.textContent ?? null;
}

test("InboxFlyout: the inbox is docked before a row is opened, and hidden once a click opens one", (t) =>
  withInbox(t.signal, {}, async (container) => {
    assert.equal(paneState(container), "docked");
    await clickRow(container, "b");
    assert.equal(paneState(container), "hidden");
  }));

test("InboxFlyout: Enter on the cursor's row hides the inbox as a click does", (t) =>
  withInbox(t.signal, {}, async (container) => {
    assert.equal(paneState(container), "docked");
    await press("Enter");
    assert.equal(paneState(container), "hidden");
  }));

test("InboxFlyout: the mark's count is the waiting rows not archived plus the live epics' needs, and holds in the project view and under a filter", (t) => {
  const epic = makeEpic("first");
  const detail = create(EpicsGetResponseSchema, { epic, questions: [{ id: "Q1", text: "Which way?" }] });
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  const archived = row({
    key: "s",
    id: "s",
    kind: "session",
    label: "old docs",
    state: "waiting",
    archived: true,
    archiveKind: "session",
  });
  return withInbox(t.signal, { rows: [...fixture(), archived], epics }, async (container) => {
    assert.equal(markCount(container), "2", "one waiting row and one epic need");
    await press("p");
    assert.equal(markCount(container), "2", "the project view");
    await press("/");
    await act(() => {
      const input = container.querySelector<HTMLInputElement>('input[aria-label="filter rows"]');
      assert.ok(input, "the filter input is drawn");
      input.value = "docs";
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    assert.deepEqual(rowKeys(container), ["c"], "the filter hides every row but the done one");
    assert.equal(markCount(container), "2", "under a filter");
  });
});

test("InboxFlyout: a step entry's page draws the mark as its first crumb, carrying the count", (t) => {
  const epic = makeEpic("first", { runningSteps: [makeStep("s1")] });
  const detail = create(EpicsGetResponseSchema, { epic, questions: [{ id: "Q1", text: "Which way?" }] });
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  return withInbox(t.signal, { rows: [], epics }, async (container) => {
    await press("G");
    const reader = 'section[aria-label="reader"] .title-row';
    assert.equal(container.querySelector(`${reader} .crumb.here`)?.textContent, "s1", "the cursor's step is the page");
    assert.equal(container.querySelector(`${reader} .crumbs`)?.firstElementChild?.getAttribute("class"), "mark-crumb");
    assert.equal(markCount(container), "1", "the step's page carries the epic's need on its mark");
  });
});

test("InboxFlyout: hovering the mark pops a hidden inbox out over the page, and the pointer leaving the inbox tucks it away", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    assert.equal(paneState(container), "hidden");
    const mark = container.querySelector(".mark-crumb");
    const list = container.querySelector(".list");
    await dispatchOn(mark, "mouseenter");
    assert.equal(paneState(container), "flyout");
    // The test DOM has no MouseEvent, so the leave carries its related target as a plain property.
    const leave = new Event("mouseleave", { bubbles: true });
    Object.defineProperty(leave, "relatedTarget", { value: list });
    await act(() => {
      mark?.dispatchEvent(leave);
    });
    assert.equal(paneState(container), "flyout", "a pointer moving from the mark into the inbox keeps it out");
    await dispatchOn(list, "mouseleave");
    assert.equal(paneState(container), "hidden", "the pointer leaving the inbox tucks it away");
  }));

test("InboxFlyout: a click on the mark or B pins the inbox beside the page, and doing either again unpins it", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    await dispatchOn(container.querySelector(".mark-crumb"), "click");
    assert.equal(paneState(container), "pinned");
    await dispatchOn(container.querySelector(".mark-crumb"), "click");
    assert.equal(paneState(container), "hidden");
    await leaveReplyBox(container);
    await press("b");
    await press("B");
    assert.equal(paneState(container), "pinned");
    await press("B");
    assert.equal(paneState(container), "hidden");
  }));

test("InboxFlyout: the brand row holds no counts row", (t) =>
  withInbox(t.signal, {}, (container) => {
    assert.equal(container.querySelector(".counts"), null);
  }));

// escapeCallPaths are the calls an Escape on a working row makes to take its queued messages back and interrupt it: a
// session's Withdraw and Interrupt, and a pane's PullQueued and SendKeys.
const escapeCallPaths = [
  "/orrery.v1.Control/Withdraw",
  "/orrery.v1.Control/Interrupt",
  "/orrery.v1.Panes/PullQueued",
  "/orrery.v1.Panes/SendKeys",
];

// withRecordedInbox mounts the app as withInbox does, over a fetch that keeps the path of each call the app makes in
// paths, in order, so a test can see which calls went out.
async function withRecordedInbox(
  signal: AbortSignal,
  paths: string[],
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const recording = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    paths.push(new URL(new Request(input, init).url).pathname);
    return quietFetch(input, init);
  };
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: recording }));
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(<App rows={fixture()} error="" now={now} clients={clients} />, container);
    });
    await check(container);
  } finally {
    await teardown();
  }
}

// settle lets the calls a key press starts reach the fake transport, so a test looks for them after they could.
async function settle(): Promise<void> {
  for (let round = 0; round < 3; round++) {
    await act(turn);
  }
}

// cursorKey is the key of the row the cursor rests on, or null when no row does.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".b-row.cur")?.getAttribute("data-key") ?? null;
}

test("InboxPin: b pops a hidden inbox out by key, and b again tucks it back to hidden", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    assert.equal(paneState(container), "hidden");
    await leaveReplyBox(container);
    await press("b");
    await press("b");
    assert.equal(paneState(container), "flyout", "b pops the inbox out over the page");
    await press("b");
    assert.equal(paneState(container), "hidden", "b again tucks it back to hidden");
  }));

test("InboxPin: b pops the inbox out, and Escape tucks it back with no interrupt or withdraw call", (t) => {
  const paths: string[] = [];
  return withRecordedInbox(t.signal, paths, async (container) => {
    await clickRow(container, "b");
    await leaveReplyBox(container);
    await press("b");
    await press("b");
    assert.equal(paneState(container), "flyout");
    const before = paths.length;
    await press("Escape");
    await settle();
    assert.equal(paneState(container), "hidden", "Escape tucks the key-popped inbox back to hidden");
    assert.deepEqual(
      paths.slice(before).filter((path) => escapeCallPaths.includes(path)),
      [],
      "no queued message is taken back and no row is interrupted",
    );
  });
});

test("InboxPin: j while hidden pops the inbox out and leaves the cursor row unchanged, then j moves the cursor, then Escape tucks the inbox and makes no interrupt call", (t) => {
  const paths: string[] = [];
  return withRecordedInbox(t.signal, paths, async (container) => {
    await clickRow(container, "a");
    assert.equal(paneState(container), "hidden");
    await leaveReplyBox(container);
    await press("b");
    await press("j");
    assert.equal(paneState(container), "flyout", "j pops the hidden inbox out");
    assert.equal(cursorKey(container), "a", "the press that pops the inbox out does not move the cursor");
    await press("j");
    assert.equal(cursorKey(container), "b", "the next j moves the cursor in the popped inbox");
    const before = paths.length;
    await press("Escape");
    await settle();
    assert.equal(paneState(container), "hidden", "Escape tucks the key-popped inbox back to hidden");
    assert.deepEqual(
      paths.slice(before).filter((path) => escapeCallPaths.includes(path)),
      [],
      "the working row under the cursor is not interrupted",
    );
  });
});

test("InboxPin: Enter opens the cursor row and hides the inbox, clearing a key pop-out", (t) =>
  withInbox(t.signal, { rows: fixture().map((item) => (item.key === "b" ? { ...item, project: "" } : item)) }, async (container) => {
    await clickRow(container, "a");
    await leaveReplyBox(container);
    await press("b");
    await press("b");
    assert.equal(paneState(container), "flyout");
    await press("j");
    assert.equal(cursorKey(container), "b");
    await press("Enter");
    assert.equal(paneState(container), "hidden", "Enter hides the inbox and clears the pop-out");
    assert.equal(
      container.querySelector('section[aria-label="reader"] .crumb.here')?.textContent,
      "web",
      "the cursor's row is the page the reader shows",
    );
  }));

test("InboxPin: h at a row's first crumb pops a hidden inbox out by key", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    assert.equal(paneState(container), "hidden");
    await leaveReplyBox(container);
    await press("b");
    await press("h");
    assert.equal(paneState(container), "flyout", "h at the row's first crumb pops the inbox out");
  }));

test("InboxPin: the keys help lists b, B and h", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await press("?");
    const help = container.querySelector('[role="dialog"][aria-label="Keys"]');
    assert.ok(help, "help panel is open");
    const listed = [...help.querySelectorAll(".keyline .ks")].map((caps) =>
      [...caps.querySelectorAll("kbd")].map((kbd) => kbd.textContent).join(" "),
    );
    for (const keys of ["b", "B", "h ←"]) {
      assert.ok(listed.includes(keys), `the keys help lists ${keys}`);
    }
  }));

// resizeWindow sets the viewport of the window the test's DOM is in, the one installTestDom made, to width pixels wide,
// as a resize does. It runs inside act, so the renders the resize causes are applied.
async function resizeWindow(width: number): Promise<void> {
  const view = (globalThis as unknown as { window: ReturnType<typeof installTestDom> }).window;
  await act(() => {
    view.happyDOM.setViewport({ width, height: 800 });
  });
}

// narrowWindow narrows the window from the wide default to width. happy-dom's media query starts out not matching, so
// it fires change for a narrowing only after a resize has shown it a match. The window is widened by a pixel first,
// which is that resize. A browser's query fires change on every crossing, so it needs no such step.
async function narrowWindow(width: number): Promise<void> {
  await resizeWindow(1025);
  await resizeWindow(width);
}

test("InboxPin: at 700px a row opened by click leaves the inbox docked, and j moves the cursor to the next row on its first press", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await narrowWindow(700);
    await clickRow(container, "a");
    assert.equal(paneState(container), "docked", "a narrow window draws the inbox docked once a row is open");
    await leaveReplyBox(container);
    await press("j");
    assert.equal(cursorKey(container), "b", "the first j moves the cursor to the next row");
    assert.equal(paneState(container), "docked", "j pops no inbox out");
  }));

test("InboxPin: at 700px b and h after a row is opened leave the inbox docked", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await narrowWindow(700);
    await clickRow(container, "b");
    await leaveReplyBox(container);
    await press("b");
    assert.equal(paneState(container), "docked", "b pops no inbox out");
    await press("h");
    assert.equal(paneState(container), "docked", "h pops no inbox out");
  }));

test("InboxPin: a window narrowed from wide with a row open reads docked, and reads hidden again once widened", (t) =>
  withInbox(t.signal, {}, async (container) => {
    // The click opens the row while the window is wide. The widened read is hidden only if that open row was kept
    // through the narrow layout, so the final assertion also shows the click took effect.
    await clickRow(container, "b");
    await narrowWindow(700);
    assert.equal(paneState(container), "docked", "the narrowed window draws the inbox docked");
    await resizeWindow(1024);
    assert.equal(paneState(container), "hidden", "widened again, the inbox is hidden as the open row leaves it");
  }));

test("InboxPin: Shift+Tab out of a reply box pops a hidden inbox out by key and focuses the cursor row, and Escape tucks it back with no interrupt or withdraw call", (t) => {
  const paths: string[] = [];
  return withRecordedInbox(t.signal, paths, async (container) => {
    await clickRow(container, "b");
    assert.equal(paneState(container), "hidden");
    await act(() => {
      replyBox(container).focus();
    });
    assert.equal(document.activeElement === replyBox(container), true, "the reply box is focused");
    const prevented = await shiftTabFrom(replyBox(container));
    assert.equal(prevented, true, "the Shift+Tab press is default-prevented");
    assert.equal(paneState(container), "flyout", "Shift+Tab pops the hidden inbox out by key");
    assert.equal(onCursorRow(container), true, "the focus is on the cursor row");
    const before = paths.length;
    await press("Escape");
    await settle();
    assert.equal(paneState(container), "hidden", "Escape tucks the key-popped inbox back to hidden");
    assert.deepEqual(
      paths.slice(before).filter((path) => escapeCallPaths.includes(path)),
      [],
      "no queued message is taken back and no row is interrupted",
    );
  });
});

test("InboxPin: Shift+Tab out of a reply box with the inbox docked focuses the cursor row and leaves the inbox docked", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await press("i");
    assert.equal(document.activeElement?.tagName === "TEXTAREA", true, "i focuses the cursor's reply box");
    await shiftTabFrom(replyBox(container));
    assert.equal(onCursorRow(container), true, "the focus is on the cursor row");
    assert.equal(paneState(container), "docked", "the docked inbox stays docked");
  }));

test("InboxPin: Shift+Tab out of a reply box with the inbox pinned focuses the cursor row and leaves the inbox pinned", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await dispatchOn(container.querySelector(".mark-crumb"), "click");
    assert.equal(paneState(container), "pinned");
    await press("i");
    assert.equal(document.activeElement?.tagName === "TEXTAREA", true, "i focuses the cursor's reply box");
    await shiftTabFrom(replyBox(container));
    assert.equal(onCursorRow(container), true, "the focus is on the cursor row");
    assert.equal(paneState(container), "pinned", "the pinned inbox stays pinned");
  }));
