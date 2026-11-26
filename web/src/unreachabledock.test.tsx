import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { liveEpoch } from "./fixtures/clock";
import {
  HeadlessSessionSchema,
  type HeadlessSession,
  HostEntrySchema,
  type HostStatusResponse,
  HostStatusResponseSchema,
  PaneSchema,
} from "./gen/orrery/v1/orrery_pb";
import { type Row, rowsFrom } from "./rows";
import { assertNone, installTestDom, removeTestDom } from "./testdom";

// now is the clock the app reads its times against, in unix seconds: the fixtures' live instant, so no test reads the wall
// clock or runs a timer of its own.
const now = Date.parse(liveEpoch) / 1000;

// serving is the host this server runs on, which answers; den is the host the rows run on, whose answer each check sets.
const serving = "halyard";
const den = "den";

// writes are the calls that send text or keys, take a queued message back, interrupt or resume a conversation. None may be
// made for a row on a host that does not answer.
const writes = [
  "/orrery.v1.Panes/SendText",
  "/orrery.v1.Panes/PullQueued",
  "/orrery.v1.Panes/SendKeys",
  "/orrery.v1.Control/Say",
  "/orrery.v1.Control/Withdraw",
  "/orrery.v1.Control/Interrupt",
  "/orrery.v1.Control/Resume",
];

// status is the host status the app reads: the serving host, which answers, and den, which answers as denReachable says.
function status(denReachable: boolean): HostStatusResponse {
  return create(HostStatusResponseSchema, {
    host: serving,
    stats: [],
    hosts: [
      create(HostEntrySchema, { name: serving, reachable: true }),
      create(HostEntrySchema, { name: den, reachable: denReachable }),
    ],
  });
}

// pane is an invented pane on den, running in a directory, whose Pane.unreachable is given, and whose state is stopped.
function pane(id: string, unreachable: boolean) {
  return create(PaneSchema, {
    id,
    host: den,
    session: "work",
    cwd: "/srv/den/app",
    claudeSession: `claude-${id}`,
    state: "stopped",
    summary: `pane ${id}`,
    changed: BigInt(now - 60),
    seen: BigInt(now - 60),
    unreachable,
  });
}

// session is an invented headless session on den, whose state is stopped, so a title row that read the state would say
// stopped where it must say unreachable.
function session(id: string): HeadlessSession {
  return create(HeadlessSessionSchema, {
    id,
    host: den,
    dir: "/srv/den/app",
    headline: `session ${id}`,
    state: "stopped",
    changed: BigInt(now - 60),
    seen: BigInt(now - 60),
  });
}

// Recording is the transport the app talks through: the path of each call it made, in order. A stream it opens stays open
// with no message, as a conversation whose host does not answer gives.
interface Recording {
  paths: string[];
  fetch: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
}

function recording(): Recording {
  const paths: string[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(new Request(input, init).url).pathname;
    paths.push(path);
    if (path.endsWith("/WatchConversation")) {
      return new Response(new ReadableStream<Uint8Array>(), {
        status: 200,
        headers: { "content-type": "application/connect+json" },
      });
    }
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  };
  return { paths, fetch };
}

// Drawn is what a check reads: the app's container, and the recording of the calls it made.
interface Drawn {
  container: HTMLElement;
  calls: Recording;
}

// withApp mounts the app at a desk width over the rows and the host status given, in a fresh DOM, and runs check. The app
// is unmounted and the DOM removed when the check ends, or fails.
async function withApp(
  rows: Row[],
  host: HostStatusResponse,
  check: (drawn: Drawn) => Promise<void> | void,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = 1024;
  const container = document.createElement("div");
  document.body.append(container);
  const calls = recording();
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: calls.fetch }));
  try {
    await act(() => {
      render(<App rows={rows} now={now} clients={clients} host={host} />, container);
    });
    await check({ container, calls });
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// reader is the cursor's reader column, which holds the title row, the reader and the dock of the row it shows.
function reader(container: HTMLElement): HTMLElement {
  const column = container.querySelector<HTMLElement>('section[aria-label="reader"]');
  assert.notEqual(column, null, "the cursor's reader is drawn");
  return column as HTMLElement;
}

// titleRowOf is the cursor's title row.
function titleRowOf(container: HTMLElement): HTMLElement {
  const titleRow = reader(container).querySelector<HTMLElement>(".title-row");
  assert.notEqual(titleRow, null, "the title row is drawn");
  return titleRow as HTMLElement;
}

// dockOf is the dock the cursor's row draws under its reader.
function dockOf(container: HTMLElement): HTMLElement {
  const dock = reader(container).querySelector<HTMLElement>(".send .dock");
  assert.notEqual(dock, null, "the dock is drawn");
  return dock as HTMLElement;
}

// assertUnreachableTitle checks the cursor's title row says unreachable with the unreachable glyph, and never stopped.
function assertUnreachableTitle(container: HTMLElement): void {
  const titleRow = titleRowOf(container);
  assert.equal(titleRow.querySelector(".state")?.textContent, "unreachable");
  assert.equal(titleRow.querySelector(".glyph")?.getAttribute("data-state"), "unreachable");
  assert.ok(!(titleRow.textContent ?? "").includes("stopped"), "the title row does not say stopped");
}

test("HostsWebUnreachable dock: a pane with Pane.unreachable says unreachable in its title row, and not stopped", async () => {
  await withApp(rowsFrom([pane("%5", true)], []), status(false), ({ container }) => {
    assertUnreachableTitle(container);
  });
});

test("HostsWebUnreachable dock: a headless session on a host whose HostEntry is not reachable says unreachable in its title row, and not stopped", async () => {
  await withApp(rowsFrom([], [], [session("s-1")]), status(false), ({ container }) => {
    assertUnreachableTitle(container);
  });
});

test("HostsWebUnreachable dock: the strip of a row on a host that does not answer names the host down, beside its HostLabel", async () => {
  await withApp(rowsFrom([pane("%5", true)], []), status(false), ({ container }) => {
    const label = titleRowOf(container).parentElement?.querySelector<HTMLElement>(".pane-strip .host-label");
    assert.ok(label, "the strip draws the host's label");
    assert.equal(label.querySelector(".name")?.textContent, den);
    assert.equal(label.nextElementSibling?.textContent, "unreachable");
  });
});

test("HostsWebUnreachable dock: a pane with Pane.unreachable reads unreachable: <host> does not answer as its whole dock", async () => {
  await withApp(rowsFrom([pane("%5", true)], []), status(false), ({ container }) => {
    assert.equal(dockOf(container).textContent, "unreachable: den does not answer");
  });
});

test("HostsWebUnreachable dock: a headless session on a host that does not answer reads unreachable: <host> does not answer as its whole dock", async () => {
  await withApp(rowsFrom([], [], [session("s-1")]), status(false), ({ container }) => {
    assert.equal(dockOf(container).textContent, "unreachable: den does not answer");
  });
});

test("HostsWebUnreachable dock: a pane on a host that does not answer draws no textbox, queue, control or button under its reader", async () => {
  await withApp(rowsFrom([pane("%5", true)], []), status(false), ({ container }) => {
    const dock = dockOf(container);
    assertNone(reader(container).querySelector("textarea"), "no textbox is drawn");
    assertNone(dock.querySelector("button"), "the dock draws no button");
    assertNone(dock.querySelector(".tray"), "the dock draws no queue");
    assertNone(dock.querySelector(".statebar .end"), "the dock draws no control");
    assertNone(dock.querySelector(".dock-waits"), "the dock draws no waits line");
  });
});

test("HostsWebUnreachable dock: a headless session on a host that does not answer draws no textbox, queue, control or button under its reader", async () => {
  await withApp(rowsFrom([], [], [session("s-1")]), status(false), ({ container }) => {
    const dock = dockOf(container);
    assertNone(reader(container).querySelector("textarea"), "no textbox is drawn");
    assertNone(dock.querySelector("button"), "the dock draws no button");
    assertNone(dock.querySelector(".tray"), "the dock draws no queue");
    assertNone(dock.querySelector(".statebar .end"), "the dock draws no control");
    assertNone(dock.querySelector(".dock-waits"), "the dock draws no waits line");
  });
});

test("HostsWebUnreachable dock: keys aimed at a row on a host that does not answer make no send, queue, interrupt or resume call", async () => {
  await withApp(rowsFrom([pane("%5", true)], []), status(false), async ({ container, calls }) => {
    const before = calls.paths.length;
    await act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "i", cancelable: true }));
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", cancelable: true }));
    });
    assert.equal(dockOf(container).textContent, "unreachable: den does not answer", "the dock is still its line");
    assert.deepEqual(
      calls.paths.slice(before).filter((path) => writes.includes(path)),
      [],
      "no send, queue, interrupt or resume call is made",
    );
  });
});

test("HostsWebUnreachable dock: a pane on a host that answers keeps its input box and draws no unreachable title or line", async () => {
  await withApp(rowsFrom([pane("%5", false)], []), status(true), ({ container }) => {
    assert.equal(titleRowOf(container).querySelector(".state"), null, "the title row names no state");
    assert.notEqual(reader(container).querySelector("textarea"), null, "the input box is drawn");
    assertNone(reader(container).querySelector(".unreachable-dock"), "no unreachable line is drawn");
  });
});
