import type { JSX } from "preact";
import { useEffect, useRef } from "preact/hooks";

import type { ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { drawnText } from "./markdown";

// Match is one occurrence of a query in a conversation's drawn text: item is the index of the item it is drawn in, and
// start is its offset in that item's searched text. A call's result is searched as part of its call, so a match in the
// result has the call's index.
export interface Match {
  item: number;
  start: number;
}

// findMatches returns the case-insensitive occurrences of query in the drawn text of items, in item order. Replies,
// prompts and step folds are searched as Markdown draws them; a call is searched by its input or question and by the
// text of the result that answers it, and a notice by its kind and text. Compaction, a send-back of a check round (it
// draws as a card), and a result that answers no call are not searched. An empty query matches nothing.
export function findMatches(items: ConversationItem[], query: string): Match[] {
  if (query === "") {
    return [];
  }
  const pattern = new RegExp(escapeRegExp(query), "gi");
  const answers = resultsByCall(items);
  const matches: Match[] = [];
  items.forEach((item, index) => {
    for (const found of searchedText(item, answers.get(index)).matchAll(pattern)) {
      matches.push({ item: index, start: found.index });
    }
  });
  return matches;
}

// searchedText is the text an item's occurrences are found in, the parts it draws joined one to a line, so a query with
// no line break cannot match across two parts. result is the result that answers the item, when it is a call.
function searchedText(item: ConversationItem, result: ConversationItem | undefined): string {
  switch (item.kind) {
    case "prompt":
      if (item.sender === "engine" && item.round >= 1) {
        return "";
      }
      return drawnText(item.text);
    case "tool":
      return joinParts([item.input, result?.text]);
    case "question":
      return joinParts([item.text, result?.text]);
    case "notice":
      return joinParts([item.notice, item.text]);
    case "compaction":
    case "result":
      return "";
    default:
      return drawnText(item.text);
  }
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

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// FindBar is the find field over the transcript: a focused input for the query, then the current match as n of N, or
// nothing when no item matches. Enter goes to the next match and Shift+Enter to the previous, and Escape closes the
// bar. The keys the bar handles stop here, so the page's keydown handler never sees them.
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
      <input
        ref={field}
        type="text"
        value={query}
        placeholder="find in transcript"
        onInput={(event) => onQuery(event.currentTarget.value)}
        onKeyDown={onKeyDown}
      />
      {matches > 0 && <span class="count">{`${current + 1} of ${matches}`}</span>}
    </div>
  );
}
