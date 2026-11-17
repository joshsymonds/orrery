// until polls check until it holds. It has no deadline of its own: the work it waits for runs at the machine's pace,
// and the web suite's --test-timeout fails, named, a test whose wait never ends. signal is the test's own, node:test's
// t.signal, which aborts when the test ends, by a timeout among other ways: the wait then stops polling and rejects, so
// no wait outlives its test and keeps the test file's process alive. When ended is given, it fails at once, naming what
// never happened, once ended reports that the producer whose work check waits for has ended while check still does not
// hold, since check can no longer come true and waiting on would only turn the failure into a hang. Each round waits on
// step, a short pause by default; a component test passes one that runs the pause inside act, so the renders it lets
// through are applied.
export async function until(
  signal: AbortSignal,
  check: () => boolean,
  what: string,
  options: { ended?: () => boolean; step?: () => Promise<void> } = {},
): Promise<void> {
  const step = options.step ?? pause;
  while (!check()) {
    if (signal.aborted) {
      throw new Error(`${what} never happened: the test ended first`, { cause: signal.reason });
    }
    if (options.ended?.()) {
      if (check()) {
        return;
      }
      throw new Error(`${what} never happened: what would do it ended first`);
    }
    await step();
  }
}

// pause lets the timers and promises queued behind it run once.
export function pause(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 2));
}

// turn resolves after one setImmediate turn, so the promise jobs and I/O queued before it run first. It reads no clock
// and arms no timer, so a wait stepped by turn ends on the work it waits for, never on time passing, and runs under a
// mocked setTimeout.
export function turn(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

// settledFlag returns a function that reports whether promise has settled, resolved or rejected, as an ended check for
// until: a wait on what an async producer does ends once that producer has returned.
export function settledFlag(promise: Promise<unknown>): () => boolean {
  let settled = false;
  promise.then(
    () => {
      settled = true;
    },
    () => {
      settled = true;
    },
  );
  return () => settled;
}
