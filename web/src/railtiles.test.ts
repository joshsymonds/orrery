import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { type Epic, EpicSchema, EpicsGetResponseSchema, EpicStepSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { type Row } from "./rows";
import { railTiles, type RailTileModel } from "./railtiles";
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

test("InboxRail gives each tile its name's first two letters, or first and last when another name shares them", () => {
  const rows = [
    makeRow({ key: "a", label: "a", project: "/srv/lighthouse" }),
    makeRow({ key: "b", label: "b", project: "/srv/lilac" }),
    makeRow({ key: "c", label: "c", project: "/srv/tide" }),
  ];
  const tiles = railTiles(buildTree(inputOf(rows)), "");
  assert.deepEqual(
    tiles.map((tile) => tile.text),
    ["le", "lc", "ti"],
  );
});

test("InboxRail counts a project's waiting things as the tree does, including an epic under a stand-in", () => {
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
