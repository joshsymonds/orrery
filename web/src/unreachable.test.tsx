import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { EpicRow } from "./epicrow";
import { blockOf } from "./epics";
import { liveEpoch } from "./fixtures/clock";
import {
  type Epic,
  EpicSchema,
  type EpicStep,
  EpicStepSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import {
  HeadlessSessionSchema,
  HostEntrySchema,
  type HostStatusResponse,
  HostStatusResponseSchema,
  type Pane,
  PaneSchema,
} from "./gen/orrery/v1/orrery_pb";
import { type HostReach, reachOf } from "./hostreach";
import { InboxRow, type RowMore } from "./inboxrow";
import { NowSection } from "./nowsection";
import { type Row, rowsFrom } from "./rows";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { styleDeclaration } from "./teststyle";
import type { EpicNode } from "./tree";

// now is the clock the rows read their times against, in unix seconds: the fixtures' live instant, so no test reads the
// wall clock or runs a timer of its own.
const now = Date.parse(liveEpoch) / 1000;

// serving is the host this server runs on, which answers; den is the host whose answer each test sets. Here den does not
// answer.
const serving = "halyard";
const den = "den";

// status is the host status the tests read: the serving host, which answers, and den, which does not.
const status: HostStatusResponse = create(HostStatusResponseSchema, {
  host: serving,
  stats: [],
  hosts: [
    create(HostEntrySchema, { name: serving, reachable: true }),
    create(HostEntrySchema, { name: den, reachable: false }),
  ],
});

// reach is the reach of each host the status lists, as the App holds it.
const reach: HostReach = reachOf(status);

const noop = () => {};

// more is a row's More menu, shut, with no items.
const more: RowMore = { open: false, items: [], toggle: noop, close: noop };

// pane is an invented pane on host, running in a directory, whose Pane.unreachable is given and whose state is stopped, so
// a row that read its state would say stopped where it must say unreachable.
function pane(id: string, host: string, unreachable: boolean): Pane {
  return create(PaneSchema, {
    id,
    host,
    session: "work",
    cwd: `/srv/${host}/app`,
    claudeSession: `claude-${id}`,
    state: "stopped",
    summary: `pane ${id}`,
    changed: BigInt(now - 60),
    seen: BigInt(now - 60),
    unreachable,
  });
}

// paneRow is the list row of the invented pane.
function paneRow(id: string, host: string, unreachable: boolean): Row {
  return rowOf(rowsFrom([pane(id, host, unreachable)], []));
}

// sessionRow is the list row of an invented headless session on host, whose state is stopped.
function sessionRow(id: string, host: string): Row {
  return rowOf(
    rowsFrom(
      [],
      [],
      [
        create(HeadlessSessionSchema, {
          id,
          host,
          dir: `/srv/${host}/app`,
          headline: `session ${id}`,
          state: "stopped",
          changed: BigInt(now - 60),
          seen: BigInt(now - 60),
        }),
      ],
    ),
  );
}

// rowOf is the one row a rowsFrom call made.
function rowOf(rows: Row[]): Row {
  const [row] = rows;
  assert.ok(row !== undefined, "one row is made");
  return row;
}

// step is an invented running step of the epic, on host, labelled by its purpose.
function step(id: string, host: string, purpose: string): EpicStep {
  return create(EpicStepSchema, {
    id,
    purpose,
    host,
    agent: "builder",
    end: "",
    doing: `${purpose} is under way`,
    started: BigInt(now - 120),
    stageStarted: BigInt(now - 60),
  });
}

// epicOn is an invented live epic on host whose running steps are steps.
function epicOn(host: string, steps: EpicStep[]): Epic {
  return create(EpicSchema, {
    id: "harbour/beacon",
    repository: "harbour",
    slug: "beacon",
    source: "orreryd",
    live: true,
    phaseAt: BigInt(now - 600),
    host,
    runningSteps: steps,
  });
}

// detailOf is the epic's Get answer: the epic with its steps, each of which is also one of its running steps.
function detailOf(listed: Epic): EpicsGetResponse {
  return create(EpicsGetResponseSchema, { epic: listed, steps: listed.runningSteps });
}

// epicNode hangs the listed epic under its conversation as the tree does, with its block read from its detail and the
// reach the tree gives it.
function epicNode(listed: Epic, hostReach: HostReach): EpicNode {
  return { kind: "epic", key: `epic:${listed.id}`, block: blockOf(listed, detailOf(listed)), listed, contract: null, reach: hostReach };
}

// sectionProps are the Now section's props for the detail, with no-op actions and the reach given.
function sectionProps(detail: EpicsGetResponse, hostReach: HostReach): Parameters<typeof NowSection>[0] {
  return {
    detail,
    now,
    narrow: false,
    numberKeys: false,
    reach: hostReach,
    onAnswer: async () => "",
    onRetry: noop,
    onStop: noop,
    onResume: noop,
  };
}

// drawn draws element in a fresh DOM, runs check on the container, and removes the DOM again.
async function drawn(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(element, container);
    });
    check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

// assertUnreachable checks a row, an epic or a step says unreachable: it draws the unreachable glyph as a dashed ring, its
// word is unreachable, and it never says stopped.
function assertUnreachable(root: Element, what: string): void {
  assert.equal(root.querySelector(".glyph")?.getAttribute("data-state"), "unreachable", `${what} draws the unreachable glyph`);
  assert.notEqual(one(root, ".glyph circle").getAttribute("stroke-dasharray"), null, `${what} draws a dashed ring`);
  assert.equal(root.querySelector(".unreach")?.textContent, "unreachable", `${what} says unreachable`);
  assertNone(root.querySelector('[data-state="stopped"]'), `${what} does not draw stopped's glyph`);
  assert.equal((root.textContent ?? "").includes("stopped"), false, `${what} does not say stopped`);
}

// one returns the element under root that matches selector, which the drawing must hold.
function one(root: ParentNode, selector: string): Element {
  const found = root.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// recording is the transport the App talks through: the path of each call it made, in order. A stream it opens stays
// open with no message.
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

// withApp mounts the App at a desk width over the rows, with the host status given, in a fresh DOM, and runs check. The
// App is unmounted and the DOM removed when the check ends, or fails.
async function withApp(rows: Row[], check: (container: HTMLElement) => Promise<void> | void): Promise<void> {
  const window = installTestDom();
  window.innerWidth = 1024;
  const container = document.createElement("div");
  document.body.append(container);
  const calls = recording();
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: calls.fetch }));
  try {
    await act(() => {
      render(<App rows={rows} now={now} clients={clients} host={status} />, container);
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

test("HostsWebUnreachable: a pane with Pane.unreachable, and a session, epic and step on the down host, draw unreachable with data-state unreachable and never stopped", async () => {
  const epic = epicOn(den, [step("lamp", den, "lamp")]);
  await drawn(
    <>
      {/* The pane is drawn with no reach, so its Pane.unreachable is the only word that its host does not answer. */}
      <InboxRow row={paneRow("%5", den, true)} now={now} cursor={false} more={more} onOpen={noop} onSeen={noop} />
      <InboxRow row={sessionRow("s-1", den)} now={now} cursor={false} more={more} onOpen={noop} onSeen={noop} reach={reach} />
      <EpicRow hung={epicNode(epic, reach)} now={now} current={false} onSelect={noop} />
      <NowSection {...sectionProps(detailOf(epic), reach)} />
    </>,
    (container) => {
      assertUnreachable(one(container, '[data-key="pane:den:%5"]'), "the pane");
      assertUnreachable(one(container, '[data-key="session:s-1"]'), "the session");
      assertUnreachable(one(container, ".row.epic"), "the epic");
      assertUnreachable(one(container, ".row.step"), "the epic's step");
      assertUnreachable(one(container, ".tbl.steps .tr"), "the Now step");
    },
  );
});

test("HostsWebUnreachable: the unreachable glyph and the word unreachable are in grey ink, not stopped's tone", () => {
  assert.equal(styleDeclaration('.glyph[data-state="unreachable"]', "color"), "var(--o-ink-3)", "the glyph is grey ink");
  assert.equal(styleDeclaration(".unreach", "color"), "var(--o-ink-3)", "the word is grey ink");
});

test("HostsWebUnreachable: a card whose host does not answer says unreachable beside its host in its head", async () => {
  await withApp(rowsFrom([pane("%5", den, false)], []), (container) => {
    const head = one(container, 'section.dir[aria-label="app"] .dir-head');
    const label = one(head, ".host-label.heading");
    assert.equal(label.querySelector(".name")?.textContent, den, "the head names the host");
    assert.equal(label.nextElementSibling?.textContent, "unreachable", "the word unreachable sits beside the host");
    assert.equal(label.nextElementSibling?.classList.contains("unreach"), true);
  });
});

test("HostsWebUnreachable: rows on the reachable host keep their own state and words", async () => {
  const epic = epicOn(serving, [step("horn", serving, "horn")]);
  await drawn(
    <>
      <InboxRow row={paneRow("%7", serving, false)} now={now} cursor={false} more={more} onOpen={noop} onSeen={noop} reach={reach} />
      <EpicRow hung={epicNode(epic, reach)} now={now} current={false} onSelect={noop} />
    </>,
    (container) => {
      const pane = one(container, '[data-key="pane:halyard:%7"]');
      assert.equal(one(pane, ".glyph").getAttribute("data-state"), "stopped", "the pane keeps its own stopped glyph");
      assertNone(pane.querySelector(".unreach"), "the pane says nothing of unreachable");
      const epicRow = one(container, ".row.epic");
      assertNone(epicRow.querySelector(".unreach"), "the epic says nothing of unreachable");
      const stepRow = one(container, ".row.step");
      assert.equal(one(stepRow, ".glyph").getAttribute("data-state"), "working", "the step keeps its own working glyph");
      assert.equal(one(stepRow, ".doing").textContent, "horn is under way", "the step keeps its own words");
    },
  );
});
