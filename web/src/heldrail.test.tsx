import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render, type JSX } from "preact";
import { act } from "preact/test-utils";

import { ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { DesignSchema } from "./gen/orrery/v1/designs_pb";
import { EpicSchema } from "./gen/orrery/v1/epics_pb";
import { RailLineage } from "./inboxrail";
import { type LineageConvo, railLineage } from "./railtiles";
import type { Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";
import { styleDeclaration } from "./teststyle";
import { buildTree, type Project, type TreeInput } from "./tree";

const serving = "halyard";
const tidepool = "|/home/owner/code/tidepool";

function noop(): void {}

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

// rowOf is a conversation of /srv/app, working or done, that archiveId names.
function rowOf(archiveId: string, state: Row["state"]): Row {
  return {
    tool: "",
    id: archiveId,
    kind: "pane",
    key: archiveId,
    label: archiveId,
    archiveId,
    project: "/srv/app",
    host: serving,
    state,
    summary: "",
    question: "",
    questionHash: "",
    changed: 300,
    seen: 300,
    unseen: false,
    archived: false,
    putOff: false,
    archiveKind: "session",
  };
}

// projectOf builds the project of /srv/app: sess-a works the design Tide alerts, whose contract is beta; sess-b steers the
// epic alpha, which no design holds.
function projectOf(): Project {
  const input: TreeInput = {
    rows: [rowOf("sess-a", "working"), rowOf("sess-b", "done")],
    epics: [
      create(EpicSchema, {
        id: "repo-a/alpha",
        repository: "repo-a",
        slug: "alpha",
        live: true,
        path: "/srv/app",
        steering: "sess-b",
      }),
    ],
    details: new Map(),
    contracts: [
      create(ContractSchema, {
        id: "repo-a/beta",
        repository: "/srv/app",
        title: "beta",
        state: "proposed",
        version: 1n,
        conversation: "sess-a",
      }),
    ],
    designs: [
      create(DesignSchema, {
        id: "design-x",
        title: "Tide alerts",
        conversation: "sess-a",
        created: 10n,
        contracts: [{ id: "repo-a/beta", position: 1n }],
      }),
    ],
    host: serving,
    view: "inbox",
  };
  const [project] = buildTree(input);
  return project;
}

// heldConvos is the session sess-tide holding the design Tide alerts, its epic alerts open and its contract table beside it,
// and the session sess-harbour working the design Harbour feed, which is not held.
const heldConvos: LineageConvo[] = [
  {
    key: "sess-tide",
    held: true,
    own: [],
    designs: [
      {
        key: "design:tide",
        name: "Tide alerts",
        open: false,
        marks: [
          { key: "epic:tide/alerts", kind: "epic", name: "alerts", open: true },
          { key: "contract:tide/table", kind: "contract", name: "table", open: false },
        ],
      },
    ],
  },
  {
    key: "sess-harbour",
    held: false,
    own: [],
    designs: [
      {
        key: "design:feed",
        name: "Harbour feed",
        open: false,
        marks: [{ key: "epic:feed/ingest", kind: "epic", name: "ingest", open: false }],
      },
    ],
  },
];

test("HeldLayout: railLineage marks the held conversation, and gives a designless open epic as its own mark", () => {
  const project = projectOf();
  assert.deepEqual(
    railLineage(project, "").map((convo) => [convo.key, convo.held]),
    [["sess-a", false]],
    "an epic steered without a design is on no lineage until it is open",
  );
  assert.deepEqual(
    railLineage(project, "design:design-x").map((convo) => [convo.key, convo.held]),
    [["sess-a", true]],
    "the conversation whose design is open is held",
  );
  const lineage = railLineage(project, "epic:repo-a/alpha");
  assert.deepEqual(lineage.find((convo) => convo.key === "sess-b"), {
    key: "sess-b",
    held: true,
    own: [{ key: "epic:repo-a/alpha", kind: "epic", name: "alpha", open: true }],
    designs: [],
  });
  assert.equal(lineage.find((convo) => convo.key === "sess-a")?.held, false, "the other conversation is not held");
});

test("HeldLayout: RailLineage draws the held conversation's mark as a neck, and the open mark as a lozenge to the rail's edge", async () => {
  await withDom((container) => {
    draw(container, <RailLineage projectKey={tidepool} convos={heldConvos} onPress={noop} />);
    assert.equal(container.querySelectorAll('.lconv[data-held="convo"]').length, 1, "only the held conversation is a neck");
    assert.ok(
      one(container, ".lconvo").querySelector('.lconv[data-held="convo"]') !== null,
      "the neck is the held conversation's own mark",
    );
    assert.equal(one(container, '[data-held="open"]').getAttribute("aria-label"), "epic: alerts", "the open epic is the lozenge");
    assert.equal(
      styleDeclaration('.lconv[data-held="convo"]', "background"),
      "var(--o-surface-page)",
      "the neck is on the page's ground",
    );
    assert.equal(
      styleDeclaration('.lconv[data-held="convo"]::after', "width"),
      "var(--o-layout-rail-width)",
      "the neck runs to the rail's edge",
    );
    assert.equal(
      styleDeclaration('.lmark.open[data-held="open"]::after', "width"),
      "var(--o-layout-rail-width)",
      "the lozenge runs to the rail's edge",
    );
  });
});

test("HeldLayout: RailLineage draws a held conversation's own marks before its designs, and presses them as any mark", async () => {
  const presses: string[] = [];
  const ownConvos: LineageConvo[] = [
    {
      key: "sess-tide",
      held: true,
      own: [{ key: "epic:tide/alerts", kind: "epic", name: "alerts", open: true }],
      designs: [
        {
          key: "design:tide",
          name: "Tide alerts",
          open: false,
          marks: [{ key: "contract:tide/table", kind: "contract", name: "table", open: false }],
        },
      ],
    },
  ];
  await withDom((container) => {
    draw(
      container,
      <RailLineage projectKey={tidepool} convos={ownConvos} onPress={(key) => presses.push(key)} />,
    );
    const children = Array.from(one(container, ".lconvo").children);
    const own = children.findIndex((child) => child.getAttribute("aria-label") === "epic: alerts");
    const design = children.findIndex((child) => child.classList.contains("ldesign"));
    assert.ok(own >= 0 && design > own, "the own mark is drawn before the design");
    one(container, 'button[aria-label="epic: alerts"]').click();
  });
  assert.deepEqual(presses, ["epic:tide/alerts"]);
});

test("OpenColour: the open mark is on var(--o-ground-open), and under the pointer on var(--o-ground-open-hover)", async () => {
  await withDom((container) => {
    draw(container, <RailLineage projectKey={tidepool} convos={heldConvos} onPress={noop} />);
    one(container, '.lmark.open[data-held="open"]');
    assert.equal(
      styleDeclaration('.lmark.open[data-held="open"]', "background"),
      "var(--o-ground-open)",
      "the open mark is on the open ground",
    );
    assert.equal(
      styleDeclaration('.lmark.open[data-held="open"]:hover', "background"),
      "var(--o-ground-open-hover)",
      "under the pointer the open mark is on the hover ground",
    );
  });
});
