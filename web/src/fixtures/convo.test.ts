import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { type ComponentChild, render } from "preact";
import { act } from "preact/test-utils";

import { Page } from "../app.stories";
import { epicEventOf, stepEventOf } from "../eventring";
import type { ConversationItem, WatchConversationResponse } from "../gen/orrery/v1/orrery_pb";
import { mockTimers } from "../testclock";
import { installTestDom, removeTestDom, teardownOnce } from "../testdom";
import { turn, until } from "../testwait";
import { at, liveEpoch } from "./clock";
import { convoLoading, convoSlices } from "./convo";
import { shippedSlices } from "./slices";
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

// shapeOf names an item by its kind, and by its sender and round when it is a prompt, or by its notice or trigger.
function shapeOf(item: ConversationItem): string {
  switch (item.kind) {
    case "prompt":
      return item.round > 0 ? `prompt/${item.sender}/${item.round}` : `prompt/${item.sender}`;
    case "result":
      return item.error ? "result/error" : "result";
    case "notice":
      return item.error ? `notice/${item.notice}/error` : `notice/${item.notice}`;
    case "agent_finished":
      return `agent_finished/${item.notice}`;
    case "compaction":
      return `compaction/${item.trigger}`;
    default:
      return item.kind;
  }
}

// factsOf is a step's facts as the step-end item carries them.
function factsOf(item: ConversationItem) {
  const facts = item.stepFacts;
  return facts === undefined
    ? undefined
    : { rounds: facts.rounds, seconds: facts.seconds, counted: facts.counted, files: facts.files, added: facts.added, removed: facts.removed };
}

test("ConvoWorld: blocks is served for pane %25 holding round 6's items in order, as the server sends them", async () => {
  const frame = await paneFrame(convoSlices, "%25");
  assert.deepEqual(frame.items.map(shapeOf), [
    "prompt/owner",
    "tool",
    "result",
    "prompt/event",
    "prompt/event",
    "prompt/event",
    "prompt/event",
    "prompt/engine/2",
    "prompt/engine/3",
    "prompt/event",
    "prompt/event",
    "prompt/event",
    "prompt/event",
  ]);
});

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

test("ConvoWorld: blocks opens on an earlier day than the fixtures' instant, so a day rule opens its transcript", async () => {
  const now = Date.parse(liveEpoch) / 1000;
  const [first] = (await paneFrame(convoSlices, "%25")).items;
  assert.ok(first.time < BigInt(now), "the first item is before the instant");
  assert.notEqual(new Date(Number(first.time) * 1000).toDateString(), new Date(now * 1000).toDateString());
});

test("ConvoWorld: blocks' three step ends carry the step facts of round 6's rows", async () => {
  const items = (await paneFrame(convoSlices, "%25")).items.filter((item) => stepEventOf(item.text) !== null);
  assert.deepEqual(items.map((item) => item.promptId), ["step:st-rota-csv", "step:st-rota-store", "step:st-rota-audit"]);
  assert.deepEqual(items.map(factsOf), [
    { rounds: 1, seconds: 480n, counted: true, files: 2n, added: 96n, removed: 4n },
    { rounds: 3, seconds: 780n, counted: true, files: 4n, added: 141n, removed: 12n },
    { rounds: 0, seconds: 60n, counted: false, files: 0n, added: 0n, removed: 0n },
  ]);
});

test("ConvoWorld: lines is served for pane %26 holding round 6's items in order, as the server sends them", async () => {
  const frame = await paneFrame(convoSlices, "%26");
  assert.deepEqual(frame.items.map(shapeOf), [
    "prompt/owner",
    "tool",
    "result",
    "tool",
    "result",
    "tool",
    "result/error",
    "tool",
    "result",
    "tool",
    "result",
    "tool",
    "result",
    "tool",
    "result",
    "tool",
    "result/error",
    "notice/task",
    "notice/teammate",
    "notice/error/error",
    "agent_finished/done",
    "agent_finished/failed",
    "agent_finished/stopped",
    "interrupted",
    "compaction/manual",
    "prompt/brief",
    "prompt/engine",
    "reply",
  ]);
  assert.deepEqual(frame.unfinished.map((text) => text.kind), ["thinking"], "the thinking is the live unfinished block");
});

test("ConvoWorld: lines' seen falls between the fold before the reply and the reply, so the new rule draws before the reply", async () => {
  const frame = await paneFrame(convoSlices, "%26");
  const seen = frame.state?.pane?.seen ?? 0n;
  const newIndex = frame.items.findIndex((item) => item.kind !== "result" && item.time > seen);
  assert.equal(frame.items[newIndex]?.kind, "reply");
});

test("ConvoWorld: lines' compaction is manual at 188k tokens, and its summary is the one the rule opens to", async () => {
  const compaction = (await paneFrame(convoSlices, "%26")).items.find((item) => item.kind === "compaction");
  assert.equal(compaction?.tokens, 188000n);
  assert.equal(compaction?.time, at("@09:42"));
});

test("ConvoWorld: find types its query into pane %11's transcript, which holds an owner prompt that query matches", async () => {
  const prompts = (await paneFrame(shippedSlices, "%11")).items.filter((item) => item.kind === "prompt");
  assert.ok(prompts.some((item) => item.text.includes("jitter")), "the prompt the query 'jitter' finds");
});

test("ConvoWorld: markdown reuses orchard's pane %13 as the shipped transcript: an owner prompt, then a reply", async () => {
  const frame = await paneFrame(shippedSlices, "%13");
  assert.deepEqual(frame.items.map(shapeOf), ["prompt/owner", "reply"]);
});

test("ConvoWorld: shell is pane %23, a terminal with no Claude session and no transcript", async () => {
  const frame = await paneFrame(shippedSlices, "%23");
  assert.equal(frame.items.length, 0);
});

test("ConvoWorld: empty is pane %27, a session with no items", async () => {
  const frame = await paneFrame(convoSlices, "%27");
  assert.equal(frame.items.length, 0);
});

test("ConvoWorld: loading holds Panes.WatchConversation unanswered while the page loads", async () => {
  assert.ok(convoLoading.hold?.has("orrery.v1.Panes/WatchConversation"), "the conversation call is held");
  const fixture = createFixtureTransport(convoLoading);
  const call = new AbortController();
  let answered = false;
  const frames = fixture.clients.panes.watchConversation({ pane: "%28" }, { signal: call.signal })[Symbol.asyncIterator]();
  const pending = frames.next().then(
    () => {
      answered = true;
    },
    () => {
      // The call's abort below ends it with a cancellation, which is the end this test wants.
    },
  );
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(answered, false, "the held call sends no frame before it is aborted");
  call.abort();
  await pending;
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
