import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";

import {
  AccountSchema,
  RouteContextSchema,
  RouteRegistrySchema,
  RouteSeatSchema,
  type Account,
  type RouteContext,
  type RouteRegistry,
  type RouteSeat,
} from "./gen/orrery/v1/accounts_pb";
import { UsageReadingSchema, UsageSeatSpendSchema, type UsageReading, type UsageSeatSpend } from "./gen/orrery/v1/usage_pb";
import {
  bandRows,
  firstInUse,
  isCurrent,
  levelOf,
  movingContexts,
  spendTotals,
  useOffer,
} from "./usagemodel";
import { noUsage, type UsageData } from "./usagedata";

// now is the fixed clock every test reads at, in unix seconds.
const now = 1794700000;

interface ReadingSpec {
  account?: string;
  claim: string;
  status?: string;
  utilization?: number;
  reset?: number;
  observed?: number;
  dropped?: boolean;
}

function reading(spec: ReadingSpec): UsageReading {
  return create(UsageReadingSchema, {
    account: spec.account ?? "work",
    claim: spec.claim,
    status: spec.status ?? "allowed",
    utilization: spec.utilization,
    reset: spec.reset === undefined ? undefined : BigInt(spec.reset),
    observed: BigInt(spec.observed ?? now),
    dropped: spec.dropped ?? false,
  });
}

function account(id: string): Account {
  return create(AccountSchema, { id, name: id, provider: "anthropic", plan: "max", fingerprint: "", created: 1794000000n });
}

function context(id: string, account: string): RouteContext {
  return create(RouteContextSchema, { id, account });
}

function seat(id: string, authMode: string, billing: string): RouteSeat {
  return create(RouteSeatSchema, { id, authMode, billing });
}

function registry(contexts: RouteContext[], seats: RouteSeat[] = []): RouteRegistry {
  return create(RouteRegistrySchema, { seats, contexts });
}

function spend(seatId: string, billed: number, list: number): UsageSeatSpend {
  return create(UsageSeatSpendSchema, { seat: seatId, billedCostNanoUsd: BigInt(billed), listCostNanoUsd: BigInt(list) });
}

function dataOf(init: Partial<UsageData>): UsageData {
  return { ...noUsage, ...init };
}

test("UsageModel: a dropped reading, and one whose reset is at or before now, is not current; a reset later than now, or none, is", () => {
  assert.equal(isCurrent(reading({ claim: "5h", dropped: true }), now), false);
  assert.equal(isCurrent(reading({ claim: "5h", reset: now - 1 }), now), false);
  assert.equal(isCurrent(reading({ claim: "5h", reset: now }), now), false);
  assert.equal(isCurrent(reading({ claim: "5h", reset: now + 1 }), now), true);
  assert.equal(isCurrent(reading({ claim: "overage", status: "rejected" }), now), true);
});

test("UsageModel: levelOf is ok under 60, warn from 60 to under 80, and crit from 80", () => {
  assert.equal(levelOf(59), "ok");
  assert.equal(levelOf(60), "warn");
  assert.equal(levelOf(79), "warn");
  assert.equal(levelOf(80), "crit");
});

test("UsageModel: a window draws its rounded percent and reset, and its level takes the edges at 60 and 80", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("work")],
      readings: [
        reading({ claim: "5h", utilization: 0.59, reset: now + 1000 }),
        reading({ claim: "7d", utilization: 0.6 }),
        reading({ claim: "7d_oi", utilization: 0.8 }),
      ],
    }),
    now,
  );
  assert.deepEqual(rows[0].windows, [
    { percent: 59, reset: now + 1000, level: "ok" },
    { percent: 60, reset: null, level: "warn" },
    { percent: 80, reset: null, level: "crit" },
  ]);
  assert.equal(rows[0].limited, false);
});

test("UsageModel: a dropped reading and one whose reset has passed draw no window, so a row holding only them draws none and reads no age", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("idle")],
      readings: [
        reading({ account: "idle", claim: "5h", utilization: 0.5, reset: now + 1000, dropped: true }),
        reading({ account: "idle", claim: "7d", utilization: 0.4, reset: now - 1 }),
      ],
    }),
    now,
  );
  assert.deepEqual(rows[0].windows, [null, null, null]);
  assert.equal(rows[0].none, true);
  assert.equal(rows[0].readAt, null);
  assert.equal(rows[0].stale, null);
  assert.deepEqual(rows[0].last, { kind: "none" });
});

test("UsageModel: a rejected 5h reading limits its account, lifts at its reset, and takes crit on every drawn window", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("work")],
      readings: [
        reading({ claim: "5h", status: "rejected", utilization: 1, reset: now + 1800 }),
        reading({ claim: "7d", utilization: 0.3, reset: now + 86400 }),
      ],
    }),
    now,
  );
  assert.equal(rows[0].limited, true);
  assert.equal(rows[0].liftsAt, now + 1800);
  assert.deepEqual(rows[0].windows, [
    { percent: 100, reset: now + 1800, level: "crit" },
    { percent: 30, reset: now + 86400, level: "crit" },
    null,
  ]);
  assert.deepEqual(rows[0].last, { kind: "lifts", at: now + 1800 });
});

test("UsageModel: a rejected 7d_oi reading limits its account, and its 5h window still takes crit", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("fable")],
      readings: [
        reading({ account: "fable", claim: "7d_oi", status: "rejected", utilization: 0.9, reset: now + 7200 }),
        reading({ account: "fable", claim: "5h", utilization: 0.2, reset: now + 1000 }),
      ],
    }),
    now,
  );
  assert.equal(rows[0].limited, true);
  assert.deepEqual(rows[0].windows, [
    { percent: 20, reset: now + 1000, level: "crit" },
    null,
    { percent: 90, reset: now + 7200, level: "crit" },
  ]);
  assert.deepEqual(rows[0].last, { kind: "lifts", at: now + 7200 });
});

test("UsageModel: liftsAt is the latest reset among the rejected readings", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("both")],
      readings: [
        reading({ account: "both", claim: "5h", status: "rejected", utilization: 1, reset: now + 1800 }),
        reading({ account: "both", claim: "7d", status: "rejected", utilization: 1, reset: now + 86400 }),
      ],
    }),
    now,
  );
  assert.equal(rows[0].liftsAt, now + 86400);
});

test("UsageModel: an overage rejection never limits its account, and draws no window of its own", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("over")],
      readings: [
        reading({ account: "over", claim: "overage", status: "rejected" }),
        reading({ account: "over", claim: "5h", utilization: 0.5, reset: now + 1000 }),
      ],
    }),
    now,
  );
  assert.equal(rows[0].limited, false);
  assert.equal(rows[0].liftsAt, null);
  assert.deepEqual(rows[0].windows, [{ percent: 50, reset: now + 1000, level: "ok" }, null, null]);
  assert.deepEqual(rows[0].last, { kind: "none" });
});

test("UsageModel: a fresh 5h beside a 7d_oi over an hour old is stale from the 7d_oi's observation", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("stale")],
      readings: [
        reading({ account: "stale", claim: "5h", utilization: 0.3, reset: now + 1000, observed: now - 60 }),
        reading({ account: "stale", claim: "7d_oi", utilization: 0.5, reset: now + 86400, observed: now - 3601 }),
      ],
    }),
    now,
  );
  assert.equal(rows[0].readAt, now - 3601);
  assert.equal(rows[0].stale, now - 3601);
  assert.deepEqual(rows[0].last, { kind: "age", at: now - 3601 });
  assert.equal(rows[0].limited, false);
});

test("UsageModel: a reading exactly an hour old is not stale", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("edge")],
      readings: [reading({ account: "edge", claim: "5h", utilization: 0.1, observed: now - 3600 })],
    }),
    now,
  );
  assert.equal(rows[0].readAt, now - 3600);
  assert.equal(rows[0].stale, null);
  assert.deepEqual(rows[0].last, { kind: "none" });
});

test("UsageModel: a fresh row with a current 7d reset shows that reset in its last cell", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("weekly")],
      readings: [
        reading({ account: "weekly", claim: "5h", utilization: 0.1, reset: now + 1000 }),
        reading({ account: "weekly", claim: "7d", utilization: 0.2, reset: now + 86400 }),
      ],
    }),
    now,
  );
  assert.deepEqual(rows[0].last, { kind: "reset", at: now + 86400 });
});

test("UsageModel: bandRows has one row per account in account order, counts each reading for its own account, and names the contexts that use it in registry order", () => {
  const rows = bandRows(
    dataOf({
      accounts: [account("work"), account("other"), account("idle")],
      readings: [
        reading({ account: "work", claim: "5h", utilization: 0.1, reset: now + 1000 }),
        reading({ account: "other", claim: "5h", status: "rejected", utilization: 1, reset: now + 100 }),
      ],
      registry: registry([
        context("main", "work"),
        context("review", ""),
        context("build", "work"),
        context("side", "other"),
      ]),
    }),
    now,
  );
  assert.deepEqual(rows.map((row) => row.account.id), ["work", "other", "idle"]);
  assert.equal(rows[0].limited, false);
  assert.equal(rows[0].inUse, true);
  assert.deepEqual(rows[0].usedBy, ["main", "build"]);
  assert.equal(rows[1].limited, true);
  assert.deepEqual(rows[1].usedBy, ["side"]);
  assert.equal(rows[2].inUse, false);
  assert.deepEqual(rows[2].usedBy, []);
  assert.deepEqual(rows[2].windows, [null, null, null]);
});

test("UsageModel: firstInUse is the first row in account order that is in use, and null when no row is", () => {
  const accounts = [account("a"), account("b"), account("c")];
  const inUse = bandRows(
    dataOf({ accounts, registry: registry([context("review", ""), context("main", "b"), context("side", "c")]) }),
    now,
  );
  assert.equal(firstInUse(inUse)?.account.id, "b");
  const idle = bandRows(dataOf({ accounts, registry: registry([context("review", "")]) }), now);
  assert.equal(firstInUse(idle), null);
});

test("UsageModel: spendTotals sums billed and list over every seat, counts forward and subscription seats at list, and counts a seat the registry lacks as metered", () => {
  const totals = spendTotals(
    [spend("api", 200, 250), spend("fwd", 100, 400), spend("sub", 100, 300), spend("quiet", 0, 0), spend("gone", 50, 70)],
    registry(
      [],
      [seat("api", "inject", "metered"), seat("fwd", "forward", ""), seat("sub", "inject", "subscription"), seat("quiet", "inject", "metered")],
    ),
  );
  assert.equal(totals.billed, 450n);
  assert.equal(totals.list, 1020n);
  assert.deepEqual(totals.seats, [
    { seat: "api", billed: 200n, list: 250n },
    { seat: "fwd", billed: 100n, list: 400n },
    { seat: "sub", billed: 100n, list: 300n },
    { seat: "gone", billed: 50n, list: 70n },
  ]);
  assert.equal(totals.subscriptionList, 700n);
});

test("UsageModel: spendTotals totals nothing before a Spend read lands", () => {
  assert.deepEqual(spendTotals(null, undefined), { billed: 0n, list: 0n, seats: [], subscriptionList: 0n });
});

test("UsageModel: useOffer is in use when every context that names an account names this one", () => {
  assert.equal(useOffer("work", registry([context("main", "work"), context("review", ""), context("build", "work")])), "in use");
});

test("UsageModel: useOffer offers use when some context names another account", () => {
  const shared = registry([context("main", "work"), context("review", "other")]);
  assert.equal(useOffer("work", shared), "use");
  assert.equal(useOffer("other", shared), "use");
  assert.equal(useOffer("idle", shared), "use");
});

test("UsageModel: useOffer is null when no context names an account", () => {
  assert.equal(useOffer("work", registry([context("main", "")])), null);
  assert.equal(useOffer("work", undefined), null);
});

test("UsageModel: movingContexts lists each context on another login in registry order, and skips those on the target or on none", () => {
  const moving = movingContexts(
    registry([context("main", "work"), context("review", ""), context("build", "other"), context("side", "work"), context("deep", "fable")]),
    "work",
  );
  assert.deepEqual(moving, [
    { context: "build", from: "other" },
    { context: "deep", from: "fable" },
  ]);
});
