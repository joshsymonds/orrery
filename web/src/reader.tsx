import type { ComponentChildren, JSX } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import type { Conversation } from "./conversation";
import { useFlash } from "./flash";
import type { EpicStep, EpicStepCheck } from "./gen/orrery/v1/epics_pb";
import type { ConversationItem, EngineText } from "./gen/orrery/v1/orrery_pb";
import { Markdown } from "./markdown";
import type { ParsedQuestion } from "./question";
import { ReplyText } from "./text";

const tokensPerThousand = 1000;
const secondsToMillis = 1000;
const answerChoices = [1, 2, 3] as const;
const nearBottomPixels = 4;
const toolRunMinimum = 3;

// Reader draws one conversation: its items, the "since you looked" rule before the first row newer than seenAt, the
// pane's question with a button per answer, and the session's unfinished text and thinking. A result is never a row:
// it folds under the call it answers, and a result whose call is not held is hidden. Three or more tool calls in a row
// fold into one run line that opens to a line per call.
// A reply, a prompt and live text draw through Markdown, which builds only its fixed elements. Every other text is drawn
// as text: through ReplyText, or as a plain child of an element, so no markup becomes an element and no link sits inside
// a button. With a step, a prompt draws by its sender and round: the brief as a fold, a send-back as a card of its
// round's checks, any other prompt as an engine fold. An owner's prompt, and every prompt without a step, is YOU.
// The reader is its own scroll container: a conversation that replaces a null one opens at its bottom, and any
// other render keeps the bottom only for a reader that sat within nearBottomPixels of it.
export function Reader({
  conversation,
  seenAt,
  open,
  question = null,
  step,
  onAnswer,
  onToggleOpen,
}: {
  conversation: Conversation | null;
  seenAt: number;
  open: boolean;
  question?: ParsedQuestion | null;
  step?: EpicStep;
  onAnswer?: (choice: 1 | 2 | 3) => void;
  onToggleOpen?: () => void;
}): JSX.Element {
  const root = useRef<HTMLDivElement>(null);
  const drewConversation = useRef(false);
  // The position is read before this render changes the DOM, so it is where the reader sat before the new items.
  const element = root.current;
  const atBottom = element !== null && distanceFromBottom(element) <= nearBottomPixels;
  const opening = conversation !== null && !drewConversation.current;
  useLayoutEffect(() => {
    drewConversation.current = conversation !== null;
    const reader = root.current;
    if (reader !== null && (opening || atBottom)) {
      reader.scrollTop = reader.scrollHeight;
    }
  });
  if (conversation === null) {
    return (
      <div class="reader" ref={root}>
        <p class="reader-loading">loading…</p>
      </div>
    );
  }
  const newIndex = conversation.items.findIndex((item) => item.kind !== "result" && Number(item.time) > seenAt);
  const keys = itemKeys(conversation.items);
  const results = resultsByCall(conversation.items);
  const items = conversation.items;
  const rows: JSX.Element[] = [];
  groupRows(items, newIndex).forEach((group) => {
    const [first] = group;
    if (first === newIndex) {
      rows.push(<NewRule key="new-rule" />);
    }
    if (group.length >= toolRunMinimum) {
      const calls = group.map((index) => ({ key: keys[index], item: items[index], result: results.get(index) }));
      rows.push(<ToolRun key={keys[first]} calls={calls} />);
      return;
    }
    rows.push(
      <ItemRow
        key={keys[first]}
        item={items[first]}
        result={results.get(first)}
        open={open}
        step={step}
        onToggleOpen={onToggleOpen}
      />,
    );
  });
  const questionCard = question === null ? null : <QuestionCard question={question} onAnswer={onAnswer} />;
  const live = conversation.unfinished
    .filter((block) => block.kind === "text" || block.kind === "thinking")
    .map((block) => <LiveRow key={`${block.messageId}/${block.index}`} block={block} />);
  return (
    <div class="reader" ref={root}>
      {rows}
      {questionCard}
      {live}
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

function ItemRow({
  item,
  result,
  open,
  step,
  onToggleOpen,
}: {
  item: ConversationItem;
  result: ConversationItem | undefined;
  open: boolean;
  step?: EpicStep;
  onToggleOpen?: () => void;
}) {
  return (
    <div class="t">
      <span class="gut">{clock(item.time)}</span>
      <div class="body">
        <ItemBody item={item} result={result} open={open} step={step} onToggleOpen={onToggleOpen} />
      </div>
    </div>
  );
}

function ItemBody({
  item,
  result,
  open,
  step,
  onToggleOpen,
}: {
  item: ConversationItem;
  result: ConversationItem | undefined;
  open: boolean;
  step?: EpicStep;
  onToggleOpen?: () => void;
}) {
  switch (item.kind) {
    case "prompt":
      if (step === undefined || item.sender === "owner") {
        return (
          <div class="you">
            <small>YOU</small>
            <ReplyMarkdown text={item.text} />
          </div>
        );
      }
      return <StepPrompt item={item} checks={step.checks} />;
    case "tool":
    case "question":
      return <CallRow item={item} result={result} />;
    case "notice":
      return (
        <div class={item.error ? "notice err" : "notice"}>
          <span class="notice-kind">{item.notice}</span>
          {item.text !== "" && <ReplyText text={item.text} />}
        </div>
      );
    case "compaction":
      return <Compaction item={item} open={open} onToggleOpen={onToggleOpen} />;
    default:
      return <ReplyMarkdown text={item.text} />;
  }
}

// ReplyMarkdown draws a reply's text as Markdown inside its .reply element, the element the reader's Markdown styles
// hang from.
function ReplyMarkdown({ text }: { text: string }) {
  return (
    <div class="reply">
      <Markdown text={text} />
    </div>
  );
}

// StepPrompt draws a prompt a step's engine sent, by its sender and round: the brief as a fold, a send-back of a check
// round as a card of that round's checks, and any other prompt, unmarked ones included, as an engine fold.
function StepPrompt({ item, checks }: { item: ConversationItem; checks: EpicStepCheck[] }) {
  if (item.sender === "brief") {
    return <Fold className="fold brief" title="Brief" names="from the engine" text={item.text} />;
  }
  if (item.sender === "engine" && item.round >= 1) {
    return <CheckCard text={item.text} round={item.round} checks={checks} />;
  }
  const [first] = item.text.split("\n");
  return <Fold className="fold engine" title="Engine" names={first} text={item.text} />;
}

// Fold draws a line of a step's prompt: its title and names on the line, and a click shows the whole text under it.
function Fold({ className, title, names, text }: { className: string; title: string; names: string; text: string }) {
  return (
    <Disclosure className={className} label={<><b>{title}</b><span class="names">{names}</span></>}>
      <div class="fold-text">
        <ReplyMarkdown text={text} />
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

// Disclosure is a button that flashes on each use and shows its children under it: a click shows them, and a second
// click folds them again. Its label is drawn as text, so a link never sits inside it.
function Disclosure({
  className,
  label,
  children,
}: {
  className: string;
  label: ComponentChildren;
  children: ComponentChildren;
}) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
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
// button: a click shows the result under the line, and a second click folds it again. A failed result marks the line.
function CallRow({ item, result }: { item: ConversationItem; result: ConversationItem | undefined }) {
  const [shown, setShown] = useState(false);
  const [flashing, flash] = useFlash();
  const className = `call${item.kind === "question" ? " question" : ""}${flashing ? " flash" : ""}`;
  const line = (
    <>
      <b>{item.tool}</b>
      <span class="in">{item.kind === "question" ? item.text : item.input}</span>
      {result?.error && <span class="fail">✗</span>}
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
      {shown && <div class={result.error ? "result err" : "result"}>{result.text}</div>}
    </>
  );
}

type RunCall = { key: string; item: ConversationItem; result: ConversationItem | undefined };

// ToolRun draws a run of three or more tool calls as one line: its count, the tools with their counts, and ✗ when any
// call's result failed, else ✓. A click shows a line per call under it, each folding its own result, and a second click
// folds them again.
function ToolRun({ calls }: { calls: RunCall[] }) {
  const [shown, setShown] = useState(false);
  const failed = calls.some((call) => call.result?.error === true);
  return (
    <div class="t">
      <span class="gut" />
      <div class="body">
        <button type="button" class="fold calls" onClick={() => setShown((value) => !value)}>
          <b>{`${calls.length} tool calls`}</b>
          <span class="names">{toolCounts(calls)}</span>
          {failed ? <span class="bad">✗</span> : <span class="ok">✓</span>}
        </button>
        {shown && (
          <div class="calls">
            {calls.map((call) => (
              <CallRow key={call.key} item={call.item} result={call.result} />
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

// itemKeys keys each item by its content, so a row keeps its key while the window drops items from the top. An item
// whose content repeats an older one takes an occurrence suffix counted from the oldest, as the phone's entryKeys does.
function itemKeys(items: ConversationItem[]): string[] {
  const seen = new Map<string, number>();
  return items.map((item) => {
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
    return occurrence === 0 ? base : `${base}#${occurrence}`;
  });
}

function clock(time: bigint): string {
  if (time === 0n) {
    return "";
  }
  return new Date(Number(time) * secondsToMillis).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
