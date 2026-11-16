// until polls check until it holds. It has no deadline of its own: the work it waits for runs at the machine's pace,
// and the web suite's --test-timeout fails, named, a test whose wait never ends. When ended is given, it fails at once,
// naming what never happened, once ended reports that the producer whose work check waits for has ended while check
// still does not hold, since check can no longer come true and waiting on would only turn the failure into a hang. Each
// round waits on step, a short pause by default; a component test passes one that runs the pause inside act, so the
// renders it lets through are applied.
export async function until(
  check: () => boolean,
  what: string,
  options: { ended?: () => boolean; step?: () => Promise<void> } = {},
): Promise<void> {
  const step = options.step ?? pause;
  while (!check()) {
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
