import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  acceptable,
  acceptStep,
  aboutMessage,
  bandText,
  concernedRows,
  footText,
  leftOf,
  lengthOf,
  type RowAbout,
  ratingTone,
  recordLine,
  statePill,
} from "./contracts";
import {
  type ContractDecision,
  ContractDecisionSchema,
  type ContractStateChange,
  ContractStateChangeSchema,
  type ContractsGetResponse,
  ContractsGetResponseSchema,
  type ContractVersion,
  ContractVersionSchema,
} from "./gen/orrery/v1/contracts_pb";
import { EpicContractSchema, EpicRequirementSchema } from "./gen/orrery/v1/epics_pb";
import { clock } from "./reader";

const contractId = "tidepool/tide-alerts";
const t1 = 1_700_000_000n;
const t2 = t1 + 3600n;

// decision is one decision of a version: open while its answer is "", and it concerns the row given.
function decision(id: string, row: string, answer = ""): ContractDecision {
  return create(ContractDecisionSchema, {
    id,
    question: `Question ${id}?`,
    options: ["yes", "no"],
    recommendation: "yes",
    reason: "Needed",
    row,
    answer,
  });
}

// version is one stored version of the contract, with its decisions and the time it was stored.
function version(n: bigint, decisions: ContractDecision[] = [], time = t1): ContractVersion {
  return create(ContractVersionSchema, { version: n, decisions, time, writer: "s1" });
}

// stateChange is one change of the contract's state.
function stateChange(init: MessageInitShape<typeof ContractStateChangeSchema>): ContractStateChange {
  return create(ContractStateChangeSchema, init);
}

// get is a Get response for the contract at state and care, with its versions oldest first and its state changes.
function get(
  versions: ContractVersion[],
  state = "proposed",
  care = "limited",
  states: ContractStateChange[] = [],
): ContractsGetResponse {
  const newest = versions[versions.length - 1];
  return create(ContractsGetResponseSchema, {
    contract: {
      id: contractId,
      repository: "tidepool",
      title: "Tide alerts",
      care,
      state,
      version: newest.version,
      openDecisions: BigInt(newest.decisions.filter((d) => d.answer === "").length),
      conversation: "s1",
      created: t1,
      changed: t2,
    },
    versions,
    states,
  });
}

test("ContractActions: Accept is acceptable for the newest proposed version once nothing is open", () => {
  assert.equal(acceptable(get([version(1n)]), 1n, new Set(), false), true);
  assert.equal(acceptable(get([version(1n, [decision("D1", "F1")])]), 1n, new Set(), false), false);
  assert.equal(acceptable(get([version(1n, [decision("D1", "F1", "yes")])]), 1n, new Set(), false), true);
});

test("ContractActions: a severe proposal stays unacceptable until its irreversible section was read", () => {
  const severe = get([version(1n)], "proposed", "severe");
  assert.equal(acceptable(severe, 1n, new Set(), false), false);
  assert.equal(acceptable(severe, 1n, new Set(["tidepool/tide-alerts@1"]), false), true);
  const serious = get([version(1n)], "proposed", "serious");
  assert.equal(acceptable(serious, 1n, new Set(), false), true);
});

test("ContractActions: Accept is not offered for an older version, a decided contract, or while sending", () => {
  const twoVersions = get([version(1n), version(2n)]);
  assert.equal(acceptable(twoVersions, 1n, new Set(), false), false);
  assert.equal(acceptable(twoVersions, 2n, new Set(), false), true);
  assert.equal(acceptable(get([version(1n)], "accepted"), 1n, new Set(), false), false);
  assert.equal(acceptable(get([version(1n)], "set_aside"), 1n, new Set(), false), false);
  assert.equal(acceptable(get([version(1n)]), 1n, new Set(), true), false);
});

test("ContractActions: acceptStep sends one Accept while the drawn version is still the newest proposed", () => {
  assert.deepEqual(acceptStep(1n, get([version(1n)])), { kind: "send" });
});

test("ContractActions: acceptStep redraws instead of sending when a newer version came in", () => {
  const reread = get([version(1n), version(2n)]);
  assert.deepEqual(acceptStep(1n, reread), { kind: "redraw", get: reread });
});

test("ContractActions: acceptStep redraws instead of sending when the state changed under the drawn version", () => {
  const reread = get([version(1n)], "accepted");
  assert.deepEqual(acceptStep(1n, reread), { kind: "redraw", get: reread });
});

test("ContractLines: band text and foot text for no items", () => {
  const none = leftOf(0, "limited", false);
  assert.equal(bandText(none), "nothing left to settle");
  assert.equal(footText(none), "Nothing is left to settle.");
});

test("ContractLines: band text and foot text for one item", () => {
  const one = leftOf(1, "limited", false);
  assert.equal(bandText(one), "1 decision is yours");
  assert.equal(footText(one), "1 thing left: 1 decision is yours.");
  const unread = leftOf(0, "severe", false);
  assert.equal(bandText(unread), "read “can’t be undone” before Accept");
  assert.equal(footText(unread), "1 thing left: read “can’t be undone” before Accept.");
});

test("ContractLines: band text and foot text for two items", () => {
  const two = leftOf(2, "severe", false);
  assert.equal(bandText(two), "2 decisions are yours; read “can’t be undone” before Accept");
  assert.equal(footText(two), "2 things left: 2 decisions are yours; read “can’t be undone” before Accept.");
  assert.equal(footText(leftOf(2, "severe", true)), "1 thing left: 2 decisions are yours.");
});

test("ContractMine: concernedRows names the rows of open decisions only", () => {
  const decisions = [decision("D1", "F1"), decision("D2", "F2", "yes"), decision("D3", ""), decision("D4", "R2")];
  assert.deepEqual(concernedRows(decisions), new Set(["F1", "R2"]));
});

test("ContractMine: aboutMessage leads the draft with the row it is about", () => {
  const about: RowAbout = { id: "F1", text: "Keep the rows" };
  assert.equal(aboutMessage(about, "make it smaller"), "About F1: Keep the rows\n\nmake it smaller");
});

test("ContractVersions: record line for one proposed version", () => {
  assert.equal(recordLine(get([version(1n, [], t1)])), `v1 proposed ${clock(t1)}`);
});

test("ContractVersions: record line for two proposed versions names the first write", () => {
  const two = get([version(1n, [], t1), version(2n, [], t2)]);
  assert.equal(recordLine(two), `v2 proposed ${clock(t2)} · first written ${clock(t1)}`);
});

test("ContractVersions: record line for an accepted contract with and without words", () => {
  const byOwner = get(
    [version(1n)],
    "accepted",
    "limited",
    [stateChange({ state: "accepted", version: 1n, by: "owner", words: "ship it", time: t2 })],
  );
  assert.equal(recordLine(byOwner), `v1 accepted by you ${clock(t2)} with “ship it”`);
  const bySession = get(
    [version(1n)],
    "accepted",
    "limited",
    [stateChange({ state: "accepted", version: 1n, by: "session", session: "s1", words: "", time: t2 })],
  );
  assert.equal(recordLine(bySession), `v1 accepted by the session ${clock(t2)}`);
});

test("ContractVersions: record line for a set-aside contract names its words", () => {
  const setAside = get(
    [version(1n), version(2n)],
    "set_aside",
    "limited",
    [stateChange({ state: "set_aside", version: 2n, by: "owner", words: "not now", time: t2 })],
  );
  assert.equal(recordLine(setAside), `v2 set aside by you ${clock(t2)} with “not now”`);
});

test("ContractDoc: ratingTone maps limited to grey, serious to yellow and severe to red", () => {
  assert.equal(ratingTone("limited"), "");
  assert.equal(ratingTone("serious"), "live");
  assert.equal(ratingTone("severe"), "bad");
});

test("ContractDoc: statePill words each state, a proposal waiting on the owner", () => {
  assert.equal(statePill("proposed"), "proposed · waits for you");
  assert.equal(statePill("accepted"), "accepted");
  assert.equal(statePill("set_aside"), "set aside");
});

test("ContractLength: an empty contract is no lines", () => {
  assert.equal(lengthOf(create(EpicContractSchema, {}), [], []), 0);
});

test("ContractLength: the intent and the approach count their non-empty lines, not the blank ones", () => {
  const contract = create(EpicContractSchema, { intent: "one\n\n   \ntwo\n", approach: "a\nb\n\nc" });
  assert.equal(lengthOf(contract, [], []), 5);
});

test("ContractLength: each failure, addition, requirement, premise, exclusion, alternative, release step and decision is one line", () => {
  const contract = create(EpicContractSchema, {
    failures: [
      { id: "F1", text: "a failure", rating: "limited", response: "" },
      { id: "F2", text: "another", rating: "serious", response: "" },
    ],
    additions: [{ location: "a.go", what: "a change", why: "", cost: "" }],
    premises: [
      { id: "P1", text: "a premise", ifWrong: "" },
      { id: "P2", text: "another", ifWrong: "" },
      { id: "P3", text: "a third", ifWrong: "" },
    ],
    exclusions: ["not this"],
    alternatives: [
      { alternative: "one way", whyNot: "", reconsider: "" },
      { alternative: "another way", whyNot: "", reconsider: "" },
    ],
    releaseSteps: [{ step: "ship", target: "", undo: "" }],
  });
  const requirement = create(EpicRequirementSchema, { id: "R1", text: "a requirement", evidence: "" });
  const requirements = [requirement, requirement, requirement, requirement];
  const decisions = [decision("D1", "F1"), decision("D2", "F2", "yes")];
  assert.equal(lengthOf(contract, requirements, decisions), 2 + 1 + 3 + 1 + 2 + 1 + 4 + 2);
});

test("ContractLength: the title, care, release-then line, checks, base and approval count no line", () => {
  const contract = create(EpicContractSchema, {
    title: "a title\nover two lines",
    approved: "approved\nin two lines",
    care: "severe",
    releaseThen: "then\nthis",
    checks: "go test\njust check",
    base: "0123456789abcdef0123456789abcdef01234567",
  });
  assert.equal(lengthOf(contract, [], []), 0);
});

test("ContractLength: an answered decision counts as one line, as an open one does", () => {
  assert.equal(lengthOf(create(EpicContractSchema, {}), [], [decision("D1", "F1"), decision("D2", "F2", "yes")]), 2);
});
