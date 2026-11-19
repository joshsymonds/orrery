// The stories of Talk: the epic's Talk command, shut and open. Talk opens its list on a press, so the open states press
// its button once it is drawn, as a reader of the page would, and the list they show is the one Talk opens.

import type { ComponentProps, VNode } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import type { Stories } from "./catalog/story";
import { gets } from "./fixtures/epics";
import { panes } from "./fixtures/panes";
import { headless, sessions } from "./fixtures/sessions";
import { createFixtureTransport } from "./fixtures/transport";
import { rowsFrom } from "./rows";
import * as talk from "./talk";

type TalkProps = ComponentProps<typeof talk.Talk>;

const noop = () => {};

// must returns value, or throws naming what the fixtures do not hold.
function must<T>(value: T | undefined, what: string): T {
  if (value === undefined) {
    throw new Error(`fixtures: no ${what}`);
  }
  return value;
}

const { clients } = createFixtureTransport();
const rota = must(gets.get("lighthouse/rota-export"), "rota epic");

// shown is the Talk of the rota epic: its two sessions, the repository they start in, and the rows that name them.
const shown: TalkProps = {
  epic: "lighthouse/rota-export",
  sessions: rota.sessions,
  repository: rota.repository,
  rows: rowsFrom(panes.panes, sessions.sessions, headless.sessions),
  start: (dir, prompt) => clients.control.start({ dir, prompt }).then((reply) => reply.id),
  onOpen: noop,
};

// Opened draws Talk and presses its button once it is drawn, so the list the state shows is the list Talk opens.
function Opened(props: TalkProps): VNode {
  const root = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    root.current?.querySelector<HTMLButtonElement>("button.act")?.click();
  }, []);
  return (
    <div ref={root}>
      <talk.Talk {...props} />
    </div>
  );
}

export const Talk: Stories = {
  closed: () => <talk.Talk {...shown} />,
  open: () => <Opened {...shown} />,
  "open-no-sessions": () => <Opened {...shown} sessions={[]} />,
  "open-without-repository": () => <Opened {...shown} repository="" />,
};
