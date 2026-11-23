// AccountsPage draws the accounts and routes page at #/accounts: its head, then the Accounts section, which is the accounts
// table, then the Seats section, which is the seats table. On mount it reads Accounts.List and Routes.Get and holds both. A
// read that fails draws its reason with a Retry. Later tasks add the Add button and the Contexts, which go before the Seats.

import { create } from "@bufbuild/protobuf";
import { ConnectError } from "@connectrpc/connect";
import type { VNode } from "preact";
import { useCallback, useEffect, useState } from "preact/hooks";

import { AccountsTable } from "./accountstable";
import type { createClients } from "./client";
import { type Account, type RouteRegistry, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { RouteSeats } from "./routeseats";

type Clients = Pick<ReturnType<typeof createClients>, "accounts" | "routes">;

interface AccountsPageProps {
  // clients are the Accounts and Routes clients the page reads and writes through, as createClients makes them.
  clients: Clients;
  // narrow is whether the page is drawn at a phone's width.
  narrow: boolean;
}

// Held is what the page holds from its last read: the accounts Accounts.List answered and the registry Routes.Get answered.
interface Held {
  accounts: readonly Account[];
  registry: RouteRegistry;
}

// Read is the page's last read: what it holds, or the reason the read failed.
type Read = { kind: "held"; held: Held } | { kind: "failed"; reason: string };

// reasonOf is the reason a failed read gives: a Connect error's message as the server wrote it, else the error's own.
function reasonOf(error: unknown): string {
  if (error instanceof ConnectError) {
    return error.rawMessage;
  }
  return error instanceof Error ? error.message : String(error);
}

// readBoth reads Accounts.List and Routes.Get together. It holds what both answer, or the reason one of them failed.
async function readBoth(clients: Clients): Promise<Read> {
  try {
    const [listed, got] = await Promise.all([clients.accounts.list({}), clients.routes.get({})]);
    return { kind: "held", held: { accounts: listed.accounts, registry: got.registry ?? create(RouteRegistrySchema) } };
  } catch (error) {
    return { kind: "failed", reason: reasonOf(error) };
  }
}

export function AccountsPage({ clients, narrow }: AccountsPageProps): VNode {
  const [read, setRead] = useState<Read | null>(null);

  // reread reads both again and holds what they answer. It resolves with the registry held, or with null when a read failed.
  const reread = useCallback(async (): Promise<RouteRegistry | null> => {
    const next = await readBoth(clients);
    setRead(next);
    return next.kind === "held" ? next.held.registry : null;
  }, [clients]);

  useEffect(() => {
    void reread();
  }, [reread]);

  // changeAccounts holds the accounts update returns, from the accounts held now. The registry held stays as it is.
  const changeAccounts = useCallback((update: (accounts: readonly Account[]) => readonly Account[]) => {
    setRead((current) =>
      current?.kind === "held" ? { kind: "held", held: { ...current.held, accounts: update(current.held.accounts) } } : current,
    );
  }, []);

  return (
    <div class={narrow ? "accounts-page narrow" : "accounts-page"}>
      <div class="ptitle">
        <h2>Accounts and routes</h2>
      </div>
      <div class="pbody">
        {read?.kind === "failed" ? (
          <p class="page-note">
            {`Could not read the accounts and routes: ${read.reason}`}{" "}
            <button type="button" class="btn quiet" onClick={() => void reread()}>
              Retry
            </button>
          </p>
        ) : null}
        {read?.kind === "held" ? (
          <section class="psec" aria-label="Accounts">
            <div class="psec-h">
              <span class="lbl">Accounts</span>
            </div>
            <AccountsTable
              accounts={read.held.accounts}
              registry={read.held.registry}
              clients={clients}
              changeAccounts={changeAccounts}
              reread={reread}
            />
          </section>
        ) : null}
        {read?.kind === "held" ? (
          <section class="psec" aria-label="Seats">
            <div class="psec-h">
              <span class="lbl">Seats</span>
            </div>
            <RouteSeats registry={read.held.registry} />
          </section>
        ) : null}
      </div>
    </div>
  );
}
