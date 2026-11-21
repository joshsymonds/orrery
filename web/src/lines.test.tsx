import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { ConversationItemSchema, type ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { Icon, type IconName } from "./glyphs";
import { EngineFold, NoticeLine, ThinkingLine, ToolLine, ToolRun, type RunCall } from "./lines";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom } from "./testdom";

// withDom renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withDom(
  element: JSX.Element,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// clickControl clicks a control inside act, so the render its click causes has run before the next check.
async function clickControl(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

// iconMarkup is the drawing of a named icon, as the page draws it, so a line's icon can be compared with it.
function iconMarkup(name: IconName): string | undefined {
  const container = document.createElement("div");
  render(<Icon name={name} />, container);
  const markup = container.querySelector("svg")?.innerHTML;
  render(null, container);
  return markup;
}

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

// call is a tool call under id, made by tool with input.
function call(tool: string, input: string, id = "c1"): ConversationItem {
  return item({ kind: "tool", time: 100n, tool, input, id });
}

// resultOf is the result of the call under id, failed when error is set.
function resultOf(id: string, text: string, error = false): ConversationItem {
  return item({ kind: "result", time: 100n, id, text, error });
}

// runCall is a call of a run under key, with its result when one is given.
function runCall(key: string, tool: string, input: string, result?: ConversationItem): RunCall {
  return { key, item: call(tool, input, key), result, running: false, current: -1 };
}

test("Shapes: a tool call draws one ToolLine with data-shape line, the tool's icon, and its input", async () => {
  await withDom(<ToolLine item={call("Read", "a.txt")} result={undefined} running={false} query="" current={-1} />, (container) => {
    const lines = container.querySelectorAll('[data-shape="line"]');
    assert.equal(lines.length, 1, "one line is drawn");
    assert.equal(lines[0]?.classList.contains("line"), true, "the line carries the line class");
    assert.equal(lines[0]?.classList.contains("tool"), true, "the line carries the tool class");
    assert.equal(lines[0]?.querySelector(".in")?.textContent, "a.txt", "the line draws the call's input");
    assert.equal(lines[0]?.querySelector("svg")?.innerHTML, iconMarkup("doc"), "a read call draws the doc icon");
  });
});

test("Shapes: each kind of tool draws its own icon, and an unknown tool the gear", async () => {
  const kinds: [string, IconName][] = [
    ["Read", "doc"],
    ["Write", "edit"],
    ["Edit", "edit"],
    ["MultiEdit", "edit"],
    ["NotebookEdit", "edit"],
    ["Bash", "shell"],
    ["Grep", "search"],
    ["Glob", "search"],
    ["Agent", "agent"],
    ["Task", "agent"],
    ["WebFetch", "gear"],
  ];
  for (const [tool, name] of kinds) {
    await withDom(<ToolLine item={call(tool, "x")} result={undefined} running={false} query="" current={-1} />, (container) => {
      assert.equal(
        container.querySelector('[data-shape="line"] svg')?.innerHTML,
        iconMarkup(name),
        `${tool} draws the ${name} icon`,
      );
    });
  }
});

test("Shapes: three or more calls in a row draw one ToolRun line, which names the calls", async () => {
  const calls = [
    runCall("a", "Read", "a.txt", resultOf("a", "alpha")),
    runCall("b", "Read", "b.txt", resultOf("b", "beta")),
    runCall("c", "Bash", "make", resultOf("c", "built")),
  ];
  await withDom(<ToolRun calls={calls} query="" />, (container) => {
    const lines = container.querySelectorAll('[data-shape="line"]');
    assert.equal(lines.length, 1, "one run line is drawn");
    assert.equal(lines[0]?.classList.contains("run"), true, "the line carries the run class");
    assert.equal(lines[0]?.querySelector("b")?.textContent, "3 tool calls", "the line counts its calls");
    assert.equal(lines[0]?.querySelector(".names")?.textContent, "Read ×2, Bash", "the line names each tool once");
  });
});

test("Shapes: a failed result marks its tool line with a cross, and a succeeded result does not", async () => {
  await withDom(
    <ToolLine item={call("Bash", "rm -rf build")} result={resultOf("c1", "denied", true)} running={false} query="" current={-1} />,
    (container) => {
      assert.equal(container.querySelector(".fail")?.textContent, "✗", "the failed line draws a cross");
    },
  );
  await withDom(
    <ToolLine item={call("Bash", "make")} result={resultOf("c1", "built")} running={false} query="" current={-1} />,
    (container) => {
      assert.equal(container.querySelector(".fail"), null, "the succeeded line draws no cross");
    },
  );
});

// editResult is the result of the call under id, which counts the lines its file edit added and removed.
function editResult(id: string, added: bigint, removed: bigint): ConversationItem {
  return item({ kind: "result", time: 100n, id, text: "ok", added, removed });
}

test("Shapes: a tool line whose result counts its lines draws its +N and −M, and one with no counts draws neither", async () => {
  await withDom(
    <ToolLine item={call("Edit", "backoff.go")} result={editResult("c1", 14n, 3n)} running={false} query="" current={-1} />,
    (container) => {
      assert.equal(container.querySelector(".more .plus")?.textContent, "+14", "the line draws the lines added");
      assert.equal(container.querySelector(".more .minus")?.textContent, "−3", "the line draws the lines removed");
    },
  );
  await withDom(
    <ToolLine item={call("Edit", "query.go")} result={editResult("c1", 0n, 0n)} running={false} query="" current={-1} />,
    (container) => {
      assert.equal(container.querySelector(".more"), null, "a result with no counts draws no counts");
    },
  );
  await withDom(
    <ToolLine item={call("Edit", "query.go")} result={undefined} running={false} query="" current={-1} />,
    (container) => {
      assert.equal(container.querySelector(".more"), null, "a call with no result draws no counts");
    },
  );
});

test("Shapes: a running call draws the running mark and no rail", async () => {
  await withDom(<ToolLine item={call("Bash", "make")} result={undefined} running={true} query="" current={-1} />, (container) => {
    const line = container.querySelector('[data-shape="line"]');
    assert.equal(line?.classList.contains("running"), true, "the line is drawn running");
    assert.equal(line?.querySelector(".running-mark")?.textContent, "running", "the running mark is drawn");
    assert.equal(line?.querySelector(".rail"), null, "a running line carries no rail");
  });
});

test("Shapes: a call with a result is a button, and its result opens under it as an opened line", (t: TestContext) => {
  mockTimers(t, ["setTimeout"]);
  return withDom(
    <ToolLine item={call("Bash", "make")} result={resultOf("c1", "built")} running={false} query="" current={-1} />,
    async (container) => {
      const line = container.querySelector<HTMLButtonElement>('button[data-shape="line"]');
      assert.ok(line !== null, "a call with a result draws its line as a button");
      assert.equal(line.nextElementSibling === null, true, "no result is drawn before the line is clicked");
      await clickControl(line);
      const opened = line.nextElementSibling;
      assert.equal(opened?.classList.contains("opened"), true, "the result opens under the line");
      assert.equal(opened?.textContent, "built", "the opened line draws the result");
    },
  );
});

// notice is a notice of kind, with text, failed when error is set.
function notice(kind: string, text: string, error = false): ConversationItem {
  return item({ kind: "notice", time: 100n, notice: kind, text, error });
}

// prompt is a prompt sent by sender, in round, with text.
function prompt(sender: string, text: string, round = 0): ConversationItem {
  return item({ kind: "prompt", time: 100n, sender, round, text });
}

test("Shapes: an unfinished thinking block draws one ThinkingLine that opens to its text", (t: TestContext) => {
  mockTimers(t, ["setTimeout"]);
  return withDom(<ThinkingLine text="weighing the handler" />, async (container) => {
    assert.equal(container.querySelectorAll('[data-shape="line"]').length, 1, "one line is drawn");
    const line = container.querySelector<HTMLButtonElement>('button[data-shape="line"].line.thinking');
    assert.ok(line !== null, "the thinking line is a button of the line and thinking classes");
    assert.equal(line.textContent, "thinking", "the line reads thinking");
    assert.equal(line.nextElementSibling === null, true, "no text is drawn before the line is clicked");
    await clickControl(line);
    assert.equal(line.nextElementSibling?.classList.contains("opened"), true, "the text opens under the line");
    assert.equal(line.nextElementSibling?.textContent, "weighing the handler", "the opened line draws the text");
  });
});

test("Shapes: a notice draws one NoticeLine with its notice word as label and its text", () =>
  withDom(<NoticeLine item={notice("task", "Task 3 of 4 is complete.")} query="" current={-1} />, (container) => {
    const lines = container.querySelectorAll('[data-shape="line"]');
    assert.equal(lines.length, 1, "one notice line is drawn");
    assert.equal(lines[0]?.classList.contains("line"), true, "the line carries the line class");
    assert.equal(lines[0]?.classList.contains("notice"), true, "the line carries the notice class");
    assert.equal(lines[0]?.querySelector(".notice-kind")?.textContent, "task", "the label is the notice word");
    assert.equal(
      lines[0]?.textContent?.endsWith("Task 3 of 4 is complete."),
      true,
      "the line draws the notice's text after its label",
    );
    assert.equal(lines[0]?.classList.contains("err"), false, "a notice that is not an error has no error class");
  }));

test("Shapes: an error notice draws its NoticeLine with the error class", () =>
  withDom(
    <NoticeLine item={notice("error", "The API refused the request.", true)} query="" current={-1} />,
    (container) => {
      const line = container.querySelector('[data-shape="line"]');
      assert.equal(line?.classList.contains("err"), true, "the error notice carries the error class");
      assert.equal(line?.querySelector(".notice-kind")?.textContent, "error", "the label is the notice word");
    },
  ));

test("Shapes: a brief draws one EngineFold Brief line with from the engine, and opens to its text", (t: TestContext) => {
  mockTimers(t, ["setTimeout"]);
  return withDom(
    <EngineFold item={prompt("brief", "Goal: export\nOwned: export.go")} query="" current={-1} />,
    async (container) => {
      const line = container.querySelector<HTMLButtonElement>('button[data-shape="line"].line.fold.brief');
      assert.ok(line !== null, "the brief is a button of the line and fold classes");
      assert.equal(line.querySelector("b")?.textContent, "Brief", "the line reads Brief");
      assert.equal(line.querySelector(".names")?.textContent, "from the engine", "the line says from the engine");
      assert.equal(line.nextElementSibling === null, true, "no text is drawn before the line is clicked");
      await clickControl(line);
      assert.equal(line.nextElementSibling?.classList.contains("opened"), true, "the brief opens under the line");
      assert.equal(
        line.nextElementSibling?.textContent?.includes("Owned: export.go"),
        true,
        "the opened line draws the whole brief",
      );
    },
  );
});

test("Shapes: an engine round-0 prompt draws one EngineFold Engine line named by its first line", (t: TestContext) => {
  mockTimers(t, ["setTimeout"]);
  return withDom(
    <EngineFold item={prompt("engine", "watchdog: no progress\nsecond line")} query="" current={-1} />,
    async (container) => {
      const line = container.querySelector<HTMLButtonElement>('button[data-shape="line"].line.fold.engine');
      assert.ok(line !== null, "the engine prompt is a button of the line and fold classes");
      assert.equal(line.querySelector("b")?.textContent, "Engine", "the line reads Engine");
      assert.equal(line.querySelector(".names")?.textContent, "watchdog: no progress", "the line names the first line");
      await clickControl(line);
      assert.equal(line.nextElementSibling?.textContent?.includes("second line"), true, "the whole text opens");
    },
  );
});
