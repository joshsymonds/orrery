import assert from "node:assert/strict";
import { test } from "node:test";

import { toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { contracts as contractList } from "./fixtures/contracts";
import { tideProposed } from "./fixtures/contractrecords";
import { type Contract, ContractsGetResponseSchema } from "./gen/orrery/v1/contracts_pb";
import { type Address } from "./router";
import type { Row } from "./rows";
import { assertSame, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries one of the tokens: ContractMine for a row's control and the dock's About, ContractLines for
// the band's and the transcript's contract lines, and ContractAddress for the phone's sheet. Each test mounts App at a
// window width of its own, since the layout reads the width when App mounts.

const now = 1000;
const contractId = "tidepool/tide-alerts";
const conversation = "sess-tide-16";
const firstRow = "F1";
const firstRowText = "The office was alerted about a table that had not changed.";

const contractAddress: Address = { order: "attention", entry: { kind: "contract", id: contractId } };

// Probe records the texts the owner sent, in order.
interface Probe {
  said: string[];
}

// envelope frames one message of a streamed answer: a flag byte, a 4-byte length, and the JSON payload.
function envelope(message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// fakeFetch answers Contracts.Get with the tide-alerts record, records each Say, and answers every other unary call
// with an empty message. WatchConversation holds its stream open, and sends the items as one reset delta when a test
// gives them.
async function fakeFetch(
  probe: Probe,
  items: object[],
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  const request = new Request(input, init);
  const path = new URL(request.url).pathname;
  if (path.endsWith("/WatchConversation")) {
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        if (items.length > 0) {
          controller.enqueue(envelope({ transcript: "t1", seq: "1", reset: true, items, state: {} }));
        }
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  }
  if (path === "/orrery.v1.Control/Say") {
    const body = JSON.parse(await request.text()) as { text?: string };
    probe.said.push(body.text ?? "");
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
function conversationRow(): Row {
  return row({
    key: `session:${conversation}`,
    id: conversation,
    label: "tide work",
    archiveId: conversation,
    archiveKind: "session",
  });
}

// contractItems are the tool call that wrote the contract, and its result, as the transcript holds them.
const contractItems = [
  { kind: "tool", time: "100", tool: "contract_propose", input: contractId, id: "c1", contract: contractId },
  { kind: "result", time: "100", id: "c1", text: "proposed", error: false },
];

// listed is the Contracts.List answer with the tide-alerts record's own entry, which has one decision open at severe
// care.
const listed: Contract[] = tideProposed.contract === undefined ? [] : [tideProposed.contract];

interface Mount {
  container: HTMLElement;
  probe: Probe;
}

// withApp mounts App at width over the rows and the address, with the contracts the list holds and the conversation's
// items, and runs check with the mount. App is unmounted and the DOM removed when the test ends.
async function withApp(
  signal: AbortSignal,
  width: number,
  setup: { rows: Row[]; address?: Address; contracts?: readonly Contract[]; items?: object[] },
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  const container = document.createElement("div");
  document.body.append(container);
  const probe: Probe = { said: [] };
  const items = setup.items ?? [];
  const contracts = setup.contracts ?? contractList.contracts;
  const clients = createClients(
    createTransport({
      baseUrl: "https://orrery.test",
      fetch: (input, init) => fakeFetch(probe, items, input, init),
    }),
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
          contracts={contracts}
          address={setup.address}
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

// panelOf is the panel labelled as given, or null when none is drawn.
function panelOf(container: HTMLElement, label: string): HTMLElement | null {
  return container.querySelector<HTMLElement>(`aside.rpanel[aria-label="${label}"]`);
}

// recordDrawn is whether a contract record is drawn in the container, or in the panel labelled as given.
function recordDrawn(container: HTMLElement, label?: string): boolean {
  const scope = label === undefined ? container : panelOf(container, label);
  return Boolean(scope?.querySelector(".contract-record"));
}

// recordTitle is the title of the newest version's body, which the record draws once Contracts.Get has answered.
const recordTitle = tideProposed.versions.at(-1)?.contract?.title ?? "";

// titledRecord is whether the record is drawn with its title, in the container or in the panel labelled as given: the
// record draws its loading line until its answer comes, and its rows and controls only after it.
function titledRecord(container: HTMLElement, label?: string): boolean {
  const scope = label === undefined ? container : panelOf(container, label);
  return scope?.querySelector(".contract-record")?.textContent?.includes(recordTitle) === true;
}

// talkControls are the row controls the record draws, each labelled Talk about this row.
function talkControls(container: HTMLElement): HTMLButtonElement[] {
  return [...container.querySelectorAll<HTMLButtonElement>('button[aria-label="Talk about this row"]')];
}

// buttonNamed is the button of the scope whose label is given, or null when none is drawn.
function buttonNamed(scope: ParentNode, label: string): HTMLButtonElement | null {
  return scope.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
}

// aboutLine is the text of the dock's About line on the shown row, or null when no About is drawn.
function aboutLine(container: HTMLElement): string | null {
  return container.querySelector(`section[aria-label="reader"] .reply:not([hidden]) .quote`)?.textContent ?? null;
}

// shownBox is the shown row's send box in the middle column, or null when none is drawn.
function shownBox(container: HTMLElement): HTMLTextAreaElement | null {
  return container.querySelector<HTMLTextAreaElement>('section[aria-label="reader"] .reply:not([hidden]) textarea');
}

async function click(element: HTMLElement): Promise<void> {
  await act(() => {
    element.click();
  });
}

// typeDraft types text into the shown send box without sending it, and returns the box.
async function typeDraft(container: HTMLElement, text: string): Promise<HTMLTextAreaElement> {
  const box = shownBox(container);
  if (box === null) {
    throw new Error("the middle column draws no send box");
  }
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
  return box;
}

// typeAndSend types text into the shown send box and presses Enter, which sends it.
async function typeAndSend(container: HTMLElement, text: string): Promise<void> {
  const box = await typeDraft(container, text);
  await act(() => {
    box.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  });
}

// bandContract is the band's contract line for the contract, or null when none is drawn.
function bandContract(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(`[data-contract="${contractId}"]`);
}

test("ContractMine: a row's control sets the dock's About, and the next send leads with it", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async ({ container, probe }) => {
    await settle(t.signal, () => titledRecord(container, "tide-alerts contract"), "the record is drawn in the panel");
    const control = talkControls(panelOf(container, "tide-alerts contract") ?? container)[0];
    assert.ok(control !== undefined, "the record draws a row's control");
    await click(control);
    await settle(t.signal, () => aboutLine(container) !== null, "the dock draws its About");
    assert.ok(aboutLine(container)?.includes(firstRow), "the About names the row");
    assert.ok(aboutLine(container)?.includes(firstRowText), "the About says the row's text");

    await typeAndSend(container, "Why?");
    await settle(t.signal, () => probe.said.length === 1, "the message is sent");
    assert.equal(probe.said[0], `About ${firstRow}: ${firstRowText}\n\nWhy?`, "the sent text leads with the About");
    await settle(t.signal, () => aboutLine(container) === null, "the send clears the About");
  }));

test("ContractMine: opening another contract clears the About the first one set", (t) => {
  const other = "tidepool/tide-other";
  const both: Contract[] = listed.flatMap((first) => [first, { ...first, id: other }]);
  return withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress, contracts: both }, async ({ container }) => {
    await settle(t.signal, () => titledRecord(container, "tide-alerts contract"), "the record is drawn in the panel");
    const control = talkControls(panelOf(container, "tide-alerts contract") ?? container)[0];
    assert.ok(control !== undefined, "the record draws a row's control");
    await click(control);
    await settle(t.signal, () => aboutLine(container) !== null, "the dock draws its About");
    const line = container.querySelector<HTMLElement>(`[data-contract="${other}"]`);
    assert.ok(line !== null, "the band draws the other contract's line");
    await click(line);
    await settle(t.signal, () => recordDrawn(container, "tide-other contract"), "the other contract opens");
    assert.equal(aboutLine(container), null, "the first contract's About is cleared");
  });
});

test("ContractMine: the row the dock's About names draws its control lit, and no other row does", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => titledRecord(container, "tide-alerts contract"), "the record is drawn in the panel");
    const panel = panelOf(container, "tide-alerts contract") ?? container;
    assert.equal(panel.querySelectorAll(".say.on").length, 0, "no row's control is lit before an About is set");
    const control = talkControls(panel)[0];
    assert.ok(control !== undefined, "the record draws a row's control");
    await click(control);
    await settle(t.signal, () => aboutLine(container) !== null, "the dock draws its About");
    const lit = [...panel.querySelectorAll(".say.on")];
    assert.equal(lit.length, 1, "one row's control is lit");
    assert.equal(lit[0].closest("[data-row]")?.getAttribute("data-row"), firstRow, "the lit control is the About's row");
  }));

test("ContractDoc: the contract page's title row carries the contract's state pill", async (t) => {
  for (const [state, pill] of [
    ["proposed", "proposed · waits for you"],
    ["accepted", "accepted"],
    ["set_aside", "set aside"],
  ] as const) {
    const shown: Contract[] = listed.map((entry) => ({ ...entry, state }));
    await withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress, contracts: shown }, async ({ container }) => {
      await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
      const title = panelOf(container, "tide-alerts contract")?.querySelector(".state");
      assert.equal(title?.textContent, pill, `a ${state} contract's title row says its state`);
    });
  }
});

test("ContractMine: Not about this clears the About, and the next send is the draft alone", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async ({ container, probe }) => {
    await settle(t.signal, () => titledRecord(container, "tide-alerts contract"), "the record is drawn in the panel");
    const control = talkControls(panelOf(container, "tide-alerts contract") ?? container)[0];
    assert.ok(control !== undefined, "the record draws a row's control");
    await click(control);
    await settle(t.signal, () => aboutLine(container) !== null, "the dock draws its About");
    const clear = buttonNamed(container, "Not about this");
    assert.ok(clear !== null, "the About line has its Not about this control");
    await click(clear);
    await settle(t.signal, () => aboutLine(container) === null, "Not about this clears the About");

    await typeAndSend(container, "Plain");
    await settle(t.signal, () => probe.said.length === 1, "the message is sent");
    assert.equal(probe.said[0], "Plain", "the sent text is the draft alone");
  }));

test("ContractMine: with no live conversation the record draws no row control", (t) =>
  withApp(t.signal, 1600, { rows: [], address: contractAddress }, async ({ container }) => {
    await settle(
      t.signal,
      () => container.textContent?.includes("Its conversation has ended") === true,
      "the record says its conversation has ended",
    );
    assert.equal(talkControls(container).length, 0, "no row control is drawn");
    assert.equal(recordDrawn(container, "tide-alerts contract"), true, "the record is still drawn");
  }));

test("ContractLines: the band's contract line says what is left, and a press opens the contract", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], contracts: listed }, async ({ container }) => {
    await settle(t.signal, () => bandContract(container) !== null, "the band draws the contract's line");
    const line = bandContract(container);
    if (line === null) {
      throw new Error("the band draws no contract line");
    }
    assert.ok(line.textContent?.includes("tide-alerts contract"), "the line names the contract");
    assert.ok(
      line.textContent?.includes("1 decision is yours; read “can’t be undone” before Accept"),
      "the line says what is left",
    );
    assert.ok(line.textContent?.includes("Read and decide"), "the line offers the way in");
    await click(line);
    await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the press opens the record");
  }));

test("ContractLines: the band's contract line says it is open on the right while the contract is open", (t) =>
  withApp(
    t.signal,
    1600,
    { rows: [conversationRow()], address: contractAddress, contracts: listed },
    async ({ container }) => {
      await settle(
        t.signal,
        () => bandContract(container)?.classList.contains("here") === true,
        "the band's line says the contract is open",
      );
      assert.ok(bandContract(container)?.textContent?.includes("open on the right"), "the line says open on the right");
    },
  ));

test("ContractLines: the transcript's contract line is drawn in the middle reader, and a press opens it", (t) =>
  withApp(
    t.signal,
    1600,
    { rows: [conversationRow()], contracts: listed, items: contractItems },
    async ({ container }) => {
      const transcriptLine = (): HTMLButtonElement | undefined =>
        [...container.querySelectorAll<HTMLButtonElement>('section[aria-label="reader"] button.block')].find(
          (button) => button.querySelector(".lbl")?.textContent === "Contract",
        );
      await settle(t.signal, () => transcriptLine() !== undefined, "the transcript draws the contract line");
      const line = transcriptLine();
      if (line === undefined) {
        throw new Error("the transcript draws no contract line");
      }
      await click(line);
      await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the press opens the record");
    },
  ));

// talkButton is the record's Talk about it control, or undefined when none is drawn.
function talkButton(container: HTMLElement): HTMLButtonElement | undefined {
  return [...container.querySelectorAll<HTMLButtonElement>(".contract-record button")].find(
    (button) => button.textContent === "Talk about it",
  );
}

// loadedRecord is whether the record has its answer in the container, which is when it draws its command bar.
function loadedRecord(container: HTMLElement): boolean {
  return container.querySelector(".contract-record .cmdbar") !== null;
}

// talkWhen mounts the phone's record of the contract in the given state, and runs check once the record is loaded.
function talkWhen(signal: AbortSignal, state: string, check: (container: HTMLElement) => void): Promise<void> {
  const contracts = listed.map((contract) => ({ ...contract, state }));
  return withApp(signal, 400, { rows: [conversationRow()], address: contractAddress, contracts }, async ({ container }) => {
    await settle(signal, () => loadedRecord(container), "the record is loaded");
    check(container);
  });
}

test("ContractAddress: under 760px Talk about it is not offered on an accepted contract", (t) =>
  talkWhen(t.signal, "accepted", (container) => {
    assert.equal(talkButton(container) !== undefined, false, "the record draws no Talk about it");
  }));

test("ContractAddress: under 760px Talk about it is not offered on a set-aside contract", (t) =>
  talkWhen(t.signal, "set_aside", (container) => {
    assert.equal(talkButton(container) !== undefined, false, "the record draws no Talk about it");
  }));

test("ContractAddress: under 760px Talk about it raises the conversation in the sheet, over the record", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => titledRecord(container), "the record is drawn");
    const talk = talkButton(container);
    assert.ok(talk !== undefined, "the record draws Talk about it");
    await click(talk);
    await settle(
      t.signal,
      () => container.querySelector('section.sheet section[aria-label="reader"]') !== null,
      "the sheet draws the conversation's reader",
    );
    assert.ok(shownBox(container) !== null, "the sheet draws the conversation's send box");
    assert.equal(recordDrawn(container), true, "the record stays drawn under the sheet");
  }));

test("ContractAddress: under 760px a row's control raises the conversation in the sheet, with its About", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => titledRecord(container), "the record is drawn");
    const control = talkControls(container)[0];
    assert.ok(control !== undefined, "the record draws a row's control");
    await click(control);
    await settle(
      t.signal,
      () => container.querySelector('section.sheet section[aria-label="reader"]') !== null,
      "the sheet draws the conversation's reader",
    );
    await settle(t.signal, () => aboutLine(container)?.includes(firstRow) === true, "the sheet's dock draws the About");
  }));

// sheetOf is the phone's raised sheet, or null when none is raised.
function sheetOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>("section.sheet");
}

// raiseSheet presses the record's Talk about it, which raises the conversation in the sheet, and waits for its send box.
async function raiseSheet(signal: AbortSignal, container: HTMLElement): Promise<void> {
  await settle(signal, () => talkButton(container) !== undefined, "the record draws Talk about it");
  const talk = talkButton(container);
  assert.ok(talk !== undefined, "the record draws Talk about it");
  await click(talk);
  await settle(signal, () => shownBox(container) !== null, "the sheet draws the conversation's send box");
}

test("ContractAddress: under 760px a draft typed in the conversation's dock survives raising the sheet and dismissing it", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => titledRecord(container), "the record is drawn");
    await raiseSheet(t.signal, container);
    const box = await typeDraft(container, "Half a thought");
    const raised = sheetOf(container);
    assert.ok(raised !== null, "the sheet is raised");
    await act(() => {
      raised.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    });
    await settle(t.signal, () => sheetOf(container) === null, "Escape dismisses the sheet");

    await raiseSheet(t.signal, container);
    assertSame(shownBox(container), box, "the sheet draws the same send box");
    assert.equal(shownBox(container)?.value, "Half a thought", "the draft is still in the box");
  }));

test("ContractAddress: under 760px a draft typed in the sheet survives its dismissal by a press on the scrim", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => titledRecord(container), "the record is drawn");
    await raiseSheet(t.signal, container);
    const box = await typeDraft(container, "Sheet draft");
    const scrim = container.querySelector(".sheet-scrim");
    assert.ok(scrim !== null, "the sheet is raised over a scrim");
    await act(() => {
      scrim.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    });
    await settle(t.signal, () => sheetOf(container) === null, "a press on the scrim dismisses the sheet");

    await raiseSheet(t.signal, container);
    assertSame(shownBox(container), box, "the sheet draws the same send box");
    assert.equal(shownBox(container)?.value, "Sheet draft", "the draft is still in the box");
  }));
