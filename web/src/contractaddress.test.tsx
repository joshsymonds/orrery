import assert from "node:assert/strict";
import { test } from "node:test";

import { create, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { contracts as contractList } from "./fixtures/contracts";
import { tideProposed } from "./fixtures/contractrecords";
import { type Contract, ContractsGetResponseSchema } from "./gen/orrery/v1/contracts_pb";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { parseAddress, type Address } from "./router";
import type { Row } from "./rows";
import { type SeenClock, type SeenWindow } from "./seen";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries the token ContractAddress: an address that names a contract draws the contract's conversation
// in the middle and its record in the panel, or as the page at a phone's width, and an epic's Contract door draws the
// record too. Each test mounts App at a window width of its own, since the layout reads the width when App mounts.

const now = 1000;
const contractId = "tidepool/tide-alerts";
const conversation = "sess-tide-16";
// recordTitle is the title of the newest version's body, which the record draws as its heading.
const recordTitle = tideProposed.versions.at(-1)?.contract?.title ?? "";
const noLive = "Its conversation has ended: you can accept it or set it aside, but not change it.";

const contractAddress: Address = { order: "attention", entry: { kind: "contract", id: contractId } };

// probe records the conversations the page asked to follow, by session id, and the sessions it marked seen.
interface Probe {
  followed: string[];
  seen: string[];
}

// seenPage is the page the seen watch reads. Its dwell is the timer the test fires: a rest arms it, and a fire runs it
// while input has come, so the test decides when a row counts as seen.
interface SeenPage {
  clock: SeenClock;
  window: SeenWindow;
  armed(): boolean;
  input(): void;
  fire(): void;
}

function seenPage(): SeenPage {
  let timer: (() => void) | undefined;
  const listeners = new Map<string, Set<() => void>>();
  const listen = {
    addEventListener(type: string, fn: () => void) {
      const set = listeners.get(type) ?? new Set<() => void>();
      set.add(fn);
      listeners.set(type, set);
    },
    removeEventListener(type: string, fn: () => void) {
      listeners.get(type)?.delete(fn);
    },
  };
  return {
    clock: {
      now: () => 0,
      setTimeout(fn: () => void): unknown {
        timer = fn;
        return fn;
      },
      clearTimeout() {
        timer = undefined;
      },
    },
    window: { document: { visibilityState: "visible", hasFocus: () => true, ...listen }, ...listen },
    armed: () => timer !== undefined,
    input() {
      for (const fn of [...(listeners.get("keydown") ?? [])]) fn();
    },
    fire() {
      const run = timer;
      timer = undefined;
      run?.();
    },
  };
}

// fakeFetch serves the contract record as the Contracts.Get answer, holds each conversation stream open, and answers
// every other unary call with an empty message.
async function fakeFetch(probe: Probe, input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  const path = new URL(request.url).pathname;
  if (path.endsWith("/WatchConversation")) {
    const body = JSON.parse((await request.text()).slice(5)) as { id?: string; pane?: string };
    probe.followed.push(body.id ?? body.pane ?? "");
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  if (path === "/orrery.v1.Control/Seen") {
    const body = JSON.parse(await request.text()) as { id?: string };
    probe.seen.push(body.id ?? "");
  }
  const answer = path === "/orrery.v1.Contracts/Get" ? toJson(ContractsGetResponseSchema, tideProposed) : {};
  return new Response(JSON.stringify(answer), { status: 200, headers: { "content-type": "application/json" } });
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    id: init.key,
    kind: "session",
    label: init.key,
    project: "/work/app",
    host: "",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// conversationRow is the session row of the conversation that proposed the contract.
function conversationRow(init: Partial<Row> = {}): Row {
  return row({
    key: `session:${conversation}`,
    id: conversation,
    label: "tide work",
    archiveId: conversation,
    archiveKind: "session",
    ...init,
  });
}

// waitingRow is another session's row, which waits for an answer and so sorts first in the attention view, where the
// cursor starts.
function waitingRow(): Row {
  return row({
    key: "session:sess-other",
    id: "sess-other",
    label: "other work",
    state: "waiting",
    archiveId: "sess-other",
    archiveKind: "session",
  });
}

// archivedRow is an archived session's row, the only row the archived view shows, where the cursor starts.
function archivedRow(): Row {
  return row({
    key: "session:sess-old",
    id: "sess-old",
    label: "old work",
    archived: true,
    archiveId: "sess-old",
    archiveKind: "session",
  });
}

// contracts is the Contracts.List answer the App is given: the tide-alerts contract, proposed by the conversation.
const contracts: Contract[] = contractList.contracts;

// epicsOf is the epics view of one epic, the contract's own, with its detail.
function epicsOf(): EpicsView {
  const epic: Epic = create(EpicSchema, {
    id: contractId,
    repository: "/home/owner/code/tidepool",
    slug: "tide-alerts",
    live: true,
    steering: conversation,
  });
  const detail: EpicsGetResponse = create(EpicsGetResponseSchema, { epic, summary: "Ship the alerts" });
  return epicsView([epic], new Map([[contractId, detail]]));
}

interface Mount {
  container: HTMLElement;
  probe: Probe;
}

// withApp mounts App at width over the rows, the epics and the address, and runs check with the mount. App is unmounted
// and the DOM removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  width: number,
  setup: { rows: Row[]; epics?: EpicsView; address: Address; seen?: SeenPage },
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  const container = document.createElement("div");
  document.body.append(container);
  const probe: Probe = { followed: [], seen: [] };
  const clients = createClients(
    createTransport({ baseUrl: "https://orrery.test", fetch: (input, init) => fakeFetch(probe, input, init) }),
  );
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(
        <App
          rows={setup.rows}
          error=""
          now={now}
          clients={clients}
          epics={setup.epics}
          contracts={contracts}
          address={setup.address}
          seenClock={setup.seen?.clock}
          seenWindow={setup.seen?.window}
        />,
        container,
      );
    });
    await check({ container, probe });
  } finally {
    await teardown();
  }
}

// settle waits until check holds, letting Preact draw what the page's requests and effects changed in between.
function settle(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// readerShown is whether the cursor's reader section is drawn.
function readerShown(container: HTMLElement): boolean {
  return container.querySelector('section[aria-label="reader"]:not([hidden])') !== null;
}

// cursorKeyOf is the key of the inbox row the cursor rests on, or "" when no row is marked.
function cursorKeyOf(container: HTMLElement): string {
  return container.querySelector('[data-row][aria-current="true"]')?.getAttribute("data-key") ?? "";
}

// panelOf is the panel labelled as given, or null when none is drawn.
function panelOf(container: HTMLElement, label: string): HTMLElement | null {
  return container.querySelector<HTMLElement>(`aside.rpanel[aria-label="${label}"]`);
}

// recordDrawn is whether a contract record is drawn in the container, or in the panel labelled as given.
function recordDrawn(container: HTMLElement, label?: string): boolean {
  const scope = label === undefined ? container : panelOf(container, label);
  return Boolean(scope?.querySelector(".contract-record"));
}

// shownSide is the panes' data-side: "panel" while a panel lies beside the conversation, "rail" otherwise.
function shownSide(container: HTMLElement): string | null {
  return container.querySelector(".panes")?.getAttribute("data-side") ?? null;
}

async function click(element: HTMLElement): Promise<void> {
  await act(() => {
    element.click();
  });
}

// titled is whether the panel labelled as given draws the record's title.
function titled(container: HTMLElement, label: string): boolean {
  return panelOf(container, label)?.textContent?.includes(recordTitle) === true;
}

test("ContractAddress: the address draws the contract's conversation in the middle and the record in the panel", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async ({ container, probe }) => {
    await settle(t.signal, () => probe.followed.includes(conversation), "the conversation is followed");
    assert.equal(readerShown(container), true, "the contract's conversation is drawn in the middle");
    await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
    await settle(t.signal, () => titled(container, "tide-alerts contract"), "the record's title is drawn");
  }));

test("ContractAddress: at the desk an open contract sets the panes to the panel side, and closing it returns the rail", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
    assert.equal(shownSide(container), "panel", "the panel lies in its own column beside the conversation");
    const close = panelOf(container, "tide-alerts contract")?.querySelector<HTMLButtonElement>(
      'button[aria-label="Close the panel"]',
    );
    assert.ok(close !== null && close !== undefined, "the panel draws its Close control");
    await click(close);
    await settle(t.signal, () => shownSide(container) === "rail", "the panes return to the rail side");
    assert.equal(recordDrawn(container, "tide-alerts contract"), false, "the panel is closed");
  }));

test("ContractAddress: the shown address names the contract, and parses back to the address it was opened with", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
    const link = panelOf(container, "tide-alerts contract")?.querySelector<HTMLAnchorElement>(
      'a[aria-label="Open in a new window"]',
    );
    assert.ok(link !== null && link !== undefined, "the panel links the address it shows");
    assert.deepEqual(parseAddress(link.getAttribute("href") ?? ""), contractAddress, "the shown address round-trips");
  }));

test("ContractAddress: an epic's Contract door draws the contract's record in the epic panel", (t) =>
  withApp(
    t.signal,
    1600,
    {
      rows: [conversationRow()],
      epics: epicsOf(),
      address: { order: "attention", entry: { kind: "epic", id: contractId, page: { kind: "contract" } } },
    },
    async ({ container }) => {
      await settle(t.signal, () => recordDrawn(container, "epic"), "the door draws the record");
      await settle(t.signal, () => titled(container, "epic"), "the record's title is drawn");
    },
  ));

// noLiveCheck opens the contract over the rows. The middle draws no reader and the no-conversation section, and the ended
// sentence is drawn once on the page, by the record, which cannot be changed.
function noLiveCheck(signal: AbortSignal, rows: Row[]): Promise<void> {
  return withApp(signal, 1600, { rows, address: contractAddress }, async ({ container }) => {
    await settle(
      signal,
      () => panelOf(container, "tide-alerts contract")?.textContent?.includes(noLive) === true,
      "the record says it cannot be changed",
    );
    assert.ok(
      container.querySelector('section.read[aria-label="no conversation"]') !== null,
      "the middle draws the no-conversation section",
    );
    assert.equal(readerShown(container), false, "no reader is drawn");
    const occurrences = (container.textContent ?? "").split(noLive).length - 1;
    assert.equal(occurrences, 1, "the ended sentence is drawn once on the page");
  });
}

test("ContractAddress: with no conversation row the middle draws no reader, and the ended sentence is drawn once", (t) =>
  noLiveCheck(t.signal, []));

test("ContractAddress: with a stopped conversation the middle draws no reader, and the ended sentence is drawn once", (t) =>
  noLiveCheck(t.signal, [conversationRow({ state: "stopped" })]));

test("ContractAddress: with a failed conversation the middle draws no reader, and the ended sentence is drawn once", (t) =>
  noLiveCheck(t.signal, [conversationRow({ state: "failed" })]));

test("ContractAddress: opening a contract moves the cursor onto its conversation's row, when the list holds it", (t) =>
  withApp(
    t.signal,
    1600,
    { rows: [waitingRow(), conversationRow()], address: contractAddress },
    async ({ container }) => {
      await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
      assert.equal(cursorKeyOf(container), `session:${conversation}`, "the cursor rests on the conversation's row");
    },
  ));

test("ContractAddress: at a phone's width the contract is the page in the conversation's place, with no reader", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    assert.equal(panelOf(container, "tide-alerts contract"), null, "no panel is drawn beside the page");
    assert.equal(readerShown(container), false, "no reader is drawn");
    await settle(t.signal, () => container.textContent?.includes(recordTitle) === true, "the record's title is drawn");
  }));

test("ContractAddress: the seen watch rests on the conversation the middle draws, when the list does not show it", (t) => {
  const seen = seenPage();
  return withApp(
    t.signal,
    1600,
    {
      rows: [archivedRow(), conversationRow({ changed: 10, seen: 0, unseen: true })],
      address: { order: "archived", entry: { kind: "contract", id: contractId } },
      seen,
    },
    async ({ container, probe }) => {
      await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
      await settle(t.signal, () => seen.armed(), "the seen watch rests on a row");
      seen.input();
      seen.fire();
      await settle(t.signal, () => probe.seen.includes(conversation), "the conversation is marked seen");
    },
  );
});
