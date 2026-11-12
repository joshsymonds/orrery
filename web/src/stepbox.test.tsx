import assert from "node:assert/strict";
import { test } from "node:test";

import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { StepBox, outcomeOf } from "./stepbox";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof StepBox>[0];

const serverText = "upstream said: secret-session-7";
const notKnownNudge = "nudge not known: it may have landed; nothing was resent";

// refusals are the codes the server refuses a call with; each carries its raw message.
const refusals: [string, unknown][] = [
  ["failed_precondition", new ConnectError("step is done", Code.FailedPrecondition)],
  ["not_found", new ConnectError("no such step", Code.NotFound)],
  ["invalid_argument", new ConnectError("bad text", Code.InvalidArgument)],
];

// unknowns are the failures that leave the call's outcome unknown.
const unknowns: [string, unknown][] = [
  ["unavailable", new ConnectError("try later", Code.Unavailable)],
  ["deadline_exceeded", new ConnectError("too slow", Code.DeadlineExceeded)],
  ["a plain Error", new Error(serverText)],
];

function baseProps(init: Partial<Props>): Props {
  return {
    label: "api",
    nudge: () => Promise.resolve(),
    hold: () => Promise.resolve(),
    stop: () => Promise.resolve(),
    ...init,
  };
}

async function withBox(
  props: Partial<Props>,
  check: (box: HTMLTextAreaElement, container: HTMLElement) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<StepBox {...baseProps(props)} />, container);
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
  assert.ok(box, "the step box renders a textarea");
  return box;
}

function findButton(container: HTMLElement, name: string): HTMLButtonElement | undefined {
  return [...container.querySelectorAll("button")].find((button) => button.textContent === name);
}

function button(container: HTMLElement, name: string): HTMLButtonElement {
  const found = findButton(container, name);
  assert.ok(found, `the step box has a ${name} button`);
  return found;
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

async function click(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

// settle lets a pending call resolve and its state update render.
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function noticeLine(container: HTMLElement): Element | null {
  return container.querySelector(".stepbox-notice");
}

function assertNotice(container: HTMLElement, text: string, role: "status" | "alert"): void {
  const line = noticeLine(container);
  assert.ok(line, "the outcome line is shown");
  assert.equal(line.textContent, text);
  assert.equal(line.getAttribute("role"), role);
}

function assertNoNotice(container: HTMLElement): void {
  assert.equal(noticeLine(container), null, "no outcome line is shown");
}

// askStop presses Stop, which reveals Confirm stop and Cancel.
async function askStop(container: HTMLElement): Promise<void> {
  await click(button(container, "Stop"));
}

test("outcomeOf refuses failed_precondition, not_found and invalid_argument with the raw message", () => {
  for (const [, error] of refusals) {
    const refused = error as ConnectError;
    assert.deepEqual(outcomeOf(refused), { outcome: "refused", message: refused.rawMessage });
  }
});

test("outcomeOf calls unavailable, deadline_exceeded and a plain Error not known", () => {
  for (const [, error] of unknowns) {
    assert.deepEqual(outcomeOf(error), { outcome: "unknown" });
  }
});

test("Enter sends the nudge text once, shows landed, and clears the draft", async () => {
  const sent: string[] = [];
  await withBox(
    {
      nudge: async (text) => {
        sent.push(text);
      },
    },
    async (box, container) => {
      assert.equal(box.placeholder, "Nudge api");
      await type(box, "check the migration");
      await press(box, "Enter");
      await settle();
      assert.deepEqual(sent, ["check the migration"]);
      assert.equal(box.value, "");
      assertNotice(container, "nudge landed", "status");
    },
  );
});

test("a blank box sends nothing on Enter", async () => {
  let calls = 0;
  await withBox({ nudge: async () => void calls++ }, async (box, container) => {
    await type(box, "   ");
    await press(box, "Enter");
    await settle();
    assert.equal(calls, 0);
    assert.equal(box.value, "   ");
    assertNoNotice(container);
  });
});

test("Enter while a nudge is in flight sends nothing more", async () => {
  const sent: string[] = [];
  let finish: () => void = () => {};
  await withBox(
    {
      nudge: (text) => {
        sent.push(text);
        return new Promise<void>((resolve) => {
          finish = resolve;
        });
      },
    },
    async (box, container) => {
      await type(box, "only once");
      await press(box, "Enter");
      await press(box, "Enter");
      await press(box, "Enter");
      assert.deepEqual(sent, ["only once"]);
      finish();
      await settle();
      assert.equal(box.value, "");
      assertNotice(container, "nudge landed", "status");
    },
  );
});

test("text typed while a nudge is in flight stays in the box when it lands", async () => {
  let finish: () => void = () => {};
  await withBox(
    {
      nudge: () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    },
    async (box) => {
      await type(box, "first message");
      await press(box, "Enter");
      await type(box, "second draft");
      await act(() => {
        finish();
      });
      await settle();
      assert.equal(box.value, "second draft");
    },
  );
});

test("a nudge that failed is never resent by the box itself", async () => {
  let calls = 0;
  await withBox(
    {
      nudge: async () => {
        calls++;
        throw new ConnectError("try later", Code.Unavailable);
      },
    },
    async (box) => {
      await type(box, "once");
      await press(box, "Enter");
      await settle();
      await settle();
      assert.equal(calls, 1);
    },
  );
});

test("each Enter after a refused nudge sends one nudge", async () => {
  let calls = 0;
  await withBox(
    {
      nudge: async () => {
        calls++;
        throw new ConnectError("step is done", Code.FailedPrecondition);
      },
    },
    async (box) => {
      await type(box, "again");
      await press(box, "Enter");
      await settle();
      await press(box, "Enter");
      await settle();
      assert.equal(calls, 2);
    },
  );
});

test("Enter after a not-known nudge sends nothing while the text is unchanged", async () => {
  let calls = 0;
  await withBox(
    {
      nudge: async () => {
        calls++;
        throw new ConnectError("try later", Code.Unavailable);
      },
    },
    async (box, container) => {
      await type(box, "maybe landed");
      await press(box, "Enter");
      await settle();
      await press(box, "Enter");
      await settle();
      assert.equal(calls, 1, "the unknown text is not sent again");
      assert.equal(box.value, "maybe landed");
      assertNotice(container, notKnownNudge, "alert");
    },
  );
});

test("editing the text after a not-known nudge lets Enter send it", async () => {
  const sent: string[] = [];
  let attempts = 0;
  await withBox(
    {
      nudge: async (text) => {
        sent.push(text);
        attempts++;
        if (attempts === 1) {
          throw new ConnectError("try later", Code.Unavailable);
        }
      },
    },
    async (box, container) => {
      await type(box, "maybe landed");
      await press(box, "Enter");
      await settle();
      await type(box, "maybe landed, retyped");
      await press(box, "Enter");
      await settle();
      assert.deepEqual(sent, ["maybe landed", "maybe landed, retyped"]);
      assertNotice(container, "nudge landed", "status");
    },
  );
});

for (const [name, error] of refusals) {
  test(`a nudge refused as ${name} keeps the draft and shows the refusal's message`, async () => {
    await withBox(
      {
        nudge: async () => {
          throw error;
        },
      },
      async (box, container) => {
        await type(box, "keep me");
        await press(box, "Enter");
        await settle();
        assert.equal(box.value, "keep me");
        assertNotice(container, `nudge refused: ${(error as ConnectError).rawMessage}`, "alert");
      },
    );
  });

  test(`an interrupt refused as ${name} shows the refusal's message`, async () => {
    let holds = 0;
    await withBox(
      {
        hold: async () => {
          holds++;
          throw error;
        },
      },
      async (_box, container) => {
        await click(button(container, "Interrupt"));
        await settle();
        assert.equal(holds, 1);
        assertNotice(container, `interrupt refused: ${(error as ConnectError).rawMessage}`, "alert");
      },
    );
  });

  test(`a stop refused as ${name} shows the refusal's message`, async () => {
    let stops = 0;
    await withBox(
      {
        stop: async () => {
          stops++;
          throw error;
        },
      },
      async (_box, container) => {
        await askStop(container);
        await click(button(container, "Confirm stop"));
        await settle();
        assert.equal(stops, 1);
        assertNotice(container, `stop refused: ${(error as ConnectError).rawMessage}`, "alert");
      },
    );
  });
}

for (const [name, error] of unknowns) {
  test(`a nudge that fails as ${name} keeps the draft and says it is not known`, async () => {
    await withBox(
      {
        nudge: async () => {
          throw error;
        },
      },
      async (box, container) => {
        await type(box, "keep me");
        await press(box, "Enter");
        await settle();
        assert.equal(box.value, "keep me");
        assertNotice(container, notKnownNudge, "alert");
        assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
      },
    );
  });

  test(`an interrupt that fails as ${name} says it is not known`, async () => {
    await withBox(
      {
        hold: async () => {
          throw error;
        },
      },
      async (_box, container) => {
        await click(button(container, "Interrupt"));
        await settle();
        assertNotice(container, "interrupt not known", "alert");
        assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
      },
    );
  });

  test(`a stop that fails as ${name} says it is not known`, async () => {
    await withBox(
      {
        stop: async () => {
          throw error;
        },
      },
      async (_box, container) => {
        await askStop(container);
        await click(button(container, "Confirm stop"));
        await settle();
        assertNotice(container, "stop not known", "alert");
        assert.equal(container.textContent?.includes("secret-session-7"), false, "server text is never shown");
      },
    );
  });
}

test("Interrupt calls hold once and shows landed, sending nothing else", async () => {
  let holds = 0;
  let nudges = 0;
  let stops = 0;
  await withBox(
    {
      hold: async () => {
        holds++;
      },
      nudge: async () => {
        nudges++;
      },
      stop: async () => {
        stops++;
      },
    },
    async (_box, container) => {
      await click(button(container, "Interrupt"));
      await settle();
      assert.equal(holds, 1);
      assert.equal(nudges, 0);
      assert.equal(stops, 0);
      assertNotice(container, "interrupt landed", "status");
    },
  );
});

test("Stop alone calls nothing and reveals Confirm stop and Cancel", async () => {
  let stops = 0;
  let holds = 0;
  let nudges = 0;
  await withBox(
    {
      stop: async () => {
        stops++;
      },
      hold: async () => {
        holds++;
      },
      nudge: async () => {
        nudges++;
      },
    },
    async (_box, container) => {
      await askStop(container);
      await settle();
      assert.ok(findButton(container, "Confirm stop"), "Confirm stop is shown");
      assert.ok(findButton(container, "Cancel"), "Cancel is shown");
      assert.equal(stops, 0);
      assert.equal(holds, 0);
      assert.equal(nudges, 0);
      assertNoNotice(container);
    },
  );
});

test("Cancel hides Confirm stop and Cancel and calls nothing", async () => {
  let stops = 0;
  await withBox(
    {
      stop: async () => {
        stops++;
      },
    },
    async (_box, container) => {
      await askStop(container);
      await click(button(container, "Cancel"));
      await settle();
      assert.equal(findButton(container, "Confirm stop"), undefined);
      assert.equal(findButton(container, "Cancel"), undefined);
      assert.ok(findButton(container, "Stop"), "Stop is shown again");
      assert.equal(stops, 0);
      assertNoNotice(container);
    },
  );
});

test("Confirm stop calls stop once and shows stopped", async () => {
  let stops = 0;
  await withBox(
    {
      stop: async () => {
        stops++;
      },
    },
    async (_box, container) => {
      await askStop(container);
      await click(button(container, "Confirm stop"));
      await settle();
      assert.equal(stops, 1);
      assert.equal(findButton(container, "Confirm stop"), undefined);
      assertNotice(container, "stopped", "status");
    },
  );
});

test("a message with markup is drawn as text, not as elements", async () => {
  const markup = '<img src="x" onerror="alert(1)"><b>bold</b>';
  await withBox(
    {
      nudge: async () => {
        throw new ConnectError(markup, Code.FailedPrecondition);
      },
    },
    async (box, container) => {
      await type(box, "x");
      await press(box, "Enter");
      await settle();
      assertNotice(container, `nudge refused: ${markup}`, "alert");
      assert.equal(container.querySelector("img"), null);
      assert.equal(container.querySelector("b"), null);
    },
  );
});
