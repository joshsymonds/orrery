import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { clock } from "./reader";
import { Talk, type TalkSession, type TalkTarget, talkPrompt } from "./talk";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof Talk>[0];

const epic = "style-b2";
const repositoryPath = "/synthetic/orrery";

// paned is a session with a pane and a label, so it opens by its pane and reads by its label.
const paned: TalkSession = {
  id: "0f3a9c21d4e56b78a0f3a9c21d4e56b7",
  pane: "%7",
  label: "api",
  call: "epic_get",
  at: 1768000000n,
};

// headless is a session with neither a pane nor a label, so it opens by its id and reads by the first eight of it.
const headless: TalkSession = {
  id: "9b2c4e6f80a1b3c5d7e9f0a2b4c6d8e0",
  pane: "",
  label: "",
  call: "trace",
  at: 1768003600n,
};

const panedLine = `api · epic_get · ${clock(paned.at)}`;
const headlessLine = `9b2c4e6f · trace · ${clock(headless.at)}`;

function baseProps(init: Partial<Props>): Props {
  return {
    epic,
    sessions: [paned, headless],
    repository: "",
    start: () => Promise.reject(new Error("start was not expected")),
    onOpen: () => undefined,
    ...init,
  };
}

async function withTalk(init: Partial<Props>, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<Talk {...baseProps(init)} />, container);
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

function buttonNamed(container: HTMLElement, name: string): HTMLButtonElement | undefined {
  return [...container.querySelectorAll("button")].find((button) => button.textContent === name);
}

function textsOf(container: HTMLElement, selector: string): (string | null)[] {
  return [...container.querySelectorAll(selector)].map((element) => element.textContent);
}

// click presses the button with the given text and reports whether there was one to press.
async function click(container: HTMLElement, name: string): Promise<boolean> {
  const target = buttonNamed(container, name);
  if (target === undefined) {
    return false;
  }
  await act(() => {
    target.dispatchEvent(new Event("click", { bubbles: true }));
  });
  return true;
}

// settle lets a pending start resolve and its state update render.
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

test("talkPrompt names the epic and asks for epic_get and then the user's words", () => {
  assert.equal(
    talkPrompt("style-b2"),
    "This session is about orrery epic style-b2. Read it with epic_get, then wait for my words.",
  );
});

test("Talk opens a list whose lines carry each session's label, call and clock", async () => {
  await withTalk({}, async (container) => {
    assert.equal(container.querySelector("ul.talk") === null, true, "the list is closed at first");
    assert.equal(container.querySelector("button.act")?.textContent, "Talk");
    assert.equal(await click(container, "Talk"), true);
    assert.deepEqual(textsOf(container, "ul.talk li button"), [panedLine, headlessLine]);
  });
});

test("a session with a pane opens its pane, and one without opens its session, each closing the list", async () => {
  const opened: TalkTarget[] = [];
  await withTalk({ onOpen: (target) => void opened.push(target) }, async (container) => {
    assert.equal(await click(container, "Talk"), true);
    assert.equal(await click(container, panedLine), true);
    assert.equal(container.querySelector("ul.talk") === null, true, "opening a session closes the list");
    assert.equal(await click(container, "Talk"), true);
    assert.equal(await click(container, headlessLine), true);
    assert.equal(container.querySelector("ul.talk") === null, true, "opening a session closes the list");
    assert.deepEqual(opened, [
      { kind: "pane", id: "%7", label: "api" },
      { kind: "session", id: "9b2c4e6f80a1b3c5d7e9f0a2b4c6d8e0", label: "9b2c4e6f" },
    ]);
  });
});

test("New session in the repository starts the epic's prompt there, then opens the returned id", async () => {
  const starts: [string, string][] = [];
  const opened: TalkTarget[] = [];
  const start = (dir: string, prompt: string): Promise<string> => {
    starts.push([dir, prompt]);
    return Promise.resolve("5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b");
  };
  const onOpen = (target: TalkTarget): void => {
    opened.push(target);
  };
  await withTalk({ repository: repositoryPath, start, onOpen }, async (container) => {
    assert.equal(await click(container, "Talk"), true);
    assert.equal(await click(container, "New session in orrery"), true);
    await settle();
    assert.deepEqual(starts, [
      [
        "/synthetic/orrery",
        "This session is about orrery epic style-b2. Read it with epic_get, then wait for my words.",
      ],
    ]);
    assert.deepEqual(opened, [{ kind: "session", id: "5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", label: "orrery" }]);
  });
});

test("while the new session starts its button is disabled, and a second press starts nothing more", async () => {
  let starts = 0;
  let finish: (id: string) => void = () => undefined;
  const opened: TalkTarget[] = [];
  const start = (): Promise<string> => {
    starts += 1;
    return new Promise<string>((resolve) => {
      finish = resolve;
    });
  };
  const onOpen = (target: TalkTarget): void => {
    opened.push(target);
  };
  await withTalk({ repository: repositoryPath, start, onOpen }, async (container) => {
    assert.equal(await click(container, "Talk"), true);
    assert.equal(await click(container, "New session in orrery"), true);
    await settle();
    assert.equal(buttonNamed(container, "New session in orrery")?.disabled, true);
    assert.equal(await click(container, "New session in orrery"), true);
    assert.equal(starts, 1);
    finish("5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b");
    await settle();
    assert.equal(opened.length, 1);
    assert.equal(opened[0]?.id, "5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b");
  });
});

test("a refused start shows its message and opens nothing", async () => {
  const opened: TalkTarget[] = [];
  const start = (): Promise<string> => Promise.reject(new Error("dir is outside the home directory"));
  const onOpen = (target: TalkTarget): void => {
    opened.push(target);
  };
  await withTalk({ repository: repositoryPath, start, onOpen }, async (container) => {
    assert.equal(await click(container, "Talk"), true);
    assert.equal(await click(container, "New session in orrery"), true);
    await settle();
    assert.deepEqual(textsOf(container, "p.error"), ["dir is outside the home directory"]);
    assert.equal(container.querySelector("p.error")?.getAttribute("role"), "alert");
    assert.equal(buttonNamed(container, "New session in orrery")?.disabled, false);
    assert.deepEqual(opened, []);
  });
});

test("an epic with no repository offers no new session, only its session lines", async () => {
  await withTalk({ repository: "" }, async (container) => {
    assert.equal(await click(container, "Talk"), true);
    assert.deepEqual(textsOf(container, "ul.talk li button"), [panedLine, headlessLine]);
    assert.equal(buttonNamed(container, "New session in orrery") === undefined, true);
  });
});
