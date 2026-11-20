import assert from "node:assert/strict";
import { test } from "node:test";

import { create, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { contracts as contractList } from "./fixtures/contracts";
import { tideProposed } from "./fixtures/contractrecords";
import { type Contract, ContractsGetResponseSchema } from "./gen/orrery/v1/contracts_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries the token ContractAddress: a press on the inbox tree's contract row, or the keyboard open of
// that row, opens the contract's panel as the rail's contract tile does, and at a phone's width opens the contract page.

const now = 1000;
const contractId = "tidepool/tide-alerts";
const conversation = "sess-tide-16";
const treeKey = `contract:${contractId}`;

const steererAddress: Address = { view: "inbox", entry: { kind: "session", id: conversation } };
const contractAddress: Address = { view: "inbox", entry: { kind: "contract", id: contractId } };
const listAddress: Address = { view: "inbox", entry: null };

// fakeFetch serves the contract record as the Contracts.Get answer, holds each conversation stream open, and answers
// every other unary call with an empty message.
async function fakeFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  const path = new URL(request.url).pathname;
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  const answer = path === "/orrery.v1.Contracts/Get" ? toJson(ContractsGetResponseSchema, tideProposed) : {};
  return new Response(JSON.stringify(answer), { status: 200, headers: { "content-type": "application/json" } });
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    id: init.key,
    kind: "session",
    label: init.key,
    project: "/work/app",
    host: "",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    tool: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// conversationRow is the session row of the conversation that proposed the contract. Its contract hangs under it in the
// inbox tree.
function conversationRow(): Row {
  return row({
    key: `session:${conversation}`,
    id: conversation,
    label: "tide work",
    archiveId: conversation,
    archiveKind: "session",
  });
}

// contracts is the Contracts.List answer the App is given: the tide-alerts contract, proposed by the conversation.
const contracts: Contract[] = contractList.contracts;

interface Mount {
  container: HTMLElement;
  reported: { address: Address; mode: string }[];
}

// withApp mounts App at width over the rows and the address, and runs check with the mount. App is unmounted and the DOM
// removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  width: number,
  setup: { rows: Row[]; address: Address },
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  const container = document.createElement("div");
  document.body.append(container);
  const reported: Mount["reported"] = [];
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch }));
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
          now={now}
          clients={clients}
          contracts={contracts}
          address={setup.address}
          onAddress={(address, mode) => reported.push({ address, mode })}
        />,
        container,
      );
    });
    await check({ container, reported });
  } finally {
    await teardown();
  }
}

// settle waits until check holds, letting Preact draw what the page's requests and effects changed in between.
function settle(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// treeRow is the inbox tree's row of the contract, or null when none is drawn.
function treeRow(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(`[data-key="${treeKey}"]`);
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

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.click();
  });
}

// pressKey sends a keydown to the document, where the keyboard reads its keys, and lets Preact draw what it changed.
async function pressKey(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
  });
}

// lastReport is the address App reported last, with how it was reported.
function lastReport(mount: Mount): { address: Address; mode: string } {
  const report = mount.reported.at(-1);
  assert.ok(report !== undefined, "App reported no address");
  return report;
}

test("ContractAddress: a press on the inbox tree's contract row opens the contract's panel, with its address pushed", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: steererAddress }, async (mount) => {
    const { container } = mount;
    await settle(t.signal, () => treeRow(container) !== null, "the tree draws the contract's row");
    assert.ok(panelOf(container, "tide-alerts contract") === null, "no panel before the press");
    const entry = treeRow(container);
    assert.ok(entry !== null, "the tree draws the contract's row");
    await press(entry);
    await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
    assert.deepEqual(lastReport(mount), { address: contractAddress, mode: "push" });
  }));

test("ContractAddress: the keyboard open on the inbox tree's contract row opens the contract's panel", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: steererAddress }, async ({ container }) => {
    await settle(t.signal, () => treeRow(container) !== null, "the tree draws the contract's row");
    assert.ok(treeRow(container)?.getAttribute("aria-current") !== "true", "the cursor starts on the conversation's row");
    await pressKey("j");
    await settle(
      t.signal,
      () => treeRow(container)?.getAttribute("aria-current") === "true",
      "the cursor moves onto the contract's row",
    );
    assert.ok(panelOf(container, "tide-alerts contract") === null, "no panel before the open");
    await pressKey("Enter");
    await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
  }));

test("ContractAddress: at a phone's width a press on the inbox tree's contract row opens the contract page, with no panel", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: listAddress }, async ({ container }) => {
    await settle(t.signal, () => treeRow(container) !== null, "the tree draws the contract's row");
    const entry = treeRow(container);
    assert.ok(entry !== null, "the tree draws the contract's row");
    await press(entry);
    await settle(t.signal, () => recordDrawn(container), "the record is drawn as the page");
    assert.ok(panelOf(container, "tide-alerts contract") === null, "no panel is drawn beside the page");
  }));
