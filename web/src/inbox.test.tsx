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
    putOff: false,
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

// fixture has one row that needs you, one working and one done, in inbox order.
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
        <App rows={props.rows ?? fixture()} now={now} clients={clients} epics={props.epics} />,
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
  await dispatchOn(container.querySelector(`[data-key="${key}"]`), "click");
}

// replyBox is the cursor's visible reply box. A missing box fails the test.
function replyBox(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) textarea");
  if (box === null) {
    assert.fail("the cursor's reply box is drawn");
  }
  return box;
}

// onCursorRow is whether the focus is on the list's cursor row.
function onCursorRow(container: HTMLElement): boolean {
  return document.activeElement === container.querySelector(".row.cur");
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

// paneState is the inbox state the app draws on the panes: expanded or collapsed.
function paneState(container: HTMLElement): string | null {
  return container.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// rowKeys is the key of each row the list draws, in order. An epic entry is drawn as a line with the row class, but it
// is no row.
function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".row[data-key]:not(.standin)")]
    .map((element) => element.getAttribute("data-key") ?? "")
    .filter((key) => !key.startsWith("epic:"));
}

test("InboxFlyout: the inbox is expanded before a row is opened, and stays expanded once a click opens one", (t) =>
  withInbox(t.signal, {}, async (container) => {
    assert.equal(paneState(container), "expanded");
    await clickRow(container, "b");
    assert.equal(paneState(container), "expanded");
  }));

test("InboxFlyout: Enter on the cursor's row leaves the inbox as it was, as a click does", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await press("Enter");
    assert.equal(paneState(container), "expanded", "Enter leaves an expanded inbox expanded");
    await leaveReplyBox(container);
    await press("b");
    await press("Enter");
    assert.equal(paneState(container), "collapsed", "Enter leaves a collapsed inbox collapsed");
  }));

test("InboxFlyout: a step entry's page has the step as its page crumb, opening on the epic's crumb with no mark or count before it", (t) => {
  const epic = makeEpic("first", { runningSteps: [makeStep("s1")] });
  const detail = create(EpicsGetResponseSchema, { epic, questions: [{ id: "Q1", text: "Which way?" }] });
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  return withInbox(t.signal, { rows: [], epics }, async (container) => {
    await press("G");
    const reader = 'section[aria-label="reader"] .title-row';
    assert.equal(container.querySelector(`${reader} .crumb.here`)?.textContent, "s1", "the cursor's step is the page");
    assert.equal(container.querySelector(`${reader} .crumbs`)?.firstElementChild?.classList.contains("crumb"), true);
    assert.equal(container.querySelector(`${reader} .mark-crumb, ${reader} .needs`), null);
  });
});

test("InboxFlyout: a click on the inbox's collapse control collapses it and the rail's mark expands it, and B does neither", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    await dispatchOn(container.querySelector('.inbox-foot button[aria-label="Collapse the inbox"]'), "click");
    assert.equal(paneState(container), "collapsed");
    await leaveReplyBox(container);
    assert.equal(paneState(container), "expanded", "Shift+Tab out of the reply box expands the inbox to its cursor row");
    await dispatchOn(container.querySelector('.inbox-foot button[aria-label="Collapse the inbox"]'), "click");
    await press("B");
    assert.equal(paneState(container), "collapsed", "B does not expand the inbox");
    await dispatchOn(container.querySelector('nav.rail.start button[aria-label="Expand the inbox"]'), "click");
    assert.equal(paneState(container), "expanded");
    await press("B");
    assert.equal(paneState(container), "expanded", "B does not collapse the inbox");
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
      render(<App rows={fixture()} now={now} clients={clients} />, container);
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
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

test("InboxRail: b collapses the inbox with a row open, and b again expands it", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    assert.equal(paneState(container), "expanded");
    await leaveReplyBox(container);
    await press("b");
    assert.equal(paneState(container), "collapsed", "b collapses the inbox to its rail");
    await press("b");
    assert.equal(paneState(container), "expanded", "b again expands it");
  }));

test("InboxRail: p opens a flyout over a working row's page, and Escape closes it with no interrupt or withdraw call", (t) => {
  const paths: string[] = [];
  return withRecordedInbox(t.signal, paths, async (container) => {
    await clickRow(container, "b");
    await leaveReplyBox(container);
    await press("b");
    await press("p");
    assert.equal(container.querySelector(".pfly")?.getAttribute("aria-label"), "web", "p opens the flyout on web");
    const before = paths.length;
    await press("Escape");
    await settle();
    assert.equal(container.querySelector(".pfly"), null, "Escape closes the flyout");
    assert.equal(paneState(container), "collapsed", "and leaves the inbox collapsed");
    assert.deepEqual(
      paths.slice(before).filter((path) => escapeCallPaths.includes(path)),
      [],
      "no queued message is taken back and no row is interrupted",
    );
  });
});

test("InboxPin: j with the inbox collapsed expands it and leaves the cursor row unchanged, then j moves the cursor", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "a");
    await leaveReplyBox(container);
    await press("b");
    assert.equal(paneState(container), "collapsed");
    await press("j");
    assert.equal(paneState(container), "expanded", "j expands the collapsed inbox");
    assert.equal(cursorKey(container), "a", "the press that expands the inbox does not move the cursor");
    await press("j");
    assert.equal(cursorKey(container), "b", "the next j moves the cursor in the expanded inbox");
  }));

test("InboxPin: Enter with the inbox collapsed and no flyout opens the cursor row and leaves the inbox collapsed", (t) =>
  withInbox(t.signal, { rows: fixture().map((item) => (item.key === "b" ? { ...item, project: "" } : item)) }, async (container) => {
    await clickRow(container, "c");
    await leaveReplyBox(container);
    await press("j");
    assert.equal(cursorKey(container), "b");
    await press("b");
    await press("Enter");
    assert.equal(paneState(container), "collapsed", "Enter leaves the inbox collapsed");
    assert.equal(
      container.querySelector('section[aria-label="reader"] .crumb.here')?.textContent,
      "web",
      "the cursor's row is the page the reader shows",
    );
  }));

test("InboxPin: h at a row's first crumb leaves the inbox as it is, expanded or collapsed", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    await leaveReplyBox(container);
    await press("h");
    assert.equal(paneState(container), "expanded", "h leaves an expanded inbox expanded");
    await press("b");
    await press("h");
    assert.equal(paneState(container), "collapsed", "h leaves a collapsed inbox collapsed");
  }));

test("InboxPin: the keys help lists b, p and h, and no B", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await press("?");
    const help = container.querySelector('[role="dialog"][aria-label="Keys"]');
    assert.ok(help, "help panel is open");
    const listed = [...help.querySelectorAll(".keyline .ks")].map((caps) =>
      [...caps.querySelectorAll("kbd")].map((kbd) => kbd.textContent).join(" "),
    );
    for (const keys of ["b", "p", "h ←"]) {
      assert.ok(listed.includes(keys), `the keys help lists ${keys}`);
    }
    assert.ok(!listed.some((keys) => keys.split(" ").includes("B")), "the keys help lists no B");
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

test("InboxPin: at 700px a row opened by click leaves the inbox as the stacked layout draws it, and j moves the cursor to the next row on its first press", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await narrowWindow(700);
    await clickRow(container, "a");
    assert.equal(paneState(container), "expanded", "a narrow window draws the inbox expanded once a row is open");
    await leaveReplyBox(container);
    await press("j");
    assert.equal(cursorKey(container), "b", "the first j moves the cursor to the next row");
    assert.equal(paneState(container), "expanded");
  }));

test("InboxPin: at 700px b and h after a row is opened leave the inbox expanded", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await narrowWindow(700);
    await clickRow(container, "b");
    await leaveReplyBox(container);
    await press("b");
    assert.equal(paneState(container), "expanded", "b collapses no stacked inbox");
    await press("h");
    assert.equal(paneState(container), "expanded", "h collapses no stacked inbox");
  }));

test("InboxPin: a collapsed inbox narrowed from wide with a row open reads expanded, and reads collapsed again once widened", (t) =>
  withInbox(t.signal, {}, async (container) => {
    // The click opens the row while the window is wide, and b collapses the inbox. The widened read is collapsed only if
    // the collapse was kept through the narrow layout.
    await clickRow(container, "b");
    await leaveReplyBox(container);
    await press("b");
    assert.equal(paneState(container), "collapsed");
    await narrowWindow(700);
    assert.equal(paneState(container), "expanded", "the narrowed window draws the stacked inbox");
    await resizeWindow(1024);
    assert.equal(paneState(container), "collapsed", "widened again, the inbox is collapsed as it was left");
  }));

test("InboxPin: Shift+Tab out of a reply box with the inbox collapsed expands it and focuses the cursor row, with no interrupt or withdraw call", (t) => {
  const paths: string[] = [];
  return withRecordedInbox(t.signal, paths, async (container) => {
    await clickRow(container, "b");
    await leaveReplyBox(container);
    await press("b");
    assert.equal(paneState(container), "collapsed");
    await press("i");
    assert.equal(document.activeElement === replyBox(container), true, "the reply box is focused");
    const before = paths.length;
    const prevented = await shiftTabFrom(replyBox(container));
    await settle();
    assert.equal(prevented, true, "the Shift+Tab press is default-prevented");
    assert.equal(paneState(container), "expanded", "Shift+Tab expands the collapsed inbox");
    assert.equal(onCursorRow(container), true, "the focus is on the cursor row");
    assert.deepEqual(
      paths.slice(before).filter((path) => escapeCallPaths.includes(path)),
      [],
      "no queued message is taken back and no row is interrupted",
    );
  });
});

test("InboxPin: Shift+Tab out of a reply box with the inbox expanded focuses the cursor row and leaves the inbox expanded", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await press("i");
    assert.equal(document.activeElement?.tagName === "TEXTAREA", true, "i focuses the cursor's reply box");
    await shiftTabFrom(replyBox(container));
    assert.equal(onCursorRow(container), true, "the focus is on the cursor row");
    assert.equal(paneState(container), "expanded", "the expanded inbox stays expanded");
  }));

test("InboxPin: Shift+Tab out of an opened row's reply box focuses the cursor row and leaves the inbox expanded", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    assert.equal(document.activeElement === replyBox(container), true, "the click opened the row into its reply box");
    await shiftTabFrom(replyBox(container));
    assert.equal(onCursorRow(container), true, "the focus is on the cursor row");
    assert.equal(paneState(container), "expanded", "the inbox stays expanded");
  }));
