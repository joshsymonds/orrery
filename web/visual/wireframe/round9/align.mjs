// The alignment check of round 9's accounts wireframe: values right-aligned over their bars and under their heads in the
// band and the popover; the head's two bands on one column line; table columns under their heads; the pin flow's,
// the groups' and the menus' columns. Each failure names the drawing and the edge that is off by more than a pixel.
//   PLAYWRIGHT_CORE=<a node_modules directory that holds playwright-core> node align.mjs
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
  const fails = [];
  let checks = 0;
  const where = e => `${e.closest('figure')?.querySelector('figcaption .lbl')?.textContent ?? '?'} / ${e.closest('section')?.querySelector('.kick')?.textContent ?? ''}`;
  const box = e => e.getBoundingClientRect();
  // the edges of an element's text, not its box
  const text = e => { const rg = document.createRange(); rg.selectNodeContents(e); const rs = [...rg.getClientRects()].filter(x => x.width > 0); if (!rs.length) return null; return { left: Math.min(...rs.map(x => x.left)), right: Math.max(...rs.map(x => x.right)) }; };
  const same = (what, ctx, xs) => { const v = xs.filter(x => x[1] != null); if (v.length < 2) return; checks++; const ref = v[0][1]; for (const [lab, x] of v) if (Math.abs(x - ref) > 1) fails.push(`${what}: ${lab} at ${Math.round(x)} vs ${v[0][0]} at ${Math.round(ref)} in ${where(ctx)}`); };
  // 1. the band: each window value, bar, window name and the last column right-aligned under its head
  for (const g of document.querySelectorAll('.ugrid')) {
    const heads = [...g.querySelectorAll(':scope > .hd')].slice(1).map(h => text(h).right);
    for (const row of g.querySelectorAll('.urow:not(.span)')) {
      const cells = [...row.children].slice(1);
      cells.forEach((c, i) => {
        const xs = [[`head ${i + 1}`, heads[i]]];
        for (const part of c.querySelectorAll('.v, .k')) xs.push([`${row.textContent.slice(0, 12)} ${part.className}`, text(part)?.right]);
        const bar = c.querySelector('.bar'); if (bar) xs.push([`${row.textContent.slice(0, 12)} bar`, box(bar).right]);
        if (c.classList.contains('urs') && c.textContent) xs.push([`${row.textContent.slice(0, 12)} last`, text(c).right]);
        same('band column', g, xs);
      });
    }
  }
  // 2. the popover: values, bars and resets right under their heads; the Use… buttons and the default slot on one edge
  for (const t of document.querySelectorAll('.ptab')) {
    const heads = [...t.querySelectorAll(':scope > .hd')].slice(1, 4).map(h => text(h).right);
    const rows = [...t.querySelectorAll('.prow')];
    for (const row of rows) [...row.querySelectorAll(':scope > .pv')].forEach((c, i) => {
      const k = ['d5', 'd7', 'fb'].findIndex(x => c.classList.contains(x));
      const xs = [['head', heads[k]]];
      for (const part of c.querySelectorAll('.v, .rs')) xs.push([part.className, text(part)?.right]);
      const bar = c.querySelector('.bar'); if (bar) xs.push(['bar', box(bar).right]);
      same('popover column', t, xs);
    });
    same('popover Use…', t, rows.map(r => [r.querySelector('.pname .t').textContent, box(r.querySelector('.pact > :last-child')).right]));
    same('popover default slot', t, rows.map(r => [r.querySelector('.pname .t').textContent, box(r.querySelector('.pact > :first-child')).right]));
    same('popover names', t, rows.map(r => [r.querySelector('.pname .t').textContent, box(r.querySelector('.pname')).left]));
  }
  // 3. the head: the host's and the account's first column and meters start on one line
  for (const h of document.querySelectorAll('.chead')) {
    const m = h.querySelector('.mband'), a = h.querySelector('.aband');
    if (!m || !a) continue;
    same('head who', h, [['host', box(m.querySelector('.bwho')).left], ['account', box(a.querySelector('.bwho')).left], ['title', box(h.querySelector('.crumbs')).left - 0]].slice(0, 2));
    same('head meters', h, [['host meters', box(m.querySelector('.meters')).left], ['account meters', box(a.querySelector('.meters')).left]]);
    same('head right', h, [['account band end', box(a.querySelector('.aright')).right], ['title row end', box(h.querySelector('.cmds')).right]]);
  }
  // 4. tables: a numeric column's text right-aligned under its head; every other column's text starts under its head
  for (const t of document.querySelectorAll('table.t')) {
    const ths = [...t.querySelectorAll('tr:first-child th')];
    ths.forEach((th, i) => {
      if (!th.textContent.trim()) return;
      const num = th.classList.contains('num');
      const xs = [['head', num ? text(th).right : text(th).left]];
      for (const tr of t.querySelectorAll('tr')) {
        if (tr.querySelector('td[colspan]') || !tr.children[i] || tr.children[i].tagName !== 'TD') continue;
        const td = tr.children[i];
        if (!td.textContent.trim()) continue;
        xs.push([td.textContent.trim().slice(0, 14), num ? text(td).right : text(td).left]);
      }
      same(`table column ${th.textContent}`, t, xs);
    });
  }
  // 5. the pin flow: names, loads and notes in columns; prefixes and their notes too
  for (const pp of document.querySelectorAll('.pinpop, .psheet')) {
    const picks = [...pp.querySelectorAll('.pick')];
    same('pick names', pp, picks.map(x => [x.querySelector('.nm').textContent, box(x.querySelector('.nm')).left]));
    for (const k of [0, 1]) same(`pick load ${k + 1}`, pp, picks.filter(x => !x.querySelector('.ld.span')).map(x => [x.querySelector('.nm').textContent, text(x.querySelectorAll('.ld')[k]).right]));
    same('pick limited', pp, [[ 'load 2', picks.filter(x => !x.querySelector('.ld.span')).map(x => text(x.querySelectorAll('.ld')[1]).right)[0] ], ...picks.filter(x => x.querySelector('.ld.span')).map(x => [x.querySelector('.nm').textContent, text(x.querySelector('.ld.span')).right])]);
    if (getComputedStyle(picks[0]?.querySelector('.note') ?? document.body).display !== 'none') same('pick notes', pp, picks.map(x => [x.querySelector('.nm').textContent, box(x.querySelector('.note')).right]));
    const pf = [...pp.querySelectorAll('.pfx')];
    same('prefixes', pp, pf.map(x => [x.querySelector('code').textContent, box(x.querySelector('code')).left]));
    same('prefix notes', pp, pf.map(x => [x.querySelector('code').textContent, text(x.querySelector('.note'))?.right]));
  }
  // 6. legends and breakdowns: figures right-aligned per column
  for (const l of document.querySelectorAll('.legend, .brk')) {
    const cols = getComputedStyle(l).gridTemplateColumns.split(' ').length;
    const kids = [...l.children];
    for (let c = 0; c < cols; c++) {
      const col = kids.filter((_, i) => i % cols === c && (kids[i].classList.contains('num') || kids[i].classList.contains('hdn')));
      if (col.length) same('figure column', l, col.map(e => [e.textContent, text(e)?.right]));
    }
  }
  // 7. phone cells: a value right-aligned over its bar
  for (const c of document.querySelectorAll('.phband .cell2, .pw .pv')) {
    const v = c.querySelector('.v'), bar = c.querySelector('.bar');
    if (v && bar) same('phone cell', c, [['value', text(v).right], ['bar', box(bar).right]]);
  }
  // 8. groups: grips, swatches, names, who and actions in columns
  for (const g of document.querySelectorAll('.glist')) {
    const rows = [...g.querySelectorAll('.grow:not(.ask):not(.gap):not(.slot):not(.float)')];
    for (const [nm, sel, edge] of [['swatch', '.sw', 'left'], ['who', '.who', 'left'], ['actions', '.acts', 'right']]) {
      same(`groups ${nm}`, g, rows.filter(r => r.querySelector(sel)).map(r => [r.textContent.slice(0, 10), box(r.querySelector(sel))[edge]]));
    }
    same('groups name', g, rows.map(r => [r.textContent.slice(0, 10), box(r.children[2]).left]));
  }
  // 9. the example of which pin wins, and the Use… menu
  for (const res of document.querySelectorAll('.res')) {
    const kids = [...res.children].slice(1);
    for (let c = 0; c < 3; c++) same('example column', res, kids.filter((_, i) => i % 3 === c).map(e => [e.textContent.slice(0, 12), box(e).left]));
  }
  for (const m of document.querySelectorAll('.menu')) {
    const ends = [...m.querySelectorAll('.mi .end')];
    same('menu ends', m, ends.map(e => [e.textContent.slice(0, 12), box(e).right]));
  }
  // 10. the inbox card's host and account, right-aligned together
  for (const d of document.querySelectorAll('.dmeta')) same('card host and account', d, [...d.children].map(e => [e.textContent, box(e).right]));
  return { checks, fails };
});
console.log(JSON.stringify(r, null, 1));
await b.close();
if (r.fails.length) process.exit(1);
