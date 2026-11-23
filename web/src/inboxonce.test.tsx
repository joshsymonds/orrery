import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the prefix InboxWebOnce: a conversation is drawn once in the inbox and in the Archived view,
// and the ended epics of a conversation in the tree fold under its own row. The App is drawn over invented rows and
// epics, in a window wide enough for the inbox to draw its rows and its page beside them.

const now = 1000;
const repository = "widgets-5d0c1a7";
const dir = "/home/owner/code/widgets";

// The conversations of the fixture: one in the tree with two ended epics and one live, one behind the ended line with an
// ended epic of its own, and one archived with an archived epic of its own.
const treeKey = "session:sess-tree";
const behindKey = "session:sess-behind";
const archivedKey = "session:sess-old";
const conversationKeys = [treeKey, behindKey, archivedKey];

// fakeFetch answers every call with an empty message, and holds each conversation stream open.
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

// row is a headless session of the fixture's directory, in the given state, under the label given, with the fields init
// gives over it.
function row(sessionId: string, state: string, label: string, init: Partial<Row> = {}): Row {
  return {
    putOff: false,
    tool: "",
    id: sessionId,
    kind: "session",
    label,
    project: dir,
    host: "",
    state,
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "headless",
    archiveId: sessionId,
    key: `session:${sessionId}`,
    ...init,
  };
}

// epicOf is an epic of the repository, steered by the session given, with the fields init gives. Its id is the repository
// and its slug.
function epicOf(slug: string, steering: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    source: "orreryd",
    steering,
    ...init,
  });
}

// keyOf is the key an epic is drawn under: epic: and its id.
function keyOf(epic: Epic): string {
  return `epic:${epic.id}`;
}

// view is the epics view of the epics, each with a detail whose summary is its slug.
function view(epics: Epic[]): EpicsView {
  const details = epics.map((epic): [string, EpicsGetResponse] => [
    epic.id,
    create(EpicsGetResponseSchema, { epic, summary: `Ship ${epic.slug}` }),
  ]);
  return epicsView(epics, new Map(details));
}

// The fixture's epics: two ended under the tree conversation, the newer one first in its fold, and one live beside them;
// one ended behind the ended line; and one archived under the archived conversation.
const alpha = epicOf("alpha", "sess-tree", { changed: 50n });
const beta = epicOf("beta", "sess-tree", { live: false, changed: 10n });
const gamma = epicOf("gamma", "sess-tree", { live: false, changed: 30n });
const delta = epicOf("delta", "sess-behind", { live: false, changed: 20n });
const epsilon = epicOf("epsilon", "sess-old", { live: false, archived: true, changed: 40n });

// fixtureRows are the conversations of the fixture: the tree one, the one behind the ended line, and the archived one.
const fixtureRows: Row[] = [
  row("sess-tree", "done", "Plans the rota"),
  row("sess-behind", "stopped", "Checked the widgets", { ended: true }),
  row("sess-old", "done", "Old plan", { archived: true }),
];

// fixtureEpics are the epics of the fixture.
const fixtureEpics: Epic[] = [alpha, beta, gamma, delta, epsilon];

// inboxAddress is the inbox, and archivedAddress the Archived view.
const inboxAddress: Address = { view: "inbox", entry: null };
const archivedAddress: Address = { view: "archived", entry: null };

interface Setup {
  rows: Row[];
  epics: EpicsView;
  address: Address;
}

// draw renders App over the setup into container.
function draw(container: HTMLElement, setup: Setup): Promise<void> {
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch }));
  return act(() => {
    render(
      <App
        rows={setup.rows}
        now={now}
        clients={clients}
        epics={setup.epics}
        address={setup.address}
        host={null}
        onAddress={() => {}}
      />,
      container,
    );
  });
}

// withApp mounts App over the setup in a fresh DOM and runs check with the container it draws into. App is unmounted and
// the DOM removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  setup: Setup,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = 1600;
  const container = document.createElement("div");
  document.body.append(container);
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await draw(container, setup);
    await check(container);
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

// shutFold is the first fold the list draws shut: a project's ended line, or a conversation's fold of ended epics.
const shutFold =
  '.list section.dir > .dir-kids > button.fold-line[aria-expanded="false"], .list .steers-fold[aria-expanded="false"]';

// openEveryFold presses each shut fold until none is shut. Each press opens one fold, and a fold it reveals is pressed in
// turn; the bound is far above the folds the fixture holds.
async function openEveryFold(container: HTMLElement): Promise<void> {
  for (let presses = 0; presses < 8; presses++) {
    const next = container.querySelector<HTMLButtonElement>(shutFold);
    if (next === null) {
      return;
    }
    await press(next);
  }
  assert.equal(container.querySelector(shutFold), null, "a fold stays shut after it is pressed");
}

// listKeys is the data-key of each element the list draws, in order.
function listKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLElement>(".list [data-key]")].map((element) => element.getAttribute("data-key") ?? "");
}

// repeatedConversations is each conversation key the list draws more than once.
function repeatedConversations(container: HTMLElement): string[] {
  const keys = listKeys(container);
  return conversationKeys.filter((key) => keys.filter((drawn) => drawn === key).length > 1);
}

// behindLineKeys is the data-key of each element a project draws after its ended line, in order.
function behindLineKeys(container: HTMLElement): string[] {
  const line = container.querySelector<HTMLElement>(".list section.dir > .dir-kids > button.fold-line");
  assert.ok(line !== null, "the project draws no ended line");
  const keys: string[] = [];
  for (let element = line.nextElementSibling; element !== null; element = element.nextElementSibling) {
    for (const drawn of [element, ...element.querySelectorAll("[data-key]")]) {
      keys.push(drawn.getAttribute("data-key") ?? "");
    }
  }
  return keys;
}

// steersOf is the .steers drawn under the conversation row with key, which the list must draw.
function steersOf(container: HTMLElement, key: string): Element {
  const drawn = container.querySelector(`.list [data-key="${key}"]`);
  assert.ok(drawn !== null, `the list draws no ${key}`);
  const steers = drawn.nextElementSibling;
  assert.ok(steers !== null && steers.classList.contains("steers"), `${key} draws no .steers under its row`);
  return steers;
}

// foldOf is the fold line of the conversation row with key, which its .steers must draw.
function foldOf(container: HTMLElement, key: string): HTMLButtonElement {
  const fold = steersOf(container, key).querySelector<HTMLButtonElement>(":scope > .steers-fold");
  assert.ok(fold !== null, `${key} draws no fold line for its ended epics`);
  return fold;
}

test("InboxWebOnce: no conversation key is drawn twice in the inbox, with every fold open", (t) =>
  withApp(t.signal, { rows: fixtureRows, epics: view(fixtureEpics), address: inboxAddress }, async (container) => {
    await openEveryFold(container);
    assert.ok(listKeys(container).includes(treeKey), "the inbox draws no conversation in the tree");
    assert.deepEqual(repeatedConversations(container), [], "a conversation key is drawn twice in the inbox");
  }));

test("InboxWebOnce: no conversation key is drawn twice in the Archived view, with every fold open", (t) =>
  withApp(t.signal, { rows: fixtureRows, epics: view(fixtureEpics), address: archivedAddress }, async (container) => {
    await openEveryFold(container);
    assert.ok(listKeys(container).includes(archivedKey), "the Archived view draws no archived conversation");
    assert.deepEqual(repeatedConversations(container), [], "a conversation key is drawn twice in the Archived view");
  }));

test("InboxWebOnce: a conversation in the tree with ended epics draws them folded under its own row, with the count 2 ended", (t) =>
  withApp(t.signal, { rows: fixtureRows, epics: view(fixtureEpics), address: inboxAddress }, (container) => {
    const fold = foldOf(container, treeKey);
    assert.equal(fold.textContent, "2 ended", "the fold line does not read the two ended epics");
    assert.equal(fold.getAttribute("aria-expanded"), "false", "the fold is open before it is pressed");
    assert.ok(steersOf(container, treeKey).querySelector(`[data-key="${keyOf(alpha)}"]`) !== null, "the live epic is not under its row");
    assert.equal(container.querySelector(`[data-key="${keyOf(beta)}"]`), null, "a shut fold draws its ended epic beta");
    assert.equal(container.querySelector(`[data-key="${keyOf(gamma)}"]`), null, "a shut fold draws its ended epic gamma");
  }));

test("InboxWebOnce: a folded conversation's ended epics draw under its fold line, newest first, once the fold is open", (t) =>
  withApp(t.signal, { rows: fixtureRows, epics: view(fixtureEpics), address: inboxAddress }, async (container) => {
    await press(foldOf(container, treeKey));
    assert.equal(foldOf(container, treeKey).getAttribute("aria-expanded"), "true", "the fold is shut once it is pressed");
    const epics = [...steersOf(container, treeKey).querySelectorAll(":scope > .row.epic")].map((element) =>
      element.getAttribute("data-key"),
    );
    assert.deepEqual(epics, [keyOf(alpha), keyOf(gamma), keyOf(beta)], "the open fold does not draw its ended epics, newest first");
  }));

test("InboxWebOnce: nothing of a conversation in the tree with ended epics is drawn behind the ended line", (t) =>
  withApp(t.signal, { rows: fixtureRows, epics: view(fixtureEpics), address: inboxAddress }, async (container) => {
    const line = container.querySelector<HTMLButtonElement>(".list section.dir > .dir-kids > button.fold-line");
    assert.ok(line !== null, "the project draws no ended line");
    await press(line);
    const behind = behindLineKeys(container);
    assert.ok(behind.includes(behindKey), "the ended line does not draw the conversation behind it");
    assert.deepEqual(
      behind.filter((key) => key === treeKey || key === keyOf(beta) || key === keyOf(gamma)),
      [],
      "the ended line draws the tree conversation or its ended epics",
    );
  }));
