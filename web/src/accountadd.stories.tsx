// The stories of AccountAdd: the Add account form under the Accounts table, blank, with an invented name and token typed, and
// refused for an API key. The refused story types its fields and presses Add once they have drawn, so the story draws the
// refusal the way a refused Add does. Every name, token and host here is invented, and each refused story makes its own fake.

import { Code, createRouterTransport } from "@connectrpc/connect";
import type { VNode } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import { AccountAdd as AccountAddView } from "./accountadd";
import type { Stories } from "./catalog/story";
import { createClients } from "./client";
import { accounts, accountsServe, registry } from "./fixtures/accounts";
import { createFixtureTransport } from "./fixtures/transport";

const clients = createFixtureTransport().clients;

// oatToken is an invented setup-token, which the form takes.
const oatToken = "sk-ant-oat01-invented-story-token";

// refusedToken is an invented setup-token the refusing fake answers with orreryd's refusal for an API key. The fake refuses by
// its queued answer, not by the token's text, so the token starts sk-ant-oat01- as every story's token does.
const refusedToken = "sk-ant-oat01-invented-refused-story-token";

// apiKeyRefusal is what orreryd says when an API key is pasted for a setup-token.
const apiKeyRefusal = "That is an API key, not a setup-token. A setup-token starts sk-ant-oat.";

const noop = () => {};

// rereadsTo answers the registry the fixture holds, so a re-read draws what the fixture stands on.
const rereadsTo = () => Promise.resolve(registry);

// refusingClients are the clients of a fake whose next Accounts.Add is refused as orreryd refuses an API key. Each call makes
// its own fake, so a story mounted again is refused again.
function refusingClients(): ReturnType<typeof createClients> {
  const fake = accountsServe({ accounts, registry });
  fake.refuseNext("Accounts.Add", Code.InvalidArgument, apiKeyRefusal);
  return createClients(createRouterTransport((router) => fake.register(router)));
}

// keystroke sets the value of the field in form that selector matches, and dispatches the input event a keystroke does.
function keystroke(form: HTMLElement, selector: string, value: string): void {
  const field = form.querySelector<HTMLInputElement>(selector);
  if (field === null) {
    throw new Error(`the form draws no ${selector}`);
  }
  field.value = value;
  field.dispatchEvent(new Event("input", { bubbles: true }));
}

interface FilledProps {
  clients: ReturnType<typeof createClients>;
  name: string;
  token: string;
  // pressAdd is whether the story presses Add once the fields have drawn.
  pressAdd: boolean;
}

// Filled draws the form with name and token typed into it. Its typing is one render, and Add is pressed in the render after
// it, so the Add reads the fields the keystrokes set.
function Filled({ clients, name, token, pressAdd }: FilledProps): VNode {
  const root = useRef<HTMLDivElement | null>(null);
  const [typed, setTyped] = useState(false);
  useEffect(() => {
    if (root.current !== null) {
      keystroke(root.current, 'input[type="text"]', name);
      keystroke(root.current, 'input[type="password"]', token);
    }
    setTyped(true);
  }, []);
  useEffect(() => {
    if (typed && pressAdd) {
      root.current?.querySelector<HTMLButtonElement>("button.primary")?.click();
    }
  }, [typed]);
  return (
    <div ref={root}>
      <AccountAddView clients={clients} added={noop} close={noop} reread={rereadsTo} />
    </div>
  );
}

// Refused is the form whose Add the server refuses for an API key: it types a name and the key, and presses Add.
function Refused(): VNode {
  const [refusing] = useState(refusingClients);
  return <Filled clients={refusing} name="max-4" token={refusedToken} pressAdd />;
}

export const AccountAdd: Stories = {
  empty: () => <AccountAddView clients={clients} added={noop} close={noop} reread={rereadsTo} />,
  typed: () => <Filled clients={clients} name="max-4" token={oatToken} pressAdd={false} />,
  refused: () => <Refused />,
};
