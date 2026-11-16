// orrery mockups: the motion tokens and the few moves the pages use. Movement happens only where something changed.
// With reduced motion nothing moves: a change is at most a 100 ms opacity fade.
(() => {
  'use strict';
  const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

  // The token table. style.css carries the same values as custom properties.
  const T = { press: 120, state: 200, layout: 280, drill: 320, leave: 200, reduced: 100 };
  const E = { out: 'cubic-bezier(0.2, 0, 0, 1)', in: 'cubic-bezier(0.4, 0, 1, 1)' };

  const media = matchMedia('(prefers-reduced-motion: reduce)');
  let forced = false;
  const reduced = () => forced || media.matches;
  const sync = () => document.documentElement.classList.toggle('rm', reduced());
  media.addEventListener('change', sync);
  sync();

  const run = (el, frames, ms, easing) => el.animate(frames, { duration: ms, easing });
  const fadeIn = (el, ms = T.reduced) => run(el, [{ opacity: 0 }, { opacity: 1 }], ms, 'linear');

  // Something new arrives: it fades in and, with motion on, travels the last few pixels to its place.
  function enter(el, { x = 0, y = 0, ms = T.layout } = {}) {
    if (!el) return;
    if (reduced()) { fadeIn(el); return; }
    run(el, [{ opacity: 0, transform: `translate(${x}px, ${y}px)` }, { opacity: 1, transform: 'none' }], ms, E.out);
  }

  // A node that has left the page fades out where it stood, inside the element that held it.
  function ghost(node, rect, scope, x = 0) {
    if (reduced()) return;
    if (getComputedStyle(scope).position === 'static') scope.style.position = 'relative';
    const box = scope.getBoundingClientRect();
    [node, ...$$('[data-flip], [data-xf]', node)].forEach((n) => { n.removeAttribute('data-flip'); n.removeAttribute('data-xf'); });
    node.classList.add('mo-ghost');
    Object.assign(node.style, {
      position: 'absolute', margin: '0', pointerEvents: 'none',
      left: `${rect.left - box.left + scope.scrollLeft - scope.clientLeft}px`, top: `${rect.top - box.top + scope.scrollTop - scope.clientTop}px`,
      width: `${rect.width}px`, height: `${rect.height}px`,
    });
    scope.append(node);
    run(node, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: `translateX(${x}px)` }], T.leave, E.in).finished.then(() => node.remove());
  }

  // Old words fade out over the new ones, in place.
  function crossfade(el, oldHTML) {
    const ms = reduced() ? T.reduced : T.state;
    const fresh = document.createElement('span');
    fresh.className = 'xf-new';
    while (el.firstChild) fresh.append(el.firstChild);
    const old = document.createElement('span');
    old.className = 'xf-old';
    old.setAttribute('aria-hidden', 'true');
    old.innerHTML = oldHTML;
    el.classList.add('xf');
    el.append(fresh, old);
    run(fresh, [{ opacity: 0 }, { opacity: 1 }], ms, E.out);
    run(old, [{ opacity: 1 }, { opacity: 0 }], ms, E.in).finished.then(() => { old.remove(); el.classList.remove('xf'); });
  }

  // Redraws scope with mutate() and carries the change across. Elements are matched by data-flip:
  // one that moved glides to its new place, a new one enters, one that is gone fades where it was.
  // Text marked data-xf crossfades when its words change. A data-tone that turns "ok" settles with a soft check.
  // On an orrery, a slice whose state changed fades in: the lit slice moves inward one orbit at a time.
  function morph(scope, mutate) {
    const before = new Map();
    const words = new Map();
    const cells = new Map();
    $$('[data-cell]', scope).forEach((el) => cells.set(el.dataset.cell, el.dataset.st));
    $$('[data-flip]', scope).forEach((el) => before.set(el.dataset.flip, { rect: el.getBoundingClientRect(), node: el, tone: el.dataset.tone }));
    $$('[data-xf]', scope).forEach((el) => words.set(el.dataset.xf, { text: el.textContent, html: el.innerHTML }));
    mutate();
    const still = new Set();
    $$('[data-flip]', scope).forEach((el) => {
      const key = el.dataset.flip;
      still.add(key);
      const was = before.get(key);
      if (!was) { enter(el, { y: -6 }); return; }
      if (was.tone && was.tone !== 'ok' && el.dataset.tone === 'ok') el.classList.add('settle');
      if (reduced()) return;
      const now = el.getBoundingClientRect();
      const dx = was.rect.left - now.left;
      const dy = was.rect.top - now.top;
      if (Math.abs(dx) > 0.5 || Math.abs(dy) > 0.5) run(el, [{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], T.layout, E.out);
    });
    before.forEach((was, key) => {
      // A node inside another that also left goes with it.
      if (still.has(key) || was.node.isConnected) return;
      if (was.node.parentElement && was.node.parentElement.closest('[data-flip]')) return;
      ghost(was.node, was.rect, scope);
    });
    $$('[data-xf]', scope).forEach((el) => {
      const was = words.get(el.dataset.xf);
      if (was && was.text !== el.textContent) crossfade(el, was.html);
    });
    $$('[data-cell]', scope).forEach((el) => {
      const was = cells.get(el.dataset.cell);
      if (was !== undefined && was !== el.dataset.st) run(el, [{ opacity: 0.1 }, { opacity: 1 }], reduced() ? T.reduced : T.state, E.out);
    });
  }

  // Replaces a host's content between levels. 'in' goes deeper: the new level slides in from the right.
  // 'out' goes back. 'fade' is a plain crossfade for a change at the same level.
  function swap(host, html, kind = 'fade') {
    const rect = host.getBoundingClientRect();
    const old = document.createElement('div');
    while (host.firstChild) old.append(host.firstChild);
    const fresh = document.createElement('div');
    fresh.className = 'mo-new';
    fresh.innerHTML = html;
    host.append(fresh);
    if (reduced()) { fadeIn(fresh); return; }
    const dir = kind === 'in' ? 1 : kind === 'out' ? -1 : 0;
    ghost(old, rect, host, -16 * dir);
    run(fresh, [{ opacity: 0, transform: `translateX(${24 * dir}px)` }, { opacity: 1, transform: 'none' }], dir ? T.drill : T.state, E.out);
  }

  // A region that opens in place grows from nothing to its height; closing reverses it, faster.
  function open(el) {
    if (!el) return;
    if (reduced()) { fadeIn(el); return; }
    const h = el.getBoundingClientRect().height;
    el.style.overflow = 'hidden';
    run(el, [{ height: '0px', opacity: 0 }, { height: `${h}px`, opacity: 1 }], T.layout, E.out).finished.then(() => { el.style.overflow = ''; });
  }
  function close(el, done) {
    if (!el || reduced()) { done(); return; }
    const h = el.getBoundingClientRect().height;
    el.style.overflow = 'hidden';
    run(el, [{ height: `${h}px`, opacity: 1 }, { height: '0px', opacity: 0 }], T.leave, E.in).finished.then(done);
  }

  // The selected line expands into the pane that describes it: a wash grows from the line's box to the pane's and fades.
  function expandFrom(from, to) {
    if (!from || !to || reduced()) return;
    const a = from.getBoundingClientRect();
    const b = to.getBoundingClientRect();
    if (!b.width || !b.height) return;
    const wash = document.createElement('div');
    wash.className = 'mo-wash';
    Object.assign(wash.style, { left: `${b.left}px`, top: `${b.top}px`, width: `${b.width}px`, height: `${b.height}px` });
    document.body.append(wash);
    run(wash, [
      { transform: `translate(${a.left - b.left}px, ${a.top - b.top}px) scale(${a.width / b.width}, ${a.height / b.height})`, opacity: 0.85 },
      { transform: 'none', opacity: 0 },
    ], T.drill, E.out).finished.then(() => wash.remove());
  }

  const wait = (ms) => new Promise((done) => { setTimeout(done, ms); });

  window.Motion = {
    T, E, reduced, enter, morph, swap, open, close, expandFrom, wait,
    // The wireframe's own switch, so the reduced behaviour can be watched without changing the system setting.
    force(on) { forced = on; sync(); },
  };
})();
