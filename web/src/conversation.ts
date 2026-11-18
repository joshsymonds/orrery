import type {
  ConversationItem,
  ConversationState,
  EngineText,
  OlderResponse,
  QueuedMessage,
  SessionsOlderResponse,
  SessionsWatchConversationResponse,
  WatchConversationResponse,
} from "./gen/orrery/v1/orrery_pb";

// What a page holds of one streamed conversation: the transcript and seq to resume from, every item it has loaded, the
// oldest cursor of those items and whether older items may remain, the state, the unfinished text of each open block,
// the owner messages a headless session has not started, oldest first, and the prompt ids of the messages it has served
// as canceled or discarded. oldest is the cursor an older page is asked before: a seq for a session, a line start for a
// pane. A pane holds no messages, so its queued set is empty.
export interface Conversation {
  transcript: string;
  seq: bigint;
  items: ConversationItem[];
  oldest: bigint;
  more: boolean;
  state: ConversationState | undefined;
  unfinished: EngineText[];
  queued: QueuedMessage[];
  unsent: string[];
}

// Folds a pane stream's or a session stream's delta into what the page holds. A reset, or a delta applied to nothing
// held, holds the delta's items, its oldest cursor, whether older items may remain (oldest above 0), its state, its
// unfinished set, its queued set, and the prompt ids of its canceled and discarded messages as the unsent set. Any other
// delta, a keepalive included, appends its items after the held ones, keeps the held oldest and more, and replaces the
// unfinished set and the queued set with its own, even empty ones: a session stream sends its whole unfinished set and
// its whole queued set with every delta, and a pane stream sends neither. The unsent set grows by the prompt ids of the
// delta's canceled and discarded messages, and keeps the ones it held. A set state replaces the held state while an
// unset one keeps it. Nothing is trimmed: the page keeps every item it has loaded. The result takes the delta's
// transcript and seq.
export function applyDelta(
  held: Conversation | null,
  delta: WatchConversationResponse | SessionsWatchConversationResponse,
): Conversation {
  const queued = "queued" in delta ? delta.queued : [];
  if (held === null || delta.reset) {
    return {
      transcript: delta.transcript,
      seq: delta.seq,
      items: delta.items,
      oldest: delta.oldest,
      more: delta.oldest > 0n,
      state: delta.state,
      unfinished: delta.unfinished,
      queued,
      unsent: unsentIn(queued),
    };
  }
  const added = unsentIn(queued).filter((promptId) => !held.unsent.includes(promptId));
  return {
    transcript: delta.transcript,
    seq: delta.seq,
    items: held.items.concat(delta.items),
    oldest: held.oldest,
    more: held.more,
    state: delta.state ?? held.state,
    unfinished: delta.unfinished,
    queued,
    unsent: held.unsent.concat(added),
  };
}

// unsentIn returns the prompt ids of the messages in queued that are canceled or discarded, in order.
function unsentIn(queued: QueuedMessage[]): string[] {
  return queued
    .filter((message) => message.state === "canceled" || message.state === "discarded")
    .map((message) => message.promptId);
}

// prependOlder folds an older page into what the page holds: the page's items go before the held ones. oldest moves to
// the page's when the page holds items, and more takes the page's. The transcript, seq, state, unfinished set, queued
// set and unsent set stay as they were.
export function prependOlder(held: Conversation, page: SessionsOlderResponse | OlderResponse): Conversation {
  return {
    ...held,
    items: page.items.concat(held.items),
    oldest: page.items.length > 0 ? page.oldest : held.oldest,
    more: page.more,
  };
}

// shownItems returns the items a page draws: the conversation's items without each prompt item whose prompt id is in its
// queued set, in any state, or in its unsent set. Such a prompt is not drawn as sent until the stream drops it from the
// queue. It returns the items themselves when none is hidden.
export function shownItems(conversation: Conversation): ConversationItem[] {
  const hidden = new Set([...conversation.queued.map((message) => message.promptId), ...conversation.unsent]);
  const kept = conversation.items.filter(
    (item) => !(item.kind === "prompt" && item.promptId !== "" && hidden.has(item.promptId)),
  );
  return kept.length === conversation.items.length ? conversation.items : kept;
}

// shownConversation is a conversation as a page draws it: its items as shownItems leaves them, and the rest as it is.
// null stays null.
export function shownConversation(conversation: Conversation | null): Conversation | null {
  return conversation === null ? null : { ...conversation, items: shownItems(conversation) };
}
