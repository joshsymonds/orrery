// Round 6 wireframe: the icon set and the state glyphs. Every drawing is on a 16 grid, stroked 1.5 in currentColor, so
// an icon takes its colour from the text around it and both clients can draw the same paths.
(() => {
  'use strict';
  const { esc } = O;

  // name: the path data. A part that starts with "fill:" is filled, not stroked.
  const PATHS = {
    back: ['M10 3 5 8l5 5'],
    chevronRight: ['M6 3l5 5-5 5'],
    chevronDown: ['M3 6l5 5 5-5'],
    chevronUp: ['M3 10l5-5 5 5'],
    close: ['M4 4l8 8M12 4l-8 8'],
    more: ['fill:M3.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM8 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM12.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2z'],
    send: ['M8 13V3M4 7l4-4 4 4'],
    sidebar: ['M4 3h8a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM6 3v10'],
    search: ['M7 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM10 10l3.5 3.5'],
    plus: ['M8 3v10M3 8h10'],
    check: ['M3.5 8.5l3 3 6-7'],
    cross: ['M4.5 4.5l7 7M11.5 4.5l-7 7'],
    stop: ['fill:M5 4h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z'],
    pause: ['M6 4v8M10 4v8'],
    play: ['fill:M5 3.5l7 4.5-7 4.5z'],
    commit: ['M5 2.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM5 10.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM11 4.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM5 5.5v5M11 7.5c0 2.5-6 1-6 3'],
    doc: ['M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 11h4'],
    chat: ['M3 3h10v7H8l-3 3v-3H3z'],
    gear: ['M8 5.8a2.2 2.2 0 1 0 0 4.4 2.2 2.2 0 0 0 0-4.4zM8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4'],
    pools: ['M8 2l6 3-6 3-6-3zM2 8l6 3 6-3M2 11l6 3 6-3'],
    archive: ['M2 3h12v3H2zM3 6v7h10V6M6.5 9h3'],
    open: ['M6 4h6v6M12 4l-8 8'],
    clock: ['M8 2.5a5.5 5.5 0 1 0 0 11 5.5 5.5 0 0 0 0-11zM8 5v3l2 1.5'],
    shell: ['M3 4l4 4-4 4M8.5 12H13'],
    agent: ['M8 2l1.5 4.5L14 8l-4.5 1.5L8 14l-1.5-4.5L2 8l4.5-1.5z'],
    warn: ['M8 2.5l6 10.5H2zM8 6.5v3M8 11.2v.3'],
    split: ['M4 3h8a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM8 3v10'],
    retry: ['M3.2 8a4.8 4.8 0 1 0 1.5-3.5M3 3v3.2h3.2'],
    bell: ['M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 13.5a1.5 1.5 0 0 0 3 0'],
    folder: ['M2 4h4l1.5 1.5H14v7H2z'],
    phone: ['M6.5 2h3A1.5 1.5 0 0 1 11 3.5v9A1.5 1.5 0 0 1 9.5 14h-3A1.5 1.5 0 0 1 5 12.5v-9A1.5 1.5 0 0 1 6.5 2zM7.5 11.5h1'],
    edit: ['M3 13l.5-2.5L11 3l2 2-7.5 7.5z'],
    goal: ['M8 2.5a5.5 5.5 0 1 0 0 11 5.5 5.5 0 0 0 0-11zM8 6a2 2 0 1 0 0 4 2 2 0 0 0 0-4z'],
    keys: ['M3.5 4h9A1.5 1.5 0 0 1 14 5.5v5a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 10.5v-5A1.5 1.5 0 0 1 3.5 4zM5 7h.5M7.8 7h.5M10.6 7h.5M5.5 9.5h5'],
    person: ['M8 2.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5zM3 13.5c0-2.5 2.2-4 5-4s5 1.5 5 4'],
    question: ['M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2zM6.3 6.4a1.8 1.8 0 1 1 2.6 1.6c-.6.3-.9.7-.9 1.3M8 11.3v.2'],
    host: ['M3 3h10v4H3zM3 9h10v4H3zM5.5 5h.5M5.5 11h.5'],
  };

  // icon draws a named icon at the icon size. label, when given, is its accessible name; without one it is hidden.
  O.icon = (name, label) => {
    const parts = PATHS[name];
    if (!parts) throw new Error(`icon: no drawing named ${name}`);
    const body = parts.map((part) => (part.startsWith('fill:')
      ? `<path d="${part.slice(5)}" fill="currentColor" stroke="none"/>`
      : `<path d="${part}"/>`)).join('');
    const name_ = label ? ` role="img" aria-label="${esc(label)}"` : ' aria-hidden="true"';
    return `<svg class="icon" viewBox="0 0 16 16"${name_}>${body}</svg>`;
  };
  O.iconNames = Object.keys(PATHS);

  // The eleven states of a session or a step, one drawing each. Held and limited share a drawing and differ in colour:
  // held waits on you, limited waits on a usage limit and asks nothing. Unreachable is a dashed ring, not stopped's
  // square: its host does not answer, so nothing was stopped. The colour is the state's, set by the data-state rule.
  const RING = '<circle cx="8" cy="8" r="6.25"/>';
  const GLYPHS = {
    working: '<circle class="track" cx="8" cy="8" r="6.25"/><path class="arc" d="M8 1.75a6.25 6.25 0 0 1 6.25 6.25"/>',
    waiting: '<circle class="solid" cx="8" cy="8" r="7"/><path class="on" d="M6.1 6.3a1.9 1.9 0 1 1 2.8 1.7c-.6.3-.9.7-.9 1.4M8 11.6v.2"/>',
    held: `${RING}<path d="M6.5 5.5v5M9.5 5.5v5"/>`,
    limited: `${RING}<path d="M6.5 5.5v5M9.5 5.5v5"/>`,
    done: `${RING}<path d="M5 8.2l2.1 2.1 4-4.4"/>`,
    interrupted: `${RING}<path d="M8 4.6v4.2M8 11.1v.3"/>`,
    interrupting: `${RING}<path d="M8 4.6v4.2M8 11.1v.3"/>`,
    failed: `${RING}<path d="M5.8 5.8l4.4 4.4M10.2 5.8l-4.4 4.4"/>`,
    stopped: `${RING}<rect class="solid" x="5.75" y="5.75" width="4.5" height="4.5" rx=".5"/>`,
    unreachable: '<circle cx="8" cy="8" r="6.25" stroke-dasharray="2.45 2.45"/>',
    idle: RING,
  };
  O.STATES = Object.keys(GLYPHS);
  O.STATE_WORDS = {
    working: 'working', waiting: 'waiting on you', held: 'held', limited: 'waiting on a limit', done: 'done', interrupted: 'cut off',
    interrupting: 'interrupting', failed: 'failed', stopped: 'stopped', unreachable: 'unreachable', idle: 'idle',
  };

  // stateGlyph draws a state. Its word is its accessible name, so a glyph is never the only way to learn the state.
  O.stateGlyph = (state) => {
    const drawing = GLYPHS[state] || GLYPHS.idle;
    const word = O.STATE_WORDS[state] || 'idle';
    return `<svg class="glyph" data-state="${esc(GLYPHS[state] ? state : 'idle')}" viewBox="0 0 16 16" role="img" aria-label="${esc(word)}"><title>${esc(word)}</title>${drawing}</svg>`;
  };

  // mark draws the orrery mark.
  O.mark = (extra = '') => `<svg class="mark ${extra}" viewBox="18 18 72 72" role="img" aria-label="orrery">${O.MARK}</svg>`;
})();
