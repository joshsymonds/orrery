import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Design, DesignSchema } from "./gen/orrery/v1/designs_pb";
import { type Epic, EpicSchema, EpicsGetResponseSchema, EpicStepSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { type Row } from "./rows";
import { flyoutOf, type FlyoutRow, railLineage, railTiles, type RailTileModel } from "./railtiles";
import { buildTree, type Project, type TreeInput } from "./tree";

const serving = "halyard";

function makeRow(init: Partial<Row> & Pick<Row, "key" | "label">): Row {
  return {
    id: "%3",
    archiveId: "",
    kind: "pane",
    project: "/srv/app",
    host: serving,
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    putOff: false,
    archiveKind: "session",
    tool: "",
    ...init,
  };
}

type EpicInit = MessageInitShape<typeof EpicSchema>;
type DetailInit = MessageInitShape<typeof EpicsGetResponseSchema>;

// makeEpic is a live epic at /srv/app, listed as repo-a/alpha, unless the init says otherwise.
function makeEpic(init: EpicInit): Epic {
  return create(EpicSchema, { id: "repo-a/alpha", repository: "repo-a", slug: "alpha", live: true, path: "/srv/app", ...init });
}

function makeDetail(init: DetailInit): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// inputOf is the tree input of the rows, in the inbox at the serving host, unless the init says otherwise.
function inputOf(rows: Row[], init: Partial<TreeInput> = {}): TreeInput {
  return { rows, epics: [], details: new Map(), contracts: [], host: serving, view: "inbox", ...init };
}

// tileOf is the model of the project with the key.
function tileOf(tiles: RailTileModel[], key: string): RailTileModel {
  const tile = tiles.find((candidate) => candidate.key === key);
  assert.ok(tile !== undefined, `a tile keyed ${key}`);
  return tile;
}

const questions = [{ id: "Q1", text: "Use Postgres?" }];

test("InboxRail keeps the tree's project order, one tile per project", () => {
  const rows = [
    makeRow({ key: "a", label: "a", project: "/srv/zeta" }),
    makeRow({ key: "b", label: "b", project: "/srv/alpha" }),
    makeRow({ key: "c", label: "c", project: "/srv/mid" }),
  ];
  const projects = buildTree(inputOf(rows));
  assert.deepEqual(
    railTiles(projects, "").map((tile) => tile.key),
    ["|/srv/zeta", "|/srv/alpha", "|/srv/mid"],
  );
});

test("InboxRail gives each tile its project's whole name, even where two names share their first letters", () => {
  const rows = [
    makeRow({ key: "a", label: "a", project: "/srv/lighthouse" }),
    makeRow({ key: "b", label: "b", project: "/srv/lilac" }),
    makeRow({ key: "c", label: "c", project: "/srv/tide" }),
  ];
  const tiles = railTiles(buildTree(inputOf(rows)), "");
  assert.deepEqual(
    tiles.map((tile) => tile.name),
    ["lighthouse", "lilac", "tide"],
  );
});

test("InboxRail makes a tile quiet when nothing in its project waits on you and nothing is new, and only then", () => {
  const rows = [
    makeRow({ key: "w", label: "w", project: "/srv/waiting", state: "waiting" }),
    makeRow({ key: "u", label: "u", project: "/srv/new", state: "done", unseen: true }),
    makeRow({ key: "r", label: "r", project: "/srv/rest", state: "working" }),
    makeRow({ key: "e", label: "e", project: "/srv/ended", state: "stopped", archiveKind: "headless", unseen: true, ended: true }),
  ];
  const tiles = railTiles(buildTree(inputOf(rows)), "");
  assert.equal(tileOf(tiles, "|/srv/waiting").quiet, false, "a project with a row that waits is not quiet");
  assert.equal(tileOf(tiles, "|/srv/new").quiet, false, "a project with an unseen row is not quiet");
  assert.equal(tileOf(tiles, "|/srv/rest").quiet, true, "a working project with nothing waiting or new is quiet");
  assert.equal(tileOf(tiles, "|/srv/ended").quiet, false, "an unseen row behind the ended line is new");
});

test("InboxRail counts a project's waiting things as the tree does, including an epic with no listed conversation", () => {
  const rows = [
    makeRow({ key: "w", label: "w", archiveId: "sess-w", project: "/srv/app", state: "waiting" }),
    makeRow({ key: "q", label: "q", archiveId: "sess-q", project: "/srv/quiet", state: "done" }),
  ];
  const projects: Project[] = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-nobody", path: "/srv/app" })],
      details: new Map([["repo-a/alpha", makeDetail({ questions })]]),
    }),
  );
  const tiles = railTiles(projects, "");
  assert.equal(tileOf(tiles, "|/srv/app").n, 2);
  assert.equal(tileOf(tiles, "|/srv/quiet").n, 0);
});

test("InboxRail is live for a project whose conversation works or whose epic has running steps", () => {
  const rows = [
    makeRow({ key: "w", label: "w", archiveId: "sess-w", project: "/srv/working", state: "working" }),
    makeRow({ key: "s", label: "s", archiveId: "sess-s", project: "/srv/steps", state: "done" }),
    makeRow({ key: "r", label: "r", archiveId: "sess-r", project: "/srv/rest", state: "done" }),
  ];
  const running = create(EpicStepSchema, { id: "step-1", task: "step one", agent: "builder" });
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-s", path: "/srv/steps", runningSteps: [running] })],
    }),
  );
  const tiles = railTiles(projects, "");
  assert.deepEqual(
    tiles.map((tile) => [tile.key, tile.live]),
    [
      ["|/srv/working", true],
      ["|/srv/steps", true],
      ["|/srv/rest", false],
    ],
  );
});

test("InboxRail is fresh for a project with an unseen conversation", () => {
  const rows = [
    makeRow({ key: "u", label: "u", project: "/srv/new", state: "done", unseen: true }),
    makeRow({ key: "s", label: "s", project: "/srv/seen", state: "done", unseen: false }),
  ];
  const tiles = railTiles(buildTree(inputOf(rows)), "");
  assert.deepEqual(
    tiles.map((tile) => [tile.key, tile.fresh]),
    [
      ["|/srv/new", true],
      ["|/srv/seen", false],
    ],
  );
});

test("InboxRail marks only the project that holds the open conversation, epic, or project key", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/one" }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/two" }),
  ];
  const projects = buildTree(
    inputOf(rows, { epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-b", path: "/srv/two" })] }),
  );
  const curKeys = (open: string): string[] =>
    railTiles(projects, open)
      .filter((tile) => tile.cur)
      .map((tile) => tile.key);
  assert.deepEqual(curKeys("a"), ["|/srv/one"]);
  assert.deepEqual(curKeys("epic:repo-a/alpha"), ["|/srv/two"]);
  assert.deepEqual(curKeys("|/srv/two"), ["|/srv/two"]);
  assert.deepEqual(curKeys(""), []);
  assert.deepEqual(curKeys("b-not-open"), []);
});

test("InboxRail titles a tile with its name, and with its waiting count when one waits", () => {
  const rows = [
    makeRow({ key: "w", label: "w", archiveId: "sess-w", project: "/srv/app", state: "waiting" }),
    makeRow({ key: "x", label: "x", archiveId: "sess-x", project: "/srv/lone", state: "waiting" }),
    makeRow({ key: "r", label: "r", archiveId: "sess-r", project: "/srv/rest", state: "done" }),
  ];
  const tiles = railTiles(buildTree(inputOf(rows, {
    epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-nobody", path: "/srv/app" })],
    details: new Map([["repo-a/alpha", makeDetail({ questions })]]),
  })), "");
  assert.equal(tileOf(tiles, "|/srv/app").title, "app: 2 wait on you");
  assert.equal(tileOf(tiles, "|/srv/lone").title, "lone: 1 waits on you");
  assert.equal(tileOf(tiles, "|/srv/rest").title, "rest");
});

// flyoutProject is one project of each kind of row a flyout draws, at /srv/app on the serving host:
// - w-old and w-new wait on you, changed at 100 and 300, and w-new is also unseen;
// - the epic alpha, steered by the seen done row d, is parked, so it waits on you, changed at 200;
// - u is a done row, unseen; e is an unseen headless row serve reports ended, so it is behind the ended line;
// - s is a seen done row, and ended-seen is a seen headless row serve reports ended, behind the ended line.
function flyoutProject(): Project {
  const rows = [
    makeRow({ key: "w-old", label: "old ask", archiveId: "sess-wo", state: "waiting", changed: 100 }),
    makeRow({ key: "w-new", label: "new ask", archiveId: "sess-wn", state: "waiting", changed: 300, unseen: true }),
    makeRow({ key: "d", label: "steers alpha", archiveId: "sess-d", state: "done", changed: 50 }),
    makeRow({ key: "u", label: "fresh", archiveId: "sess-u", state: "done", changed: 400, unseen: true }),
    makeRow({ key: "s", label: "seen", archiveId: "sess-s", state: "done", changed: 20 }),
    makeRow({ key: "e", label: "ended fresh", archiveId: "sess-e", state: "stopped", archiveKind: "headless", changed: 10, unseen: true, ended: true }),
    makeRow({ key: "ended-seen", label: "ended seen", archiveId: "sess-x", state: "stopped", archiveKind: "headless", changed: 5, ended: true }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", slug: "alpha", steering: "sess-d", changed: 200n, parked: "workflow node red" })],
      details: new Map([["repo-a/alpha", makeDetail({ parked: "workflow node red" })]]),
    }),
  );
  assert.equal(projects.length, 1, "the rows are one project");
  return projects[0];
}

// keysOf is each flyout row's item key, in order.
function keysOf(rows: readonly FlyoutRow[]): string[] {
  return rows.map((row) => row.key);
}

test("ProjectFlyout names the project and its path, and counts what waits and what is new in words", () => {
  const model = flyoutOf(flyoutProject(), 1000);
  assert.equal(model.key, "|/srv/app");
  assert.equal(model.name, "app");
  assert.equal(model.path, "/srv/app");
  assert.equal(model.waits, "3 wait", "the two waiting rows and the parked epic wait");
  assert.equal(model.fresh, "3 new", "w-new, u and the ended e are unseen");
});

test("ProjectFlyout says waits for one, and draws no count for nothing", () => {
  const one = buildTree(inputOf([makeRow({ key: "w", label: "w", state: "waiting" })]))[0];
  assert.equal(flyoutOf(one, 1000).waits, "1 waits");
  assert.equal(flyoutOf(one, 1000).fresh, "", "nothing is new");
  const none = buildTree(inputOf([makeRow({ key: "s", label: "s", state: "done" })]))[0];
  assert.equal(flyoutOf(none, 1000).waits, "", "nothing waits");
});

test("ProjectFlyout lists what waits on you oldest first, as w scoped to the project does, then the new rows not already listed", () => {
  const model = flyoutOf(flyoutProject(), 1000);
  assert.deepEqual(keysOf(model.rows), ["w-old", "epic:repo-a/alpha", "w-new", "u", "e"]);
});

test("ProjectFlyout draws a waiting epic by its slug, with its blocker as the ask and its change as its time", () => {
  const model = flyoutOf(flyoutProject(), 1000);
  const epic = model.rows.find((row) => row.key === "epic:repo-a/alpha");
  assert.deepEqual(epic, { kind: "hung", key: "epic:repo-a/alpha", name: "alpha", ask: "workflow node red", changed: 200 });
});

test("ProjectFlyout carries a conversation's row, so the flyout draws its title, age and second line from it", () => {
  const model = flyoutOf(flyoutProject(), 1000);
  const row = model.rows.find((candidate) => candidate.key === "w-old");
  assert.ok(row !== undefined && row.kind === "row", "w-old is a conversation's row");
  assert.equal(row.row.label, "old ask");
});

test("ProjectFlyout counts the rest: the tree's other entries, none waiting, and the conversations behind the ended line", () => {
  // The tree holds w-old, w-new, d, u, s and the epic: four are listed, so d and s are the two more. Behind the ended
  // line are e and ended-seen.
  assert.equal(flyoutOf(flyoutProject(), 1000).rest, "and 2 more, none waiting · 2 ended");
});

test("ProjectFlyout's rest line drops a zero part, and is empty when there is no rest", () => {
  const tree = buildTree(inputOf([makeRow({ key: "w", label: "w", state: "waiting" }), makeRow({ key: "s", label: "s" })]))[0];
  assert.equal(flyoutOf(tree, 1000).rest, "and 1 more, none waiting");
  const ended = buildTree(
    inputOf([
      makeRow({ key: "w", label: "w", state: "waiting" }),
      makeRow({ key: "e", label: "e", state: "stopped", archiveKind: "headless", ended: true }),
    ]),
  )[0];
  assert.equal(flyoutOf(ended, 1000).rest, "1 ended");
  const alone = buildTree(inputOf([makeRow({ key: "w", label: "w", state: "waiting" })]))[0];
  assert.equal(flyoutOf(alone, 1000).rest, "");
});

test("ProjectFlyout of a quiet project lists no rows, so a click on its tile opens the project's page", () => {
  const quiet = buildTree(inputOf([makeRow({ key: "s", label: "s" })]))[0];
  const model = flyoutOf(quiet, 1000);
  assert.deepEqual(model.rows, []);
  assert.equal(model.rest, "and 1 more, none waiting");
});

type DesignInit = MessageInitShape<typeof DesignSchema>;

// makeDesign is a design of the conversation sess-a, created at 10, unless the init says otherwise.
function makeDesign(init: DesignInit): Design {
  return create(DesignSchema, { id: "design", title: "Design", conversation: "sess-a", created: 10n, ...init });
}

// makeContract is a proposed contract of the repository /srv/app, proposed by sess-a, unless the init says otherwise.
function makeContract(id: string, init: MessageInitShape<typeof ContractSchema> = {}): Contract {
  return create(ContractSchema, {
    id,
    repository: "/srv/app",
    title: id,
    state: "proposed",
    version: 1n,
    conversation: "sess-a",
    ...init,
  });
}

// lineageProject is /srv/app with sess-a working design-x, whose contracts are beta at position 2 and the epic alpha at
// position 1, and sess-b working nothing; sess-a is the first row of the tree, since it is the working one.
function lineageProject(): Project {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", state: "working" }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/app", state: "done" }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-nobody", path: "/srv/app" })],
      details: new Map([["repo-a/alpha", makeDetail({})]]),
      contracts: [makeContract("repo-a/beta")],
      designs: [
        makeDesign({
          id: "design-x",
          title: "Tide alerts",
          contracts: [
            { id: "repo-a/beta", position: 2n },
            { id: "repo-a/alpha", position: 1n },
          ],
        }),
      ],
    }),
  );
  assert.equal(projects.length, 1, "the rows are one project");
  return projects[0];
}

test("RailLineage lists each conversation that works a design, with its design and that design's marks in position order", () => {
  assert.deepEqual(railLineage(lineageProject(), ""), [
    {
      key: "a",
      designs: [
        {
          key: "design:design-x",
          name: "Tide alerts",
          open: false,
          marks: [
            { key: "epic:repo-a/alpha", kind: "epic", name: "alpha", open: false },
            { key: "contract:repo-a/beta", kind: "contract", name: "beta", open: false },
          ],
        },
      ],
    },
  ]);
});

test("RailLineage lists two designs of one conversation in created order, each with its own marks", () => {
  const rows = [makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", state: "working" })];
  const project = buildTree(
    inputOf(rows, {
      contracts: [makeContract("repo-a/beta"), makeContract("repo-a/gamma")],
      designs: [
        makeDesign({ id: "design-late", title: "Late", created: 30n, contracts: [{ id: "repo-a/gamma", position: 1n }] }),
        makeDesign({ id: "design-early", title: "Early", created: 20n, contracts: [{ id: "repo-a/beta", position: 1n }] }),
      ],
    }),
  )[0];
  const [convo] = railLineage(project, "");
  assert.deepEqual(
    convo.designs.map((design) => [design.name, design.marks.map((mark) => mark.name)]),
    [
      ["Early", ["beta"]],
      ["Late", ["gamma"]],
    ],
  );
});

test("RailLineage gives a project whose conversations work no design no lineage", () => {
  const rows = [makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", state: "working" })];
  const project = buildTree(inputOf(rows))[0];
  assert.deepEqual(railLineage(project, ""), []);
});

test("RailLineage opens the design or the child whose key is open, and no other", () => {
  const project = lineageProject();
  const open = (key: string) => {
    const [convo] = railLineage(project, key);
    return [convo.designs[0].open, convo.designs[0].marks.map((mark) => mark.open)];
  };
  assert.deepEqual(open("design:design-x"), [true, [false, false]]);
  assert.deepEqual(open("epic:repo-a/alpha"), [false, [true, false]]);
  assert.deepEqual(open("contract:repo-a/beta"), [false, [false, true]]);
  assert.deepEqual(open("epic:repo-a/other"), [false, [false, false]]);
});

test("RailLineage is carried by each tile, with the open thing of the lineage the tiles are given", () => {
  const tiles = railTiles([lineageProject()], "", "contract:repo-a/beta");
  assert.deepEqual(
    tiles[0].lineage?.[0].designs[0].marks.map((mark) => [mark.name, mark.open]),
    [
      ["alpha", false],
      ["beta", true],
    ],
  );
  assert.deepEqual(railTiles([buildTree(inputOf([makeRow({ key: "s", label: "s" })]))[0]], "")[0].lineage, []);
});
