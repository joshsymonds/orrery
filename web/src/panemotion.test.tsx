import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the word PaneMotion. The checks read the data-move the App draws on the held layout's elements, and
// the elements a move leaves mounted until their animationend, which each check dispatches itself. No check waits on the clock:
// a move's end is the animationend the element's animation fires, dispatched here as the browser fires it.

const now = 1000;
const desk = 1600;
const phone = 400;
const repository = "widgets-5d0c1a7";
const reducedQuery = "(prefers-reduced-motion: reduce)";

type Clients = ReturnType<typeof createClients>;
type Dom = ReturnType<typeof installTestDom>;

// Setup is what App is drawn with: its rows, its epics and its address.
interface Setup {
  rows: Row[];
  epics: EpicsView;
  address: Address;
}

// Mount is what a check gets: the container App draws into, and rerender, which draws App again with the setup given.
interface Mount {
  container: HTMLElement;
  rerender: (setup: Setup) => Promise<void>;
}

// fakeFetch answers each unary call with an empty message, and holds each conversation stream open.
async function fakeFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const path = new URL(new Request(input, init).url).pathname;
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "box",
    state: "done",
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

// steererRow is the session s1, which steers alpha and beta.
function steererRow(): Row {
  return row({
    key: "session:s1",
    id: "s1",
    kind: "session",
    host: "",
    label: "auth work",
    project: "/work/api",
    archiveId: "s1",
    archiveKind: "session",
  });
}

// otherRow is the session s2, which steers gamma: a conversation of its own, in a project of its own.
function otherRow(): Row {
  return row({
    key: "session:s2",
    id: "s2",
    kind: "session",
    host: "",
    label: "tide work",
    project: "/work/tide",
    archiveId: "s2",
    archiveKind: "session",
  });
}

function epicOf(slug: string, steering: string): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    steering,
  });
}

const alpha = epicOf("alpha", "s1");
const beta = epicOf("beta", "s1");
const delta = epicOf("delta", "s1");
const gamma = epicOf("gamma", "s2");

// view is the epics view of the epics, each with a detail whose summary names its slug.
function view(epics: Epic[]): EpicsView {
  const details = epics.map((epic): [string, EpicsGetResponse] => [
    epic.id,
    create(EpicsGetResponseSchema, { epic, summary: `Ship ${epic.slug}` }),
  ]);
  return epicsView(epics, new Map(details));
}

// epicAddress is the address of an epic's page.
function epicAddress(slug: string): Address {
  return { view: "inbox", entry: { kind: "epic", id: `${repository}/${slug}`, page: { kind: "epic" } } };
}

const steererAddress: Address = { view: "inbox", entry: { kind: "session", id: "s1" } };

// draw renders App over the setup in the container.
function draw(container: HTMLElement, clients: Clients, setup: Setup): Promise<void> {
  return act(() => {
    render(
      <App rows={setup.rows} now={now} clients={clients} epics={setup.epics} address={setup.address} />,
      container,
    );
  });
}

// runsMoves makes each element that carries a data-move run the animation style.css draws for it. The document lists those
// animations, each by the element it is on: a card, the tab and the chat column on their own element, and a neck on its ::before
// or ::after. As in the browser, an element's own getAnimations leaves its pseudo-elements out, so a neck's draw-back is seen
// only through the document. The window runs no CSS animation, so a leaving card or tab stays mounted until the animationend a
// check dispatches ends it.
function runsMoves(window: Dom): void {
  const doc = window.document as unknown as { getAnimations: () => unknown[] };
  doc.getAnimations = () =>
    [...window.document.querySelectorAll("[data-move]")].map((element) => ({ effect: { target: element } }));
}

// reduceMotion makes the window match reduced motion, and leaves every other query to the window. App reads the query at mount.
function reduceMotion(window: Dom): void {
  type Query = ReturnType<Dom["matchMedia"]>;
  const matchMedia = window.matchMedia.bind(window);
  const reduced = (media: string): Query =>
    ({ media, matches: true, addEventListener: () => undefined, removeEventListener: () => undefined }) as unknown as Query;
  window.matchMedia = (query: string) => (query === reducedQuery ? reduced(query) : matchMedia(query));
}

// mountApp mounts App at width in a fresh DOM, under reduced motion when reduced, and runs check with the mount. App is unmounted
// and the DOM removed when the test ends, or as soon as signal aborts first.
async function mountApp(
  signal: AbortSignal,
  width: number,
  setup: Setup,
  check: (mount: Mount) => void | Promise<void>,
  reduced = false,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  runsMoves(window);
  if (reduced) {
    reduceMotion(window);
  }
  const container = document.createElement("div");
  document.body.append(container);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch }));
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await draw(container, clients, setup);
    await check({ container, rerender: (next) => draw(container, clients, next) });
  } finally {
    await teardown();
  }
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// animationEnd fires the animationend an element's animation fires when it has run, and lets Preact draw what that changed. The
// event bubbles, as the browser's does, so the panes hear it.
async function animationEnd(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("animationend", { bubbles: true }));
  });
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// foldBack is the Fold back control of the held card.
function foldBack(container: HTMLElement): HTMLElement {
  return one(container, 'article.held-card button[aria-label="Fold back"]');
}

// leavingNecks selects the necks of a leaving tree: the rows and marks that draw back, every leaving element but the tab.
const leavingNecks = '[data-held][data-move="leave"]:not(.convo-block[data-held="tab"])';

test("PaneMotion open: opening an epic from its conversation alone marks the card, both necks, the tab and the chat column with data-move=open", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: steererAddress }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") });
    assert.equal(one(container, "article.held-card").getAttribute("data-move"), "open", "the card rises");
    assert.equal(one(container, '.list .row[data-held="convo"]').getAttribute("data-move"), "open", "the conversation's neck grows");
    assert.equal(one(container, '.list .row[data-held="open"]').getAttribute("data-move"), "open", "the epic's neck grows");
    assert.equal(one(container, '.list .convo-block[data-held="tab"]').getAttribute("data-move"), "open", "the tab's ground fades in");
    assert.equal(one(container, "section.read.held").getAttribute("data-move"), "open", "the chat column moves to the right");
  }));

test("PaneMotion open: the chat moves from the middle on its own, and its animationend clears the column's open mark while the card keeps its own", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: steererAddress }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") });
    await animationEnd(one(container, 'section.read[data-move="open"] > .reader'));
    assertNone(container.querySelector("section.read[data-move]"), "the column's mark clears on the chat's animationend");
    assert.equal(one(container, "article.held-card").getAttribute("data-move"), "open", "the card is not moved by the chat's move");
  }));

test("PaneMotion switch: switching to another epic of the same conversation keeps the old card with data-move=leave beside the new card with data-move=switch, until the old one's animationend removes it", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha, beta]), address: epicAddress("alpha") }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow()], epics: view([alpha, beta]), address: epicAddress("beta") });
    const leaving = one(container, 'article.held-card[data-move="leave"]');
    assert.equal(container.querySelectorAll("article.held-card").length, 2, "the old card stays mounted beside the new one");
    assert.equal(one(container, 'article.held-card[data-move="switch"]').getAttribute("data-kind"), "epic", "the new card rises");
    await animationEnd(leaving);
    assertNone(container.querySelector('article.held-card[data-move="leave"]'), "the old card is removed on its animationend");
    assert.equal(container.querySelectorAll("article.held-card").length, 1, "only the new card remains");
    assert.equal(one(container, "article.held-card").getAttribute("data-move"), "switch", "the new card keeps its move until its own animationend");
  }));

test("PaneMotion switch: a switch that repeats the last one draws its move again, once the new card's animationend has cleared it", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha, beta, delta]), address: epicAddress("alpha") }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow()], epics: view([alpha, beta, delta]), address: epicAddress("beta") });
    await animationEnd(one(container, 'article.held-card[data-move="leave"]'));
    await animationEnd(one(container, "article.held-card"));
    assertNone(container.querySelector("article.held-card[data-move]"), "the first switch has ended");
    await rerender({ rows: [steererRow()], epics: view([alpha, beta, delta]), address: epicAddress("delta") });
    assert.equal(one(container, 'article.held-card[data-move="switch"]').getAttribute("data-kind"), "epic", "the second switch rises");
    assert.equal(container.querySelectorAll("article.held-card").length, 2, "the card it leaves stays mounted");
  }));

test("PaneMotion other: opening a thing of another conversation marks the old tab leave, and the new tab and the head's title other", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow(), otherRow()], epics: view([alpha, gamma]), address: epicAddress("alpha") }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow(), otherRow()], epics: view([alpha, gamma]), address: epicAddress("gamma") });
    const leavingTab = one(container, '.convo-block[data-move="leave"]');
    assert.equal(one(container, '.convo-block[data-move="other"]').dataset.held, "tab", "the new tab is replaced in");
    await animationEnd(leavingTab);
    await animationEnd(one(container, leavingNecks));
    assertNone(container.querySelector('.convo-block[data-move="leave"]'), "the old tab is drawn at rest once its animationend has run");
    assert.equal(one(container, "section.read").getAttribute("data-move"), "other", "the head's title and the chat fade where they stand");
    assert.equal(one(container, 'article.held-card[data-move="other"]').getAttribute("data-kind"), "epic", "the new card rises from its own row");
  }));

test("PaneMotion other: the animationend that bubbles up from the column's fading reader clears the column's other mark", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow(), otherRow()], epics: view([alpha, gamma]), address: epicAddress("alpha") }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow(), otherRow()], epics: view([alpha, gamma]), address: epicAddress("gamma") });
    await animationEnd(one(container, 'section.read[data-move="other"] > .reader'));
    assertNone(container.querySelector("section.read[data-move]"), "the column's mark clears on the reader's animationend");
  }));

test("PaneMotion close: Fold back keeps the card with data-move=leave until its animationend, then the conversation is alone", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container }) => {
    await press(foldBack(container));
    const leaving = one(container, 'article.held-card[data-move="leave"]');
    await animationEnd(leaving);
    assertNone(container.querySelector("article.held-card"), "the card is removed on its animationend");
    assertNone(container.querySelector("section.read.held"), "the conversation is alone");
    assert.ok(container.querySelector(".reader") !== null, "the conversation's reader is drawn");
  }));

test("PaneMotion close: while the card folds the chat carries data-move=close and leaves the held layout, so it moves to the middle", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container }) => {
    await press(foldBack(container));
    assert.ok(container.querySelector('article.held-card[data-move="leave"]') !== null, "the card stays mounted while it folds");
    assert.equal(one(container, "section.read").getAttribute("data-move"), "close", "the chat carries the close move");
    assertNone(container.querySelector("section.read.held"), "the chat is no longer in the held layout while the card folds");
  }));

test("RiseBolder close: after Fold back the tab's animationend leaves the necks drawn, and the necks' own animationend takes them out", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container }) => {
    await press(foldBack(container));
    const tab = one(container, '.convo-block[data-move="leave"]');
    const neck = one(container, leavingNecks);
    await animationEnd(tab);
    assert.ok(container.querySelector(leavingNecks) !== null, "the necks stay drawn while they draw back after the tab's ground has faded");
    await animationEnd(neck);
    assertNone(container.querySelector(leavingNecks), "the necks are taken out at their own animationend");
    assertNone(container.querySelector('[data-held][data-move="leave"]'), "the leaving tab is taken out with them");
  }));

test("RiseBolder close: a leaving tab with no neck still drawing back is taken out at its own animationend, so no tree is left", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container }) => {
    await press(foldBack(container));
    for (const neck of container.querySelectorAll<HTMLElement>(leavingNecks)) {
      neck.removeAttribute("data-move");
    }
    await animationEnd(one(container, '.convo-block[data-move="leave"]'));
    assertNone(container.querySelector('[data-held][data-move="leave"]'), "the leaving tab is taken out at its animationend");
  }));

test("PaneMotion rail: between 760px and 1250px the rail's conversation neck and open mark carry the opening's move", (t) =>
  mountApp(t.signal, 1000, { rows: [steererRow()], epics: view([alpha]), address: steererAddress }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") });
    assert.equal(one(container, '.rail-lineage .lmark.lconv[data-held="convo"]').getAttribute("data-move"), "open", "the neck grows");
    assert.equal(one(container, '.rail-lineage .lmark.open[data-held="open"]').getAttribute("data-move"), "open", "the open mark grows");
  }));

test("PaneMotion phone: the phone's sheet opens with data-move=open, and the chat does not move", (t) =>
  mountApp(t.signal, phone, { rows: [steererRow()], epics: view([alpha]), address: steererAddress }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") });
    assert.equal(one(container, "section.held-sheet").getAttribute("data-move"), "open", "the sheet unfolds");
    assertNone(container.querySelector("section.read[data-move]"), "the chat does not move");
  }));

test("PaneMotion clears: the card's animationend clears its own data-move, and the open neck keeps its until its own animationend", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: steererAddress }, async ({ container, rerender }) => {
    await rerender({ rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") });
    const card = one(container, "article.held-card");
    await animationEnd(card);
    assertNone(container.querySelector("article.held-card[data-move]"), "the card's animationend clears its data-move");
    assert.equal(one(container, '.list .row[data-held="open"]').getAttribute("data-move"), "open", "the neck has not ended");
  }));

test("PaneMotion reduced: under reduced motion opening and folding back set no data-move, and the card is gone at once after Fold back", (t) =>
  mountApp(
    t.signal,
    desk,
    { rows: [steererRow()], epics: view([alpha]), address: steererAddress },
    async ({ container, rerender }) => {
      await rerender({ rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") });
      assert.ok(container.querySelector("article.held-card") !== null, "the epic is held");
      assertNone(container.querySelector("[data-move]"), "opening draws no data-move");
      await press(foldBack(container));
      assertNone(container.querySelector("article.held-card"), "the card is gone at once after Fold back");
      assertNone(container.querySelector("[data-move]"), "folding back draws no data-move");
    },
    true,
  ));
