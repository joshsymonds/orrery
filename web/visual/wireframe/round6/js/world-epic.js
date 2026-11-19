// Round 6 wireframe: the epics of the synthetic world. rota-export is the one drawn in most states; the others
// each show one state an epic can rest in.
(() => {
  'use strict';
  const { W } = O;
  const P = W.PIPS;

  const SUMMARY = 'A keeper’s week of shifts becomes a file for the harbour office. Shifts are stored per keeper and week, one endpoint serves a week, and rota export writes it as CSV, one row per shift. Export refuses to overwrite a file and writes every run to the audit log. Fixtures cover every port zone. No dependency changes.';

  const TASKS = [
    { name: 'shift-store', req: 'R1', place: 'integrated', tone: 'ok', time: '11m' },
    { name: 'rota-endpoint', req: 'R2', place: 'integrated', tone: 'ok', time: '9m' },
    { name: 'csv-writer', req: 'R3', place: 'integrate', tone: 'acc', words: 'accepted · waits to integrate', time: '2m' },
    { name: 'export-cli', req: 'R4', place: 'implement', tone: 'run', words: 'implement · gate just check', time: '2:41' },
    { name: 'tz-fixtures', req: 'R5', place: 'implement', tone: 'mine', words: 'implement · held on your answer', time: '2m' },
    { name: 'audit-log', req: 'R6', place: 'planned', tone: 'plan' },
    { name: 'readme', req: 'R6', place: 'planned', tone: 'plan' },
  ];
  const REQS = [
    { id: 'R1', text: 'Shifts are stored per keeper and week.', red: 'holds', green: 'passes', proven: true },
    { id: 'R2', text: 'The rota endpoint serves one week of shifts.', red: 'holds', green: 'passes', proven: true },
    { id: 'R3', text: 'A week exports as CSV, one row per shift.', red: 'holds', green: 'passes', proven: true },
    { id: 'R4', text: 'Export refuses to overwrite an existing file.', red: 'holds', green: 'running' },
    { id: 'R5', text: 'Fixtures cover every port zone.', red: 'holds', green: 'none' },
    { id: 'R6', text: 'Every export is written to the audit log.', red: 'review', green: 'none' },
  ];
  const HISTORY = [
    { at: '14:39', text: '**tz-fixtures** asked you a question' },
    { at: '14:38', text: '**export-cli** · checks passed, the gate is running' },
    { at: '14:31', text: '**csv-writer** · done · accepted by the judge' },
  ];
  const track = (build, o = {}) => [
    { name: 'Contract', state: 'done', words: 'accepted 13:50' },
    { name: 'Build', state: 'ahead', ...build },
    { name: 'Review', state: 'ahead', ...(o.review || {}) },
    { name: 'Release', state: 'ahead', ...(o.release || {}) },
    { name: 'Report', state: 'ahead', ...(o.report || {}) },
  ];
  const DONE = { state: 'done' };
  const allDone = TASKS.map((t) => ({ ...t, place: 'integrated', tone: 'ok', words: undefined }));
  const allProven = REQS.map((r) => ({ ...r, red: r.red === 'review' ? 'review' : 'holds', green: r.red === 'review' ? 'review' : 'passes', proven: true }));

  const BASE = {
    dir: 'lighthouse', slug: 'rota-export', summary: SUMMARY,
    contract: 'accepted 13:50 · serious care · 6 requirements', conversation: 'lighthouse · Planned the rota export, and steers its epic',
    controls: { pause: true, stop: true }, asOf: 'as of 14:41',
  };

  W.epic = {};

  W.epic.building = {
    ...BASE, state: 'building · 49m', tone: 'live',
    track: track({ state: 'now', words: '2 of 7 tasks done' }),
    now: 'Three tasks are being worked on.',
    running: [
      { label: 'export-cli', pips: P.run3, doing: 'implement · gate just check', time: '2:41' },
      { label: 'tz-fixtures', pips: ['run', '', '', '', ''], doing: 'red · capturing the failing test', time: '6m' },
      { label: 'csv-writer', pips: P.acc, doing: 'accepted · waits to integrate', time: '2m' },
    ],
    next: 'audit-log and readme start when tz-fixtures is done.',
    history: HISTORY.slice(1), historyMore: 9,
    tasks: TASKS.map((t) => (t.name === 'tz-fixtures' ? { ...t, tone: 'run', words: 'red · capturing the failing test', place: 'red', time: '6m' } : t)), reqs: REQS, hl: 'export-cli',
  };

  W.epic.question = {
    ...BASE, state: 'building · 49m', tone: 'live', blocker: 'tz-fixtures asks you a question · 2m',
    track: track({ state: 'now', tone: 'attention', words: '2 of 7 tasks done · 1 waits on you' }),
    now: 'Two tasks are being worked on, and one waits on you.',
    ask: W.rotaAsk,
    running: [
      { label: 'export-cli', pips: P.run3, doing: 'implement · gate just check', time: '2:41' },
      { label: 'tz-fixtures', held: true, pips: P.held2, doing: 'held · waits on your answer', time: '2m' },
    ],
    next: 'audit-log and readme start when tz-fixtures is done.',
    history: HISTORY, historyMore: 9, tasks: TASKS, reqs: REQS, hl: 'tz-fixtures',
  };

  W.epic.parked = {
    ...BASE, dir: 'orrery', slug: 'notices-usable', state: 'parked · 29h', tone: 'attention', blocker: 'asks you what to do',
    summary: 'Notices name a directory and what its session is doing, never an id; they stack three deep and count the rest; a click goes to the thing the notice is about.',
    contract: 'accepted Sun 09:12 · limited care · 5 requirements', conversation: 'orrery · Follow-ups from the round-5 review', conversationGo: 'convo-many-epics',
    controls: { stop: true },
    track: track({ state: 'now', tone: 'attention', words: 'parked before any task' }),
    now: 'Parked. Nothing runs until you answer.',
    ask: {
      variant: 'epic', who: 'notices-usable · the engine', age: '29h',
      q: ['Requirement R5 is already met before any change.', 'Its red check ran at the epic’s base and passed, so there is nothing to prove red. Run the check again with a fresh judge, drop R5 as already met, or end the epic.'],
      opts: [
        { name: 'Retry', desc: 'Run R5’s red check again with a fresh judge.', rec: true, why: 'The first judge may have read stale output.' },
        { name: 'Drop R5', desc: 'Record R5 as already met, as your decision, and go on without its red.' },
        { name: 'End the epic', desc: 'Stop here. The epic ends as “already met”.' },
      ],
      picked: 0, record: 'D2 14:41 owner: retry the red check of R5',
    },
    history: [{ at: 'Sun', text: 'The engine parked the epic: R5 met at the epic’s base' }, { at: 'Sun', text: 'red run of R5 · passed, judged **holds**' }],
    tasks: [], reqs: REQS.slice(0, 5).map((r, i) => ({ ...r, red: i === 4 ? 'met' : 'none', green: 'none', proven: false })),
  };

  W.epic.plannerParked = {
    ...W.epic.building, state: 'building · 52m', blocker: undefined,
    now: 'A check failed and the planner is deciding what to do. Nothing is asked of you.',
    nowExtra: O.inote('The planner holds this park: workflow node gate failed. It will retry, replan, or ask you.'),
    history: [{ at: '14:44', text: 'The planner took the park: gate failed on **export-cli**', tail: 'just check: exit 1\nexport_test.go:88: want ErrExists, got nil' }, ...HISTORY.slice(1)],
  };

  W.epic.limited = {
    ...W.epic.building, state: 'building · 1h 12m', tone: '', blocker: undefined,
    track: track({ state: 'now', tone: 'quiet', words: '2 of 7 tasks done · waiting on a limit' }),
    now: 'Two steps wait on the weekly usage limit, which resets at 15:10. They resume on their own a minute after.',
    nowExtra: `<div class="doors">${O.btn('Resume now', { icon: 'play', tip: 'Try before the reset. A step still over the limit stays held.', tipSide: 'right' })}</div>`,
    running: [
      { label: 'export-cli', limited: true, pips: P.limit3, doing: 'waits on the weekly limit · resets 15:10', time: '23m' },
      { label: 'tz-fixtures', limited: true, pips: P.limit2, doing: 'waits on the weekly limit · resets 15:10', time: '23m' },
    ],
    tasks: TASKS.map((t) => (t.name === 'export-cli' || t.name === 'tz-fixtures' ? { ...t, tone: 'plan', words: 'waits on the weekly limit', time: '23m' } : t)), hl: undefined,
  };

  W.epic.paused = {
    ...W.epic.building, state: 'paused · 3h', tone: '', controls: { resume: true, stop: true },
    track: track({ state: 'now', tone: 'quiet', words: '2 of 7 tasks done · paused' }),
    now: 'Paused. The steps that were running finished; nothing new starts until you resume.', running: [], next: undefined,
  };

  W.epic.planning = {
    ...BASE, dir: 'tidepool', slug: 'buoy-feeds', state: 'planning · 3m', tone: 'live',
    summary: 'Each buoy’s feed is read once a minute, gaps over five minutes are flagged, and the harbour page shows the newest reading with its age.',
    contract: 'accepted 14:38 · limited care · 4 requirements', conversation: 'tidepool · Started from the shell: buoy-feeds',
    track: track({ state: 'now', words: 'the planner is outlining tasks' }),
    now: 'The planner is reading the contract and outlining tasks. No task has started.',
    history: [{ at: '14:38', text: 'planner turn 1 · running' }, { at: '14:38', text: 'The epic was created from the accepted contract' }],
    tasks: [], reqs: REQS.slice(0, 4).map((r) => ({ ...r, red: 'none', green: 'none', proven: false })),
  };

  W.epic.reviewing = {
    ...BASE, state: 'reviewing · 6m', tone: 'live', controls: { pause: true, stop: true },
    track: track({ ...DONE, words: '7 of 7 tasks done' }, { review: { state: 'now', words: '2 of 3 reviewers done' } }),
    now: 'Three reviewers are reading the whole change against the contract.',
    tasks: allDone, reqs: allProven,
    review: { words: '2 of 3 reviewers done · 1 finding', body: O.table('auto minmax(0, 1fr) auto', ['reviewer', 'what they found', ''], [
      { cells: ['<span class="strong">contract</span>', '<span class="wide">Every requirement has a red and a green run at the stored base.</span>', `<span class="end">${O.chip('holds', 'ok', 'check')}</span>`] },
      { cells: ['<span class="strong">security</span>', '<span class="wide">Export writes outside the rota directory when the name holds “../”. <span class="ink-3">export.go:41</span></span>', `<span class="end">${O.chip('fails', 'bad', 'cross')}</span>`], detail: `<div class="run-line"><span>F1 · confirmed · a fix plan will close it</span>${O.chip('open', 'attention')}</div>` },
      { cells: ['<span class="strong">tests</span>', '<span class="wide ink-3">reading…</span>', `<span class="end">${O.chip('running', 'live')}</span>`] },
    ], { touch: 'minmax(0, 1fr) auto' }) },
    history: [{ at: '14:52', text: 'security reviewer · **fails** · 1 finding' }, { at: '14:50', text: 'contract reviewer · **holds**' }, { at: '14:47', text: 'The review froze the change' }], historyMore: 31,
  };

  const RELEASE_STEPS = [
    { step: 'Merge the epic’s candidate into main', target: 'origin/main', undo: 'revert the merge', state: 'passed', evidence: 'main is at the candidate; CI started.' },
    { step: 'Tag v0.46.0 and push the tag', target: 'origin', undo: 'none', state: 'running' },
    { step: 'Publish the release notes', target: 'the releases page', undo: 'delete the draft', state: 'waiting' },
  ];
  W.epic.releasing = {
    ...BASE, dir: 'orrery', slug: 'orbit-labels', state: 'releasing · 4m', tone: 'engine', controls: { stop: true },
    summary: 'The orrery names the wedge under the pointer in a caption, and draws no labels on the chart.',
    contract: 'accepted 12:02 · limited care · 4 requirements', conversation: 'orrery · Follow-ups from the round-5 review', conversationGo: 'convo-many-epics',
    track: track({ ...DONE, words: '4 of 4 tasks done' }, { review: { ...DONE, words: 'clean' }, release: { state: 'now', tone: 'ok', words: 'step 2 of 3' } }),
    now: 'Releasing: step 2 of 3 is running.',
    release: { words: 'step 2 of 3', steps: RELEASE_STEPS, then: 'the judge reads the tag and the notes, and the epic ends released.' },
    tasks: allDone.slice(0, 4), reqs: allProven.slice(0, 4),
    history: [{ at: '14:37', text: 'release step 1 · passed' }, { at: '14:36', text: 'The epic took the release slot for orrery' }], historyMore: 44,
  };

  W.epic.releaseFailed = {
    ...W.epic.releasing, dir: 'tidepool', slug: 'offline-tables', state: 'release stopped · 9m', tone: 'attention', blocker: 'step 2 failed and cannot be undone',
    summary: 'Tide tables are readable with no network: the last seven days are kept on the device and marked with their age.',
    contract: 'accepted 11:20 · serious care · 6 requirements', conversation: 'tidepool · Planned offline tables',
    track: track({ ...DONE, words: '6 of 6 tasks done' }, { review: { ...DONE, words: 'clean' }, release: { state: 'now', tone: 'attention', words: 'stopped at step 2' } }),
    now: 'The release stopped at step 2, and that step has no undo. It waits on you.',
    release: { words: 'stopped at step 2 of 3', steps: [
      RELEASE_STEPS[0],
      { ...RELEASE_STEPS[1], step: 'Tag v0.45.1 and push the tag', state: 'failed', reason: 'CI is red on the tag: lint failed in tables/offline.go:212.',
        cmds: `${O.btn('Release from step 2', { variant: 'primary', tip: 'Your word that step 2 is done or safe to run again', tipSide: 'right' })}${O.btn('Stop…', { variant: 'danger' })}` },
      RELEASE_STEPS[2],
    ] },
    tasks: allDone.slice(0, 6), reqs: allProven,
    history: [{ at: '14:32', text: 'release step 2 · **failed** · CI is red on the tag' }, { at: '14:30', text: 'release step 1 · passed' }], historyMore: 52,
  };

  W.epic.slot = {
    ...W.epic.releasing, dir: 'orrery', slug: 'orrery-followups', state: 'ready to release · 2m', tone: '', controls: { stop: true },
    track: track({ ...DONE, words: '3 of 3 tasks done' }, { review: { ...DONE, words: 'clean' }, release: { state: 'now', tone: 'quiet', words: 'waiting its turn' } }),
    now: 'Ready to release. It waits its turn: **orbit-labels** is releasing in this repository.',
    release: { words: 'waiting for the release slot', steps: RELEASE_STEPS.map((s) => ({ ...s, state: 'waiting', evidence: undefined })) },
    tasks: allDone.slice(0, 3), reqs: allProven.slice(0, 3), history: [{ at: '14:39', text: 'The review ended **clean**' }], historyMore: 27,
  };

  W.epic.shipped = {
    ...BASE, dir: 'tidepool', slug: 'holder-scopes', state: 'released · 09:52', tone: 'ok', controls: {},
    summary: 'A holder’s scope names the ports it may read, and every read outside it is refused and logged.',
    contract: 'accepted 08:04 · serious care · 4 requirements', conversation: 'tidepool · Planned holder scopes',
    track: track({ ...DONE, words: '5 of 5 tasks done' }, { review: { ...DONE, words: 'clean' }, release: { ...DONE, words: '3 of 3 steps' }, report: { state: 'now', tone: 'ok', words: 'released v0.45.0' } }),
    report: { outcome: 'Released v0.45.0. No gaps.', why: 'All four requirements were proven red at the base and green on the change. The review found one finding, which a fix plan closed in one round. The release ran its three steps and the judge passed it after 1 h 48 min.', shipped: 'R1, R2, R3, R4' },
    release: { words: '3 of 3 steps passed', steps: RELEASE_STEPS.map((s) => ({ ...s, state: 'passed', evidence: undefined })) },
    tasks: allDone.slice(0, 5), reqs: allProven.slice(0, 4), history: [{ at: '09:52', text: 'Released. No gaps.' }], historyMore: 61, scrollTo: '[aria-label="Report"]',
  };

  W.epic.gaps = {
    ...BASE, dir: 'tidepool', slug: 'tide-cache', state: 'ended with gaps · Tue', tone: 'attention', blocker: 'release it anyway, or close it', controls: {},
    summary: 'Tide tables are cached per port and day, and a stale table is never served past midnight.',
    contract: 'accepted Tue 10:11 · limited care · 3 requirements', conversation: 'tidepool · Planned the tide cache',
    track: track({ ...DONE, words: '4 of 4 tasks done' }, { review: { ...DONE, words: '1 gap' }, report: { state: 'now', tone: 'attention', words: 'ended with 1 gap' } }),
    report: { outcome: 'Ended with 1 gap. It did not release.', why: 'R3 (a stale table is never served past midnight) was green on the change, but the review confirmed a finding the fix plans did not close in three rounds: the cache serves yesterday’s table for up to a minute after midnight.',
      shipped: 'none', notShipped: 'R1, R2, R3', cmds: `<div class="doors">${O.btn('Release it anyway', { tip: 'Your word that the gap is acceptable', tipSide: 'right' })}${O.btn('Close as superseded…')}${O.btn('Close as abandoned…', { variant: 'quiet' })}</div>` },
    tasks: allDone.slice(0, 4), reqs: allProven.slice(0, 3).map((r, i) => (i === 2 ? { ...r, proven: false } : r)),
    history: [{ at: 'Tue', text: 'Ended with 1 gap' }], historyMore: 38, scrollTo: '[aria-label="Report"]',
  };

  // dense: an epic with 120 tasks. The chart still reads; the table shows twelve and folds the rest.
  const many = [];
  for (let i = 0; i < 120; i += 1) {
    const done = i < 96;
    const place = done ? 'integrated' : i < 99 ? 'review' : i < 104 ? 'green' : i < 107 ? 'implement' : i < 110 ? 'red' : 'planned';
    many.push({ name: `unit-${String(i + 1).padStart(3, '0')}`, req: `R${(i % 6) + 1}`, place, tone: done ? 'ok' : i === 101 ? 'bad' : i === 106 ? 'mine' : place === 'planned' ? 'plan' : 'run', time: done ? '' : `${(i % 9) + 1}m` });
  }
  W.epic.dense = {
    ...W.epic.building, dir: 'commonframe', slug: 'deck-pipeline', state: 'building · 6h 12m',
    summary: 'Every deck in the catalogue is rebuilt from its source, checked against its recorded playthrough, and published with a manifest. 120 decks, six requirements.',
    contract: 'accepted 08:29 · serious care · 6 requirements', conversation: 'commonframe · Planned the deck pipeline',
    track: track({ state: 'now', words: '96 of 120 tasks done' }).map((st, i) => (i === 0 ? { ...st, words: 'accepted 08:29' } : st)), now: 'Fourteen tasks are being worked on.',
    running: W.epic.building.running.slice(0, 2), next: '10 more start as these finish.',
    tasks: many.slice(96).concat(many.slice(0, 96)).sort((a, b) => a.name.localeCompare(b.name)), taskCap: 12, hl: 'unit-102',
    reqs: REQS.map((r, i) => ({ ...r, red: 'holds', green: i < 4 ? 'passes' : 'running', proven: i < 4 })),
  };

  W.epic.evidence = {
    ...W.epic.building,
    reqs: REQS.map((r) => (r.id === 'R4' ? { ...r, open: true, runs: [
      { phase: 'red', verdict: 'holds', state: 'ended', passed: false, exit: 1, source: 'captured', commit: 'a1b2c3d', log: 'open', text: '--- FAIL: TestExportRefusesOverwrite (0.01s)\n    export_test.go:88: want ErrExists, got nil\nFAIL', stderr: 'exit status 1' },
      { phase: 'green', state: 'judging', source: 'captured', commit: 'e4f5a6b', artifacts: [{ path: 'shots/export-refused.png', state: 'kept', bytes: '48 kB', image: 'export-refused.png, drawn here' }, { path: 'trace.zip', state: 'over_cap' }] },
    ] } : r)),
    scrollTo: '[aria-label="Requirements"]',
  };

  W.epic.all = W.epic;
  W.epicTasks = TASKS;
  W.epicReqs = REQS;
})();
