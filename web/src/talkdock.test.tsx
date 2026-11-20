import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { TalkDock } from "./talkdock";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

type Props = Parameters<typeof TalkDock>[0];
type DomWindow = ReturnType<typeof installTestDom>;

const resumePath = "/orrery.v1.Control/Resume";
const sayPath = "/orrery.v1.Control/Say";
const sessionTarget = { kind: "session" as const, id: "s1", label: "api" };

// listed is a row the dock's head titles from. Only the fields a title reads are set; the rest are empty.
function listed(init: Pick<Row, "kind" | "id" | "project" | "label">): Row {
  return {
    key: `${init.kind}:${init.id}`,
    host: "",
    state: "",
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

interface Stream {
  body: Record<string, unknown>;
  controller: ReadableStreamDefaultController<Uint8Array>;
}

// Gauge counts the events a fake server sees. A test waits for a count with reached, and the wait resolves on the event
// that reaches it, so the test waits on events, never on a clock.
class Gauge {
  private count = 0;
  private waits: { least: number; resolve: () => void }[] = [];

  bump(): void {
    this.count += 1;
    const ready = this.waits.filter((wait) => wait.least <= this.count);
    this.waits = this.waits.filter((wait) => wait.least > this.count);
    for (const wait of ready) {
      wait.resolve();
    }
  }

  reached(least: number): Promise<void> {
    if (this.count >= least) {
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      this.waits.push({ least, resolve });
    });
  }
}

// FakeServer answers the calls the dock makes through TransportOptions.fetch. Each Sessions.WatchConversation call is a
// stream the test feeds. Control.Resume answers with an empty reply, or with resumeError when the test sets one.
// Control.Say answers with an empty reply and keeps its body in says. Sessions.Older answers with one older item and no
// more.
class FakeServer {
  readonly streams: Stream[] = [];
  readonly resumes: Record<string, unknown>[] = [];
  readonly says: Record<string, unknown>[] = [];
  readonly olderBodies: Record<string, unknown>[] = [];
  // opened counts the conversation streams the client has opened; resumed counts the Resume calls it has received.
  readonly opened = new Gauge();
  readonly resumed = new Gauge();
  resumeError: { code: string; message: string } | null = null;

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const bytes = new Uint8Array(await request.arrayBuffer());
    const json = { "content-type": "application/json" };
    if (path === resumePath) {
      this.resumes.push(JSON.parse(new TextDecoder().decode(bytes)));
      this.resumed.bump();
      if (this.resumeError === null) {
        return new Response("{}", { status: 200, headers: json });
      }
      return new Response(JSON.stringify(this.resumeError), { status: 400, headers: json });
    }
    if (path === sayPath) {
      this.says.push(JSON.parse(new TextDecoder().decode(bytes)));
      return new Response("{}", { status: 200, headers: json });
    }
    if (path.endsWith("/Older")) {
      this.olderBodies.push(JSON.parse(new TextDecoder().decode(bytes)));
      return new Response(
        JSON.stringify({ items: [{ kind: "assistant", text: "older words" }], oldest: "4", more: false }),
        { status: 200, headers: json },
      );
    }
    const body = decodeRequest(bytes);
    const stream = new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.streams.push({ body, controller });
        this.opened.bump();
      },
    });
    return new Response(stream, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  // clients is the one set of clients every dock in a test uses, so a change of target is the only change the dock
  // sees.
  readonly clients: Props["clients"] = createClients(
    createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }),
  );

  emit(index: number, message: object): void {
    this.streams[index].controller.enqueue(envelope(0, message));
  }
}

function decodeRequest(bytes: Uint8Array): Record<string, unknown> {
  const length = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(1);
  return JSON.parse(new TextDecoder().decode(bytes.subarray(5, 5 + length)));
}

// envelope frames one Connect message: a flag byte, a 4-byte big-endian length, and the JSON payload.
function envelope(flag: number, message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = flag;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// shown resolves once check holds for the dock's DOM. The wait ends on a mutation the dock itself makes, so it resolves
// on the render the assertion reads, not on a clock.
function shown(window: DomWindow, container: HTMLElement, check: () => boolean): Promise<void> {
  return new Promise((resolve) => {
    const observer = new window.MutationObserver(() => {
      if (check()) {
        observer.disconnect();
        resolve();
      }
    });
    // happy-dom types its nodes apart from the DOM library the tests compile against; the container is the same object.
    const target = container as unknown as Parameters<typeof observer.observe>[0];
    observer.observe(target, { attributes: true, characterData: true, childList: true, subtree: true });
    if (check()) {
      observer.disconnect();
      resolve();
    }
  });
}

function baseProps(server: FakeServer, init: Partial<Props>): Props {
  return {
    clients: server.clients,
    rows: [],
    target: sessionTarget,
    onClose: () => {},
    ...init,
  };
}

async function withDock(
  server: FakeServer,
  init: Partial<Props>,
  check: (container: HTMLElement, window: DomWindow) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<TalkDock {...baseProps(server, init)} />, container);
    });
    await server.opened.reached(1);
    await check(container, window);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

async function click(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

function textOf(container: HTMLElement, selector: string): string {
  return container.querySelector(selector)?.textContent ?? "";
}

function hasButton(container: HTMLElement, name: string): boolean {
  return [...container.querySelectorAll("button")].some((button) => button.textContent === name);
}

function buttonNamed(container: HTMLElement, name: string): HTMLButtonElement {
  const found = [...container.querySelectorAll("button")].find((button) => button.textContent === name);
  if (found === undefined) {
    throw new Error(`the dock has a ${name} button`);
  }
  return found;
}

function closeButton(container: HTMLElement): HTMLButtonElement {
  const found = container.querySelector<HTMLButtonElement>('button[aria-label="close"]');
  if (found === null) {
    throw new Error("the dock has a close button");
  }
  return found;
}

// interruptedState is the state a headless session's stream carries once its turn is interrupted.
const interruptedState = { session: { state: "interrupted" } };

test("the dock names its conversation by label, and its close button calls onClose", { timeout: 2000 }, async () => {
  const server = new FakeServer();
  let closes = 0;
  await withDock(server, { onClose: () => { closes += 1; } }, async (container) => {
    assert.equal(container.querySelector('aside.talk-panel[aria-label="talk"]') !== null, true);
    assert.equal(textOf(container, "h1"), "api");
    assert.equal(container.querySelector('button.close[aria-label="close"]') !== null, true);

    await click(closeButton(container));
    assert.equal(closes, 1);
  });
});

test("an interrupted session shows Resume, and a press sends it with the target id", { timeout: 2000 }, async () => {
  const server = new FakeServer();
  await withDock(server, {}, async (container, window) => {
    server.emit(0, { transcript: "t1", seq: "5", reset: true, items: [], state: interruptedState });
    await shown(window, container, () => hasButton(container, "Resume"));

    await click(buttonNamed(container, "Resume"));
    await server.resumed.reached(1);
    assert.deepEqual(server.resumes, [{ id: "s1" }]);
    assert.equal(textOf(container, ".inote.error"), "");
  });
});

test("a working session shows no Resume", { timeout: 2000 }, async () => {
  const server = new FakeServer();
  await withDock(server, {}, async (container, window) => {
    server.emit(0, {
      transcript: "t1",
      seq: "5",
      reset: true,
      items: [{ kind: "assistant", text: "still going" }],
      state: { session: { state: "working" } },
    });
    await shown(window, container, () => textOf(container, ".reader").includes("still going"));
    assert.equal(hasButton(container, "Resume"), false);
  });
});

test("a refused Resume shows its message in the dock", { timeout: 2000 }, async () => {
  const server = new FakeServer();
  server.resumeError = { code: "failed_precondition", message: "session is not interrupted" };
  await withDock(server, {}, async (container, window) => {
    server.emit(0, { transcript: "t1", seq: "5", reset: true, items: [], state: interruptedState });
    await shown(window, container, () => hasButton(container, "Resume"));

    await click(buttonNamed(container, "Resume"));
    await shown(window, container, () => textOf(container, ".inote.error") !== "");
    assert.equal(textOf(container, ".inote.error"), "Not resumed: session is not interrupted");
  });
});

test("the dock's reader asks for the page before its oldest item, and draws the page above", { timeout: 2000 }, async (t) => {
  const server = new FakeServer();
  await withDock(server, {}, async (container) => {
    server.emit(0, {
      transcript: "t1",
      seq: "5",
      reset: true,
      oldest: "9",
      items: [{ kind: "assistant", text: "nine" }],
    });
    await rendered(t.signal, () => textOf(container, ".reader").includes("nine"), "the held item");

    await rendered(t.signal, () => server.olderBodies.length === 1, "the older request");
    assert.deepEqual(server.olderBodies, [{ id: "s1", before: "9" }]);
    await rendered(t.signal, () => textOf(container, ".reader").includes("older words"), "the older page");
    const reader = textOf(container, ".reader");
    assert.ok(reader.indexOf("older words") < reader.indexOf("nine"), "the older page is drawn above the held item");
  });
});

test("a new target restarts the conversation stream with its id", { timeout: 2000 }, async () => {
  const server = new FakeServer();
  await withDock(server, {}, async (container) => {
    await act(() => {
      render(<TalkDock {...baseProps(server, { target: { kind: "session", id: "s2", label: "web" } })} />, container);
    });
    await server.opened.reached(2);
    assert.equal(server.streams[1].body.id, "s2");
    assert.equal(textOf(container, "h1"), "web");
  });
});

test("TitleParts: the head draws the listed row's directory and headline, and shows no id", { timeout: 2000 }, async () => {
  const server = new FakeServer();
  const headline = listed({ kind: "session", id: "s1", project: "/work/api", label: "fix the retry queue" });
  await withDock(server, { rows: [headline] }, async (container) => {
    assert.equal(textOf(container, "h1 .title-dir"), "api");
    assert.equal(textOf(container, "h1 .title-line"), "fix the retry queue");
    assert.equal(textOf(container, "h1"), "api fix the retry queue", "the head is the directory and the headline, no id");
  });
});

test("TitleParts: the head follows a listed row whose line changes on rerender, and the dock names the same row", { timeout: 2000 }, async () => {
  const server = new FakeServer();
  const headline = (label: string): Row => listed({ kind: "session", id: "s1", project: "/work/api", label });
  await withDock(server, { rows: [headline("first headline")] }, async (container) => {
    assert.equal(textOf(container, "h1 .title-line"), "first headline");
    await act(() => {
      render(<TalkDock {...baseProps(server, { rows: [headline("second headline")] })} />, container);
    });
    assert.equal(textOf(container, "h1 .title-line"), "second headline", "the head shows the refreshed line");
    assert.equal(textOf(container, "h1"), "api second headline");
    assert.equal(container.querySelector("textarea")?.getAttribute("placeholder"), "Message api");
  });
});

test("TitleParts: with no listed row the head falls back to the served label, with no directory", { timeout: 2000 }, async () => {
  const server = new FakeServer();
  await withDock(server, { rows: [] }, async (container) => {
    assert.equal(textOf(container, "h1"), "api");
    assertNone(container.querySelector("h1 .title-dir"), "no directory is drawn");
    assert.equal(container.querySelector("textarea")?.getAttribute("placeholder"), "Message api");
  });
});

// rendered waits as testwait's until does, with each turn run inside act, so the renders the stream's events queue are
// applied. It ends with the test's own signal, so a wait that never holds fails the test by name.
function rendered(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

test("a message sent from the dock draws as a sending row until its stored prompt arrives", { timeout: 2000 }, async (t) => {
  const server = new FakeServer();
  await withDock(server, {}, async (container) => {
    server.emit(0, { transcript: "t1", seq: "5", reset: true, items: [], state: { session: { state: "done" } } });
    const box = container.querySelector<HTMLTextAreaElement>("textarea");
    assert.ok(box, "the dock has a composer");
    await act(() => {
      box.value = "from the dock";
      box.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(() => {
      box.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", cancelable: true }));
    });
    await rendered(t.signal, () => container.querySelector(".reader .sending-row") !== null, "the sending row");
    assert.equal(textOf(container, ".reader .sending-row .gut.sending"), "sending");
    assert.equal(textOf(container, ".reader .sending-row").includes("from the dock"), true, "the row holds the text");

    await rendered(t.signal, () => server.says.length === 1, "the say");
    const promptId = String(server.says[0].promptId);
    server.emit(0, { transcript: "t1", seq: "6", items: [{ kind: "prompt", text: "from the dock", promptId }] });
    await rendered(t.signal, () => container.querySelector(".reader .sending-row") === null, "the sending row leaves");
    assert.equal(container.querySelectorAll(".reader .you").length, 1, "the stored prompt is the one prompt row");

    server.emit(0, { transcript: "t1", seq: "7", reset: true, items: [{ kind: "assistant", text: "a later reply" }] });
    await rendered(t.signal, () => textOf(container, ".reader").includes("a later reply"), "the reset that drops the prompt");
    // A boolean, not the element: a failing assertion would format the DOM node, which stalls the test.
    assert.equal(container.querySelector(".reader .sending-row") === null, true, "a sent message that left never draws again");
  });
});

test("a message the dock's target holds draws as one queued row, and no sending row", { timeout: 2000 }, async (t) => {
  const server = new FakeServer();
  await withDock(server, {}, async (container) => {
    server.emit(0, { transcript: "t1", seq: "5", reset: true, items: [], state: { session: { state: "working" } } });
    const box = container.querySelector<HTMLTextAreaElement>("textarea");
    assert.ok(box, "the dock has a composer");
    await act(() => {
      box.value = "held from the dock";
      box.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(() => {
      box.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", cancelable: true }));
    });
    await rendered(t.signal, () => server.says.length === 1, "the say");
    const promptId = String(server.says[0].promptId);
    await rendered(t.signal, () => container.querySelector(".reader .sending-row") !== null, "the sending row");
    server.emit(0, {
      transcript: "t1",
      seq: "6",
      items: [],
      state: { session: { state: "stopped" } },
      queued: [{ promptId, text: "held from the dock", state: "queued" }],
    });
    await rendered(t.signal, () => container.querySelector(".reader .sending-row") === null, "the sending row leaves");
    assert.deepEqual(
      [...container.querySelectorAll(".dock .queue li .text")].map((text) => text.textContent),
      ["held from the dock"],
      "the held message draws one queued row",
    );
  });
});
