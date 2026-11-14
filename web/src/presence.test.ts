import assert from "node:assert/strict";
import { test } from "node:test";

import type { SeenClock, SeenWindow } from "./seen";
import { watchPresence } from "./presence";

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
  const state = { visibilityState: "visible" };
  const seenWindow: SeenWindow = {
    document: {
      get visibilityState() {
        return state.visibilityState;
      },
      hasFocus: () => true,
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
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

  page.doc.emit("keydown");
  assert.deepEqual(sends, [0]);
});

test("input 10 s and 20 s after a send yields one send at 30 s carrying the newest age", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

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
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

  page.doc.emit("keydown");
  timers.advance(30_000);
  page.doc.emit("keydown");
  assert.deepEqual(sends, [0, 0]);
});

test("no input after a send yields no further send", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

  page.doc.emit("keydown");
  timers.advance(120_000);
  assert.deepEqual(sends, [0]);
});

test("keydown, pointerdown, pointermove, and wheel each count as input", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

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
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

  page.doc.emit("scroll");
  timers.advance(60_000);
  assert.deepEqual(sends, []);
});

test("input while hidden is ignored, so showing the page sends nothing", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const sends: number[] = [];
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

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
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

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
  watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

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
  const watch = watchPresence(page.seenWindow, (age) => sends.push(age), timers.clock);

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
