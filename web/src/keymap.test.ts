import assert from "node:assert/strict";
import { after, before, test } from "node:test";

import { type Where, keyGroups } from "./keys";
import { handleKey, type KeyContext } from "./keymap";
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
    putOff: false,
    tool: "",
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

test("AppSplit: a list key with the inbox collapsed expands it and moves nothing", () => {
  for (const name of ["j", "k", "ArrowDown", "ArrowUp", "g", "G", "/", "v"]) {
    const fake = fakeContext({ collapsed: true, index: 0, flat: three });
    const event = key(name);
    handleKey(event, fake.context);
    assert.deepEqual(fake.calls, [["expandInbox"]], `the ${name} key`);
    assert.equal(event.defaultPrevented, true, `the ${name} key`);
  }
});

test("AppSplit: b collapses or expands the inbox in the list, collapsed and with a flyout open, and B does nothing", () => {
  const contexts: Partial<KeyContext>[] = [{}, { collapsed: true }, { collapsed: true, flyoutOpen: true }];
  for (const init of contexts) {
    const fake = fakeContext({ index: 0, flat: three, ...init });
    const event = key("b");
    handleKey(event, fake.context);
    assert.deepEqual(fake.calls, [["toggleInbox"]], `b in ${JSON.stringify(init)}`);
    assert.equal(event.defaultPrevented, true);
    const shifted = fakeContext({ index: 0, flat: three, ...init });
    const upper = key("B");
    handleKey(upper, shifted.context);
    assert.deepEqual(shifted.calls, [], `B in ${JSON.stringify(init)}`);
    assert.equal(upper.defaultPrevented, false);
  }
});

test("AppSplit: p opens the flyout while the inbox is collapsed, and does nothing in the list", () => {
  const list = fakeContext({ index: 0, flat: three });
  const listed = key("p");
  handleKey(listed, list.context);
  assert.deepEqual(list.calls, []);
  assert.equal(listed.defaultPrevented, false);
  for (const init of [{ collapsed: true }, { collapsed: true, flyoutOpen: true }]) {
    const fake = fakeContext({ index: 0, flat: three, ...init });
    const event = key("p");
    handleKey(event, fake.context);
    assert.deepEqual(fake.calls, [["openFlyout"]]);
    assert.equal(event.defaultPrevented, true);
  }
});

test("AppSplit: with a flyout open, ↑ and k move it up a tile, ↓ and j down, ⏎ opens its row and Escape closes it", () => {
  const cursor = row("a");
  const cases: [name: string, calls: unknown[][]][] = [
    ["ArrowUp", [["moveFlyout", -1]]],
    ["k", [["moveFlyout", -1]]],
    ["ArrowDown", [["moveFlyout", 1]]],
    ["j", [["moveFlyout", 1]]],
    ["Enter", [["openFlyoutRow"]]],
    ["Escape", [["closeFlyout"]]],
  ];
  for (const [name, calls] of cases) {
    const fake = fakeContext({
      collapsed: true,
      flyoutOpen: true,
      index: 0,
      flat: [cursor],
      cursorItem: cursor,
      cursorRow: cursor,
      cursorRowKey: cursor.key,
    });
    const event = key(name);
    handleKey(event, fake.context);
    assert.deepEqual(fake.calls, calls, `the ${name} key`);
    assert.equal(event.defaultPrevented, true, `the ${name} key`);
  }
});

test("AppSplit: with a flyout open, ⏎ opens its row even while a button holds the focus", () => {
  const button = document.createElement("button");
  document.body.append(button);
  try {
    button.focus();
    assertSame(document.activeElement, button, "expected the button to hold focus");
    const fake = fakeContext({ collapsed: true, flyoutOpen: true });
    const event = key("Enter");
    handleKey(event, fake.context);
    assert.deepEqual(fake.calls, [["openFlyoutRow"]]);
    const without = fakeContext({ collapsed: true });
    const passed = key("Enter");
    handleKey(passed, without.context);
    assert.deepEqual(without.calls, [], "with no flyout, a focused button keeps its own Enter");
    assert.equal(passed.defaultPrevented, false);
  } finally {
    button.remove();
  }
});

test("AppSplit: with a flyout open, the flyout's keys typed in a text box are text", () => {
  const box = document.createElement("textarea");
  document.body.append(box);
  try {
    box.focus();
    const fake = fakeContext({ collapsed: true, flyoutOpen: true });
    for (const name of ["j", "k", "ArrowUp", "ArrowDown", "Enter", "p", "b"]) {
      const event = key(name);
      handleKey(event, fake.context);
      assert.equal(event.defaultPrevented, false, `${name} should not be prevented`);
    }
    assert.deepEqual(fake.calls, []);
  } finally {
    box.remove();
  }
});

test("AppSplit: collapsed with no flyout, ⏎ opens the cursor row and Escape takes back from it, as in the list", () => {
  const cursor = row("a");
  const init = { collapsed: true, index: 0, flat: [cursor], cursorItem: cursor, cursorRow: cursor, cursorRowKey: cursor.key };
  const enter = fakeContext(init);
  handleKey(key("Enter"), enter.context);
  assert.deepEqual(enter.calls.map(([name]) => name), ["setRowOpened", "writeSeen", "setBoxOpened"]);
  const escape = fakeContext(init);
  handleKey(key("Escape"), escape.context);
  assert.deepEqual(escape.calls.map(([name]) => name), ["escapeRow", "setHelpOpen"]);
});

// actsIn is the contexts among list, collapsed and flyout where the table binds the chord with no modifier.
function actsIn(name: string): Where[] {
  const contexts: Where[] = ["list", "collapsed", "flyout"];
  return contexts.filter((where) =>
    keyGroups.some((group) =>
      group.lines.some((line) =>
        line.acts.some(
          (act) =>
            act.in.includes(where) &&
            act.chords.some((chord) => chord.key === name && Object.keys(chord).length === 1),
        ),
      ),
    ),
  );
}

test("AppSplit: p and the flyout's keys act in exactly the contexts the table binds them in", () => {
  const contexts: [Where, Partial<KeyContext>][] = [
    ["list", {}],
    ["collapsed", { collapsed: true }],
    ["flyout", { collapsed: true, flyoutOpen: true }],
  ];
  for (const name of ["p", "j", "k", "ArrowUp", "ArrowDown", "Enter", "Escape"]) {
    const acting = contexts
      .filter(([, init]) => {
        const cursor = row("b");
        const fake = fakeContext({ index: 1, flat: three, cursorItem: cursor, cursorRow: cursor, cursorRowKey: "b", ...init });
        const event = key(name);
        handleKey(event, fake.context);
        return event.defaultPrevented || fake.calls.length > 0;
      })
      .map(([where]) => where);
    assert.deepEqual(acting, actsIn(name), `the ${name} key acts where the table binds it`);
  }
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

test("InboxTree: a and p do nothing, and v opens the archived view", () => {
  for (const name of ["a", "p"]) {
    const fake = fakeContext({ index: 0, flat: three });
    const event = key(name);
    handleKey(event, fake.context);
    assert.deepEqual(fake.calls, [], `the ${name} key`);
    assert.equal(event.defaultPrevented, false, `the ${name} key`);
  }
  const fake = fakeContext({ index: 0, flat: three });
  handleKey(key("v"), fake.context);
  assert.deepEqual(fake.calls, [["setView", "archived"]]);
});

test("InboxTree: w moves to the counted thing after the cursor's in waitKeys, wrapping, and from an entry that counts nothing to the first", () => {
  const waitKeys = ["a", "epic:w/first", "contract:w/tide"];
  const cases: [from: string, to: string][] = [
    ["a", "epic:w/first"],
    ["epic:w/first", "contract:w/tide"],
    ["contract:w/tide", "a"],
    ["c", "a"],
  ];
  for (const [from, to] of cases) {
    const fake = fakeContext({ waitKeys, cursorItem: row(from), index: 0, flat: three });
    handleKey(key("w"), fake.context);
    assert.deepEqual(fake.calls, [["waitTo", to]], `w from ${from}`);
  }
});

test("InboxTree: w does nothing when no counted thing is listed", () => {
  const fake = fakeContext({ waitKeys: [], cursorItem: three[0], index: 0, flat: three });
  handleKey(key("w"), fake.context);
  assert.deepEqual(fake.calls, []);
});

test("InboxTree: A archives the cursor's row in the inbox and unarchives it in Archived", () => {
  const item: Row = { ...row("a"), archiveKind: "session", archiveId: "sess-a" };
  const inbox = fakeContext({ cursorItem: item, cursorRow: item, index: 0, flat: [item], view: "inbox" });
  handleKey(key("A"), inbox.context);
  assert.deepEqual(inbox.calls, [["setArchive", item, true]]);
  const archived = fakeContext({ cursorItem: item, cursorRow: item, index: 0, flat: [item], view: "archived" });
  handleKey(key("A"), archived.context);
  assert.deepEqual(archived.calls, [["setArchive", item, false]]);
});
