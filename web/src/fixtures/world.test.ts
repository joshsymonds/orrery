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

test("FixtureWorld: the fixtures hold the 17 conversations of round 6", () => {
  assert.equal(rows.length, 17);
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
