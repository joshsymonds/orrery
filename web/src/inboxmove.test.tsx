import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";

// InboxWebMove: a row a list answer moves to another place is drawn in its new place, marked as moving until its move's
// time has passed on the fake clock; a row that lands behind a shut ended line marks that line's count, and the line stays
// shut; reduced motion draws no mark, but the row still lands; and the open entry a list answer moves stays drawn in its
// place. Each test mounts App over invented rows in a fresh DOM and reads the DOM.

const now = 1000;
// wideWidth is the window the tests run at, past the 760px the wide layout starts at.
const wideWidth = 1024;
// pastMove is more time than a moved row's mark lasts on the fake clock.
const pastMove = 1000;
const widgets = "/home/owner/code/widgets";
const gadgets = "/home/owner/code/gadgets";
// reducedQuery is the query of reduced motion, as App reads it.
const reducedQuery = "(prefers-reduced-motion: reduce)";

// session is a headless session row of the widgets directory, which a list answer gives as done, neither ended nor
// archived, unless the init says otherwise.
function session(id: string, init: Partial<Row> = {}): Row {
  return {
    key: `session:${id}`,
    id,
    archiveId: id,
    kind: "session",
    label: `${id} work`,
    project: widgets,
    host: "",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    tool: "",
    putOff: false,
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "headless",
    ...init,
  };
}

// first is the row a list answer draws first in the widgets project, the cursor's entry until another is chosen, and
// second the row after it.
const first = session("a", { changed: 300 });
const second = session("b", { changed: 200 });

// fakeFetch answers each unary call with an empty message, and holds each WatchConversation stream open, as the reader of
// an open conversation follows it.
function fakeFetch(): (input: RequestInfo | URL, init?: RequestInit) => Promise<Response> {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(new Request(input, init).url).pathname;
    if (path.endsWith("/WatchConversation")) {
      return new Response(new ReadableStream<Uint8Array>(), {
        status: 200,
        headers: { "content-type": "application/connect+json" },
      });
    }
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  };
}

// show renders App over the rows a list answer carries, into container, and lets Preact draw what it changed.
function show(container: HTMLElement, clients: ReturnType<typeof createClients>, rows: Row[]): Promise<void> {
  return act(() => {
    render(
      <App rows={rows} now={now} clients={clients} host={null} onAddress={() => {}} />,
      container,
    );
  });
}

// reduceMotion makes the window's query for reduced motion match as reduced says, and leaves every other query to the
// window. It returns the function that changes the match, which calls the change listeners the App added, as the window
// does.
function reduceMotion(window: ReturnType<typeof installTestDom>, reduced: boolean): (matches: boolean) => void {
  type Query = ReturnType<typeof window.matchMedia>;
  const matchMedia = window.matchMedia.bind(window);
  const listeners = new Set<() => void>();
  let matches = reduced;
  const motion = (media: string): Query =>
    ({
      media,
      get matches() {
        return matches;
      },
      addEventListener: (_type: string, listener: () => void) => {
        listeners.add(listener);
      },
      removeEventListener: (_type: string, listener: () => void) => {
        listeners.delete(listener);
      },
    }) as unknown as Query;
  window.matchMedia = (query: string) => (query === reducedQuery ? motion(query) : matchMedia(query));
  return (next) => {
    matches = next;
    for (const listener of listeners) {
      listener();
    }
  };
}

interface Shown {
  container: HTMLElement;
  // rerender draws App over the rows a later list answer carries.
  rerender: (rows: Row[]) => Promise<void>;
  // tick moves the fake clock forward, and lets Preact draw what its timers changed.
  tick: (ms: number) => Promise<void>;
  // setReducedMotion changes whether reduced motion matches, and lets Preact draw the change.
  setReducedMotion: (matches: boolean) => Promise<void>;
}

// withApp mounts App over the rows in a fresh DOM, at a wide window, with its timers on the fake clock, and runs check. With
// reduced, the window matches reduced motion at mount. App is unmounted and the DOM removed when the test ends.
async function withApp(t: TestContext, rows: Row[], check: (shown: Shown) => Promise<void>, reduced = false): Promise<void> {
  mockTimers(t, ["setTimeout"]);
  const window = installTestDom();
  window.happyDOM.setViewport({ width: wideWidth, height: 800 });
  const changeMotion = reduceMotion(window, reduced);
  const container = document.createElement("div");
  document.body.append(container);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch() }));
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await show(container, clients, rows);
    await check({
      container,
      rerender: (next) => show(container, clients, next),
      tick: (ms) =>
        act(() => {
          t.mock.timers.tick(ms);
        }),
      setReducedMotion: (matches) =>
        act(() => {
          changeMotion(matches);
        }),
    });
  } finally {
    await teardown();
  }
}

// cardNamed is the project card of the project named name, which the inbox must draw.
function cardNamed(container: HTMLElement, name: string): HTMLElement {
  const card = container.querySelector<HTMLElement>(`section.dir[aria-label="${name}"]`);
  assert.ok(card !== null, `the inbox draws no card of ${name}`);
  return card;
}

// placed is the keys of the rows a project card draws in its tree, before its ended line's button, and behind that line,
// after it, which draw only while the line is open.
function placed(card: HTMLElement): { tree: string[]; behind: string[] } {
  const kids = [...card.querySelectorAll<HTMLElement>(".dir-kids > *")];
  const line = kids.findIndex((kid) => kid.classList.contains("fold-line"));
  const keysOf = (among: HTMLElement[]): string[] =>
    among.filter((kid) => kid.hasAttribute("data-row")).map((kid) => kid.dataset.key ?? "");
  if (line === -1) {
    return { tree: keysOf(kids), behind: [] };
  }
  return { tree: keysOf(kids.slice(0, line)), behind: keysOf(kids.slice(line + 1)) };
}

// rowOf is the row element drawn under key, which the test asserts is drawn.
function rowOf(container: HTMLElement, key: string): HTMLElement {
  const element = container.querySelector<HTMLElement>(`[data-row][data-key="${key}"]`);
  assert.ok(element !== null, `${key} is not drawn`);
  return element;
}

// cursorKey is the key of the entry the cursor rests on.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

// click clicks the first element the selector names, and lets Preact draw what the click changed. Then it unfocuses.
async function click(container: HTMLElement, selector: string): Promise<void> {
  const target = container.querySelector<HTMLElement>(selector);
  assert.ok(target !== null, `${selector} is drawn`);
  await act(() => {
    target.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
  await act(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });
}

// press sends one keydown to the document, as a key press does, and lets Preact draw what it changed.
async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

test("InboxWebMove: a row a second list answer moves to another place is marked moving in its new place, until its move's time has passed", (t) =>
  withApp(t, [first, second], async ({ container, rerender, tick }) => {
    assertNone(container.querySelector("[data-moving]"), "the first answer moves nothing");
    await rerender([first, { ...second, project: gadgets }]);
    assert.deepEqual(placed(cardNamed(container, "gadgets")), { tree: [second.key], behind: [] }, "the moved row is not in its new place");
    assert.deepEqual(placed(cardNamed(container, "widgets")), { tree: [first.key], behind: [] }, "the moved row is drawn in its old place too");
    assert.equal(rowOf(container, second.key).dataset.moving, "true", "the moved row is not marked moving");
    await tick(1);
    assert.equal(rowOf(container, second.key).dataset.moving, "true", "the mark clears before its move's time has passed");
    await tick(pastMove);
    assert.equal(rowOf(container, second.key).hasAttribute("data-moving"), false, "the mark is still there after its move's time");
    assert.deepEqual(placed(cardNamed(container, "gadgets")), { tree: [second.key], behind: [] }, "the moved row left its new place");
  }));

test("InboxWebMove: a row a second list answer ends behind a shut ended line marks the line's count, and the line stays shut", (t) =>
  withApp(t, [first, second], async ({ container, rerender, tick }) => {
    await rerender([first, { ...second, ended: true }]);
    const card = cardNamed(container, "widgets");
    const line = card.querySelector<HTMLElement>(".fold-line");
    assert.ok(line !== null, "the project draws no ended line");
    assert.equal(line.textContent?.trim(), "1 ended", "the ended line does not count the row that ended");
    assert.equal(line.dataset.moving, "true", "the line's count is not marked moving");
    assert.equal(line.getAttribute("aria-expanded"), "false", "the ended line is not shut");
    assert.deepEqual(placed(card), { tree: [first.key], behind: [] }, "the ended row is not drawn behind its shut line");
    await tick(1);
    assert.equal(line.dataset.moving, "true", "the line's mark clears before its move's time has passed");
    await tick(pastMove);
    assert.equal(line.hasAttribute("data-moving"), false, "the line's mark is still there after its move's time");
    assert.equal(line.getAttribute("aria-expanded"), "false", "the ended line opened once its mark cleared");
    assert.deepEqual(placed(card), { tree: [first.key], behind: [] }, "the ended row is drawn behind its shut line once its mark clears");
  }));

test("InboxWebMove: with reduced motion matched, a moved row draws no mark but still lands in its new place, and its mark shows once reduced motion stops matching", (t) =>
  withApp(
    t,
    [first, second],
    async ({ container, rerender, setReducedMotion }) => {
      await rerender([first, { ...second, project: gadgets }]);
      assert.deepEqual(placed(cardNamed(container, "gadgets")), { tree: [second.key], behind: [] }, "the moved row is not in its new place");
      assertNone(container.querySelector("[data-moving]"), "a row is marked moving under reduced motion");
      await setReducedMotion(false);
      assert.equal(rowOf(container, second.key).dataset.moving, "true", "the move's mark is not drawn once reduced motion stops matching");
    },
    true,
  ));

test("InboxWebMove: with reduced motion matched, a row that lands behind a shut ended line draws no mark on the line, and the line's mark shows once reduced motion stops matching", (t) =>
  withApp(
    t,
    [first, second],
    async ({ container, rerender, setReducedMotion }) => {
      await rerender([first, { ...second, project: gadgets }]);
      await rerender([first, { ...second, project: gadgets, ended: true }]);
      const line = cardNamed(container, "gadgets").querySelector<HTMLElement>(".fold-line");
      assert.ok(line !== null, "the moved row's project draws no ended line");
      assert.equal(line.textContent?.trim(), "1 ended", "the row that ended is not counted behind the line");
      assert.equal(line.hasAttribute("data-moving"), false, "the ended line is marked moving under reduced motion");
      await setReducedMotion(false);
      assert.equal(
        cardNamed(container, "gadgets").querySelector<HTMLElement>(".fold-line")?.dataset.moving,
        "true",
        "the ended line's mark is not drawn once reduced motion stops matching",
      );
    },
    true,
  ));

test("InboxWebMove: an open conversation a second list answer moves into a shut ended line stays drawn in its place, unmarked, until the cursor leaves it", (t) =>
  withApp(t, [first, second], async ({ container, rerender }) => {
    await click(container, `[data-key="${first.key}"]`);
    assert.equal(cursorKey(container), first.key, "the conversation is the cursor entry once it is clicked");
    await rerender([{ ...first, ended: true }, second]);
    const card = cardNamed(container, "widgets");
    assert.deepEqual(placed(card), { tree: [first.key, second.key], behind: [] }, "the open conversation left its place");
    assert.equal(cursorKey(container), first.key, "the open conversation stopped being the cursor entry");
    assert.equal(rowOf(container, first.key).getAttribute("aria-current"), "true", "the open conversation is not drawn as open");
    assertNone(container.querySelector("[data-moving]"), "the open conversation is marked moving");
    await press("j");
    assert.notEqual(cursorKey(container), first.key, "the cursor has not moved on");
    assert.deepEqual(placed(cardNamed(container, "widgets")), { tree: [second.key], behind: [] }, "the ended conversation is not behind its shut line once the cursor leaves it");
  }));
