// The usage model: what the collapsed inbox's usage tile and its popover draw, computed from the usage data and a clock. It
// does no I/O. Every time it takes or returns is unix seconds, as a number; a clock is passed in as now.

import type { Account, RouteRegistry } from "./gen/orrery/v1/accounts_pb";
import type { UsageReading } from "./gen/orrery/v1/usage_pb";
import { windowClaims, type UsageData } from "./usagedata";

// drawnClaims are the claims whose rejection limits an account. They are the claims drawn as windows, so an overage
// reading never limits an account.
const drawnClaims: readonly string[] = windowClaims;

// Level is the colour a window takes: ok under 60 percent, warn from 60 to under 80, crit from 80.
export type Level = "ok" | "warn" | "crit";

// levelOf is the level of a percent of a limit in use.
function levelOf(percent: number): Level {
  if (percent >= 80) return "crit";
  if (percent >= 60) return "warn";
  return "ok";
}

// isCurrent is whether a reading holds now: it is not dropped, and its reset is unset or later than now. A reading whose
// reset is at or before now has passed.
function isCurrent(reading: UsageReading, now: number): boolean {
  return !reading.dropped && (reading.reset === undefined || Number(reading.reset) > now);
}

// WindowDraw is one window column of a row. percent is the reading's share of the limit in whole percent, or null when
// the reading gives no utilization. reset is when the limit resets, or null when the reading gives none. level is levelOf
// the percent, crit on a limited row, and null when there is no percent to level.
export interface WindowDraw {
  percent: number | null;
  reset: number | null;
  level: Level | null;
}

// BandRow is one account as the usage tile and the popover draw it. windows has one entry per windowClaims claim, in that
// order: null when no current reading carries the claim. usedBy is the ids of the contexts that name the account, in
// registry order, and inUse is whether any does.
export interface BandRow {
  account: Account;
  windows: (WindowDraw | null)[];
  inUse: boolean;
  usedBy: string[];
}

// contextsOf is the registry's contexts, or none when there is no registry yet.
function contextsOf(registry: RouteRegistry | undefined) {
  return registry?.contexts ?? [];
}

// bandRows is one row per account, in the order of the accounts. Each row draws the current readings of its own account.
export function bandRows(data: UsageData, now: number): BandRow[] {
  return data.accounts.map((account) => bandRow(account, data.readings, data.registry, now));
}

// bandRow is the row of one account. A rejected 5h, 7d or 7d_oi reading limits the account, and then each of its windows
// draws crit.
function bandRow(account: Account, readings: readonly UsageReading[], registry: RouteRegistry | undefined, now: number): BandRow {
  const current = readings.filter((reading) => reading.account === account.id && isCurrent(reading, now));
  const drawn = windowClaims.map((claim) => current.find((reading) => reading.claim === claim));
  const limited = current.some((reading) => drawnClaims.includes(reading.claim) && reading.status === "rejected");

  const windows = drawn.map((reading): WindowDraw | null => {
    if (reading === undefined) return null;
    const percent = reading.utilization === undefined ? null : Math.round(reading.utilization * 100);
    const level: Level | null = limited ? "crit" : percent === null ? null : levelOf(percent);
    return { percent, reset: reading.reset === undefined ? null : Number(reading.reset), level };
  });

  const usedBy = contextsOf(registry)
    .filter((context) => context.account === account.id)
    .map((context) => context.id);

  return { account, windows, inUse: usedBy.length > 0, usedBy };
}

// firstInUse is the first row, in account order, that is in use, or null when none is.
export function firstInUse(rows: readonly BandRow[]): BandRow | null {
  return rows.find((row) => row.inUse) ?? null;
}
