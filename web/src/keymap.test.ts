import assert from "node:assert/strict";
import { after, before, test } from "node:test";

import { handleKey, type KeyContext } from "./keymap";
import type { Row } from "./rows";
import { assertSame, installTestDom, removeTestDom } from "./testdom";

// handleKey reads a keydown against a context, with no App mounted. Each test's name carries the token AppSplit.

type Dom = ReturnType<typeof installTestDom>;

// Call is one callback or setter a key called, by its name, with its arguments.
type Call = [name: string, ...args: unknown[]];

interface Fake {
  context: KeyContext;
  calls: Call[];
}

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

// fakeContext is a context for handleKey whose callbacks and setters record their calls in order. A setter given an
// updater resolves it against helpOpen, as React does.
function fakeContext(init: Partial<KeyContext> = {}): Fake {
  const calls: Call[] = [];
  const helpOpen = init.helpOpen ?? false;
  const record =
    (name: string) =>
    (...args: unknown[]): void => {
      calls.push([name, ...args]);
    };
  const context: KeyContext = {
    pickerOpen: false,
    openMenu: null,
    helpOpen,
    inboxHidden: false,
    keyPopped: false,
    index: 0,
    flat: [],
    cursorItem: null,
    cursorRow: null,
    cursorRowKey: "",
    cursorStep: null,
    cursorStepKey: "",
    cursorPane: undefined,
    pinned: null,
    inputRef: { current: null },
    composerInputs: { current: new Map() },
    stepInputs: { current: new Map() },
    pendingG: { current: false },
    moveTo: record("moveTo"),
    nextUnseen: record("nextUnseen"),
    writeSeen: (target) => {
      calls.push(["writeSeen", target]);
      return Promise.resolve();
    },
    setArchive: record("setArchive"),
    escapeRow: (target, box) => {
      calls.push(["escapeRow", target, box]);
      return Promise.resolve();
    },
    replyBoxTarget: (box) => {
      calls.push(["replyBoxTarget", box]);
      return null;
    },
    focusCursorRow: record("focusCursorRow"),
    backOneCrumb: record("backOneCrumb"),
    togglePanel: record("togglePanel"),
    answerCursor: record("answerCursor"),
    toggleOpen: record("toggleOpen"),
    closePin: record("closePin"),
    togglePin: record("togglePin"),
    toggleStrips: record("toggleStrips"),
    setPickerOpen: record("setPickerOpen"),
    setMenuKey: record("setMenuKey"),
    setFilter: record("setFilter"),
    setPopout: record("setPopout"),
    setRowOpened: record("setRowOpened"),
    setBoxOpened: record("setBoxOpened"),
    setView: record("setView"),
    setPinned: record("setPinned"),
    setPinStream: record("setPinStream"),
    setHelpOpen: (update) => {
      calls.push(["setHelpOpen", typeof update === "function" ? update(helpOpen) : update]);
    },
    ...init,
  };
  return { context, calls };
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
