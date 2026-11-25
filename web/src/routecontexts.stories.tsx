// The stories of RouteContexts: the section over the fixture's invented accounts and registry, over a registry whose default
// is work, which has no subagents and no model bindings, and over the fixture's registry with personal's Edit open. The
// section's writes go to the fixture's clients, and its re-read answers the registry the story is given, so a story draws
// what it was given and changes nothing.

import { create } from "@bufbuild/protobuf";
import type { VNode } from "preact";
import { useEffect, useRef } from "preact/hooks";

import type { Stories } from "./catalog/story";
import { accounts, registry } from "./fixtures/accounts";
import { createFixtureTransport } from "./fixtures/transport";
import { type RouteRegistry, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { RouteContexts as RouteContextsView } from "./routecontexts";

const clients = createFixtureTransport().clients;

// workDefault is the fixture's registry with work as the default, and a second context, tiltyard, that has one model binding
// and no subagents, so its card names no subagents and work's card names no models.
const workDefault = create(RouteRegistrySchema, {
  defaultContext: "work",
  contexts: [
    { id: "personal", defaultSeat: "anthropic", account: "max-1" },
    { id: "work", defaultSeat: "work-bedrock", account: "" },
    { id: "tiltyard", defaultSeat: "anthropic", models: { "example-haiku": "anthropic-haiku" }, account: "max-2" },
  ],
});

// holdsRegistry is the change a story makes to the registry the page holds: none, since a story draws what it is given.
const holdsRegistry = () => undefined;

// rereadsTo is the re-read a story's section is given: it answers the registry the story draws.
const rereadsTo = (draws: RouteRegistry) => () => Promise.resolve(draws);

// Editing draws the section over the fixture's registry with personal's Edit open. The story presses Edit once the section has
// drawn, so the story draws the table of personal's model selectors and their seats, as the page draws it after Edit.
function Editing(): VNode {
  const root = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const buttons = root.current?.querySelectorAll<HTMLButtonElement>('article[data-context="personal"] button');
    [...(buttons ?? [])].find((button) => button.textContent === "Edit")?.click();
  }, []);
  return (
    <div ref={root}>
      <RouteContextsView
        accounts={accounts}
        registry={registry}
        clients={clients}
        changeRegistry={holdsRegistry}
        reread={rereadsTo(registry)}
      />
    </div>
  );
}

export const RouteContexts: Stories = {
  editing: () => <Editing />,
  default: () => (
    <RouteContextsView
      accounts={accounts}
      registry={registry}
      clients={clients}
      changeRegistry={holdsRegistry}
      reread={rereadsTo(registry)}
    />
  ),
  "work-default": () => (
    <RouteContextsView
      accounts={accounts}
      registry={workDefault}
      clients={clients}
      changeRegistry={holdsRegistry}
      reread={rereadsTo(workDefault)}
    />
  ),
};
