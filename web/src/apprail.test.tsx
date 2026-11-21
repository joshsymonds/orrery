import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import type { SeenClock } from "./seen";
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

// stillClock is a seen clock whose timers never fire, so an unseen row under the cursor is never marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

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
    putOff: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// rows are three projects: api, where a and the older a-old wait on you and b works; docs, whose c is new and d is seen;
// and ops, whose q is seen and done, so ops is quiet.
function rows(): Row[] {
  return [
    row({ key: "a", label: "api one", state: "waiting", question: "Ship it?", changed: 300, seen: 300 }),
    row({ key: "a-old", label: "api old", state: "waiting", question: "Merge it?", changed: 100, seen: 100 }),
    row({ key: "b", label: "api two", changed: 200, seen: 200 }),
    row({ key: "c", label: "docs new", project: "/work/docs", state: "done", changed: 150, unseen: true }),
    row({ key: "d", label: "docs seen", project: "/work/docs", state: "done", changed: 140, seen: 140 }),
    row({ key: "q", label: "ops quiet", project: "/work/ops", state: "done", changed: 130, seen: 130 }),
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
      render(<App rows={rows()} now={now} clients={clients} seenClock={stillClock} />, container);
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

// inboxState is the inbox state the app draws on the panes: expanded or collapsed.
function inboxState(container: HTMLElement): string | null {
  return container.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// flyoutName is the project the open flyout names, or null with none open.
function flyoutName(container: HTMLElement): string | null {
  return container.querySelector(".pfly")?.getAttribute("aria-label") ?? null;
}

// projectPage is the project page section, when it is drawn.
function projectPage(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label="project"]:not([hidden])');
}

// cursorKey is the key of the row the cursor rests on, or null when no row does.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

// press dispatches a click on the element, and lets Preact draw what the click changed.
async function press(element: Element | null): Promise<void> {
  assert.ok(element, "the element to press is drawn");
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

// openRow clicks the list's row for key, then takes the focus off the reply box it opens, so the keys that follow reach
// the page as they do once a press lands on the page.
async function openRow(container: HTMLElement, key: string): Promise<void> {
  await press(container.querySelector(`.list [data-key="${key}"]`));
  await blur();
}

// blur takes the focus off whatever holds it.
async function blur(): Promise<void> {
  await act(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });
}

// pointer dispatches a pointer event of the type on the element: pointerover bubbles, pointerleave does not.
async function pointer(element: Element, type: "pointerover" | "pointerleave"): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event(type, { bubbles: type === "pointerover" }));
  });
}

// key is a key press on the document, as the keys reach App, with Shift held when shift is set.
async function key(name: string, shift = false): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: name, shiftKey: shift, cancelable: true }));
  });
}

// footCollapse is the inbox foot's collapse control, when it is drawn.
function footCollapse(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('.inbox-foot button[aria-label="Collapse the inbox"]');
}

// collapse collapses the inbox with the foot's control.
async function collapse(container: HTMLElement): Promise<void> {
  await press(footCollapse(container));
  assert.equal(inboxState(container), "collapsed", "the foot's control collapses the inbox");
}

// railLabels are the labels of the children of the rail, in order, with the tiles' scroll area's children in its place.
function railLabels(container: HTMLElement): string[] {
  return Array.from(rail(container).children).flatMap((child) =>
    child.classList.contains("rail-tiles") ? Array.from(child.children).map(labelOf) : [labelOf(child)],
  );
}

test("InboxRail: at 760px and wider the inbox is expanded, draws no rail, and its foot draws the collapse control after Keys", (t) =>
  withDesk(t.signal, wideWidth, (container) => {
    assert.equal(inboxState(container), "expanded");
    assert.equal(container.querySelector("nav.rail.start"), null);
    const foot = Array.from(container.querySelectorAll(".inbox-foot > *")).map(labelOf);
    assert.deepEqual(foot.slice(-2), ["Keys", "Collapse the inbox"]);
    assert.equal(footCollapse(container)?.getAttribute("title"), "Collapse the inbox  b");
  }));

test("InboxRail: collapsed, the rail draws the mark, a tile per project in the tree's order, New, Search, Notices and Keys, and the mark expands it", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    assert.deepEqual(railLabels(container), [
      "Expand the inbox",
      "rail-sep",
      "api: 2 wait on you",
      "docs",
      "ops",
      "spring",
      "New session",
      "Search",
      "Notices",
      "Keys",
    ]);
    const listed = Array.from(container.querySelectorAll("section.dir")).map((section) => section.getAttribute("aria-label"));
    assert.deepEqual(listed, ["api", "docs", "ops"], "the list draws the projects in the order the rail's tiles follow");
    assert.equal(one(tileOf(container, "api"), ".vn").textContent, "api");
    assert.equal(one(tileOf(container, "api"), ".n").textContent, "2", "the api tile counts its two waiting rows");
    assert.equal(tileOf(container, "ops").classList.contains("quiet"), true, "ops has nothing waiting or new");
    assert.equal(tileOf(container, "docs").classList.contains("quiet"), false, "docs has a new row");
    await press(tileOf(container, "Expand the inbox"));
    assert.equal(inboxState(container), "expanded", "the mark expands the inbox");
    assert.equal(container.querySelector("nav.rail.start"), null);
  }));

test("InboxRail: b collapses the inbox and b again expands it, and B does neither", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await key("B");
    assert.equal(inboxState(container), "expanded", "B does not collapse the inbox");
    await key("b");
    assert.equal(inboxState(container), "collapsed", "b collapses the inbox");
    await key("B");
    assert.equal(inboxState(container), "collapsed", "B does not expand the inbox");
    await key("b");
    assert.equal(inboxState(container), "expanded", "b expands the inbox");
  }));

test("InboxRail: opening a row leaves the inbox as it was, expanded or collapsed", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    assert.equal(inboxState(container), "expanded", "a click on a row leaves the inbox expanded");
    await key("j");
    await key("Enter");
    await blur();
    assert.equal(inboxState(container), "expanded", "Enter on a row leaves the inbox expanded");
    await collapse(container);
    await press(tileOf(container, "docs"));
    assert.equal(cursorKey(container), "c", "the docs tile opened its new row");
    assert.equal(inboxState(container), "collapsed", "an opening from the rail leaves the inbox collapsed");
    await press(tileOf(container, "ops"));
    assert.ok(projectPage(container) !== null, "the ops tile opened its project's page");
    assert.equal(inboxState(container), "collapsed", "a project's page leaves the inbox collapsed");
  }));

test("InboxRail: under 760px the inbox is drawn as the stacked layout draws it, with no rail and no collapse control, and b does nothing", (t) =>
  withDesk(t.signal, narrowWidth, async (container) => {
    assert.equal(inboxState(container), "expanded");
    assert.equal(footCollapse(container), null, "the foot draws no collapse control");
    await key("b");
    assert.equal(inboxState(container), "expanded", "b does not collapse the stacked inbox");
    assert.equal(container.querySelector("nav.rail.start"), null);
  }));

test("InboxRail: the tile of the project holding the open page is current, whether a conversation or the project's page is open", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    await collapse(container);
    assert.ok(tileOf(container, "api").classList.contains("cur"), "the conversation's project is current");
    assert.ok(!tileOf(container, "docs").classList.contains("cur"), "the other project is not");
    await press(tileOf(container, "ops"));
    assert.ok(tileOf(container, "ops").classList.contains("cur"), "the project page's project is current");
    assert.ok(!tileOf(container, "api").classList.contains("cur"), "the project the page left is not");
  }));

test("InboxRail: a click on a tile goes to the project's oldest waiting row, else its first new row, else its page", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    await press(tileOf(container, "api"));
    assert.equal(cursorKey(container), "a-old", "api's oldest waiting row is opened");
    assert.equal(document.activeElement, container.querySelector(".reply:not([hidden]) textarea"), "into its reply box");
    await blur();
    await press(tileOf(container, "docs"));
    assert.equal(cursorKey(container), "c", "docs waits on nothing, so its new row is opened");
    await blur();
    await press(tileOf(container, "ops"));
    const page = projectPage(container);
    assert.ok(page !== null, "ops has nothing waiting or new, so its page is opened");
    assert.equal(page.querySelectorAll(".pc-row").length, 1, "the page is ops', which holds one conversation");
    assert.equal(inboxState(container), "collapsed");
  }));

test("InboxRail: the pointer on a tile opens its project's flyout, a tile under it moves the flyout, and leaving the rail closes it", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    assert.equal(flyoutName(container), null, "no flyout is open at first");
    await pointer(one(tileOf(container, "api"), ".vn"), "pointerover");
    assert.equal(flyoutName(container), "api", "the pointer on api's tile opens its flyout");
    await pointer(one(container, ".pfly .pf-head"), "pointerover");
    assert.equal(flyoutName(container), "api", "the pointer in the flyout keeps it");
    await pointer(tileOf(container, "docs"), "pointerover");
    assert.equal(flyoutName(container), "docs", "the pointer on docs' tile moves the flyout to docs");
    await pointer(tileOf(container, "Keys"), "pointerover");
    assert.equal(flyoutName(container), null, "the pointer on the rail's other tiles closes it");
    await pointer(tileOf(container, "ops"), "pointerover");
    await pointer(rail(container), "pointerleave");
    assert.equal(flyoutName(container), null, "the pointer leaving the rail closes it");
  }));

test("InboxRail: the flyout lists the project's rows that wait, then its new rows, with its counts, its rest and its page", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    await pointer(tileOf(container, "api"), "pointerover");
    const fly = one(container, ".pfly");
    assert.equal(one(fly, ".pf-nm").textContent, "api");
    assert.equal(one(fly, ".pf-path").textContent, "/work/api");
    assert.equal(one(fly, ".pf-n .is-attention").textContent, "2 wait");
    assert.deepEqual(
      Array.from(fly.querySelectorAll(".pf-row")).map((item) => item.getAttribute("data-key")),
      ["a-old", "a"],
    );
    assert.equal(one(fly, '.pf-row[data-key="a-old"] .asks').textContent, "Merge it?");
    assert.equal(one(fly, ".pf-more").textContent, "and 1 more, none waiting");
    assert.equal(one(fly, "button.pf-open").textContent, "Open api’s page");
    await press(one(fly, "button.pf-open"));
    assert.ok(projectPage(container) !== null, "the foot opens api's page");
    assert.equal(flyoutName(container), null, "an opening closes the flyout");
    assert.equal(inboxState(container), "collapsed");
  }));

test("InboxRail: a click on a flyout's row opens that row", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    await pointer(tileOf(container, "api"), "pointerover");
    await press(one(container, '.pfly .pf-row[data-key="a"]'));
    assert.equal(cursorKey(container), "a", "the row pressed is opened");
    assert.equal(flyoutName(container), null);
  }));

test("InboxRail: p opens the flyout on the current project's tile, and ↓ j ↑ k move it a tile at a time, held at the ends", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "c");
    await collapse(container);
    await key("p");
    assert.equal(flyoutName(container), "docs", "p opens the flyout on docs, which holds the open row");
    await key("ArrowDown");
    assert.equal(flyoutName(container), "ops", "↓ moves it to the tile below");
    await key("j");
    assert.equal(flyoutName(container), "ops", "j at the last tile stays there");
    await key("k");
    assert.equal(flyoutName(container), "docs", "k moves it to the tile above");
    await key("ArrowUp");
    await key("ArrowUp");
    assert.equal(flyoutName(container), "api", "↑ at the first tile stays there");
    assert.equal(cursorKey(container), "c", "moving the flyout moves no cursor");
    assert.equal(inboxState(container), "collapsed");
  }));

test("InboxRail: p with no project's page open opens the flyout on the first tile", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await key("v");
    assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "archived");
    await collapse(container);
    assert.equal(rail(container).querySelector(".vtile.cur"), null, "no tile is current in an empty Archived view");
    await key("p");
    assert.equal(flyoutName(container), "api");
  }));

test("InboxRail: ⏎ in the flyout opens its first row, as a click on its tile does, even with a tile focused", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    await key("p");
    assert.equal(flyoutName(container), "api");
    assert.ok(one(container, '.pfly .pf-row[data-key="a-old"]').classList.contains("cur"), "the first row is the cursor");
    await act(() => {
      tileOf(container, "ops").focus();
    });
    await key("Enter");
    assert.equal(cursorKey(container), "a-old", "⏎ opens api's oldest waiting row, not the focused tile");
    assert.equal(flyoutName(container), null, "the opening closes the flyout");
    assert.equal(projectPage(container), null, "the focused ops tile was not pressed");
    assert.equal(inboxState(container), "collapsed");
  }));

test("InboxRail: Escape closes a flyout and nothing else, and b closes it and expands the inbox", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    await key("p");
    await key("Escape");
    assert.equal(flyoutName(container), null, "Escape closes the flyout");
    assert.equal(inboxState(container), "collapsed", "and leaves the inbox collapsed");
    await key("p");
    await key("b");
    assert.equal(inboxState(container), "expanded", "b expands the inbox");
    await key("b");
    assert.equal(flyoutName(container), null, "and the flyout it closed stays closed");
  }));

test("InboxRail: a flyout p opened stays when the pointer leaves the rail, and the pointer on a tile takes it over", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    await key("p");
    await pointer(rail(container), "pointerleave");
    assert.equal(flyoutName(container), "api", "the pointer leaving keeps a flyout a key opened");
    await pointer(tileOf(container, "docs"), "pointerover");
    assert.equal(flyoutName(container), "docs", "the pointer on docs' tile takes it over");
    await pointer(rail(container), "pointerleave");
    assert.equal(flyoutName(container), null, "and once the pointer took it, the pointer leaving closes it");
  }));

for (const name of ["j", "k", "ArrowDown", "ArrowUp", "g", "G", "/", "v"]) {
  test(`InboxRail: collapsed with no flyout, ${name} expands the inbox and does nothing else`, (t) =>
    withDesk(t.signal, wideWidth, async (container) => {
      await openRow(container, "b");
      await collapse(container);
      await key(name);
      assert.equal(inboxState(container), "expanded", `${name} expands the inbox`);
      assert.equal(cursorKey(container), "b", "the cursor row is unchanged");
      assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "inbox", "the view is unchanged");
      assert.equal(container.querySelector(".inbox-search input"), null, "no search field opened");
    }));
}

test("InboxRail: h at a row's first crumb leaves a collapsed inbox collapsed", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    await collapse(container);
    await key("h");
    await key("ArrowLeft");
    assert.equal(inboxState(container), "collapsed");
  }));

test("InboxRail: Shift+Tab from a reply box expands a collapsed inbox and focuses the cursor row", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await openRow(container, "b");
    await collapse(container);
    const box = one(container, ".reply:not([hidden]) textarea");
    await act(() => {
      box.focus();
    });
    const tab = new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true });
    await act(() => {
      box.dispatchEvent(tab);
    });
    assert.equal(tab.defaultPrevented, true);
    assert.equal(inboxState(container), "expanded", "Shift+Tab expands the inbox");
    assert.equal(document.activeElement, container.querySelector(".row.cur"), "the focus is on the cursor row");
    assert.equal(cursorKey(container), "b");
  }));

test("InboxRail: the rail's Search tile expands the inbox and opens its search field", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    await press(tileOf(container, "Search"));
    assert.equal(inboxState(container), "expanded");
    assert.ok(container.querySelector(".inbox-search input") !== null, "the search field is open");
  }));

test("InboxRail: in the Archived view the tiles are the inbox's, so a project keeps its tile and count, and switching back draws the same", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await collapse(container);
    const inbox = railLabels(container);
    await press(tileOf(container, "Expand the inbox"));
    await key("v");
    await collapse(container);
    assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "archived", "v shows the Archived view");
    assert.equal(one(tileOf(container, "api"), ".n").textContent, "2", "the api tile keeps its count in the Archived view");
    assert.deepEqual(railLabels(container), inbox, "the Archived view's rail draws the inbox's tiles and counts");
    await press(tileOf(container, "Expand the inbox"));
    const back = [...container.querySelectorAll<HTMLElement>(".inbox-foot button")].find(
      (button) => button.textContent === "Back to the inbox",
    );
    await press(back ?? null);
    await collapse(container);
    assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "inbox", "Back returns to the inbox view");
    assert.deepEqual(railLabels(container), inbox, "the inbox view draws the same tiles and counts");
  }));

test("InboxRail: in the Archived view a click on a tile brings the inbox view back and opens the project's waiting row", (t) =>
  withDesk(t.signal, wideWidth, async (container) => {
    await key("v");
    await collapse(container);
    await press(tileOf(container, "api"));
    assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "inbox", "the inbox view is back");
    assert.equal(cursorKey(container), "a-old", "api's oldest waiting row is opened");
    assert.equal(inboxState(container), "collapsed");
  }));
