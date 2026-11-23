// RouteContexts draws the Contexts section of round 8's page D: a card for each context of the registry the page holds, in
// registry order. Each card shows the context's id, a default tag when it is the registry's default, an account select, its
// default seat, its count of model bindings, and its subagents' seats when set. The select sends Routes.SetContextAccount for
// that context alone. An answer's registry is drawn in place of the one held; a refusal leaves the select on the account it
// stood on and says why; a write with no answer re-reads the accounts and routes and says it could not tell.

import { Fragment, type VNode } from "preact";
import { useState } from "preact/hooks";

import { couldNotTell, nothingChanged, runWrite } from "./accountswrite";
import type { createClients } from "./client";
import type { Account, RouteContext, RouteRegistry, RouteSubagents } from "./gen/orrery/v1/accounts_pb";

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

// Note is what a card says after a write: its text, and a tone of bad for a refusal or quiet for a write that could not be
// told.
interface Note {
  text: string;
  tone: "bad" | "quiet";
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

export function RouteContexts({ accounts, registry, clients, changeRegistry, reread }: RouteContextsProps): VNode {
  const [notes, setNotes] = useState<Readonly<Record<string, Note | undefined>>>({});

  // say sets the note of the card with id, or clears it when note is undefined.
  const say = (id: string, note: Note | undefined) => {
    setNotes((current) => ({ ...current, [id]: note }));
  };

  // choose sends Routes.SetContextAccount for context, naming account. An answer holds the registry it returns, and clears
  // the card's note. A refusal says why nothing changed, and the select goes back to the account it stood on, which the
  // card draws again from the registry held. A write with no answer, or an answer with no registry, re-reads the accounts
  // and routes and says it could not tell.
  const choose = async (context: RouteContext, account: string) => {
    const outcome = await runWrite(() => clients.routes.setContextAccount({ context: context.id, account }));
    if (outcome.kind === "done" && outcome.answer.registry !== undefined) {
      changeRegistry(outcome.answer.registry);
      say(context.id, undefined);
      return;
    }
    if (outcome.kind === "refused") {
      say(context.id, { text: `Not set: ${outcome.reason} ${nothingChanged}`, tone: "bad" });
      return;
    }
    await reread();
    say(context.id, { text: couldNotTell, tone: "quiet" });
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
