// The stories of AccountsPage: the page at the wide width and at the narrow one. The page is drawn over the fixture's
// accounts and routes, which the stub does not read yet.

import { AccountsPage as AccountsPageView } from "./accountspage";
import type { Stories } from "./catalog/story";
import { createFixtureTransport } from "./fixtures/transport";

const clients = createFixtureTransport().clients;

export const AccountsPage: Stories = {
  wide: () => <AccountsPageView clients={clients} narrow={false} />,
  narrow: () => <AccountsPageView clients={clients} narrow={true} />,
};
