import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { contracts as contractList } from "./fixtures/contracts";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the token HeldLayout, FoldBack or OpenColour: HeldLayout is an opened epic or contract held as a
// card set into its conversation, FoldBack is Fold back and the ] key, and OpenColour is the open colour the held thing and
// its tree marks draw. The checks read the DOM and the declarations of style.css, never pixels or the clock. Each test mounts
// App at a window width of its own, since the layout reads the width when App mounts.

const now = 1000;
const repository = "widgets-5d0c1a7";
const desk = 1600;

type Clients = ReturnType<typeof createClients>;

// Setup is what App is drawn with: its rows, its epics, its contracts and its address.
interface Setup {
  rows?: Row[];
  epics?: EpicsView;
  contracts?: Contract[];
  address?: Address;
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

// steererRow is the session row s1, which steers the epics these tests name.
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

// conversationRow is the session row of the conversation that proposed the tide-alerts contract.
function conversationRow(): Row {
  return row({
    key: "session:sess-tide-16",
    id: "sess-tide-16",
    label: "tide work",
    archiveId: "sess-tide-16",
    archiveKind: "session",
  });
}

function epicOf(slug: string, steering: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    steering,
    ...init,
  });
}

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
const contractAddress: Address = { view: "inbox", entry: { kind: "contract", id: "tidepool/tide-alerts" } };

const alpha = epicOf("alpha", "s1");

// draw renders App over the setup in the container.
function draw(container: HTMLElement, clients: Clients, setup: Setup): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows ?? []}
        now={now}
        clients={clients}
        epics={setup.epics}
        contracts={setup.contracts}
        address={setup.address}
      />,
      container,
    );
  });
}

// mountApp mounts App at width in a fresh DOM and runs check with the mount. App is unmounted and the DOM removed when the
// test ends, or as soon as signal aborts first.
async function mountApp(
  signal: AbortSignal,
  width: number,
  setup: Setup,
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
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

// pressKey sends a key to the document, as the keyboard does.
async function pressKey(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
  });
}

// childOf is the child of parent that matches the selector, or null when none does.
function childOf(parent: Element, selector: string): HTMLElement | null {
  return ([...parent.children].find((child) => child.matches(selector)) as HTMLElement | undefined) ?? null;
}

// heldColumn is the conversation's column when it holds a thing, or null.
function heldColumn(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>("section.read.held");
}

// foldBack is the Fold back control of the held card.
function foldBack(container: HTMLElement): HTMLElement {
  const control = container.querySelector<HTMLElement>("article.held-card button[aria-label=\"Fold back\"]");
  assert.ok(control !== null, "the held card draws Fold back");
  return control;
}

test("HeldLayout: an opened epic is an article.held-card inside the conversation's section.read.held, after its head and before its reader, and no aside.rpanel draws it", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, ({ container }) => {
    const held = heldColumn(container);
    assert.ok(held !== null, "the conversation's column is held");
    const card = childOf(held, "article.held-card");
    const head = childOf(held, ".convo-head");
    const reader = childOf(held, ".reader");
    assert.ok(card !== null, "the epic's card is set into the column");
    assert.ok(head !== null && reader !== null, "the head and the reader are drawn in the column");
    const children = [...held.children];
    assert.ok(children.indexOf(head) < children.indexOf(card), "the card comes after the head");
    assert.ok(children.indexOf(card) < children.indexOf(reader), "the card comes before the reader");
    assertNone(container.querySelector("aside.rpanel"), "no side panel draws the epic");
  }));

test("HeldLayout: a contract opens the same way, its card set into its conversation's column after the head and before the reader", (t) =>
  mountApp(
    t.signal,
    desk,
    { rows: [conversationRow()], contracts: contractList.contracts, address: contractAddress },
    ({ container }) => {
      const held = heldColumn(container);
      assert.ok(held !== null, "the conversation's column is held");
      const card = childOf(held, 'article.held-card[data-kind="contract"]');
      const head = childOf(held, ".convo-head");
      const reader = childOf(held, ".reader");
      assert.ok(card !== null, "the contract's card is set into the column");
      assert.ok(head !== null && reader !== null, "the head and the reader are drawn in the column");
      const children = [...held.children];
      assert.ok(children.indexOf(head) < children.indexOf(card), "the card comes after the head");
      assert.ok(children.indexOf(card) < children.indexOf(reader), "the card comes before the reader");
      assertNone(container.querySelector("aside.rpanel"), "no side panel draws the contract");
    },
  ));

test("HeldLayout: the tree draws the tab and both necks for the held epic", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, ({ container }) => {
    assert.ok(container.querySelector('.list .convo-block[data-held="tab"]'), "the conversation draws its tab");
    assert.ok(container.querySelector('.list .row[data-held="convo"]'), "the conversation's row is the first neck");
    assert.ok(container.querySelector('.list .row[data-held="open"]'), "the epic's row is the open neck");
  }));

test("HeldLayout: the head draws no work link while the epic is held", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: steererAddress }, async ({ container, rerender }) => {
    assert.ok(container.querySelector(".convo-head .wlink") !== null, "before the epic is held the head names its work link");
    await rerender({ rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") });
    assertNone(container.querySelector(".convo-head .wlink"), "held, the head names no work link");
  }));

test("HeldLayout: the card's title row draws no back button, and its crumbs stay", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, ({ container }) => {
    const titleRow = container.querySelector<HTMLElement>("article.held-card .title-row");
    assert.ok(titleRow !== null, "the card's page draws its title row");
    assertNone(titleRow.querySelector(".back"), "the title row draws no back button");
    assert.equal(titleRow.querySelector(".crumbs")?.textContent.includes("alpha"), true, "the crumbs name the epic");
  }));

test("HeldLayout: between 760 and 1250 the inbox draws as its rail with its neck and the open mark, and the card is held", (t) =>
  mountApp(t.signal, 1000, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, ({ container }) => {
    assert.equal(container.querySelector(".panes")?.getAttribute("data-inbox"), "collapsed", "the inbox draws as its rail");
    assert.ok(container.querySelector("nav.rail") !== null, "the rail is drawn");
    assert.ok(container.querySelector('.rail-lineage .lmark.lconv[data-held="convo"]') !== null, "the rail's conversation mark is its neck");
    assert.ok(container.querySelector('.rail-lineage .lmark.open[data-held="open"]') !== null, "the rail's open mark is drawn");
    assert.ok(container.querySelector('article.held-card[data-kind="epic"]') !== null, "the epic is held");
  }));

test("FoldBack: a press on Fold back leaves the conversation alone with the cursor on its row, and no row is held", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container }) => {
    await press(foldBack(container));
    assertNone(container.querySelector("article.held-card"), "Fold back folds the card back");
    assert.ok(container.querySelector(".reader") !== null, "the conversation's reader is still drawn");
    assert.equal(container.querySelector(".list .row.cur")?.getAttribute("data-key"), "session:s1", "the cursor rests on the conversation's row");
    assert.equal(container.querySelectorAll(".list [data-held]").length, 0, "no row of the tree is held");
  }));

test("FoldBack: ] folds the held card back, and ] again opens it", (t) =>
  mountApp(t.signal, desk, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container }) => {
    await pressKey("]");
    assertNone(container.querySelector("article.held-card"), "] folds the card back");
    await pressKey("]");
    assert.ok(container.querySelector('article.held-card[data-kind="epic"]') !== null, "] again opens the card");
  }));