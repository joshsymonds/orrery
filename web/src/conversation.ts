import type {
  ConversationItem,
  ConversationState,
  EngineText,
  SessionsWatchConversationResponse,
  WatchConversationResponse,
} from "./gen/orrery/v1/orrery_pb";

// What a page holds of one streamed conversation: the transcript and seq to resume from, the newest items, the state,
// and the unfinished text of each open block.
export interface Conversation {
  transcript: string;
  seq: bigint;
  items: ConversationItem[];
  state: ConversationState | undefined;
  unfinished: EngineText[];
}

// Folds a pane stream's or a session stream's delta into what the page holds. A reset, or a delta applied to nothing
// held, holds the delta's newest keep items, its state, and its unfinished set. Any other delta, a keepalive included,
// appends its items and replaces the unfinished set with its own, even an empty one: a session stream sends its whole
// unfinished set with every delta, and a pane stream never sets it. A set state replaces the held state while an unset
// one keeps it. Items are trimmed to the newest keep. The result takes the delta's transcript and seq.
export function applyDelta(
  held: Conversation | null,
  delta: WatchConversationResponse | SessionsWatchConversationResponse,
  keep: number = 50,
): Conversation {
  if (held === null || delta.reset) {
    return {
      transcript: delta.transcript,
      seq: delta.seq,
      items: delta.items.slice(Math.max(0, delta.items.length - keep)),
      state: delta.state,
      unfinished: delta.unfinished,
    };
  }
  const items = held.items.concat(delta.items);
  return {
    transcript: delta.transcript,
    seq: delta.seq,
    items: items.slice(Math.max(0, items.length - keep)),
    state: delta.state ?? held.state,
    unfinished: delta.unfinished,
  };
}
