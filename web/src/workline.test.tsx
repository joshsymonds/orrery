import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import type { JSX } from "preact";

import { PaneSchema, type Pane } from "./gen/orrery/v1/orrery_pb";
import { installTestDom, removeTestDom } from "./testdom";
import { Pill, rowPill, stepPill, WorkLine, WorkNote } from "./workline";

const now = 1_000_000;

function pane(init: MessageInitShape<typeof PaneSchema>): Pane {
  return create(PaneSchema, init);
}

// workingTool is a working pane's tool, as the server sends it: the JSON of id, name, input and start.
function workingTool(name: string, start: number): Pane {
  return pane({ state: "working", tool: JSON.stringify({ id: "t1", name, input: "{}", start }), changed: BigInt(start) });
}

async function withRender(node: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(node, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function withLine(
  props: { pane: Pane | undefined; now: number },
  check: (container: HTMLElement) => void,
): Promise<void> {
  return withRender(<WorkLine pane={props.pane} now={props.now} />, check);
}

function textOf(container: HTMLElement, selector: string): string {
  return container.querySelector(selector)?.textContent ?? "";
}

const table: [string, string][] = [
  ["Bash", "running a command"],
  ["Read", "reading a file"],
  ["Write", "writing a file"],
  ["Edit", "editing a file"],
  ["MultiEdit", "editing a file"],
  ["NotebookEdit", "editing a file"],
  ["Grep", "searching code"],
  ["Glob", "finding files"],
  ["WebFetch", "reading a web page"],
  ["WebSearch", "searching the web"],
  ["Agent", "running an agent"],
  ["Task", "running an agent"],
  ["TodoWrite", "updating its plan"],
  ["Skill", "loading a skill"],
  ["ToolSearch", "loading tools"],
  ["AskUserQuestion", "asking you"],
];

for (const [name, words] of table) {
  test(`the tool ${name} reads "${words}" labelled working`, () =>
    withLine({ pane: workingTool(name, now), now }, (container) => {
      assert.equal(textOf(container, ".work-label"), "working");
      assert.equal(textOf(container, ".work-tool"), words);
      assert.equal(container.querySelector(".work-line")?.classList.contains("background"), false);
    }));
}

for (const name of ["step_wait", "mcp__orrery__step_wait", "Monitor"]) {
  test(`the background wait ${name} reads as waiting on background work, labelled background`, () =>
    withLine({ pane: workingTool(name, now), now }, (container) => {
      assert.equal(textOf(container, ".work-label"), "background");
      assert.equal(textOf(container, ".work-tool"), "waiting on background work");
      assert.equal(container.querySelector(".work-line")?.classList.contains("background"), true);
    }));
}

test("an unknown tool reads its name, labelled working and not background", () =>
  withLine({ pane: workingTool("Frobnicate", now), now }, (container) => {
    assert.equal(textOf(container, ".work-label"), "working");
    assert.equal(textOf(container, ".work-tool"), "Frobnicate");
    assert.equal(container.querySelector(".work-line")?.classList.contains("background"), false);
  }));

test("an unknown MCP tool reads its short name after the last double underscore", () =>
  withLine({ pane: workingTool("mcp__orrery__frobnicate", now), now }, (container) => {
    assert.equal(textOf(container, ".work-label"), "working");
    assert.equal(textOf(container, ".work-tool"), "frobnicate");
  }));

test("elapsed time runs from the tool's start, not from the pane's last change", () =>
  withLine(
    {
      pane: pane({
        state: "working",
        tool: JSON.stringify({ id: "t1", name: "Bash", input: "{}", start: now - 72 }),
        changed: BigInt(now - 900),
      }),
      now,
    },
    (container) => assert.equal(textOf(container, ".work-time"), "1:12"),
  ));

test("elapsed time with no tool runs from the pane's last change", () =>
  withLine({ pane: pane({ state: "working", changed: BigInt(now - 5) }), now }, (container) =>
    assert.equal(textOf(container, ".work-time"), "0:05"),
  ));

test("elapsed time reads h:mm:ss from an hour", () =>
  withLine({ pane: workingTool("Bash", now - 3_725), now }, (container) =>
    assert.equal(textOf(container, ".work-time"), "1:02:05"),
  ));

test("elapsed time never reads negative", () =>
  withLine({ pane: workingTool("Bash", now + 30), now }, (container) =>
    assert.equal(textOf(container, ".work-time"), "0:00"),
  ));

test("the note names where a message lands, with no last turn when none has ended", () =>
  withLine({ pane: workingTool("Bash", now), now }, (container) =>
    assert.equal(textOf(container, ".work-note"), "a message sent now lands at Claude's next step"),
  ));

for (const [seconds, age] of [
  [45, "45s"],
  [300, "5m"],
  [7_200, "2h"],
  [259_200, "3d"],
] as const) {
  test(`the note says the last turn ended ${age} ago when it ended ${seconds} seconds back`, () =>
    withLine(
      { pane: pane({ state: "working", tool: "", changed: BigInt(now), lastTurnEnd: BigInt(now - seconds) }), now },
      (container) =>
        assert.equal(
          textOf(container, ".work-note"),
          `a message sent now lands at Claude's next step · last turn ended ${age} ago`,
        ),
    ));
}

for (const [label, paneValue] of [
  ["a done pane", pane({ state: "done", changed: BigInt(now) })],
  ["a waiting pane", pane({ state: "waiting", changed: BigInt(now) })],
  ["no pane", undefined],
] as const) {
  test(`nothing draws for ${label}`, () =>
    withLine({ pane: paneValue, now }, (container) => {
      assert.equal(container.childNodes.length, 0);
    }));
}

test("a tool name holding markup draws as text with no element", () =>
  withLine({ pane: workingTool('<img src=x onerror="alert(1)">', now), now }, (container) => {
    assert.equal(container.querySelector("img"), null);
    assert.equal(textOf(container, ".work-tool"), '<img src=x onerror="alert(1)">');
  }));

test("a working pane's pill reads working, Bash and 1:00", () =>
  withRender(<Pill model={rowPill("working", workingTool("Bash", now - 60))} now={now} />, (container) => {
    assert.equal(textOf(container, ".word"), "working");
    assert.equal(textOf(container, ".tool"), "Bash");
    assert.equal(textOf(container, ".clock"), "1:00");
  }));

test("a working pane's pill reads the short name of an MCP tool", () => {
  const model = rowPill("working", workingTool("mcp__x__Bash", now));
  assert.equal(model.tool, "Bash");
  assert.equal(model.since, now);
});

test("a working pane with no tool reads its clock from the pane's last change", () => {
  const model = rowPill("working", pane({ state: "working", changed: BigInt(now - 5) }));
  assert.equal(model.tool, "");
  assert.equal(model.since, now - 5);
});

test("a session's working state has no tool and no clock", () => {
  const model = rowPill("working");
  assert.equal(model.tone, "working");
  assert.equal(model.word, "working");
  assert.equal(model.tool, "");
  assert.equal(model.since === undefined, true);
});

test("a pane given takes its own state over the state argument", () => {
  const model = rowPill("working", pane({ state: "done", changed: BigInt(now) }));
  assert.equal(model.tone, "done");
  assert.equal(model.word, "done");
  assert.equal(model.tool, "");
  assert.equal(model.since === undefined, true);
});

test("a pane given with an empty state reads idle", () => {
  const model = rowPill("done", pane({ state: "", changed: BigInt(now) }));
  assert.equal(model.tone, "idle");
  assert.equal(model.word, "idle");
});

const sessionStates: [string, string, string][] = [
  ["waiting", "waiting", "waiting"],
  ["done", "done", "done"],
  ["interrupted", "interrupted", "interrupted"],
  ["failed", "ended", "failed"],
  ["stopped", "ended", "stopped"],
  ["paused", "idle", "paused"],
  ["", "idle", "idle"],
];

for (const [state, tone, word] of sessionStates) {
  test(`the session state "${state}" reads tone ${tone} and word ${word} with no tool or clock`, () => {
    const model = rowPill(state);
    assert.equal(model.tone, tone);
    assert.equal(model.word, word);
    assert.equal(model.tool, "");
    assert.equal(model.since === undefined, true);
  });
}

test("a held step is held with no clock", () => {
  const model = stepPill(true, now - 30);
  assert.equal(model.tone, "held");
  assert.equal(model.word, "held");
  assert.equal(model.tool, "");
  assert.equal(model.since === undefined, true);
});

test("a running step reads running with its clock from its start", () => {
  const model = stepPill(false, now - 60);
  assert.equal(model.tone, "working");
  assert.equal(model.word, "running");
  assert.equal(model.since, now - 60);
});

test("a running step's pill draws its clock as 1:00 with the spinner", () =>
  withRender(<Pill model={stepPill(false, now - 60)} now={now} />, (container) => {
    assert.equal(textOf(container, ".word"), "running");
    assert.equal(textOf(container, ".clock"), "1:00");
    assert.equal(container.querySelector(".g-working")?.getAttribute("aria-hidden"), "true");
    assert.equal(container.querySelector(".pill")?.classList.contains("tone-working"), true);
  }));

test("a held step's pill draws no clock and no tool", () =>
  withRender(<Pill model={stepPill(true, now - 60)} now={now} />, (container) => {
    assert.equal(textOf(container, ".word"), "held");
    assert.equal(container.querySelector(".clock"), null);
    assert.equal(container.querySelector(".tool"), null);
    assert.equal(container.querySelector(".g-working"), null);
    assert.equal(container.querySelector(".pill")?.classList.contains("tone-held"), true);
  }));

test("a waiting pill draws its tone and word with no spinner", () =>
  withRender(<Pill model={rowPill("waiting")} now={now} />, (container) => {
    assert.equal(container.querySelector(".pill")?.classList.contains("tone-waiting"), true);
    assert.equal(textOf(container, ".word"), "waiting");
    assert.equal(container.querySelector(".g-working"), null);
  }));

test("a tool holding markup draws as text in the pill with no element", () =>
  withRender(
    <Pill model={rowPill("working", workingTool('<img src=x onerror="alert(1)">', now))} now={now} />,
    (container) => {
      assert.equal(container.querySelector("img"), null);
      assert.equal(textOf(container, ".tool"), '<img src=x onerror="alert(1)">');
    },
  ));

test("a working note draws the note for a working pane", () =>
  withRender(<WorkNote pane={workingTool("Bash", now)} now={now} />, (container) =>
    assert.equal(
      textOf(container, "p.how.work-note"),
      "a message sent now lands at Claude's next step",
    ),
  ));

test("a working note names when the last turn ended", () =>
  withRender(
    <WorkNote pane={pane({ state: "working", changed: BigInt(now), lastTurnEnd: BigInt(now - 300) })} now={now} />,
    (container) =>
      assert.equal(
        textOf(container, "p.how.work-note"),
        "a message sent now lands at Claude's next step · last turn ended 5m ago",
      ),
  ));

for (const [label, paneValue] of [
  ["a done pane", pane({ state: "done", changed: BigInt(now) })],
  ["a waiting pane", pane({ state: "waiting", changed: BigInt(now) })],
  ["no pane", undefined],
] as const) {
  test(`the working note draws nothing for ${label}`, () =>
    withRender(<WorkNote pane={paneValue} now={now} />, (container) => {
      assert.equal(container.childNodes.length, 0);
    }));
}
