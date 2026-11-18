// The stories of Composer: the send box under a reader, with its queue above it. Each state draws the queue or the state
// its props name. Its waiting messages are the fixture pane's, and its session messages are built as a stream holds them.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import * as composer from "./composer";
import { applyDelta } from "./conversation";
import { paneStreams } from "./fixtures/panes";
import { QueuedMessageSchema } from "./gen/orrery/v1/orrery_pb";

// must returns value, or throws naming what the fixtures do not hold.
function must<T>(value: T | undefined, what: string): T {
  if (value === undefined) {
    throw new Error(`fixtures: no ${what}`);
  }
  return value;
}

// The pane's waiting messages, as its conversation holds them, and the same messages with the second canceled.
const waiting = applyDelta(null, must(paneStreams.get("%11"), "lamp stream")).state?.waiting ?? [];
const canceledWaiting = waiting.map((message, index) => (index === 1 ? { ...message, canceled: true } : message));

// queuedMessage is a session's message as its stream holds it, in the state the session gives it.
const queuedMessage = (promptId: string, text: string, state: string) =>
  create(QueuedMessageSchema, { promptId, text, state });

const base = {
  label: "lighthouse",
  send: () => Promise.resolve(),
  pull: null,
  newPromptId: () => "prompt-story",
};

export const Composer: Stories = {
  idle: () => <composer.Composer {...base} state="done" />,
  working: () => <composer.Composer {...base} state="working" />,
  "working-queued": () => <composer.Composer {...base} state="working" queued={waiting} />,
  "canceled-queued": () => <composer.Composer {...base} state="working" queued={canceledWaiting} />,
  waiting: () => <composer.Composer {...base} state="waiting" />,
  "session-queued": () => (
    <composer.Composer
      {...base}
      state="working"
      sessionQueue={[queuedMessage("prompt-queued", "Also check the spool directory.", "queued")]}
      withdraw={() => Promise.resolve([])}
    />
  ),
  "session-undelivered": () => (
    <composer.Composer
      {...base}
      state="done"
      sessionQueue={[queuedMessage("prompt-discarded", "Retry the upload after the spool drains.", "discarded")]}
    />
  ),
};
