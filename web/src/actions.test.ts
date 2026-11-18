import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";

import { answer, interrupt, markSeen, newPromptId, pullQueued, send, withdraw } from "./actions";
import { createClients, createTransport } from "./client";
import type { Conversation } from "./conversation";
import { ConversationStateSchema, OpenRequestSchema } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";

// Synthetic fixtures only: a pane and a headless session with made-up ids and text.
const paneRow: Row = {
  key: "pane:%3",
  id: "%3",
  archiveId: "",
  kind: "pane",
  label: "api",
  project: "api",
  host: "box",
  state: "waiting",
  summary: "",
  question: "Proceed?",
  questionHash: "qh-1",
  changed: 40,
  seen: 10,
  unseen: true,
  archived: false,
  archiveKind: "none",
};

const sessionRow: Row = {
  key: "session:s1",
  id: "s1",
  archiveId: "s1",
  kind: "session",
  label: "notes",
  project: "",
  host: "box",
  state: "waiting",
  summary: "",
  question: "Proceed?",
  questionHash: "",
  changed: 30,
  seen: 30,
  unseen: true,
  archived: false,
  archiveKind: "session",
};

const openConversation: Conversation = {
  transcript: "",
  seq: 0n,
  items: [],
  oldest: 0n,
  more: false,
  unfinished: [],
  queued: [],
  unsent: [],
  state: create(ConversationStateSchema, {
    request: create(OpenRequestSchema, {
      id: "req-7",
      kind: "question",
      question: "Proceed?",
      options: ["Yes", "No", "Maybe"],
    }),
  }),
};

interface Call {
  path: string;
  header: string | null;
  body: unknown;
}

const pullPath = "/orrery.v1.Panes/PullQueued";

function okJSON(body: unknown): Response {
  return new Response(JSON.stringify(body), { headers: { "content-type": "application/json" } });
}

// fakeClients records every call that reaches it and answers with reply, so a test sees each request's path and body.
function fakeClients(reply: (path: string) => Response = (path) => okJSON(path === pullPath ? { text: "queued" } : {})) {
  const calls: Call[] = [];
  const fakeFetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const text = await request.text();
    calls.push({
      path,
      header: request.headers.get("X-Orrery-Client"),
      body: text === "" ? undefined : JSON.parse(text),
    });
    return reply(path);
  };
  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch });
  return { clients: createClients(transport), calls };
}

test("markSeen on a pane calls Panes.MarkSeen with the pane id and no read-up-to", async () => {
  const { clients, calls } = fakeClients();
  await markSeen(clients, paneRow);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/orrery.v1.Panes/MarkSeen");
  assert.deepEqual(calls[0].body, { pane: "%3" });
  assert.ok(calls[0].header?.trim(), "the client header is set");
});

test("markSeen on a session calls Control.Seen with the session id", async () => {
  const { clients, calls } = fakeClients();
  await markSeen(clients, sessionRow);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/orrery.v1.Control/Seen");
  assert.deepEqual(calls[0].body, { id: "s1" });
});

test("answer on a waiting pane sends the digit as its one key, expecting the row's question hash", async () => {
  const { clients, calls } = fakeClients();
  const sent = await answer(clients, paneRow, null, 2);
  assert.equal(sent, true);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/orrery.v1.Panes/SendKeys");
  assert.deepEqual(calls[0].body, { pane: "%3", keys: ["2"], expectQuestion: "qh-1" });
});

test("answer on a session names the open request and the option at the digit's position", async () => {
  const { clients, calls } = fakeClients();
  const sent = await answer(clients, sessionRow, openConversation, 3);
  assert.equal(sent, true);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/orrery.v1.Control/Answer");
  assert.deepEqual(calls[0].body, { id: "s1", requestId: "req-7", answer: "Maybe" });
});

test("answer on a pane that is not waiting sends nothing and resolves false", async () => {
  const { clients, calls } = fakeClients();
  const sent = await answer(clients, { ...paneRow, state: "working" }, null, 1);
  assert.equal(sent, false);
  assert.equal(calls.length, 0);
});

test("answer on a waiting pane with an empty question hash sends nothing and resolves false", async () => {
  const { clients, calls } = fakeClients();
  const sent = await answer(clients, { ...paneRow, questionHash: "" }, null, 1);
  assert.equal(sent, false);
  assert.equal(calls.length, 0);
});

test("answer on a session with no conversation sends nothing and resolves false", async () => {
  const { clients, calls } = fakeClients();
  const sent = await answer(clients, sessionRow, null, 1);
  assert.equal(sent, false);
  assert.equal(calls.length, 0);
});

test("answer on a session with no open request sends nothing and resolves false", async () => {
  const { clients, calls } = fakeClients();
  const noRequest: Conversation = { ...openConversation, state: create(ConversationStateSchema, {}) };
  const sent = await answer(clients, sessionRow, noRequest, 1);
  assert.equal(sent, false);
  assert.equal(calls.length, 0);
});

test("answer on a session whose request has no option at the digit's position sends nothing and resolves false", async () => {
  const { clients, calls } = fakeClients();
  const twoOptions: Conversation = {
    ...openConversation,
    state: create(ConversationStateSchema, {
      request: create(OpenRequestSchema, { id: "req-7", kind: "question", options: ["Yes", "No"] }),
    }),
  };
  const sent = await answer(clients, sessionRow, twoOptions, 3);
  assert.equal(sent, false);
  assert.equal(calls.length, 0);
});

test("send to a pane calls Panes.SendText with the pane id and the text", async () => {
  const { clients, calls } = fakeClients();
  await send(clients, paneRow, "keep going", "unused-for-panes");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/orrery.v1.Panes/SendText");
  assert.deepEqual(calls[0].body, { pane: "%3", text: "keep going" });
});

test("send to a session calls Control.Say with the given prompt id", async () => {
  const { clients, calls } = fakeClients();
  await send(clients, sessionRow, "keep going", "prompt-1");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/orrery.v1.Control/Say");
  assert.deepEqual(calls[0].body, { id: "s1", text: "keep going", promptId: "prompt-1" });
});

test("pullQueued on a pane returns the text Panes.PullQueued took back", async () => {
  const { clients, calls } = fakeClients();
  const text = await pullQueued(clients, paneRow);
  assert.equal(text, "queued");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, pullPath);
  assert.deepEqual(calls[0].body, { pane: "%3" });
});

test("pullQueued on a session resolves empty and sends nothing", async () => {
  const { clients, calls } = fakeClients();
  const text = await pullQueued(clients, sessionRow);
  assert.equal(text, "");
  assert.equal(calls.length, 0);
});

test("withdraw on a session calls Control.Withdraw with every held message and resolves their texts in order", async () => {
  const { clients, calls } = fakeClients(() => okJSON({ texts: ["first", "second"] }));
  const texts = await withdraw(clients, sessionRow);
  assert.deepEqual(texts, [], "a session resolves none");
  assert.equal(calls.length, 0, "a session makes no withdraw call: interrupt takes its queued messages back");
});

test("withdraw on a session with nothing held resolves no texts", async () => {
  const { clients } = fakeClients(() => okJSON({}));
  assert.deepEqual(await withdraw(clients, sessionRow), []);
});

test("withdraw on a pane resolves its pulled text as one message, and none when the pull is empty", async () => {
  const { clients, calls } = fakeClients();
  assert.deepEqual(await withdraw(clients, paneRow), ["queued"]);
  assert.equal(calls[0].path, pullPath);
  assert.deepEqual(calls[0].body, { pane: "%3" });
  const empty = fakeClients(() => okJSON({ text: "" }));
  assert.deepEqual(await withdraw(empty.clients, paneRow), []);
});

test("interrupt on a session calls Control.Interrupt with the session id", async () => {
  const { clients, calls } = fakeClients(() => okJSON({ texts: ["first", "second"] }));
  assert.deepEqual(await interrupt(clients, sessionRow), ["first", "second"], "the texts it cancelled, in order");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/orrery.v1.Control/Interrupt");
  assert.deepEqual(calls[0].body, { id: "s1" });
});

test("interrupt on a pane sends the Escape key to it, and no question is expected", async () => {
  const { clients, calls } = fakeClients();
  await interrupt(clients, paneRow);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/orrery.v1.Panes/SendKeys");
  assert.deepEqual(calls[0].body, { pane: "%3", keys: ["Escape"] });
});

test("a failed call rejects and is not repeated", async () => {
  const { clients, calls } = fakeClients(
    () => new Response(JSON.stringify({ code: "internal", message: "boom" }), {
      status: 500,
      headers: { "content-type": "application/json" },
    }),
  );
  await assert.rejects(send(clients, paneRow, "keep going", "unused-for-panes"));
  assert.equal(calls.length, 1);
});

test("newPromptId returns a fresh UUID each time", () => {
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
  const first = newPromptId();
  const second = newPromptId();
  assert.match(first, uuid);
  assert.match(second, uuid);
  assert.notEqual(first, second);
});
