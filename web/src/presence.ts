import { create } from "@bufbuild/protobuf";

import { type PresenceReportRequest, PresenceReportRequestSchema } from "./gen/orrery/v1/orrery_pb";
import type { SeenClock, SeenWindow } from "./seen";

const WINDOW_MS = 30_000;
const INPUT_EVENTS = ["keydown", "pointerdown", "pointermove", "wheel"];

// The viewing oneof of a Presence.Report request: the key of what the page shows, or no key.
export type Viewing = PresenceReportRequest["viewing"];

// The entry the cursor rests on, as the viewing key reads it: a row's kind and id, a step's, or an epic's.
export interface ViewedEntry {
  kind: "pane" | "session" | "epic" | "step";
  id: string;
}

// viewingOf maps the cursor's entry to the viewing key of its report. A pane row keys its pane, a session row or a
// step keys its session (a step's id is its session's), and an epic entry keys its epic. No entry keys nothing.
export function viewingOf(entry: ViewedEntry | null | undefined): Viewing {
  if (!entry) {
    return { case: undefined };
  }
  switch (entry.kind) {
    case "pane":
      return { case: "viewingPane", value: entry.id };
    case "session":
    case "step":
      return { case: "viewingSession", value: entry.id };
    case "epic":
      return { case: "viewingEpic", value: entry.id };
  }
}

// Reports send a Presence.Report request while the page is visible: its input age, the milliseconds since the newest
// input, and the key of the view it shows. The first input after WINDOW_MS without a send goes at once. Input within
// the window is sent once at the window's end, with the age of the newest input. Nothing is sent without new input,
// nothing while hidden, and hiding drops a pending send. Scroll is not input: the reader sets scrollTop itself.
//
// A request carries the key from viewing only while the page is visible and focused at the moment it is sent, and
// carries no key otherwise. The returned viewChanged is called when the view the page shows changes. While the page is
// visible and focused, and after some input, it sends at once with the newest input's age when the key differs from
// the last one sent. Without any input there is no newest input to age, so it sends nothing. Hiding and blurring send
// nothing; the key then lapses on the server's own window.
export function watchPresence(
  win: SeenWindow,
  send: (request: PresenceReportRequest) => void,
  clock: SeenClock,
  viewing: () => Viewing,
): { stop(): void; viewChanged(): void } {
  const doc = win.document;
  let newest: number | undefined;
  let lastSent: number | undefined;
  let lastViewing: Viewing = { case: undefined };
  let timer: { handle: unknown } | undefined;
  let stopped = false;

  function cancel() {
    if (timer !== undefined) {
      clock.clearTimeout(timer.handle);
      timer = undefined;
    }
  }

  // focused reports whether the page is visible and its document focused, the state the viewing key needs.
  function focused(): boolean {
    return doc.visibilityState === "visible" && doc.hasFocus();
  }

  function sendAt(now: number, age: number, key: Viewing) {
    lastSent = now;
    lastViewing = key;
    send(create(PresenceReportRequestSchema, { inputAgeMs: BigInt(age), viewing: key }));
  }

  function flush() {
    timer = undefined;
    if (newest === undefined) return;
    const now = clock.now();
    sendAt(now, now - newest, focused() ? viewing() : { case: undefined });
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

  function viewChanged() {
    if (stopped || newest === undefined || !focused()) return;
    const key = viewing();
    if (key.case === lastViewing.case && key.value === lastViewing.value) return;
    const now = clock.now();
    sendAt(now, now - newest, key);
  }

  for (const type of INPUT_EVENTS) doc.addEventListener(type, onInput);
  doc.addEventListener("visibilitychange", onVisibility);

  return {
    stop() {
      stopped = true;
      cancel();
      for (const type of INPUT_EVENTS) doc.removeEventListener(type, onInput);
      doc.removeEventListener("visibilitychange", onVisibility);
    },
    viewChanged,
  };
}
