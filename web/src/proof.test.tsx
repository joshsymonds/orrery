import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { render } from "preact";

import type { LoadArtifact } from "./evidence";
import {
  EpicContractSchema,
  type EpicRequirement,
  EpicRequirementSchema,
  type EvidenceRun,
  EvidenceRunSchema,
} from "./gen/orrery/v1/epics_pb";
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

// runCountsOf is how many evidence runs each proof card draws.
function runCountsOf(root: ParentNode): number[] {
  return [...root.querySelectorAll(".proof-card")].map((card) => card.querySelectorAll(".run").length);
}

// childTagsOf is the tag of each child of the first section a selector matches.
function childTagsOf(root: ParentNode, selector: string): string[] {
  const section = root.querySelector(selector);
  return section === null ? [] : [...section.children].map((child) => child.tagName);
}

test("the red proof has a card per requirement in order, each with its id, text, kind and conclusion when set", async () => {
  const drawn = await mounted(<RedProof requirements={[first, second, third]} loadArtifact={noArtifacts} />, (c) => ({
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
  const drawn = await mounted(<RedProof requirements={[first, second, third]} loadArtifact={noArtifacts} />, (c) => ({
    counts: runCountsOf(c),
    phases: textsOf(c, ".run .phase"),
    verdicts: textsOf(c, ".run .verdict"),
  }));
  assert.deepEqual(drawn.counts, [1, 0, 0]);
  assert.deepEqual(drawn.phases, ["red"]);
  assert.deepEqual(drawn.verdicts, ["fails"]);
});

test("the green proof draws the same cards, each with its green runs only", async () => {
  const drawn = await mounted(<GreenProof requirements={[first, second, third]} loadArtifact={noArtifacts} />, (c) => ({
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
  assert.equal(await sectionClass(<RedProof requirements={[first]} loadArtifact={noArtifacts} />), "proof red");
  assert.equal(await sectionClass(<RedProof requirements={[notPassed]} loadArtifact={noArtifacts} />), "proof red");
});

test("the red proof is dim with no requirements, or none with a red run", async () => {
  assert.equal(await sectionClass(<RedProof requirements={[]} loadArtifact={noArtifacts} />), "proof red dim");
  assert.equal(await sectionClass(<RedProof requirements={[second, third]} loadArtifact={noArtifacts} />), "proof red dim");
});

test("the green proof is reached only by a green run that passed", async () => {
  const redOnly = requirement({ id: "R4", text: "a red run that passed", runs: [redRun] });
  assert.equal(await sectionClass(<GreenProof requirements={[first]} loadArtifact={noArtifacts} />), "proof green");
  assert.equal(await sectionClass(<GreenProof requirements={[third]} loadArtifact={noArtifacts} />), "proof green dim");
  assert.equal(await sectionClass(<GreenProof requirements={[redOnly]} loadArtifact={noArtifacts} />), "proof green dim");
  assert.equal(await sectionClass(<GreenProof requirements={[]} loadArtifact={noArtifacts} />), "proof green dim");
});

test("the release proof draws each step with its target and undo, then the line after them", async () => {
  const drawn = await mounted(<ReleaseProof contract={contract} released={true} />, (c) => ({
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
  const drawn = await mounted(<ReleaseProof contract={contract} released={false} />, (c) => ({
    section: c.querySelector("section")?.className ?? null,
    steps: textsOf(c, ".release-step .step"),
  }));
  assert.equal(drawn.section, "proof release dim");
  assert.deepEqual(drawn.steps, ["tag the release", "publish the image"]);
});

test("the release proof with no contract is its heading alone, dim until released", async () => {
  const released = await mounted(<ReleaseProof contract={undefined} released={true} />, (c) => ({
    section: c.querySelector("section")?.className ?? null,
    text: c.textContent,
    steps: textsOf(c, ".release-step"),
  }));
  assert.equal(released.section, "proof release");
  assert.equal(released.text, "Release proof");
  assert.deepEqual(released.steps, []);
  assert.equal(
    await sectionClass(<ReleaseProof contract={undefined} released={false} />),
    "proof release dim",
  );
});

test("the report is its heading alone and always dim", async () => {
  const drawn = await mounted(<Report />, (c) => ({
    section: c.querySelector("section")?.className ?? null,
    text: c.textContent,
  }));
  assert.equal(drawn.section, "proof report dim");
  assert.equal(drawn.text, "Report");
});
