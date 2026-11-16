import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { LoadArtifact } from "./evidence";
import {
  EpicContractSchema,
  type EpicReleaseProof,
  EpicReleaseProofSchema,
  EpicReportSchema,
  type EpicRequirement,
  EpicRequirementSchema,
  type EvidenceRun,
  EvidenceRunSchema,
} from "./gen/orrery/v1/epics_pb";
import type { Phase, Wedge } from "./phases";
import { GreenProof, RedProof, Report, ReleaseProof } from "./proof";
import { installTestDom, removeTestDom } from "./testdom";

function run(init: MessageInitShape<typeof EvidenceRunSchema>): EvidenceRun {
  return create(EvidenceRunSchema, init);
}

function requirement(init: MessageInitShape<typeof EpicRequirementSchema>): EpicRequirement {
  return create(EpicRequirementSchema, init);
}

// revision is a full commit id; no test here reads it, only that each run has one.
const revision = "0123456789abcdef0123456789abcdef01234567";

// redRun is a red run that passed: its conclusion fails.
const redRun = run({
  id: "r-red",
  phase: "red",
  source: "captured",
  state: "ended",
  verdict: "fails",
  passed: true,
  exit: 1,
  revision,
});

// greenRun is a green run that passed: its conclusion holds.
const greenRun = run({
  id: "r-green",
  phase: "green",
  source: "captured",
  state: "ended",
  verdict: "holds",
  passed: true,
  exit: 0,
  revision,
});

// greenOpen is a green run that did not pass: its conclusion fails.
const greenOpen = run({
  id: "r-green-open",
  phase: "green",
  source: "captured",
  state: "ended",
  verdict: "fails",
  passed: false,
  exit: 0,
  revision,
});

// redOpen is a red run that did not pass: its conclusion holds.
const redOpen = run({
  id: "r-red-open",
  phase: "red",
  source: "captured",
  state: "ended",
  verdict: "holds",
  passed: false,
  exit: 0,
  revision,
});

// first has a red run and a green run, with a kind and a conclusion.
const first = requirement({
  id: "R1",
  text: "the red proof draws",
  kind: "check",
  conclusion: "the conclusion holds",
  runs: [redRun, greenRun],
});

// second has no runs and no kind or conclusion.
const second = requirement({ id: "R2", text: "the release draws", runs: [] });

// third has only a green run that did not pass, with a kind and no conclusion.
const third = requirement({ id: "R3", text: "the report stays dim", kind: "review", runs: [greenOpen] });

// contract has two release steps and the line after them.
const contract = create(EpicContractSchema, {
  title: "Ship the report",
  releaseSteps: [
    { step: "tag the release", target: "the git remote", undo: "delete the tag" },
    { step: "publish the image", target: "the registry", undo: "delete the image tag" },
  ],
  releaseThen: "The epic is released.",
});

// proofOf makes a release step's proof from its fields.
function proofOf(init: MessageInitShape<typeof EpicReleaseProofSchema>): EpicReleaseProof {
  return create(EpicReleaseProofSchema, init);
}

// passed is the first release step's proof: its run passed, with evidence and a reason.
const passed = proofOf({
  position: 1n,
  state: "passed",
  evidence: "tag v1.2.0 pushed",
  reason: "the remote accepted the tag",
  attempt: 1n,
  at: 9_900n,
});

// failed is the first release step's proof: its run failed, with evidence and a reason.
const failed = proofOf({
  position: 1n,
  state: "failed",
  evidence: "push rejected",
  reason: "the remote refused the tag",
  attempt: 1n,
  at: 9_900n,
});

// running is the second release step's proof: it runs, and no run has decided it, so it has no evidence or reason.
const running = proofOf({ position: 2n, state: "running" });

// waiting is the first release step's proof with no run yet.
const waiting = proofOf({ position: 1n, state: "waiting" });

// wedge is a task's wedge with the fields a proof card reads: its task and the requirements it names.
function wedge(task: string, requirements: string[]): Wedge {
  return { task, title: `the task ${task}`, requirements, place: "implementing", tone: "run", slices: [] };
}

// wedges name the requirements in order: T1 names R3, T2 and T3 both name R1, and no wedge names R2.
const wedges = [wedge("T1", ["R3"]), wedge("T2", ["R1"]), wedge("T3", ["R1"])];

// noArtifacts loads nothing. No test here opens a log or draws an image, so it is never called.
const noArtifacts: LoadArtifact = () => Promise.reject(new Error("no artifact is loaded in this test"));

// mounted renders a tree into a fresh container, returns what check reads from it, and removes the tree after.
async function mounted<T>(tree: JSX.Element, check: (container: HTMLElement) => T): Promise<T> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(tree, container);
    return check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// sectionClass is the class string of the section a tree draws.
function sectionClass(tree: JSX.Element): Promise<string | null> {
  return mounted(tree, (container) => container.querySelector("section")?.className ?? null);
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

// perCard is the text of the first match of a selector in each proof card, null for a card without one.
function perCard(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(".proof-card")].map((card) => textOf(card, selector));
}

// perStep is the text of the first match of a selector in each release step row, null for a row without one.
function perStep(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(".release-step")].map((row) => textOf(row, selector));
}

// runCountsOf is how many evidence runs each proof card draws.
function runCountsOf(root: ParentNode): number[] {
  return [...root.querySelectorAll(".proof-card")].map((card) => card.querySelectorAll(".run").length);
}

// childTagsOf is the tag of each child of the first section a selector matches.
function childTagsOf(root: ParentNode, selector: string): string[] {
  const section = root.querySelector(selector);
  return section === null ? [] : [...section.children].map((child) => child.tagName);
}

// pressed mounts the section a tree draws with a handler that records each open as "task phase", reads the role and
// class of each proof card, presses the cards at the given indexes in order, and returns what it read and what the
// presses opened.
async function pressed(
  tree: (onOpenPhase: (task: string, phase: Phase) => void) => JSX.Element,
  presses: number[],
): Promise<{ roles: (string | null)[]; classes: string[]; opened: string[] }> {
  const window = installTestDom();
  const container = document.createElement("div");
  const opened: string[] = [];
  try {
    render(tree((task, phase) => opened.push(`${task} ${phase}`)), container);
    const cards = [...container.querySelectorAll<HTMLElement>(".proof-card")];
    const roles = cards.map((card) => card.getAttribute("role"));
    const classes = cards.map((card) => card.className);
    await act(() => {
      for (const index of presses) {
        const card = cards[index];
        assert.ok(card, `proof card ${index} is drawn`);
        card.click();
      }
    });
    return { roles, classes, opened };
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("the red proof has a card per requirement in order, each with its id, text, kind and conclusion when set", async () => {
  const drawn = await mounted(<RedProof requirements={[first, second, third]} wedges={[]} loadArtifact={noArtifacts} />, (c) => ({
    heading: textOf(c, "section.red h3"),
    rids: perCard(c, ".rid"),
    texts: perCard(c, ".text"),
    kinds: perCard(c, ".kind"),
    conclusions: perCard(c, ".conclusion"),
  }));
  assert.equal(drawn.heading, "Red proof");
  assert.deepEqual(drawn.rids, ["R1", "R2", "R3"]);
  assert.deepEqual(drawn.texts, ["the red proof draws", "the release draws", "the report stays dim"]);
  assert.deepEqual(drawn.kinds, ["check", null, "review"]);
  assert.deepEqual(drawn.conclusions, ["the conclusion holds", null, null]);
});

test("the red proof draws each card's red runs only", async () => {
  const drawn = await mounted(<RedProof requirements={[first, second, third]} wedges={[]} loadArtifact={noArtifacts} />, (c) => ({
    counts: runCountsOf(c),
    phases: textsOf(c, ".run .phase"),
    verdicts: textsOf(c, ".run .verdict"),
  }));
  assert.deepEqual(drawn.counts, [1, 0, 0]);
  assert.deepEqual(drawn.phases, ["red"]);
  assert.deepEqual(drawn.verdicts, ["fails"]);
});

test("the green proof draws the same cards, each with its green runs only", async () => {
  const drawn = await mounted(<GreenProof requirements={[first, second, third]} wedges={[]} loadArtifact={noArtifacts} />, (c) => ({
    heading: textOf(c, "section.green h3"),
    counts: runCountsOf(c),
    phases: textsOf(c, ".run .phase"),
    passes: textsOf(c, ".run .pass"),
  }));
  assert.equal(drawn.heading, "Green proof");
  assert.deepEqual(drawn.counts, [1, 0, 1]);
  assert.deepEqual(drawn.phases, ["green", "green"]);
  assert.deepEqual(drawn.passes, ["passed", "not passed"]);
});

test("the red proof is reached by a red run, passed or not", async () => {
  const notPassed = requirement({ id: "R4", text: "a red run that did not pass", runs: [redOpen] });
  assert.equal(await sectionClass(<RedProof requirements={[first]} wedges={[]} loadArtifact={noArtifacts} />), "proof red");
  assert.equal(await sectionClass(<RedProof requirements={[notPassed]} wedges={[]} loadArtifact={noArtifacts} />), "proof red");
});

test("the red proof is dim with no requirements, or none with a red run", async () => {
  assert.equal(await sectionClass(<RedProof requirements={[]} wedges={[]} loadArtifact={noArtifacts} />), "proof red dim");
  assert.equal(await sectionClass(<RedProof requirements={[second, third]} wedges={[]} loadArtifact={noArtifacts} />), "proof red dim");
});

test("the green proof is reached only by a green run that passed", async () => {
  const redOnly = requirement({ id: "R4", text: "a red run that passed", runs: [redRun] });
  assert.equal(await sectionClass(<GreenProof requirements={[first]} wedges={[]} loadArtifact={noArtifacts} />), "proof green");
  assert.equal(await sectionClass(<GreenProof requirements={[third]} wedges={[]} loadArtifact={noArtifacts} />), "proof green dim");
  assert.equal(await sectionClass(<GreenProof requirements={[redOnly]} wedges={[]} loadArtifact={noArtifacts} />), "proof green dim");
  assert.equal(await sectionClass(<GreenProof requirements={[]} wedges={[]} loadArtifact={noArtifacts} />), "proof green dim");
});

test("the release proof draws each step with its target and undo, then the line after them", async () => {
  const drawn = await mounted(<ReleaseProof contract={contract} releaseSteps={[]} released={true} />, (c) => ({
    section: c.querySelector("section")?.className ?? null,
    steps: textsOf(c, ".release-step .step"),
    targets: textsOf(c, ".release-step .target"),
    undos: textsOf(c, ".release-step .undo"),
    then: textOf(c, "p.then"),
    children: childTagsOf(c, "section.release"),
  }));
  assert.equal(drawn.section, "proof release");
  assert.deepEqual(drawn.steps, ["tag the release", "publish the image"]);
  assert.deepEqual(drawn.targets, ["the git remote", "the registry"]);
  assert.deepEqual(drawn.undos, ["delete the tag", "delete the image tag"]);
  assert.equal(drawn.then, "The epic is released.");
  assert.deepEqual(drawn.children, ["H3", "OL", "P"]);
});

test("the release proof is dim until released, and draws its steps either way", async () => {
  const drawn = await mounted(<ReleaseProof contract={contract} releaseSteps={[]} released={false} />, (c) => ({
    section: c.querySelector("section")?.className ?? null,
    steps: textsOf(c, ".release-step .step"),
  }));
  assert.equal(drawn.section, "proof release dim");
  assert.deepEqual(drawn.steps, ["tag the release", "publish the image"]);
});

test("each release step draws its state, and its evidence and reason when a run has decided it", async () => {
  const drawn = await mounted(
    <ReleaseProof contract={contract} releaseSteps={[passed, running]} released={false} />,
    (c) => ({
      states: perStep(c, ".state"),
      evidence: perStep(c, ".evidence"),
      reasons: perStep(c, ".reason"),
    }),
  );
  assert.deepEqual(drawn.states, ["passed", "running"]);
  assert.deepEqual(drawn.evidence, ["tag v1.2.0 pushed", null]);
  assert.deepEqual(drawn.reasons, ["the remote accepted the tag", null]);
});

test("a release step's proof is matched by its position from 1, not by its place in the list", async () => {
  const drawn = await mounted(
    <ReleaseProof contract={contract} releaseSteps={[running, passed]} released={false} />,
    (c) => ({ states: perStep(c, ".state") }),
  );
  assert.deepEqual(drawn.states, ["passed", "running"]);
});

test("the release proof is reached by a step that is running or failed, and dim while every step waits", async () => {
  assert.equal(
    await sectionClass(<ReleaseProof contract={contract} releaseSteps={[running]} released={false} />),
    "proof release",
  );
  assert.equal(
    await sectionClass(<ReleaseProof contract={contract} releaseSteps={[failed]} released={false} />),
    "proof release",
  );
  assert.equal(
    await sectionClass(<ReleaseProof contract={contract} releaseSteps={[waiting]} released={false} />),
    "proof release dim",
  );
  assert.equal(
    await sectionClass(<ReleaseProof contract={contract} releaseSteps={[]} released={false} />),
    "proof release dim",
  );
});

test("the release proof with no contract is its heading alone, dim until released", async () => {
  const released = await mounted(<ReleaseProof contract={undefined} releaseSteps={[]} released={true} />, (c) => ({
    section: c.querySelector("section")?.className ?? null,
    text: c.textContent,
    steps: textsOf(c, ".release-step"),
  }));
  assert.equal(released.section, "proof release");
  assert.equal(released.text, "Release proof");
  assert.deepEqual(released.steps, []);
  assert.equal(
    await sectionClass(<ReleaseProof contract={undefined} releaseSteps={[]} released={false} />),
    "proof release dim",
  );
});

test("the report is its heading alone and dim until the epic writes one", async () => {
  const drawn = await mounted(<Report report={undefined} />, (c) => ({
    section: c.querySelector("section")?.className ?? null,
    text: c.textContent,
  }));
  assert.equal(drawn.section, "proof report dim");
  assert.equal(drawn.text, "Report");
});

test("a report shows its outcome, shipped and not-shipped ids and why, and is reached", async () => {
  const report = create(EpicReportSchema, {
    outcome: "released to production",
    shipped: ["R1", "R2"],
    notShipped: ["R3"],
    why: "R3 failed its green run",
  });
  const drawn = await mounted(<Report report={report} />, (c) => ({
    section: c.querySelector("section")?.className ?? null,
    heading: textOf(c, "section h3"),
    outcome: textOf(c, ".report .outcome"),
    shipped: textOf(c, ".report .shipped"),
    notShipped: textOf(c, ".report .not-shipped"),
    why: textOf(c, ".report .why"),
  }));
  assert.equal(drawn.section, "proof report");
  assert.equal(drawn.heading, "Report");
  assert.equal(drawn.outcome, "released to production");
  assert.equal(drawn.shipped, "shipped R1, R2");
  assert.equal(drawn.notShipped, "not shipped R3");
  assert.equal(drawn.why, "R3 failed its green run");
});

test("a report draws no line for an empty list", async () => {
  const report = create(EpicReportSchema, { outcome: "done", shipped: [], notShipped: ["R1"], why: "R1 has no green run" });
  const drawn = await mounted(<Report report={report} />, (c) => ({
    shipped: c.querySelector(".report .shipped") !== null,
    notShipped: textOf(c, ".report .not-shipped"),
  }));
  assert.equal(drawn.shipped, false);
  assert.equal(drawn.notShipped, "not shipped R1");
});

test("a red card is a button, and a press opens the first wedge naming its requirement in implementing", async () => {
  const drawn = await pressed(
    (onOpenPhase) => (
      <RedProof requirements={[first, second, third]} wedges={wedges} loadArtifact={noArtifacts} onOpenPhase={onOpenPhase} />
    ),
    [0, 2],
  );
  assert.deepEqual(drawn.roles, ["button", null, "button"]);
  assert.deepEqual(drawn.classes, ["proof-card open", "proof-card", "proof-card open"]);
  assert.deepEqual(drawn.opened, ["T2 implementing", "T1 implementing"]);
});

test("a green card is a button, and a press opens the first wedge naming its requirement in checking", async () => {
  const drawn = await pressed(
    (onOpenPhase) => (
      <GreenProof requirements={[first, third]} wedges={wedges} loadArtifact={noArtifacts} onOpenPhase={onOpenPhase} />
    ),
    [0, 1],
  );
  assert.deepEqual(drawn.roles, ["button", "button"]);
  assert.deepEqual(drawn.classes, ["proof-card open", "proof-card open"]);
  assert.deepEqual(drawn.opened, ["T2 checking", "T1 checking"]);
});

test("a card no wedge names is not a button, and a press on it opens nothing", async () => {
  const red = await pressed(
    (onOpenPhase) => (
      <RedProof requirements={[second, first]} wedges={wedges} loadArtifact={noArtifacts} onOpenPhase={onOpenPhase} />
    ),
    [0],
  );
  assert.deepEqual(red.roles, [null, "button"]);
  assert.deepEqual(red.classes, ["proof-card", "proof-card open"]);
  assert.deepEqual(red.opened, []);
  const green = await pressed(
    (onOpenPhase) => (
      <GreenProof requirements={[second]} wedges={wedges} loadArtifact={noArtifacts} onOpenPhase={onOpenPhase} />
    ),
    [0],
  );
  assert.deepEqual(green.roles, [null]);
  assert.deepEqual(green.classes, ["proof-card"]);
  assert.deepEqual(green.opened, []);
});

test("with no onOpenPhase no card is a button, and a press opens nothing", async () => {
  const red = await pressed(
    () => <RedProof requirements={[first, second, third]} wedges={wedges} loadArtifact={noArtifacts} />,
    [0, 2],
  );
  assert.deepEqual(red.roles, [null, null, null]);
  assert.deepEqual(red.classes, ["proof-card", "proof-card", "proof-card"]);
  assert.deepEqual(red.opened, []);
  const green = await pressed(
    () => <GreenProof requirements={[first, third]} wedges={wedges} loadArtifact={noArtifacts} />,
    [0, 1],
  );
  assert.deepEqual(green.roles, [null, null]);
  assert.deepEqual(green.classes, ["proof-card", "proof-card"]);
  assert.deepEqual(green.opened, []);
});
