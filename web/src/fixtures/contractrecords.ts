// The contract records the fixture slices hold: the Contracts.Get of tidepool/tide-alerts in each state a contract can
// be in. Each record is the contract's two versions, first written and then changed at the owner's asking, with the
// decision the owner has to make and the states the contract has been through.

import { create } from "@bufbuild/protobuf";

import {
  ContractDecisionSchema,
  ContractSchema,
  ContractStateChangeSchema,
  ContractVersionSchema,
  type ContractDecision,
  type ContractsGetResponse,
  ContractsGetResponseSchema,
  type ContractVersion,
} from "../gen/orrery/v1/contracts_pb";
import {
  EpicAdditionSchema,
  EpicAlternativeSchema,
  EpicContractSchema,
  EpicFailureSchema,
  EpicPremiseSchema,
  EpicReleaseStepSchema,
  EpicRequirementSchema,
} from "../gen/orrery/v1/epics_pb";
import { at } from "./clock";

const tideId = "tidepool/tide-alerts";
const tideBase = "9f2c1ab4e0d7a6c53b82f1e9d04c7a6b5e3f2d18";
const tideWriter = "sess-tide-16";

const decisionD1 = {
  id: "D1",
  question: "Alert on every change, or only on changes to the next two days’ tables?",
  options: ["The next two days only", "Every change"],
  recommendation: "The next two days only",
  reason: "The office plans two days out, and later tables change often and nobody acts on them yet.",
  row: "R1",
};

// tideRequirements are the five requirements of the body; the first version's R3 says one alert a day.
function tideRequirements(second: boolean) {
  return [
    create(EpicRequirementSchema, { id: "R1", text: "A changed table alerts the office within a minute.", evidence: "TestAlertWithinMinute" }),
    create(EpicRequirementSchema, { id: "R2", text: "An unchanged table sends nothing.", evidence: "TestNoAlertUnchanged" }),
    create(EpicRequirementSchema, {
      id: "R3",
      text: second ? "One table makes one alert." : "One table makes one alert a day.",
      evidence: "TestOneAlertPerTable",
    }),
    create(EpicRequirementSchema, { id: "R4", text: "The alert names the table’s own port.", evidence: "TestPortFromTable" }),
    create(EpicRequirementSchema, { id: "R5", text: "A failed send is retried three times.", evidence: "TestRetryThrice" }),
  ];
}

// tideBody is the contract body of one version of tide-alerts. Version two changes F2 and R3. approved is the sentence
// that approved it, and accepted the unix second it was accepted at, or 0 while it is only proposed.
function tideBody(second: boolean, approved: string, accepted: bigint) {
  return create(EpicContractSchema, {
    title: "Alert the harbour office within a minute when a published tide table changes.",
    approved,
    intent: "Alert the harbour office when a tide table changes after it was published, so a pilot never works from a stale table.",
    failures: [
      create(EpicFailureSchema, { id: "F1", text: "The office was alerted about a table that had not changed.", rating: "limited", response: "prevent: compare the published hash before alerting." }),
      create(EpicFailureSchema, {
        id: "F2",
        text: "The office received the same alert many times.",
        rating: "serious",
        response: second ? "reduce: one alert per table; repeats are dropped." : "reduce: one alert per table and day.",
      }),
      create(EpicFailureSchema, { id: "F3", text: "An alert named the wrong port, and a pilot was sent to it.", rating: "severe", response: "prevent: the port is read from the table, never from the request." }),
    ],
    care: "severe",
    additions: [
      create(EpicAdditionSchema, {
        location: "alerts/send.go",
        what: "Retry a failed send three times.",
        why: "The office’s endpoint dropped 2 of the last 100 test sends.",
        cost: "One file. No new service.",
      }),
    ],
    premises: [
      create(EpicPremiseSchema, { id: "P1", text: "A published table has one hash, seen in tables/publish.go.", ifWrong: "The intent survives; F1’s guard needs another key." }),
      create(EpicPremiseSchema, { id: "P2", text: "The office’s endpoint answers within five seconds.", ifWrong: "The intent survives; sends queue, and R1’s minute is at risk." }),
    ],
    exclusions: ["No SMS or email. The office asked for its endpoint only.", "No change to how tables are published. It is out of scope."],
    approach: "A watcher compares each published table’s hash on write and sends one alert per changed table to the office’s endpoint.",
    alternatives: [
      create(EpicAlternativeSchema, { alternative: "Poll each table once a minute.", whyNot: "Alerts up to a minute late, and costs a query per port per minute.", reconsider: "The publisher cannot emit an event." }),
      create(EpicAlternativeSchema, { alternative: "Alert from the publisher itself.", whyNot: "Ties publishing a table to the office’s endpoint being up.", reconsider: "Never." }),
    ],
    releaseSteps: [
      create(EpicReleaseStepSchema, { step: "Merge the epic’s candidate into main, so the watcher ships.", target: "origin/main", undo: "revert the merge" }),
      create(EpicReleaseStepSchema, { step: "Turn alerts on for the harbour office, so they start receiving them.", target: "the office’s endpoint", undo: "none" }),
    ],
    releaseThen: "Then: one real change to a table alerts the office once, within a minute.",
    checks: "go test ./alerts/...; then the full gate, just check.",
    base: accepted === 0n ? "" : tideBase,
    accepted,
  });
}

// tideDecisions is D1, with the answer the owner gave it, or none while it is open.
function tideDecisions(answer: string): ContractDecision[] {
  return [create(ContractDecisionSchema, { ...decisionD1, answer })];
}

// tideVersion is one version of the contract as the record holds it.
function tideVersion(
  version: bigint,
  second: boolean,
  time: string,
  words: string,
  answer: string,
  approved: string,
  accepted: bigint,
): ContractVersion {
  return create(ContractVersionSchema, {
    version,
    contract: tideBody(second, approved, accepted),
    requirements: tideRequirements(second),
    decisions: tideDecisions(answer),
    words,
    writer: tideWriter,
    time: at(time),
    changedRows: second ? ["F2", "R3"] : [],
    changedSections: second ? ["failures", "requirements"] : [],
  });
}

const firstWords = "First written: three ways it could go wrong, one row you did not ask for, five requirements, one decision.";
const changedWords = "F2: one alert per table, where v1 said one per table and day. R3 says the same. Asked for at 14:39.";

// tideContract is the Contracts.List entry of tide-alerts, at the state and version the record is in.
function tideContract(state: string, version: bigint, openDecisions: bigint, changed: string) {
  return create(ContractSchema, {
    id: tideId,
    repository: "/home/owner/code/tidepool",
    title: "Tide alerts for the harbour office",
    care: "severe",
    state,
    version,
    openDecisions,
    conversation: tideWriter,
    created: at("@14:15"),
    changed: at(changed),
  });
}

const firstProposed = create(ContractStateChangeSchema, {
  state: "proposed",
  version: 1n,
  by: "session",
  session: tideWriter,
  words: "",
  time: at("@14:15"),
});

// tideProposed is the contract as the owner first sees it: v2 proposed, with D1 open, after v1 was written.
export const tideProposed: ContractsGetResponse = create(ContractsGetResponseSchema, {
  contract: tideContract("proposed", 2n, 1n, "@14:39"),
  versions: [
    tideVersion(1n, false, "@14:15", firstWords, "", "", 0n),
    tideVersion(2n, true, "@14:39", changedWords, "", "", 0n),
  ],
  states: [firstProposed],
});

// tideReady is the contract as proposed, with D1 answered and nothing left open.
export const tideReady: ContractsGetResponse = create(ContractsGetResponseSchema, {
  contract: tideContract("proposed", 2n, 0n, "@14:39"),
  versions: [
    tideVersion(1n, false, "@14:15", firstWords, "", "", 0n),
    tideVersion(2n, true, "@14:39", changedWords, "The next two days only", "", 0n),
  ],
  states: [firstProposed],
});

// tideAccepted is the contract accepted by its owner at v2, with the words the owner accepted it with.
export const tideAccepted: ContractsGetResponse = create(ContractsGetResponseSchema, {
  contract: tideContract("accepted", 2n, 0n, "@14:40"),
  versions: [
    tideVersion(1n, false, "@14:15", firstWords, "", "", 0n),
    tideVersion(2n, true, "@14:39", changedWords, "The next two days only", "approved by you, with “Accept, with the next two days only.”", at("@14:40")),
  ],
  states: [
    firstProposed,
    create(ContractStateChangeSchema, {
      state: "accepted",
      version: 2n,
      by: "owner",
      session: "",
      words: "Accept, with the next two days only.",
      time: at("@14:40"),
    }),
  ],
});

// tideSetAside is the contract its owner set aside at v2, with D1 still open when it was set aside.
export const tideSetAside: ContractsGetResponse = create(ContractsGetResponseSchema, {
  contract: tideContract("set_aside", 2n, 1n, "@14:40"),
  versions: [
    tideVersion(1n, false, "@14:15", firstWords, "", "", 0n),
    tideVersion(2n, true, "@14:39", changedWords, "", "", 0n),
  ],
  states: [
    firstProposed,
    create(ContractStateChangeSchema, {
      state: "set_aside",
      version: 2n,
      by: "owner",
      session: "",
      words: "Not until the pilots’ season ends.",
      time: at("@14:40"),
    }),
  ],
});
