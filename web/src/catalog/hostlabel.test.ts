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
  rows: object[];
}

// Wireframe is the namespace the scripts build on window.O: the world they draw from and the parts a drawing is made of.
interface Wireframe {
  frame: string;
  W: {
    rows: Record<string, object>;
    epics: Record<string, object>;
    epic: Record<string, object>;
    project: object;
    rotaPin: object;
    workRota: { epics: object[] };
    dirs(): HostDir[];
    hostDirs(): HostDir[];
  };
  scenarios: { id: string; desk?: () => string; phone?: () => string }[];
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
