// R5 of the round-6 hosts revision: every host value a drawing shows names the server field it reads and the epic that
// delivers it, and the catalogue, SYSTEM.md and round6.json carry the host components and scenario ids. The wireframe is
// plain scripts that expect a window and a document, so this runs them in one node context with a stub of each, as the
// other host tests do, and reads the markup each drawing writes for the host values in it.

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

// Scenario is one registered scenario: its id, its note, and the drawing it has for each frame.
interface Scenario {
  id: string;
  note: string;
  desk?: () => string;
  phone?: () => string;
}

// Wireframe is the namespace the scripts build on window.O: the field each kind of host value reads, and the scenarios.
interface Wireframe {
  frame: string;
  HOST_FROM: Record<string, string>;
  scenarios: Scenario[];
}

// Names is the round6.json the names driver writes: each component with its states, and each scenario with its frames.
interface Names {
  components: Record<string, string[]>;
  scenarios: Record<string, string[]>;
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
const NAMES = JSON.parse(readFileSync(new URL("../../src/catalog/round6.json", import.meta.url), "utf8")) as Names;

// The kinds of host value a drawing shows, each named by the wireframe's own field note for it (HOST_FROM). A session's
// and a pane's value is the host it runs on; the others read the unreachable state, the hosts view, the Starter, or a
// title row, whose kind of page is not visible in its markup, so any of the kinds that page could show will do.
const REQUIRED: Record<string, string[]> = {
  session: ["session"],
  sessionDown: ["sessionDown"],
  epic: ["epic"],
  step: ["step"],
  hostDown: ["hostDown"],
  hosts: ["hosts"],
  proposal: ["proposal"],
  start: ["start"],
  titleHost: ["pane", "session", "epic", "step"],
  titleDown: ["paneDown", "sessionDown"],
  otherHost: ["pane", "session", "epic", "step"],
};

// SHOWN is what a drawing must show for a note to name each field: a title row's host is a page's, whichever kind of page
// it is, so a title shows any of the four page kinds. The Starter's proposal and start fields are left out: the Starter
// reads them in every one of its states, and its notes name them whatever its drawing shows, as the Starter's own test says.
const SHOWN: Record<string, string[]> = {
  session: ["session", "titleHost", "otherHost"],
  pane: ["titleHost", "otherHost"],
  epic: ["epic", "titleHost", "otherHost"],
  step: ["step", "titleHost"],
  sessionDown: ["sessionDown"],
  paneDown: ["titleDown"],
  hostDown: ["hostDown"],
  hosts: ["hosts"],
};

// unshownFields lists the fields a note names that its drawing does not show, so a note names only what is drawn.
function unshownFields(note: string, kinds: Set<string>): string[] {
  return Object.keys(SHOWN).filter((field) => {
    const text = O.HOST_FROM[field];
    return text !== undefined && note.includes(text) && !SHOWN[field].some((kind) => kinds.has(kind));
  });
}

// EPIC_OF is the host epic each field's server value comes from, as the wireframe's field notes name it.
const EPIC_OF: Record<string, string> = {
  pane: "orrery-host-surfaces",
  paneDown: "orrery-host-surfaces",
  session: "orrery-first-class-hosts",
  sessionDown: "orrery-first-class-hosts",
  epic: "orrery-host-relay",
  step: "orrery-host-relay",
  proposal: "orrery-host-relay",
  start: "orrery-host-relay",
  hosts: "orrery-host-surfaces",
  hostDown: "orrery-host-surfaces",
};

// sweep removes each match of re from html, and calls found with its markup, so each kind of host value is counted once.
function sweep(html: string, re: RegExp, found: (markup: string) => void): string {
  return html.replace(re, (markup) => {
    found(markup);
    return "";
  });
}

// visibleDrawing is a desk drawing as a reader sees it: at rest the inbox is folded to its rail, and its rows are hidden.
function visibleDrawing(html: string): string {
  if (!html.includes('data-inbox="rail"')) return html;
  const start = html.indexOf('<nav class="inbox"');
  if (start < 0) return html;
  return html.slice(0, start) + html.slice(html.indexOf("</nav>", start) + "</nav>".length);
}

// hostKinds reads a drawing's markup for the host values in it, by where each sits: a card's head, a row, a title row,
// the hosts view, the Starter's proposals and host choice. A host label it cannot place is an otherHost.
function hostKinds(html: string): Set<string> {
  const kinds = new Set<string>();
  let rest = html;
  // A card's head, and a project page's: the host of its headless sessions, and unreachable when it does not answer.
  rest = sweep(rest, /<span class="host-label heading">[\s\S]*?<\/span><\/span>(<span class="unreach">)?/g, (markup) => {
    kinds.add("session");
    if (markup.includes('<span class="unreach">')) kinds.add("hostDown");
  });
  // A row: a session's, an epic's or a step's. A step or a session that does not answer reads unreachable in its glyph.
  rest = sweep(rest, /<a class="row[^"]*"[\s\S]*?<\/a>/g, (markup) => {
    const classes = (markup.match(/^<a class="([^"]*)"/) ?? ["", ""])[1].split(" ");
    const type = classes.includes("step") ? "step" : classes.includes("epic") ? "epic" : "session";
    if (markup.includes('class="host-label')) kinds.add(type);
    if (markup.includes('data-state="unreachable"')) kinds.add(type === "step" ? "hostDown" : "sessionDown");
  });
  // The hosts view: a row for each host, and unreachable when that host does not answer.
  rest = sweep(rest, /<div class="host-row"[\s\S]*?<\/div>/g, (markup) => {
    kinds.add("hosts");
    if (markup.includes('data-state="unreachable"')) kinds.add("hostDown");
  });
  // The Starter's ranked proposals, and its host choice.
  rest = sweep(rest, /<div class="starter-list"[\s\S]*?<\/div>/g, () => kinds.add("proposal"));
  rest = sweep(rest, /<label class="field host-choice">[\s\S]*?<\/label>/g, () => kinds.add("start"));
  // A title row: the page's host, and unreachable when its host does not answer.
  rest = sweep(rest, /<header class="titlerow">[\s\S]*?<\/header>/g, (markup) => {
    if (!markup.includes('class="host-label')) return;
    kinds.add("titleHost");
    if (markup.includes('class="unreach"') || markup.includes('data-state="unreachable"')) kinds.add("titleDown");
  });
  if (rest.includes('class="host-label')) kinds.add("otherHost");
  return kinds;
}

// missingFields lists the kinds a drawing shows whose field the note does not name.
function missingFields(note: string, kinds: Set<string>): string[] {
  const named = (field: string): boolean => {
    const text = O.HOST_FROM[field];
    return text !== undefined && note.includes(text);
  };
  return [...kinds].filter((kind) => !REQUIRED[kind].some(named));
}

// drawingOf draws a scenario for one frame, as that frame shows it: the desk with its inbox as it rests.
function drawingOf(scenario: Scenario, frame: "desk" | "phone"): string | undefined {
  const draw = scenario[frame];
  if (draw === undefined) return undefined;
  O.frame = frame;
  return frame === "desk" ? visibleDrawing(draw()) : draw();
}

test("R5 DrawingFields: every host field the drawings name is one a host epic delivers", () => {
  for (const [kind, epic] of Object.entries(EPIC_OF)) {
    const field = O.HOST_FROM[kind];
    assert.ok(field !== undefined, `the field of ${kind} is named`);
    assert.ok(field.includes(`(${epic}`), `${kind} is read from ${epic}: ${field}`);
  }
});

test("R5 DrawingFields: each drawn host value names its server field and the epic that delivers it", () => {
  const drawn = new Set<string>();
  const failures: string[] = [];
  for (const scenario of O.scenarios) {
    const kinds = new Set<string>();
    for (const frame of ["desk", "phone"] as const) {
      const html = drawingOf(scenario, frame);
      if (html !== undefined) {
        for (const kind of hostKinds(html)) kinds.add(kind);
      }
    }
    for (const kind of kinds) drawn.add(kind);
    const missing = missingFields(scenario.note, kinds);
    if (missing.length > 0) failures.push(`${scenario.id} draws ${missing.join(", ")} but its note does not name the field`);
    const unshown = unshownFields(scenario.note, kinds);
    if (unshown.length > 0) failures.push(`${scenario.id} names ${unshown.join(", ")} but its drawing does not show it`);
  }
  assert.deepEqual(failures, [], "every scenario names the field and epic of each host value it draws, and only those");
  // The check reads the drawings: a scenario of each kind of value is in them, so none of it passes by reading nothing.
  for (const kind of ["session", "sessionDown", "epic", "step", "hostDown", "hosts", "proposal", "start", "titleDown"]) {
    assert.ok(drawn.has(kind), `a scenario draws a ${kind} value`);
  }
});

test("R5 DrawingFields: each catalogue component that draws a host value names its server field and epic", () => {
  const catalogue = written["[data-catalog]"];
  const failures: string[] = [];
  const sections = catalogue.split("<section><h3>").slice(1);
  for (const section of sections) {
    const headEnd = section.indexOf("</h3>");
    const head = section.slice(0, headEnd);
    const kinds = hostKinds(section.slice(headEnd));
    const name = head.slice(0, head.indexOf("<small>"));
    const missing = missingFields(head, kinds);
    if (missing.length > 0) failures.push(`${name} draws ${missing.join(", ")} but its description does not name the field`);
  }
  assert.ok(sections.length > 0, "the catalogue has component sections");
  assert.deepEqual(failures, [], "every catalogue component that draws a host value names its field and epic");
});

// The host components the host tasks added, with the states round6.json names for each.
const HOST_COMPONENTS = ["HostLabel", "HostsView", "Starter"];

const SYSTEM_ROWS = readFileSync(new URL("../../visual/system/SYSTEM.md", import.meta.url), "utf8").split("\n");

// listsState reads whether a components-table row names a state as a word of its own, so Pane is not found in UnreachablePane.
function listsState(row: string, state: string): boolean {
  return new RegExp(`\\b${state}\\b`).test(row);
}

// The states the host drawings add to components that had rows: an epic whose steps run on two hosts, and a terminal
// pane's title row. Their rows are the ones this change edits.
test("R5 DrawingFields: SYSTEM.md's EpicRow and TitleRow rows list the states the host drawings add", () => {
  const epicRow = SYSTEM_ROWS.find((line) => line.startsWith("| EpicRow |"));
  const titleRow = SYSTEM_ROWS.find((line) => line.startsWith("| TitleRow |"));
  assert.ok(epicRow !== undefined && titleRow !== undefined, "SYSTEM.md has an EpicRow row and a TitleRow row");
  assert.ok(listsState(epicRow, "StepsOnTwoHosts"), "the EpicRow row lists StepsOnTwoHosts");
  assert.ok(listsState(titleRow, "Pane"), "the TitleRow row lists Pane");
});

// HostLabel's row is the one no other test reads: it is the host the drawings name, with its two states.
test("R5 DrawingFields: SYSTEM.md has a HostLabel row, and it lists each state HostLabel draws", () => {
  const states = NAMES.components.HostLabel;
  assert.ok(states !== undefined, "round6.json names the HostLabel component");
  const row = SYSTEM_ROWS.find((line) => line.startsWith("| HostLabel |"));
  assert.ok(row !== undefined, "SYSTEM.md has a HostLabel row");
  for (const state of states) {
    assert.ok(listsState(row, state), `the HostLabel row lists ${state}`);
  }
});

test("R5 DrawingFields: SYSTEM.md's component rows list the Unreachable states the drawings add", () => {
  const unreachable = Object.entries(NAMES.components).filter(([, states]) => states.some((state) => state.includes("Unreachable")));
  assert.ok(unreachable.length > 0, "some component draws an unreachable state");
  for (const [name] of unreachable) {
    const row = SYSTEM_ROWS.find((line) => line.startsWith(`| ${name} |`));
    assert.ok(row !== undefined, `SYSTEM.md has a ${name} row`);
    assert.ok(row.includes("Unreachable"), `the ${name} row lists its Unreachable state`);
  }
});

test("R5 DrawingFields: round6.json names the host components and every scenario and cell the wireframe draws", () => {
  for (const name of HOST_COMPONENTS) {
    assert.ok(NAMES.components[name] !== undefined, `round6.json names the ${name} component`);
  }
  for (const scenario of O.scenarios) {
    const frames = (["desk", "phone"] as const).filter((frame) => scenario[frame] !== undefined);
    assert.deepEqual(NAMES.scenarios[scenario.id], frames, `round6.json names ${scenario.id} with the frames it draws`);
  }
  const cells = [...written["[data-catalog]"].matchAll(/<span class="name"><b>([^<]*)<\/b>\/([^<]*)<\/span>/g)];
  assert.ok(cells.length > 0, "the catalogue draws named cells");
  for (const [, name, state] of cells) {
    assert.ok(NAMES.components[name]?.includes(state), `round6.json names the ${name}/${state} cell`);
  }
});
