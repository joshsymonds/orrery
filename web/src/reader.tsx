import { Component, createElement, Fragment, type ComponentChildren, type JSX } from "preact";
import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";

import { agentFinishedTask, BackgroundRow, TaskLine } from "./backgroundrow";
import { CheckBlock, EventBlock, YouBlock } from "./blocks";
import { type EpicCalls, EpicWidget } from "./card";
import { ContractBlock } from "./contractblock";
import { type Conversation, shownConversation } from "./conversation";
import type { Match } from "./find";
import { EventRing, epicEventOf, StepCard, stepEventOf } from "./eventring";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { EpicStep } from "./gen/orrery/v1/epics_pb";
import type { ConversationItem, EngineText } from "./gen/orrery/v1/orrery_pb";
import { type CallReveal, EngineFold, NoticeLine, sameCalls, ThinkingLine, ToolLine, ToolRun, useReveal } from "./lines";
import { Reply } from "./reply";
import { Rule } from "./rules";
import type { Sending } from "./sending";
import { ReplyText } from "./text";

const tokensPerThousand = 1000;
const secondsToMillis = 1000;
const nearBottomPixels = 4;
const nearTopPixels = 4;
const toolRunMinimum = 3;
// pageOverlapPixels is how much of the last screen a page keeps on screen, so the owner keeps his place across a page.
const pageOverlapPixels = 40;
// pagingQuery is the width from which a reader pages by its handle. Under it the reader does not scroll on its own, and
// the keys that page it are left to the browser.
export const pagingQuery = "(min-width: 760px)";

// ReaderHandle is what the page calls on a reader to page its transcript by a screen. page moves the reader one screen
// up or down, less pageOverlapPixels, clamped to its top and bottom, and returns true. Where the reader does not scroll
// on its own, under pagingQuery, page does nothing and returns false.
export interface ReaderHandle {
  page: (direction: "up" | "down") => boolean;
}

// ContractCalls are the calls a reader draws its contract lines through: the contracts the list holds, and onOpen,
// which a press on a line calls with the contract's id.
export interface ContractCalls {
  contracts: readonly Contract[];
  onOpen: (id: string) => void;
}

// Reader draws one conversation: its items, the new rule before the first row newer than seenAt, a day rule before
// the first row when its local day is not the day of now (unix seconds; a reader given no now draws no leading day
// rule) and between two rows of different local days, the question's Ask after them, and the session's unfinished text
// and thinking. A result is never a row: it folds under the call it answers, and a result whose call is not held is
// hidden. Three or more tool calls in a row fold into one run line that opens to a line per call. A call still running,
// with nothing but calls and results after it and no question drawn, carries the running rail and mark, and so does a
// run that holds one. A call that creates an epic is never in a run: once its result is not an error, and the reader is
// given epic calls, the epic's card draws in its place. A background row's link reveals the Agent call it names: the
// call's row opens, with its result, and comes into view, and a run that holds the call opens first.
// A prompt draws by its sender, with or without a step (DL-G3). An owner's prompt, and one with no sender, is YOU: a
// YouBlock with its time. An event prompt is an EventBlock from the engine; an event whose text is an epic's event
// block draws as the event's ring when the reader holds epic calls, and one whose text is a step's end draws as its
// step card, with the step's facts and time. A cron prompt is an EventBlock labelled CRON. A brief, and an engine prompt
// of round 0, is an EngineFold line. An engine prompt of round 1 or more is a CheckBlock of its round's checks, which
// are the step's when the reader has a step. A prompt of any other sender draws no row.
// A reply is a Reply, and unfinished text a live Reply with its caret; an unfinished thinking block is a ThinkingLine.
// An agent_finished item is a TaskLine. A kind the reader does not know draws no row.
// The reader is its own scroll container: a conversation that replaces a null one opens at its bottom, pinned there. A
// reader pinned to its bottom, within nearBottomPixels of it as the owner last scrolled, stays there: after a render
// that draws items or live text, and whenever its own box or a row's box changes size, as the composer area above or
// below it does. A reader the owner scrolled up keeps its place. When items are drawn above the first row shown before,
// the reader keeps the rows on screen where they were. Once it sits within nearTopPixels of its top while the
// conversation has older items, it calls onOlder, which loads the page before its oldest item. The page pages it a
// screen at a time through the handle register gets.
// With answered, its line draws after the item its answer named as newest, or after every item when that item is no
// longer drawn, and a run of tool calls ends where the line falls. Items that arrive later draw after the line.
// With find, every case-insensitive occurrence of its query is a mark in what the reader draws of a reply, a prompt, a
// call's input and result, a notice, a compaction's summary, an agent-finished line, a step's brief, an epic's event
// sentence, and live text. Background rows, contract lines, thinking, the Ask, sending rows, a move's line, the card of
// an epic that was created, and the interrupted line are not searched. The current match, find.current's index in
// find.matches, is the mark with class current: it is scrolled into view as it arrives, and the fold, call or run that
// holds it opens. A fold opens when the current match moves into it, and the owner can close it; it opens again only
// when the current match moves into it once more.
// After the items and live text, the reader draws a row for each background task the conversation's state holds, in the
// state's order. A caller event the conversation holds pending is not drawn: it draws as its card once its prompt
// arrives. The sending entries draw after them, each as a YOU block marked sending in a row of its own. They are not
// items: they keep no key and no place in the scroll place, and a reader at its bottom keeps it as a new row arrives.
// A reader with nothing to draw says that nothing has been said yet, and then asks for something below, or says its
// hint in place of that, in place of its rows.
export function Reader({
  conversation,
  seenAt,
  open,
  asking = false,
  ask = null,
  answered,
  step,
  onToggleOpen,
  onOlder,
  onNewest,
  register,
  sending = [],
  find,
  epicCalls,
  contractCalls,
  now,
  hint = "Say something below to begin.",
}: {
  conversation: Conversation | null;
  seenAt: number;
  now?: number; // unix seconds: the day a leading day rule is judged against; no now draws no leading day rule
  open: boolean;
  asking?: boolean; // whether an open question is drawn after the items, which ends the calls still running
  ask?: ComponentChildren; // the question's Ask, drawn after the items
  answered?: { after: string | null; line: ComponentChildren }; // the answered line, and the key of the item it follows
  step?: EpicStep;
  onToggleOpen?: () => void;
  onOlder?: () => void;
  onNewest?: (atNewest: boolean) => void; // told each time the owner's scroll or a page changes whether the reader is pinned
  register?: (handle: ReaderHandle | null) => void; // gets the handle while mounted, and null once unmounted
  sending?: Sending[];
  find?: { query: string; matches: Match[]; current: number };
  epicCalls?: EpicCalls;
  contractCalls?: ContractCalls;
  hint?: string; // what an empty reader says in place of asking for something below
}): JSX.Element {
  const root = useRef<HTMLDivElement>(null);
  const drewConversation = useRef(false);
  const shownKeys = useRef<string[]>([]);
  // pinned is whether the owner last left the reader within nearBottomPixels of its bottom. Only the owner's scrolls set
  // it: a render or a size change moves the content under the reader without the owner, so it does not.
  const pinned = useRef(true);
  // The derivations of the items are made once per items array, so a redraw that holds the same array reuses them.
  const conversationItems = conversation?.items;
  const keys = useMemo(() => (conversationItems === undefined ? [] : itemKeys(conversationItems)), [conversationItems]);
  const results = useMemo(
    () => (conversationItems === undefined ? new Map<number, ConversationItem>() : resultsByCall(conversationItems)),
    [conversationItems],
  );
  const newIndex = useMemo(
    () =>
      conversationItems === undefined
        ? -1
        : conversationItems.findIndex((item) => item.kind !== "result" && Number(item.time) > seenAt),
    [conversationItems, seenAt],
  );
  // A contract line draws in place of its call row, so while the reader holds contract calls the call is never folded
  // into a run.
  const contracted = contractCalls !== undefined;
  // The answered line falls after the item its answer named, so a run of tool calls ends there. Without a line the index
  // is -1, which ends no run.
  const answeredAt = answered === undefined ? -1 : answeredIndexOf(answered.after, keys);
  const groups = useMemo(
    () =>
      conversationItems === undefined ? [] : groupRows(conversationItems, newIndex, results, contracted, answeredAt),
    [conversationItems, newIndex, results, contracted, answeredAt],
  );
  // The rows reach onToggleOpen through toggle, so the callback a row holds is the same function on every render, and a
  // caller's new callback does not make a row draw again. A row gets a toggle only when the caller passed one, since a
  // compaction row draws its rule as a button only then.
  const toggle = useRef(onToggleOpen);
  toggle.current = onToggleOpen;
  const toggleOpen = useCallback(() => toggle.current?.(), []);
  const rowToggle = onToggleOpen === undefined ? undefined : toggleOpen;
  // The rows reach the epic calls through rowCalls, which carries the epics of the caller's calls and passes each call
  // to the calls of the latest render. It is made again only when the epics change or the calls come or go, so a
  // caller's new calls object over the same epics does not make a card or ring row draw again, while a new epics load
  // does, and the card draws what it holds then.
  const latestCalls = useRef(epicCalls);
  latestCalls.current = epicCalls;
  const rowCalls = useMemo(
    () => (epicCalls === undefined ? undefined : callsAt(latestCalls, epicCalls)),
    [epicCalls?.epics],
  );
  // A background row's link asks for its Agent call to be revealed. Each click makes a new request, so a click on the same
  // call again is acted on, and the call's row ends the request once it has acted on it.
  const [reveal, setReveal] = useState<CallReveal | undefined>(undefined);
  const openCall = useCallback((toolUseId: string) => {
    const request: CallReveal = {
      id: toolUseId,
      done: () => setReveal((held) => (held === request ? undefined : held)),
    };
    setReveal(request);
  }, []);
  // query is what the reader marks, and focus is the current match, when there is a query and a current one.
  const query = find?.query ?? "";
  const focus = find !== undefined && query !== "" && find.current >= 0 ? find.matches[find.current] : undefined;
  const focusItem = focus?.item ?? -1;
  const focusStart = focus?.start ?? -1;
  const currentAt = (index: number): number => (index === focusItem ? focusStart : -1);
  // The position is read before this render changes the DOM, so it is where the reader sat before the new items. A size
  // change the observer has not reported yet is not in that reading, so the observer follows it.
  const element = root.current;
  const followRender = pinned.current && element !== null && distanceFromBottom(element) <= nearBottomPixels;
  const before = element === null ? null : { top: element.scrollTop, height: element.scrollHeight };
  const opening = conversation !== null && !drewConversation.current;
  const more = conversation?.more ?? false;
  const askOlder = (): void => {
    const reader = root.current;
    if (more && reader !== null && reader.scrollTop <= nearTopPixels) {
      onOlder?.();
    }
  };
  const followScroll = (): void => {
    const reader = root.current;
    if (reader !== null) {
      const wasPinned = pinned.current;
      pinned.current = distanceFromBottom(reader) <= nearBottomPixels;
      if (pinned.current !== wasPinned) {
        onNewest?.(pinned.current);
      }
    }
    askOlder();
  };
  // page moves the reader a screen less pageOverlapPixels, clamped to its top and bottom, then reads its place as an
  // owner's scroll does, so a page up from the bottom unpins the reader.
  const page = (direction: "up" | "down"): boolean => {
    const reader = root.current;
    if (reader === null || !window.matchMedia(pagingQuery).matches) {
      return false;
    }
    const screen = reader.clientHeight - pageOverlapPixels;
    const furthest = Math.max(0, reader.scrollHeight - reader.clientHeight);
    const target = direction === "down" ? reader.scrollTop + screen : reader.scrollTop - screen;
    reader.scrollTop = Math.min(Math.max(target, 0), furthest);
    followScroll();
    return true;
  };
  useLayoutEffect(() => {
    const previous = shownKeys.current;
    shownKeys.current = keys;
    drewConversation.current = conversation !== null;
    const reader = root.current;
    if (reader === null) {
      return;
    }
    // A conversation that opens starts pinned at its bottom, whatever the owner did to the one shown before it.
    if (opening) {
      pinned.current = true;
      onNewest?.(true);
    }
    // A render that draws items above the first row shown before keeps that row where it was: the reader moves by
    // how much its content grew above it.
    const prepended = previous.length > 0 && keys.indexOf(previous[0]) > 0;
    if (before !== null && prepended) {
      reader.scrollTop = before.top + (reader.scrollHeight - before.height);
    } else if (opening || followRender) {
      reader.scrollTop = reader.scrollHeight;
    }
    askOlder();
  });
  // A size change that no render reports moves the bottom too: the composer area above or below the reader changes the
  // reader's own box, and a row grows as its content does, when a fold opens in it. The observer watches what this
  // render drew, so it is made again on each render, and the previous one is disconnected before the next is made and
  // when the reader unmounts.
  useLayoutEffect(() => {
    const reader = root.current;
    if (reader === null) {
      return;
    }
    const sizes = new ResizeObserver(() => {
      if (pinned.current) {
        reader.scrollTop = reader.scrollHeight;
      }
    });
    sizes.observe(reader);
    for (const row of reader.children) {
      sizes.observe(row);
    }
    return () => {
      sizes.disconnect();
    };
  });
  // The page holds the handle while the reader is mounted. It is made again on each render, so page asks with the
  // older-items flag and callback that render drew, and null is passed before each new handle and once unmounted.
  useLayoutEffect(() => {
    register?.({ page });
    return () => register?.(null);
  });
  // Under pagingQuery the page scrolls as a whole, not the reader, so the owner's scroll is the window's: it reports
  // whether the page sits at its end, as the reader's own scroll reports its bottom. At pagingQuery and wider the reader
  // scrolls on its own, and the window's scroll changes nothing.
  useLayoutEffect(() => {
    const followWindow = (): void => {
      if (window.matchMedia(pagingQuery).matches) {
        return;
      }
      const wasPinned = pinned.current;
      pinned.current = distanceFromBottom(document.documentElement) <= nearBottomPixels;
      if (pinned.current !== wasPinned) {
        onNewest?.(pinned.current);
      }
    };
    window.addEventListener("scroll", followWindow);
    return () => window.removeEventListener("scroll", followWindow);
  }, [onNewest]);
  const sendingRows = sending.map((entry) => (
    <div key={entry.promptId} class="t owner-turn sending-row">
      <span class="gut" />
      <div class="body">
        <YouBlock text={entry.text} sending />
      </div>
    </div>
  ));
  if (conversation === null) {
    return (
      <div class="reader" ref={root}>
        <p class="reader-loading">loading the conversation…</p>
        {sendingRows}
      </div>
    );
  }
  const items = conversation.items;
  const liveBlocks = conversation.unfinished.filter((block) => block.kind === "text" || block.kind === "thinking");
  // An open question's Ask or live text drawn after the items ends the calls, so none of them is running.
  const running = runningCalls(items, results, asking || liveBlocks.length > 0);
  const rows: JSX.Element[] = [];
  let answeredDrawn = false;
  const today = now === undefined ? undefined : new Date(now * secondsToMillis).toDateString();
  groups.forEach((group, rank) => {
    const [first] = group;
    if (answered !== undefined && !answeredDrawn && first > answeredAt) {
      rows.push(<Fragment key="answered">{answered.line}</Fragment>);
      answeredDrawn = true;
    }
    if (dayChanged(items, first, today, rank === 0)) {
      rows.push(<DayRule key={`day/${keys[first]}`} time={items[first].time} />);
    }
    if (first === newIndex) {
      rows.push(<NewRule key="new-rule" />);
    }
    if (group.length >= toolRunMinimum) {
      const calls = group.map((index) => ({
        key: keys[index],
        item: items[index],
        result: results.get(index),
        running: running.has(index),
        current: currentAt(index),
        reveal: revealOf(items[index], reveal),
      }));
      rows.push(<MemoToolRun key={keys[first]} calls={calls} query={query} />);
      return;
    }
    // Only a compaction row draws the open state, so a toggle of it leaves every other row as it was.
    rows.push(
      <MemoItemRow
        key={keys[first]}
        item={items[first]}
        result={results.get(first)}
        running={running.has(first)}
        open={open && items[first].kind === "compaction"}
        step={step}
        onToggleOpen={rowToggle}
        query={query}
        current={currentAt(first)}
        epicCalls={rowCalls}
        contractCalls={contractCalls}
        reveal={revealOf(items[first], reveal)}
      />,
    );
  });
  if (answered !== undefined && !answeredDrawn) {
    rows.push(<Fragment key="answered">{answered.line}</Fragment>);
  }
  const live = liveBlocks.map((block) => (
    <LiveRow
      key={`${block.messageId}/${block.index}`}
      block={block}
      query={query}
      current={currentAt(items.length + conversation.unfinished.indexOf(block))}
    />
  ));
  const background = (conversation.state?.background ?? []).map((task) => (
    <BackgroundRow key={task.id} task={task} onOpen={openCall} />
  ));
  const nothingYet =
    items.length === 0 &&
    liveBlocks.length === 0 &&
    background.length === 0 &&
    sending.length === 0 &&
    !asking &&
    answered === undefined;
  return (
    <div class="reader" ref={root} onScroll={followScroll}>
      {nothingYet && (
        <div class="blank">
          <span class="t-body">Nothing has been said yet.</span>
          <span>{hint}</span>
        </div>
      )}
      {rows}
      {ask}
      {live}
      {background}
      {sendingRows}
    </div>
  );
}

// ReaderHead names the conversation the reader draws and what it is doing: its label as the heading, which is text or a
// title a caller draws, its where line when there is one, and the children after it, the pill the app passes in.
export function ReaderHead({
  label,
  where,
  children,
}: {
  label: ComponentChildren;
  where: string;
  children?: ComponentChildren;
}): JSX.Element {
  return (
    <header class="head">
      <h1>{label}</h1>
      {where !== "" && <span class="where">{where}</span>}
      {children}
    </header>
  );
}

// NewRule draws the since-you-looked mark before the first item newer than the owner last looked.
function NewRule() {
  return (
    <RuleRow>
      <Rule kind="new" label="New since you looked" />
    </RuleRow>
  );
}

// DayRule draws the local day a transcript opens or changes to, before the first item of that day.
function DayRule({ time }: { time: bigint }) {
  return (
    <RuleRow>
      <Rule kind="day" label={dayLabel(time)} />
    </RuleRow>
  );
}

// RuleRow is the row a rule draws in. A rule spans the column, so the row has no gutter time.
function RuleRow({ children }: { children: ComponentChildren }) {
  return (
    <div class="t">
      <span class="gut" />
      <div class="body">{children}</div>
    </div>
  );
}

// ItemRowProps are an item's row's props. query is the find's query, and current is the offset in the item's searched
// text where the current match starts, or -1 when the current match is not in this item.
interface ItemRowProps {
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  open: boolean;
  step?: EpicStep;
  onToggleOpen?: () => void;
  query: string;
  current: number;
  epicCalls?: EpicCalls;
  contractCalls?: ContractCalls;
  reveal?: CallReveal;
}

function ItemRow({
  item,
  result,
  running,
  open,
  step,
  onToggleOpen,
  query,
  current,
  epicCalls,
  contractCalls,
  reveal,
}: ItemRowProps) {
  // An item whose body draws nothing draws no row at all: no gutter time, and no owner's-turn gap.
  const body = itemBody({
    item,
    result,
    running,
    open,
    step,
    onToggleOpen,
    query,
    current,
    epicCalls,
    contractCalls,
    reveal,
  });
  if (body === null) {
    return null;
  }
  // Its row carries the owner-turn class, which sets the gap above it, unless the row is an epic's event ring.
  const ringed = item.kind === "prompt" && epicCalls !== undefined && epicEventOf(item.text) !== null;
  return (
    <div class={ownersTurn(item) && !ringed ? "t owner-turn" : "t"}>
      <span class="gut">{item.kind === "interrupted" ? "" : clock(item.time)}</span>
      <div class="body">{body}</div>
    </div>
  );
}

// MemoItemRow draws an ItemRow again only when sameItemRow finds its props changed.
const MemoItemRow = memo(ItemRow, sameItemRow);

// sameItemRow is whether new props draw the row the old ones drew: the same item, result, running and open state and
// toggle, a step that reads the same, the same find query and current match, the same reveal, and the same epic calls
// when the row reads them. An item or result is compared by its object, which the held items keep from one delta to the
// next.
function sameItemRow(previous: ItemRowProps, next: ItemRowProps): boolean {
  return (
    previous.item === next.item &&
    previous.result === next.result &&
    previous.running === next.running &&
    previous.open === next.open &&
    previous.onToggleOpen === next.onToggleOpen &&
    previous.query === next.query &&
    previous.current === next.current &&
    previous.reveal === next.reveal &&
    sameStep(previous.step, next.step) &&
    (previous.epicCalls === next.epicCalls || !readsEpicCalls(next.item)) &&
    (previous.contractCalls === next.contractCalls || !readsContractCalls(next.item))
  );
}

// readsEpicCalls is whether a row of item draws from the epic calls: an event prompt whose text is an epic's event block,
// which draws as the event's ring, or a step's end, which draws as a card that opens the step; or a call that creates an
// epic, which draws its epic's card. Every other row reads nothing of them, so the calls it is given do not change it.
function readsEpicCalls(item: ConversationItem): boolean {
  if (item.kind === "prompt") {
    return epicEventOf(item.text) !== null || (item.sender === "event" && stepEventOf(item.text) !== null);
  }
  return (item.kind === "tool" || item.kind === "question") && item.epic !== "";
}

// readsContractCalls is whether a row of item draws from the contract calls: a tool call that writes a contract, which
// draws its contract line in place of its call row when the result is not an error.
function readsContractCalls(item: ConversationItem): boolean {
  return item.kind === "tool" && item.contract !== "";
}

// drawsContractLine is whether a tool call draws its contract's line in place of its call row: it writes a contract and
// its result is not an error. The reader draws the line only while it holds contract calls.
function drawsContractLine(item: ConversationItem, result: ConversationItem | undefined): boolean {
  return item.contract !== "" && result !== undefined && !result.error;
}

// callsAt makes epic calls over the epics of made that pass each call to the calls latest holds when the call is made,
// or to made once latest holds none. A row holding them reaches the caller's latest calls without drawing again.
function callsAt(latest: { current: EpicCalls | undefined }, made: EpicCalls): EpicCalls {
  const current = (): EpicCalls => latest.current ?? made;
  return {
    get: (id) => current().get(id),
    answer: (id, question, choice, text) => current().answer(id, question, choice, text),
    openEpic: (id) => current().openEpic(id),
    openStep: (epic, step) => current().openStep(epic, step),
    stepResult: (id) => current().stepResult(id),
    sessionTitle: (session) => current().sessionTitle?.(session),
    get now() {
      return current().now;
    },
    epics: made.epics,
  };
}

// sameStep is whether two steps read the same to a row: both absent, or both present with checks that match in order by
// name, round and ok. A row reads nothing else of a step, so a step rebuilt with the same checks is the same step.
function sameStep(previous: EpicStep | undefined, next: EpicStep | undefined): boolean {
  if (previous === undefined || next === undefined) {
    return previous === next;
  }
  return (
    previous.checks.length === next.checks.length &&
    previous.checks.every((check, index) => {
      const other = next.checks[index];
      return other !== undefined && check.name === other.name && check.round === other.round && check.ok === other.ok;
    })
  );
}

// memo draws a component again only when same finds its props changed, as preact/compat's memo does. It is written here
// because importing preact/compat installs its option hooks on every vnode of the app.
function memo<P extends object>(
  component: (props: P) => JSX.Element | null,
  same: (previous: P, next: P) => boolean,
) {
  return class Memoed extends Component<P> {
    shouldComponentUpdate(next: Readonly<P>): boolean {
      return !same(this.props, next);
    }

    render(): JSX.Element {
      return createElement(component, this.props);
    }
  };
}

// itemBody is the body an item draws, or null when it draws nothing: ItemRow draws no row for null.
function itemBody({
  item,
  result,
  running,
  open,
  step,
  onToggleOpen,
  query,
  current,
  epicCalls,
  contractCalls,
  reveal,
}: {
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  open: boolean;
  step?: EpicStep;
  onToggleOpen?: () => void;
  query: string;
  current: number;
  epicCalls?: EpicCalls;
  contractCalls?: ContractCalls;
  reveal?: CallReveal;
}): JSX.Element | null {
  switch (item.kind) {
    case "prompt": {
      if (item.sender === "event") {
        const stepEnd = stepEventOf(item.text);
        if (stepEnd !== null) {
            return (
            <StepCard
              event={stepEnd}
              facts={item.stepFacts}
              time={item.time}
              calls={epicCalls}
              query={query}
              current={current}
            />
          );
        }
      }
      if (item.sender === "cron") {
        return <EventBlock text={item.text} time={item.time} cron query={query} current={current} />;
      }
      if (epicCalls !== undefined) {
        const event = epicEventOf(item.text);
        if (event !== null) {
          return <EventRing event={event} calls={epicCalls} query={query} current={current} />;
        }
      }
      if (item.sender === "event") {
        return <EventBlock text={item.text} time={item.time} query={query} current={current} />;
      }
      if (foldsAsEngine(item)) {
        return <EngineFold item={item} query={query} current={current} />;
      }
      if (item.sender === "engine") {
        // A send-back of round 1 or more draws its round's checks: the step's, when the reader has a step, else none.
        return (
          <CheckBlock
            text={item.text}
            round={item.round}
            checks={step?.checks ?? []}
            time={item.time}
            query={query}
            current={current}
          />
        );
      }
      if (ownersTurn(item)) {
        return (
          <YouBlock
            text={item.text}
            time={item.time}
            phone={item.client === "orrery-android"}
            query={query}
            current={current}
          />
        );
      }
      return null;
    }
    case "tool":
    case "question": {
      // An epic call whose result is not an error draws its epic's card in place of its row, when the reader holds
      // epic calls.
      if (item.epic !== "" && epicCalls !== undefined && result !== undefined && !result.error) {
        return <EpicWidget epic={item.epic} calls={epicCalls} />;
      }
      // A tool call that writes a contract draws the contract's line in place of its row, when the reader holds
      // contract calls.
      if (contractCalls !== undefined && drawsContractLine(item, result)) {
        return (
          <ContractBlock
            id={item.contract}
            contract={contractCalls.contracts.find((contract) => contract.id === item.contract)}
            onOpen={contractCalls.onOpen}
          />
        );
      }
      return (
        <ToolLine item={item} result={result} running={running} query={query} current={current} reveal={reveal} />
      );
    }
    case "notice":
      return <NoticeLine item={item} query={query} current={current} />;
    case "compaction":
      return <Compaction item={item} open={open} onToggleOpen={onToggleOpen} query={query} current={current} />;
    case "interrupted":
      return <Rule kind="interrupted" label="Interrupted by you" detail={clock(item.time)} />;
    case "reply":
      return <Reply text={item.text} query={query} current={current} />;
    case "agent_finished":
      return <TaskLine {...agentFinishedTask(item)} query={query} current={current} />;
    default:
      return null;
  }
}

// revealOf is the reveal a row of item is given: the reveal when item is the tool call it names, and none for any other
// item.
function revealOf(item: ConversationItem, reveal: CallReveal | undefined): CallReveal | undefined {
  return reveal !== undefined && item.kind === "tool" && item.id === reveal.id ? reveal : undefined;
}

// ownersTurn is whether an item is the owner's turn, YOU: a prompt the owner sent, or one stored with no sender. Its row
// carries the owner-turn class, and its body draws as a YouBlock.
function ownersTurn(item: ConversationItem): boolean {
  return item.kind === "prompt" && (item.sender === "owner" || item.sender === "");
}

// foldsAsEngine is whether a prompt draws as an EngineFold line, with or without a step: a brief, or an engine prompt of
// round 0.
function foldsAsEngine(item: ConversationItem): boolean {
  return item.kind === "prompt" && (item.sender === "brief" || (item.sender === "engine" && item.round === 0));
}

// MemoToolRun draws a ToolRun again only when its find query or its calls change: sameCalls compares them by content.
const MemoToolRun = memo(
  ToolRun,
  (previous, next) => previous.query === next.query && sameCalls(previous.calls, next.calls),
);

// LiveRow draws one unfinished block: a text block as a live Reply, which ends in its caret while it streams and marks
// the find's matches as a reply does, and a thinking block as a ThinkingLine. current is the offset where the current
// match starts in the block's text, or -1 when the current match is not in it.
function LiveRow({ block, query, current }: { block: EngineText; query: string; current: number }) {
  return (
    <div class="t live-row">
      <span class="gut live">live</span>
      <div class="body">
        {block.kind === "thinking" ? (
          <ThinkingLine text={block.text} />
        ) : (
          <Reply text={block.text} live query={query} current={current} />
        )}
      </div>
    </div>
  );
}

// Compaction draws a compaction as a Rule, labelled Compacted with its trigger and tokens as detail, and under it the
// summary once the rule is open. The summary is marked with the find's query. When the current match moves into the
// summary, the rule is opened through onToggleOpen unless it is open, as a fold opens (useReveal), so the owner can close
// it again and it opens once more only when the current match moves into it again.
function Compaction({
  item,
  open,
  onToggleOpen,
  query,
  current,
}: {
  item: ConversationItem;
  open: boolean;
  onToggleOpen?: () => void;
  query: string;
  current: number;
}) {
  useReveal(current >= 0, (shown) => {
    if (shown && !open) {
      onToggleOpen?.();
    }
  });
  const parts: string[] = [];
  if (item.trigger !== "") {
    parts.push(item.trigger);
  }
  if (item.tokens > 0n) {
    parts.push(`${Math.round(Number(item.tokens) / tokensPerThousand)}k tokens`);
  }
  return (
    <div class={open ? "compaction open" : "compaction"}>
      <Rule kind="compaction" label="Compacted" detail={parts.join(" · ")} onToggle={onToggleOpen} open={open} />
      {open && (
        <div class="summary">
          <ReplyText text={item.text} query={query} current={current} />
        </div>
      )}
    </div>
  );
}

// distanceFromBottom is how far the element's scroll sits above its bottom.
function distanceFromBottom(element: HTMLElement): number {
  return element.scrollHeight - element.scrollTop - element.clientHeight;
}

// resultsByCall pairs each result with the call it answers, as the phone's ConversationModel does: a result answers the
// latest call before it with the same id, and a later result for that call replaces an earlier one. A result with no
// such call is in no entry, so it is never drawn. The map is keyed by the call's index in items.
function resultsByCall(items: ConversationItem[]): Map<number, ConversationItem> {
  const callIndexById = new Map<string, number>();
  const results = new Map<number, ConversationItem>();
  items.forEach((item, index) => {
    if (item.kind === "tool" || item.kind === "question") {
      callIndexById.set(item.id, index);
    } else if (item.kind === "result") {
      const call = callIndexById.get(item.id);
      if (call !== undefined) {
        results.set(call, item);
      }
    }
  });
  return results;
}

// groupRows lists the rows a reader draws as the indices of their items, in order. A run of toolRunMinimum or more tool
// calls with only results between them is one row; every other item is a row of its own, and a result is in none. A
// question or any other item ends a run, and so does the since-you-looked rule, which starts the row at newIndex. A
// tool call that creates an epic is never in a run: it is a row of its own, and the calls either side of it fold apart.
// A tool call whose contract line draws, when contracted, is never in a run either.
function groupRows(
  items: ConversationItem[],
  newIndex: number,
  results: Map<number, ConversationItem>,
  contracted: boolean,
  answeredAt: number,
): number[][] {
  const rows: number[][] = [];
  let run: number[] = [];
  const endRun = () => {
    if (run.length >= toolRunMinimum) {
      rows.push(run);
    } else {
      run.forEach((index) => rows.push([index]));
    }
    run = [];
  };
  const foldable = (item: ConversationItem, index: number) =>
    item.kind === "tool" && item.epic === "" && !(contracted && drawsContractLine(item, results.get(index)));
  items.forEach((item, index) => {
    // The answered line falls after the item at answeredAt, so the run that holds it ends there, before any result.
    if (index === answeredAt + 1) {
      endRun();
    }
    if (item.kind === "result") {
      return;
    }
    if (index === newIndex || !foldable(item, index)) {
      endRun();
    }
    if (foldable(item, index)) {
      run.push(index);
    } else {
      rows.push([index]);
    }
  });
  endRun();
  return rows;
}

// runningCalls returns the indices of the tool calls that are running: each has no result and is followed by nothing
// but tool calls and results. When something follows the items, a question or live text, no call is running.
function runningCalls(
  items: ConversationItem[],
  results: Map<number, ConversationItem>,
  somethingFollows: boolean,
): Set<number> {
  const running = new Set<number>();
  if (somethingFollows) {
    return running;
  }
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const { kind } = items[index];
    if (kind !== "tool" && kind !== "result") {
      break;
    }
    if (kind === "tool" && !results.has(index)) {
      running.add(index);
    }
  }
  return running;
}

// answeredIndexOf is the index of the item an answered line follows, from the key of the item newest when its answer was
// pressed: -1 when that was none, so the line draws first, and the item count when that item is no longer drawn, so the
// line draws after every item.
function answeredIndexOf(after: string | null, keys: string[]): number {
  if (after === null) {
    return -1;
  }
  const index = keys.indexOf(after);
  return index === -1 ? keys.length : index;
}

// newestItemKey is the key of the newest item a conversation draws, which an answer records when it is pressed, or null
// when it draws none.
export function newestItemKey(conversation: Conversation | null): string | null {
  const keys = itemKeys(shownConversation(conversation)?.items ?? []);
  return keys.at(-1) ?? null;
}

// itemKeys keys each item by its content, so a row keeps its key while the window drops items from the top or prepends
// older ones. An item whose content repeats a newer one takes an occurrence suffix counted from the newest: the
// occurrence of an item then depends only on the items after it, so a page prepended above leaves every held key as
// it was. A repeat appended after an item renumbers the item's earlier twins, which remount.
function itemKeys(items: ConversationItem[]): string[] {
  const seen = new Map<string, number>();
  const keys: string[] = new Array<string>(items.length);
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    const base = JSON.stringify([
      item.kind,
      String(item.time),
      item.text,
      item.tool,
      item.input,
      item.id,
      item.error,
      item.notice,
      item.trigger,
      String(item.tokens),
    ]);
    const occurrence = seen.get(base) ?? 0;
    seen.set(base, occurrence + 1);
    keys[index] = occurrence === 0 ? base : `${base}#${occurrence}`;
  }
  return keys;
}

// dayLabelFormat is the formatter of a day rule's label, as the rule's words are in English: Monday 16 November.
const dayLabelFormat = new Intl.DateTimeFormat("en-GB", { weekday: "long", day: "numeric", month: "long" });

// dayLabel is the label of the day rule for the local day a time falls on.
function dayLabel(time: bigint): string {
  return dayLabelFormat.format(new Date(Number(time) * secondsToMillis));
}

// dayOfTime is the local day a time falls on, as toDateString names it, or "" for a time of 0, which is no time: clock
// draws none for it either.
function dayOfTime(time: bigint): string {
  return time === 0n ? "" : new Date(Number(time) * secondsToMillis).toDateString();
}

// dayChanged is whether the row at first opens a local day, and so draws a day rule before it: the first row drawn when
// its day is not today (today is the day of the reader's now, or undefined when it has none, and then no first row
// opens a day), and any other row whose day is not the day of the item before it. A row whose item or whose predecessor
// has no time opens no day.
function dayChanged(items: ConversationItem[], first: number, today: string | undefined, firstRow: boolean): boolean {
  const day = dayOfTime(items[first].time);
  if (day === "") {
    return false;
  }
  if (firstRow) {
    return today !== undefined && day !== today;
  }
  const before = dayOfTime(items[first - 1].time);
  return before !== "" && before !== day;
}

// clockFormat is the formatter of every gutter time: 24-hour hours and minutes in the local zone. One formatter serves
// all rows, since a formatter built per call costs far more than the format it makes.
const clockFormat = new Intl.DateTimeFormat([], { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

export function clock(time: bigint): string {
  if (time === 0n) {
    return "";
  }
  return clockFormat.format(new Date(Number(time) * secondsToMillis));
}
