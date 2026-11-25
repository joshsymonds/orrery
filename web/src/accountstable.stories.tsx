// The stories of AccountsTable: the table over the fixture's invented accounts and registry, and over a registry where max-1
// is used by two contexts, so its row names both. The table's writes go to the fixture's clients, and its re-read answers the
// registry the story is given, so a story draws what it was given and changes nothing.

import { create } from "@bufbuild/protobuf";

import { AccountsTable as AccountsTableView } from "./accountstable";
import type { Stories } from "./catalog/story";
import { accounts, registry } from "./fixtures/accounts";
import { createFixtureTransport } from "./fixtures/transport";
import { type RouteRegistry, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";

const clients = createFixtureTransport().clients;

// shared is the fixture's registry with a second context, tiltyard, that uses max-1 as personal does.
const shared = create(RouteRegistrySchema, {
  defaultContext: "personal",
  contexts: [
    { id: "personal", defaultSeat: "anthropic", account: "max-1" },
    { id: "tiltyard", defaultSeat: "anthropic", account: "max-1" },
    { id: "work", defaultSeat: "work-bedrock", account: "" },
  ],
});

// holdsAccounts is the change a story makes to the accounts the page holds: none, since a story draws what it is given.
const holdsAccounts = () => undefined;

// rereadsTo is the re-read a story's table is given: it answers the registry the story draws.
const rereadsTo = (draws: RouteRegistry) => () => Promise.resolve(draws);

export const AccountsTable: Stories = {
  default: () => (
    <AccountsTableView
      accounts={accounts}
      registry={registry}
      clients={clients}
      changeAccounts={holdsAccounts}
      reread={rereadsTo(registry)}
    />
  ),
  shared: () => (
    <AccountsTableView
      accounts={accounts}
      registry={shared}
      clients={clients}
      changeAccounts={holdsAccounts}
      reread={rereadsTo(shared)}
    />
  ),
};
