import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView, noEpics } from "./epics";
import type { InAppNotice } from "./gen/orrery/v1/notice_pb";
import type { HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import { listHolds } from "./listholds";
import { type HeldEpic, loadEpics } from "./loadepics";
import { loadRows } from "./load";
import { watchNotices } from "./notices";
import { type Viewing, watchPresence } from "./presence";
import { type Address, type Entry, formatAddress, parseAddress } from "./router";
import type { Row } from "./rows";
import type { SeenClock } from "./seen";

const defaultIntervalMs = 5000;

// How long a stream that failed unavailable waits before it reopens from the last seq it delivered.
const defaultRetryMs = 1000;

// How long an event waits before its reload, so the events of a burst share one reload.
const eventReloadMs = 100;

// The element index.html provides for the page to mount into.
const pageRootId = "app";

// The clock the presence watch reads and times its reports with.
const realClock: SeenClock = {
  now: () => Date.now(),
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

interface Loaded {
  rows: Row[];
  error: string;
  epics: EpicsView;
  epicsError: string;
}

// A loaded or popped address that names an entry, held until the list its entry is in has answered its first load.
type Held = Address & { entry: Entry };

// pause resolves after ms, or as soon as signal aborts, so stopping the page does not wait out a retry.
function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        resolve();
      },
      { once: true },
    );
  });
}

// start renders App into root and reloads the rows and the epics. Each load that reports a seq above 0 opens
// Events.Watch from it, and every event schedules one reload. While the stream is open each poll tick loads the epics
// and moves the age labels to the current time, and the rows wait for the stream's events. A stream that fails
// unavailable reopens from the last seq it delivered after retryMs. Once the stream ends or fails any other way, the
// poll reloads the rows, and the next successful load reopens the stream. A tick or event that lands while a reload is
// in flight never starts a second one: an event waits for the reload in flight, then reloads again. The epics load has
// its own guard: an event that lands while it is in flight is left to the next tick, and a tick that lands while it is
// in flight is remembered, so the next epics load fetches every live epic. An epics load keeps the held detail of each
// live epic whose listed message is unchanged, so an event reload fetches only the rest. The host's status is read at
// start and on each tick, under its own guard, and a refused read hands App null. The page also reports its input:
// watchPresence sends each Presence.Report request, its input age and the view App shows, and a failed report is
// dropped. The returned function stops reloading, aborts the stream, stops the presence watch, aborts the notices watch
// and unmounts App. The page also watches the in-app notices its banner shows until each ends; a notices stream that
// fails reopens after retryMs. The page's address is bound to the URL hash: a loaded hash is given to App once the list
// its entry is in has answered, and falls back to its order when that list lacks the entry. App's reports are written
// to the history, and a popstate applies the address Back or Forward lands on.
export function start(
  root: HTMLElement,
  clients: ReturnType<typeof createClients>,
  intervalMs = defaultIntervalMs,
  retryMs = defaultRetryMs,
): () => void {
  let loaded: Loaded = { rows: [], error: "", epics: noEpics, epicsError: "" };
  let notices: InAppNotice[] = [];
  let host: HostStatusResponse | null = null;
  let now = 0;
  let stopped = false;
  let loading = false;
  let epicsLoading = false;
  // The held epic details, each with the Epics.List message it was fetched against, kept across epics loads.
  let held: ReadonlyMap<string, HeldEpic> = new Map();
  // Whether a poll tick has landed since the last epics load started. A tick that lands while an epics load is in
  // flight stays set for the next one.
  let tickPending = false;
  let hostLoading = false;
  let rerun = false;
  let eventTimer: ReturnType<typeof setTimeout> | undefined;
  let stream: AbortController | null = null;
  // The address App is given, which is undefined until one applies, so App opens nothing before then. holding is the
  // address with an entry that waits on its list's first answer. rowsAnswered and epicsAnswered are whether the rows,
  // which a session or a pane is held by, and the epics, which a step or an epic is held by, have answered their first
  // load.
  let address: Address | undefined;
  let holding: Held | null = null;
  let rowsAnswered = false;
  let epicsAnswered = false;

  const draw = () => {
    const error = [loaded.error, loaded.epicsError].filter((text) => text !== "").join("; ");
    render(
      <App
        rows={loaded.rows}
        error={error}
        epics={loaded.epics}
        now={now}
        clients={clients}
        notices={notices}
        host={host}
        onViewing={onViewing}
        address={address}
        onAddress={onAddress}
      />,
      root,
    );
  };

  // listAnswered reports whether the list an entry is in has answered its first load: the rows for a session or a pane,
  // and the epics for a step or an epic.
  const listAnswered = (entry: Entry): boolean =>
    entry.kind === "session" || entry.kind === "pane" ? rowsAnswered : epicsAnswered;

  // settleAddress decides the held address once the list its entry is in has answered. If that list holds the entry,
  // the address stays as the prop and App opens it. If not, the prop becomes the address's order with no entry, and the
  // URL is replaced with that form.
  const settleAddress = () => {
    if (holding === null || !listAnswered(holding.entry)) {
      return;
    }
    const pending = holding;
    holding = null;
    if (listHolds(pending, loaded.rows, loaded.epics)) {
      address = pending;
      return;
    }
    address = { order: pending.order, entry: null };
    window.history.replaceState(null, "", formatAddress(address));
  };

  // takeAddress makes next the address App is given: one with no entry at once, and one with an entry once it is held
  // and settled.
  const takeAddress = (next: Address) => {
    if (next.entry === null) {
      address = next;
      return;
    }
    holding = { order: next.order, entry: next.entry };
    settleAddress();
  };

  // onAddress writes App's reports to the history and nothing else. App reports from an effect and opens any prop whose
  // form differs from the one it shows, so a report fed back as the prop could arrive stale and undo a later change.
  const onAddress = (next: Address, mode: "push" | "replace") => {
    const form = formatAddress(next);
    if (mode === "push") {
      window.history.pushState(null, "", form);
    } else {
      window.history.replaceState(null, "", form);
    }
  };

  // onPopState applies the address Back or Forward lands on. The undefined draw opens nothing, and makes the next prop
  // a change of form, so a step back to the form the prop held still opens it.
  const onPopState = () => {
    address = undefined;
    holding = null;
    draw();
    takeAddress(parseAddress(window.location.hash));
    draw();
  };

  // loadHostStatus reads Host.Status. A refused or failed read hands App null and reports nothing. A read that lands
  // while the last one is in flight is left to the next tick.
  const loadHostStatus = async () => {
    if (hostLoading) {
      return;
    }
    hostLoading = true;
    try {
      const status = await clients.host.status({}).catch(() => null);
      if (stopped) {
        return;
      }
      host = status;
      draw();
    } finally {
      hostLoading = false;
    }
  };

  const loadEpicsView = async () => {
    if (epicsLoading) {
      return;
    }
    epicsLoading = true;
    const tick = tickPending;
    tickPending = false;
    try {
      const next = await loadEpics(clients, held, tick);
      if (stopped) {
        return;
      }
      held = next.held;
      loaded = { ...loaded, epics: epicsView(next.epics, next.details), epicsError: next.error };
      epicsAnswered = true;
      settleAddress();
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
      rowsAnswered = true;
      settleAddress();
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

  // watchEvents streams the events after seq and holds the stream slot while it does. A stream that fails unavailable
  // reopens from the last seq it delivered after retryMs, still holding the slot, so the poll does not open a second
  // stream meanwhile. Any other end leaves the poll to reload and reopen the stream.
  const watchEvents = async (after: bigint) => {
    const controller = new AbortController();
    stream = controller;
    let seq = after;
    try {
      while (!stopped) {
        try {
          for await (const event of clients.events.watch({ after: seq }, { signal: controller.signal })) {
            if (stopped) {
              return;
            }
            seq = event.seq;
            onEvent();
          }
          return;
        } catch (error) {
          if (ConnectError.from(error).code !== Code.Unavailable) {
            return;
          }
        }
        await pause(retryMs, controller.signal);
      }
    } finally {
      if (stream === controller) {
        stream = null;
      }
    }
  };

  // The view App shows is kept for the presence watch, which reads it at each report. A change of it sends at once
  // while the page is visible and focused. The watch is made before the first draw, so onViewing never meets no watch.
  let viewing: Viewing = { case: undefined };
  const presence = watchPresence(
    window,
    (request) => {
      // A failed report is dropped without a page error; the next input sends again.
      void clients.presence.report(request).catch(() => undefined);
    },
    realClock,
    () => viewing,
  );
  const onViewing = (next: Viewing) => {
    viewing = next;
    presence.viewChanged();
  };

  // A page loaded with a hash is given that address, and an empty hash gives App none, so a bare load opens nothing and
  // writes no history.
  const loadedHash = window.location.hash;
  if (loadedHash !== "") {
    takeAddress(parseAddress(loadedHash));
  }
  window.addEventListener("popstate", onPopState);
  draw();
  void reload();
  void loadHostStatus();
  const timer = setInterval(() => {
    // The tick is marked before any load starts, so the epics load it reaches, or the next one if it is in flight,
    // refetches every live epic whether the stream is open or down.
    tickPending = true;
    void loadHostStatus();
    if (stream === null) {
      void reload();
      return;
    }
    void loadEpicsView();
    now = Math.floor(Date.now() / 1000);
    draw();
  }, intervalMs);

  const noticesWatch = new AbortController();
  void watchNotices(
    clients,
    (list) => {
      notices = list;
      draw();
    },
    retryMs,
    noticesWatch.signal,
  );

  return () => {
    stopped = true;
    window.removeEventListener("popstate", onPopState);
    presence.stop();
    clearInterval(timer);
    clearTimeout(eventTimer);
    stream?.abort();
    noticesWatch.abort();
    render(null, root);
  };
}

// Importing this module in a test has no DOM to mount into, so it starts nothing.
const mount = globalThis.document?.getElementById(pageRootId);
if (mount) {
  start(mount, createClients(createTransport()));
}
