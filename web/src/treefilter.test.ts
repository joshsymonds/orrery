import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { countsOf, narrow, projectWaiting, search, waitOrder } from "./treefilter";
import { buildTree, type Convo, type Project, type TreeInput } from "./tree";
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

// threeQuestions is an epic detail that asks three questions, so an epic with it has three needs.
const threeQuestions = makeDetail({
  questions: [
    { id: "Q1", text: "one?" },
    { id: "Q2", text: "two?" },
    { id: "Q3", text: "three?" },
  ],
});

// projectAt is the project keyed as the tree keys it: `${host}|${dir}`, or `repo:${repository}`, or "nodir".
function projectAt(projects: Project[], key: string): Project {
  const project = projects.find((candidate) => candidate.key === key);
  assert.ok(project !== undefined, `a project keyed ${key}`);
  return project;
}

// layout maps each conversation of a list to the keys of the things that hang under it.
function layout(convos: Convo[]): Record<string, string[]> {
  return Object.fromEntries(convos.map((convo) => [convo.key, convo.hung.map((hung) => hung.key)]));
}

test("InboxTree counts an epic with three questions once, and its waiting conversation once", () => {
  const details = new Map([["repo-a/alpha", threeQuestions]]);
  const epics = [makeEpic({ steering: "sess-a" })];
  const quiet = [makeRow({ key: "a", label: "a", archiveId: "sess-a", state: "done" })];
  const waiting = [...quiet, makeRow({ key: "w", label: "w", archiveId: "sess-w", state: "waiting" })];
  assert.equal(countsOf(buildTree(inputOf(quiet, { epics, details }))).waiting, 1);
  assert.equal(countsOf(buildTree(inputOf(waiting, { epics, details }))).waiting, 2);
});

test("InboxTree counts a proposed contract under a conversation that waits as covered, and one under a done conversation once", () => {
  const underWaiting = buildTree(
    inputOf([makeRow({ key: "w", label: "w", archiveId: "sess-w", state: "waiting" })], {
      contracts: [makeContract({ conversation: "sess-w" })],
    }),
  );
  assert.equal(countsOf(underWaiting).waiting, 1);
  const underDone = buildTree(
    inputOf([makeRow({ key: "d", label: "d", archiveId: "sess-d", state: "done" })], {
      contracts: [makeContract({ conversation: "sess-d" })],
    }),
  );
  assert.equal(countsOf(underDone).waiting, 1);
});

test("InboxTree counts an epic carrying a proposed contract once, and not again under its waiting conversation", () => {
  const contract = makeContract({ id: "repo-a/alpha", conversation: "sess-w" });
  const underWaiting = buildTree(
    inputOf([makeRow({ key: "w", label: "w", archiveId: "sess-w", state: "waiting" })], {
      epics: [makeEpic({ steering: "sess-w" })],
      contracts: [contract],
    }),
  );
  assert.equal(countsOf(underWaiting).waiting, 1);
  const underDone = buildTree(
    inputOf([makeRow({ key: "d", label: "d", archiveId: "sess-d", state: "done" })], {
      epics: [makeEpic({ steering: "sess-d" })],
      contracts: [makeContract({ id: "repo-a/alpha", conversation: "sess-d" })],
    }),
  );
  assert.equal(countsOf(underDone).waiting, 1);
});

test("InboxTree never counts an archived conversation or an archived epic as waiting", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", state: "waiting", archived: true }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", state: "done" }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      view: "archived",
      epics: [
        makeEpic({ id: "repo-a/beta", steering: "sess-b", archived: true }),
        makeEpic({ id: "repo-a/gamma", steering: "sess-a" }),
      ],
      details: new Map([
        ["repo-a/beta", threeQuestions],
        ["repo-a/gamma", threeQuestions],
      ]),
    }),
  );
  assert.equal(countsOf(projects).waiting, 0);
});

test("InboxTree counts a live epic and a proposed contract hung under an archived stand-in, each once, in countsOf and projectWaiting", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", archived: true }),
    makeRow({ key: "b", label: "b", archiveId: "sess-b", project: "/srv/app", state: "done" }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-a" })],
      details: new Map([["repo-a/alpha", threeQuestions]]),
      contracts: [makeContract({ id: "repo-a/beta", conversation: "sess-a" })],
    }),
  );
  assert.deepEqual(layout(projectAt(projects, "|/srv/app").convos), {
    b: [],
    "standin:archived:|/srv/app": ["epic:repo-a/alpha", "contract:repo-a/beta"],
  });
  assert.equal(countsOf(projects).waiting, 2);
  assert.equal(projectWaiting(projectAt(projects, "|/srv/app")), 2);
});

test("InboxTree counts each unseen row once, in the tree or behind the ended line", () => {
  const rows = [
    makeRow({ key: "u1", label: "u1", archiveId: "sess-u1", state: "done", unseen: true }),
    makeRow({ key: "x", label: "x", archiveId: "sess-x", state: "done", unseen: true }),
    makeRow({ key: "s", label: "s", archiveId: "sess-s", state: "done" }),
    makeRow({
      key: "h",
      label: "h",
      archiveId: "sess-h",
      project: "",
      host: "",
      kind: "session",
      archiveKind: "headless",
      state: "stopped",
      unseen: true,
    }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-x" }), makeEpic({ id: "repo-a/beta", steering: "sess-x", live: false })],
    }),
  );
  assert.equal(countsOf(projects).fresh, 3);
});

test("InboxTree a folded project's waiting number is the count over that project alone", () => {
  const rows = [
    makeRow({ key: "a", label: "a", archiveId: "sess-a", project: "/srv/app", state: "done" }),
    makeRow({ key: "w", label: "w", archiveId: "sess-w", project: "/srv/app", state: "waiting" }),
    makeRow({ key: "q", label: "q", archiveId: "sess-q", project: "/srv/quiet", state: "done" }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ steering: "sess-a" })],
      details: new Map([["repo-a/alpha", threeQuestions]]),
    }),
  );
  const app = projectAt(projects, "|/srv/app");
  const quiet = projectAt(projects, "|/srv/quiet");
  assert.equal(projectWaiting(app), 2);
  assert.equal(projectWaiting(quiet), 0);
  assert.equal(countsOf(projects).waiting, projects.reduce((sum, project) => sum + projectWaiting(project), 0));
});

test("InboxTree narrows to what waits, keeping each waiting conversation as the parent of what waits under it and dropping the ended line", () => {
  const rows = [
    makeRow({ key: "w", label: "w", archiveId: "sess-w", project: "/srv/app", state: "waiting", changed: 30 }),
    makeRow({ key: "d", label: "d", archiveId: "sess-d", project: "/srv/app", state: "done", changed: 20 }),
    makeRow({ key: "n", label: "n", archiveId: "sess-n", project: "/srv/app", state: "done", changed: 10 }),
    makeRow({ key: "q", label: "q", archiveId: "sess-q", project: "/srv/quiet", state: "done", changed: 5 }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [
        makeEpic({ id: "repo-a/alpha", steering: "sess-d" }),
        makeEpic({ id: "repo-a/beta", steering: "sess-d", live: false }),
        makeEpic({ id: "repo-a/gamma", steering: "sess-n", live: false }),
      ],
      details: new Map([["repo-a/alpha", threeQuestions]]),
    }),
  );
  assert.equal(projectAt(projects, "|/srv/app").ended.length, 2);
  const waiting = narrow(projects, "waiting");
  assert.deepEqual(waiting.map((project) => project.name), ["app"]);
  const app = waiting[0];
  assert.deepEqual(layout(app.convos), { w: [], d: ["epic:repo-a/alpha"] });
  assert.deepEqual(app.ended, []);
});

test("InboxTree narrows to unseen rows, each with nothing hung under it and no ended line", () => {
  const rows = [
    makeRow({ key: "u1", label: "u1", archiveId: "sess-u1", state: "done", unseen: true }),
    makeRow({ key: "x", label: "x", archiveId: "sess-x", state: "done", unseen: true }),
    makeRow({ key: "s", label: "s", archiveId: "sess-s", state: "done" }),
    makeRow({
      key: "h",
      label: "h",
      archiveId: "sess-h",
      project: "",
      host: "",
      kind: "session",
      archiveKind: "headless",
      state: "stopped",
      unseen: true,
    }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [makeEpic({ id: "repo-a/alpha", steering: "sess-x" }), makeEpic({ id: "repo-a/beta", steering: "sess-x", live: false })],
    }),
  );
  const fresh = narrow(projects, "new");
  const keys = fresh.flatMap((project) => project.convos.map((convo) => convo.key));
  assert.deepEqual(keys.sort(), ["h", "u1", "x"]);
  assert.deepEqual(fresh.flatMap((project) => project.convos.flatMap((convo) => convo.hung)), []);
  assert.deepEqual(fresh.flatMap((project) => project.ended), []);
});

test("InboxTree narrow with no filter returns the projects unchanged", () => {
  const rows = [makeRow({ key: "a", label: "a", archiveId: "sess-a", state: "done" })];
  const projects = buildTree(inputOf(rows));
  assert.deepEqual(narrow(projects, ""), projects);
});

test("InboxTree waitOrder lists each counted thing oldest first, ties by key, and skips the things the count skips", () => {
  const rows = [
    makeRow({ key: "w", label: "w", archiveId: "sess-w", state: "waiting", changed: 30 }),
    makeRow({ key: "a", label: "a", archiveId: "sess-a", state: "done", changed: 10 }),
    makeRow({ key: "z", label: "z", archiveId: "sess-z", state: "done", changed: 10 }),
  ];
  const projects = buildTree(
    inputOf(rows, {
      epics: [
        makeEpic({ id: "repo-a/alpha", steering: "sess-a", changed: 10n }),
        makeEpic({ id: "repo-a/gamma", steering: "sess-a", changed: 1n }),
      ],
      contracts: [
        makeContract({ id: "repo-a/beta", conversation: "sess-z", changed: 10n }),
        makeContract({ id: "repo-a/delta", conversation: "sess-w", changed: 1n }),
      ],
      details: new Map([["repo-a/alpha", threeQuestions]]),
    }),
  );
  assert.deepEqual(waitOrder(projects), ["contract:repo-a/beta", "epic:repo-a/alpha", "w"]);
});

test("InboxTree search ignores case and needs every word to match", () => {
  const projects = searchTree();
  assert.deepEqual(search(projects, ["SHIPPING"]).map((project) => project.name), ["lighthouse"]);
  assert.deepEqual(search(projects, ["shipping", "lighthouse"]).map((project) => project.name), ["lighthouse"]);
  assert.deepEqual(search(projects, ["shipping", "gamma"]), []);
});

test("InboxTree search keeps a matching thing's conversation and project, and only the things that match", () => {
  const projects = search(searchTree(), ["planning"]);
  assert.deepEqual(projects.map((project) => project.name), ["lighthouse"]);
  assert.deepEqual(layout(projects[0].convos), { a: ["epic:repo-a/beta"] });
});

test("InboxTree search keeps all of a conversation's things when the conversation itself matches", () => {
  const projects = search(searchTree(), ["alpha"]);
  assert.deepEqual(layout(projectAt(projects, "|/home/owner/code/lighthouse").convos), {
    a: ["epic:repo-a/alpha", "epic:repo-a/beta"],
  });
});

test("InboxTree search finds a contract by its title, a conversation by its state, and a project by its path", () => {
  const projects = searchTree();
  assert.deepEqual(layout(search(projects, ["gamma"]).flatMap((project) => project.convos)), {
    b: ["contract:repo-c/gamma"],
  });
  assert.deepEqual(layout(search(projects, ["waiting"]).flatMap((project) => project.convos)), {
    b: ["contract:repo-c/gamma"],
  });
  assert.deepEqual(search(projects, ["~/code"]).map((project) => project.name), ["lighthouse"]);
});

test("InboxTree search matches a row by its host and keeps that conversation with all its things", () => {
  const projects = searchTree();
  assert.deepEqual(layout(projectAt(search(projects, ["halyard"]), "|/home/owner/code/lighthouse").convos), {
    a: ["epic:repo-a/alpha", "epic:repo-a/beta"],
  });
  assert.deepEqual(layout(search(projects, ["brine"]).flatMap((project) => project.convos)), {
    b: ["contract:repo-c/gamma"],
  });
});

test("InboxTree search returns nothing when no word matches", () => {
  assert.deepEqual(search(searchTree(), ["zebra"]), []);
});

// searchTree is two projects: lighthouse, where conversation a steers alpha and beta, and /srv/other, where
// conversation b, waiting, writes the proposed contract gamma.
function searchTree(): Project[] {
  const rows = [
    makeRow({ key: "a", label: "alpha convo", archiveId: "sess-a", project: "/home/owner/code/lighthouse", host: "halyard" }),
    makeRow({ key: "b", label: "beta convo", archiveId: "sess-b", project: "/srv/other", host: "brine", state: "waiting" }),
  ];
  return buildTree(
    inputOf(rows, {
      epics: [
        makeEpic({ id: "repo-a/alpha", steering: "sess-a", slug: "alpha-work", outcome: "shipping" }),
        makeEpic({ id: "repo-a/beta", steering: "sess-a", slug: "beta-work", outcome: "planning" }),
      ],
      contracts: [makeContract({ id: "repo-c/gamma", title: "Gamma contract", conversation: "sess-b" })],
    }),
  );
}
