// The usage fixtures: invented totals for three sessions, each the rows SessionTotals answers for one session. The rows
// are in the order the server would send them, which is not largest first, so the model-and-agent rows must sort them.
// Costs are in nano US dollars: 1000000000 is one dollar. Later tests, Android's included, restate these values, so a
// change here moves those tests too.
//
// The invented usage the fixture serves: the limit readings and seat spend of round 8's usage popover, for the fixture
// accounts, and usageServe, a stateful in-memory Usage service that answers from them. Every account, seat and figure here
// is invented, and the fake holds no token.

import { clone, create } from "@bufbuild/protobuf";
import { Code, ConnectError, type ConnectRouter } from "@connectrpc/connect";

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
  Usage,
  type UsageEpicSession,
  UsageEpicSessionSchema,
  type UsageReading,
  UsageReadingSchema,
  type UsageReadingsRequest,
  UsageReadingsResponseSchema,
  type UsageSeatSpend,
  UsageSeatSpendSchema,
  type UsageSessionTotal,
  UsageSessionTotalSchema,
  type UsageSessionTotalsRequest,
  UsageSessionTotalsResponseSchema,
  type UsageSpendRequest,
  UsageSpendResponseSchema,
} from "../gen/orrery/v1/usage_pb";
import { at } from "./clock";

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

// now is the fixture clock: 14:41 UTC on 14 November 2026, the instant the live browser starts at. Every observed time and
// reset below is set from it.
const now = at("@14:41");
const minute = 60n;
const hour = 3600n;
const day = 86400n;

// usageReadings are the invented limit readings the fixture starts from, in the shape of round 8's usage popover. Each
// utilization is a share of its limit, so 0.24 is 24 percent. max-3 is limited: its status is rejected in every window.
// pro-1 was observed two hours before the fixture clock, and the rest a few minutes or less before it.
export const usageReadings: readonly UsageReading[] = [
  create(UsageReadingSchema, {
    account: "max-1",
    claim: "5h",
    utilization: 0.24,
    reset: now + 2n * hour + 20n * minute,
    status: "allowed",
    observed: now - minute,
    dropped: false,
  }),
  create(UsageReadingSchema, {
    account: "max-1",
    claim: "7d",
    utilization: 0.54,
    reset: now + 3n * day,
    status: "allowed",
    observed: now - minute,
    dropped: false,
  }),
  create(UsageReadingSchema, {
    account: "max-1",
    claim: "7d_oi",
    utilization: 0.31,
    reset: now + 3n * day,
    status: "allowed",
    observed: now - minute,
    dropped: false,
  }),
  create(UsageReadingSchema, {
    account: "max-2",
    claim: "7d",
    utilization: 0.72,
    reset: now + 2n * day,
    status: "allowed",
    observed: now - 12n * minute,
    dropped: false,
  }),
  create(UsageReadingSchema, {
    account: "max-3",
    claim: "5h",
    utilization: 1,
    reset: now + hour + 10n * minute,
    status: "rejected",
    observed: now - 4n * minute,
    dropped: false,
  }),
  create(UsageReadingSchema, {
    account: "max-3",
    claim: "7d",
    utilization: 0.96,
    reset: now + day,
    status: "rejected",
    observed: now - 4n * minute,
    dropped: false,
  }),
  create(UsageReadingSchema, {
    account: "max-3",
    claim: "7d_oi",
    utilization: 0.8,
    reset: now + day,
    status: "rejected",
    observed: now - 4n * minute,
    dropped: false,
  }),
  create(UsageReadingSchema, {
    account: "pro-1",
    claim: "5h",
    utilization: 0.03,
    reset: now + 4n * hour + 40n * minute,
    status: "allowed",
    observed: now - 2n * hour,
    dropped: false,
  }),
  create(UsageReadingSchema, {
    account: "pro-1",
    claim: "7d",
    utilization: 0.18,
    reset: now + 4n * day,
    status: "allowed",
    observed: now - 2n * hour,
    dropped: false,
  }),
];

// usageSpend is the invented seat spend the fixture starts from, over the seven days to the fixture clock. The metered
// seats are billed what they list at. The forward seat anthropic is subscription traffic, billed nothing and priced at its
// list price. Every cost is in nano USD, so $44.33 is 44330000000.
export const usageSpend: readonly UsageSeatSpend[] = [
  create(UsageSeatSpendSchema, {
    seat: "work-bedrock",
    billedCostNanoUsd: 44_330_000_000n,
    listCostNanoUsd: 44_330_000_000n,
    unknownCostRequests: 0n,
  }),
  create(UsageSeatSpendSchema, {
    seat: "openrouter-sol",
    billedCostNanoUsd: 3_100_000_000n,
    listCostNanoUsd: 3_100_000_000n,
    unknownCostRequests: 0n,
  }),
  create(UsageSeatSpendSchema, {
    seat: "anthropic",
    billedCostNanoUsd: 0n,
    listCostNanoUsd: 1_240_000_000_000n,
    unknownCostRequests: 0n,
  }),
];

// Requests names the request each method of the fake carries, by the method's service and name.
interface Requests {
  "Usage.Readings": UsageReadingsRequest;
  "Usage.SessionTotals": UsageSessionTotalsRequest;
  "Usage.Spend": UsageSpendRequest;
}

// Method is a method the fake serves, named as its service and method: "Usage.Readings".
export type Method = keyof Requests;

// Call is one call the fake received: its method and the request it carried.
export type Call = { [M in Method]: { readonly method: M; readonly request: Requests[M] } }[Method];

// UsageSeed is the state the fake starts from: the limit readings and the seat spend it answers with.
export interface UsageSeed {
  readonly readings: readonly UsageReading[];
  readonly spend: readonly UsageSeatSpend[];
}

// UsageServe is the fake: its handlers are registered on a router, its calls are recorded, and its next calls can be
// refused or dropped.
export interface UsageServe {
  // register serves Usage on router, so a createRouterTransport answers its calls from this state.
  register(router: ConnectRouter): void;
  // calls holds every call received, in order, refused and dropped calls included.
  readonly calls: readonly Call[];
  // refuseNext makes the next call of method fail with code and message, changing nothing. Each refusal and drop is taken
  // by one call, in the order they were given.
  refuseNext(method: Method, code: Code, message: string): void;
  // dropNext makes the next call of method fail as a deadline does, changing nothing.
  dropNext(method: Method): void;
}

// Outcome is what a queued call is answered with instead of being served: a refusal, or a drop.
type Outcome = { readonly code: Code; readonly message: string } | "drop";

// usageServe returns the fake, starting from seed. The fake copies the seed, so no change it makes reaches the seed.
export function usageServe(seed: UsageSeed): UsageServe {
  const readings: UsageReading[] = seed.readings.map((reading) => clone(UsageReadingSchema, reading));
  const spend: UsageSeatSpend[] = seed.spend.map((seat) => clone(UsageSeatSpendSchema, seat));
  const calls: Call[] = [];
  const queued = new Map<Method, Outcome[]>();

  // arrive records a call, then fails it when an outcome is queued for its method.
  function arrive<M extends Method>(method: M, request: Requests[M]): void {
    calls.push({ method, request } as Call);
    const outcome = queued.get(method)?.shift();
    if (outcome === undefined) {
      return;
    }
    if (outcome === "drop") {
      throw new ConnectError("deadline exceeded", Code.DeadlineExceeded);
    }
    throw new ConnectError(outcome.message, outcome.code);
  }

  // served wraps a handler so that the call is recorded and may be refused or dropped before the handler answers it.
  function served<M extends Method, R>(method: M, handle: (request: Requests[M]) => R): (request: Requests[M]) => R {
    return (request) => {
      arrive(method, request);
      return handle(request);
    };
  }

  return {
    register(router) {
      router.service(Usage, {
        readings: served("Usage.Readings", () =>
          create(UsageReadingsResponseSchema, { readings: readings.map((reading) => clone(UsageReadingSchema, reading)) }),
        ),
        // SessionTotals answers no totals, since the fixture holds no session's requests.
        sessionTotals: served("Usage.SessionTotals", () => create(UsageSessionTotalsResponseSchema)),
        // Spend answers the seed's seats whatever the span the call asks for.
        spend: served("Usage.Spend", () =>
          create(UsageSpendResponseSchema, { seats: spend.map((seat) => clone(UsageSeatSpendSchema, seat)) }),
        ),
      });
    },
    calls,
    refuseNext(method, code, message) {
      queued.set(method, [...(queued.get(method) ?? []), { code, message }]);
    },
    dropNext(method) {
      queued.set(method, [...(queued.get(method) ?? []), "drop"]);
    },
  };
}
