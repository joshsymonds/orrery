// Round 6 wireframe: the epic's scenarios: the one epic page in every state, the contract, a task's phases, a
// worker's session, and Talk.
(() => {
  'use strict';
  const { W, icon } = O;
  const E = W.epic;

  // epic registers one epic scenario for both frames. At the desk the conversation that steers the epic stays in the
  // middle and the epic is the panel its tile on the right rail opens; under 1250 the panel lies over the conversation.
  const epic = (id, name, note, e, o = {}) => O.scenario({
    id, group: o.group || 'Epic', title: name, note, differs: o.differs, widths: o.widths,
    desk: o.desk === null ? undefined : () => O.desk({ inboxState: o.inboxState || 'rail', open: id, side: 'panel', over: o.over,
      pages: [(o.convo || rotaConvo)({ panel: O.sidePanel({ label: 'rota-export', tone: 'attention', page: O.epicPage(e, { record: true, close: o.back || 'convo-epic', notices: o.notices, menu: o.menu }) }) })] }),
    phone: o.phone === null ? undefined : () => O.screen(o.phoneScreen ? o.phoneScreen() : O.epicPage(e), { over: o.phoneOver ? o.phoneOver() : undefined }),
  });
  const NEEDS_SERVER = 'This control has no public call today: the engine does it from a session or the CLI. It is drawn because the page asks for it.';

  epic('epic-building', 'An epic building, nothing asked', 'The page answers in order: what it is, where it is (the track), what it is doing now, what just happened, every task, every requirement. A stage it has not reached is one cell of the track and nothing else on the page.', E.building, { inboxState: 'docked' });

  epic('epic-question', 'An epic that asks you a question', 'The question is the Ask, the same component a session uses, first thing under Now. An epic\'s answer is pick, then confirm, and shows the line that will be recorded. The title row says it in one pill.', E.question);

  epic('epic-question-other', 'Answering with something else', 'The last option opens a text box. The button names what will be sent.', { ...E.question, ask: { ...W.rotaAsk, picked: undefined, other: 'Vendor the Lisbon rule as a fixture file; do not touch go.mod.', record: 'D7 14:41 owner: vendor the Lisbon rule as a fixture; no go.mod change' } });

  epic('epic-parked', 'A parked epic', 'A park the owner holds is a question with choices, so it is the same Ask: retry, drop, or end. No raw engine sentence and no commit hash.', E.parked);

  epic('epic-planner-parked', 'A park the planner is handling', 'Not every park is yours. When the planner holds it, the page says so quietly and asks nothing; today the two look alike on Android.', E.plannerParked);

  epic('epic-limited', 'Steps waiting on a usage limit', 'A limit wait is grey, not peach: nothing is asked of you. It says when the limit resets and that the steps resume by themselves; Resume tries sooner.', E.limited);

  epic('epic-paused', 'A paused epic', 'Paused is grey. Resume is the one command.', E.paused);

  epic('epic-planning', 'An epic with no tasks yet', 'Before the planner has outlined anything there is no chart and no task table: the page is its summary, its track, one sentence, and the requirements with no proof yet.', E.planning);

  epic('epic-reviewing', 'The end-of-epic review', 'When building is done the Review section appears: each reviewer, its verdict, and each finding with what happened to it.', E.reviewing);

  epic('epic-releasing', 'Releasing', 'Release is a table: what leaves this machine, where to, how it is undone, and how each step went.', E.releasing);

  epic('epic-release-failed', 'A release that stopped at a step with no undo', `Only you can say the failed step is safe to pass. ${NEEDS_SERVER}`, E.releaseFailed);

  epic('epic-slot', 'Releasing beside another epic', 'Every clean epic releases at once, so this one does not wait for orbit-labels, which is releasing in the same repository. The summary says so, and nothing is asked.', E.slot);

  epic('epic-shipped', 'A released epic', 'An ended epic is the same page with the Report filled in, opened at the Report.', E.shipped);

  epic('epic-ended-gaps', 'Ended with gaps', `The review left a gap, so it did not release. The report says which requirement and why; the two ways on are yours. ${NEEDS_SERVER}`, E.gaps);

  epic('epic-dense', 'An epic with 120 tasks', 'The chart holds 120 wedges with no labels to collide; the wedge under the pointer is outlined and named in the caption. The table shows twelve tasks and folds the rest.', E.dense, { widths: [1600] });

  epic('epic-evidence', 'A requirement\'s evidence, opened', 'A requirement row opens to its runs. A run is one sentence and one verdict, not five status words; its log and artifacts fold under it.', E.evidence);

  epic('epic-stop-confirm', 'Stopping an epic asks first', 'The command turns into its question in place at the desk; on touch it is a sheet.', { ...E.building, controls: { confirm: true } },
    { phoneOver: () => O.confirmSheet('Stop rota-export?', 'Stop rota-export', 'Keep it running', 'Its running steps are stopped. Nothing is released.') });

  epic('epic-command-error', 'An epic command that failed', 'The error is on the epic\'s page, under its title row. Today it is a line in the inbox\'s head, which is hidden here.', { ...E.building, cmdError: 'Pause did not land: the server did not answer. The epic is still running.' }, { group: 'Empty, loading, error' });

  epic('epic-problem', 'An epic the engine cannot read cleanly', 'The engine\'s own problem with the epic is one line at the top, in red.', { ...E.building, problem: 'The epic’s workflow file did not load: workflow.yaml line 14, unknown node kind “gatee”.' }, { group: 'Empty, loading, error' });

  epic('epic-loading', 'An epic before its first answer', 'Loading and failed are different states with different words; today both read "epic unavailable".', { ...E.building, unavailable: 'loading the epic…', loading: true }, { group: 'Empty, loading, error' });

  epic('epic-unavailable', 'An epic that could not be read', 'With the way to try again.', { ...E.building, unavailable: 'The server could not read this epic.' }, { group: 'Empty, loading, error' });

  // ---------- the conversation that steers an epic ----------

  const TALK = [
    { k: 'you', at: '14:13', text: ['Why did csv-writer need a retry?'] },
    { k: 'tools', list: [{ name: 'epic_get', arg: 'rota-export', result: 'ok' }] },
    { k: 'reply', at: '14:14', md: ['Its first try, on haiku, made the same edit three times, so the monitor ended it at 13:58 and the planner retried on sonnet. The retry integrated, which proves `R3`.'] },
  ];
  // An epic is steered from one conversation. Its head moves it to another in the same project, or to a new one.
  const TALK_SESSIONS = [
    { head: 'Steer rota-export from' },
    { label: 'Planned the rota export, and steers its epic', icon: 'check', sub: 'steers it now · made it 13:50' },
    { label: 'Retry backoff and jitter for the telemetry uploader', icon: 'chat', sub: 'lighthouse · working · 15%', hover: true, go: 'epic-moved' },
    'sep',
    { label: 'A new conversation in lighthouse', icon: 'plus', sub: 'starts with the contract and the epic’s log', go: 'epic-moved' },
  ];
  // rotaConvo is the conversation that steers rota-export, in the middle of the desk while one of its things is open in
  // the panel beside it. o: { items, title, dock, ended, work, on, panel }
  const ROTA_DOCK = { state: 'done', what: '· 27m ago', name: 'lighthouse', placeholder: 'Tell it what to change, or ask why' };
  const rotaConvo = (o = {}) => O.convoPage({
    title: { toggle: true, waiting: 6, cmds: O.convoCmds(), dir: 'lighthouse', here: 'Planned the rota export, and steers its epic', strip: W.strip(W.ctx.mid), ...(o.title || {}) },
    work: o.work || W.workRota, on: o.on === undefined ? 'rota-export' : o.on, items: o.items || W.rotaplan.slice(0, 5).concat(TALK), dock: o.dock || ROTA_DOCK, ended: o.ended, panel: o.panel,
  });
  const talkPage = () => O.convoPage({
    title: { back: 'epic-question', dir: 'lighthouse', here: 'Planned the rota export, and steers its epic', strip: W.strip(W.ctx.mid) },
    work: W.workRota, items: TALK, dock: { state: 'done', what: '· 27m ago', name: 'lighthouse', placeholder: 'Tell it what to change, or ask why' },
  });
  const talkList = () => O.subPage({
    label: 'steer from', back: 'epic-question', crumbs: [{ t: 'lighthouse' }, { t: 'rota-export', go: 'epic-question' }], here: 'Steer rota-export from',
    body: `<section><div class="group">${TALK_SESSIONS.filter((t) => t.sub && t.icon !== 'plus').map((t) => `<a class="litem" href="${O.go(t.go || 'epic-talk')}"><span class="t">${O.esc(t.label)}</span><span class="s">${O.esc(t.sub)}</span><span class="e">${icon(t.icon === 'check' ? 'check' : 'chevronRight')}</span></a>`).join('')}</div></section>`
      + `<section>${O.btn('A new conversation in lighthouse', { variant: 'primary', icon: 'plus', go: 'epic-moved' })}</section>`,
  });

  epic('epic-talk', 'An epic and its conversation, under 1250 wide', 'Changing or guiding an epic is the commonest thing done to it, so the conversation that steers it stays in the middle with its input ready, and the epic is the panel its tile opens. Under 1250 there is no room for both side by side, so the panel lies over the conversation from the right edge; Close or ] returns to the conversation, which never moved. On the phone the bar at the foot opens the conversation as the next page. It is the one conversation that steers this epic; the epic moves only when you move it.', E.question,
    { group: 'Talk', phoneScreen: talkPage, widths: [1000] });

  epic('epic-handover', 'Moving an epic to another conversation', 'An epic is steered from exactly one conversation. Its record has the control that moves it: to another conversation in the same project, or to a new one that starts with the contract and the epic\'s log. Another conversation can still read the epic and talk about it; to change it, it takes it over, here or by being asked to.', E.question,
    { group: 'Talk', menu: O.menu(TALK_SESSIONS, { class: 'two' }), phoneScreen: talkList, widths: [1600] });

  const MOVED = { at: '14:44', text: 'moved to **Retry backoff and jitter for the telemetry uploader** from **Planned the rota export**, by you' };
  const movedConvo = (x = {}) => rotaConvo({ ...x, title: { dir: 'lighthouse', here: 'Retry backoff and jitter for the telemetry uploader', strip: W.strip(W.ctx.low) }, items: W.lamp.slice(0, 3).concat(W.tookOver), dock: { ...ROTA_DOCK, what: '· just now' } });
  epic('epic-moved', 'An epic after it moved', 'The move is a line on the epic\'s log, and the conversation in the middle is now the one that took it. Its questions go there.', { ...E.question, conversation: 'lighthouse · Retry backoff and jitter for the telemetry uploader', conversationGo: 'convo-took-over', history: [MOVED, ...(E.question.history || [])] },
    { group: 'Talk', convo: movedConvo, back: 'convo-took-over' });

  const endedConvo = (x = {}) => rotaConvo({ ...x, ended: { when: '· 2d ago', text: 'This conversation has ended, and rota-export still asks you a question. Continue it in a new conversation, which starts with the contract and the epic’s log, or move it to one that is running.' } });
  epic('epic-convo-ended', 'An epic whose conversation ended', 'An epic is never without a conversation. When its own ends, the conversation says so where its dock was and offers the ways on; until then the epic\'s questions still show on its record and in the inbox. A conversation that steers a live epic stays in the inbox\'s tree, not behind its ended line.', E.question,
    { group: 'Talk', convo: endedConvo });

  // ---------- contract ----------

  const row = (cells, detail) => ({ cells, detail });
  // The contract is Gambit's document: a decision line, then ten sections in its order. "How bad" is each failure
  // row's own rating; the worst row sets the level of care. There is no likelihood.
  const RATE = { limited: '', serious: 'live', severe: 'bad' };
  const did = (verb, rest) => `<span class="wide"><span class="verb">${verb}:</span> ${rest}</span>`;
  const code = (t) => `<code>${O.esc(t)}</code>`;
  const DECISION = {
    variant: 'epic', who: 'tidepool · Wrote the tide-alerts contract',
    q: ['Alert on every change, or only on changes to the next two days’ tables?'],
    opts: [{ name: 'The next two days only', desc: 'Later tables change often and nobody acts on them yet.', rec: true, why: 'the office plans two days out.' }, { name: 'Every change', desc: 'About forty alerts a day at today’s rate of change.' }],
  };
  const contractSections = (o = {}) => {
    const say = o.say ? ` <span class="say on">${O.ctl('Talk about this row', 'chat', { only: true })}</span>` : '';
    const failures = O.table('auto minmax(0, 1.3fr) auto minmax(0, 1fr)', ['', 'if this happened', 'how bad', 'what we do'], [
      row([`<span>${O.idc('F1')}</span>`, '<span class="strong wide">The office was alerted about a table that had not changed.</span>', `<span>${O.chip('limited', RATE.limited)}</span>`, did('prevent', 'compare the published hash before alerting.')]),
      row([`<span>${O.idc('F2')}</span>`, '<span class="strong wide">The office received the same alert many times.</span>', `<span>${O.chip('serious', RATE.serious)}</span>`, did('reduce', `one alert per table; repeats are dropped.${o.changed ? ` ${O.chip('changed in v2', 'new')}` : ''}${say}`)]),
      row([`<span>${O.idc('F3')}</span>`, '<span class="strong wide">An alert named the wrong port, and a pilot was sent to it.</span>', `<span>${O.chip('severe', RATE.severe)}</span>`, did('prevent', 'the port is read from the table, never from the request.')]),
    ], { touch: 'auto minmax(0, 1fr) auto' }) + `<p class="inote"><span>Level of care: <b class="is-bad">severe</b>, set by ${O.idc('F3')}</span></p>`;
    const additions = O.table('auto minmax(0, 1fr) minmax(0, 1fr) minmax(0, 1fr) auto', ['where', 'what', 'why', 'cost', ''], [
      row([`<span>${O.idc('F2')}</span>`, '<span class="strong wide">Retry a failed send three times.</span>', '<span class="wide">The office’s endpoint dropped 2 of the last 100 test sends.</span>', '<span class="wide ink-3">One file. No new service.</span>',
        `<span class="end">${o.toAccept ? `<span class="keepdrop">${O.btn('Keep', { variant: o.kept ? 'primary' : '' })}${O.btn('Strike', { variant: 'quiet' })}</span>` : O.chip('kept', 'ok', 'check')}</span>`]),
    ], { touch: 'auto minmax(0, 1fr)' });
    const R = [['R1', 'A changed table alerts the office within a minute.', 'TestAlertWithinMinute'], ['R2', 'An unchanged table sends nothing.', 'TestNoAlertUnchanged'], ['R3', 'One table makes one alert.', 'TestOneAlertPerTable'], ['R4', 'The alert names the table’s own port.', 'TestPortFromTable'], ['R5', 'A failed send is retried three times.', 'TestRetryThrice']];
    const reqs = O.table('auto minmax(0, 1.2fr) minmax(0, 1fr)', ['', 'must be true', 'how we’ll know'], R.map(([id, text, test]) => row([`<span>${O.idc(id)}</span>`, `<span class="strong wide">${O.esc(text)}${o.changed && id === 'R3' ? ` ${O.chip('changed in v2', 'new')}` : ''}</span>`, `<span class="wide">${O.esc(test)} passes: ${code(`go test ./alerts -run ${test.slice(4)}`)}</span>`])), { touch: 'auto minmax(0, 1fr)' });
    const assume = O.table('auto minmax(0, 1fr) minmax(0, 1fr)', ['', 'assumption', 'if wrong'], [
      row([`<span>${O.idc('P1')}</span>`, `<span class="strong wide">A published table has one hash. Seen in ${code('tables/publish.go')}.</span>`, '<span class="wide">The intent survives; F1’s guard needs another key.</span>']),
      row([`<span>${O.idc('P2')}</span>`, '<span class="strong wide">The office’s endpoint answers within five seconds.</span>', '<span class="wide">The intent survives; sends queue, and R1’s minute is at risk.</span>']),
    ], { touch: 'auto minmax(0, 1fr)' });
    const alternatives = O.table('minmax(0, 1fr) minmax(0, 1.2fr) minmax(0, 1fr)', ['alternative', 'why not', 'reconsider when'], [
      row(['<span class="strong wide">Poll each table once a minute.</span>', '<span class="wide">Alerts up to a minute late, and costs a query per port per minute.</span>', '<span class="wide ink-3">The publisher cannot emit an event.</span>']),
      row(['<span class="strong wide">Alert from the publisher itself.</span>', '<span class="wide">Ties publishing a table to the office’s endpoint being up.</span>', '<span class="wide ink-3">Never.</span>']),
    ], { touch: 'minmax(0, 1fr)' });
    const leaves = O.table('auto minmax(0, 1.4fr) minmax(0, 1fr) auto', ['', 'step', 'target', 'undo'], [
      row(['<span class="id">1</span>', '<span class="strong wide">Merge the epic’s candidate into main, so the watcher ships.</span>', '<span class="wide">origin/main</span>', '<span>revert the merge</span>']),
      row(['<span class="id">2</span>', '<span class="strong wide">Turn alerts on for the harbour office, so they start receiving them.</span>', '<span class="wide">the office’s endpoint</span>', `<span>${O.chip('none', 'bad')}</span>`]),
    ], { touch: 'auto minmax(0, 1fr) auto' }) + '<p class="inote"><span>Then: one real change to a table alerts the office once, within a minute.</span></p>';
    const decisions = o.toAccept
      ? O.ask({ ...DECISION, picked: o.decided ? 0 : undefined, record: o.decided ? 'D1 owner: alert on the next two days’ tables only' : undefined })
      : `<p class="inote ok">${icon('check')}<span>None. Settled: <b>D1</b> alert on the next two days’ tables only.</span></p>`;
    const checks = `<div class="checks-list"><span class="k">Done</span><span>${code('go test ./alerts/...')}; then the full gate, ${code('just check')}.</span>`
      + `<span class="k">Evidence</span><span>The five tests named under “how we’ll know”.</span>`
      + `<span class="k">Steps</span><span>Read-only: schema check. Implementer: schema check, then head, ancestry, clean and owned files, then the gate ${code('check')}.</span>`
      + `<span class="k">Quality bar</span><span>Fixed for every epic: failing, low-quality or bad code is unacceptable; missing a platonic ideal is not a defect.</span>`
      + `<span class="k">Review gate</span><span>FLOOR · reviewer-ro · 900 s · must: no alert without a hash change.</span></div>`;
    const LOOK = { 'What you asked for': ['person', 'ref'], 'What could go wrong, and how much we care': ['warn', '3'], 'Things you did not ask for': ['plus', '2'], 'What will be true when done': ['check', '4'], 'What I’m assuming': ['question', '1'], 'What we won’t do': ['cross', 'halt'], 'How, and why not the other ways': ['gear', '5'], 'What leaves this machine or can’t be undone': ['open', 'engine'], 'Decisions I need from you': ['question', 'ref'], 'Checks the machines run': ['shell', 'ok'] };
    const s = (name, o2, body) => `<section aria-label="${name}">${O.sec(name, { icon: LOOK[name][0], hue: LOOK[name][1], ...o2 })}${body}</section>`;
    return s('What you asked for', {}, '<p class="summary measure ink-2">Alert the harbour office when a tide table changes after it was published, so a pilot never works from a stale table.</p>')
      + s('What could go wrong, and how much we care', {}, failures)
      + s('Things you did not ask for', { n: 1, variant: o.toAccept && !o.kept ? 'attention' : '' }, additions)
      + s('What will be true when done', { n: 5 }, reqs)
      + s('What I’m assuming', { n: 2 }, assume)
      + s('What we won’t do', {}, '<div class="prose"><ul><li>No SMS or email. The office asked for its endpoint only.</li><li>No change to how tables are published. It is out of scope.</li></ul></div>')
      + s('How, and why not the other ways', {}, `<p class="summary measure ink-2">A watcher compares each published table’s hash on write and sends one alert per changed table to the office’s endpoint.</p>${alternatives}`)
      + s('What leaves this machine or can’t be undone', { variant: o.unread ? 'attention' : '', rest: o.unread ? '<span class="unread">read this before Accept</span>' : '' }, leaves)
      + s('Decisions I need from you', { n: o.toAccept && !o.decided ? 1 : undefined, variant: o.toAccept && !o.decided ? 'attention' : '' }, decisions)
      + s('Checks the machines run', {}, checks);
  };
  // dline draws the decision line a contract opens with, and under it the contract's own record: which version this
  // is, who wrote it and when, and where its versions are. A contract is kept from the moment it is proposed.
  // o: { decisions, accepted, aside, versions (draw the list open) }
  const WRITER = 'tidepool · Wrote the tide-alerts contract';
  const dline = (o = {}) => {
    const record = o.accepted ? `v2 accepted 14:52 with “Accept, with the next two days only.”`
      : o.aside ? 'v2 set aside 2d ago with “Not until the pilots’ season ends.”'
      : 'v2 proposed 14:41 · first written 14:15';
    return `<section class="dline"><h2>Alert the harbour office within a minute when a published tide table changes.</h2><div class="facts"><span>Level of care ${O.chip('severe', 'bad')}</span>`
      + `<span>Decisions needed: <b class="${o.decisions ? 'is-attention' : 'ink-1'}">${o.decisions || 'none'}</b></span><span class="ink-3">${record}</span>${o.accepted ? O.commit('9f2c1ab') : ''}</div>`
      + `<div class="facts rec"><span class="by">${icon('chat')}<span>written by <a class="link" href="${O.go('convo-contract')}">${O.esc(WRITER)}</a></span></span>`
      + `<a class="link" href="${O.go(o.versions ? 'contract-to-accept' : 'contract-versions')}">${icon(o.versions ? 'chevronDown' : 'chevronRight')}2 versions</a>`
      + `${O.when(o.accepted, () => `<a class="link" href="${O.go('epic-building')}">${icon('pools')}its epic, tide-alerts</a>`)}</div>`
      + `${O.when(o.versions, () => versionList())}</section>`;
  };
  // versionList draws a contract's versions, newest first: when, who wrote it, and what changed.
  const versionList = () => O.table('auto auto minmax(0, 1fr) minmax(0, 1.6fr)', ['', 'when', 'by', 'what changed'], [
    row([`<span>${O.chip('v2', 'new')}</span>`, '<span class="id">14:41</span>', `<span class="wide">${O.esc(WRITER)}</span>`, `<span class="wide">${O.idc('F2')} one alert per table, where v1 said one per table and day. ${O.idc('R3')} says the same. Asked for at 14:39.</span>`]),
    row(['<span class="chip">v1</span>', '<span class="id">14:15</span>', `<span class="wide">${O.esc(WRITER)}</span>`, '<span class="wide ink-2">First written: three ways it could go wrong, one row you did not ask for, five requirements, one decision.</span>']),
  ], { touch: 'auto minmax(0, 1fr)' });

  // CONTRACT_TALK: what the conversation that wrote the contract said about it. Asking for a change is a reply there.
  const CONTRACT_TALK = [
    { k: 'reply', at: '14:15', md: ['The contract is written. It has three ways this could go wrong, one row you did not ask for, and one decision that is yours.'] },
    { k: 'you', at: '14:38', text: ['Why three retries and not one?'] },
    { k: 'reply', at: '14:38', md: ['The office’s endpoint dropped 2 of the last 100 test sends. Three tries put a missed alert under one in a hundred thousand; one try leaves it at one in fifty.'] },
  ];
  const contractDock = (about) => ({ state: 'waiting', name: 'tidepool', placeholder: 'Ask for a change, or ask why', about, focused: !!about, draft: about ? 'Make it one alert per table, not per table and day.' : '' });
  // tideConvo is the conversation that wrote the contract, in the middle while the contract is open on the right.
  const tideConvo = (about, panel) => O.convoPage({
    title: { toggle: true, waiting: 6, cmds: O.convoCmds(), dir: 'tidepool', here: 'Wrote the tide-alerts contract', strip: W.strip(W.ctx.low) },
    work: W.workContract, on: 'tide-alerts', panel, items: W.tideplan.concat(CONTRACT_TALK.slice(1)), dock: contractDock(about),
  });

  const contract = (id, name, note, o) => O.scenario({
    id, group: 'Contract', title: name, note,
    desk: () => O.desk({ inboxState: 'rail', open: id, side: 'panel', pages: [tideConvo(o.about, O.sidePanel({ label: 'tide-alerts contract', tone: 'attention', page: O.subPage({ label: 'contract', record: true, close: 'convo-contract', ...o.page(false) }) }))] }),
    phone: () => O.screen(O.subPage({ label: 'contract', ...o.page(true) }), { over: o.phoneOver ? o.phoneOver() : undefined }),
  });

  const CONTRACT_CRUMBS = { back: 'convo-contract', crumbs: [{ t: 'tidepool' }, { t: 'tide-alerts' }], here: 'Contract' };
  const acceptBar = (left, ready) => `<div class="cmdbar">${left}<span class="end">${O.touch() ? O.btn('Talk about it', { icon: 'chat', go: 'contract-talk' }) : ''}${O.btn('Accept', { variant: 'primary', state: ready ? '' : 'disabled' })}</span></div>`;

  contract('contract-accepted', 'A contract, accepted', 'Gambit\'s contract as the page it is: the decision line (what is approved, the level of care, decisions needed), then its ten sections in its own order. "How bad" is each failure\'s own rating and the worst row sets the level of care. Acceptance freezes it; the sentence that accepted it is on the decision line.', {
    page: () => ({ ...CONTRACT_CRUMBS, body: `${dline({ accepted: true })}${contractSections()}` }),
  });

  contract('contract-to-accept', 'A contract waiting to be accepted', 'The Director (the session that ran the brainstorm) wrote it and presents it; that conversation stays in the middle and the contract is the record on the right. What is yours is marked in attention: the decision it needs, each added row to keep or strike, and at severe care the section that cannot be undone. Accept opens when nothing is left. It is a record from the moment it is proposed, so it can be left and found again: under its project, in its conversation\'s Work, and from any notice about it.', {
    page: () => ({
      ...CONTRACT_CRUMBS, state: O.pill('proposed · waits for you · 26m', 'attention'),
      body: `${dline({ decisions: 1 })}${contractSections({ toAccept: true, unread: true })}`,
      foot: acceptBar(O.inote('3 things left: 1 decision, 1 added row to keep or strike, and read “can’t be undone”.'), false),
    }),
  });

  contract('contract-talk', 'Talking about a contract', 'Asking for a change is a reply to the session that wrote the contract, in the middle, while the contract stays in view on the right. A row\'s control puts that row on the message as what it is about. On the phone the conversation rises as a sheet over the lower half.', {
    about: `${O.idc('F2')} The office received the same alert many times.`,
    page: () => ({
      ...CONTRACT_CRUMBS, state: O.pill('waits for you to accept · 26m', 'attention'),
      body: `${dline({ decisions: 1 })}${contractSections({ toAccept: true, unread: true, say: true })}`,
      foot: acceptBar(O.inote('3 things left: 1 decision, 1 added row to keep or strike, and read “can’t be undone”.'), false),
    }),
    phoneOver: () => O.sheet(`<div class="flat">${O.items(CONTRACT_TALK.slice(1))}</div>${O.dock(contractDock(`${O.idc('F2')} The office received the same alert many times.`)).replace('class="dock"', 'class="dock flat"')}`, { title: 'Wrote the tide-alerts contract', sub: 'tidepool · the session that wrote this contract' }),
  });

  contract('contract-versions', 'A contract\'s versions', 'A contract is a record from the moment it is proposed, not words in a conversation. Each change the session makes is a new version with its time, its writer and what changed; the rows a version changed carry its mark. Accepting freezes the version accepted, and the epic is made from it.', {
    page: () => ({
      ...CONTRACT_CRUMBS, state: O.pill('proposed · waits for you · 26m', 'attention'),
      body: `${dline({ decisions: 1, versions: true })}${contractSections({ toAccept: true, unread: true, changed: true })}`,
      foot: acceptBar(O.inote('3 things left: 1 decision, 1 added row to keep or strike, and read “can’t be undone”.'), false),
    }),
  });

  contract('contract-set-aside', 'A contract set aside', 'Not accepted, and not gone. It keeps its versions and the words it was set aside with, and is found under its project with what has ended. Reopen makes it proposed again, and its conversation picks it up.', {
    page: () => ({
      ...CONTRACT_CRUMBS, state: O.pill('set aside · 2d'),
      body: `${dline({ aside: true })}${contractSections({})}`,
      foot: `<div class="cmdbar">${O.inote('Set aside by you 2 days ago. Nothing was built.')}<span class="end">${O.btn('Reopen', { icon: 'retry' })}</span></div>`,
    }),
  });

  contract('contract-ready', 'A contract with nothing left to settle', 'The decision is made, the added row is kept and the section that cannot be undone has been read. Accept is the one primary action on the page.', {
    page: () => ({
      ...CONTRACT_CRUMBS, state: O.pill('ready to accept', 'attention'),
      body: `${dline({})}${contractSections({ toAccept: true, kept: true, decided: true })}`,
      foot: acceptBar(O.inote('Nothing is left to settle.', 'ok'), true),
    }),
  });

  // ---------- phase and worker ----------

  // A task's page: its five stages in order (red, implement, green, review, integrate), the one it is in opened.
  const RED_RUN = { phase: 'red', verdict: 'holds', state: 'ended', passed: false, exit: 1, source: 'captured', commit: 'a1b2c3d' };
  const GREEN_RUN = { phase: 'green', verdict: 'holds', state: 'ended', passed: true, exit: 0, source: 'captured', commit: 'e4f5a6b' };
  const ROUNDS = `<div class="flat">${O.items([
    { k: 'checks', turn: false, round: 1, checks: [{ name: 'tests', ok: true }, { name: 'lint', ok: true }, { name: 'owned files', ok: false }], note: 'owned files: the change touches go.mod, which this task does not own.' },
    { k: 'checks', turn: false, round: 2, checks: [{ name: 'tests', ok: true }, { name: 'lint', ok: true }, { name: 'owned files', ok: true }], note: 'All three checks pass.' },
  ])}</div>`;
  const stageBody = (o) => ({
    red: O.evidenceRun(RED_RUN),
    implement: `<span class="sub">Attempts</span>` + O.table('auto minmax(0, 1fr) auto', null, [
      row(['<span class="id">1</span>', '<span class="wide">Wrote the refusal after the file was opened for writing; the review confirmed it.</span>', `<span class="end">${O.chip('spent', 'bad')}</span>`]),
      row(['<span class="id">2</span>', '<span class="wide">Checks the file exists before opening it; returns ErrExists.</span>', `<span class="end">${o.at === 'implement' ? O.chip('running', 'live') : O.chip('done', 'ok')}</span>`]),
    ], { touch: 'auto minmax(0, 1fr) auto' })
      + `<span class="sub">Attempt 2 · check rounds</span>${ROUNDS}<span class="sub">Gate · just check · 2:41 of 30 min</span><div class="log">ok   lighthouse/internal/export   0.412s\nok   lighthouse/internal/roster   1.038s\n…   lighthouse/internal/telemetry</div>`
      + `<span class="sub">Judge</span>${o.at === 'implement' ? O.inote('The judge reads the change once the gate passes.') : O.inote('Judged: the goal is met.', 'ok')}`,
    green: `<span class="sub">Reproduction</span>${O.inote('The red test, run on the change, now passes.', 'ok')}<span class="sub">Green run</span>${O.evidenceRun(o.at === 'green' ? { phase: 'green', state: 'judging', source: 'captured', commit: 'e4f5a6b' } : GREEN_RUN)}`,
    review: O.table('auto minmax(0, 1.4fr) auto minmax(0, 1fr)', ['gate', 'finding', 'where', 'what happened'], [
      row(['<span class="strong">floor</span>', '<span class="wide ink-3">no findings</span>', '<span></span>', `<span class="wide">${O.chip('holds', 'ok', 'check')}</span>`]),
      row(['<span class="strong">security</span>', '<span class="strong wide">A name holding “../” writes outside the rota directory.</span>', '<span class="id">export.go:41</span>', `<span class="wide">${o.at === 'review' ? `${O.chip('verifying', 'live')} a verifier is checking it` : `${O.chip('confirmed', 'bad')} fixed in attempt 2`}</span>`]),
      row(['<span></span>', '<span class="strong wide">The error message names the wrong path.</span>', '<span class="id">export.go:93</span>', `<span class="wide">${O.chip('dropped')} it names the path the user gave</span>`]),
    ], { touch: 'auto minmax(0, 1fr)' }),
    integrate: O.inote('Accepted. It waits for its merge: shift-store’s is ahead of it.'),
  });
  const STAGES = ['red', 'implement', 'green', 'review', 'integrate'];
  const SUM = {
    red: { passed: 'the test fails before any change · judged to hold', now: 'capturing the failing test' },
    implement: { passed: 'attempt 2 · the gate passed · judged met', now: 'attempt 2 · round 2 passed · the gate is running' },
    green: { passed: 'the test passes on the change · judged to hold', now: 'a judge is reading the green run' },
    review: { passed: 'floor holds · security: 1 finding confirmed and fixed', now: 'security found 1 thing · a verifier is checking it' },
    integrate: { passed: 'merged', now: 'accepted · waits for its merge' },
  };
  const TIME = { red: '1m', implement: '14m', green: '40s', review: '6m', integrate: '' };
  const SESSION = { implement: 'worker-running', review: 'worker-ended' };
  const phases = (open, at, o) => STAGES.map((name, i) => {
    const state = i < STAGES.indexOf(at) ? 'passed' : name === at ? 'now' : 'ahead';
    return O.phaseRow({ name, state, tone: name === at ? o.tone : undefined, sum: (SUM[name] || {})[state], time: state === 'now' ? o.time : state === 'passed' ? TIME[name] : '', open: name === open, body: stageBody({ at })[name], session: SESSION[name] });
  }).join('');

  // BAND ties a task's page to its epic: the epic's line, and every task of the epic with this one marked.
  const BAND = () => O.band({ pin: { ...W.rotaPin, blocker: '', go: 'epic-building' }, tasks: W.epicTasks, cur: 'export-cli' });

  const phasePage = (open, at, o) => ({
    label: 'task', back: 'epic-building', band: BAND(), crumbs: [{ t: 'lighthouse' }, { t: 'rota-export', go: 'epic-building' }], here: 'export-cli',
    state: o.state || O.pill(`${at} · ${o.time}`, at === 'integrate' ? 'engine' : 'live', at === 'integrate' ? {} : { state: 'working' }),
    cmds: o.cmds !== undefined ? o.cmds : `${O.btn('Interrupt', { class: 'stopper', icon: 'stop', tip: 'Hold this step where it is', tipSide: 'left' })}${O.btn('Stop…', { variant: 'quiet' })}`,
    touchCmds: o.cmds !== undefined ? o.cmds : `${O.btn('Interrupt', { class: 'stopper', icon: 'stop' })}${O.iconBtn('more', 'Stop this step…')}`,
    note: o.note,
    body: `<section><p class="now-sentence">${O.idc('R4')} Export refuses to overwrite an existing file</p><div class="brief"><span class="lbl">The planner’s brief</span><span>Make export refuse to overwrite an existing file. Own <code>internal/export/export.go</code> and its test. Do not change <code>go.mod</code>.</span></div></section><section>${phases(open, at, o)}</section>`,
  });
  // The conversation that steers the epic stays in the middle beside its tasks' records too: the words never move.
  const phase = (id, name, note, open, at, o = {}) => O.scenario({
    id, group: 'Phase and worker', title: name, note,
    desk: () => O.desk({ inboxState: 'rail', open: id, side: 'panel', pages: [rotaConvo({ panel: O.sidePanel({ label: 'export-cli', tone: 'attention', page: O.subPage({ ...phasePage(open, at, { time: '2:41', ...o }), record: true, up: 'epic-building', close: 'convo-epic' }) }) })] }),
    phone: () => O.screen(O.subPage(phasePage(open, at, { time: '2:41', ...o }))),
  });

  phase('phase-implement', 'A task being implemented', 'A task\'s page says what it belongs to before anything else: its epic\'s line, every task of that epic with this one marked, the requirement it proves and the planner\'s brief. Then its five stages in order, the one it is in opened: here implement, with its attempts, its check rounds as the same block the conversation uses, the gate\'s last lines and the judge. The epic\'s conversation stays in the middle; the task is the record on the right, under the epic in its crumbs.', 'implement', 'implement');
  phase('phase-red', 'A task\'s red stage', 'Red is one evidence run: the task\'s test, failing before any change.', 'red', 'implement');
  phase('phase-green', 'A task\'s green stage', 'Green is the reproduction, then the green evidence run, read by a judge.', 'green', 'green', { time: '0:40' });
  phase('phase-review', 'A task\'s review', 'One row per review gate and per finding: what, where, and what happened to it. A finding being verified says so; verify is part of review, not a stage of its own.', 'review', 'review', { time: '6m' });
  phase('phase-integrate', 'A task waiting to integrate', 'An accepted task is teal: every gate took it and it waits for its merge.', 'integrate', 'integrate', { tone: 'acc', state: O.pill('accepted · waits to integrate', 'engine'), cmds: '' });
  phase('phase-refused', 'A step command that was refused', 'The refusal is under the title row of the page that sent it, with the server\'s reason.', 'implement', 'implement', { note: 'Interrupt was refused: the step ended a moment ago.' });

  const WORKER = [
    { k: 'fold', v: 'brief', text: ['Make export refuse to overwrite an existing file. Own `internal/export/export.go` and its test. Do not change `go.mod`.'] },
    { k: 'reply', at: '14:21', md: ['I’ll read the writer first, then add the refusal before the file is opened.'] },
    { k: 'run', n: 4, names: 'Read ×2, Edit, Bash', dots: ['', '', '', ''] },
    { k: 'checks', at: '14:29', round: 1, checks: [{ name: 'tests', ok: true }, { name: 'lint', ok: true }, { name: 'owned files', ok: false }], note: 'owned files: the change touches go.mod, which this task does not own.' },
    { k: 'tools', list: [{ name: 'Bash', arg: 'git checkout go.mod', result: 'ok' }] },
    { k: 'reply', at: '14:33', md: ['Reverted `go.mod`. The refusal returns `ErrExists` and the test covers it.'] },
    { k: 'checks', at: '14:36', round: 2, checks: [{ name: 'tests', ok: true }, { name: 'lint', ok: true }, { name: 'owned files', ok: true }], note: 'All three checks pass.' },
  ];
  const worker = (id, name, note, dock, o = {}) => O.convoScenario({
    id, group: 'Phase and worker', name, note,
    title: { back: 'phase-implement', crumbs: [{ t: 'lighthouse' }, { t: 'rota-export', go: 'epic-building' }, { t: 'export-cli', go: 'phase-implement' }], here: 'Worker session', cmds: '', strip: W.strip(W.ctx.low) },
    desk: { title: { toggle: true } }, band: BAND(), items: o.items || WORKER, dock, phone: { over: o.phoneOver },
  });
  worker('worker-running', 'A worker\'s session, running', 'A worker\'s conversation uses the same four shapes: its brief folded to a line, check rounds as blocks. The dock is a step\'s: a message is a nudge, and the placeholder says it interrupts the turn first.',
    { step: { label: 'export-cli' }, tool: 'gate', what: 'just check', clock: '2:41', name: 'export-cli', placeholder: 'Nudge export-cli. It interrupts the turn, then gives it your words' });
  worker('worker-held', 'A worker\'s session, held', 'Held is peach: it waits on you. A nudge resumes it with your words.',
    { state: 'held', step: { label: 'tz-fixtures', held: true }, what: 'waits on your answer · 2m', name: 'tz-fixtures', placeholder: 'Nudge tz-fixtures. It resumes with your words' });
  worker('worker-stop-confirm', 'Stopping a step asks first', 'Stop is behind More and a confirm, in place at the desk and as a sheet on touch.',
    { step: { label: 'export-cli', confirm: true }, what: 'just check', clock: '2:44', name: 'export-cli' }, { phoneOver: O.confirmSheet('Stop export-cli?', 'Stop export-cli', 'Keep it running', 'The task ends short and the planner decides what to do next.') });
  worker('worker-ended', 'A worker\'s session that has ended', 'An ended step has no dock: there is nothing to send to. Its end is the last block.',
    null, { items: WORKER.concat([{ k: 'stepend', at: '14:41', agent: 'export-cli', end: 'done', summary: 'The refusal returns ErrExists; round 2 passed and the gate passed.' }]) });
  worker('worker-note', 'A nudge that may or may not have landed', 'The call left and no answer came. The note says so and that nothing was resent.',
    { step: { label: 'export-cli' }, tool: 'gate', what: 'just check', clock: '2:52', name: 'export-cli', note: 'No answer to your nudge. It may have landed; nothing was sent twice.' });
})();
