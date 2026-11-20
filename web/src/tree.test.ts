import assert from "node:assert/strict";
import { test } from "node:test";

import { buildTree, projectOf, type Project, type View } from "./tree";
import { type Row } from "./rows";

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
