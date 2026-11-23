import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { tideProposed } from "./fixtures/contractrecords";
import { type Contract, ContractSchema, ContractsGetResponseSchema } from "./gen/orrery/v1/contracts_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries the token ContractTile: a proposed contract of the middle conversation is a work link in its
// title row, named by its slug, and pressing it opens the contract panel, which its Close shuts. Each test mounts App at a
// window width of its own, since the layout reads the width when App mounts.

const now = 1000;
const contractId = "tidepool/tide-alerts";
const conversation = "sess-tide-16";
const contractName = "tide-alerts";

const steererAddress: Address = { view: "inbox", entry: { kind: "session", id: conversation } };
const contractAddress: Address = { view: "inbox", entry: { kind: "contract", id: contractId } };

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

interface Setup {
  rows: Row[];
  contracts: Contract[];
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
          now={now}
          clients={clients}
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

// workLinkNames are the names the title row's work links carry, in order.
function workLinkNames(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLElement>(".title-row .wlink .nm")].map((name) => name.textContent ?? "");
}

// contractLink is the work link of the contract, which the title row must draw.
function contractLink(container: HTMLElement): HTMLElement {
  const link = container.querySelector<HTMLElement>(`.title-row .wlink[title="Open ${contractName}"]`);
  assert.ok(link !== null, `no work link opens ${contractName}`);
  return link;
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

test("ContractTile: the proposed contract is a work link in the title row, named by its slug", (t) =>
  withApp(
    t.signal,
    {
      rows: [conversationRow()],
      contracts: [makeContract(contractId)],
      address: steererAddress,
      width: 1600,
    },
    async (container) => {
      await settle(t.signal, () => workLinkNames(container).length === 1, "the contract's work link is drawn");
      assert.deepEqual(workLinkNames(container), [contractName], "the title names the contract by its slug");
    },
  ));

test("ContractTile: under 760px the contract is the title row's one work link, and no more links are drawn", (t) =>
  withApp(
    t.signal,
    {
      rows: [conversationRow()],
      contracts: [makeContract(contractId)],
      address: steererAddress,
      width: 400,
    },
    async (container) => {
      await settle(t.signal, () => workLinkNames(container).length === 1, "the contract's work link is drawn");
      assert.equal(container.querySelector(".title-row .wlink-more"), null, "no +n control is drawn for one link");
    },
  ));

test("ContractTile: a contract another conversation proposed, or one accepted or set aside, has no work link", (t) =>
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
      await settle(t.signal, () => workLinkNames(container).length > 0, "the contract's work link is drawn");
      assert.deepEqual(workLinkNames(container), [contractName]);
    },
  ));

test("ContractTile: pressing the contract's work link opens its panel, with its address pushed", (t) =>
  withApp(
    t.signal,
    { rows: [conversationRow()], contracts: [makeContract(contractId)], address: steererAddress, width: 1600 },
    async (container, probe) => {
      await settle(t.signal, () => workLinkNames(container).length === 1, "the contract's work link is drawn");
      assert.equal(panelOf(container, "tide-alerts contract"), null, "no panel before the press");
      await press(contractLink(container));
      await settle(
        t.signal,
        () => panelOf(container, "tide-alerts contract")?.querySelector(".contract-record") != null,
        "the record is drawn in the panel",
      );
      assert.deepEqual(lastReport(probe), { address: contractAddress, mode: "push" });
    },
  ));

test("ContractTile: Close shuts the contract panel, with the steerer's address pushed", (t) =>
  withApp(
    t.signal,
    { rows: [conversationRow()], contracts: [makeContract(contractId)], address: contractAddress, width: 1600 },
    async (container, probe) => {
      await settle(
        t.signal,
        () => panelOf(container, "tide-alerts contract")?.querySelector(".contract-record") != null,
        "the record is drawn in the panel",
      );
      const close = panelOf(container, "tide-alerts contract")?.querySelector<HTMLElement>(
        '.panel-ctl button[aria-label="Close the panel"]',
      );
      assert.ok(close !== null && close !== undefined, "the contract panel has a Close button");
      await press(close);
      assert.equal(panelOf(container, "tide-alerts contract"), null, "the panel is closed");
      assert.deepEqual(lastReport(probe), { address: steererAddress, mode: "push" });
    },
  ));
