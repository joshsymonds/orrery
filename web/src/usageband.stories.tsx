// The stories of UsageBand: the band over the fixture's invented accounts, contexts, limit readings and seat spend, at the
// fixture clock. A story draws what it is given and changes nothing.

import { type Stories } from "./catalog/story";
import { accounts, registry } from "./fixtures/accounts";
import { at } from "./fixtures/clock";
import { usageReadings, usageSpend } from "./fixtures/usage";
import { noUsage } from "./usagedata";
import { UsageBand as UsageBandView } from "./usageband";
import { bandRows, spendTotals } from "./usagemodel";

// fixtureNow is the fixture clock in unix seconds.
const fixtureNow = Number(at("@14:41"));

// rows is the band the fixture's accounts, registry and readings draw.
const rows = bandRows({ ...noUsage, accounts, registry, readings: usageReadings, answered: true }, fixtureNow);

// spend is the fixture's seven days of seat spend, totalled as the band's line totals it.
const spend = spendTotals(usageSpend, registry);

// noPress is the press a story's band is given: none, since a story draws what it is given and changes nothing.
const noPress = () => undefined;

export const UsageBand: Stories = {
  default: () => <UsageBandView rows={rows} now={fixtureNow} spend={spend} onOpen={noPress} onAccounts={noPress} />,
  "before-spend": () => <UsageBandView rows={rows} now={fixtureNow} spend={null} onOpen={noPress} onAccounts={noPress} />,
};
