import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Design, DesignSchema } from "./gen/orrery/v1/designs_pb";
import type { Row } from "./rows";
import type { SeenClock } from "./seen";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the token HeldLayout: an opened design is held as a card set into its conversation's column, drawn
// as the epic and contract cards are. The checks read the DOM only. Each test mounts App at a window width of its own, since
// the layout reads the width when App mounts. No clock runs: the seen clock's timers never fire.

const now = 1000;
const desk = 1600;
const railWidth = 1000;
const repository = "repo-a";
const session = "sess-tide";

type Clients = ReturnType<typeof createClients>;

// Setup is what App is drawn with: its rows, its contracts and its designs.
interface Setup {
  rows: Row[];
  contracts: Contract[];
  designs: Design[];
}

// Mount is what a check gets: the container App draws into.
interface Mount {
  container: HTMLElement;
}

// stillClock is a seen clock whose timers never fire, so an unseen row under the cursor is never marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// fakeFetch answers each unary call with an empty message, and holds each conversation stream open with no message.
async function fakeFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const path = new URL(new Request(input, init).url).pathname;
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

// tideRow is the working session sess-tide of /work/tide, which the design works.
const tideRow: Row = {
  tool: "",
  id: session,
  kind: "pane",
  key: session,
  label: "tide work",
  archiveId: session,
  project: "/work/tide",
  host: "",
  state: "working",
  summary: "",
  question: "",
  questionHash: "",
  changed: 300,
  seen: 300,
  unseen: false,
  archived: false,
  putOff: false,
  archiveKind: "session",
};

// contractOf is a contract of the session, under the repository, with the title and state given.
function contractOf(slug: string, title: string, state: string): Contract {
  return create(ContractSchema, {
    id: `${repository}/${slug}`,
    repository,
    title,
    care: "limited",
    state,
    version: 1n,
    openDecisions: 0n,
    conversation: session,
    changed: 200n,
  });
}

// tide is the design the session works, whose contracts are given out of position order: alpha is first in the start order and
// beta second, so the card must draw alpha above beta.
const tide: Design = create(DesignSchema, {
  id: "tide",
  title: "Tide alerts",
  conversation: session,
  created: 10n,
  state: "One contract accepted, one proposed",
  contracts: [
    { id: `${repository}/beta`, title: "beta", repository, state: "proposed", position: 2n },
    { id: `${repository}/alpha`, title: "alpha", repository, state: "accepted", position: 1n },
  ],
});

// setup is the one conversation, its two contracts and the tide design.
function setup(): Setup {
  return {
    rows: [tideRow],
    contracts: [contractOf("alpha", "alpha", "accepted"), contractOf("beta", "beta", "proposed")],
    designs: [tide],
  };
}

// draw renders App over the setup in the container.
function draw(container: HTMLElement, clients: Clients, next: Setup): Promise<void> {
  return act(() => {
    render(
      <App
        rows={next.rows}
        now={now}
        clients={clients}
        contracts={next.contracts}
        designs={next.designs}
        seenClock={stillClock}
      />,
      container,
    );
  });
}

// mountApp mounts App at width in a fresh DOM and runs check with the mount. App is unmounted and the DOM removed when the
// test ends, or as soon as signal aborts first.
async function mountApp(signal: AbortSignal, width: number, check: (mount: Mount) => void | Promise<void>): Promise<void> {
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
    await draw(container, clients, setup());
    await check({ container });
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

// required is the element that matches selector under root, which must be drawn.
function required(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// heldDesign is the held card of a design, or null when none is drawn.
function heldDesign(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('article.held-card[data-kind="design"]');
}

// foldBack is the Fold back control of the held card.
function foldBack(container: HTMLElement): HTMLElement {
  return required(container, 'article.held-card button[aria-label="Fold back"]');
}

// contractRow is the row of the held design's card that names the contract with the title given.
function contractRow(container: HTMLElement, title: string): HTMLElement {
  const row = [...container.querySelectorAll<HTMLElement>("article.held-card .design-card-contract")].find(
    (candidate) => candidate.querySelector(".design-card-title")?.textContent === title,
  );
  assert.ok(row !== undefined, `the held design's card draws the contract ${title}`);
  return row;
}

test("HeldLayout: a press on a design's tab holds its card in its conversation's column, with the design's tab the open row and the conversation's tab and both necks", (t) =>
  mountApp(t.signal, desk, async ({ container }) => {
    assertNone(heldDesign(container), "no design is held before the press");
    await press(required(container, '.list .design-tab[data-key="design:tide"]'));
    const card = heldDesign(container);
    assert.ok(card !== null, "the design's card is held");
    const column = required(container, "section.read.held");
    assert.equal(card.parentElement, column, "the card is set into the conversation's column");
    assert.ok(container.querySelector('.list .design-tab[data-held="open"]') !== null, "the design's tab is the open row");
    assert.ok(container.querySelector('.list .convo-block[data-held="tab"]') !== null, "the conversation draws its tab");
    assert.ok(container.querySelector('.list .row[data-held="convo"]') !== null, "the conversation's row is the first neck");
    assert.equal(container.querySelectorAll(".list .design-tab[data-held]").length, 1, "only the design's tab is held");
  }));

test("HeldLayout: the held card draws the design's state, then its contracts in position order, each with its title and its state", (t) =>
  mountApp(t.signal, desk, async ({ container }) => {
    await press(required(container, '.list .design-tab[data-key="design:tide"]'));
    const card = required(container, 'article.held-card[data-kind="design"]');
    assert.equal(required(card, ".design-card-state").textContent, "One contract accepted, one proposed", "the state is the record's phrase");
    const rows = [...card.querySelectorAll<HTMLElement>(".design-card-contract")];
    assert.deepEqual(
      rows.map((row) => row.querySelector(".design-card-title")?.textContent),
      ["alpha", "beta"],
      "the contracts are in position order",
    );
    assert.deepEqual(
      rows.map((row) => row.querySelector(".design-card-contract-state")?.textContent),
      ["accepted", "proposed"],
      "each contract draws its state",
    );
  }));

test("HeldLayout: a design link in the title opens the design's card set into its conversation's column", (t) =>
  mountApp(t.signal, desk, async ({ container }) => {
    assertNone(heldDesign(container), "no design is held before the press");
    await press(required(container, '.wlink[title="Open Tide alerts"]'));
    const card = heldDesign(container);
    assert.ok(card !== null, "the link holds the design's card");
    assert.equal(card.parentElement, required(container, "section.read.held"), "the card is set into the conversation's column");
    assert.ok(container.querySelector('.list .design-tab[data-held="open"]') !== null, "the design's tab is the open row");
  }));

test("HeldLayout: between 760 and 1250 the rail's design mark opens the card, and the rail stays drawn with the design's mark open", (t) =>
  mountApp(t.signal, railWidth, async ({ container }) => {
    await press(required(container, '.inbox-foot button[aria-label="Collapse the inbox"]'));
    assert.equal(required(container, ".panes").getAttribute("data-inbox"), "collapsed", "the inbox draws as its rail");
    assertNone(heldDesign(container), "no design is held before the press");
    await press(required(container, '.rail-lineage button[aria-label="design: Tide alerts"]'));
    assert.ok(heldDesign(container) !== null, "the rail's design mark holds the design's card");
    assert.equal(required(container, ".panes").getAttribute("data-inbox"), "collapsed", "the rail stays drawn while the card is held");
    assert.ok(
      container.querySelector('.rail-lineage button[aria-label="design: Tide alerts"][data-held="open"]') !== null,
      "the rail's design mark is open",
    );
  }));

test("HeldLayout: a press on one of the held design's contract rows holds that contract instead of the design", (t) =>
  mountApp(t.signal, desk, async ({ container }) => {
    await press(required(container, '.list .design-tab[data-key="design:tide"]'));
    await press(contractRow(container, "beta"));
    assertNone(heldDesign(container), "the design's card is no longer held");
    assert.ok(container.querySelector('article.held-card[data-kind="contract"]') !== null, "the contract's card is held");
    assert.ok(container.querySelector('.list .row[data-held="convo"]') !== null, "the conversation stays the neck of the held contract");
  }));

test("HeldLayout: Fold back returns the conversation alone, with the cursor on its row and no row held", (t) =>
  mountApp(t.signal, desk, async ({ container }) => {
    await press(required(container, '.list .design-tab[data-key="design:tide"]'));
    await press(foldBack(container));
    assertNone(heldDesign(container), "Fold back folds the design's card back");
    assert.ok(container.querySelector(".reader") !== null, "the conversation's reader is still drawn");
    assert.equal(container.querySelector(".list .row.cur")?.getAttribute("data-key"), session, "the cursor rests on the conversation's row");
    assert.equal(container.querySelectorAll(".list [data-held]").length, 0, "no row of the tree is held");
  }));
