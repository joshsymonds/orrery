import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { type ComponentChild, render } from "preact";
import { act } from "preact/test-utils";

import { Page } from "../app.stories";
import { epicEventOf, stepEventOf } from "../eventring";
import type { WatchConversationResponse } from "../gen/orrery/v1/orrery_pb";
import { mockTimers } from "../testclock";
import { installTestDom, removeTestDom, teardownOnce } from "../testdom";
import { turn, until } from "../testwait";
import { convoSlices } from "./convo";
import { createFixtureTransport } from "./transport";

// paneFrame reads the first frame Panes.WatchConversation sends for a pane, then ends the call.
async function paneFrame(
  slices: typeof convoSlices,
  pane: string,
): Promise<WatchConversationResponse> {
  assert.ok(slices.paneStreams.has(pane), `pane ${pane} has a conversation stream`);
  const fixture = createFixtureTransport(slices);
  const call = new AbortController();
  const frames = fixture.clients.panes.watchConversation({ pane }, { signal: call.signal })[Symbol.asyncIterator]();
  const frame = await frames.next();
  call.abort();
  assert.ok(!frame.done, `pane ${pane} sends its frame`);
  return frame.value;
}


test("ConvoWorld: blocks' engine events parse as the epic and step events round 6 draws", async () => {
  const items = (await paneFrame(convoSlices, "%25")).items;
  const steps = items.filter((item) => stepEventOf(item.text) !== null).map((item) => stepEventOf(item.text)?.end);
  assert.deepEqual(steps, ["done", "checks_failed", "stopped"]);
  const epicKinds = items
    .filter((item) => item.sender === "event")
    .map((item) => epicEventOf(item.text)?.kind)
    .filter((kind) => kind !== undefined);
  assert.deepEqual(epicKinds, ["park", "release", "end", "question"]);
});

// The words convo-blocks types into its reply box and sends while the say call is held.
const sentWords = "Keep the pin and skip the Lisbon case.";

// following is compareDocumentPosition's bit for a node that comes after the one it is called on.
const following = 4;

// mountPage draws story into a fresh DOM at the wide width, as the transport tests draw a page story, and waits until the
// story's first load has answered and drawn holds on its root. The story is unmounted and its DOM removed when check returns
// or throws, or as soon as the test's signal aborts. The test must call mockTimers first.
async function mountPage(
  t: TestContext,
  story: () => ComponentChild,
  drawn: (root: HTMLElement) => boolean,
  check: (root: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = 1280;
  const root = document.createElement("div");
  document.body.append(root);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, root);
    });
    root.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(story(), root);
    });
    await until(
      t.signal,
      () => root.querySelector("[data-loaded]") !== null && drawn(root),
      "the story's page",
      { step: () => act(turn) },
    );
    check(root);
  } finally {
    await teardown();
  }
}

// readerOf is the reader column of the page, which holds the conversation's rows, or null when none is drawn.
function readerOf(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>("section.read");
}

// blockWith is the first block of the reader whose text includes words, or null when none does.
function blockWith(reader: HTMLElement, words: string): HTMLElement | null {
  return Array.from(reader.querySelectorAll<HTMLElement>(".block")).find((block) => (block.textContent ?? "").includes(words)) ?? null;
}

test("ConvoWorld: working draws one New since you looked rule, after the 14:31 prompt and before the tool calls that follow it", (t) => {
  mockTimers(t, ["setTimeout"]);
  return mountPage(
    t,
    Page["convo-working"],
    (root) => (readerOf(root)?.textContent ?? "").includes("Cap the total retry window"),
    (root) => {
      const reader = readerOf(root);
      assert.ok(reader !== null, "the reader is drawn");
      const rules = reader.querySelectorAll(".rule.new");
      assert.equal(rules.length, 1, "one new rule is drawn");
      const prompt = blockWith(reader, "Cap the total retry window");
      assert.ok(prompt !== null, "the 14:31 prompt is drawn");
      const tool = Array.from(reader.querySelectorAll<HTMLElement>(".line.run, .line.tool")).find(
        (line) => prompt.compareDocumentPosition(line) === following,
      );
      assert.ok(tool !== undefined, "a tool call follows the 14:31 prompt");
      assert.equal(prompt.compareDocumentPosition(rules[0]), following, "the rule comes after the 14:31 prompt");
      assert.equal(rules[0].compareDocumentPosition(tool), following, "the rule comes before the first tool call after the prompt");
    },
  );
});

test("ConvoWorld: blocks ends with a sending YouBlock of the words typed into the reply box, which stays sending while the say call is held", (t) => {
  mockTimers(t, ["setTimeout"]);
  return mountPage(
    t,
    Page["convo-blocks"],
    (root) => blockWith(readerOf(root) ?? root, sentWords)?.classList.contains("pending") === true,
    (root) => {
      const reader = readerOf(root);
      assert.ok(reader !== null, "the reader is drawn");
      const blocks = reader.querySelectorAll<HTMLElement>(".block");
      const last = blocks[blocks.length - 1];
      assert.ok(last.classList.contains("you"), "the last block is the owner's");
      assert.ok(last.classList.contains("pending"), "the last block is still sending");
      assert.equal(last.querySelector(".label")?.textContent, "SENDING", "the block is labelled SENDING");
      assert.equal(last.textContent?.includes(sentWords), true, "the block holds the words sent");
    },
  );
});

// openedLines names each thing round 6 opens in convo-lines that is not open: the thought, the failed call, the tool run,
// the compaction summary and the brief. It is empty once all five are open.
function unopenedLines(root: HTMLElement): string[] {
  const reader = readerOf(root);
  if (reader === null) {
    return ["the reader"];
  }
  const opened = Array.from(reader.querySelectorAll<HTMLElement>(".opened, .compaction.open")).map((element) => element.textContent ?? "");
  const checks: [string, boolean][] = [
    ["the thought", opened.some((text) => text.includes("The page renders one row per shift"))],
    ["the failed call", opened.some((text) => text.includes("--- FAIL: TestSlowWeek"))],
    ["the tool run", reader.querySelector(".opened.calls") !== null],
    ["the compaction summary", opened.some((text) => text.includes("The roster page was slow because"))],
    ["the brief", opened.some((text) => text.includes("Implement the week query"))],
  ];
  return checks.filter(([, drawn]) => !drawn).map(([name]) => name);
}

test("ConvoWorld: lines opens the thought, the failed call, the tool run, the compaction summary and the brief, as round 6 does", (t) => {
  mockTimers(t, ["setTimeout"]);
  return mountPage(
    t,
    Page["convo-lines"],
    (root) => unopenedLines(root).length === 0,
    (root) => {
      assert.deepEqual(unopenedLines(root), [], "every item round 6 opens is open");
    },
  );
});
