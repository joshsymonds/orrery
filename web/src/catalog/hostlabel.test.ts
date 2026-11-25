// R1 of the round-6 hosts revision: every pane, headless session, epic and step draws its host, once on a card heading
// where its rows are grouped by host and folder, and inline elsewhere. The wireframe is plain scripts that expect a window
// and a document, so this runs them in one node context with a stub of each, draws the scenarios and the parts they are
// built from, and asserts where each HostLabel lands.

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

// HostDir is a directory card's data: the folder, its host and what it holds.
interface HostDir {
  name: string;
  path: string;
  host?: string;
  unreachable?: boolean;
  rows: object[];
}

// Wireframe is the namespace the scripts build on window.O: the world they draw from and the parts a drawing is made of.
interface Wireframe {
  frame: string;
  STATES: string[];
  W: {
    rows: Record<string, object>;
    epics: Record<string, object>;
    epic: Record<string, object>;
    project: object;
    rotaPin: object;
    workRota: { epics: object[] };
    dirs(): HostDir[];
    hostDirs(): HostDir[];
    unreachableDirs(): HostDir[];
  };
  scenarios: { id: string; note: string; desk?: () => string; phone?: () => string }[];
  stateGlyph(state: string): string;
  dock(d: object): string;
  sessionRow(row: object, o?: { card?: boolean }): string;
  epicRow(epic: object): string;
  epicPin(pin: object): string;
  workCard(epic: object): string;
  dir(dir: HostDir): string;
  epicPage(epic: object, o?: { record?: boolean; close?: string }): string;
  subPage(p: object): string;
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

// titleRowsOf is the title rows in a drawing, each as its markup.
function titleRowsOf(html: string): string {
  return (html.match(/<header class="titlerow">[\s\S]*?<\/header>/g) ?? []).join("");
}

// headsOf is the names of the card headings in a drawing, in order.
function headsOf(html: string): string[] {
  return labelsIn(html)
    .filter((label) => label.heading)
    .map((label) => label.name);
}

const inline = (name: string): Label => ({ name, heading: false });
const head = (name: string): Label => ({ name, heading: true });

test("R1 HostLabel: a terminal pane's title row draws its host inline, on the desk and on the phone", () => {
  for (const frame of ["desk", "phone"] as const) {
    assert.deepEqual(labelsIn(titleRowsOf(drawOf("convo-shell", frame))), [inline("halyard")], `the ${frame} title row of a pane`);
  }
});

test("R1 HostLabel: a headless session's InboxRow draws its host inline, and under a card it draws none", () => {
  assert.deepEqual(labelsIn(inFrame("desk", () => O.sessionRow(O.W.rows.lamp))), [inline("halyard")]);
  assert.deepEqual(labelsIn(inFrame("desk", () => O.sessionRow(O.W.rows.lamp, { card: true }))), []);
});

test("R1 HostLabel: an EpicRow draws its host inline, and each StepRow under it its own host, with one host and with several", () => {
  assert.deepEqual(labelsIn(inFrame("desk", () => O.epicRow(O.W.epics.rota))), [inline("halyard"), inline("halyard"), inline("halyard")]);
  assert.deepEqual(labelsIn(inFrame("desk", () => O.epicRow(O.W.epics.buoy))), [inline("sextant"), inline("sextant"), inline("lantern")]);
});

test("R1 HostLabel: an EpicPin and a WorkCard draw their epic's host inline", () => {
  assert.deepEqual(labelsIn(inFrame("desk", () => O.epicPin(O.W.rotaPin))), [inline("halyard")]);
  assert.deepEqual(labelsIn(inFrame("desk", () => O.workCard(O.W.workRota.epics[0]))), [inline("halyard")]);
});

test("R1 HostLabel: an epic's page title row draws its host, on the desk's record and on the phone, and the phone's steps name theirs", () => {
  assert.deepEqual(
    labelsIn(titleRowsOf(inFrame("desk", () => O.epicPage(O.W.epic.building, { record: true, close: "convo-epic" })))),
    [inline("halyard")],
    "the desk record title of an epic",
  );
  assert.deepEqual(
    labelsIn(drawOf("epic-hosts", "phone")),
    [inline("sextant"), inline("sextant"), inline("lantern")],
    "the phone epic page names its host, and each step its own",
  );
});

test("R1 HostLabel: a step's page title row draws its host, on the desk's record and on the phone", () => {
  const page = { label: "task", here: "gap-alerts", body: "", host: "lantern", crumbs: [], back: "epic-hosts" };
  assert.deepEqual(labelsIn(titleRowsOf(inFrame("desk", () => O.subPage({ ...page, record: true })))), [inline("lantern")], "the desk record");
  assert.deepEqual(labelsIn(titleRowsOf(inFrame("phone", () => O.subPage(page)))), [inline("lantern")], "the phone page");
});

test("R1 HostLabel: a card draws its host once on its head, and its rows draw none of their own, with one host", () => {
  const card: HostDir = { name: "lighthouse", path: "~/code", host: "halyard", rows: [O.W.rows.lamp] };
  assert.deepEqual(labelsIn(inFrame("desk", () => O.dir(card))), [head("halyard")]);
});

test("R1 HostLabel: with several hosts each card names its host once on its head, and what is under it inline", () => {
  const cards = O.W.hostDirs();
  assert.deepEqual(
    [...cards].flatMap((card) => headsOf(inFrame("desk", () => O.dir(card)))),
    ["halyard", "sextant", "lantern", "brine"],
  );
  assert.deepEqual(
    labelsIn(inFrame("desk", () => O.dir(cards[1]))),
    [head("sextant"), inline("sextant"), inline("sextant"), inline("lantern")],
    "the buoys card: its epic and its steps, each on its own host",
  );
});

test("R1 HostLabel: the inbox with several hosts names each card's host once on its head, on the desk and on the phone", () => {
  for (const frame of ["desk", "phone"] as const) {
    assert.deepEqual(headsOf(drawOf("inbox-hosts", frame)), ["halyard", "sextant", "lantern", "brine"], `the ${frame} inbox`);
  }
});

test("R1 HostLabel: the inbox at load names each card's host once on its head", () => {
  assert.deepEqual(headsOf(drawOf("inbox-home", "desk")), [...O.W.dirs()].map((dir) => dir.host));
});

test("R1 HostLabel: a project page names its host once on its head", () => {
  const projectHead = (html: string): string => (html.match(/<section class="proj-head">[\s\S]*?<\/section>/) ?? [""])[0];
  for (const frame of ["desk", "phone"] as const) {
    assert.deepEqual(labelsIn(projectHead(drawOf("project", frame))), [head("halyard")], `the ${frame} project head`);
  }
});

test("R1 HostLabel: the catalogue draws HostLabel inline, then as a card heading", () => {
  const catalogue = written["[data-catalog]"];
  const inlineAt = catalogue.indexOf("<b>HostLabel</b>/Inline");
  const headingAt = catalogue.indexOf("<b>HostLabel</b>/CardHeading");
  assert.ok(inlineAt >= 0 && headingAt > inlineAt, "the catalogue holds HostLabel's Inline state, then its CardHeading state");
  assert.deepEqual(labelsIn(catalogue.slice(inlineAt, headingAt)), [inline("halyard")]);
  assert.deepEqual(labelsIn(catalogue.slice(headingAt, catalogue.indexOf("</section>", headingAt))), [head("sextant")]);
});

// R4 Unreachable: a host that does not answer is not stopped. Its rows, panes, cards and steps say unreachable, in grey,
// with a glyph of their own, and the word sits beside the host. The SYSTEM.md State table carries the same state.

// UNREACH_WORD is the word unreachable as a host draws it beside itself.
const UNREACH_WORD = '<span class="unreach">unreachable</span>';

// unreachableDir is the brine card of the inbox where that host does not answer.
function unreachableDir(): HostDir {
  const dir = O.W.unreachableDirs().find((candidate) => candidate.host === "brine");
  assert.ok(dir !== undefined, "the inbox where a host does not answer has a card for brine");
  return dir;
}

test("R4 Unreachable: the glyph is a dashed ring of its own, worded unreachable, and not stopped's square", () => {
  const glyph = O.stateGlyph("unreachable");
  assert.ok(glyph.includes('data-state="unreachable"'), "the glyph is drawn as unreachable");
  assert.ok(glyph.includes('aria-label="unreachable"'), "its word is unreachable");
  assert.ok(glyph.includes("stroke-dasharray"), "its ring is dashed");
  assert.ok(!glyph.includes("<rect"), "it has no stopped square");
  assert.ok(O.STATES.includes("unreachable"), "unreachable is one of the states");
});

test("R4 Unreachable: rows and panes whose host is unreachable say unreachable, not stopped", () => {
  // A session row draws the word beside its host and no last words; under a card its line reads unreachable.
  const row = inFrame("desk", () => O.sessionRow(O.W.rows.unreach));
  assert.ok(row.includes('data-state="unreachable"') && row.includes(UNREACH_WORD), "a session row says unreachable beside its host");
  assert.ok(!row.includes('data-state="stopped"'), "a session row is not drawn as stopped");
  assert.ok(
    inFrame("desk", () => O.sessionRow(O.W.rows.unreach, { card: true })).includes('<span class="l2 unreach">unreachable</span>'),
    "under a card, a session's line reads unreachable",
  );

  // A card whose host does not answer says so on its head, and each conversation under it reads unreachable.
  const card = inFrame("desk", () => O.dir(unreachableDir()));
  assert.ok(card.includes(UNREACH_WORD), "the card head says unreachable beside its host");
  assert.ok(card.includes('data-state="unreachable"') && !card.includes('data-state="stopped"'), "its conversations read unreachable, not stopped");

  // An epic's step on that host reads unreachable, and its doing is not drawn, since it is not known now.
  const step = { ...(O.W.epics.rota as { steps: object[] }).steps[0], down: true };
  const epic = inFrame("desk", () => O.epicRow({ ...O.W.epics.rota, steps: [step] }));
  assert.ok(epic.includes('data-state="unreachable"') && epic.includes(UNREACH_WORD), "a step on that host says unreachable beside its host");
  assert.ok(!epic.includes("gate just check"), "a step on that host draws no doing");
  assert.ok(!epic.includes('data-state="stopped"'), "a step on that host is not drawn as stopped");
});

test("R4 Unreachable: a pane's title row says unreachable, and its dock says so and offers nothing that would act on the pane", () => {
  for (const frame of ["desk", "phone"] as const) {
    const drawing = drawOf("pane-host-unreachable", frame);
    const dock = inFrame(frame, () => O.dock({ state: "unreachable", host: "halyard", what: "· shown as last stored" }));
    assert.ok(drawing.includes(dock), `the ${frame} dock of the pane is the unreachable dock`);
    assert.ok(dock.includes("unreachable: halyard does not answer"), `the ${frame} dock says unreachable: halyard does not answer`);
    for (const action of ["Send", "Interrupt", "Take all back", "Queued"]) {
      assert.ok(!dock.includes(action), `the ${frame} dock offers no ${action}`);
    }
  }
  const title = titleRowsOf(drawOf("pane-host-unreachable", "desk"));
  assert.ok(title.includes(UNREACH_WORD) && title.includes('data-state="unreachable"'), "the pane's title row says unreachable beside its host");
  assert.ok(!title.includes('data-state="stopped"'), "the pane's title row is not drawn as stopped");
});

test("R4 Unreachable: the inbox with one host down draws that card as unreachable on the desk and the phone", () => {
  assert.ok(
    drawOf("inbox-host-unreachable", "desk").includes(inFrame("desk", () => O.dir(unreachableDir()))),
    "the desk inbox draws the brine card as unreachable",
  );
  assert.ok(drawOf("inbox-host-unreachable", "phone").includes(UNREACH_WORD), "the phone inbox says unreachable beside the host");
});

test("R4 Unreachable: the catalogue draws the unreachable states of StateGlyph, InboxRow, Dir and TitleRow", () => {
  const catalogue = written["[data-catalog]"];
  for (const cell of ["<b>StateGlyph</b>/Unreachable", "<b>InboxRow</b>/Unreachable", "<b>Dir</b>/Unreachable", "<b>TitleRow</b>/UnreachablePane"]) {
    assert.ok(catalogue.includes(cell), `the catalogue holds ${cell}`);
  }
});

test("R4 Unreachable: the scenario notes name the server fields the drawings read", () => {
  const note = (id: string): string => {
    const scenario = O.scenarios.find((candidate) => candidate.id === id);
    assert.ok(scenario !== undefined, `the scenario ${id} is registered`);
    return scenario.note;
  };
  assert.ok(note("inbox-host-unreachable").includes("orrery-first-class-hosts"), "the inbox note names a session's unreachable state");
  assert.ok(note("pane-host-unreachable").includes("Pane.unreachable (orrery-host-surfaces"), "the pane note names a pane listed unreachable");
});

test("R4 Unreachable: SYSTEM.md's State table has an unreachable row, and says why it is not stopped", () => {
  const system = readFileSync(new URL("../../visual/system/SYSTEM.md", import.meta.url), "utf8");
  const start = system.indexOf("### State");
  assert.ok(start >= 0, "SYSTEM.md has a State section");
  const section = system.slice(start, system.indexOf("\n### ", start + 1));
  const row = section.split("\n").find((line) => line.startsWith("| unreachable |"));
  assert.ok(row !== undefined, "the State table has an unreachable row");
  assert.deepEqual(
    row.split("|").slice(1, -1).map((cell) => cell.trim()),
    ["unreachable", "`ink.3`", "dashed hollow ring", "unreachable: `<host>` does not answer"],
    "the row gives its state, colour, glyph and what the dock says",
  );
  assert.ok(section.includes("Unreachable is not stopped"), "the State section says why unreachable is not stopped");
});
