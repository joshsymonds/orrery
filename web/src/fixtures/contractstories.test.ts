import assert from "node:assert/strict";
import { test } from "node:test";

import { at } from "./clock";
import { paneStreams } from "./panes";
import { contractStories, type Slices } from "./slices";

// Each contract page story is served from its own slices: the Contracts.Get record its scenario shows, and the
// Contracts.List entry of the same contract. The List entry and the record's newest version agree on the version, and
// the decisions the newest version holds are open or settled as the scenario has them.

const tide = "tidepool/tide-alerts";

interface Expected {
  state: string;
  lastState: string;
  openDecisions: bigint;
  answers: string[];
}

const stories: [string, Slices, Expected][] = [
  ["to-accept", contractStories.toAccept, { state: "proposed", lastState: "proposed", openDecisions: 1n, answers: [""] }],
  ["accepted", contractStories.accepted, { state: "accepted", lastState: "accepted", openDecisions: 0n, answers: ["The next two days only"] }],
  ["set-aside", contractStories.setAside, { state: "set_aside", lastState: "set_aside", openDecisions: 1n, answers: [""] }],
  ["ready", contractStories.ready, { state: "proposed", lastState: "proposed", openDecisions: 0n, answers: ["The next two days only"] }],
];

for (const [name, slices, expected] of stories) {
  test(`contract story ${name}: its record and List entry hold the state the scenario shows`, () => {
    const record = slices.contractGets[tide];
    const entry = slices.contracts.contracts.find((candidate) => candidate.id === tide);
    assert.ok(record !== undefined, `no record for ${tide}`);
    assert.ok(entry !== undefined, `no List entry for ${tide}`);
    const newest = record.versions.at(-1);
    assert.ok(newest !== undefined, "the record holds no version");

    assert.equal(record.contract?.state, expected.state, "the record's state");
    assert.equal(entry.state, expected.state, "the List entry's state");
    assert.equal(entry.version, 2n, "the List entry's version");
    assert.equal(newest.version, entry.version, "the newest version the record holds");
    assert.equal(record.states.at(-1)?.state, expected.lastState, "the last state change");
    assert.deepEqual(
      newest.decisions.map((decision) => decision.answer),
      expected.answers,
      "the newest version's decision answers",
    );
    assert.equal(
      newest.decisions.filter((decision) => decision.answer === "").length,
      Number(entry.openDecisions),
      "the List entry's open decisions against the record's",
    );
    assert.equal(entry.openDecisions, expected.openDecisions, "the List entry's open decisions");
  });
}

// The accepted record's List entry and its accepted state change say the same time, so the List's changed time, the time
// the transcript line draws, is the time the contract was accepted.
test("contract story accepted: the List entry's changed time is its accepted state change's time", () => {
  const record = contractStories.accepted.contractGets[tide];
  const entry = contractStories.accepted.contracts.contracts.find((candidate) => candidate.id === tide);
  const accepted = record?.states.find((change) => change.state === "accepted");
  assert.ok(entry !== undefined, `no List entry for ${tide}`);
  assert.ok(accepted !== undefined, "the record holds no accepted state change");
  assert.equal(entry.changed, accepted.time, "the List entry's changed time against the accepted state change's time");
});

// The tide conversation holds the exchange after the contract call: the owner asks why three retries, and the session
// answers at the same time the question was asked.
test("tide transcript: the question and the answer follow the contract call", () => {
  const items = paneStreams.get("%16")?.items ?? [];
  const proposed = items.findIndex((item) => item.kind === "tool" && item.tool === "contract_propose");
  assert.notEqual(proposed, -1, "the transcript holds no contract_propose call");
  const asked = items.findIndex((item, index) => index > proposed && item.kind === "prompt" && item.text === "Why three retries and not one?");
  assert.notEqual(asked, -1, "the transcript holds no question after the contract call");
  assert.equal(items[asked]?.time, at("@14:38"), "the question's time");
  const answer = items[asked + 1];
  assert.equal(answer?.kind, "reply", "the item after the question");
  assert.equal(answer.time, at("@14:38"), "the answer's time");
  assert.equal(
    answer.text,
    "The office’s endpoint dropped 2 of the last 100 test sends. Three tries put a missed alert under one in a hundred thousand; one try leaves it at one in fifty.",
    "the answer's text",
  );
});
