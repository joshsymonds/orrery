// Round 6 wireframe: the board. Every scenario, by group, with a link to each frame that draws it.
(() => {
  'use strict';
  const { esc, each } = O;
  const groups = O.GROUPS.map((g) => [g, O.scenarios.filter((s) => s.group === g)]).filter(([, list]) => list.length);
  const stray = O.scenarios.filter((s) => !O.GROUPS.includes(s.group));
  if (stray.length) throw new Error(`scenarios in no known group: ${stray.map((s) => s.id).join(', ')}`);
  const desk = O.scenarios.filter((s) => s.desk).length;
  const phone = O.scenarios.filter((s) => s.phone).length;
  const link = (s, frame) => (s[frame] ? `<a href="${frame}.html#${esc(s.id)}">${frame}</a>` : `<span>${frame === 'desk' ? 'no desk' : 'no phone'}</span>`);
  document.querySelector('[data-board]').innerHTML = `<h1>orrery, round 6</h1>`
    + `<p class="wf-lede">One language for the web at the desk, the web under 760, and the Android app: ${O.scenarios.length} scenarios, ${desk} drawn for the desk and ${phone} for the phone, all from one set of components that reads one token file. `
    + `Open <a href="desk.html">the desk</a> or <a href="phone.html">the phone</a> and page with the arrow keys; <a href="catalog.html">the catalogue</a> shows every component in every state. `
    + `What changed and why is in <a href="DECISIONS.md">DECISIONS.md</a>; the flows are in <a href="flows.md">flows.md</a>; the language is <a href="../../system/SYSTEM.md">SYSTEM.md</a>.</p>`
    + `<div class="cards">${each([
      ['Grouped by directory, then conversation', 'The inbox is a tree of directories. Each holds its conversations, each with the epics and contracts it steers hanging under it, then what ended.'],
      ['One conversation per epic', 'An epic is steered from exactly one conversation, and moves only when you move it. One conversation may steer several.'],
      ['State lives with its controls', 'A conversation’s state is on its dock, beside Interrupt. The dock is three things: the state, your queue, the input.'],
      ['Every page is a pair', 'Beside a conversation is the work it started. Beside an epic or a contract is the conversation that steers it.'],
      ['One Ask', 'A session’s question, a permission, an epic’s decision and a park are one component; a session’s can be put off with Not now.'],
      ['A page has an order', 'An epic’s page is its summary, its track, now, tasks, requirements, release, report. Nothing is drawn before it exists.'],
      ['Every control says what it does', 'Labels where there is room, a tip with the key where there is not, and no bare number or mark.'],
    ], ([t, d]) => `<div class="card"><b>${esc(t)}</b><span>${esc(d)}</span></div>`)}</div>`
    + each(groups, ([g, list]) => `<section><h2>${esc(g)}</h2><table><thead><tr><th>scenario</th><th>what it shows</th><th>open</th></tr></thead><tbody>`
      + each(list, (s) => `<tr><td class="id">${esc(s.title)}</td><td>${esc(s.note || '')}</td><td class="links">${link(s, 'desk')} · ${link(s, 'phone')}</td></tr>`)
      + `</tbody></table></section>`);
})();
