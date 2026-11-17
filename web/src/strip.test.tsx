import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type HostStatusResponse, HostStatusResponseSchema } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

const now = 1000;
const cursorStrip = 'section[aria-label="reader"] .pane-strip';

// The serving host's status: its name and three stats. The second is at warn and the third at crit, and each of the
// three has a percent.
const denHost = create(HostStatusResponseSchema, {
  host: "den",
  stats: [
    { label: "cpu", value: "12%", percent: 12, level: "ok" },
    { label: "disk", value: "65%", percent: 65, level: "warn" },
    { label: "mem", value: "7.1 GB", percent: 88, level: "crit" },
  ],
});

// The pane contexts the strip is checked at: tokens out of a 200000-token window, the percent each reads, the blocks it
// fills and its band.
const contextCases = [
  { tokens: "60000", percent: "30%", filled: 2, band: "green" },
  { tokens: "100000", percent: "50%", filled: 3, band: "yellow" },
  { tokens: "140000", percent: "70%", filled: 4, band: "peach" },
  { tokens: "180000", percent: "90%", filled: 5, band: "red" },
];

// Stream is one WatchConversation call the app opened, with the controller that feeds it.
interface Stream {
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// fakeTransport answers the app's unary calls with an empty body, and each WatchConversation call with a stream the
// test feeds through emit. Each stream is recorded in the order it was opened.
function fakeTransport() {
  const streams: Stream[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
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
  return { fetch, streams };
}

// emit sends one message down a stream: a flag byte of 0, the payload's length, and the JSON payload.
function emit(stream: Stream, message: object): void {
  assert.ok(stream.controller !== null, "the stream is open");
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  stream.controller.enqueue(bytes);
}

// row is a pane row that has been seen, so no dwell timer runs on the cursor's row.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "den",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 200,
    seen: 200,
    unseen: false,
    archived: false,
    archiveKind: "none",
    ...init,
  };
}

// withStrip mounts the app over the fake transport in a fresh DOM and runs check. The app is unmounted and the DOM
// removed when the test ends, or as soon as signal aborts if it times out first.
async function withStrip(
  signal: AbortSignal,
  props: { rows?: Row[]; host: HostStatusResponse },
  check: (container: HTMLElement, transport: ReturnType<typeof fakeTransport>) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const transport = fakeTransport();
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: transport.fetch }));
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(
        <App
          rows={props.rows ?? [row({ key: "a", label: "api" })]}
          error=""
          now={now}
          clients={clients}
          host={props.host}
        />,
        container,
      );
    });
    await check(container, transport);
  } finally {
    await teardown();
  }
}

// untilTurn waits for check, stepping by turn inside act, so each round applies the renders and stream deltas it lets
// through, and no clock runs.
function untilTurn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// leavePinnedBox focuses the pinned reader's reply box, as | does on origin, and presses Shift+Tab in it, which takes
// the focus to the list's cursor row.
async function leavePinnedBox(container: HTMLElement): Promise<void> {
  const box = container.querySelector<HTMLTextAreaElement>(
    'section[aria-label="pinned reader"] .pin-reply:not([hidden]) .composer textarea',
  );
  if (box === null) {
    assert.fail("the pinned reader has its reply box");
  }
  await act(() => {
    box.focus();
  });
  await act(() => {
    box.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
  });
}

function textOf(container: HTMLElement, selector: string): string | null {
  return container.querySelector(selector)?.textContent ?? null;
}

function textsOf(container: HTMLElement, selector: string): string[] {
  return [...container.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

function classesOf(container: HTMLElement, selector: string): string[] {
  return [...container.querySelectorAll(selector)].map((element) => element.getAttribute("class") ?? "");
}

function widthsOf(container: HTMLElement, selector: string): string[] {
  return [...container.querySelectorAll<HTMLElement>(selector)].map((element) => element.style.width);
}

test("PaneStrip: a pane page's strip names the serving host, and draws each stat's label, value and bar level", (t) =>
  withStrip(t.signal, { host: denHost }, (container) => {
    assert.equal(textOf(container, `${cursorStrip} .pane-strip-host`), "den");
    assert.deepEqual(textsOf(container, `${cursorStrip} .pane-strip-label`), ["cpu", "disk", "mem"]);
    assert.deepEqual(textsOf(container, `${cursorStrip} .pane-strip-value`), ["12%", "65%", "7.1 GB"]);
    assert.deepEqual(classesOf(container, `${cursorStrip} .bar`), ["bar level-ok", "bar level-warn", "bar level-crit"]);
    assert.deepEqual(widthsOf(container, `${cursorStrip} .bar`), ["12%", "65%", "88%"]);
    assert.equal(
      container.querySelector(`${cursorStrip} .pane-strip-context`) === null,
      true,
      "no context before the stream reports one",
    );
  }));

test("PaneStrip: a pane conversation's context draws five blocks, filled by its percent, in its band", (t) =>
  withStrip(t.signal, { host: denHost }, async (container, transport) => {
    await untilTurn(t.signal, () => transport.streams.length === 1, "the pane stream");
    const [stream] = transport.streams;
    for (const { tokens, percent, filled, band } of contextCases) {
      emit(stream, { transcript: "t", seq: "1", items: [], state: { context: { tokens, window: "200000" } } });
      await untilTurn(
        t.signal,
        () => textOf(container, `${cursorStrip} .ctx-pct`) === percent,
        `the ${percent} context`,
      );
      assert.equal(
        container.querySelectorAll(`${cursorStrip} .ctx-blocks i`).length,
        5,
        `${percent} draws five blocks`,
      );
      assert.equal(
        container.querySelectorAll(`${cursorStrip} .ctx-blocks i.on`).length,
        filled,
        `${percent} fills ${filled} blocks`,
      );
      assert.equal(
        classesOf(container, `${cursorStrip} .pane-strip-context`)[0],
        `pane-strip-context band-${band}`,
        `${percent} reads the ${band} band`,
      );
    }
  }));

test("PaneStrip: a context whose window is zero draws no context part", (t) =>
  withStrip(t.signal, { host: denHost }, async (container, transport) => {
    await untilTurn(t.signal, () => transport.streams.length === 1, "the pane stream");
    const [stream] = transport.streams;
    emit(stream, { transcript: "t", seq: "1", items: [], state: { context: { tokens: "60000", window: "200000" } } });
    await untilTurn(t.signal, () => textOf(container, `${cursorStrip} .ctx-pct`) === "30%", "the 30% context");
    emit(stream, { transcript: "t", seq: "2", items: [], state: { context: { tokens: "60000", window: "0" } } });
    await untilTurn(t.signal, () => textOf(container, `${cursorStrip} .ctx-pct`) !== "30%", "the zero-window state");
    assert.equal(container.querySelector(`${cursorStrip} .pane-strip-context`) === null, true);
    assert.equal(textOf(container, `${cursorStrip} .pane-strip-host`), "den", "the host part still draws");
  }));

test("PaneStrip: a Host.Status with no stats draws the host name alone", (t) =>
  withStrip(t.signal, { host: create(HostStatusResponseSchema, { host: "den" }) }, (container) => {
    assert.equal(textOf(container, `${cursorStrip} .pane-strip-host`), "den");
    assert.equal(container.querySelectorAll(`${cursorStrip} .pane-strip-stat`).length, 0);
  }));

test("PaneStrip: a pane row on another host names that host alone, with no stats", (t) =>
  withStrip(t.signal, { rows: [row({ key: "x", host: "laptop" })], host: denHost }, (container) => {
    assert.equal(textOf(container, `${cursorStrip} .pane-strip-host`), "laptop");
    assert.equal(container.querySelectorAll(`${cursorStrip} .pane-strip-stat`).length, 0);
  }));

test("PaneStrip: a split shows a strip under each of its two panes' title rows", (t) =>
  withStrip(t.signal, { host: denHost }, async (container) => {
    await press("|");
    assert.deepEqual(textsOf(container, ".pane-strip-host"), ["den", "den"]);
    assert.equal(container.querySelectorAll(cursorStrip).length, 1);
    assert.equal(container.querySelectorAll('section[aria-label="pinned reader"] .pane-strip').length, 1);
  }));

test("PaneStrip: m hides every strip, and a second m shows them again", (t) =>
  withStrip(t.signal, { host: denHost }, async (container) => {
    await press("|");
    assert.equal(container.querySelectorAll(".pane-strip").length, 2);
    await leavePinnedBox(container);
    await press("m");
    assert.equal(container.querySelectorAll(".pane-strip").length, 0);
    await press("m");
    assert.equal(container.querySelectorAll(".pane-strip").length, 2);
  }));

test("PaneStrip: no inbox footer remains, and the keys hint sits in the inbox head", (t) =>
  withStrip(t.signal, { host: denHost }, (container) => {
    assert.equal(container.querySelector(".lfoot") === null, true);
    assert.equal(textOf(container, ".list > header.top .keys-hint"), "? keys");
  }));
