import { Code, ConnectError } from "@connectrpc/connect";

import type { createClients } from "./client";
import { applyDelta, type Conversation, prependOlder } from "./conversation";
import type {
  OlderResponse,
  SessionsOlderResponse,
  SessionsWatchConversationResponse,
  WatchConversationResponse,
} from "./gen/orrery/v1/orrery_pb";

export interface Followed {
  kind: "pane" | "session";
  id: string;
}

// Follow is what follow returns: stop ends the stream, and older asks for the page before the oldest item held.
export interface Follow {
  stop: () => void;
  older: () => void;
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
// The returned older asks for the page before the oldest item held, and prepends it when it answers. Only one page is
// asked for at a time, and none is asked for once the held conversation has no older items. A page that answers after
// stop, or after a reset changed the held transcript or oldest, is dropped. A failed_precondition answer means the
// pane's transcript changed under the page, so the history is taken to end there; any other failure leaves more as it
// was, and a later call asks again.
export function follow(
  clients: ReturnType<typeof createClients>,
  row: Followed,
  onChange: (conversation: Conversation) => void,
  retryMs: number = 1000,
  schedule: Schedule = timerSchedule,
): Follow {
  const controller = new AbortController();
  let held: Conversation | null = null;
  let stopped = false;
  let olderInFlight = false;
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

  // stillHolds reports whether the held conversation is still the one a page was asked of: the same transcript, with
  // the same oldest cursor. A reset that moved either makes the page's cursor wrong for it.
  const stillHolds = (asked: Conversation): boolean =>
    held !== null && held.transcript === asked.transcript && held.oldest === asked.oldest;

  const older = (): void => {
    if (stopped || held === null || !held.more || olderInFlight) {
      return;
    }
    const asked = held;
    olderInFlight = true;
    const signal = controller.signal;
    const request: Promise<SessionsOlderResponse | OlderResponse> =
      row.kind === "pane"
        ? clients.panes.older({ pane: row.id, transcript: asked.transcript, before: asked.oldest }, { signal })
        : clients.sessions.older({ id: row.id, before: asked.oldest }, { signal });
    request.then(
      (page) => {
        olderInFlight = false;
        if (stopped || !stillHolds(asked) || held === null) {
          return;
        }
        held = prependOlder(held, page);
        onChange(held);
      },
      (error: unknown) => {
        olderInFlight = false;
        if (stopped || !stillHolds(asked) || held === null) {
          return;
        }
        if (ConnectError.from(error).code === Code.FailedPrecondition) {
          held = { ...held, more: false };
          onChange(held);
        }
      },
    );
  };

  void pump();

  const stop = () => {
    stopped = true;
    cancel();
    wake();
    controller.abort();
  };

  return { stop, older };
}
