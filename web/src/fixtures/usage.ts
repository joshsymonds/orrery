// The usage fixtures: invented totals for three sessions, each the rows SessionTotals answers for one session. The rows
// are in the order the server would send them, which is not largest first, so the model-and-agent rows must sort them.
// Costs are in nano US dollars: 1000000000 is one dollar. Later tests, Android's included, restate these values, so a
// change here moves those tests too.

import { create } from "@bufbuild/protobuf";

import { type UsageSessionTotal, UsageSessionTotalSchema } from "../gen/orrery/v1/usage_pb";

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
