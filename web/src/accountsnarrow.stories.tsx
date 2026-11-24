// The stories of AccountsNarrow: the page's phone layout over the invented accounts and registry the fixture starts from. Its
// sheets are raised by a press, so the stories draw the list, with and without a note under it.

import { AccountsNarrow as AccountsNarrowView } from "./accountsnarrow";
import type { Stories } from "./catalog/story";
import { accounts, registry } from "./fixtures/accounts";
import { createFixtureTransport } from "./fixtures/transport";

const clients = createFixtureTransport().clients;

export const AccountsNarrow: Stories = {
  list: () => (
    <AccountsNarrowView
      accounts={accounts}
      registry={registry}
      clients={clients}
      changeAccounts={() => undefined}
      changeRegistry={() => undefined}
      reread={() => Promise.resolve(null)}
      adding={false}
      addNote={null}
      addAccount={() => undefined}
      closeAdd={() => undefined}
    />
  ),
  "with-note": () => (
    <AccountsNarrowView
      accounts={accounts}
      registry={registry}
      clients={clients}
      changeAccounts={() => undefined}
      changeRegistry={() => undefined}
      reread={() => Promise.resolve(null)}
      adding={false}
      addNote="Could not tell whether that landed: no answer came. This is what stands now."
      addAccount={() => undefined}
      closeAdd={() => undefined}
    />
  ),
};
