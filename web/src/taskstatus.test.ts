import assert from "node:assert/strict";
import { test } from "node:test";

import { taskStateOf, type TaskState } from "./taskstatus";

// Each recorded word with a mixed-case spelling and the state it counts as.
const recorded: [word: string, mixed: string, state: TaskState][] = [
  ["done", "DoNe", "done"],
  ["integrated", "Integrated", "done"],
  ["dispatched", "DISPATCHED", "dispatched"],
  ["gated", "Gated", "gated"],
  ["pending", "pEnDiNg", "pending"],
  ["gap", "GaP", "gap"],
  ["split", "SPLIT", "split"],
];

for (const [word, mixed, state] of recorded) {
  test(`"${word}" is ${state} in lower case`, () => {
    assert.equal(taskStateOf(word), state);
  });

  test(`"${mixed}" is ${state} in mixed case`, () => {
    assert.equal(taskStateOf(mixed), state);
  });
}

test("a commit-suffixed done is unknown, not done", () => {
  assert.equal(taskStateOf("done at 3f9c2e1"), "unknown");
});

test("a padded status is unknown, not trimmed", () => {
  assert.equal(taskStateOf(" done"), "unknown");
});

test("the empty string is unknown", () => {
  assert.equal(taskStateOf(""), "unknown");
});

test("an unrecognised word is unknown", () => {
  assert.equal(taskStateOf("frobnicated"), "unknown");
});

test("an object property name is unknown, not a lookup hit", () => {
  assert.equal(taskStateOf("constructor"), "unknown");
});
