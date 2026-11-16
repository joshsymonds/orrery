import type { createClients } from "./client";
import { applyDelta, type Conversation } from "./conversation";
import type { SessionsWatchConversationResponse, WatchConversationResponse } from "./gen/orrery/v1/orrery_pb";

export interface Followed {
  kind: "pane" | "session";
  id: string;
}

// Schedule runs run after ms and returns the function that cancels it. follow's retry wait goes through one, so a test
// can see the wait armed and end it when it chooses.
export type Schedule = (run: () => void, ms: number) => () => void;

// timerSchedule is the Schedule of a page: setTimeout, cancelled by clearTimeout.
const timerSchedule: Schedule = (run, ms) => {
  const timer = setTimeout(run, ms);
  return () => clearTimeout(timer);
};

// follow streams one row's conversation into onChange and keeps it current. A stream that ends or fails is retried
// after retryMs, waited through schedule, resuming from the held transcript and seq, so the server answers with only
// what came after. The returned stop aborts the stream and any pending retry, and nothing reaches onChange after it.
export function follow(
  clients: ReturnType<typeof createClients>,
  row: Followed,
  onChange: (conversation: Conversation) => void,
  retryMs: number = 1000,
  schedule: Schedule = timerSchedule,
): () => void {
  const controller = new AbortController();
  let held: Conversation | null = null;
  let stopped = false;
  let cancel: () => void = () => {};
  let wake: () => void = () => {};

  const pause = () =>
    new Promise<void>((resolve) => {
      wake = resolve;
      cancel = schedule(resolve, retryMs);
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
    cancel();
    wake();
    controller.abort();
  };
}
