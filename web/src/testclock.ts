import type { MockTimersOptions, TestContext } from "node:test";
import { options } from "preact";

// realSetImmediate is the process's own setImmediate, taken when this module loads, before any test mocks a timer.
const realSetImmediate = setImmediate;

// mockTimers mocks apis for the test t, as t.mock.timers.enable does, and keeps the mocked clock's queue in order for
// the rest of the test. Every test that mocks timers does it through mockTimers.
//
// Node's MockTimers schedules a timer given no delay at NaN, and its queue orders a NaN timer by id alone, which breaks
// the queue's order: a tick can then stop at a later timer and leave a due one unfired, such as the page's 100 ms event
// reload, and the test waits for it forever. Preact's hooks schedule effects after paint with a timer of 100 ms followed
// by setTimeout(callback), with no delay. So mockTimers does two things while the test runs:
//
//   - A setTimeout given no delay, or a delay that is not a number, runs as setTimeout(callback, 0) does under the
//     mock, and a setInterval as one of 1 ms, since an interval of 0 would keep the mock's tick firing it forever. This
//     covers an after-paint timer started on the real clock by an earlier render, which calls the mocked setTimeout
//     when it fires.
//   - Preact runs the effects it holds for after paint on the real setImmediate instead of on the mocked clock, so they
//     run without the test advancing the clock, and an effect held when the test ends is not dropped with the mocked
//     clock's timers. The option is restored when the test ends; Preact's act sets and restores the same option, so the
//     two nest.
export function mockTimers(t: TestContext, apis: MockTimersOptions["apis"]): void {
  t.mock.timers.enable({ apis });
  if (apis.includes("setTimeout")) {
    const mocked = globalThis.setTimeout;
    t.mock.method(globalThis, "setTimeout", (callback: (...args: unknown[]) => void, delay?: unknown, ...args: unknown[]) =>
      mocked(callback, mockedDelay(delay, 0), ...args),
    );
  }
  if (apis.includes("setInterval")) {
    const mocked = globalThis.setInterval;
    t.mock.method(globalThis, "setInterval", (callback: (...args: unknown[]) => void, delay?: unknown, ...args: unknown[]) =>
      mocked(callback, mockedDelay(delay, 1), ...args),
    );
  }
  const previous = options.requestAnimationFrame;
  options.requestAnimationFrame = (callback) => {
    realSetImmediate(callback);
  };
  t.after(() => {
    options.requestAnimationFrame = previous;
  });
}

// mockedDelay is delay as a number of milliseconds, or least when delay is missing or not a number.
function mockedDelay(delay: unknown, least: number): number {
  const ms = Number(delay);
  return Number.isNaN(ms) ? least : ms;
}
