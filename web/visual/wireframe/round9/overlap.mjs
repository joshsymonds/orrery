// The text-overlap check of round 9's accounts wireframe: every text node's client rects, compared pairwise; a pair that
// overlaps by more than a pixel across and two down fails. Text cut short by an ellipsis or hidden by a clipping ancestor
// is skipped, and so is text under an overlay against the text it lies over on purpose (a popover, a menu, a sheet, a
// tip, a dragged row). Text that runs out of its frame fails too.
//   PLAYWRIGHT_CORE=<a node_modules directory that holds playwright-core> node overlap.mjs
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const page = path.join(here, 'accounts.html');
if (!process.env.PLAYWRIGHT_CORE) { console.error('set PLAYWRIGHT_CORE to a node_modules directory that holds playwright-core'); process.exit(1); }
const require = createRequire(path.join(path.resolve(process.env.PLAYWRIGHT_CORE), '/'));
const { chromium } = require('playwright-core');
const b = await chromium.launch({ executablePath: process.env.CHROMIUM || '/run/current-system/sw/bin/chromium' });
const p = await b.newPage({ viewport: { width: 1280, height: 900 } });
await p.goto('file://' + page);
await p.evaluate(() => document.fonts.ready);
const r = await p.evaluate(() => {
  const OVER = '.pop, .psheet, .ptip, .menu, .palette, .pinpop, .toast, .scrim, .lifted';
  const FRAMES = '.deskf, .phone, .chead, .pagef, .convof, .pop, .pinpop, .menu, .palette, .uband, .psheet, .card, .inl, .asking, .res, .form, .toast, .glist, table.t, .phband, .ctxcard';
  const where = e => `${e.closest('figure')?.querySelector('figcaption .lbl')?.textContent ?? 'intro'} / ${e.closest('section')?.querySelector('.kick')?.textContent ?? ''}`;
  const clipped = e => { for (let x = e; x && x !== document.body; x = x.parentElement) { const cs = getComputedStyle(x); if ((cs.textOverflow === 'ellipsis' || cs.webkitLineClamp !== 'none') && x.scrollWidth + x.scrollHeight > x.clientWidth + x.clientHeight + 1) return true; } return false; };
  const hidden = e => { const cs = getComputedStyle(e); return cs.visibility === 'hidden' || cs.opacity === '0'; };
  // text wholly outside an ancestor that clips is not drawn at all
  const unseen = (e, rc) => { for (let x = e.parentElement; x && x !== document.body; x = x.parentElement) { const cs = getComputedStyle(x); if (cs.overflow !== 'visible' || cs.overflowY !== 'visible') { const b = x.getBoundingClientRect(); if (rc.top >= b.bottom || rc.bottom <= b.top || rc.left >= b.right || rc.right <= b.left) return true; } } return false; };
  const boxes = [];
  const w = document.createTreeWalker(document.querySelector('main'), NodeFilter.SHOW_TEXT);
  for (let n; (n = w.nextNode());) {
    if (!n.textContent.trim()) continue;
    const el = n.parentElement;
    if (hidden(el) || clipped(el)) continue;
    const range = document.createRange(); range.selectNodeContents(n);
    for (const rc of range.getClientRects()) if (rc.width > 0 && rc.height > 0 && !unseen(el, rc)) boxes.push({ t: n.textContent.trim().slice(0, 28), rc, el });
  }
  const hits = [];
  for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) {
    const a = boxes[i].rc, c = boxes[j].rc;
    const ox = Math.min(a.right, c.right) - Math.max(a.left, c.left), oy = Math.min(a.bottom, c.bottom) - Math.max(a.top, c.top);
    if (ox > 1 && oy > 2) {
      const oa = boxes[i].el.closest(OVER), ob = boxes[j].el.closest(OVER);
      if (oa !== ob) continue;
      hits.push(`${boxes[i].t} × ${boxes[j].t} (${Math.round(ox)}x${Math.round(oy)}) in ${where(boxes[i].el)}`);
    }
  }
  const out = [];
  for (const bx of boxes) {
    const f = bx.el.closest(FRAMES); if (!f) continue;
    const fr = f.getBoundingClientRect();
    // a frame that clips hides what runs past it, which is a failure too: the text is cut off.
    if (bx.rc.right > fr.right + 1 || bx.rc.left < fr.left - 1 || bx.rc.bottom > fr.bottom + 1) {
      // a frame taller than the drawing on purpose (a phone, a desk) clips its foot: only sideways counts there
      const tall = f.matches('.deskf, .phone, .convof, .psheet');
      if (tall && bx.rc.right <= fr.right + 1 && bx.rc.left >= fr.left - 1) continue;
      out.push(`${bx.t} runs ${Math.round(Math.max(bx.rc.right - fr.right, fr.left - bx.rc.left, bx.rc.bottom - fr.bottom))}px out of .${f.className.split(' ')[0]} in ${where(bx.el)}`);
    }
  }
  return { overlaps: hits, outside: out };
});
console.log(JSON.stringify(r, null, 1));
await b.close();
if (r.overlaps.length || r.outside.length) process.exit(1);
