import type { ComponentChildren, JSX } from "preact";
import { useMemo } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import type { EpicCalls } from "./card";
import { type Conversation, shownConversation } from "./conversation";
import { Dock, type DockHandle } from "./dock";
import { rowFacts, stepFacts } from "./dockfacts";
import type { StepItem } from "./epics";
import { FindBar, type Match } from "./find";
import { readerQuestion } from "./inboxrow";
import { ConversationHead, Reader, type ReaderHandle } from "./reader";
import type { Row } from "./rows";
import type { Sending, SentText } from "./sending";
import { type InboxMark, TitleRow } from "./titlerow";

// FindState is a column's find bar, open on the row it was opened on: the query typed in it, and the match it shows, an
// index into the matches the query finds in that row's conversation. A bar whose row leaves its column is closed.
export interface FindState {
  key: string;
  query: string;
  current: number;
}

// The section markup each column draws: the cursor's reader, and the pinned reader beside the list.
const columnNames = {
  cursor: { section: "read", label: "reader", reply: "reply" },
  pinned: { section: "read pinned", label: "pinned reader", reply: "pin-reply" },
} as const;

// A step keeps no seen mark. Infinity makes no item newer than it, so the step's reader draws no since-you-looked rule.
const stepSeenAt = Number.POSITIVE_INFINITY;

// ReaderColumnProps are the state and callbacks a reader column draws from. App keeps all of it; the column only draws.
export interface ReaderColumnProps {
  column: "cursor" | "pinned";
  clients: Clients;
  now: number;
  // The row the column shows: the cursor's row, or the pinned row. Null hides the column when no step is given either.
  row: Row | null;
  // The conversation the column's reader holds, when its stream belongs to row.
  conversation: Conversation | null;
  // The rows that keep a dock in this column. The shown row gets one before the effect that keeps it has run.
  rows: Row[];
  // The rows whose interrupt mark holds, by row key: the column reads only whether a row is marked.
  interrupts: Pick<ReadonlyMap<string, number>, "has">;
  mark: InboxMark;
  // pageStrip draws the strip under a title row for a row and its conversation, or for no row.
  pageStrip: (row: Row | null, conversation: Conversation | null) => ComponentChildren;
  // onBack is the back button of the column's head: the cursor's focus, or the pin's close.
  onBack: () => void;
  // The column's find bar, when it is open on row, with its matches and the index of the current one.
  finding: FindState | null;
  matches: Match[];
  current: number;
  onFind: (finding: FindState | null) => void;
  // The column's dock inputs by row key, and its dock handles, which an Escape calls.
  inputs: { current: Map<string, HTMLTextAreaElement> };
  handles: { current: Map<string, DockHandle> };
  // The column's reader handle, which an empty dock's Page Up and Page Down page, and the older-page callback the
  // reader calls at its top.
  reader: { current: ReaderHandle | null };
  older: { current: () => void };
  sending: Sending[];
  epicCalls: EpicCalls;
  addSending: (rowKey: string, sent: SentText, held: Conversation | null) => void;
  retractSending: (rowKey: string, promptId: string) => void;
  // open is the cursor's open toggle; the pinned column passes false and no toggle or answer.
  open: boolean;
  onToggleOpen?: () => void;
  onAnswer?: (choice: 1 | 2 | 3) => void;
  // onInterrupt is a dock's state line Interrupt on a row: the row's Escape path. No Interrupt control is drawn without it.
  onInterrupt?: (row: Row) => void;
  // interrupt is a session row's Send now interrupt, which resolves the texts the interrupt cancelled. No Send now is
  // drawn without it.
  interrupt?: (row: Row) => Promise<string[]>;
  // The cursor column's step, when the cursor rests on one: the step it draws, the step boxes it keeps, and their inputs.
  step?: StepItem | null;
  steps?: StepItem[];
  stepInputs?: { current: Map<string, HTMLTextAreaElement> };
}

// ReaderColumn draws one reader column: the cursor's, or the pinned row's beside the list. It draws the row's head, the
// find bar while one is open, the reader of the row, and the send box, with a dock for each kept row
// and only the shown row's dock visible. The cursor column also draws its step, when one is given: the step's title
// row, its reader, and a step box for each kept step. The column keeps no state; App passes it all in.
export function ReaderColumn({
  column,
  clients,
  now,
  row,
  conversation,
  rows,
  interrupts,
  mark,
  pageStrip,
  onBack,
  finding,
  matches,
  current,
  onFind,
  inputs,
  handles,
  reader,
  older,
  sending,
  epicCalls,
  addSending,
  retractSending,
  open,
  onToggleOpen,
  onAnswer,
  onInterrupt,
  interrupt,
  step = null,
  steps = [],
  stepInputs,
}: ReaderColumnProps): JSX.Element {
  const names = columnNames[column];
  const rowKey = row?.key ?? "";
  const stepKey = step?.key ?? "";
  // The conversation the readers and the shown dock draw, without the prompts its queue still holds or has discarded.
  const shown = useMemo(() => shownConversation(conversation), [conversation]);
  return (
    <section class={names.section} aria-label={names.label} hidden={row === null && step === null}>
      {row !== null && (
        <ConversationHead
          dir={row.project}
          line={row.label}
          onBack={onBack}
          mark={mark}
          strip={pageStrip(row, conversation)}
        />
      )}
      {finding !== null && (
        <FindBar
          query={finding.query}
          matches={matches.length}
          current={current}
          onQuery={(query) => onFind({ key: finding.key, query, current: 0 })}
          onNext={() => onFind({ ...finding, current: stepCurrent(current, matches.length, 1) })}
          onPrevious={() => onFind({ ...finding, current: stepCurrent(current, matches.length, -1) })}
          onClose={() => {
            onFind(null);
            inputs.current.get(finding.key)?.focus();
          }}
        />
      )}
      {step !== null && (
        <TitleRow
          crumbs={[{ label: stepWhere(step) }, { label: step.label }]}
          onBack={onBack}
          mark={mark}
          strip={pageStrip(null, null)}
        />
      )}
      {row !== null && (
        <Reader
          conversation={shown}
          seenAt={row.seen}
          open={open}
          question={column === "cursor" ? readerQuestion(row, conversation) : null}
          onAnswer={onAnswer}
          onToggleOpen={onToggleOpen}
          onOlder={() => older.current()}
          register={(handle) => {
            reader.current = handle;
          }}
          sending={sending}
          find={finding === null ? undefined : { query: finding.query, matches, current }}
          epicCalls={epicCalls}
        />
      )}
      {step !== null && (
        <Reader
          conversation={shown}
          seenAt={stepSeenAt}
          open={open}
          step={step.step}
          onToggleOpen={onToggleOpen}
          onOlder={() => older.current()}
          register={(handle) => {
            reader.current = handle;
          }}
        />
      )}
      <div class="send">
        {dockRows(rows, row).map((entry) => (
          <div key={entry.key} class={names.reply} hidden={entry.key !== rowKey}>
            <Dock
              facts={rowFacts(entry, entry.key === rowKey ? conversation : null, interrupts.has(entry.key))}
              now={now}
              send={(text, promptId) => actions.send(clients, entry, text, promptId)}
              pull={entry.kind === "pane" ? () => actions.pullQueued(clients, entry) : null}
              newPromptId={actions.newPromptId}
              register={(handle) => registerHandle(handles, entry.key, handle)}
              onPage={(direction) => reader.current?.page(direction) ?? false}
              onFind={() => onFind({ key: entry.key, query: "", current: 0 })}
              onInterrupt={onInterrupt === undefined ? undefined : () => onInterrupt(entry)}
              interrupt={interrupt === undefined || entry.kind !== "session" ? undefined : () => interrupt(entry)}
              resume={
                entry.kind === "session"
                  ? async () => {
                      await clients.control.resume({ id: entry.id });
                    }
                  : undefined
              }
              queued={entry.key === rowKey ? conversation?.state?.waiting : undefined}
              sessionQueue={entry.key === rowKey ? conversation?.queued : undefined}
              withdraw={
                entry.kind === "session" ? (promptId) => actions.withdrawQueued(clients, entry, promptId) : undefined
              }
              items={entry.key === rowKey ? shown?.items : undefined}
              pending={entry.key === rowKey ? conversation?.state?.pending : undefined}
              onSending={(sent) => addSending(entry.key, sent, entry.key === rowKey ? conversation : null)}
              onUnsent={(promptId) => retractSending(entry.key, promptId)}
              inputRef={(element) => {
                if (element === null) {
                  inputs.current.delete(entry.key);
                } else {
                  inputs.current.set(entry.key, element);
                }
              }}
            />
          </div>
        ))}
        {dockRows(steps, step).map((entry) => {
          const calls = stepCalls(clients, entry.id);
          return (
            <div key={entry.id} class={names.reply} hidden={entry.key !== stepKey}>
              <Dock
                facts={stepFacts(entry, entry.key === stepKey ? shown : null)}
                now={now}
                send={calls.nudge}
                pull={null}
                newPromptId={actions.newPromptId}
                stepCalls={{ hold: calls.hold, stop: calls.stop }}
                inputRef={(element) => {
                  if (element === null) {
                    stepInputs?.current.delete(entry.key);
                  } else {
                    stepInputs?.current.set(entry.key, element);
                  }
                }}
              />
            </div>
          );
        })}
      </div>
    </section>
  );
}

// headState is the state a row's keys read: the state its dock's line draws, so a stream that reads working counts as
// working where the list still says otherwise.
export function headState(row: Row, conversation: Conversation | null): string {
  return rowFacts(row, conversation, false).state;
}

// stepCalls are the owner's calls on one step, each naming the step by its id: Steps.Nudge with the text, Steps.Hold,
// and Steps.Stop. The page has no other call on a step.
export function stepCalls(clients: Clients, id: string) {
  return {
    nudge: async (text: string): Promise<void> => {
      await clients.steps.nudge({ id, text });
    },
    hold: async (): Promise<void> => {
      await clients.steps.hold({ id });
    },
    stop: async (): Promise<void> => {
      await clients.steps.stop({ id });
    },
  };
}

// registerHandle keeps a dock's handle under its row key while the dock is mounted, and drops it on null.
function registerHandle(handles: { current: Map<string, DockHandle> }, key: string, handle: DockHandle | null) {
  if (handle === null) {
    handles.current.delete(key);
  } else {
    handles.current.set(key, handle);
  }
}

// dockRows returns the entries that render a dock: the kept entries, and the shown entry before the effect has added
// it.
function dockRows<T extends { key: string }>(kept: T[], shown: T | null): T[] {
  if (shown === null || kept.some((entry) => entry.key === shown.key)) {
    return kept;
  }
  return [...kept, shown];
}

// stepCurrent moves the current match by step, wrapping past either end. With no matches there is no match to move to.
function stepCurrent(current: number, matches: number, step: number): number {
  return matches === 0 ? -1 : (((current + step) % matches) + matches) % matches;
}

// stepWhere is a step's where line: its epic, its agent, and its round.
function stepWhere(step: StepItem): string {
  return `${step.epicSlug} › ${step.agent} · step · round ${step.round}`;
}
