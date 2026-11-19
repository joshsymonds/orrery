// The stories of ReaderColumn: a column draws a row's head, reader and send box, or the cursor's step. Each state draws the
// fixture row and conversation it names. The column takes the fixture transport's clients, and no state calls them.

import type { Stories } from "./catalog/story";
import { epicCallsOf } from "./card";
import { applyDelta, type Conversation } from "./conversation";
import { epicsView, noEpics, type StepItem } from "./epics";
import { liveEpoch } from "./fixtures/clock";
import { epics, gets } from "./fixtures/epics";
import { paneStreams, panes } from "./fixtures/panes";
import { headless, sessions, sessionStreams } from "./fixtures/sessions";
import { createFixtureTransport } from "./fixtures/transport";
import { findMatches } from "./find";
import * as readercolumn from "./readercolumn";
import { type Row, rowsFrom } from "./rows";
import { sendingEntry } from "./sending";
import type { InboxMark } from "./titlerow";

const now = Date.parse(liveEpoch) / 1000;
const noop = () => {};
const quiet: InboxMark = { needs: 0, enter: noop, leave: noop, pin: noop };
const { clients } = createFixtureTransport();
const epicCalls = epicCallsOf(clients, noop, noop, noEpics);

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

const lamp = rowOf("pane:halyard:%11");
const lampConversation = paneConversation("%11");
const beacon = rowOf("pane:halyard:%12");
const beaconConversation = paneConversation("%12");
const planning = rowOf("pane:halyard:%14");
const planningConversation = paneConversation("%14");
const headlessRow = rowOf("session:4be0c6d2-8f3a-4c71-9e25-ephemeris001");

// The rota epic's running steps, as the list's epic block holds them: the worker's, and the one held for Q1.
const rotaBlock = must(
  epicsView(epics.epics, gets).blocks.find((block) => block.epic.id === "lighthouse/rota-export"),
  "rota block",
);
const workerStep: StepItem = must(
  rotaBlock.steps.find((step) => step.id === "st-rota-t2-impl"),
  "worker step",
);
const heldStep: StepItem = must(rotaBlock.steps.find((step) => step.id === "st-rota-t1-impl"), "held step");
const workerConversation = applyDelta(null, must(sessionStreams.get("st-rota-t2-impl"), "worker stream"));

// columnOf is the props of a column that shows row's conversation: the cursor's or the pinned, with the one composer of
// its row, and no find, sending or step.
function columnOf(
  column: "cursor" | "pinned",
  row: Row | null,
  conversation: Conversation | null,
): readercolumn.ReaderColumnProps {
  return {
    column,
    clients,
    now,
    row,
    conversation,
    rows: row === null ? [] : [row],
    interrupts: new Set<string>(),
    mark: quiet,
    pageStrip: () => undefined,
    onBack: noop,
    finding: null,
    matches: [],
    current: -1,
    onFind: noop,
    inputs: { current: new Map() },
    handles: { current: new Map() },
    reader: { current: null },
    older: { current: noop },
    sending: [],
    epicCalls,
    addSending: noop,
    retractSending: noop,
    open: false,
  };
}

export const ReaderColumn: Stories = {
  "cursor-pane": () => <readercolumn.ReaderColumn {...columnOf("cursor", lamp, lampConversation)} />,
  "cursor-question": () => (
    <readercolumn.ReaderColumn {...columnOf("cursor", beacon, beaconConversation)} onAnswer={noop} />
  ),
  "pinned-pane": () => <readercolumn.ReaderColumn {...columnOf("pinned", planning, planningConversation)} />,
  find: () => (
    <readercolumn.ReaderColumn
      {...columnOf("cursor", lamp, lampConversation)}
      finding={{ key: lamp.key, query: "Backoff", current: 0 }}
      matches={findMatches(lampConversation.items, "Backoff")}
      current={0}
    />
  ),
  sending: () => (
    <readercolumn.ReaderColumn
      {...columnOf("cursor", lamp, lampConversation)}
      sending={[sendingEntry({ promptId: "prompt-sending", text: "Also log the batch id." }, lampConversation)]}
    />
  ),
  step: () => (
    <readercolumn.ReaderColumn
      {...columnOf("cursor", null, workerConversation)}
      step={workerStep}
      steps={[workerStep, heldStep]}
      stepInputs={{ current: new Map() }}
    />
  ),
  "held-step": () => (
    <readercolumn.ReaderColumn
      {...columnOf("cursor", null, null)}
      step={heldStep}
      steps={[workerStep, heldStep]}
      stepInputs={{ current: new Map() }}
    />
  ),
  "session-loading": () => <readercolumn.ReaderColumn {...columnOf("cursor", headlessRow, null)} />,
};
