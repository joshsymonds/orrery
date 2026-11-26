import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import {
  type HeadlessSession,
  HeadlessSessionSchema,
  HostEntrySchema,
  type HostStatusResponse,
  HostStatusResponseSchema,
  type Pane,
  PaneSchema,
} from "./gen/orrery/v1/orrery_pb";
import { type Row, rowsFrom } from "./rows";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the token HostsWebHostsView. A pane's or a headless session's head and band name its host as a
// HostLabel and draw no meter, on the serving host and on another host.

// now is the clock App reads its times against, in unix seconds, and wideWidth is a window the layout draws wide.
const now = 1000;
const wideWidth = 1024;

// serving is the host this server runs on, and other is a host a row can run on that is not the serving one.
const serving = "halyard";
const other = "brine";

// status is the serving host's status: its own stats, which the head no longer draws, and the hosts Host.Status lists in
// order: halyard, brine, which does not answer, and sextant, whose load is past 100%.
const status: HostStatusResponse = create(HostStatusResponseSchema, {
  host: serving,
  stats: [
    { label: "cpu", value: "12%", percent: 12, level: "ok" },
    { label: "ram", value: "44%", percent: 44, level: "warn" },
  ],
  hosts: [
    create(HostEntrySchema, { name: "halyard", loadPercent: 12.4, memoryPercent: 44.2, reachable: true }),
    create(HostEntrySchema, { name: "brine", reachable: false }),
    create(HostEntrySchema, { name: "sextant", loadPercent: 171.5, memoryPercent: 61.8, reachable: true }),
  ],
});

// stillClock is a seen clock whose timers never fire, so no row is marked seen on a timer.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// fakeFetch answers each unary call with an empty message, and each WatchConversation call with a stream that never
// speaks, so the App's reads land and no call holds the test open.
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

// paneOn is an invented pane on host, in a directory, whose conversation has been seen.
function paneOn(id: string, host: string): Pane {
  return create(PaneSchema, {
    id,
    host,
    session: "work",
    cwd: "/srv/app",
    claudeSession: `claude-${id}`,
    state: "done",
    summary: `pane ${id}`,
    changed: BigInt(now - 800),
    seen: BigInt(now - 800),
    orreryId: `conv-${id}`,
  });
}

// headlessOn is an invented headless session on host, in a directory, which has been seen.
function headlessOn(id: string, host: string): HeadlessSession {
  return create(HeadlessSessionSchema, {
    id,
    host,
    dir: "/srv/app",
    headline: `session ${id}`,
    state: "done",
    changed: BigInt(now - 800),
    seen: BigInt(now - 800),
  });
}

// mounted draws App over the fake transport at a wide width with rows, in a fresh DOM, and runs check on its container.
// App is unmounted and the DOM removed when check returns or throws, or as soon as the test ends if it times out first.
async function mounted(
  t: TestContext,
  rows: Row[],
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = wideWidth;
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch }));
  const container = document.createElement("div");
  document.body.append(container);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(<App rows={rows} now={now} clients={clients} seenClock={stillClock} host={status} />, container);
    });
    await check(container);
  } finally {
    await teardown();
  }
}

// hostNamesIn lists the names of the HostLabels under root, in order.
function hostNamesIn(root: ParentNode): string[] {
  return [...root.querySelectorAll(".host-label")].map((label) => label.querySelector(".name")?.textContent ?? "");
}

// assertHostOnlyHead checks the reader's head for a row on host: its band names host as its one HostLabel, and the head
// draws no meter and no line about meters.
function assertHostOnlyHead(container: HTMLElement, host: string, what: string): void {
  const head = container.querySelector<HTMLElement>('section[aria-label="reader"] .convo-head');
  assert.ok(head !== null, `${what} draws its head`);
  const band = head.querySelector<HTMLElement>(".machine-band");
  assert.ok(band !== null, `${what} draws its machine band`);
  assert.deepEqual(hostNamesIn(band), [host], `${what}: its band names its host as a HostLabel`);
  assert.equal(head.querySelectorAll(".meter").length, 0, `${what}: its head draws no meter`);
  assertNone(head.querySelector(".other"), `${what}: its head draws no line about meters`);
}

test("HostsWebHostsView head: a pane's head and band name its host as a HostLabel and draw no meter, on the serving host and on another", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  for (const host of [serving, other]) {
    await mounted(t, rowsFrom([paneOn("%1", host)], [], []), (container) => {
      assertHostOnlyHead(container, host, `a pane on ${host}`);
    });
  }
});

test("HostsWebHostsView head: a headless session's head and band name its host as a HostLabel and draw no meter, on the serving host and on another", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  for (const host of [serving, other]) {
    await mounted(t, rowsFrom([], [], [headlessOn("sess-1", host)]), (container) => {
      assertHostOnlyHead(container, host, `a headless session on ${host}`);
    });
  }
});
