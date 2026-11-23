// AccountsNarrow draws the accounts and routes page under 760px, as round 8's section F draws its phone page: the Accounts
// list, each account by its name and its plan, token hash and the contexts that use it, with a ⋯ that opens its sheet; the
// Contexts list, each context by its id, the account it sends through and its default seat, with a › that opens a sheet to
// choose its account; and the Seats summary, the count of seats and of each kind. The page head's + opens the Add account
// form in a sheet. An account's sheet holds Rename and Remove, which act as the wide table's do: Remove asks first. The writes
// are the wide sections' own, through renameAccount, removeAccount and chooseAccount, so a refusal and a write with no answer
// read as they do on the wide page.

import type { VNode } from "preact";
import { useState } from "preact/hooks";

import { AccountAdd } from "./accountadd";
import { type Note, removeAccount, renameAccount } from "./accountstable";
import { hashOf, usedBy } from "./accountswrite";
import type { createClients } from "./client";
import type { Account, RouteContext, RouteRegistry } from "./gen/orrery/v1/accounts_pb";
import { Icon } from "./glyphs";
import { chooseAccount } from "./routecontexts";
import { Sheet } from "./sheet";

// ownLogin is what a context with no account sends: the session's own login.
const ownLogin = "the session's own login";

// seatKinds are the auth modes the Seats summary counts, in the order it names them.
const seatKinds = ["forward", "inject", "sigv4"];

export interface AccountsNarrowProps {
  // accounts are the accounts the page holds, in the order Accounts.List answered them.
  accounts: readonly Account[];
  // registry is the route registry the page holds: its contexts are the Contexts list, and its seats the summary.
  registry: RouteRegistry;
  // clients are the Accounts and Routes clients the page's writes go through.
  clients: Pick<ReturnType<typeof createClients>, "accounts" | "routes">;
  // changeAccounts changes the accounts the page holds: update takes the accounts held now and returns the ones to hold.
  changeAccounts: (update: (accounts: readonly Account[]) => readonly Account[]) => void;
  // changeRegistry holds the registry a choice of account returns, so the page draws it.
  changeRegistry: (registry: RouteRegistry) => void;
  // reread re-reads Accounts.List and Routes.Get after a write that needs it, so the page draws what stands.
  reread: () => Promise<RouteRegistry | null>;
  // adding is whether the Add account sheet is raised.
  adding: boolean;
  // addNote is the line drawn under the Accounts list after the Add form closed with a write that had no answer, or null.
  addNote: string | null;
  // addAccount takes the account an answered Add returns, so the page holds it in its list.
  addAccount: (account: Account) => void;
  // closeAdd closes the Add account sheet. note is the line the page draws under its list in the sheet's place, or null.
  closeAdd: (note: string | null) => void;
}

// Opened is the sheet over the page: an account's, by its id, or a context's, by its id.
type Opened = { kind: "account"; id: string } | { kind: "context"; id: string };

// factsOf is an account's line on the Accounts list: its plan, its token hash, and the contexts that use it, when any do.
function factsOf(account: Account, registry: RouteRegistry): string {
  return [account.plan, hashOf(account.fingerprint), usedBy(registry, account.id).join(", ")]
    .filter((part) => part !== "")
    .join(" · ");
}

// senderOf names the account a context sends through, or the session's own login when it has none.
function senderOf(context: RouteContext, accounts: readonly Account[]): string {
  return accounts.find((account) => account.id === context.account)?.name ?? ownLogin;
}

// seatsOf is the Seats summary's two lines: the count of seats, and the count of each kind that has any, such as
// "forward 2 · inject 2 · sigv4 1".
function seatsOf(registry: RouteRegistry): { count: string; kinds: string } {
  const total = registry.seats.length;
  const kinds = seatKinds
    .map((kind) => ({ kind, count: registry.seats.filter((seat) => seat.authMode === kind).length }))
    .filter((entry) => entry.count > 0)
    .map((entry) => `${entry.kind} ${entry.count}`)
    .join(" · ");
  return { count: total === 1 ? "1 seat" : `${total} seats`, kinds };
}

export function AccountsNarrow({
  accounts,
  registry,
  clients,
  changeAccounts,
  changeRegistry,
  reread,
  adding,
  addNote,
  addAccount,
  closeAdd,
}: AccountsNarrowProps): VNode {
  const [opened, setOpened] = useState<Opened | null>(null);
  // accountNotes and contextNotes are what each account's and each context's sheet says after a write, by id.
  const [accountNotes, setAccountNotes] = useState<Readonly<Record<string, Note | undefined>>>({});
  const [contextNotes, setContextNotes] = useState<Readonly<Record<string, Note | undefined>>>({});

  // sayAccount sets the note of the account with id, or clears it when note is undefined.
  const sayAccount = (id: string, note: Note | undefined) => {
    setAccountNotes((current) => ({ ...current, [id]: note }));
  };
  // sayContext sets the note of the context with id, or clears it when note is undefined.
  const sayContext = (id: string, note: Note | undefined) => {
    setContextNotes((current) => ({ ...current, [id]: note }));
  };

  // rename writes the name through renameAccount. Its note, if it has one, is the account's.
  const rename = async (account: Account, name: string) => {
    sayAccount(account.id, undefined);
    const note = await renameAccount(clients, account, name, changeAccounts, reread);
    if (note !== null) {
      sayAccount(account.id, note);
    }
  };

  // remove writes the removal through removeAccount. A removal that lands closes the sheet; any other note stays in it.
  const remove = async (account: Account) => {
    sayAccount(account.id, undefined);
    const note = await removeAccount(clients, account, changeAccounts, reread);
    if (note === null) {
      setOpened(null);
    } else {
      sayAccount(account.id, note);
    }
  };

  // choose sends Routes.SetContextAccount for context through chooseAccount. A choice that lands closes the sheet; a refusal
  // or a write with no answer stays in it, with its note.
  const choose = async (context: RouteContext, account: string) => {
    sayContext(context.id, undefined);
    const note = await chooseAccount(clients, context, account, changeRegistry, reread);
    if (note === null) {
      setOpened(null);
    } else {
      sayContext(context.id, note);
    }
  };

  // goneNotes are the notes of accounts the list no longer draws, such as a Remove that had no answer but landed. They are
  // drawn under the list, so what a write says is never lost with its row.
  const goneNotes = Object.entries(accountNotes).filter(
    (entry): entry is [string, Note] => entry[1] !== undefined && !accounts.some((account) => account.id === entry[0]),
  );
  const openedAccount = opened?.kind === "account" ? accounts.find((account) => account.id === opened.id) : undefined;
  const openedContext = opened?.kind === "context" ? registry.contexts.find((context) => context.id === opened.id) : undefined;
  const seats = seatsOf(registry);

  return (
    <>
      <section class="psec" aria-label="Accounts">
        <div class="psec-h">
          <span class="lbl">Accounts</span>
        </div>
        <div class="phlist">
          {accounts.map((account) => (
            <div key={account.id} class="phli" data-account={account.id}>
              <span class="a">{account.name}</span>
              <span class="b">{factsOf(account, registry)}</span>
              <button
                type="button"
                class="btn quiet icon-only"
                aria-label={`Actions for ${account.name}`}
                onClick={() => setOpened({ kind: "account", id: account.id })}
              >
                <Icon name="more" />
              </button>
            </div>
          ))}
        </div>
        {addNote !== null && (
          <p class="acc-note" data-tone="quiet">
            {addNote}
          </p>
        )}
        {goneNotes.map(([id, note]) => (
          <p key={id} class="acc-note acc-gone-note" data-gone={id} data-tone={note.tone}>
            {note.text}
          </p>
        ))}
      </section>
      <section class="psec" aria-label="Contexts">
        <div class="psec-h">
          <span class="lbl">Contexts</span>
        </div>
        <div class="phlist">
          {registry.contexts.map((context) => (
            <div key={context.id} class="phli" data-context={context.id}>
              <span class="a">{context.id}</span>
              <span class="b">{`${senderOf(context, accounts)} · ${context.defaultSeat}`}</span>
              <button
                type="button"
                class="btn quiet icon-only"
                aria-label={`Account of ${context.id}`}
                onClick={() => setOpened({ kind: "context", id: context.id })}
              >
                <Icon name="chevronRight" />
              </button>
            </div>
          ))}
        </div>
      </section>
      <section class="psec" aria-label="Seats">
        <div class="psec-h">
          <span class="lbl">Seats</span>
        </div>
        <div class="phlist">
          <div class="phli">
            <span class="a">{seats.count}</span>
            {seats.kinds !== "" && <span class="b">{seats.kinds}</span>}
          </div>
        </div>
      </section>
      {adding && (
        <Sheet title="Add account" onClose={() => closeAdd(null)}>
          <div class="sheet-body">
            <AccountAdd clients={clients} added={addAccount} close={closeAdd} reread={reread} />
          </div>
        </Sheet>
      )}
      {openedAccount !== undefined && (
        <Sheet title={openedAccount.name} onClose={() => setOpened(null)}>
          <AccountMenu
            account={openedAccount}
            note={accountNotes[openedAccount.id]}
            rename={(name) => void rename(openedAccount, name)}
            remove={() => void remove(openedAccount)}
          />
        </Sheet>
      )}
      {openedContext !== undefined && (
        <Sheet title={openedContext.id} onClose={() => setOpened(null)}>
          <ContextPick
            context={openedContext}
            accounts={accounts}
            note={contextNotes[openedContext.id]}
            choose={(account) => void choose(openedContext, account)}
          />
        </Sheet>
      )}
    </>
  );
}

interface AccountMenuProps {
  account: Account;
  note: Note | undefined;
  rename: (name: string) => void;
  remove: () => void;
}

// AccountMenu is an account's sheet: its two actions. Rename turns the name into a field with Save and Cancel, and Remove asks
// before it sends, as the wide table's row does. note is the line a write under the sheet said.
function AccountMenu({ account, note, rename, remove }: AccountMenuProps): VNode {
  // mode is what the sheet shows: its actions, the name field, or the question Remove asks.
  const [mode, setMode] = useState<"actions" | "renaming" | "asking">("actions");
  const [draft, setDraft] = useState(account.name);
  return (
    <div class="sheet-body">
      {mode === "renaming" ? (
        <>
          <input
            type="text"
            class="acc-name"
            aria-label={`New name for ${account.name}`}
            value={draft}
            onInput={(event) => setDraft(event.currentTarget.value)}
          />
          <div class="foot">
            <button type="button" class="btn quiet" onClick={() => setMode("actions")}>
              Cancel
            </button>
            <button
              type="button"
              class="btn primary"
              onClick={() => {
                setMode("actions");
                rename(draft);
              }}
            >
              Save
            </button>
          </div>
        </>
      ) : mode === "asking" ? (
        <>
          <p class="sure">Remove {account.name}?</p>
          <div class="foot">
            <button type="button" class="btn quiet" onClick={() => setMode("actions")}>
              Keep
            </button>
            <button
              type="button"
              class="btn primary"
              onClick={() => {
                setMode("actions");
                remove();
              }}
            >
              Remove
            </button>
          </div>
        </>
      ) : (
        <div class="foot">
          <button
            type="button"
            class="btn quiet"
            onClick={() => {
              setDraft(account.name);
              setMode("renaming");
            }}
          >
            Rename
          </button>
          <button type="button" class="btn quiet" onClick={() => setMode("asking")}>
            Remove
          </button>
        </div>
      )}
      {note !== undefined && (
        <p class="acc-note" data-tone={note.tone}>
          {note.text}
        </p>
      )}
    </div>
  );
}

interface ContextPickProps {
  context: RouteContext;
  accounts: readonly Account[];
  note: Note | undefined;
  choose: (account: string) => void;
}

// ContextPick is a context's sheet: the session's own login, then each account, each a button, with the one the context sends
// through marked as current. note is the line a choice under the sheet said.
function ContextPick({ context, accounts, note, choose }: ContextPickProps): VNode {
  const options = [{ id: "", name: ownLogin }, ...accounts.map((account) => ({ id: account.id, name: account.name }))];
  return (
    <div class="sheet-body">
      <div class="phlist">
        {options.map((option) => (
          <button
            key={option.id}
            type="button"
            class="btn quiet phopt"
            aria-current={option.id === context.account ? "true" : undefined}
            onClick={() => choose(option.id)}
          >
            {option.name}
          </button>
        ))}
      </div>
      {note !== undefined && (
        <p class="acc-note" data-tone={note.tone}>
          {note.text}
        </p>
      )}
    </div>
  );
}
