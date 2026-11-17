import type { Conversation } from "./conversation";
import type { ConversationItem } from "./gen/orrery/v1/orrery_pb";

// SentText is a message a composer sent: its text and the prompt id it was sent under.
export interface SentText {
  promptId: string;
  text: string;
}

// Sending is a message the page shows as sending until its stored prompt is held. after is the newest item the
// conversation held when the send began, or undefined when it held none: a pane's send matches only a prompt that
// comes after it.
export interface Sending extends SentText {
  after: ConversationItem | undefined;
}

// SendKind is the kind of conversation a row holds: a pane or a headless session. The two match a send differently.
export type SendKind = "pane" | "session";

// sendingEntry makes the entry for a send that starts while held is the conversation on screen.
export function sendingEntry(sent: SentText, held: Conversation | null): Sending {
  return { ...sent, after: held?.items.at(-1) };
}

// unsentEntries returns the entries that are still unsent against the conversation a row holds, in their order. A
// session's entry is sent once a prompt item with its prompt id is held, or its message is in the held set. A pane's
// entry is sent once a prompt item with its text, compared trimmed, is held after the item the send began after. A
// prompt before that item, such as one an older page prepended, never matches. When that item is no longer held, a
// reset dropped it, and any held prompt with the text matches. With no conversation held, every entry is unsent. It
// returns entries itself when none is sent.
export function unsentEntries(entries: Sending[], held: Conversation | null, kind: SendKind): Sending[] {
  if (held === null || entries.length === 0) {
    return entries;
  }
  const kept = entries.filter((entry) => !sentIn(entry, held, kind));
  return kept.length === entries.length ? entries : kept;
}

function sentIn(entry: Sending, held: Conversation, kind: SendKind): boolean {
  if (kind === "session") {
    return (
      held.items.some((item) => item.kind === "prompt" && item.promptId === entry.promptId) ||
      held.held.some((message) => message.promptId === entry.promptId)
    );
  }
  // from is the index a matching prompt must pass: -1 when the send began with nothing held or its item was dropped.
  const from = entry.after === undefined ? -1 : held.items.indexOf(entry.after);
  return held.items.some((item, index) => index > from && item.kind === "prompt" && sameText(item.text, entry.text));
}

// sameText compares two messages' text as the page does for a sent message: trimmed.
export function sameText(a: string, b: string): boolean {
  return a.trim() === b.trim();
}

// withoutTexts takes texts back out of entries: for each text in order, the first entry still in the list whose text is
// the same text is removed, so entries of equal text are taken one for each text. It returns entries itself when no
// text matches.
export function withoutTexts<T extends { text: string }>(entries: T[], texts: string[]): T[] {
  const kept = [...entries];
  for (const text of texts) {
    const index = kept.findIndex((entry) => sameText(entry.text, text));
    if (index >= 0) {
      kept.splice(index, 1);
    }
  }
  return kept.length === entries.length ? entries : kept;
}
