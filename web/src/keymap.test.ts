import assert from "node:assert/strict";
import { after, before, test } from "node:test";

import { handleKey } from "./keymap";
import type { Row } from "./rows";
import { assertSame, installTestDom, removeTestDom } from "./testdom";
import { fakeContext } from "./testkeys";

// handleKey reads a keydown against a context, with no App mounted. Each test's name carries the token AppSplit.

type Dom = ReturnType<typeof installTestDom>;

let dom: Dom;

before(() => {
  dom = installTestDom();
});

after(async () => {
  await removeTestDom(dom);
});

// row is a pane row with the key, as the list holds it.
function row(key: string): Row {
  return {
    key,
    id: key,
    archiveId: "",
    kind: "pane",
    label: key,
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
  };
}

const three = [row("a"), row("b"), row("c")];

// key is a keydown for the key, cancelable as a browser's is, with any modifiers given.
function key(name: string, init: KeyboardEventInit = {}): KeyboardEvent {
  return new KeyboardEvent("keydown", { key: name, cancelable: true, ...init });
}

test("AppSplit: j moves the cursor to the next entry and prevents the default", () => {
  const fake = fakeContext({ index: 1, flat: three });
  const event = key("j");
  handleKey(event, fake.context);
  assert.deepEqual(fake.calls, [["moveTo", 2]]);
  assert.equal(event.defaultPrevented, true);
});

test("AppSplit: k moves the cursor to the previous entry and prevents the default", () => {
  const fake = fakeContext({ index: 1, flat: three });
  const event = key("k");
  handleKey(event, fake.context);
  assert.deepEqual(fake.calls, [["moveTo", 0]]);
  assert.equal(event.defaultPrevented, true);
});

test("AppSplit: g then g moves the cursor to the first entry through pendingG", () => {
  const fake = fakeContext({ index: 2, flat: three });
  handleKey(key("g"), fake.context);
  assert.deepEqual(fake.calls, []);
  assert.equal(fake.context.pendingG.current, true);
  handleKey(key("g"), fake.context);
  assert.deepEqual(fake.calls, [["moveTo", 0]]);
  assert.equal(fake.context.pendingG.current, false);
});

test("AppSplit: G moves the cursor to the last entry and prevents the default", () => {
  const fake = fakeContext({ index: 0, flat: three });
  const event = key("G");
  handleKey(event, fake.context);
  assert.deepEqual(fake.calls, [["moveTo", 2]]);
  assert.equal(event.defaultPrevented, true);
});

test("AppSplit: ? opens the help panel when it is shut and closes it when it is open", () => {
  const opening = fakeContext({ helpOpen: false });
  const event = key("?");
  handleKey(event, opening.context);
  assert.deepEqual(opening.calls, [["setHelpOpen", true]]);
  assert.equal(event.defaultPrevented, true);

  const closing = fakeContext({ helpOpen: true });
  handleKey(key("?"), closing.context);
  assert.deepEqual(closing.calls, [["setHelpOpen", false]]);
});

test("AppSplit: ⌘K opens the palette and prevents the default", () => {
  const fake = fakeContext();
  const event = key("k", { metaKey: true });
  handleKey(event, fake.context);
  assert.deepEqual(fake.calls, [["setPickerOpen", true]]);
  assert.equal(event.defaultPrevented, true);
});

test("AppSplit: a list key with the inbox hidden pops it out by key and moves nothing", () => {
  const fake = fakeContext({ inboxHidden: true, index: 0, flat: three });
  const event = key("j");
  handleKey(event, fake.context);
  assert.deepEqual(fake.calls, [["setPopout", "key"]]);
  assert.equal(event.defaultPrevented, true);
});

test("AppSplit: Enter on the cursor row opens it, marks it seen and focuses its reply box", () => {
  const cursor = row("a");
  const fake = fakeContext({
    index: 0,
    flat: [cursor],
    cursorItem: cursor,
    cursorRow: cursor,
    cursorRowKey: cursor.key,
  });
  const event = key("Enter");
  handleKey(event, fake.context);
  assert.deepEqual(fake.calls, [
    ["setRowOpened", true],
    ["setPopout", "none"],
    ["writeSeen", cursor],
    ["setBoxOpened", { column: "cursor", key: "a" }],
  ]);
  assert.equal(event.defaultPrevented, true);
});

test("AppSplit: a key typed in a textarea does nothing but Escape and Shift+Tab", () => {
  const box = document.createElement("textarea");
  document.body.append(box);
  try {
    box.focus();
    assertSame(document.activeElement, box, "expected the textarea to hold focus");
    const fake = fakeContext({ index: 0, flat: three });
    for (const name of ["j", "G", "?", "Enter"]) {
      const event = key(name);
      handleKey(event, fake.context);
      assert.equal(event.defaultPrevented, false, `${name} should not be prevented`);
    }
    assert.deepEqual(fake.calls, []);
  } finally {
    box.remove();
  }
});
