import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import type { Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";

const now = 1000;

interface Props {
  rows: Row[];
  error: string;
  now: number;
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "box",
    state: "working",
    summary: "",
    question: "",
    changed: 0,
    seen: 0,
    unseen: false,
    ...init,
  };
}

// Attention order: a (needs you), c (new), b (working), d (seen). Project order: a, d, c, b.
function fixture(): Row[] {
  return [
    row({
      key: "a",
      label: "api",
      project: "/work/api",
      state: "waiting",
      question: "Ship it?",
      summary: "build green",
      changed: 300,
      seen: 100,
      unseen: true,
    }),
    row({
      key: "c",
      label: "docs",
      project: "/work/docs",
      host: "laptop",
      state: "done",
      summary: "wrote the guide",
      changed: 150,
      seen: 50,
      unseen: true,
    }),
    row({ key: "b", label: "web", project: "/work/web", state: "working", summary: "bundling", changed: 200, seen: 200 }),
    row({
      key: "d",
      label: "old",
      project: "/work/api",
      state: "done",
      summary: "finished last week",
      changed: 10,
      seen: 10,
    }),
  ];
}

const keysHelpList = ["j", "k", "↓", "↑", "gg", "G", "a", "p", "w", "n", "/", "?", "Esc"];

async function mount(container: HTMLElement, props: Props): Promise<void> {
  await act(() => {
    render(<App rows={props.rows} error={props.error} now={props.now} />, container);
  });
}

async function withApp(
  props: Partial<Props>,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await mount(container, { rows: fixture(), error: "", now, ...props });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

async function press(key: string, modifiers: KeyboardEventInit = {}): Promise<KeyboardEvent> {
  const event = new KeyboardEvent("keydown", { key, cancelable: true, ...modifiers });
  await act(() => {
    document.dispatchEvent(event);
  });
  return event;
}

async function pressEach(keys: string[]): Promise<void> {
  for (const key of keys) {
    await press(key);
  }
}

async function typeFilter(container: HTMLElement, text: string): Promise<void> {
  const input = filterInput(container);
  await act(() => {
    input.value = text;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function filterInput(container: HTMLElement): HTMLInputElement {
  const input = container.querySelector("input");
  assert.ok(input, "the filter input is rendered");
  return input;
}

function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll("[data-row]")].map((element) => element.getAttribute("data-key") ?? "");
}

function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".b-row.cur")?.getAttribute("data-key") ?? null;
}

function sectionTitles(container: HTMLElement): (string | null)[] {
  return [...container.querySelectorAll(".b-sec")].map((element) => element.textContent);
}

function rowElement(container: HTMLElement, key: string): Element {
  const element = container.querySelector(`[data-row][data-key="${key}"]`);
  assert.ok(element, `row ${key} is rendered`);
  return element;
}

test("j and ArrowDown move the cursor one row down and clamp at the last row", () =>
  withApp({}, async (container) => {
    assert.equal(cursorKey(container), "a");
    await press("j");
    assert.equal(cursorKey(container), "c");
    await press("ArrowDown");
    assert.equal(cursorKey(container), "b");
    await press("ArrowDown");
    assert.equal(cursorKey(container), "d");
    await press("j");
    assert.equal(cursorKey(container), "d");
  }));

test("k and ArrowUp move the cursor one row up and clamp at the first row", () =>
  withApp({}, async (container) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
    await press("k");
    assert.equal(cursorKey(container), "b");
    await press("ArrowUp");
    assert.equal(cursorKey(container), "c");
    await press("ArrowUp");
    assert.equal(cursorKey(container), "a");
    await press("k");
    assert.equal(cursorKey(container), "a");
  }));

test("gg goes to the first row, and a lone g or a g broken by another key does not", () =>
  withApp({}, async (container) => {
    await press("G");
    await press("g");
    assert.equal(cursorKey(container), "d", "one g moved the cursor");
    await press("k");
    await press("g");
    assert.equal(cursorKey(container), "b", "g after another key moved the cursor");
    await press("g");
    assert.equal(cursorKey(container), "a");
  }));

test("G goes to the last row", () =>
  withApp({}, async (container) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
  }));

test("a shows rows by attention and keeps the cursor's row", () =>
  withApp({}, async (container) => {
    await press("p");
    await press("j");
    assert.equal(cursorKey(container), "d");
    await press("a");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
    assert.equal(cursorKey(container), "d");
    assert.deepEqual(sectionTitles(container), ["Needs you", "New since you looked", "Working", "Seen"]);
  }));

test("p shows rows by project and keeps the cursor's row", () =>
  withApp({}, async (container) => {
    await press("j");
    assert.equal(cursorKey(container), "c");
    await press("p");
    assert.deepEqual(rowKeys(container), ["a", "d", "c", "b"]);
    assert.equal(cursorKey(container), "c");
    assert.deepEqual(sectionTitles(container), ["/work/api", "/work/docs", "/work/web"]);
  }));

test("w goes to the first waiting row and does nothing when no row waits", () =>
  withApp({}, async (container) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
    await press("w");
    assert.equal(cursorKey(container), "a");

    await mount(container, { rows: fixture().slice(1), error: "", now });
    await press("G");
    await press("w");
    assert.equal(cursorKey(container), "d");
  }));

test("n goes to the next unseen row after the cursor and wraps", () =>
  withApp({}, async (container) => {
    assert.equal(cursorKey(container), "a");
    await press("n");
    assert.equal(cursorKey(container), "c");
    await press("n");
    assert.equal(cursorKey(container), "a");
  }));

test("/ focuses the filter input and does not type the slash", () =>
  withApp({}, async (container) => {
    const event = await press("/");
    assert.equal(event.defaultPrevented, true, "the slash was left to the input");
    assert.equal(document.activeElement, filterInput(container));
    assert.equal(filterInput(container).value, "");
  }));

test("? toggles a help panel listing exactly the keys", () =>
  withApp({}, async (container) => {
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
    await press("?");
    const help = container.querySelector('[aria-label="keys"]');
    assert.ok(help, "help panel is open");
    const listed = [...help.querySelectorAll("kbd")].map((element) => element.textContent);
    assert.deepEqual(listed, keysHelpList);
    await press("?");
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
  }));

test("Esc closes the help when the filter does not have focus", () =>
  withApp({}, async (container) => {
    await press("?");
    await press("Escape");
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
  }));

test("with the filter focused, shortcut keys do nothing and Enter blurs it", () =>
  withApp({}, async (container) => {
    await press("/");
    await press("j");
    await press("G");
    await press("?");
    assert.equal(cursorKey(container), "a");
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
    await press("Enter");
    assert.notEqual(document.activeElement, filterInput(container));
    await press("j");
    assert.equal(cursorKey(container), "c");
  }));

test("Esc in the filter clears it and blurs it", () =>
  withApp({}, async (container) => {
    await press("/");
    await typeFilter(container, "docs");
    assert.deepEqual(rowKeys(container), ["c"]);
    await press("Escape");
    assert.equal(filterInput(container).value, "");
    assert.notEqual(document.activeElement, filterInput(container));
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
  }));

test("the filter keeps rows whose fields contain every typed word, case-insensitively", () =>
  withApp({}, async (container) => {
    await press("/");
    await typeFilter(container, "API WAITING");
    assert.deepEqual(rowKeys(container), ["a"]);
    await typeFilter(container, "api done");
    assert.deepEqual(rowKeys(container), ["d"]);
    await typeFilter(container, "build");
    assert.deepEqual(rowKeys(container), ["a"]);
    await typeFilter(container, "laptop");
    assert.deepEqual(rowKeys(container), ["c"]);
    await typeFilter(container, "");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
  }));

test("the filter hides sections left empty", () =>
  withApp({}, async (container) => {
    await press("/");
    await typeFilter(container, "docs");
    assert.deepEqual(sectionTitles(container), ["New since you looked"]);
  }));

test("modifier keys leave the cursor alone", () =>
  withApp({}, async (container) => {
    await press("j", { ctrlKey: true });
    await press("j", { metaKey: true });
    await press("j", { altKey: true });
    await press("G", { ctrlKey: true });
    assert.equal(cursorKey(container), "a");
  }));

test("u, N and Enter outside the filter change nothing", () =>
  withApp({}, async (container) => {
    await press("u");
    await press("N");
    await press("Enter");
    assert.equal(cursorKey(container), "a");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
    assert.equal(filterInput(container).value, "");
  }));

test("the cursor keeps its row across new rows and clamps when its row leaves", () =>
  withApp({}, async (container) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");

    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture(), added], error: "", now });
    assert.equal(cursorKey(container), "b");

    await mount(container, { rows: fixture().filter((item) => item.key !== "b"), error: "", now });
    assert.equal(cursorKey(container), "d");
  }));

test("an unmoved cursor keeps its row when a row sorts in above it", () =>
  withApp({}, async (container) => {
    assert.equal(cursorKey(container), "a");
    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture(), added], error: "", now });
    assert.equal(cursorKey(container), "a");
  }));

test("a clamped cursor keeps the row it moved to", () =>
  withApp({}, async (container) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");
    await mount(container, { rows: fixture().filter((item) => item.key !== "b"), error: "", now });
    assert.equal(cursorKey(container), "d");
    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture().filter((item) => item.key !== "b"), added], error: "", now });
    assert.equal(cursorKey(container), "d");
  }));

test("only unseen rows carry the unseen mark", () =>
  withApp({}, (container) => {
    const marked = [...container.querySelectorAll("[data-unseen]")].map((element) => element.getAttribute("data-key"));
    assert.deepEqual(marked, ["a", "c"]);
    assert.equal(rowElement(container, "b").hasAttribute("data-unseen"), false);
    assert.equal(rowElement(container, "d").hasAttribute("data-unseen"), false);
  }));

test("a row shows its state, label, project's last path element, age, and reply text", () =>
  withApp({}, (container) => {
    const element = rowElement(container, "a");
    assert.equal(element.querySelector(".label")?.textContent, "api");
    assert.equal(element.querySelector(".proj")?.textContent, "api");
    assert.equal(element.querySelector(".age")?.textContent, "11m");
    assert.ok(element.textContent?.includes("waiting"));
    assert.equal(element.querySelector(".l2")?.textContent, "Ship it?", "the question replaces the summary");
  }));

test("a row that is not waiting shows its summary, not its question", () =>
  withApp(
    {
      rows: [row({ key: "s", state: "working", question: "stale?", summary: "compiling" })],
    },
    (container) => {
      assert.equal(rowElement(container, "s").querySelector(".l2")?.textContent, "compiling", "only a waiting row shows its question");
    },
  ));

test("reply text renders as text, never as markup", () =>
  withApp(
    {
      rows: [row({ key: "x", state: "working", summary: "<script>alert(1)</script> and <img src=x onerror=alert(1)>" })],
    },
    (container) => {
      assert.equal(container.querySelector("script"), null);
      assert.equal(container.querySelector("img"), null);
      assert.equal(
        rowElement(container, "x").querySelector(".l2")?.textContent,
        "<script>alert(1)</script> and <img src=x onerror=alert(1)>",
      );
    },
  ));

test("an empty project title shows the row's host", () =>
  withApp({ rows: [row({ key: "p", project: "", host: "box", state: "working" })] }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["box"]);
  }));

test("an empty project title with no host shows no directory", () =>
  withApp({ rows: [row({ key: "p", project: "", host: "", state: "working" })] }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["no directory"]);
  }));

test("a non-empty error shows one line above the list", () =>
  withApp({ error: "list unavailable" }, (container) => {
    assert.equal(container.querySelector('[role="alert"]')?.textContent, "list unavailable");
  }));

test("no error line shows when the error is empty", () =>
  withApp({ error: "" }, (container) => {
    assert.equal(container.querySelector('[role="alert"]'), null);
  }));
