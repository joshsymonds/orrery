// The usage model: what the inbox band, the popover, the rail and Use draw, computed from the usage data and a clock.
// It does no I/O. Every time it takes or returns is unix seconds, as a number; a clock is passed in as now.

import type { Account, RouteContext, RouteRegistry } from "./gen/orrery/v1/accounts_pb";
import type { UsageReading, UsageSeatSpend } from "./gen/orrery/v1/usage_pb";
import { windowClaims, type UsageData } from "./usagedata";

// staleAfter is how old, in seconds, the oldest reading an account draws may be before the account is stale.
const staleAfter = 3600;

// drawnClaims are the claims a row draws, and whose rejection limits its account. The claim list is shared with the
// window columns, so a rejected 5h, 7d or 7d_oi reading limits an account and an overage reading never does.
const drawnClaims: readonly string[] = windowClaims;

// Level is the colour a window or row takes: ok under 60 percent, warn from 60 to under 80, crit from 80.
export type Level = "ok" | "warn" | "crit";

// levelOf is the level of a percent of a limit in use.
export function levelOf(percent: number): Level {
  if (percent >= 80) return "crit";
  if (percent >= 60) return "warn";
  return "ok";
}

// isCurrent is whether a reading holds now: it is not dropped, and its reset is unset or later than now. A reading
// whose reset is at or before now has passed.
export function isCurrent(reading: UsageReading, now: number): boolean {
  return !reading.dropped && (reading.reset === undefined || Number(reading.reset) > now);
}

// WindowDraw is one window column of a row. percent is the reading's share of the limit in whole percent, or null when
// the reading gives no utilization. reset is when the limit resets, or null when the reading gives none. level is
// levelOf the percent, crit on a limited row, and null when there is no percent to level.
export interface WindowDraw {
  percent: number | null;
  reset: number | null;
  level: Level | null;
}

// LastCell is what the last column of a row shows: the time its limit lifts when it is limited, the age of its readings
// when they are stale, the 7d reset when it has one, and nothing otherwise. lifts is null when no rejected reading
// gives a reset.
export type LastCell =
  | { kind: "lifts"; at: number | null }
  | { kind: "age"; at: number }
  | { kind: "reset"; at: number }
  | { kind: "none" };

// BandRow is one account as the band draws it. windows has one entry per windowClaims claim, in that order: null when
// no current reading carries the claim. usedBy is the ids of the contexts that name the account, in registry order.
// liftsAt is the latest reset among the rejected readings; readAt is the oldest observed time among the readings drawn;
// stale is readAt when those readings are over an hour old, and null otherwise.
export interface BandRow {
  account: Account;
  windows: (WindowDraw | null)[];
  inUse: boolean;
  usedBy: string[];
  limited: boolean;
  liftsAt: number | null;
  readAt: number | null;
  stale: number | null;
  none: boolean;
  last: LastCell;
}

// contextsOf is the registry's contexts, or none when there is no registry yet.
function contextsOf(registry: RouteRegistry | undefined): readonly RouteContext[] {
  return registry?.contexts ?? [];
}

// bandRows is one row per account, in the order of the accounts. Each row draws the current readings of its own account.
export function bandRows(data: UsageData, now: number): BandRow[] {
  return data.accounts.map((account) => bandRow(account, data.readings, data.registry, now));
}

function bandRow(account: Account, readings: readonly UsageReading[], registry: RouteRegistry | undefined, now: number): BandRow {
  const current = readings.filter((reading) => reading.account === account.id && isCurrent(reading, now));
  const drawn = windowClaims.map((claim) => current.find((reading) => reading.claim === claim));
  const rejected = current.filter((reading) => drawnClaims.includes(reading.claim) && reading.status === "rejected");
  const limited = rejected.length > 0;

  const resets = rejected.flatMap((reading) => (reading.reset === undefined ? [] : [Number(reading.reset)]));
  const liftsAt = resets.length === 0 ? null : Math.max(...resets);

  const windows = drawn.map((reading): WindowDraw | null => {
    if (reading === undefined) return null;
    const percent = reading.utilization === undefined ? null : Math.round(reading.utilization * 100);
    let level: Level | null = percent === null ? null : levelOf(percent);
    if (limited) level = "crit";
    return { percent, reset: reading.reset === undefined ? null : Number(reading.reset), level };
  });

  const observed = drawn.flatMap((reading) => (reading === undefined ? [] : [Number(reading.observed)]));
  const readAt = observed.length === 0 ? null : Math.min(...observed);
  const stale = readAt !== null && now - readAt > staleAfter ? readAt : null;

  const sevenDayReset = current.find((reading) => reading.claim === "7d")?.reset;
  let last: LastCell;
  if (limited) {
    last = { kind: "lifts", at: liftsAt };
  } else if (stale !== null) {
    last = { kind: "age", at: stale };
  } else if (sevenDayReset !== undefined) {
    last = { kind: "reset", at: Number(sevenDayReset) };
  } else {
    last = { kind: "none" };
  }

  const usedBy = contextsOf(registry)
    .filter((context) => context.account === account.id)
    .map((context) => context.id);

  return {
    account,
    windows,
    inUse: usedBy.length > 0,
    usedBy,
    limited,
    liftsAt,
    readAt,
    stale,
    none: drawn.every((reading) => reading === undefined),
    last,
  };
}

// SeatSpend is one seat's spend over the span, in nano US dollars.
export interface SeatSpend {
  seat: string;
  billed: bigint;
  list: bigint;
}

// SpendTotals are the spend over the span, in nano US dollars. billed and list are summed over every seat. seats are the
// seats billed more than nothing, in the order of the Spend answer. subscriptionList is the list sum over the seats that
// are forward or subscription-billed in the registry; a seat the registry does not hold counts as metered.
export interface SpendTotals {
  billed: bigint;
  list: bigint;
  seats: SeatSpend[];
  subscriptionList: bigint;
}

// isSubscription is whether the registry holds the seat as a forward seat or one billed by subscription.
function isSubscription(seatId: string, registry: RouteRegistry | undefined): boolean {
  const seat = registry?.seats.find((candidate) => candidate.id === seatId);
  return seat !== undefined && (seat.authMode === "forward" || seat.billing === "subscription");
}

// spendTotals totals the spend of every seat. A null spend, before any Spend read lands, totals nothing.
export function spendTotals(spend: readonly UsageSeatSpend[] | null, registry: RouteRegistry | undefined): SpendTotals {
  const totals: SpendTotals = { billed: 0n, list: 0n, seats: [], subscriptionList: 0n };
  for (const entry of spend ?? []) {
    totals.billed += entry.billedCostNanoUsd;
    totals.list += entry.listCostNanoUsd;
    if (entry.billedCostNanoUsd > 0n) {
      totals.seats.push({ seat: entry.seat, billed: entry.billedCostNanoUsd, list: entry.listCostNanoUsd });
    }
    if (isSubscription(entry.seat, registry)) {
      totals.subscriptionList += entry.listCostNanoUsd;
    }
  }
  return totals;
}

// UseOffer is what Use offers an account: "in use" when every context that names an account names this one, and "use"
// when some context names another account.
export type UseOffer = "in use" | "use";

// useOffer is the offer for an account, or null when no context names an account.
export function useOffer(accountId: string, registry: RouteRegistry | undefined): UseOffer | null {
  const named = contextsOf(registry).filter((context) => context.account !== "");
  if (named.length === 0) return null;
  return named.every((context) => context.account === accountId) ? "in use" : "use";
}

// MovingContext is a context that Use would move off its own login, and the account it is on now.
export interface MovingContext {
  context: string;
  from: string;
}

// movingContexts are the contexts that name an account other than target, in registry order.
export function movingContexts(registry: RouteRegistry | undefined, target: string): MovingContext[] {
  return contextsOf(registry)
    .filter((context) => context.account !== "" && context.account !== target)
    .map((context) => ({ context: context.id, from: context.account }));
}

// firstInUse is the first row, in account order, that is in use, or null when none is.
export function firstInUse(rows: readonly BandRow[]): BandRow | null {
  return rows.find((row) => row.inUse) ?? null;
}
