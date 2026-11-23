// R3 of the round-6 hosts revision: the hosts view lists every host, one row each, with its name as a HostLabel, its load
// and memory percent as meters, and whether it answers. It opens from the inbox head, on the desk and the phone. The
// conversation head keeps its host and its context, and draws no meters. The wireframe is plain scripts that expect a
// window and a document, so this runs them in one node context with a stub of each, draws the drawings, and reads the
// catalogue and the two documents the change rewrites.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { createContext, runInContext } from "node:vm";

// The round-6 scripts in the order desk.html loads them, then the catalogue.
const SCRIPTS = [
  "h.js",
  "mark.js",
  "icons.js",
  "prims.js",
  "frame.js",
  "convo.js",
  "shell.js",
  "epic.js",
  "world.js",
  "world-epic.js",
  "scenarios-shell.js",
  "scenarios-convo.js",
  "scenarios-epic.js",
  "scenarios-more.js",
  "catalog.js",
];

// Host is one host as the hosts view draws it: its name, whether it answers, and its load and memory when it does.
interface Host {
  name: string;
  reachable: boolean;
  load?: { pct: number; level: string };
  memory?: { pct: number; level: string };
}

// Wireframe is the namespace the scripts build on window.O: the world the hosts view draws from and the parts it is made of.
interface Wireframe {
  W: {
    hostsAll: Host[];
    hosts: Host[];
    hostsOneDown: Host[];
  };
  scenarios: { id: string; group: string; note: string; desk?: () => string; phone?: () => string; differs?: string[] }[];
  stateGlyph(state: string): string;
  hostsView(hosts: Host[]): string;
  frame: string;
}

// Label is one HostLabel in a drawing: its host, and whether it is the card-heading form.
interface Label {
  name: string;
  heading: boolean;
}

const ROUND_SIX = new URL("../../visual/wireframe/round6/", import.meta.url);

// load runs the scripts in one context, whose window is the context itself, so window.O and O are the same. The document
// stub records what the catalogue writes to each selector.
function load(): { O: Wireframe; written: Record<string, string> } {
  const written: Record<string, string> = {};
  const sandbox: Record<string, unknown> = {
    document: {
      documentElement: { dataset: {}, style: {} },
      querySelector: (selector: string) => ({
        dataset: {},
        style: {},
        set innerHTML(html: string) {
          written[selector] = html;
        },
      }),
      querySelectorAll: () => [],
    },
    getComputedStyle: () => ({ getPropertyValue: () => "" }),
  };
  sandbox.window = sandbox;
  createContext(sandbox);
  for (const name of SCRIPTS) {
    runInContext(readFileSync(new URL(`js/${name}`, ROUND_SIX), "utf8"), sandbox, { filename: name });
  }
  return { O: sandbox.O as Wireframe, written };
}

const { O, written } = load();

// inFrame draws with the page's frame set, as the page sets it: the phone's drawings are the touch ones.
function inFrame(frame: "desk" | "phone", draw: () => string): string {
  O.frame = frame;
  return draw();
}

// drawOf draws a scenario's drawing for the frame it is for. It fails the test when the scenario draws no such frame.
function drawOf(id: string, frame: "desk" | "phone"): string {
  const scenario = O.scenarios.find((candidate) => candidate.id === id);
  assert.ok(scenario !== undefined, `the scenario ${id} is registered`);
  const draw = scenario[frame];
  assert.ok(draw !== undefined, `the scenario ${id} draws the ${frame}`);
  return inFrame(frame, draw);
}

// labelsIn lists the HostLabels in a drawing, in order: each host's name, and whether it is the card-heading form.
function labelsIn(html: string): Label[] {
  return [...html.matchAll(/<span class="host-label( heading)?">[\s\S]*?<span class="name">([^<]*)<\/span><\/span>/g)].map(
    (m) => ({ name: m[2], heading: m[1] !== undefined }),
  );
}

// rowsOf is the host rows of a hosts view, each as its markup, in order. A row holds no div of its own.
function rowsOf(html: string): string[] {
  return html.match(/<div class="host-row"[\s\S]*?<\/div>/g) ?? [];
}

// metersOf lists a host row's meters in order: each one's level, name and value.
function metersOf(row: string): string[][] {
  return [...row.matchAll(/<span class="meter" data-level="(\w+)"[^>]*><span class="name">(\w+)<\/span>[\s\S]*?<span class="value">([^<]*)<\/span>/g)].map(
    (m) => [m[1], m[2], m[3]],
  );
}

// stripOf is the head's strip in a drawing, as its markup.
function stripOf(html: string): string {
  return (html.match(/<div class="strip"[\s\S]*?<\/div>/) ?? [""])[0];
}

// titleRowsOf is the title rows in a drawing, each as its markup.
function titleRowsOf(html: string): string {
  return (html.match(/<header class="titlerow">[\s\S]*?<\/header>/g) ?? []).join("");
}

// sectionOf is the catalogue's section for a component, from its heading to the next component's section.
function sectionOf(catalogue: string, component: string): string {
  const start = catalogue.indexOf(`<section><h3>${component}<small>`);
  assert.ok(start >= 0, `the catalogue has a ${component} section`);
  const next = catalogue.indexOf("<section><h3>", start + 1);
  return catalogue.slice(start, next < 0 ? catalogue.length : next);
}

// rowCells reads a markdown table's row whose first cell is the given text, as its cells without the outer bars.
function rowCells(markdown: string, first: string): string[] {
  const line = markdown.split("\n").find((candidate) => candidate.startsWith(`| ${first} |`));
  assert.ok(line !== undefined, `there is a row for ${first}`);
  return line.split("|").slice(1, -1).map((cell) => cell.trim());
}

const inline = (name: string): Label => ({ name, heading: false });

test("R3 HostsView: the hosts view lists each host in order, one row each, with its name as a HostLabel", () => {
  for (const hosts of [O.W.hostsAll, O.W.hosts, O.W.hostsOneDown]) {
    const html = O.hostsView(hosts);
    assert.equal(rowsOf(html).length, hosts.length, "one row for each host");
    assert.deepEqual(
      rowsOf(html).map((row) => labelsIn(row)),
      [...hosts].map((host) => [inline(host.name)]),
      "each row names its host as a HostLabel, in the order the server lists them",
    );
  }
});

test("R3 HostsView: a reachable host's row draws its load and memory percent as meters, and says it is reachable", () => {
  const [halyard] = rowsOf(O.hostsView(O.W.hostsAll));
  assert.deepEqual(metersOf(halyard), [
    ["ok", "load", "12%"],
    ["ok", "memory", "44%"],
  ]);
  assert.ok(halyard.includes("width:12%") && halyard.includes("width:44%"), "each meter's bar is its percent");
  assert.ok(halyard.includes('<span class="reach">reachable</span>'), "the row says the host is reachable");
});

test("R3 HostsView: a stat at warn or crit takes that level, and an unreachable host draws no numbers", () => {
  const [, sextant] = rowsOf(O.hostsView(O.W.hosts));
  assert.deepEqual(
    metersOf(sextant),
    [
      ["warn", "load", "71%"],
      ["crit", "memory", "94%"],
    ],
    "sextant's load is warn and its memory is crit",
  );

  const brine = rowsOf(O.hostsView(O.W.hostsOneDown))[3];
  assert.ok(brine.includes(O.stateGlyph("unreachable")), "the row draws the unreachable glyph");
  assert.ok(brine.includes('<span class="unreach">unreachable</span>'), "the word unreachable sits beside its name");
  assert.deepEqual(metersOf(brine), [], "the row draws no meters");
  assert.ok(!brine.includes("%") && !brine.includes('class="reach"'), "the row draws no numbers and does not say reachable");
  assert.ok(!brine.includes('data-state="stopped"'), "the row is not drawn as stopped");
});

test("R3 HostsView: the other rows of a view with one host down are as they were", () => {
  const all = rowsOf(O.hostsView(O.W.hostsAll));
  const oneDown = rowsOf(O.hostsView(O.W.hostsOneDown));
  assert.deepEqual(oneDown.slice(0, 3), all.slice(0, 3), "halyard, sextant and lantern draw as they do with every host up");
});

test("R3 HostsView: the hosts view opens from a Hosts control in the inbox head, on the desk and on the phone", () => {
  for (const frame of ["desk", "phone"] as const) {
    const head = (drawOf("inbox-home", frame).match(/<header class="inbox-head">[\s\S]*?<\/header>/) ?? [""])[0];
    assert.ok(head.includes('href="#hosts"') && head.includes('aria-label="Hosts"'), `the ${frame} inbox head has a Hosts control`);
  }
});

test("R3 HostsView: the hosts and hosts-unreachable scenarios draw on both clients, each with a differs line under 760", () => {
  for (const id of ["hosts", "hosts-unreachable"]) {
    const scenario = O.scenarios.find((candidate) => candidate.id === id);
    assert.ok(scenario !== undefined, `the scenario ${id} is registered`);
    assert.equal(scenario.group, "Inbox", `${id} is in the Inbox group`);
    for (const frame of ["desk", "phone"] as const) {
      assert.ok(drawOf(id, frame).includes('<section class="hosts"'), `${id} draws the hosts view on the ${frame}`);
    }
    assert.ok(
      scenario.differs?.some((line) => line.includes("web under 760")),
      `${id} has a differs line for the web under 760`,
    );
  }
  assert.ok(drawOf("hosts-unreachable", "phone").includes('data-state="unreachable"'), "the phone draws brine as unreachable");
});

test("R3 HostsView: the conversation head draws its host as a HostLabel and its context, and no meters", () => {
  for (const id of ["convo-hot", "convo-other-host", "pane-host-unreachable"]) {
    const strip = stripOf(drawOf(id, "desk"));
    assert.ok(strip.length > 0, `the desk head of ${id} has a strip`);
    assert.ok(!strip.includes('class="meter"'), `the strip of ${id} draws no meters`);
    assert.ok(!titleRowsOf(drawOf(id, "desk")).includes('class="meter"'), `the desk title row of ${id} draws no meters`);
  }
  assert.deepEqual(labelsIn(stripOf(drawOf("convo-hot", "desk"))), [inline("halyard")], "the head keeps its host");
  assert.ok(stripOf(drawOf("convo-hot", "desk")).includes('class="ctx"'), "the head keeps the context");
  assert.deepEqual(labelsIn(stripOf(drawOf("convo-other-host", "desk"))), [inline("brine")], "another host is named too");
  assert.ok(!titleRowsOf(drawOf("convo-hot", "phone")).includes('class="meter"'), "the phone's title row draws no meters");
});

test("R3 HostsView: the catalogue keeps MeterStrip's four cells, draws none of their meters, and draws HostsView's three states", () => {
  const catalogue = written["[data-catalog]"];
  const meterStrip = sectionOf(catalogue, "MeterStrip");
  for (const state of ["HostAndContext", "HostOnly", "UnderLoad", "OtherHost"]) {
    assert.ok(meterStrip.includes(`<b>MeterStrip</b>/${state}`), `the catalogue holds MeterStrip/${state}`);
  }
  assert.ok(!meterStrip.includes('class="meter"'), "the MeterStrip cells draw no meters");

  const hostsView = sectionOf(catalogue, "HostsView");
  for (const state of ["AllReachable", "UnderLoad", "OneUnreachable"]) {
    assert.ok(hostsView.includes(`<b>HostsView</b>/${state}`), `the catalogue holds HostsView/${state}`);
  }
  assert.ok(hostsView.includes('data-state="unreachable"'), "the OneUnreachable cell draws its host as unreachable");
});

test("R3 HostsView: SYSTEM.md marks MeterStrip superseded by HostsView and has a HostsView row with its three states", () => {
  const system = readFileSync(new URL("../../visual/system/SYSTEM.md", import.meta.url), "utf8");
  const meterStrip = rowCells(system, "MeterStrip");
  assert.ok(meterStrip[1].includes("Superseded by HostsView, to be removed by the build epic."), "the MeterStrip row says it is superseded");

  const hostsRow = rowCells(system, "HostsView");
  assert.ok(hostsRow[1].startsWith("AllReachable, UnderLoad, OneUnreachable."), "the HostsView row names its three states");
});

test("R3 HostsView: DECISIONS.md's two host rows say the meters moved to the hosts view and that every host's stats come from Host.Status", () => {
  const decisions = readFileSync(new URL("../../visual/wireframe/round6/DECISIONS.md", import.meta.url), "utf8");
  const phoneHeader = rowCells(decisions, "Host meters stay in the phone's header.");
  assert.ok(
    phoneHeader[1].includes("The meters moved to the hosts view on both clients."),
    "the phone header row's now says the meters moved to the hosts view on both clients",
  );

  const otherHost = rowCells(decisions, "A session's meters on another host");
  assert.ok(
    otherHost[1].startsWith("Every host's load, memory and reach comes from `Host.Status`'s per-host list (orrery-host-surfaces R4"),
    "the other-host row says every host's load, memory and reach comes from Host.Status's per-host list",
  );
});

test("R3 HostsView: the Revision 7 table gains the hosts view's field, and the scenario notes name it", () => {
  const decisions = readFileSync(new URL("../../visual/wireframe/round6/DECISIONS.md", import.meta.url), "utf8");
  const revision7 = decisions.slice(decisions.indexOf("## Revision 7: hosts"), decisions.indexOf("\n## ", decisions.indexOf("## Revision 7: hosts") + 1));
  assert.ok(
    revision7.split("\n").some((line) => line.startsWith("| the hosts view, opened from the inbox head") && line.includes("orrery-host-surfaces R4")),
    "the Revision 7 table has the hosts view row with its server field",
  );

  for (const id of ["hosts", "hosts-unreachable"]) {
    const scenario = O.scenarios.find((candidate) => candidate.id === id);
    assert.ok(scenario !== undefined, `the scenario ${id} is registered`);
    assert.ok(
      scenario.note.includes("Host.Status lists every host with load percent, memory percent and reachable (orrery-host-surfaces R4"),
      `the ${id} note names the Host.Status fields`,
    );
  }
});
