import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { Conversation } from "./conversation";
import {
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  type EpicStep,
  EpicStepSchema,
} from "./gen/orrery/v1/epics_pb";
import { ConversationItemSchema, ConversationStateSchema, type ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { clock } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";
import { WorkerSessionPage } from "./workersession";

type Props = Parameters<typeof WorkerSessionPage>[0];
type StepInit = MessageInitShape<typeof EpicStepSchema>;

const resumesPlaceholder = "Nudge API endpoint. It resumes with your words";
const interruptsPlaceholder = "Nudge API endpoint. It interrupts the turn, then gives it your words";
const unknownNote = "nudge not known: it may have landed; nothing was resent";

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

function conversation(items: ConversationItem[]): Conversation {
  return {
    transcript: "t1",
    seq: 1n,
    items,
    oldest: 0n,
    more: false,
    state: create(ConversationStateSchema, {}),
    unfinished: [],
    queued: [],
    unsent: [],
  };
}

// step is the step of task api, running unless the init says otherwise.
function step(init: StepInit = {}): EpicStep {
  return create(EpicStepSchema, { id: "s1", task: "api", end: "", held: false, started: 100n, ...init });
}

// detailOf is the epic's Get answer that holds the step: the task api, titled, and its one requirement.
function detailOf(...steps: EpicStep[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic: { slug: "orrery-style", outcome: "accepted" },
    buildTasks: [{ id: "api", title: "API endpoint", status: "dispatched", stage: "implement", requirements: ["R1"] }],
    requirements: [{ id: "R1", text: "The endpoint answers" }],
    steps,
  });
}

// baseProps is the page's props with each handler recording its name, and a nudge its text, in calls.
function baseProps(calls: string[], init: Partial<Props>): Props {
  const first = step();
  return {
    conversation: null,
    now: 100_000,
    detail: detailOf(first),
    step: first,
    task: "api",
    phase: "implementing",
    onOpenEpic: () => calls.push("onOpenEpic"),
    onOpenTask: (task, phase) => calls.push(`onOpenTask ${task} ${phase}`),
    onBack: () => calls.push("onBack"),
    onOlder: () => calls.push("onOlder"),
    nudge: async (text) => {
      calls.push(`nudge ${text}`);
    },
    hold: async () => {
      calls.push("hold");
    },
    stop: async () => {
      calls.push("stop");
    },
    ...init,
  };
}

// withPage renders the page with the props the init gives over baseProps, and runs check over its container.
async function withPage(
  calls: string[],
  init: Partial<Props>,
  check: (container: HTMLElement) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<WorkerSessionPage {...baseProps(calls, init)} />, container);
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// withStep renders the page over a detail that holds the step, with the step as the page's step.
async function withStep(
  calls: string[],
  init: Partial<Props> & { step: EpicStep },
  check: (container: HTMLElement) => Promise<void>,
): Promise<void> {
  await withPage(calls, { detail: detailOf(init.step), ...init }, check);
}

function find<T extends Element = HTMLElement>(root: ParentNode, selector: string): T {
  const found = root.querySelector<T>(selector);
  if (found === null) {
    throw new Error(`the page has an element matching ${selector}`);
  }
  return found;
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function crumbTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".crumbs .crumb")].map((crumb) => crumb.textContent ?? "");
}

function crumbButton(container: HTMLElement, label: string): HTMLButtonElement {
  const found = [...container.querySelectorAll<HTMLButtonElement>("button.crumb")].find(
    (crumb) => crumb.textContent === label,
  );
  if (found === undefined) {
    throw new Error(`the crumbs have a ${label} button`);
  }
  return found;
}

// buttonNamed is the button whose text, or whose accessible name for an icon-only control, is label, or null when none is.
function buttonNamed(container: HTMLElement, label: string): HTMLButtonElement | null {
  return (
    [...container.querySelectorAll<HTMLButtonElement>("button")].find(
      (button) => button.textContent === label || button.getAttribute("aria-label") === label,
    ) ?? null
  );
}

function buttonOf(container: HTMLElement, label: string): HTMLButtonElement {
  const found = buttonNamed(container, label);
  if (found === null) {
    throw new Error(`the page has a ${label} button`);
  }
  return found;
}

function textarea(container: HTMLElement): HTMLTextAreaElement {
  return find<HTMLTextAreaElement>(container, "textarea");
}

async function type(box: HTMLTextAreaElement, text: string): Promise<void> {
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function press(box: HTMLTextAreaElement, key: string): Promise<void> {
  await act(() => {
    box.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

async function clickElement(element: HTMLElement): Promise<void> {
  await act(() => {
    element.click();
  });
}

// settle lets a pending call resolve and its state update render.
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

test("WorkerSession: the worker's conversation shows its assistant text in the reader", async () => {
  const items = [item({ kind: "reply", time: 100n, text: "reading the migration now" })];
  await withPage([], { conversation: conversation(items) }, async (container) => {
    assert.equal(textOf(container, ".reader .reply"), "reading the migration now");
  });
});

test("WorkerSession: the reader at its top asks for the older page while older items remain, and not once none remain", async () => {
  const items = [item({ kind: "reply", time: 100n, text: "reading the migration now" })];
  const asked: string[] = [];
  await withPage(asked, { conversation: { ...conversation(items), oldest: 9n, more: true } }, async () => {
    assert.deepEqual(asked, ["onOlder"]);
  });
  const none: string[] = [];
  await withPage(none, { conversation: conversation(items) }, async () => {
    assert.deepEqual(none, []);
  });
});

test("WorkerSession: a null conversation shows the reader's loading line", async () => {
  await withPage([], { conversation: null }, async (container) => {
    assert.equal(textOf(container, ".reader-loading"), "loading the conversation…");
  });
});

test("WorkerSession: the crumbs read the epic's slug, the task's title and Worker session, the last being the page itself", async () => {
  await withPage([], {}, async (container) => {
    assert.deepEqual(crumbTexts(container), ["orrery-style", "API endpoint", "Worker session"]);
    assert.equal(find(container, ".crumb.here").tagName, "SPAN");
    assert.equal(find(container, ".crumb.here").textContent, "Worker session");
  });
});

test("WorkerSession: the title row draws no commands", async () => {
  await withPage([], {}, async (container) => {
    assert.equal(find(container, ".title-row .acts").children.length, 0);
  });
});

test("WorkerSession: the epic's band draws under the title row and before the conversation", async () => {
  await withPage([], {}, async (container) => {
    const children = [...find(container, ".worker-session").children];
    const at = (selector: string) => children.findIndex((child) => child.matches(selector));
    assert.ok(at(".title-row") >= 0, "the title row is drawn");
    assert.equal(at(".band") > at(".title-row"), true, "the band is under the title row");
    assert.equal(at(".reader") > at(".band"), true, "the conversation is under the band");
    assert.equal(textOf(container, ".band .epic-pin .slug"), "orrery-style");
    assert.equal(textOf(container, ".band .tchip.cur .tt"), "API endpoint");
  });
});

test("WorkerSession: the crumbs open the epic, and the task's title opens its record at the session's phase", async () => {
  const calls: string[] = [];
  await withPage(calls, {}, async (container) => {
    await clickElement(crumbButton(container, "orrery-style"));
    await clickElement(crumbButton(container, "API endpoint"));
    assert.deepEqual(calls, ["onOpenEpic", "onOpenTask api implementing"]);
  });
});

test("WorkerSession: ‹ calls onBack", async () => {
  const calls: string[] = [];
  await withPage(calls, {}, async (container) => {
    assert.equal(textOf(container, ".back"), "‹");
    await clickElement(find(container, ".back"));
    assert.deepEqual(calls, ["onBack"]);
  });
});

test("WorkerSession: the brief draws as a folded line, and each check round draws as a CheckBlock of its checks", async () => {
  const items = [
    item({ kind: "prompt", sender: "brief", time: 100n, text: "Build the endpoint." }),
    item({ kind: "prompt", sender: "engine", round: 1, time: 110n, text: "lint failed\nsee the log" }),
  ];
  const checks = [
    { round: 1, name: "lint", ok: false },
    { round: 1, name: "test", ok: true },
  ];
  await withStep([], { step: step({ checks }), conversation: conversation(items) }, async (container) => {
    const brief = find(container, '[data-shape="line"].fold.brief');
    assert.equal(brief.nextElementSibling?.classList.contains("opened") ?? false, false, "the brief is folded");
    const block = find(container, '[data-shape="block"].checks');
    assert.equal(block.dataset.tone, "bad", "a failed check of the round makes the block bad");
    assert.equal(textOf(block, ".head .words"), "round 1 from the engine", "the block names its round");
    assert.deepEqual(
      [...block.querySelectorAll(".chip")].map((chip) => [chip.textContent, chip.classList.contains("bad")]),
      [
        ["lint", true],
        ["test", false],
      ],
    );
  });
});

test("WorkerSession: a running step's box names the task and sends one nudge per send", async () => {
  const calls: string[] = [];
  await withPage(calls, { step: step({ end: "", held: false }) }, async (container) => {
    assert.equal(container.querySelector(".dock-box") !== null, true);
    assert.equal(textarea(container).placeholder, interruptsPlaceholder);
    await type(textarea(container), "check the migration");
    await press(textarea(container), "Enter");
    await settle();
    assert.deepEqual(calls, ["nudge check the migration"]);
  });
});

test("WorkerSession: a held step's box is held and offers the nudge that resumes it", async () => {
  const calls: string[] = [];
  await withPage(calls, { step: step({ end: "", held: true, doing: "waiting on a schema" }) }, async (container) => {
    assert.equal(container.querySelector('.dock-box[data-state="held"]') !== null, true);
    assert.equal(textarea(container).placeholder, resumesPlaceholder);
    await type(textarea(container), "use the new schema");
    await press(textarea(container), "Enter");
    await settle();
    assert.deepEqual(calls, ["nudge use the new schema"]);
  });
});

test("WorkerSession: Interrupt holds a running step once, and a held step offers none", async () => {
  const running: string[] = [];
  await withPage(running, { step: step({ end: "" }) }, async (container) => {
    await clickElement(buttonOf(container, "Interrupt"));
    await settle();
    assert.deepEqual(running, ["hold"]);
  });
  await withPage([], { step: step({ end: "", held: true }) }, async (container) => {
    assert.equal(buttonNamed(container, "Interrupt"), null, "a held step has no Interrupt");
  });
});

test("WorkerSession: Stop sits behind More and asks first, then sends one Stop", async () => {
  const calls: string[] = [];
  await withPage(calls, { step: step({ end: "" }) }, async (container) => {
    assert.equal(buttonNamed(container, "Stop API endpoint"), null, "no Stop is drawn before More");
    await clickElement(buttonOf(container, "Stop this step…"));
    assert.equal(textOf(container, ".statebar .confirm > span"), "Stop API endpoint?");
    await clickElement(buttonOf(container, "Keep it running"));
    await settle();
    assert.deepEqual(calls, [], "Keep it running sends no Stop");
    await clickElement(buttonOf(container, "Stop this step…"));
    await clickElement(buttonOf(container, "Stop API endpoint"));
    await settle();
    assert.deepEqual(calls, ["stop"]);
  });
});

test("WorkerSession: an ended step shows no dock, and its end is the last block", async () => {
  const ended = step({
    end: "done",
    agent: "builder",
    started: 100n,
    ended: 150n,
    summary: "the endpoint answers\nits tests pass",
  });
  await withStep([], { step: ended, conversation: conversation([]) }, async (container) => {
    assert.equal(container.querySelector(".dock"), null, "no dock is drawn");
    assert.equal(container.querySelector("textarea"), null, "no compose box is drawn");
    const last = find(container, ".worker-session").lastElementChild;
    assert.equal(last?.classList.contains("step-end-row"), true, "the step's end is the last block");
    assert.equal(last?.querySelector(".t > .body > .step-card") !== null, true, "the end sits in the conversation's column");
    assert.equal(textOf(last ?? container, ".t > .gut"), clock(150n), "the end's time is in the gutter");
    assert.equal(textOf(last ?? container, '[data-shape="block"].stepend .head .label'), "STEP ENDED");
    assert.equal(textOf(last ?? container, '[data-shape="block"].stepend .head .words'), "builder");
    assert.equal(textOf(last ?? container, '[data-shape="block"].stepend .head .end .chip'), "done");
    assert.deepEqual(
      [...find(container, ".step-card").querySelectorAll(".step-summary")].map((line) => line.textContent),
      ["the endpoint answers", "its tests pass"],
    );
  });
});

test("WorkerSession: a nudge with no answer shows the may-have-landed note and is not sent again until its text changes", async () => {
  const calls: string[] = [];
  const nudge = async (text: string) => {
    calls.push(`nudge ${text}`);
    throw new Error("connection reset");
  };
  await withPage(calls, { step: step({ end: "" }), nudge }, async (container) => {
    await type(textarea(container), "check the schema");
    await press(textarea(container), "Enter");
    await settle();
    assert.equal(textOf(container, '.inote[role="alert"]'), unknownNote);
    await press(textarea(container), "Enter");
    await settle();
    assert.deepEqual(calls, ["nudge check the schema"], "the unknown nudge is not resent");
    await type(textarea(container), "check the schema again");
    await press(textarea(container), "Enter");
    await settle();
    assert.deepEqual(calls, ["nudge check the schema", "nudge check the schema again"]);
  });
});
