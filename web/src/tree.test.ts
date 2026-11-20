import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { buildTree, convoWaits, type Convo, projectOf, type Project, type TreeInput, type View, waits } from "./tree";
import { type Row } from "./rows";

const serving = "halyard";

function makeRow(init: Partial<Row> & Pick<Row, "key" | "label">): Row {
  return {
    putOff: false,
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

function treeOf(rows: Row[], view: View = "inbox"): Project[] {
  return buildTree({ rows, epics: [], details: new Map(), contracts: [], host: serving, view });
}

function labelsOf(project: Project): string[] {
  return project.convos.map((convo) => convo.row?.label ?? "");
}

test("InboxTree projectOf names a home, a path under home, and a path outside it", () => {
  assert.deepEqual(
    { name: projectOf("/home/owner", serving, serving).name, path: projectOf("/home/owner", serving, serving).path },
    { name: "~", path: "~" },
  );
  const underHome = projectOf("/home/owner/code/lighthouse", serving, serving);
  assert.deepEqual({ name: underHome.name, path: underHome.path, dir: underHome.dir }, {
    name: "lighthouse",
    path: "~/code/lighthouse",
    dir: "/home/owner/code/lighthouse",
  });
  const macHome = projectOf("/Users/owner/code/x", serving, serving);
  assert.deepEqual({ name: macHome.name, path: macHome.path }, { name: "x", path: "~/code/x" });
  const outside = projectOf("/srv/app/", serving, serving);
  assert.deepEqual({ name: outside.name, path: outside.path, dir: outside.dir }, {
    name: "app",
    path: "/srv/app",
    dir: "/srv/app",
  });
});

test("InboxTree projectOf folds a worktree into its repo and leaves a last worktree segment alone", () => {
  const folded = projectOf("/home/owner/code/tide.worktrees/off/sub", serving, serving);
  assert.deepEqual({ name: folded.name, path: folded.path, dir: folded.dir }, {
    name: "sub",
    path: "~/code/tide/sub",
    dir: "/home/owner/code/tide/sub",
  });
  const dashed = projectOf("/srv/tide-worktrees/off", serving, serving);
  assert.deepEqual({ name: dashed.name, path: dashed.path }, { name: "tide", path: "/srv/tide" });
  const bare = projectOf("/srv/tide.worktrees", serving, serving);
  assert.deepEqual({ name: bare.name, path: bare.path }, { name: "tide.worktrees", path: "/srv/tide.worktrees" });
});

test("InboxTree projectOf shows a host only when it is not the serving host", () => {
  const dir = "/srv/app";
  assert.equal(projectOf(dir, "brine", serving).host, "brine");
  assert.equal(projectOf(dir, serving, serving).host, "");
  assert.equal(projectOf(dir, "", serving).host, "");
});

test("InboxTree groups rows into one project per directory and host", () => {
  const rows = [
    makeRow({ key: "a", label: "one", project: "/home/owner/code/lighthouse" }),
    makeRow({ key: "b", label: "two", project: "/home/owner/code/lighthouse" }),
    makeRow({ key: "c", label: "three", project: "/home/owner/code/lighthouse", host: "brine" }),
    makeRow({ key: "d", label: "four", project: "/home/owner/code/lighthouse/docs" }),
  ];
  const projects = treeOf(rows);
  const byName = projects.map((project) => ({ name: project.name, host: project.host, count: project.convos.length }));
  assert.deepEqual(
    byName.sort((a, b) => a.name.localeCompare(b.name) || a.host.localeCompare(b.host)),
    [
      { name: "docs", host: "", count: 1 },
      { name: "lighthouse", host: "", count: 2 },
      { name: "lighthouse", host: "brine", count: 1 },
    ],
  );
});

test("InboxTree groups a worktree and a subdirectory under their own project", () => {
  const rows = [
    makeRow({ key: "a", label: "main", project: "/home/owner/code/tide" }),
    makeRow({ key: "b", label: "off", project: "/home/owner/code/tide.worktrees/off" }),
    makeRow({ key: "c", label: "dashed", project: "/home/owner/code/tide-worktrees/x" }),
    makeRow({ key: "d", label: "sub", project: "/home/owner/code/tide/sub" }),
    makeRow({ key: "e", label: "subwt", project: "/home/owner/code/tide.worktrees/y/sub" }),
  ];
  const projects = treeOf(rows);
  assert.equal(projects.length, 2);
  const tide = projects.find((project) => project.path === "~/code/tide");
  assert.deepEqual(tide && labelsOf(tide).sort(), ["dashed", "main", "off"]);
  const sub = projects.find((project) => project.path === "~/code/tide/sub");
  assert.deepEqual(sub && labelsOf(sub).sort(), ["sub", "subwt"]);
});

test("InboxTree puts a row with no directory in No directory, last", () => {
  const rows = [
    makeRow({ key: "a", label: "headless", project: "", host: "", kind: "session", archiveKind: "headless", state: "waiting" }),
    makeRow({ key: "b", label: "pane", project: "/srv/app", state: "done" }),
  ];
  const projects = treeOf(rows);
  const last = projects[projects.length - 1];
  assert.deepEqual({ key: last.key, name: last.name, dir: last.dir, path: last.path, host: last.host }, {
    key: "nodir",
    name: "No directory",
    dir: "",
    path: "",
    host: "",
  });
  assert.deepEqual(labelsOf(last), ["headless"]);
});

test("InboxTree leaves archived rows out of the inbox", () => {
  const rows = [
    makeRow({ key: "a", label: "live" }),
    makeRow({ key: "b", label: "gone", archived: true }),
  ];
  const labels = treeOf(rows).flatMap(labelsOf);
  assert.deepEqual(labels, ["live"]);
});

test("InboxTree archived view draws only the archived rows", () => {
  const rows = [
    makeRow({ key: "a", label: "live" }),
    makeRow({ key: "b", label: "gone", archived: true }),
  ];
  const labels = treeOf(rows, "archived").flatMap(labelsOf);
  assert.deepEqual(labels, ["gone"]);
});

test("InboxTree ranks conversations waiting, unseen, working, rest, newest first", () => {
  const rows = [
    makeRow({ key: "rest-old", label: "rest old", state: "done", changed: 5, seen: 5 }),
    makeRow({ key: "rest-new", label: "rest new", state: "done", changed: 40, seen: 40 }),
    makeRow({ key: "working", label: "working", state: "working", changed: 30, seen: 30 }),
    makeRow({ key: "unseen", label: "unseen", state: "done", changed: 20, seen: 10, unseen: true }),
    makeRow({ key: "waiting", label: "waiting", state: "waiting", changed: 10, seen: 10 }),
    makeRow({ key: "terminal", label: "terminal", state: "", changed: 50, seen: 50 }),
  ];
  const [project] = treeOf(rows);
  assert.deepEqual(labelsOf(project), [
    "waiting",
    "unseen",
    "working",
    "terminal",
    "rest new",
    "rest old",
  ]);
});

test("InboxTree ranks projects by their best conversation, then newest change", () => {
  const rows = [
    makeRow({ key: "rest", label: "rest", project: "/srv/rest", state: "done", changed: 100, seen: 100 }),
    makeRow({ key: "working-old", label: "working old", project: "/srv/working-old", state: "working", changed: 1, seen: 1 }),
    makeRow({ key: "working-new", label: "working new", project: "/srv/working-new", state: "working", changed: 9, seen: 9 }),
    makeRow({ key: "waiting", label: "waiting", project: "/srv/waiting", state: "waiting", changed: 1, seen: 1 }),
  ];
  assert.deepEqual(treeOf(rows).map((project) => project.name), ["waiting", "working-new", "working-old", "rest"]);
});

test("InboxTree shows each inbox row once", () => {
  const rows = [
    makeRow({ key: "a", label: "a", project: "/srv/one" }),
    makeRow({ key: "b", label: "b", project: "/srv/one" }),
    makeRow({ key: "c", label: "c", project: "" }),
    makeRow({ key: "d", label: "d", project: "/srv/two", archived: true }),
  ];
  const keys = treeOf(rows).flatMap((project) => project.convos.map((convo) => convo.key));
  assert.deepEqual([...keys].sort(), ["a", "b", "c"]);
});

type EpicInit = MessageInitShape<typeof EpicSchema>;
type ContractInit = MessageInitShape<typeof ContractSchema>;
type DetailInit = MessageInitShape<typeof EpicsGetResponseSchema>;

// makeEpic is a live epic at /srv/app, listed as repo-a/alpha, unless the init says otherwise.
function makeEpic(init: EpicInit): Epic {
  return create(EpicSchema, { id: "repo-a/alpha", repository: "repo-a", slug: "alpha", live: true, path: "/srv/app", ...init });
}

// makeContract is a proposed contract of repo-a/beta written at /srv/app, unless the init says otherwise.
function makeContract(init: ContractInit): Contract {
  return create(ContractSchema, { id: "repo-a/beta", repository: "/srv/app", title: "Beta", care: "serious", state: "proposed", ...init });
}

function makeDetail(init: DetailInit): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// inputOf is the tree input of the rows, in the inbox at the serving host, unless the init says otherwise.
function inputOf(rows: Row[], init: Partial<TreeInput> = {}): TreeInput {
  return { rows, epics: [], details: new Map(), contracts: [], host: serving, view: "inbox", ...init };
}

// projectAt is the project with the key: `${host}|${dir}`, or `repo:${repository}` for a repository's card.
function projectAt(projects: Project[], key: string): Project {
  const project = projects.find((candidate) => candidate.key === key);
  assert.ok(project !== undefined, `a project keyed ${key}`);
  return project;
}

// layout maps each conversation of a list to the keys of the things that hang under it.
function layout(convos: Convo[]): Record<string, string[]> {
  return Object.fromEntries(convos.map((convo) => [convo.key, convo.hung.map((hung) => hung.key)]));
}

// keysOf is every key the tree draws, in the tree and behind the ended lines.
function keysOf(projects: Project[]): string[] {
  return projects
    .flatMap((project) => [...project.convos, ...project.ended])
    .flatMap((convo) => convo.hung.map((hung) => hung.key));
}

// rowConvo is the conversation in the tree of the row with the label, or undefined.
function rowConvo(projects: Project[], label: string): Convo | undefined {
  return projects.flatMap((project) => project.convos).find((convo) => convo.row?.label === label);
}

test("InboxTree hangs an epic under the conversation that steers it, and nowhere else", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app" }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/other" }),
  ];
  // The epic's path is /srv/app, where a runs; placement follows the steering conversation b alone.
  const projects = buildTree(inputOf(rows, { epics: [makeEpic({ steering: "sess-b" })] }));
  assert.deepEqual(layout(projectAt(projects, "|/srv/app").convos), { a: [] });
  assert.deepEqual(layout(projectAt(projects, "|/srv/other").convos), { b: ["epic:repo-a/alpha"] });
  assert.deepEqual(keysOf(projects), ["epic:repo-a/alpha"]);
});

test("InboxTree moves an epic to the conversation that steers it when its steering changes", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app" }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/app" }),
  ];
  const before = buildTree(inputOf(rows, { epics: [makeEpic({ steering: "sess-a" })] }));
  const after = buildTree(inputOf(rows, { epics: [makeEpic({ steering: "sess-b" })] }));
  assert.deepEqual(layout(projectAt(before, "|/srv/app").convos), { a: ["epic:repo-a/alpha"], b: [] });
  assert.deepEqual(layout(projectAt(after, "|/srv/app").convos), { a: [], b: ["epic:repo-a/alpha"] });
});

test("InboxTree draws an epic and its proposed contract of one id once, as the epic carrying the contract", () => {
  const rows = [makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app" })];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ steering: "sess-a" })],
      contracts: [makeContract({ id: "repo-a/alpha", conversation: "sess-a" })],
    }),
  );
  assert.deepEqual(keysOf(projects), ["epic:repo-a/alpha"]);
  const node = rowConvo(projects, "a")?.hung[0];
  assert.equal(node?.kind === "epic" ? node.contract?.state : undefined, "proposed");
});

test("InboxTree hangs a contract under its conversation when proposed, or accepted with no listed epic of its id", () => {
  const rows = [makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app" })];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ steering: "sess-a" })],
      contracts: [
        makeContract({ id: "repo-a/beta", conversation: "sess-a" }),
        makeContract({ id: "repo-a/gamma", conversation: "sess-a", state: "accepted" }),
        makeContract({ id: "repo-a/alpha", conversation: "sess-a", state: "accepted" }),
        makeContract({ id: "repo-a/delta", conversation: "sess-a", state: "set_aside" }),
      ],
    }),
  );
  assert.deepEqual(layout(projectAt(projects, "|/srv/app").convos), {
    a: ["epic:repo-a/alpha", "contract:repo-a/beta", "contract:repo-a/gamma"],
  });
});

test("InboxTree stands an epic with no listed conversation under its project's unlisted stand-in, and no empty id matches a row", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app" }),
    makeRow({ key: "pane", label: "pane", archiveId: "", project: "/srv/app" }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "" }), makeEpic({ id: "repo-a/beta", steering: "sess-gone" })],
    }),
  );
  const app = projectAt(projects, "|/srv/app");
  assert.deepEqual(layout(app.convos), {
    a: [],
    pane: [],
    "standin:unlisted:|/srv/app": ["epic:repo-a/alpha", "epic:repo-a/beta"],
  });
  assert.equal(app.convos.find((convo) => convo.standIn === "unlisted")?.row, null);
});

test("InboxTree hangs an epic whose path is not absolute in a card named by its repository", () => {
  const projects = buildTree(
    inputOf([], { epics: [makeEpic({ id: "lighthouse/beta", repository: "lighthouse", slug: "beta", path: "" })] }),
  );
  const card = projectAt(projects, "repo:lighthouse");
  assert.deepEqual(
    { name: card.name, dir: card.dir, path: card.path, host: card.host },
    { name: "lighthouse", dir: "", path: "", host: "" },
  );
  assert.deepEqual(layout(card.convos), { "standin:unlisted:repo:lighthouse": ["epic:lighthouse/beta"] });
});

test("InboxTree stands a contract with no listed conversation under its repository's stand-in, or a repository card", () => {
  const projects = buildTree(
    inputOf([], {
      contracts: [
        makeContract({ id: "repo-c/one", repository: "/srv/app", conversation: "" }),
        makeContract({ id: "lighthouse/two", repository: "lighthouse", conversation: "sess-gone" }),
      ],
    }),
  );
  assert.deepEqual(layout(projectAt(projects, "|/srv/app").convos), { "standin:unlisted:|/srv/app": ["contract:repo-c/one"] });
  assert.deepEqual(layout(projectAt(projects, "repo:lighthouse").convos), {
    "standin:unlisted:repo:lighthouse": ["contract:lighthouse/two"],
  });
});

test("InboxTree puts what steers an archived conversation under its project's archived stand-in, out of the tree's rows", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", archived: true }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/app" }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-a" })],
      contracts: [makeContract({ id: "repo-a/beta", conversation: "sess-a" })],
    }),
  );
  assert.deepEqual(layout(projectAt(projects, "|/srv/app").convos), {
    b: [],
    "standin:archived:|/srv/app": ["epic:repo-a/alpha", "contract:repo-a/beta"],
  });
});

test("InboxTree leaves archived epics and archived conversations out of the inbox", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app" }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/app", archived: true }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [
        makeEpic({ id: "repo-a/alpha", steering: "sess-a", archived: true }),
        makeEpic({ id: "repo-a/beta", steering: "sess-b", archived: true }),
        makeEpic({ id: "repo-a/gamma", steering: "", archived: true }),
      ],
    }),
  );
  assert.deepEqual(keysOf(projects), []);
  assert.deepEqual(layout(projectAt(projects, "|/srv/app").convos), { a: [] });
});

test("InboxTree draws an ended epic behind the ended line, and its conversation again as the parent when it stays in the tree", () => {
  const rows = [makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", state: "working" })];
  const projects = buildTree(
    inputOf(rows, {
      epics: [
        makeEpic({ id: "repo-a/alpha", steering: "sess-a" }),
        makeEpic({ id: "repo-a/beta", steering: "sess-a", live: false }),
      ],
    }),
  );
  const app = projectAt(projects, "|/srv/app");
  assert.deepEqual(layout(app.convos), { a: ["epic:repo-a/alpha"] });
  assert.deepEqual(layout(app.ended), { a: ["epic:repo-a/beta"] });
  assert.equal(app.ended[0].again, true);
});

test("InboxTree moves a finished conversation that steers only ended epics behind the line, and keeps one that steers a live epic", () => {
  const rows = [
    makeRow({ key: "d", label: "d1", archiveId: "sess-d", project: "/srv/app", state: "done", changed: 10 }),
    makeRow({ key: "l", label: "l1", archiveId: "sess-l", project: "/srv/app", state: "done", changed: 20 }),
    makeRow({
      key: "h",
      label: "h1",
      archiveId: "sess-h",
      project: "",
      host: "",
      kind: "session",
      archiveKind: "headless",
      state: "stopped",
    }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-l" }), makeEpic({ id: "repo-a/beta", steering: "sess-d", live: false })],
    }),
  );
  const app = projectAt(projects, "|/srv/app");
  assert.deepEqual(layout(app.convos), { l: ["epic:repo-a/alpha"] });
  assert.deepEqual(layout(app.ended), { d: ["epic:repo-a/beta"] });
  assert.deepEqual(layout(projectAt(projects, "nodir").ended), { h: [] });
});

test("InboxTree keeps a headless session that failed unseen in the tree, and ends one that failed seen or stopped, newest first", () => {
  const rows = [
    makeRow({
      key: "h1",
      label: "h1",
      archiveId: "sess-1",
      project: "",
      host: "",
      kind: "session",
      archiveKind: "headless",
      state: "failed",
      unseen: true,
      changed: 10,
    }),
    makeRow({
      key: "h2",
      label: "h2",
      archiveId: "sess-2",
      project: "",
      host: "",
      kind: "session",
      archiveKind: "headless",
      state: "failed",
      changed: 30,
    }),
    makeRow({
      key: "h3",
      label: "h3",
      archiveId: "sess-3",
      project: "",
      host: "",
      kind: "session",
      archiveKind: "headless",
      state: "stopped",
      changed: 20,
    }),
  ];
  const nodir = projectAt(buildTree(inputOf(rows)), "nodir");
  assert.deepEqual(nodir.convos.map((convo) => convo.row?.label), ["h1"]);
  assert.deepEqual(nodir.ended.map((convo) => convo.row?.label), ["h2", "h3"]);
});

test("InboxTree ranks a conversation that waits through an epic's open question ahead of an older-changed one that does not", () => {
  const rows = [
    makeRow({ key: "x", label: "x", archiveId: "sess-x", project: "/srv/app", state: "done", changed: 10 }),
    makeRow({ key: "y", label: "y", archiveId: "sess-y", project: "/srv/app", state: "done", changed: 50 }),
  ];
  const details = new Map([["repo-a/alpha", makeDetail({ questions: [{ id: "Q1", text: "Use Postgres?" }] })]]);
  const projects = buildTree(inputOf(rows, { epics: [makeEpic({ steering: "sess-x" })], details }));
  assert.deepEqual(projectAt(projects, "|/srv/app").convos.map((convo) => convo.row?.label), ["x", "y"]);
});

test("InboxTree ranks a conversation that waits through a proposed contract it wrote ahead of a newer one", () => {
  const rows = [
    makeRow({ key: "p", label: "p", archiveId: "sess-p", project: "/srv/app", state: "done", changed: 10 }),
    makeRow({ key: "q", label: "q", archiveId: "sess-q", project: "/srv/app", state: "done", changed: 50 }),
  ];
  const projects = buildTree(inputOf(rows, { contracts: [makeContract({ conversation: "sess-p" })] }));
  assert.deepEqual(projectAt(projects, "|/srv/app").convos.map((convo) => convo.row?.label), ["p", "q"]);
});

test("InboxTree counts a live epic with an open question and a proposed contract as waiting, and an ended epic as not", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", state: "done" }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/app", state: "done" }),
    makeRow({ key: "w", label: "w", archiveId: "sess-w", project: "/srv/app", state: "waiting" }),
    makeRow({ key: "x", label: "x", archiveId: "sess-x", project: "/srv/app", state: "done" }),
  ];
  const questions = [{ id: "Q1", text: "Use Postgres?" }];
  const projects = buildTree(
    inputOf(rows, {
      epics: [
        makeEpic({ id: "repo-a/alpha", steering: "sess-a" }),
        makeEpic({ id: "repo-a/beta", steering: "sess-b", live: false }),
      ],
      contracts: [makeContract({ id: "repo-a/gamma", conversation: "sess-a", state: "accepted" })],
      details: new Map([
        ["repo-a/alpha", makeDetail({ questions })],
        ["repo-a/beta", makeDetail({ questions })],
      ]),
    }),
  );
  const app = projectAt(projects, "|/srv/app");
  assert.deepEqual(rowConvo(projects, "a")?.hung.map(waits), [true, false]);
  assert.deepEqual(app.ended.flatMap((convo) => convo.hung).map(waits), [false]);
  assert.deepEqual(
    Object.fromEntries(app.convos.map((convo) => [convo.row?.label ?? "", convoWaits(convo)])),
    { a: true, w: true, x: false },
  );
});

test("InboxTree archived view hangs each archived conversation's epics and contracts under it, and an archived epic under its live conversation", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", archived: true }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/app" }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      view: "archived",
      epics: [
        makeEpic({ id: "repo-a/alpha", steering: "sess-a" }),
        makeEpic({ id: "repo-a/beta", steering: "sess-b", archived: true }),
        makeEpic({ id: "repo-a/gamma", steering: "", archived: true }),
        makeEpic({ id: "repo-a/delta", steering: "sess-b" }),
      ],
      contracts: [makeContract({ id: "repo-a/one", conversation: "sess-a" }), makeContract({ id: "repo-a/two", conversation: "sess-b" })],
    }),
  );
  const app = projectAt(projects, "|/srv/app");
  assert.deepEqual(layout(app.convos), {
    a: ["epic:repo-a/alpha", "contract:repo-a/one"],
    b: ["epic:repo-a/beta"],
    "standin:unlisted:|/srv/app": ["epic:repo-a/gamma"],
  });
  assert.deepEqual(app.ended, []);
});

test("InboxTree puts an ended epic with no listed conversation behind the ended line, under its stand-in", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", state: "working" }),
    makeRow({ key: "z", label: "z", archiveId: "sess-z", project: "/srv/app", archived: true }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [
        makeEpic({ id: "repo-a/alpha", steering: "", live: false }),
        makeEpic({ id: "repo-a/beta", steering: "sess-z", live: false }),
      ],
    }),
  );
  assert.deepEqual(layout(projectAt(projects, "|/srv/app").ended), {
    "standin:unlisted:|/srv/app": ["epic:repo-a/alpha"],
    "standin:archived:|/srv/app": ["epic:repo-a/beta"],
  });
});

test("InboxTree carries only a proposed contract on the epic of its id", () => {
  const rows = [makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app" })];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ steering: "sess-a" })],
      contracts: [makeContract({ id: "repo-a/alpha", conversation: "sess-a", state: "accepted" })],
    }),
  );
  const node = rowConvo(projects, "a")?.hung[0];
  assert.equal(node?.kind === "epic" ? node.contract : undefined, null);
});

test("InboxTree keeps a stopped session that wrote an accepted contract with no epic in the tree, with the contract", () => {
  const rows = [
    makeRow({
      key: "h1",
      label: "h1",
      archiveId: "sess-1",
      project: "/srv/app",
      host: "",
      kind: "session",
      archiveKind: "headless",
      state: "stopped",
    }),
  ];
  const projects = buildTree(
    inputOf(rows, { contracts: [makeContract({ id: "repo-a/gamma", conversation: "sess-1", state: "accepted" })] }),
  );
  const app = projectAt(projects, "|/srv/app");
  assert.deepEqual(layout(app.convos), { h1: ["contract:repo-a/gamma"] });
  assert.deepEqual(app.ended, []);
});

test("InboxTree keeps an ended epic that carries a proposed contract in the tree, so its conversation waits", () => {
  const rows = [makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app" })];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ steering: "sess-a", live: false })],
      contracts: [makeContract({ id: "repo-a/alpha", conversation: "sess-a" })],
    }),
  );
  const app = projectAt(projects, "|/srv/app");
  assert.deepEqual(layout(app.convos), { a: ["epic:repo-a/alpha"] });
  assert.deepEqual(app.ended, []);
  assert.equal(convoWaits(app.convos[0]), true);
});

test("WaitsOnYou: a put-off waiting conversation ranks by its state, not as waiting", () => {
  const rows = [
    makeRow({ key: "put-off", label: "put off", state: "waiting", putOff: true, changed: 10, seen: 10 }),
    makeRow({ key: "working", label: "working", state: "working", changed: 1, seen: 1 }),
    makeRow({ key: "rest", label: "rest", state: "done", changed: 40, seen: 40 }),
  ];
  assert.deepEqual(labelsOf(treeOf(rows)[0]), ["working", "rest", "put off"]);
});

test("WaitsOnYou: an archived waiting conversation ranks by its state in the archived view, not as waiting", () => {
  const rows = [
    makeRow({ key: "waiting", label: "waiting", archiveId: "sess-w", state: "waiting", archived: true, changed: 10, seen: 10 }),
    makeRow({ key: "working", label: "working", archiveId: "sess-k", state: "working", archived: true, changed: 1, seen: 1 }),
    makeRow({ key: "rest", label: "rest", archiveId: "sess-r", state: "done", archived: true, changed: 40, seen: 40 }),
  ];
  assert.deepEqual(labelsOf(treeOf(rows, "archived")[0]), ["working", "rest", "waiting"]);
});
