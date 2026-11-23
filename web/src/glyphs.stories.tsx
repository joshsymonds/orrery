// The stories of the glyphs, under round-6 names: each state of a state glyph, each gauge state, and every icon.

import type { Stories } from "./catalog/story";
import * as glyphs from "./glyphs";

export const StateGlyph: Stories = {
  broken: () => <glyphs.StateGlyph state="broken" />,
  done: () => <glyphs.StateGlyph state="done" />,
  failed: () => <glyphs.StateGlyph state="failed" />,
  held: () => <glyphs.StateGlyph state="held" />,
  idle: () => <glyphs.StateGlyph state="idle" />,
  interrupted: () => <glyphs.StateGlyph state="interrupted" />,
  interrupting: () => <glyphs.StateGlyph state="interrupting" />,
  limited: () => <glyphs.StateGlyph state="limited" />,
  stopped: () => <glyphs.StateGlyph state="stopped" />,
  waiting: () => <glyphs.StateGlyph state="waiting" />,
  working: () => <glyphs.StateGlyph state="working" />,
};

export const Gauge: Stories = {
  building: () => <glyphs.Gauge done={2} started={3} total={7} tone="live" />,
  "asks-you": () => <glyphs.Gauge done={2} started={3} total={7} tone="attention" inner="dot" />,
  "no-tasks": () => <glyphs.Gauge total={0} tone="live" />,
  paused: () => <glyphs.Gauge done={5} started={1} total={9} />,
  releasing: () => <glyphs.Gauge done={4} total={4} tone="engine" />,
  shipped: () => <glyphs.Gauge done={4} total={4} tone="ok" inner="check" />,
  "ended-short": () => <glyphs.Gauge done={0} started={1} total={3} inner="dash" />,
};

export const Icons: Stories = {
  all: () => (
    <div>
      {glyphs.ICON_NAMES.map((name) => (
        <span class="ink-2" title={name}>
          <glyphs.Icon name={name} />
        </span>
      ))}
    </div>
  ),
};
