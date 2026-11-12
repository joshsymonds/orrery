import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  ConversationItemSchema,
  ConversationStateSchema,
  EngineTextSchema,
  SessionsWatchConversationResponseSchema,
  WatchConversationResponseSchema,
  type ConversationItem,
  type ConversationState,
  type EngineText,
  type SessionsWatchConversationResponse,
  type WatchConversationResponse,
} from "./gen/orrery/v1/orrery_pb";
import { applyDelta, type Conversation } from "./conversation";

function makeItem(text: string): ConversationItem {
  return create(ConversationItemSchema, { kind: "assistant", text });
}

function makeState(init: MessageInitShape<typeof ConversationStateSchema>): ConversationState {
  return create(ConversationStateSchema, init);
}

function makeUnfinished(text: string): EngineText {
  return create(EngineTextSchema, { messageId: "m1", index: 0, text });
}

function paneDelta(init: MessageInitShape<typeof WatchConversationResponseSchema>): WatchConversationResponse {
  return create(WatchConversationResponseSchema, init);
}

function sessionDelta(
  init: MessageInitShape<typeof SessionsWatchConversationResponseSchema>,
): SessionsWatchConversationResponse {
  return create(SessionsWatchConversationResponseSchema, init);
}

function texts(conversation: Conversation): string[] {
  return conversation.items.map((item) => item.text);
}

function held(init: Partial<Conversation> = {}): Conversation {
  return {
    transcript: "t1",
    seq: 5n,
    items: [makeItem("old")],
    state: makeState({ goal: "ship" }),
    unfinished: [makeUnfinished("partial")],
    ...init,
  };
}

test("a reset holds the delta's newest keep items, its state, and its unfinished set", () => {
  const next = applyDelta(
    held(),
    paneDelta({
      transcript: "t2",
      seq: 9n,
      reset: true,
      items: [makeItem("a"), makeItem("b"), makeItem("c")],
      state: makeState({ goal: "reset goal" }),
      unfinished: [makeUnfinished("fresh")],
    }),
    2,
  );

  assert.deepEqual(texts(next), ["b", "c"]);
  assert.equal(next.state?.goal, "reset goal");
  assert.deepEqual(
    next.unfinished.map((text) => text.text),
    ["fresh"],
  );
  assert.equal(next.transcript, "t2");
  assert.equal(next.seq, 9n);
});

test("a delta applied to nothing held holds its items, its state, and its unfinished set", () => {
  const next = applyDelta(
    null,
    paneDelta({
      transcript: "t1",
      seq: 3n,
      items: [makeItem("a"), makeItem("b")],
      state: makeState({ goal: "first" }),
      unfinished: [makeUnfinished("open")],
    }),
  );

  assert.deepEqual(texts(next), ["a", "b"]);
  assert.equal(next.state?.goal, "first");
  assert.deepEqual(
    next.unfinished.map((text) => text.text),
    ["open"],
  );
  assert.equal(next.transcript, "t1");
  assert.equal(next.seq, 3n);
});

test("a delta applied to nothing held with no state holds an undefined state", () => {
  const next = applyDelta(null, paneDelta({ transcript: "t1", seq: 1n, items: [makeItem("a")] }));

  assert.equal(next.state, undefined);
  assert.deepEqual(next.unfinished, []);
});

test("a later delta appends its items, trimmed to the newest keep", () => {
  const next = applyDelta(
    held({ items: [makeItem("one"), makeItem("two")] }),
    paneDelta({ transcript: "t1", seq: 6n, items: [makeItem("three"), makeItem("four")] }),
    3,
  );

  assert.deepEqual(texts(next), ["two", "three", "four"]);
});

test("the default keep holds the newest 50 items", () => {
  const older = Array.from({ length: 30 }, (_, index) => makeItem(`old-${index}`));
  const fresh = Array.from({ length: 30 }, (_, index) => makeItem(`new-${index}`));

  const next = applyDelta(held({ items: older }), paneDelta({ transcript: "t1", seq: 7n, items: fresh }));

  assert.equal(next.items.length, 50);
  assert.equal(next.items[0].text, "old-10");
  assert.equal(next.items[49].text, "new-29");
});

test("a later delta replaces the unfinished set even with an empty one", () => {
  const next = applyDelta(
    held({ unfinished: [makeUnfinished("partial")] }),
    paneDelta({ transcript: "t1", seq: 6n, items: [makeItem("done")], unfinished: [] }),
  );

  assert.deepEqual(next.unfinished, []);
});

test("a later delta replaces the state when set", () => {
  const next = applyDelta(
    held({ state: makeState({ goal: "old goal" }) }),
    paneDelta({ transcript: "t1", seq: 6n, items: [makeItem("a")], state: makeState({ goal: "new goal" }) }),
  );

  assert.equal(next.state?.goal, "new goal");
});

test("a later delta keeps the state when unset", () => {
  const before = makeState({ goal: "kept goal" });
  const next = applyDelta(
    held({ state: before }),
    paneDelta({ transcript: "t1", seq: 6n, items: [makeItem("a")] }),
  );

  assert.equal(next.state, before);
  assert.equal(next.state?.goal, "kept goal");
});

test("a pane keepalive keeps the items and state and takes its seq", () => {
  const before = held({ items: [makeItem("a")] });
  const next = applyDelta(before, paneDelta({ transcript: "t1", seq: 42n }));

  assert.deepEqual(texts(next), ["a"]);
  assert.equal(next.state, before.state);
  assert.deepEqual(next.unfinished, []);
  assert.equal(next.transcript, "t1");
  assert.equal(next.seq, 42n);
});

test("a session delta with no items and an empty unfinished set clears the held unfinished text", () => {
  const before = held({ items: [makeItem("a")], unfinished: [makeUnfinished("partial")] });
  const next = applyDelta(before, sessionDelta({ transcript: "s1", seq: 9n }));

  assert.deepEqual(texts(next), ["a"]);
  assert.equal(next.state, before.state);
  assert.deepEqual(next.unfinished, []);
  assert.equal(next.seq, 9n);
});

test("the result takes the delta's transcript and seq", () => {
  const next = applyDelta(held(), paneDelta({ transcript: "t9", seq: 77n, items: [makeItem("a")] }));

  assert.equal(next.transcript, "t9");
  assert.equal(next.seq, 77n);
});

test("a session reset holds the delta's newest keep items, its state, and its unfinished set", () => {
  const next = applyDelta(
    null,
    sessionDelta({
      transcript: "s1",
      seq: 4n,
      reset: true,
      items: [makeItem("a"), makeItem("b"), makeItem("c")],
      state: makeState({ goal: "session goal" }),
      unfinished: [makeUnfinished("typing")],
    }),
    2,
  );

  assert.deepEqual(texts(next), ["b", "c"]);
  assert.equal(next.state?.goal, "session goal");
  assert.deepEqual(
    next.unfinished.map((text) => text.text),
    ["typing"],
  );
  assert.equal(next.transcript, "s1");
  assert.equal(next.seq, 4n);
});

test("a session delta appends, replaces unfinished, keeps state when unset, and a keepalive keeps the items", () => {
  const start = applyDelta(
    null,
    sessionDelta({
      transcript: "s1",
      seq: 4n,
      items: [makeItem("a")],
      state: makeState({ goal: "session goal" }),
      unfinished: [makeUnfinished("typing")],
    }),
    50,
  );

  const appended = applyDelta(
    start,
    sessionDelta({ transcript: "s1", seq: 5n, items: [makeItem("b")], unfinished: [] }),
    50,
  );
  assert.deepEqual(texts(appended), ["a", "b"]);
  assert.deepEqual(appended.unfinished, []);
  assert.equal(appended.state?.goal, "session goal");
  assert.equal(appended.seq, 5n);

  const keptAlive = applyDelta(appended, sessionDelta({ transcript: "s1", seq: 6n }), 50);
  assert.deepEqual(texts(keptAlive), ["a", "b"]);
  assert.equal(keptAlive.state, appended.state);
  assert.equal(keptAlive.seq, 6n);
});
