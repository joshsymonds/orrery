// The usage model: what the inbox band draws, computed from the usage a page holds and a clock. It does no I/O. Every time
// it takes or returns is unix seconds, as a number; a clock is passed in as now.

import type { Account } from "./gen/orrery/v1/accounts_pb";
import type { UsageReading } from "./gen/orrery/v1/usage_pb";
import { windowClaims, type UsageData } from "./usagedata";

// Level is the colour a window takes: ok under 60 percent, warn from 60 to under 80, crit from 80.
type Level = "ok" | "warn" | "crit";

// levelOf is the level of a percent of a limit in use.
function levelOf(percent: number): Level {
  if (percent >= 80) return "crit";
  if (percent >= 60) return "warn";
  return "ok";
}

// isCurrent is whether a reading holds now: it is not dropped, and its reset is unset or later than now. A reading
// whose reset is at or before now has passed.
function isCurrent(reading: UsageReading, now: number): boolean {
  return !reading.dropped && (reading.reset === undefined || Number(reading.reset) > now);
}

// WindowDraw is one window column of a row. percent is the reading's share of the limit in whole percent, or null when
// the reading gives no utilization. reset is when the limit resets, or null when the reading gives none. level is
// levelOf the percent, and null when there is no percent to level.
export interface WindowDraw {
  percent: number | null;
  reset: number | null;
  level: Level | null;
}

// BandRow is one account as the band draws it. windows has one entry per windowClaims claim, in that order: null when no
// current reading carries the claim. inUse is whether a context names the account.
export interface BandRow {
  account: Account;
  windows: (WindowDraw | null)[];
  inUse: boolean;
}

// bandRows is one row per account, in the order of the accounts. Each row draws the current readings of its own account.
export function bandRows(data: UsageData, now: number): BandRow[] {
  const contexts = data.registry?.contexts ?? [];
  return data.accounts.map((account) => {
    const current = data.readings.filter((reading) => reading.account === account.id && isCurrent(reading, now));
    const windows = windowClaims.map((claim): WindowDraw | null => {
      const reading = current.find((candidate) => candidate.claim === claim);
      if (reading === undefined) return null;
      const percent = reading.utilization === undefined ? null : Math.round(reading.utilization * 100);
      return {
        percent,
        reset: reading.reset === undefined ? null : Number(reading.reset),
        level: percent === null ? null : levelOf(percent),
      };
    });
    return { account, windows, inUse: contexts.some((context) => context.account === account.id) };
  });
}
