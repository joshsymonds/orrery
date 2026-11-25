// The usage fixtures: invented totals for three sessions, each the rows SessionTotals answers for one session. The rows
// are in the order the server would send them, which is not largest first, so the model-and-agent rows must sort them.
// Costs are in nano US dollars: 1000000000 is one dollar. Later tests, Android's included, restate these values, so a
// change here moves those tests too.

import { create } from "@bufbuild/protobuf";

import {
  type EpicRequirement,
  EpicRequirementSchema,
  type EpicSession,
  EpicSessionSchema,
  type EpicStep,
  EpicStepSchema,
  EvidenceRunSchema,
} from "../gen/orrery/v1/epics_pb";
import {
  type UsageEpicSession,
  UsageEpicSessionSchema,
  type UsageSessionTotal,
  UsageSessionTotalSchema,
} from "../gen/orrery/v1/usage_pb";

// sessionTotals is a session that its kinds' costs are known for, and that billed some of its list cost. Its four kinds
// sum to its list cost of $3.77, which is billed $2.79 over 420 requests, and it asks for three models, one with an
// empty agent type.
export const sessionTotals: UsageSessionTotal[] = [
  create(UsageSessionTotalSchema, {
    model: "sonnet-5-5",
    agentType: "task-reviewer",
    requests: 30n,
    inputTokens: 100_000n,
    outputTokens: 10_000n,
    cacheReadTokens: 4_000_000n,
    cacheWriteTokens: 100_000n,
    billedCostNanoUsd: 200_000_000n,
    listCostNanoUsd: 220_000_000n,
    listInputNanoUsd: 30_000_000n,
    listOutputNanoUsd: 30_000_000n,
    listCacheReadNanoUsd: 100_000_000n,
    listCacheWriteNanoUsd: 60_000_000n,
  }),
  create(UsageSessionTotalSchema, {
    model: "opus-5-5",
    agentType: "",
    requests: 240n,
    inputTokens: 900_000n,
    outputTokens: 150_000n,
    cacheReadTokens: 30_000_000n,
    cacheWriteTokens: 2_000_000n,
    billedCostNanoUsd: 1_900_000_000n,
    listCostNanoUsd: 2_670_000_000n,
    listInputNanoUsd: 270_000_000n,
    listOutputNanoUsd: 450_000_000n,
    listCacheReadNanoUsd: 750_000_000n,
    listCacheWriteNanoUsd: 1_200_000_000n,
  }),
  create(UsageSessionTotalSchema, {
    model: "haiku-5-5",
    agentType: "Explore",
    requests: 90n,
    inputTokens: 300_000n,
    outputTokens: 20_000n,
    cacheReadTokens: 4_000_000n,
    cacheWriteTokens: 100_000n,
    billedCostNanoUsd: 280_000_000n,
    listCostNanoUsd: 310_000_000n,
    listInputNanoUsd: 90_000_000n,
    listOutputNanoUsd: 60_000_000n,
    listCacheReadNanoUsd: 100_000_000n,
    listCacheWriteNanoUsd: 60_000_000n,
  }),
  create(UsageSessionTotalSchema, {
    model: "opus-5-5",
    agentType: "steelman",
    requests: 60n,
    inputTokens: 200_000n,
    outputTokens: 40_000n,
    cacheReadTokens: 6_000_000n,
    cacheWriteTokens: 400_000n,
    billedCostNanoUsd: 410_000_000n,
    listCostNanoUsd: 570_000_000n,
    listInputNanoUsd: 60_000_000n,
    listOutputNanoUsd: 120_000_000n,
    listCacheReadNanoUsd: 150_000_000n,
    listCacheWriteNanoUsd: 240_000_000n,
  }),
];

// unsummedTotals is a session whose four kinds' costs do not sum to its list cost: they come to $4.05 against the $4.90
// list, so its dollars are not known. It is billed $3.90 over 70 requests.
export const unsummedTotals: UsageSessionTotal[] = [
  create(UsageSessionTotalSchema, {
    model: "opus-5-5",
    agentType: "",
    requests: 50n,
    inputTokens: 400_000n,
    outputTokens: 30_000n,
    cacheReadTokens: 2_000_000n,
    cacheWriteTokens: 100_000n,
    billedCostNanoUsd: 300_000_000n,
    listCostNanoUsd: 400_000_000n,
    listInputNanoUsd: 120_000_000n,
    listOutputNanoUsd: 90_000_000n,
    listCacheReadNanoUsd: 50_000_000n,
    listCacheWriteNanoUsd: 60_000_000n,
  }),
  create(UsageSessionTotalSchema, {
    model: "haiku-5-5",
    agentType: "Explore",
    requests: 20n,
    inputTokens: 100_000n,
    outputTokens: 5_000n,
    cacheReadTokens: 400_000n,
    cacheWriteTokens: 50_000n,
    billedCostNanoUsd: 90_000_000n,
    listCostNanoUsd: 90_000_000n,
    listInputNanoUsd: 30_000_000n,
    listOutputNanoUsd: 15_000_000n,
    listCacheReadNanoUsd: 10_000_000n,
    listCacheWriteNanoUsd: 30_000_000n,
  }),
];

// subscriptionTotals is a session on a subscription: it is billed nothing, and its list cost is $1.40 over 190 requests.
// Its kinds sum to that list cost, so its dollars are known.
export const subscriptionTotals: UsageSessionTotal[] = [
  create(UsageSessionTotalSchema, {
    model: "opus-5-5",
    agentType: "",
    requests: 150n,
    inputTokens: 600_000n,
    outputTokens: 90_000n,
    cacheReadTokens: 12_000_000n,
    cacheWriteTokens: 800_000n,
    billedCostNanoUsd: 0n,
    listCostNanoUsd: 1_230_000_000n,
    listInputNanoUsd: 180_000_000n,
    listOutputNanoUsd: 270_000_000n,
    listCacheReadNanoUsd: 300_000_000n,
    listCacheWriteNanoUsd: 480_000_000n,
  }),
  create(UsageSessionTotalSchema, {
    model: "sonnet-5-5",
    agentType: "Explore",
    requests: 40n,
    inputTokens: 100_000n,
    outputTokens: 10_000n,
    cacheReadTokens: 2_000_000n,
    cacheWriteTokens: 100_000n,
    billedCostNanoUsd: 0n,
    listCostNanoUsd: 170_000_000n,
    listInputNanoUsd: 30_000_000n,
    listOutputNanoUsd: 30_000_000n,
    listCacheReadNanoUsd: 50_000_000n,
    listCacheWriteNanoUsd: 60_000_000n,
  }),
];

// The epic fixture: one epic's sessions as its usage answers them and as Epics.Get holds them. Its steerer is shared, so its
// row is outside the header's sums. Its step sessions are an implement step, a review step, an evidence judge of requirement
// R4, and an agent-named step with no task. Its cache writes fall in each token format's band: 48M, 2.4M and 890k. The step
// sessions list $2.70 at list, billed $2.01, and the steerer's $11.40 is not in that sum.

// epicSteps are the epic's steps, each step's id its session's id. The implement step writes task T3; the review step
// reviews T3 read-only; the judge is an evidence step whose task names run r-judge1; the agent step has no task.
export const epicSteps: EpicStep[] = [
  create(EpicStepSchema, {
    id: "3b9a4e10-build",
    task: "T3",
    agent: "builder",
    role: "implementer",
    readOnly: false,
    ended: 9_000n,
  }),
  create(EpicStepSchema, {
    id: "8f2e6c71-review",
    task: "T3",
    agent: "task-reviewer",
    role: "reviewer",
    readOnly: true,
    ended: 9_100n,
  }),
  create(EpicStepSchema, {
    id: "d4a07b53-judge",
    task: "evidence-r-judge1",
    agent: "judge",
    role: "evidence",
    readOnly: true,
    ended: 9_200n,
  }),
  create(EpicStepSchema, {
    id: "e61f9a2c-agent",
    task: "",
    agent: "steelman",
    role: "",
    readOnly: false,
    ended: 0n,
  }),
];

// epicRequirements are the epic's requirements: R4's run r-judge1 is the one the judge step ran for.
export const epicRequirements: EpicRequirement[] = [
  create(EpicRequirementSchema, {
    id: "R4",
    text: "A release names what it shipped",
    runs: [create(EvidenceRunSchema, { id: "r-judge1", phase: "judge", state: "ended", verdict: "holds", passed: true })],
  }),
  create(EpicRequirementSchema, {
    id: "R5",
    text: "A failed gate names its check",
    runs: [create(EvidenceRunSchema, { id: "r-other", phase: "judge", state: "ended", verdict: "fails", passed: false })],
  }),
];

// epicSessions are the sessions Epics.Get lists for the epic: the steerer, which called the epic's tools first.
export const epicSessions: EpicSession[] = [
  create(EpicSessionSchema, { id: "7c1d0e2f-steering", label: "steering session", creator: true }),
];

// epicUsage is the epic's sessions as Usage.EpicTotals answers them, in session id order, as the server sends them. The
// steerer is shared; its totals are at list $11.40 over 1,204 requests and bill nothing. The implement step has two totals,
// and its row names the model of the larger, haiku-5-5, which the totals list second, after the smaller sonnet-5-5, so the
// row's model is not the first total's.
export const epicUsage: UsageEpicSession[] = [
  create(UsageEpicSessionSchema, {
    session: "3b9a4e10-build",
    shared: false,
    totals: [
      create(UsageSessionTotalSchema, {
        model: "sonnet-5-5",
        agentType: "",
        requests: 20n,
        inputTokens: 40_000n,
        outputTokens: 3_000n,
        cacheReadTokens: 300_000n,
        cacheWriteTokens: 40_000n,
        billedCostNanoUsd: 60_000_000n,
        listCostNanoUsd: 90_000_000n,
      }),
      create(UsageSessionTotalSchema, {
        model: "haiku-5-5",
        agentType: "builder",
        requests: 388n,
        inputTokens: 1_100_000n,
        outputTokens: 60_000n,
        cacheReadTokens: 9_000_000n,
        cacheWriteTokens: 850_000n,
        billedCostNanoUsd: 900_000_000n,
        listCostNanoUsd: 1_120_000_000n,
      }),
    ],
  }),
  create(UsageEpicSessionSchema, {
    session: "7c1d0e2f-steering",
    shared: true,
    totals: [
      create(UsageSessionTotalSchema, {
        model: "opus-5-5",
        agentType: "",
        requests: 1_204n,
        inputTokens: 2_000_000n,
        outputTokens: 210_000n,
        cacheReadTokens: 120_000_000n,
        cacheWriteTokens: 48_000_000n,
        billedCostNanoUsd: 0n,
        listCostNanoUsd: 11_400_000_000n,
      }),
    ],
  }),
  create(UsageEpicSessionSchema, {
    session: "8f2e6c71-review",
    shared: false,
    totals: [
      create(UsageSessionTotalSchema, {
        model: "sonnet-5-5",
        agentType: "task-reviewer",
        requests: 96n,
        inputTokens: 300_000n,
        outputTokens: 40_000n,
        cacheReadTokens: 22_000_000n,
        cacheWriteTokens: 2_400_000n,
        billedCostNanoUsd: 800_000_000n,
        listCostNanoUsd: 1_050_000_000n,
      }),
    ],
  }),
  create(UsageEpicSessionSchema, {
    session: "d4a07b53-judge",
    shared: false,
    totals: [
      create(UsageSessionTotalSchema, {
        model: "sonnet-5-5",
        agentType: "judge",
        requests: 31n,
        inputTokens: 90_000n,
        outputTokens: 8_000n,
        cacheReadTokens: 6_000_000n,
        cacheWriteTokens: 300_000n,
        billedCostNanoUsd: 150_000_000n,
        listCostNanoUsd: 210_000_000n,
      }),
    ],
  }),
  create(UsageEpicSessionSchema, {
    session: "e61f9a2c-agent",
    shared: false,
    totals: [
      create(UsageSessionTotalSchema, {
        model: "opus-5-5",
        agentType: "steelman",
        requests: 44n,
        inputTokens: 150_000n,
        outputTokens: 20_000n,
        cacheReadTokens: 4_000_000n,
        cacheWriteTokens: 900_000n,
        billedCostNanoUsd: 100_000_000n,
        listCostNanoUsd: 230_000_000n,
      }),
    ],
  }),
];
