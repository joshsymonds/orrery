import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { conversationAsk, type AskModel, type AskOption } from "./askmodel";
import { answeredOf, askKey, askView, observe, press, settle, type AskState } from "./askstate";
import type { Conversation } from "./conversation";
import {
  ConversationStateSchema,
  HeadlessSessionSchema,
  OpenRequestSchema,
  PaneSchema,
  type ConversationState,
  type OpenRequest,
  type Pane,
  type HeadlessSession,
} from "./gen/orrery/v1/orrery_pb";
import { rowsFrom, type Row } from "./rows";

const refusedWords = "Not answered: the question changed before your answer arrived. This is what it asks now.";
// newestKey is the key of the item newest when the test presses, which every press here is made after.
const newestKey = "item-newest";

function paneRow(init: MessageInitShape<typeof PaneSchema>): Row {
  const pane: Pane = create(PaneSchema, {
    id: "%3",
    host: "box",
    session: "work",
    cwd: "/work/api",
    claudeSession: "claude-1",
    state: "waiting",
    summary: "Plan the import",
    changed: 1794700000n,
    ...init,
  });
  return rowsFrom([pane], [])[0];
}

function headlessRow(): Row {
  const session: HeadlessSession = create(HeadlessSessionSchema, {
    id: "a1b2c3d4e5f60718293a4b5c6d7e8f90",
    dir: "/work/tidepool",
    headline: "Why the roster page is slow",
    state: "waiting",
    changed: 1794700100n,
  });
  return rowsFrom([], [], [session])[0];
}

function conversation(request: OpenRequest): Conversation {
  const state: ConversationState = create(ConversationStateSchema, { request });
  return { transcript: "", seq: 0n, items: [], oldest: 0n, more: false, state, unfinished: [], queued: [], unsent: [] };
}

function requestOf(init: MessageInitShape<typeof OpenRequestSchema>): OpenRequest {
  return create(OpenRequestSchema, init);
}

// askedOf is a row's Ask, which every test here expects to exist.
function askedOf(row: Row, conversationOf: Conversation | null): AskModel {
  const model = conversationAsk(row, conversationOf);
  if (model === null) {
    throw new Error("the row has no Ask");
  }
  return model;
}

// keyOf is the key of a row's Ask, which every test here expects to have a target.
function keyOf(row: Row, model: AskModel): string {
  const key = askKey(row, model);
  if (key === null) {
    throw new Error("the Ask has no answer target");
  }
  return key;
}

// startedOf is the answer a press starts from an Ask that can be pressed, which every test here expects to send.
function startedOf(
  state: AskState,
  key: string,
  option: AskOption,
  question: string,
  after: string | null = newestKey,
): AskState {
  const started = press(state, key, option, question, after);
  if (started === null) {
    throw new Error("the press sent nothing");
  }
  return started;
}

// paneModel is a waiting pane's Ask with its row, so the test can name the question by its key.
function paneModel(questionHash: string): { row: Row; model: AskModel } {
  const row = paneRow({ question: "Allow the edit to plan.md?", questionHash });
  return { row, model: askedOf(row, null) };
}

// headlessModel is a headless session's Ask with two options, whose request is req-1.
function headlessModel(): { row: Row; model: AskModel } {
  const row = headlessRow();
  const request = requestOf({ id: "req-1", kind: "question", question: "Which fix should I make?", options: ["One query", "Cache keepers"] });
  return { row, model: askedOf(row, conversation(request)) };
}

test("AskAnswer: a press sends the picked option's number and names its question", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);

  const state = startedOf({ phase: "idle" }, key, model.options[1], model.question);

  assert.deepEqual(state, {
    phase: "sending",
    key,
    n: 2,
    name: model.options[1].name,
    question: model.question,
    after: newestKey,
  });
});

test("AskAnswer: a second press while the answer is sending sends nothing, for the same question", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const sending = startedOf({ phase: "idle" }, key, model.options[0], model.question);

  assert.equal(press(sending, key, model.options[2], model.question, newestKey), null);
});

test("AskAnswer: a press while another question's answer is sending sends nothing", () => {
  const first = paneModel("h1");
  const second = paneModel("h2");
  const sending = startedOf({ phase: "idle" }, keyOf(first.row, first.model), first.model.options[0], first.model.question);

  assert.equal(press(sending, keyOf(second.row, second.model), second.model.options[0], second.model.question, newestKey), null);
});

test("AskAnswer: a press after the server took the answer sends nothing while the Ask still shows sending", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const sent = settle(startedOf({ phase: "idle" }, key, model.options[0], model.question), true);

  assert.equal(press(sent, key, model.options[1], model.question, newestKey), null);
});

test("AskAnswer: an answer keeps the key of the item newest at its press through sent and answered", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const pressedAfter = "item-at-press";

  const sent = settle(startedOf({ phase: "idle" }, key, model.options[0], model.question, pressedAfter), true);
  assert.deepEqual(sent, { phase: "sent", key, n: 1, name: model.options[0].name, question: model.question, after: pressedAfter });

  const observed = observe(sent, null);
  assert.deepEqual(answeredOf(observed), { n: 1, name: model.options[0].name, question: model.question, after: pressedAfter });
});

test("AskAnswer: an answer pressed with no item drawn keeps a null key", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);

  const answered = observe(settle(startedOf({ phase: "idle" }, key, model.options[0], model.question, null), true), null);

  assert.equal(answeredOf(answered)?.after, null);
});

test("AskAnswer: a refused answer leaves the question open with the refused-answer words", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const refused = settle(startedOf({ phase: "idle" }, key, model.options[0], model.question), false);

  assert.equal(refused.phase, "error");
  assert.deepEqual(askView(refused, key), { phase: "error", picked: null, note: refusedWords });
  assert.equal(answeredOf(refused), null);
});

test("AskAnswer: a press is allowed again on the same question after a refused answer", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const refused = settle(startedOf({ phase: "idle" }, key, model.options[0], model.question), false);

  assert.deepEqual(press(refused, key, model.options[1], model.question, newestKey), {
    phase: "sending",
    key,
    n: 2,
    name: model.options[1].name,
    question: model.question,
    after: newestKey,
  });
});

test("AskAnswer: an answer the server took stays sending while the stream still shows its question", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const sent = settle(startedOf({ phase: "idle" }, key, model.options[1], model.question), true);

  const observed = observe(sent, key);

  assert.deepEqual(askView(observed, key), { phase: "sending", picked: 2, note: "" });
  assert.equal(answeredOf(observed), null);
});

test("AskAnswer: an answer folds to its answered line once the stream shows no question", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const sent = settle(startedOf({ phase: "idle" }, key, model.options[1], model.question), true);

  const observed = observe(sent, null);

  assert.deepEqual(answeredOf(observed), { n: 2, name: model.options[1].name, question: model.question, after: newestKey });
});

test("AskAnswer: an answer folds once the stream shows a different question", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const sent = settle(startedOf({ phase: "idle" }, key, model.options[0], model.question), true);
  const other = paneModel("h2");

  const observed = observe(sent, keyOf(other.row, other.model));

  assert.deepEqual(answeredOf(observed), { n: 1, name: model.options[0].name, question: model.question, after: newestKey });
  assert.deepEqual(askView(observed, keyOf(other.row, other.model)), { phase: "open", picked: null, note: "" });
});

test("AskAnswer: an answered line stays while nothing shows and goes idle when a different question shows", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const answered = observe(settle(startedOf({ phase: "idle" }, key, model.options[0], model.question), true), null);
  const other = paneModel("h2");

  assert.deepEqual(answeredOf(observe(answered, null)), { n: 1, name: model.options[0].name, question: model.question, after: newestKey });
  assert.deepEqual(observe(answered, keyOf(other.row, other.model)), { phase: "idle" });
});

test("AskAnswer: a refused answer clears to idle when the question changes", () => {
  const { row, model } = paneModel("h1");
  const key = keyOf(row, model);
  const refused = settle(startedOf({ phase: "idle" }, key, model.options[0], model.question), false);
  const other = paneModel("h2");

  assert.deepEqual(observe(refused, keyOf(other.row, other.model)), { phase: "idle" });
});

test("AskAnswer: askView for another question is open with nothing picked", () => {
  const { row, model } = paneModel("h1");
  const sending = startedOf({ phase: "idle" }, keyOf(row, model), model.options[0], model.question);
  const other = paneModel("h2");

  assert.deepEqual(askView(sending, keyOf(other.row, other.model)), { phase: "open", picked: null, note: "" });
});

test("AskAnswer: settle changes nothing when no answer is sending", () => {
  assert.deepEqual(settle({ phase: "idle" }, false), { phase: "idle" });
});

test("AskAnswer: a pane's key names its question hash and a headless session's key is its request id", () => {
  const first = paneModel("h1");
  const second = paneModel("h2");
  const headless = headlessModel();

  assert.notEqual(keyOf(first.row, first.model), keyOf(second.row, second.model));
  assert.equal(keyOf(headless.row, headless.model), "req-1");
});

test("AskAnswer: a headless session's answer is sent for its open request", () => {
  const { row, model } = headlessModel();
  const key = keyOf(row, model);
  const state: AskState = startedOf({ phase: "idle" }, key, model.options[1], model.question);

  assert.deepEqual(state, { phase: "sending", key: "req-1", n: 2, name: "Cache keepers", question: model.question, after: newestKey });
});
