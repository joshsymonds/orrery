// The fixture world of round 6: the host, the directories and the conversations the fixture slices hold. The conversations
// are read the way the inbox reads them, through rowsFrom over the shipped slices, and the instant is the fixtures' own.

import assert from "node:assert/strict";
import { test } from "node:test";

import { rowsFrom, type Row } from "../rows";
import { buildTree, type Convo, type Project } from "../tree";
import { at, liveEpoch } from "./clock";
import { shippedSlices } from "./slices";

// rows are the fixture conversations, one row each, as load.ts reads the panes and the headless sessions.
const rows = rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions);

// treeInput is the fixture world the inbox tree places, as the page places it on the serving host.
const treeInput = {
  rows,
  epics: shippedSlices.epics.epics,
  details: shippedSlices.gets,
  contracts: shippedSlices.contracts.contracts,
  host: shippedSlices.host.host,
};
const inboxTree = buildTree({ ...treeInput, view: "inbox" });
const archivedTree = buildTree({ ...treeInput, view: "archived" });

// drawnConvo is the conversation the tree draws for the row key. It fails the test when the tree draws none.
function drawnConvo(tree: Project[], rowKey: string): Convo {
  const convo = tree.flatMap((project) => project.convos).find((candidate) => candidate.row?.key === rowKey);
  assert.ok(convo !== undefined, `the tree draws the conversation ${rowKey}`);
  return convo;
}

// titled is the fixture row whose title, its line, is title. The fixtures hold each title once.
function titled(title: string): Row {
  const found = rows.filter((row) => row.label === title);
  assert.equal(found.length, 1, `the fixtures hold one conversation titled "${title}"`);
  return found[0];
}

test("FixtureWorld: Host.Status serves host halyard", () => {
  assert.equal(shippedSlices.host.host, "halyard");
});

test("FixtureWorld: Host.Status reports the stats of round 6: cpu 12%, ram 14G, disk 49% and net ↓1M ↑298K", () => {
  assert.deepEqual(
    shippedSlices.host.stats.map((stat) => [stat.label, stat.value]),
    [["cpu", "12%"], ["ram", "14G"], ["disk", "49%"], ["net", "↓1M ↑298K"]],
  );
});

test("FixtureWorld: every pane sits on host halyard but kelp's, which sits on host brine", () => {
  const elsewhere = rows.filter((row) => row.kind === "pane" && row.host !== "halyard").map((row) => [row.host, row.label]);
  assert.deepEqual(elsewhere, [["brine", "Kelp bed survey importer"]]);
});

test("FixtureWorld: the conversations sit in the directories lighthouse, tidepool, commonframe, almanac, orrery, orchard, kelp and the home directory", () => {
  const projects = [...new Set(rows.map((row) => row.project))].sort();
  assert.deepEqual(projects, [
    "/home/owner",
    "/home/owner/code/almanac",
    "/home/owner/code/kelp",
    "/home/owner/code/lighthouse",
    "/home/owner/code/orchard",
    "/home/owner/code/orrery",
    "/home/owner/code/tidepool",
    "/home/owner/work/commonframe",
  ]);
});

test("FixtureWorld: the fixtures hold the 20 conversations: the 17 of round 6, the buoy-feeds steering session, and the two archived", () => {
  assert.equal(rows.length, 20);
});

// Each conversation by its title, its state and its directory. The directory is the row's project.
const conversations: [title: string, state: string, dir: string, project: string][] = [
  ["Retry backoff and jitter for the telemetry uploader", "working", "lighthouse", "/home/owner/code/lighthouse"],
  ["Planned the rota export, and steers its epic", "done", "lighthouse", "/home/owner/code/lighthouse"],
  ["Why the roster page is slow past forty shifts", "waiting", "tidepool", "/home/owner/code/tidepool"],
  ["Wrote the tide-alerts contract", "waiting", "tidepool", "/home/owner/code/tidepool"],
  ["Planned offline tables", "done", "tidepool", "/home/owner/code/tidepool"],
  ["Started from the shell: buoy-feeds", "done", "tidepool", "/home/owner/code/tidepool"],
  ["Planned holder scopes", "done", "tidepool", "/home/owner/code/tidepool"],
  ["Planned the tide cache", "done", "tidepool", "/home/owner/code/tidepool"],
  ["Tide table cache warm-up", "stopped", "tidepool", "/home/owner/code/tidepool"],
  ["Merging main into the story-out client branch", "waiting", "commonframe", "/home/owner/work/commonframe"],
  ["Phase endpoint returns ISO dates", "done", "almanac", "/home/owner/code/almanac"],
  ["The 2027 ephemeris import", "interrupted", "almanac", "/home/owner/code/almanac"],
  ["Follow-ups from the round-5 review", "done", "orrery", "/home/owner/code/orrery"],
  ["Parser tests for nested scion grafting", "done", "orchard", "/home/owner/code/orchard"],
  ["Kelp bed survey importer", "failed", "kelp", "/home/owner/code/kelp"],
  ["Swapping the phone’s font for a subset", "waiting", "the home directory", "/home/owner"],
  ["Steering buoy-feeds", "working", "tidepool", "/home/owner/code/tidepool"],
  ["Moon phase rounding at the date line", "done", "almanac", "/home/owner/code/almanac"],
  ["Leap-second table refresh", "done", "almanac", "/home/owner/code/almanac"],
];

for (const [title, state, dir, project] of conversations) {
  test(`FixtureWorld: "${title}" is ${state} in ${dir}`, () => {
    const row = titled(title);
    assert.equal(row.state, state, `"${title}" is ${state}`);
    assert.equal(row.project, project, `"${title}" sits in ${project}`);
  });
}

// The two archived sessions of almanac: each is done, archived, and seen after it changed, and its summary is the line it draws.
const archivedSessions: [title: string, changed: string, summary: string][] = [
  ["Moon phase rounding at the date line", "@-9d 14:00", "Fixed and merged."],
  ["Leap-second table refresh", "@-12d 14:00", "The table is current to 2027."],
];

for (const [title, changed, summary] of archivedSessions) {
  test(`FixtureWorld: "${title}" is an archived done session in almanac, changed ${changed} and seen after it`, () => {
    const row = titled(title);
    assert.deepEqual(
      [row.archived, row.archiveKind, row.state, row.project, row.changed, row.summary],
      [true, "headless", "done", "/home/owner/code/almanac", Number(at(changed)), summary],
    );
    assert.ok(row.seen > row.changed, `"${title}" is seen after it changed`);
  });
}

test('FixtureWorld: "zsh" is a terminal pane with no state in orrery', () => {
  const row = titled("zsh");
  assert.equal(row.kind, "pane");
  assert.equal(row.state, "");
  assert.equal(row.project, "/home/owner/code/orrery");
});

test("FixtureWorld: \"Why the roster page is slow past forty shifts\" changed four minutes before the fixtures' instant", () => {
  const instant = Date.parse(liveEpoch) / 1000;
  assert.equal(instant - titled("Why the roster page is slow past forty shifts").changed, 240);
});

// buoyFeedsSession is the headless session that steers tidepool/buoy-feeds.
const buoyFeedsSession = "6c2f9e14-3a7b-4d58-9b0e-2f7a1c8d4e55";
// moonSession is the archived almanac conversation Moon phase rounding, which steers almanac/moon-rounding.
const moonSession = "e7c4b2a1-5d9f-4e3a-9b61-2a8f0c4d7e13";
// warmupSession is the stopped tidepool conversation Tide table cache warm-up, which stays listed and steers
// tidepool/tide-warmup, an archived epic.
const warmupSession = "9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90";

// steering is each epic and the session that steers it. The session is a row of the fixtures by its archive id.
const steering: [epic: string, session: string][] = [
  ["lighthouse/rota-export", "sess-rota-14"],
  ["orrery/orrery-followups", "sess-follow-22"],
  ["orrery/orbit-labels", "sess-follow-22"],
  ["orrery/notices-usable", "sess-follow-22"],
  ["orrery/harbour-map", "sess-follow-22"],
  ["tidepool/tide-alerts", "sess-tide-16"],
  ["tidepool/offline-tables", "sess-offline-17"],
  ["orrery/holder-scopes", "sess-holder-19"],
  ["tidepool/tide-cache", "sess-cache-20"],
  ["tidepool/buoy-feeds", buoyFeedsSession],
  ["almanac/moon-rounding", moonSession],
  ["tidepool/tide-warmup", warmupSession],
];

for (const [epic, session] of steering) {
  test(`FixtureWorld: ${epic} is steered by ${session}, which is a row of the fixtures`, () => {
    const listed = shippedSlices.epics.epics.find((candidate) => candidate.id === epic);
    assert.ok(listed !== undefined, `the fixtures list ${epic}`);
    assert.equal(listed.steering, session, `the list row of ${epic}`);
    const detail = shippedSlices.gets.get(epic);
    assert.ok(detail !== undefined, `the fixtures hold the Epics.Get answer of ${epic}`);
    assert.equal(detail.epic?.steering, session, `the detail of ${epic}`);
    assert.ok(rows.some((row) => row.archiveId === session), `a row of the fixtures has the archive id ${session}`);
  });
}

test("FixtureWorld: the headless session Steering buoy-feeds runs two agents and one shell", () => {
  const stream = shippedSlices.sessionStreams.get(buoyFeedsSession);
  assert.ok(stream !== undefined, "the fixtures hold the conversation of Steering buoy-feeds");
  const running = (stream.state?.background ?? []).filter((task) => task.status === "running");
  assert.deepEqual(running.map((task) => [task.type, task.description]), [
    ["agent", "Map the buoy feed formats"],
    ["agent", "Check the station list against NOAA's"],
    ["shell", "just serve --port 8710"],
  ]);
});

test("InboxTree: the Archived view holds Moon phase rounding, archived, in almanac, with the archived moon-rounding epic under it", () => {
  const convo = drawnConvo(archivedTree, `session:${moonSession}`);
  const project = archivedTree.find((candidate) => candidate.convos.includes(convo));
  assert.equal(convo.row?.archived, true, "the conversation is archived");
  assert.equal(project?.dir, "/home/owner/code/almanac", "the conversation sits in almanac");
  assert.deepEqual(convo.hung.map((thing) => thing.key), ["epic:almanac/moon-rounding"]);
});

test("InboxTree: the Archived view draws the archived tide-warmup epic as a row of its own, since Tide table cache warm-up stays listed", () => {
  const own = archivedTree.flatMap((project) => project.convos).find((convo) => convo.key === "epic:tidepool/tide-warmup");
  assert.ok(own !== undefined, "the Archived view draws the tide-warmup epic as a row of its own");
  const project = archivedTree.find((candidate) => candidate.convos.includes(own));
  assert.equal(own.row, null, "no conversation is drawn above it");
  assert.equal(project?.dir, "/home/owner/code/tidepool", "the epic sits in tidepool");
  assert.deepEqual(own.hung.map((thing) => thing.key), ["epic:tidepool/tide-warmup"]);
  assert.equal(
    archivedTree.some((candidate) => candidate.convos.some((convo) => convo.row?.key === `session:${warmupSession}`)),
    false,
    "the conversation is not drawn in the Archived view, since it is not archived",
  );
});

test("InboxTree: the inbox draws neither archived epic under any conversation", () => {
  const archivedEpics = ["epic:almanac/moon-rounding", "epic:tidepool/tide-warmup"];
  const drawn = inboxTree
    .flatMap((project) => [...project.convos, ...project.ended])
    .flatMap((convo) => convo.hung.map((thing) => thing.key));
  assert.deepEqual(drawn.filter((key) => archivedEpics.includes(key)), []);
});

test("InboxTree: pane %12 has the orrery_id sess-beacon-12, whose Session is listed unarchived, so its row archives as that session", () => {
  const session = shippedSlices.sessions.sessions.find((candidate) => candidate.id === "sess-beacon-12");
  const row = rows.find((candidate) => candidate.key === "pane:halyard:%12");
  assert.deepEqual([session?.pane, session?.archived], ["%12", false], "the Session of the orrery_id is listed for pane %12");
  assert.deepEqual([row?.archiveId, row?.archiveKind, row?.archived], ["sess-beacon-12", "session", false]);
});
