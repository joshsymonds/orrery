import assert from "node:assert/strict";
import { test } from "node:test";

import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";

import { createClients, createTransport } from "./client";
import { EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import {
  conversationEnded,
  steerCallsOf,
  steerChoices,
  steerFailureOf,
  steersLive,
} from "./steer";

// Synthetic rows only: made-up ids, directories and states.
function makeRow(init: Partial<Row>): Row {
  return {
    tool: "",
    key: "pane:box:%3",
    id: "%3",
    archiveId: "conv-1",
    kind: "pane",
    label: "api",
    project: "/work/api",
    host: "box",
    state: "waiting",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "session",
    ...init,
  };
}

const repository = "/work/api";

test("SteerMenu: steerChoices marks the steerer as current and offers, in order, only the live in-project conversations", () => {
  const rows: Row[] = [
    makeRow({ key: "pane:box:%2", id: "%2", archiveId: "conv-a", project: "/work/api", state: "waiting" }),
    makeRow({ key: "session:sess-steer", id: "sess-steer", archiveId: "sess-steer", kind: "session", archiveKind: "headless", project: "/work/api", state: "working" }),
    makeRow({ key: "pane:box:%3", id: "%3", archiveId: "conv-b", project: "/work/api.worktrees/feature", state: "working" }),
    makeRow({ key: "session:sess-sub", id: "sess-sub", archiveId: "sess-sub", kind: "session", archiveKind: "headless", project: "/work/api/cmd/tool", state: "working" }),
    makeRow({ key: "session:sess-stopped", id: "sess-stopped", archiveId: "sess-stopped", kind: "session", archiveKind: "headless", project: "/work/api", state: "stopped" }),
    makeRow({ key: "session:sess-failed", id: "sess-failed", archiveId: "sess-failed", kind: "session", archiveKind: "headless", project: "/work/api", state: "failed" }),
    makeRow({ key: "pane:box:%4", id: "%4", archiveId: "", archiveKind: "none", project: "/work/api", state: "idle" }),
    makeRow({ key: "pane:box:%5", id: "%5", archiveId: "conv-empty", project: "/work/api", state: "" }),
    makeRow({ key: "pane:box:%6", id: "%6", archiveId: "conv-archived", project: "/work/api", state: "working", archived: true }),
    makeRow({ key: "session:sess-list", id: "sess-list", archiveId: "sess-list", kind: "session", archiveKind: "session", project: "", state: "working" }),
    makeRow({ key: "pane:box:%7", id: "%7", archiveId: "conv-other", project: "/work/other", state: "working" }),
    makeRow({ key: "pane:box:%8", id: "%8", archiveId: "conv-prefix", project: "/work/apiary", state: "working" }),
  ];
  const choices = steerChoices(rows, "sess-steer", repository);
  assert.equal(choices.current?.archiveId, "sess-steer");
  assert.deepEqual(
    choices.others.map((row) => row.archiveId),
    ["conv-a", "conv-b", "sess-sub"],
  );
});

test("SteerMenu: steerFailureOf reads Unavailable, DeadlineExceeded, Unknown and a non-Connect error as unanswered", () => {
  for (const error of [
    new ConnectError("try later", Code.Unavailable),
    new ConnectError("too slow", Code.DeadlineExceeded),
    new ConnectError("unknown", Code.Unknown),
    new Error("fetch failed"),
  ]) {
    assert.deepEqual(steerFailureOf(error), { kind: "unanswered" });
  }
});

test("SteerMenu: steerFailureOf reads other Connect codes as refused with orreryd's raw message", () => {
  for (const code of [Code.FailedPrecondition, Code.InvalidArgument]) {
    assert.deepEqual(steerFailureOf(new ConnectError("epic is released", code)), {
      kind: "refused",
      reason: "epic is released",
    });
  }
});

test("SteerMenu: steersLive is true for a live orreryd epic only", () => {
  const epic = (init: { live: boolean; source: string }) =>
    create(EpicsGetResponseSchema, { epic: create(EpicSchema, { id: "orrery-1/planner", ...init }) });
  assert.equal(steersLive(undefined), false);
  assert.equal(steersLive(epic({ live: true, source: "orreryd" })), true);
  assert.equal(steersLive(epic({ live: false, source: "orreryd" })), false);
  assert.equal(steersLive(epic({ live: true, source: "record" })), false);
});

// fakeClients records each request the real Clients make, and answers with reply for its path.
function fakeClients(reply: (path: string) => unknown = () => ({})) {
  const calls: { path: string; body: unknown }[] = [];
  const fakeFetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const text = await request.text();
    calls.push({ path, body: text === "" ? undefined : JSON.parse(text) });
    return new Response(JSON.stringify(reply(path)), { headers: { "content-type": "application/json" } });
  };
  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch });
  return { clients: createClients(transport), calls };
}

test("SteerMenu: steerCallsOf sends each call once with the given ids", async () => {
  const { clients, calls } = fakeClients((path) => {
    if (path === "/orrery.v1.Epics/Continue") {
      return { session: "sess-new" };
    }
    if (path === "/orrery.v1.Epics/Get") {
      return { epic: { id: "orrery-1/planner", steering: "sess-2" } };
    }
    return {};
  });
  const steer = steerCallsOf(clients);

  await steer.move("orrery-1/planner", "sess-2");
  assert.deepEqual(calls, [
    { path: "/orrery.v1.Epics/Move", body: { id: "orrery-1/planner", session: "sess-2" } },
  ]);

  const started = await steer.continue("orrery-1/planner");
  assert.equal(started, "sess-new");
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[1], { path: "/orrery.v1.Epics/Continue", body: { id: "orrery-1/planner" } });

  const detail = await steer.get("orrery-1/planner");
  assert.equal(detail.epic?.steering, "sess-2");
  assert.equal(calls.length, 3);
  assert.deepEqual(calls[2], { path: "/orrery.v1.Epics/Get", body: { id: "orrery-1/planner" } });
});

test("EndedBar: conversationEnded is true for a stopped, failed or unlisted steerer, and false for a listed live one and for empty steering", () => {
  const rows: Row[] = [
    makeRow({ key: "session:sess-live", id: "sess-live", archiveId: "sess-live", kind: "session", archiveKind: "headless", project: "/work/api", state: "working" }),
    makeRow({ key: "session:sess-stopped", id: "sess-stopped", archiveId: "sess-stopped", kind: "session", archiveKind: "headless", project: "/work/api", state: "stopped" }),
    makeRow({ key: "session:sess-failed", id: "sess-failed", archiveId: "sess-failed", kind: "session", archiveKind: "headless", project: "/work/api", state: "failed" }),
    makeRow({ key: "pane:box:%4", id: "%4", archiveId: "", archiveKind: "none", project: "/work/api", state: "idle" }),
  ];
  assert.equal(conversationEnded(rows, "sess-live"), false);
  assert.equal(conversationEnded(rows, "sess-stopped"), true);
  assert.equal(conversationEnded(rows, "sess-failed"), true);
  assert.equal(conversationEnded(rows, "sess-gone"), true);
  assert.equal(conversationEnded(rows, ""), false);
});
