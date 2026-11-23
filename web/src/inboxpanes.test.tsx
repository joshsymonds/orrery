import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type Pane, PaneSchema } from "./gen/orrery/v1/orrery_pb";
import { rowsFrom } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the token InboxWebPanes: a pane with neither a bound conversation nor an unbound mark draws no
// row, and an unbound pane draws the broken icon and the line that says how to restart it. The panes are invented, rowsFrom
// makes their rows, and the app draws them; each check reads the DOM.

const now = 1000;
const serving = "box";

// quietFetch answers the app's calls without data: a unary call with an empty object, and a stream with a body that
// stays open until its call aborts, so no conversation arrives and no timer runs.
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

// pane is an invented pane of the serving host in one directory, seen after it changed, so no dwell timer runs.
function pane(init: MessageInitShape<typeof PaneSchema>): Pane {
  return create(PaneSchema, {
    host: serving,
    session: "work",
    cwd: "/work/app",
    changed: 940n,
    seen: 1000n,
    ...init,
  });
}

// withPanes mounts the app over the rows rowsFrom makes of the panes, in a fresh DOM, and runs check on its container. The
// app is unmounted and the DOM removed when the test ends, or as soon as signal aborts.
async function withPanes(
  signal: AbortSignal,
  panes: Pane[],
  check: (container: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
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
      render(<App rows={rowsFrom(panes, [], [])} now={now} clients={clients} />, container);
    });
    check(container);
  } finally {
    await teardown();
  }
}

// rowKeys is the key of each row the list draws, in order.
function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".row[data-key]")].map((element) => element.getAttribute("data-key") ?? "");
}

test("InboxWebPanes: a pane with neither a bound conversation nor an unbound mark draws no row, and a bound pane beside it does", (t) =>
  withPanes(
    t.signal,
    [
      pane({ id: "%23", command: "zsh", state: "", summary: "zsh" }),
      pane({ id: "%3", command: "claude", claudeSession: "claude-1", state: "idle", summary: "Plan the import" }),
    ],
    (container) => {
      assert.deepEqual(rowKeys(container), ["pane:box:%3"], "only the bound pane draws a row");
      assert.equal(container.querySelector('[data-key="pane:box:%23"]'), null, "the pane with no conversation draws no row");
    },
  ));

test("InboxWebPanes: an unbound pane draws its row with the broken icon and the line telling you to restart it with claude --resume", (t) =>
  withPanes(t.signal, [pane({ id: "%41", command: "claude", unbound: true, state: "", summary: "orphan shell" })], (container) => {
    const row = container.querySelector('[data-key="pane:box:%41"]');
    assert.ok(row !== null, "the unbound pane draws its row");
    assert.equal(row.querySelector("svg.glyph")?.getAttribute("data-state"), "broken", "the unbound pane draws the broken icon");
    const line = row.querySelector(".l2");
    assert.equal(
      line?.textContent,
      "Not bound to a conversation. Restart it with claude --resume.",
      "the second line tells you to restart it with claude --resume",
    );
    assert.equal(line?.querySelector("code")?.textContent, "claude --resume", "the command is set in a code element");
  }));
