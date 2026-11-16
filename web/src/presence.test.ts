import assert from "node:assert/strict";
import { test } from "node:test";

import type { PresenceReportRequest } from "./gen/orrery/v1/orrery_pb";
import { type Viewing, viewingOf, watchPresence } from "./presence";
import type { SeenClock, SeenWindow } from "./seen";

// noView is the getter of a page that shows no view.
const noView: () => Viewing = () => ({ case: undefined });

// viewState is the view the page shows, as a test sets it. get is the getter watchPresence reads.
function viewState() {
  let current: Viewing = { case: undefined };
  return {
    get: (): Viewing => current,
    set(next: Viewing) {
      current = next;
    },
  };
}

// A clock whose timers fire only when the test advances time.
function fakeClock() {
  let now = 0;
  let nextId = 1;
  const timers = new Map<unknown, { at: number; fn: () => void }>();
  const clock: SeenClock = {
    now: () => now,
    setTimeout(fn, ms) {
      const id = nextId++;
      timers.set(id, { at: now + ms, fn });
      return id;
    },
    clearTimeout(handle) {
      timers.delete(handle);
    },
  };
  return {
    clock,
    pending: () => timers.size,
    advance(ms: number) {
      const end = now + ms;
      for (;;) {
        let next: { id: unknown; at: number; fn: () => void } | undefined;
        for (const [id, timer] of timers) {
          if (timer.at <= end && (next === undefined || timer.at < next.at)) {
            next = { id, at: timer.at, fn: timer.fn };
          }
        }
        if (next === undefined) break;
        now = next.at;
        timers.delete(next.id);
        next.fn();
      }
      now = end;
    },
  };
}

// Listener registry for one event target, with a count for leak checks.
function listeners() {
  const byType = new Map<string, Set<() => void>>();
  return {
    addEventListener(type: string, fn: () => void) {
      const set = byType.get(type) ?? new Set<() => void>();
      set.add(fn);
      byType.set(type, set);
    },
    removeEventListener(type: string, fn: () => void) {
      byType.get(type)?.delete(fn);
    },
    emit(type: string) {
      for (const fn of [...(byType.get(type) ?? [])]) fn();
    },
    count() {
      let total = 0;
      for (const set of byType.values()) total += set.size;
      return total;
    },
  };
}

function fakeWindow() {
  const win = listeners();
  const doc = listeners();
  const state = { visibilityState: "visible", focused: true };
  const seenWindow: SeenWindow = {
    document: {
      get visibilityState() {
        return state.visibilityState;
      },
      hasFocus: () => state.focused,
      addEventListener: doc.addEventListener,
      removeEventListener: doc.removeEventListener,
    },
    addEventListener: win.addEventListener,
    removeEventListener: win.removeEventListener,
  };
  return { seenWindow, win, doc, state };
}

test("input with no earlier send is sent at once with age zero", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.doc.emit("keydown");
  assert.deepEqual(sends, [0]);
});

test("input 10 s and 20 s after a send yields one send at 30 s carrying the newest age", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.doc.emit("keydown");
  assert.deepEqual(sends, [0]);
  timers.advance(10_000);
  page.doc.emit("pointerdown");
  timers.advance(10_000);
  page.doc.emit("pointermove");
  timers.advance(9999);
  assert.deepEqual(sends, [0], "nothing is sent before the window ends");
  timers.advance(1);
  assert.deepEqual(sends, [0, 10_000]);
});

test("input 30 s or more after a send is sent at once", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.doc.emit("keydown");
  timers.advance(30_000);
  page.doc.emit("keydown");
  assert.deepEqual(sends, [0, 0]);
});

test("no input after a send yields no further send", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.doc.emit("keydown");
  timers.advance(120_000);
  assert.deepEqual(sends, [0]);
});

test("keydown, pointerdown, pointermove, and wheel each count as input", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  for (const type of ["keydown", "pointerdown", "pointermove", "wheel"]) {
    timers.advance(30_000);
    page.doc.emit(type);
  }
  assert.deepEqual(sends, [0, 0, 0, 0]);
});

test("a scroll event is not input and sends nothing", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.doc.emit("scroll");
  timers.advance(60_000);
  assert.deepEqual(sends, []);
});

test("input while hidden is ignored, so showing the page sends nothing", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.state.visibilityState = "hidden";
  page.doc.emit("keydown");
  timers.advance(40_000);
  assert.deepEqual(sends, []);

  page.state.visibilityState = "visible";
  page.doc.emit("visibilitychange");
  timers.advance(40_000);
  assert.deepEqual(sends, [], "showing the page without new input sends nothing");
});

test("hiding with a send pending drops it", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.doc.emit("keydown");
  timers.advance(10_000);
  page.doc.emit("keydown");
  assert.deepEqual(sends, [0]);
  timers.advance(5000);
  page.state.visibilityState = "hidden";
  page.doc.emit("visibilitychange");
  timers.advance(30_000);
  assert.deepEqual(sends, [0]);
});

test("a send made at the window end is not repeated without new input", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.doc.emit("keydown");
  timers.advance(10_000);
  page.doc.emit("keydown");
  timers.advance(20_000);
  assert.deepEqual(sends, [0, 20_000]);
  timers.advance(60_000);
  assert.deepEqual(sends, [0, 20_000]);
});

test("stop removes every listener and the pending send", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  const watch = watchPresence(page.seenWindow, (request) => sends.push(Number(request.inputAgeMs)), timers.clock, noView);

  page.doc.emit("keydown");
  timers.advance(10_000);
  page.doc.emit("keydown");
  assert.equal(timers.pending(), 1, "a send is pending before stop");
  watch.stop();

  assert.equal(page.win.count(), 0);
  assert.equal(page.doc.count(), 0);
  assert.equal(timers.pending(), 0);
  timers.advance(60_000);
  assert.deepEqual(sends, [0]);
});

test("viewingOf maps a pane row to viewingPane with its pane id", () => {
  assert.deepEqual(viewingOf({ kind: "pane", id: "%3" }), { case: "viewingPane", value: "%3" });
});

test("viewingOf maps a session row and a step to viewingSession with the session id", () => {
  assert.deepEqual(viewingOf({ kind: "session", id: "sess-1" }), { case: "viewingSession", value: "sess-1" });
  assert.deepEqual(
    viewingOf({ kind: "step", id: "sess-2" }),
    { case: "viewingSession", value: "sess-2" },
    "a step's id is its session's",
  );
});

test("viewingOf maps an epic entry to viewingEpic with the epic id", () => {
  assert.deepEqual(viewingOf({ kind: "epic", id: "widgets/quiet" }), { case: "viewingEpic", value: "widgets/quiet" });
});

test("viewingOf maps no entry to no viewing key", () => {
  assert.equal(viewingOf(undefined).case, undefined);
  assert.equal(viewingOf(null).case, undefined);
});

test("viewing: a send while visible and focused carries the key the getter returns", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  view.set({ case: "viewingPane", value: "%3" });
  watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  page.doc.emit("keydown");
  assert.equal(requests.length, 1);
  assert.equal(Number(requests[0].inputAgeMs), 0);
  assert.deepEqual(requests[0].viewing, { case: "viewingPane", value: "%3" });
});

test("viewing: a send while unfocused carries no viewing key", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  view.set({ case: "viewingSession", value: "sess-1" });
  watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  page.state.focused = false;
  page.doc.emit("keydown");
  assert.equal(requests.length, 1);
  assert.equal(requests[0].viewing.case, undefined, "an unfocused page sends no key");

  page.state.focused = true;
  timers.advance(30_000);
  page.doc.emit("keydown");
  assert.equal(requests[1].viewing.case, "viewingSession", "a focused page sends its key again");
});

test("viewing: the window-end send carries no viewing key when the page is unfocused at its send", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  view.set({ case: "viewingEpic", value: "widgets/quiet" });
  watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  page.doc.emit("keydown");
  timers.advance(10_000);
  page.doc.emit("keydown");
  page.state.focused = false;
  timers.advance(20_000);
  assert.equal(requests.length, 2);
  assert.equal(Number(requests[1].inputAgeMs), 20_000);
  assert.equal(requests[1].viewing.case, undefined, "the window-end send is unfocused, so it carries no key");
});

test("viewing: the window-end send carries the key when the page is focused at its send", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  view.set({ case: "viewingEpic", value: "widgets/quiet" });
  watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  page.doc.emit("keydown");
  timers.advance(10_000);
  page.doc.emit("keydown");
  timers.advance(20_000);
  assert.equal(requests.length, 2);
  assert.deepEqual(requests[1].viewing, { case: "viewingEpic", value: "widgets/quiet" });
});

test("viewing: nothing is sent while hidden, whatever the view", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  view.set({ case: "viewingPane", value: "%3" });
  const watch = watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  // A visible input sets the newest input, so a view change below reaches the focus check rather than stopping at
  // "no input yet".
  page.doc.emit("keydown");
  assert.equal(requests.length, 1);

  timers.advance(5000);
  page.state.visibilityState = "hidden";
  page.doc.emit("visibilitychange");
  view.set({ case: "viewingPane", value: "%4" });
  watch.viewChanged();
  assert.equal(requests.length, 1, "a view change while hidden sends nothing");

  page.doc.emit("keydown");
  timers.advance(60_000);
  assert.equal(requests.length, 1, "input and time while hidden send nothing");
});

test("viewing: a view change while visible and focused sends at once with the newest input's age", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  const watch = watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  page.doc.emit("keydown");
  assert.equal(requests.length, 1);
  timers.advance(5000);
  view.set({ case: "viewingPane", value: "%3" });
  watch.viewChanged();
  assert.equal(requests.length, 2, "the view change sends without waiting for the window's end");
  assert.equal(Number(requests[1].inputAgeMs), 5000);
  assert.deepEqual(requests[1].viewing, { case: "viewingPane", value: "%3" });
});

test("viewing: a view change to the key already sent sends nothing", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  view.set({ case: "viewingPane", value: "%3" });
  const watch = watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  page.doc.emit("keydown");
  watch.viewChanged();
  assert.equal(requests.length, 1);
});

test("viewing: a view change before any input sends nothing", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  const watch = watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  view.set({ case: "viewingPane", value: "%3" });
  watch.viewChanged();
  assert.deepEqual(requests, [], "with no input there is no newest input to age");
});

test("viewing: a view change while unfocused sends nothing, and the next send carries the view then", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  view.set({ case: "viewingPane", value: "%3" });
  const watch = watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  page.doc.emit("keydown");
  page.state.focused = false;
  view.set({ case: "viewingPane", value: "%4" });
  watch.viewChanged();
  assert.equal(requests.length, 1, "an unfocused view change sends nothing");

  page.state.focused = true;
  timers.advance(30_000);
  page.doc.emit("keydown");
  assert.deepEqual(requests[1].viewing, { case: "viewingPane", value: "%4" });
});

test("viewing: a view change after stop sends nothing", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const requests: PresenceReportRequest[] = [];
  const view = viewState();
  const watch = watchPresence(page.seenWindow, (request) => requests.push(request), timers.clock, view.get);

  page.doc.emit("keydown");
  watch.stop();
  view.set({ case: "viewingPane", value: "%3" });
  watch.viewChanged();
  assert.equal(requests.length, 1);
});
