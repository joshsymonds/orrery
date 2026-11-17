import type {
  ConversationItem,
  ConversationState,
  EngineText,
  OlderResponse,
  SessionsOlderResponse,
  SessionsWatchConversationResponse,
  WatchConversationResponse,
} from "./gen/orrery/v1/orrery_pb";

// What a page holds of one streamed conversation: the transcript and seq to resume from, every item it has loaded, the
// oldest cursor of those items and whether older items may remain, the state, and the unfinished text of each open
// block. oldest is the cursor an older page is asked before: a seq for a session, a line start for a pane.
export interface Conversation {
  transcript: string;
  seq: bigint;
  items: ConversationItem[];
  oldest: bigint;
  more: boolean;
  state: ConversationState | undefined;
  unfinished: EngineText[];
}

// Folds a pane stream's or a session stream's delta into what the page holds. A reset, or a delta applied to nothing
// held, holds the delta's items, its oldest cursor, whether older items may remain (oldest above 0), its state, and its
// unfinished set. Any other delta, a keepalive included, appends its items after the held ones, keeps the held oldest
// and more, and replaces the unfinished set with its own, even an empty one: a session stream sends its whole
// unfinished set with every delta, and a pane stream never sets it. A set state replaces the held state while an unset
// one keeps it. Nothing is trimmed: the page keeps every item it has loaded. The result takes the delta's transcript
// and seq.
export function applyDelta(
  held: Conversation | null,
  delta: WatchConversationResponse | SessionsWatchConversationResponse,
): Conversation {
  if (held === null || delta.reset) {
    return {
      transcript: delta.transcript,
      seq: delta.seq,
      items: delta.items,
      oldest: delta.oldest,
      more: delta.oldest > 0n,
      state: delta.state,
      unfinished: delta.unfinished,
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
  };
}

// prependOlder folds an older page into what the page holds: the page's items go before the held ones. oldest moves to
// the page's when the page holds items, and more takes the page's. The transcript, seq, state and unfinished set stay as
// they were.
export function prependOlder(held: Conversation, page: SessionsOlderResponse | OlderResponse): Conversation {
  return {
    ...held,
    items: page.items.concat(held.items),
    oldest: page.items.length > 0 ? page.oldest : held.oldest,
    more: page.more,
  };
}
