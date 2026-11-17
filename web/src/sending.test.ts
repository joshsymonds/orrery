import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";

import type { Conversation } from "./conversation";
import { ConversationItemSchema, ConversationStateSchema, type ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { sendingEntry, unsentEntries, type Sending } from "./sending";

function prompt(text: string, promptId = ""): ConversationItem {
  return create(ConversationItemSchema, { kind: "prompt", text, promptId, time: 100n });
}

function reply(text: string): ConversationItem {
  return create(ConversationItemSchema, { kind: "reply", text, time: 101n });
}

function held(items: ConversationItem[]): Conversation {
  return {
    transcript: "t1",
    seq: 1n,
    items,
    oldest: 0n,
    more: false,
    state: create(ConversationStateSchema, {}),
    unfinished: [],
  };
}

// entry makes a send of text under promptId that began after anchor, the newest item held when it began.
function entry(text: string, promptId: string, anchor?: ConversationItem): Sending {
  return { promptId, text, after: anchor };
}

test("a session's entry is sent once a prompt with its prompt id is held, whatever its text", () => {
  const entries = [entry("fix the build", "p1"), entry("run the tests", "p2")];
  const conversation = held([prompt("fix the build", "p1")]);
  assert.deepEqual(unsentEntries(entries, conversation, "session"), [entries[1]]);
});

test("a session's entry with the same text under another prompt id is unsent", () => {
  const entries = [entry("fix the build", "p2")];
  const conversation = held([prompt("fix the build", "p1")]);
  assert.deepEqual(unsentEntries(entries, conversation, "session"), entries);
});

test("a pane's entry is sent once a prompt with its text is held after the item the send began after", () => {
  const anchor = reply("working on it");
  const entries = [entry("fix the build", "p1", anchor)];
  const conversation = held([prompt("fix the build"), anchor, prompt("fix the build")]);
  assert.deepEqual(unsentEntries(entries, conversation, "pane"), []);
});

test("a pane's entry stays unsent while the only held prompt with its text comes before the send's anchor", () => {
  const anchor = reply("working on it");
  const entries = [entry("fix the build", "p1", anchor)];
  const conversation = held([prompt("fix the build"), anchor]);
  assert.deepEqual(unsentEntries(entries, conversation, "pane"), entries);
});

test("a pane's entry is sent by a held prompt with its text after a reset dropped the send's anchor", () => {
  const entries = [entry("fix the build", "p1", reply("working on it"))];
  const afterReset = held([prompt("fix the build")]);
  assert.deepEqual(unsentEntries(entries, afterReset, "pane"), []);
});

test("a pane's text matches trimmed, so surrounding space does not keep an entry unsent", () => {
  const anchor = reply("working on it");
  const entries = [entry("  fix the build\n", "p1", anchor)];
  const conversation = held([anchor, prompt("fix the build")]);
  assert.deepEqual(unsentEntries(entries, conversation, "pane"), []);
});

test("a pane's entry with no item held when the send began matches any held prompt with its text", () => {
  const entries = [entry("fix the build", "p1", undefined)];
  assert.deepEqual(unsentEntries(entries, held([prompt("fix the build")]), "pane"), []);
  assert.deepEqual(unsentEntries(entries, held([prompt("other")]), "pane"), entries);
});

test("with no conversation held every entry is unsent, and the same array comes back when none is sent", () => {
  const entries = [entry("fix the build", "p1")];
  assert.equal(unsentEntries(entries, null, "session"), entries);
  const conversation = held([prompt("other", "p9")]);
  assert.equal(unsentEntries(entries, conversation, "session"), entries);
});

test("sendingEntry anchors a send to the newest item held when it began, and to none when no conversation is held", () => {
  const newest = reply("done");
  const anchored = sendingEntry({ promptId: "p1", text: "fix" }, held([prompt("earlier"), newest]));
  assert.equal(anchored.after, newest);
  assert.deepEqual(sendingEntry({ promptId: "p2", text: "fix" }, null), {
    promptId: "p2",
    text: "fix",
    after: undefined,
  });
});
