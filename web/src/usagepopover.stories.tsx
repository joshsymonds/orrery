// The stories of the usage popover: the accounts drawn at reading size over invented accounts, contexts and limit readings,
// one story for each state a row takes, and the popover's spend foot over the fixture's invented accounts, contexts,
// readings and seven days of seat spend, drawn with that spend and without it, as the popover draws before a Spend read
// lands. The Use stories draw a row offering Use, a question asked in a row, and the words a refused move leaves in it.
// The narrow stories draw the popover as a sheet, under 760px: its table, a Use's question in a sheet of its own, and the
// words a refused move leaves in that sheet. Every story draws against a fixed clock, and changes nothing.

import { create } from "@bufbuild/protobuf";

import { nothingChanged } from "./accountswrite";
import type { Stories } from "./catalog/story";
import { accounts, registry } from "./fixtures/accounts";
import { at } from "./fixtures/clock";
import { createFixtureTransport } from "./fixtures/transport";
import { usageReadings, usageSpend } from "./fixtures/usage";
import { type Account, AccountSchema, RouteContextSchema, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { UsageReadingSchema } from "./gen/orrery/v1/usage_pb";
import { noUsage, type UsageData } from "./usagedata";
import { bandRows, movingContexts, spendTotals } from "./usagemodel";
import { endWords, type UseCalls, type UseState, UsagePopover as Popover } from "./usagepopover";

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

// fixtureNow is the fixture clock in unix seconds, which the spend stories draw against.
const fixtureNow = Number(at("@14:41"));

// fixtureRows are the rows the fixture's accounts, registry and readings draw, which the spend stories' popover draws.
const fixtureRows = bandRows({ ...noUsage, accounts, registry, readings: usageReadings, answered: true }, fixtureNow);

// spend is the fixture's seven days of seat spend, totalled as the popover's foot totals it.
const spend = spendTotals(usageSpend, registry);

// fixtureRoutes is the fixture's routes client. The Use stories draw with it, and no story presses a move, so no call reaches it.
const fixtureRoutes = createFixtureTransport().clients.routes;

// useCalls are the Use calls a story draws with: the fixture's routes, over the registry given, and no press does anything.
function useCalls(registryHeld: UseCalls["registry"]): UseCalls {
  return { routes: fixtureRoutes, registry: registryHeld, onDone: noop, onRead: noop };
}

// asking is the question Use asks in max-2's row over shared: personal and tiltyard, both on max-1, move to max-2.
const asking: UseState = {
  kind: "asking",
  account: "max-2",
  plan: movingContexts(shared.registry, "max-2"),
  moving: false,
};

// refused is the words a move to max-2 leaves in its row when the call for personal is refused and nothing moved.
const refused: UseState = {
  kind: "words",
  account: "max-2",
  words: endWords(
    {
      kind: "partial",
      reason: `context: unknown id ${nothingChanged}`,
      moved: [],
      notMoved: movingContexts(shared.registry, "max-2"),
    },
    false,
    "max-2",
    shared.accounts,
  ),
};

export const UsagePopover: Stories = {
  ok: () => <Popover rows={bandRows(shared, now)} now={now} onAccounts={noop} onClose={noop} />,
  "warn-and-crit": () => <Popover rows={bandRows(warm, now)} now={now} onAccounts={noop} onClose={noop} />,
  limited: () => <Popover rows={bandRows(limited, now)} now={now} onAccounts={noop} onClose={noop} />,
  "no-reading": () => <Popover rows={bandRows(bare, now)} now={now} onAccounts={noop} onClose={noop} />,
  "with-spend": () => <Popover rows={fixtureRows} now={fixtureNow} spend={spend} onAccounts={noop} onClose={noop} />,
  "before-spend": () => <Popover rows={fixtureRows} now={fixtureNow} spend={null} onAccounts={noop} onClose={noop} />,
  use: () => (
    <Popover rows={bandRows(shared, now)} now={now} use={useCalls(shared.registry)} onAccounts={noop} onClose={noop} />
  ),
  asking: () => (
    <Popover
      rows={bandRows(shared, now)}
      now={now}
      use={useCalls(shared.registry)}
      initialUse={asking}
      onAccounts={noop}
      onClose={noop}
    />
  ),
  refused: () => (
    <Popover
      rows={bandRows(shared, now)}
      now={now}
      use={useCalls(shared.registry)}
      initialUse={refused}
      onAccounts={noop}
      onClose={noop}
    />
  ),
  "narrow-accounts": () => <Popover narrow rows={bandRows(shared, now)} now={now} onAccounts={noop} onClose={noop} />,
  "narrow-asking": () => (
    <Popover
      narrow
      rows={bandRows(shared, now)}
      now={now}
      use={useCalls(shared.registry)}
      initialUse={asking}
      onAccounts={noop}
      onClose={noop}
    />
  ),
  "narrow-refused": () => (
    <Popover
      narrow
      rows={bandRows(shared, now)}
      now={now}
      use={useCalls(shared.registry)}
      initialUse={refused}
      onAccounts={noop}
      onClose={noop}
    />
  ),
};
