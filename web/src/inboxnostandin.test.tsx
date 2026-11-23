import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Epic, EpicSchema } from "./gen/orrery/v1/epics_pb";
import { epicsView } from "./epics";
import type { Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the prefix InboxWebNoStandIn: a thing with no drawn conversation is its own row in its
// project, and no line says that a conversation is not listed or is archived.

const now = 1000;
const apiDir = "/work/api";
const standInWords = ["Its conversation is not listed", "Its conversation is archived"];

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

// row is a pane in the api directory on the serving host, seen as it changed, so no dwell timer runs.
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

// rows are a listed conversation and an archived one, both in the api directory.
function rows(): Row[] {
  return [
    row({ key: "listed", label: "api listed", archiveId: "conv-listed", changed: 300, seen: 300 }),
    row({ key: "archived", label: "api archived", archiveId: "conv-archived", archived: true, changed: 200, seen: 200 }),
  ];
}

type EpicInit = MessageInitShape<typeof EpicSchema>;

// makeEpic is a live epic of repo-a at the api directory, with the slug given and the steering the init says.
function makeEpic(slug: string, init: EpicInit): Epic {
  return create(EpicSchema, {
    id: `repo-a/${slug}`,
    repository: "repo-a",
    slug,
    outcome: "",
    live: true,
    path: apiDir,
    ...init,
  });
}

// epics are an epic whose conversation is not listed, one whose conversation is archived, and an archived epic whose
// conversation is listed.
function epics(): Epic[] {
  return [
    makeEpic("alpha", { steering: "conv-gone" }),
    makeEpic("beta", { steering: "conv-archived" }),
    makeEpic("gamma", { steering: "conv-listed", archived: true }),
  ];
}

// contracts are a proposed contract of repo-a whose conversation is not listed.
function contracts(): Contract[] {
  return [
    create(ContractSchema, {
      id: "repo-a/delta",
      repository: apiDir,
      title: "Delta",
      care: "serious",
      state: "proposed",
      conversation: "conv-gone",
      version: 1n,
      changed: 250n,
    }),
  ];
}

interface Mounted {
  container: HTMLElement;
  // show renders App again, holding the address as its prop.
  show: (address: Address | undefined) => Promise<void>;
}

// withApp mounts App over the rows, epics and contracts above, and runs check. App is unmounted and the DOM removed when
// the test ends, or as soon as signal aborts first.
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
  const show = (address: Address | undefined) =>
    act(() => {
      render(
        <App
          rows={rows()}
          epics={epicsView(epics(), new Map())}
          contracts={contracts()}
          now={now}
          clients={clients}
          address={address}
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
    await show(undefined);
    await check({ container, show });
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

// standInLines are the elements under root whose text says a conversation is not listed or is archived.
function standInLines(root: ParentNode): Element[] {
  return Array.from(root.querySelectorAll("*")).filter((element) =>
    standInWords.some((words) => (element.textContent ?? "").includes(words)),
  );
}

test("InboxWebNoStandIn: an epic and a proposed contract whose conversation is not listed each draw as their own row in their project", (t) =>
  withApp(t.signal, ({ container }) => {
    const project = one(container, 'section.dir[aria-label="api"]');
    const epic = one(project, '.dir-kids > .row.epic[data-key="epic:repo-a/alpha"]');
    assert.equal(one(epic, ".title").textContent, "alpha", "the epic draws under its own name");
    const contract = one(project, '.dir-kids > .row.epic.contract[data-key="contract:repo-a/delta"]');
    assert.equal(one(contract, ".title").textContent, "delta", "the contract draws under its own name");
    assert.equal(project.querySelector(".steers"), null, "neither hangs under a conversation's row");
  }));

test("InboxWebNoStandIn: the inbox and the Archived view each draw no line that says a conversation is not listed or archived, and no .standin element", (t) =>
  withApp(t.signal, async ({ container, show }) => {
    assert.deepEqual(standInLines(container), [], "the inbox draws no stand-in line");
    assert.equal(container.querySelector(".standin"), null, "the inbox has no .standin element");
    await show({ view: "archived", entry: null });
    assert.deepEqual(standInLines(container), [], "the Archived view draws no stand-in line");
    assert.equal(container.querySelector(".standin"), null, "the Archived view has no .standin element");
  }));

test("InboxWebNoStandIn: the Archived view draws an archived epic whose conversation is listed as its own row, and the epic of an archived conversation under that conversation", (t) =>
  withApp(t.signal, async ({ container, show }) => {
    await show({ view: "archived", entry: null });
    const project = one(container, 'section.dir[aria-label="api"]');
    one(project, '.dir-kids > .row.epic[data-key="epic:repo-a/gamma"]');
    one(project, '.dir-kids > .row[data-key="archived"] + .steers > .row.epic[data-key="epic:repo-a/beta"]');
    assert.equal(project.querySelector('[data-key="epic:repo-a/alpha"]'), null, "a live epic with no archived conversation is not drawn");
  }));
