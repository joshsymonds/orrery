import type { JSX } from "preact";
import { useEffect, useRef } from "preact/hooks";

import { checkLines } from "./blocks";
import type { ConversationItem, EngineText } from "./gen/orrery/v1/orrery_pb";
import { epicEventOf, stepEventOf } from "./eventring";
import { Icon } from "./glyphs";
import { drawnText } from "./markdown";
import { occurrences } from "./text";

// Match is one occurrence of a query in a conversation's drawn text: item is the index of the item it is drawn in, and
// start is its offset in that item's searched text. A call's result is searched as part of its call, so a match in the
// result has the call's index. A live reply is not an item: its match's item is the item count plus the live block's
// index among the unfinished blocks, so the matches of the items and of the live replies are told apart by their item.
export interface Match {
  item: number;
  start: number;
}

// findMatches returns the case-insensitive occurrences of query in the drawn text of items, in item order, then those
// of the unfinished text blocks, in their order. A prompt is searched as its row draws it (promptText), a call by its
// input or question and by the text of the result that answers it, a notice by its kind and text, a compaction by its
// summary, an agent-finished item by its name and description, and a live reply as Markdown draws it. Unfinished
// thinking is not searched. An interrupted item, a result that answers no call, and a call that creates an epic and
// whose result is not an error (it draws as the epic's card) are not searched. An empty query matches nothing.
export function findMatches(items: ConversationItem[], query: string, unfinished: EngineText[] = []): Match[] {
  const answers = resultsByCall(items);
  const matches: Match[] = [];
  items.forEach((item, index) => {
    for (const [start] of occurrences(searchedText(item, answers.get(index)), query)) {
      matches.push({ item: index, start });
    }
  });
  unfinished.forEach((block, index) => {
    if (block.kind !== "text") {
      return;
    }
    for (const [start] of occurrences(drawnText(block.text), query)) {
      matches.push({ item: items.length + index, start });
    }
  });
  return matches;
}

// searchedText is the text an item's occurrences are found in, the parts it draws joined one to a line, so a query with
// no line break cannot match across two parts. result is the result that answers the item, when it is a call.
function searchedText(item: ConversationItem, result: ConversationItem | undefined): string {
  switch (item.kind) {
    case "prompt":
      return promptText(item);
    case "tool":
    case "question":
      if (item.epic !== "" && result !== undefined && !result.error) {
        return "";
      }
      return joinParts([item.kind === "question" ? item.text : item.input, result?.text]);
    case "notice":
      return joinParts([item.notice, item.text]);
    case "compaction":
      return item.text;
    case "agent_finished":
      return joinParts([item.tool, item.text]);
    case "interrupted":
    case "result":
      return "";
    default:
      return drawnText(item.text);
  }
}

// promptText is the text a prompt's row searches, as the reader draws the row: a step's end its brief's lines, each as
// Markdown; a cron block and an event block its Markdown; an epic's event its sentence, the rest of its body being
// undrawn; a check round its note and the rest under it, each as Markdown; any other prompt its Markdown. A moved event
// has no sentence, so it is not searched.
function promptText(item: ConversationItem): string {
  if (item.sender === "event") {
    const step = stepEventOf(item.text);
    if (step !== null) {
      return step.brief.map((line) => drawnText(line)).join("\n");
    }
  }
  if (item.sender === "cron") {
    return drawnText(item.text);
  }
  const epic = epicEventOf(item.text);
  if (epic !== null) {
    return epic.sentence;
  }
  if (item.sender === "engine" && item.round >= 1) {
    const { note, rest } = checkLines(item.text);
    return joinParts([drawnText(note), drawnText(rest.join("\n"))]);
  }
  return drawnText(item.text);
}

function joinParts(parts: (string | undefined)[]): string {
  return parts.filter((part) => part !== undefined).join("\n");
}

// resultsByCall pairs each result with the call it answers, as reader.tsx does: a result answers the latest call before
// it with the same id, and a later result for that call replaces an earlier one. The map is keyed by the call's index.
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

// FindBar is the find field over the transcript: a focused input for the query, then the current match as n of N, or
// no matches when a query finds none, and nothing while the query is empty. Enter goes to the next match and
// Shift+Enter to the previous, and Escape or the Close find button closes the bar. The keys the bar handles stop here,
// so the page's keydown handler never sees them.
export function FindBar({
  query,
  matches,
  current,
  onQuery,
  onNext,
  onPrevious,
  onClose,
}: {
  query: string;
  matches: number;
  current: number;
  onQuery: (query: string) => void;
  onNext: () => void;
  onPrevious: () => void;
  onClose: () => void;
}): JSX.Element {
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => {
    field.current?.focus();
  }, []);
  const onKeyDown = (event: KeyboardEvent) => {
    switch (event.key) {
      case "Enter":
        event.stopPropagation();
        if (event.shiftKey) {
          onPrevious();
        } else {
          onNext();
        }
        break;
      case "Escape":
        event.stopPropagation();
        onClose();
        break;
    }
  };
  return (
    <div class="find">
      <Icon name="search" />
      <input
        ref={field}
        type="text"
        value={query}
        placeholder="Find in this conversation"
        onInput={(event) => onQuery(event.currentTarget.value)}
        onKeyDown={onKeyDown}
      />
      {matches > 0 && <span class="count">{`${current + 1} of ${matches}`}</span>}
      {matches === 0 && query !== "" && <span class="count">no matches</span>}
      <button type="button" class="icon" aria-label="Close find" onClick={onClose}>
        <Icon name="close" />
      </button>
    </div>
  );
}
