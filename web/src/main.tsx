import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView, noEpics } from "./epics";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { InAppNotice } from "./gen/orrery/v1/notice_pb";
import type { HostStatusResponse, WatchResponse } from "./gen/orrery/v1/orrery_pb";
import { listHolds } from "./listholds";
import { changedOnly, type HeldEpic, loadEpics, type Refetch } from "./loadepics";
import { loadRows } from "./load";
import { bundledBuild, holdsUnsentText, watchPageBuild } from "./pagebuild";
import { watchNotices } from "./notices";
import { type Viewing, watchPresence } from "./presence";
import { type Affects, affectsOf, stepSessions } from "./refresh";
import { type Address, type Entry, formatAddress, parseAddress } from "./router";
import type { Row } from "./rows";
import type { SeenClock, SeenWindow } from "./seen";

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

// What an event that has waited for its reload asks for before any event has come.
const noAffects: Affects = { rows: false, epics: false };

interface Loaded {
  rows: Row[];
  error: string;
  epics: EpicsView;
  epicsError: string;
  contracts: Contract[];
}

// PageBuild is the build the page was bundled with and how it reloads itself.
export interface PageBuild {
  build: string;
  reload: () => void;
}

// The page's own build and reload: the build scripts/build-web.sh bundled it with, and a reload of the window.
const bundledPage: PageBuild = { build: bundledBuild, reload: () => window.location.reload() };

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

// coalesced makes the call that runs run, one at a time. A call while a run is in flight starts none. A queued call
// marks one more run, which starts when the one in flight ends, however many queued calls landed meanwhile; any other
// call is dropped. Nothing runs once stopped holds.
function coalesced(run: () => Promise<void>, stopped: () => boolean): (queued: boolean) => void {
  let flying = false;
  let again = false;
  const go = (queued: boolean): void => {
    if (stopped()) {
      return;
    }
    if (flying) {
      again ||= queued;
      return;
    }
    flying = true;
    void run().finally(() => {
      flying = false;
      if (again) {
        again = false;
        go(true);
      }
    });
  };
  return go;
}

// joinRefetch is the refetch that names every epic either names.
function joinRefetch(held: Refetch | null, next: Refetch): Refetch {
  return held === null ? next : new Set([...held, ...next]);
}

// start renders App into root and loads the rows and the epics. Each rows load that reports a seq above 0 opens
// Events.Watch from it. An event reloads only what it can change (affectsOf): the rows, the epics, both or neither, so
// the streaming text of an agent's reply reloads nothing. The events of a burst share one reload after eventReloadMs.
// The rows load and the epics load each run one at a time: a reload asked for while one is in flight runs once that
// one ends, however many were asked for meanwhile, except that a tick's rows reload is dropped. An epics load lists the epics and asks Epics.Get only for each live
// epic it holds no detail for, whose listed message changed, or that it names. Each poll tick loads the epics and names
// the epic the page shows, so a change only Epics.Get carries reaches an open epic page within a tick; moving the view
// onto a live epic loads the epics naming it at once. A tick also reads the host's status and moves the age labels to the current time, and
// while the stream is down it reloads the rows too. A stream that fails unavailable reopens from the last seq it
// delivered after retryMs. Once the stream ends or fails any other way, the poll reloads the rows, and the next
// successful rows load reopens the stream.
//
// While win's document is hidden the page fetches nothing and holds no stream: hiding it closes Events.Watch and
// Notices.Watch and marks the page stale, a tick does nothing, a rows load that lands opens no stream, and a page hidden
// at start opens neither. When it becomes visible a stale page reopens the notices watch, reads the host, and reloads
// the rows, whose load reopens Events.Watch, and the epics, asking Epics.Get for each live epic whose listed message
// changed and for the epic the page shows. An epic's listed message carries its revision, which every write to the
// epic but its steps' moves, so nothing an Epics.Get answers changes unseen. The host's status is read under its own
// guard, and a refused read hands App null. The page also reports its input: watchPresence sends each Presence.Report request, its input age and
// the view App shows, and a failed report is dropped. The returned function stops reloading, aborts the stream, stops
// the presence watch, aborts the notices watch and unmounts App. The page also watches the in-app notices its banner
// shows until each ends; a notices stream that fails reopens after retryMs. The page's address is bound to the URL
// hash: a loaded hash is given to App once the list its entry is in has answered, and falls back to its view when that
// list lacks the entry. App's reports are written to the history, and a popstate applies the address Back or Forward
// lands on.
//
// A page an earlier serve sent reloads itself: once a Host.Status answer names a page build other than page's, the page
// reloads through page.reload at the first moment it is hidden and no text box under root holds unsent text, checked at
// that answer, at each change of visibility, at each input under root and at each tick (watchPageBuild).
export function start(
  root: HTMLElement,
  clients: ReturnType<typeof createClients>,
  intervalMs = defaultIntervalMs,
  retryMs = defaultRetryMs,
  win: SeenWindow = window,
  page: PageBuild = bundledPage,
): () => void {
  let loaded: Loaded = { rows: [], error: "", epics: noEpics, epicsError: "", contracts: [] };
  let notices: InAppNotice[] = [];
  let host: HostStatusResponse | null = null;
  let now = 0;
  let stopped = false;
  // The held epic details, each with the Epics.List message it was fetched against, kept across epics loads.
  let held: ReadonlyMap<string, HeldEpic> = new Map();
  // The ids of the step sessions the loaded epics hold, which tell a step's events from a row's.
  let steps: ReadonlySet<string> = new Set();
  // The epics the next epics load asks Epics.Get for whatever their listed message, joined from every ask since the
  // last load started, or null when nothing was asked.
  let refetch: Refetch | null = null;
  let hostLoading = false;
  // What the events waiting for their reload can change, joined across the burst.
  let eventAffects: Affects = noAffects;
  let eventTimer: ReturnType<typeof setTimeout> | undefined;
  let stream: AbortController | null = null;
  // Whether a tick or an event asked for a load while the page was hidden.
  let stale = false;
  // The address App is given, which is undefined until one applies, so App opens nothing before then. holding is the
  // address with an entry that waits on its list's first answer. rowsAnswered and epicsAnswered are whether the rows,
  // which a session or a pane is held by, and the epics, which a step or an epic is held by, have answered their first
  // load.
  let address: Address | undefined;
  let holding: Held | null = null;
  let rowsAnswered = false;
  let epicsAnswered = false;

  const hidden = () => win.document.visibilityState === "hidden";
  const build = watchPageBuild(page.build, hidden, () => holdsUnsentText(root), page.reload);
  const onInput = () => build.check();
  const isStopped = () => stopped;

  const draw = () => {
    const error = [loaded.error, loaded.epicsError].filter((text) => text !== "").join("; ");
    render(
      <App
        rows={loaded.rows}
        error={error}
        epics={loaded.epics}
        contracts={loaded.contracts}
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
  // the address stays as the prop and App opens it. If not, the prop becomes the address's view with no entry, and the
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
    address = { view: pending.view, entry: null };
    window.history.replaceState(null, "", formatAddress(address));
  };

  // takeAddress makes next the address App is given: one with no entry at once, and one with an entry once it is held
  // and settled.
  const takeAddress = (next: Address) => {
    if (next.entry === null) {
      address = next;
      return;
    }
    holding = { view: next.view, entry: next.entry };
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
      if (status !== null) {
        build.served(status.pageBuild);
      }
    } finally {
      hostLoading = false;
    }
  };

  // runEpics runs one epics load at a time, each taking the refetch asked for since the last one started.
  const runEpics = coalesced(async () => {
    const asked = refetch ?? changedOnly;
    refetch = null;
    const next = await loadEpics(clients, held, asked, loaded.contracts);
    if (stopped) {
      return;
    }
    held = next.held;
    steps = stepSessions(next.epics, next.details);
    loaded = { ...loaded, epics: epicsView(next.epics, next.details), epicsError: next.error, contracts: next.contracts };
    epicsAnswered = true;
    settleAddress();
    now = Math.floor(Date.now() / 1000);
    draw();
  }, isStopped);

  // reloadEpics asks for an epics load that also asks Epics.Get for the epics also names. One asked for while a load is
  // in flight runs once that load ends.
  const reloadEpics = (also: Refetch) => {
    refetch = joinRefetch(refetch, also);
    runEpics(true);
  };

  // runRows runs one rows load at a time. A rows load that reports a seq opens the stream when none is open.
  const runRows = coalesced(async () => {
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
    // the poll carries on until a load reports a seq. A hidden page holds no stream; showing it reloads the rows.
    if (stream === null && next.seq > 0n && !hidden()) {
      void watchEvents(next.seq);
    }
  }, isStopped);
  // reloadRows asks for a rows load that runs once a load in flight ends, as an event's does.
  const reloadRows = () => runRows(true);
  // pollRows asks for a rows load that is dropped while one is in flight, as a tick's is: the load in flight is as new
  // as the tick, and the next tick polls again.
  const pollRows = () => runRows(false);

  // shownEpic is the refetch that names the epic the page shows, or names none when it shows no epic.
  const shownEpic = (): Refetch => (viewing.case === "viewingEpic" ? new Set([viewing.value]) : changedOnly);

  // onEvent reloads what event can change, after eventReloadMs, joined with the burst it lands in. While the page is
  // hidden it only marks the page stale.
  const onEvent = (event: WatchResponse) => {
    const affects = affectsOf(event, steps);
    if (!affects.rows && !affects.epics) {
      return;
    }
    if (hidden()) {
      stale = true;
      return;
    }
    eventAffects = { rows: eventAffects.rows || affects.rows, epics: eventAffects.epics || affects.epics };
    if (eventTimer !== undefined) {
      return;
    }
    eventTimer = setTimeout(() => {
      eventTimer = undefined;
      const due = eventAffects;
      eventAffects = noAffects;
      if (stopped) {
        return;
      }
      if (hidden()) {
        stale = true;
        return;
      }
      if (due.rows) {
        reloadRows();
      }
      if (due.epics) {
        reloadEpics(changedOnly);
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
            onEvent(event);
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

  // The view App shows is kept for the presence watch, which reads it at each report, and for the epics loads, which
  // name the epic it shows. A change of it sends at once while the page is visible and focused, and a move onto an epic
  // loads the epics naming it. The watch is made before the first draw, so onViewing never meets no watch.
  let viewing: Viewing = { case: undefined };
  const presence = watchPresence(
    win,
    (request) => {
      // A failed report is dropped without a page error; the next input sends again.
      void clients.presence.report(request).catch(() => undefined);
    },
    realClock,
    () => viewing,
  );
  const onViewing = (next: Viewing) => {
    // Only a live epic is ever asked for, so a move onto an ended one loads nothing.
    const moved =
      next.case === "viewingEpic" &&
      (viewing.case !== "viewingEpic" || viewing.value !== next.value) &&
      loaded.epics.listed.get(next.value)?.live === true;
    viewing = next;
    presence.viewChanged();
    if (moved && !hidden()) {
      reloadEpics(shownEpic());
    }
  };

  // The notices watch, aborted while the page is hidden and made again when it shows. Null while none is open.
  let noticesWatch: AbortController | null = null;
  const openNotices = () => {
    const controller = new AbortController();
    noticesWatch = controller;
    void watchNotices(
      clients,
      (list) => {
        notices = list;
        draw();
      },
      retryMs,
      controller.signal,
    );
  };

  // closeStreams ends Events.Watch and Notices.Watch. The stream slot is freed at once, so the rows load that showing
  // the page starts reopens Events.Watch even before the aborted stream has wound down.
  const closeStreams = () => {
    stream?.abort();
    stream = null;
    noticesWatch?.abort();
    noticesWatch = null;
  };

  // onVisibility closes the streams of a page that is hidden and marks it stale, and reloads a stale page as it becomes
  // visible: the notices watch, the host, the rows, which reopen Events.Watch, and the epics that changed or that the
  // page shows.
  const onVisibility = () => {
    if (stopped) {
      return;
    }
    if (hidden()) {
      closeStreams();
      stale = true;
      build.check();
      return;
    }
    if (!stale) {
      return;
    }
    stale = false;
    if (noticesWatch === null) {
      openNotices();
    }
    void loadHostStatus();
    reloadRows();
    reloadEpics(shownEpic());
  };

  // A page loaded with a hash is given that address, and an empty hash gives App none, so a bare load opens nothing and
  // writes no history.
  const loadedHash = window.location.hash;
  if (loadedHash !== "") {
    takeAddress(parseAddress(loadedHash));
  }
  window.addEventListener("popstate", onPopState);
  win.document.addEventListener("visibilitychange", onVisibility);
  root.addEventListener("input", onInput);
  draw();
  if (hidden()) {
    stale = true;
  } else {
    reloadRows();
    reloadEpics(changedOnly);
    void loadHostStatus();
  }
  const timer = setInterval(() => {
    build.check();
    if (hidden()) {
      stale = true;
      return;
    }
    void loadHostStatus();
    reloadEpics(shownEpic());
    if (stream === null) {
      pollRows();
    }
    now = Math.floor(Date.now() / 1000);
    draw();
  }, intervalMs);

  if (!hidden()) {
    openNotices();
  }

  return () => {
    stopped = true;
    window.removeEventListener("popstate", onPopState);
    win.document.removeEventListener("visibilitychange", onVisibility);
    root.removeEventListener("input", onInput);
    presence.stop();
    clearInterval(timer);
    clearTimeout(eventTimer);
    closeStreams();
    render(null, root);
  };
}

// Importing this module in a test has no DOM to mount into, so it starts nothing.
const mount = globalThis.document?.getElementById(pageRootId);
if (mount) {
  start(mount, createClients(createTransport()));
}
