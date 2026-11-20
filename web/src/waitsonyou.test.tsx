import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Epic, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
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

// row is a seen working row unless the init says otherwise, so no dwell timer runs on the cursor's row.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
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
    archiveId: "",
    ...init,
  };
}

// waitingRow is a pane row that waits on an answer to Ship it?, with its owner's put-off as given.
function waitingRow(putOff: boolean): Row {
  return row({ key: "a", label: "api", project: "/work/api", state: "waiting", question: "Ship it?", putOff, changed: 300, seen: 300 });
}

const widgetsRepo = "widgets-5d0c1a7";

// liveEpic is a live epic whose detail asks one question, so it raises one need.
function liveEpic(): { epics: EpicsView } {
  const epic: Epic = create(EpicSchema, {
    id: `${widgetsRepo}/first`,
    repository: widgetsRepo,
    slug: "first",
    outcome: "",
    live: true,
  });
  const detail = create(EpicsGetResponseSchema, { epic, questions: [{ id: "Q1", text: "Which way?" }] });
  return { epics: epicsView([epic], new Map([[epic.id, detail]])) };
}

// markCount is the count the mark crumb carries, or null when it carries none.
function markCount(container: HTMLElement): string | null {
  return container.querySelector(".mark-crumb .needs")?.textContent ?? null;
}

// withInbox mounts the app over quietFetch in a fresh DOM with the rows and epics, and runs check. The app is unmounted
// and the DOM removed when the test ends, or as soon as signal aborts.
async function withInbox(
  signal: AbortSignal,
  props: { rows: Row[]; epics?: EpicsView },
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
      render(<App rows={props.rows} now={now} clients={clients} epics={props.epics} />, container);
    });
    await check(container);
  } finally {
    await teardown();
  }
}

test("WaitsOnYou: the mark counts a put-off waiting row out, and the live epic's need in", (t) =>
  withInbox(t.signal, { rows: [waitingRow(true)], ...liveEpic() }, (container) => {
    assert.equal(markCount(container), "1", "only the epic's need: the put-off row does not need you");
  }));

test("WaitsOnYou: the mark counts the same waiting row again once its question is not put off", (t) =>
  withInbox(t.signal, { rows: [waitingRow(false)], ...liveEpic() }, (container) => {
    assert.equal(markCount(container), "2", "the waiting row and the epic's need");
  }));

test("WaitsOnYou: an archived waiting row leaves the mark number unchanged", (t) =>
  withInbox(t.signal, { rows: [{ ...waitingRow(false), archived: true, archiveKind: "session", archiveId: "sess-a" }], ...liveEpic() }, (container) => {
    assert.equal(markCount(container), "1", "only the epic's need: the archived row does not need you");
  }));
