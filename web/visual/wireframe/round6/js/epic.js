// Round 6 wireframe: the epic's pages. One page per epic, in the epic's own order; a contract; a task's phases.
(() => {
  'use strict';
  const { esc, cls, attrs, each, when, icon, md } = O;

  // ---------- small parts ----------

  // table draws a Table. cols: the desk's grid columns. head: column names. rows: [{ cells: [html], go, cur, detail }]
  O.table = (cols, head, rows, o = {}) => `<div class="tbl" style="--cols:${cols}${o.touch ? `;--cols-touch:${o.touch}` : ''}">`
    + `${when(head, () => `<div class="tr head">${each(head, (h) => (typeof h === 'string' ? `<span>${esc(h)}</span>` : h.html))}</div>`)}`
    + each(rows, (r) => `<${r.go ? 'a' : 'div'} class="${cls('tr', r.cur && 'cur')}"${attrs({ href: r.go ? O.go(r.go) : undefined })}>${r.cells.join('')}</${r.go ? 'a' : 'div'}>${when(r.detail, () => `<div class="tr-detail">${r.detail}</div>`)}`)
    + `</div>`;

  // track draws the Track: five stages. stages: [{ name, state: 'done' | 'now' | 'ahead', tone, words }]
  O.track = (stages) => `<nav class="track" aria-label="where this epic is">${each(stages, (s) => `<a class="${cls('stage', s.state, s.tone)}" href="#"${attrs({ 'aria-current': s.state === 'now' ? 'step' : undefined })}>`
    + `<span class="nm">${when(s.state === 'done', () => icon('check'))}${esc(s.name)}</span><span class="ds">${esc(s.words || (s.state === 'ahead' ? 'not reached' : ''))}</span></a>`)}</nav>`;

  // ---------- the orrery ----------

  // A task's stages, in the order it passes them. Verify (a failed review's findings being checked) and orchestrate
  // (a request being decided) happen inside review and implement, and are said in words there.
  const PLACES = ['red', 'implement', 'green', 'review', 'integrate'];
  O.PLACES = PLACES;
  // The four rings' radii, outside in. A task moves inward as it is built.
  const RINGS = [[134, 146], [119, 131], [104, 116], [89, 101], [74, 86]];
  const polar = (r, deg) => { const a = (deg - 90) * Math.PI / 180; return `${(160 + r * Math.cos(a)).toFixed(2)},${(160 + r * Math.sin(a)).toFixed(2)}`; };
  const sector = (r0, r1, a0, a1) => { const big = a1 - a0 > 180 ? 1 : 0; return `M${polar(r1, a0)}A${r1},${r1} 0 ${big} 1 ${polar(r1, a1)}L${polar(r0, a1)}A${r0},${r0} 0 ${big} 0 ${polar(r0, a0)}Z`; };

  // cellsOf gives a task's four cells: passed before its place, its tone at its place, ahead after.
  O.cellsOf = (t) => {
    if (t.place === 'integrated') return PLACES.map(() => 'ok');
    const at = PLACES.indexOf(t.place);
    return PLACES.map((_, i) => (at < 0 ? '' : i < at ? 'ok' : i === at ? t.tone : ''));
  };

  // orrery draws the Orrery. e: { tasks: [{ name, req, place, tone }], reqs: [{ id, proven }], hl (a task's name) }
  // Each ring names itself, once, on the ring: its name runs along the ring's own track through the gap at the top and
  // ends where the ring's first cell begins, so a name cannot be read as belonging to the ring beside it.
  let orreries = 0;
  O.orrery = (e) => {
    const tasks = e.tasks || [];
    if (!tasks.length) return '';
    const gap = 62;
    const sweep = 360 - gap;
    const each_ = sweep / tasks.length;
    const pad = tasks.length > 60 ? 0 : Math.min(1.5, each_ * 0.12);
    let cells = '';
    let reqs = '';
    let hl = '';
    tasks.forEach((t, i) => {
      const a0 = gap / 2 + i * each_ + pad;
      const a1 = gap / 2 + (i + 1) * each_ - pad;
      O.cellsOf(t).forEach((tone, ring) => { cells += `<path class="${cls('c', tone)}" d="${sector(RINGS[ring][0], RINGS[ring][1], a0, a1)}"/>`; });
      const proven = (e.reqs || []).some((r) => r.id === t.req && r.proven);
      reqs += `<path class="${cls('req', proven && 'proven')}" d="${sector(151, 155, a0, a1)}"><title>${esc(t.name)} · proves ${esc(t.req || 'nothing yet')}</title></path>`;
      if (e.hl === t.name) hl = `<path class="hl" d="${sector(66, 158, a0 - pad, a1 + pad)}"/>`;
    });
    orreries += 1;
    const names = RINGS.map(([r0, r1], i) => {
      const r = (r0 + r1) / 2;
      const id = `ring-${orreries}-${i}`;
      return `<path id="${id}" class="ring-way" d="M${polar(r, -gap / 2)}A${r},${r} 0 0 1 ${polar(r, gap / 2 - 2)}"/>`
        + `<text class="ring-name"><textPath href="#${id}" startOffset="100%">${PLACES[i]}</textPath></text>`;
    }).join('');
    const done = tasks.filter((t) => t.place === 'integrated').length;
    const at = tasks.find((t) => t.name === e.hl);
    return `<figure class="orrery-fig"><svg class="orrery" viewBox="0 0 320 320" role="img" aria-label="${done} of ${tasks.length} tasks done">${reqs}${cells}${hl}${names}`
      + `<text class="count" x="160" y="170">${done}</text><text class="of" x="160" y="190">of ${tasks.length} done</text></svg>`
      + `${when(at, () => `<figcaption class="orrery-cap"><b class="ink-1">${esc(at.name)}</b> · ${esc(at.words || at.place)}${when(at.time, () => ` · ${esc(at.time)}`)}</figcaption>`)}</figure>`;
  };

  const PLACE_WORDS = { planned: 'not started', red: 'red', implement: 'implement', green: 'green', review: 'review', integrate: 'integrate', integrated: 'done' };
  const TONE_CELL = { run: 'live', mine: 'is-attention', bad: 'bad', acc: 'is-engine', ok: 'ok', plan: 'none' };

  // taskTable draws every task as a row: its name, the requirement it proves, its four phases, where it is, how long.
  O.taskTable = (e) => O.table('minmax(0, 1.2fr) auto auto minmax(0, 1fr) auto', ['task', 'proves', 'phases', 'now', ''], (e.tasks || []).slice(0, e.taskCap || 99).map((t) => ({
    go: t.go || 'phase-implement', cur: e.hl === t.name,
    cells: [`<span class="strong cutc">${esc(t.name)}</span>`, `<span>${when(t.req, () => O.idc(t.req))}</span>`, O.pips(O.cellsOf(t)),
      `<span class="cell ${TONE_CELL[t.tone] || ''} cutc wide">${esc(t.words || PLACE_WORDS[t.place] || t.place)}</span>`, `<span class="end id">${esc(t.time || '')}${icon('chevronRight')}</span>`],
  })), { touch: 'minmax(0, 1fr) auto auto' }) + when((e.tasks || []).length > (e.taskCap || 99), () => `<button class="fold-line">${icon('chevronRight')}<span>${e.tasks.length - e.taskCap} more tasks</span></button>`);

  // ---------- evidence ----------

  // evidenceRun draws an EvidenceRun: one sentence, one verdict, the commit, and its log when opened.
  // r: { phase: 'red' | 'green', verdict: 'holds' | 'fails' | '', state: 'ended' | 'capturing' | 'judging', passed, exit,
  //      source, commit, log: 'open' | 'loading' | 'unavailable', text, stderr, artifacts: [{ path, state, bytes, image }] }
  O.evidenceRun = (r) => {
    const running = r.state && r.state !== 'ended';
    const sentence = running ? `${r.phase === 'red' ? 'Red' : 'Green'} run: ${r.state === 'judging' ? 'a judge is reading the output' : 'capturing the output'}…`
      : r.phase === 'red' ? (r.passed ? 'Red run: the test passes before any change. It should fail.' : 'Red run: the test fails before any change, as it should.')
        : (r.passed ? 'Green run: the test passes on the change.' : 'Green run: the test still fails on the change.');
    const chip = running ? O.chip(r.state, 'live') : r.verdict === 'holds' ? O.chip('holds', 'ok', 'check') : r.verdict === 'fails' ? O.chip('fails', 'bad', 'cross') : O.chip('no verdict');
    const log = r.log === 'open' ? `<div class="log side"><span class="lbl">stdout</span>${esc(r.text)}</div>${when(r.stderr, () => `<div class="log side"><span class="lbl is-halt">stderr</span>${esc(r.stderr)}</div>`)}`
      : r.log === 'loading' ? '<div class="log side">loading the log…</div>' : r.log === 'unavailable' ? '<div class="log side">The log is not available.</div>' : '';
    const artifacts = each(r.artifacts, (a) => `<div class="artifact"><span>${esc(a.path)} · ${esc(a.state === 'over_cap' ? 'too large to keep' : a.state)}${when(a.bytes, () => ` · ${esc(a.bytes)}`)}</span>${when(a.image, () => `<div class="img">${esc(a.image)}</div>`)}</div>`);
    return `<div class="run-line"><span>${esc(sentence)}</span>${chip}<span class="meta">${esc([r.source === 'supplied' ? 'supplied by the agent' : 'captured', running ? '' : `exit ${r.exit}`].filter(Boolean).join(' · '))}</span>`
      + `<span class="end">${when(r.commit, () => O.commit(r.commit))}<a class="link" href="#">${r.log === 'open' ? 'hide the log' : 'log'}</a></span></div>${log}${artifacts}`;
  };

  // The two cells of a requirement's proof.
  const RED = { holds: ['ok', 'check', 'fails at base'], met: ['bad', 'cross', 'passes at base'], running: ['live', '', 'running'], none: ['none', '', 'no test yet'], review: ['none', '', 'by review'] };
  const GREEN = { passes: ['ok', 'check', 'passes'], fails: ['bad', 'cross', 'fails'], running: ['live', '', 'running'], none: ['none', '', 'not yet'], review: ['ok', 'check', 'reviewed'] };
  const proofCell = (map, key) => { const [tone, ic, word] = map[key] || map.none; return `<span class="cell ${tone}">${when(ic, () => icon(ic))}${esc(word)}</span>`; };

  // reqTable draws the requirements: what will be true, and its red and green proof in two cells.
  O.reqTable = (e) => O.table('auto minmax(0, 1fr) auto auto auto', ['', 'what will be true when done', { html: '<span class="th-red">red</span>' }, { html: '<span class="th-green">green</span>' }, ''], (e.reqs || []).map((r) => ({
    go: r.go, cur: r.open,
    cells: [`<span>${O.idc(r.id)}</span>`, `<span class="strong wide wrapc">${esc(r.text)}</span>`, proofCell(RED, r.red), proofCell(GREEN, r.green),
      `<span class="end">${r.proven ? O.chip('proven', 'ok') : O.chip('open')}</span>`],
    detail: r.open ? each(r.runs, O.evidenceRun) : '',
  })), { touch: 'auto auto minmax(0, 1fr)' });

  // ---------- the epic page ----------

  // happenings draws a HappeningList.
  O.happenings = (list, more) => `<div class="happen">${each(list, (h) => `<div class="h"><span class="at">${esc(h.at)}</span><span>${md(h.text)}</span>${when(h.tail, () => `<div class="log tail">${esc(h.tail)}</div>`)}</div>`)}</div>`
    + when(more, () => `<button class="fold-line">${icon('chevronRight')}<span>${esc(more)} earlier</span></button>`);

  // releaseTable draws the release steps: what leaves this machine, where to, how it is undone, and how each went.
  const RELEASE = { waiting: ['none', '', 'waiting'], running: ['live', '', 'running'], passed: ['ok', 'check', 'passed'], failed: ['bad', 'cross', 'failed'] };
  O.releaseTable = (steps) => O.table('auto minmax(0, 1fr) auto auto', ['', 'step', 'undo', ''], (steps || []).map((s, i) => ({
    cells: [`<span class="id">${i + 1}</span>`, `<span class="wide"><span class="strong">${esc(s.step)}</span> <span class="ink-3">→ ${esc(s.target)}</span></span>`, `<span class="id">${esc(s.undo)}</span>`,
      `<span class="end">${proofCell(RELEASE, s.state)}</span>`],
    detail: (s.reason || s.evidence || s.cmds) ? `${when(s.reason, () => O.inote(s.reason, 'error'))}${when(s.evidence, () => `<div class="log">${esc(s.evidence)}</div>`)}${when(s.cmds, () => `<div class="doors">${s.cmds}</div>`)}` : '',
  })), { touch: 'auto minmax(0, 1fr) auto' });

  // epicCmds draws an epic's commands. Talking to it is not one of them: its conversation is beside the page, or
  // folded to the rail there.
  O.epicCmds = (e) => {
    const c = e.controls || {};
    if (c.confirm) return O.confirm(`Stop ${e.slug}?`, `Stop ${e.slug}`, 'Keep it running');
    return `${when(c.pause, () => O.btn('Pause', { icon: 'pause', tip: 'Finish the running steps, start nothing new', tipSide: 'left' }))}`
      + `${when(c.resume, () => O.btn('Resume', { icon: 'play' }))}`
      + `${when(c.stop, () => O.btn('Stop…', { variant: 'danger', tip: 'End this epic. Asks first.', tipSide: 'left', go: 'epic-stop-confirm' }))}`
      + `${when(!O.touch(), () => O.iconBtn('split', 'Steer it from another conversation', { tipSide: 'left', go: 'epic-handover' }))}`;
  };

  // epicState draws the epic's state as pills: the state, and what blocks it.
  O.epicState = (e) => `${O.pill(e.state, e.tone)}${when(e.blocker, () => O.pill(e.blocker, 'attention'))}`;

  // epicBody draws the epic page's sections, in the epic's own order. A section the epic has not reached is not
  // drawn; the track says it is ahead.
  O.epicBody = (e) => {
    const LOOK = { Now: ['clock', 'live'], 'Just happened': ['retry', '1'], Tasks: ['pools', '4'], Requirements: ['check', 'ok'], Review: ['search', '2'], Release: ['open', 'engine'], Report: ['doc', '5'] };
    const sec = (name, o, body) => `<section aria-label="${esc(name)}">${O.sec(name, { icon: (LOOK[name] || [])[0], hue: (LOOK[name] || [])[1], ...o })}${body}</section>`;
    const lede = `<section class="lede" aria-label="summary">${when(e.problem, () => O.inote(e.problem, 'error'))}<p class="summary">${esc(e.summary)}</p><div class="doors">`
      + `${when(e.contract, () => `<a class="door" href="${O.go(e.contractGo || 'contract-accepted')}">${icon('doc')}<b>Contract</b><span>${esc(e.contract)}</span>${icon('chevronRight')}</a>`)}`
      + `${when(e.conversation, () => `<a class="door" href="${O.go(e.conversationGo || 'convo-epic')}">${icon('chat')}<b>Steered from</b><span>${esc(e.conversation)}</span>${icon('chevronRight')}</a>`)}</div></section>`;
    const now = when(e.now || e.ask || e.running, () => sec('Now', { rest: e.asOf }, `${when(e.now, () => `<p class="now-sentence">${md(e.now)}</p>`)}${when(e.ask, () => O.ask(e.ask))}${e.nowExtra || ''}`
      + when(e.running && e.running.length, () => O.table('auto minmax(0, 1fr) auto minmax(0, 2fr) auto', null, e.running.map((s) => ({
        go: s.go || 'worker-running',
        cells: [O.stateGlyph(s.limited ? 'limited' : s.held ? 'held' : 'working'), `<span class="strong cutc">${esc(s.label)}</span>`, O.pips(s.pips), `<span class="cutc wide ${s.held && !s.limited ? 'is-attention' : ''}">${esc(s.doing)}</span>`, `<span class="end id">${esc(s.time || '')}${icon('chevronRight')}</span>`],
      })), { touch: 'auto minmax(0, 1fr) auto' }))
      + when(e.next, () => `<p class="inote">Next: ${esc(e.next)}</p>`)));
    const build = when(e.tasks && e.tasks.length, () => sec('Tasks', { n: `${e.tasks.filter((t) => t.place === 'integrated').length} of ${e.tasks.length} done` }, `<div class="build">${O.orrery(e)}<div>${O.taskTable(e)}</div></div>`));
    const reqs = when(e.reqs && e.reqs.length, () => sec('Requirements', { n: `${e.reqs.filter((r) => r.proven).length} of ${e.reqs.length} proven` }, O.reqTable(e)));
    const review = when(e.review, () => sec('Review', { n: e.review.words }, e.review.body));
    const release = when(e.release, () => sec('Release', { n: e.release.words }, O.releaseTable(e.release.steps) + when(e.release.then, () => `<p class="inote">Then: ${esc(e.release.then)}</p>`)));
    const report = when(e.report, () => sec('Report', {}, `<p class="now-sentence">${esc(e.report.outcome)}</p><p class="summary measure ink-2">${esc(e.report.why)}</p>`
      + `<div class="doors">${when(e.report.shipped, () => `<span class="door"><span class="cell is-ok">${icon('check')}shipped</span><span>${esc(e.report.shipped)}</span></span>`)}${when(e.report.notShipped, () => `<span class="door"><span class="cell is-bad">${icon('cross')}not shipped</span><span>${esc(e.report.notShipped)}</span></span>`)}</div>${e.report.cmds || ''}`));
    const activity = when(e.history, () => sec('Just happened', {}, O.happenings(e.history, e.historyMore)));
    return `<div class="spage" ${e.scrollTo ? `data-scroll-to="${esc(e.scrollTo)}"` : ''}><div class="inner">${lede}${O.track(e.track)}${now}${activity}${build}${reqs}${review}${release}${report}${e.after || ''}</div></div>`;
  };

  // epicPage draws the epic's page for the frame it is in. At the desk it is the panel its tile opens beside the
  // conversation that steers it (o.record, o.close: the conversation with the panel shut).
  O.epicPage = (e, o = {}) => {
    const touch = O.touch();
    const title = !touch && o.record ? O.recordTitle({ close: o.close, here: e.slug, one: true, state: O.epicState(e), cmds: O.epicCmds(e), menu: o.menu, note: e.cmdError }) : O.titleRow({
      toggle: !touch && o.toggle !== false, waiting: 6, back: touch ? 'inbox-home' : undefined,
      dir: e.dir, here: e.slug, one: true,
      state: O.epicState(e), cmds: O.epicCmds(e), menu: o.menu, strip: O.W.strip(null), noStrip: touch, note: e.cmdError,
    });
    const body = e.unavailable
      ? `<div class="blank">${O.inote(e.unavailable, e.loading ? '' : 'error')}${when(!e.loading, () => O.btn('Try again', { icon: 'retry' }))}</div>`
      : O.epicBody(e);
    const foot = when(touch && !e.unavailable, () => `<div class="cmdbar">${O.btn('Talk to the session that steers it', { icon: 'chat', class: 'grow', go: 'epic-talk' })}${O.iconBtn('more', 'Pause, stop')}</div>`);
    return O.page({ label: 'epic', class: !touch && o.record ? 'rpage' : '', title, body, foot, notices: o.notices, over: o.over });
  };

  // subPage draws a page under an epic: a contract, a phase, with the crumbs back up.
  O.subPage = (p) => {
    const touch = O.touch();
    return O.page({
      label: p.label, class: !touch && p.record ? 'rpage' : '', pinned: p.band || '',
      title: !touch && p.record ? O.recordTitle({ up: p.up, close: p.close, crumbs: (p.crumbs || []).slice(1), here: p.here, one: true, state: p.state, cmds: p.cmds, menu: p.menu, note: p.note })
        : O.titleRow({ toggle: !touch, waiting: 6, back: p.back, crumbs: p.crumbs, here: p.here, one: true, state: touch ? '' : p.state, cmds: p.cmds, strip: O.W.strip(null), noStrip: touch, note: p.note }),
      body: `<div class="spage"><div class="inner">${p.body}</div></div>`,
      foot: when(touch && (p.state || p.touchCmds), () => `<div class="cmdbar">${p.state || ''}<span class="end">${p.touchCmds || ''}</span></div>`) + (p.foot || ''),
      over: p.over,
    });
  };

  // band draws a ContextBand: the epic a task belongs to, and every task of that epic with this one marked.
  // b: { pin (an EpicPin's data), tasks, cur }
  O.band = (b) => `<div class="band">${O.epicPin(b.pin)}<nav class="tstrip" aria-label="this epic's tasks">${each(b.tasks, (t) => `<a class="${cls('tchip', b.cur === t.name && 'cur')}" href="${O.go(t.go || 'phase-implement')}"${attrs({ 'aria-current': b.cur === t.name ? 'page' : undefined })}><span>${esc(t.name)}</span>${O.pips(O.cellsOf(t))}</a>`)}</nav></div>`;

  // phaseRow draws a PhaseRow. p: { name, state: 'passed' | 'now' | 'ahead', tone, sum, time, open, body, session }
  O.phaseRow = (p) => `<div class="${cls('phase', p.state, p.tone)}"><button class="phase-head" aria-expanded="${!!p.open}"${p.state === 'ahead' ? ' disabled' : ''}>`
    + `${icon(p.open ? 'chevronDown' : 'chevronRight')}<span class="nm">${esc(p.name)}</span><span class="sum">${esc(p.sum || (p.state === 'ahead' ? 'not reached' : ''))}</span><span class="end">${esc(p.time || '')}</span></button>`
    + `${when(p.open, () => `<div class="phase-body">${p.body || ''}${when(p.session, () => `<div class="doors"><a class="door" href="${O.go(p.session)}">${icon('chat')}<b>Open the session</b>${icon('chevronRight')}</a></div>`)}</div>`)}</div>`;
})();
