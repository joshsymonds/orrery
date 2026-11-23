import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { epicsView } from "./epics";
import type { Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

// InboxWebPlace: the web inbox places each conversation from serve's fields on its row. A row serve reports ended draws
// behind its project's ended line, a row serve reports archived draws only in the Archived view, and any other draws in
// its project's tree. Each test mounts App over invented rows in a fresh DOM and reads where each row is drawn.

const now = 1000;
const dir = "/home/owner/code/widgets";
const inboxAddress: Address = { view: "inbox", entry: null };
const archivedAddress: Address = { view: "archived", entry: null };

// row is a pane row of the widgets directory on the box, in the state done and neither ended nor archived, unless the init
// says otherwise.
function row(init: Partial<Row> & Pick<Row, "key" | "label">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    kind: "pane",
    project: dir,
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

// fakeFetch answers each unary call with an empty message, and holds each conversation stream open with no frame.
async function fakeFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  const path = new URL(request.url).pathname;
  await request.text();
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

interface Setup {
  rows: Row[];
  address: Address;
}

// draw renders App over the setup into container, with the clients given, and lets Preact draw what it changed.
async function draw(container: HTMLElement, clients: ReturnType<typeof createClients>, setup: Setup): Promise<void> {
  await act(() => {
    render(
      <App
        rows={setup.rows}
        now={now}
        clients={clients}
        epics={epicsView([], new Map())}
        address={setup.address}
        host={null}
        onAddress={() => {}}
      />,
      container,
    );
  });
}

// withApp mounts App over the setup at a wide window in a fresh DOM and runs check with the container and a rerender that
// draws App again at the address given. App is unmounted and the DOM removed when the test ends, or as soon as signal aborts.
async function withApp(
  signal: AbortSignal,
  setup: Setup,
  check: (container: HTMLElement, rerender: (address: Address) => Promise<void>) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = 1600;
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
    await check(container, (address) => draw(container, clients, { ...setup, address }));
  } finally {
    await teardown();
  }
}

// cardNamed is the project card of the project named name, which the inbox must draw.
function cardNamed(container: HTMLElement, name: string): HTMLElement {
  const card = container.querySelector<HTMLElement>(`section.dir[aria-label="${name}"]`);
  assert.ok(card !== null, `the inbox draws no card of ${name}`);
  return card;
}

// placed is the keys of the rows a project card draws in its tree, before its ended line's button, and behind that line,
// after it, which draw only while the line is open.
function placed(card: HTMLElement): { tree: string[]; behind: string[] } {
  const kids = [...card.querySelectorAll<HTMLElement>(".dir-kids > *")];
  const line = kids.findIndex((kid) => kid.classList.contains("fold-line"));
  const keysOf = (among: HTMLElement[]): string[] =>
    among.filter((kid) => kid.hasAttribute("data-row")).map((kid) => kid.dataset.key ?? "");
  if (line === -1) {
    return { tree: keysOf(kids), behind: [] };
  }
  return { tree: keysOf(kids.slice(0, line)), behind: keysOf(kids.slice(line + 1)) };
}

// rowKeysIn is the keys of every row the inbox list draws, in the tree or behind an ended line, in the view shown.
function rowKeysIn(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLElement>(".inbox-rows [data-row]")].map((item) => item.dataset.key ?? "");
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

const live = row({ key: "pane:box:%1", label: "Tide table" });

test("InboxWebPlace: a row serve reports neither ended nor archived is drawn in its project's tree, with no ended line", (t) =>
  withApp(t.signal, { rows: [live], address: inboxAddress }, (container) => {
    const card = cardNamed(container, "widgets");
    assert.deepEqual(placed(card), { tree: [live.key], behind: [] }, "the live row is not in the project's tree");
    assert.equal(card.querySelector(".fold-line"), null, "a project with no ended row draws an ended line");
  }));

// stopped is a headless session of the gadgets directory, which serve reports neither ended nor archived though its state
// is stopped: its state does not place it behind the ended line.
const gadgetsDir = "/home/owner/code/gadgets";
const stopped = row({
  key: "session:sess-stop",
  kind: "session",
  archiveKind: "headless",
  archiveId: "sess-stop",
  label: "Stopped run",
  project: gadgetsDir,
  host: "",
  state: "stopped",
});

test("InboxWebPlace: a headless session serve reports neither ended nor archived is drawn in its project's tree, though its state is stopped", (t) =>
  withApp(t.signal, { rows: [stopped], address: inboxAddress }, (container) => {
    const card = cardNamed(container, "gadgets");
    assert.deepEqual(placed(card), { tree: [stopped.key], behind: [] }, "the stopped session is not in the project's tree");
    assert.equal(card.querySelector(".fold-line"), null, "a project with no ended row draws an ended line");
  }));

test("InboxWebPlace: a row serve reports ended is behind its project's ended line and not in the tree", (t) => {
  const ended = row({ key: "pane:box:%2", label: "Old thread", ended: true });
  return withApp(t.signal, { rows: [live, ended], address: inboxAddress }, async (container) => {
    assert.deepEqual(
      placed(cardNamed(container, "widgets")),
      { tree: [live.key], behind: [] },
      "the ended row is drawn in the tree or behind a line that is shut",
    );
    const line = cardNamed(container, "widgets").querySelector<HTMLElement>(".fold-line");
    assert.ok(line !== null, "the project draws no ended line");
    assert.equal(line.textContent?.trim(), "1 ended", "the ended line does not count the one ended row");
    await press(line);
    assert.deepEqual(
      placed(cardNamed(container, "widgets")),
      { tree: [live.key], behind: [ended.key] },
      "the ended row is not drawn behind the ended line once it opens",
    );
  });
});

test("InboxWebPlace: a row serve reports archived is drawn only in the Archived view, and in no inbox place", (t) => {
  const archived = row({ key: "pane:box:%3", label: "Old chat", archived: true, ended: true });
  return withApp(t.signal, { rows: [live, archived], address: inboxAddress }, async (container, rerender) => {
    assert.deepEqual(rowKeysIn(container), [live.key], "the inbox draws the archived row in a place");
    assert.equal(container.querySelector(".fold-line"), null, "an archived row gives its project an ended line");
    await rerender(archivedAddress);
    assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "archived", "the Archived view is not shown");
    assert.deepEqual(rowKeysIn(container), [archived.key], "the Archived view does not draw the archived row");
  });
});
