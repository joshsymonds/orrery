import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type DockHandle, Dock } from "./dock";
import type { DockFacts } from "./dockwords";
import { flashMillis } from "./flash";
import {
  ConversationItemSchema,
  PendingEventSchema,
  QueuedMessageSchema,
  WaitingMessageSchema,
  type PendingEvent,
  type QueuedMessage,
  type WaitingMessage,
} from "./gen/orrery/v1/orrery_pb";
import type { SentText } from "./sending";
import { clock } from "./reader";
import { mockTimers } from "./testclock";
import { assertNone, assertSame, installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof Dock>[0];

const serverText = "upstream said: secret-session-7";

// now is the instant every test's dock reads its state line at, in unix seconds.
const now = 100_000;

// dockFacts is a session's facts, named web and done, with every other fact at its zero value.
function dockFacts(init: Partial<DockFacts> = {}): DockFacts {
  return {
    kind: "session",
    name: "web",
    state: "done",
    interrupting: false,
    interruptingSince: 0,
    turnSince: 0,
    tool: null,
    changed: 0,
    reason: "",
    interruptedAt: 0,
    putOff: false,
    step: null,
    ...init,
  };
}

function baseProps(init: Partial<Props>): Props {
  return {
    facts: dockFacts(),
    now,
    send: () => Promise.resolve(),
    pull: null,
    newPromptId: () => "prompt-1",
    ...init,
  };
}

async function withDock(
  props: Partial<Props>,
  check: (box: HTMLTextAreaElement, container: HTMLElement) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<Dock {...baseProps(props)} />, container);
    });
    await check(textarea(container), container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

function textarea(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector("textarea");
  assert.ok(box, "the dock renders a textarea");
  return box;
}

async function type(box: HTMLTextAreaElement, text: string): Promise<void> {
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// press dispatches a keydown and reports whether the dock prevented it.
async function press(
  box: HTMLTextAreaElement,
  key: string,
  modifiers: { shiftKey?: boolean; altKey?: boolean; ctrlKey?: boolean; metaKey?: boolean } = {},
): Promise<boolean> {
  const event = new KeyboardEvent("keydown", { key, cancelable: true, ...modifiers });
  await act(() => {
    box.dispatchEvent(event);
  });
  return event.defaultPrevented;
}

// settle lets a pending send or pull resolve and its state update render.
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function errorLine(container: HTMLElement): Element | null {
  return container.querySelector(".inote.error");
}

// advance moves the mocked clock forward by ms and flushes the renders its timers cause.
async function advance(t: TestContext, ms: number): Promise<void> {
  await act(() => {
    t.mock.timers.tick(ms);
  });
}

function boxOf(container: HTMLElement): Element {
  const found = container.querySelector(".dock-box");
  assert.ok(found, "the dock renders its box");
  return found;
}

function flashing(container: HTMLElement): boolean {
  return boxOf(container).classList.contains("flash");
}

// toneOf is the state line's tone the box carries as data-state.
function toneOf(container: HTMLElement): string {
  return boxOf(container).getAttribute("data-state") ?? "";
}

function textOf(container: HTMLElement, selector: string): string {
  return container.querySelector(selector)?.textContent ?? "";
}

// controlNamed is the state line's control drawn with the label, or null when the line draws none with it.
function controlNamed(container: HTMLElement, label: string): HTMLButtonElement | null {
  const controls = container.querySelectorAll<HTMLButtonElement>("button.ctl");
  return Array.from(controls).find((control) => control.querySelector("span")?.textContent === label) ?? null;
}

// clickControl presses a control the line draws, and fails when the line draws none.
async function clickControl(button: HTMLButtonElement | null): Promise<void> {
  assert.ok(button, "the line draws the control");
  await act(() => {
    button.click();
  });
}

function sendButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>("button.go");
  assert.ok(button, "the dock renders a send button");
  return button;
}

async function clickSend(container: HTMLElement): Promise<void> {
  await act(() => {
    sendButton(container).click();
  });
}

function queuedMessage(text: string, canceled = false): WaitingMessage {
  return create(WaitingMessageSchema, { text, canceled });
}

// sessionMessage is a session's queued message under promptId, in the given state.
function sessionMessage(promptId: string, text: string, state = "queued", waits = ""): QueuedMessage {
  return create(QueuedMessageSchema, { promptId, text, state, waits });
}

// trayRow is one drawn row of the tray: its label ("" when it has none), its text, its note ("" when none), whether it
// is cancelled, and the aria-label of its control ("" when it has none).
interface TrayRow {
  label: string;
  text: string;
  note: string;
  cancelled: boolean;
  control: string;
}

// trayRows reads each drawn tray row, in the order drawn.
function trayRows(container: HTMLElement): TrayRow[] {
  return Array.from(container.querySelectorAll(".tray-row"), (row) => ({
    label: row.querySelector(".lbl")?.textContent ?? "",
    text: row.querySelector(".text")?.textContent ?? "",
    note: row.querySelector(".note")?.textContent ?? "",
    cancelled: row.classList.contains("cancelled"),
    control: row.querySelector("button")?.getAttribute("aria-label") ?? "",
  }));
}

// trayHead reads the tray's head: its label, its why and the names of its controls, or null when no tray is drawn.
function trayHead(container: HTMLElement): { label: string; why: string; controls: string[] } | null {
  const head = container.querySelector(".tray-head");
  if (head === null) {
    return null;
  }
  return {
    label: textOf(container, ".tray-head .lbl"),
    why: textOf(container, ".tray-head .why"),
    controls: Array.from(head.querySelectorAll("button"), (button) => controlName(button)),
  };
}

// controlName is a control's name: its aria-label, or its text when it has none.
function controlName(button: HTMLButtonElement): string {
  return button.getAttribute("aria-label") ?? button.textContent ?? "";
}

// trayButton is the control of a tray row that is named label, and fails when no row carries it.
function trayButton(container: HTMLElement, label: string): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>(`.tray-row button[aria-label="${label}"]`);
  assert.ok(button, `a tray row carries the control "${label}"`);
  return button;
}

// headButton is the tray head's control named label, and fails when the head draws none with it.
function headButton(container: HTMLElement, label: string): HTMLButtonElement {
  const button = Array.from(container.querySelectorAll<HTMLButtonElement>(".tray-head button")).find(
    (candidate) => controlName(candidate) === label,
  );
  assert.ok(button, `the tray head draws "${label}"`);
  return button;
}

// noteLine is the note under the box that is not an error, or null when none is drawn.
function noteLine(container: HTMLElement): Element | null {
  return container.querySelector(".inote:not(.error)");
}

// settleSends lets a send that is pending resolve or reject, and the state it sets render, inside act.
async function settleSends(): Promise<void> {
  await act(async () => {
    for (let turn = 0; turn < 5; turn += 1) {
      await Promise.resolve();
    }
  });
}

// withRerenderableDock mounts a dock as withDock does, and gives the check a rerender that draws the props
// it names over the mounted dock's first props.
async function withRerenderableDock(
  props: Partial<Props>,
  check: (
    box: HTMLTextAreaElement,
    container: HTMLElement,
    rerender: (next: Partial<Props>) => Promise<void>,
  ) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const rerender = (next: Partial<Props>) =>
    act(() => {
      render(<Dock {...baseProps({ ...props, ...next })} />, container);
    });
  try {
    await rerender({});
    await check(textarea(container), container, rerender);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

test("Enter sends the text once with a prompt id and clears the box on success", async () => {
  const calls: { text: string; promptId: string }[] = [];
  await withDock(
    {
      send: async (text, promptId) => {
        calls.push({ text, promptId });
      },
      newPromptId: () => "fresh-id",
    },
    async (box) => {
      await type(box, "hello claude");
      await press(box, "Enter");
      await settle();
      assert.deepEqual(calls, [{ text: "hello claude", promptId: "fresh-id" }]);
      assert.equal(box.value, "");
    },
  );
});

test("a blank box sends nothing on Enter", async () => {
  let calls = 0;
  await withDock({ send: async () => void calls++ }, async (box) => {
    await type(box, "   ");
    await press(box, "Enter");
    await settle();
    assert.equal(calls, 0);
    assert.equal(box.value, "   ");
  });
});

test("Shift+Enter in a box holding text puts a line break at the cursor and sends nothing", async () => {
  let calls = 0;
  await withDock({ send: async () => void calls++ }, async (box) => {
    await type(box, "helloworld");
    box.setSelectionRange(5, 5);
    await press(box, "Enter", { shiftKey: true });
    await settle();
    assert.equal(box.value, "hello\nworld");
    assert.equal(box.selectionStart, 6, "the cursor sits just after the break");
    assert.equal(box.selectionEnd, 6);
    assert.equal(calls, 0);
  });
});

test("Alt+Enter in a box holding text puts a line break at the cursor and sends nothing", async () => {
  let calls = 0;
  await withDock({ send: async () => void calls++ }, async (box) => {
    await type(box, "helloworld");
    box.setSelectionRange(5, 5);
    await press(box, "Enter", { altKey: true });
    await settle();
    assert.equal(box.value, "hello\nworld");
    assert.equal(box.selectionStart, 6, "the cursor sits just after the break");
    assert.equal(box.selectionEnd, 6);
    assert.equal(calls, 0);
  });
});

test("a line break typed with Shift+Enter, then Enter, sends one message with both lines", async () => {
  const calls: { text: string; promptId: string }[] = [];
  await withDock(
    {
      send: async (text, promptId) => {
        calls.push({ text, promptId });
      },
      newPromptId: () => "break-id",
    },
    async (box) => {
      await type(box, "line one");
      await press(box, "Enter", { shiftKey: true });
      await type(box, `${box.value}line two`);
      assert.equal(box.value, "line one\nline two");
      await press(box, "Enter");
      await settle();
      assert.deepEqual(calls, [{ text: "line one\nline two", promptId: "break-id" }]);
      assert.equal(box.value, "");
    },
  );
});

test("a refused send keeps the text, shows the fixed line, and is not retried by the dock", async () => {
  const promptIds: string[] = [];
  await withDock(
    {
      send: async (_text, promptId) => {
        promptIds.push(promptId);
        throw new ConnectError(serverText, Code.FailedPrecondition);
      },
      newPromptId: () => `id-${promptIds.length + 1}`,
    },
    async (box, container) => {
      await type(box, "keep me");
      await press(box, "Enter");
      await settle();
      await settle();
      assert.equal(box.value, "keep me");
      const line = errorLine(container);
      assert.ok(line, "the error line is shown");
      assert.equal(line.getAttribute("role"), "alert");
      assert.equal(line.textContent, "Not sent. Your draft is kept; send again.");
      assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
      assert.deepEqual(promptIds, ["id-1"], "no call happens without a new Enter");
    },
  );
});

test("Enter after a rejected send reuses its prompt id while the text is unchanged", async () => {
  const promptIds: string[] = [];
  let attempts = 0;
  let minted = 0;
  await withDock(
    {
      send: async (_text, promptId) => {
        promptIds.push(promptId);
        attempts++;
        if (attempts === 1) {
          throw new Error(serverText);
        }
      },
      newPromptId: () => `id-${++minted}`,
    },
    async (box) => {
      await type(box, "retry me");
      await press(box, "Enter");
      await settle();
      await press(box, "Enter");
      await settle();
      assert.deepEqual(promptIds, ["id-1", "id-1"]);
      assert.equal(minted, 1);
      assert.equal(box.value, "");
    },
  );
});

test("Enter after the text changed on a rejected send takes a fresh prompt id", async () => {
  const promptIds: string[] = [];
  let attempts = 0;
  let minted = 0;
  await withDock(
    {
      send: async (_text, promptId) => {
        promptIds.push(promptId);
        attempts++;
        if (attempts === 1) {
          throw new Error(serverText);
        }
      },
      newPromptId: () => `id-${++minted}`,
    },
    async (box) => {
      await type(box, "first draft");
      await press(box, "Enter");
      await settle();
      await type(box, "second draft");
      await press(box, "Enter");
      await settle();
      assert.deepEqual(promptIds, ["id-1", "id-2"]);
    },
  );
});

test("Enter during an in-flight send makes no second call", async () => {
  const calls: string[] = [];
  let finish: () => void = () => {};
  await withDock(
    {
      send: (text) => {
        calls.push(text);
        return new Promise<void>((resolve) => {
          finish = resolve;
        });
      },
    },
    async (box) => {
      await type(box, "only once");
      await press(box, "Enter");
      await press(box, "Enter");
      await press(box, "Enter");
      assert.deepEqual(calls, ["only once"]);
      finish();
      await settle();
      assert.equal(box.value, "");
    },
  );
});

test("Up in an empty box puts the pulled text in the box", async () => {
  let pulls = 0;
  await withDock(
    {
      pull: async () => {
        pulls++;
        return "queued text";
      },
    },
    async (box) => {
      await press(box, "ArrowUp");
      await settle();
      assert.equal(pulls, 1);
      assert.equal(box.value, "queued text");
    },
  );
});

test("Up with text in the box calls nothing", async () => {
  let pulls = 0;
  await withDock(
    {
      pull: async () => {
        pulls++;
        return "queued text";
      },
    },
    async (box) => {
      await type(box, "typed");
      await press(box, "ArrowUp");
      await settle();
      assert.equal(pulls, 0);
      assert.equal(box.value, "typed");
    },
  );
});

test("Up with no pull for the row calls nothing", async () => {
  await withDock({ pull: null }, async (box, container) => {
    await press(box, "ArrowUp");
    await settle();
    assert.equal(box.value, "");
    assert.equal(errorLine(container), null);
  });
});

test("a rejected pull shows the fixed line and leaves the box empty", async () => {
  await withDock(
    {
      pull: async () => {
        throw new Error(serverText);
      },
    },
    async (box, container) => {
      await press(box, "ArrowUp");
      await settle();
      assert.equal(box.value, "");
      const line = errorLine(container);
      assert.ok(line, "the error line is shown");
      assert.equal(line.getAttribute("role"), "alert");
      assert.equal(line.textContent, "Could not take them back.");
      assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
    },
  );
});

test("text typed while a send is in flight stays in the box when the send resolves", async () => {
  let resolveSend: () => void = () => {};
  const send = () =>
    new Promise<void>((resolve) => {
      resolveSend = resolve;
    });
  await withDock({ send }, async (box) => {
    await type(box, "first message");
    await press(box, "Enter");
    await type(box, "second draft");
    await act(() => {
      resolveSend();
    });
    await settle();
    assert.equal(box.value, "second draft");
  });
});

test("text typed while a pull is in flight follows the pulled text", async () => {
  let resolvePull: (text: string) => void = () => {};
  const pull = () =>
    new Promise<string>((resolve) => {
      resolvePull = resolve;
    });
  await withDock({ pull }, async (box) => {
    await press(box, "ArrowUp");
    await type(box, "typed meanwhile");
    await act(() => {
      resolvePull("queued text");
    });
    await settle();
    assert.equal(box.value, "queued text\ntyped meanwhile");
  });
});

test("Enter with text flashes the dock at once and clears 300 ms later", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withDock({}, async (box, container) => {
    await type(box, "hello claude");
    await press(box, "Enter");
    assert.equal(flashing(container), true, "flashing at once after a send starts");
    await advance(t, flashMillis - 1);
    assert.equal(flashing(container), true, "still flashing 1 ms before the window ends");
    await advance(t, 1);
    assert.equal(flashing(container), false, "cleared when the window ends");
  });
});

test("Enter on a blank box does not flash", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withDock({}, async (box, container) => {
    await press(box, "Enter");
    assert.equal(flashing(container), false, "an empty box does not flash");
    await type(box, "   ");
    await press(box, "Enter");
    assert.equal(flashing(container), false, "a blank box does not flash");
  });
});

test("Enter during an in-flight send does not flash", async (t) => {
  mockTimers(t, ["setTimeout"]);
  let finish: () => void = () => {};
  const send = () =>
    new Promise<void>((resolve) => {
      finish = resolve;
    });
  await withDock({ send }, async (box, container) => {
    await type(box, "only once");
    await press(box, "Enter");
    await advance(t, flashMillis);
    assert.equal(flashing(container), false, "the first flash has ended");
    await press(box, "Enter");
    assert.equal(flashing(container), false, "Enter while the send is in flight does not flash");
    await act(() => {
      finish();
    });
  });
});

test("a send that starts during a flash restarts the window", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withDock({}, async (box, container) => {
    await type(box, "first");
    await press(box, "Enter");
    await advance(t, 200);
    await type(box, "second");
    await press(box, "Enter");
    await advance(t, 200);
    assert.equal(flashing(container), true, "still flashing at 400 ms, inside the restarted window");
    await advance(t, 99);
    assert.equal(flashing(container), true, "still flashing 1 ms before the restarted window ends");
    await advance(t, 1);
    assert.equal(flashing(container), false, "cleared 300 ms after the second send");
  });
});

test("Up in an empty box with a pull flashes at once and clears 300 ms later", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withDock({ pull: async () => "queued text" }, async (box, container) => {
    await press(box, "ArrowUp");
    assert.equal(flashing(container), true, "flashing at once after a pull starts");
    await advance(t, flashMillis - 1);
    assert.equal(flashing(container), true, "still flashing 1 ms before the window ends");
    await advance(t, 1);
    assert.equal(flashing(container), false, "cleared when the window ends");
  });
});

test("Up with text in the box does not flash", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withDock({ pull: async () => "queued text" }, async (box, container) => {
    await type(box, "typed");
    await press(box, "ArrowUp");
    assert.equal(flashing(container), false, "a box with text does not flash on Up");
  });
});

test("ReaderPaging: Page Up and Page Down in an empty box call onPage with their direction and are prevented", async () => {
  const directions: string[] = [];
  await withDock(
    {
      onPage: (direction) => {
        directions.push(direction);
        return true;
      },
    },
    async (box) => {
      assert.equal(await press(box, "PageUp"), true, "Page Up is prevented");
      assert.equal(await press(box, "PageDown"), true, "Page Down is prevented");
      assert.deepEqual(directions, ["up", "down"]);
    },
  );
});

test("ReaderPaging: Page Up and Page Down with text in the box call nothing and are not prevented", async () => {
  let calls = 0;
  await withDock(
    {
      onPage: () => {
        calls++;
        return true;
      },
    },
    async (box) => {
      await type(box, "typed");
      assert.equal(await press(box, "PageUp"), false, "Page Up is not prevented");
      assert.equal(await press(box, "PageDown"), false, "Page Down is not prevented");
      assert.equal(calls, 0);
      assert.equal(box.value, "typed");
    },
  );
});

test("ReaderPaging: a Page key that onPage does not take, or with no onPage, is not prevented", async () => {
  const directions: string[] = [];
  await withDock(
    {
      onPage: (direction) => {
        directions.push(direction);
        return false;
      },
    },
    async (box) => {
      assert.equal(await press(box, "PageUp"), false, "a declined Page Up is not prevented");
      assert.equal(await press(box, "PageDown"), false, "a declined Page Down is not prevented");
      assert.deepEqual(directions, ["up", "down"]);
    },
  );
  await withDock({}, async (box) => {
    assert.equal(await press(box, "PageUp"), false, "Page Up with no onPage is not prevented");
    assert.equal(await press(box, "PageDown"), false, "Page Down with no onPage is not prevented");
  });
});

test("ReaderPaging: a Page key with a modifier held calls nothing and is not prevented", async () => {
  let calls = 0;
  await withDock(
    {
      onPage: () => {
        calls++;
        return true;
      },
    },
    async (box) => {
      for (const modifiers of [{ shiftKey: true }, { altKey: true }, { ctrlKey: true }, { metaKey: true }]) {
        const held = Object.keys(modifiers).join();
        assert.equal(await press(box, "PageUp", modifiers), false, `Page Up with ${held} is not prevented`);
        assert.equal(await press(box, "PageDown", modifiers), false, `Page Down with ${held} is not prevented`);
      }
      assert.equal(calls, 0);
    },
  );
});

test("/ in an empty box calls onFind, is prevented, and leaves the box empty", async () => {
  let finds = 0;
  await withDock(
    {
      onFind: () => {
        finds++;
      },
    },
    async (box) => {
      assert.equal(await press(box, "/"), true, "the slash is prevented");
      assert.equal(finds, 1);
      assert.equal(box.value, "");
    },
  );
});

test("/ with text in the box calls nothing and is not prevented", async () => {
  let finds = 0;
  await withDock(
    {
      onFind: () => {
        finds++;
      },
    },
    async (box) => {
      await type(box, "typed");
      assert.equal(await press(box, "/"), false, "the slash is not prevented");
      assert.equal(finds, 0);
      assert.equal(box.value, "typed");
    },
  );
});

test("/ with a modifier held calls nothing and is not prevented", async () => {
  let finds = 0;
  await withDock(
    {
      onFind: () => {
        finds++;
      },
    },
    async (box) => {
      for (const modifiers of [{ shiftKey: true }, { altKey: true }, { ctrlKey: true }, { metaKey: true }]) {
        const held = Object.keys(modifiers).join();
        assert.equal(await press(box, "/", modifiers), false, `/ with ${held} is not prevented`);
      }
      assert.equal(finds, 0);
    },
  );
});

// The state cases: working queues the text, waiting answers, and any other state, known or not, says message.
const stateCases = [
  { state: "working", tone: "working", placeholder: "Queue a message for api. Claude takes it at its next tool result" },
  { state: "waiting", tone: "waiting", placeholder: "Answer api" },
  { state: "idle", tone: "idle", placeholder: "Message api" },
  { state: "unknown-state", tone: "idle", placeholder: "Message api" },
];

for (const { state, tone, placeholder } of stateCases) {
  test(`a ${state} dock's box carries the ${tone} tone and the placeholder "${placeholder}"`, async () => {
    await withDock({ facts: dockFacts({ name: "api", state }) }, async (box, container) => {
      assert.equal(toneOf(container), tone, "the box carries the state line's tone");
      assert.equal(box.placeholder, placeholder);
    });
  });
}

test("DockState: working shows the tool's short name, its arg and the turn's elapsed time, and Interrupt calls onInterrupt", async () => {
  let interrupts = 0;
  const facts = dockFacts({
    state: "working",
    turnSince: now - 85,
    tool: { name: "mcp__orrery__step_start", arg: "deploy", start: now - 10 },
  });
  await withDock(
    {
      facts,
      onInterrupt: () => {
        interrupts += 1;
      },
    },
    async (_box, container) => {
      assert.equal(toneOf(container), "working");
      assert.equal(textOf(container, ".statebar .word"), "working");
      assert.equal(textOf(container, ".statebar .name"), "step_start");
      assert.equal(textOf(container, ".statebar .what"), "step_start deploy");
      assert.equal(textOf(container, ".statebar .clock"), "1:25");
      await clickControl(controlNamed(container, "Interrupt"));
      assert.equal(interrupts, 1, "Interrupt calls onInterrupt once");
    },
  );
});

test("DockState: working with no onInterrupt draws no Interrupt", async () => {
  await withDock({ facts: dockFacts({ state: "working" }) }, async (_box, container) => {
    assertNone(controlNamed(container, "Interrupt"), "no Interrupt is drawn without onInterrupt");
    assert.equal(textOf(container, ".statebar .what"), "thinking", "the arg is missing, so the line says thinking");
  });
});

test("DockState: interrupting while working says interrupting…, waits for the turn to end, and draws Interrupting… spent", async () => {
  const facts = dockFacts({ state: "working", interrupting: true, interruptingSince: now - 20, turnSince: now - 100 });
  await withDock({ facts, onInterrupt: () => {} }, async (box, container) => {
    assert.equal(toneOf(container), "interrupting");
    assert.equal(textOf(container, ".statebar .word"), "interrupting…");
    assert.equal(textOf(container, ".statebar .what"), "waiting for the turn to end");
    assert.equal(textOf(container, ".statebar .clock"), "0:20", "the clock counts from the interrupt");
    const spent = controlNamed(container, "Interrupting…");
    assert.ok(spent, "the line draws Interrupting…");
    assert.equal(spent.disabled, true, "Interrupting… is spent");
    assertNone(controlNamed(container, "Interrupt"), "no second Interrupt is drawn");
    assert.equal(box.placeholder, "Stopping web…");
  });
});

test("DockState: done after your interrupt says interrupted by you, with the time it was interrupted, and no control", async () => {
  const interruptedAt = now - 600;
  const facts = dockFacts({ state: "done", interruptedAt });
  await withDock({ facts, onInterrupt: () => {}, resume: async () => {} }, async (box, container) => {
    assert.equal(toneOf(container), "interrupted");
    assert.equal(textOf(container, ".statebar .word"), "interrupted by you");
    assert.equal(textOf(container, ".statebar .what"), `· ${clock(BigInt(interruptedAt))}`);
    assert.equal(container.querySelectorAll("button.ctl").length, 0, "the line draws no control");
    assert.equal(box.placeholder, "web stopped where it was. Say what to do next");
  });
});

test("DockState: cut off says cut off with its reason and age, offers Resume, and Resume calls resume", async () => {
  let resumes = 0;
  const facts = dockFacts({ state: "interrupted", reason: "tool timeout", changed: now - 180 });
  await withDock({ facts, resume: async () => void (resumes += 1) }, async (box, container) => {
    assert.equal(toneOf(container), "interrupted");
    assert.equal(textOf(container, ".statebar .word"), "cut off");
    assert.equal(textOf(container, ".statebar .what"), "· tool timeout · 3m ago");
    assert.equal(box.placeholder, "web was cut off. Resume it, or say what to do next");
    await clickControl(controlNamed(container, "Resume"));
    assert.equal(resumes, 1, "Resume calls resume once");
    assertNone(errorLine(container), "a resume that landed shows no line");
  });
});

test("DockState: a refused Resume shows its message under the box, and any other failure shows the unknown line", async () => {
  const refused = new ConnectError("session is not interrupted", Code.FailedPrecondition);
  await withDock(
    { facts: dockFacts({ state: "interrupted" }), resume: () => Promise.reject(refused) },
    async (_box, container) => {
      await clickControl(controlNamed(container, "Resume"));
      await settle();
      assert.equal(errorLine(container)?.textContent, "Not resumed: session is not interrupted");
      assert.equal(errorLine(container)?.getAttribute("role"), "alert");
    },
  );
  await withDock(
    { facts: dockFacts({ state: "interrupted" }), resume: () => Promise.reject(new Error(serverText)) },
    async (_box, container) => {
      await clickControl(controlNamed(container, "Resume"));
      await settle();
      assert.equal(
        errorLine(container)?.textContent,
        "No answer from the server. It may have resumed: check before trying again.",
      );
      assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
    },
  );
});

test("DockState: failed says failed in the failed tone, with its reason and age, and no control", async () => {
  const facts = dockFacts({ state: "failed", reason: "upstream returned 500", changed: now - 120 });
  await withDock({ facts, onInterrupt: () => {}, resume: async () => {} }, async (_box, container) => {
    assert.equal(toneOf(container), "failed");
    assert.equal(textOf(container, ".statebar .word"), "failed");
    assert.equal(textOf(container, ".statebar .what"), "· upstream returned 500 · 2m ago");
    assert.equal(container.querySelectorAll("button.ctl").length, 0, "the line draws no control");
  });
});

test("DockState: stopped says stopped with its age, and no control", async () => {
  const facts = dockFacts({ state: "stopped", changed: now - 7_200 });
  await withDock({ facts, onInterrupt: () => {}, resume: async () => {} }, async (_box, container) => {
    assert.equal(toneOf(container), "stopped");
    assert.equal(textOf(container, ".statebar .word"), "stopped");
    assert.equal(textOf(container, ".statebar .what"), "· 2h ago");
    assert.equal(container.querySelectorAll("button.ctl").length, 0, "the line draws no control");
  });
});

test("the send button is a button labeled send inside the dock", async () => {
  await withDock({}, async (_box, container) => {
    const button = sendButton(container);
    assert.equal(button.getAttribute("type"), "button");
    assert.equal(button.getAttribute("aria-label"), "send");
    assert.equal(button.textContent, "↑");
    assert.equal(container.querySelector(".dock button.go") !== null, true);
  });
});

test("a click on the send button sends the text once with a prompt id and clears the box", async () => {
  const calls: { text: string; promptId: string }[] = [];
  await withDock(
    {
      send: async (text, promptId) => {
        calls.push({ text, promptId });
      },
      newPromptId: () => "button-id",
    },
    async (box, container) => {
      await type(box, "hello from the button");
      await clickSend(container);
      await settle();
      assert.deepEqual(calls, [{ text: "hello from the button", promptId: "button-id" }]);
      assert.equal(box.value, "");
    },
  );
});

test("a second click on the send button while in flight makes no second call", async () => {
  const calls: string[] = [];
  let finish: () => void = () => {};
  await withDock(
    {
      send: (text) => {
        calls.push(text);
        return new Promise<void>((resolve) => {
          finish = resolve;
        });
      },
    },
    async (box, container) => {
      await type(box, "only once");
      await clickSend(container);
      await clickSend(container);
      assert.deepEqual(calls, ["only once"]);
      await act(() => {
        finish();
      });
      await settle();
      assert.equal(box.value, "");
    },
  );
});

test("a click on the send button with a blank box sends nothing", async () => {
  let calls = 0;
  await withDock({ send: async () => void calls++ }, async (box, container) => {
    await type(box, "   ");
    await clickSend(container);
    await settle();
    assert.equal(calls, 0);
    assert.equal(box.value, "   ");
  });
});

test("DockQueue: a pane's rows draw oldest first under one head that counts them, with the tray's why, and a canceled row says not delivered with no control", async () => {
  const queued = [queuedMessage("first"), queuedMessage("second", true), queuedMessage("third")];
  await withDock({ facts: dockFacts({ kind: "pane", name: "lamp", state: "working" }), queued }, async (_box, container) => {
    assert.deepEqual(trayRows(container), [
      { label: "", text: "first", note: "", cancelled: false, control: "" },
      { label: "not delivered", text: "second", note: "", cancelled: true, control: "" },
      { label: "", text: "third", note: "", cancelled: false, control: "" },
    ]);
    assert.deepEqual(trayHead(container), { label: "Queued 3", why: "Claude Code takes them when it chooses", controls: [] });
    assertSame(container.querySelector(".tray-head"), container.querySelector(".tray")?.firstElementChild, "the head opens the tray");
    assert.equal(container.querySelectorAll(".tray-row").length, 3, "the head is not drawn as a row");
  });
});

test("DockQueue: a pane whose rows are all canceled says the turn was cancelled", async () => {
  await withDock({ facts: dockFacts({ kind: "pane", name: "lamp", state: "working" }), queued: [queuedMessage("only", true)] }, async (_box, container) => {
    assert.equal(trayHead(container)?.why, "dropped when the turn was cancelled");
  });
});

test("DockQueue: no tray is drawn when no message is queued", async () => {
  await withDock({ queued: [] }, async (_box, container) => {
    assert.equal(container.querySelector(".tray"), null);
    assert.equal(container.querySelector(".tray-row"), null);
  });
});

test("DockQueue: a session's queued rows draw oldest first, each with a take back control, and say Claude takes them at its next tool result", async () => {
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "first"), sessionMessage("p2", "second")],
      withdraw: async () => [],
    },
    async (_box, container) => {
      assert.deepEqual(trayRows(container), [
        { label: "", text: "first", note: "", cancelled: false, control: "Take this back into the box" },
        { label: "", text: "second", note: "", cancelled: false, control: "Take this back into the box" },
      ]);
      assert.deepEqual(trayHead(container), { label: "Queued 2", why: "Claude takes them at its next tool result", controls: [] });
    },
  );
});

test("DockQueue: a session's why counts the rows that wait for a turn, and takes no canceled row into account", async () => {
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "one", "queued", "start")],
      withdraw: async () => [],
    },
    async (_box, container) => {
      assert.equal(trayHead(container)?.why, "Claude takes it as its next turn");
    },
  );
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "one", "queued", "start"), sessionMessage("p2", "two", "queued", "start")],
      withdraw: async () => [],
    },
    async (_box, container) => {
      assert.equal(trayHead(container)?.why, "Claude takes them as its next turns");
    },
  );
});

test("DockQueue: a session's canceled message draws nothing, and its discarded one draws as not delivered with put back", async () => {
  await withDock(
    {
      facts: dockFacts({ state: "done" }),
      sessionQueue: [sessionMessage("p1", "gone", "canceled"), sessionMessage("p2", "dropped", "discarded")],
    },
    async (_box, container) => {
      assert.deepEqual(trayRows(container), [
        { label: "not delivered", text: "dropped", note: "", cancelled: true, control: "Put back in the box" },
      ]);
      assert.deepEqual(trayHead(container), { label: "Queued 1", why: "dropped when the session ended", controls: [] });
    },
  );
});

test("DockQueue: a take back puts the withdrawn text after the draft, and the row draws no note", async () => {
  const taken: string[] = [];
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "queued words")],
      withdraw: async (promptId) => {
        taken.push(promptId);
        return ["withdrawn words"];
      },
    },
    async (box, container) => {
      await type(box, "draft");
      await clickControl(trayButton(container, "Take this back into the box"));
      await settle();
      assert.deepEqual(taken, ["p1"], "the take back names the message's prompt id");
      assert.equal(box.value, "draft\n\nwithdrawn words");
      assert.equal(trayRows(container)[0].note, "", "a take back that returned text draws no note");
    },
  );
});

test("DockQueue: a take back that returns no text says Claude already took it in, and takes its control away", async () => {
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "queued words")],
      withdraw: async () => [],
    },
    async (box, container) => {
      await clickControl(trayButton(container, "Take this back into the box"));
      await settle();
      assert.equal(trayRows(container)[0].note, "Claude already took it in.");
      assert.equal(trayRows(container)[0].control, "", "the take back control is gone");
      assert.equal(box.value, "");
    },
  );
});

test("DockQueue: a refused take back says Could not take it back. on its row, and puts no text in the box", async () => {
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "queued words")],
      withdraw: () => Promise.reject(new Error(serverText)),
    },
    async (box, container) => {
      await clickControl(trayButton(container, "Take this back into the box"));
      await settle();
      assert.equal(trayRows(container)[0].note, "Could not take it back.");
      assert.equal(box.value, "");
      assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
    },
  );
});

test("DockQueue: put back puts a discarded message's text in the box and hides its row", async () => {
  await withDock({ sessionQueue: [sessionMessage("p1", "dropped words", "discarded")] }, async (box, container) => {
    await clickControl(trayButton(container, "Put back in the box"));
    await settle();
    assert.equal(box.value, "dropped words");
    assert.equal(container.querySelector(".tray"), null, "the row is hidden once put back");
  });
});

test("DockQueue: a pane's Take all back pulls its text into the box", async () => {
  await withDock(
    {
      facts: dockFacts({ kind: "pane", name: "lamp", state: "working" }),
      queued: [queuedMessage("one")],
      pull: async () => "pulled words",
    },
    async (box, container) => {
      assert.deepEqual(trayHead(container)?.controls, ["Take all back"]);
      await clickControl(headButton(container, "Take all back"));
      await settle();
      assert.equal(box.value, "pulled words");
    },
  );
});

test("DockQueue: a refused Take all back says Could not take them back. as the error line", async () => {
  await withDock(
    {
      facts: dockFacts({ kind: "pane", name: "lamp", state: "working" }),
      queued: [queuedMessage("one")],
      pull: () => Promise.reject(new Error(serverText)),
    },
    async (box, container) => {
      await clickControl(headButton(container, "Take all back"));
      await settle();
      assert.equal(box.value, "");
      assert.equal(errorLine(container)?.textContent, "Could not take them back.");
      assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
    },
  );
});

test("DockQueue: Send now is drawn only for a working session with an interrupt", async () => {
  await withRerenderableDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "one")],
      withdraw: async () => [],
      interrupt: async () => [],
    },
    async (_box, container, rerender) => {
      assert.deepEqual(trayHead(container)?.controls, ["Send now"]);
      await rerender({ facts: dockFacts({ state: "done" }) });
      assert.deepEqual(trayHead(container)?.controls, [], "Send now is not drawn once the session is done");
    },
  );
});

test("DockQueue: Send now interrupts, then says each cancelled text again in order, each under a fresh prompt id", async () => {
  const events: string[] = [];
  let minted = 0;
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "first")],
      withdraw: async () => [],
      interrupt: async () => {
        events.push("interrupt");
        return ["one", "two"];
      },
      send: async (text, promptId) => {
        events.push(`say ${text} ${promptId}`);
      },
      newPromptId: () => `id-${++minted}`,
    },
    async (box, container) => {
      await clickControl(headButton(container, "Send now"));
      await settle();
      assert.deepEqual(events, ["interrupt", "say one id-1", "say two id-2"]);
      assert.equal(box.value, "", "nothing is held when every text is said");
      assert.equal(noteLine(container), null);
    },
  );
});

test("DockQueue: a second Send now while the first is in flight interrupts nothing more", async () => {
  let interrupts = 0;
  let release: (texts: string[]) => void = () => {};
  const said: string[] = [];
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "first")],
      withdraw: async () => [],
      interrupt: () => {
        interrupts += 1;
        return new Promise<string[]>((resolve) => {
          release = resolve;
        });
      },
      send: async (text) => {
        said.push(text);
      },
    },
    async (_box, container) => {
      await clickControl(headButton(container, "Send now"));
      await clickControl(headButton(container, "Send now"));
      assert.equal(interrupts, 1, "the second press interrupts nothing");
      await act(() => release(["one"]));
      await settle();
      assert.deepEqual(said, ["one"], "the text is said again once");
    },
  );
});

test("DockQueue: a refused re-say leaves its text and the later ones in the box, with the not-sent-again note", async () => {
  const said: string[] = [];
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      sessionQueue: [sessionMessage("p1", "first")],
      withdraw: async () => [],
      interrupt: async () => ["one", "two", "three"],
      send: async (text) => {
        said.push(text);
        if (text === "two") {
          throw new ConnectError("session is busy", Code.FailedPrecondition);
        }
      },
    },
    async (box, container) => {
      await type(box, "draft");
      await clickControl(headButton(container, "Send now"));
      await settle();
      assert.deepEqual(said, ["one", "two"], "the texts after the refused one are not said");
      assert.equal(box.value, "draft\n\ntwo\n\nthree", "the refused text and the later ones are back in the box after the draft");
      assert.equal(noteLine(container)?.textContent, "2 messages were not sent again. They are back in the box.");
      assert.equal(errorLine(container), null, "the note is not an error");
    },
  );
});

test("DockQueue: Escape's texts go into the box after the draft, in order, with the back-in-the-box note", async () => {
  const handles: DockHandle[] = [];
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      register: (handle) => {
        if (handle !== null) {
          handles.push(handle);
        }
      },
    },
    async (box, container) => {
      await type(box, "draft");
      const [handle] = handles;
      await act(() => {
        handle.append(["one", "two"]);
      });
      assert.equal(box.value, "draft\n\none\n\ntwo");
      assert.equal(noteLine(container)?.textContent, "Your 2 queued messages are back in the box.");
    },
  );
});

test("Up in an empty box still pulls the text back with messages queued", async () => {
  let pulls = 0;
  await withDock(
    {
      queued: [queuedMessage("waiting one")],
      pull: async () => {
        pulls++;
        return "pulled text";
      },
    },
    async (box) => {
      await press(box, "ArrowUp");
      await settle();
      assert.equal(pulls, 1);
      assert.equal(box.value, "pulled text");
    },
  );
});

test("queued text draws as text, never as markup", async () => {
  const markup = '<img src="x" onerror="alert(1)"> <b>bold</b> `code`';
  await withDock({ queued: [queuedMessage(markup)] }, async (_box, container) => {
    assert.equal(trayRows(container)[0].text, markup);
    assert.equal(container.querySelector(".tray-row img") !== null, false, "no image element is drawn");
    assert.equal(container.querySelector(".tray-row b") !== null, false, "no bold element is drawn");
  });
});

test("a send calls onSending with the text and prompt id at once, while the send is still pending", async () => {
  const sent: SentText[] = [];
  let resolveSend: () => void = () => {};
  const send = () =>
    new Promise<void>((resolve) => {
      resolveSend = resolve;
    });
  await withDock(
    {
      send,
      onSending: (entry) => {
        sent.push(entry);
      },
      newPromptId: () => "sent-1",
    },
    async (box) => {
      await type(box, "hello claude");
      await press(box, "Enter");
      assert.deepEqual(sent, [{ text: "hello claude", promptId: "sent-1" }]);
      resolveSend();
      await settleSends();
      assert.deepEqual(sent, [{ text: "hello claude", promptId: "sent-1" }], "the send is reported once");
    },
  );
});

test("a send whose outcome is unknown calls onUnsent, leaves no sending or queued entry, keeps the draft, and shows the unknown note", async () => {
  const unsent: string[] = [];
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      send: async () => {
        throw new Error(serverText);
      },
      onUnsent: (promptId) => {
        unsent.push(promptId);
      },
      newPromptId: () => "failed-1",
    },
    async (box, container) => {
      await type(box, "hello claude");
      await press(box, "Enter");
      await settleSends();
      assert.deepEqual(unsent, ["failed-1"]);
      assert.deepEqual(trayRows(container), [], "the failed message leaves the queue");
      assert.equal(box.value, "hello claude", "the draft is kept");
      assert.equal(noteLine(container)?.textContent, "No answer from the server. It may have been sent: check the conversation before sending again.");
      assert.equal(errorLine(container), null, "the unknown line is a note, not an error");
      assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
    },
  );
});

for (const { kind, pull } of [
  { kind: "session", pull: null },
  { kind: "pane", pull: async () => "" },
]) {
  test(`a send to a working ${kind} draws above the box as queued, with no note, and leaves when the turn ends`, async () => {
    await withRerenderableDock(
      { facts: dockFacts({ state: "working" }), pull, send: async () => {} },
      async (box, container, rerender) => {
        await type(box, "hello");
        await press(box, "Enter");
        await settleSends();
        assert.deepEqual(trayRows(container), [{ label: "", text: "hello", note: "", cancelled: false, control: "" }]);
        const root = container.querySelector(".dock");
        assert.ok(root, "the dock renders its root");
        assert.ok(container.querySelector(".dock-box .tray"), "the tray sits in the box");
        assert.equal(toneOf(container), "working");
        await rerender({ facts: dockFacts({ state: "idle" }) });
        assert.deepEqual(trayRows(container), [], "the message leaves when the turn ends");
        assertNone(container.querySelector(".tray"));
      },
    );
  });
}

test("a pane's queued message with the text a send drew above the box is drawn once", async () => {
  await withRerenderableDock(
    { facts: dockFacts({ state: "working" }), pull: async () => "", send: async () => {} },
    async (box, container, rerender) => {
      await type(box, "hello");
      await press(box, "Enter");
      await settleSends();
      await rerender({ queued: [queuedMessage("hello")] });
      assert.deepEqual(trayRows(container), [{ label: "", text: "hello", note: "", cancelled: false, control: "" }]);
      await rerender({ queued: [queuedMessage("hello", true)] });
      assert.deepEqual(
        trayRows(container).map((row) => [row.label, row.text]),
        [
          ["not delivered", "hello"],
          ["", "hello"],
        ],
        "a canceled message with the same text does not stand for the sent one",
      );
    },
  );
});

test("a held message draws one queued row, and the local entry the held set carries under its prompt id is dropped", async () => {
  await withRerenderableDock(
    { facts: dockFacts({ state: "working" }), send: async () => {}, newPromptId: () => "p1" },
    async (box, container, rerender) => {
      await type(box, "hello");
      await press(box, "Enter");
      await settleSends();
      assert.equal(trayRows(container).length, 1, "the sent message is queued");
      await rerender({ sessionQueue: [sessionMessage("p1", "hello")] });
      assert.deepEqual(trayRows(container), [{ label: "", text: "hello", note: "", cancelled: false, control: "" }]);
    },
  );
});

// promptItem is a conversation's prompt item with text, under promptId when one is given, as a pane's carries none.
function promptItem(text: string, promptId = "") {
  return create(ConversationItemSchema, { kind: "prompt", text, promptId });
}

test("a message sent to a working session leaves the queue once the conversation's items hold its prompt, while it stays working", async () => {
  await withRerenderableDock(
    { facts: dockFacts({ state: "working" }), send: async () => {}, newPromptId: () => "p1", items: [promptItem("earlier", "p0")] },
    async (box, container, rerender) => {
      await type(box, "hello");
      await press(box, "Enter");
      await settleSends();
      assert.equal(trayRows(container).length, 1, "the sent message is queued");
      await rerender({ items: [promptItem("earlier", "p0"), promptItem("hello", "p9")] });
      assert.equal(trayRows(container).length, 1, "a prompt with the same text under another prompt id is not this one");
      await rerender({ items: [promptItem("earlier", "p0"), promptItem("hello", "p9"), promptItem("hello", "p1")] });
      assert.deepEqual(trayRows(container), [], "the prompt with its prompt id ends it while the state stays working");
    },
  );
});

test("a message sent to a working session leaves the queue once it leaves the held set it was in", async () => {
  await withRerenderableDock(
    { facts: dockFacts({ state: "working" }), send: async () => {}, newPromptId: () => "p1" },
    async (box, container, rerender) => {
      await type(box, "hello");
      await press(box, "Enter");
      await settleSends();
      await rerender({ sessionQueue: [sessionMessage("p1", "hello")] });
      assert.equal(trayRows(container).length, 1, "the held message draws one row");
      await rerender({ sessionQueue: [] });
      assert.deepEqual(trayRows(container), [], "a message that left the held set is not queued");
    },
  );
});

test("a message sent to a working pane leaves the queue once a prompt with its text comes after the items it was sent after", async () => {
  const before = [promptItem("hello")];
  await withRerenderableDock(
    { facts: dockFacts({ state: "working" }), pull: async () => "", send: async () => {}, items: before },
    async (box, container, rerender) => {
      await type(box, "hello");
      await press(box, "Enter");
      await settleSends();
      assert.equal(trayRows(container).length, 1, "a prompt with its text before the send is not this one");
      await rerender({ items: [...before, promptItem("hello")] });
      assert.deepEqual(trayRows(container), [], "the prompt after the send ends it while the state stays working");
    },
  );
});

test("the page's handle appends returned texts after the box's text, joined by blank lines, and forgets the local entries they match", async () => {
  const handles: DockHandle[] = [];
  await withDock(
    {
      facts: dockFacts({ state: "working" }),
      send: async () => {},
      newPromptId: () => "p1",
      register: (handle) => {
        if (handle !== null) handles.push(handle);
      },
    },
    async (box, container) => {
      await type(box, "hello");
      await press(box, "Enter");
      await settleSends();
      await type(box, "draft");
      const [handle] = handles;
      await act(() => {
        handle.append(["one", "two"]);
        handle.forget(["hello"]);
      });
      assert.equal(box.value, "draft\n\none\n\ntwo");
      assert.deepEqual(trayRows(container), [], "the forgotten message leaves the queue");
    },
  );
});

// pendingEvent is a pending event of a session that waits as given.
function pendingEvent(waits: string): PendingEvent {
  return create(PendingEventSchema, { id: `event-${waits}`, kind: "step_end", waits });
}

// waitsText is the waits line's text, or null when no line is drawn.
function waitsText(container: HTMLElement): string | null {
  return container.querySelector(".dock-waits")?.textContent ?? null;
}

test("DockWaits: the line counts the events that wait until you speak, else the events that wait on a question you put off", async () => {
  await withRerenderableDock({ pending: [pendingEvent("speak"), pendingEvent("speak")] }, async (_box, container, rerender) => {
    assert.equal(waitsText(container), "2 events wait until you speak");
    await rerender({ pending: [pendingEvent("request")] });
    assert.equal(waitsText(container), null, "a request waits on nothing while no question is put off");
    await rerender({ facts: dockFacts({ putOff: true }), pending: [pendingEvent("request")] });
    assert.equal(waitsText(container), "1 event waits on the question you put off");
    await rerender({ facts: dockFacts({ putOff: true }), pending: [pendingEvent("free")] });
    assert.equal(waitsText(container), null, "a free event waits on nothing");
  });
});

test("DockWaits: the line is gone once a send resolves, and comes back when the pending events change", async () => {
  await withRerenderableDock({ pending: [pendingEvent("speak")], send: async () => {} }, async (box, container, rerender) => {
    assert.equal(waitsText(container), "1 event waits until you speak");
    await type(box, "reply");
    await press(box, "Enter");
    await settleSends();
    assert.equal(waitsText(container), null, "the line is gone after the send");
    await rerender({ pending: [pendingEvent("speak"), pendingEvent("speak")] });
    assert.equal(waitsText(container), "2 events wait until you speak", "a change to the pending events draws the line again");
  });
});

test("DockWaits: a later state with the same pending events keeps the line gone after a send", async () => {
  await withRerenderableDock({ pending: [pendingEvent("speak")], send: async () => {} }, async (box, container, rerender) => {
    await type(box, "reply");
    await press(box, "Enter");
    await settleSends();
    assert.equal(waitsText(container), null, "the line is gone after the send");
    await rerender({ pending: [pendingEvent("speak")] });
    assert.equal(waitsText(container), null, "the same events, newly delivered, do not draw it again");
  });
});

test("DockSend: a refused send keeps the draft and shows Not sent as the error line", async () => {
  await withDock({ send: () => Promise.reject(new ConnectError("session is busy", Code.FailedPrecondition)) }, async (box, container) => {
    await type(box, "keep me");
    await press(box, "Enter");
    await settleSends();
    assert.equal(box.value, "keep me");
    assert.equal(errorLine(container)?.textContent, "Not sent. Your draft is kept; send again.");
    assert.equal(noteLine(container), null, "the refusal is the only line");
  });
});

test("DockSend: an unknown send keeps the draft and shows the unknown line as a note, not an error", async () => {
  await withDock({ send: () => Promise.reject(new Error(serverText)) }, async (box, container) => {
    await type(box, "keep me");
    await press(box, "Enter");
    await settleSends();
    assert.equal(box.value, "keep me");
    assert.equal(
      noteLine(container)?.textContent,
      "No answer from the server. It may have been sent: check the conversation before sending again.",
    );
    assert.equal(errorLine(container), null, "the unknown line is not an error");
    assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
  });
});

test("DockSend: a shell's box says Type a command… and sends what is typed", async () => {
  const calls: string[] = [];
  await withDock(
    {
      facts: dockFacts({ kind: "shell", name: "tidepool", state: "idle" }),
      send: async (text) => {
        calls.push(text);
      },
    },
    async (box) => {
      assert.equal(box.placeholder, "Type a command…");
      await type(box, "ls -la");
      await press(box, "Enter");
      await settle();
      assert.deepEqual(calls, ["ls -la"]);
    },
  );
});

test("DockSend: the about line says what the message is about, and Not about this clears it and calls onClearAbout", async () => {
  let cleared = 0;
  await withRerenderableDock(
    {
      about: "the deploy check",
      onClearAbout: () => {
        cleared += 1;
      },
    },
    async (_box, container, rerender) => {
      assert.equal(textOf(container, ".quote .text"), "about the deploy check");
      await clickControl(container.querySelector<HTMLButtonElement>('.quote button[aria-label="Not about this"]'));
      assert.equal(container.querySelector(".quote"), null, "the about line is cleared");
      assert.equal(cleared, 1, "Not about this calls onClearAbout once");
      await rerender({ about: "the next check" });
      assert.equal(textOf(container, ".quote .text"), "about the next check", "a new about draws the line again");
    },
  );
});

test("DockSend: the box carries focused while its textarea has focus", async () => {
  await withDock({}, async (box, container) => {
    assert.equal(boxOf(container).classList.contains("focused"), false, "the box is not focused at first");
    await act(() => {
      box.dispatchEvent(new Event("focus"));
    });
    assert.equal(boxOf(container).classList.contains("focused"), true, "the box carries focused");
    await act(() => {
      box.dispatchEvent(new Event("blur"));
    });
    assert.equal(boxOf(container).classList.contains("focused"), false, "the box drops focused on blur");
  });
});
