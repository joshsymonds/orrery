import type { createClients } from "./client";
import { applyDelta, type Conversation } from "./conversation";
import type { SessionsWatchConversationResponse, WatchConversationResponse } from "./gen/orrery/v1/orrery_pb";

export interface Followed {
  kind: "pane" | "session";
  id: string;
}

// follow streams one row's conversation into onChange and keeps it current. A stream that ends or fails is retried
// after retryMs, resuming from the held transcript and seq, so the server answers with only what came after. The
// returned stop aborts the stream and any pending retry, and nothing reaches onChange after it.
export function follow(
  clients: ReturnType<typeof createClients>,
  row: Followed,
  onChange: (conversation: Conversation) => void,
  retryMs: number = 1000,
): () => void {
  const controller = new AbortController();
  let held: Conversation | null = null;
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let wake: () => void = () => {};

  const pause = () =>
    new Promise<void>((resolve) => {
      wake = resolve;
      timer = setTimeout(resolve, retryMs);
    });

  const streams = (): AsyncIterable<WatchConversationResponse | SessionsWatchConversationResponse> => {
    const signal = controller.signal;
    const transcript = held?.transcript ?? "";
    const after = held?.seq ?? 0n;
    if (row.kind === "pane") {
      return clients.panes.watchConversation({ pane: row.id, transcript, after }, { signal });
    }
    return clients.sessions.watchConversation({ id: row.id, after }, { signal });
  };

  const pump = async () => {
    while (!stopped) {
      try {
        for await (const delta of streams()) {
          if (stopped) {
            return;
          }
          held = applyDelta(held, delta);
          onChange(held);
        }
      } catch {
        // A failed stream is retried below, with the held transcript and seq.
      }
      if (stopped) {
        return;
      }
      await pause();
    }
  };

  void pump();

  return () => {
    stopped = true;
    clearTimeout(timer);
    wake();
    controller.abort();
  };
}
