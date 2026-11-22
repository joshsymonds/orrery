// Round 6 wireframe: the catalogue. Foundations, then one cell per Component/State. A cell's name is the story
// name the client's own catalogue should use.
(() => {
  'use strict';
  const { esc, each, icon, W } = O;
  const out = [];
  const h2 = (t) => out.push(`<h2>${esc(t)}</h2>`);
  // comp adds a component: its name, a line on what it is, and its states as [name, html, stage classes].
  const comp = (name, what, cells, o = {}) => out.push(`<section><h3>${esc(name)}<small>${esc(what)}</small></h3><div class="cells" style="--cell:${o.cell || 300}px">`
    + each(cells, ([state, html, stage]) => `<div class="cell"><span class="name"><b>${esc(name)}</b>/${esc(state)}</span><div class="cellbox ${stage || ''}${o.touch ? ' touch' : ''}">${html}</div></div>`) + `</div></section>`);

  // ---------- foundations ----------
  const css = getComputedStyle(document.documentElement);
  const val = (name) => css.getPropertyValue(name).trim();
  const sw = (name, note) => `<div class="swatch"><i class="sw" data-var="${name}"></i><b>${esc(name.replace('--o-', ''))}</b><span>${esc(note || '')}</span></div>`;
  out.push(`<h1>Catalogue</h1>`);
  h2('Foundations');
  out.push(`<section><h3>Surfaces and lines<small>where a thing sits</small></h3><div class="swatches">${each([['--o-surface-side', 'the inbox column'], ['--o-surface-well', 'sunk: code, results, a block from someone else, the dock'], ['--o-surface-page', 'the page'], ['--o-surface-raised', 'lifted: your block, the open row, buttons, chips'], ['--o-surface-pressed', 'a pressed control'], ['--o-line-soft', 'hairlines'], ['--o-line-firm', 'a control’s or an overlay’s outline'], ['--o-line-glyph', 'strokes that are not text']], ([n, d]) => sw(n, d))}</div></section>`);
  out.push(`<section><h3>Ink<small>four, and nothing is dimmed with opacity</small></h3><div class="swatches">${each([['--o-ink-1', '11.3:1 · content'], ['--o-ink-2', '7.4:1 · supporting words'], ['--o-ink-3', '5.8:1 · meta and machinery'], ['--o-ink-4', '4.4:1 · only what repeats or is not information']], ([n, d]) => sw(n, d))}</div></section>`);
  out.push(`<section><h3>Roles<small>a colour means one thing</small></h3><div class="swatches">${each([['--o-role-you', 'you said it · focus · the one action'], ['--o-role-attention', 'it waits on you'], ['--o-role-live', 'running now'], ['--o-role-ok', 'a verdict: passed'], ['--o-role-bad', 'a verdict: failed'], ['--o-role-halt', 'cut short'], ['--o-role-engine', 'the engine said it'], ['--o-role-ref', 'a name you can open'], ['--o-role-new', 'new since you looked']], ([n, d]) => sw(n, d))}</div></section>`);
  out.push(`<section><h3>Series, place, prose and code<small>hues that tell things apart or colour reading text; none is a state</small></h3><div class="swatches">${each([['--o-role-place', 'a directory'], ['--o-series-1', 'first of a series'], ['--o-series-2', 'second'], ['--o-series-3', 'third'], ['--o-series-4', 'fourth'], ['--o-series-5', 'fifth'], ['--o-prose-heading', 'a heading in a reply'], ['--o-prose-code', 'inline code'], ['--o-syntax-keyword', 'code: keyword'], ['--o-syntax-string', 'code: string'], ['--o-syntax-number', 'code: number'], ['--o-syntax-function', 'code: call'], ['--o-syntax-type', 'code: type'], ['--o-syntax-operator', 'code: operator']], ([n, d]) => sw(n, d))}</div></section>`);
  out.push(`<section><h3>Type<small>five levels, one face</small></h3><div class="specimen">${each([['label', 'Section head · YOU'], ['small', 'Machinery and meta: a tool line, an age, a chip, a button'], ['body', 'Reading text, your words, a row’s title'], ['title', 'The page’s title'], ['display', '2 of 7']], ([n, s]) => `<span class="k">type.${n}</span><span class="k">${esc(val(`--o-type-${n}-size`))} / ${esc(val(`--o-type-${n}-line`))}</span><span class="t-${n}">${esc(s)}</span>`)}</div></section>`);
  out.push(`<section><h3>Space, radius, size<small>the only lengths there are</small></h3><div class="scale">${each([2, 4, 8, 12, 16, 24, 32, 48], (n) => `<div><i class="sp" data-size="${n}"></i>${n}</div>`)}</div></section>`);

  // ---------- primitives ----------
  h2('Primitives');
  comp('Kbd', 'a key cap; never drawn on touch', [['Default', O.kbd('j') + O.kbd('⌘K'), 'row'], ['Active', O.kbd('1', 'active'), 'row'], ['Attention', O.kbd('1', 'attn'), 'row']], { cell: 200 });
  comp('Button', 'height 32 at the desk, 48 on touch', [
    ['Default', O.btn('Talk', { icon: 'chat' }), 'row'], ['Quiet', O.btn('Archived', { variant: 'quiet', icon: 'archive' }), 'row'], ['Primary', O.btn('Answer with Keep the pin', { variant: 'primary' }), 'row'], ['Danger', O.btn('Stop…', { variant: 'danger' }), 'row'], ['Stopper', O.btn('Interrupt', { class: 'stopper', icon: 'stop' }), 'row'],
    ['Hover', O.btn('Talk', { state: 'hover', icon: 'chat' }), 'row'], ['Pressed', O.btn('Talk', { state: 'pressed', icon: 'chat' }), 'row'], ['Focused', O.btn('Talk', { state: 'focused', icon: 'chat' }), 'row'], ['Disabled', O.btn('Accept', { state: 'disabled' }), 'row'],
  ], { cell: 240 });
  comp('IconButton', 'square; its label is its tip', [['Back', O.iconBtn('back', 'Back'), 'row'], ['Close', O.iconBtn('close', 'Close'), 'row'], ['More', O.iconBtn('more', 'More'), 'row'], ['Send', O.btn('Send', { variant: 'primary', icon: 'send', iconOnly: true }), 'row'], ['WithTip', O.iconBtn('sidebar', 'Show the inbox', { tip: 'Show the inbox  b', class: 'show-tip', tipSide: 'right' }) + '<span class="t-small ink-3">&nbsp;</span>', 'row']], { cell: 200 });
  comp('ConfirmButton', 'a command that asks first', [['Rest', O.btn('Stop…', { variant: 'danger' }), 'row'], ['Asking', O.confirm('Stop rota-export?', 'Stop rota-export', 'Keep it running'), 'row']], { cell: 460 });
  comp('Tip', 'what a control does, with its key', [['Default', O.tip('Open beside', '|'), 'row']], { cell: 220 });
  comp('Field', 'a one-line input', [['Empty', O.field({ icon: 'search', placeholder: 'Search sessions and epics' })], ['Filled', O.field({ icon: 'search', value: 'tide' })], ['Focused', O.field({ icon: 'search', value: 'tide', state: 'focused', count: '2 of 3' })], ['Error', O.field({ icon: 'host', value: 'https://halyrd', state: 'error' })]]);
  comp('Chip', 'a short verdict or tag', [['Neutral', O.chip('recommended'), 'row'], ['Ok', O.chip('holds', 'ok', 'check'), 'row'], ['Bad', O.chip('checks failed', 'bad', 'cross'), 'row'], ['Attention', O.chip('severe care', 'attention'), 'row'], ['Live', O.chip('running', 'live'), 'row'], ['Engine', O.chip('accepted', 'engine'), 'row']], { cell: 180 });
  comp('FilterChip', 'narrows the inbox', [['Off', O.fchip('wait on you', 6, 'attention'), 'row side'], ['On', O.fchip('wait on you', 6, 'attention', true), 'row side'], ['New', O.fchip('new', 2, 'new'), 'row side']], { cell: 200 });
  comp('StateGlyph', 'a session’s or a step’s state', O.STATES.map((s) => [s[0].toUpperCase() + s.slice(1), `${O.stateGlyph(s)}<span class="t-small ink-2">${esc(O.STATE_WORDS[s])}</span>`, 'row']), { cell: 180 });
  comp('Pill', 'a state as a word', [['Neutral', O.pill('paused · 3h'), 'row'], ['Live', O.pill('building · 49m', 'live'), 'row'], ['Attention', O.pill('asks you a question · 2m', 'attention'), 'row'], ['Ok', O.pill('released · 09:52', 'ok'), 'row'], ['Engine', O.pill('releasing · 4m', 'engine'), 'row'], ['WithGlyph', O.pill('checking · 2:41', 'live', { state: 'working' }), 'row']], { cell: 240 });
  comp('Meter', 'one host stat; its own colour until warn or crit', [['Series1', O.meter({ series: 1, ...W.stats[0] }), 'row'], ['Series2', O.meter({ series: 2, ...W.stats[1] }), 'row'], ['Series3', O.meter({ series: 3, ...W.stats[2] }), 'row'], ['Series4NoBar', O.meter({ series: 4, ...W.stats[3] }), 'row'], ['Warn', O.meter(W.statsHot[0]), 'row'], ['Crit', O.meter(W.statsHot[1]), 'row'], ['Stacked', O.meter({ series: 2, ...W.stats[1] }, true), 'row']], { cell: 220 });
  comp('ContextMeter', 'five blocks of twenty', [['Ok', O.ctx(W.ctx.low), 'row'], ['Warn', O.ctx(W.ctx.mid), 'row'], ['Crit', O.ctx(W.ctx.high), 'row'], ['Bare', O.ctx(W.ctx.mid, { bare: true }), 'row']], { cell: 300 });
  comp('Gauge', 'an epic at a glance: done, started, planned', [['Building', O.gauge({ done: 2, started: 3, total: 7, tone: 'live' }), 'row'], ['AsksYou', O.gauge({ done: 2, started: 3, total: 7, tone: 'attention', inner: 'dot' }), 'row'], ['NoTasks', O.gauge({ total: 0, tone: 'live' }), 'row'], ['Paused', O.gauge({ done: 5, started: 1, total: 9 }), 'row'], ['Releasing', O.gauge({ done: 4, total: 4, tone: 'engine' }), 'row'], ['Shipped', O.gauge({ done: 4, total: 4, tone: 'ok', inner: 'check' }), 'row'], ['EndedShort', O.gauge({ done: 0, started: 1, total: 3, inner: 'dash' }), 'row']], { cell: 160 });
  comp('Pips', 'a task’s five stages: red, implement, green, review, integrate', Object.entries({ Running: ['ok', 'run', '', '', ''], Held: ['ok', 'mine', '', '', ''], Limited: ['ok', 'plan', '', '', ''], Failed: ['ok', 'bad', '', '', ''], Accepted: ['ok', 'ok', 'ok', 'ok', 'acc'], Done: ['ok', 'ok', 'ok', 'ok', 'ok'], NotStarted: ['', '', '', '', ''] }).map(([n, p]) => [n, O.pips(p), 'row']), { cell: 160 });
  comp('CommitMark', 'a commit is a mark; its hash is its name', [['Default', O.commit('a1b2c3d'), 'row']], { cell: 160 });
  comp('SectionHead', 'a label and a count', [['Plain', O.sec('Tasks', { n: '2 of 7 done' })], ['Attention', O.sec('Things you did not ask for', { n: 1, variant: 'attention' })], ['WithRest', O.sec('Now', { rest: 'as of 14:41' })]]);
  comp('InlineNote', 'one line saying what happened', [['Quiet', O.inote('Next: audit-log and readme start when tz-fixtures is done.')], ['Error', O.inote('Not sent. Your draft is kept; send again.', 'error')], ['Ok', O.inote('Connected to halyard.', 'ok')]]);
  comp('Menu', 'an overlay list of commands; a sheet on touch', [['Open', O.rowMenu(W.rows.beacon)]]);
  comp('Switch', 'on or off', [['Off', O.toggle(false, 'off'), 'row'], ['On', O.toggle(true, 'on'), 'row']], { cell: 160 });
  comp('Icons', 'sixteen-grid strokes in the text’s colour', [['All', each(O.iconNames, (n) => `<span class="ink-2" title="${esc(n)}">${icon(n)}</span>`), 'row']], { cell: 600 });

  // ---------- shell ----------
  h2('Shell');
  comp('HostLabel', 'the host a pane, a session, an epic or a step runs on: inline on its row, and once on a card\'s heading where rows are grouped by host and folder', [
    ['Inline', O.hostLabel('halyard'), 'row'], ['CardHeading', O.hostLabel('sextant', { heading: true }), 'row'],
  ], { cell: 200 });
  comp('InboxRow', 'a session', [
    ['Working', O.sessionRow(W.rows.lamp), 'side'], ['WaitingQuestion', O.sessionRow(W.rows.beacon), 'side'], ['Done', O.sessionRow(W.rows.rotaplan), 'side'], ['Unseen', O.sessionRow(W.rows.phase), 'side'],
    ['CutOff', O.sessionRow(W.rows.almanac), 'side'], ['Failed', O.sessionRow(W.rows.failed), 'side'], ['Stopped', O.sessionRow(W.rows.stopped), 'side'], ['Idle', O.sessionRow(W.rows.shell), 'side'],
    ['Unreachable', O.sessionRow(W.rows.unreach), 'side'],
    ['Open', O.sessionRow(W.rows.lamp, { open: true }), 'side'], ['Cursor', O.sessionRow(W.rows.beacon, { cur: true }), 'side'], ['OpenAndCursor', O.sessionRow(W.rows.lamp, { open: true, cur: true }), 'side'], ['Hover', O.sessionRow(W.rows.rotaplan, { hover: true }), 'side'],
  ], { cell: 340 });
  comp('EpicRow', 'an epic, and its running steps, hanging under the conversation that steers it; a contract not yet accepted is the same row', [
    ['Asking', O.epicRow(W.epics.rota), 'side'], ['Building', O.epicRow(W.epics.followups), 'side'], ['ContractProposed', O.epicRow(W.epics.tide), 'side'], ['ReleaseFailed', O.epicRow(W.epics.offline), 'side'],
    ['Releasing', O.epicRow(W.epics.labels), 'side'], ['Parked', O.epicRow(W.epics.parked), 'side'], ['Paused', O.epicRow(W.epics.paused), 'side'], ['Planning', O.epicRow(W.epics.planning), 'side'],
    ['Shipped', O.epicRow(W.epics.holder), 'side'], ['EndedShort', O.epicRow(W.epics.cache), 'side'], ['StepsOnTwoHosts', O.epicRow(W.epics.buoy), 'side'],
  ], { cell: 340 });
  comp('ConvoRow', 'a conversation in the inbox, and what it steers hanging under it', [['Plain', O.convoRow(W.rows.lamp), 'side'], ['SteersEpic', O.convoRow(W.steerers.rotaplan), 'side'], ['WroteContract', O.convoRow(W.steerers.tideplan), 'side'], ['SteersSeveral', O.convoRow(W.steerers.followplan), 'side'], ['FromShell', O.convoRow(W.steerers.buoyshell), 'side']], { cell: 340 });
  comp('Dir', 'a project in the inbox: its head, then its conversations, each with what it steers', [['Open', O.dir({ name: 'lighthouse', path: '~/code', host: 'halyard', rows: [W.rows.lamp, W.steerers.rotaplan] }), 'side'], ['Folded', O.dir({ name: 'orrery', path: '~/code', host: 'halyard', collapsed: true, count: 4 }), 'side'], ['FoldedWaiting', O.dir({ name: 'tidepool', path: '~/code', host: 'halyard', collapsed: true, count: 5, waiting: 3 }), 'side'], ['OtherHost', O.dir({ name: 'kelp', path: '~/code', host: 'brine', collapsed: true, count: 1 }), 'side'], ['Unreachable', O.dir({ name: 'kelp', path: '~/code', host: 'brine', unreachable: true, rows: [W.rows.unreach] }), 'side']], { cell: 340 });
  comp('MeterStrip', 'the host, its meters, the context', [['HostAndContext', O.strip(W.strip(W.ctx.low))], ['HostOnly', O.strip(W.strip(null))], ['UnderLoad', O.strip(W.strip(W.ctx.high, { hot: true }))], ['OtherHost', O.strip(W.strip(null, { host: 'brine', other: true }))]], { cell: 640 });
  comp('TitleRow', 'where you are and what you can do', [
    ['Conversation', O.titleRow({ ...W.lampTitle, cmds: O.convoCmds() }), 'flush'], ['InboxHidden', O.titleRow({ ...W.lampTitle, toggle: true, waiting: 6, cmds: O.convoCmds() }), 'flush'],
    ['Epic', O.titleRow({ toggle: true, waiting: 6, dir: 'lighthouse', here: 'rota-export', state: O.epicState(W.epic.question), cmds: O.epicCmds(W.epic.question), strip: W.strip(null, { host: W.epics.rota.host }) }), 'flush'],
    ['Subpage', O.titleRow({ toggle: true, waiting: 6, back: 'epic-building', crumbs: [{ t: 'lighthouse' }, { t: 'rota-export', go: 'epic-building' }], here: 'export-cli', state: O.pill('checking · 2:41', 'live', { state: 'working' }), cmds: O.btn('Interrupt', { class: 'stopper', icon: 'stop' }), strip: W.strip(null, { host: W.epic.building.running[0].host }) }), 'flush'],
    ['Pane', O.titleRow({ toggle: true, waiting: 6, dir: 'orrery', here: 'zsh', strip: W.strip(null, { host: W.rows.shell.host }) }), 'flush'],
    ['UnreachablePane', O.titleRow({ toggle: true, waiting: 6, dir: 'orrery', here: 'zsh', state: O.pill('unreachable', 'unreachable', { state: 'unreachable' }), strip: W.strip(null, { host: W.rows.shell.host, down: true }) }), 'flush'],
    ['WithError', O.titleRow({ ...W.shortTitle, cmds: O.convoCmds(), note: 'Could not archive this session: the server did not answer.' }), 'flush'],
  ], { cell: 760 });
  comp('EpicPin', 'an epic as one line: on a task’s page, and on touch', [['Asking', O.epicPin(W.rotaPin), 'flush'], ['Quiet', O.epicPin({ ...W.rotaPin, blocker: '' }), 'flush']], { cell: 640 });
  comp('Notice', 'what happened elsewhere, in two lines', [['AsksYou', O.notice(W.notices[0])], ['EpicAsks', O.notice(W.notices[1])], ['Shipped', O.notice(W.notices[2])], ['Failed', O.notice(W.noticeDays[0][1][3])], ['InTheCentre', O.notice(W.notices[0], true)]], { cell: 460 });
  comp('NoticeCentre', 'where notices collect', [['Days', O.noticeCentre(W.noticeDays).replace('class="ncentre"', 'class="ncentre flat"')], ['Empty', O.noticeCentre([]).replace('class="ncentre"', 'class="ncentre flat"')]], { cell: 520 });
  comp('WorkCard', 'an epic or a contract as a live card, on a project\'s page', [['EpicAsks', O.workCard(W.workRota.epics[0])], ['EpicBuilding', O.workCard({ ...W.workRota.epics[0], tone: 'live', asks: '', now: 'Three tasks are being worked on.' })], ['ContractWaiting', O.workCard(W.workContract.contract)]], { cell: 400 });
  comp('WorkRail', 'what a conversation steers, in named groups', [['Groups', O.workRail(W.workFull).replace('class="rail end"', 'class="rail end" style="display:flex"'), 'side'], ['Many', O.workRail(W.workMany).replace('class="rail end"', 'class="rail end" style="display:flex"'), 'side']], { cell: 160 });
  comp('Starter', 'where a session runs: a prompt first, then ranked host and folder proposals in a dropdown, or a host and a folder typed', [
    ['Prompt', O.starterBody(W.starter.states.prompt)], ['Loading', O.starterBody(W.starter.states.loading)], ['Several', O.starterBody(W.starter.states.several)],
    ['None', O.starterBody(W.starter.states.none)], ['Failed', O.starterBody(W.starter.states.failed)], ['TypedFolder', O.starterBody(W.starter.states.typed)],
  ], { cell: 460 });
  comp('CompactHead', 'touch: the header folded to one line, the same whether scrolling or you folded it', [['Quiet', O.compactHead({ ...W.workFull, epics: [{ ...W.workRota.epics[0], asks: '' }] })], ['Asking', O.compactHead(W.workFull)]], { cell: 412 });
  comp('ListPanel', 'one of a conversation\'s lists, opened from its tile', [['Agents', O.listBody('agents', W.workFull)], ['Running', O.listBody('running', { background: W.shells })]], { cell: 520 });
  comp('SubjectBand', 'what a conversation is for, and what it steers that waits on you, under its title row', [['Goal', O.subject({ goal: W.workFull.goal })], ['Asks', O.subject(W.workFull)], ['Contract', O.subject(W.workContract)], ['OpenOnRight', O.subject(W.workRota, 'rota-export')]], { cell: 760 });
  comp('Tile', 'one thing on a rail, and its state at a glance; a right-rail tile names itself under its face and is lit while its panel is open', [['Mark', O.tile({ mark: true, bare: true, title: 'Open the inbox' }), 'row side'], ['Lit', O.tile({ gauge: W.epics.rota.gauge, tone: 'attention', n: '?', cap: 'rota', cur: true, on: true, title: 'rota-export, open' }), 'row side'], ['Project', O.tile({ text: 'li', tone: 'place', title: 'lighthouse' }), 'row side'], ['ProjectWaiting', O.tile({ text: 'ti', tone: 'place', n: 3, dots: ['live'], title: 'tidepool' }), 'row side'], ['ProjectNew', O.tile({ text: 'al', tone: 'place', dots: ['new'], title: 'almanac' }), 'row side'], ['Current', O.tile({ text: 'li', tone: 'place', cur: true, dots: ['live'], title: 'lighthouse' }), 'row side'], ['EpicAsks', O.tile({ gauge: W.epics.rota.gauge, tone: 'attention', n: '?', cap: 'rota', title: 'rota-export asks' }), 'row side'], ['SubAgents', O.tile({ icon: 'agent', tone: 'live', n: 2, nKind: 'live', cap: 'agents', title: '2 sub-agents' }), 'row side'], ['LeftRunning', O.tile({ icon: 'shell', n: 1, nKind: 'quiet', cap: 'running', title: '1 left running' }), 'row side'], ['New', O.tile({ icon: 'plus', tone: 'primary', title: 'New session' }), 'row side']], { cell: 150 });
  comp('ContextBand', 'ties a task to its epic and its sibling tasks', [['Default', O.band({ pin: { ...W.rotaPin, blocker: '' }, tasks: W.epicTasks, cur: 'export-cli' }), 'flush']], { cell: 760 });

  // ---------- conversation ----------
  h2('Conversation');
  const one = (it) => `<div class="flat">${O.items([{ turn: false, ...it }])}</div>`;
  comp('YouBlock', 'your words', [['Default', one(W.lamp[0])], ['FromPhone', one(W.lamp[4])], ['Sending', one({ ...W.lamp[4], sending: true, from: '' })]], { cell: 460 });
  comp('EventBlock', 'the engine or another agent', [['Default', one(W.blocks[3])]], { cell: 460 });
  comp('StepEndBlock', 'a step ended; the rail is the verdict', [['Ok', one(W.blocks[4])], ['Failed', one(W.blocks[5])], ['Stopped', one(W.blocks[6])]], { cell: 460 });
  comp('EpicEventBlock', 'one line from an epic', [['Created', one(W.blocks[2])], ['Question', one(W.blocks[12])], ['Parked', one(W.blocks[9])], ['Released', one(W.blocks[10])], ['Ended', one(W.blocks[11])]], { cell: 460 });
  comp('CheckBlock', 'a send-back round', [['Bad', one(W.blocks[7])], ['Ok', one(W.blocks[8])], ['NoteOpen', one({ ...W.blocks[7], open: true })]], { cell: 460 });
  comp('ContractBlock', 'where a contract was written, one line', [['Waiting', one(W.tideplan[3])], ['Accepted', one(W.blocks[14])]], { cell: 460 });
  comp('IdChip', 'a row\'s short id, coloured by its kind', [['Kinds', ['R4', 'F2', 'P1', 'A1', 'D7'].map(O.idc).join(' '), 'row']], { cell: 300 });
  comp('ToolLine', 'one call', [['Done', one({ k: 'tools', list: [W.lines[2].list[0]] })], ['Failed', one({ k: 'tools', list: [{ ...W.lines[2].list[2], open: false }] })], ['Running', one(W.lamp[8])], ['Open', one({ k: 'tools', list: [{ ...W.lines[2].list[1], open: true, result: '14 matches in 3 files' }] })], ['OpenFailed', one({ k: 'tools', list: [W.lines[2].list[2]] })]], { cell: 460 });
  comp('ToolRun', 'three or more calls as one line', [['Closed', one(W.lamp[2])], ['Open', one(W.lines[3])], ['Running', one({ k: 'run', n: 4, names: 'Read ×3, Bash', dots: ['', '', '', 'r'], running: true })]], { cell: 460 });
  comp('ThinkingLine', 'a thought', [['Closed', one({ k: 'thinking' })], ['Open', one(W.lines[1])]], { cell: 460 });
  comp('TaskLine', 'a background task that ended', [['Finished', one(W.lines[7])], ['Failed', one(W.lines[8])], ['Stopped', one(W.lines[9])]], { cell: 460 });
  comp('NoticeLine', 'a notice in the transcript', [['Default', one(W.lines[4])], ['Error', one(W.lines[6])]], { cell: 460 });
  comp('EngineFold', 'a brief or an engine prompt', [['BriefClosed', one({ ...W.lines[12], open: false })], ['BriefOpen', one(W.lines[12])], ['EngineClosed', one(W.lines[13])]], { cell: 460 });
  comp('Rule', 'a mark across the column', [['Day', one(W.blocks[0])], ['New', one({ k: 'rule', v: 'new' })], ['Interrupted', one(W.lines[10])], ['Compaction', one(W.lamp[1])], ['CompactionOpen', one(W.lines[11])]], { cell: 460 });
  comp('Reply', 'the only plain full-width text', [['Default', one(W.short[2])], ['Live', one(W.lamp[7])]], { cell: 460 });
  comp('Ask', 'it waits on you', [
    ['Session', O.ask(W.beaconAsk)], ['Permission', O.ask({ ...W.beaconAsk, permission: true, q: ['Run `git push --force-with-lease`?'], opts: [{ name: 'Yes' }, { name: 'Yes, and don’t ask again' }, { name: 'No' }] })],
    ['Epic', O.ask(W.rotaAsk)], ['EpicSomethingElse', O.ask({ ...W.rotaAsk, picked: undefined, other: 'Vendor the Lisbon rule as a fixture file.', record: 'D7 14:41 owner: vendor the Lisbon rule as a fixture file' })],
    ['Sending', O.ask({ ...W.beaconAsk, picked: 0, state: 'sending' })], ['Error', O.ask({ ...W.beaconAsk, state: 'error', error: 'Not answered: the question changed before your answer arrived.' })],
    ['Stale', O.ask({ ...W.beaconAsk, age: '5d', stale: '5 days' })], ['Unanswerable', O.ask({ ...W.beaconAsk, opts: [], state: 'unanswerable' })],
    ['Answered', O.ask({ state: 'answered', who: 'Which fix should I make?', answer: { n: 1, name: 'One query per week' } })], ['Settled', O.ask({ state: 'settled', who: 'rota-export · tz-fixtures' })],
  ], { cell: 560 });
  const dk = (d) => O.dock({ name: 'tidepool', ...d }).replace('class="dock"', 'class="dock flat"');
  comp('Ctl', 'a control inside a line: the same small size at the desk and on touch', [['Interrupt', O.ctl('Interrupt', 'stop', { tone: 'bad', kbd: 'esc' }), 'row'], ['Resume', O.ctl('Resume', 'play', { tone: 'go' }), 'row'], ['SendNow', O.ctl('Send now', 'send'), 'row'], ['IconOnly', O.ctl('More', 'more', { only: true }), 'row'], ['Disabled', O.ctl('Interrupting…', '', { disabled: true }), 'row']], { cell: 200 });
  comp('Dock', 'the state, your queue, the input', [
    ['Done', dk({ state: 'done', what: '· 4m ago' })], ['Working', dk({ state: 'working', tool: 'Bash', what: 'just warm', clock: '0:46' })], ['Waiting', dk({ state: 'waiting' })],
    ['Interrupting', dk({ state: 'interrupting', word: 'interrupting…', what: 'waiting for the turn to end', clock: '0:04' })], ['InterruptedByYou', dk({ state: 'interrupted', byYou: true, word: 'interrupted by you', what: '· 14:32' })], ['EventsWait', dk({ state: 'interrupted', byYou: true, word: 'interrupted by you', what: '· 14:32', waits: 2 })], ['CutOff', dk({ state: 'interrupted', what: '· the server restarted mid-turn' })],
    ['Failed', dk({ state: 'failed', what: '· the API refused the request' })], ['Stopped', dk({ state: 'stopped', what: '· 2d ago' })], ['Shell', dk({ state: 'idle', shell: true })],
    ['Focused', dk({ state: 'working', tool: 'Bash', what: 'just warm', clock: '0:46', draft: 'And skip warm ports.', focused: true })],
    ['WithQueued', dk({ state: 'working', what: 'thinking', clock: '0:12', queued: [{ text: 'Also log the batch id.' }, { text: 'Then update the README.' }] })], ['PaneQueue', dk({ state: 'working', pane: true, what: 'thinking', clock: '0:12', queued: [{ text: 'Also log the batch id.' }] })],
    ['QueuedCancelled', dk({ state: 'done', what: '· 1m ago', queued: [{ text: 'Then update the README.', cancelled: true }], queueWhy: 'dropped when the turn was cancelled' })], ['About', dk({ state: 'waiting', about: `${O.idc('F2')} The harbour office gets the same alert many times.`, draft: 'Make it one alert per table.' })],
    ['SendError', dk({ state: 'done', what: '· 4m ago', draft: 'Run it for January too.', error: 'Not sent. Your draft is kept; send again.' })],
    ['StepRunning', dk({ step: { label: 'export-cli' }, tool: 'gate', what: 'just check', clock: '2:41', placeholder: 'Nudge export-cli. It interrupts the turn, then gives it your words' })], ['StepHeld', dk({ state: 'held', step: { label: 'tz-fixtures', held: true }, what: 'waits on your answer', placeholder: 'Nudge tz-fixtures. It resumes with your words' })],
    ['StepConfirm', dk({ step: { label: 'export-cli', confirm: true }, what: 'just check', clock: '2:44' })],
  ], { cell: 560 });
  comp('FindBar', 'find in a conversation', [['Matches', O.findBar({ value: 'jitter', count: '2 of 3' }).replace('class="findbar"', 'class="findbar flat"'), 'flush'], ['NoMatch', O.findBar({ value: 'lisbon' }).replace('class="findbar"', 'class="findbar flat"'), 'flush']], { cell: 460 });

  // ---------- epic ----------
  h2('Epic');
  comp('Track', 'where an epic is, in five stages', [['Building', O.track(W.epic.building.track)], ['AsksYou', O.track(W.epic.question.track)], ['Reviewing', O.track(W.epic.reviewing.track)], ['Released', O.track(W.epic.shipped.track)]], { cell: 640 });
  comp('Orrery', 'every task a wedge, every phase a ring', [['Running', O.orrery(W.epic.building)], ['Mine', O.orrery(W.epic.question)], ['Done', O.orrery(W.epic.shipped)], ['Dense', O.orrery(W.epic.dense)]], { cell: 360 });
  comp('TaskTable', 'every task as a row', [['Default', O.taskTable(W.epic.question)]], { cell: 640 });
  comp('RequirementTable', 'what will be true, and its proof', [['Default', O.reqTable(W.epic.question)], ['WithRuns', O.reqTable(W.epic.evidence)]], { cell: 640 });
  comp('EvidenceRun', 'one run as a sentence and a verdict', [['RedHolds', O.evidenceRun({ phase: 'red', verdict: 'holds', state: 'ended', passed: false, exit: 1, source: 'captured', commit: 'a1b2c3d' })], ['RedMetAtBase', O.evidenceRun({ phase: 'red', verdict: 'fails', state: 'ended', passed: true, exit: 0, source: 'captured', commit: 'a1b2c3d' })], ['GreenPasses', O.evidenceRun({ phase: 'green', verdict: 'holds', state: 'ended', passed: true, exit: 0, source: 'supplied', commit: 'e4f5a6b' })], ['Judging', O.evidenceRun({ phase: 'green', state: 'judging', source: 'captured' })], ['NoVerdict', O.evidenceRun({ phase: 'green', verdict: '', state: 'ended', passed: false, exit: 75, source: 'captured' })], ['LogOpen', O.evidenceRun({ phase: 'red', verdict: 'holds', state: 'ended', passed: false, exit: 1, source: 'captured', log: 'open', text: '--- FAIL: TestExportRefusesOverwrite', stderr: 'exit status 1' })], ['LogUnavailable', O.evidenceRun({ phase: 'red', verdict: 'holds', state: 'ended', passed: false, exit: 1, source: 'captured', log: 'unavailable' })]], { cell: 560 });
  comp('ReleaseTable', 'what leaves this machine', [['Running', O.releaseTable(W.epic.releasing.release.steps)], ['Failed', O.releaseTable(W.epic.releaseFailed.release.steps)]], { cell: 640 });
  comp('HappeningList', 'what just happened', [['Default', O.happenings(W.epic.question.history, 9)], ['WithTail', O.happenings(W.epic.plannerParked.history.slice(0, 1))]], { cell: 460 });
  comp('PhaseRow', 'one of a task’s five stages', [['Passed', O.phaseRow({ name: 'red', state: 'passed', sum: 'the test fails before any change', time: '1m' })], ['Now', O.phaseRow({ name: 'implement', state: 'now', sum: 'attempt 2 · the gate is running', time: '2:41' })], ['NowOpen', O.phaseRow({ name: 'integrate', state: 'now', tone: 'acc', sum: 'accepted', open: true, body: O.inote('Waits to integrate.') })], ['Ahead', O.phaseRow({ name: 'review', state: 'ahead' })]], { cell: 560 });

  // ---------- touch ----------
  h2('On touch');
  comp('Button', '48 high; no key caps', [['Default', O.btn('Talk', { icon: 'chat' }), 'row'], ['Primary', O.btn('Send', { variant: 'primary', icon: 'send' }), 'row'], ['Danger', O.btn('Interrupt', { variant: 'danger', icon: 'stop' }), 'row']], { cell: 220, touch: true });
  comp('Ask', 'options are 48 high and have no key caps', [['Session', O.ask(W.beaconAsk)]], { cell: 380, touch: true });
  comp('QuickKeys', 'Android only: keys sent to a terminal pane', [['Default', O.quickKeys({})], ['Flash', O.quickKeys({ keyFlash: 'Esc', keyNote: 'Esc sent. The pane is now done.', keyNoteKind: 'ok' })], ['NotSent', O.quickKeys({ keyNote: 'Ctrl-C was not sent.', keyNoteKind: 'error' })]], { cell: 380, touch: true });
  comp('ListItem', 'a setting, a pool member, a talk target', [['Default', `<div class="group"><div class="litem"><span class="t">Questions and failures</span><span class="s">Sound and vibration</span><span class="e">${O.toggle(true, 'on')}</span></div></div>`]], { cell: 380, touch: true });
  comp('Sheet', 'a menu or a confirm, from the foot of the screen', [['Menu', `<section class="sheet" style="position:static">${O.menu(O.rowMenuItems(W.rows.beacon).map((it) => (it === 'sep' ? it : { ...it, kbd: undefined })))}</section>`, 'flush'], ['Confirm', `<section class="sheet" style="position:static"><div class="sheet-head"><b>Stop rota-export?</b>Its running steps are stopped.</div><div class="foot">${O.btn('Keep it running')}${O.btn('Stop rota-export', { variant: 'danger' })}</div></section>`, 'flush']], { cell: 380, touch: true });
  comp('Toast', 'one line at the foot, with the way back', [['WithUndo', O.toast('Archived “Tide table cache warm-up”.', 'Undo').replace('class="toast"', 'class="toast" style="position:static"')]], { cell: 380, touch: true });

  document.querySelector('[data-catalog]').innerHTML = out.join('');
  // The swatches and the spacing bars take their values from the tokens themselves.
  document.querySelectorAll('.sw').forEach((el) => { el.style.background = `var(${el.dataset.var})`; });
  document.querySelectorAll('.sp').forEach((el) => { el.style.width = `var(--o-space-${el.dataset.size})`; el.style.height = `var(--o-space-${el.dataset.size})`; });
})();
