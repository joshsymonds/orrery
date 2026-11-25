import assert from "node:assert/strict";
import { test } from "node:test";

import { create, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { sessionTotals, subscriptionTotals } from "./fixtures/usage";
import { HostStatusResponseSchema } from "./gen/orrery/v1/orrery_pb";
import { type UsageSessionTotal, UsageSessionTotalsResponseSchema } from "./gen/orrery/v1/usage_pb";
import type { Row } from "./rows";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

const now = 1000;
const wideWidth = 1024;
const phoneWidth = 400;
const sessionTotalsPath = "/orrery.v1.Usage/SessionTotals";
const denHost = create(HostStatusResponseSchema, { host: "den", stats: [] });

// Answer is what the server answers one SessionTotals read with: the totals of the session, or the code of the error that
// fails the read.
type Answer = { totals: UsageSessionTotal[] } | { error: "unavailable" | "internal" };

// Read is one SessionTotals call the app made: the session it named, and whether the app has taken the whole answer, which
// is when the app has read the body to its end.
interface Read {
  session: string;
  taken: () => boolean;
}

// Stream is one WatchConversation call the app opened: the id it watches, a pane's or a session's, and the controller that
// feeds it.
interface Stream {
  id: string;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// reply answers with a status and a JSON body in one chunk. taken is set once the app asks for more after that chunk, so it
// reads true only when the app has read the body to its end.
function reply(status: number, body: unknown): { response: Response; taken: () => boolean } {
  const bytes = new TextEncoder().encode(JSON.stringify(body));
  let sent = false;
  let taken = false;
  const stream = new ReadableStream<Uint8Array>({
    pull: (controller) => {
      if (sent) {
        taken = true;
        controller.close();
        return;
      }
      sent = true;
      controller.enqueue(bytes);
    },
  });
  return {
    response: new Response(stream, { status, headers: { "content-type": "application/json" } }),
    taken: () => taken,
  };
}

// fakeTransport answers SessionTotals with the answers in turn, the last one repeated, and records each read. The
// WatchConversation calls each get a stream the test feeds through emit, in the order opened.
function fakeTransport(answers: readonly Answer[]) {
  const streams: Stream[] = [];
  const reads: Read[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (path === sessionTotalsPath) {
      const { session } = JSON.parse(await request.text()) as { session: string };
      const answer = answers[Math.min(reads.length, answers.length - 1)];
      const served =
        "error" in answer
          ? reply(answer.error === "unavailable" ? 503 : 500, { code: answer.error, message: "the ledger did not answer" })
          : reply(
              200,
              toJson(UsageSessionTotalsResponseSchema, create(UsageSessionTotalsResponseSchema, { totals: answer.totals })),
            );
      reads.push({ session, taken: served.taken });
      return served.response;
    }
    if (!path.endsWith("/WatchConversation")) {
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
    }
    const watched = JSON.parse(new TextDecoder().decode(new Uint8Array(await request.arrayBuffer()).subarray(5)));
    const stream: Stream = { id: watched.pane ?? watched.id, controller: null };
    streams.push(stream);
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        stream.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };
  return { fetch, streams, reads };
}

type FakeTransport = ReturnType<typeof fakeTransport>;

// emit sends one message down a stream: a flag byte of 0, the payload's length, and the JSON payload.
function emit(stream: Stream, message: object): void {
  assert.ok(stream.controller !== null, "the stream is open");
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  stream.controller.enqueue(bytes);
}

// sessionRow is the pane row the middle conversation draws, whose session is archiveId and whose state is state. Its usage
// is read by archiveId, which is the orrery session id, not the pane's own id.
function sessionRow(state: string, archiveId = "orr-api"): Row {
  return {
    key: "api",
    id: "api",
    kind: "pane",
    label: "api",
    project: "/work/app",
    host: "den",
    state,
    summary: "",
    question: "",
    questionHash: "",
    tool: "",
    changed: 200,
    seen: 200,
    unseen: false,
    putOff: false,
    archived: false,
    archiveKind: "session",
    archiveId,
  };
}

// sessionListRow is a Sessions.List row, whose conversation is the session's own: its activity lines, and so its list
// panels, are drawn, which a pane's are not. Its stream is watched by its session id, and its usage by the same id.
function sessionListRow(): Row {
  return { ...sessionRow("working"), key: "sess", id: "orr-api", kind: "session", label: "api" };
}

interface Setup {
  width?: number;
  answers: Answer[];
  archiveId?: string;
  // row is the row the app is given, a pane's unless the test names a session's.
  row?: Row;
}

// Harness is what a check reads: the app's container, its fake transport, and setRows, which draws App again over the same
// container with rows in place of the rows it drew, so the app's state holds and only its props move.
interface Harness {
  container: HTMLElement;
  transport: FakeTransport;
  setRows: (rows: Row[]) => Promise<void>;
}

// withApp mounts the app over the fake transport in a fresh DOM at the width given, and runs check. The app is unmounted and
// the DOM removed when the test ends, or as soon as signal aborts if it times out first. At a phone's width the cursor's row
// is tapped open, as the owner does, since the inbox is the home page there.
async function withApp(signal: AbortSignal, setup: Setup, check: (harness: Harness) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  window.innerWidth = setup.width ?? wideWidth;
  const container = document.createElement("div");
  document.body.append(container);
  const transport = fakeTransport(setup.answers);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: transport.fetch }));
  const draw = async (rows: Row[]) => {
    await act(() => {
      render(<App rows={rows} now={now} clients={clients} host={denHost} />, container);
    });
  };
  const rows = [setup.row ?? sessionRow("working", setup.archiveId ?? "orr-api")];
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await draw(rows);
    if (window.innerWidth < 760) {
      await clickOn(container, ".list .row.cur");
    }
    await check({
      container,
      transport,
      setRows: async (next) => {
        await draw(next);
      },
    });
  } finally {
    await teardown();
  }
}

// untilTurn waits for check, stepping by turn inside act, so each round applies the renders and stream deltas it lets through,
// and no clock runs.
function untilTurn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// answered waits until the app has taken the answer to read number index, and then lets the renders that answer causes run.
async function answered(signal: AbortSignal, transport: FakeTransport, index: number): Promise<void> {
  await untilTurn(signal, () => transport.reads[index]?.taken() === true, `read ${index} is answered`);
  await act(turn);
}

// clickOn clicks the first element that matches selector, and fails when none does.
async function clickOn(container: HTMLElement, selector: string): Promise<void> {
  const element = container.querySelector<HTMLElement>(selector);
  if (element === null) {
    assert.fail(`${selector} is drawn`);
  }
  await act(() => {
    element.click();
  });
}

// streamOf is the newest stream the app opened for the id given.
function streamOf(transport: FakeTransport, id: string): Stream {
  const stream = [...transport.streams].reverse().find((candidate) => candidate.id === id);
  assert.ok(stream !== undefined, `the stream of ${id} is open`);
  return stream;
}

// openConversation waits for the stream the app opened for the id, and feeds it a reset whose state holds a context of
// 60000 tokens in a 200000 window, so the title row draws its meter, and the state given. It returns once the meter is drawn.
async function openConversation(
  signal: AbortSignal,
  container: HTMLElement,
  transport: FakeTransport,
  state: Record<string, unknown> = {},
  id = "api",
): Promise<void> {
  await untilTurn(signal, () => transport.streams.some((stream) => stream.id === id), `the stream of ${id}`);
  emit(streamOf(transport, id), {
    transcript: "t",
    seq: "1",
    reset: true,
    items: [],
    state: { context: { tokens: "60000", window: "200000" }, ...state },
  });
  await untilTurn(
    signal,
    () => container.querySelector('section[aria-label="reader"] .title-row .ctx') !== null,
    "the context meter",
  );
}

// readoutOf is the readout of the middle conversation's title row, or null while none is drawn.
function readoutOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label="reader"] .title-row .usage-readout');
}

// figureOf is the list total a root draws, or null when none is drawn.
function figureOf(root: ParentNode): string | null {
  return root.querySelector(".usage-list")?.textContent ?? null;
}

// panelOf is the Usage panel drawn beside the conversation, an aside labelled Usage, or null while none is drawn.
function panelOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('aside[aria-label="Usage"]');
}

// usageOf is the Usage panel the conversation draws, beside it or in a sheet, and fails the test when neither is drawn.
function usageOf(container: HTMLElement): HTMLElement {
  const drawn = panelOf(container) ?? sheetOf(container);
  assert.ok(drawn !== null, "the Usage panel is drawn");
  return drawn;
}

// sheetOf is the Usage sheet drawn under 760px, a dialog labelled Usage, or null while none is drawn.
function sheetOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[role="dialog"][aria-label="Usage"]');
}

test("SessionUsageReadout", async (t) => {
  await t.test("the title row draws the label usage and the list total after the context meter", (st) =>
    withApp(st.signal, { answers: [{ totals: sessionTotals }] }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await untilTurn(st.signal, () => readoutOf(container) !== null, "the readout");
      const readout = readoutOf(container);
      assert.equal(readout?.textContent, "usage $3.77", "the readout reads usage and the list total");
      const context = container.querySelector('section[aria-label="reader"] .title-row .ctx');
      assert.ok(context !== null && readout !== null, "the context meter and the readout are drawn");
      assert.ok(
        context.compareDocumentPosition(readout) & Node.DOCUMENT_POSITION_FOLLOWING,
        "the readout follows the context meter",
      );
    }));

  await t.test("no readout when the answer has no totals", (st) =>
    withApp(st.signal, { answers: [{ totals: [] }] }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      assertNone(readoutOf(container), "an answer with no totals draws no readout");
    }));

  await t.test("no readout when the answer is unavailable", (st) =>
    withApp(st.signal, { answers: [{ error: "unavailable" }] }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      assertNone(readoutOf(container), "an unavailable answer draws no readout");
    }));

  await t.test("no readout when the read errors", (st) =>
    withApp(st.signal, { answers: [{ error: "internal" }] }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      assertNone(readoutOf(container), "a read that errors draws no readout");
    }));

  await t.test("no read and no readout for a row with no archiveId", (st) =>
    withApp(st.signal, { answers: [{ totals: sessionTotals }], archiveId: "" }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await act(turn);
      assert.equal(transport.reads.length, 0, "a row with no archiveId makes no read");
      assertNone(readoutOf(container), "no read draws no readout");
    }));

  await t.test("one read when the conversation opens, of the row's session", (st) =>
    withApp(st.signal, { answers: [{ totals: sessionTotals }] }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      assert.ok(readoutOf(container) !== null, "the readout is drawn once the read answers");
      assert.equal(transport.reads.length, 1, "opening the conversation reads its totals once");
      assert.equal(transport.reads[0].session, "orr-api", "the read names the row's archiveId");
    }));

  await t.test("a turn's end reads the totals again, and the readout shows that read", (st) =>
    withApp(st.signal, { answers: [{ totals: sessionTotals }, { totals: subscriptionTotals }] }, async (harness) => {
      const { container, transport, setRows } = harness;
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      assert.equal(readoutOf(container)?.textContent, "usage $3.77");
      await setRows([sessionRow("idle")]);
      await answered(st.signal, transport, 1);
      assert.equal(transport.reads.length, 2, "the turn's end is a second read, and no more");
      assert.equal(transport.reads[1].session, "orr-api");
      assert.equal(readoutOf(container)?.textContent, "usage $1.40", "the readout shows the second read's total");
    }));

  await t.test("the open panel shows the same figures as the readout, and follows a turn's end with it", (st) =>
    withApp(st.signal, { answers: [{ totals: sessionTotals }, { totals: subscriptionTotals }] }, async (harness) => {
      const { container, transport, setRows } = harness;
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
      assert.equal(figureOf(usageOf(container)), "$3.77", "the panel shows the readout's total");
      await setRows([sessionRow("idle")]);
      await answered(st.signal, transport, 1);
      assert.equal(readoutOf(container)?.textContent, "usage $1.40");
      assert.equal(figureOf(usageOf(container)), "$1.40", "the panel shows the second read, as the readout does");
    }));
});

test("SessionUsagePanel", async (t) => {
  await t.test("at a wide width the panel draws beside the conversation as an aside labelled Usage", (st) =>
    withApp(st.signal, { answers: [{ totals: sessionTotals }] }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
      const panel = panelOf(container);
      assert.ok(panel !== null, "an aside labelled Usage is drawn");
      const panes = container.querySelector(".panes");
      assert.ok(panes?.contains(panel) === true, "the panel is drawn in the panes, beside the conversation");
      const reader = container.querySelector('section[aria-label="reader"]');
      assert.ok(panes?.contains(reader) === true, "the conversation is drawn in the same panes");
      assert.equal(container.querySelector(".panes")?.getAttribute("data-side"), "panel", "the panes make room for the panel");
      assert.equal(panel.querySelector("h2")?.textContent, "Usage", "the panel's head reads Usage");
      assert.equal(figureOf(panel), "$3.77", "the panel draws the session's list total");
      assert.equal(readoutOf(container)?.getAttribute("aria-pressed"), "true", "the readout is lit while the panel is open");
      assertNone(sheetOf(container), "no sheet is drawn at a wide width");
    }));

  await t.test("under 760px the panel draws in a dialog sheet labelled Usage, headless", (st) =>
    withApp(st.signal, { width: phoneWidth, answers: [{ totals: sessionTotals }] }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
      const sheet = sheetOf(container);
      assert.ok(sheet !== null, "a dialog labelled Usage is drawn");
      assertNone(panelOf(container), "no aside is drawn under 760px");
      assertNone(sheet.querySelector("h2"), "the sheet's title names the panel, so the panel is headless");
      assert.equal(figureOf(sheet), "$3.77", "the sheet draws the session's list total");
    }));

  await t.test("the panel's Close and a second press on the readout each shut it", (st) =>
    withApp(st.signal, { answers: [{ totals: sessionTotals }] }, async ({ container, transport }) => {
      await openConversation(st.signal, container, transport);
      await answered(st.signal, transport, 0);
      await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
      await clickOn(container, 'aside[aria-label="Usage"] button[aria-label="Close the panel"]');
      assertNone(panelOf(container), "Close shuts the panel");
      assert.equal(readoutOf(container)?.getAttribute("aria-pressed"), "false", "the readout is unlit once it shuts");
      await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
      assert.ok(panelOf(container) !== null, "a press on the readout opens the panel again");
      await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
      assertNone(panelOf(container), "a second press on the readout shuts it");
    }));

  await t.test("the Usage panel and a list panel shut each other", (st) =>
    withApp(st.signal, { row: sessionListRow(), answers: [{ totals: sessionTotals }] }, async ({ container, transport }) => {
      await openConversation(
        st.signal,
        container,
        transport,
        { background: [{ id: "a1", type: "agent", status: "running" }] },
        "orr-api",
      );
      await answered(st.signal, transport, 0);
      await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
      assert.ok(panelOf(container) !== null, "the Usage panel is open");
      await clickOn(container, ".activity-line");
      assertNone(panelOf(container), "opening a list panel shuts the Usage panel");
      assert.ok(container.querySelector('aside[aria-label="Sub-agents"]') !== null, "the list panel is open");
      await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
      assert.ok(panelOf(container) !== null, "the Usage panel opens again");
      assertNone(container.querySelector('aside[aria-label="Sub-agents"]'), "opening the Usage panel shuts the list panel");
    }));

  await t.test("a read that answers no totals shuts the panel, and a later read with totals does not reopen it", (st) =>
    withApp(
      st.signal,
      { answers: [{ totals: sessionTotals }, { totals: [] }, { totals: subscriptionTotals }] },
      async (harness) => {
        const { container, transport, setRows } = harness;
        await openConversation(st.signal, container, transport);
        await answered(st.signal, transport, 0);
        await clickOn(container, 'section[aria-label="reader"] .title-row .usage-readout');
        assert.ok(panelOf(container) !== null, "the Usage panel is open");
        await setRows([sessionRow("idle")]);
        await answered(st.signal, transport, 1);
        assertNone(readoutOf(container), "an answer with no totals draws no readout");
        assertNone(panelOf(container), "an answer with no totals shuts the panel");
        await setRows([sessionRow("working")]);
        await setRows([sessionRow("idle")]);
        await answered(st.signal, transport, 2);
        assert.equal(readoutOf(container)?.textContent, "usage $1.40", "the readout draws the later read's total");
        assertNone(panelOf(container), "the later read does not reopen the panel");
      },
    ));
});
