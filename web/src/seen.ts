export interface SeenClock {
  now(): number;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

export interface SeenWindow {
  document: {
    visibilityState: string;
    hasFocus(): boolean;
    addEventListener(type: string, fn: () => void): void;
    removeEventListener(type: string, fn: () => void): void;
  };
  addEventListener(type: string, fn: () => void): void;
  removeEventListener(type: string, fn: () => void): void;
}

const DWELL_MS = 1000;
const INPUT_WINDOW_MS = 30_000;
const INPUT_EVENTS = ["keydown", "pointerdown", "pointermove", "wheel"];

const realClock: SeenClock = {
  now: () => Date.now(),
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

// Decides when resting on a row counts as having seen it (decision ED8). The row is written
// once, after DWELL_MS of presence: the page visible, the document focused, and input within
// INPUT_WINDOW_MS. A window focus or a visibility change restarts the check, and input restarts it only when it
// begins presence.
export function watchSeen(
  win: SeenWindow,
  write: (key: string) => void,
  clock: SeenClock = realClock,
): {
  rest(key: string, unseen: boolean): void;
  stop(): void;
} {
  const doc = win.document;
  let key: string | undefined;
  let unseen = false;
  let written = false;
  let lastInput: number | undefined;
  let timer: { handle: unknown } | undefined;
  let stopped = false;

  function cancel() {
    if (timer !== undefined) {
      clock.clearTimeout(timer.handle);
      timer = undefined;
    }
  }

  function present(): boolean {
    return (
      doc.visibilityState === "visible" &&
      doc.hasFocus() &&
      lastInput !== undefined &&
      clock.now() - lastInput <= INPUT_WINDOW_MS
    );
  }

  function fire() {
    timer = undefined;
    if (key === undefined || !present()) return;
    written = true;
    write(key);
  }

  function arm() {
    cancel();
    if (stopped || key === undefined || !unseen || written) return;
    timer = { handle: clock.setTimeout(fire, DWELL_MS) };
  }

  // Input restarts the check only when it begins presence or none is pending, so steady input such as pointer
  // movement never postpones a dwell already counting.
  function onInput() {
    const wasPresent = present();
    lastInput = clock.now();
    if (!wasPresent || timer === undefined) arm();
  }

  // A focus or a visibility change can begin presence, so it restarts the check.
  function onRearm() {
    arm();
  }

  for (const type of INPUT_EVENTS) doc.addEventListener(type, onInput);
  doc.addEventListener("visibilitychange", onRearm);
  win.addEventListener("focus", onRearm);

  return {
    rest(next: string, nextUnseen: boolean) {
      if (stopped || (next === key && nextUnseen === unseen)) return;
      if (next !== key) {
        key = next;
        written = false;
      }
      unseen = nextUnseen;
      arm();
    },
    stop() {
      stopped = true;
      cancel();
      for (const type of INPUT_EVENTS) doc.removeEventListener(type, onInput);
      doc.removeEventListener("visibilitychange", onRearm);
      win.removeEventListener("focus", onRearm);
    },
  };
}
