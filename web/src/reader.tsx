import type { JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import type { Conversation } from "./conversation";
import { useFlash } from "./flash";
import type { ConversationItem, WaitingMessage } from "./gen/orrery/v1/orrery_pb";
import type { ParsedQuestion } from "./question";
import { ReplyText } from "./text";

const tokensPerThousand = 1000;
const secondsToMillis = 1000;
const answerChoices = [1, 2, 3] as const;
const nearBottomPixels = 4;

// Reader draws one conversation: its items, the "since you looked" rule before the first item newer than seenAt, the
// pane's question with a button per answer, the session's unfinished text, and the queue of waiting messages. Every
// text is drawn as text: through ReplyText, or as a plain child of a button, so no markup becomes an element. The
// reader is its own scroll container: a conversation that replaces a null one opens at its bottom, and any other
// render keeps the bottom only for a reader that sat within nearBottomPixels of it.
export function Reader({
  conversation,
  seenAt,
  open,
  question = null,
  onAnswer,
  onToggleOpen,
}: {
  conversation: Conversation | null;
  seenAt: number;
  open: boolean;
  question?: ParsedQuestion | null;
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
  const newIndex = conversation.items.findIndex((item) => Number(item.time) > seenAt);
  const keys = itemKeys(conversation.items);
  const rows: JSX.Element[] = [];
  conversation.items.forEach((item, index) => {
    if (index === newIndex) {
      rows.push(<NewRule key="new-rule" />);
    }
    rows.push(<ItemRow key={keys[index]} item={item} open={open} onToggleOpen={onToggleOpen} />);
  });
  const questionCard = question === null ? null : <QuestionCard question={question} onAnswer={onAnswer} />;
  const live = conversation.unfinished
    .filter((block) => block.kind === "text")
    .map((block, index) => (
      <div class="t live-row" key={`live-${index}`}>
        <span class="gut live">live</span>
        <div class="body">
          <ReplyText text={block.text} />
          <span class="caret soft" />
        </div>
      </div>
    ));
  const waiting = conversation.state?.waiting ?? [];
  return (
    <div class="reader" ref={root}>
      {rows}
      {questionCard}
      {live}
      {waiting.map((message, index) => (
        <QueuedRow key={`queued-${index}`} message={message} />
      ))}
    </div>
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
  open,
  onToggleOpen,
}: {
  item: ConversationItem;
  open: boolean;
  onToggleOpen?: () => void;
}) {
  return (
    <div class="t">
      <span class="gut">{clock(item.time)}</span>
      <div class="body">
        <ItemBody item={item} open={open} onToggleOpen={onToggleOpen} />
      </div>
    </div>
  );
}

function ItemBody({
  item,
  open,
  onToggleOpen,
}: {
  item: ConversationItem;
  open: boolean;
  onToggleOpen?: () => void;
}) {
  switch (item.kind) {
    case "prompt":
      return (
        <div class="you">
          <small>YOU</small>
          <ReplyText text={item.text} />
        </div>
      );
    case "tool":
      return (
        <div class="call">
          <b>{item.tool}</b>
          <span class="in">
            <ReplyText text={item.input} />
          </span>
        </div>
      );
    case "question":
      return (
        <div class="question">
          <b>{item.tool}</b>
          <ReplyText text={item.text} />
        </div>
      );
    case "result":
      return (
        <div class={item.error ? "result err" : "result"}>
          <ReplyText text={item.text} />
        </div>
      );
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
      return <ReplyText text={item.text} />;
  }
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

function QueuedRow({ message }: { message: WaitingMessage }) {
  return (
    <div class={message.canceled ? "t queued canceled" : "t queued"}>
      <span class="gut" />
      <div class="body">
        <span class="tag">{message.canceled ? "canceled" : "waiting"}</span>
        <ReplyText text={message.text} />
      </div>
    </div>
  );
}

// distanceFromBottom is how far the element's scroll sits above its bottom.
function distanceFromBottom(element: HTMLElement): number {
  return element.scrollHeight - element.scrollTop - element.clientHeight;
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
