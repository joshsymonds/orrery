// AccountsPage draws the accounts and routes page at #/accounts. For now it draws only its title: the page's accounts,
// contexts and seats come in later tasks.

import type { VNode } from "preact";

import type { createClients } from "./client";

interface AccountsPageProps {
  // clients are the Accounts and Routes clients the page reads and writes through, as createClients makes them.
  clients: Pick<ReturnType<typeof createClients>, "accounts" | "routes">;
  // narrow is whether the page is drawn at a phone's width.
  narrow: boolean;
}

export function AccountsPage(_props: AccountsPageProps): VNode {
  return <h1>Accounts and routes</h1>;
}
