import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { Conversation } from "./conversation";
import { EpicStepSchema, type EpicStep } from "./gen/orrery/v1/epics_pb";
import { ConversationItemSchema, ConversationStateSchema, type ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { installTestDom, removeTestDom } from "./testdom";
import { WorkerSessionPage } from "./workersession";

type Props = Parameters<typeof WorkerSessionPage>[0];

const resumesPlaceholder = "Nudge api: it resumes the step with your text";
const interruptsPlaceholder = "Nudge api: it interrupts the turn, then prompts";

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
    held: [],
  };
}

function step(init: MessageInitShape<typeof EpicStepSchema> = {}): EpicStep {
  return create(EpicStepSchema, { id: "s1", task: "api", end: "", held: false, ...init });
}

// baseProps is the page's props with each handler recording its name, and a nudge its text, in calls.
function baseProps(calls: string[], init: Partial<Props>): Props {
  return {
    conversation: null,
    step: step(),
    epic: "orrery-style",
    task: "api",
    phase: "implementing",
    onGoList: () => calls.push("onGoList"),
    onGoEpic: () => calls.push("onGoEpic"),
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

test("the worker's conversation shows its assistant text in the reader", async () => {
  const items = [item({ kind: "reply", time: 100n, text: "reading the migration now" })];
  await withPage([], { conversation: conversation(items) }, async (container) => {
    assert.equal(textOf(container, ".reader .reply"), "reading the migration now");
  });
});

test("the reader at its top asks for the older page while older items remain, and not once none remain", async () => {
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

test("a null conversation shows the reader's loading line", async () => {
  await withPage([], { conversation: null }, async (container) => {
    assert.equal(textOf(container, ".reader-loading"), "loading…");
  });
});

test("the crumbs read orrery, the epic, task · phase, and session, and the last is the page itself", async () => {
  await withPage([], {}, async (container) => {
    assert.deepEqual(crumbTexts(container), ["orrery", "orrery-style", "api · implementing", "session"]);
    assert.equal(find(container, ".crumb.here").tagName, "SPAN");
  });
});

test("‹ calls onBack and no crumb", async () => {
  const calls: string[] = [];
  await withPage(calls, {}, async (container) => {
    assert.equal(textOf(container, ".back"), "‹");
    await clickElement(find(container, ".back"));
    assert.deepEqual(calls, ["onBack"]);
  });
});

test("the orrery crumb calls onGoList, the epic crumb onGoEpic, and the phase crumb onBack", async () => {
  const calls: string[] = [];
  await withPage(calls, {}, async (container) => {
    await clickElement(crumbButton(container, "orrery"));
    await clickElement(crumbButton(container, "orrery-style"));
    await clickElement(crumbButton(container, "api · implementing"));
    assert.deepEqual(calls, ["onGoList", "onGoEpic", "onBack"]);
  });
});

test("a running step's box sends its nudge text through nudge", async () => {
  const calls: string[] = [];
  await withPage(calls, { step: step({ end: "", held: false }) }, async (container) => {
    assert.equal(container.querySelector(".stepbox") !== null, true);
    assert.equal(textarea(container).placeholder, interruptsPlaceholder);
    await type(textarea(container), "check the migration");
    await press(textarea(container), "Enter");
    await settle();
    assert.deepEqual(calls, ["nudge check the migration"]);
  });
});

test("a held step's box offers the nudge that resumes it", async () => {
  const calls: string[] = [];
  await withPage(calls, { step: step({ end: "", held: true }) }, async (container) => {
    assert.equal(container.querySelector(".composer.edge-held") !== null, true);
    assert.equal(textarea(container).placeholder, resumesPlaceholder);
    await type(textarea(container), "use the new schema");
    await press(textarea(container), "Enter");
    await settle();
    assert.deepEqual(calls, ["nudge use the new schema"]);
  });
});

test("an ended step shows no box", async () => {
  await withPage([], { step: step({ end: "done" }) }, async (container) => {
    assert.equal(container.querySelector(".stepbox") === null, true);
  });
});
