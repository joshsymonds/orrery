// The stories of the glyphs, under round-6 names: each state of a state glyph, and every icon.

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
