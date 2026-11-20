import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

const now = 1000;
const wideWidth = 1024;
const narrowWidth = 700;

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

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/api",
    host: "",
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

// rows are two conversations in the api directory, one waiting, and one in docs, all seen, so no dwell timer runs.
function rows(): Row[] {
  return [
    row({ key: "a", label: "api one", state: "waiting", question: "Ship it?", changed: 300, seen: 300 }),
    row({ key: "b", label: "api two", changed: 200, seen: 200 }),
    row({ key: "c", label: "docs", project: "/work/docs", state: "done", changed: 150, seen: 150 }),
  ];
}

// withDesk mounts App over quietFetch at width in a fresh DOM and runs check with its container. App is unmounted and
// the DOM removed when the test ends, or as soon as signal aborts first.
async function withDesk(
  signal: AbortSignal,
  width: number,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
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
      render(<App rows={rows()} now={now} clients={clients} />, container);
    });
    await check(container);
  } finally {
    await teardown();
  }
}

// one returns the element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// labelOf names a rail child: its aria-label when it has one, else its class.
function labelOf(element: Element): string {
  return element.getAttribute("aria-label") ?? element.className;
}

// rail is the inbox rail, which must be drawn.
function rail(container: HTMLElement): HTMLElement {
  return one(container, "nav.rail.start");
}

// tileOf is the rail's tile whose label starts with title, which must be drawn. A project's label goes on past its
// name with its waiting count, when it has one.
function tileOf(container: HTMLElement, title: string): HTMLElement {
  return one(rail(container), `button[aria-label^="${title}"]`);
}

// paneState is the inbox state the app draws on the panes: docked, pinned, rail or flyout.
function paneState(container: HTMLElement): string | null {
  return container.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// projectPage is the project page section, when it is drawn.
function projectPage(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label="project"]:not([hidden])');
}

// press dispatches a click on the element, and lets Preact draw what the click changed.
async function press(element: Element | null): Promise<void> {
  assert.ok(element, "the element to press is drawn");
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

// clickRow clicks the list's row for key, which opens it.
async function clickRow(container: HTMLElement, key: string): Promise<void> {
  await press(container.querySelector(`[data-key="${key}"]`));
}

// openRow clicks the list's row for key, then takes the focus off the reply box it opens, so the keys that follow reach
// the page as they do once a press lands on the page.
async function openRow(container: HTMLElement, key: string): Promise<void> {
  await clickRow(container, key);
  await act(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });
}

// pointer dispatches a pointer or mouse event of the type on the element, with to as the element the pointer moved to.
async function pointer(element: Element, type: string, to: EventTarget | null = null): Promise<void> {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "relatedTarget", { value: to });
  await act(() => {
    element.dispatchEvent(event);
  });
}

// press is a key press on the document, as the keys reach App.
async function key(name: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: name, cancelable: true }));
  });
}

// replyBox is the cursor's visible reply box, which must be drawn.
function replyBox(container: HTMLElement): HTMLTextAreaElement {
  return one(container, ".reply:not([hidden]) textarea") as HTMLTextAreaElement;
}

// footPin is the inbox foot's pin control, under either of its names, when it is drawn.
function footPin(container: HTMLElement): HTMLElement | null {
  const pin = container.querySelector<HTMLElement>(
    '.inbox-foot button[aria-label="Pin the inbox beside the page"], .inbox-foot button[aria-label="Unpin the inbox"]',
  );
  return pin;
}

// labelsOf are the labels of the children of the rail, in order.
function railLabels(container: HTMLElement): string[] {
  return Array.from(rail(container).children).map(labelOf);
}

test("InboxRail: with a page open at 760px and wider the inbox rests as a rail, drawing the mark, a tile per project in the tree's order, New, Search and Keys", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    assert.equal(paneState(container), "rail");
    assert.deepEqual(railLabels(container), [
      "Open the inbox",
      "rail-sep",
      "api: 1 waits on you",
      "docs",
      "spring",
      "New session",
      "Search",
      "Keys",
    ]);
    const listed = Array.from(container.querySelectorAll("section.dir")).map((section) => section.getAttribute("aria-label"));
    assert.deepEqual(listed, ["api", "docs"], "the list draws the projects in the order the rail's tiles follow");
    assert.equal(tileOf(container, "api").querySelector(".n")?.textContent, "1", "the api tile counts its one waiting row");
  }));

test("InboxRail: with no page open the inbox is docked and no rail is drawn", (t) =>
  withDesk(t.signal, wideWidth, (container) => {
    assert.equal(paneState(container), "docked");
    assert.equal(container.querySelector("nav.rail.start"), null);
  }));

test("InboxRail: under 760px no rail is drawn, even with a row open", (t) =>
  withDesk(t.signal, narrowWidth, async (container) => {
    await openRow(container, "b");
    assert.equal(paneState(container), "docked");
    assert.equal(container.querySelector("nav.rail.start"), null);
  }));

test("InboxRail: the tile of the project holding the open page is current, whether a conversation or the project's page is open", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    assert.ok(tileOf(container, "api").classList.contains("cur"), "the conversation's project is current");
    assert.ok(!tileOf(container, "docs").classList.contains("cur"), "the other project is not");
    await press(one(container, 'section.dir[aria-label="docs"] button.proj'));
    assert.ok(tileOf(container, "docs").classList.contains("cur"), "the project page's project is current");
    assert.ok(!tileOf(container, "api").classList.contains("cur"), "the project the page left is not");
  }));

test("InboxRail: a tile opens its project's page beside the rail", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    await press(tileOf(container, "docs"));
    const page = projectPage(container);
    assert.ok(page !== null, "the project's page is drawn");
    assert.equal(page.querySelectorAll(".pc-row").length, 1, "the page is docs', which holds one conversation");
    assert.equal(paneState(container), "rail", "the inbox keeps resting as a rail beside the page");
  }));

test("InboxRail: the pointer on the rail brings the inbox out over the page, keeping the reply box's focus, its text and the page's reader", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    const reader = one(container, 'section[aria-label="reader"]');
    const box = replyBox(container);
    await act(() => {
      box.focus();
      box.value = "a draft";
      box.dispatchEvent(new Event("input", { bubbles: true }));
    });
    // happy-dom lays nothing out, so the reader's box is given a height and a content height, and is scrolled the way
    // the owner scrolls it, as headapp.test.tsx's scrollReader does.
    const scroller = one(reader, ".reader");
    Object.defineProperty(scroller, "scrollHeight", { value: 2000, configurable: true });
    Object.defineProperty(scroller, "clientHeight", { value: 400, configurable: true });
    await act(() => {
      scroller.scrollTop = 120;
      scroller.dispatchEvent(new Event("scroll"));
    });
    const panes = one(container, ".panes");
    const columns = [...panes.children].filter((child) => !child.classList.contains("rail")).map((child) => child.className);
    await pointer(rail(container), "pointerenter");
    assert.equal(paneState(container), "flyout", "the pointer on the rail brings the inbox out");
    assert.equal(document.activeElement, box, "the reply box keeps the focus");
    assert.equal(box.value, "a draft", "the reply box keeps its text");
    assert.equal(container.querySelector('section[aria-label="reader"]'), reader, "the page's reader is the same node");
    assert.equal(scroller.scrollTop, 120, "the page keeps its scroll");
    assert.deepEqual(
      [...panes.children].filter((child) => !child.classList.contains("rail")).map((child) => child.className),
      columns,
      "the page's columns keep their order and classes",
    );
  }));

test("InboxRail: esc and b tuck a key-popped inbox back, and the pointer leaving the rail tucks a pointer-popped one", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    await key("b");
    assert.equal(paneState(container), "flyout", "b brings the inbox out");
    await key("Escape");
    assert.equal(paneState(container), "rail", "Escape tucks it back");
    await key("b");
    await key("b");
    assert.equal(paneState(container), "rail", "b again tucks it back");
    await pointer(rail(container), "pointerenter");
    assert.equal(paneState(container), "flyout", "the pointer on the rail brings the inbox out");
    await pointer(rail(container), "pointerleave", null);
    assert.equal(paneState(container), "rail", "the pointer leaving the rail tucks it back");
  }));

test("InboxRail: the pointer moving between the rail and the inbox keeps it out, and leaving both tucks it", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    const list = one(container, ".list");
    await pointer(rail(container), "pointerenter");
    await pointer(rail(container), "pointerleave", list);
    assert.equal(paneState(container), "flyout", "a pointer moving from the rail into the inbox keeps it out");
    await pointer(list, "mouseleave", rail(container));
    assert.equal(paneState(container), "flyout", "a pointer moving from the inbox into the rail keeps it out");
    await pointer(list, "mouseleave", null);
    assert.equal(paneState(container), "rail", "the pointer leaving the inbox tucks it back");
  }));

test("InboxRail: the mark tile brings the inbox out by key, and Escape tucks it back", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    await press(tileOf(container, "Open the inbox"));
    assert.equal(paneState(container), "flyout", "the mark tile brings the inbox out");
    await key("Escape");
    assert.equal(paneState(container), "rail", "Escape tucks it back");
  }));

test("InboxRail: the search tile opens the search field with the inbox out, and Escape tucks the inbox back once the field is shut", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    await press(tileOf(container, "Search"));
    assert.equal(paneState(container), "flyout", "the search tile brings the inbox out");
    assert.ok(container.querySelector(".inbox-search input") !== null, "the search field is open");
    await key("Escape");
    await key("Escape");
    assert.equal(paneState(container), "rail", "Escape shuts the field, then tucks the inbox back");
  }));

for (const name of ["j", "k", "g", "G", "/"]) {
  test(`InboxRail: ${name} brings the rail's inbox out by key, and leaves the cursor row where it was`, (t) =>
    withDesk(t.signal, wideWidth, async (container) => {
      await openRow(container, "b");
      assert.equal(paneState(container), "rail", "the inbox rests as the rail before the key");
      const cursor = container.querySelector(".row.cur")?.getAttribute("data-key");
      await key(name);
      assert.ok(rail(container), "the rail stays drawn beside the page");
      assert.equal(paneState(container), "flyout", `${name} pops the rail's inbox out`);
      assert.equal(container.querySelector(".row.cur")?.getAttribute("data-key"), cursor, "the cursor row is unchanged");
    }));
}

test("InboxRail: the inbox's pin control and B pin and unpin the inbox, and the rail returns when it is unpinned", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    await key("B");
    assert.equal(paneState(container), "pinned", "B pins the inbox beside the page");
    assert.equal(container.querySelector("nav.rail.start"), null, "a pinned inbox draws no rail");
    await press(footPin(container));
    assert.equal(paneState(container), "rail", "the foot control unpins it, back to the rail");
    await press(footPin(container));
    assert.equal(paneState(container), "pinned", "the foot control pins it again");
    await key("B");
    assert.equal(paneState(container), "rail", "B unpins it");
  }));

test("InboxRail: the foot draws the pin control after Keys at 760px and wider, and under 760px draws none", (t) =>
  withDesk(t.signal, wideWidth, (container) => {
    const labels = Array.from(container.querySelectorAll(".inbox-foot > *")).map(labelOf);
    assert.deepEqual(labels.slice(-2), ["Keys", "Pin the inbox beside the page"]);
  }));

test("InboxRail: under 760px the foot draws no pin control", (t) =>
  withDesk(t.signal, narrowWidth, (container) => {
    assert.equal(footPin(container), null);
  }));
