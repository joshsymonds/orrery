import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
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
import type { Row } from "./rows";
import { type Address } from "./router";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries the token ContractPageLook: the contract page is a section.page, as the epic page is. At the
// desk it sits directly under the panel's .rcard, which gives it the card's padding; under 760px it sits in .epic-read,
// which takes the panes' gutter the epic page takes there. Its title row and its record are a section gap apart. Each test
// mounts App at a window width of its own, since the layout reads the width when App mounts.

const now = 1000;
const contractId = "tidepool/tide-alerts";
const conversation = "sess-tide-16";
const contractAddress: Address = { view: "inbox", entry: { kind: "contract", id: contractId } };
const epicContractAddress: Address = {
  view: "inbox",
  entry: { kind: "epic", id: contractId, page: { kind: "contract" } },
};

// css is style.css with its comments cut, which the CSS checks read as the desk and the phone do.
const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

// under is the text inside every @media block of the query, such as "(width < 760px)".
function under(query: string): string {
  const opener = `@media ${query} {`;
  let text = "";
  let from = css.indexOf(opener);
  while (from !== -1) {
    const start = from + opener.length;
    let depth = 1;
    let end = start;
    while (depth > 0) {
      if (css[end] === "{") {
        depth += 1;
      } else if (css[end] === "}") {
        depth -= 1;
      }
      end += 1;
    }
    text += css.slice(start, end - 1);
    from = css.indexOf(opener, end);
  }
  return text;
}

// valueOf is the value a property takes from the last rule of the text that names the selector, or undefined when none does.
function valueOf(text: string, selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const match of text.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!match[1].split(",").some((part) => part.trim() === selector)) {
      continue;
    }
    for (const declaration of match[2].split(";")) {
      const colon = declaration.indexOf(":");
      if (colon > 0 && declaration.slice(0, colon).trim() === property) {
        value = declaration.slice(colon + 1).trim();
      }
    }
  }
  return value;
}

// row is a session row, with the fields the list reads.
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

interface Mount {
  container: HTMLElement;
}

// withApp mounts App at width over the rows, the epics and the address, and runs check with the mount. App is unmounted
// and the DOM removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  width: number,
  setup: { rows: Row[]; epics?: EpicsView; address: Address },
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  const container = document.createElement("div");
  document.body.append(container);
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
          epics={setup.epics}
          contracts={contracts}
          address={setup.address}
        />,
        container,
      );
    });
    await check({ container });
  } finally {
    await teardown();
  }
}

// settle waits until check holds, letting Preact draw what the page's requests and effects changed in between.
function settle(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// recordDrawn is whether a contract record is drawn in the container, or in the panel labelled as given.
function recordDrawn(container: HTMLElement, label?: string): boolean {
  const scope =
    label === undefined ? container : container.querySelector<HTMLElement>(`aside.rpanel[aria-label="${label}"]`);
  return Boolean(scope?.querySelector(".contract-record"));
}

// titleAndRecord is whether the page's only children are its title row, then its record, so a section gap separates them.
function titleAndRecord(page: Element): boolean {
  return (
    page.children.length === 2 &&
    page.children[0]?.classList.contains("title-row") === true &&
    page.children[1]?.classList.contains("contract-record") === true
  );
}

test("ContractPageLook: at the desk the contract page is a section.page directly under the panel's .rcard, which gives it the card's padding", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
    const page = container.querySelector<HTMLElement>(
      'aside.rpanel[aria-label="tide-alerts contract"] section.page[aria-label="contract"]',
    );
    assert.ok(page !== null, "the panel draws the contract as a section.page");
    assert.equal(page.parentElement?.classList.contains("rcard"), true, "the section is a direct child of the card");
    assert.equal(
      valueOf(under("(min-width: 760px)"), ".rcard > section.page", "padding"),
      "var(--o-space-16) var(--o-space-24) var(--o-space-8)",
      "the card's section padding is the epic page's at the desk",
    );
  }));

test("ContractPageLook: at the desk the contract's title row and record are a section gap apart, 32px, in a grid", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => recordDrawn(container, "tide-alerts contract"), "the record is drawn in the panel");
    const page = container.querySelector<HTMLElement>(
      'aside.rpanel[aria-label="tide-alerts contract"] section.page[aria-label="contract"]',
    );
    assert.ok(page !== null && titleAndRecord(page), "the section holds the title row, then the record");
    assert.equal(valueOf(css, ".contract-page", "display"), "grid", "the page lays its two parts out in a grid");
    assert.equal(
      valueOf(under("(min-width: 760px)"), ".contract-page", "gap"),
      "var(--o-space-32)",
      "the gap at the desk is the epic page's section gap",
    );
  }));

test("ContractPageLook: at a phone's width the contract page is a section.page in .epic-read, which takes the panes' gutter", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    const page = container.querySelector<HTMLElement>('.epic-read > section.page[aria-label="contract"]');
    assert.ok(page !== null, "the contract is a section.page inside .epic-read");
    assert.equal(page.parentElement?.parentElement?.classList.contains("panes"), true, "the page sits in the panes");
    assert.equal(
      valueOf(css, ".panes", "padding-left"),
      "max(var(--o-space-16), env(safe-area-inset-left, 0))",
      "the panes pad the page's left edge, as they do the epic page",
    );
    assert.equal(
      valueOf(css, ".panes", "padding-right"),
      "max(var(--o-space-16), env(safe-area-inset-right, 0))",
      "and its right edge",
    );
    assert.equal(valueOf(css, ".epic-read", "padding"), undefined, ".epic-read adds no padding of its own");
    assert.equal(valueOf(under("(width < 760px)"), ".contract-page", "padding"), undefined, "the page adds none under 760px");
  }));

test("ContractPageLook: under 760px the contract's title row and record are a section gap apart, 24px", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async ({ container }) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    const page = container.querySelector<HTMLElement>('.epic-read > section.page[aria-label="contract"]');
    assert.ok(page !== null && titleAndRecord(page), "the section holds the title row, then the record");
    assert.equal(
      valueOf(under("(width < 760px)"), ".contract-page", "gap"),
      "var(--o-space-24)",
      "the gap under 760px is the epic page's section gap there",
    );
  }));

test("ContractPageLook: an epic's contract door draws the contract as a section.page inside the epic page, with its title row and record a section gap apart", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], epics: epicsOf(), address: epicContractAddress }, async ({ container }) => {
    await settle(t.signal, () => recordDrawn(container, "epic"), "the door draws the record");
    const page = container.querySelector<HTMLElement>('section.page[aria-label="epic"] section.page[aria-label="contract"]');
    assert.ok(page !== null && titleAndRecord(page), "the epic page holds the contract's title row, then its record");
  }));
