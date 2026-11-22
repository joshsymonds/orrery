// R2 of the round-6 hosts revision: the Starter takes a prompt, and proposes where to run it. The proposals are ranked
// host-and-folder pairs in a dropdown under the prompt, the top one preselected, and each shows its folder and its host.
// A host and a folder can be typed instead. No host that does not answer is ever offered. The wireframe is plain scripts
// that expect a window and a document, so this runs them in one node context with a stub of each, draws the Starter's
// states and the scenarios that show them, and asserts what each drawing holds.

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

// StateKey names the Starter's states as world.js keys them.
type StateKey = "prompt" | "loading" | "several" | "none" | "failed" | "typed";

// StarterState is the input of O.starterBody: the state, the prompt, and the place when there is one.
interface StarterState {
  state: string;
  prompt: string;
  host?: string;
  folder?: string;
}

// Proposal is one ranked host-and-folder pair the server answers with.
interface Proposal {
  host: string;
  folder: string;
  note: string;
}

// Scenario is one registered scenario: its group, its notes, and the drawings it has for each frame.
interface Scenario {
  id: string;
  group: string;
  note: string;
  differs: string[];
  desk?: () => string;
  phone?: () => string;
}

// Wireframe is the namespace the scripts build on window.O: the world they draw from and the parts a drawing is made of.
interface Wireframe {
  frame: string;
  W: {
    starter: {
      prompt: string;
      none: string;
      hosts: string[];
      proposals: Proposal[];
      states: Record<StateKey, StarterState>;
    };
    unreachableDirs(): { host?: string; unreachable?: boolean }[];
  };
  scenarios: Scenario[];
  starterBody(s: StarterState): string;
  starterPanel(s: StarterState): string;
  newPanel: (n: unknown) => string;
}

// Label is one HostLabel in a drawing: its host, and whether it is the card-heading form.
interface Label {
  name: string;
  heading: boolean;
}

const ROUND_SIX = new URL("../../visual/wireframe/round6/", import.meta.url);
const SYSTEM = new URL("../../visual/system/SYSTEM.md", import.meta.url);
const DECISIONS = new URL("../../visual/wireframe/round6/DECISIONS.md", import.meta.url);

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

const STATE_KEYS: StateKey[] = ["prompt", "loading", "several", "none", "failed", "typed"];

// The six start scenarios, each with the Starter state it draws.
const SCENARIO_STATE: [string, StateKey][] = [
  ["start-prompt", "prompt"],
  ["start-loading", "loading"],
  ["start-proposals", "several"],
  ["start-none", "none"],
  ["start-failed", "failed"],
  ["start-typed-folder", "typed"],
];

// stateOf is the Starter's input for one state of the world.
function stateOf(key: StateKey): StarterState {
  return O.W.starter.states[key];
}

// inFrame draws with the page's frame set, as the page sets it: the phone's drawings are the touch ones.
function inFrame(frame: "desk" | "phone", draw: () => string): string {
  O.frame = frame;
  return draw();
}

// scenarioOf is the registered scenario with that id; it fails the test when there is none.
function scenarioOf(id: string): Scenario {
  const scenario = O.scenarios.find((candidate) => candidate.id === id);
  assert.ok(scenario !== undefined, `the scenario ${id} is registered`);
  return scenario;
}

// drawOf draws a scenario's drawing for the frame it is for. It fails the test when the scenario draws no such frame.
function drawOf(id: string, frame: "desk" | "phone"): string {
  const scenario = scenarioOf(id);
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

const inline = (name: string): Label => ({ name, heading: false });

// dropdownOf is the proposals' dropdown in a drawing, or "" when the drawing has none.
function dropdownOf(html: string): string {
  return html.match(/<div class="starter-list"[\s\S]*?<\/div>/)?.[0] ?? "";
}

// optionsOf lists the dropdown's proposals, each as its markup, in the order drawn.
function optionsOf(html: string): string[] {
  return dropdownOf(html).match(/<button [\s\S]*?<\/button>/g) ?? [];
}

// inputValues lists the value of each filled field in a drawing: the prompt's first, then the folder's.
function inputValues(html: string): string[] {
  return [...html.matchAll(/<input value="([^"]*)"/g)].map((m) => m[1]);
}

// hostOptions lists the hosts in a drawing's host choice, in order.
function hostOptions(html: string): string[] {
  return [...html.matchAll(/<option[^>]*>([^<]*)<\/option>/g)].map((m) => m[1]);
}

// selectedHost is the host the host choice has selected, or undefined when it has none.
function selectedHost(html: string): string | undefined {
  return html.match(/<option selected>([^<]*)<\/option>/)?.[1];
}

// startOff reports whether the drawing's Start button is drawn disabled. It fails the test when there is no Start button.
function startOff(html: string): boolean {
  const at = html.indexOf("Start the session");
  assert.ok(at >= 0, "the drawing has a Start the session button");
  return / disabled/.test(html.slice(html.lastIndexOf("<button", at), at));
}

// sectionOf is the part of a markdown document from its heading to the next second-level heading.
function sectionOf(text: string, heading: string): string {
  const start = text.indexOf(heading);
  assert.ok(start >= 0, `the document has the heading ${heading}`);
  const end = text.indexOf("\n## ", start + 1);
  return end < 0 ? text.slice(start) : text.slice(start, end);
}

test("R2 Starter: the prompt is drawn alone, with nothing proposed and no place, and Start is off", () => {
  const html = O.starterBody(stateOf("prompt"));
  assert.ok(html.includes('placeholder="What should it do? Say it in your words"'), "the prompt asks what should be done");
  assert.equal(dropdownOf(html), "", "no proposal is drawn before the prompt is sent");
  assert.ok(!html.includes('class="starter-place"'), "no place is drawn yet");
  assert.ok(startOff(html), "Start is off until there is a place");
});

test("R2 Starter: while the place is asked for, the prompt is kept, the wait is said, and no proposal or place is drawn", () => {
  const html = O.starterBody(stateOf("loading"));
  assert.deepEqual(inputValues(html), [O.W.starter.prompt], "the prompt is kept");
  assert.ok(html.includes("Finding where to run it…"), "the wait is said");
  assert.equal(dropdownOf(html), "", "no proposal is drawn yet");
  assert.ok(!html.includes('class="starter-place"'), "no place is drawn yet");
  assert.ok(startOff(html), "Start waits for the answer");
});

test("R2 Starter: several proposals draw as a dropdown under the prompt, the top one preselected, each with its folder and its host", () => {
  const html = O.starterBody(stateOf("several"));
  // The wireframe runs in its own realm, so its arrays are copied here before they are compared.
  const proposals = [...O.W.starter.proposals];
  const options = optionsOf(html);
  assert.equal(options.length, proposals.length, "one row per proposal");
  assert.ok(
    options.every((option) => option.includes('role="option"')),
    "each row is an option in the dropdown, so another can be picked",
  );
  assert.deepEqual(
    options.map((option) => option.match(/<span class="t">([^<]*)<\/span>/)?.[1]),
    proposals.map((proposal) => proposal.folder),
    "each row names its folder, in the ranked order",
  );
  assert.deepEqual(
    options.map((option) => labelsIn(option)),
    proposals.map((proposal) => [inline(proposal.host)]),
    "each row draws its host",
  );
  assert.deepEqual(
    options.map((option) => option.includes('aria-selected="true"')),
    proposals.map((_, i) => i === 0),
    "only the top proposal is preselected",
  );
  assert.ok(options[0].includes('class="pick-row cur"'), "the top proposal is the current row");
  assert.ok(html.indexOf("<input value=") < html.indexOf('role="listbox"'), "the dropdown sits under the prompt");
});

test("R2 Starter: with the top proposal preselected, the place below is its host and its folder, and Start is on", () => {
  const html = O.starterBody(stateOf("several"));
  const top = O.W.starter.proposals[0];
  assert.equal(selectedHost(html), top.host, "the place's host is the top proposal's");
  assert.equal(inputValues(html)[1], top.folder, "the place's folder is the top proposal's");
  assert.ok(!startOff(html), "Start is on: there is a place");
});

test("R2 Starter: with no reachable folder that fits, it says so, keeps the prompt, leaves the folder empty, and Start is off", () => {
  const html = O.starterBody(stateOf("none"));
  assert.ok(html.includes("No reachable host has a folder that fits this."), "it says that nothing fits");
  assert.deepEqual(inputValues(html), [O.W.starter.none], "the prompt is kept, and no folder is filled in");
  assert.ok(html.includes('placeholder="A folder on that host"'), "the folder is asked for");
  assert.equal(dropdownOf(html), "", "no proposal is drawn");
  assert.ok(startOff(html), "Start is off until there is a folder");
});

test("R2 Starter: a failed ask keeps the prompt, says why, offers to try again, and draws no proposal with Start off", () => {
  const html = O.starterBody(stateOf("failed"));
  assert.deepEqual(inputValues(html), [O.W.starter.prompt], "the prompt is kept");
  assert.ok(html.includes("the server did not answer in 90 seconds"), "the reason is said");
  assert.ok(html.includes("<span>Try again</span>"), "the ask can be tried again");
  assert.equal(dropdownOf(html), "", "no proposal is drawn");
  assert.ok(startOff(html), "Start is off");
});

test("R2 Starter: a host chosen and a folder typed draws that host selected with that folder, no proposal, and Start on", () => {
  const typed = stateOf("typed");
  const html = O.starterBody(typed);
  assert.equal(selectedHost(html), typed.host, "the chosen host is selected");
  assert.equal(inputValues(html)[1], typed.folder, "the typed folder is in the folder field");
  assert.equal(dropdownOf(html), "", "no proposal is drawn once a place is typed");
  assert.ok(html.includes(`The folder is checked on ${typed.host} when the session starts.`), "the folder is checked on that host");
  assert.ok(!startOff(html), "Start is on: there is a place");
});

test("R2 Starter: no unreachable host is offered: the host choice lists the hosts that answer, and no state draws the one that does not", () => {
  const dirs = [...O.W.unreachableDirs()];
  const unreachable = dirs.filter((dir) => dir.unreachable === true).map((dir) => dir.host);
  assert.deepEqual(unreachable, ["brine"], "the world has one host that does not answer");
  const reachable = dirs.filter((dir) => dir.unreachable !== true).map((dir) => dir.host);
  assert.deepEqual(hostOptions(O.starterBody(stateOf("typed"))), reachable, "the host choice lists only the hosts that answer");
  for (const key of STATE_KEYS) {
    assert.ok(!O.starterBody(stateOf(key)).includes("brine"), `the ${key} state offers no proposal or host that does not answer`);
  }
});

test("R2 Starter: the six start scenarios are in New and find, drawn for the desk and the phone, and each names the server fields it reads", () => {
  for (const [id] of SCENARIO_STATE) {
    const scenario = scenarioOf(id);
    assert.equal(scenario.group, "New and find", `${id} is in New and find`);
    assert.ok(scenario.desk !== undefined && scenario.phone !== undefined, `${id} draws the desk and the phone`);
    for (const field of ["Picker.Ask", "orrery-host-relay R5", "Control.Start", "orrery-host-relay R4"]) {
      assert.ok(scenario.note.includes(field), `${id}'s note names ${field}`);
    }
  }
});

test("R2 Starter: each start scenario has a differs line for the web under 760, and one for Android", () => {
  for (const [id] of SCENARIO_STATE) {
    const { differs } = scenarioOf(id);
    assert.ok(differs.some((line) => line.includes("760")), `${id} says how the web under 760 differs`);
    assert.ok(differs.some((line) => line.includes("Android")), `${id} says how Android differs`);
  }
});

test("R2 Starter: each start scenario draws its own state, as a panel over the page on the desk and a sheet over the inbox on the phone", () => {
  for (const [id, key] of SCENARIO_STATE) {
    const desk = drawOf(id, "desk");
    assert.ok(desk.includes(inFrame("desk", () => O.starterPanel(stateOf(key)))), `the ${id} desk drawing is the ${key} panel`);
    assert.ok(!desk.includes("sheet-scrim"), `the ${id} desk drawing is a panel, not a sheet`);
    const phone = drawOf(id, "phone");
    assert.ok(phone.includes(inFrame("phone", () => O.starterPanel(stateOf(key)))), `the ${id} phone drawing is the ${key} sheet`);
    assert.ok(phone.includes("sheet-scrim"), `the ${id} phone drawing is a sheet`);
  }
});

test("R2 Starter: the catalogue draws Starter's Prompt, Loading, Several, None, Failed and TypedFolder, in that order", () => {
  const catalogue = written["[data-catalog]"];
  const at = ["Prompt", "Loading", "Several", "None", "Failed", "TypedFolder"].map((state) => catalogue.indexOf(`<b>Starter</b>/${state}`));
  assert.ok(
    at.every((index) => index >= 0),
    "each state has a cell",
  );
  assert.deepEqual(at, [...at].sort((a, b) => a - b), "the cells are in that order");
});

test("R2 Starter: the new-* scenarios and the New panel stay until the build epic removes them", () => {
  for (const id of ["new-empty", "new-working", "new-results", "new-form", "new-none", "new-error"]) {
    assert.ok(O.scenarios.some((candidate) => candidate.id === id), `${id} is still registered`);
  }
  assert.equal(typeof O.newPanel, "function", "the New panel is still drawn");
});

test("R2 Starter: the start-prompt note and DECISIONS.md say the Starter takes over starting a session from the new-* scenarios", () => {
  const note = scenarioOf("start-prompt").note;
  assert.ok(note.includes("takes over starting a session from the New panel"), "the note says the Starter takes over");
  assert.ok(note.includes("the build epic removes them"), "the note says the build epic removes the new-* scenarios");
  const decisions = sectionOf(readFileSync(DECISIONS, "utf8"), "## Revision 7: hosts").replace(/\s+/g, " ");
  assert.ok(decisions.includes("takes over starting a session from the `new-*` scenarios"), "DECISIONS.md says the Starter takes over");
  assert.ok(decisions.includes("the build epic removes them"), "DECISIONS.md says the build epic removes them");
});

test("R2 Starter: SYSTEM.md's components table has a Starter row naming its six states", () => {
  const row = readFileSync(SYSTEM, "utf8")
    .split("\n")
    .find((line) => line.startsWith("| Starter |"));
  assert.ok(row !== undefined, "the components table has a Starter row");
  assert.ok(
    row.split("|")[2].trim().startsWith("Prompt, Loading, Several, None, Failed, TypedFolder"),
    "the Starter row names its six states in order",
  );
});

test("R2 Starter: DECISIONS.md's Revision 7 table names the two server fields the Starter reads", () => {
  const rows = sectionOf(readFileSync(DECISIONS, "utf8"), "## Revision 7: hosts")
    .split("\n")
    .filter((line) => line.startsWith("|"));
  assert.ok(
    rows.some((row) => row.includes("`Picker.Ask`") && row.includes("orrery-host-relay R5")),
    "a row names the ranked proposals call beside Picker.Ask, and orrery-host-relay R5",
  );
  assert.ok(
    rows.some((row) => row.includes("`Control.Start`") && row.includes("orrery-host-relay R4")),
    "a row names the host Control.Start takes, and orrery-host-relay R4",
  );
});
