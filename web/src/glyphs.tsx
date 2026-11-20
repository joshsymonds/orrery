// The round-6 glyphs: the icon set, the state glyphs and an epic's gauge. Every drawing is on a 16 grid
// (the gauge's on 24), stroked in currentColor, so each takes its colour from the text around it. The data-state and
// data-tone attributes carry the colour rules the stylesheet sets.

import type { JSX } from "preact";

// IconPath is one icon's drawing: its path data, and whether the path is filled rather than stroked.
interface IconPath {
  readonly d: string;
  readonly fill?: true;
}

// defineIcons keeps each icon's name as its own key, so IconName is the union of the names, and checks each drawing's shape.
function defineIcons<const T extends Record<string, IconPath>>(icons: T): T {
  return icons;
}

// ICONS is every icon by name, with its path data.
const ICONS = defineIcons({
  back: { d: "M10 3 5 8l5 5" },
  chevronRight: { d: "M6 3l5 5-5 5" },
  chevronDown: { d: "M3 6l5 5 5-5" },
  chevronUp: { d: "M3 10l5-5 5 5" },
  close: { d: "M4 4l8 8M12 4l-8 8" },
  more: { d: "M3.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM8 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM12.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2z", fill: true },
  send: { d: "M8 13V3M4 7l4-4 4 4" },
  sidebar: { d: "M4 3h8a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM6 3v10" },
  search: { d: "M7 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM10 10l3.5 3.5" },
  plus: { d: "M8 3v10M3 8h10" },
  check: { d: "M3.5 8.5l3 3 6-7" },
  cross: { d: "M4.5 4.5l7 7M11.5 4.5l-7 7" },
  stop: { d: "M5 4h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z", fill: true },
  pause: { d: "M6 4v8M10 4v8" },
  play: { d: "M5 3.5l7 4.5-7 4.5z", fill: true },
  commit: { d: "M5 2.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM5 10.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM11 4.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM5 5.5v5M11 7.5c0 2.5-6 1-6 3" },
  doc: { d: "M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 11h4" },
  chat: { d: "M3 3h10v7H8l-3 3v-3H3z" },
  gear: { d: "M8 5.8a2.2 2.2 0 1 0 0 4.4 2.2 2.2 0 0 0 0-4.4zM8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4" },
  pools: { d: "M8 2l6 3-6 3-6-3zM2 8l6 3 6-3M2 11l6 3 6-3" },
  archive: { d: "M2 3h12v3H2zM3 6v7h10V6M6.5 9h3" },
  open: { d: "M6 4h6v6M12 4l-8 8" },
  clock: { d: "M8 2.5a5.5 5.5 0 1 0 0 11 5.5 5.5 0 0 0 0-11zM8 5v3l2 1.5" },
  shell: { d: "M3 4l4 4-4 4M8.5 12H13" },
  agent: { d: "M8 2l1.5 4.5L14 8l-4.5 1.5L8 14l-1.5-4.5L2 8l4.5-1.5z" },
  warn: { d: "M8 2.5l6 10.5H2zM8 6.5v3M8 11.2v.3" },
  split: { d: "M4 3h8a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM8 3v10" },
  retry: { d: "M3.2 8a4.8 4.8 0 1 0 1.5-3.5M3 3v3.2h3.2" },
  bell: { d: "M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 13.5a1.5 1.5 0 0 0 3 0" },
  folder: { d: "M2 4h4l1.5 1.5H14v7H2z" },
  phone: { d: "M6.5 2h3A1.5 1.5 0 0 1 11 3.5v9A1.5 1.5 0 0 1 9.5 14h-3A1.5 1.5 0 0 1 5 12.5v-9A1.5 1.5 0 0 1 6.5 2zM7.5 11.5h1" },
  edit: { d: "M3 13l.5-2.5L11 3l2 2-7.5 7.5z" },
  goal: { d: "M8 2.5a5.5 5.5 0 1 0 0 11 5.5 5.5 0 0 0 0-11zM8 6a2 2 0 1 0 0 4 2 2 0 0 0 0-4z" },
  keys: { d: "M3.5 4h9A1.5 1.5 0 0 1 14 5.5v5a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 10.5v-5A1.5 1.5 0 0 1 3.5 4zM5 7h.5M7.8 7h.5M10.6 7h.5M5.5 9.5h5" },
  person: { d: "M8 2.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5zM3 13.5c0-2.5 2.2-4 5-4s5 1.5 5 4" },
  question: { d: "M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2zM6.3 6.4a1.8 1.8 0 1 1 2.6 1.6c-.6.3-.9.7-.9 1.3M8 11.3v.2" },
  host: { d: "M3 3h10v4H3zM3 9h10v4H3zM5.5 5h.5M5.5 11h.5" },
});

// IconName is the name of an icon in ICONS.
export type IconName = keyof typeof ICONS;

// Icon draws a named icon at the icon size. label, when given, is its accessible name; without one it is hidden.
export function Icon({ name, label }: { name: IconName; label?: string }): JSX.Element {
  const path: IconPath = ICONS[name];
  const named = label !== undefined;
  return (
    <svg
      class="icon"
      viewBox="0 0 16 16"
      role={named ? "img" : undefined}
      aria-label={label}
      aria-hidden={named ? undefined : "true"}
    >
      {path.fill ? <path d={path.d} fill="currentColor" stroke="none" /> : <path d={path.d} />}
    </svg>
  );
}

// STATE_WORDS is each state's word: a glyph's accessible name, and the word beside it.
const STATE_WORDS: Readonly<Record<string, string>> = {
  working: "working",
  waiting: "waiting on you",
  held: "held",
  limited: "waiting on a limit",
  done: "done",
  interrupted: "cut off",
  interrupting: "interrupting",
  failed: "failed",
  stopped: "stopped",
  idle: "idle",
};

// stateDrawing draws a state's ring. Held and limited share a drawing and differ in colour: held waits on you, limited
// waits on a usage limit and asks nothing. Any state it does not name draws the idle ring.
function stateDrawing(state: string): JSX.Element {
  switch (state) {
    case "working":
      return (
        <>
          <circle class="track" cx="8" cy="8" r="6.25" />
          <path class="arc" d="M8 1.75a6.25 6.25 0 0 1 6.25 6.25" />
        </>
      );
    case "waiting":
      return (
        <>
          <circle class="solid" cx="8" cy="8" r="7" />
          <path class="on" d="M6.1 6.3a1.9 1.9 0 1 1 2.8 1.7c-.6.3-.9.7-.9 1.4M8 11.6v.2" />
        </>
      );
    case "held":
    case "limited":
      return (
        <>
          <circle cx="8" cy="8" r="6.25" />
          <path d="M6.5 5.5v5M9.5 5.5v5" />
        </>
      );
    case "done":
      return (
        <>
          <circle cx="8" cy="8" r="6.25" />
          <path d="M5 8.2l2.1 2.1 4-4.4" />
        </>
      );
    case "interrupted":
    case "interrupting":
      return (
        <>
          <circle cx="8" cy="8" r="6.25" />
          <path d="M8 4.6v4.2M8 11.1v.3" />
        </>
      );
    case "failed":
      return (
        <>
          <circle cx="8" cy="8" r="6.25" />
          <path d="M5.8 5.8l4.4 4.4M10.2 5.8l-4.4 4.4" />
        </>
      );
    case "stopped":
      return (
        <>
          <circle cx="8" cy="8" r="6.25" />
          <rect class="solid" x="5.75" y="5.75" width="4.5" height="4.5" rx=".5" />
        </>
      );
    default:
      return <circle cx="8" cy="8" r="6.25" />;
  }
}

// stateWord is a state's word, which a state it does not know reads as idle.
export function stateWord(state: string): string {
  return STATE_WORDS[Object.hasOwn(STATE_WORDS, state) ? state : "idle"];
}

// StateGlyph draws a session's or a step's state. Its word is its accessible name, so a glyph is never the only way to
// learn the state. A state it does not know is drawn as idle.
export function StateGlyph({ state }: { state: string }): JSX.Element {
  const name = Object.hasOwn(STATE_WORDS, state) ? state : "idle";
  const word = stateWord(name);
  return (
    <svg class="glyph" data-state={name} viewBox="0 0 16 16" role="img" aria-label={word}>
      <title>{word}</title>
      {stateDrawing(name)}
    </svg>
  );
}

// GaugeInner is the mark an epic's gauge draws at its centre.
export type GaugeInner = "check" | "dash" | "dot";

// Gauge draws an epic's one-ring gauge: the done share of its tasks, the started share after it, and the planned ring.
// tone is the colour the stylesheet sets; inner is the mark at the centre, if any.
export function Gauge({
  done = 0,
  started = 0,
  total = 0,
  tone,
  inner,
}: {
  done?: number;
  started?: number;
  total?: number;
  tone?: string;
  inner?: GaugeInner;
}): JSX.Element {
  const size = Math.max(1, total);
  const doneShare = (100 * done) / size;
  const startedShare = (100 * (done + started)) / size;
  const label = total ? `${done} of ${total} tasks done` : "no tasks yet";
  return (
    <svg class="gauge" viewBox="0 0 24 24" role="img" aria-label={label} data-tone={tone}>
      <title>{label}</title>
      <circle class="planned" cx="12" cy="12" r="9" stroke-dasharray={total ? undefined : "2 3"} />
      {startedShare > 0 && <Arc klass="started" share={startedShare} />}
      {doneShare > 0 && <Arc klass="done" share={doneShare} />}
      {inner === "check" && (
        <path class="done" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" d="M8.6 12.3l2.3 2.3 4.4-4.9" />
      )}
      {inner === "dash" && <path class="done" stroke-width="1.5" stroke-linecap="round" d="M9 12h6" />}
      {inner === "dot" && <circle class="core" cx="12" cy="12" r="3" />}
    </svg>
  );
}

// Arc draws one share of the gauge's ring, from the top, clockwise.
function Arc({ klass, share }: { klass: string; share: number }): JSX.Element {
  return (
    <circle
      class={klass}
      cx="12"
      cy="12"
      r="9"
      pathLength={100}
      stroke-dasharray={`${share.toFixed(1)} 100`}
      transform="rotate(-90 12 12)"
    />
  );
}

// ICON_NAMES is every icon's name, in the order ICONS lists them.
export const ICON_NAMES = Object.keys(ICONS) as IconName[];
