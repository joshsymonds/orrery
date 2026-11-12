import { render } from "preact";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { loadRows } from "./load";
import type { Row } from "./rows";

const defaultIntervalMs = 5000;

// How long an event waits before its reload, so the events of a burst share one reload.
const eventReloadMs = 100;

// The element index.html provides for the page to mount into.
const pageRootId = "app";

interface Loaded {
  rows: Row[];
  error: string;
}

// start renders App into root and reloads the rows. Each load that reports a seq above 0 opens Events.Watch from it,
// and every event schedules one reload. While the stream is open the poll every intervalMs does nothing; once the
// stream ends or fails, the poll reloads, and the next successful load reopens the stream. A tick or event that lands
// while a reload is in flight never starts a second one: an event waits for the reload in flight, then reloads again.
// The returned function stops reloading, aborts the stream and unmounts App.
export function start(
  root: HTMLElement,
  clients: ReturnType<typeof createClients>,
  intervalMs = defaultIntervalMs,
): () => void {
  let loaded: Loaded = { rows: [], error: "" };
  let now = 0;
  let stopped = false;
  let loading = false;
  let rerun = false;
  let eventTimer: ReturnType<typeof setTimeout> | undefined;
  let stream: AbortController | null = null;

  const draw = () => {
    render(<App rows={loaded.rows} error={loaded.error} now={now} clients={clients} />, root);
  };

  const reload = async () => {
    if (loading) {
      return;
    }
    loading = true;
    try {
      const next = await loadRows(clients);
      if (stopped) {
        return;
      }
      loaded = next;
      now = Math.floor(Date.now() / 1000);
      draw();
      // A seq of 0 is a failed Sessions.List or an empty store; a stream from 0 would replay every stored event, so
      // the poll carries on until a load reports a seq.
      if (stream === null && next.seq > 0n) {
        void watchEvents(next.seq);
      }
    } finally {
      loading = false;
      if (rerun && !stopped) {
        rerun = false;
        void reload();
      }
    }
  };

  const onEvent = () => {
    if (eventTimer !== undefined) {
      return;
    }
    eventTimer = setTimeout(() => {
      eventTimer = undefined;
      if (stopped) {
        return;
      }
      if (loading) {
        rerun = true;
      } else {
        void reload();
      }
    }, eventReloadMs);
  };

  // watchEvents streams the events after seq until the stream ends, fails or start stops. Its end leaves the poll to
  // reload and reopen the stream.
  const watchEvents = async (after: bigint) => {
    const controller = new AbortController();
    stream = controller;
    try {
      for await (const _event of clients.events.watch({ after }, { signal: controller.signal })) {
        if (stopped) {
          return;
        }
        onEvent();
      }
    } catch {
      // A failed stream is left to the poll, which reloads and reopens it.
    } finally {
      if (stream === controller) {
        stream = null;
      }
    }
  };

  draw();
  void reload();
  const timer = setInterval(() => {
    if (stream === null) {
      void reload();
    }
  }, intervalMs);

  return () => {
    stopped = true;
    clearInterval(timer);
    clearTimeout(eventTimer);
    stream?.abort();
    render(null, root);
  };
}

// Importing this module in a test has no DOM to mount into, so it starts nothing.
const mount = globalThis.document?.getElementById(pageRootId);
if (mount) {
  start(mount, createClients(createTransport()));
}
