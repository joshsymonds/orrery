import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { formatAddress, type Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { projectOf } from "./tree";

const now = 1000;
const apiDir = "/work/api";
const apiKey = projectOf(apiDir, "", "").key;

// quietFetch answers the app's calls without data: a unary call with an empty object, and a stream with a body that
// stays open until its call aborts, so no conversation arrives and no timer runs.
async function quietFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  if (new URL(request.url).pathname.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: apiDir,
    host: "",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    putOff: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// rows are two conversations in the api directory and one in another, all seen, so no dwell timer runs. The waiting row
// holds a question hash and every row can be archived, so an answer or an archive reaches the server.
function rows(): Row[] {
  return [
    row({
      key: "a",
      label: "api one",
      state: "waiting",
      question: "Ship it?",
      questionHash: "q-a",
      archiveKind: "session",
      archiveId: "a",
      changed: 300,
      seen: 300,
    }),
    row({ key: "b", label: "api two", archiveKind: "session", archiveId: "b", changed: 200, seen: 200 }),
    row({
      key: "c",
      label: "docs",
      project: "/work/docs",
      state: "done",
      archiveKind: "session",
      archiveId: "c",
      changed: 150,
      seen: 150,
    }),
  ];
}

interface Mounted {
  container: HTMLElement;
  // The addresses App reported, in order.
  reports: Address[];
  // The path of each request the app sent, in order.
  calls: string[];
  // show renders App again, holding address as its prop and whether the first load has answered.
  show: (address: Address | undefined, answered?: boolean) => Promise<void>;
}

// withApp mounts App over quietFetch, the rows and the contracts, holding address as its prop, and runs check. App is unmounted and the
// DOM removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  address: Address | undefined,
  check: (mounted: Mounted) => void | Promise<void>,
  contracts: Contract[] = [],
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  // sent records each request's path, in order, before quietFetch answers it.
  const sent: string[] = [];
  const clients = createClients(
    createTransport({
      baseUrl: "https://orrery.test",
      fetch: (input, init) => {
        sent.push(new URL(new Request(input, init).url).pathname);
        return quietFetch(input, init);
      },
    }),
  );
  const reports: Address[] = [];
  const shown = rows();
  const show = (next: Address | undefined, answered = true) =>
    act(() => {
      render(
        <App
          rows={shown}
          contracts={contracts}
          now={now}
          clients={clients}
          answered={answered}
          address={next}
          onAddress={(reported) => {
            reports.push(reported);
          }}
        />,
        container,
      );
    });
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await show(address);
    await check({ container, reports, calls: sent, show });
  } finally {
    await teardown();
  }
}

// resizeWindow sets the viewport of the window the test's DOM is in, as a resize does.
async function resizeWindow(width: number): Promise<void> {
  const view = (globalThis as unknown as { window: ReturnType<typeof installTestDom> }).window;
  await act(() => {
    view.happyDOM.setViewport({ width, height: 800 });
  });
}

// narrowWindow narrows the window from the wide default to width. happy-dom's media query fires change for a narrowing
// only after a resize has shown it a match, so the window is widened by a pixel first.
async function narrowWindow(width: number): Promise<void> {
  await resizeWindow(1025);
  await resizeWindow(width);
}

// press clicks an element, and lets Preact draw what the click changed.
async function press(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// one returns the element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// buttonIn returns the button under the scope whose text is label, which must be there.
function buttonIn(container: HTMLElement, scope: string, label: string): HTMLElement {
  const found = [...container.querySelectorAll<HTMLElement>(`${scope} button`)].find(
    (button) => button.textContent.trim() === label,
  );
  assert.ok(found !== undefined, `no ${label} button in ${scope}`);
  return found;
}

// headOf is the head button of the project whose name is name, the press that opens its page.
function headOf(container: HTMLElement, name: string): HTMLElement {
  return one(container, `section.dir[aria-label="${name}"] button.proj`);
}

// projectPage is the project page section, when it is drawn.
function projectPage(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label="project"]:not([hidden])');
}

// paneState is the inbox state the app draws on the panes: docked, pinned, rail or flyout.
function paneState(container: HTMLElement): string | null {
  return container.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// listHidden is whether the list the inbox draws is hidden.
function listHidden(container: HTMLElement): boolean {
  return container.querySelector(".list")?.hasAttribute("hidden") === true;
}

// cursorKey is the key of the entry the cursor rests on.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

test("ProjectPage: a project's head opens its page beside the inbox, which takes the state of an opened page", (t) =>
  withApp(t.signal, undefined, async ({ container }) => {
    await press(headOf(container, "api"));
    const page = projectPage(container);
    assert.ok(page !== null, "the project's page is drawn");
    assert.equal(page.querySelectorAll(".pc-row").length, 2, "the page lists the project's two conversations");
    assert.equal(listHidden(container), false, "at the desk the list stays drawn beside the page");
    assert.equal(paneState(container), "rail", "the inbox rests as it does with any page opened");
  }));

test("ProjectPage: its address is the project's, written while the page is open", (t) =>
  withApp(t.signal, undefined, async ({ container, reports }) => {
    await press(headOf(container, "api"));
    const last = reports[reports.length - 1];
    assert.ok(last !== undefined, "App reports the address the page opened to");
    assert.equal(formatAddress(last), `#/inbox/project/${encodeURIComponent(apiKey)}`);
  }));

test("ProjectPage: the address of a project reopens its page", (t) =>
  withApp(t.signal, { view: "inbox", entry: { kind: "project", key: apiKey } }, ({ container }) => {
    assert.ok(projectPage(container) !== null, "the page is drawn from its address");
    assert.equal(one(container, 'section[aria-label="project"]').querySelectorAll(".pc-row").length, 2);
  }));

test("ProjectPage: a click on its conversation opens that conversation and closes the page", (t) =>
  withApp(t.signal, undefined, async ({ container }) => {
    await press(headOf(container, "api"));
    await press(one(container, '.pc-row[data-key="b"]'));
    assert.ok(projectPage(container) === null, "the page closes");
    assert.equal(cursorKey(container), "b", "the cursor rests on the conversation");
    assert.ok(one(container, 'section[aria-label="reader"]').hidden === false, "the conversation's reader is drawn");
  }));

test("ProjectPage: a conversation the waiting chip keeps out of the list opens itself, not a neighbour", (t) =>
  withApp(t.signal, undefined, async ({ container }) => {
    await press(one(container, ".inbox-attn .fchip.attention"));
    assert.equal(container.querySelector('.row[data-key="b"]'), null, "the chip keeps the quiet conversation out");
    await press(headOf(container, "api"));
    await press(one(container, '.pc-row[data-key="b"]'));
    assert.ok(projectPage(container) === null, "the page closes");
    assert.equal(cursorKey(container), "b", "the cursor rests on the conversation pressed");
  }));

test("ProjectPage: Enter shuts the page and opens the cursor's conversation in its place", (t) =>
  withApp(t.signal, { view: "inbox", entry: { kind: "project", key: apiKey } }, async ({ container }) => {
    assert.ok(projectPage(container) !== null, "the page is drawn from its address");
    const cursor = cursorKey(container);
    assert.ok(cursor !== null, "the cursor rests on a row");
    await act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    assert.ok(projectPage(container) === null, "the page closes");
    assert.ok(one(container, 'section[aria-label="reader"]').hidden === false, "the cursor's conversation is drawn");
  }));

test("ProjectPage: a press on a stand-in, which the tree draws no entry of, opens nothing", (t) =>
  withApp(
    t.signal,
    undefined,
    async ({ container }) => {
      await press(headOf(container, "api"));
      const before = cursorKey(container);
      await press(one(container, '.pc-row[data-key^="standin:"]'));
      assert.ok(projectPage(container) !== null, "the page stays open");
      assert.equal(cursorKey(container), before, "the cursor does not move to a neighbour");
    },
    [create(ContractSchema, { id: "api/orphan", repository: apiDir, title: "Orphan", state: "proposed", conversation: "gone", version: 1n, changed: 250n })],
  ));

test("ProjectPage: a press on a contract's card opens the contract's panel, as the tree's press on it does, and closes the page", (t) =>
  withApp(
    t.signal,
    undefined,
    async ({ container, reports }) => {
      await press(headOf(container, "api"));
      assert.equal(container.querySelector('aside[aria-label="orphan contract"]'), null, "no panel before the press");
      await press(one(container, 'section[aria-label="project"] .wcard[data-key="contract:api/orphan"]'));
      assert.ok(projectPage(container) === null, "the page closes");
      assert.ok(
        container.querySelector('aside[aria-label="orphan contract"]') !== null,
        "the contract's panel is drawn",
      );
      const last = reports[reports.length - 1];
      assert.ok(last !== undefined, "App reports the address the press opened");
      assert.deepEqual(last, { view: "inbox", entry: { kind: "contract", id: "api/orphan" } });
    },
    [create(ContractSchema, { id: "api/orphan", repository: apiDir, title: "Orphan", state: "proposed", conversation: "gone", version: 1n, changed: 250n })],
  ));

test("ProjectPage: under 760px the page draws alone, and its Back returns to the inbox", (t) =>
  withApp(t.signal, undefined, async ({ container }) => {
    await narrowWindow(700);
    await press(headOf(container, "api"));
    assert.ok(projectPage(container) !== null, "the page is drawn");
    assert.equal(listHidden(container), true, "the list hides while the page draws alone");
    await press(buttonIn(container, 'section[aria-label="project"] .title-row', "‹"));
    assert.ok(projectPage(container) === null, "Back closes the page");
    assert.equal(listHidden(container), false, "the inbox draws again");
  }));

test("ProjectPage: New session here opens New's start form on the project's directory", (t) =>
  withApp(t.signal, undefined, async ({ container }) => {
    await press(headOf(container, "api"));
    await press(buttonIn(container, 'section[aria-label="project"]', "New session here"));
    const directory = one(container, 'input[aria-label="Directory"]') as HTMLInputElement;
    assert.equal(directory.value, apiDir);
  }));

test("ProjectPage: a project key the tree does not hold draws the line that nothing is in it", (t) =>
  withApp(t.signal, { view: "inbox", entry: { kind: "project", key: "nowhere|/nowhere" } }, ({ container }) => {
    const page = projectPage(container);
    assert.ok(page !== null, "the page area draws the project section");
    assert.equal(one(page, "p").textContent, "Nothing in the inbox is in this project.");
  }));

test("ProjectPage: an address that names no project closes the page", (t) =>
  withApp(t.signal, { view: "inbox", entry: { kind: "project", key: apiKey } }, async ({ container, show }) => {
    assert.ok(projectPage(container) !== null, "the page is drawn from its address");
    await show({ view: "inbox", entry: null });
    assert.ok(projectPage(container) === null, "the page closes");
  }));

test("ProjectPage: an address to a project draws its page once the first load has answered", (t) =>
  withApp(t.signal, undefined, async ({ container, show }) => {
    const address: Address = { view: "inbox", entry: { kind: "project", key: apiKey } };
    await show(address, false);
    assert.ok(projectPage(container) === null, "the page waits for the first load");
    await show(address, true);
    assert.ok(projectPage(container) !== null, "the page draws once the load has answered");
  }));

// pressKey presses a key on the document, as the app's key listener takes it, and lets Preact draw what the press changed.
async function pressKey(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
  });
}

// writesOf leaves out the conversation streams a cursor move opens: they are reads, not the presses' writes.
function writesOf(paths: string[]): string[] {
  return paths.filter((path) => !path.endsWith("/WatchConversation"));
}

test("ProjectPage: 1 to 9 send no answer while the page covers the cursor's waiting row", (t) =>
  withApp(t.signal, undefined, async ({ container, calls }) => {
    await press(headOf(container, "api"));
    await pressKey("b");
    assert.equal(cursorKey(container), "a", "the cursor rests on the waiting row");
    const sentBefore = calls.length;
    for (const digit of ["1", "2", "3", "4", "5", "6", "7", "8", "9"]) {
      await pressKey(digit);
    }
    assert.deepEqual(writesOf(calls.slice(sentBefore)), [], "no answer is sent");
    assert.ok(projectPage(container) !== null, "the page stays open");
  }));

test("ProjectPage: Escape sends no interrupt or take-back while the page covers the working row", (t) =>
  withApp(t.signal, undefined, async ({ container, calls }) => {
    // A cursor move shuts the page, so the cursor reaches the working row before the page opens.
    await pressKey("j");
    await press(headOf(container, "api"));
    await pressKey("B");
    assert.equal(cursorKey(container), "b", "the cursor rests on the working row");
    assert.ok(projectPage(container) !== null, "the page is drawn over the working row");
    const sentBefore = calls.length;
    await pressKey("Escape");
    assert.deepEqual(writesOf(calls.slice(sentBefore)), [], "no interrupt or take-back is sent");
    assert.ok(projectPage(container) !== null, "the page stays open");
  }));

test("ProjectPage: Escape tucks a key-popped inbox over the page and sends nothing", (t) =>
  withApp(t.signal, undefined, async ({ container, calls }) => {
    await press(headOf(container, "api"));
    await pressKey("b");
    assert.equal(paneState(container), "flyout", "the inbox is out over the page");
    const sentBefore = calls.length;
    await pressKey("Escape");
    assert.equal(paneState(container), "rail", "Escape tucks the inbox back");
    assert.deepEqual(writesOf(calls.slice(sentBefore)), [], "no interrupt or take-back is sent");
  }));

test("ProjectPage: A, x and e send nothing while the page covers the cursor row over the rail", (t) =>
  withApp(t.signal, undefined, async ({ container, calls }) => {
    await press(headOf(container, "api"));
    assert.equal(paneState(container), "rail", "the inbox rests as the rail");
    assert.equal(cursorKey(container), "a", "the cursor rests on the waiting row");
    const sentBefore = calls.length;
    await pressKey("A");
    await pressKey("x");
    await pressKey("e");
    assert.deepEqual(writesOf(calls.slice(sentBefore)), [], "no archive or mark-seen is sent");
  }));

test("ProjectPage: with a conversation's own page open and the inbox at rest, A still acts on that row", (t) =>
  withApp(t.signal, undefined, async ({ container, calls }) => {
    await press(one(container, '.row[data-key="a"]'));
    assert.equal(paneState(container), "rail", "the inbox rests as the rail beside the conversation");
    // The click puts the focus in the row's reply box, where A is typed; the key goes to the page once it is left.
    await act(() => {
      (document.activeElement as HTMLElement | null)?.blur();
    });
    const sentBefore = calls.length;
    await pressKey("A");
    assert.ok(writesOf(calls.slice(sentBefore)).length > 0, "A sends the archive of the row on screen");
  }));

test("ProjectPage: with the inbox brought out over the page by b, x and A act on the cursor row", (t) =>
  withApp(t.signal, undefined, async ({ container, calls }) => {
    await press(headOf(container, "api"));
    await pressKey("b");
    assert.equal(paneState(container), "flyout", "the inbox is out over the page");
    const sentBefore = calls.length;
    await pressKey("x");
    assert.ok(writesOf(calls.slice(sentBefore)).length > 0, "x sends the mark-seen");
    const afterX = calls.length;
    await pressKey("A");
    assert.ok(writesOf(calls.slice(afterX)).length > 0, "A sends the archive");
  }));

test("ProjectPage: with the inbox pinned beside the page by B, x and A act on the cursor row", (t) =>
  withApp(t.signal, undefined, async ({ container, calls }) => {
    await press(headOf(container, "api"));
    await pressKey("B");
    assert.equal(paneState(container), "pinned", "the inbox is pinned beside the page");
    const sentBefore = calls.length;
    await pressKey("x");
    assert.ok(writesOf(calls.slice(sentBefore)).length > 0, "x sends the mark-seen");
    const afterX = calls.length;
    await pressKey("A");
    assert.ok(writesOf(calls.slice(afterX)).length > 0, "A sends the archive");
  }));

test("ProjectPage: under 760px, A, x and e send nothing while the page draws alone", (t) =>
  withApp(t.signal, undefined, async ({ container, calls }) => {
    await narrowWindow(700);
    await press(headOf(container, "api"));
    assert.ok(projectPage(container) !== null, "the page is drawn");
    assert.equal(listHidden(container), true, "the list is hidden");
    assert.equal(cursorKey(container), "a", "the cursor rests on the waiting row");
    const sentBefore = calls.length;
    await pressKey("A");
    await pressKey("x");
    await pressKey("e");
    assert.deepEqual(writesOf(calls.slice(sentBefore)), [], "no archive or mark-seen is sent");
  }));
