import { render } from "preact";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView, noEpics } from "./epics";
import { loadEpics } from "./loadepics";
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
  epics: EpicsView;
  epicsError: string;
}

// start renders App into root and reloads the rows and the epics. Each load that reports a seq above 0 opens
// Events.Watch from it, and every event schedules one reload. While the stream is open each poll tick loads the epics
// and moves the age labels to the current time, and the rows wait for the stream's events; once the stream ends or
// fails, the poll reloads the rows, and the next successful load reopens the stream. A tick or event that lands while
// a reload is in flight never starts a second one: an event waits for the reload in flight, then reloads again. The
// epics load has its own guard: a tick or event that lands while it is in flight is left to the next tick. The
// returned function stops reloading, aborts the stream and unmounts App.
export function start(
  root: HTMLElement,
  clients: ReturnType<typeof createClients>,
  intervalMs = defaultIntervalMs,
): () => void {
  let loaded: Loaded = { rows: [], error: "", epics: noEpics, epicsError: "" };
  let now = 0;
  let stopped = false;
  let loading = false;
  let epicsLoading = false;
  let rerun = false;
  let eventTimer: ReturnType<typeof setTimeout> | undefined;
  let stream: AbortController | null = null;

  const draw = () => {
    const error = [loaded.error, loaded.epicsError].filter((text) => text !== "").join("; ");
    render(<App rows={loaded.rows} error={error} epics={loaded.epics} now={now} clients={clients} />, root);
  };

  const loadEpicsView = async () => {
    if (epicsLoading) {
      return;
    }
    epicsLoading = true;
    try {
      const next = await loadEpics(clients);
      if (stopped) {
        return;
      }
      loaded = { ...loaded, epics: epicsView(next.epics, next.details), epicsError: next.error };
      now = Math.floor(Date.now() / 1000);
      draw();
    } finally {
      epicsLoading = false;
    }
  };

  const reload = async () => {
    if (loading) {
      return;
    }
    loading = true;
    void loadEpicsView();
    try {
      const next = await loadRows(clients);
      if (stopped) {
        return;
      }
      loaded = { ...loaded, rows: next.rows, error: next.error };
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
      return;
    }
    void loadEpicsView();
    now = Math.floor(Date.now() / 1000);
    draw();
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
