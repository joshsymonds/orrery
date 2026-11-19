// The stories of TalkDock: the dock beside an epic page that follows the conversation Talk opened. Each state names the
// target it follows; the fixture stream answers that target, and a state whose stream is quiet draws its loading line.

import type { ComponentProps } from "preact";

import type { Stories } from "./catalog/story";
import { noEpics } from "./epics";
import { panes } from "./fixtures/panes";
import { headless, sessions, sessionStreams } from "./fixtures/sessions";
import { shippedSlices } from "./fixtures/slices";
import { createFixtureTransport } from "./fixtures/transport";
import type { SessionsWatchConversationResponse } from "./gen/orrery/v1/orrery_pb";
import { rowsFrom } from "./rows";
import * as talkdock from "./talkdock";

type DockProps = ComponentProps<typeof talkdock.TalkDock>;

const noop = () => {};
const workerId = "st-rota-t2-impl";
const headlessId = "4be0c6d2-8f3a-4c71-9e25-ephemeris001";

// must returns value, or throws naming what the fixtures do not hold.
function must<T>(value: T | undefined, what: string): T {
  if (value === undefined) {
    throw new Error(`fixtures: no ${what}`);
  }
  return value;
}

// interruptedWorker is the worker's stream with its session interrupted, as a session stopped in its turn reads.
function interruptedWorker(): SessionsWatchConversationResponse {
  const stream = must(sessionStreams.get(workerId), "worker stream");
  const state = must(stream.state, "worker state");
  const session = must(state.session, "worker session");
  return { ...stream, state: { ...state, session: { ...session, state: "interrupted" } } };
}

const rows = rowsFrom(panes.panes, sessions.sessions, headless.sessions);
const { clients } = createFixtureTransport();
const interrupted = createFixtureTransport({
  ...shippedSlices,
  sessionStreams: new Map([[workerId, interruptedWorker()]]),
}).clients;

// dock is the props every state shares: the rows, the clients it follows through, and the openers the reader's epic
// cards use, which no state presses.
const dock: Omit<DockProps, "target"> = {
  clients,
  rows,
  onClose: noop,
  openEpic: noop,
  openStep: noop,
  epics: noEpics,
};

export const TalkDock: Stories = {
  pane: () => <talkdock.TalkDock {...dock} target={{ kind: "pane", id: "%11", label: "lamp-telemetry" }} />,
  "session-worker": () => <talkdock.TalkDock {...dock} target={{ kind: "session", id: workerId, label: "T2" }} />,
  "session-interrupted": () => (
    <talkdock.TalkDock {...dock} clients={interrupted} target={{ kind: "session", id: workerId, label: "T2" }} />
  ),
  "session-loading": () => (
    <talkdock.TalkDock {...dock} target={{ kind: "session", id: headlessId, label: "ephemeris-import" }} />
  ),
};
