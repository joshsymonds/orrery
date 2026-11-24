import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render, type JSX } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Design, DesignSchema } from "./gen/orrery/v1/designs_pb";
import { InboxRail } from "./inboxrail";
import type { FlyoutRow, LineageConvo, RailTileModel } from "./railtiles";
import type { Row } from "./rows";
import type { SeenClock } from "./seen";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { styleDeclaration } from "./teststyle";

const now = 1000;
const tidepool = "|/home/owner/code/tidepool";

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the element into the container, and flushes its effects.
function draw(container: HTMLElement, element: JSX.Element): void {
  act(() => {
    render(element, container);
  });
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// tokenOf is the value of the custom property name in web/src/tokens.css, which tokens.json generates for the web app. The
// file is read from the repository root, where every check runs.
function tokenOf(name: string): string | undefined {
  const css = readFileSync(join(process.cwd(), "web/src/tokens.css"), "utf8");
  return css.match(new RegExp(`^\\s*${name}: (.*);$`, "m"))?.[1];
}

// stacksOf is each design's stack the rail draws under the project's tile, in order: the design's head, then its marks, by
// their labels.
function stacksOf(container: HTMLElement): string[][] {
  return Array.from(container.querySelectorAll(".ldesign")).map((design) =>
    Array.from(design.querySelectorAll(".lmark")).map((mark) => mark.getAttribute("aria-label") ?? ""),
  );
}

// propsOf gives the rail its props for the tiles, with no flyout, no unread notice, and each press recorded in presses.
function propsOf(tiles: RailTileModel[], presses: string[]) {
  return {
    tiles,
    flyout: null,
    now,
    onMark: () => presses.push("mark"),
    onTile: (key: string) => presses.push(`tile:${key}`),
    onHover: (key: string) => presses.push(`hover:${key}`),
    onHoverOff: () => presses.push("hover-off"),
    onFlyoutRow: (item: FlyoutRow) => presses.push(`row:${item.key}`),
    onFlyoutPage: (key: string) => presses.push(`page:${key}`),
    onLineage: (key: string) => presses.push(`lineage:${key}`),
    onNew: () => presses.push("new"),
    onSearch: () => presses.push("search"),
    onNotices: () => presses.push("notices"),
    onKeys: () => presses.push("keys"),
    unread: 0,
  };
}

// tileOf is a project's tile in the rail's model, quiet, with the lineage given.
function tileOf(lineage: LineageConvo[]): RailTileModel {
  return {
    key: tidepool,
    name: "tidepool",
    n: 0,
    live: false,
    fresh: false,
    quiet: true,
    cur: false,
    title: "tidepool",
    lineage,
  };
}

// oneDesign is the session sess-tide working the design Tide alerts, whose contracts and epics are the contract beta at
// position 1 and the epic alpha at position 2, in that order.
const oneDesign: LineageConvo[] = [
  {
    key: "sess-tide",
    held: false,
    own: [],
    designs: [
      {
        key: "design:tide",
        name: "Tide alerts",
        open: false,
        marks: [
          { key: "contract:tide/beta", kind: "contract", name: "beta", open: false },
          { key: "epic:tide/alpha", kind: "epic", name: "alpha", open: false },
        ],
      },
    ],
  },
];

// twoDesigns is the session working two designs, each with its own marks.
const twoDesigns: LineageConvo[] = [
  {
    key: "sess-tide",
    held: false,
    own: [],
    designs: [
      {
        key: "design:tide",
        name: "Tide alerts",
        open: false,
        marks: [{ key: "contract:tide/beta", kind: "contract", name: "beta", open: false }],
      },
      {
        key: "design:feed",
        name: "Harbour feed",
        open: false,
        marks: [
          { key: "epic:feed/ingest", kind: "epic", name: "ingest", open: false },
          { key: "epic:feed/publish", kind: "epic", name: "publish", open: false },
        ],
      },
    ],
  },
];

test("RailLineage: a project's design shows its mark, then one mark per contract or epic in position order, for one design", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([tileOf(oneDesign)], [])} />);
    assert.deepEqual(stacksOf(container), [
      ["design: Tide alerts", "contract: beta", "epic: alpha"],
    ]);
    const design = one(container, ".ldesign");
    assert.ok(design.firstElementChild?.classList.contains("lmark"), "the design's mark heads its pill");
    assert.ok(one(design, ".lwell").previousElementSibling?.classList.contains("lmark"), "the well is under the mark");
  });
});

test("RailLineage: two designs of a project make two stacks, each with its own mark and its own marks", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([tileOf(twoDesigns)], [])} />);
    assert.equal(container.querySelectorAll(".rail-lineage").length, 1, "one lineage block for the project");
    assert.deepEqual(stacksOf(container), [
      ["design: Tide alerts", "contract: beta"],
      ["design: Harbour feed", "epic: ingest", "epic: publish"],
    ]);
  });
});

test("RailLineage: the open epic's mark has class open, and the open class's ground is the open colour", async () => {
  const openEpic: LineageConvo[] = [
    {
      key: "sess-tide",
      held: true,
      own: [],
      designs: [
        {
          ...oneDesign[0].designs[0],
          marks: [
            { key: "contract:tide/beta", kind: "contract", name: "beta", open: false },
            { key: "epic:tide/alpha", kind: "epic", name: "alpha", open: true },
          ],
        },
      ],
    },
  ];
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([tileOf(openEpic)], [])} />);
    const open = Array.from(container.querySelectorAll(".lmark.open")).map((mark) => mark.getAttribute("aria-label"));
    assert.deepEqual(open, ["epic: alpha"], "only the open epic's mark is open");
    assert.equal(styleDeclaration(".lmark.open", "background"), "var(--open)", "the open mark's ground is the open colour");
    assert.equal(styleDeclaration(".rail-lineage", "--open"), "var(--o-ground-open)", "the open colour is the open ground token");
    assert.equal(
      tokenOf("--o-ground-open"),
      "color-mix(in srgb, var(--o-role-focus) 35%, var(--o-surface-side))",
      "the open ground is the focus role at 35 in 100 on the side surface",
    );
  });
});

test("RailLineage: a press on a child mark calls onLineage with its key", async () => {
  const presses: string[] = [];
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([tileOf(oneDesign)], presses)} />);
    one(container, 'button[aria-label="epic: alpha"]').click();
  });
  assert.deepEqual(presses, ["lineage:epic:tide/alpha"]);
});

// quietFetch answers the app's calls without data: a unary call with an empty object, and a stream with a body that stays
// open until its call aborts, so no conversation arrives and no timer runs.
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

// stillClock is a seen clock whose timers never fire, so an unseen row under the cursor is never marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// tideRow is the working session sess-tide of /work/tide, which the design works.
const tideRow: Row = {
  tool: "",
  id: "sess-tide",
  kind: "pane",
  key: "sess-tide",
  label: "tide work",
  archiveId: "sess-tide",
  project: "/work/tide",
  host: "",
  state: "working",
  summary: "",
  question: "",
  questionHash: "",
  changed: 300,
  seen: 300,
  unseen: false,
  archived: false,
  putOff: false,
  archiveKind: "none",
};

// withApp mounts App at a wide width over quietFetch, with the tide design and its proposed contract beta in the tree, in a
// fresh DOM; App is unmounted and the DOM removed when the check ends, or as soon as signal aborts first.
async function withApp(signal: AbortSignal, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  window.innerWidth = 1024;
  const container = document.createElement("div");
  document.body.append(container);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: quietFetch }));
  const contract: Contract = create(ContractSchema, {
    id: "repo-a/beta",
    repository: "/work/tide",
    title: "Beta",
    care: "limited",
    state: "proposed",
    version: 1n,
    openDecisions: 0n,
    conversation: "sess-tide",
    changed: 200n,
  });
  const design: Design = create(DesignSchema, {
    id: "tide",
    title: "Tide alerts",
    conversation: "sess-tide",
    created: 10n,
    contracts: [{ id: "repo-a/beta", position: 1n }],
  });
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
        <App rows={[tideRow]} now={now} clients={clients} seenClock={stillClock} contracts={[contract]} designs={[design]} />,
        container,
      );
    });
    await check(container);
  } finally {
    await teardown();
  }
}

test("RailLineage: pressing a child mark on the collapsed rail opens its panel, and the mark is then open", (t) =>
  withApp(t.signal, async (container) => {
    await act(() => {
      one(container, '.inbox-foot button[aria-label="Collapse the inbox"]').click();
    });
    assert.equal(one(container, ".panes").getAttribute("data-inbox"), "collapsed", "the inbox is collapsed");
    const mark = one(container, '.rail-lineage button[aria-label="contract: beta"]');
    assertNone(container.querySelector('article.held-card[aria-label="beta"]'), "no card before the press");
    await act(() => {
      mark.click();
    });
    assert.ok(container.querySelector('article.held-card[data-kind="contract"][aria-label="beta"]') !== null, "the contract's card is held");
    assert.ok(one(container, '.rail-lineage button[aria-label="contract: beta"]').classList.contains("open"), "the mark is open");
  }));
