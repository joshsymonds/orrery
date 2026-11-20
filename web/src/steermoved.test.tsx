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

// Each test's name carries the token SteerMoved: a move or a continue that answered, and the epics list that names the
// steerer. App mounts at the desk width, where the steering command is an icon in the epic panel's title row.

const now = 1000;
const repository = "widgets-5d0c1a7";
const deskWidth = 1280;

const movePath = "/orrery.v1.Epics/Move";
const continuePath = "/orrery.v1.Epics/Continue";
const getPath = "/orrery.v1.Epics/Get";
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";

type Clients = ReturnType<typeof createClients>;

// Probe records the calls the page makes, the session each conversation stream follows, and the replies queued per unary
// path. watchers run after each call or stream, so a wait ends on the signal that makes its check hold.
interface Probe {
  calls: { path: string; body: Record<string, unknown> }[];
  followed: string[];
  replies: Map<string, object[]>;
  watchers: Set<() => void>;
}

// Mount is what a check gets: the container App draws into, the probe, and rerender, which draws App again over the setup.
interface Mount {
  container: HTMLElement;
  probe: Probe;
  rerender: (setup: Setup) => Promise<void>;
}

interface Setup {
  rows: Row[];
  epics: EpicsView;
  address?: Address;
}

// notify runs the waits whose checks may hold now.
function notify(probe: Probe): void {
  for (const watcher of [...probe.watchers]) {
    watcher();
  }
}

// fakeFetch records each unary call with its body, answers it with the next reply queued for its path or an empty message,
// and holds each conversation stream open. A stream's request is one connect envelope, a flag byte and a length, then its
// JSON, which names the session it follows.
async function fakeFetch(probe: Probe, input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  const path = new URL(request.url).pathname;
  const text = await request.text();
  if (path.endsWith("/WatchConversation")) {
    const body = JSON.parse(text.slice(5)) as { id?: string; pane?: string };
    probe.followed.push(body.id ?? body.pane ?? "");
    notify(probe);
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  probe.calls.push({ path, body: text === "" ? {} : (JSON.parse(text) as Record<string, unknown>) });
  notify(probe);
  const reply = probe.replies.get(path)?.shift() ?? {};
  return new Response(JSON.stringify(reply), { status: 200, headers: { "content-type": "application/json" } });
}

// waitFor resolves once check holds, checked as the page makes each call and follows each stream. When the test's signal
// aborts first, it rejects, naming what never happened, so a wait never outlives its test.
function waitFor(probe: Probe, signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const abandon = () => {
      probe.watchers.delete(watch);
      reject(new Error(`${what} never happened: the test ended first`, { cause: signal.reason }));
    };
    const watch = () => {
      if (check()) {
        probe.watchers.delete(watch);
        signal.removeEventListener("abort", abandon);
        resolve();
      }
    };
    if (check()) {
      resolve();
      return;
    }
    probe.watchers.add(watch);
    signal.addEventListener("abort", abandon, { once: true });
  });
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    id: init.key,
    kind: "pane",
    label: init.key,
    project: repository,
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

// sessionRow is a headless conversation in the epic's repository, by its session id.
function sessionRow(id: string, label: string): Row {
  return row({ key: `session:${id}`, id, kind: "session", label, archiveId: id, archiveKind: "headless" });
}

// epicOf is a live orreryd epic in the repository, steered by the session steering.
function epicOf(steering: string): Epic {
  return create(EpicSchema, {
    id: `${repository}/first`,
    repository,
    slug: "first",
    outcome: "",
    live: true,
    source: "orreryd",
    steering,
  });
}

// viewOf is the epics view of the epic, whose detail is its Get answer.
function viewOf(epic: Epic): EpicsView {
  const detail = create(EpicsGetResponseSchema, { epic, repository });
  return epicsView([epic], new Map([[epic.id, detail]]));
}

// epicAddress is the address of the epic's entry, with the cursor on it.
function epicAddress(): Address {
  return { order: "attention", entry: { kind: "epic", id: `${repository}/first`, page: { kind: "epic" } } };
}

// sessionAddress is the address of a session's row, with the cursor on it.
function sessionAddress(id: string): Address {
  return { order: "attention", entry: { kind: "session", id } };
}

// draw renders App over the setup into container.
function draw(container: HTMLElement, clients: Clients, probe: Probe, setup: Setup): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows}
        error=""
        now={now}
        clients={clients}
        epics={setup.epics}
        address={setup.address}
        host={null}
        onAddress={() => undefined}
      />,
      container,
    );
  });
}

// withSteer mounts App at the desk width in a fresh DOM and runs check with the mount. App is unmounted and the DOM removed
// when the test ends, or as soon as signal aborts first.
async function withSteer(signal: AbortSignal, setup: Setup, check: (mount: Mount) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  window.innerWidth = deskWidth;
  const container = document.createElement("div");
  document.body.append(container);
  const probe: Probe = { calls: [], followed: [], replies: new Map(), watchers: new Set() };
  const clients = createClients(
    createTransport({ baseUrl: "https://orrery.test", fetch: (input, init) => fakeFetch(probe, input, init) }),
  );
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await draw(container, clients, probe, setup);
    await check({ container, probe, rerender: (next) => draw(container, clients, probe, next) });
  } finally {
    await teardown();
  }
}

// cursorKey is the key of the row or entry the cursor rests on, or null when none does.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".b-row.cur")?.getAttribute("data-key") ?? null;
}

// panelHolds is whether the epic panel is drawn and names the slug in its title row.
function panelHolds(container: HTMLElement, slug: string): boolean {
  const panel = container.querySelector<HTMLElement>('aside.rpanel[aria-label="epic"]');
  return panel?.textContent?.includes(slug) === true;
}

// railTiles is each tile the right rail draws, in order.
function railTiles(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".rail .tile-w")];
}

// railLists is whether the rail draws an epic tile whose short name is short.
function railLists(container: HTMLElement, short: string): boolean {
  return railTiles(container).some((tile) => tile.querySelector(".cap")?.textContent === short);
}

// epicGetAnswer is the Get answer that names the session as the epic's steerer.
function epicGetAnswer(steering: string): object {
  return { epic: { id: `${repository}/first`, repository, slug: "first", steering, live: true, source: "orreryd" }, repository };
}

// callsTo is the calls the page made to the path, in order.
function callsTo(probe: Probe, path: string): { path: string; body: Record<string, unknown> }[] {
  return probe.calls.filter((call) => call.path === path);
}

test("SteerMoved: a move answered and read back as the taker puts the taker's conversation in the middle, with the cursor on the epic and its panel open", (t) => {
  const oldSteerer = sessionRow("sess-old", "old steerer");
  const taker = sessionRow("sess-new", "next steerer");
  return withSteer(
    t.signal,
    { rows: [oldSteerer, taker], epics: viewOf(epicOf("sess-old")), address: epicAddress() },
    async ({ container, probe }) => {
      assert.equal(cursorKey(container), `epic:${repository}/first`, "the cursor rests on the epic");
      probe.replies.set(getPath, [epicGetAnswer("sess-new")]);
      await act(() => {
        container.querySelector<HTMLButtonElement>(".epic-page .title-row .icon.steer")?.dispatchEvent(new Event("click"));
      });
      await act(() => {
        container.querySelector<HTMLButtonElement>('.steer-menu [data-session="sess-new"]')?.dispatchEvent(new Event("click"));
      });
      await waitFor(probe, t.signal, () => probe.followed.includes("sess-new"), "the taker's stream");
      assert.deepEqual(callsTo(probe, movePath).map((call) => call.body), [{ id: `${repository}/first`, session: "sess-new" }], "one move, to the chosen session");
      assert.equal(callsTo(probe, getPath).length, 1, "the epic is read once after the move");
      assert.equal(container.querySelector(".steer-menu"), null, "the menu closes once it steers");
      assert.equal(cursorKey(container), `epic:${repository}/first`, "the cursor stays on the epic");
      assert.equal(probe.followed.at(-1), "sess-new", "the middle is the taker's conversation, and its stream is the one that follows");
      assert.ok(panelHolds(container, "first"), "the epic panel stays open");
    },
  );
});

test("SteerMoved: a continue answered empty and read back as a new session puts that session's conversation in the middle, with the cursor on the epic and its panel open", (t) => {
  const oldSteerer = sessionRow("sess-old", "old steerer");
  const fresh = sessionRow("sess-fresh", "fresh steerer");
  return withSteer(
    t.signal,
    { rows: [oldSteerer], epics: viewOf(epicOf("sess-old")), address: epicAddress() },
    async ({ container, probe, rerender }) => {
      probe.replies.set(continuePath, [{ session: "" }]);
      probe.replies.set(getPath, [epicGetAnswer("sess-fresh")]);
      await act(() => {
        container.querySelector<HTMLButtonElement>(".epic-page .title-row .icon.steer")?.dispatchEvent(new Event("click"));
      });
      await act(() => {
        container.querySelector<HTMLButtonElement>(".steer-menu .steer-row.new")?.dispatchEvent(new Event("click"));
      });
      await waitFor(probe, t.signal, () => callsTo(probe, continuePath).length === 1, "the continue call");
      await rerender({ rows: [oldSteerer, fresh], epics: viewOf(epicOf("sess-old")), address: epicAddress() });
      await waitFor(probe, t.signal, () => probe.followed.includes("sess-fresh"), "the new session's stream");
      assert.deepEqual(callsTo(probe, continuePath).map((call) => call.body), [{ id: `${repository}/first` }], "one continue for the epic");
      assert.equal(callsTo(probe, movePath).length, 0, "a continue sends no move");
      assert.equal(callsTo(probe, getPath).length, 1, "the epic is read once after the continue");
      assert.equal(probe.followed.at(-1), "sess-fresh", "the middle is the new session's conversation, and its stream is the one that follows");
      assert.equal(cursorKey(container), `epic:${repository}/first`, "the cursor stays on the epic");
      assert.ok(panelHolds(container, "first"), "the epic panel stays open");
    },
  );
});

test("SteerMoved: the conversation an epic left lists the epic until a rerender's epics list names the taker as steerer, then it does not", (t) => {
  const oldSteerer = sessionRow("sess-old", "old steerer");
  const taker = sessionRow("sess-new", "next steerer");
  return withSteer(
    t.signal,
    { rows: [oldSteerer, taker], epics: viewOf(epicOf("sess-old")), address: sessionAddress("sess-old") },
    async ({ container, rerender }) => {
      assert.equal(cursorKey(container), "session:sess-old", "the cursor rests on the conversation the epic left");
      assert.ok(railLists(container, "first"), "the rail lists the epic while the list names the old steerer");
      await rerender({ rows: [oldSteerer, taker], epics: viewOf(epicOf("sess-new")), address: sessionAddress("sess-old") });
      assert.equal(cursorKey(container), "session:sess-old", "the cursor stays on the conversation the epic left");
      assert.equal(railLists(container, "first"), false, "the rail stops listing the epic once the list names the taker");
    },
  );
});
