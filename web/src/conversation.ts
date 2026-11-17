import type {
  ConversationItem,
  ConversationState,
  EngineText,
  HeldMessage,
  OlderResponse,
  SessionsOlderResponse,
  SessionsWatchConversationResponse,
  WatchConversationResponse,
} from "./gen/orrery/v1/orrery_pb";

// What a page holds of one streamed conversation: the transcript and seq to resume from, every item it has loaded, the
// oldest cursor of those items and whether older items may remain, the state, the unfinished text of each open block,
// and the held messages of a headless session, oldest first. oldest is the cursor an older page is asked before: a seq
// for a session, a line start for a pane. A pane holds no messages, so its held set is empty.
export interface Conversation {
  transcript: string;
  seq: bigint;
  items: ConversationItem[];
  oldest: bigint;
  more: boolean;
  state: ConversationState | undefined;
  unfinished: EngineText[];
  held: HeldMessage[];
}

// Folds a pane stream's or a session stream's delta into what the page holds. A reset, or a delta applied to nothing
// held, holds the delta's items, its oldest cursor, whether older items may remain (oldest above 0), its state, its
// unfinished set, and its held set. Any other delta, a keepalive included, appends its items after the held ones, keeps
// the held oldest and more, and replaces the unfinished set and the held set with its own, even empty ones: a session
// stream sends its whole unfinished set and its whole held set with every delta, and a pane stream sends neither. A set
// state replaces the held state while an unset one keeps it. Nothing is trimmed: the page keeps every item it has
// loaded. The result takes the delta's transcript and seq.
export function applyDelta(
  held: Conversation | null,
  delta: WatchConversationResponse | SessionsWatchConversationResponse,
): Conversation {
  const heldMessages = "held" in delta ? delta.held : [];
  if (held === null || delta.reset) {
    return {
      transcript: delta.transcript,
      seq: delta.seq,
      items: delta.items,
      oldest: delta.oldest,
      more: delta.oldest > 0n,
      state: delta.state,
      unfinished: delta.unfinished,
      held: heldMessages,
    };
  }
  return {
    transcript: delta.transcript,
    seq: delta.seq,
    items: held.items.concat(delta.items),
    oldest: held.oldest,
    more: held.more,
    state: delta.state ?? held.state,
    unfinished: delta.unfinished,
    held: heldMessages,
  };
}

// prependOlder folds an older page into what the page holds: the page's items go before the held ones. oldest moves to
// the page's when the page holds items, and more takes the page's. The transcript, seq, state, unfinished set and held
// set stay as they were.
export function prependOlder(held: Conversation, page: SessionsOlderResponse | OlderResponse): Conversation {
  return {
    ...held,
    items: page.items.concat(held.items),
    oldest: page.items.length > 0 ? page.oldest : held.oldest,
    more: page.more,
  };
}
