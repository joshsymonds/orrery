import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { epicsView } from "./epics";
import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Design, DesignSchema } from "./gen/orrery/v1/designs_pb";
import { type Epic, EpicSchema } from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import { styleDeclaration } from "./teststyle";
import { assertNone, assertSame, installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the prefix InboxTree: a design is a tab and a well in its conversation's block, and the tree's
// containers draw no line to their children (round 8, D13, D18).

const now = 1000;
const apiDir = "/work/api";
const conversation = "conv-tide";

// quietFetch answers the app's calls without data, and holds each conversation stream open, so no timer runs.
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

// row is a pane in the api directory, seen as it changed, so no dwell timer runs.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: apiDir,
    host: "",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: 300,
    seen: 300,
    unseen: false,
    archived: false,
    putOff: false,
    archiveKind: "session",
    archiveId: "",
    ...init,
  };
}

// rows are the conversation that works the designs.
function rows(): Row[] {
  return [row({ key: "tide", label: "tide work", archiveId: conversation })];
}

type EpicInit = MessageInitShape<typeof EpicSchema>;

// makeEpic is a live epic of repo-a at the api directory, steered by the conversation.
function makeEpic(slug: string, init: EpicInit = {}): Epic {
  return create(EpicSchema, {
    id: `repo-a/${slug}`,
    repository: "repo-a",
    slug,
    outcome: "",
    live: true,
    path: apiDir,
    steering: conversation,
    ...init,
  });
}

// epics are the live epics of the conversation: alpha, which no design takes, then beta and gamma, which the designs take.
function epics(): Epic[] {
  return [makeEpic("alpha"), makeEpic("beta"), makeEpic("gamma")];
}

// contracts are the proposed contract delta of the conversation, which the first design takes by its id.
function contracts(): Contract[] {
  return [
    create(ContractSchema, {
      id: "repo-a/delta",
      repository: apiDir,
      title: "Delta",
      care: "serious",
      state: "proposed",
      conversation,
      version: 1n,
      changed: 250n,
    }),
  ];
}

type DesignInit = MessageInitShape<typeof DesignSchema>;

// makeDesign is a design of the conversation, created at created, with the state and contracts the init says.
function makeDesign(id: string, init: DesignInit): Design {
  return create(DesignSchema, { id, title: id, conversation, created: 10n, state: "", ...init });
}

// designs are two designs of the conversation. The first, created first, takes its contracts in position order, given here
// out of order, so its children are beta, then delta; the second takes gamma.
function designs(): Design[] {
  return [
    makeDesign("design-a", {
      title: "Tide alerts",
      created: 10n,
      state: "1 released · 1 building",
      contracts: [
        { id: "repo-a/delta", position: 2n },
        { id: "repo-a/beta", position: 1n },
      ],
    }),
    makeDesign("design-b", {
      title: "Harbour map",
      created: 20n,
      state: "1 building",
      contracts: [{ id: "repo-a/gamma", position: 1n }],
    }),
  ];
}

interface Mounted {
  container: HTMLElement;
}

// withApp mounts App over the rows, epics, contracts and designs above, and runs check. App is unmounted and the DOM removed
// when the test ends, or as soon as signal aborts first.
async function withApp(signal: AbortSignal, check: (mounted: Mounted) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const clients = createClients(
    createTransport({
      baseUrl: "https://orrery.test",
      fetch: (input, init) => quietFetch(input, init),
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
          rows={rows()}
          epics={epicsView(epics(), new Map())}
          contracts={contracts()}
          designs={designs()}
          now={now}
          clients={clients}
          address={undefined}
        />,
        container,
      );
    });
    await check({ container });
  } finally {
    await teardown();
  }
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// tabOf is the tab of the design with id, which the tree must draw.
function tabOf(container: ParentNode, id: string): HTMLElement {
  return one(container, `.design-tab[data-key="design:${id}"]`);
}

// blockOf is the design block of the design with id: the block the design's tab begins.
function blockOf(container: ParentNode, id: string): HTMLElement {
  const block = tabOf(container, id).parentElement;
  assert.ok(block !== null && block.classList.contains("design-block"), `design ${id} draws no block`);
  return block;
}

// keysOf is the data-key of each element directly under parent that carries one, in order.
function keysOf(parent: Element): string[] {
  return [...parent.querySelectorAll(":scope > [data-key]")].map((element) => element.getAttribute("data-key") ?? "");
}

test("InboxTree a design's block sits inside its conversation's block, a step in under the conversation's steers", (t) =>
  withApp(t.signal, ({ container }) => {
    const convo = one(container, ".convo-block");
    const block = blockOf(container, "design-a");
    assert.ok(convo.contains(block), "the design's block is inside the conversation's block");
    assert.equal(block.parentElement?.classList.contains("steers"), true, "the design's block is a step in, under the steers");
    assert.equal(convo.firstElementChild?.getAttribute("data-key"), "tide", "the conversation's row heads its block");
  }));

test("InboxTree the block begins with the tab holding the design's mark and bold name, and the well is the tab's next sibling", (t) =>
  withApp(t.signal, ({ container }) => {
    const tab = tabOf(container, "design-a");
    const block = one(container, ".design-block");
    assertSame(block.firstElementChild, tab, "the block begins with the tab");
    assert.ok(tab.querySelector("svg.design-mark") !== null, "the tab holds the design's mark");
    assert.equal(one(tab, ".design-name").textContent, "Tide alerts", "the tab holds the design's name");
    assert.equal(styleDeclaration(".inbox .design-tab .design-name", "font-weight"), "var(--o-font-weight-bold)", "the name is bold");
    assert.equal(tab.nextElementSibling?.classList.contains("design-well"), true, "the well is the tab's next sibling");
    assert.equal(styleDeclaration(".inbox .design-tab", "background"), styleDeclaration(".inbox .design-well", "background"), "the tab and the well share a ground");
    assert.equal(styleDeclaration(".inbox .design-tab", "max-width"), "60%", "the tab is no wider than three fifths of the row");
    assert.equal(styleDeclaration(".inbox .design-tab .design-name", "text-overflow"), "ellipsis", "a long name is cut with an ellipsis");
  }));

test("InboxTree the well's first line is the design's state phrase, then its children in position order", (t) =>
  withApp(t.signal, ({ container }) => {
    const well = one(container, ".design-well");
    assert.equal(well.firstElementChild?.textContent, "1 released · 1 building", "the first line is the design's state");
    assert.equal(well.firstElementChild?.classList.contains("design-state"), true, "the state is the page's first line");
    assert.deepEqual(keysOf(well), ["epic:repo-a/beta", "contract:repo-a/delta"], "each child follows, in position order");
    assert.equal(one(well, '[data-key="contract:repo-a/delta"] .title').textContent, "delta", "the contract draws under its own name");
    assert.equal(styleDeclaration(".inbox .design-well > .row.epic .title", "font-weight"), "var(--o-font-weight-regular)", "a child's name is regular");
  }));

test("InboxTree a designless epic is a row of the conversation's block outside any design block", (t) =>
  withApp(t.signal, ({ container }) => {
    const convo = one(container, ".convo-block");
    const alpha = one(container, '[data-key="epic:repo-a/alpha"]');
    assert.equal(alpha.parentElement?.classList.contains("steers"), true, "the epic is a row under the steers");
    assertSame(alpha.closest(".convo-block"), convo, "the epic is in the conversation's block");
    assertNone(alpha.closest(".design-block"), "the epic is in no design's block");
  }));

test("InboxTree two designs of one conversation are two blocks, each holding only its own children", (t) =>
  withApp(t.signal, ({ container }) => {
    const convo = one(container, ".convo-block");
    const blocks = [...convo.querySelectorAll<HTMLElement>(":scope > .steers > .design-block")];
    assert.equal(blocks.length, 2, "the conversation's block holds two design blocks");
    assert.deepEqual(
      blocks.map((block) => one(block, ".design-tab").getAttribute("data-key")),
      ["design:design-a", "design:design-b"],
      "the designs are blocks in created order",
    );
    assert.deepEqual(
      blocks.map((block) => keysOf(one(block, ".design-well"))),
      [["epic:repo-a/beta", "contract:repo-a/delta"], ["epic:repo-a/gamma"]],
      "each block holds its own children, and no other design's",
    );
  }));

test("InboxTree the count of a design's contracts reads beside its tab, in the singular for one", (t) =>
  withApp(t.signal, ({ container }) => {
    const first = blockOf(container, "design-a");
    const second = blockOf(container, "design-b");
    assert.equal(one(first, ".design-count").textContent, "2 contracts", "the first design counts its two contracts");
    assert.equal(one(second, ".design-count").textContent, "1 contract", "the second design counts its one contract");
    assert.equal(styleDeclaration(".inbox .design-count", "position"), "absolute", "the count is set beside the tab, on the block's ground");
  }));

test("InboxTree no tree container's style draws a border line to its children", () => {
  for (const selector of [".dir-kids", ".inbox .convo-block", ".inbox .steers", ".inbox .design-block", ".inbox .design-well"]) {
    for (const property of ["border", "border-left"]) {
      assert.equal(styleDeclaration(selector, property), undefined, `${selector} draws no ${property} to its children`);
    }
  }
});
