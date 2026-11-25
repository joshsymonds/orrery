// The usage data the usage interfaces code against: what the band and the accounts page hold, the empty value they hold
// before any read lands, and the windows and span the usage figures are drawn over.

import type { Account, RouteRegistry } from "./gen/orrery/v1/accounts_pb";
import type { UsageReading, UsageSeatSpend } from "./gen/orrery/v1/usage_pb";

// UsageData is the usage a page holds: the accounts and route registry, the latest limit readings, and the seat spend.
// answered is whether Accounts.List, Routes.Get and Usage.Readings have all answered at least once. failed is whether
// the latest read failed.
export interface UsageData {
  accounts: readonly Account[];
  registry: RouteRegistry | undefined;
  readings: readonly UsageReading[];
  // spend is null until a Spend read lands.
  spend: readonly UsageSeatSpend[] | null;
  answered: boolean;
  failed: boolean;
}

// noUsage is the empty UsageData: nothing has answered and nothing has failed.
export const noUsage: UsageData = {
  accounts: [],
  registry: undefined,
  readings: [],
  spend: null,
  answered: false,
  failed: false,
};

// windowClaims are the claims drawn as windows, in column order. 7d_oi is the column headed Fable.
export const windowClaims = ["5h", "7d", "7d_oi"] as const;

// spanSeconds is the span of the spend the usage figures total: seven days, in seconds.
export const spanSeconds = 7 * 86400;
