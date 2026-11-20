import assert from "node:assert/strict";
import { test } from "node:test";

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
