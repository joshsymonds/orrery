// The stories of the reader: Reader draws one conversation, and ReaderHead and ConversationHead draw the heads over one.
// Each state draws the fixture conversation it names, folded from its stream by applyDelta as the app folds it.

import type { Stories } from "./catalog/story";
import { applyDelta, type Conversation } from "./conversation";
import { at } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import { host } from "./fixtures/host";
import { paneStreams, panes } from "./fixtures/panes";
import { headless, sessions, sessionStreams } from "./fixtures/sessions";
import { findMatches } from "./find";
import { readerQuestion } from "./inboxrow";
import * as reader from "./reader";
import { type Row, rowsFrom } from "./rows";
import { sendingEntry } from "./sending";
import { PaneStrip } from "./strip";
import { TitleParts } from "./titleparts";
import type { InboxMark } from "./titlerow";

const noop = () => {};
const quiet: InboxMark = { needs: 0, enter: noop, leave: noop, pin: noop };

// must returns value, or throws naming what the fixtures do not hold.
function must<T>(value: T | undefined, what: string): T {
  if (value === undefined) {
    throw new Error(`fixtures: no ${what}`);
  }
  return value;
}

const rows = rowsFrom(panes.panes, sessions.sessions, headless.sessions);
const rowOf = (key: string): Row => must(rows.find((row) => row.key === key), `row ${key}`);
const paneConversation = (pane: string): Conversation =>
  applyDelta(null, must(paneStreams.get(pane), `pane stream ${pane}`));
const sessionConversation = (id: string): Conversation =>
  applyDelta(null, must(sessionStreams.get(id), `session stream ${id}`));

const lamp = rowOf("pane:halyard:%11");
const lampConversation = paneConversation("%11");
const beacon = rowOf("pane:halyard:%12");
const beaconConversation = paneConversation("%12");
const workerConversation = sessionConversation("st-rota-t2-impl");
const workerStep = must(
  must(gets.get("lighthouse/rota-export"), "rota epic").steps.find((step) => step.id === "st-rota-t2-impl"),
  "worker step",
);

export const Reader: Stories = {
  loading: () => <reader.Reader conversation={null} seenAt={0} open={false} />,
  // A pane's conversation, with the "since you looked" rule drawn before the items that came after the pane was seen.
  "pane-conversation": () => <reader.Reader conversation={lampConversation} seenAt={lamp.seen} open={false} />,
  "compaction-open": () => (
    <reader.Reader conversation={lampConversation} seenAt={lamp.seen} open onToggleOpen={noop} />
  ),
  question: () => (
    <reader.Reader
      conversation={beaconConversation}
      seenAt={beacon.seen}
      open={false}
      question={readerQuestion(beacon, beaconConversation)}
      onAnswer={noop}
    />
  ),
  find: () => (
    <reader.Reader
      conversation={lampConversation}
      seenAt={lamp.seen}
      open={false}
      find={{ query: "Backoff", matches: findMatches(lampConversation.items, "Backoff"), current: 0 }}
    />
  ),
  sending: () => (
    <reader.Reader
      conversation={lampConversation}
      seenAt={lamp.seen}
      open={false}
      sending={[sendingEntry({ promptId: "prompt-sending", text: "Also log the batch id." }, lampConversation)]}
    />
  ),
  "session-live-text": () => (
    <reader.Reader conversation={workerConversation} seenAt={Number(at("@14:30"))} open={false} />
  ),
  "session-step": () => (
    <reader.Reader
      conversation={workerConversation}
      seenAt={Number.POSITIVE_INFINITY}
      open={false}
      step={workerStep}
    />
  ),
};

export const ReaderHead: Stories = {
  titled: () => (
    <reader.ReaderHead
      label={<TitleParts dir="/home/owner/code/lighthouse" line="Retry backoff and jitter for the telemetry uploader" />}
      where=""
    />
  ),
  where: () => <reader.ReaderHead label="rota-export" where="lighthouse › T2 · implementing" />,
};

export const ConversationHead: Stories = {
  "with-strip": () => (
    <reader.ConversationHead
      dir={lamp.project}
      line={lamp.label}
      onBack={noop}
      strip={<PaneStrip host={host} context={lampConversation.state?.context} />}
    />
  ),
  "needs-count": () => (
    <reader.ConversationHead
      dir={lamp.project}
      line={lamp.label}
      onBack={noop}
      mark={{ ...quiet, needs: 3 }}
    />
  ),
};
