// AccountAdd draws the Add account form under the Accounts table, as round 8's page D draws it: a name, a provider, a plan and
// a token, with the hint that says where the token comes from, and Add and Cancel. Add sends Accounts.Add through runWrite,
// and what the write says decides the form. An answer closes the form and holds the account the page adds to its table. A
// refusal keeps the form as it was and says why nothing changed, in the hint's place. No answer closes the form, empties the
// token, and hands the page the line that says it could not tell, after the page re-reads what stands. Cancel closes the
// form, and so does Escape from anywhere on the page. The token is held only by this form, so it goes when the form closes.

import type { VNode } from "preact";
import { useEffect, useState } from "preact/hooks";

import { couldNotTell, nothingChanged, runWrite } from "./accountswrite";
import type { createClients } from "./client";
import type { Account, RouteRegistry } from "./gen/orrery/v1/accounts_pb";
import { Icon } from "./glyphs";

export interface AccountAddProps {
  // clients are the Accounts client the form writes through, as createClients makes it.
  clients: Pick<ReturnType<typeof createClients>, "accounts">;
  // added takes the account an answered Add returns, so the page holds it in its table.
  added: (account: Account) => void;
  // close closes the form. note is the line the page draws under its table in the form's place, or null for none.
  close: (note: string | null) => void;
  // reread re-reads Accounts.List and Routes.Get after an Add with no answer, so the page draws what stands.
  reread: () => Promise<RouteRegistry | null>;
}

// plans are the plans the Plan select offers, first the default.
const plans = ["Claude Max", "Claude Pro"];

// unspellchecked sets the token's spellcheck attribute to false, so the token is never checked as text. Preact sets spellcheck
// as a property where the DOM has one, and drops a false it cannot set, so the attribute is set here, the same on every DOM.
function unspellchecked(field: HTMLInputElement | null): void {
  field?.setAttribute("spellcheck", "false");
}

export function AccountAdd({ clients, added, close, reread }: AccountAddProps): VNode {
  const [name, setName] = useState("");
  const [plan, setPlan] = useState(plans[0]);
  const [token, setToken] = useState("");
  // refusal is the line a refused Add drew, or null while no Add has been refused.
  const [refusal, setRefusal] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // add sends Accounts.Add with the fields as they stand. An answer holds its account and closes the form. A refusal keeps
  // the fields and draws its line. A write with no answer closes the form, empty of its token, and has the page re-read.
  const add = async (): Promise<void> => {
    setBusy(true);
    setRefusal(null);
    const outcome = await runWrite(() => clients.accounts.add({ name, provider: "anthropic", plan, token }));
    if (outcome.kind === "done" && outcome.answer.account !== undefined) {
      added(outcome.answer.account);
      close(null);
      return;
    }
    if (outcome.kind === "refused") {
      setBusy(false);
      setRefusal(`Not added: ${outcome.reason} ${nothingChanged}`);
      return;
    }
    close(couldNotTell);
    await reread();
  };

  // Escape closes the form from anywhere on the page. The window's capture listener runs before the page's own key handler,
  // and it stops the key there, so the page never acts on it, as the rail's picker's Escape does. A narrow form's sheet
  // handles Escape too, but this listener runs first and stops the key, so the sheet sees it only once.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        close(null);
      }
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [close]);

  return (
    <div class="acc-add" role="group" aria-label="Add account">
      <label class="acc-add-field">
        <span class="lbl">Name</span>
        <span class="field">
          <input type="text" value={name} onInput={(event) => setName(event.currentTarget.value)} />
        </span>
      </label>
      <label class="acc-add-field">
        <span class="lbl">Provider</span>
        <span class="sel">
          <select value="anthropic">
            <option value="anthropic">Claude subscription</option>
          </select>
          <Icon name="chevronDown" />
        </span>
      </label>
      <label class="acc-add-field">
        <span class="lbl">Plan</span>
        <span class="sel">
          <select value={plan} onChange={(event) => setPlan(event.currentTarget.value)}>
            {plans.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </select>
          <Icon name="chevronDown" />
        </span>
      </label>
      <label class="acc-add-field">
        <span class="lbl">Token</span>
        <span class={refusal === null ? "field" : "field error"}>
          <input
            type="password"
            autocomplete="off"
            ref={unspellchecked}
            aria-invalid={refusal === null ? undefined : "true"}
            value={token}
            onInput={(event) => setToken(event.currentTarget.value)}
          />
        </span>
      </label>
      {refusal === null ? (
        <p class="help">
          Run <span class="code">claude setup-token</span> on any machine with a browser, sign in to the account, and paste what
          it prints. It lasts a year; orrery shows only its hash.
        </p>
      ) : (
        <p class="help" data-tone="bad">
          {refusal}
        </p>
      )}
      <div class="acts">
        <button type="button" class="btn primary" disabled={busy} onClick={() => void add()}>
          Add account
        </button>
        <button type="button" class="btn quiet" onClick={() => close(null)}>
          Cancel
        </button>
      </div>
    </div>
  );
}
