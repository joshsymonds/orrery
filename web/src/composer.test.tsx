import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { Composer } from "./composer";
import { flashMillis } from "./flash";
import { WaitingMessageSchema, type WaitingMessage } from "./gen/orrery/v1/orrery_pb";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof Composer>[0];

const sendFailedLine = "Not sent. Your draft is kept; press Enter to try again.";
const pullFailedLine = "Could not pull the message back.";
const serverText = "upstream said: secret-session-7";

function baseProps(init: Partial<Props>): Props {
  return {
    label: "web",
    send: () => Promise.resolve(),
    pull: null,
    newPromptId: () => "prompt-1",
    ...init,
  };
}

async function withComposer(
  props: Partial<Props>,
  check: (box: HTMLTextAreaElement, container: HTMLElement) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<Composer {...baseProps(props)} />, container);
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
  assert.ok(box, "the composer renders a textarea");
  return box;
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

// settle lets a pending send or pull resolve and its state update render.
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function errorLine(container: HTMLElement): Element | null {
  return container.querySelector(".composer-error");
}

// advance moves the mocked clock forward by ms and flushes the renders its timers cause.
async function advance(t: TestContext, ms: number): Promise<void> {
  await act(() => {
    t.mock.timers.tick(ms);
  });
}

function flashing(container: HTMLElement): boolean {
  const root = container.querySelector(".composer");
  assert.ok(root, "the composer renders its root");
  return root.classList.contains("flash");
}

function sendButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>("button.go");
  assert.ok(button, "the composer renders a send button");
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

// queuedRows reads each drawn queued message as its tag, its text and whether it is marked canceled.
function queuedRows(container: HTMLElement): { tag: string; text: string; canceled: boolean }[] {
  return Array.from(container.querySelectorAll("li.queued"), (row) => ({
    tag: row.querySelector(".tag")?.textContent ?? "",
    text: row.querySelector(".text")?.textContent ?? "",
    canceled: row.classList.contains("canceled"),
  }));
}

test("Enter sends the text once with a prompt id and clears the box on success", async () => {
  const calls: { text: string; promptId: string }[] = [];
  await withComposer(
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
  await withComposer({ send: async () => void calls++ }, async (box) => {
    await type(box, "   ");
    await press(box, "Enter");
    await settle();
    assert.equal(calls, 0);
    assert.equal(box.value, "   ");
  });
});

test("a rejected send keeps the text, shows the fixed line, and is not retried by the composer", async () => {
  const promptIds: string[] = [];
  await withComposer(
    {
      send: async (_text, promptId) => {
        promptIds.push(promptId);
        throw new Error(serverText);
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
      assert.equal(line.textContent, sendFailedLine);
      assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
      assert.deepEqual(promptIds, ["id-1"], "no call happens without a new Enter");
    },
  );
});

test("Enter after a rejected send reuses its prompt id while the text is unchanged", async () => {
  const promptIds: string[] = [];
  let attempts = 0;
  let minted = 0;
  await withComposer(
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
  await withComposer(
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
  await withComposer(
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
  await withComposer(
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
  await withComposer(
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
  await withComposer({ pull: null }, async (box, container) => {
    await press(box, "ArrowUp");
    await settle();
    assert.equal(box.value, "");
    assert.equal(errorLine(container), null);
  });
});

test("a rejected pull shows the fixed line and leaves the box empty", async () => {
  await withComposer(
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
      assert.equal(line.textContent, pullFailedLine);
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
  await withComposer({ send }, async (box) => {
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
  await withComposer({ pull }, async (box) => {
    await press(box, "ArrowUp");
    await type(box, "typed meanwhile");
    await act(() => {
      resolvePull("queued text");
    });
    await settle();
    assert.equal(box.value, "queued text\ntyped meanwhile");
  });
});

test("Enter with text flashes the composer at once and clears 300 ms later", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  await withComposer({}, async (box, container) => {
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
  t.mock.timers.enable({ apis: ["setTimeout"] });
  await withComposer({}, async (box, container) => {
    await press(box, "Enter");
    assert.equal(flashing(container), false, "an empty box does not flash");
    await type(box, "   ");
    await press(box, "Enter");
    assert.equal(flashing(container), false, "a blank box does not flash");
  });
});

test("Enter during an in-flight send does not flash", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let finish: () => void = () => {};
  const send = () =>
    new Promise<void>((resolve) => {
      finish = resolve;
    });
  await withComposer({ send }, async (box, container) => {
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
  t.mock.timers.enable({ apis: ["setTimeout"] });
  await withComposer({}, async (box, container) => {
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
  t.mock.timers.enable({ apis: ["setTimeout"] });
  await withComposer({ pull: async () => "queued text" }, async (box, container) => {
    await press(box, "ArrowUp");
    assert.equal(flashing(container), true, "flashing at once after a pull starts");
    await advance(t, flashMillis - 1);
    assert.equal(flashing(container), true, "still flashing 1 ms before the window ends");
    await advance(t, 1);
    assert.equal(flashing(container), false, "cleared when the window ends");
  });
});

test("Up with text in the box does not flash", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  await withComposer({ pull: async () => "queued text" }, async (box, container) => {
    await type(box, "typed");
    await press(box, "ArrowUp");
    assert.equal(flashing(container), false, "a box with text does not flash on Up");
  });
});

test("the placeholder names the row", async () => {
  await withComposer({ label: "api" }, async (box) => {
    assert.equal(box.placeholder, "Reply to api");
  });
});

test("the send button is a button labeled send inside the composer", async () => {
  await withComposer({}, async (_box, container) => {
    const button = sendButton(container);
    assert.equal(button.getAttribute("type"), "button");
    assert.equal(button.getAttribute("aria-label"), "send");
    assert.equal(button.textContent, "↑");
    assert.equal(container.querySelector(".composer button.go") !== null, true);
  });
});

test("a click on the send button sends the text once with a prompt id and clears the box", async () => {
  const calls: { text: string; promptId: string }[] = [];
  await withComposer(
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
  await withComposer(
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
  await withComposer({ send: async () => void calls++ }, async (box, container) => {
    await type(box, "   ");
    await clickSend(container);
    await settle();
    assert.equal(calls, 0);
    assert.equal(box.value, "   ");
  });
});

test("queued messages draw above the input in the given order, each tagged, a canceled one marked", async () => {
  const queued = [queuedMessage("first"), queuedMessage("second", true), queuedMessage("third")];
  await withComposer({ queued }, async (_box, container) => {
    assert.deepEqual(queuedRows(container), [
      { tag: "waiting", text: "first", canceled: false },
      { tag: "canceled", text: "second", canceled: true },
      { tag: "waiting", text: "third", canceled: false },
    ]);
    const root = container.querySelector(".compose");
    assert.ok(root, "the composer renders its root");
    assert.deepEqual(Array.from(root.children, (child) => child.className), ["queue", "composer"]);
  });
});

test("no queue is drawn when no message is queued", async () => {
  await withComposer({ queued: [] }, async (_box, container) => {
    assert.equal(container.querySelector(".queue") !== null, false);
    assert.equal(container.querySelector("li.queued") !== null, false);
  });
});

test("Up in an empty box still pulls the text back with messages queued", async () => {
  let pulls = 0;
  await withComposer(
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
  await withComposer({ queued: [queuedMessage(markup)] }, async (_box, container) => {
    assert.equal(queuedRows(container)[0].text, markup);
    assert.equal(container.querySelector("li.queued img") !== null, false, "no image element is drawn");
    assert.equal(container.querySelector("li.queued b") !== null, false, "no bold element is drawn");
  });
});
