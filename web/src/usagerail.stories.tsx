// The stories of the collapsed inbox's usage tile: the account in use drawn as two rings, 7d outside and 5h inside, one
// story for each level the rings take. The accounts, contexts and readings are invented, and every story draws against the
// one fixed clock below.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { AccountSchema, type Account, RouteContextSchema, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { UsageReadingSchema } from "./gen/orrery/v1/usage_pb";
import type { UsageData } from "./usagedata";
import { type BandRow, bandRows, firstInUse } from "./usagemodel";
import { UsageRailTile as Tile } from "./usagerail";

const noop = () => undefined;

// now is the story clock, in unix seconds. Every observed time and reset below is set from it.
const now = 1_800_000_000;
const hour = 3600;
const day = 86400;

// account makes an invented account.
function account(id: string): Account {
  return create(AccountSchema, { id, name: id, provider: "claude" });
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

// inUse is the first account in use among the data's rows. The invented data of every story names one.
function inUse(data: UsageData): BandRow {
  const row = firstInUse(bandRows(data, now));
  if (row === null) {
    throw new Error("usagerail stories: the data names no account in use");
  }
  return row;
}

// max1 is in use by personal: its 5h is at 24 percent and its 7d at 54, both in the ok level.
const max1 = inUse({
  accounts: [account("max-1"), account("max-2")],
  registry: create(RouteRegistrySchema, {
    contexts: [create(RouteContextSchema, { id: "personal", account: "max-1" })],
  }),
  readings: [
    reading("max-1", "5h", 0.24, "allowed", now + 2 * hour),
    reading("max-1", "7d", 0.54, "allowed", now + 3 * day),
  ],
  spend: null,
  answered: true,
  failed: false,
});

// max2 is in use by tiltyard: its 5h is at 66 percent, warn, and its 7d at 83, crit.
const max2 = inUse({
  accounts: [account("max-2")],
  registry: create(RouteRegistrySchema, {
    contexts: [create(RouteContextSchema, { id: "tiltyard", account: "max-2" })],
  }),
  readings: [
    reading("max-2", "5h", 0.66, "allowed", now + hour),
    reading("max-2", "7d", 0.83, "allowed", now + 2 * day),
  ],
  spend: null,
  answered: true,
  failed: false,
});

// max3 is in use by work and rejected: both its drawn windows are crit, and its lift is the latest reset of its rejections.
const max3 = inUse({
  accounts: [account("max-3")],
  registry: create(RouteRegistrySchema, {
    contexts: [create(RouteContextSchema, { id: "work", account: "max-3" })],
  }),
  readings: [
    reading("max-3", "5h", 1, "rejected", now + hour),
    reading("max-3", "7d", 0.96, "allowed", now + day),
  ],
  spend: null,
  answered: true,
  failed: false,
});

// bare is in use by a context, with no reading yet: its rings are their tracks alone.
const bare = inUse({
  accounts: [account("pro-1")],
  registry: create(RouteRegistrySchema, {
    contexts: [create(RouteContextSchema, { id: "notes", account: "pro-1" })],
  }),
  readings: [],
  spend: null,
  answered: true,
  failed: false,
});

export const UsageRailTile: Stories = {
  ok: () => <Tile row={max1} onOpen={noop} />,
  "warn-and-crit": () => <Tile row={max2} onOpen={noop} />,
  limited: () => <Tile row={max3} onOpen={noop} />,
  "no-reading": () => <Tile row={bare} onOpen={noop} />,
};
