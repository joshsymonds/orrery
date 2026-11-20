import type { ComponentChildren, JSX } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import { Ask, AskLine } from "./ask";
import { type AskModel, conversationAsk } from "./askmodel";
import { type AskState, answeredOf, askKey, askView } from "./askstate";
import type { EpicCalls } from "./card";
import type { RowAbout } from "./contracts";
import { type Conversation, shownConversation } from "./conversation";
import { Dock, type DockHandle } from "./dock";
import { rowFacts, stepFacts } from "./dockfacts";
import type { StepItem } from "./epics";
import { FindBar, type Match } from "./find";
import { Head, type HeadProps } from "./head";
import { foldOf } from "./headfold";
import { type ContractCalls, Reader, type ReaderHandle } from "./reader";
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

// Reading is where the window and the reader's own scroll sat, so closing a find bar returns them there.
interface Reading {
  x: number;
  y: number;
  reader: number;
}

// ColumnHead is what App gives a column's head for its row and conversation: the strip, the asks, the note, the More
// menu, under 760px the middle column's tiles, and the epic open in the panel beside the middle column. The column adds the fold, which it reads from its own reader.
export type ColumnHead = Pick<
  HeadProps,
  | "host"
  | "showStrip"
  | "lines"
  | "askingWords"
  | "badges"
  | "note"
  | "menu"
  | "work"
  | "here"
  | "contracts"
  | "onOpenContract"
  | "hereContract"
>;

// The section markup each column draws: the cursor's reader, and the pinned reader beside the list.
const columnNames = {
  cursor: { section: "read", label: "reader", reply: "reply" },
  pinned: { section: "read pinned", label: "pinned reader", reply: "pin-reply" },
} as const;

// A step keeps no seen mark. Infinity makes no item newer than it, so the step's reader draws no since-you-looked rule.
const stepSeenAt = Number.POSITIVE_INFINITY;

// AskNote is the note an Ask's own call left: the question it names by its key, and the server's reason, "" for none.
// It shows only while the question it names is the one the column shows.
export interface AskNote {
  key: string;
  text: string;
}

// AskCalls are the cursor column's Ask: the answer's life and the note the App holds for its conversation's question,
// and the calls its controls make. A pinned column draws no Ask.
export interface AskCalls {
  state: AskState;
  note: AskNote;
  onAnswer: (n: number) => void;
  onNotNow: () => void;
  onBringBack: () => void;
  onDecline: () => void;
  onArchive: () => void;
}

// ReaderColumnProps are the state and callbacks a reader column draws from. App keeps all of it; the column only draws.
export interface ReaderColumnProps {
  column: "cursor" | "pinned";
  // bare is whether the column draws its reader and dock without its head, as the phone's conversation sheet does.
  bare?: boolean;
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
  // pageStrip draws the strip under a step's title row, or under no row.
  pageStrip: (row: Row | null, conversation: Conversation | null) => ComponentChildren;
  // head is what the column's head draws for its row and conversation.
  head: (row: Row, conversation: Conversation | null) => ColumnHead;
  // narrow is the phone layout, where the head folds. hidden is the owner's hide, which holds across conversations.
  narrow: boolean;
  hidden: boolean;
  // onHide hides the head, onShow opens it again, and onOpenEpic opens the epic an ask line names.
  onHide: () => void;
  onShow: () => void;
  onOpenEpic: (epic: string) => void;
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
  // contractCalls are the reader's contract calls, which its contract lines draw through.
  contractCalls?: ContractCalls;
  // about is the About the shown row's dock holds: its next message leads with it, and onClearAbout clears it, on Not
  // about this and once that message is sent.
  about?: RowAbout | null;
  onClearAbout?: () => void;
  addSending: (rowKey: string, sent: SentText, held: Conversation | null) => void;
  retractSending: (rowKey: string, promptId: string) => void;
  // open is the cursor's open toggle; the pinned column passes false and no toggle or answer.
  open: boolean;
  onToggleOpen?: () => void;
  // ask is the cursor column's Ask, drawn for the row's own question after the items. Once answered, the answered line
  // draws in the reader at the place the answer's life records, not here.
  ask?: AskCalls;
  // onInterrupt is a dock's state line Interrupt on a row: the row's Escape path. No Interrupt control is drawn without it.
  onInterrupt?: (row: Row) => void;
  // interrupt is a session row's Send now interrupt, which resolves the texts the interrupt cancelled. No Send now is
  // drawn without it.
  interrupt?: (row: Row) => Promise<string[]>;
  // The cursor column's step, when the cursor rests on one: the step it draws, the step boxes it keeps, and their inputs.
  step?: StepItem | null;
  steps?: StepItem[];
  stepInputs?: { current: Map<string, HTMLTextAreaElement> };
  // ended is what stands in place of a kept row's dock, when it gives a node for the row; null keeps the dock.
  ended?: (row: Row) => ComponentChildren | null;
}

// ReaderColumn draws one reader column: the cursor's, or the pinned row's beside the list. It draws the row's head, the
// find bar while one is open, the reader of the row, and the send box, with a dock for each kept row and only the
// shown row's dock visible. The cursor column also draws its step, when one is given: the step's title row, its reader,
// and a dock for each kept step. The column keeps only how its reader is scrolled: whether it is at the newest line, and
// whether the owner tapped the folded head open since.
export function ReaderColumn({
  column,
  bare = false,
  clients,
  now,
  row,
  conversation,
  rows,
  interrupts,
  mark,
  pageStrip,
  head,
  narrow,
  hidden,
  onHide,
  onShow,
  onOpenEpic,
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
  contractCalls,
  about = null,
  onClearAbout,
  addSending,
  retractSending,
  open,
  onToggleOpen,
  ask,
  onInterrupt,
  interrupt,
  step = null,
  steps = [],
  stepInputs,
  ended,
}: ReaderColumnProps): JSX.Element {
  const names = columnNames[column];
  const rowKey = row?.key ?? "";
  const stepKey = step?.key ?? "";
  // section is the column's root, and reading is where the page sat when the find bar it draws last opened.
  const section = useRef<HTMLElement>(null);
  const reading = useRef<Reading | null>(null);
  const openFind = (next: FindState) => {
    if (finding === null) {
      reading.current = readingNow(section.current);
    }
    onFind(next);
  };
  // The conversation the readers and the shown dock draw, without the prompts its queue still holds or has discarded.
  const shown = useMemo(() => shownConversation(conversation), [conversation]);
  const [atNewest, setAtNewest] = useState(true);
  const [tapped, setTapped] = useState(false);
  // A tap on the folded line holds for its row alone, so another row's head starts folded again.
  useEffect(() => {
    setTapped(false);
  }, [rowKey]);
  const fold = foldOf(narrow, hidden, atNewest, tapped);
  // The row's own question is drawn as its Ask, and once answered its answered line is handed to the reader. The Ask's key
  // names the question its answer and note belong to.
  const model: AskModel | null = ask === undefined || row === null ? null : conversationAsk(row, conversation);
  const key = model === null || row === null ? null : askKey(row, model);
  const answered = ask === undefined || model !== null ? null : answeredOf(ask.state);
  const askNode =
    ask === undefined || model === null || key === null ? null : askFor(ask, model, key, now, narrow);
  // sendRow sends a dock's message to its row; the Dock leads it with the shown row's About, which is cleared once the
  // message is sent.
  const sendRow = async (entry: Row, text: string, promptId: string): Promise<void> => {
    const aboutOf = entry.key === rowKey ? about : null;
    await actions.send(clients, entry, text, promptId);
    if (aboutOf !== null) {
      onClearAbout?.();
    }
  };
  return (
    <section ref={section} class={names.section} aria-label={names.label} hidden={row === null && step === null}>
      {row !== null && !bare && (
        <Head
          {...head(row, conversation)}
          row={row}
          conversation={conversation}
          title={row.label}
          fold={fold}
          narrow={narrow}
          mark={mark}
          onBack={onBack}
          onFind={() => openFind({ key: row.key, query: "", current: 0 })}
          onHide={onHide}
          onShow={() => {
            setTapped(true);
            onShow();
          }}
          onOpenEpic={onOpenEpic}
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
            returnTo(section.current, reading.current);
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
          now={now}
          open={open}
          asking={askNode !== null}
          ask={askNode}
          answered={
            answered === null
              ? undefined
              : {
                  after: answered.after,
                  line: <AskLine state="answered" n={answered.n} name={answered.name} question={answered.question} keyCaps />,
                }
          }
          onToggleOpen={onToggleOpen}
          onOlder={() => older.current()}
          onNewest={(value) => {
            setAtNewest(value);
            setTapped(false);
          }}
          register={(handle) => {
            reader.current = handle;
          }}
          sending={sending}
          find={finding === null ? undefined : { query: finding.query, matches, current }}
          epicCalls={epicCalls}
          contractCalls={contractCalls}
        />
      )}
      {step !== null && (
        <Reader
          conversation={shown}
          seenAt={stepSeenAt}
          now={now}
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
            {ended?.(entry) ?? (
              <Dock
                facts={rowFacts(entry, entry.key === rowKey ? conversation : null, interrupts.has(entry.key))}
                now={now}
                send={(text, promptId) => sendRow(entry, text, promptId)}
                pull={entry.kind === "pane" ? () => actions.pullQueued(clients, entry) : null}
                newPromptId={actions.newPromptId}
                register={(handle) => registerHandle(handles, entry.key, handle)}
                onPage={(direction) => reader.current?.page(direction) ?? false}
                onFind={() => openFind({ key: entry.key, query: "", current: 0 })}
                onInterrupt={onInterrupt === undefined ? undefined : () => onInterrupt(entry)}
                interrupt={interrupt === undefined || entry.kind !== "session" ? undefined : () => interrupt(entry)}
                resume={
                  entry.kind === "session"
                    ? async () => {
                        await clients.control.resume({ id: entry.id });
                      }
                    : undefined
                }
                about={entry.key === rowKey ? about?.text : undefined}
                aboutId={entry.key === rowKey ? about?.id : undefined}
                onClearAbout={onClearAbout}
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
            )}
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

// askFor is the Ask of a conversation's question the cursor column draws: the App's answer life and note for key, and
// its calls. The note the call left shows over the refused-answer words, and only for this question.
function askFor(ask: AskCalls, model: AskModel, key: string, now: number, narrow: boolean): JSX.Element {
  const view = askView(ask.state, key);
  const note = ask.note.key === key && ask.note.text !== "" ? ask.note.text : view.note;
  return (
    <Ask
      model={model}
      now={now}
      phase={view.phase}
      picked={view.picked}
      note={note}
      keyCaps
      narrow={narrow}
      numberKeys={false}
      onAnswer={ask.onAnswer}
      onNotNow={ask.onNotNow}
      onBringBack={ask.onBringBack}
      onDecline={ask.onDecline}
      onArchive={ask.onArchive}
    />
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

// readingNow is the window's scroll and the reader's own, in the section a column draws.
function readingNow(section: HTMLElement | null): Reading {
  return { x: window.scrollX, y: window.scrollY, reader: readerOf(section)?.scrollTop ?? 0 };
}

// returnTo puts the window and the reader back where reading was taken. It runs after the bar's focus moves, since a
// focus scrolls the box it lands in.
function returnTo(section: HTMLElement | null, reading: Reading | null): void {
  if (reading === null) {
    return;
  }
  const reader = readerOf(section);
  if (reader !== null) {
    reader.scrollTop = reading.reader;
  }
  window.scrollTo(reading.x, reading.y);
}

// readerOf is the reader a column draws for its row, if it draws one.
function readerOf(section: HTMLElement | null): HTMLElement | null {
  return section?.querySelector<HTMLElement>(".reader") ?? null;
}

// stepWhere is a step's where line: its epic, its agent, and its round.
function stepWhere(step: StepItem): string {
  return `${step.epicSlug} › ${step.agent} · step · round ${step.round}`;
}
