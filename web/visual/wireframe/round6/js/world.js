// Round 6 wireframe: the synthetic world every scenario draws from. It keeps round 5's cast (lighthouse and its
// rota-export epic, tidepool and its tide-alerts contract) so the rounds can be compared, and adds what has shipped
// since: step ends, held events, background tasks, interrupts. Nothing here is real data.
(() => {
  'use strict';
  const W = {};
  O.W = W;

  // ---------- host and context ----------

  W.stats = [
    { name: 'cpu', value: '12%', pct: 12, level: 'ok', title: 'cpu: 12% of 32 cores' },
    { name: 'ram', value: '14G', pct: 44, level: 'ok', title: 'ram: 14G of 32G' },
    { name: 'disk', value: '49%', pct: 49, level: 'ok', title: 'disk: 49% of 1.8T' },
    { name: 'net', value: '↓1M ↑298K', level: 'ok', title: 'net: down 1M/s, up 298K/s' },
  ];
  W.statsHot = [
    { name: 'cpu', value: '71%', pct: 71, level: 'warn', title: 'cpu: 71% of 32 cores' },
    { name: 'ram', value: '30G', pct: 94, level: 'crit', title: 'ram: 30G of 32G' },
    { name: 'disk', value: '49%', pct: 49, level: 'ok' },
    { name: 'net', value: '↓1M ↑298K', level: 'ok' },
  ];
  W.strip = (ctx, o = {}) => ({ host: o.host || 'halyard', stats: o.other ? [] : (o.hot ? W.statsHot : W.stats), other: o.other, ctx });
  W.ctx = { low: { pct: 15, used: '152k', window: '1M' }, mid: { pct: 64, used: '640k', window: '1M' }, high: { pct: 87, used: '174k', window: '200k' } };

  // ---------- the inbox ----------

  const PIPS = { run2: ['ok', 'run', '', '', ''], held2: ['ok', 'mine', '', '', ''], limit2: ['ok', 'plan', '', '', ''], limit3: ['ok', 'ok', 'plan', '', ''], run3: ['ok', 'ok', 'run', '', ''], bad1: ['ok', 'bad', '', '', ''], acc: ['ok', 'ok', 'ok', 'ok', 'acc'] };
  W.PIPS = PIPS;

  W.rows = {
    lamp: { id: 'convo-working', state: 'working', title: 'Retry backoff and jitter for the telemetry uploader', age: '1m', l2kind: 'live', tool: 'Bash', l2: 'go test ./internal/telemetry/...', clock: '0:46' },
    rotaplan: { id: 'convo-epic', state: 'done', title: 'Planned the rota export, and steers its epic', age: '13m', l2: 'The epic is building. I will tell you when it asks for anything.' },
    beacon: { id: 'ask-session', state: 'waiting', title: 'Why the roster page is slow past forty shifts', age: '4m', l2kind: 'asks', l2: 'Which fix should I make?' },
    tideplan: { id: 'convo-contract', state: 'waiting', title: 'Wrote the tide-alerts contract', age: '26m', l2kind: 'asks', l2: 'Read and accept the tide-alerts contract?' },
    stale: { id: 'ask-stale', state: 'waiting', title: 'Merging main into the story-out client branch', age: '5d', l2kind: 'asks', l2: 'The AWS fleet won’t launch. How should the gates run?' },
    almanac: { id: 'dock-cut-off', state: 'interrupted', title: 'The 2027 ephemeris import', age: '2h', l2: 'Cut off mid-turn when the server restarted.' },
    phase: { id: 'dock-idle', state: 'done', title: 'Phase endpoint returns ISO dates', age: '39m', unseen: true, l2: 'Done. `GET /phase` now answers ISO 8601 and the three callers are updated.' },
    orchard: { id: 'convo-markdown', state: 'done', title: 'Parser tests for nested scion grafting', age: '20m', unseen: true, l2: 'All 41 cases pass. Two grammar gaps are written up at the end.' },
    shell: { id: 'convo-shell', state: 'idle', title: 'zsh', age: '3h', tag: 'a terminal with no conversation' },
    failed: { id: 'dock-failed', state: 'failed', title: 'Kelp bed survey importer', age: '1d', l2: 'The session ended with an error: the API refused the request.' },
    stopped: { id: 'dock-stopped', state: 'stopped', title: 'Tide table cache warm-up', age: '2d', l2: 'You stopped it.' },
    home: { id: 'dock-waiting', state: 'waiting', title: 'Swapping the phone’s font for a subset', age: '8m', l2kind: 'asks', l2: 'Its turn ended on a question in words' },
  };

  W.epics = {
    rota: {
      id: 'epic-question', slug: 'rota-export', count: '3 of 6 proven', words: 'building · 49m · 2 of 7 tasks done',
      blocker: 'tz-fixtures: bump the embedded tz database?', gauge: { done: 2, started: 3, total: 7, tone: 'attention', inner: 'dot' },
      steps: [
        { label: 'export-cli', pips: PIPS.run3, doing: 'implement · gate just check · 2:41', go: 'worker-running' },
        { label: 'tz-fixtures', held: true, pips: PIPS.held2, doing: 'held · waits on your answer', go: 'worker-held' },
      ],
      tail: '3 not started',
    },
    followups: {
      id: 'epic-building', slug: 'orrery-followups', count: '1 of 3 proven', words: 'building · 20m · 0 of 3 tasks done',
      gauge: { done: 0, started: 2, total: 3, tone: 'live' },
      steps: [{ label: 'judge-head-tail', pips: PIPS.run2, doing: 'implement · round 2 after a send-back' }],
    },
    // A contract hangs under the conversation that wrote it, as an epic hangs under the one that steers it.
    tide: {
      id: 'contract-to-accept', slug: 'tide-alerts', contract: true, count: 'v2', words: 'contract · proposed 26m ago',
      blocker: 'Read it and accept it', gauge: { done: 0, started: 0, total: 0, tone: 'attention', inner: 'dot' },
    },
    offline: {
      id: 'epic-release-failed', slug: 'offline-tables', count: '6 of 6 proven', words: 'releasing · 9m · step 2 of 3',
      blocker: 'Release step 2 failed: CI is red on the tag', gauge: { done: 6, started: 0, total: 6, tone: 'attention', inner: 'dot' },
    },
    labels: {
      id: 'epic-releasing', slug: 'orbit-labels', count: '4 of 4 proven', words: 'releasing · 4m · pushing tag v0.46.0',
      gauge: { done: 4, started: 0, total: 4, tone: 'engine' },
    },
    parked: {
      id: 'epic-parked', slug: 'notices-usable', count: '0 of 5 proven', words: 'parked · 29h',
      blocker: 'R5 is already met: retry, drop it, or end?', gauge: { done: 0, started: 1, total: 5, tone: 'attention', inner: 'dot' },
    },
    paused: {
      id: 'epic-paused', slug: 'harbour-map', count: '2 of 4 proven', words: 'paused by you · 3h · 5 of 9 tasks done',
      gauge: { done: 5, started: 1, total: 9, tone: '' },
    },
    planning: {
      id: 'epic-planning', slug: 'buoy-feeds', count: '0 of 4 proven', words: 'planning · 3m · no tasks yet',
      gauge: { done: 0, started: 0, total: 0, tone: 'live' },
    },
    holder: { id: 'epic-shipped', slug: 'holder-scopes', ended: true, count: '9:52', words: 'shipped v0.45.0 · all 4 proven', gauge: { done: 4, total: 4, tone: 'ok', inner: 'check' } },
    cache: { id: 'epic-ended-gaps', slug: 'tide-cache', ended: true, count: 'Tue', words: 'superseded by offline-tables · 0 of 3 proven', gauge: { done: 0, started: 1, total: 3, tone: '', inner: 'dash' } },
  };

  // The conversations that steer epics. Every epic belongs to exactly one conversation and hangs under it; one
  // conversation may steer several. An epic started from a shell gets a conversation on the spot.
  const R0 = W.rows;
  const E0 = W.epics;
  W.steerers = {
    rotaplan: { ...R0.rotaplan, epics: [E0.rota] },
    tideplan: { ...R0.tideplan, epics: [E0.tide] },
    offlineplan: { id: 'epic-release-failed', state: 'done', title: 'Planned offline tables', age: '2h', l2: 'Release is running. I will tell you how it goes.', epics: [E0.offline] },
    followplan: { id: 'convo-many-epics', state: 'done', title: 'Follow-ups from the round-5 review', age: '20m', l2: 'Four epics are out. One is parked on a question for you.', epics: [E0.followups, E0.labels, E0.parked, E0.paused] },
    buoyshell: { id: 'epic-planning', state: 'done', title: 'Started from the shell: buoy-feeds', age: '3m', l2: 'Made for the epic you created with orrery epic create.', epics: [E0.planning] },
    holderplan: { id: 'epic-shipped', state: 'done', title: 'Planned holder scopes', age: '9:52', l2: 'Shipped v0.45.0.', epics: [E0.holder] },
    cacheplan: { id: 'epic-ended-gaps', state: 'done', title: 'Planned the tide cache', age: 'Tue', l2: 'Superseded by offline-tables.', epics: [E0.cache] },
  };

  // dirs builds the inbox's directories. A directory holds its conversations, each with the epics and contracts it
  // steers hanging under it, then one line for what has ended.
  W.dirs = (o = {}) => {
    const R = W.rows;
    const S = W.steerers;
    return [
      { name: 'lighthouse', path: '~/code', count: 3, rows: [R.lamp, S.rotaplan] },
      { name: 'tidepool', path: '~/code', count: 6, waiting: 3, rows: [R.beacon, S.tideplan, S.offlineplan, S.buoyshell], ended: { label: '3 ended', open: !!o.endedOpen, rows: [S.holderplan, S.cacheplan, R.stopped] } },
      { name: 'commonframe', path: '~/work', count: 1, waiting: 1, rows: [R.stale] },
      { name: 'almanac', path: '~/code', count: 2, rows: [R.phase, R.almanac] },
      { name: 'orrery', path: '~/code', count: 5, collapsed: !!o.collapsed, rows: [S.followplan, R.shell] },
      { name: 'orchard', path: '~/code', count: 1, rows: [R.orchard] },
      { name: 'kelp', path: '~/code', host: 'brine', count: 1, rows: [R.failed] },
      { name: '~', path: 'home', count: 1, waiting: 1, rows: [R.home] },
    ];
  };
  W.attn = { waiting: 6, fresh: 2 };
  // inbox builds the Inbox's data for a scenario: which row is open, where the keyboard is, what is filtered.
  W.inbox = (o = {}) => ({ dirs: W.dirs(o), attn: W.attn, open: o.open, cur: o.cur === undefined ? o.open : o.cur, ...o });

  // waitingDirs is the inbox narrowed to what waits on you: the same tree, with only those rows, and a conversation
  // whose epic or contract waits kept as that epic's parent.
  W.waitingDirs = () => {
    const R = W.rows;
    const S = W.steerers;
    return [
      { name: 'lighthouse', path: '~/code', rows: [S.rotaplan] },
      { name: 'tidepool', path: '~/code', rows: [R.beacon, S.tideplan, S.offlineplan] },
      { name: 'commonframe', path: '~/work', rows: [R.stale] },
      { name: '~', path: 'home', rows: [R.home] },
    ];
  };
  W.newDirs = () => [
    { name: 'almanac', path: '~/code', rows: [W.rows.phase] },
    { name: 'orchard', path: '~/code', rows: [W.rows.orchard] },
  ];


  // project: everything working in lighthouse, for its page: its conversations, each with what it steers.
  W.project = {
    name: 'lighthouse', path: '~/code/lighthouse', host: 'halyard',
    counts: [['chat', '2 conversations'], ['pools', '1 epic'], ['question', '1 thing waits on you', 'is-attention'], ['clock', 'last change 1m ago']],
    convos: [
      { ...W.rows.lamp, said: 'go test ./internal/telemetry/... · running 0:46. Full jitter is in; now wiring the 5-minute window into Flush.', ctx: W.ctx.low },
      { ...W.rows.rotaplan, said: 'The epic is building. I will tell you when it asks for anything.', ctx: W.ctx.mid, cards: [] },
    ],
    ended: '4 ended conversations; one left a contract set aside',
  };

  W.archivedDirs = () => [
    { name: 'tidepool', path: '~/code', rows: [{ ...W.rows.stopped, archived: true }, { ...W.steerers.cacheplan, archived: true }] },
    { name: 'almanac', path: '~/code', rows: [{ id: 'dock-idle', state: 'done', title: 'Moon phase rounding at the date line', age: '9d', l2: 'Fixed and merged.', archived: true }, { id: 'dock-idle', state: 'done', title: 'Leap-second table refresh', age: '12d', l2: 'The table is current to 2027.', archived: true }] },
  ];

  // ---------- conversations ----------

  const BACKOFF = 'func (b Backoff) Delay(n int) time.Duration {\n    d := b.Base << n\n    if d > b.Max || d <= 0 {\n        return b.Max\n    }\n    return d\n}';

  // lamp: an ordinary working session. It shows your block, a reply with code, lines, a run, a compaction, the new
  // rule, a running call and live text.
  W.lamp = [
    { k: 'you', at: '14:02', text: ['The uploader loses readings whenever the collector is unreachable. Make it retry, and spool to disk when retrying is not enough.'] },
    { k: 'rule', v: 'compaction', detail: 'auto · 611k tokens' },
    { k: 'run', n: 6, names: 'Read, Write ×2, Bash ×2, Edit', dots: ['', '', '', 'f', '', ''] },
    { k: 'reply', at: '14:27', md: ['Backoff is in and the schedule test passes. Delays double from 250 ms and stop growing at 30 s:', { code: BACKOFF, lang: 'go' }, '`Flush` now retries a failed batch in place. **Still open:** there is no jitter, so lamps that drop together retry together, and retries never give up.'] },
    { k: 'you', at: '14:31', from: 'from your phone', text: ['Cap the total retry window at 5 minutes and add jitter.'] },
    { k: 'rule', v: 'new' },
    { k: 'tools', list: [
      { name: 'Edit', arg: 'internal/telemetry/backoff.go', diff: [14, 3], result: '+14 −3' },
      { name: 'Bash', arg: "go test ./internal/telemetry/... -run 'Backoff|Jitter'", note: 'ok lighthouse/internal/telemetry 0.412s', result: 'ok' },
    ] },
    { k: 'reply', live: true, md: ['Full jitter is in: each delay is drawn uniformly between zero and the capped value. Now wiring the 5-minute window into `Flush`, so a batch that outlives it goes to the on-disk spool instead of being dro'] },
    { k: 'tools', list: [{ name: 'Bash', arg: 'go test ./internal/telemetry/...', status: 'running', clock: '0:46' }] },
  ];


  // short: the tail of a quiet conversation, for scenarios whose subject is the dock.
  W.short = [
    { k: 'you', at: '14:02', text: ['Warm the tide table cache for the next 30 days, one port at a time.'] },
    { k: 'run', n: 4, names: 'Read ×2, Bash ×2', dots: ['', '', '', ''] },
    { k: 'reply', at: '14:09', md: ['The cache is warm for all 14 ports through December 17. Each port took between 2 and 9 seconds; the whole run took 71 seconds.', 'The warm-up is now a `just warm` recipe, and the README says when to run it.'] },
  ];
  W.shortTitle = { dir: 'tidepool', here: 'Tide table cache warm-up', strip: W.strip(W.ctx.low) };

  // blocks: every Block a conversation can hold, in one transcript.
  W.blocks = [
    { k: 'rule', v: 'day', detail: 'Monday 16 November' },
    { k: 'you', at: '13:48', text: ['Plan the rota export as an epic. Six requirements; fixtures must cover every port zone.'] },
    { k: 'epicevent', v: 'created', text: 'Created the epic from the accepted contract.', slug: 'rota-export', gauge: W.epics.rota.gauge, go: 'epic-building' },
    { k: 'event', at: '13:58', from: 'from the engine', text: ['The planner outlined 7 tasks for **rota-export**. Two start now; five wait on them.'] },
    { k: 'stepend', at: '14:06', agent: 'csv-writer', end: 'done', summary: 'Wrote the CSV writer and its row-per-shift test. 21 rows for the fixture week.', stats: { rounds: '1 round', time: '8m', files: '2 files', plus: 96, minus: 4 }, pips: ['ok', 'ok', 'ok', 'ok', 'acc'], go: 'worker-ended' },
    { k: 'stepend', at: '14:11', agent: 'shift-store', end: 'checks_failed', summary: 'Stored shifts per keeper and week.', reason: 'The gate failed: `just check` exited 1 in store_test.go:27.', stats: { rounds: '3 rounds', time: '13m', files: '4 files', plus: 141, minus: 12 }, pips: ['ok', 'bad', '', '', ''], go: 'worker-ended' },
    { k: 'stepend', at: '14:12', agent: 'audit-log', end: 'stopped', summary: 'Stopped by you before its first check round.', stats: { time: '1m' }, pips: ['plan', '', '', '', ''], go: 'worker-ended' },
    { k: 'checks', at: '14:14', round: 2, checks: [{ name: 'tests', ok: true }, { name: 'lint', ok: true }, { name: 'owned files', ok: false }], note: 'owned files: the change touches go.mod, which this task does not own.', more: 'Revert go.mod, or ask the planner to widen the task.' },
    { k: 'checks', at: '14:19', round: 3, checks: [{ name: 'tests', ok: true }, { name: 'lint', ok: true }, { name: 'owned files', ok: true }], note: 'All three checks pass.' },
    { k: 'epicevent', v: 'park', text: 'Parked: the release gate needs a decision only you can make.', slug: 'offline-tables', go: 'epic-parked' },
    { k: 'epicevent', v: 'release', text: 'Released v0.45.0. All four requirements are proven.', slug: 'holder-scopes', go: 'epic-shipped' },
    { k: 'epicevent', v: 'end', text: 'Ended: superseded by offline-tables.', slug: 'tide-cache', go: 'epic-ended-gaps' },
    { k: 'epicevent', v: 'question', text: 'tz-fixtures asks: bump the embedded tz database?', slug: 'rota-export', go: 'epic-question' },
    { k: 'ask', state: 'settled', who: 'rota-export · tz-fixtures' },
    { k: 'contract', accepted: '13:50', title: 'A keeper’s week of shifts becomes a file for the harbour office', counts: 'serious care · 6 requirements', go: 'contract-accepted' },
    { k: 'you', at: '14:40', sending: true, text: ['Keep the pin and skip the Lisbon case.'] },
  ];

  // lines: every Line and Rule, and what each opens.
  W.lines = [
    { k: 'you', at: '09:12', text: ['Find out why the roster page is slow for weeks with more than forty shifts.'] },
    { k: 'thinking', open: true, text: 'The page renders one row per shift and each row asks for its keeper. That would be one query per row. Check the handler before guessing.' },
    { k: 'tools', list: [
      { name: 'Read', arg: 'internal/roster/handler.go', result: 'ok' },
      { name: 'Grep', arg: 'keeperFor\\(', note: '14 matches in 3 files', result: 'ok' },
      { name: 'Bash', arg: 'go test ./internal/roster -run Slow -count=1', status: 'failed', open: true, result: '--- FAIL: TestSlowWeek (4.21s)\n    roster_test.go:88: week of 44 shifts took 4.1s, want under 500ms\nFAIL' },
    ] },
    { k: 'run', n: 5, names: 'Read ×3, Edit, Bash', dots: ['', '', '', '', 'f'], open: [
      { name: 'Read', arg: 'internal/roster/query.go', result: 'ok' },
      { name: 'Read', arg: 'internal/roster/keeper.go', result: 'ok' },
      { name: 'Read', arg: 'internal/roster/roster_test.go', result: 'ok' },
      { name: 'Edit', arg: 'internal/roster/query.go', diff: [9, 22], result: 'ok' },
      { name: 'Bash', arg: 'go vet ./...', status: 'failed', result: 'vet: unused variable' },
    ] },
    { k: 'notice', kind: 'task', text: 'Task 3 of 4 is complete: one query per week instead of one per shift.' },
    { k: 'notice', kind: 'teammate', text: 'reviewer joined to read the diff.' },
    { k: 'notice', kind: 'error', error: true, text: 'The API refused the request: overloaded. Retrying in 8 s.' },
    { k: 'task', type: 'agent', agent: 'Explore', desc: 'Find every caller of keeperFor', status: 'done' },
    { k: 'task', type: 'agent', agent: 'reviewer', desc: 'Review the query change', status: 'failed' },
    { k: 'task', type: 'shell', desc: 'pnpm dev --port 5173', status: 'stopped' },
    { k: 'rule', v: 'interrupted', detail: '09:41' },
    { k: 'rule', v: 'compaction', detail: 'manual · 188k tokens', open: true, text: 'The roster page was slow because each shift row loaded its keeper separately. The handler now loads a week in one query. One vet warning is open.' },
    { k: 'fold', v: 'brief', text: ['Implement the week query. Own `internal/roster/query.go` and its test. Do not change `go.mod`.'], open: true },
    { k: 'fold', v: 'engine', text: 'Round 2: the owned-files check failed. Revert go.mod.\nThe other checks passed.' },
    { k: 'rule', v: 'new' },
    { k: 'reply', at: '09:44', md: ['One query per week is in. The slow test now passes in 31 ms.'] },
  ];

  // prose: every Markdown element a reply can hold.
  W.markdown = [
    { k: 'you', at: '11:20', text: ['Write up the grammar gaps you found, with **what breaks** and `where`.', { ul: ['Keep it short.', 'A table is fine.'] }] },
    { k: 'reply', at: '11:26', md: [
      { h: 'Parser tests for nested scion grafting', level: 2 },
      'All 41 cases pass. Two gaps remain; neither blocks the release. *Both* are in `grammar/graft.peg`, and the upstream note is [here](https://example.invalid/graft).',
      { h: 'What breaks', level: 3 },
      { ul: ['A scion nested three deep loses its rootstock label.', 'A comment between `graft` and its target is read as the target.'] },
      { ol: ['Add a depth counter to `Scion`.', 'Skip trivia before the target.', 'Re-run the 41 cases.'] },
      { quote: 'A graft takes only where cambium meets cambium.' },
      { table: { head: ['case', 'depth', 'result'], rows: [['nested-2', '2', 'passes'], ['nested-3', '3', '**label lost**'], ['comment-target', '1', '**wrong target**']] } },
      { code: 'Graft  <- "graft" _ Target _ "onto" _ Rootstock\nTarget <- Scion / Ident', lang: 'peg' },
      { hr: true },
      'Struck text is drawn ~~like this~~ in the client; an image is drawn as its alt text.',
    ] },
  ];

  // An epic moves between conversations only when you move it. Each side keeps one line saying so, and the epic's log
  // keeps one more. tookOver: the lamp conversation after it took rota-export; handedOver: the planning one after.
  W.MOVED_IN = 'Took over rota-export from “Planned the rota export, and steers its epic”. Its questions and its ending come here now.';
  W.MOVED_OUT = 'rota-export moved to “Retry backoff and jitter for the telemetry uploader”, by you. Its questions go there now.';
  W.tookOver = [
    { k: 'you', at: '14:43', text: ['Take over rota-export. The tz question needs someone who knows the telemetry fixtures.'] },
    { k: 'tools', list: [{ name: 'epic_steer', arg: 'rota-export', result: 'ok' }] },
    { k: 'epicevent', v: 'moved', text: W.MOVED_IN, slug: 'rota-export', gauge: W.epics.rota.gauge, go: 'epic-moved' },
    { k: 'reply', at: '14:44', md: ['I have the contract and the epic’s log. tz-fixtures is held on the Lisbon question; the planner recommends keeping the pin, and I agree: R5 holds for every port on today’s rota.'] },
  ];

  // rotaplan: the session that created an epic. Its epic is pinned under the title row; the epic's words arrive here.
  W.rotaplan = [
    { k: 'you', at: '13:48', text: ['Plan the rota export as an epic. Six requirements; fixtures must cover every port zone.'] },
    { k: 'run', n: 3, names: 'Read ×2, epic_create', dots: ['', '', ''] },
    { k: 'contract', accepted: '13:50', title: 'A keeper’s week of shifts becomes a file for the harbour office', counts: 'serious care · 6 requirements', go: 'contract-accepted' },
    { k: 'epicevent', v: 'created', text: 'Created the epic from the accepted contract.', slug: 'rota-export', gauge: W.epics.rota.gauge, go: 'epic-question' },
    { k: 'reply', at: '13:51', md: ['The epic is building. I will tell you when it asks for anything.'] },
    { k: 'stepend', at: '14:06', agent: 'csv-writer', end: 'done', summary: 'Wrote the CSV writer and its row-per-shift test. 21 rows for the fixture week.', stats: { rounds: '1 round', time: '8m', files: '2 files', plus: 96, minus: 4 }, pips: ['ok', 'ok', 'ok', 'ok', 'acc'], go: 'worker-ended' },
    { k: 'rule', v: 'new' },
    { k: 'epicevent', v: 'question', text: 'tz-fixtures asks: bump the embedded tz database?', slug: 'rota-export', go: 'epic-question' },
  ];
  W.rotaPin = { slug: 'rota-export', gauge: W.epics.rota.gauge, words: 'building · 49m · 2 of 7 tasks done', blocker: 'tz-fixtures asks you a question · 2m', go: 'epic-question' };
  W.rotaAsk = {
    variant: 'epic', who: 'rota-export · tz-fixtures', age: '2m',
    q: ['Bump the embedded tz database?', 'The pinned database lacks the 2026 Lisbon rule, so one fixture cannot pass. A bump changes `go.mod`, which is outside this task’s owned files, and the contract says no dependency changes.'],
    chips: ['R5 fixtures cover every port zone', 'won’t do: dependency changes'],
    opts: [
      { name: 'Keep the pin', desc: 'Skip the Lisbon case. The contract holds: R5 is met for every port on today’s rota.', rec: true, why: 'Lisbon is not on any rota until March.' },
      { name: 'Allow the bump', desc: 'A contract change. Adds go.mod to this task and reruns the full gate.' },
    ],
    picked: 0, record: 'D7 14:41 owner: keep the tz pin; skip Lisbon 2026 (R5)',
  };

  // tideplan: a session whose contract waits to be read and accepted.
  W.tideplan = [
    { k: 'you', at: '14:02', text: ['Alert the harbour office when a tide table changes after it was published.'] },
    { k: 'run', n: 7, names: 'Read ×4, Grep ×2, Write', dots: ['', '', '', '', '', '', ''] },
    { k: 'reply', at: '14:15', md: ['The contract is written. It has three ways this could go wrong, one row you did not ask for, and one decision that is yours. Nothing starts until you accept it.'] },
    { k: 'contract', slug: 'tide-alerts', care: 'severe', age: '26m', title: 'The harbour office hears within a minute when a published tide table changes', left: ['1 decision is yours', '1 added row to keep or drop', 'read “can’t be undone” before Accept'], go: 'contract-to-accept' },
  ];

  // beacon: a session that asks a question with options.
  W.beacon = [
    { k: 'you', at: '14:30', text: ['Why is the roster page slow for weeks with more than forty shifts?'] },
    { k: 'run', n: 5, names: 'Read ×3, Grep, Bash', dots: ['', '', '', '', ''] },
    { k: 'reply', at: '14:37', md: ['Each shift row loads its keeper with its own query, so a week of 44 shifts makes 45 queries. There are three ways to fix it, and they differ in how much they change.'] },
  ];
  W.beaconAsk = {
    variant: 'session', who: 'tidepool · Why the roster page is slow past forty shifts', age: '4m',
    q: ['Which fix should I make?'],
    opts: [
      { name: 'One query per week', desc: 'Join keepers in the week query. Smallest change; touches `query.go` only.', rec: true },
      { name: 'Cache keepers for the request', desc: 'Keeps the row code as it is. Adds a per-request map.' },
      { name: 'Paginate the page', desc: 'Twenty shifts a page. Changes what the harbour office sees.' },
    ],
  };
  W.beaconTitle = { dir: 'tidepool', here: 'Why the roster page is slow past forty shifts', strip: W.strip(W.ctx.mid) };

  // work: what a conversation has started: its goal, and a tile on its right rail for each thing.
  W.workRota = { epics: [{
    slug: 'rota-export', gauge: W.epics.rota.gauge, tone: 'attention', state: 'building · 49m', pill: 'live', track: ['done', 'now', '', '', ''],
    now: 'Two tasks are being worked on, and one waits on you.', asks: 'tz-fixtures asks: bump the embedded tz database?', count: '3 of 6 proven · 2 of 7 tasks', go: 'epic-question', steps: W.epics.rota.steps,
  }] };
  W.project.convos[1].cards = W.workRota.epics;
  // workMany: a conversation steering four epics, more than its band shows.
  const card = (e, o) => ({ slug: e.slug, gauge: e.gauge, tone: e.blocker ? 'attention' : o.tone || '', state: o.state, pill: e.blocker ? 'attention' : o.pill || '', track: o.track, now: o.now, asks: e.blocker, act: o.act, count: e.count, go: e.id, steps: e.steps });
  W.workMany = { epics: [
    card(W.epics.followups, { state: 'building · 20m', tone: 'live', pill: 'live', track: ['done', 'now', '', '', ''], now: 'One task is being worked on, round 2 after a send-back.' }),
    card(W.epics.labels, { state: 'releasing · 4m', tone: 'engine', pill: 'engine', track: ['done', 'done', 'done', 'now', ''], now: 'Pushing tag v0.46.0.' }),
    card(W.epics.parked, { state: 'parked · 29h', act: 'Decide', track: ['done', 'now', '', '', ''], now: 'Parked: R5 is already met at base.' }),
    card(W.epics.paused, { state: 'paused by you · 3h', track: ['done', 'now', '', '', ''], now: 'Paused with 5 of 9 tasks done.' }),
  ] };
  W.handedOver = () => W.rotaplan.concat([{ k: 'epicevent', v: 'moved', text: W.MOVED_OUT, slug: 'rota-export', gauge: W.epics.rota.gauge, go: 'convo-took-over' }]);
  W.workContract = { contract: {
    slug: 'tide-alerts', gauge: W.epics.tide.gauge, tone: 'attention', state: 'severe care', pill: 'attention', track: ['now', '', '', '', ''],
    now: 'The harbour office hears within a minute when a published tide table changes.', asks: '1 added row to keep or drop; read “can’t be undone”', act: 'Read and decide', count: 'written 14:15 · waiting 26m', go: 'contract-to-accept', record: 'proposed v2 · waits for you',
  } };
  W.agents = [{ type: 'agent', agent: 'Explore', desc: 'Find every caller of keeperFor', activity: 'Grep keeperFor\\(', clock: '2:11' }, { type: 'agent', agent: 'reviewer', desc: 'Review the query change', activity: 'Read query.go', clock: '0:48' }];
  W.shells = [{ type: 'shell', desc: 'pnpm dev --port 5173', activity: 'ready on :5173', clock: '41m' }, { type: 'shell', desc: 'sleep 1800 && just warm', clock: '12m' }];
  W.workFull = { goal: 'Every port’s tide table is cached through December 17 and the warm-up is one command.', epics: W.workRota.epics, agents: W.agents, background: W.shells.slice(0, 1) };

  // notices: what happened elsewhere. Each says what it is about, in two lines, and when.
  W.notices = [
    { tone: 'attention', state: 'waiting', dir: 'tidepool', title: 'asks you a question', body: 'Why the roster page is slow past forty shifts: which fix should I make?', when: 'now', act: 'Answer', unread: true },
    { tone: 'attention', gauge: W.epics.rota.gauge, title: 'rota-export asks you a question', body: 'tz-fixtures: bump the embedded tz database?', when: '2m', act: 'Answer', unread: true },
    { tone: 'ok', gauge: W.epics.holder.gauge, title: 'holder-scopes shipped v0.45.0', body: 'All four requirements proven. No gaps.', when: '09:52' },
  ];
  W.noticeDays = [
    ['Today', [...W.notices,
      { tone: 'bad', gauge: W.epics.offline.gauge, title: 'offline-tables: release step 2 failed', body: 'CI is red on the tag. The step has no undo.', when: '09:31' },
      { tone: 'live', icon: 'clock', title: 'Weekly usage limit reached', body: 'Held: rota-export, orrery-followups. Resets 15:10.', when: '08:47' }]],
    ['Yesterday', [{ tone: '', state: 'done', dir: 'orchard', title: 'finished', body: 'Parser tests for nested scion grafting: all 41 cases pass.', when: '18:20' }, { tone: 'bad', state: 'failed', dir: 'kelp', title: 'failed', body: 'Kelp bed survey importer: the API refused the request.', when: '16:02' }]],
  ];

  W.lampDock = { state: 'working', tool: 'Bash', what: 'go test ./internal/telemetry/...', clock: '0:46', name: 'lighthouse' };
  W.lampTitle = { dir: 'lighthouse', here: 'Retry backoff and jitter for the telemetry uploader', strip: W.strip(W.ctx.low) };
})();
