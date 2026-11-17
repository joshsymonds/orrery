import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import {
  type Epic,
  EpicSchema,
  type EpicStep,
  EpicStepSchema,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

const now = 1000;

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

// row is a seen row, so no dwell timer runs on the cursor's row.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "box",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    ...init,
  };
}

// fixture has one row that needs you, one working and one done, in attention order.
function fixture(): Row[] {
  return [
    row({
      key: "a",
      label: "api",
      project: "/work/api",
      state: "waiting",
      question: "Ship it?",
      changed: 300,
      seen: 300,
    }),
    row({ key: "b", label: "web", project: "/work/web", state: "working", changed: 200, seen: 200 }),
    row({ key: "c", label: "docs", project: "/work/docs", state: "done", changed: 150, seen: 150 }),
  ];
}

const widgetsRepo = "widgets-5d0c1a7";

function makeEpic(slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${widgetsRepo}/${slug}`,
    repository: widgetsRepo,
    slug,
    outcome: "",
    live: true,
    ...init,
  });
}

function makeStep(id: string): EpicStep {
  return create(EpicStepSchema, { id, task: id, agent: "builder", doing: "", held: false });
}

// withInbox mounts the app over quietFetch in a fresh DOM and runs check. The app is unmounted and the DOM removed when
// the test ends, or as soon as signal aborts, as app.test.tsx's withApp does.
async function withInbox(
  signal: AbortSignal,
  props: { rows?: Row[]; epics?: EpicsView },
  check: (container: HTMLElement) => void | Promise<void>,
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
      render(
        <App rows={props.rows ?? fixture()} error="" now={now} clients={clients} epics={props.epics} />,
        container,
      );
    });
    await check(container);
  } finally {
    await teardown();
  }
}

async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// dispatchOn fires an event of the type on the element, as the browser does for a pointer or a click.
async function dispatchOn(element: Element | null, type: string): Promise<void> {
  assert.ok(element, `the page has an element for ${type}`);
  await act(() => {
    element.dispatchEvent(new Event(type, { bubbles: true }));
  });
}

async function clickRow(container: HTMLElement, key: string): Promise<void> {
  await dispatchOn(container.querySelector(`.b-row[data-key="${key}"]`), "click");
}

// paneState is the inbox state the app draws on the panes: docked, pinned, hidden or flyout.
function paneState(container: HTMLElement): string | null {
  return container.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// rowKeys is the key of each row the list draws, in order. An epic entry is drawn as a line with the row class, but it
// is no row.
function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".b-row")]
    .map((element) => element.getAttribute("data-key") ?? "")
    .filter((key) => !key.startsWith("epic:"));
}

// markCount is the count the mark crumb carries, or null when it carries none.
function markCount(container: HTMLElement): string | null {
  return container.querySelector(".mark-crumb .needs")?.textContent ?? null;
}

test("InboxFlyout: the inbox is docked before a row is opened, and hidden once a click opens one", (t) =>
  withInbox(t.signal, {}, async (container) => {
    assert.equal(paneState(container), "docked");
    await clickRow(container, "b");
    assert.equal(paneState(container), "hidden");
  }));

test("InboxFlyout: Enter on the cursor's row hides the inbox as a click does", (t) =>
  withInbox(t.signal, {}, async (container) => {
    assert.equal(paneState(container), "docked");
    await press("Enter");
    assert.equal(paneState(container), "hidden");
  }));

test("InboxFlyout: the mark's count is the waiting rows not archived plus the live epics' needs, and holds in the project view and under a filter", (t) => {
  const epic = makeEpic("first");
  const detail = create(EpicsGetResponseSchema, { epic, questions: [{ id: "Q1", text: "Which way?" }] });
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  const archived = row({
    key: "s",
    id: "s",
    kind: "session",
    label: "old docs",
    state: "waiting",
    archived: true,
    archiveKind: "session",
  });
  return withInbox(t.signal, { rows: [...fixture(), archived], epics }, async (container) => {
    assert.equal(markCount(container), "2", "one waiting row and one epic need");
    await press("p");
    assert.equal(markCount(container), "2", "the project view");
    await press("/");
    await act(() => {
      const input = container.querySelector<HTMLInputElement>('input[aria-label="filter rows"]');
      assert.ok(input, "the filter input is drawn");
      input.value = "docs";
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    assert.deepEqual(rowKeys(container), ["c"], "the filter hides every row but the done one");
    assert.equal(markCount(container), "2", "under a filter");
  });
});

test("InboxFlyout: a step entry's page draws the mark as its first crumb, carrying the count", (t) => {
  const epic = makeEpic("first", { runningSteps: [makeStep("s1")] });
  const detail = create(EpicsGetResponseSchema, { epic, questions: [{ id: "Q1", text: "Which way?" }] });
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  return withInbox(t.signal, { rows: [], epics }, async (container) => {
    await press("G");
    const reader = 'section[aria-label="reader"] .title-row';
    assert.equal(container.querySelector(`${reader} .crumb.here`)?.textContent, "s1", "the cursor's step is the page");
    assert.equal(container.querySelector(`${reader} .crumbs`)?.firstElementChild?.getAttribute("class"), "mark-crumb");
    assert.equal(markCount(container), "1", "the step's page carries the epic's need on its mark");
  });
});

test("InboxFlyout: hovering the mark pops a hidden inbox out over the page, and the pointer leaving the inbox tucks it away", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    assert.equal(paneState(container), "hidden");
    const mark = container.querySelector(".mark-crumb");
    const list = container.querySelector(".list");
    await dispatchOn(mark, "mouseenter");
    assert.equal(paneState(container), "flyout");
    // The test DOM has no MouseEvent, so the leave carries its related target as a plain property.
    const leave = new Event("mouseleave", { bubbles: true });
    Object.defineProperty(leave, "relatedTarget", { value: list });
    await act(() => {
      mark?.dispatchEvent(leave);
    });
    assert.equal(paneState(container), "flyout", "a pointer moving from the mark into the inbox keeps it out");
    await dispatchOn(list, "mouseleave");
    assert.equal(paneState(container), "hidden", "the pointer leaving the inbox tucks it away");
  }));

test("InboxFlyout: a click on the mark or B pins the inbox beside the page, and doing either again unpins it", (t) =>
  withInbox(t.signal, {}, async (container) => {
    await clickRow(container, "b");
    await dispatchOn(container.querySelector(".mark-crumb"), "click");
    assert.equal(paneState(container), "pinned");
    await dispatchOn(container.querySelector(".mark-crumb"), "click");
    assert.equal(paneState(container), "hidden");
    await press("b");
    assert.equal(paneState(container), "pinned");
    await press("B");
    assert.equal(paneState(container), "hidden");
  }));

test("InboxFlyout: the brand row holds no counts row", (t) =>
  withInbox(t.signal, {}, (container) => {
    assert.equal(container.querySelector(".counts"), null);
  }));
