// Round 6 wireframe: the conversation's scenarios: every item kind, the dock in every state, and the Ask.
(() => {
  'use strict';
  const { W } = O;

  // convo registers one conversation scenario for both frames from one description.
  //   c.title, c.items, c.dock, c.reader, c.pins, c.find, c.notices: the page.
  //   c.phone: fields that differ on the phone ({ title, dock, over, skip }). c.desk: { inboxState, open, pages, over }.
  // convo registers a conversation scenario. c.panel names one of its lists to draw open: 'agents' or 'running'; at
  // the desk it is the panel beside the conversation, on touch a sheet over it.
  const convo = (c) => {
    const KEY = { agents: 'agents', running: 'running' };
    const page = (touch) => O.convoPage({
      title: touch ? { ...c.title, ...((c.phone || {}).title || {}) } : { toggle: true, waiting: 6, cmds: O.convoCmds(), ...c.title, ...((c.desk || {}).title || {}) },
      items: c.items, reader: c.reader, work: c.work, band: c.band, find: c.find, notices: c.notices, over: touch ? undefined : (c.desk || {}).pageOver,
      head: touch ? (c.phone || {}).head : undefined,
      on: c.panel ? KEY[c.panel] : undefined, pick: c.pick, panel: c.panel && !touch ? O.listPanel(c.panel, c.work, 'work-rail') : '',
      dock: c.dock === null ? null : { ...(c.dock || {}), ...(touch ? ((c.phone || {}).dock || {}) : {}) },
    });
    // Its title row names the host the thing runs on: a pane's, or a session's. The inbox at rest is a rail, which hides
    // its rows, so the page's own title is the only host drawn; c.hosts names what else is drawn.
    const kinds = [c.pane ? 'pane' : 'session'];
    O.scenario({
      id: c.id, group: c.group, title: c.name, note: `${c.note}${O.hostsNote(c.hosts || kinds)}`, differs: c.differs, widths: c.widths,
      desk: c.desk === null ? undefined : () => O.desk({ inboxState: (c.desk || {}).inboxState || 'rail', open: (c.desk || {}).open || c.id, inbox: (c.desk || {}).inbox, pages: [page(false), ...((c.desk || {}).pages || [])], over: (c.desk || {}).over, side: c.panel ? 'panel' : 'rail' }),
      phone: c.phone === null ? undefined : () => O.screen(page(true), { over: (c.phone || {}).over || (c.panel ? O.listSheet(c.panel, c.work) : undefined) }),
    });
  };
  O.convoScenario = convo;
  const PANE_KEYS = ['Android draws **quick keys** under the dock for a terminal pane: Esc, Ctrl-C, Enter and the arrows, sent straight to the pane. The web has a keyboard and draws none.'];

  // ---------- conversation ----------

  convo({
    id: 'convo-working', group: 'Conversation', name: 'A working session',
    note: 'The reply is the only plain full-width text. Your block is raised with a mauve rail; machinery is grey lines on a thread; the running call has the live rail. The state is drawn once, on the dock, beside Interrupt.',
    title: W.lampTitle, items: W.lamp, dock: W.lampDock,
    phone: { dock: { quickKeys: true } }, differs: PANE_KEYS,
  });

  convo({
    id: 'convo-blocks', group: 'Conversation', name: 'Every Block',
    note: 'One shape for everything someone said: a rail, a label in the rail\'s tone, a body. Your words, an engine event, a step that ended well, badly or stopped, check rounds, an epic\'s lines, a settled question, an accepted contract, and a message still sending.',
    title: { dir: 'lighthouse', here: 'Planned the rota export, and steers its epic', strip: W.strip(W.ctx.mid) },
    items: W.blocks, reader: { scroll: 'top' }, dock: { state: 'working', what: 'thinking', clock: '0:12', name: 'lighthouse' },
  });

  convo({
    id: 'convo-lines', group: 'Conversation', name: 'Every Line and Rule',
    note: 'Machinery is one line each on a hairline thread: a thought, tool calls (a failed one opened), a run opened to its calls, notices, background tasks that ended. Rules mark an interrupt, a compaction (opened), and what is new. A step\'s brief and an engine prompt fold to a line.',
    title: W.beaconTitle, items: W.lines, reader: { scroll: 'top' }, dock: { state: 'done', what: '· 2m ago', name: 'tidepool' },
  });

  convo({
    id: 'convo-markdown', group: 'Conversation', name: 'Markdown in a reply and in your block',
    note: 'Headings stop at the title size. Inline code is sunk, not raised, so it never looks like a control. Code blocks and tables scroll sideways inside the column.',
    title: { dir: 'orchard', here: 'Parser tests for nested scion grafting', strip: W.strip(W.ctx.low) },
    items: W.markdown, reader: { scroll: 'top' }, dock: { state: 'done', what: '· 20m ago', name: 'orchard' },
  });

  convo({
    id: 'convo-find', group: 'New and find', name: 'Find in a conversation',
    note: 'Opens from the title row\'s search control, or / in an empty reply box. Matches are marked; the current one is solid and scrolled into view. Enter and Shift+Enter step through.',
    title: W.lampTitle, find: { value: 'jitter', count: '2 of 3' },
    items: W.lamp.map((it) => (it.k === 'you' && it.from ? { ...it, text: ['Cap the total retry window at 5 minutes and add jitter.'] } : it)),
    dock: W.lampDock,
    phone: { dock: { quickKeys: true } },
    differs: ['Android has no find today. The same bar is drawn for it, opened from More.'],
  });

  convo({
    id: 'convo-epic', group: 'The right rail', name: 'A conversation that started an epic',
    note: 'What a conversation has started is on its right rail, live, for as long as it matters: the epic\'s gauge with a mark when it asks. What it asks is also a line in the band under the title row, in words, with Answer: the band holds only what needs you. The transcript keeps one line where the epic was created; it does not scroll away. The tile, or Answer, opens the epic in its panel.',
    title: { dir: 'lighthouse', here: 'Planned the rota export, and steers its epic', strip: W.strip(W.ctx.mid) },
    work: W.workRota, items: W.rotaplan.concat([{ k: 'ask', ...W.rotaAsk }]), dock: { state: 'done', what: '· 13m ago', name: 'lighthouse' },
    differs: ['On touch the tiles are a strip under the title row; an epic\'s or a contract\'s opens it as the next page, a list opens as a sheet.'],
  });

  convo({
    id: 'convo-many-epics', group: 'The right rail', name: 'A conversation steering four epics',
    note: 'There is no cap on how many epics one conversation steers; the load is drawn instead. The rail has them in one group, as dots past three, and the context meter is in the title row. The band shows only the one that asks. When it carries too much, move an epic to a new conversation.',
    title: { dir: 'orrery', here: 'Follow-ups from the round-5 review', strip: W.strip(W.ctx.mid) },
    work: W.workMany, desk: { side: 'rail' },
    items: [
      { k: 'you', at: '13:02', text: ['Turn the round-5 review into epics: the judge follow-ups, orbit labels, notices, and the harbour map.'] },
      { k: 'run', n: 6, names: 'Read ×2, epic_create ×4', dots: ['', '', '', '', '', ''] },
      { k: 'epicevent', v: 'created', text: 'Created the epic from the accepted contract.', slug: 'orrery-followups', gauge: W.epics.followups.gauge, go: 'epic-building' },
      { k: 'epicevent', v: 'created', text: 'Created the epic from the accepted contract.', slug: 'orbit-labels', gauge: W.epics.labels.gauge, go: 'epic-releasing' },
      { k: 'epicevent', v: 'park', text: 'Parked: R5 is already met at base. Retry, drop it, or end?', slug: 'notices-usable', gauge: W.epics.parked.gauge, go: 'epic-parked' },
      { k: 'reply', at: '13:40', md: ['Four epics are out. notices-usable is parked on a question for you; the other three need nothing.'] },
    ],
    dock: { state: 'done', what: '· 20m ago', name: 'orrery' },
  });

  convo({
    id: 'convo-took-over', group: 'The right rail', name: 'A conversation that took an epic over',
    note: 'Moving an epic is deliberate and leaves a line on each side. Here the conversation was asked to take rota-export, did it with a tool call, and says so in one line; it starts with the contract and the epic\'s log, so it knows what it is steering. From now on the epic\'s questions and its ending come here, and it is drawn under this conversation in the inbox.',
    title: W.lampTitle, work: W.workRota,
    items: W.lamp.slice(0, 3).concat(W.tookOver), dock: { state: 'done', what: '· just now', name: 'lighthouse' },
  });

  convo({
    id: 'convo-handed-over', group: 'The right rail', name: 'A conversation an epic moved away from',
    note: 'The conversation that gave the epic up keeps one line saying where it went and who moved it, and the epic leaves its rail and its band. Its earlier lines about the epic stay as they were.',
    title: { dir: 'lighthouse', here: 'Planned the rota export, and steers its epic', strip: W.strip(W.ctx.mid) },
    work: {}, items: W.handedOver(), dock: { state: 'done', what: '· just now', name: 'lighthouse' },
  });

  convo({
    id: 'convo-contract', group: 'The right rail', name: 'A conversation that wrote a contract',
    note: 'A contract that waits for you is a tile on the rail with a mark, and a line in the band saying what is left to settle, with Read and decide. The transcript keeps one line where it was written. The tile opens the contract in its panel.',
    title: { dir: 'tidepool', here: 'Wrote the tide-alerts contract', strip: W.strip(W.ctx.low) },
    work: W.workContract, items: W.tideplan, dock: { state: 'waiting', name: 'tidepool', placeholder: 'Ask tidepool for a change to the contract' },
  });

  convo({
    id: 'convo-shell', group: 'Conversation', name: 'A terminal pane with no conversation',
    note: 'A plain shell has no transcript. The page says so, and the dock sends a command.', pane: true,
    title: { dir: 'orrery', here: 'zsh', strip: W.strip(null, { host: W.rows.shell.host }) }, reader: { state: 'empty', hint: 'This is a terminal pane with no Claude session in it.' },
    dock: { state: 'idle', shell: true, name: 'orrery' },
    phone: { dock: { quickKeys: true } }, differs: PANE_KEYS,
  });

  convo({
    id: 'convo-hot', group: 'Conversation', name: 'A host under load, and a context nearly full',
    note: 'A meter is grey until its level is warn or crit; then its value and bar take yellow or red. The context does the same from 60 and from 80 percent. A quantity is never peach.',
    title: { dir: 'tidepool', here: 'Tide table cache warm-up', strip: W.strip(W.ctx.high, { hot: true }) }, items: W.short, dock: { state: 'done', what: '· 4m ago', name: 'tidepool' },
  });

  convo({
    id: 'convo-other-host', group: 'Conversation', name: 'A session on another host',
    note: 'The server reads meters only for the host it runs on. Another host shows its name and says there are none, in place of a blank strip.',
    title: { dir: 'kelp', here: 'Kelp bed survey importer', strip: W.strip(null, { host: 'brine', other: true }) }, items: W.short, dock: { state: 'done', what: '· 1d ago', name: 'kelp' },
  });

  convo({
    id: 'pane-host-unreachable', group: 'Conversation', name: 'A pane whose host does not answer',
    note: 'A terminal pane\'s conversation as it was last stored. Its host does not answer, so its title row says unreachable with no meters, and its dock says so in words. The dock offers nothing that would act on the pane: no input, no queue, no Interrupt and no quick keys. The transcript stays as it was stored.',
    pane: true, hosts: ['paneDown', 'hostDown'],
    title: { dir: 'tidepool', here: 'Tide table cache warm-up', state: O.pill('unreachable', 'unreachable', { state: 'unreachable' }), strip: W.strip(null, { host: 'halyard', down: true }) },
    items: W.short, dock: { state: 'unreachable', host: 'halyard', what: '· shown as last stored' },
    differs: ['Android draws no quick keys under this dock, since they would send keys to a pane that does not answer. The web under 760 draws the same dock, with no input.'],
  });

  convo({
    id: 'convo-command-error', group: 'Empty, loading, error', name: 'A command from this page failed',
    note: 'Today every such error is a line in the inbox\'s head, which is hidden while a page is open. It is drawn on the page that sent the command, under the title row.',
    title: { ...W.shortTitle, note: 'Could not archive this session: the server did not answer. Nothing changed.' }, items: W.short, dock: { state: 'done', what: '· 4m ago', name: 'tidepool' },
  });

  convo({
    id: 'convo-loading', group: 'Empty, loading, error', name: 'A conversation before its first answer',
    note: 'The title row and dock draw at once from the row; the transcript says it is loading.',
    title: W.shortTitle, reader: { state: 'loading' }, dock: { state: 'idle', name: 'tidepool' },
  });

  convo({
    id: 'convo-empty', group: 'Empty, loading, error', name: 'A session with nothing said yet',
    note: 'A session just started from New.',
    title: { dir: 'tidepool', here: 'New session', strip: W.strip(null) }, reader: { state: 'empty' }, dock: { state: 'idle', name: 'tidepool', focused: true },
  });

  convo({
    id: 'convo-notices', group: 'Notices', name: 'Notices over a page',
    note: 'What happened elsewhere, as it happens: two lines each, led by the thing it is about (an epic\'s gauge, a session\'s state), with when and what to do about it. Three show; the rest are in Notices, behind the bell.',
    title: W.lampTitle, items: W.lamp, dock: W.lampDock,
    notices: { list: W.notices, more: 2 },
  });

  convo({
    id: 'notices-centre', group: 'Notices', name: 'Where notices collect',
    note: 'The bell counts the unread and opens every notice since you last cleared them, by day. A notice that left the screen is still here. On touch it is a page.',
    title: W.lampTitle, items: W.lamp, dock: W.lampDock, desk: { pageOver: O.noticeCentre(W.noticeDays) },
    phone: null,
  });
  O.scenario({
    id: 'notices-page', group: 'Notices', title: 'Notices, on the phone',
    note: 'The same list as a page, reached from the bell in the inbox\'s head. Android\'s own notifications are separate and are drawn under Android only.',
    phone: () => O.screen(O.page({ label: 'notices', title: O.titleRow({ here: 'Notices', one: true, touchCmds: false }), body: `<div class="spage"><div class="nlist">${O.noticeList(W.noticeDays)}</div></div>` })),
  });
  convo({
    id: 'notices-none', group: 'Empty, loading, error', name: 'Notices: none',
    note: 'The empty centre says what will collect in it.',
    title: W.lampTitle, items: W.lamp, dock: W.lampDock, desk: { pageOver: O.noticeCentre([]) }, phone: null,
  });

  // ---------- the dock ----------

  const dock = (id, name, note, d, extra = {}) => convo({
    id, group: extra.group || (extra.work ? 'The right rail' : 'The dock'), name, note, title: extra.title || W.shortTitle, items: extra.items || W.short, dock: { name: 'tidepool', ...d }, ...extra,
  });

  dock('dock-idle', 'Done: nothing is pending', 'The quietest dock: one line saying the turn is done and when, and the input.', { state: 'done', what: '· 4m ago' });

  dock('dock-queued', 'Working, with messages queued', 'A message sent while a turn runs is queued. The tray says when it will be sent and how to change it, in the place the question arises. Each queued message has your dashed rail.',
    { state: 'working', tool: 'Bash', what: 'just warm --port lisbon', clock: '12:40', queued: [{ text: 'Also log the batch id whenever a batch is spooled.' }, { text: 'Then put the 5-minute cap in the README.' }] });

  dock('dock-draft', 'Typing while it works', 'The input is focused: the box takes the focus outline and keeps its state rail. Send here queues.', { state: 'working', tool: 'Bash', what: 'just warm --port lisbon', clock: '12:44', draft: 'And skip ports that are already warm.', focused: true, queued: [{ text: 'Also log the batch id whenever a batch is spooled.' }] });

  dock('dock-running', 'Sub-agents a turn is waiting on', 'An agent the turn started and is waiting for is work in progress: a tile on the right rail with a count, and its own panel, opened here, with each agent\'s task, what it is doing and a clock. The dock stays three things: the state, your queue, the input. On the phone the tile opens the same list as a sheet.',
    { state: 'working', tool: 'Agent', what: 'waiting on 2 sub-agents', clock: '3:02' }, { work: { agents: W.agents }, panel: 'agents', group: 'The right rail' });

  dock('dock-background', 'Left running in the background', 'A dev server or a timer left running is not work in progress and is not drawn as it: a grey tile, and a panel of grey rows. The session can be done while they run.',
    { state: 'done', what: '· 4m ago' }, { work: { background: W.shells }, panel: 'running', group: 'The right rail' });

  dock('dock-full', 'Everything at once', 'The busiest a conversation gets: a goal, an epic that asks, two sub-agents, a server left running, a held event, and a queue. The band has the goal and the epic that asks; the rail has each thing it started; the dock holds only what you typed.',
    { state: 'working', tool: 'Agent', what: 'waiting on 2 sub-agents', clock: '3:02', queued: [{ text: 'Also log the batch id whenever a batch is spooled.' }, { text: 'Then put the 5-minute cap in the README.' }] }, { work: W.workFull, group: 'The right rail' });

  dock('work-empty', 'A conversation that has started nothing', 'Every conversation has its right rail. With nothing started it is empty, so the reading column is in the same place in every conversation.',
    { state: 'done', what: '· 4m ago' }, { work: {}, group: 'The right rail', phone: null });

  dock('work-rail', 'The right rail', 'One tile for each thing the conversation steers: its contract, each epic\'s gauge with a mark when it asks, its sub-agents, what it left running in groups each named once. Each opens its own panel beside the conversation; the open one is lit, and pressing it again closes it. The goal is not a tile: it heads the band under the title row, with a line for whatever there asks you.',
    { state: 'working', tool: 'Agent', what: 'waiting on 2 sub-agents', clock: '3:02' }, { work: W.workFull, phone: null });

  convo({
    id: 'work-rail-group', group: 'The right rail', name: 'A group of many: its picker',
    note: 'A group of more than three is one tile of dots, one per thing in its colour, with a count, or a question mark when one of them asks. Pressing it opens a picker that names each thing and its state; choosing one opens its panel.',
    title: { dir: 'orrery', here: 'Follow-ups from the round-5 review', strip: W.strip(W.ctx.mid) },
    work: W.workMany, pick: 'epic', items: [{ k: 'you', at: '13:02', text: ['Turn the round-5 review into epics: the judge follow-ups, orbit labels, notices, and the harbour map.'] }], dock: { state: 'done', what: '· 20m ago', name: 'orrery' }, phone: null,
  });

  // The phone's header, folded. Reading back folds it to one line; you can hide it for good; the keyboard hides it all.
  dock('phone-head-scrolled', 'The phone\'s header while you read back', 'Scrolling back through the conversation folds the meters, the goal, the band and the tiles into one line under the title: the rail\'s groups as small badges. Back at the newest line, or a tap on it, opens them again.',
    { state: 'working', tool: 'Agent', what: 'waiting on 2 sub-agents', clock: '3:02' }, { work: { ...W.workFull, epics: [{ ...W.workRota.epics[0], asks: '', state: 'building · 49m' }] }, items: W.lamp, desk: null, phone: { head: 'scrolled' } });
  dock('phone-head-hidden', 'The phone\'s header hidden, with an ask waiting', 'The chevron at the end of the tiles, or Hide the header in More, keeps it folded in every conversation until you open it; it looks the same as the line scrolling makes. An ask is never hidden by it: the line turns peach, says how many ask you, and the badge shows the question mark. It does not open by itself.',
    { state: 'working', tool: 'Agent', what: 'waiting on 2 sub-agents', clock: '3:02' }, { work: W.workFull, items: W.lamp, desk: null, phone: { head: 'pinned' } });

  dock('dock-interrupting', 'Interrupting', 'From the press until the turn has ended, the state says so and the control is spent. A tool call in flight can take a while to end, so the bar counts how long it has been. Esc took the queued messages back into the box first.', { state: 'interrupting', word: 'interrupting…', what: 'waiting for the turn to end', clock: '0:04', draft: 'Also log the batch id whenever a batch is spooled.\n\nThen put the 5-minute cap in the README.', note: 'Your 2 queued messages are back in the box.' });

  dock('dock-interrupted', 'Interrupted by you: it has stopped', 'The answer to "has it stopped?": the state reads interrupted by you with the time, and the transcript ends on the rule. After your interrupt the session takes no events until you speak, so the dock says how many wait; they go in with your next message.',
    { state: 'interrupted', byYou: true, word: 'interrupted by you', what: '· 14:32', waits: 1 }, { items: W.short.concat([{ k: 'rule', v: 'interrupted', detail: '14:32' }]) });

  dock('dock-interrupt-queue', 'Interrupted, and the queue went next', 'An interrupt ends the turn; if messages were queued, the first one starts at once and the session keeps working. The rule marks where your interrupt landed and your message follows it. This is what Send now does.',
    { state: 'working', what: 'thinking', clock: '0:03', queued: [{ text: 'Then put the 5-minute cap in the README.' }] }, { items: W.short.concat([{ k: 'rule', v: 'interrupted', detail: '14:32' }, { k: 'you', at: '14:32', from: 'queued 14:20', text: ['Also log the batch id whenever a batch is spooled.'] }]) });

  dock('dock-cut-off', 'Cut off: the server stopped mid-turn', 'Not your interrupt: the server restarted while a turn ran. It has its own word and offers Resume, which carries on from where it was cut. A message does the same with new words.',
    { state: 'interrupted', what: '· the server restarted mid-turn · 2h ago' }, { title: { dir: 'almanac', here: 'The 2027 ephemeris import', strip: W.strip(W.ctx.mid) } });

  dock('dock-pane-queue', 'A terminal pane\'s queue', 'A pane\'s queue is Claude Code\'s own, with different rules, so it has different words: Claude Code takes a message when it chooses, and queued messages come back only all together.',
    { state: 'working', pane: true, tool: 'Bash', what: 'just warm --port lisbon', clock: '4:10', queued: [{ text: 'Also log the batch id whenever a batch is spooled.' }, { text: 'Then put the 5-minute cap in the README.' }] }, { pane: true, phone: { dock: { quickKeys: true } } });

  dock('dock-waiting', 'Waiting on you, with no options', 'The turn ended on a question in words. The state says it waits on you; the input answers.', { state: 'waiting' }, { items: W.short.concat([{ k: 'reply', at: '14:12', md: ['Should the warm-up also run on deploy, or only by hand?'] }]) });

  dock('dock-failed', 'Failed', 'A session that ended with an error says why, in red, once. A message starts it again.', { state: 'failed', what: '· the API refused the request · 1d ago' }, { title: { dir: 'kelp', here: 'Kelp bed survey importer', strip: W.strip(null, { host: 'brine', other: true }) } });

  dock('dock-stopped', 'Stopped by you', 'Stopped is grey, not red: you stopped it.', { state: 'stopped', what: '· 2d ago' });

  dock('dock-send-error', 'A send that did not land', 'The draft is kept and the line says what to do.', { state: 'done', what: '· 4m ago', draft: 'Run it for January too.', focused: true, error: 'Not sent. Your draft is kept; send again.' });

  dock('dock-send-unknown', 'A send with no answer', 'The request left and nothing came back, so the server may have taken it. It says to check first.', { state: 'done', what: '· 4m ago', draft: 'Run it for January too.', note: 'No answer from the server. It may have been sent: check the conversation before sending again.', noteKind: '' });

  dock('dock-cancelled', 'A queued message that was not delivered', 'Claude Code dropped a queued message when the turn was cancelled. It is struck through and labelled, not silently gone.', { state: 'done', what: '· 1m ago', queued: [{ text: 'Then put the 5-minute cap in the README.', cancelled: true }], queueWhy: 'dropped when the turn was cancelled' });

  // ---------- asks ----------

  const ask = (id, name, note, a, extra = {}) => convo({
    id, group: 'Asks', name, note, title: extra.title || W.beaconTitle, items: (extra.items || W.beacon).concat([{ k: 'ask', ...W.beaconAsk, ...a }]),
    dock: { state: 'waiting', name: 'tidepool', ...(extra.dock || {}) }, ...extra.more,
  });

  ask('ask-session', 'A session asks, with options', 'The one loud thing on the page. The question is body text at regular weight; each option is a numbered row with what it does; the recommended one is marked. One press answers.', {});

  ask('ask-permission', 'A permission prompt', 'A tool asks to run. The same component: the tool and what it would do as the question, and the three fixed answers.',
    { permission: true, q: ['Run this command?', { code: 'git push origin epic/rota-export --force-with-lease', lang: 'Bash' }], opts: [{ name: 'Yes' }, { name: 'Yes, and don’t ask again', desc: 'for commands like this one in this session' }, { name: 'No', desc: 'and say what to do instead, below' }] });

  ask('ask-many', 'More than three options, and a second question', 'Every option is answerable: keys run to 9 (today only the first three answer). A session that asks several questions shows which one this is.',
    { who: 'tidepool · Why the roster page is slow past forty shifts · question 1 of 2', opts: [...W.beaconAsk.opts, { name: 'Precompute the week nightly', desc: 'Fast reads; the page can be a day stale.' }, { name: 'Leave it', desc: 'Forty shifts is rare. Close this out.' }] });

  ask('ask-sending', 'An answer on its way', 'The picked option holds the focus ring until the session has it.', { picked: 0, state: 'sending' });

  ask('ask-error', 'An answer that was refused', 'The question moved on before the answer landed. The Ask says so and stays open on what is asked now.', { state: 'error', error: 'Not answered: the question changed before your answer arrived. This is what it asks now.' });

  convo({
    id: 'ask-answered', group: 'Asks', name: 'Answered', note: 'An answered Ask folds to one line where it stood, and the session works on.',
    title: W.beaconTitle, items: W.beacon.concat([{ k: 'ask', state: 'answered', who: 'Which fix should I make?', answer: { n: 1, name: 'One query per week' } }, { k: 'tools', list: [{ name: 'Edit', arg: 'internal/roster/query.go', status: 'running', clock: '0:04' }] }]),
    dock: { state: 'working', tool: 'Edit', what: 'internal/roster/query.go', clock: '0:04', name: 'tidepool' },
  });

  ask('ask-stale', 'A question nobody answered', 'An old question says how long it has waited. Not now takes it off your list without sending anything; archiving its session takes the whole session out of the inbox.',
    { who: 'commonframe · Merging main into the story-out client branch', age: '5d', stale: '5 days', q: ['The AWS fleet won’t launch: the budget pace check puts November spend ahead of today’s line, so it refuses every gate box. How should the gates run?'], opts: [{ name: 'Raise the budget line by $100', rec: true, desc: 'Wakes resume now; covers the remaining three gates.' }, { name: 'Raise it by $50', desc: 'Covers this gate and some of the next.' }, { name: 'Wait for the 14th', desc: 'No spend. Nothing gates until the line catches up.' }] },
    { title: { dir: 'commonframe', here: 'Merging main into the story-out client branch', strip: W.strip(W.ctx.low) }, items: [{ k: 'reply', at: 'Nov 12', md: ['No fleet boxes are running at all, so the likely blocker is the budget pace check.'] }], dock: { name: 'commonframe' } });

  ask('ask-decline', 'Declining a question, from its More', 'Not now is the visible control because it is the gentle one: it sends nothing. Declining denies the request and ends the turn, so it is in More, and named for what it does.', { menu: true });

  ask('ask-later', 'A question you put off', 'Not now took it off your list: out of the count and the peach, and quiet here. The session is still waiting on it, so while it is open the session takes no events; the dock says so. Bring it back puts it on your list again; anything new it asks comes back by itself.',
    { who: 'commonframe · Merging main into the story-out client branch', age: '5d', later: '2d', q: ['The AWS fleet won’t launch: the budget pace check puts November spend ahead of today’s line, so it refuses every gate box. How should the gates run?'], opts: [{ name: 'Raise the budget line by $100', rec: true, desc: 'Wakes the fleet today.' }, { name: 'Run the gates locally', desc: 'Slower, and nothing is spent.' }] },
    { title: { dir: 'commonframe', here: 'Merging main into the story-out client branch', strip: W.strip(W.ctx.low) }, items: [{ k: 'reply', at: 'Nov 12', md: ['No fleet boxes are running at all, so the likely blocker is the budget pace check.'] }], dock: { name: 'commonframe', state: 'idle', word: 'waiting on a question you put off', waits: 1, waitsWhy: 'on the question you put off' } });

  ask('ask-unanswerable', 'A question that cannot be answered by a press', 'Some requests have no options the client can send. The Ask says so and points at the input.', { opts: [], state: 'unanswerable', q: ['Paste the harbour office’s API token so I can test the alert end to end.'] });
})();
