import type { JSX } from "preact";

import type { Conversation } from "./conversation";
import type { ConversationItem, WaitingMessage } from "./gen/orrery/v1/orrery_pb";
import type { ParsedQuestion } from "./question";
import { ReplyText } from "./text";

const tokensPerThousand = 1000;
const secondsToMillis = 1000;
const answerChoices = [1, 2, 3] as const;

// Reader draws one conversation: its items, the "since you looked" rule before the first item newer than seenAt, the
// pane's question with a button per answer, the session's unfinished text, and the queue of waiting messages. Every
// text is drawn as text: through ReplyText, or as a plain child of a button, so no markup becomes an element.
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
  if (conversation === null) {
    return (
      <div class="reader">
        <p class="reader-loading">loading…</p>
      </div>
    );
  }
  const newIndex = conversation.items.findIndex((item) => Number(item.time) > seenAt);
  const rows: JSX.Element[] = [];
  conversation.items.forEach((item, index) => {
    if (index === newIndex) {
      rows.push(<NewRule key="new-rule" />);
    }
    rows.push(<ItemRow key={index} item={item} open={open} onToggleOpen={onToggleOpen} />);
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
    <div class="reader">
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
          return (
            <button type="button" class="option" key={index} onClick={() => onAnswer?.(choice)}>
              {label}
            </button>
          );
        })}
      </div>
    </div>
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
  return (
    <div class={open ? "compaction open" : "compaction"}>
      {onToggleOpen ? (
        <button type="button" class="rule" onClick={() => onToggleOpen()}>
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

function clock(time: bigint): string {
  if (time === 0n) {
    return "";
  }
  return new Date(Number(time) * secondsToMillis).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
