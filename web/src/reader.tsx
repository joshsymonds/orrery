import type { ComponentChildren, JSX } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import type { Conversation } from "./conversation";
import type { Match } from "./find";
import { useFlash } from "./flash";
import type { EpicStep, EpicStepCheck } from "./gen/orrery/v1/epics_pb";
import type { ConversationItem, EngineText } from "./gen/orrery/v1/orrery_pb";
import { Markdown } from "./markdown";
import type { ParsedQuestion } from "./question";
import type { Sending } from "./sending";
import { MarkedText, ReplyText } from "./text";
import { TitleRow } from "./titlerow";

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
const pagingQuery = "(min-width: 760px)";

// ReaderHandle is what the page calls on a reader to page its transcript by a screen. page moves the reader one screen
// up or down, less pageOverlapPixels, clamped to its top and bottom, and returns true. Where the reader does not scroll
// on its own, under pagingQuery, page does nothing and returns false.
export interface ReaderHandle {
  page: (direction: "up" | "down") => boolean;
}

// Reader draws one conversation: its items, the "since you looked" rule before the first row newer than seenAt, the
// pane's question with a button per answer, and the session's unfinished text and thinking. A result is never a row:
// it folds under the call it answers, and a result whose call is not held is hidden. Three or more tool calls in a row
// fold into one run line that opens to a line per call. A call still running, with nothing but calls and results after
// it and no question drawn, carries the running rail and mark, and so does a run that holds one.
// A reply, a prompt and live text draw through Markdown, which builds only its fixed elements. Every other text is drawn
// as text: through ReplyText, or as a plain child of an element, so no markup becomes an element and no link sits inside
// a button. A prompt an event sent draws as an EVENT row, with or without a step, and never as YOU or a step prompt.
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
// call's input and result, and a notice. Compaction, check cards, live text, thinking, the question card, sending rows
// and event lines are not searched. The current match, find.current's index in find.matches, is the mark with class
// current: it is scrolled into view as it arrives, and the fold, call or run that holds it opens. A fold opens when the
// current match moves into it, and the owner can close it; it opens again only when the current match moves into it
// once more.
// The sending entries draw after the items and live text, each as a dimmed prompt row marked sending. They are not items:
// they keep no key and no place in the scroll place, and a reader at its bottom keeps it as a new row arrives.
export function Reader({
  conversation,
  seenAt,
  open,
  question = null,
  step,
  onAnswer,
  onToggleOpen,
  onOlder,
  register,
  sending = [],
  find,
}: {
  conversation: Conversation | null;
  seenAt: number;
  open: boolean;
  question?: ParsedQuestion | null;
  step?: EpicStep;
  onAnswer?: (choice: 1 | 2 | 3) => void;
  onToggleOpen?: () => void;
  onOlder?: () => void;
  register?: (handle: ReaderHandle | null) => void; // gets the handle while mounted, and null once unmounted
  sending?: Sending[];
  find?: { query: string; matches: Match[]; current: number };
}): JSX.Element {
  const root = useRef<HTMLDivElement>(null);
  const drewConversation = useRef(false);
  const shownKeys = useRef<string[]>([]);
  // pinned is whether the owner last left the reader within nearBottomPixels of its bottom. Only the owner's scrolls set
  // it: a render or a size change moves the content under the reader without the owner, so it does not.
  const pinned = useRef(true);
  const keys = conversation === null ? [] : itemKeys(conversation.items);
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
      pinned.current = distanceFromBottom(reader) <= nearBottomPixels;
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
  const sendingRows = sending.map((entry) => <SendingRow key={entry.promptId} text={entry.text} />);
  if (conversation === null) {
    return (
      <div class="reader" ref={root}>
        <p class="reader-loading">loading…</p>
        {sendingRows}
      </div>
    );
  }
  const newIndex = conversation.items.findIndex((item) => item.kind !== "result" && Number(item.time) > seenAt);
  const results = resultsByCall(conversation.items);
  const items = conversation.items;
  const liveBlocks = conversation.unfinished.filter((block) => block.kind === "text" || block.kind === "thinking");
  // A question or live text drawn after the items ends the calls, so none of them is running.
  const running = runningCalls(items, results, question !== null || liveBlocks.length > 0);
  const rows: JSX.Element[] = [];
  groupRows(items, newIndex).forEach((group) => {
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
      }));
      rows.push(<ToolRun key={keys[first]} calls={calls} query={query} />);
      return;
    }
    rows.push(
      <ItemRow
        key={keys[first]}
        item={items[first]}
        result={results.get(first)}
        running={running.has(first)}
        open={open}
        step={step}
        onToggleOpen={onToggleOpen}
        query={query}
        current={currentAt(first)}
      />,
    );
  });
  const questionCard = question === null ? null : <QuestionCard question={question} onAnswer={onAnswer} />;
  const live = liveBlocks.map((block) => <LiveRow key={`${block.messageId}/${block.index}`} block={block} />);
  return (
    <div class="reader" ref={root} onScroll={followScroll}>
      {rows}
      {questionCard}
      {live}
      {sendingRows}
    </div>
  );
}

// ReaderHead names the conversation the reader draws and what it is doing: its label as the heading, its where line
// when there is one, and the children after it, the pill the app passes in.
export function ReaderHead({
  label,
  where,
  children,
}: {
  label: string;
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

// ConversationHead heads a row's conversation with the shared title row: the back button, the crumbs orrery › the row's
// label, and the row's state in the state slot. The children, the pill the app passes in, draw after the state.
export function ConversationHead({
  label,
  state,
  onBack,
  onGoList,
  children,
}: {
  label: string;
  state: string;
  onBack: () => void;
  onGoList: () => void;
  children?: ComponentChildren;
}): JSX.Element {
  return (
    <TitleRow crumbs={[{ label: "orrery", onGo: onGoList }, { label }]} onBack={onBack} state={state}>
      {children}
    </TitleRow>
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

// ItemRow draws an item's row. query is the find's query, and current is the offset in the item's searched text where
// the current match starts, or -1 when the current match is not in this item.
function ItemRow({
  item,
  result,
  running,
  open,
  step,
  onToggleOpen,
  query,
  current,
}: {
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  open: boolean;
  step?: EpicStep;
  onToggleOpen?: () => void;
  query: string;
  current: number;
}) {
  // Its row carries the owner-turn class, which sets the gap above it.
  return (
    <div class={ownersTurn(item, step) ? "t owner-turn" : "t"}>
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
        />
      </div>
    </div>
  );
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
}: {
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  open: boolean;
  step?: EpicStep;
  onToggleOpen?: () => void;
  query: string;
  current: number;
}) {
  switch (item.kind) {
    case "prompt":
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
    case "tool":
    case "question":
      return <CallRow item={item} result={result} running={running} query={query} current={current} />;
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

// ownersTurn is whether an item is the owner's turn, YOU: a prompt the owner sent, or any prompt without a step that
// an event did not send. Its row carries the owner-turn class, and its body draws as YOU.
function ownersTurn(item: ConversationItem, step?: EpicStep): boolean {
  return item.kind === "prompt" && item.sender !== "event" && (step === undefined || item.sender === "owner");
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
// opens when the current match moves into it, and then its match is marked. query and current are as ItemRow's are.
function CallRow({
  item,
  result,
  running,
  query,
  current,
}: {
  item: ConversationItem;
  result: ConversationItem | undefined;
  running: boolean;
  query: string;
  current: number;
}) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
  const called = item.kind === "question" ? item.text : item.input;
  const resultCurrent = result === undefined ? -1 : partCurrent(current, called.length + 1, result.text.length);
  useReveal(resultCurrent >= 0, setShown);
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
    return <div class={className}>{line}</div>;
  }
  return (
    <>
      <button
        type="button"
        class={className}
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
              />
            ))}
          </div>
        )}
      </div>
    </div>
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
// question or any other item ends a run, and so does the since-you-looked rule, which starts the row at newIndex.
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
  items.forEach((item, index) => {
    if (item.kind === "result") {
      return;
    }
    if (index === newIndex || item.kind !== "tool") {
      endRun();
    }
    if (item.kind === "tool") {
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

export function clock(time: bigint): string {
  if (time === 0n) {
    return "";
  }
  return new Date(Number(time) * secondsToMillis).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  });
}
