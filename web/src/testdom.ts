import { Window } from "happy-dom";

const globalNames = ["window", "document", "Node", "Element", "HTMLElement", "Event", "KeyboardEvent"] as const;

// installTestDom makes a fresh happy-dom window the global DOM for a component test and returns it.
export function installTestDom(): Window {
  const window = new Window({ url: "https://orrery.test/" });
  const target = globalThis as unknown as Record<string, unknown>;
  target.window = window;
  target.document = window.document;
  target.Node = window.Node;
  target.Element = window.Element;
  target.HTMLElement = window.HTMLElement;
  target.Event = window.Event;
  target.KeyboardEvent = window.KeyboardEvent;
  return window;
}

// removeTestDom removes the globals installTestDom set, while they are still this window's, and closes the window. A
// removal that comes late, once the next test has installed its own DOM, leaves that DOM in place.
export async function removeTestDom(window: Window): Promise<void> {
  const target = globalThis as unknown as Record<string, unknown>;
  if (target.window === window) {
    for (const name of globalNames) {
      delete target[name];
    }
  }
  await window.close();
}

// teardownOnce returns the function that runs teardown once, which signal, the test's own, also runs when it aborts.
// node:test aborts a test's signal when the test times out, and starts the next test without waiting for the timed-out
// one's body, so a page that waits for its own end to stop would run on into the next test, and its removed DOM would
// fail that test in its place. Run on the abort, the teardown stops the page before the next test starts, and the
// timed-out test's own call later finds it done.
export function teardownOnce(signal: AbortSignal, teardown: () => Promise<void>): () => Promise<void> {
  let done: Promise<void> | null = null;
  const run = (): Promise<void> => {
    if (done === null) {
      signal.removeEventListener("abort", onAbort);
      done = teardown();
    }
    return done;
  };
  const onAbort = () => {
    void run();
  };
  if (signal.aborted) {
    void run();
  } else {
    signal.addEventListener("abort", onAbort, { once: true });
  }
  return run;
}
