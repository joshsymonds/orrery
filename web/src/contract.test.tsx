import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import { ContractBody } from "./contract";
import {
  type EpicContract,
  EpicContractSchema,
  type EpicRequirement,
  EpicRequirementSchema,
} from "./gen/orrery/v1/epics_pb";
import { clock } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";

// acceptedAt is the unix second the synthetic contract was accepted at.
const acceptedAt = 1_783_000_000n;

// fields is a synthetic accepted contract, with a value in every field the page shows.
const fields = {
  title: "Show the accepted contract",
  approved: "approved by you",
  intent: "Show the accepted contract read-only.",
  failures: [
    { id: "F1", text: "The page drifts from the contract.", rating: "limited", response: "prevent: a test" },
    { id: "F2", text: "A field goes missing.", rating: "minor", response: "detect: the section test" },
  ],
  care: "Level of care set by F1 and F2.",
  additions: [
    { location: "web/src/contract.tsx", what: "a read-only body", why: "the summary opens it", cost: "one component" },
  ],
  premises: [{ id: "A1", text: "The contract is final once accepted.", ifWrong: "the page shows a stale copy." }],
  exclusions: ["no Accept button", "no editing"],
  approach: "Render the stored contract as text.",
  alternatives: [
    { alternative: "Render markdown", whyNot: "It would parse markup.", reconsider: "If the contract gains formatting." },
  ],
  releaseSteps: [{ step: "Deploy the web bundle", target: "the web host", undo: "redeploy the last bundle" }],
  releaseThen: "Nothing else leaves the machine.",
  checks: "go test ./...\njust check",
  base: "0123456789ab",
  accepted: acceptedAt,
};

// contract is the contract the init describes, as the page receives it.
function contract(init: MessageInitShape<typeof EpicContractSchema>): EpicContract {
  return create(EpicContractSchema, init);
}

const requirements: EpicRequirement[] = [
  create(EpicRequirementSchema, { id: "R1", text: "The page opens with the title.", evidence: "the title test" }),
  create(EpicRequirementSchema, { id: "R2", text: "No field is editable.", evidence: "the read-only test" }),
];

// headings are the section headings of the contract page body, in the contract's order.
const headings = [
  "What you asked for",
  "What could go wrong, and how much we care",
  "Things you did not ask for",
  "What will be true when done",
  "What I'm assuming",
  "What we won't do",
  "How, and why not the other ways",
  "What leaves this machine or can't be undone",
  "Checks the machines run",
];

async function withBody(value: EpicContract, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<ContractBody contract={value} requirements={requirements} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

// sectionTexts is the text of each element matching selector inside the section under heading, or no texts when no
// section has that heading.
function sectionTexts(root: ParentNode, heading: string, selector: string): (string | null)[] {
  const section = [...root.querySelectorAll("section")].find(
    (element) => element.querySelector("h3")?.textContent === heading,
  );
  return section === undefined ? [] : textsOf(section, selector);
}

test("the body opens with the title and approved line, then the sections' headings in the contract's order", () =>
  withBody(contract({ ...fields }), (container) => {
    assert.equal(container.querySelectorAll("article.contract").length, 1);
    assert.equal(textOf(container, "article.contract > h2"), fields.title);
    assert.equal(textOf(container, "article.contract > p.approved"), fields.approved);
    assert.deepEqual(textsOf(container, "article.contract h3"), headings);
  }));

test("each field's text lands in its section, row by row", () =>
  withBody(contract({ ...fields }), (container) => {
    assert.deepEqual(sectionTexts(container, headings[0], ".intent"), [fields.intent]);

    assert.deepEqual(sectionTexts(container, headings[1], ".row .id"), ["F1", "F2"]);
    assert.deepEqual(sectionTexts(container, headings[1], ".row .text"), [
      "The page drifts from the contract.",
      "A field goes missing.",
    ]);
    assert.deepEqual(sectionTexts(container, headings[1], ".row .rating"), ["limited", "minor"]);
    assert.deepEqual(sectionTexts(container, headings[1], ".row .response"), [
      "prevent: a test",
      "detect: the section test",
    ]);
    assert.deepEqual(sectionTexts(container, headings[1], ".care"), [fields.care]);

    assert.deepEqual(sectionTexts(container, headings[2], ".none"), []);
    assert.deepEqual(sectionTexts(container, headings[2], ".row .location"), ["web/src/contract.tsx"]);
    assert.deepEqual(sectionTexts(container, headings[2], ".row .what"), ["a read-only body"]);
    assert.deepEqual(sectionTexts(container, headings[2], ".row .why"), ["the summary opens it"]);
    assert.deepEqual(sectionTexts(container, headings[2], ".row .cost"), ["one component"]);

    assert.deepEqual(sectionTexts(container, headings[3], ".row .id"), ["R1", "R2"]);
    assert.deepEqual(sectionTexts(container, headings[3], ".row .text"), [
      "The page opens with the title.",
      "No field is editable.",
    ]);
    assert.deepEqual(sectionTexts(container, headings[3], ".row .evidence"), ["the title test", "the read-only test"]);

    assert.deepEqual(sectionTexts(container, headings[4], ".row .id"), ["A1"]);
    assert.deepEqual(sectionTexts(container, headings[4], ".row .text"), ["The contract is final once accepted."]);
    assert.deepEqual(sectionTexts(container, headings[4], ".row .if-wrong"), ["the page shows a stale copy."]);

    assert.deepEqual(sectionTexts(container, headings[5], ".row"), ["no Accept button", "no editing"]);

    assert.deepEqual(sectionTexts(container, headings[6], ".approach"), [fields.approach]);
    assert.deepEqual(sectionTexts(container, headings[6], ".row .alternative"), ["Render markdown"]);
    assert.deepEqual(sectionTexts(container, headings[6], ".row .why-not"), ["It would parse markup."]);
    assert.deepEqual(sectionTexts(container, headings[6], ".row .reconsider"), ["If the contract gains formatting."]);

    assert.deepEqual(sectionTexts(container, headings[7], ".row .step"), ["Deploy the web bundle"]);
    assert.deepEqual(sectionTexts(container, headings[7], ".row .target"), ["the web host"]);
    assert.deepEqual(sectionTexts(container, headings[7], ".row .undo"), ["redeploy the last bundle"]);
    assert.deepEqual(sectionTexts(container, headings[7], ".release-then"), [fields.releaseThen]);

    assert.deepEqual(sectionTexts(container, headings[8], ".checks"), [fields.checks]);
  }));

test("a contract with no additions says None under its heading", () =>
  withBody(contract({ ...fields, additions: [] }), (container) => {
    assert.deepEqual(sectionTexts(container, headings[2], ".none"), ["None"]);
    assert.deepEqual(sectionTexts(container, headings[2], ".row"), []);
  }));

test("the accepted line gives the clock, and the base when there is one", async () => {
  const clockText = clock(acceptedAt);
  await withBody(contract({ ...fields }), (container) => {
    assert.equal(textOf(container, "article.contract > p.accepted"), `accepted ${clockText} · base 0123456789ab`);
  });
  await withBody(contract({ ...fields, base: "" }), (container) => {
    assert.equal(textOf(container, "article.contract > p.accepted"), `accepted ${clockText}`);
  });
});

test("nothing on the page is editable: no input, textarea or form", () =>
  withBody(contract({ ...fields }), (container) => {
    assert.equal(container.querySelectorAll("input, textarea, form").length, 0);
  }));

test("markup in a field renders as text, never as elements", () => {
  const markup = '<img src="x" onerror="alert(1)"><b>bold</b>';
  return withBody(contract({ ...fields, intent: markup }), (container) => {
    assert.equal(textOf(container, "article.contract .intent"), markup);
    assert.equal(container.querySelectorAll(".intent *").length, 0);
    assert.equal(container.querySelectorAll("img, b").length, 0);
  });
});
