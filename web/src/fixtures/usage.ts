// The invented usage the fixture serves: the limit readings and seat spend of round 8's usage popover, for the fixture
// accounts, and usageServe, a stateful in-memory Usage service that answers from them. Every account, seat and figure here
// is invented, and the fake holds no token.

import { clone, create } from "@bufbuild/protobuf";
import { Code, ConnectError, type ConnectRouter } from "@connectrpc/connect";

import {
  Usage,
  type UsageReading,
  UsageReadingSchema,
  type UsageReadingsRequest,
  UsageReadingsResponseSchema,
  type UsageSeatSpend,
  UsageSeatSpendSchema,
  type UsageSessionTotalsRequest,
  UsageSessionTotalsResponseSchema,
  type UsageSpendRequest,
  UsageSpendResponseSchema,
} from "../gen/orrery/v1/usage_pb";
import { at } from "./clock";

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
