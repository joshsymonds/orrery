import type { Conversation } from "./conversation";
import { type ArchiveControl, RowMenu } from "./epicline";
import { rowNeed } from "./needs";
import { type ParsedQuestion, parseQuestion } from "./question";
import type { Row } from "./rows";
import { ReplyText } from "./text";
import { TitleParts } from "./titleparts";

export const stateWaiting = "waiting";

// The keys 1-3 answer the first three options of a question, and no others.
const keyedOptions = 3;

const secondsPerMinute = 60;
const minutesPerHour = 60;
const hoursPerDay = 24;

// A landed answer from the list: the choice and its label, kept for the row and the question it answered.
export interface Answer {
  question: string;
  choice: number;
  label: string;
}

// InboxRow draws one row of the inbox list: its dot and glyph, its line with type, title, age and ⋯ menu, and its
// second line with the reply text and, for a waiting row, the answer or the options. A click calls onOpen and a
// double-click calls onSeen, each unless the press landed on a link. cursor is whether the row is the cursor's.
export function InboxRow({
  row,
  now,
  cursor,
  archive,
  conversation,
  answer,
  onOpen,
  onSeen,
}: {
  row: Row;
  now: number;
  cursor: boolean;
  archive: ArchiveControl;
  conversation: Conversation | null;
  answer?: Answer;
  onOpen: () => void;
  onSeen: () => void;
}) {
  return (
    <div
      class={cursor ? "b-row cur" : "b-row"}
      tabIndex={-1}
      data-row=""
      data-key={row.key}
      data-unseen={row.unseen ? "" : undefined}
      aria-current={cursor ? "true" : undefined}
      onClick={(event) => {
        if (!isLinkClick(event)) {
          onOpen();
        }
      }}
      onDblClick={(event) => {
        if (!isLinkClick(event)) {
          onSeen();
        }
      }}
    >
      <span class="dot" />
      <span class={`g g-${row.state}`} aria-hidden="true" />
      <div class="main">
        <div class="l1">
          {row.state === stateWaiting && <span class="ty">{row.question !== "" ? "QUESTION" : "WAITING"}</span>}
          <TitleParts dir={row.project} line={row.label} />
          <span class="age">{ageText(row.changed, now)}</span>
          {row.archiveKind !== "none" && <RowMenu control={archive} />}
        </div>
        <div class={row.unseen ? "l2" : "l2 one"}>
          <ReplyText text={replyText(row)} />
        </div>
        <RowOptions row={row} conversation={conversation} answer={answer} />
      </div>
    </div>
  );
}

// readerQuestion is the question the cursor's reader draws: a waiting pane row's stored question, or a session's open
// request as its stream holds it. A row with no question draws none.
export function readerQuestion(row: Row, conversation: Conversation | null): ParsedQuestion | null {
  if (row.kind === "pane") {
    return row.state === stateWaiting && row.question !== "" ? parseQuestion(row.question) : null;
  }
  const request = conversation?.state?.request;
  if (request === undefined) {
    return null;
  }
  return { title: request.question, options: request.options.map((label) => ({ label, description: "" })) };
}

// RowOptions draws a waiting row's line under its text: the answer a landed answer left, or else the options its keys
// answer as chips. Chips are labels, not buttons. A row that is not waiting draws neither.
function RowOptions({ row, conversation, answer }: { row: Row; conversation: Conversation | null; answer?: Answer }) {
  if (row.state !== stateWaiting) {
    return null;
  }
  if (answer !== undefined && answer.question === row.question) {
    return <div class="ans">{`You answered ${answer.choice} · ${answer.label}`}</div>;
  }
  const options = readerQuestion(row, conversation)?.options ?? [];
  if (options.length === 0) {
    return null;
  }
  return (
    <div class="opts">
      {options.map((option, index) => {
        const keyed = index < keyedOptions;
        return (
          <span class="opt" key={index}>
            {keyed && <kbd>{index + 1}</kbd>}
            {keyed ? optionText(option.label, index + 1) : option.label}
          </span>
        );
      })}
    </div>
  );
}

// optionText is an option's label without the number of its own key cap, when the label starts with that number. A
// label without it is named as it is.
export function optionText(label: string, choice: number): string {
  const numbered = `${choice} `;
  return label.startsWith(numbered) ? label.slice(numbered.length) : label;
}

// replyText is a row's second line: the sentence of a waiting row, else a session's summary. A pane's summary is its
// title's line, so a pane that is not waiting has no second line.
function replyText(row: Row): string {
  if (row.state === stateWaiting) {
    return rowNeed(row).text;
  }
  return row.kind === "session" ? row.summary : "";
}

// isLinkClick reports whether a click or double click landed on a link, which opens it and moves nothing else.
export function isLinkClick(event: Event): boolean {
  return event.target instanceof Element && event.target.closest("a") !== null;
}

function ageText(changed: number, now: number): string {
  if (changed <= 0) {
    return "";
  }
  const seconds = Math.max(0, now - changed);
  if (seconds < secondsPerMinute) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / secondsPerMinute);
  if (minutes < minutesPerHour) {
    return `${minutes}m`;
  }
  const hours = Math.floor(minutes / minutesPerHour);
  if (hours < hoursPerDay) {
    return `${hours}h`;
  }
  return `${Math.floor(hours / hoursPerDay)}d`;
}
