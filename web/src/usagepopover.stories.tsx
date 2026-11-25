// The stories of the usage popover: the accounts drawn at reading size over invented accounts, contexts and limit readings,
// one story for each state a row takes. Every story draws against the one fixed clock below, and changes nothing.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { type Account, AccountSchema, RouteContextSchema, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { UsageReadingSchema } from "./gen/orrery/v1/usage_pb";
import type { UsageData } from "./usagedata";
import { bandRows } from "./usagemodel";
import { UsagePopover as Popover } from "./usagepopover";

const noop = () => undefined;

// now is the story clock, in unix seconds. Every observed time and reset below is set from it.
const now = 1_800_000_000;
const hour = 3600;
const day = 86400;

// account makes an invented account of the plan given.
function account(id: string, plan: string): Account {
  return create(AccountSchema, { id, name: id, provider: "claude", plan });
}

// reading makes one invented limit reading of an account's claim, observed a minute before now.
function reading(id: string, claim: string, utilization: number, status: string, reset: number) {
  return create(UsageReadingSchema, {
    account: id,
    claim,
    utilization,
    reset: BigInt(reset),
    status,
    observed: BigInt(now - 60),
    dropped: false,
  });
}

// dataOf is the usage a story draws: the accounts, the contexts, each naming an account or none, and the readings.
function dataOf(accounts: Account[], contexts: [string, string][], readings: ReturnType<typeof reading>[]): UsageData {
  return {
    accounts,
    registry: create(RouteRegistrySchema, {
      contexts: contexts.map(([id, owner]) => create(RouteContextSchema, { id, account: owner })),
    }),
    readings,
    spend: null,
    answered: true,
    failed: false,
  };
}

// shared is the popover's first state: two accounts at ok, personal and tiltyard both naming max-1, which is in use.
const shared = dataOf(
  [account("max-1", "Claude Max"), account("max-2", "Claude Max")],
  [
    ["personal", "max-1"],
    ["tiltyard", "max-1"],
  ],
  [
    reading("max-1", "5h", 0.24, "allowed", now + 2 * hour),
    reading("max-1", "7d", 0.54, "allowed", now + 3 * day),
    reading("max-2", "5h", 0.1, "allowed", now + hour),
    reading("max-2", "7d", 0.2, "allowed", now + 2 * day),
  ],
);

// warm is an account at warn and crit: its 5h at 66 percent, warn, and its 7d at 83, crit.
const warm = dataOf(
  [account("max-2", "Claude Max")],
  [["work", "max-2"]],
  [reading("max-2", "5h", 0.66, "allowed", now + hour), reading("max-2", "7d", 0.83, "allowed", now + 2 * day)],
);

// limited is an account rejected on its 5h, whose limit lifts an hour and ten minutes from now.
const limited = dataOf(
  [account("max-3", "Claude Max")],
  [],
  [reading("max-3", "5h", 1, "rejected", now + 70 * 60), reading("max-3", "7d", 0.96, "allowed", now + day)],
);

// bare is an account with no reading yet: its windows read no reading, and under its name stands its plan alone.
const bare = dataOf([account("pro-1", "Claude Pro")], [], []);

export const UsagePopover: Stories = {
  ok: () => <Popover rows={bandRows(shared, now)} now={now} onAccounts={noop} onClose={noop} />,
  "warn-and-crit": () => <Popover rows={bandRows(warm, now)} now={now} onAccounts={noop} onClose={noop} />,
  limited: () => <Popover rows={bandRows(limited, now)} now={now} onAccounts={noop} onClose={noop} />,
  "no-reading": () => <Popover rows={bandRows(bare, now)} now={now} onAccounts={noop} onClose={noop} />,
};
