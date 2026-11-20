// The stories of the reader: Reader draws one conversation, and ReaderHead draws the head over one.
// Each state draws the fixture conversation it names, folded from its stream by applyDelta as the app folds it.

import { create } from "@bufbuild/protobuf";
import type { ComponentChildren } from "preact";
import { useEffect, useRef } from "preact/hooks";

import type { Stories } from "./catalog/story";
import { applyDelta, type Conversation } from "./conversation";
import { at } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import { paneStreams, panes } from "./fixtures/panes";
import { headless, sessions, sessionStreams } from "./fixtures/sessions";
import { Ask } from "./ask";
import { conversationAsk } from "./askmodel";
import { findMatches } from "./find";
import { type EpicStepCheck, EpicStepCheckSchema } from "./gen/orrery/v1/epics_pb";
import * as reader from "./reader";
import { type Row, rowsFrom } from "./rows";
import { sendingEntry } from "./sending";
import { TitleParts } from "./titleparts";

const noop = () => {};

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
const beaconAsk = must(conversationAsk(beacon, beaconConversation) ?? undefined, "beacon's question");
const beaconNow = Number(at("@14:41"));
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
      asking
      ask={
        <Ask
          model={beaconAsk}
          now={beaconNow}
          phase="open"
          picked={null}
          note=""
          keyCaps
          narrow={false}
          numberKeys={false}
          onAnswer={noop}
        />
      }
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

// NoteOpen opens the fold of the note its child block draws, once it mounts, so the story draws the fold open.
function NoteOpen({ children }: { children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector<HTMLButtonElement>("button.note")?.click();
  }, []);
  return <div ref={ref}>{children}</div>;
}

const failedChecks: EpicStepCheck[] = [
  create(EpicStepCheckSchema, { round: 1, name: "schema", ok: true }),
  create(EpicStepCheckSchema, { round: 1, name: "gate", ok: false }),
];
const passedChecks: EpicStepCheck[] = [
  create(EpicStepCheckSchema, { round: 1, name: "schema", ok: true }),
  create(EpicStepCheckSchema, { round: 1, name: "gate", ok: true }),
];
const gateOutput = "gate failed: exit 1\ncli/export_test.go:88: want ErrExists, got nil\nthe gate ran just check";

export const CheckBlock: Stories = {
  bad: () => <reader.CheckBlock text="gate failed: exit 1" round={1} checks={failedChecks} />,
  ok: () => <reader.CheckBlock text="all checks passed" round={1} checks={passedChecks} />,
  "note-open": () => (
    <NoteOpen>
      <reader.CheckBlock text={gateOutput} round={1} checks={failedChecks} />
    </NoteOpen>
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
