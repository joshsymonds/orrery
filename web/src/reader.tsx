import { Component, createElement, type ComponentChildren, type JSX } from "preact";
import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";

import { BackgroundRow } from "./backgroundrow";
import { type EpicCalls, EpicWidget } from "./card";
import type { Conversation } from "./conversation";
import type { Match } from "./find";
import { EventRing, epicEventOf, StepCard, stepEventOf } from "./eventring";
import { useFlash } from "./flash";
import type { EpicStep, EpicStepCheck } from "./gen/orrery/v1/epics_pb";
import type { ConversationItem, EngineText } from "./gen/orrery/v1/orrery_pb";
import { Markdown } from "./markdown";
import type { ParsedQuestion } from "./question";
import type { Sending } from "./sending";
import { MarkedText, ReplyText } from "./text";

const tokensPerThousand = 1000;
const secondsToMillis = 1000;
const answerChoices = [1, 2, 3] as const;
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

// CallReveal is a request to bring the tool call with id into view and open it. done ends the request once the call's
// row has acted on it, so a row that mounts later, as a closed run opens again, does not act on it a second time.
interface CallReveal {
  id: string;
  done: () => void;
}

// Reader draws one conversation: its items, the "since you looked" rule before the first row newer than seenAt, the
// pane's question with a button per answer, and the session's unfinished text and thinking. A result is never a row:
// it folds under the call it answers, and a result whose call is not held is hidden. Three or more tool calls in a row
// fold into one run line that opens to a line per call. A call still running, with nothing but calls and results after
// it and no question drawn, carries the running rail and mark, and so does a run that holds one. A call that creates an
// epic is never in a run: once its result is not an error, and the reader is given epic calls, the epic's card draws in
// its place. A background row's link reveals the Agent call it names: the call's row opens, with its result, and comes
// into view, and a run that holds the call opens first.
// A reply, a prompt and live text draw through Markdown, which builds only its fixed elements. Every other text is drawn
// as text: through ReplyText, or as a plain child of an element, so no markup becomes an element and no link sits inside
// a button. A prompt whose text is an epic's event block draws as the event's ring when the reader holds epic calls. An
// event prompt whose text is a step's end block draws as its step card, and never as an EVENT row; each such prompt draws
// its own card. Any other prompt an event sent draws as an EVENT row, with or without a step, and never as YOU or a step
// prompt. A prompt a cron sent draws as a CRON row the same way, and never as YOU, with or without a step.
// With a step, any other prompt draws by its sender and round: the brief as a fold, a send-back as a card of its
// round's checks, any other prompt as an engine fold. An owner's prompt, and every other prompt without a step, is YOU.
// The reader is its own scroll container: a conversation that replaces a null one opens at its bottom, pinned there. A
// reader pinned to its bottom, within nearBottomPixels of it as the owner last scrolled, stays there: after a render
// that draws items or live text, and whenever its own box or a row's box changes size, as the composer area above or
// below it does. A reader the owner scrolled up keeps its place. When items are drawn above the first row shown before,
// the reader keeps the rows on screen where they were. Once it sits within nearTopPixels of its top while the
// conversation has older items, it calls onOlder, which loads the page before its oldest item. The page pages it a
// screen at a time through the handle register gets.
// With find, every case-insensitive occurrence of its query is a mark in what the reader draws of a reply, a prompt, a
// call's input and result, and a notice. Compaction, check cards, epic cards, event rings, step cards,
// background rows, live text, thinking, the question card, sending rows, event lines, cron rows and agent-finished
// lines are not searched. The current match, find.current's index in find.matches, is the mark with class current: it
// is scrolled into view as it arrives, and the fold, call or run that holds it opens. A fold opens when the current
// match moves into it, and the owner can close it; it opens again only when the current match moves into it once more.
// After the items and live text, the reader draws a row for each background task the conversation's state holds, in the
// state's order. A caller event the conversation holds pending is not drawn: it draws as its card once its prompt
// arrives. The sending entries draw after them, each as a dimmed prompt
// row marked sending. They are not items: they keep no key and no place in the scroll place, and a reader
// at its bottom keeps it as a new row arrives.
export function Reader({
  conversation,
  seenAt,
  open,
  question = null,
  step,
  onAnswer,
  onToggleOpen,
  onOlder,
  onNewest,
  register,
  sending = [],
  find,
  epicCalls,
}: {
  conversation: Conversation | null;
  seenAt: number;
  open: boolean;
  question?: ParsedQuestion | null;
  step?: EpicStep;
  onAnswer?: (choice: 1 | 2 | 3) => void;
  onToggleOpen?: () => void;
  onOlder?: () => void;
  onNewest?: (atNewest: boolean) => void; // told each time the owner's scroll or a page changes whether the reader is pinned
  register?: (handle: ReaderHandle | null) => void; // gets the handle while mounted, and null once unmounted
  sending?: Sending[];
  find?: { query: string; matches: Match[]; current: number };
  epicCalls?: EpicCalls;
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
  const groups = useMemo(
    () => (conversationItems === undefined ? [] : groupRows(conversationItems, newIndex)),
    [conversationItems, newIndex],
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
  const sendingRows = sending.map((entry) => <SendingRow key={entry.promptId} text={entry.text} />);
  if (conversation === null) {
    return (
      <div class="reader" ref={root}>
        <p class="reader-loading">loading…</p>
        {sendingRows}
      </div>
    );
  }
  const items = conversation.items;
  const liveBlocks = conversation.unfinished.filter((block) => block.kind === "text" || block.kind === "thinking");
  // A question or live text drawn after the items ends the calls, so none of them is running.
  const running = runningCalls(items, results, question !== null || liveBlocks.length > 0);
  const rows: JSX.Element[] = [];
  groups.forEach((group) => {
    const [first] = group;
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
        reveal={revealOf(items[first], reveal)}
      />,
    );
  });
  const questionCard = question === null ? null : <QuestionCard question={question} onAnswer={onAnswer} />;
  const live = liveBlocks.map((block) => <LiveRow key={`${block.messageId}/${block.index}`} block={block} />);
  const background = (conversation.state?.background ?? []).map((task) => (
    <BackgroundRow key={task.id} task={task} onOpen={openCall} />
  ));
  return (
    <div class="reader" ref={root} onScroll={followScroll}>
      {rows}
      {questionCard}
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

function NewRule() {
  return (
    <div class="t">
      <span class="gut new">new</span>
      <div class="body">
        <div class="rule new">since you looked</div>
      </div>
    </div>
  );
}

function QuestionCard({
  question,
  onAnswer,
}: {
  question: ParsedQuestion;
  onAnswer?: (choice: 1 | 2 | 3) => void;
}) {
  return (
    <div class="t">
      <span class="gut" />
      <div class="body question-card">
        <div class="question">
          <ReplyText text={question.title} />
        </div>
        {question.options.map((option, index) => {
          const label = (
            <>
              <span class="option-label">{option.label}</span>
              {option.description !== "" && <span class="option-description">{option.description}</span>}
            </>
          );
          const choice = answerChoices[index];
          if (choice === undefined) {
            return (
              <div class="option" key={index}>
                {label}
              </div>
            );
          }
          return <OptionButton key={index} choice={choice} label={label} onAnswer={onAnswer} />;
        })}
      </div>
    </div>
  );
}

// OptionButton answers its choice on click and flashes for flashMillis, so each option flashes on its own.
function OptionButton({
  choice,
  label,
  onAnswer,
}: {
  choice: 1 | 2 | 3;
  label: JSX.Element;
  onAnswer?: (choice: 1 | 2 | 3) => void;
}) {
  const [flashing, flash] = useFlash();
  return (
    <button
      type="button"
      class={flashing ? "option flash" : "option"}
      onClick={() => {
        flash();
        onAnswer?.(choice);
      }}
    >
      {label}
    </button>
  );
}

// SendingRow draws a message the page sent that the stored conversation does not hold yet: a prompt row, dimmed, with
// a mark reading sending. Its text draws through Markdown, as a prompt's does.
function SendingRow({ text }: { text: string }) {
  return (
    <div class="t owner-turn sending-row">
      <span class="gut sending">sending</span>
      <div class="body">
        <div class="you">
          <small>YOU</small>
          <ReplyMarkdown text={text} />
        </div>
      </div>
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
  reveal,
}: ItemRowProps) {
  // Its row carries the owner-turn class, which sets the gap above it, unless the row is an epic's event ring.
  const ringed = item.kind === "prompt" && epicCalls !== undefined && epicEventOf(item.text) !== null;
  return (
    <div class={ownersTurn(item, step) && !ringed ? "t owner-turn" : "t"}>
      <span class="gut">{clock(item.time)}</span>
      <div class="body">
        <ItemBody
          item={item}
          result={result}
          running={running}
          open={open}
          step={step}
          onToggleOpen={onToggleOpen}
          query={query}
          current={current}
          epicCalls={epicCalls}
          reveal={reveal}
        />
      </div>
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
    (previous.epicCalls === next.epicCalls || !readsEpicCalls(next.item))
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

// callsAt makes epic calls over the epics of made that pass each call to the calls latest holds when the call is made,
// or to made once latest holds none. A row holding them reaches the caller's latest calls without drawing again.
function callsAt(latest: { current: EpicCalls | undefined }, made: EpicCalls): EpicCalls {
  const current = (): EpicCalls => latest.current ?? made;
  return {
    get: (id) => current().get(id),
    pause: (id) => current().pause(id),
    resume: (id) => current().resume(id),
    answer: (id, question, choice, text) => current().answer(id, question, choice, text),
    openEpic: (id) => current().openEpic(id),
    openStep: (epic, step) => current().openStep(epic, step),
    stepResult: (id) => current().stepResult(id),
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
function memo<P extends object>(component: (props: P) => JSX.Element, same: (previous: P, next: P) => boolean) {
  return class Memoed extends Component<P> {
    shouldComponentUpdate(next: Readonly<P>): boolean {
      return !same(this.props, next);
    }

    render(): JSX.Element {
      return createElement(component, this.props);
    }
  };
}

function ItemBody({
  item,
  result,
  running,
  open,
  step,
  onToggleOpen,
  query,
  current,
  epicCalls,
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
  reveal?: CallReveal;
}) {
  switch (item.kind) {
    case "prompt": {
      if (item.sender === "event") {
        const stepEnd = stepEventOf(item.text);
        if (stepEnd !== null) {
          return <StepCard event={stepEnd} calls={epicCalls} />;
        }
      }
      if (item.sender === "cron") {
        return (
          <div class="cron">
            <small>CRON</small>
            <ReplyMarkdown text={item.text} />
          </div>
        );
      }
      if (epicCalls !== undefined) {
        const event = epicEventOf(item.text);
        if (event !== null) {
          return <EventRing event={event} calls={epicCalls} />;
        }
      }
      if (item.sender === "event") {
        return (
          <div class="event">
            <small>EVENT</small>
            <ReplyMarkdown text={item.text} />
          </div>
        );
      }
      if (step !== undefined && !ownersTurn(item, step)) {
        return <StepPrompt item={item} checks={step.checks} query={query} current={current} />;
      }
      return (
        <div class="you">
          <small>YOU</small>
          <ReplyMarkdown text={item.text} query={query} current={current} />
        </div>
      );
    }
    case "tool":
    case "question": {
      // An epic call whose result is not an error draws its epic's card in place of its row, when the reader holds
      // epic calls.
      if (item.epic !== "" && epicCalls !== undefined && result !== undefined && !result.error) {
        return <EpicWidget epic={item.epic} calls={epicCalls} />;
      }
      return (
        <CallRow item={item} result={result} running={running} query={query} current={current} reveal={reveal} />
      );
    }
    case "notice":
      return (
        <div class={item.error ? "notice err" : "notice"}>
          <span class="notice-kind">
            <MarkedText text={item.notice} query={query} current={partCurrent(current, 0, item.notice.length)} />
          </span>
          {item.text !== "" && (
            <ReplyText
              text={item.text}
              query={query}
              current={partCurrent(current, item.notice.length + 1, item.text.length)}
            />
          )}
        </div>
      );
    case "compaction":
      return <Compaction item={item} open={open} onToggleOpen={onToggleOpen} />;
    case "interrupted":
      return <div class="rule interrupted">Interrupted by you</div>;
    case "agent_finished":
      return <div class="rule agent-finished">{agentFinishedLine(item)}</div>;
    default:
      return <ReplyMarkdown text={item.text} query={query} current={current} />;
  }
}

// partCurrent is the offset within a part of an item's searched text where the current match starts, when it starts in
// that part, and -1 when it does not. The part starts at start in the item's searched text, where findMatches joins the
// item's parts one to a line, so the part after a newline starts one past the part before it.
function partCurrent(current: number, start: number, length: number): number {
  return current >= start && current < start + length ? current - start : -1;
}

// revealOf is the reveal a row of item is given: the reveal when item is the tool call it names, and none for any other
// item.
function revealOf(item: ConversationItem, reveal: CallReveal | undefined): CallReveal | undefined {
  return reveal !== undefined && item.kind === "tool" && item.id === reveal.id ? reveal : undefined;
}

// agentFinishedLine is the line an agent_finished item draws: its description after Agent finished, with failed or
// stopped added when the agent did not finish. An agent that is done names no status.
function agentFinishedLine(item: ConversationItem): string {
  const ending = item.notice === "failed" || item.notice === "stopped" ? ` (${item.notice})` : "";
  return `Agent finished: ${item.text}${ending}`;
}

// ownersTurn is whether an item is the owner's turn, YOU: a prompt the owner sent, or any prompt without a step that
// neither an event nor a cron sent. Its row carries the owner-turn class, and its body draws as YOU.
function ownersTurn(item: ConversationItem, step?: EpicStep): boolean {
  return (
    item.kind === "prompt" &&
    item.sender !== "event" &&
    item.sender !== "cron" &&
    (step === undefined || item.sender === "owner")
  );
}

// ReplyMarkdown draws a reply's text as Markdown inside its .reply element, the element the reader's Markdown styles
// hang from. Its query and current are the item's, as ItemRow's are, and its text is all of the item's searched text.
function ReplyMarkdown({ text, query = "", current = -1 }: { text: string; query?: string; current?: number }) {
  return (
    <div class="reply">
      <Markdown text={text} query={query} current={current} />
    </div>
  );
}

// StepPrompt draws a prompt a step's engine sent, by its sender and round: the brief as a fold, a send-back of a check
// round as a card of that round's checks, and any other prompt, unmarked ones included, as an engine fold.
function StepPrompt({
  item,
  checks,
  query,
  current,
}: {
  item: ConversationItem;
  checks: EpicStepCheck[];
  query: string;
  current: number;
}) {
  if (item.sender === "brief") {
    return (
      <Fold
        className="fold brief"
        title="Brief"
        names="from the engine"
        text={item.text}
        query={query}
        current={current}
      />
    );
  }
  if (item.sender === "engine" && item.round >= 1) {
    return <CheckCard text={item.text} round={item.round} checks={checks} />;
  }
  const [first] = item.text.split("\n");
  return (
    <Fold className="fold engine" title="Engine" names={first} text={item.text} query={query} current={current} />
  );
}

// Fold draws a line of a step's prompt: its title and names on the line, and a click shows the whole text under it. The
// line is drawn as written and is not marked. The fold's text is marked, and the fold opens when the current match is in
// that text.
function Fold({
  className,
  title,
  names,
  text,
  query,
  current,
}: {
  className: string;
  title: string;
  names: string;
  text: string;
  query: string;
  current: number;
}) {
  return (
    <Disclosure
      className={className}
      label={<><b>{title}</b><span class="names">{names}</span></>}
      revealed={current >= 0}
    >
      <div class="fold-text">
        <ReplyMarkdown text={text} query={query} current={current} />
      </div>
    </Disclosure>
  );
}

// CheckCard draws a send-back of a check round: its checks of that round as chips, in the step's order, under a cap
// naming the round. Its edge is bad when one of those checks failed. The text's first line is the note; a text of more
// lines folds the rest under a button that shows it.
function CheckCard({ text, round, checks }: { text: string; round: number; checks: EpicStepCheck[] }) {
  const roundChecks = checks.filter((check) => check.round === round);
  const failed = roundChecks.some((check) => !check.ok);
  const [first, ...rest] = text.split("\n");
  return (
    <div class={failed ? "card bad" : "card ok"}>
      <div class="cap">
        Checks · round {round}
        <span>from the engine</span>
      </div>
      <div class="chips">
        {roundChecks.map((check, index) => (
          <span key={index} class={check.ok ? "chip" : "chip bad"}>
            {`${check.name} ${check.ok ? "✓" : "✗"}`}
          </span>
        ))}
      </div>
      {rest.length === 0 ? (
        <p class="note">
          <ReplyText text={first} />
        </p>
      ) : (
        <Disclosure className="note" label={first}>
          <div class="rest">
            <ReplyText text={rest.join("\n")} />
          </div>
        </Disclosure>
      )}
    </div>
  );
}

// useReveal opens a fold when revealed turns true, which is when a find's current match moves into it. The owner can
// close the fold after that, and it opens again only when revealed turns true once more. The fold opens in a layout
// effect, so the current mark it holds mounts in the render that follows and scrolls itself into view.
function useReveal(revealed: boolean, setShown: (shown: boolean) => void): void {
  useLayoutEffect(() => {
    if (revealed) {
      setShown(true);
    }
  }, [revealed]);
}

// Disclosure is a button that flashes on each use and shows its children under it: a click shows them, and a second
// click folds them again. Its label is drawn as text, so a link never sits inside it. A revealed disclosure opens, as
// useReveal says.
function Disclosure({
  className,
  label,
  revealed = false,
  children,
}: {
  className: string;
  label: ComponentChildren;
  revealed?: boolean;
  children: ComponentChildren;
}) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
  useReveal(revealed, setShown);
  return (
    <>
      <button
        type="button"
        class={flashing ? `${className} flash` : className}
        onClick={() => {
          flash();
          setShown((value) => !value);
        }}
      >
        {label}
      </button>
      {shown && children}
    </>
  );
}

// CallRow draws a tool or question item as one line of its tool and its input or question. A call with a result is a
// button: a click shows the result under the line, and a second click folds it again. A failed result marks the line
// with ✗. A running call's line carries the running class, which draws its rail, and the running mark. The result
// opens when the current match moves into it, and then its match is marked. A reveal the reader gives opens the result
// too, and brings the line into view. query and current are as ItemRow's are.
function CallRow({
  item,
  result,
  running,
  query,
  current,
  reveal,
}: {
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  query: string;
  current: number;
  reveal?: CallReveal;
}) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
  const element = useRef<HTMLElement>(null);
  const attach = (node: HTMLElement | null): void => {
    element.current = node;
  };
  const called = item.kind === "question" ? item.text : item.input;
  const resultCurrent = result === undefined ? -1 : partCurrent(current, called.length + 1, result.text.length);
  useReveal(resultCurrent >= 0, setShown);
  useLayoutEffect(() => {
    if (reveal !== undefined) {
      setShown(true);
      element.current?.scrollIntoView({ block: "nearest" });
      reveal.done();
    }
  }, [reveal]);
  const className = `call${running ? " running" : ""}${flashing ? " flash" : ""}`;
  const line = (
    <>
      <b>{item.tool}</b>
      <span class="in">
        <MarkedText text={called} query={query} current={partCurrent(current, 0, called.length)} />
      </span>
      {result?.error && <span class="fail">✗</span>}
      {running && <span class="running-mark">running</span>}
    </>
  );
  if (result === undefined) {
    return (
      <div class={className} ref={attach}>
        {line}
      </div>
    );
  }
  return (
    <>
      <button
        type="button"
        class={className}
        ref={attach}
        onClick={() => {
          flash();
          setShown((value) => !value);
        }}
      >
        {line}
      </button>
      {shown && (
        <div class={result.error ? "result err" : "result"}>
          <MarkedText text={result.text} query={query} current={resultCurrent} />
        </div>
      )}
    </>
  );
}

type RunCall = {
  key: string;
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  current: number;
  reveal?: CallReveal;
};

// ToolRun draws a run of three or more tool calls as one line: its count, the tools with their counts, and a trail of
// one dot per call in call order. Each dot carries its call's status: ok, failed, running, or none for a call with no
// result that is not running. A run holding a running call carries the running class and mark too. A click shows a
// line per call under it, each folding its own result, and a second click folds them again. The run opens when the
// current match moves into one of its calls.
function ToolRun({ calls, query }: { calls: RunCall[]; query: string }) {
  const [shown, setShown] = useState(false);
  const running = calls.some((call) => call.running);
  useReveal(calls.some((call) => call.current >= 0), setShown);
  // A reveal of a call in the run opens the run, so the call's line mounts and brings itself into view.
  const reveal = calls.find((call) => call.reveal !== undefined)?.reveal;
  useLayoutEffect(() => {
    if (reveal !== undefined) {
      setShown(true);
    }
  }, [reveal]);
  return (
    <div class="t">
      <span class="gut" />
      <div class="body">
        <button
          type="button"
          class={running ? "fold calls running" : "fold calls"}
          onClick={() => setShown((value) => !value)}
        >
          <b>{`${calls.length} tool calls`}</b>
          <span class="names">{toolCounts(calls)}</span>
          {running && <span class="running-mark">running</span>}
          <span class="trail">
            {calls.map((call) => (
              <span key={call.key} class={dotClass(call)} />
            ))}
          </span>
        </button>
        {shown && (
          <div class="calls">
            {calls.map((call) => (
              <CallRow
                key={call.key}
                item={call.item}
                result={call.result}
                running={call.running}
                query={query}
                current={call.current}
                reveal={call.reveal}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// MemoToolRun draws a ToolRun again only when its find query or its calls change: sameCalls compares them by content.
const MemoToolRun = memo(
  ToolRun,
  (previous, next) => previous.query === next.query && sameCalls(previous.calls, next.calls),
);

// sameCalls is whether two runs hold the same calls in the same order: each call has the same key, item, result,
// running state, current match and reveal. A run's calls array is rebuilt on each render, so the calls are what is
// compared, not the array.
function sameCalls(previous: RunCall[], next: RunCall[]): boolean {
  return (
    previous.length === next.length &&
    previous.every((call, index) => {
      const other = next[index];
      return (
        other !== undefined &&
        call.key === other.key &&
        call.item === other.item &&
        call.result === other.result &&
        call.running === other.running &&
        call.current === other.current &&
        call.reveal === other.reveal
      );
    })
  );
}

// toolCounts names each tool of a run once, in first-seen order, with its count when it is more than one.
function toolCounts(calls: RunCall[]): string {
  const counts = new Map<string, number>();
  for (const call of calls) {
    counts.set(call.item.tool, (counts.get(call.item.tool) ?? 0) + 1);
  }
  return [...counts].map(([tool, count]) => (count > 1 ? `${tool} ×${count}` : tool)).join(", ");
}

// dotClass is the class of a call's dot in a run's trail: running while the call is running, failed when its result
// failed, ok when it succeeded, and none when it has no result and is not running.
function dotClass(call: RunCall): string {
  if (call.running) {
    return "dot running";
  }
  if (call.result === undefined) {
    return "dot none";
  }
  return call.result.error === true ? "dot failed" : "dot ok";
}

// LiveRow draws one unfinished block: a text block as it streams, a thinking block as a folded row.
function LiveRow({ block }: { block: EngineText }) {
  if (block.kind === "thinking") {
    return <ThinkingRow text={block.text} />;
  }
  return (
    <div class="t live-row">
      <span class="gut live">live</span>
      <div class="body">
        <ReplyMarkdown text={block.text} />
        <span class="caret soft" />
      </div>
    </div>
  );
}

// ThinkingRow is the live row of a thinking block, reading thinking. A click shows the block's text under the row, and
// a second click folds it again. Its state lives in the row, keyed by the block, so it stays open as the deltas grow.
function ThinkingRow({ text }: { text: string }) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
  return (
    <div class="t live-row">
      <span class="gut live">live</span>
      <div class="body">
        <button
          type="button"
          class={flashing ? "thinking flash" : "thinking"}
          onClick={() => {
            flash();
            setShown((value) => !value);
          }}
        >
          thinking
        </button>
        {shown && <div class="thinking-text">{text}</div>}
      </div>
    </div>
  );
}

function Compaction({
  item,
  open,
  onToggleOpen,
}: {
  item: ConversationItem;
  open: boolean;
  onToggleOpen?: () => void;
}) {
  const parts = ["compacted"];
  if (item.trigger !== "") {
    parts.push(item.trigger);
  }
  if (item.tokens > 0n) {
    parts.push(`${Math.round(Number(item.tokens) / tokensPerThousand)}k`);
  }
  const rule = parts.join(" · ");
  const [flashing, flash] = useFlash();
  return (
    <div class={open ? "compaction open" : "compaction"}>
      {onToggleOpen ? (
        <button
          type="button"
          class={flashing ? "rule flash" : "rule"}
          onClick={() => {
            flash();
            onToggleOpen();
          }}
        >
          {rule}
        </button>
      ) : (
        <div class="rule">{rule}</div>
      )}
      {open && (
        <div class="summary">
          <ReplyText text={item.text} />
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
function groupRows(items: ConversationItem[], newIndex: number): number[][] {
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
  const foldable = (item: ConversationItem) => item.kind === "tool" && item.epic === "";
  items.forEach((item, index) => {
    if (item.kind === "result") {
      return;
    }
    if (index === newIndex || !foldable(item)) {
      endRun();
    }
    if (foldable(item)) {
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

// clockFormat is the formatter of every gutter time: 24-hour hours and minutes in the local zone. One formatter serves
// all rows, since a formatter built per call costs far more than the format it makes.
const clockFormat = new Intl.DateTimeFormat([], { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

export function clock(time: bigint): string {
  if (time === 0n) {
    return "";
  }
  return clockFormat.format(new Date(Number(time) * secondsToMillis));
}
