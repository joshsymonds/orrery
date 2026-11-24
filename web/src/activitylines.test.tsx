import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { ActivityLines } from "./activitylines";
import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type BackgroundTask, BackgroundTaskSchema } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";
import type { SeenClock, SeenWindow } from "./seen";
import { assertNone, assertSame, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";
import { activityOf, type WorkConversation } from "./work";

// Each component test renders ActivityLines on its own, into a container on the document, so a press bubbles to the
// document as it does in the page. The clock is a fixed now in unix seconds, so the times the lines draw are the ones
// the fixture makes. The App tests mount the page over a fake transport, and check the lines and the panels it draws.

const now = 1_700_000_000;

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
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

// textsOf returns the text of each element under root that matches selector.
function textsOf(root: ParentNode, selector: string): string[] {
  return all(root, selector).map((element) => element.textContent ?? "");
}

// runningTask is a running background task of the type given, started ago seconds before now, with what init sets.
function runningTask(
  id: string,
  type: string,
  description: string,
  ago: number,
  init: MessageInitShape<typeof BackgroundTaskSchema> = {},
): BackgroundTask {
  return create(BackgroundTaskSchema, {
    id,
    description,
    type,
    status: "running",
    started: BigInt(now - ago),
    ...init,
  });
}

// conversationOf is a conversation of the session s1, not a pane, that runs the background tasks given.
function conversationOf(background: BackgroundTask[]): WorkConversation {
  return { session: "s1", pane: false, background };
}

test("ActivityLines: nothing running draws no lines", () =>
  withDom((container) => {
    const lines = activityOf(conversationOf([]));
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    assert.equal(container.innerHTML, "");
    assertNone(container.querySelector('[aria-label="running now"]'));
  }));

test("ActivityLines: one agent's line shows who, what, what it is doing and its time", () =>
  withDom((container) => {
    const task = runningTask("t1", "agent", "Find every caller of keeperFor", 125, {
      subagentType: "Explore",
      activity: "Grep keeperFor\\(",
    });
    const lines = activityOf(conversationOf([task]));
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    const group = one(container, '[aria-label="running now"]');
    const line = one(group, ".activity-line");
    assert.equal(one(line, ".who").textContent, "Explore");
    assert.equal(one(line, ".what").textContent, "Find every caller of keeperFor");
    assert.equal(one(line, ".doing").textContent, "Grep keeperFor\\(");
    assert.equal(one(line, ".clock").textContent, "2:05");
  }));

test("ActivityLines: agents come before shells, each kind in the order it started", () =>
  withDom((container) => {
    const lines = activityOf(
      conversationOf([
        runningTask("shell", "shell", "just dev", 900),
        runningTask("late", "agent", "Review the change", 30, { subagentType: "reviewer" }),
        runningTask("early", "agent", "Find the caller", 90, { subagentType: "Explore" }),
      ]),
    );
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    assert.deepEqual(textsOf(container, ".who"), ["Explore", "reviewer", "shell"]);
    assert.deepEqual(textsOf(container, ".what"), ["Find the caller", "Review the change", "just dev"]);
  }));

test("ActivityLines: four running fold to three lines and 'and 1 more running'", () =>
  withDom((container) => {
    const lines = activityOf(
      conversationOf([
        runningTask("a1", "agent", "Find the caller", 90, { subagentType: "Explore" }),
        runningTask("a2", "agent", "Review the change", 48, { subagentType: "reviewer" }),
        runningTask("s1", "shell", "just dev", 2460),
        runningTask("s2", "shell", "sleep 1800 && just warm", 720),
      ]),
    );
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    assert.deepEqual(textsOf(container, ".activity-line:not(.more) .what"), [
      "Find the caller",
      "Review the change",
      "just dev",
    ]);
    const more = one(container, ".activity-line.more");
    assert.equal(more.textContent, "and 1 more running");
    assert.equal(container.textContent?.includes("sleep 1800 && just warm"), false);
  }));

test("ActivityLines: the more line opens agents when a folded line is an agent, else running", () => {
  const opened: string[] = [];
  return withDom((container) => {
    const agents = activityOf(
      conversationOf([1, 2, 3, 4].map((index) => runningTask(`a${index}`, "agent", `Agent ${index}`, index * 10))),
    );
    draw(container, <ActivityLines lines={agents} now={now} open={null} onOpen={(kind) => opened.push(kind)} />);
    act(() => one(container, ".activity-line.more").click());

    const shells = activityOf(
      conversationOf([1, 2, 3, 4].map((index) => runningTask(`s${index}`, "shell", `Shell ${index}`, index * 10))),
    );
    draw(container, <ActivityLines lines={shells} now={now} open={null} onOpen={(kind) => opened.push(kind)} />);
    act(() => one(container, ".activity-line.more").click());

    assert.deepEqual(opened, ["agents", "running"]);
  });
});

test("ActivityLines: an ended task has no line", () =>
  withDom((container) => {
    const lines = activityOf(
      conversationOf([
        runningTask("done", "agent", "Finished scan", 300, {
          subagentType: "Explore",
          status: "done",
          ended: BigInt(now - 200),
        }),
        runningTask("failed", "shell", "Dead server", 300, { status: "failed", ended: BigInt(now - 100) }),
        runningTask("live", "agent", "Still reviewing", 40, { subagentType: "reviewer" }),
      ]),
    );
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    assert.deepEqual(textsOf(container, ".activity-line .who"), ["reviewer"]);
    assert.deepEqual(textsOf(container, ".activity-line .what"), ["Still reviewing"]);
    assert.equal(container.textContent?.includes("Finished scan"), false);
    assert.equal(container.textContent?.includes("Dead server"), false);
  }));

test("ActivityLines: the line of the open panel's kind is lit, and no other", () =>
  withDom((container) => {
    const lines = activityOf(
      conversationOf([
        runningTask("agent", "agent", "Find the caller", 90, { subagentType: "Explore" }),
        runningTask("shell", "shell", "just dev", 2460),
      ]),
    );
    draw(container, <ActivityLines lines={lines} now={now} open="running" onOpen={() => undefined} />);

    const [agentLine, shellLine] = all(container, ".activity-line");
    assert.equal(agentLine.classList.contains("on"), false);
    assert.equal(shellLine.classList.contains("on"), true);
  }));

// untilTurn waits for check to hold, one turn of the event loop at a time, so the wait arms no timer or clock.
function untilTurn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// Stream is a stream the App opened on the fake transport, and controller feeds it while it is open.
interface Stream {
  path: string;
  signal: AbortSignal;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// fakeTransport answers every unary call with an empty message, and holds each watch stream open for the test to feed.
function fakeTransport(): { streams: Stream[]; fetch: typeof globalThis.fetch } {
  const streams: Stream[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (!path.endsWith("/WatchConversation")) {
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
    }
    // A call's signal is the one the transport passed: a Request's own signal stops following it once the garbage
    // collector takes the Request.
    const signal = init?.signal ?? request.signal;
    const stream: Stream = { path, signal, controller: null };
    streams.push(stream);
    signal.addEventListener("abort", () => {
      stream.controller = null;
    });
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        stream.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };
  return { streams, fetch };
}

// envelope is one message on the Connect stream: a flag byte, a 4-byte length and the JSON payload.
function envelope(flag: number, message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = flag;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// emit feeds one message to a stream the App still holds open.
function emit(stream: Stream, message: object): void {
  assert.ok(stream.controller !== null, `the stream to ${stream.path} is open`);
  stream.controller.enqueue(envelope(0, message));
}

// sessionRow is the cursor's row: a headless session, which the page watches for its conversation.
function sessionRow(): Row {
  return {
    key: "s1",
    id: "s1",
    archiveId: "s1",
    kind: "session",
    archiveKind: "session",
    label: "notes",
    project: "",
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

// runningState is the session's conversation as its stream reports it: an agent running for 125 seconds and a shell
// running since the start of the clock.
function runningState(): object {
  return {
    transcript: "t1",
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

// lineWho is the activity line of the who given, which must be drawn above the composer.
function lineWho(container: HTMLElement, who: string): HTMLElement {
  const line = all(container, '[aria-label="running now"] .activity-line').find(
    (element) => element.querySelector(".who")?.textContent === who,
  );
  assert.ok(line !== undefined, `no activity line of ${who}`);
  return line;
}

// withApp mounts the page with the rows given over a fake transport, and runs check on the container and the streams
// it opened. The page is unmounted and its DOM removed when the test ends, or as soon as the test's signal aborts.
async function withApp(
  signal: AbortSignal,
  rows: Row[],
  check: (container: HTMLElement, streams: Stream[]) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = 1024;
  const container = document.createElement("div");
  document.body.append(container);
  const transport = fakeTransport();
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: transport.fetch }));
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
    for (const stream of transport.streams) {
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
        <App rows={rows} now={now} clients={clients} seenClock={seenClock} seenWindow={seenWindow} />,
        container,
      );
    });
    await check(container, transport.streams);
  } finally {
    await teardown();
  }
}

test("ActivityLines: the App draws the running lines directly above the cursor's composer", (t) =>
  withApp(t.signal, [sessionRow()], async (container, streams) => {
    await untilTurn(t.signal, () => streams.length === 1, "the session stream");
    emit(streams[0], runningState());
    await untilTurn(t.signal, () => container.querySelector('[aria-label="running now"]') !== null, "the lines");

    const group = one(container, '[aria-label="running now"]');
    const composer = one(container, '[aria-label="reader"] .send > .reply:not([hidden])');
    assertSame(group.nextElementSibling, composer, "the lines sit immediately above the composer");
    assertSame(group.parentElement, composer.parentElement, "the lines and the composer share one send box");
    assert.deepEqual(textsOf(group, ".who"), ["Explore", "shell"]);
  }));

test("ActivityLines: pressing a line in the App opens its list panel beside the chat, and lights the line", (t) =>
  withApp(t.signal, [sessionRow()], async (container, streams) => {
    await untilTurn(t.signal, () => streams.length === 1, "the session stream");
    emit(streams[0], runningState());
    await untilTurn(t.signal, () => container.querySelector('[aria-label="running now"]') !== null, "the lines");
    assertNone(container.querySelector('aside[aria-label="Sub-agents"]'), "no panel is open before a press");

    act(() => lineWho(container, "Explore").click());
    const agents = one(container, 'aside[aria-label="Sub-agents"]');
    assert.equal(one(agents, ".title").textContent, "Find the caller");
    assert.equal(lineWho(container, "Explore").classList.contains("on"), true, "the agent's line is lit");
    assert.equal(lineWho(container, "shell").classList.contains("on"), false);

    act(() => lineWho(container, "shell").click());
    const running = one(container, 'aside[aria-label="Left running"]');
    assert.equal(one(running, ".title").textContent, "just dev");
    assertNone(container.querySelector('aside[aria-label="Sub-agents"]'), "the shell's panel replaces the agents'");
    assert.equal(lineWho(container, "shell").classList.contains("on"), true, "the shell's line is lit");
    assert.equal(lineWho(container, "Explore").classList.contains("on"), false);

    act(() => lineWho(container, "shell").click());
    assertNone(container.querySelector('aside[aria-label="Left running"]'), "a press on the lit line shuts its panel");
    assert.equal(lineWho(container, "shell").classList.contains("on"), false);
  }));
