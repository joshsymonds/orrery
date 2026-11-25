// The stories of the usage popover's spend foot: the popover of the account in use in the fixture, drawn with the fixture's
// seven days of seat spend, and without it, as the popover draws before a Spend read lands. The accounts, contexts,
// readings and spend are the fixture's invented ones, and every story draws against the fixture clock. A story draws what
// it is given and changes nothing.

import type { Stories } from "./catalog/story";
import { accounts, registry } from "./fixtures/accounts";
import { at } from "./fixtures/clock";
import { usageReadings, usageSpend } from "./fixtures/usage";
import { noUsage } from "./usagedata";
import { type BandRow, bandRows, firstInUse, spendTotals } from "./usagemodel";
import { UsagePopover as Popover } from "./usagepopover";

// fixtureNow is the fixture clock in unix seconds.
const fixtureNow = Number(at("@14:41"));

// inUse is the account in use in the fixture, the one the popover draws.
const inUse = firstInUse(bandRows({ ...noUsage, accounts, registry, readings: usageReadings, answered: true }, fixtureNow));
if (inUse === null) {
  throw new Error("usagepopover stories: the fixture names no account in use");
}
const row: BandRow = inUse;

// spend is the fixture's seven days of seat spend, totalled as the popover's foot totals it.
const spend = spendTotals(usageSpend, registry);

export const UsagePopover: Stories = {
  "with-spend": () => <Popover row={row} now={fixtureNow} spend={spend} />,
  "before-spend": () => <Popover row={row} now={fixtureNow} spend={null} />,
};
