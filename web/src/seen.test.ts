import assert from "node:assert/strict";
import { test } from "node:test";

import { watchSeen, type SeenClock, type SeenWindow } from "./seen";

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

test("a resting unseen row with recent input is written once, one second after resting", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("keydown");
  timers.advance(5000);
  watch.rest("row-a", true);

  timers.advance(999);
  assert.deepEqual(writes, []);
  timers.advance(1);
  assert.deepEqual(writes, ["row-a"]);

  page.doc.emit("keydown");
  timers.advance(5000);
  watch.rest("row-a", true);
  timers.advance(5000);
  assert.deepEqual(writes, ["row-a"], "a row is written once, not again on later rests or input");
});

test("steady pointer movement does not postpone the write past one second of resting", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("pointermove");
  watch.rest("row-a", true);
  for (let step = 0; step < 9; step++) {
    timers.advance(100);
    page.doc.emit("pointermove");
  }
  timers.advance(99);
  assert.deepEqual(writes, []);
  timers.advance(1);
  assert.deepEqual(writes, ["row-a"]);
});

test("a hidden page writes nothing until the page is visible and input arrives", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.state.visibilityState = "hidden";
  page.doc.emit("keydown");
  watch.rest("row-a", true);
  timers.advance(2000);
  assert.deepEqual(writes, []);

  page.state.visibilityState = "visible";
  page.doc.emit("pointerdown");
  timers.advance(999);
  assert.deepEqual(writes, []);
  timers.advance(1);
  assert.deepEqual(writes, ["row-a"]);
});

test("an unfocused document writes nothing until it is focused and input arrives", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.state.focused = false;
  page.doc.emit("keydown");
  watch.rest("row-a", true);
  timers.advance(2000);
  assert.deepEqual(writes, []);

  page.state.focused = true;
  page.doc.emit("wheel");
  timers.advance(999);
  assert.deepEqual(writes, []);
  timers.advance(1);
  assert.deepEqual(writes, ["row-a"]);
});

test("input older than 30 seconds writes nothing until new input arrives", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("keydown");
  timers.advance(30_001);
  watch.rest("row-a", true);
  timers.advance(2000);
  assert.deepEqual(writes, []);

  page.doc.emit("pointermove");
  timers.advance(999);
  assert.deepEqual(writes, []);
  timers.advance(1);
  assert.deepEqual(writes, ["row-a"]);
});

test("input exactly 30 seconds old still counts", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("keydown");
  timers.advance(29_000);
  watch.rest("row-a", true);
  timers.advance(1000);
  assert.deepEqual(writes, ["row-a"]);
});

test("a window focus re-arms the check, so the write waits one second after it", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.state.focused = false;
  page.doc.emit("keydown");
  watch.rest("row-a", true);
  timers.advance(500);
  page.state.focused = true;
  page.win.emit("focus");
  timers.advance(999);
  assert.deepEqual(writes, []);
  timers.advance(1);
  assert.deepEqual(writes, ["row-a"]);
});

test("a visibility change re-arms the check, so the write waits one second after it", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.state.visibilityState = "hidden";
  page.doc.emit("keydown");
  watch.rest("row-a", true);
  timers.advance(500);
  page.state.visibilityState = "visible";
  page.doc.emit("visibilitychange");
  timers.advance(999);
  assert.deepEqual(writes, []);
  timers.advance(1);
  assert.deepEqual(writes, ["row-a"]);
});

test("resting on a seen row writes nothing and cancels a pending write", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("keydown");
  watch.rest("row-a", true);
  timers.advance(500);
  watch.rest("row-a", false);
  timers.advance(5000);
  assert.deepEqual(writes, []);
});

test("moving to another row before one second cancels the first row and times the second", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("keydown");
  watch.rest("row-a", true);
  timers.advance(500);
  watch.rest("row-b", true);
  timers.advance(500);
  assert.deepEqual(writes, [], "row-a's original deadline passes without a write");
  timers.advance(500);
  assert.deepEqual(writes, ["row-b"]);
});

test("repeating the rest call for the same row does not restart its one second", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("keydown");
  watch.rest("row-a", true);
  timers.advance(600);
  watch.rest("row-a", true);
  timers.advance(400);
  assert.deepEqual(writes, ["row-a"]);
});

test("stop removes every listener and the pending timer", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("keydown");
  watch.rest("row-a", true);
  timers.advance(500);
  assert.ok(page.win.count() + page.doc.count() > 0, "the watch registers listeners");
  assert.equal(timers.pending(), 1, "the watch holds a pending timer before stop");
  watch.stop();

  assert.equal(page.win.count(), 0);
  assert.equal(page.doc.count(), 0);
  assert.equal(timers.pending(), 0);
  timers.advance(5000);
  assert.deepEqual(writes, []);
});

test("rest after stop writes nothing and arms no timer", () => {
  const timers = fakeClock();
  const page = fakeWindow();
  const writes: string[] = [];
  const watch = watchSeen(page.seenWindow, (key) => writes.push(key), timers.clock);

  page.doc.emit("keydown");
  watch.stop();
  watch.rest("row-a", true);
  assert.equal(timers.pending(), 0);
  timers.advance(5000);
  assert.deepEqual(writes, []);
});
