// Round 6 wireframe: the rest. Starting a session, the key list, what only Android has, and what only the web has.
(() => {
  'use strict';
  const { W, icon, esc } = O;

  // ---------- New ----------

  const RESULTS = [
    { kind: 'jump', title: 'tidepool · Why the roster page is slow past forty shifts', note: 'A session that is waiting on you, 4 minutes old.' },
    { kind: 'resume', title: 'tidepool · Tide table cache warm-up', note: 'Stopped 2 days ago. Resuming picks it up where it was.' },
    { kind: 'fresh', title: '~/code/tidepool', note: 'Start a new session in this directory.', first: true },
    { kind: 'pane', title: '~/code/tidepool.worktrees/offline', note: 'Start a new session in this worktree.' },
  ];
  // The panel is drawn over the desk's open inbox, and over the phone's inbox: every kind of host is drawn in it.
  const newScenario = (id, name, note, n, o = {}) => O.scenario({
    id, group: 'New and find', title: name, note: `${note}${O.hostsNote(['session', 'epic', 'step'])}`, differs: o.differs,
    desk: () => O.desk({ open: 'convo-working', pages: [O.lampPage()], over: O.newPanel(n) }),
    phone: () => O.home({ over: O.newPanel(n) }),
  });
  newScenario('new-empty', 'New: the panel', 'New replaces Ask: it starts a session, or finds one. One field; the hint says what it takes. At the desk it is a panel over the page; on touch a sheet over the inbox.', { state: 'empty' },
    { differs: ['On Android today this is an inline panel under the head with no label or placeholder. It becomes the same sheet.'] });
  newScenario('new-working', 'New: asking', 'An ask can take a while. The field says so and the rest of the panel waits.', { state: 'working', query: 'roster page tidepool' });
  newScenario('new-results', 'New: results', 'Each result says in one word what choosing it does: go to a session, resume one, or start in a directory. A row that would start with your words says so.', { state: 'results', query: 'roster page tidepool', rows: RESULTS, cur: 0 });
  newScenario('new-form', 'New: starting a session', 'Choosing a directory opens the start form: where, and the first message. One primary action.', { state: 'form', query: 'roster page tidepool', form: { dir: '~/code/tidepool', text: 'Why is the roster page slow for weeks with more than forty shifts?' } });
  newScenario('new-none', 'New: nothing found', 'Today an empty answer is an empty panel.', { state: 'noresults', query: 'harbour pilot' });
  newScenario('new-error', 'New: the ask failed', 'The reason, and the field keeps what was typed.', { state: 'error', query: 'roster page tidepool', error: 'The search did not finish: the server did not answer in 90 seconds.' });

  // The Starter: a prompt first, then where it runs. It takes over starting a session from the new-* scenarios above, which
  // the build epic removes. brine does not answer in this world, so it is in no proposal and not in the host choice.
  const STARTER_WEB = 'The web under 760 draws this sheet over the inbox the same way, with the proposals at full width.';
  const STARTER_ANDROID = 'On Android today a prompt lists rows to start in, and the start form is a Directory and a First message, with no host choice. This sheet replaces both.';
  const STARTER_BEHIND = 'Behind it the inbox draws brine unreachable, as inbox-host-unreachable does: its card says so, and the Starter leaves it out.';
  const starter = (id, name, note, s, differs = [STARTER_WEB, STARTER_ANDROID]) => O.scenario({
    id, group: 'New and find', title: name, note: `${note} ${STARTER_BEHIND}${O.hostsNote(['proposal', 'start'])}`, differs,
    desk: () => O.desk({ inbox: { dirs: W.unreachableDirs() }, open: 'convo-working', pages: [O.lampPage()], over: O.starterPanel(s) }),
    phone: () => O.home({ dirs: W.unreachableDirs(), over: O.starterPanel(s) }),
  });
  starter('start-prompt', 'Starter: the prompt', 'The Starter takes over starting a session from the New panel: you type what to start first, and the server proposes where to run it. The new-* scenarios stay until the build epic removes them. Nothing is proposed until you press ⏎.',
    W.starter.states.prompt);
  starter('start-loading', 'Starter: finding a place', 'The prompt is sent and the place is asked for. Start waits for the answer, or for a host and a folder typed by hand.',
    W.starter.states.loading);
  starter('start-proposals', 'Starter: proposals', 'Ranked host and folder proposals in a dropdown under the prompt, the top one preselected and the place below set to it. Each shows its folder and its host. Another proposal can be picked. A host that does not answer, brine in this world, is in no proposal and not in the host choice.',
    W.starter.states.several);
  starter('start-none', 'Starter: nothing fits', 'No reachable host has a folder that fits the prompt. It says so, and a host and a folder are typed by hand. Start stays off until there is a folder.',
    W.starter.states.none);
  starter('start-failed', 'Starter: the ask failed', 'The proposals did not come back. The reason is said, the prompt is kept, and the place can be typed by hand or the ask tried again.',
    W.starter.states.failed);
  starter('start-typed-folder', 'Starter: a folder typed', 'A host chosen from the reachable hosts, and a folder typed. The list closes: the place is yours until a proposal is picked again. The folder is checked on that host when the session starts.',
    W.starter.states.typed, [STARTER_WEB, 'On Android today the start form has a Directory and no host to choose; here the host is chosen too, from the reachable hosts.']);

  // ---------- keys ----------

  O.scenario({
    id: 'keys', group: 'Web only', title: 'The key list',
    note: 'Every key the client answers to, grouped by what it is for. Escape\'s real behaviour (take back, then interrupt) is listed; today the list says only that it closes help. Opened by ? or the control at the foot of the inbox.' + O.hostsNote(['session', 'epic', 'step']),
    desk: () => O.desk({ open: 'convo-working', pages: [O.lampPage()], over: O.keyList() }), widths: [1600],
  });

  O.scenario({
    id: 'more-menu', group: 'Conversation', title: 'A conversation\'s More menu',
    note: 'Everything that had no pointer control is here with its key: archive, hide the meters, page the transcript, the key list. Stop is here too, behind its confirm.' + O.hostsNote(['session']),
    desk: () => O.desk({ inboxState: 'rail', open: 'convo-working', pages: [O.lampPage({ title: { toggle: true, waiting: 6, menu: O.menu([
      { label: 'Open in a new window', icon: 'open' }, { label: 'Find in this conversation', icon: 'search', kbd: '/' }, { label: 'Hide the meters', icon: 'host', kbd: 'm' }, 'sep',
      { label: 'Mark unseen', icon: 'check', kbd: 'x' }, { label: 'Archive', icon: 'archive', kbd: 'A' }, 'sep', { label: 'Keys', icon: 'keys', kbd: '?' },
    ]) } })] }),
    phone: () => O.screen(O.lampPage({ dock: { quickKeys: true } }), { over: O.sheet(O.menu([
      { label: 'Find in this conversation', icon: 'search' }, { label: 'Hide the meters', icon: 'host' }, 'sep', { label: 'Mark unseen', icon: 'check' }, { label: 'Archive', icon: 'archive' }, 'sep', { label: 'Stop this session…', icon: 'stop', danger: true },
    ]), { title: 'Retry backoff and jitter for the telemetry uploader', sub: 'lighthouse · working' }) }),
    widths: [1600],
  });

  // ---------- Android only ----------

  const androidPage = (here, body, o = {}) => O.page({
    label: here,
    title: O.titleRow({ back: o.back === null ? undefined : 'inbox-home', here, one: true, touchCmds: false }),
    body: `<div class="spage"><div class="inner">${body}</div></div>`, over: o.over,
  });
  const android = (id, name, note, draw, differs) => O.scenario({ id, group: 'Android only', title: name, note, phone: () => O.screen(draw()), differs: differs || ['Android only. The web has no such screen.'] });

  android('android-settings', 'Settings', 'One setting today: the server\'s address. It gets a title row and back like every page, says whether the address answers, and says where notifications stand, which the app never tells you today.', () => androidPage('Settings',
    `<section>${O.sec('Server')}<div class="form-row"><span class="t-label">Address</span>${O.field({ value: 'https://halyard.tail1234.ts.net', icon: 'host' })}</div>${O.inote('Connected to halyard.', 'ok')}<div class="doors">${O.btn('Save', { variant: 'primary' })}</div></section>`
    + `<section>${O.sec('Notifications')}<div class="group"><div class="litem"><span class="t">Questions and failures</span><span class="s">Sound and vibration</span><span class="e">${O.toggle(true, 'Questions and failures')}</span></div><div class="litem"><span class="t">Everything else</span><span class="s">Silent</span><span class="e">${O.toggle(true, 'Everything else')}</span></div></div>${O.inote('Push is registered with this phone’s distributor.', 'ok')}</section>`
    + `<section>${O.sec('About')}<div class="group"><a class="litem" href="#"><span class="t">Font licence</span><span class="s">Orrery Mono, a subset of Maple Mono NF</span><span class="e">${icon('chevronRight')}</span></a><div class="litem"><span class="t">Version</span><span class="s">0.46.0</span></div></div></section>`));

  android('android-first-run', 'First run: no server yet', 'With no address, Settings is the only screen. It asks one question and has one action.', () => androidPage('Welcome',
    `<section><p class="now-sentence">Which orrery server should this phone talk to?</p><div class="form-row"><span class="t-label">Address</span>${O.field({ placeholder: 'https://host.your-tailnet.ts.net', icon: 'host', state: 'focused' })}</div><div class="doors">${O.btn('Connect', { variant: 'primary', state: 'disabled' })}</div></section>`, { back: null }));

  android('android-settings-error', 'Settings: the address does not answer', 'Saving tests the address and says what happened.', () => androidPage('Settings',
    `<section>${O.sec('Server')}<div class="form-row"><span class="t-label">Address</span>${O.field({ value: 'https://halyrd.tail1234.ts.net', icon: 'host', state: 'error' })}</div>${O.inote('No answer from that address. Nothing was changed.', 'error')}<div class="doors">${O.btn('Save', { variant: 'primary' })}</div></section>`));

  const member = (name, state, line, action, tone) => `<div class="litem"><span class="t">${esc(name)} ${O.chip(state, tone || '')}</span><span class="s">${esc(line)}</span><span class="e">${O.btn(action)}</span></div>`;
  android('android-pools', 'Pools', 'Each pool, its members, who holds what and for how long, and who waits. Hold and Free are the two commands.', () => androidPage('Pools',
    `<section>${O.sec('gpu', { n: '2 members · 1 waiting' })}<div class="group">${member('gnomon', 'awake', 'granted to commonframe · just gate · 12m', 'Hold', 'live')}${member('sundial', 'asleep', 'free', 'Hold')}</div>`
    + `<div class="group"><div class="litem"><span class="t">#1 lighthouse</span><span class="s">just check · needs 1</span><span class="e">waiting 3m</span></div></div></section>`
    + `<section>${O.sec('builders', { n: '1 member' })}<div class="group">${member('anvil', 'awake', 'held by you · 2h', 'Free', 'attention')}</div></section>`));

  android('android-pools-empty', 'Pools: none in use', 'The empty state says so.', () => androidPage('Pools', `<div class="blank"><span class="t-body">No pools are in use.</span><span>A pool shows here when a step asks for a machine from it.</span></div>`));

  const push = (o) => `<div class="${O.cls('push', o.quiet && 'quiet')}"><div class="app-line">${O.mark()}<span>Orrery · ${esc(o.when)}</span></div><span class="t">${esc(o.title)}</span><span>${esc(o.text)}</span>${o.acts ? `<div class="acts">${o.acts.map((a) => `<span>${esc(a)}</span>`).join('')}</div>` : ''}</div>`;
  android('android-notifications', 'Notifications in the shade', 'What a push looks like outside the app. A title is a directory and what is asked, never an id. A question carries its options by name, not 1 2 3. Questions and failures sound; the rest are silent. The small icon is the orrery mark, not Android\'s info glyph.', () => `<div class="shade">`
    + push({ when: 'now', title: 'tidepool asks: which fix should I make?', text: 'Why the roster page is slow past forty shifts', acts: ['One query per week', 'Cache keepers', 'Reply'] })
    + push({ when: '2m', title: 'rota-export asks you a question', text: 'tz-fixtures: bump the embedded tz database?', acts: ['Open'] })
    + push({ when: '9m', title: 'offline-tables: release step 2 failed', text: 'CI is red on the tag.', acts: ['Open'] })
    + push({ when: '26m', title: 'tide-alerts: a contract is ready to read', text: 'severe care · 1 decision is yours', quiet: true })
    + push({ when: '31m', title: 'lighthouse · done', text: 'Retry backoff and jitter for the telemetry uploader', acts: ['Reply'], quiet: true })
    + push({ when: '1h', title: 'Weekly usage limit reached', text: 'Held: rota-export, orrery-followups. Resets 15:10.', quiet: true })
    + push({ when: '2h', title: 'holder-scopes shipped v0.45.0', text: 'All four requirements proven. No gaps.', quiet: true })
    + `</div>`);

  android('android-notification-result', 'A notification after its action', 'An answer from the shade either lands and the notification leaves, or it says in words why not.', () => `<div class="shade">`
    + push({ when: 'now', title: 'tidepool: not answered', text: 'The question changed before your answer arrived. Open it to see what it asks now.', acts: ['Open'] })
    + push({ when: '1m', title: 'lighthouse: not sent', text: 'Your reply did not reach the server. It is kept in the app.', acts: ['Open'] })
    + `</div>`);

  O.convoScenario({
    id: 'android-keyboard', group: 'Android only', name: 'Typing: the keyboard is up',
    note: 'With the keyboard up the whole header folds to nothing: the title, the goal, the ask line and the tiles. The dock stays whole above the keyboard: state, queue and input. The quick keys fold away while the draft has text.',
    desk: null, title: W.lampTitle, work: W.workFull, phone: { head: 'keyboard' }, items: W.lamp, hosts: [],
    dock: { ...W.lampDock, draft: 'And skip ports that are already warm.', focused: true },
    differs: ['The web under 760 keeps its title row; a browser\'s own bar already folds.'],
  });

  O.convoScenario({
    id: 'android-quick-keys', group: 'Android only', name: 'Quick keys, after Esc',
    note: 'A quick key goes straight to the terminal. After Esc or Ctrl-C the dock says what happened to the pane within five seconds.',
    desk: null, title: W.lampTitle, items: W.short.concat([{ k: 'notice', kind: 'interrupted', text: 'Request interrupted by user' }]),
    dock: { state: 'done', what: '· just now', name: 'lighthouse', pane: true, quickKeys: true, keyFlash: 'Esc', keyNote: 'Esc sent. The pane is now done.', keyNoteKind: 'ok' },
  });

  O.convoScenario({
    id: 'android-key-not-sent', group: 'Android only', name: 'A quick key that was not sent',
    note: 'The key did not reach the terminal. One line, in red, until a later key lands.',
    desk: null, title: W.lampTitle, items: W.lamp, dock: { ...W.lampDock, pane: true, quickKeys: true, keyNote: 'Ctrl-C was not sent: the server did not answer.', keyNoteKind: 'error' },
  });

  O.scenario({
    id: 'android-toast', group: 'Android only', title: 'A row archived, with undo',
    note: 'On touch a command that removes something says so in one line at the foot, with the way back.' + O.hostsNote(['session', 'epic', 'step']),
    phone: () => O.home({ over: O.toast('Archived “Tide table cache warm-up”.', 'Undo') }),
    differs: ['The web under 760 shows the same line.'],
  });
})();
