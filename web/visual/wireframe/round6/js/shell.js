// Round 6 wireframe: the shell. The frame, the inbox, the title row with its strip, and what lies over a page:
// notices, the New panel, the key list, a menu, and on touch a sheet.
(() => {
  'use strict';
  const { esc, cls, attrs, each, when, icon } = O;

  // ---------- inbox ----------

  // sessionRow draws an InboxRow. r: { id, state, title, age, l2, l2kind: '' | 'asks' | 'live', tool, clock, unseen,
  //   tag, go, acts }. o: { cur, open, menu, hover }
  O.sessionRow = (r, o = {}) => {
    const l2 = r.l2kind === 'live'
      ? `<span class="l2">${when(r.tool, () => `<span class="name">${esc(r.tool)}</span> `)}${esc(r.l2 || '')}${when(r.clock, () => ` · ${esc(r.clock)}`)}</span>`
      : when(r.l2, () => `<span class="${cls('l2', r.l2kind, r.unseen && r.l2kind !== 'asks' && 'wrap2')}">${when(r.l2kind === 'asks', () => icon('question'))}${esc(r.l2)}</span>`);
    const menu = when(o.menu, () => O.rowMenu(r));
    return `<a class="${cls('row', o.cur && 'cur', o.open && 'open', o.menu && 'menu-open', o.hover && 'hover')}"${attrs({ href: O.go(r.go || r.id), 'data-unseen': r.unseen, 'aria-current': o.open ? 'page' : undefined })}>`
      + `<span class="dot"${attrs({ title: r.unseen ? 'new since you looked' : undefined })}></span>${O.stateGlyph(r.state)}<span class="title">${esc(r.title)}</span><span class="age">${esc(r.age || '')}</span>`
      + `${l2}${when(r.tag, () => `<span class="l2 tag">${esc(r.tag)}</span>`)}`
      + `${O.iconBtn('more', 'Archive, open beside…', { class: 'more-btn', tipSide: 'left' })}${menu}</a>`;
  };

  // rowMenu draws what a row's More opens. On touch the same items are a sheet, opened by a long press.
  O.rowMenuItems = (r) => [
    { label: 'Open in a new window', icon: 'open' },
    { label: r && r.unseen ? 'Mark seen' : 'Mark unseen', icon: 'check', kbd: 'x' },
    'sep',
    { label: r && r.archived ? 'Unarchive' : 'Archive', icon: 'archive', kbd: 'A' },
  ];
  O.rowMenu = (r) => O.menu(O.rowMenuItems(r));

  // epicRow draws an EpicRow and, under it, its running steps. A contract not yet accepted is the same row led by the
  // contract's mark. e: { id, slug, contract, count, words, blocker, cmds, gauge, steps: [{ held, label, pips, doing }],
  //   more, tail, ended, go }
  O.epicRow = (e, o = {}) => `<a class="${cls('row', 'epic', e.contract && 'contract', e.ended && 'ended', o.cur && 'cur', o.open && 'open')}" href="${O.go(e.go || e.id)}">`
    + `${e.contract ? `<span class="cmark">${icon('doc')}</span>` : O.gauge(e.gauge)}<span class="title">${esc(e.slug)}</span><span class="age">${esc(e.contract ? e.count || '' : '')}</span>`
    + `<span class="l2">${esc(e.contract ? e.words : [e.words, e.count].filter(Boolean).join(' · '))}</span>${when(e.blocker, () => `<span class="l3">${icon('question')}${esc(e.blocker)}</span>`)}`
    + `${when(e.cmds, () => `<span class="acts">${e.cmds}</span>`)}</a>`
    + each(e.steps, (s) => `<a class="${cls('row', 'step', s.held && !s.limited && 'held', o.curStep === s.label && 'cur')}" href="${O.go(s.go || 'worker-running')}">${O.stateGlyph(s.limited ? 'limited' : s.held ? 'held' : 'working')}<span class="label">${esc(s.label)}</span>${O.pips(s.pips)}<span class="doing">${esc(s.doing)}</span></a>`)
    + when(e.more || e.tail, () => `<div class="fold-line">${esc([e.more, e.tail].filter(Boolean).join(' · '))}</div>`);

  // convoRow draws a conversation's row and, hanging under it, every epic and contract it steers. An epic belongs to
  // exactly one conversation, so it is drawn once, under that conversation, and never on its own.
  O.convoRow = (r, o = {}) => O.sessionRow(r, { cur: o.cur === r.id, open: o.open === r.id, menu: o.menu === r.id })
    + when(r.epics && r.epics.length, () => `<div class="steers" aria-label="what this conversation steers">${each(r.epics, (e) => O.epicRow(e, { cur: o.cur === e.id, open: o.open === e.id, curStep: o.curStep }))}</div>`);
  // epicsIn gives every epic and contract the conversations of a directory steer.
  O.epicsIn = (d) => (d.rows || []).flatMap((r) => r.epics || []);

  // dir draws a Dir: a project. Its head is the project: its name, its path, and the way to its page. What it holds
  // hangs from a line under the head: its conversations, each with what it steers under it, then one line for what
  // has ended.
  O.dir = (d, o = {}) => {
    const full = d.full || (d.path === 'home' ? 'your home directory' : `${d.path}/${d.name}`);
    const head = `<div class="dir-head"><button class="fold" aria-expanded="${!d.collapsed}" aria-label="${d.collapsed ? 'Unfold' : 'Fold'} ${esc(d.name)}">${icon(d.collapsed ? 'chevronRight' : 'chevronDown')}</button>`
      + `<a class="proj" href="${O.go('project')}" title="Everything working in ${esc(full)}">${icon('folder')}<span class="nm">${esc(d.name)}</span><span class="path">${esc(full)}</span></a>`
      + `<span class="end">${when(d.host, () => O.chip(d.host, '', 'host'))}${when(d.collapsed && d.waiting, () => `<span class="is-attention">${d.waiting} waiting</span>`)}${when(d.collapsed, () => `<span>${d.count}</span>`)}</span></div>`;
    if (d.collapsed) return `<section class="dir" aria-label="${esc(d.name)}">${head}</section>`;
    const rows = each(d.rows, (r) => O.convoRow(r, o));
    const ended = when(d.ended, () => `<button class="fold-line" aria-expanded="${!!d.ended.open}">${icon(d.ended.open ? 'chevronDown' : 'chevronRight')}<span>${esc(d.ended.label)}</span></button>`
      + when(d.ended.open, () => each(d.ended.rows, (r) => O.convoRow(r, o))));
    return `<section class="dir" aria-label="${esc(d.name)}">${head}<div class="dir-kids">${rows}${ended}</div></section>`;
  };

  // inbox draws the Inbox. i: { dirs, cur, open, menu, curStep, attn: { waiting, fresh }, filter: '' | 'waiting' | 'new',
  //   search, state: '' | 'loading' | 'empty' | 'error' | 'nomatch', view: '' | 'archived', pinned, errors: [] }
  O.inbox = (i) => {
    const touch = O.touch();
    const attn = i.attn || {};
    const head = `<header class="inbox-head">${O.mark()}<span class="name">orrery</span><span class="end">`
      + `${O.iconBtn('search', 'Search sessions and epics', { kbd: '', tip: touch ? undefined : 'Search  /' })}`
      + `${O.bell(i.unread === undefined ? 2 : i.unread)}${when(touch, () => O.iconBtn('pools', 'Pools') + O.iconBtn('gear', 'Settings'))}`
      + `${O.btn('New', { icon: 'plus', kbd: touch ? '' : 'N', tip: 'Start a session, or find one', tipSide: 'left', go: 'new-empty' })}</span></header>`;
    const chips = when(i.view !== 'archived' && (attn.waiting || attn.fresh), () => `<div class="inbox-attn">`
      + `${when(attn.waiting, () => O.fchip(attn.waiting === 1 ? 'waits on you' : 'wait on you', attn.waiting, 'attention', i.filter === 'waiting', i.filter === 'waiting' ? 'inbox-home' : 'inbox-waiting'))}`
      + `${when(attn.fresh, () => O.fchip('new', attn.fresh, 'new', i.filter === 'new', i.filter === 'new' ? 'inbox-home' : 'inbox-new'))}</div>`);
    const search = when(i.search !== undefined, () => `<div class="inbox-search">${O.field({ icon: 'search', value: i.search, placeholder: 'Search sessions and epics', state: 'focused', trail: O.kbd('esc') })}</div>`);
    const errors = each(i.errors, (e) => O.inote(e, 'error'));
    let body;
    if (i.state === 'loading') body = `<div class="blank"><span>loading…</span></div>`;
    else if (i.state === 'error') body = `<div class="blank">${O.inote('The server did not answer.', 'error')}<span>Trying again every few seconds.</span>${O.btn('Try now', { icon: 'retry' })}</div>`;
    else if (i.state === 'empty') body = `<div class="blank"><span class="t-body">Nothing is running.</span><span>Start a session in a directory and it shows here, grouped by that directory.</span>${O.btn('New', { variant: 'primary', icon: 'plus', go: 'new-empty' })}</div>`;
    else if (i.state === 'nomatch') body = `<div class="blank"><span class="t-body">Nothing matches “${esc(i.search)}”.</span><span>Search looks at titles, directories, hosts and states.</span></div>`;
    else body = each(i.dirs, (d) => O.dir(d, i));
    const title = when(i.view === 'archived', () => `<div class="inbox-attn">${O.sec('Archived', { n: i.count })}</div>`);
    const foot = `<footer class="inbox-foot">${i.view === 'archived'
      ? O.btn('Back to the inbox', { variant: 'quiet', icon: 'back', go: 'inbox-home' })
      : O.btn('Archived', { variant: 'quiet', icon: 'archive', kbd: touch ? '' : 'v', go: 'inbox-archived' })}`
      + `<span class="end">${when(!touch, () => O.iconBtn('keys', 'Keys', { tip: 'Keys  ?', tipSide: 'left', go: 'keys' }) + O.iconBtn('sidebar', 'Fold the inbox to its rail', { tip: i.pinned ? 'Unpin the inbox  B' : 'Fold the inbox to its rail  b', tipSide: 'left', go: 'inbox-rail' }))}</span></footer>`;
    return `<nav class="inbox" aria-label="inbox">${head}${chips}${title}${search}${errors}<div class="inbox-rows">${body}</div>${foot}</nav>`;
  };

  // ---------- title row and strip ----------

  // strip draws a MeterStrip. s: { host, stats: [meter], ctx, other }. other: a host this server cannot read.
  O.strip = (s) => {
    if (!s) return '';
    const touch = O.touch();
    return `<div class="strip" aria-label="host and context">${when(s.host, () => `<span class="host">${icon('host')}${esc(s.host)}</span>`)}`
      + `${when(s.other, '<span class="other">on another host: no meters</span>')}`
      + `${each(s.stats, (m, i) => O.meter({ series: (i % 5) + 1, ...m }, touch))}${when(!touch, () => O.ctx(s.ctx))}</div>`;
  };

  // bell draws the control that opens the notice centre, counting the unread.
  O.bell = (n, o = {}) => O.badged(O.iconBtn('bell', 'Notices', { tip: n ? `Notices · ${n} unread` : 'Notices', tipSide: o.tipSide || 'left', go: 'notices-centre' }), n, 'new');

  // titleRow draws a TitleRow. t: {
  //   back (scenario id), crumbs: [{ t, go }], dir, here, state (html), cmds (html), menu (html), strip, note, folded }
  O.titleRow = (t) => {
    const touch = O.touch();
    if (touch) {
      if (t.folded) return '';
      return `<header class="titlerow"><div class="line1"><span class="lead">${O.iconBtn('back', 'Back', { go: t.back || 'inbox-home' })}</span>`
        + `<div class="heading"><span class="dirline">${esc([[...(t.crumbs || []).map((c) => c.t), t.dir].filter(Boolean).join(' / '), t.strip && t.strip.host ? `on ${t.strip.host}` : ''].filter(Boolean).join(' · '))}</span><span class="${cls('here', t.one && 'one')}">${esc(t.here)}</span></div>`
        + `${when(t.strip && t.strip.ctx, () => O.ctx(t.strip.ctx, { bare: true }))}${when(t.touchCmds !== false, () => O.iconBtn('more', 'More'))}</div>`
        + `${when(t.state, () => `<div class="line1 state">${t.state}</div>`)}`
        + `${when(t.strip && !t.noStrip, () => O.strip(t.strip))}</header>${when(t.note, () => `<div class="page-note">${O.inote(t.note, 'error')}</div>`)}`;
    }
    const lead = when(t.back, () => O.iconBtn('back', 'Back', { tip: 'Back  h', tipSide: 'right', go: t.back }));
    const crumbs = `<nav class="crumbs" aria-label="where you are">${each(t.crumbs, (c, i) => `${c.go ? `<a class="${cls('crumb', i === 0 && 'dir')}" href="${O.go(c.go)}">${esc(c.t)}</a>` : `<span class="${cls('crumb', i === 0 && 'dir')}">${esc(c.t)}</span>`}<span class="crumb sep">/</span>`)}`
      + `${when(t.dir, () => `<span class="crumb dir">${esc(t.dir)}</span><span class="crumb sep">/</span>`)}<h1 class="crumb here" title="${esc(t.here)}">${esc(t.here)}</h1></nav>`;
    return `<header class="titlerow"><div class="line1">${when(lead, () => `<span class="lead">${lead}</span>`)}${crumbs}${when(t.state, () => `<span class="state">${t.state}</span>`)}`
      + `<span class="cmds">${t.cmds || ''}${t.menu || ''}</span></div>${when(t.line2, () => `<div class="line2">${t.line2}</div>`)}${when(!t.noStrip, () => O.strip(t.strip))}</header>`
      + when(t.note, () => `<div class="page-note">${O.inote(t.note, 'error')}</div>`);
  };

  // The commands a conversation's title row carries at the desk: find, a window of its own, and the rest behind More.
  O.convoCmds = () => `${O.iconBtn('search', 'Find in this conversation', { tip: 'Find  /', tipSide: 'left', go: 'convo-find' })}`
    + `${O.iconBtn('open', 'Open in a new window', { tipSide: 'left' })}`
    + `${O.iconBtn('more', 'Archive, hide meters, keys', { tipSide: 'left', go: 'more-menu' })}`;

  // epicPin draws an EpicPin. p: { slug, gauge, words, blocker, go }
  O.epicPin = (p) => `<a class="epic-pin" href="${O.go(p.go)}">${O.gauge(p.gauge)}<span class="slug">${esc(p.slug)}</span><span class="words">${esc(p.words)}</span>`
    + `${when(p.blocker, () => `<span class="blocker">${esc(p.blocker)}</span>`)}<span class="end"><span>open the epic</span>${icon('chevronRight')}</span></a>`;

  // ---------- pages ----------

  // page draws one page: a title row, what is pinned under it, a body, a foot. The notices lie over the body.
  O.page = (p) => `<section class="${cls('page', p.class)}" aria-label="${esc(p.label || 'page')}">${p.title || ''}${p.pinned || ''}`
    + `<div class="page-body">${when(p.notices, () => O.notices(p.notices))}${p.body || ''}${p.foot || ''}${p.over || ''}</div></section>`;

  // ---------- work: what a conversation has started ----------

  // workCard draws a WorkCard: an epic this conversation started, live. e: { slug, gauge, tone, state, track: five of
  //   'done' | 'now' | '', now, asks, count, go }
  O.workCard = (e) => `<div class="${cls('wcard', e.tone)}"><div class="top">${O.gauge(e.gauge)}<span class="slug">${esc(e.slug)}</span><span class="end">${O.pill(e.state, e.pill === undefined ? e.tone : e.pill)}</span></div>`
    + `<div class="mtrack" title="contract, build, review, release, report">${each(e.track, (st) => `<i class="${st}"></i>`)}</div>`
    + `${when(e.now, () => `<p class="now">${esc(e.now)}</p>`)}`
    + `${when(e.asks, () => `<div class="asks">${icon('question')}<span>${esc(e.asks)}</span>${O.btn(e.act || 'Answer', { variant: 'primary', go: e.go })}</div>`)}`
    + `<div class="foot"><span>${esc(e.count || '')}</span><a class="link end" href="${O.go(e.go)}">Open</a></div></div>`;

  // lrow draws one row of a list panel: a state, what it is, how long, and a second line.
  const lrow = (state, title, age, l2) => `<div class="row"><span class="dot"></span>${state}<span class="title">${esc(title)}</span><span class="age">${esc(age || '')}</span>${when(l2, () => `<span class="l2">${l2}</span>`)}</div>`;

  // tile draws a Tile on a rail. t: { text | icon | gauge | mark, cap, on, n, nKind, dots: ['live' | 'new'], tone, cur, bare,
  //   title, go, tip }. A tile with a cap names itself under its face: the right rail's tiles do, since nobody expects
  //   work on the right. One that is on has its panel open beside it.
  O.tile = (t) => {
    const tag = t.go ? 'a' : 'button';
    const at = attrs({ href: t.go ? O.go(t.go) : undefined, title: t.tip ? undefined : t.title, 'aria-label': t.title, 'data-tip': t.tip, 'data-tip-side': t.tipSide });
    const face = `${t.mark ? O.mark('tile-mark') : t.gauge ? O.gauge(t.gauge) : t.icon ? icon(t.icon) : esc(t.text)}${when(t.n, () => `<span class="${cls('n', t.nKind)}">${esc(t.n)}</span>`)}`
      + `${when(t.dots && t.dots.length, () => `<span class="dots">${each(t.dots, (d) => `<i class="${d}"></i>`)}</span>`)}`;
    const look = cls('tile', t.tone, t.cur && 'cur', t.bare && 'bare', t.mark && 'brand');
    if (!t.cap) return `<${tag} class="${cls(look, t.showTip && 'show-tip')}"${at}>${face}</${tag}>`;
    return `<${tag} class="${cls('tile-w', t.on && 'on', t.on && t.tone, t.showTip && 'show-tip')}"${at}><span class="${look}">${face}</span><span class="cap">${esc(t.cap)}</span></${tag}>`;
  };

  // ---------- the right rail and its panels ----------

  // activitiesOf gives what a conversation steers, as the right rail's tiles, in order: its contract, its epics, its
  // sub-agents, and what it left running. Each opens its own panel beside the conversation.
  O.activitiesOf = (w) => {
    if (!w) return [];
    const short = (slug) => slug.split('-')[0];
    return [
      ...(w.contract ? [{ key: w.contract.slug, kind: 'contract', gauge: w.contract.gauge, tone: 'attention', n: '?', cap: short(w.contract.slug), title: `${w.contract.slug}: a contract waits for you`, go: w.contract.go }] : []),
      ...(w.epics || []).map((e) => ({ key: e.slug, kind: 'epic', gauge: e.gauge, tone: e.asks ? 'attention' : '', n: e.asks ? '?' : '', cap: short(e.slug), title: `${e.slug}: ${e.asks || e.state}`, go: e.go })),
      ...(w.agents && w.agents.length ? [{ key: 'agents', kind: 'agents', icon: 'agent', tone: 'live', n: w.agents.length, nKind: 'live', cap: 'agents', title: `${w.agents.length} sub-agents running`, go: 'dock-running' }] : []),
      ...(w.background && w.background.length ? [{ key: 'running', kind: 'running', icon: 'shell', n: w.background.length, nKind: 'quiet', cap: 'running', title: `${w.background.length} left running`, go: 'dock-background' }] : []),
    ];
  };

  // railGroups gives the right rail's groups, each named once: the contract, the epics, the sub-agents, what was left
  // running. A group of more than three is one tile of dots, one dot per thing in its colour.
  const GROUP = { contract: 'contract', epic: 'epics', agents: 'agents', running: 'running' };
  O.railGroups = (w) => {
    const groups = [];
    O.activitiesOf(w).forEach((a) => {
      const g = groups.find((x) => x.kind === a.kind);
      if (g) g.items.push(a); else groups.push({ kind: a.kind, items: [a] });
    });
    return groups.map((g) => ({ ...g, label: g.kind === 'epic' && g.items.length === 1 ? 'epic' : GROUP[g.kind], many: g.items.length > 3 }));
  };
  // groupTile draws a many-thing group as one tile: a grid of dots, one per thing, and a count; it opens a picker.
  const DOT = { attention: 'attention', live: 'live', engine: 'engine', '': 'quiet' };
  const groupTile = (g, on, o = {}) => {
    const asks = g.items.filter((x) => x.tone === 'attention').length;
    const lit = g.items.find((x) => x.key === on);
    return `<${o.go ? 'a' : 'button'} class="${cls('tile-w', 'group', lit && 'on', lit && lit.tone)}"${attrs({ href: o.go ? O.go(o.go) : undefined, title: `${g.items.length} ${g.label}: ${g.items.map((x) => x.key).join(', ')}`, 'aria-label': `${g.items.length} ${g.label}` })}>`
      + `<span class="${cls('tile', asks && 'attention', lit && 'cur')}"><span class="dotgrid">${each(g.items.slice(0, 9), (x) => `<i class="${DOT[x.tone] || 'quiet'}"></i>`)}</span>`
      + `<span class="${cls('n', asks ? '' : 'quiet')}">${asks ? '?' : g.items.length}</span></span><span class="cap">${g.items.length}</span></${o.go ? 'a' : 'button'}>`;
  };
  // railGroup draws one group: its name, then its tiles. A one-thing group of a list (agents, running) is named
  // by its group, so its tile has no word of its own; an epic's or a contract's tile keeps its short name.
  const railGroup = (g, on, o = {}) => `<div class="${cls('rgroup', g.kind)}"><span class="rlabel">${esc(g.label)}</span>`
    + `${g.many ? groupTile(g, on, { go: o.pickGo }) : each(g.items, (a) => O.tile({ ...a, cap: a.kind === 'epic' || a.kind === 'contract' ? a.cap : undefined, cur: a.key === on, on: a.key === on, tipSide: o.tipSide }))}`
    + `${when(o.pick, () => O.railPicker(g))}</div>`;

  // workRail draws the right rail: what the conversation steers, in named groups, always there; the thing whose panel
  // is open lit. With nothing started it is an empty rail, so the reading column is in the same place in every
  // conversation. o.pick: the group whose picker is open.
  O.workRail = (w, on, o = {}) => `<aside class="rail end" aria-label="what this conversation steers">${each(O.railGroups(w), (g) => railGroup(g, on, { tipSide: 'left', pickGo: 'work-rail-group', pick: o.pick === g.kind }))}</aside>`;

  // railPicker draws the picker a group of dots opens: each thing in the group, its state, and the way into its panel.
  O.railPicker = (g) => `<div class="rpick" role="menu"><div class="menu-head">${g.items.length} ${esc(g.label)}</div>${each(g.items, (x) => `<a class="rpick-row" role="menuitem" href="${O.go(x.go)}">${x.gauge ? O.gauge(x.gauge) : icon(x.icon || 'doc')}<span class="t">${esc(x.key)}</span><span class="${cls('s', x.tone === 'attention' && 'is-attention')}">${esc(x.title.split(': ').slice(1).join(': '))}</span></a>`)}</div>`;

  // tileStrip draws the same groups on touch, as a row under the title row; a tile opens its thing as a page, or a
  // list as a sheet.
  // Its last control folds the header away until you open it again (the same as the folded line's chevron).
  O.tileStrip = (w, on) => `<nav class="tilestrip" aria-label="what this conversation steers">${each(O.railGroups(w), (g) => railGroup(g, on))}${O.foldHead()}</nav>`;
  O.foldHead = () => `<a class="fold-head" href="${O.go('phone-head-hidden')}" aria-label="Hide the header" title="Hide the header">${icon('chevronUp')}</a>`;

  // sidePanel draws one thing opened from the right rail, beside the conversation. It lies on the rail's ground with
  // the thing inset as a card whose top edge takes the thing's colour, so it reads as the lit tile opened.
  O.sidePanel = (p) => `<aside class="page side rpanel" aria-label="${esc(p.label || 'panel')}"><div class="${cls('rcard', p.tone)}">${p.page}</div></aside>`;

  // listPanel draws the panel of one of a conversation's lists: its sub-agents, or what it left running. At the desk it
  // is a side panel; on touch, a sheet.
  const LISTS = {
    agents: { name: 'Sub-agents', tone: 'live', note: () => 'This turn is waiting on them.', rows: (w) => each(w.agents, (t) => lrow(O.stateGlyph('working'), t.desc, t.clock, `<span class="name">${esc(t.agent || 'agent')}</span> ${esc(t.activity || '')}`)) },
    running: { name: 'Left running', tone: '', note: () => 'Nothing waits on these, and the session can be done while they run.', rows: (w) => each(w.background, (t) => lrow(O.stateGlyph('idle'), t.desc, t.clock, esc(t.activity || 'shell'))) },
  };
  O.listBody = (kind, w) => `<div class="plist"><p class="inote">${esc(LISTS[kind].note(w))}</p>${LISTS[kind].rows(w)}</div>`;
  O.listPanel = (kind, w, close) => O.sidePanel({ label: LISTS[kind].name, tone: LISTS[kind].tone, page: O.page({
    label: LISTS[kind].name, class: 'rpage', title: O.recordTitle({ here: LISTS[kind].name, one: true, close }), body: `<div class="spage"><div class="inner">${O.listBody(kind, w)}</div></div>`,
  }) });
  O.listSheet = (kind, w) => O.sheet(O.listBody(kind, w), { title: LISTS[kind].name });

  // inboxRail draws the inbox at rest: its control, a tile per project, then what the inbox's head and foot hold.
  O.inboxRail = (i) => {
    const waits = (d) => (d.rows || []).filter((r) => r.state === 'waiting').length + O.epicsIn(d).filter((e) => e.blocker).length;
    const live = (d) => (d.rows || []).some((r) => r.state === 'working') || O.epicsIn(d).some((e) => (e.steps || []).length);
    const fresh = (d) => (d.rows || []).some((r) => r.unseen);
    const holds = (d) => (d.rows || []).some((r) => r.id === i.open) || O.epicsIn(d).some((e) => e.id === i.open);
    // A project's tile is its first two letters; when two projects would share them, the first and the last.
    const names = (i.railDirs || i.dirs).map((d) => d.name);
    const short = (name) => (names.filter((n) => n.slice(0, 2) === name.slice(0, 2)).length > 1 ? name[0] + name[name.length - 1] : name.slice(0, 2));
    return `<nav class="rail start" aria-label="inbox, at rest">${O.tile({ mark: true, bare: true, title: 'Open the inbox', tip: 'Inbox  b', tipSide: 'right', go: 'inbox-flyout', showTip: i.showTip })}<span class="rail-sep"></span>`
      + `${each(i.railDirs || i.dirs, (d) => O.tile({ text: short(d.name), tone: 'place', cur: holds(d), n: waits(d) || '', dots: [live(d) && 'live', fresh(d) && 'new'].filter(Boolean), title: `${d.name}${waits(d) ? `: ${waits(d)} wait on you` : ''}`, go: 'project' }))}`
      + `<span class="spring"></span>${O.tile({ icon: 'plus', tone: 'primary', title: 'New session', go: 'new-empty' })}${O.tile({ icon: 'search', bare: true, title: 'Search' })}`
      + `${O.tile({ icon: 'bell', bare: true, n: i.unread === undefined ? 2 : i.unread, nKind: 'new', title: 'Notices', go: 'notices-centre' })}${O.tile({ icon: 'keys', bare: true, title: 'Keys', go: 'keys' })}</nav>`;
  };

  // asksOf gives what a conversation steers that waits on you: a contract to settle, an epic with a question.
  O.asksOf = (w) => (!w ? [] : [
    ...(w.contract ? [{ kind: 'contract', slug: w.contract.slug, name: `${w.contract.slug} contract`, asks: w.contract.asks || 'waits for you to accept', act: w.contract.act || 'Read', gauge: w.contract.gauge, go: w.contract.go }] : []),
    ...(w.epics || []).filter((e) => e.asks).map((e) => ({ kind: 'epic', slug: e.slug, name: e.slug, asks: e.asks, act: e.act || 'Answer', gauge: e.gauge, go: e.go })),
  ]);
  // subject draws a SubjectBand under a conversation's title row: its goal, what it is for; then one line for each
  // thing it steers that waits on you, in words, with the way to answer. Everything it steers is on its right rail; the
  // band is only what needs you. A line whose thing is open in the panel says so instead of offering the way in.
  O.subject = (w, here) => {
    const asks = O.asksOf(w);
    const goal = w && w.goal;
    if (!goal && !asks.length) return '';
    const list = asks.slice(0, 3);
    // On touch with no tiles under it, the band carries the control that folds the header.
    const fold = O.touch() && !O.activitiesOf(w).length ? O.foldHead() : '';
    return `<div class="subject" aria-label="what this conversation is for, and what waits on you">${when(goal, () => `<div class="subj goal">${icon('goal')}<span class="what">Goal</span><span class="st">${esc(goal)}</span>${fold}</div>`)}${each(list, (x) => {
      const me = here && x.slug === here;
      return `<${me ? 'div' : 'a'} class="${cls('subj', 'attention', me && 'here')}"${attrs({ href: me ? undefined : O.go(x.go) })}>${x.gauge ? O.gauge(x.gauge) : icon(x.kind === 'contract' ? 'doc' : 'pools')}`
        + `<span class="what"><b>${esc(x.name)}</b></span><span class="st">${esc(x.asks)}</span><span class="end">${me ? `open on the right${icon('chevronRight')}` : `${esc(x.act)}${icon('chevronRight')}`}</span></${me ? 'div' : 'a'}>`;
    })}${when(asks.length > 3, () => `<div class="subj more">${icon('question')}<span class="what">and ${asks.length - 3} more ask</span><span class="st">${esc(asks.slice(3).map((x) => x.slug).join(', '))}</span><span class="end">on the rail</span></div>`)}</div>`;
  };

  // recordTitle is the title row of a thing open in a panel: up a level when it has one (a task's epic), its crumbs,
  // its commands, and Close, which puts the panel back on its tile; its state on the line under them.
  O.recordTitle = (t) => O.titleRow({ ...t, state: undefined, line2: t.state, toggle: false, back: t.up, crumbs: t.crumbs || [], dir: undefined, noStrip: true,
    cmds: `${t.cmds || ''}${when(t.window !== false, () => O.iconBtn('open', 'Open in a new window', { tipSide: 'left' }))}${O.iconBtn('close', 'Close the panel', { tip: 'Close  ]', tipSide: 'left', go: t.close })}` });

  // endedBar stands where the dock was when the conversation steering a live epic has ended. The epic is never left
  // without one: the first way on continues in a new conversation that starts with the contract and the epic's log.
  O.endedBar = (e) => `<div class="dock"><div class="col"><div class="dock-box ended" data-state="stopped">${O.stateBar({ state: 'stopped', word: 'ended', what: e.when, controls: '' })}`
    + `<div class="ended-body"><p>${esc(e.text)}</p><div class="acts">${O.btn('Continue in a new conversation', { variant: 'primary', icon: 'plus', go: 'epic-moved' })}${O.btn('Steer from another…', { variant: 'quiet', go: 'epic-handover' })}</div></div></div></div></div>`;

  // convoPage draws a conversation's page: title row, its band (goal, and what it steers), what is pinned under it,
  // find bar, reader, dock; and at the desk, the panel open beside it and its right rail. On touch the tiles are a strip
  // under the band.
  // c: { title, band, work (null for a page that has no rail), on (the tile whose panel is open), panel (html), find,
  //   items, reader, dock, ended, notices, class, label, over, pick (the rail group whose picker is open) }
  // On touch the header has four states (c.head): open (title, meters, band, tiles); scrolled, while reading back, and
  // pinned, hidden by you until you open it, both one compact line under the title; and keyboard, with nothing at all.
  O.convoPage = (c) => {
    const head = O.touch() ? c.head || 'open' : 'open';
    const open = head === 'open';
    return O.page({
    class: c.class, label: c.label || 'conversation', notices: c.notices, over: c.over,
    title: O.titleRow(open ? c.title : { ...c.title, noStrip: true, folded: head === 'keyboard' }),
    pinned: (c.band || '') + (open ? O.subject(c.work, c.on) + when(O.touch() && O.activitiesOf(c.work).length, () => O.tileStrip(c.work, c.on)) : head === 'keyboard' ? '' : O.compactHead(c.work))
      + when(c.find, () => O.findBar(c.find)),
    body: O.reader(c.items || [], c.reader || {}),
    foot: c.ended ? O.endedBar(c.ended) : c.dock === null ? '' : O.dock(c.dock || {}),
  }) + when(c.work !== null && !O.touch(), () => (c.panel || '') + O.workRail(c.work || {}, c.on, { pick: c.pick }));
  };

  // compactHead draws the phone header folded to one line: the right rail's groups as small badges, and the way to open
  // it again. Folded, an ask is never hidden: the line turns peach and its badge shows the question mark; it does not
  // open by itself. Folded by scrolling or hidden by you, it looks the same: the difference is only that scrolling back
  // to the newest line opens the first.
  O.compactHead = (w, o = {}) => {
    const asking = O.asksOf(w);
    const asks = asking.length;
    const badge = (g) => {
      const n = g.items.length;
      const q = g.items.some((x) => x.tone === 'attention');
      const face = g.kind === 'epic' || g.kind === 'contract' ? (g.items[0].gauge ? O.gauge(g.items[0].gauge) : icon('doc')) : icon(g.items[0].icon);
      return `<span class="${cls('cb', q && 'attention')}">${face}${q ? '?' : n > 1 || g.kind === 'agents' || g.kind === 'running' ? n : ''}</span>`;
    };
    return `<a class="${cls('compact', asks && 'attention')}" href="${O.go(o.open || 'dock-full')}" aria-label="Show the header">`
      + `${when(asks, () => `<span class="cask">${asks === 1 ? `${esc(asking[0].slug)} asks` : `${asks} ask you`}</span>`)}${each(O.railGroups(w), badge)}`
      + `<span class="end">${icon('chevronDown')}</span></a>`;
  };

  // app draws the desk frame: the inbox and one, two or three pages. a: { inbox, inboxState, pages, over, narrow }
  O.app = (a) => `<div class="${cls('app', a.narrow && 'narrow')}" data-inbox="${esc(a.inboxState || 'docked')}" data-side="${esc(a.side || 'rail')}">${when(a.inbox, () => O.inboxRail(a.inbox) + O.inbox(a.inbox))}${(a.pages || []).join('')}${a.over || ''}</div>`;

  // screen draws the phone frame: one page at a time. s: html of the page, o.over: a sheet or a toast.
  O.screen = (html, o = {}) => `<div class="app touch">${html}${o.over || ''}</div>`;

  // projectPage draws a Project: everything working in one directory, as its conversations, each with what it last
  // said and, under it, the epics and contracts it steers as live cards. p: { name, path, host, counts: [[icon, text,
  //   tone]], convos: [row + said, ctx, cards: [WorkCard data]], ended }
  O.projectPage = (p) => {
    const touch = O.touch();
    const convo = (r) => `<div class="pconv"><a class="pc-row" href="${O.go(r.id)}">${O.stateGlyph(r.state)}<span class="t">${esc(r.title)}</span>`
      + `<span class="${cls('said', r.l2kind === 'asks' && 'asked')}">${when(r.l2kind === 'asks', () => icon('question'))}${when(r.tool, () => `${esc(r.tool)} `)}${esc(r.said || r.l2 || '')}</span>`
      + `<span class="end">${O.ctx(r.ctx, { bare: true })}<span class="id">${esc(r.age)}</span>${icon('chevronRight')}</span></a>`
      + `${when(r.cards && r.cards.length, () => `<div class="pc-kids"><div class="cards">${each(r.cards, O.workCard)}</div></div>`)}</div>`;
    return O.page({
      label: 'project',
      title: O.titleRow({ toggle: !touch, waiting: 6, back: touch ? 'inbox-home' : undefined, here: p.name, one: true, cmds: O.btn('New session here', { icon: 'plus', go: 'new-form' }), strip: O.W.strip(null), noStrip: touch, touchCmds: false }),
      body: `<div class="spage"><div class="inner"><section class="proj-head"><span class="path">${esc(p.path)}${when(p.host, () => ` · on ${esc(p.host)}`)}</span><div class="counts">${each(p.counts, ([ic, text, tone]) => `<span class="${tone || ''}">${icon(ic)}${esc(text)}</span>`)}</div></section>`
        + `<section>${O.sec('Conversations', { icon: 'chat', hue: '1', n: p.convos.length })}<div class="pconvs">${each(p.convos, convo)}</div></section>`
        + `${when(p.ended, () => `<section><button class="fold-line">${icon('chevronRight')}<span>${esc(p.ended)}</span></button></section>`)}</div></div>`,
      foot: when(touch, () => `<div class="cmdbar">${O.btn('New session here', { icon: 'plus', class: 'grow', go: 'new-form' })}</div>`),
    });
  };

  // ---------- over the page ----------

  // notice draws one Notice: what it is about (a gauge, a state, or an icon), who and what in two lines, when, and
  // what to do about it. x: { tone, gauge, state, icon, dir, title, body, when, act, unread }
  O.notice = (x, flat = false) => `<div class="${cls('notice', x.tone, flat && 'flat', flat && x.unread && 'unread')}" role="status">${x.gauge ? O.gauge(x.gauge) : x.state ? O.stateGlyph(x.state) : icon(x.icon || 'bell')}`
    + `<div class="t">${when(x.dir, () => `<span class="dirn">${esc(x.dir)}</span> `)}${esc(x.title)}</div><span class="when">${esc(x.when || '')}</span><div class="b">${esc(x.body)}</div>`
    + `${when(!flat, () => `<div class="acts">${O.btn(x.act || 'Open', { variant: x.tone === 'attention' ? 'primary' : '' })}${O.btn('Dismiss', { variant: 'quiet' })}</div>`)}</div>`;

  // notices draws a NoticeStack: the newest notices as they arrive. n: { list, more }
  O.notices = (n) => `<aside class="notices" aria-label="notices">${each(n.list, (x) => O.notice(x))}`
    + `${when(n.more, () => `<a class="rest link" href="${O.go('notices-centre')}">${n.more} more in Notices</a>`)}</aside>`;

  // noticeCentre draws where notices collect: every one since you last cleared them, newest first, by day.
  // days: [[day, [notice]]]. On touch it is a page.
  O.noticeList = (days) => (days.length ? each(days, ([day, list]) => `<div class="day">${esc(day)}</div><div class="nlist">${each(list, (x) => O.notice(x, true))}</div>`)
    : `<div class="blank"><span class="t-body">No notices.</span><span>Questions, endings and failures from sessions and epics you are not looking at collect here.</span></div>`);
  O.noticeCentre = (days) => `<section class="ncentre" role="dialog" aria-label="notices"><div class="head"><h2 class="t-title">Notices</h2>${when(days.length, () => O.btn('Mark all read', { variant: 'quiet' }))}${O.iconBtn('close', 'Close', { tip: 'Close  esc', tipSide: 'left' })}</div>${O.noticeList(days)}</section>`;

  // panel draws a Panel over the page, dimming it.
  O.panel = (title, body, o = {}) => `<div class="scrim"><section class="${cls('panel', o.wide && 'wide')}" role="dialog" aria-label="${esc(title)}"><div class="head"><h2 class="t-title">${esc(title)}</h2>${when(o.hint, () => `<span class="t-small ink-3">${o.hint}</span>`)}${O.iconBtn('close', 'Close', { tip: 'Close  esc', tipSide: 'left' })}</div>${body}</section></div>`;

  // sheet draws a Sheet rising from the foot of a phone screen.
  O.sheet = (body, o = {}) => `<div class="sheet-scrim"><section class="sheet" role="dialog">${when(o.title, () => `<div class="sheet-head"><b>${esc(o.title)}</b>${esc(o.sub || '')}</div>`)}${body}</section></div>`;

  // toast draws a Toast.
  O.toast = (text, action) => `<div class="toast" role="status"><span class="grow">${esc(text)}</span>${when(action, () => O.btn(action, { variant: 'quiet' }))}</div>`;

  // The key list, grouped by what the keys are for. Every key the client answers to is here.
  O.KEYS = [
    ['Moving', [[['j', 'k'], 'down and up the inbox'], [['g g', 'G'], 'first and last row'], [['w'], 'next thing that waits on you'], [['n'], 'next new row'], [['h'], 'back one page'], [['⏎'], 'open the row and go to its reply box'], [['⇧ ⇥'], 'from a reply box, back to the inbox']]],
    ['The inbox', [[['b'], 'bring the inbox out from its rail; again to tuck it back'], [['B'], 'pin the inbox beside the page'], [['/'], 'search sessions and epics'], [['v'], 'archived'], [['A'], 'archive the row'], [['x'], 'mark it seen'], [['e'], 'mark it seen and go to the next new row'], [['N', '⌘K'], 'new session, or find one']]],
    ['Reading', [[['i'], 'go to the reply box'], [['/'], 'in an empty reply box: find in the conversation'], [['⇞', '⇟'], 'in an empty reply box: page the conversation'], [['o'], 'open or close a compaction'], [[']'], 'shut the open panel, or reopen the last one'], [['m'], 'hide or show the meters']]],
    ['Answering and stopping', [[['1', '…', '9'], 'answer the question on screen with that option'], [['⏎'], 'send'], [['⇧ ⏎'], 'a new line'], [['↑'], 'in an empty reply box: edit the newest queued message'], [['esc'], 'take queued messages back, then interrupt a working turn']]],
    ['Everywhere', [[['?'], 'this list'], [['esc'], 'close a menu, a panel, find, or this list']]],
  ];
  O.keyList = () => O.panel('Keys', `<div class="keygroups">${each(O.KEYS, ([group, lines]) => `<div class="keygroup"><span class="t-label">${esc(group)}</span>${each(lines, ([ks, what]) => `<div class="keyline"><span class="ks">${each(ks, (k) => (k === '…' ? '<span class="ink-4">…</span>' : O.kbd(k)))}</span><span>${esc(what)}</span></div>`)}</div>`)}</div>`, { wide: true, hint: `${O.kbd('?')} or ${O.kbd('esc')} closes` });

  // newPanel draws the New panel (the palette). n: { query, state: 'empty' | 'working' | 'results' | 'noresults' |
  //   'form' | 'error', rows: [{ kind, title, note, first }], cur, form: { dir, text, starting } }
  O.NEW_KINDS = { jump: 'go to', resume: 'resume', continue: 'continue', pane: 'start in', fresh: 'start in' };
  O.newBody = (n) => {
    const ask = O.field({ icon: 'search', value: n.query, placeholder: 'A directory, a session, or what you want to start', state: n.state === 'form' ? '' : 'focused', trail: n.state === 'working' ? '<span class="count">asking…</span>' : O.kbd('⏎') });
    const rows = when(n.rows && n.state !== 'form', () => `<div class="rows">${each(n.rows, (r, i) => `<button class="${cls('pick-row', n.cur === i && 'cur')}"><span class="k">${esc(O.NEW_KINDS[r.kind] || r.kind)}</span><span class="t">${esc(r.title)}</span>${when(r.first, () => O.chip('starts with your words', 'live'))}<span class="n">${esc(r.note)}</span></button>`)}</div>`);
    const form = when(n.state === 'form', () => `<div class="form-row"><span class="t-label">Directory</span>${O.field({ icon: 'folder', value: n.form.dir })}</div>`
      + `<div class="form-row"><span class="t-label">First message</span><div class="field area focused">${esc(n.form.text)}</div></div>`
      + `<div class="foot">${O.btn('Other results', { variant: 'quiet', icon: 'back' })}<span class="end">${when(n.form.starting, '<span class="inote">starting…</span>')}${O.btn('Start the session', { variant: 'primary', kbd: O.touch() ? '' : '⌘⏎', state: n.form.starting ? 'disabled' : '' })}</span></div>`);
    const hint = n.state === 'empty' ? O.inote('Name a directory to start a session there, or say a few words about a session to find it.')
      : n.state === 'noresults' ? O.inote(`Nothing found for “${n.query}”. Try a directory name.`)
        : n.state === 'error' ? O.inote(n.error || 'The search did not finish.', 'error') : '';
    return `${ask}${hint}${rows}${form}`;
  };
  O.newPanel = (n) => (O.touch() ? O.sheet(`<div class="sheet-body">${O.newBody(n)}</div>`, { title: 'New session' }) : O.panel('New session', O.newBody(n), { hint: `${O.kbd('N')} or ${O.kbd('⌘K')}` }));

  // confirmSheet draws a ConfirmButton's Asking state on touch.
  O.confirmSheet = (question, yes, no = 'Keep it running', sub = '') => O.sheet(`<div class="foot">${O.btn(no)}${O.btn(yes, { variant: 'danger' })}</div>`, { title: question, sub });
})();
