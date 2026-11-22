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
import { ContractsGetResponseSchema } from "./gen/orrery/v1/contracts_pb";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import type { Address } from "./router";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name starts with ContractPageLook. Each renders App at a window width of its own, as contractaddress.test.tsx
// does, and reads the declarations of style.css, as recordstyle.test.ts does, with comments removed. The contract page is a
// section.page: at the desk it sits directly under the panel's .rcard, which pads it, and under 760px it sits in .epic-read,
// in the panes that pad the epic page.

const now = 1000;
const contractId = "tidepool/tide-alerts";
const conversation = "sess-tide-16";
const contractAddress: Address = { view: "inbox", entry: { kind: "contract", id: contractId } };
const epicAddress: Address = { view: "inbox", entry: { kind: "epic", id: contractId, page: { kind: "contract" } } };
const contracts = contractList.contracts;

const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

// mediaBodies is the bodies of every @media block of query, such as "(width < 760px)", one after another.
function mediaBodies(query: string): string {
  const opener = `@media ${query} {`;
  const bodies: string[] = [];
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
    bodies.push(css.slice(start, end - 1));
    from = css.indexOf(opener, end);
  }
  assert.ok(bodies.length > 0, `style.css has a ${query} block`);
  return bodies.join("\n");
}

// declaredIn is the value the rules of body set for selector's property, the last one that sets it, or undefined when none does.
function declaredIn(body: string, selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const [, selectors, declarations] of body.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!selectors.split(",").some((part) => part.trim() === selector)) {
      continue;
    }
    for (const declaration of declarations.split(";")) {
      const colon = declaration.indexOf(":");
      if (colon > 0 && declaration.slice(0, colon).trim() === property) {
        value = declaration.slice(colon + 1).trim();
      }
    }
  }
  return value;
}

// row is a session row with the fields the App reads, and the rest left at their empty values.
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

// epicsOf is the epics view of the contract's own epic, with its detail.
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
  const path = new URL(new Request(input, init).url).pathname;
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  const answer = path === "/orrery.v1.Contracts/Get" ? toJson(ContractsGetResponseSchema, tideProposed) : {};
  return new Response(JSON.stringify(answer), { status: 200, headers: { "content-type": "application/json" } });
}

// withApp mounts App at width over the rows, the epics and the address, and runs check with the container. App is unmounted
// and the DOM removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  width: number,
  setup: { rows: Row[]; epics?: EpicsView; address: Address },
  check: (container: HTMLElement) => void | Promise<void>,
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
    await check(container);
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

// recordDrawn is whether the contract record is drawn in the container.
function recordDrawn(container: HTMLElement): boolean {
  return container.querySelector(".contract-record") !== null;
}

// titleThenRecord is whether the page holds exactly two children, its title row and then its record.
function titleThenRecord(page: Element): boolean {
  const children = [...page.children];
  return children.length === 2 && children[0].classList.contains("title-row") && children[1].classList.contains("contract-record");
}

test("ContractPageLook: at the desk the contract page is a section.page directly under the panel's .rcard, which pads it", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async (container) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    const page = panelOf(container, "tide-alerts contract")?.querySelector(".rcard > section.page.contract-page");
    assert.ok(page, "the panel's card holds the contract page as a section.page");
    assert.ok(page.querySelector(".contract-record"), "the record is drawn in the page");
    assert.equal(
      declaredIn(mediaBodies("(min-width: 760px)"), ".rcard > section.page", "padding"),
      "var(--o-space-16) var(--o-space-24) var(--o-space-8)",
      "the card's rule pads a section.page directly under it",
    );
  }));

test("ContractPageLook: under 760px the contract page is a section.page in .epic-read, which the panes pad as the epic page", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async (container) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    const page = container.querySelector(".panes > .epic-read > section.page.contract-page");
    assert.ok(page, "the contract page is drawn as a section.page directly in .epic-read, in the panes");
    assert.equal(panelOf(container, "tide-alerts contract"), null, "no panel is drawn beside the page");
    assert.equal(
      declaredIn(css, ".panes", "padding-left"),
      "max(var(--o-space-16), env(safe-area-inset-left, 0))",
      "the panes pad the phone's left edge, where the epic page's section sits too",
    );
  }));

test("ContractPageLook: at the desk the title row and the record are a section gap of 32 apart", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], address: contractAddress }, async (container) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    const page = container.querySelector("section.page.contract-page");
    assert.ok(page && titleThenRecord(page), "the title row and the record are the page's two children");
    assert.equal(
      declaredIn(mediaBodies("(min-width: 760px)"), "section.page.contract-page", "gap"),
      "var(--o-space-32)",
      "the desk page gaps its title row from its record by a section gap of 32",
    );
  }));

test("ContractPageLook: under 760px the title row and the record are a section gap of 24 apart", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], address: contractAddress }, async (container) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    const page = container.querySelector("section.page.contract-page");
    assert.ok(page && titleThenRecord(page), "the title row and the record are the page's two children");
    assert.equal(
      declaredIn(mediaBodies("(width < 760px)"), "section.page.contract-page", "gap"),
      "var(--o-space-24)",
      "the phone page gaps its title row from its record by a section gap of 24",
    );
  }));

test("ContractPageLook: an epic's contract page at the desk is a section.page under the epic panel's .rcard, inside the epic's section", (t) =>
  withApp(t.signal, 1600, { rows: [conversationRow()], epics: epicsOf(), address: epicAddress }, async (container) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    const page = panelOf(container, "epic")?.querySelector(
      ".rcard > section.page[aria-label='epic'] > section.page.contract-page",
    );
    assert.ok(page, "the epic's section holds the contract page as a section.page under the panel's card");
    assert.ok(titleThenRecord(page), "the title row and the record are the page's two children");
  }));

test("ContractPageLook: an epic's contract page under 760px is a section.page in the .epic-read the epic page is in", (t) =>
  withApp(t.signal, 400, { rows: [conversationRow()], epics: epicsOf(), address: epicAddress }, async (container) => {
    await settle(t.signal, () => recordDrawn(container), "the record is drawn");
    const page = container.querySelector(
      ".panes > .epic-read > section.page[aria-label='epic'] > section.page.contract-page",
    );
    assert.ok(page, "the epic's section, in .epic-read, holds the contract page as a section.page");
    assert.ok(titleThenRecord(page), "the title row and the record are the page's two children");
  }));
