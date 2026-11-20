import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { tideProposed } from "./fixtures/contractrecords";
import { type Contract, ContractSchema, ContractsGetResponseSchema } from "./gen/orrery/v1/contracts_pb";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries the token ContractTile: a proposed contract of the middle conversation heads the rail and
// the phone's strip as a tile, and pressing it opens the contract panel, which its lit tile closes. Each test mounts
// App at a window width of its own, since the layout reads the width when App mounts.

const now = 1000;
const contractId = "tidepool/tide-alerts";
const conversation = "sess-tide-16";
const contractLabel = "tide-alerts: a contract waits for you";

const steererAddress: Address = { order: "attention", entry: { kind: "session", id: conversation } };
const contractAddress: Address = { order: "attention", entry: { kind: "contract", id: contractId } };

interface Probe {
  reported: { address: Address; mode: string }[];
}

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

// makeContract is a contract of the repository tidepool, proposed by the conversation unless init says otherwise.
function makeContract(id: string, init: MessageInitShape<typeof ContractSchema> = {}): Contract {
  return create(ContractSchema, {
    id,
    repository: "/home/owner/code/tidepool",
    title: "Tide alerts",
    care: "limited",
    state: "proposed",
    version: 1n,
    openDecisions: 0n,
    conversation,
    ...init,
  });
}

// epicsOf is the epics view of one epic, the contract's own, steered by the conversation.
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

interface Setup {
  rows: Row[];
  contracts: Contract[];
  epics?: EpicsView;
  address: Address;
  width: number;
}

// withApp mounts App over the setup at its width, and runs check with the container and the probe. App is unmounted and
// the DOM removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  setup: Setup,
  check: (container: HTMLElement, probe: Probe) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = setup.width;
  const container = document.createElement("div");
  document.body.append(container);
  const probe: Probe = { reported: [] };
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
          error=""
          now={now}
          clients={clients}
          epics={setup.epics}
          contracts={setup.contracts}
          address={setup.address}
          onAddress={(address, mode) => probe.reported.push({ address, mode })}
        />,
        container,
      );
    });
    await check(container, probe);
  } finally {
    await teardown();
  }
}

// settle waits until check holds, letting Preact draw what the page's requests and effects changed in between.
function settle(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// railTiles is each tile the right rail draws, in order.
function railTiles(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".rail .tile-w")];
}

// contractTiles is each contract tile the rail draws, in order.
function contractTiles(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".rail .rgroup.contract .tile-w")];
}

// litTile is the rail's one lit tile, or null when none is lit.
function litTile(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(".rail .tile-w.on");
}

// panelOf is the panel labelled as given, or null when none is drawn.
function panelOf(container: HTMLElement, label: string): HTMLElement | null {
  return container.querySelector<HTMLElement>(`aside.rpanel[aria-label="${label}"]`);
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// lastReport is the address App reported last, with how it was reported.
function lastReport(probe: Probe): { address: Address; mode: string } {
  const report = probe.reported.at(-1);
  assert.ok(report !== undefined, "App reported no address");
  return report;
}

test("ContractTile: the contract group heads the rail, ahead of the epic, its tile named by slug and title", (t) =>
  withApp(
    t.signal,
    {
      rows: [conversationRow()],
      contracts: [makeContract(contractId)],
      epics: epicsOf(),
      address: steererAddress,
      width: 1600,
    },
    async (container) => {
      await settle(t.signal, () => contractTiles(container).length === 1, "the contract tile is drawn");
      const groups = [...container.querySelectorAll<HTMLElement>("aside.rail .rgroup")];
      assert.deepEqual(
        groups.map((group) => group.className),
        ["rgroup contract", "rgroup epic"],
      );
      assert.equal(contractTiles(container)[0].getAttribute("aria-label"), contractLabel);
    },
  ));

test("ContractTile: the phone's strip heads with the contract group, drawn as a tile", (t) =>
  withApp(
    t.signal,
    {
      rows: [conversationRow()],
      contracts: [makeContract(contractId)],
      epics: epicsOf(),
      address: steererAddress,
      width: 400,
    },
    async (container) => {
      await settle(
        t.signal,
        () => container.querySelector("nav.tilestrip .rgroup.contract") !== null,
        "the strip draws the contract group",
      );
      const first = container.querySelector("nav.tilestrip .rgroup");
      assert.equal(first?.className, "rgroup contract");
      assert.equal(first?.querySelector(".tile-w")?.getAttribute("aria-label"), contractLabel);
    },
  ));

test("ContractTile: a contract another conversation proposed, or one accepted or set aside, has no tile", (t) =>
  withApp(
    t.signal,
    {
      rows: [conversationRow()],
      contracts: [
        makeContract(contractId),
        makeContract("tidepool/accepted", { state: "accepted" }),
        makeContract("tidepool/set-aside", { state: "set_aside" }),
        makeContract("tidepool/theirs", { conversation: "sess-other" }),
      ],
      address: steererAddress,
      width: 1600,
    },
    async (container) => {
      await settle(t.signal, () => contractTiles(container).length > 0, "the contract tile is drawn");
      assert.deepEqual(
        contractTiles(container).map((tile) => tile.getAttribute("aria-label")),
        [contractLabel],
      );
    },
  ));

test("ContractTile: pressing the contract tile opens its panel, with its address pushed, and lights the tile", (t) =>
  withApp(
    t.signal,
    { rows: [conversationRow()], contracts: [makeContract(contractId)], address: steererAddress, width: 1600 },
    async (container, probe) => {
      await settle(t.signal, () => contractTiles(container).length === 1, "the contract tile is drawn");
      assert.equal(panelOf(container, "tide-alerts contract"), null, "no panel before the press");
      await press(contractTiles(container)[0]);
      await settle(
        t.signal,
        () => panelOf(container, "tide-alerts contract")?.querySelector(".contract-record") != null,
        "the record is drawn in the panel",
      );
      assert.equal(litTile(container), contractTiles(container)[0], "the contract's tile is lit");
      assert.deepEqual(lastReport(probe), { address: contractAddress, mode: "push" });
    },
  ));

test("ContractTile: pressing the lit contract tile closes its panel, with the steerer's address pushed", (t) =>
  withApp(
    t.signal,
    { rows: [conversationRow()], contracts: [makeContract(contractId)], address: contractAddress, width: 1600 },
    async (container, probe) => {
      await settle(
        t.signal,
        () => panelOf(container, "tide-alerts contract")?.querySelector(".contract-record") != null,
        "the record is drawn in the panel",
      );
      assert.equal(litTile(container), contractTiles(container)[0], "the contract's tile is lit before the press");
      await press(contractTiles(container)[0]);
      assert.equal(panelOf(container, "tide-alerts contract"), null, "the panel is closed");
      assert.equal(litTile(container), null, "no tile is lit");
      assert.deepEqual(lastReport(probe), { address: steererAddress, mode: "push" });
    },
  ));
