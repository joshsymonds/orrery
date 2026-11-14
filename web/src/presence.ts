import type { SeenClock, SeenWindow } from "./seen";

const WINDOW_MS = 30_000;
const INPUT_EVENTS = ["keydown", "pointerdown", "pointermove", "wheel"];

// Reports send(ageMs), the milliseconds since the newest input, while the page is visible. The first
// input after WINDOW_MS without a send goes at once. Input within the window is sent once at the
// window's end, with the age of the newest input. Nothing is sent without new input, nothing while
// hidden, and hiding drops a pending send. Scroll is not input: the reader sets scrollTop itself.
export function watchPresence(
  win: SeenWindow,
  send: (ageMs: number) => void,
  clock: SeenClock,
): { stop(): void } {
  const doc = win.document;
  let newest = 0;
  let lastSent: number | undefined;
  let timer: { handle: unknown } | undefined;

  function cancel() {
    if (timer !== undefined) {
      clock.clearTimeout(timer.handle);
      timer = undefined;
    }
  }

  function flush() {
    timer = undefined;
    const now = clock.now();
    lastSent = now;
    send(now - newest);
  }

  function onInput() {
    if (doc.visibilityState !== "visible") return;
    const now = clock.now();
    newest = now;
    if (timer !== undefined) return;
    if (lastSent === undefined || now - lastSent >= WINDOW_MS) {
      flush();
      return;
    }
    timer = { handle: clock.setTimeout(flush, lastSent + WINDOW_MS - now) };
  }

  function onVisibility() {
    if (doc.visibilityState === "visible") return;
    cancel();
  }

  for (const type of INPUT_EVENTS) doc.addEventListener(type, onInput);
  doc.addEventListener("visibilitychange", onVisibility);

  return {
    stop() {
      cancel();
      for (const type of INPUT_EVENTS) doc.removeEventListener(type, onInput);
      doc.removeEventListener("visibilitychange", onVisibility);
    },
  };
}
