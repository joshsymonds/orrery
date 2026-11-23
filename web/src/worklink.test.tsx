import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Design, DesignContractSchema, DesignSchema } from "./gen/orrery/v1/designs_pb";
import { type Epic, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { App } from "./app";
import { createClients, createTransport } from "./client";
import { epicsView, type EpicsView, noEpics } from "./epics";
import { Head } from "./head";
import type { Address } from "./router";
import type { Row } from "./rows";
import type { SeenClock, SeenWindow } from "./seen";
import { assertNone, assertSame, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";
import { type WorkLinkItem, workLinksOf } from "./work";
import { WorkLink } from "./worklink";

// Each test draws into a container on the document, and reads the DOM only. The session is s1, and the things it works on are
// built from the fixtures below, through workLinksOf, as the middle conversation's head is.

const session = "s1";
const repository = "widgets-5d0c1a7";
const noop = () => undefined;
const now = 1000;
const wideWidth = 1024;

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    await check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the element into the container, and flushes its effects.
function draw(container: HTMLElement, element: JSX.Element): void {
  act(() => {
    render(element, container);
  });
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// makeEpic is a live epic of the repository that session steers, unless init says otherwise.
function makeEpic(slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    steering: session,
    ...init,
  });
}

// makeContract is a proposed contract that session proposed, whose id is its repository and slug joined.
function makeContract(slug: string, init: MessageInitShape<typeof ContractSchema> = {}): Contract {
  return create(ContractSchema, {
    id: `${repository}/${slug}`,
    repository,
    title: slug,
    care: "limited",
    state: "proposed",
    version: 1n,
    openDecisions: 0n,
    conversation: session,
    ...init,
  });
}

// makeDesign is a design that session works, which holds the contracts given.
function makeDesign(id: string, title: string, contracts: MessageInitShape<typeof DesignContractSchema>[] = []): Design {
  return create(DesignSchema, { id, title, conversation: session, created: 10n, state: "", ofOne: false, contracts });
}

// heldBy is a design's contract that holds the epic with the slug given, as a design's contract names it.
function heldBy(slug: string): MessageInitShape<typeof DesignContractSchema> {
  return { id: `${repository}/${slug}`, epic: `${repository}/${slug}`, title: slug, repository };
}

// viewOf is the epics view of the epics given, each detailed by the questions it asks, if any.
function viewOf(epics: Epic[], asking: string[] = []): EpicsView {
  return epicsView(
    epics,
    new Map(
      epics
        .filter((epic) => asking.includes(epic.slug))
        .map((epic) => [epic.id, create(EpicsGetResponseSchema, { epic, questions: [{ id: "Q1", text: "Use Postgres?" }] })]),
    ),
  );
}

// row is the conversation the head draws: a pane that holds nothing the head reads.
function row(): Row {
  return {
    key: "api",
    id: "%1",
    archiveId: session,
    kind: "pane",
    label: "orrery",
    project: "/work/orrery",
    host: "den",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    tool: "",
    changed: 200,
    seen: 200,
    unseen: false,
    archived: false,
    archiveKind: "session",
    putOff: false,
  };
}

// headWith draws the middle conversation's head with the work links given, at the width given.
function headWith(links: WorkLinkItem[] | undefined, narrow: boolean): JSX.Element {
  return (
    <Head
      row={row()}
      conversation={null}
      title="Orrery work links"
      host={null}
      showStrip={false}
      onBack={noop}
      note=""
      lines={[]}
      askingWords=""
      badges={[]}
      fold="open"
      narrow={narrow}
      onOpenEpic={noop}
      onFind={noop}
      onHide={noop}
      onShow={noop}
      menu={{ stripsShown: false, toggleStrips: noop, markSeen: noop, archive: null, keys: noop }}
      workLinks={links === undefined ? undefined : { items: links, onOpen: noop }}
    />
  );
}

// namesOf is the name each link of root draws, in order.
function namesOf(root: ParentNode): string[] {
  return all(root, ".wlink .nm").map((name) => name.textContent ?? "");
}

test("WorkLink: the links follow the title inside the title row", async () => {
  await withDom((container) => {
    const links = workLinksOf(session, [makeDesign("d1", "Round 8 links")], noEpics, []);
    draw(container, headWith(links, false));

    const title = one(container, ".title-row .crumb.here");
    const drawn = one(container, ".title-row .wlinks");
    assert.ok(title.compareDocumentPosition(drawn) & Node.DOCUMENT_POSITION_FOLLOWING, "the links come after the title");
    assertNone(drawn.closest(".crumbs"), "the links are not a crumb");
    assert.equal(title.textContent, "Orrery work links");
  });
});

test("WorkLink: a design link names its title, and a designless epic's link its slug", async () => {
  await withDom((container) => {
    // The rail epic is held by the design, so only the picker, which no design holds, is a link of its own.
    const design = makeDesign("d1", "Round 8 links", [heldBy("rail")]);
    const view = viewOf([makeEpic("rail"), makeEpic("picker")]);
    draw(container, <WorkLink items={workLinksOf(session, [design], view, [])} narrow={false} onOpen={noop} />);

    assert.deepEqual(namesOf(container), ["Round 8 links", "picker"]);
    const [designLink, epicLink] = all(container, ".wlink");
    assert.equal(designLink.getAttribute("title"), "Open Round 8 links");
    assert.equal(epicLink.getAttribute("title"), "Open picker");
  });
});

test("WorkLink: three things give two links and +1 at the desk, and one link and +2 under 760px", async () => {
  await withDom((container) => {
    const items = workLinksOf(session, [makeDesign("d1", "Round 8 links")], viewOf([makeEpic("rail")]), [
      makeContract("picker-spec"),
    ]);
    assert.equal(items.length, 3);

    draw(container, <WorkLink items={items} narrow={false} onOpen={noop} />);
    assert.equal(all(container, ".wlink").length, 2, "two links at the desk");
    assert.equal(one(container, ".wlink-more").textContent, "+1");

    draw(container, <WorkLink items={items} narrow={true} onOpen={noop} />);
    assert.equal(all(container, ".wlink").length, 1, "one link under 760px");
    assert.equal(one(container, ".wlink-more").textContent, "+2");
  });
});

test("WorkLink: no link holds a chevron, a state word or an ask", async () => {
  await withDom((container) => {
    // The epic asks, the contract has a decision open, and the design is working: none of that is drawn on a link.
    const items = workLinksOf(
      session,
      [makeDesign("d1", "Round 8 links")],
      viewOf([makeEpic("rail")], ["rail"]),
      [makeContract("picker-spec", { openDecisions: 2n })],
    );
    draw(container, <WorkLink items={items} narrow={false} onOpen={noop} />);

    const links = all(container, ".wlink");
    assert.equal(links.length, 2);
    for (const [index, link] of links.entries()) {
      assert.equal(link.textContent, items[index].name, "a link's text is its name, and nothing else");
      assert.equal(link.querySelectorAll("svg").length, 1, "a link draws its mark alone, with no chevron beside it");
    }
  });
});

test("WorkLink: a conversation that works on nothing draws no links", async () => {
  await withDom((container) => {
    draw(container, <WorkLink items={[]} narrow={false} onOpen={noop} />);
    assertNone(container.querySelector(".wlinks"), "no span for nothing");

    const items = workLinksOf("", [makeDesign("d1", "Round 8 links")], noEpics, [makeContract("picker-spec")]);
    draw(container, headWith(items, false));
    assertNone(container.querySelector(".title-row .wlinks"), "an empty session draws no links, whatever the epics and designs hold");
  });
});

test("WorkLink: a press on the +n control's menu item opens that thing", async () => {
  await withDom((container) => {
    const opened: WorkLinkItem[] = [];
    const items = workLinksOf(session, [makeDesign("d1", "Round 8 links")], viewOf([makeEpic("rail")]), [
      makeContract("picker-spec"),
    ]);
    draw(container, <WorkLink items={items} narrow={false} onOpen={(item) => opened.push(item)} />);

    assertNone(container.querySelector(".menu"), "the menu is shut until the control is pressed");
    act(() => {
      one(container, ".wlink-more").click();
    });
    const entries = all(container, ".menu [role='menuitem']");
    assert.deepEqual(
      entries.map((entry) => entry.textContent),
      items.map((item) => item.name),
      "the menu lists every thing",
    );
    act(() => {
      entries[2].click();
    });
    assert.deepEqual(opened, [items[2]]);
    assertNone(container.querySelector(".menu"), "choosing an item shuts the menu");
    assertSame(container.querySelector(".wlink-more")?.getAttribute("aria-expanded"), "false");
  });
});

// quietFetch answers the app's calls with an empty body, and a conversation's stream with a body that stays open, so no
// conversation arrives and no timer runs.
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

// withApp mounts App at the desk over the row, epics and designs given, in a fresh DOM, and runs check with its container.
// App is unmounted and the DOM removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  setup: { rows: Row[]; epics: EpicsView; designs: Design[] },
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = wideWidth;
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
      render(<App rows={setup.rows} epics={setup.epics} designs={setup.designs} now={now} clients={clients} />, container);
    });
    await check(container);
  } finally {
    await teardown();
  }
}

// press sends a key to the document, as the owner's key does.
async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// recordScrolls replaces scrollIntoView on the test window's elements with one that records the element it is called on:
// happy-dom has no layout, so its own does nothing. The record holds the elements in the order they were scrolled.
function recordScrolls(t: TestContext): Element[] {
  const scrolled: Element[] = [];
  t.mock.method(HTMLElement.prototype, "scrollIntoView", function (this: Element) {
    scrolled.push(this);
  });
  return scrolled;
}

test("WorkLink: pressing an epic link opens its epic panel", (t) =>
  withApp(
    t.signal,
    {
      rows: [row()],
      epics: viewOf([makeEpic("rail"), makeEpic("picker")]),
      designs: [makeDesign("d1", "Round 8 links", [heldBy("rail")])],
    },
    (container) => {
      assertNone(container.querySelector('aside.rpanel[aria-label="epic"]'), "no epic panel before the press");
      act(() => {
        one(container, '.wlink[title="Open picker"]').click();
      });
      assert.ok(container.querySelector('aside.rpanel[aria-label="epic"]') !== null, "pressing the epic's link opens its panel");
    },
  ));

test("WorkLink: pressing a design link brings its tab into the inbox", (t) =>
  withApp(
    t.signal,
    {
      rows: [row()],
      epics: viewOf([makeEpic("rail"), makeEpic("picker")]),
      designs: [makeDesign("d1", "Round 8 links", [heldBy("rail")])],
    },
    async (container) => {
      const scrolled = recordScrolls(t);
      await press("b");
      assertSame(container.querySelector(".panes")?.getAttribute("data-inbox"), "collapsed", "the inbox is collapsed");
      act(() => {
        one(container, '.wlink[title="Open Round 8 links"]').click();
      });
      assertSame(container.querySelector(".panes")?.getAttribute("data-inbox"), "expanded", "the press expands the inbox");
      const tab = one(container, '.design-tab[data-key="design:d1"]');
      assert.deepEqual(scrolled, [tab], "the design's tab is scrolled into view");
    },
  ));

const phoneWidth = 412;
const deskWidth = 1600;
const sessionAddress: Address = { view: "inbox", entry: { kind: "session", id: session } };

// Stream is a watch stream the App opened on the fake transport, and controller feeds it while it is open.
interface Stream {
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// streamingFetch answers the app's unary calls with an empty body, and holds each watch stream open, for the check to feed.
function streamingFetch(streams: Stream[]): (input: RequestInfo | URL, init?: RequestInit) => Promise<Response> {
  return async (input, init) => {
    const request = new Request(input, init);
    if (!new URL(request.url).pathname.endsWith("/WatchConversation")) {
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
    }
    const stream: Stream = { controller: null };
    streams.push(stream);
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        stream.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };
}

// emit feeds one message, framed as Connect's JSON streaming frames it, to a stream the App still holds open.
function emit(stream: Stream, message: object): void {
  assert.ok(stream.controller !== null, "the stream is open");
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = 0;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  stream.controller.enqueue(bytes);
}

// runningState is the session's conversation as its stream reports it: a running agent and a running shell.
function runningState(): object {
  return {
    transcript: "s1.jsonl",
    seq: "1",
    reset: true,
    items: [],
    state: {
      background: [
        {
          id: "agent-1",
          type: "agent",
          description: "Find the caller",
          subagentType: "Explore",
          activity: "Grep keeperFor",
          status: "running",
          started: String(now - 125),
        },
        { id: "shell-1", type: "shell", description: "just dev", status: "running", started: "0" },
      ],
    },
  };
}

// sessionRow is the headless session s1, the row the page shows for the conversation the work links name.
function sessionRow(): Row {
  return {
    key: `session:${session}`,
    id: session,
    archiveId: session,
    kind: "session",
    archiveKind: "session",
    label: "notes",
    project: "/work/api",
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
    putOff: false,
  };
}

// withStreamedApp mounts App over the rows and epics at the width given, in a fresh DOM, and runs check with the container
// and the streams it opened. App is unmounted and the DOM removed when the test ends, or as soon as signal aborts first.
async function withStreamedApp(
  signal: AbortSignal,
  setup: { width: number; rows: Row[]; epics: EpicsView; address?: Address },
  check: (container: HTMLElement, streams: Stream[]) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = setup.width;
  const container = document.createElement("div");
  document.body.append(container);
  const streams: Stream[] = [];
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: streamingFetch(streams) }));
  // The page's dwell clock never fires, and its window reports no input, so no seen write is made.
  const seenClock: SeenClock = { now: () => now, setTimeout: () => null, clearTimeout: () => {} };
  const seenWindow: SeenWindow = {
    document: {
      visibilityState: "visible",
      hasFocus: () => true,
      addEventListener: () => {},
      removeEventListener: () => {},
    },
    addEventListener: () => {},
    removeEventListener: () => {},
  };
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    for (const stream of streams) {
      if (stream.controller !== null) {
        stream.controller.close();
        stream.controller = null;
      }
    }
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(
        <App
          rows={setup.rows}
          epics={setup.epics}
          address={setup.address}
          now={now}
          clients={clients}
          seenClock={seenClock}
          seenWindow={seenWindow}
        />,
        container,
      );
    });
    await check(container, streams);
  } finally {
    await teardown();
  }
}

// untilDrawn waits until check holds, one turn of the event loop at a time, so the wait arms no timer or clock.
function untilDrawn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// feedOpen sends the running state to the stream the App holds open last, once one is open.
async function feedOpen(signal: AbortSignal, streams: Stream[]): Promise<void> {
  await untilDrawn(signal, () => streams.some((stream) => stream.controller !== null), "the session's stream");
  const open = streams.filter((stream) => stream.controller !== null).at(-1);
  assert.ok(open !== undefined, "a stream is open");
  emit(open, runningState());
}

test("WorkLink: at the desk no action bar stands beside the chat, the work link is in the title, and the activity lines sit above the composer", (t) =>
  withStreamedApp(t.signal, { width: deskWidth, rows: [sessionRow()], epics: viewOf([makeEpic("rail")]) }, async (container, streams) => {
    await feedOpen(t.signal, streams);
    await untilDrawn(t.signal, () => container.querySelector('[aria-label="running now"]') !== null, "the activity lines");

    assertNone(container.querySelector(".rail.end"), "no rail stands beside the chat");
    const beside = all(container, '[aria-label="what this conversation steers"]').filter((group) => group.closest(".list") === null);
    assert.deepEqual(beside, [], "nothing labelled as what the conversation steers stands beside the chat");
    assert.ok(container.querySelector(".title-row .wlink") !== null, "the work link is in the title row");
    const group = one(container, '[aria-label="running now"]');
    const composer = one(container, '[aria-label="reader"] .send > .reply:not([hidden])');
    assertSame(group.nextElementSibling, composer, "the activity lines sit immediately above the composer");
  }));

test("WorkLink: under 760px no tile strip sits under the band, while the work link and the activity lines are drawn", (t) =>
  withStreamedApp(t.signal, { width: phoneWidth, rows: [sessionRow()], epics: viewOf([makeEpic("rail")]), address: sessionAddress }, async (container, streams) => {
    await feedOpen(t.signal, streams);
    await untilDrawn(t.signal, () => container.querySelector('[aria-label="running now"]') !== null, "the activity lines");

    assertNone(container.querySelector(".tilestrip"), "no tile strip is drawn under the band");
    assert.ok(container.querySelector(".title-row .wlink") !== null, "the work link is in the title row");
    assert.equal(all(container, '[aria-label="running now"] .activity-line').length, 2, "both running tasks are lines");
  }));
