// The fixture world of round 6: the host, the directories and the conversations the fixture slices hold. The conversations
// are read the way the inbox reads them, through rowsFrom over the shipped slices, and the instant is the fixtures' own.

import assert from "node:assert/strict";
import { test } from "node:test";

import { rowsFrom, type Row } from "../rows";
import { liveEpoch } from "./clock";
import { shippedSlices } from "./slices";

// rows are the fixture conversations, one row each, as load.ts reads the panes and the headless sessions.
const rows = rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions);

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

test("FixtureWorld: the fixtures hold the 18 conversations of round 6", () => {
  assert.equal(rows.length, 18);
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
];

for (const [title, state, dir, project] of conversations) {
  test(`FixtureWorld: "${title}" is ${state} in ${dir}`, () => {
    const row = titled(title);
    assert.equal(row.state, state, `"${title}" is ${state}`);
    assert.equal(row.project, project, `"${title}" sits in ${project}`);
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
