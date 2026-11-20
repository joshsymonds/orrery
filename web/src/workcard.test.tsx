import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";

import { blockOf } from "./epics";
import type { Epic } from "./gen/orrery/v1/epics_pb";
import { liveEpoch } from "./fixtures/clock";
import { contracts } from "./fixtures/contracts";
import { epics, gets } from "./fixtures/epics";
import { installTestDom, removeTestDom } from "./testdom";
import type { ContractItem, EpicNode } from "./tree";
import { type CardModel, WorkCard, workCardOf } from "./workcard";

// now is the clock the card reads its times against, in unix seconds: the fixtures' live instant.
const now = Date.parse(liveEpoch) / 1000;

// listedEpic is the Epics.List row of the id in the fixtures, as a copy the test may change.
function listedEpic(id: string, change: (epic: Epic) => void = () => {}): Epic {
  const found = epics.epics.find((epic) => epic.id === id);
  if (found === undefined) {
    throw new Error(`the fixture epics hold no row for ${id}`);
  }
  const copy: Epic = { ...found, runningSteps: [...found.runningSteps] };
  change(copy);
  return copy;
}

// epicNode hangs the listed epic under its conversation as the tree does, with its block read from the fixture's detail.
function epicNode(listed: Epic): EpicNode {
  return { kind: "epic", key: `epic:${listed.id}`, block: blockOf(listed, gets.get(listed.id)), listed, contract: null };
}

// contractItem hangs the fixture's proposed contract under its conversation, with its open decisions set.
function contractItem(openDecisions: bigint): ContractItem {
  const contract = contracts.contracts[0];
  return { kind: "contract", key: `contract:${contract.id}`, contract: { ...contract, openDecisions } };
}

// questionOf is the text of the first open question of a fixture epic.
function questionOf(id: string): string {
  const question = gets.get(id)?.questions[0];
  if (question === undefined) {
    throw new Error(`the fixture holds no question for ${id}`);
  }
  return question.text;
}

// withCard draws the card in a fresh DOM with onOpen, runs check on the container, and removes the DOM again.
async function withCard(
  model: CardModel,
  onOpen: (key: string) => void,
  check: (container: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<WorkCard card={model} onOpen={onOpen} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

// pressOn clicks the first element that matches selector, which must be drawn.
function pressOn(root: ParentNode, selector: string): void {
  const element = root.querySelector<HTMLElement>(selector);
  if (element === null) {
    throw new Error(`no ${selector} is drawn`);
  }
  element.click();
}

test("ProjectPage WorkCard words for a contract waiting: its care, title, decisions left, Read and decide, and version", () => {
  assert.deepEqual(workCardOf(contractItem(2n), now), {
    key: "contract:tidepool/tide-alerts",
    slug: "tide-alerts",
    gauge: null,
    tone: "attention",
    state: "serious care",
    track: ["now", "ahead", "ahead", "ahead", "ahead"],
    now: "Tide alerts for the harbour office",
    asks: "2 decisions left to answer",
    act: "Read and decide",
    count: "v2",
  });
  assert.equal(workCardOf(contractItem(1n), now).asks, "1 decision left to answer");
  assert.equal(workCardOf(contractItem(0n), now).asks, "waits for you to accept");
});

test("ProjectPage WorkCard leaves an accepted contract with no listed epic with no tone and nothing asked", () => {
  const accepted: ContractItem = { ...contractItem(0n), contract: { ...contracts.contracts[0], state: "accepted", care: "" } };
  const card = workCardOf(accepted, now);
  assert.equal(card.tone, "");
  assert.equal(card.state, "accepted");
  assert.equal(card.asks, "");
  assert.equal(card.act, "");
});

test("ProjectPage WorkCard gives an epic with no detail its contract passed and no part now", () => {
  const listed = listedEpic("lighthouse/rota-export");
  const node: EpicNode = { kind: "epic", key: `epic:${listed.id}`, block: blockOf(listed, undefined), listed, contract: null };
  assert.deepEqual(workCardOf(node, now).track, ["done", "ahead", "ahead", "ahead", "ahead"]);
});

test("ProjectPage WorkCard words for a contract under an epic whose contract is proposed: the contract's words", () => {
  const node: EpicNode = { ...epicNode(listedEpic("lighthouse/rota-export")), contract: contracts.contracts[0] };
  const card = workCardOf(node, now);
  assert.equal(card.key, "epic:lighthouse/rota-export");
  assert.equal(card.act, "Read and decide");
  assert.equal(card.count, "v2");
});

test("ProjectPage WorkCard words for an epic that asks: its question, Answer, now sentence, and track", () => {
  assert.deepEqual(workCardOf(epicNode(listedEpic("lighthouse/rota-export")), now), {
    key: "epic:lighthouse/rota-export",
    slug: "rota-export",
    gauge: { done: 0, total: 0, tone: "attention", inner: "dot" },
    tone: "attention",
    state: "building · 49 min",
    track: ["done", "done", "now", "ahead", "ahead"],
    now: "2 tasks are being worked on, and one waits on you.",
    asks: questionOf("lighthouse/rota-export"),
    act: "Answer",
    count: "3 of 6 proven",
  });
});

test("ProjectPage WorkCard words for an epic with another need: its blocker and Decide", () => {
  const card = workCardOf(epicNode(listedEpic("tidepool/offline-tables")), now);
  assert.equal(card.tone, "attention");
  assert.equal(
    card.asks,
    "Release step 2 failed: CI is red on the tag v0.4.0 for the second time: go vet: cache/key_test.go:41.",
  );
  assert.equal(card.act, "Decide");
});

test("ProjectPage WorkCard words for an epic building: live, with no asks", () => {
  assert.deepEqual(workCardOf(epicNode(listedEpic("orrery/orrery-followups")), now), {
    key: "epic:orrery/orrery-followups",
    slug: "orrery-followups",
    gauge: { done: 0, total: 0, tone: "live" },
    tone: "live",
    state: "building · 20 min",
    track: ["done", "done", "now", "ahead", "ahead"],
    now: "2 tasks are being worked on.",
    asks: "",
    act: "",
    count: "1 of 3 proven",
  });
});

test("ProjectPage WorkCard leaves an ended epic with no tone or asks, and an archived live epic with no tone or asks", () => {
  const ended = workCardOf(epicNode(listedEpic("orrery/holder-scopes")), now);
  assert.equal(ended.tone, "");
  assert.equal(ended.asks, "");
  assert.equal(ended.state, "shipped · 4 h 49 min ago");
  assert.deepEqual(ended.track, ["done", "done", "done", "done", "now"]);
  const archived = workCardOf(
    epicNode(listedEpic("lighthouse/rota-export", (epic) => {
      epic.archived = true;
    })),
    now,
  );
  assert.equal(archived.tone, "");
  assert.equal(archived.asks, "");
  assert.equal(archived.act, "");
});

test("ProjectPage WorkCard draws its words, and its card, act and Open each call onOpen with the item key", async () => {
  const opened: string[] = [];
  const model = workCardOf(epicNode(listedEpic("lighthouse/rota-export")), now);
  await withCard(
    model,
    (key) => opened.push(key),
    (container) => {
      assert.equal(textOf(container, ".slug"), "rota-export");
      assert.equal(textOf(container, ".pill"), "building · 49 min");
      assert.equal(textOf(container, "p.now"), "2 tasks are being worked on, and one waits on you.");
      assert.equal(textOf(container, ".asks span"), questionOf("lighthouse/rota-export"));
      assert.equal(textOf(container, "button.act"), "Answer");
      assert.equal(textOf(container, "button.link"), "Open");
      pressOn(container, "button.act");
      pressOn(container, "button.link");
      pressOn(container, ".wcard");
      assert.deepEqual(opened, [
        "epic:lighthouse/rota-export",
        "epic:lighthouse/rota-export",
        "epic:lighthouse/rota-export",
      ]);
    },
  );
});
