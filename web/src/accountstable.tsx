// AccountsTable draws the Accounts section of round 8's page D: each account's name, plan, token hash, the contexts that use
// it and the day it was added, with Rename and Remove on each row. Rename turns the name into a field with Save and Cancel.
// Remove asks in the row first. A write that is refused or has no answer re-reads the accounts and routes, and the row then
// says what stands.

import { Code } from "@connectrpc/connect";
import { Fragment, type VNode } from "preact";
import { useState } from "preact/hooks";

import { couldNotTell, hashOf, nothingChanged, runWrite, usedBy } from "./accountswrite";
import type { createClients } from "./client";
import type { Account, RouteRegistry } from "./gen/orrery/v1/accounts_pb";

export interface AccountsTableProps {
  // accounts are the accounts the page holds, in the order Accounts.List answered them.
  accounts: readonly Account[];
  // registry is the route registry the page holds, which says which contexts use each account.
  registry: RouteRegistry;
  // clients are the Accounts and Routes clients the table writes through.
  clients: Pick<ReturnType<typeof createClients>, "accounts" | "routes">;
  // changeAccounts changes the accounts the page holds: update takes the accounts held now and returns the ones to hold.
  changeAccounts: (update: (accounts: readonly Account[]) => readonly Account[]) => void;
  // reread re-reads Accounts.List and Routes.Get and holds what they answer. It resolves with the registry Routes.Get
  // answered, or with null when a read fails, and the page then draws the failure instead of the table.
  reread: () => Promise<RouteRegistry | null>;
}

// Editing is the row whose name is a field, with the name the field holds.
interface Editing {
  id: string;
  name: string;
}

// Note is what a row says after a write: its text, and a tone of bad for a refusal or quiet for a write that could not be
// told.
interface Note {
  text: string;
  tone: "bad" | "quiet";
}

const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

// addedOf is the day an account was created, as its month and day in UTC, such as Nov 12.
function addedOf(created: bigint): string {
  const day = new Date(Number(created) * 1000);
  return `${months[day.getUTCMonth()]} ${day.getUTCDate()}`;
}

// joinNames lists names as a sentence does: a, a and b, or a, b and c.
function joinNames(names: readonly string[]): string {
  if (names.length === 1) {
    return names[0];
  }
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

export function AccountsTable({ accounts, registry, clients, changeAccounts, reread }: AccountsTableProps): VNode {
  const [editing, setEditing] = useState<Editing | null>(null);
  const [asking, setAsking] = useState<string | null>(null);
  const [notes, setNotes] = useState<Readonly<Record<string, Note | undefined>>>({});

  // say sets the note of the row with id, or clears it when note is undefined.
  const say = (id: string, note: Note | undefined) => {
    setNotes((current) => ({ ...current, [id]: note }));
  };

  // rename sends Accounts.Rename for account. An answer holds the account it returns. A refusal, or a write with no answer,
  // re-reads the accounts and routes, and the row says what stands.
  const rename = async (account: Account, name: string) => {
    say(account.id, undefined);
    const outcome = await runWrite(() => clients.accounts.rename({ id: account.id, name }));
    if (outcome.kind === "done" && outcome.answer.account !== undefined) {
      const renamed = outcome.answer.account;
      changeAccounts((held) => held.map((candidate) => (candidate.id === renamed.id ? renamed : candidate)));
      return;
    }
    await reread();
    say(
      account.id,
      outcome.kind === "refused"
        ? { text: `Not renamed: ${outcome.reason} ${nothingChanged}`, tone: "bad" }
        : { text: couldNotTell, tone: "quiet" },
    );
  };

  // remove sends Accounts.Remove for account. An answer takes its row away. A refusal as in use re-reads the accounts and
  // routes, then names every context the fresh registry holds the account for, and the table stays as it is. Any other
  // refusal re-reads and says why nothing changed. A write with no answer re-reads and says it could not tell.
  const remove = async (account: Account) => {
    say(account.id, undefined);
    const outcome = await runWrite(() => clients.accounts.remove({ id: account.id }));
    if (outcome.kind === "done") {
      changeAccounts((held) => held.filter((candidate) => candidate.id !== account.id));
      return;
    }
    const fresh = await reread();
    if (outcome.kind === "unknown") {
      say(account.id, { text: couldNotTell, tone: "quiet" });
      return;
    }
    const holders = fresh === null ? [] : usedBy(fresh, account.id);
    if (outcome.code === Code.FailedPrecondition && holders.length > 0) {
      say(account.id, {
        text: `${account.name} is in use by ${joinNames(holders)}. Move them to another account first, then remove it. ${nothingChanged}`,
        tone: "bad",
      });
      return;
    }
    say(account.id, { text: `Not removed: ${outcome.reason} ${nothingChanged}`, tone: "bad" });
  };

  return (
    <div class="acc-wrap">
      <table class="acc-table">
        <thead>
          <tr>
            <th>name</th>
            <th>plan</th>
            <th>token</th>
            <th>used by</th>
            <th>added</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {accounts.map((account) => {
            const draft = editing?.id === account.id ? editing : null;
            const holders = usedBy(registry, account.id);
            const note = notes[account.id];
            return (
              <Fragment key={account.id}>
                <tr data-account={account.id}>
                  <td class="main">
                    {draft === null ? (
                      account.name
                    ) : (
                      <input
                        type="text"
                        class="acc-name"
                        aria-label={`New name for ${account.name}`}
                        value={draft.name}
                        onInput={(event) => setEditing({ id: account.id, name: event.currentTarget.value })}
                      />
                    )}
                  </td>
                  <td>{account.plan}</td>
                  <td class="code">{hashOf(account.fingerprint)}</td>
                  <td class={holders.length === 0 ? "ink-4" : undefined}>
                    {holders.length === 0 ? "none" : holders.join(", ")}
                  </td>
                  <td>{addedOf(account.created)}</td>
                  <td class="acts">
                    {draft !== null ? (
                      <>
                        <button
                          type="button"
                          class="btn primary"
                          onClick={() => {
                            setEditing(null);
                            void rename(account, draft.name);
                          }}
                        >
                          Save
                        </button>
                        <button type="button" class="btn quiet" onClick={() => setEditing(null)}>
                          Cancel
                        </button>
                      </>
                    ) : asking === account.id ? (
                      <>
                        <span class="sure">Remove {account.name}?</span>
                        <button type="button" class="btn quiet" onClick={() => setAsking(null)}>
                          Keep
                        </button>
                        <button
                          type="button"
                          class="btn primary"
                          onClick={() => {
                            setAsking(null);
                            void remove(account);
                          }}
                        >
                          Remove
                        </button>
                      </>
                    ) : (
                      <>
                        <button
                          type="button"
                          class="btn quiet"
                          onClick={() => setEditing({ id: account.id, name: account.name })}
                        >
                          Rename
                        </button>
                        <button type="button" class="btn quiet" onClick={() => setAsking(account.id)}>
                          Remove
                        </button>
                      </>
                    )}
                  </td>
                </tr>
                {note !== undefined ? (
                  <tr class="acc-note-row">
                    <td colSpan={6}>
                      <p class="acc-note" data-tone={note.tone}>
                        {note.text}
                      </p>
                    </td>
                  </tr>
                ) : null}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
