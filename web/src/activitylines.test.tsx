import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { useState } from "preact/hooks";
import { act } from "preact/test-utils";

import { ActivityLines } from "./activitylines";
import { type BackgroundTask, BackgroundTaskSchema } from "./gen/orrery/v1/orrery_pb";
import { ListPanel, listHead, SidePanel } from "./sidepanel";
import { assertNone, assertSame, installTestDom, removeTestDom } from "./testdom";
import { type ActivityLine, activityOf, type WorkConversation } from "./work";

// Each test renders ActivityLines on its own, into a container on the document, so a press bubbles to the document as
// it does in the page. The clock is a fixed now in unix seconds, so the times the lines draw are the ones the fixture
// makes.

const now = 1_700_000_000;

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the element into the container, and flushes its effects.
function draw(container: HTMLElement, element: JSX.Element): void {
  act(() => {
    render(element, container);
  });
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// textsOf returns the text of each element under root that matches selector.
function textsOf(root: ParentNode, selector: string): string[] {
  return all(root, selector).map((element) => element.textContent ?? "");
}

// runningTask is a running background task of the type given, started ago seconds before now, with what init sets.
function runningTask(
  id: string,
  type: string,
  description: string,
  ago: number,
  init: MessageInitShape<typeof BackgroundTaskSchema> = {},
): BackgroundTask {
  return create(BackgroundTaskSchema, {
    id,
    description,
    type,
    status: "running",
    started: BigInt(now - ago),
    ...init,
  });
}

// conversationOf is a conversation of the session s1, not a pane, that runs the background tasks given.
function conversationOf(background: BackgroundTask[]): WorkConversation {
  return { session: "s1", pane: false, background };
}

// Harness draws the lines the way the page does: a press opens the list panel of the line's kind, which lights the
// line, and the panel is a side panel named by its list. It holds the open panel in its own state, as the page holds it.
function Harness({ lines, background }: { lines: ActivityLine[]; background: BackgroundTask[] }): JSX.Element {
  const [open, setOpen] = useState<"agents" | "running" | null>(null);
  const kind = open === "agents" ? "agent" : "shell";
  const tasks = background.filter((task) => task.status === "running" && task.type === kind);
  return (
    <>
      <ActivityLines lines={lines} now={now} open={open} onOpen={(kind) => setOpen(kind)} />
      {open !== null && (
        <SidePanel label={listHead(open)} tone="live" href="#" onClose={() => setOpen(null)}>
          <ListPanel kind={open} tasks={tasks} now={now} />
        </SidePanel>
      )}
    </>
  );
}

test("ActivityLines: nothing running draws no lines", () =>
  withDom((container) => {
    const lines = activityOf(conversationOf([]));
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    assert.equal(container.innerHTML, "");
    assertNone(container.querySelector('[aria-label="running now"]'));
  }));

test("ActivityLines: one agent's line shows who, what and its time, and sits immediately above the composer", () =>
  withDom((container) => {
    const task = runningTask("t1", "agent", "Find every caller of keeperFor", 125, {
      subagentType: "Explore",
      activity: "Grep keeperFor\\(",
    });
    const lines = activityOf(conversationOf([task]));
    // The composer as the reader column draws it: the lines first, then the reply box the dock is in.
    draw(
      container,
      <div class="send">
        <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />
        <div class="reply">the composer</div>
      </div>,
    );

    const group = one(container, '[aria-label="running now"]');
    const line = one(group, ".activity-line");
    assert.equal(one(line, ".who").textContent, "Explore");
    assert.equal(one(line, ".what").textContent, "Find every caller of keeperFor");
    assert.equal(one(line, ".doing").textContent, "Grep keeperFor\\(");
    assert.equal(one(line, ".clock").textContent, "2:05");
    assertSame(group.nextElementSibling, one(container, ".reply"), "the lines sit immediately above the composer");
  }));

test("ActivityLines: agents come before shells, each kind in the order it started", () =>
  withDom((container) => {
    const lines = activityOf(
      conversationOf([
        runningTask("shell", "shell", "just dev", 900),
        runningTask("late", "agent", "Review the change", 30, { subagentType: "reviewer" }),
        runningTask("early", "agent", "Find the caller", 90, { subagentType: "Explore" }),
      ]),
    );
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    assert.deepEqual(textsOf(container, ".who"), ["Explore", "reviewer", "shell"]);
    assert.deepEqual(textsOf(container, ".what"), ["Find the caller", "Review the change", "just dev"]);
  }));

test("ActivityLines: four running fold to three lines and 'and 1 more running'", () =>
  withDom((container) => {
    const lines = activityOf(
      conversationOf([
        runningTask("a1", "agent", "Find the caller", 90, { subagentType: "Explore" }),
        runningTask("a2", "agent", "Review the change", 48, { subagentType: "reviewer" }),
        runningTask("s1", "shell", "just dev", 2460),
        runningTask("s2", "shell", "sleep 1800 && just warm", 720),
      ]),
    );
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    assert.deepEqual(textsOf(container, ".activity-line:not(.more) .what"), [
      "Find the caller",
      "Review the change",
      "just dev",
    ]);
    const more = one(container, ".activity-line.more");
    assert.equal(more.textContent, "and 1 more running");
    assert.equal(container.textContent?.includes("sleep 1800 && just warm"), false);
  }));

test("ActivityLines: the more line opens agents when a folded line is an agent, else running", () => {
  const opened: string[] = [];
  return withDom((container) => {
    const agents = activityOf(
      conversationOf([1, 2, 3, 4].map((index) => runningTask(`a${index}`, "agent", `Agent ${index}`, index * 10))),
    );
    draw(container, <ActivityLines lines={agents} now={now} open={null} onOpen={(kind) => opened.push(kind)} />);
    act(() => one(container, ".activity-line.more").click());

    const shells = activityOf(
      conversationOf([1, 2, 3, 4].map((index) => runningTask(`s${index}`, "shell", `Shell ${index}`, index * 10))),
    );
    draw(container, <ActivityLines lines={shells} now={now} open={null} onOpen={(kind) => opened.push(kind)} />);
    act(() => one(container, ".activity-line.more").click());

    assert.deepEqual(opened, ["agents", "running"]);
  });
});

test("ActivityLines: an ended task has no line", () =>
  withDom((container) => {
    const lines = activityOf(
      conversationOf([
        runningTask("done", "agent", "Finished scan", 300, {
          subagentType: "Explore",
          status: "done",
          ended: BigInt(now - 200),
        }),
        runningTask("failed", "shell", "Dead server", 300, { status: "failed", ended: BigInt(now - 100) }),
        runningTask("live", "agent", "Still reviewing", 40, { subagentType: "reviewer" }),
      ]),
    );
    draw(container, <ActivityLines lines={lines} now={now} open={null} onOpen={() => undefined} />);

    assert.deepEqual(textsOf(container, ".activity-line .who"), ["reviewer"]);
    assert.deepEqual(textsOf(container, ".activity-line .what"), ["Still reviewing"]);
    assert.equal(container.textContent?.includes("Finished scan"), false);
    assert.equal(container.textContent?.includes("Dead server"), false);
  }));

test("ActivityLines: pressing a line opens its list panel and lights the line", () =>
  withDom((container) => {
    const background = [
      runningTask("agent", "agent", "Find the caller", 90, { subagentType: "Explore" }),
      runningTask("shell", "shell", "just dev", 2460),
    ];
    draw(container, <Harness lines={activityOf(conversationOf(background))} background={background} />);
    assertNone(container.querySelector('aside[aria-label="Sub-agents"]'));
    assert.equal(one(container, ".activity-line:not(.more)").classList.contains("on"), false);

    const [agentLine, shellLine] = all(container, ".activity-line");
    act(() => agentLine.click());

    const panel = one(container, 'aside[aria-label="Sub-agents"]');
    assert.equal(one(panel, ".title").textContent, "Find the caller");
    assert.equal(agentLine.classList.contains("on"), true);
    assert.equal(shellLine.classList.contains("on"), false);
  }));
