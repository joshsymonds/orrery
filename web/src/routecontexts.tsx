// RouteContexts draws the Contexts section of round 8's page D: a card for each context of the registry the page holds, in
// registry order. Each card shows the context's id, a default tag when it is the registry's default, an account select, its
// default seat, its count of model bindings, and its subagents' seats when set. The select sends Routes.SetContextAccount for
// that context alone. Edit opens a table with one row per model selector the context has, each with a seat select; Save reads
// Routes.Get afresh, sets the seats the edit changed in that fresh registry, and sends Routes.Replace with it. An answer's
// registry is drawn in place of the one held; a refusal leaves the select on the account it stood on and says why; a write
// with no answer re-reads the accounts and routes and says it could not tell.

import { clone, create } from "@bufbuild/protobuf";
import { Fragment, type VNode } from "preact";
import { useState } from "preact/hooks";

import type { Note } from "./accountstable";
import { couldNotTell, nothingChanged, runWrite } from "./accountswrite";
import type { createClients } from "./client";
import {
  type Account,
  type RouteContext,
  type RouteRegistry,
  RouteRegistrySchema,
  type RouteSubagents,
} from "./gen/orrery/v1/accounts_pb";

export interface RouteContextsProps {
  // accounts are the accounts the page holds, in the order Accounts.List answered them. The select offers each by name.
  accounts: readonly Account[];
  // registry is the route registry the page holds, whose contexts the section draws, in registry order.
  registry: RouteRegistry;
  // clients are the Routes client the section writes through.
  clients: Pick<ReturnType<typeof createClients>, "routes">;
  // changeRegistry holds the registry an answer returns, so the page draws it, and every card with it.
  changeRegistry: (registry: RouteRegistry) => void;
  // reread re-reads Accounts.List and Routes.Get and holds what they answer. It resolves with the registry Routes.Get
  // answered, or with null when a read fails, and the page then draws the failure instead of the section.
  reread: () => Promise<RouteRegistry | null>;
}

// Editing is the card whose Edit is open: its context's id, the seat each of its model selectors held when Edit opened, and
// the seat each selector's row holds now.
interface Editing {
  id: string;
  opened: Readonly<Record<string, string>>;
  rows: Readonly<Record<string, string>>;
}

// SubagentSeat is one seat a card's subagents facts name: the agent type it is for, or null for the default seat, and the
// seat's id.
interface SubagentSeat {
  agentType: string | null;
  seat: string;
}

// subagentSeatsOf lists the seats a context's subagents are sent to: the default seat first, when set, then each agent type's
// seat in the order the registry holds them. A context whose subagents name no seat has none.
function subagentSeatsOf(subagents: RouteSubagents | undefined): SubagentSeat[] {
  if (subagents === undefined) {
    return [];
  }
  const seats: SubagentSeat[] = [];
  if (subagents.defaultSeat !== "") {
    seats.push({ agentType: null, seat: subagents.defaultSeat });
  }
  for (const [agentType, seat] of Object.entries(subagents.agentTypes)) {
    seats.push({ agentType, seat });
  }
  return seats;
}

// chooseAccount sends Routes.SetContextAccount for context, naming account, and gives null when the answer's registry is
// held through changeRegistry. A refusal gives the note that says why nothing changed. A write with no answer, or an answer
// with no registry, re-reads the accounts and routes and gives the note that says it could not tell.
export async function chooseAccount(
  clients: Pick<ReturnType<typeof createClients>, "routes">,
  context: RouteContext,
  account: string,
  changeRegistry: (registry: RouteRegistry) => void,
  reread: () => Promise<RouteRegistry | null>,
): Promise<Note | null> {
  const outcome = await runWrite(() => clients.routes.setContextAccount({ context: context.id, account }));
  if (outcome.kind === "done" && outcome.answer.registry !== undefined) {
    changeRegistry(outcome.answer.registry);
    return null;
  }
  if (outcome.kind === "refused") {
    return { text: `Not set: ${outcome.reason} ${nothingChanged}`, tone: "bad" };
  }
  await reread();
  return { text: couldNotTell, tone: "quiet" };
}

export function RouteContexts({ accounts, registry, clients, changeRegistry, reread }: RouteContextsProps): VNode {
  const [notes, setNotes] = useState<Readonly<Record<string, Note | undefined>>>({});
  const [editing, setEditing] = useState<Editing | null>(null);
  const [saving, setSaving] = useState(false);

  // say sets the note of the card with id, or clears it when note is undefined.
  const say = (id: string, note: Note | undefined) => {
    setNotes((current) => ({ ...current, [id]: note }));
  };

  // choose writes the account through chooseAccount, and says what the write's note is, if any, on the card. A card's note
  // clears when the write lands.
  const choose = async (context: RouteContext, account: string) => {
    const note = await chooseAccount(clients, context, account, changeRegistry, reread);
    say(context.id, note === null ? undefined : note);
  };

  // edit opens context's card for editing, with each of its model selectors' seats as the registry holds them.
  const edit = (context: RouteContext) => {
    const seats = { ...context.models };
    setEditing({ id: context.id, opened: seats, rows: seats });
  };

  // pick sets the seat of the open edit's row for selector to seat.
  const pick = (selector: string, seat: string) => {
    setEditing((current) => (current === null ? null : { ...current, rows: { ...current.rows, [selector]: seat } }));
  };

  // save sends the edit of the card with id. It reads Routes.Get afresh, and in that registry sets the seat of each selector
  // the edit changed, for the selectors the fresh context still has, and leaves everything else as the read answered. It sends
  // Routes.Replace with that registry. An answer holds the registry it returns and closes the edit. A refusal keeps the edit
  // open and says why nothing changed. A fresh read that fails, or a fresh registry that no longer holds the context, sends
  // nothing and says why nothing changed. A write with no answer closes the edit, re-reads the accounts and routes, and says
  // it could not tell.
  const save = async (id: string, draft: Editing) => {
    setSaving(true);
    say(id, undefined);
    const fresh = await runWrite(() => clients.routes.get({}));
    if (fresh.kind !== "done") {
      setSaving(false);
      const why = fresh.kind === "refused" ? fresh.reason : "the routes could not be read.";
      say(id, { text: `Not saved: ${why} ${nothingChanged}`, tone: "bad" });
      return;
    }
    const next = clone(RouteRegistrySchema, fresh.answer.registry ?? create(RouteRegistrySchema));
    const target = next.contexts.find((candidate) => candidate.id === id);
    if (target === undefined) {
      setSaving(false);
      say(id, { text: `Not saved: ${id} is no longer in the routes. ${nothingChanged}`, tone: "bad" });
      return;
    }
    for (const selector of Object.keys(draft.rows)) {
      if (draft.rows[selector] !== draft.opened[selector] && Object.hasOwn(target.models, selector)) {
        target.models[selector] = draft.rows[selector];
      }
    }
    const outcome = await runWrite(() => clients.routes.replace({ registry: next }));
    setSaving(false);
    if (outcome.kind === "done" && outcome.answer.registry !== undefined) {
      changeRegistry(outcome.answer.registry);
      setEditing(null);
      say(id, undefined);
      return;
    }
    if (outcome.kind === "refused") {
      say(id, { text: `Not saved: ${outcome.reason} ${nothingChanged}`, tone: "bad" });
      return;
    }
    setEditing(null);
    await reread();
    say(id, { text: couldNotTell, tone: "quiet" });
  };

  return (
    <section class="psec" aria-label="Contexts">
      <div class="psec-h">
        <span class="lbl">Contexts</span>
      </div>
      <p class="ctx-note">
        A session reaches a context by its base URL. A context's account is the token its forward seats send; with none, they
        send the session's own login.
      </p>
      <div class="ctx-cards">
        {registry.contexts.map((context) => {
          const note = notes[context.id];
          const seats = subagentSeatsOf(context.subagents);
          const draft = editing?.id === context.id ? editing : null;
          return (
            <article key={context.id} class="ctx-card" data-context={context.id}>
              <div class="ctx-card-h">
                <span class="ctx-name">{context.id}</span>
                {registry.defaultContext === context.id ? <span class="chip">default</span> : null}
                <span class="ctx-account">
                  <span>account</span>
                  <select
                    class="ctx-select"
                    aria-label={`Account of ${context.id}`}
                    value={context.account}
                    onChange={(event) => void choose(context, event.currentTarget.value)}
                  >
                    <option value="">the session's own login</option>
                    {accounts.map((account) => (
                      <option key={account.id} value={account.id}>
                        {account.name}
                      </option>
                    ))}
                  </select>
                </span>
                {draft === null ? (
                  <button type="button" class="btn quiet" disabled={saving} onClick={() => edit(context)}>
                    Edit
                  </button>
                ) : null}
              </div>
              <div class="ctx-facts">
                <span>
                  default seat <b>{context.defaultSeat}</b>
                </span>
                <span>
                  models <b>{Object.keys(context.models).length}</b>
                </span>
                {seats.length > 0 ? (
                  <span>
                    subagents{" "}
                    {seats.map((seat, index) => (
                      <Fragment key={index}>
                        {index > 0 ? ", " : null}
                        {seat.agentType !== null ? `${seat.agentType} ` : null}
                        <b>{seat.seat}</b>
                      </Fragment>
                    ))}
                  </span>
                ) : null}
              </div>
              {draft !== null ? (
                <div class="ctx-edit-wrap">
                  <table class="ctx-edit">
                    <thead>
                      <tr>
                        <th>model selector</th>
                        <th>seat</th>
                      </tr>
                    </thead>
                    <tbody>
                      {Object.keys(context.models).map((selector) => (
                        <tr key={selector} data-selector={selector}>
                          <td class="code">{selector}</td>
                          <td>
                            <select
                              aria-label={`Seat of ${selector}`}
                              value={draft.rows[selector]}
                              disabled={saving}
                              onChange={(event) => pick(selector, event.currentTarget.value)}
                            >
                              {registry.seats.map((seat) => (
                                <option key={seat.id} value={seat.id}>
                                  {seat.id}
                                </option>
                              ))}
                            </select>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  <div class="ctx-edit-acts">
                    <button
                      type="button"
                      class="btn primary"
                      disabled={saving}
                      onClick={() => void save(context.id, draft)}
                    >
                      Save
                    </button>
                    <button type="button" class="btn quiet" disabled={saving} onClick={() => setEditing(null)}>
                      Cancel
                    </button>
                  </div>
                </div>
              ) : null}
              {note !== undefined ? (
                <p class="acc-note" data-tone={note.tone}>
                  {note.text}
                </p>
              ) : null}
            </article>
          );
        })}
      </div>
    </section>
  );
}
