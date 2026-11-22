// Round 6 wireframe: the primitives. Each function draws one component of SYSTEM.md part 8.1 and nothing else.
(() => {
  'use strict';
  const { esc, cls, attrs, each, when, icon } = O;

  // kbd draws a key cap. variant: '' | 'active' | 'attn'.
  O.kbd = (key, variant = '') => `<kbd class="${cls('kbd', variant)}">${esc(key)}</kbd>`;

  // btn draws a Button. o.variant: '' (Default) | 'quiet' | 'primary' | 'danger'. o.state: 'hover' | 'pressed' |
  // 'focused' | 'disabled', for the catalogue. o.tip is what the control does, shown on hover; o.go opens a scenario.
  O.btn = (label, o = {}) => {
    const tag = o.go ? 'a' : 'button';
    const a = attrs({
      class: cls('btn', o.variant, o.state, o.iconOnly && 'icon-only', o.class),
      href: o.go ? O.go(o.go) : undefined,
      'data-tip': o.tip, 'data-tip-side': o.tipSide,
      'aria-label': o.iconOnly ? label : undefined,
      disabled: tag === 'button' && o.state === 'disabled',
      type: tag === 'button' ? 'button' : undefined,
    });
    return `<${tag}${a}>${when(o.icon, () => icon(o.icon))}${o.iconOnly ? '' : `<span>${esc(label)}</span>`}${when(o.kbd, () => O.kbd(o.kbd, o.variant === 'primary' ? '' : ''))}</${tag}>`;
  };
  // iconBtn draws an IconButton: square, its label its accessible name and its tip.
  O.iconBtn = (name, label, o = {}) => O.btn(label, { variant: 'quiet', ...o, icon: name, iconOnly: true, tip: o.tip === undefined ? label : o.tip });

  // confirm draws a ConfirmButton in its Asking state: the question, the confirm and the keep.
  O.confirm = (question, yes, no = 'Keep') => `<span class="confirm"><span>${esc(question)}</span>${O.btn(yes, { variant: 'primary' })}${O.btn(no, { variant: 'quiet' })}</span>`;

  // tip draws a Tip by itself, as the catalogue shows it.
  O.tip = (text, key) => `<span class="tip">${esc(text)}${when(key, () => O.kbd(key))}</span>`;

  // field draws a Field. o.state: '' | 'focused' | 'error'.
  O.field = (o = {}) => `<label class="${cls('field', o.state, o.class)}">${when(o.icon, () => icon(o.icon))}${when(o.kbd, () => O.kbd(o.kbd))}`
    + (o.value ? `<input value="${esc(o.value)}" aria-label="${esc(o.label || o.placeholder || '')}">` : `<input placeholder="${esc(o.placeholder || '')}" aria-label="${esc(o.label || o.placeholder || '')}">`)
    + `${when(o.count, () => `<span class="count">${esc(o.count)}</span>`)}${o.trail || ''}</label>`;

  // chip draws a Chip. variant: '' (Neutral) | 'ok' | 'bad' | 'attention' | 'live' | 'engine' | 'halt'.
  O.chip = (text, variant = '', iconName = '') => `<span class="${cls('chip', variant)}">${when(iconName, () => icon(iconName))}${esc(text)}</span>`;

  // fchip draws a FilterChip: a word and a count that narrow a list.
  O.fchip = (label, n, variant = '', on = false, go = '') => `<a class="${cls('fchip', variant, on && 'on')}"${attrs({ href: go ? O.go(go) : undefined })} role="button" aria-pressed="${on}"><b>${esc(n)}</b>${esc(label)}</a>`;

  // pill draws a Pill. tone: '' | 'live' | 'attention' | 'ok' | 'bad' | 'halt' | 'engine'. o.state adds its glyph.
  O.pill = (text, tone = '', o = {}) => `<span class="pill"${attrs({ 'data-tone': tone })}>${when(o.state, () => O.stateGlyph(o.state))}${esc(text)}${o.trail || ''}</span>`;

  // meter draws a Meter. m: { name, value, pct, level }. Without pct there is no bar.
  O.meter = (m, stacked = false) => `<span class="${cls('meter', stacked && 'stacked')}" data-level="${esc(m.level || 'ok')}"${attrs({ title: m.title, 'data-series': m.series })}>`
    + `<span class="name">${esc(m.name)}</span>${when(m.pct !== undefined && !stacked, () => `<span class="bar"><i style="width:${Number(m.pct)}%"></i></span>`)}`
    + `<span class="value">${esc(m.value)}</span>${when(m.pct !== undefined && stacked, () => `<span class="bar"><i style="width:${Number(m.pct)}%"></i></span>`)}</span>`;

  // The context's level, by the token rule: below 60 ok, below 80 warn, from there crit.
  O.ctxLevel = (pct) => (pct >= 80 ? 'crit' : pct >= 60 ? 'warn' : 'ok');
  // ctx draws a ContextMeter. c: { pct, used, window }. o.bare leaves out the label and the tokens.
  O.ctx = (c, o = {}) => {
    if (!c) return '';
    const on = Math.max(c.pct > 0 ? 1 : 0, Math.round(c.pct / 20));
    const blocks = [0, 1, 2, 3, 4].map((i) => `<i class="${i < on ? 'on' : ''}"></i>`).join('');
    return `<span class="ctx" data-level="${O.ctxLevel(c.pct)}" title="context: ${esc(c.used)} of ${esc(c.window)} tokens">`
      + `${when(!o.bare, '<span class="name">context</span>')}<span class="blocks">${blocks}</span><span class="pct">${c.pct}%</span>`
      + `${when(!o.bare && c.used, () => `<span>${esc(c.used)} of ${esc(c.window)}</span>`)}</span>`;
  };

  // commit draws a CommitMark: the mark, with the short hash as its name.
  O.commit = (hash) => `<span class="commit" title="commit ${esc(hash)}">${icon('commit', `commit ${hash}`)}</span>`;

  // sec draws a SectionHead. o.variant: '' | 'attention' | 'new'. o.rest is drawn at the far right.
  O.sec = (name, o = {}) => `<div class="${cls('sec', o.variant)}"${attrs({ 'data-hue': o.hue })}>${when(o.icon, () => icon(o.icon))}<span class="name">${esc(name)}</span>${when(o.n !== undefined, () => `<span class="n">${esc(o.n)}</span>`)}${when(o.rest, () => `<span class="rest">${o.rest}</span>`)}</div>`;

  // idc draws an IdChip: a row's short id (R4, F2, P1), coloured by its kind.
  O.idc = (id) => `<span class="idc" data-k="${esc(String(id)[0])}">${esc(id)}</span>`;

  // hostLabel draws a HostLabel: the host a pane, a session, an epic or a step runs on, with the host icon. o.heading
  // draws it on a card's heading, once for the rows under it, which all run on that host; inline it sits on its own row.
  O.hostLabel = (name, o = {}) => `<span class="${cls('host-label', o.heading && 'heading')}">${icon('host')}<span class="name">${esc(name)}</span></span>`;
  // HOST_FROM names where each kind of host a drawing shows comes from: the server field, and the epic that gives it.
  O.HOST_FROM = {
    pane: 'a pane’s host is Pane.host (orrery-host-surfaces)',
    session: 'a headless session’s host is its session row’s host (orrery-first-class-hosts)',
    epic: 'an epic’s host is Epic.host (orrery-host-relay)',
    step: 'a step’s host is EpicStep.host (orrery-host-relay)',
  };
  // hostsNote is the sentence a scenario's note ends with when its drawing shows a host; none when it shows none.
  O.hostsNote = (kinds) => (kinds.length ? ` Hosts: ${kinds.map((k) => O.HOST_FROM[k]).join('; ')}.` : '');

  // ctl draws a Ctl: a control inside a line. o.tone: '' | 'bad' | 'go'. o.only: the icon alone, its label its name.
  O.ctl = (label, iconName, o = {}) => `<button class="${cls('ctl', o.tone, o.only && 'only')}"${attrs({ 'aria-label': o.only ? label : undefined, title: o.tip || (o.only ? label : undefined), disabled: o.disabled })}>${when(iconName, () => icon(iconName))}${o.only ? '' : `<span>${esc(label)}</span>`}${when(o.kbd && !O.touch(), () => O.kbd(o.kbd))}</button>`;

  // badged puts a count on a control's corner. kind: '' (waits on you) | 'new'.
  O.badged = (html, n, kind = '') => `<span class="badged">${html}${when(n, () => `<span class="${cls('n', kind)}">${esc(n)}</span>`)}</span>`;

  // inote draws an InlineNote. variant: '' (Quiet) | 'error' | 'ok'.
  O.inote = (text, variant = '') => `<p class="${cls('inote', variant)}">${when(variant === 'error', () => icon('warn'))}${when(variant === 'ok', () => icon('check'))}<span>${esc(text)}</span></p>`;

  // menu draws a Menu. An item is { label, icon, kbd, sub, danger, go }, the string 'sep', or { head }.
  O.menu = (items, o = {}) => `<div class="${cls('menu', o.class)}" role="menu">${each(items, (it) => {
    if (it === 'sep') return '<div class="menu-sep"></div>';
    if (it.head) return `<div class="menu-head">${esc(it.head)}</div>`;
    const tag = it.go ? 'a' : 'button';
    return `<${tag} class="${cls('menu-item', it.danger && 'danger', it.hover && 'hover')}" role="menuitem"${attrs({ href: it.go ? O.go(it.go) : undefined })}>${when(it.icon, () => icon(it.icon))}<span>${esc(it.label)}</span>${when(it.sub, () => `<span class="sub">${esc(it.sub)}</span>`)}${when(it.kbd, () => O.kbd(it.kbd))}</${tag}>`;
  })}</div>`;

  // gauge draws a Gauge. g: { done, started, total, tone, inner }. inner: '' | 'check' | 'dash' | 'dot'.
  O.gauge = (g) => {
    const total = Math.max(1, g.total || 0);
    const done = (100 * (g.done || 0)) / total;
    const started = (100 * ((g.done || 0) + (g.started || 0))) / total;
    const arc = (klass, share) => (share > 0 ? `<circle class="${klass}" cx="12" cy="12" r="9" pathLength="100" stroke-dasharray="${share.toFixed(1)} 100" transform="rotate(-90 12 12)"/>` : '');
    const inner = { check: '<path class="done" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" d="M8.6 12.3l2.3 2.3 4.4-4.9"/>', dash: '<path class="done" stroke-width="1.5" stroke-linecap="round" d="M9 12h6"/>', dot: '<circle class="core" cx="12" cy="12" r="3"/>' }[g.inner] || '';
    const label = g.total ? `${g.done || 0} of ${g.total} tasks done` : 'no tasks yet';
    return `<svg class="gauge" viewBox="0 0 24 24" role="img" aria-label="${esc(label)}"${attrs({ 'data-tone': g.tone })}><title>${esc(label)}</title>`
      + `<circle class="planned" cx="12" cy="12" r="9"${g.total ? '' : ' stroke-dasharray="2 3"'}/>${arc('started', started)}${arc('done', done)}${inner}</svg>`;
  };

  // pips draws Pips: one cell per phase, each a tone ('' for a phase not reached).
  O.pips = (tones) => `<span class="pips" aria-hidden="true">${each(tones, (t) => `<i${attrs({ 'data-tone': t })}></i>`)}</span>`;

  // toggle draws a Switch.
  O.toggle = (on, label) => `<span class="${cls('switch', on && 'on')}" role="switch" aria-checked="${!!on}" aria-label="${esc(label)}"><i></i></span>`;
})();
