import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { FindBar, findMatches } from "./find";
import { ConversationItemSchema, type ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { installTestDom, removeTestDom } from "./testdom";

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

// itemsOf returns the item index of each match of query in items, in order.
function itemsOf(items: ConversationItem[], query: string): number[] {
  return findMatches(items, query).map((match) => match.item);
}

test("TranscriptSearch: a query matches case-insensitively in drawn reply, prompt and tool-input text", () => {
  const items = [
    item({ kind: "reply", text: "The **Build** passed." }),
    item({ kind: "prompt", sender: "owner", text: "rerun the BUILD" }),
    item({ kind: "tool", tool: "Bash", input: "make build", id: "c1" }),
  ];
  assert.deepEqual(findMatches(items, "build"), [
    { item: 0, start: 4 },
    { item: 1, start: 10 },
    { item: 2, start: 5 },
  ]);
});

test("TranscriptSearch: a query does not match Markdown syntax, only the text Markdown draws", () => {
  const items = [item({ kind: "reply", text: "## Plan\n\nUse **bold**, `code` and [docs](https://example.test/guide)." })];
  assert.deepEqual(findMatches(items, "##"), []);
  assert.deepEqual(findMatches(items, "**"), []);
  assert.deepEqual(findMatches(items, "`"), []);
  assert.deepEqual(findMatches(items, "https"), []);
  assert.equal(findMatches(items, "bold").length, 1);
  assert.equal(findMatches(items, "docs").length, 1);
});

test("TranscriptSearch: a match inside a call's result is counted under the call, and a result that answers no call is not", () => {
  const items = [
    item({ kind: "reply", text: "looking" }),
    item({ kind: "tool", tool: "Bash", input: "ls", id: "c1" }),
    item({ kind: "result", id: "c1", text: "found Needle here" }),
    item({ kind: "result", id: "ghost", text: "needle" }),
  ];
  assert.deepEqual(itemsOf(items, "needle"), [1]);
});

test("TranscriptSearch: compaction and check-card items are not searched", () => {
  const items = [
    item({ kind: "compaction", text: "lint summary", trigger: "auto", tokens: 9000n }),
    item({ kind: "prompt", sender: "engine", round: 1, text: "lint failed" }),
    item({ kind: "prompt", sender: "engine", round: 0, text: "lint the brief" }),
  ];
  assert.deepEqual(itemsOf(items, "lint"), [2]);
});

test("TranscriptSearch: an empty query matches nothing", () => {
  assert.deepEqual(findMatches([item({ kind: "reply", text: "anything" })], ""), []);
});

interface BarProps {
  query: string;
  matches: number;
  current: number;
  onQuery: (query: string) => void;
  onNext: () => void;
  onPrevious: () => void;
  onClose: () => void;
}

// withFindBar renders a bar into the document, so the keys it sees bubble as they do on the page, and checks it. The
// props it does not name take a bar with a query and three matches, the first current.
async function withFindBar(
  props: Partial<BarProps>,
  check: (input: HTMLInputElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const barProps: BarProps = {
    query: "build",
    matches: 3,
    current: 0,
    onQuery: () => {},
    onNext: () => {},
    onPrevious: () => {},
    onClose: () => {},
    ...props,
  };
  try {
    await act(() => {
      render(<FindBar {...barProps} />, container);
    });
    const input = container.querySelector("input");
    assert.ok(input, "the bar draws its input");
    await check(input);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// press dispatches a keydown on target inside act, as the browser does: it bubbles from the element to the document.
async function press(target: Element, key: string, init: KeyboardEventInit = {}): Promise<void> {
  await act(() => {
    target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init }));
  });
}

// keysAtDocument records the key of each keydown that reaches the document, the page's own keydown listener's view.
function keysAtDocument(): string[] {
  const keys: string[] = [];
  document.addEventListener("keydown", (event) => {
    keys.push(event.key);
  });
  return keys;
}

test("TranscriptSearch: the bar takes focus and shows the current match as 1 of N, or no count when none match", async () => {
  await withFindBar({}, async (input) => {
    assert.equal(document.activeElement, input);
    assert.equal(input.closest(".find")?.querySelector(".count")?.textContent, "1 of 3");
  });
  await withFindBar({ matches: 0 }, (input) => {
    assert.equal(input.closest(".find")?.querySelector(".count"), null);
  });
});

test("TranscriptSearch: typing in the bar passes the field's text to onQuery", async () => {
  const typed: string[] = [];
  await withFindBar({ onQuery: (query) => typed.push(query) }, async (input) => {
    await act(() => {
      input.value = "bu";
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    assert.deepEqual(typed, ["bu"]);
  });
});

test("TranscriptSearch: Enter calls next, Shift+Enter calls previous, and neither reaches the document", async () => {
  const moves: string[] = [];
  await withFindBar({ onNext: () => moves.push("next"), onPrevious: () => moves.push("previous") }, async (input) => {
    const reached = keysAtDocument();
    await press(input, "a");
    await press(input, "Enter");
    await press(input, "Enter", { shiftKey: true });
    assert.deepEqual(moves, ["next", "previous"]);
    assert.deepEqual(reached, ["a"], "only the key the bar does not handle reaches the document");
  });
});

test("TranscriptSearch: Escape calls close and does not reach a document keydown listener", async () => {
  let closed = 0;
  await withFindBar({ onClose: () => (closed += 1) }, async (input) => {
    const reached = keysAtDocument();
    await press(input, "a");
    await press(input, "Escape");
    assert.equal(closed, 1);
    assert.deepEqual(reached, ["a"], "Escape does not reach the document");
  });
});
