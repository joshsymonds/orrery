import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  ConversationItemSchema,
  ConversationStateSchema,
  EngineTextSchema,
  OlderResponseSchema,
  SessionsOlderResponseSchema,
  SessionsWatchConversationResponseSchema,
  WatchConversationResponseSchema,
  type ConversationItem,
  type ConversationState,
  type EngineText,
  type OlderResponse,
  type SessionsOlderResponse,
  type SessionsWatchConversationResponse,
  type WatchConversationResponse,
} from "./gen/orrery/v1/orrery_pb";
import { applyDelta, type Conversation, prependOlder } from "./conversation";

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

function olderPage(init: MessageInitShape<typeof OlderResponseSchema>): OlderResponse {
  return create(OlderResponseSchema, init);
}

function sessionOlderPage(init: MessageInitShape<typeof SessionsOlderResponseSchema>): SessionsOlderResponse {
  return create(SessionsOlderResponseSchema, init);
}

function texts(conversation: Conversation): string[] {
  return conversation.items.map((item) => item.text);
}

function held(init: Partial<Conversation> = {}): Conversation {
  return {
    transcript: "t1",
    seq: 5n,
    items: [makeItem("old")],
    oldest: 1n,
    more: false,
    state: makeState({ goal: "ship" }),
    unfinished: [makeUnfinished("partial")],
    ...init,
  };
}

test("a reset holds every item the delta carries, its oldest cursor, its state, and its unfinished set", () => {
  const next = applyDelta(
    held(),
    paneDelta({
      transcript: "t2",
      seq: 9n,
      reset: true,
      items: [makeItem("a"), makeItem("b"), makeItem("c")],
      oldest: 30n,
      state: makeState({ goal: "reset goal" }),
      unfinished: [makeUnfinished("fresh")],
    }),
  );

  assert.deepEqual(texts(next), ["a", "b", "c"]);
  assert.equal(next.oldest, 30n);
  assert.equal(next.more, true);
  assert.equal(next.state?.goal, "reset goal");
  assert.deepEqual(
    next.unfinished.map((text) => text.text),
    ["fresh"],
  );
  assert.equal(next.transcript, "t2");
  assert.equal(next.seq, 9n);
});

test("a reset whose oldest is 0 holds no older items", () => {
  const next = applyDelta(held({ more: true }), paneDelta({ transcript: "t2", seq: 9n, reset: true, items: [makeItem("a")] }));

  assert.equal(next.oldest, 0n);
  assert.equal(next.more, false);
});

test("a delta applied to nothing held holds its items, its oldest cursor, its state, and its unfinished set", () => {
  const next = applyDelta(
    null,
    paneDelta({
      transcript: "t1",
      seq: 3n,
      items: [makeItem("a"), makeItem("b")],
      oldest: 12n,
      state: makeState({ goal: "first" }),
      unfinished: [makeUnfinished("open")],
    }),
  );

  assert.deepEqual(texts(next), ["a", "b"]);
  assert.equal(next.oldest, 12n);
  assert.equal(next.more, true);
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

test("a later delta appends its items with no trim", () => {
  const next = applyDelta(
    held({ items: [makeItem("one"), makeItem("two")] }),
    paneDelta({ transcript: "t1", seq: 6n, items: [makeItem("three"), makeItem("four")] }),
  );

  assert.deepEqual(texts(next), ["one", "two", "three", "four"]);
});

test("a page keeps every item it has loaded past 50", () => {
  const older = Array.from({ length: 30 }, (_, index) => makeItem(`old-${index}`));
  const fresh = Array.from({ length: 30 }, (_, index) => makeItem(`new-${index}`));

  const next = applyDelta(held({ items: older }), paneDelta({ transcript: "t1", seq: 7n, items: fresh }));

  assert.equal(next.items.length, 60);
  assert.equal(next.items[0].text, "old-0");
  assert.equal(next.items[59].text, "new-29");
});

test("a later delta keeps the held oldest cursor and more", () => {
  const next = applyDelta(
    held({ oldest: 40n, more: true }),
    paneDelta({ transcript: "t1", seq: 6n, items: [makeItem("a")] }),
  );

  assert.equal(next.oldest, 40n);
  assert.equal(next.more, true);
});

test("a later delta keeps more false when nothing older remains", () => {
  const next = applyDelta(held({ oldest: 0n, more: false }), paneDelta({ transcript: "t1", seq: 6n, items: [makeItem("a")] }));

  assert.equal(next.oldest, 0n);
  assert.equal(next.more, false);
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

test("a pane keepalive keeps the items, state, oldest and more, and takes its seq", () => {
  const before = held({ items: [makeItem("a")], oldest: 7n, more: true });
  const next = applyDelta(before, paneDelta({ transcript: "t1", seq: 42n }));

  assert.deepEqual(texts(next), ["a"]);
  assert.equal(next.state, before.state);
  assert.equal(next.oldest, 7n);
  assert.equal(next.more, true);
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

test("a session reset holds every item the delta carries, its oldest cursor, its state, and its unfinished set", () => {
  const next = applyDelta(
    null,
    sessionDelta({
      transcript: "s1",
      seq: 4n,
      reset: true,
      items: [makeItem("a"), makeItem("b"), makeItem("c")],
      oldest: 2n,
      state: makeState({ goal: "session goal" }),
      unfinished: [makeUnfinished("typing")],
    }),
  );

  assert.deepEqual(texts(next), ["a", "b", "c"]);
  assert.equal(next.oldest, 2n);
  assert.equal(next.more, true);
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
  );

  const appended = applyDelta(
    start,
    sessionDelta({ transcript: "s1", seq: 5n, items: [makeItem("b")], unfinished: [] }),
  );
  assert.deepEqual(texts(appended), ["a", "b"]);
  assert.deepEqual(appended.unfinished, []);
  assert.equal(appended.state?.goal, "session goal");
  assert.equal(appended.seq, 5n);

  const keptAlive = applyDelta(appended, sessionDelta({ transcript: "s1", seq: 6n }));
  assert.deepEqual(texts(keptAlive), ["a", "b"]);
  assert.equal(keptAlive.state, appended.state);
  assert.equal(keptAlive.seq, 6n);
});

test("prependOlder puts an older page's items before the held items", () => {
  const before = held({ items: [makeItem("two"), makeItem("three")], oldest: 50n, more: true });

  const next = prependOlder(before, olderPage({ items: [makeItem("zero"), makeItem("one")], oldest: 20n, more: true }));

  assert.deepEqual(texts(next), ["zero", "one", "two", "three"]);
});

test("prependOlder takes the page's oldest and more when the page holds items", () => {
  const before = held({ items: [makeItem("two")], oldest: 50n, more: true });

  const more = prependOlder(before, olderPage({ items: [makeItem("one")], oldest: 20n, more: true }));
  assert.equal(more.oldest, 20n);
  assert.equal(more.more, true);

  const last = prependOlder(before, olderPage({ items: [makeItem("one")], oldest: 3n, more: false }));
  assert.equal(last.oldest, 3n);
  assert.equal(last.more, false);
});

test("prependOlder keeps the held oldest for a page with no items, and takes its more", () => {
  const before = held({ items: [makeItem("two")], oldest: 50n, more: true });

  const next = prependOlder(before, olderPage({ items: [], oldest: 0n, more: false }));

  assert.deepEqual(texts(next), ["two"]);
  assert.equal(next.oldest, 50n);
  assert.equal(next.more, false);
});

test("prependOlder takes a session page the same way", () => {
  const before = held({ items: [makeItem("two")], oldest: 9n, more: true });

  const next = prependOlder(before, sessionOlderPage({ items: [makeItem("one")], oldest: 4n, more: false }));

  assert.deepEqual(texts(next), ["one", "two"]);
  assert.equal(next.oldest, 4n);
  assert.equal(next.more, false);
});

test("prependOlder leaves the transcript, seq, state and unfinished set as they were", () => {
  const state = makeState({ goal: "kept" });
  const unfinished = [makeUnfinished("typing")];
  const before = held({ transcript: "t7", seq: 13n, state, unfinished, items: [makeItem("two")], oldest: 50n, more: true });

  const next = prependOlder(before, olderPage({ items: [makeItem("one")], oldest: 20n, more: true }));

  assert.equal(next.transcript, "t7");
  assert.equal(next.seq, 13n);
  assert.equal(next.state, state);
  assert.equal(next.unfinished, unfinished);
});
