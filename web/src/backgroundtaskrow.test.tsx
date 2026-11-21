import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { useEffect, useState } from "preact/hooks";
import { act } from "preact/test-utils";

import { agentFinishedTask, TaskLine } from "./backgroundrow";
import { createClients, createTransport } from "./client";
import type { Conversation } from "./conversation";
import { findMatches } from "./find";
import { follow } from "./follow";
import {
  type BackgroundTask,
  BackgroundTaskSchema,
  type ConversationItem,
  ConversationItemSchema,
  ConversationStateSchema,
} from "./gen/orrery/v1/orrery_pb";
import { Icon, type IconName } from "./glyphs";
import { Reader } from "./reader";
import { assertSame, installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

// The background rows over the reader and the fake transport: a reader follows a session's conversation through a fake
// WatchConversation stream that the test feeds, so its state's background list and its items arrive as the server sends
// them. Each test's name carries the token BackgroundTaskRow.

const watchPath = "/orrery.v1.Sessions/WatchConversation";
const sessionId = "s1";
// seenAt is past every item's time, so the reader draws no since-you-looked rule among the rows the tests count.
const seenAt = Number.POSITIVE_INFINITY;

type Clients = ReturnType<typeof createClients>;

// envelope frames a stream message as Connect's JSON streaming frames it: a flag of 0, the payload's length, the payload.
function envelope(message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = 0;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// FakeSessions answers the session's WatchConversation with one stream the test feeds through emit. Its clients are the
// transport the reader's follow reads through.
class FakeSessions {
  private controller: ReadableStreamDefaultController<Uint8Array> | null = null;

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(new Request(input, init).url).pathname;
    if (path !== watchPath) {
      throw new Error(`the test answers no call to ${path}`);
    }
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  readonly clients: Clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  get open(): boolean {
    return this.controller !== null;
  }

  emit(message: object): void {
    if (this.controller === null) {
      throw new Error("the watch stream is not open");
    }
    this.controller.enqueue(envelope(message));
  }

  close(): void {
    this.controller?.close();
    this.controller = null;
  }
}

// Following draws a reader over the session's conversation as the app's follow holds it.
function Following({ clients }: { clients: Clients }) {
  const [conversation, setConversation] = useState<Conversation | null>(null);
  useEffect(() => {
    const followed = follow(clients, { kind: "session", id: sessionId }, setConversation);
    return followed.stop;
  }, [clients]);
  return <Reader conversation={conversation} seenAt={seenAt} open={false} />;
}

interface Page {
  container: HTMLElement;
  fake: FakeSessions;
}

// withPage draws the following reader in a fresh DOM once its stream is open, runs check on it, then unmounts the reader,
// closes the stream and removes the DOM, in that order, so the follow has stopped before its stream ends.
async function withPage(t: TestContext, check: (page: Page) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const fake = new FakeSessions();
  try {
    await act(() => {
      render(<Following clients={fake.clients} />, container);
    });
    await settle(t, () => fake.open, "the session's stream opens");
    await check({ container, fake });
  } finally {
    await act(() => {
      render(null, container);
    });
    fake.close();
    container.remove();
    await removeTestDom(window);
  }
}

// settle waits until check holds, each round running inside act, so the renders a message causes are applied before the
// next check. It ends with the test's own signal, so a wait that never holds fails the test by name.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

// recordScrolls replaces scrollIntoView on the test window's elements with one that records the element it is called on:
// happy-dom has no layout, so its own does nothing. The record holds the elements in the order they were scrolled.
function recordScrolls(t: TestContext): Element[] {
  const scrolled: Element[] = [];
  t.mock.method(HTMLElement.prototype, "scrollIntoView", function (this: Element) {
    scrolled.push(this);
  });
  return scrolled;
}

// delta is a WatchConversation message holding items, and the state's background list when one is given. The server sends
// the whole background list with each state it sends.
function delta(
  items: ConversationItem[],
  options: { seq: number; reset?: boolean; background?: BackgroundTask[] },
): object {
  const state =
    options.background === undefined ? undefined : create(ConversationStateSchema, { background: options.background });
  return {
    transcript: `${sessionId}.jsonl`,
    seq: String(options.seq),
    reset: options.reset ?? false,
    items: items.map((entry) => toJson(ConversationItemSchema, entry)),
    state: state === undefined ? undefined : toJson(ConversationStateSchema, state),
  };
}

// task is a background task the session reports, its fields given as the server gives them.
function task(init: MessageInitShape<typeof BackgroundTaskSchema>): BackgroundTask {
  return create(BackgroundTaskSchema, init);
}

// item is a conversation item, as the server holds it.
function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

// RowFields are the text a background row draws: its label, its description, its latest activity, and its status once
// it has ended.
interface RowFields {
  label: string;
  description: string;
  activity: string;
  status: string;
}

// rowsOf are the lines a background task draws: a TaskLine, which the reader draws for each task the state holds.
function rowsOf(root: ParentNode): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>('[data-shape="line"].task')];
}

function fieldsOf(row: HTMLElement): RowFields {
  return {
    label: textOf(row, ".name"),
    description: textOf(row, ".arg"),
    activity: textOf(row, ".more"),
    status: textOf(row, ".end"),
  };
}

// iconMarkup is the markup Icon draws for a name, so a test can compare the icon a line draws with it. The test DOM is
// installed by the caller.
function iconMarkup(name: IconName): string {
  const probe = document.createElement("div");
  render(<Icon name={name} />, probe);
  const markup = probe.innerHTML;
  render(null, probe);
  return markup;
}

function textOf(root: ParentNode, selector: string): string {
  return root.querySelector(selector)?.textContent ?? "";
}

function textsOf(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

function countOf(root: ParentNode, selector: string): number {
  return root.querySelectorAll(selector).length;
}

// callNamed is the call line of the tool named tool, the one call of that tool the reader draws.
function callNamed(root: ParentNode, tool: string): HTMLElement {
  const call = [...root.querySelectorAll<HTMLElement>(".call")].find((element) => textOf(element, "b") === tool);
  if (call === undefined) {
    throw new Error(`the ${tool} call is drawn`);
  }
  return call;
}

// runButton is the button that opens and folds the run of tool calls the reader draws.
function runButton(root: ParentNode): HTMLElement {
  const button = root.querySelector<HTMLElement>(".fold.calls");
  if (button === null) {
    throw new Error("the run of tool calls is drawn");
  }
  return button;
}

// click clicks a control inside act, so the render its click causes has run before the next check.
async function click(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

test("BackgroundTaskRow: a running agent task and a running shell task each draw a running line with their type label, description and latest activity", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const background = [
      task({
        id: "a1",
        toolUseId: "toolu_1",
        description: "Map the repo",
        type: "agent",
        subagentType: "Explore",
        status: "running",
        activity: "Read the readme",
        started: 100n,
      }),
      task({
        id: "b1",
        description: "npm run watch",
        type: "shell",
        status: "running",
        activity: "compiled 12 files",
        started: 101n,
      }),
    ];
    fake.emit(delta([], { seq: 2, reset: true, background }));
    await settle(t, () => rowsOf(container).length === 2, "both rows are drawn");
    const [agent, shell] = rowsOf(container);
    assert.deepEqual(fieldsOf(agent), {
      label: "Explore",
      description: "Map the repo",
      activity: "Read the readme",
      status: "",
    });
    assert.deepEqual(fieldsOf(shell), {
      label: "shell",
      description: "npm run watch",
      activity: "compiled 12 files",
      status: "",
    });
    assert.equal(countOf(container, ".line.task.running"), 2, "each running task draws a running line");
    assert.equal(countOf(container, ".running-mark"), 2, "each running line carries the running mark");
    assert.equal(countOf(container, "button.line.task"), 1, "the agent row is a link, and the shell row is not");
  });
});

test("BackgroundTaskRow: an agent task that names no sub-agent type is labelled agent, and one with no tool use is no link", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const background = [
      task({ id: "a2", description: "Plan the change", type: "agent", status: "running", activity: "", started: 100n }),
    ];
    fake.emit(delta([], { seq: 2, reset: true, background }));
    await settle(t, () => rowsOf(container).length === 1, "the row is drawn");
    assert.deepEqual(fieldsOf(rowsOf(container)[0]), {
      label: "agent",
      description: "Plan the change",
      activity: "",
      status: "",
    });
    assert.equal(countOf(container, "button.background-row"), 0, "a task with no tool use is no link");
  });
});

test("BackgroundTaskRow: a later delta with new activity updates a running row in place", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const running = (activity: string) => [
      task({
        id: "a1",
        toolUseId: "toolu_1",
        description: "Map the repo",
        type: "agent",
        subagentType: "Explore",
        status: "running",
        activity,
        started: 100n,
      }),
    ];
    fake.emit(delta([], { seq: 2, reset: true, background: running("Read the readme") }));
    await settle(t, () => textOf(container, ".more") === "Read the readme", "the first activity is drawn");
    fake.emit(delta([], { seq: 3, background: running("Ran the tests") }));
    await settle(t, () => textOf(container, ".more") === "Ran the tests", "the later activity is drawn");
    assert.equal(rowsOf(container).length, 1, "the row is updated, not drawn again");
  });
});

test("BackgroundTaskRow: a later delta that ends a task draws its status, and no live mark", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const shell = (status: string, ended: bigint) =>
      task({
        id: "b1",
        description: "npm run watch",
        type: "shell",
        status,
        activity: "compiled 12 files",
        started: 101n,
        ended,
      });
    fake.emit(delta([], { seq: 2, reset: true, background: [shell("running", 0n)] }));
    await settle(t, () => countOf(container, ".line.task.running") === 1, "the running row is running");
    fake.emit(delta([], { seq: 3, background: [shell("done", 120n)] }));
    await settle(t, () => textOf(container, ".end") === "finished", "the ended row draws its end word");
    assert.equal(countOf(container, ".line.task.running"), 0, "an ended task draws no running line");
    assert.equal(countOf(container, ".running-mark"), 0, "an ended task draws no running mark");
    assert.equal(rowsOf(container).length, 1, "the ended task is the same row");
  });
});

test("BackgroundTaskRow: a click on an agent row brings its Agent call into view and opens its result", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const scrolled = recordScrolls(t);
    const items = [
      item({ kind: "tool", time: 100n, tool: "Agent", input: "Map the repo", id: "toolu_1" }),
      item({ kind: "result", time: 101n, id: "toolu_1", text: "Mapped it" }),
    ];
    const background = [
      task({
        id: "a1",
        toolUseId: "toolu_1",
        description: "Map the repo",
        type: "agent",
        subagentType: "Explore",
        status: "running",
        activity: "",
        started: 100n,
      }),
    ];
    fake.emit(delta(items, { seq: 2, reset: true, background }));
    await settle(t, () => rowsOf(container).length === 1, "the row is drawn");
    assert.equal(countOf(container, ".result"), 0, "the Agent call's result is folded before the click");

    await click(rowsOf(container)[0]);
    await settle(t, () => countOf(container, ".result") === 1, "the Agent call's result opens");
    assert.deepEqual(textsOf(container, ".result"), ["Mapped it"]);
    assert.equal(scrolled.length, 1, "the call is brought into view once");
    assertSame(scrolled[0], callNamed(container, "Agent"), "the element brought into view is the Agent call");
  });
});

test("BackgroundTaskRow: a click on an agent row whose call is folded in a run opens the run and brings the call into view, once", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const scrolled = recordScrolls(t);
    const items = [
      item({ kind: "tool", time: 100n, tool: "Read", input: "README.md", id: "r1" }),
      item({ kind: "tool", time: 101n, tool: "Agent", input: "Map the repo", id: "toolu_1" }),
      item({ kind: "tool", time: 102n, tool: "Grep", input: "TODO", id: "g1" }),
      item({ kind: "result", time: 103n, id: "r1", text: "ok" }),
      item({ kind: "result", time: 104n, id: "toolu_1", text: "Mapped it" }),
      item({ kind: "result", time: 105n, id: "g1", text: "none" }),
    ];
    const background = [
      task({
        id: "a1",
        toolUseId: "toolu_1",
        description: "Map the repo",
        type: "agent",
        subagentType: "Explore",
        status: "running",
        activity: "",
        started: 100n,
      }),
    ];
    fake.emit(delta(items, { seq: 2, reset: true, background }));
    await settle(t, () => countOf(container, ".fold.calls") === 1, "the run is drawn folded");
    assert.equal(countOf(container, "div.calls"), 0, "the run is folded before the click");

    await click(rowsOf(container)[0]);
    await settle(t, () => countOf(container, ".result") === 1, "the run opens, and the Agent call's result with it");
    assert.deepEqual(textsOf(container, ".result"), ["Mapped it"]);
    assert.equal(scrolled.length, 1, "the call is brought into view once");
    assertSame(scrolled[0], callNamed(container, "Agent"), "the element brought into view is the Agent call");

    // Folding the run and opening it again draws the call folded, and brings nothing into view a second time.
    await click(runButton(container));
    assert.equal(countOf(container, "div.calls"), 0, "the run folds");
    await click(runButton(container));
    await settle(t, () => countOf(container, "div.calls") === 1, "the run opens again");
    assert.equal(countOf(container, ".result"), 0, "the Agent call's result is folded again");
    assert.equal(scrolled.length, 1, "reopening the run brings no call into view");
  });
});

test("BackgroundTaskRow: an agent_finished item draws a TaskLine ending finished, failed or stopped, and find searches its name and description", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const items = [
      item({ kind: "agent_finished", time: 100n, text: "Map the repo", id: "toolu_1", tool: "Explore", notice: "done" }),
      item({ kind: "agent_finished", time: 101n, text: "Run the suite", id: "toolu_2", notice: "failed" }),
      item({ kind: "agent_finished", time: 102n, text: "Watch the build", id: "toolu_3", notice: "stopped" }),
    ];
    fake.emit(delta(items, { seq: 2, reset: true }));
    await settle(t, () => rowsOf(container).length === 3, "the three lines are drawn");
    assert.deepEqual(
      rowsOf(container).map((row) => textOf(row, ".arg")),
      ["Map the repo", "Run the suite", "Watch the build"],
    );
    assert.deepEqual(
      rowsOf(container).map((row) => textOf(row, ".end")),
      ["finished", "failed", "stopped"],
    );
    assert.deepEqual(findMatches(items, "Map the repo"), [{ item: 0, start: 8 }], "find searches the line's description after its name");
  });
});

test("Shapes: a background task draws one TaskLine: the agent icon, its type, its description and its end word", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const background = [
      task({
        id: "a1",
        toolUseId: "toolu_1",
        description: "Map the repo",
        type: "agent",
        subagentType: "Explore",
        status: "done",
        started: 100n,
        ended: 120n,
      }),
    ];
    fake.emit(delta([], { seq: 2, reset: true, background }));
    await settle(t, () => rowsOf(container).length === 1, "the row is drawn");
    assert.equal(countOf(container, '[data-shape="line"]'), 1, "the task draws more or fewer than one line");
    const line = rowsOf(container)[0];
    assert.equal(line.classList.contains("line"), true, "the line is not of class line");
    assert.equal(line.innerHTML.includes(iconMarkup("agent")), true, "the agent icon does not lead the line");
    assert.equal(textOf(line, ".name"), "Explore");
    assert.equal(textOf(line, ".arg"), "Map the repo");
    assert.equal(textOf(line, ".end"), "finished", "a done task ends with the word finished");
  });
});

test("Shapes: an agent_finished item draws one TaskLine: the agent icon, its subagent type, its description and its end word", async () => {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    const done = item({ kind: "agent_finished", time: 100n, text: "Map the repo", id: "toolu_1", tool: "Explore", notice: "done" });
    const failed = item({
      kind: "agent_finished",
      time: 101n,
      text: "Run the suite",
      id: "toolu_2",
      tool: "general-purpose",
      notice: "failed",
    });
    render(<TaskLine {...agentFinishedTask(done)} />, container);
    const lines = container.querySelectorAll<HTMLElement>('[data-shape="line"]');
    assert.equal(lines.length, 1, "the item draws more or fewer than one line");
    assert.equal(lines[0].classList.contains("task"), true, "the line is not of class task");
    assert.equal(lines[0].innerHTML.includes(iconMarkup("agent")), true, "the agent icon does not lead the line");
    assert.equal(textOf(lines[0], ".name"), "Explore");
    assert.equal(textOf(lines[0], ".arg"), "Map the repo");
    assert.equal(textOf(lines[0], ".end"), "finished", "a done agent's end word is finished");

    render(<TaskLine {...agentFinishedTask(failed)} />, container);
    assert.equal(textOf(container, ".name"), "general-purpose");
    assert.equal(textOf(container, ".end"), "failed", "a failed agent's end word is its notice");
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
});
