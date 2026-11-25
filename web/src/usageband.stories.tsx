// The stories of UsageBand: the band over the fixture's invented accounts, contexts, limit readings and seat spend, at the
// fixture clock. A story draws what it is given and changes nothing.

import { create } from "@bufbuild/protobuf";

import { type Stories } from "./catalog/story";
import { accounts, registry } from "./fixtures/accounts";
import { at } from "./fixtures/clock";
import { usageReadings, usageSpend } from "./fixtures/usage";
import { type UsageReading, UsageReadingSchema } from "./gen/orrery/v1/usage_pb";
import { noUsage } from "./usagedata";
import { UsageBand as UsageBandView } from "./usageband";
import { bandRows, spendTotals } from "./usagemodel";

// fixtureNow is the fixture clock in unix seconds.
const fixtureNow = Number(at("@14:41"));
const minute = 60;
const hour = 3600;
const day = 86400;

// rows is the band the fixture's accounts, registry and readings draw.
const rows = bandRows({ ...noUsage, accounts, registry, readings: usageReadings, answered: true }, fixtureNow);

// spend is the fixture's seven days of seat spend, totalled as the band's line totals it.
const spend = spendTotals(usageSpend, registry);

// noPress is the press a story's band is given: none, since a story draws what it is given and changes nothing.
const noPress = () => undefined;

// reading is one invented limit reading of account for claim: its status, its share of the limit in use, when its limit
// resets and when it was observed, in unix seconds.
function reading(
  account: string,
  claim: string,
  status: string,
  utilization: number,
  reset: number,
  observed: number,
): UsageReading {
  return create(UsageReadingSchema, {
    account,
    claim,
    status,
    utilization,
    reset: BigInt(reset),
    observed: BigInt(observed),
    dropped: false,
  });
}

// bandOf draws the band over the accounts given by id, with the readings given, at the fixture clock.
function bandOf(ids: readonly string[], readings: readonly UsageReading[]) {
  const shown = bandRows(
    { ...noUsage, accounts: accounts.filter((account) => ids.includes(account.id)), registry, readings, answered: true },
    fixtureNow,
  );
  return <UsageBandView rows={shown} now={fixtureNow} spend={null} onOpen={noPress} onAccounts={noPress} />;
}

export const UsageBand: Stories = {
  default: () => <UsageBandView rows={rows} now={fixtureNow} spend={spend} onOpen={noPress} onAccounts={noPress} />,
  "before-spend": () => <UsageBandView rows={rows} now={fixtureNow} spend={null} onOpen={noPress} onAccounts={noPress} />,
  // limited: max-1 is rejected in its 5h and 7d windows, so its dot is the limited glyph and its last column the time
  // until its 5h limit lifts; max-2 is not limited.
  limited: () =>
    bandOf(
      ["max-1", "max-2"],
      [
        reading("max-1", "5h", "rejected", 1, fixtureNow + hour, fixtureNow - minute),
        reading("max-1", "7d", "rejected", 0.96, fixtureNow + 2 * day, fixtureNow - minute),
        reading("max-2", "7d", "allowed", 0.72, fixtureNow + 2 * day, fixtureNow - minute),
      ],
    ),
  // stale: max-2's 5h is fresh, but its 7d_oi was observed two hours ago, so its last column is that age; pro-1 was read
  // two hours ago, so its last column is its age too.
  stale: () =>
    bandOf(
      ["max-2", "pro-1"],
      [
        reading("max-2", "5h", "allowed", 0.12, fixtureNow + 2 * hour, fixtureNow - minute),
        reading("max-2", "7d_oi", "allowed", 0.4, fixtureNow + 3 * day, fixtureNow - 2 * hour),
        reading("pro-1", "7d", "allowed", 0.18, fixtureNow + 4 * day, fixtureNow - 2 * hour),
      ],
    ),
  // no-reading: pro-1 has no current reading, so it reads no reading yet across its windows.
  "no-reading": () =>
    bandOf(["max-1", "pro-1"], [reading("max-1", "5h", "allowed", 0.24, fixtureNow + 2 * hour, fixtureNow - minute)]),
};
