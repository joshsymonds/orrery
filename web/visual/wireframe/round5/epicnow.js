// orrery mockups: the epic view, the phase output and the frame every page shares. One page per epic, top to bottom in
// the epic's own order: the summary paragraph (the door to the contract), what it is doing now and what blocks it, red
// proof, the orrery, green proof, release proof, the report. A slice of the orrery opens that phase's output; the worker's
// session is one step further. The web view and the phone both draw from this file. Synthetic data. Nothing leaves the page.
// Round 5 adds the web's title row (directory, summary, the pane's own meters) and the cards a conversation can show.
(() => {
  'use strict';
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

  // ---------- what the design needs that orrery does not send today ----------
  // key: [fields, what they are for, where they are today, the short name drawn on the mockup]
  const NEEDS = {
    para: ['Epic.summary', 'The paragraph at the top of the epic view, generated once from the accepted contract.',
      'Nothing generates it. The contract’s intent and requirements are in the store.'],
    since: ['Epic.phase_started', 'How long the epic has been in its state: “building · 49 min” in the title row and on the list line.',
      'Epic.changed is the last write, not the start of the phase.'],
    ask: ['EpicsGetResponse.questions', 'The form at the top of the epic view: the open question, its options, the recommendation, and when it was raised.',
      'epic_answer exists as a session tool. The open questions are in the store and not on EpicsGetResponse.'],
    contract: ['EpicsGetResponse.contract, accepted_at', 'The contract page after acceptance.',
      'The store has every contract row. No call returns them to the web view or the phone.', 'EpicsGetResponse.contract'],
    proof: ['EpicRequirement.red, green', 'Red and green proof: per requirement, the test and its output at the base commit and at the task’s commit.',
      'EpicSlice carries red_test and tests_pass as booleans per task. The evidence text stays in the step’s result.'],
    stage: ['EpicStep.stage, stage_started', 'Which part of its loop a running step is in (turn, checks, gate, judge) and since when: which slice glows, and for how long.',
      'The store keeps a running gate (StepGate) and the loop knows its round. The judge call is in memory only. None of it reaches EpicStep.', 'EpicStep.stage'],
    gate: ['EpicStep.gate_name, gate_limit', 'Which gate is running and its limit: “2:41 of 30 min”.',
      'StartStep takes gate_name and the engine resolves the limit. Neither is on EpicStep.', 'EpicStep.gate_name'],
    gateout: ['StepGate.output', 'The gate’s last lines, in the checking phase.', 'The store keeps the gate run. Its output is not sent.'],
    verdict: ['EpicStep.verdicts', 'The done-judge\'s verdict and reason for each round.',
      'The store keeps every StepVerdict. EpicStep.reason carries only the reason a step ended.'],
    detail: ['EpicStepCheck.detail', 'The failing check\'s own line, such as the gate\'s last output.',
      'The engine\'s StepCheck has detail. serve drops it.'],
    readonly: ['EpicStep.read_only', 'Telling a reviewer\'s read-only step from an implementer\'s.',
      'On the engine\'s Step. serve reads it to build the rings and does not send it.'],
    role: ['EpicStep.role', 'Naming a step: implementer, task reviewer, scout, verifier. The reviewing phase and its session need it.',
      'Only the session that started the step knows it. StartStep has no role, and an agent name such as sonnet-high-ro only hints.'],
    req: ['EpicTask.requirement', 'The requirement a task proves: the orrery\'s outer ring, and where a proof card leads.',
      'In the record\'s effort state.json. EpicTask carries the subject and not the requirement.'],
    summary: ['EpicStep.summary', 'What an accepted step says it did.',
      'In the engine Step\'s result, which serve parses for the rings and does not send.'],
    limit: ['EpicStep.limited, limit_kind, limit_resets', 'A step paused by a usage limit, when the limit resets, and its Resume button.',
      'On the engine\'s Step. Not on EpicStep.', 'EpicStep.limited'],
    next: ['Epic.next_actions', 'What starts next and what it waits for: Next, under the orrery.',
      'In the record\'s state.json. Not on Epic.'],
    finding: ['EpicsGetResponse.findings', 'Review findings on a task and on the epic, with what happened to each.',
      'In the record\'s review ledger. EpicsGetResponse has no field for it.'],
    history: ['Epics.History (a new call)', 'Just happened, under the orrery, and the rest of it opened in place: one time-ordered list per epic.',
      'The store has every row: planner turns and their triggers, steps, gates, evidence runs, questions, decisions, notices. No call returns them together for one epic.', 'epic history API'],
    release: ['Epic.release_steps', 'Release proof: the steps orrery runs by itself, each with its state, its evidence and its undo.',
      'The contract’s release steps are in the store with their target and undo. Epic carries an outcome and no steps.', 'Epic.release_steps'],
    report: ['Epic.report', 'The Report section: what shipped, what did not, and every decision.', 'Designed in O3e4. Epic carries an outcome and no report.'],
    talk: ['Epic.sessions, Sessions.Start{cwd, epic}', 'Talk: the sessions that called a tool on this epic, and a new session in its repository with the epic in context.',
      'Tool calls are logged per session, and nothing lists them by epic. A session starts in a folder with no epic in context.', 'Epic.sessions'],
    cmd: ['Epics.Pause, Resume, Stop, Retry, Answer', 'The command buttons and the answer form.',
      'They exist as session tools (epic_pause, epic_resume, epic_stop, epic_retry, epic_answer). The phone-facing service has only Steps.Hold, Nudge and Stop.', 'Epics.Pause …'],
    resume: ['Sessions.Resume', 'The Resume button on an interrupted session.', 'An interrupted session resumes when it gets a message.'],
  };
  NEEDS.title = ['Pane.summary, Session.summary: generated', 'A session’s title: its directory, then one line saying what it has been doing, rewritten as it works.',
    'Pane.summary carries the pane’s @orrery_summary option, and nothing writes it from the conversation. Epics already get a summary from Haiku (enginehost/epicsummary.go).', 'Pane.summary'];
  NEEDS.widget = ['ConversationItem.widget{kind, epic}', 'A card in a conversation: a contract to read and accept, an epic that follows its state, a decision to answer.',
    'A conversation item is text, a tool call, a question or a compaction. None names an epic.', 'ConversationItem.widget'];
  NEEDS.origin = ['EpicSession.created', 'The link from an epic back to the conversation that created it.',
    'Epic.sessions lists each session with its newest epic call only, so the one that called epic_create is lost once it calls anything else.'];
  NEEDS.hoststat = ['Host.Status per host', 'The meters in a page’s title row are for the host that page’s session or epic runs on.',
    'Host.Status answers for the host that serves the page. A session on another host has no meters.', 'Host.Status{host}'];
  const short = (k) => NEEDS[k][3] || NEEDS[k][0];
  // One tag per field. They are wireframe annotation, shown only when the bar's switch is on.
  const need = (keys) => (keys || []).map((k) => `<span class="wf-need" title="${NEEDS[k][2]}"><i>needs data</i>${short(k)}</span>`).join('');

  // ---------- icons: one small drawing per kind of thing ----------
  const IC = {
    you: 'M8 8.2a2.7 2.7 0 1 0 0-5.4 2.7 2.7 0 0 0 0 5.4zM3 13.6c.6-2.6 2.6-3.8 5-3.8s4.4 1.2 5 3.8',
    planner: 'M8 1.8l6.2 6.2L8 14.2 1.8 8zM8 5.4L10.6 8 8 10.6 5.4 8z',
    step: 'M5 3.2v9.6L12.6 8z',
    ret: 'M2.8 9.5v3.7h10.4V9.5M8 2.5v7M5 6.6l3 3 3-3',
    checks: 'M2.5 4.6l1.5 1.5 2.6-2.8M9 4.6h4.5M2.5 10.8l1.5 1.5 2.6-2.8M9 10.8h4.5',
    gate: 'M3 13.5V6.5a5 5 0 0 1 10 0v7M6.3 13.5V5M9.7 13.5V5M3 9.5h10',
    evidence: 'M8 2.5a5.5 5.5 0 1 0 0 11 5.5 5.5 0 1 0 0-11zM8 2.5v11',
    judge: 'M8 2.5v11M5 13.5h6M3 4.5h10M3 4.5L1.5 9h3zM13 4.5L11.5 9h3z',
    review: 'M1.5 8S4 3.6 8 3.6 14.5 8 14.5 8 12 12.4 8 12.4 1.5 8 1.5 8zM8 9.8a1.8 1.8 0 1 0 0-3.6 1.8 1.8 0 0 0 0 3.6z',
    integrate: 'M8 2.5v8M4.6 7.2L8 10.6l3.4-3.4M3 13.5h10',
    decision: 'M8 1.8l6.2 6.2L8 14.2 1.8 8z',
    question: 'M5.6 6a2.4 2.4 0 1 1 3.6 2.1c-.8.5-1.2 1-1.2 1.9M8 12.6v.4',
    notice: 'M8 2.8v6.4M8 12.2v.6',
    hold: 'M5.6 3.2v9.6M10.4 3.2v9.6',
    limit: 'M4 2.5h8M4 13.5h8M4.8 2.5c0 3 3.2 3.4 3.2 5.5s-3.2 2.5-3.2 5.5M11.2 2.5c0 3-3.2 3.4-3.2 5.5s3.2 2.5 3.2 5.5',
    release: 'M8 10.8v-8M4.6 6L8 2.6 11.4 6M3 13.5h10',
    end: 'M4 14V2.5M4 3h8.5l-2 2.8 2 2.8H4',
    contract: 'M4 1.8h5.6l2.9 2.9v9.5H4zM9.4 1.8v3.1h3.1M6 8h4.5M6 10.6h4.5',
    interrupted: 'M13 8a5 5 0 1 1-1.6-3.7M13.2 2.4v3.2H10',
    talk: 'M2.5 3.2h11v7.6H7.6l-3 2.7v-2.7H2.5z',
  };
  const icon = (name) => `<svg class="ic${name === 'decision' ? ' solid' : ''}" viewBox="0 0 16 16" aria-hidden="true"><path d="${IC[name] || IC.notice}"/></svg>`;
  // What each kind of history row is, in words, for its card.
  const EV_KIND = {
    you: 'Your action', planner: 'Planner turn', step: 'Step started', ret: 'Step returned', checks: 'Engine checks', gate: 'Gate',
    evidence: 'Evidence run', judge: 'Done-judge verdict', review: 'Review', integrate: 'Integration', decision: 'Decision', question: 'Question',
    notice: 'Notice', hold: 'Hold', limit: 'Usage limit', release: 'Release step', end: 'Epic ended', contract: 'Contract',
  };

  // ---------- data ----------
  const UNMET = 'The invalid-UTF-8 test passes before and after the fix, so the new cut is unproven on that path.';
  // A history row: time, kind, task, what happened and why, more for its card, flags.
  const E = (t, k, task, text, more = '', x = {}) => ({ t, k, task, text, more, ...x });

  const J = { slug: 'judge-head-tail', tier: 'haiku-medium', req: 'R1', pane: 'generic',
    subject: 'The done-judge sees a long Task or Return section’s head and tail, with a line naming the bytes left out.' };
  const P = { slug: 'planner-reset-unknown', tier: 'haiku-medium', req: 'R2', pane: 'generic',
    subject: 'A planner turn freed by a limit with no reset says “reset time unknown”.' };
  const D = { slug: 'docs-judge-reset', tier: 'haiku-medium', req: 'R3', pane: 'generic',
    subject: 'The docs state the new judge input and the new limit text.' };
  const D0 = { ...D, tier: '', state: 'none', live: false, phrase: 'not started', line: 'not started · waits for the two tasks above to integrate' };
  const FIRST = 'First turn. Nothing has returned yet.';
  const SCOUT = { k: 'scout', tier: 'sonnet-high-ro', e: 'done', cls: 'ok', d: 'confirmed stepjudge.go:29 keeps only a section’s first 8 KiB' };
  const REVIEW_P = { k: 'review planner-reset-unknown', tier: 'sonnet-high-ro', e: 'done', cls: 'ok', d: '0 findings, 2 observations' };
  const J_GAP = { ...J, state: 'bad', live: true, stage: 'Recorded as a gap', round: '2 rounds', when: '27 min', whenLabel: 'ended 10:48', phrase: 'recorded as a gap',
    doing: 'You kept the goal_unmet result as gap G1 at 10:55, so no second try started.',
    line: 'ended goal_unmet · gap G1', track: 'dddb--', times: { 0: '2 rounds', 3: 'unmet ×2' },
    sentence: 'Both rounds passed every check and the gate, and the judge was not satisfied either time. The commit <code>166091a</code> stays on the task’s branch and is not merged.',
    reason: { cap: 'Done-judge · 10:48 · unmet', text: `${UNMET} The 16 KiB case is not reported. The return has a duplicate <code>green_evidence</code> key.` },
    next: 'Nothing more runs for this task.', n: { reason: ['verdict'] } };
  const P_DONE = { ...P, state: 'ok', live: false, fresh: true, stage: 'Done', round: '', when: '37 min', whenLabel: 'integrated 10:58', phrase: 'integrated',
    doing: 'Merged onto the epic branch at <code>2010f6f</code>.',
    line: 'integrated at 2010f6f', track: 'dddddd', times: { 0: '14 min', 2: '5:02', 3: 'met', 4: '6 min', 5: '0:50' },
    sentence: 'Accepted in one round, reviewed clean at 10:46 and integrated at 10:58: “A zero reset now stores ‘&lt;kind&gt; rate limit, reset time unknown’.”',
    next: 'Done.', n: { sent: ['summary'] } };
  const done = (slug, req, ref, subject) => ({ slug, req, ref, subject, state: 'ok', live: false, phrase: 'integrated', line: `integrated at ${ref}` });

  const EPICS = {
    followups: {
      key: 'followups', slug: 'orrery-followups', proj: 'orrery', host: 'vermissian', folder: '~/Personal/orrery', noun: 'task', started: '10:21',
      reqs: [
        { id: 'R1', kind: 'check', text: 'The judge gets the Task and Return sections whole up to 16 KiB, else their first and last 8 KiB and a line naming the bytes left out.' },
        { id: 'R2', kind: 'check', text: 'A planner turn freed by a rejected limit with no reset stores “reset time unknown”.' },
        { id: 'R3', kind: 'review', text: 'docs/contract.md and engine-protocol.md state R1 and R2.' },
      ],
      events: [
        E('10:19', 'you', '', 'You accepted the contract, so the planner could start: 3 requirements, limited care.'),
        E('10:21', 'planner', '', 'Planner turn 1 outlined 3 tasks and started 2, because R1 and R2 touch different files.', 'Trigger: the contract was accepted. docs-judge-reset waits, since R3 documents the other two.'),
        E('10:21', 'step', '', 'A read-only scout started, to confirm where the judge’s input is cut.', 'Agent sonnet-high-ro. An engine step, not a task.'),
        E('10:21', 'step', 'judge-head-tail', 'Its implementer started on haiku-medium, round 1.'),
        E('10:21', 'step', 'planner-reset-unknown', 'Its implementer started on haiku-medium, round 1.'),
        E('10:24', 'ret', '', 'The scout returned: stepjudge.go:29 keeps only a section’s first 8 KiB.', '', { tone: 'ok' }),
        E('10:31', 'limit', 'planner-reset-unknown', 'A 5-hour usage limit paused the step. It resumes when you say the limit has reset, after 11:30.', '', { alt: 1, tone: 'mine' }),
        E('10:33', 'ret', 'judge-head-tail', 'Returned done at 166091a after 12 min, so its checks could start.'),
        E('10:33', 'evidence', 'judge-head-tail', 'Evidence run: 2 of 3 tests went red, then green. The invalid-UTF-8 test was green both times.', 'Red at the parent commit, green at 166091a. A test that never fails proves nothing about the change.'),
        E('10:33', 'checks', 'judge-head-tail', 'Five checks passed: schema, head, ancestry, clean, owned.', '', { tone: 'ok' }),
        E('10:35', 'ret', 'planner-reset-unknown', 'Returned done at 2010f6f after 14 min.', '', { noalt: 1 }),
        E('10:35', 'evidence', 'planner-reset-unknown', 'Evidence run: the zero-reset test went red, then green.', 'Red at the parent commit, green at 2010f6f.', { noalt: 1, tone: 'ok' }),
        E('10:38', 'gate', 'judge-head-tail', 'The gate <code>just check</code> passed in 4:12, so the done-judge got the return.', '', { tone: 'ok' }),
        E('10:39', 'judge', 'judge-head-tail', 'Done-judge: unmet. Sent back once, because the invalid-UTF-8 test passes before and after the fix.', 'One more unmet verdict ends the step goal_unmet.', { tone: 'bad' }),
        E('10:39', 'step', 'judge-head-tail', 'Round 2 started, with the judge’s reason as its prompt.'),
        E('10:40', 'gate', 'planner-reset-unknown', 'The gate passed in 5:02.', '', { noalt: 1, tone: 'ok' }),
        E('10:40', 'judge', 'planner-reset-unknown', 'Done-judge: met. The step ended done, which proves R2.', '', { noalt: 1, tone: 'ok' }),
        E('10:40', 'review', 'planner-reset-unknown', 'A task reviewer started reading the change against R2, because every accepted task is reviewed before it integrates.', 'Agent sonnet-high-ro, read-only.', { noalt: 1 }),
        E('10:44', 'hold', 'judge-head-tail', 'You held the step mid-turn. It waits for Resume or Stop.', '', { alt: 1, tone: 'mine' }),
        E('10:46', 'review', 'planner-reset-unknown', 'Review clean: 0 findings, 2 observations. It waits to integrate with its wave.', '', { tone: 'ok' }),
        E('10:47', 'ret', 'judge-head-tail', 'Returned a second time. The checks and the gate passed again.'),
        E('10:48', 'judge', 'judge-head-tail', 'Done-judge: unmet again, so the step ended goal_unmet. The test still passes before the fix.', UNMET, { tone: 'bad' }),
        E('10:48', 'question', 'judge-head-tail', 'orrery asked you: keep judge-head-tail as a gap, or try again? Wave 1 cannot settle by itself.', '', { tone: 'mine' }),
        E('10:55', 'decision', 'judge-head-tail', 'You answered: keep it as gap G1, with no second try, because both rounds failed the same way.', '', { tone: 'bad' }),
        E('10:58', 'integrate', 'planner-reset-unknown', 'Integrated onto the epic branch at 2010f6f.', '', { tone: 'ok' }),
        E('10:58', 'planner', '', 'Planner turn 2 started docs-judge-reset, because wave 1 is settled.', 'Trigger: planner-reset-unknown integrated and gap G1 recorded.'),
        E('10:58', 'step', 'docs-judge-reset', 'Its implementer started on haiku-medium, round 1.'),
        E('11:07', 'ret', 'docs-judge-reset', 'Returned done after 9 min. The checks passed.'),
        E('11:11', 'judge', 'docs-judge-reset', 'The gate passed in 3:40 and the done-judge ruled met.', '', { tone: 'ok' }),
        E('11:14', 'review', 'docs-judge-reset', 'Review clean. R3 is a review requirement, so this is its evidence.', '', { tone: 'ok' }),
        E('11:16', 'integrate', 'docs-judge-reset', 'Integrated at 8c4e1d2.', '', { tone: 'ok' }),
        E('11:20', 'review', '', 'Final review of the epic branch: 0 findings. Gap G1 is carried to the report.'),
        E('11:20', 'end', '', 'The epic ended with gaps: R1 is unproven, so nothing was released.', '', { tone: 'bad' }),
      ],
      moments: [
        {
          at: '10:27', label: 'implementing', phase: 'building', word: 'Building.',
          now: 'Both tasks are being implemented, and neither has returned yet.',
          next: 'Each return is checked, gated and judged. docs-judge-reset starts once both are integrated.',
          units: [
            { ...J, state: 'now', live: true, stage: 'Implementing', round: 'round 1', clock: 370, phrase: 'implementing',
              doing: 'Restating the three 8 KiB cut tests for a head, an omission line and a tail.',
              track: 'n-----', times: { 0: '6:10' }, sentence: FIRST,
              next: 'Next: its return, then the checks and the gate.' },
            { ...P, state: 'now', live: true, stage: 'Implementing', round: 'round 1', clock: 348, phrase: 'implementing',
              doing: 'Writing the failing test: a rejected limit whose reset is zero.',
              track: 'n-----', times: { 0: '5:48' }, sentence: FIRST,
              next: 'Next: its return, then the checks and the gate.' },
            D0,
          ],
          engine: [SCOUT],
        },
        {
          at: '10:38', label: 'being checked', phase: 'building', word: 'Building.',
          now: 'Both tasks have returned and are being checked.',
          next: 'Each is accepted or sent back once. docs-judge-reset starts once both are integrated.',
          units: [
            { ...J, state: 'now', live: true, stage: 'Done-judge deciding', round: 'round 1', clock: 1024, phrase: 'being checked', tag: 'judge deciding',
              doing: 'Haiku is reading the return and the diff against the task’s goal.',
              track: 'dddn--', times: { 0: '12 min', 2: '4:12', 3: '0:20' },
              sentence: 'Returned done at <code>166091a</code> after 12 min. All six checks passed, the gate in 4:12. The judge has had it for 20 s: met ends the step done, unmet sends it back once.',
              next: 'Next: accepted, or sent back with the judge’s reason.', n: { stage: ['stage'] } },
            { ...P, state: 'now', live: true, stage: 'Gate running', round: 'round 1', clock: 1002, phrase: 'being checked', tag: 'gate running',
              doing: 'The <code>check</code> gate is running <code>just check</code> on its return at <code>2010f6f</code>.',
              track: 'ddn---', times: { 0: '14 min', 2: '2:41' },
              sentence: 'Returned done at 10:35. Schema, head, ancestry, clean and owned passed. The gate has run 2:41 of its 30 min limit.',
              next: 'Next: the done-judge, if the gate exits 0.', n: { stage: ['stage', 'gate'] } },
            D0,
          ],
          engine: [SCOUT],
        },
        {
          at: '10:41', label: 'sent back', phase: 'building', word: 'Building.',
          now: 'One task is accepted and in review, and one is being reworked.',
          next: 'judge-head-tail returns a second time and is judged again.',
          exc: { tone: 'info', short: 'The judge sent judge-head-tail back: one test does not prove the fix.', unit: 'judge-head-tail', needs: ['verdict'] },
          units: [
            { ...J, state: 'now', live: true, stage: 'Sent back by the done-judge', round: 'round 2', clock: 1210, phrase: 'sent back by the judge', tag: 'reworking, round 2',
              doing: 'Reworking the invalid-UTF-8 test so that it fails against the old cut.',
              track: 'nddb--', times: { 0: '2:02', 3: 'unmet' },
              sentence: 'Round 1 passed every check and the gate. The judge then ruled it unmet and sent it back at 10:39. One more unmet verdict ends the step goal_unmet.',
              reason: { cap: 'Done-judge · 10:39 · unmet, sent back', text: UNMET },
              next: 'Next: a second return, checked and judged again.', n: { stage: ['stage'], reason: ['verdict'] } },
            { ...P, tier: 'sonnet-high-ro', state: 'now', live: true, stage: 'Reviewer reading', round: 'accepted 10:40', clock: 70, phrase: 'in review',
              doing: 'Reading runner.go:373 and the new limit test against R2.',
              track: 'ddddn-', times: { 0: '14 min', 2: '5:02', 3: 'met', 4: '1:10' },
              sentence: 'The implementer’s step ended done at 10:40 in one round: “A zero reset now stores ‘&lt;kind&gt; rate limit, reset time unknown’. A limit with a reset keeps its text.” A read-only task reviewer is checking the change.',
              next: 'Next: findings go back to the implementer, or it waits to integrate.', n: { stage: ['readonly', 'role'], sent: ['summary'] } },
            D0,
          ],
          engine: [SCOUT, { k: 'review planner-reset-unknown', tier: 'sonnet-high-ro', e: 'running', cls: 'run', d: 'reading runner.go:373 against R2' }],
        },
        {
          at: '10:49', label: 'goal unmet', phase: 'building', word: 'Building.',
          now: 'judge-head-tail ended short and waits on your answer, and planner-reset-unknown is ready to integrate.',
          next: 'Your answer keeps it as a gap or starts a second try.',
          exc: { tone: 'mine', since: '1 min', block: 'waits on your answer', short: 'judge-head-tail ended short. Keep it as a gap, or try again?', unit: 'judge-head-tail' },
          units: [
            { ...J, state: 'bad', live: true, stage: 'Ended: goal unmet', round: '2 rounds', when: '27 min', whenLabel: 'ended 10:48', phrase: 'ended short: goal unmet', tag: 'goal unmet',
              doing: 'The done-judge ruled its return unmet twice, so the step ended without acceptance.',
              track: 'dddb--', times: { 0: '2 rounds', 3: 'unmet ×2' },
              sentence: 'Both rounds passed every check and the gate, and the judge was not satisfied either time. The commit <code>166091a</code> is kept on the task’s branch.',
              reason: { cap: 'Done-judge · 10:48 · unmet', text: `${UNMET} The 16 KiB case is not reported. The return has a duplicate <code>green_evidence</code> key.` },
              next: 'Next: your answer keeps it as a gap or starts a second try.' },
            { ...P, state: 'acc', live: true, stage: 'Reviewed, waiting to integrate', round: '', when: '25 min', whenLabel: 'reviewed 10:46', phrase: 'reviewed, waiting to integrate', tag: 'waits to integrate',
              doing: 'The task review found nothing to fix: 0 findings, 2 observations.',
              track: 'ddddd-', times: { 0: '14 min', 2: '5:02', 3: 'met', 4: '6 min' },
              sentence: 'Accepted at 10:40 in one round and reviewed clean at 10:46. It integrates with judge-head-tail in wave 1, so it waits on that task.',
              next: 'Next: integration, once judge-head-tail is settled.', n: { stage: ['role'], sent: ['next'] } },
            D0,
          ],
          engine: [SCOUT, REVIEW_P],
        },
        {
          at: '11:02', label: 'a task done', phase: 'building', word: 'Building.', gaps: 1,
          now: 'The last task, docs-judge-reset, is being implemented.',
          next: 'It is checked and integrated, then the epic ends with 1 gap.',
          exc: { tone: 'bad', short: '1 gap: judge-head-tail ended goal unmet, so R1 stays unproven.', unit: 'judge-head-tail' },
          units: [
            { ...J_GAP, tag: 'gap G1' }, P_DONE,
            { ...D, state: 'now', live: true, stage: 'Implementing', round: 'round 1', clock: 240, phrase: 'implementing',
              doing: 'Writing the new limit text into <code>docs/contract.md</code>.',
              track: 'n-----', times: { 0: '4:00' },
              sentence: 'First turn, nothing returned yet. R3 is a review requirement, so a reviewer reads the docs after the gate.',
              next: 'Next: its return, then the checks and the gate.' },
          ],
          engine: [SCOUT, REVIEW_P],
        },
        {
          at: '11:20', label: 'ended', phase: 'ended', gaps: 1,
          ended: {
            kind: 'gaps', words: 'Ended with gaps: 1 gap, R1 unproven.', head: 'Ended with gaps: 1 gap.', when: '11:20', took: '59 min', phases: 'dgd-d',
            line: 'Two of three requirements shipped, and R1 did not.',
            shipped: [
              { req: 'R2', slug: 'planner-reset-unknown', text: 'A planner turn freed by a limit with no reset now says “reset time unknown”.', ref: '2010f6f' },
              { req: 'R3', slug: 'docs-judge-reset', text: 'The docs state the new limit text. The judge-input paragraph is left out with R1.', ref: '8c4e1d2' },
            ],
            missed: [
              { req: 'R1', slug: 'judge-head-tail', text: 'The judge sees a long section’s head and tail.',
                why: `The done-judge ruled it unmet twice: ${UNMET.charAt(0).toLowerCase()}${UNMET.slice(1)} The commit 166091a is kept on its branch, unmerged.` },
            ],
          },
          units: [
            { ...J_GAP, live: false, tag: 'gap G1' },
            { ...P_DONE, fresh: false },
            { ...D, state: 'ok', live: false, stage: 'Done', round: '', when: '16 min', whenLabel: 'integrated 11:16', phrase: 'integrated',
              doing: 'Merged onto the epic branch at <code>8c4e1d2</code>.',
              line: 'integrated at 8c4e1d2', track: 'dddddd', times: { 0: '9 min', 2: '3:40', 3: 'met', 4: '2 min', 5: '0:40' },
              sentence: 'Accepted in one round and reviewed clean. The paragraph on the judge’s input was left out, since R1 is a gap.',
              next: 'Done.' },
          ],
          engine: [SCOUT, REVIEW_P, { k: 'review docs-judge-reset', tier: 'sonnet-high-ro', e: 'done', cls: 'ok', d: 'clean' },
            { k: 'final review', tier: 'opus', e: 'done', cls: 'ok', d: '0 findings · 1 gap carried to the report' }],
        },
        {
          at: '10:44', label: 'held + limit', phase: 'building', word: 'Building.', alt: true,
          now: 'Nothing is running.',
          next: 'Resume continues judge-head-tail. planner-reset-unknown can resume after 11:30.',
          exc: { tone: 'mine', since: 'since 10:44', block: 'nothing is running', short: 'Nothing is running: you hold one task, and a usage limit holds the other until 11:30.', unit: 'judge-head-tail', needs: ['limit'] },
          units: [
            { ...J, state: 'held', live: true, mine: 'hold', stage: 'Held by you', round: 'round 2', when: '23 min', whenLabel: 'held 10:44', phrase: 'held by you',
              doing: 'You held it at 10:44, mid-turn, while it was reworking the invalid-UTF-8 test.',
              track: 'wddb--', times: { 0: 'held', 3: 'unmet' },
              sentence: 'A hold interrupts the turn, and the interrupted turn is no round. The step checks nothing while it is held.',
              next: 'Resume continues the turn.', held: true },
            { ...P, state: 'held', live: true, mine: 'limit', stage: 'Waiting on you: usage limit', round: 'round 1', when: '10 min', whenLabel: 'paused 10:31', phrase: 'held: usage limit until 11:30', tag: 'limit until 11:30',
              doing: 'Paused by a 5-hour usage limit at 10:31. The limit resets at 11:30.',
              track: 'w-----', times: { 0: 'paused' },
              sentence: 'A limited step spends nothing and ignores its timers. Its work so far is kept in its session, and it resumes when you say the limit has reset.',
              next: 'Resume it after 11:30.', held: true, n: { stage: ['limit'] } },
            D0,
          ],
          engine: [SCOUT],
        },
      ],
    },
    rota: {
      key: 'rota', slug: 'rota-export', proj: 'lighthouse', host: 'halyard', folder: '~/code/lighthouse', noun: 'unit', started: '13:52',
      grew: 'Grew at 14:10: planner turn 3 outlined 2 more units, 5 to 7.',
      reqs: [
        { id: 'R1', kind: 'check', text: 'Shifts are stored per keeper and week.' },
        { id: 'R2', kind: 'check', text: 'The rota endpoint serves one week of shifts.' },
        { id: 'R3', kind: 'check', text: 'A week exports as CSV, one row per shift.' },
        { id: 'R4', kind: 'check', text: 'Export refuses to overwrite an existing file.' },
        { id: 'R5', kind: 'check', text: 'Fixtures cover every port zone.' },
        { id: 'R6', kind: 'review', text: 'Every export is written to the audit log, and the docs say so.' },
      ],
      events: [
        E('13:50', 'you', '', 'You accepted the contract: 6 requirements, serious care.'),
        E('13:52', 'planner', '', 'Planner turn 1 outlined 5 units and started rota-store and csv-writer, because the others build on them.', 'Trigger: the contract was accepted.'),
        E('13:58', 'notice', 'csv-writer', 'Try 1 on haiku was looping, so it was nudged, ended, and retried on sonnet.', 'The monitor saw the same edit three times.', { tone: 'bad' }),
        E('14:04', 'integrate', 'rota-store', 'Integrated at a41b7d0, which proves R1.', '', { tone: 'ok' }),
        E('14:10', 'planner', '', 'Planner turn 3 outlined 2 more units, export-docs and rota-audit: 5 to 7. The audit requirement needed its own unit.', 'Trigger: rota-store integrated. The orrery gained two wedges.'),
        E('14:12', 'integrate', 'csv-writer', 'Integrated at 3f9c2e1 after its retry, which proves R3.', '', { tone: 'ok' }),
        E('14:19', 'gate', 'export-cli', 'The gate exited 1 on round 1, so it was sent back with the gate’s output.', 'cli/export_test.go:88: want ErrExists, got nil.', { tone: 'bad' }),
        E('14:31', 'judge', 'export-cli', 'Round 2 passed the gate and the done-judge ruled met.', '', { tone: 'ok' }),
        E('14:33', 'review', 'rota-handler', 'Review clean, so it could integrate.', '', { tone: 'ok' }),
        E('14:37', 'review', 'export-cli', 'Review finding F1 confirmed: export overwrites an existing file, and R4 says it must refuse. Sent back.', 'cli/export.go:57. A verifier reproduced it before it was sent back.', { tone: 'bad' }),
        E('14:39', 'decision', 'tz-fixtures', 'Planner turn 4 raised a decision for you and paused this unit: bump the tz database?', 'It is outside the unit’s owned files, and the contract forbids dependency changes.', { tone: 'mine' }),
        E('14:40', 'integrate', 'rota-handler', 'The engine started merging it onto the epic branch.'),
      ],
      moments: [
        {
          at: '14:41', label: '14:41', phase: 'building', word: 'Building.',
          now: 'Two units are being worked on, and one waits on you.',
          next: 'export-docs and rota-audit are outlined and start after these.',
          exc: { tone: 'mine', since: '2 min', block: 'waits on your decision', short: 'tz-fixtures waits on your decision: bump the tz database?', unit: 'tz-fixtures' },
          units: [
            { slug: 'tz-fixtures', tier: 'haiku', req: 'R5', subject: 'Fixtures cover every port zone.', ask: true,
              state: 'held', live: true, stage: 'Waiting on you: a decision', round: 'round 1', when: '2 min', whenLabel: 'paused 14:39', phrase: 'waiting on your decision', tag: 'your decision',
              doing: 'It asked to bump the embedded tz database. That changes <code>go.mod</code>, which is outside its owned files.',
              track: 'w-----', times: { 0: 'paused' },
              sentence: 'Planner turn 4 raised the decision at 14:39 and paused this unit alone. Your answer is written to decisions.md and resumes it.',
              next: 'Only this unit is paused.' },
            { slug: 'export-cli', tier: 'sonnet', req: 'R4', subject: 'Export refuses to overwrite an existing file.', pane: 'step',
              state: 'now', live: true, stage: 'Fixing a review finding', round: 'round 3', clock: 252, phrase: 'sent back by the reviewer', tag: 'fixing F1',
              doing: 'Adding the refuse-on-existing check and its test.',
              track: 'ndddb-', times: { 0: '4:12', 2: '3:58', 4: 'F1' },
              sentence: 'Round 2 passed its gate and the judge. The task review then confirmed finding F1 and sent it back into this session at 14:37.',
              checkNote: 'Round 1: gate exit 1, <code>cli/export_test.go:88: want ErrExists, got nil</code>.',
              reason: { cap: 'Review finding F1 · confirmed 14:37', text: '<code>export</code> overwrites an existing file without asking. R4 says it must refuse. <span class="at">cli/export.go:57</span>' },
              next: 'Next: its return is checked, gated and judged again, then F1 is verified closed.',
              n: { reason: ['finding'], check: ['detail'] }, mon: 'model time 22 of 60 min · last event 12 s ago' },
            { slug: 'rota-handler', tier: 'haiku', req: 'R2', subject: 'The rota endpoint serves one week of shifts.', pane: 'generic',
              state: 'now', live: true, stage: 'Integrating', round: '', clock: 540, phrase: 'integrating',
              doing: 'The engine is merging it onto the epic branch.',
              track: 'dddddn', times: { 5: '0:40' },
              sentence: 'Implemented, gated, judged and reviewed clean. The merge onto the epic branch is the last part.',
              next: 'Then it counts as done: 3 of 7.', mon: 'model time 9 of 60 min · last event 40 s ago' },
            { ...done('rota-store', 'R1', 'a41b7d0', 'Shifts are stored per keeper and week.'), tier: 'haiku' },
            { ...done('csv-writer', 'R3', '3f9c2e1', 'A week exports as CSV, one row per shift.'), tier: 'sonnet', line: 'integrated at 3f9c2e1, after a retry' },
            { slug: 'export-docs', req: 'R6', subject: 'The README documents the export command and its audit line.', tier: '', state: 'out', live: false, phrase: 'outlined', line: 'outlined, not started' },
            { slug: 'rota-audit', req: 'R6', subject: 'Every export writes one line to the audit log.', tier: '', state: 'out', live: false, phrase: 'outlined', line: 'outlined, not started' },
          ],
          engine: [
            { k: 'planner turn 3', tier: 'opus', e: 'returned', cls: 'ok', d: '1 new unit, 2 more outlined' },
            { k: 'review rota-handler', tier: 'sonnet', e: 'done', cls: 'ok', d: 'clean' },
            { k: 'csv-writer, try 1', tier: 'haiku', e: 'looping', cls: 'bad', d: 'nudged, ended, retried on sonnet' },
            { k: 'review export-cli', tier: 'sonnet', e: 'done', cls: 'ok', d: '1 finding, verifier confirmed' },
          ],
        },
      ],
    },
    tidealerts: {
      key: 'tidealerts', slug: 'tide-alerts', proj: 'tidepool', host: 'halyard', folder: '~/code/tidepool', noun: 'unit',
      reqs: [
        { id: 'R1', kind: 'check', text: 'A text goes out the evening before a tide above 4.5 m.' },
        { id: 'R2', kind: 'check', text: 'Last year’s tide tables replay with no alert missed.' },
        { id: 'R3', kind: 'check', text: 'The phone number comes from config only.' },
        { id: 'R4', kind: 'check', text: 'One alert per tide id.' },
        { id: 'R5', kind: 'check', text: 'A dry-run flag logs instead of sending.' },
      ],
      events: [
        E('14:02', 'you', '', 'You asked for a text the evening before a tide above 4.5 m.'),
        E('14:13', 'review', '', 'The steelman pass closed 3 findings in the draft, which set the care level to severe.'),
        E('14:15', 'contract', '', 'The contract is ready for you to accept, with 1 decision and 1 added row open.', '', { tone: 'mine' }),
      ],
      moments: [{
        at: '14:41', phase: 'contract', word: 'Contract written.', since: '26 min',
        now: 'The contract is written, and nothing is built until you accept it.',
        next: 'One decision and one added row are yours to settle first.',
        exc: { tone: 'mine', since: '26 min', block: 'waits on you to accept', short: 'The contract is ready for you to accept: severe care, 1 decision open.' },
        units: [],
      }],
    },
    offline: {
      key: 'offline', slug: 'offline-tables', proj: 'tidepool', host: 'halyard', folder: '~/code/tidepool', noun: 'unit', started: '12:10',
      reqs: [
        { id: 'R1', kind: 'check', text: 'Tide tables for the home port are stored on the phone.' },
        { id: 'R2', kind: 'check', text: 'Stored tables refresh when the network returns.' },
        { id: 'R3', kind: 'check', text: 'The sync worker retries with backoff.' },
        { id: 'R4', kind: 'check', text: 'A sync never blocks the screen.' },
        { id: 'R5', kind: 'check', text: 'The tables screen works with no network.' },
        { id: 'R6', kind: 'review', text: 'The screen says how old its data is.' },
      ],
      events: [
        E('12:08', 'you', '', 'You accepted the contract: 6 requirements, serious care.'),
        E('13:21', 'integrate', 'offline-screen', 'The last unit integrated, so the final review could start.', '', { tone: 'ok' }),
        E('13:40', 'review', '', 'Final review finding I1: the table cache key leaves out the port.', 'cache/key.go:41. Confirmed 13:44, corrected at 9be21f4.', { tone: 'bad' }),
        E('14:02', 'review', '', 'The finding’s closure passed, so the review is clean.', '', { tone: 'ok' }),
        E('14:03', 'release', '', 'orrery started the release by itself, because the epic is clean: 6 of 6 proven, 0 gaps.'),
        E('14:05', 'release', '', 'Step 1 done: pushed main and the signed tag v0.4.0.', 'Undo: delete the tag, until someone has fetched it.', { tone: 'ok' }),
        E('14:11', 'release', '', 'Step 2 failed: CI is red on the tag, run 5512.', 'go vet: cache/key_test.go:41.', { tone: 'bad' }),
        E('14:21', 'release', '', 'orrery retried step 2 once by itself, because a CI run can fail for no reason of ours.'),
        E('14:32', 'question', '', 'CI was red again, run 5519, so orrery stopped and asked you: retry, or stop the release?', 'A fix needs the tag to move, and that push cannot be undone. orrery does not repeat it without your word.', { tone: 'mine' }),
        E('14:42', 'you', '', 'You chose retry, so orrery started CI run 5524 on the tag.', '', { from: 1 }),
      ],
      moments: [
        {
          at: '14:41', phase: 'releasing', word: 'Release stopped.', since: '9 min', act: 'stopped at step 2 of 3',
          now: 'Built and reviewed clean. orrery was releasing it by itself and stopped at step 2: CI is red on the tag.',
          next: 'Nothing else runs until you retry step 2 or stop the release.',
          exc: { tone: 'mine', since: '9 min', block: 'waits on you: retry or stop', short: 'Release step 2 failed: CI is red on the tag. Retry, or stop the release?' },
          release: { ask: 'CI is red on the tag v0.4.0 for the second time. A retry runs CI again on the same tag. Stopping ends the epic as stopped and leaves the tag where it is.',
            steps: [
              { name: 'Push main and tag v0.4.0, signed', state: 'done', note: 'github · 14:05' },
              { name: 'CI green on the tag', state: 'fail', note: 'red twice · run 5519 · 14:32' },
              { name: 'Publish the APK to the release page', state: 'todo', note: 'after step 2' },
              ] },
          units: [done('table-store', 'R1 R2', '41c0e77'), done('sync-worker', 'R3 R4', 'a90d2f1'), done('offline-screen', 'R5 R6', '9be21f4')],
        },
        {
          at: '14:42', phase: 'releasing', word: 'Releasing.', since: '39 min', act: 're-running CI on the tag v0.4.0',
          now: 'orrery is running CI again on the tag v0.4.0, as you asked.',
          next: 'If CI is green it publishes the APK and writes the report by itself.',
          release: { steps: [
            { name: 'Push main and tag v0.4.0, signed', state: 'done', note: 'github · 14:05' },
            { name: 'CI green on the tag', state: 'run', note: 'run 5524 · try 3, the one you asked for', clock: 14 },
            { name: 'Publish the APK to the release page', state: 'todo', note: 'after step 2' },
          ] },
          units: [done('table-store', 'R1 R2', '41c0e77'), done('sync-worker', 'R3 R4', 'a90d2f1'), done('offline-screen', 'R5 R6', '9be21f4')],
          engine: [{ k: 'final review', tier: 'opus', e: 'done', cls: 'ok', d: '1 finding, closed at 14:02' }],
        },
      ],
    },
    orbitlabels: {
      key: 'orbitlabels', slug: 'orbit-labels', proj: 'orrery', host: 'vermissian', folder: '~/Personal/orrery', noun: 'task', started: '13:58',
      reqs: [
        { id: 'R1', kind: 'check', text: 'Every orbit on the epic chart carries its stage name.' },
        { id: 'R2', kind: 'check', text: 'Hovering a body shows its task, state and timing.' },
        { id: 'R3', kind: 'check', text: 'The phone draws the same chart from the same cells.' },
        { id: 'R4', kind: 'review', text: 'The chart spec states the new labels.' },
      ],
      events: [
        E('13:56', 'you', '', 'You accepted the contract: 4 requirements, limited care.'),
        E('14:31', 'integrate', 'spec-labels', 'The last task integrated, so the final review could start.', '', { tone: 'ok' }),
        E('14:36', 'review', '', 'Final review of the epic branch: 0 findings.', '', { tone: 'ok' }),
        E('14:37', 'release', '', 'orrery started the release by itself, because the epic is clean: 4 of 4 proven, 0 gaps.'),
        E('14:38', 'release', '', 'Step 1 done: merged the epic branch to main, fast-forward.', '', { tone: 'ok' }),
        E('14:40', 'release', '', 'Step 2 started: pushing the signed tag v0.46.0.', 'A pushed tag cannot be undone once someone has fetched it. The contract listed it, and you accepted it there.'),
      ],
      moments: [{
        at: '14:41', phase: 'releasing', word: 'Releasing.', since: '4 min', act: 'pushing tag v0.46.0',
        now: 'Built and reviewed clean. orrery is releasing it by itself: pushing tag v0.46.0.',
        next: 'CI on the tag, then the report.',
        release: { steps: [
          { name: 'Merge the epic branch to main', state: 'done', note: 'fast-forward · 14:38' },
          { name: 'Push tag v0.46.0, signed', state: 'run', note: 'github · no undo once fetched', clock: 22 },
          { name: 'CI green on the tag', state: 'todo', note: 'after step 2' },
        ] },
        units: [done('orbit-names', 'R1', '5d0a7c2', 'Every orbit carries its stage name.'), done('hover-cards', 'R2 R3', 'e19f3b0', 'A card for every body, on the web and the phone.'), done('spec-labels', 'R4', '7ab4411', 'The chart spec states the new labels.')],
        engine: [{ k: 'final review', tier: 'opus', e: 'done', cls: 'ok', d: '0 findings' }],
      }],
    },
    holder: {
      key: 'holder', slug: 'holder-scopes', proj: 'orrery', host: 'vermissian', folder: '~/Personal/orrery', noun: 'task',
      reqs: [
        { id: 'R1', kind: 'check', text: 'Each holder runs in its own systemd scope.' },
        { id: 'R2', kind: 'check', text: 'The terminal memory cap covers holder scopes.' },
        { id: 'R3', kind: 'check', text: 'orreryd starts as Type=notify with a 900 s ceiling.' },
        { id: 'R4', kind: 'review', text: 'The devspaces doc names no orrery version.' },
      ],
      events: [
        E('08:04', 'you', '', 'You accepted the contract: 4 requirements, serious care.'),
        E('09:31', 'integrate', 'docs-devspaces', 'The last task integrated.', '', { tone: 'ok' }),
        E('09:38', 'review', '', 'Final review of the epic branch: 0 findings.', '', { tone: 'ok' }),
        E('09:39', 'release', '', 'orrery started the release by itself, because the epic is clean.'),
        E('09:44', 'release', '', 'Tagged v0.45.0 and pushed. CI went green on the tag.', '', { tone: 'ok' }),
        E('09:52', 'end', '', 'Released v0.45.0. vermissian runs it.', '', { tone: 'ok' }),
      ],
      moments: [{
        at: '09:52', phase: 'ended',
        ended: {
          kind: 'released', words: 'Released v0.45.0.', head: 'Released v0.45.0.', when: '09:52', took: '1 h 48 min', phases: 'ddddd',
          line: 'All four requirements shipped.',
          shipped: [
            { req: 'R1', slug: 'holder-scope-unit', text: 'Each holder runs in its own systemd scope.', ref: 'b91e1ed' },
            { req: 'R2', slug: 'memory-cap', text: 'The terminal memory cap covers holder scopes.', ref: '984ef49' },
            { req: 'R3', slug: 'notify-start', text: 'orreryd starts as Type=notify with a 900 s ceiling.', ref: '9cc2be1' },
            { req: 'R4', slug: 'docs-devspaces', text: 'The devspaces doc names no orrery version.', ref: '9593e0b' },
          ],
          missed: [],
        },
        units: [done('holder-scope-unit', 'R1', 'b91e1ed'), done('memory-cap', 'R2', '984ef49'), done('notify-start', 'R3', '9cc2be1'), done('docs-devspaces', 'R4', '9593e0b')],
      }],
    },
    tidecache: {
      key: 'tidecache', slug: 'tide-cache', proj: 'tidepool', host: 'halyard', folder: '~/code/tidepool', noun: 'unit',
      reqs: [
        { id: 'R1', kind: 'check', text: 'Tide tables are cached on disk.' },
        { id: 'R2', kind: 'check', text: 'The cache drops tables older than a week.' },
        { id: 'R3', kind: 'review', text: 'The docs describe the cache.' },
      ],
      events: [
        E('08:48', 'you', '', 'You accepted the contract: 3 requirements, limited care.'),
        E('08:50', 'planner', '', 'Planner turn 1 outlined 3 units and started 2.'),
        E('09:12', 'you', '', 'You stopped the epic, because offline-tables covers the same ground.', 'Two unit branches are kept, unmerged.'),
        E('09:12', 'end', '', 'The epic ended as stopped. Nothing shipped.'),
      ],
      moments: [{
        at: '09:12', phase: 'ended',
        ended: {
          kind: 'stopped', words: 'Stopped: superseded by offline-tables.', head: 'Stopped.', when: '09:12', took: '22 min', phases: 'ds---',
          line: 'Nothing shipped.',
          shipped: [],
          missed: [
            { req: 'R1–R3', slug: 'all three units', text: 'A cache of tide tables on disk.',
              why: 'You stopped it at 09:12, because offline-tables covers the same ground. Two unit branches are kept, unmerged.' },
          ],
        },
        units: [
          { slug: 'cache-store', req: 'R1', state: 'stop', live: false, track: 'ds----', phrase: 'stopped while being checked', line: 'stopped while being checked · branch kept' },
          { slug: 'cache-evict', req: 'R2', state: 'stop', live: false, track: 's-----', phrase: 'stopped while implementing', line: 'stopped while implementing · branch kept' },
          { slug: 'cache-docs', req: 'R3', state: 'none', live: false, phrase: 'never started', line: 'never started' },
        ],
      }],
    },
  };
  const ORDER = ['followups', 'rota', 'tidealerts', 'offline', 'orbitlabels', 'holder', 'tidecache'];

  // The rest of the list: what needs the owner, and the sessions. Both pages draw these.
  // A session's title is its directory and one line that says what it has been doing. label is the pane's own label, which
  // only the phone still draws. ctx is [tokens, window].
  const SESS = {
    beacon: { label: 'beacon-schedule', proj: 'lighthouse', sum: 'Why the nightly backfill takes forty minutes', host: 'halyard', state: 'waiting', age: '12m', pane: 'question', sm: 'asks which index to add', ctx: [88000, 200000],
      text: 'Which index should I add? The nightly backfill scans every shift because the rotation table has no index on <code>keeper_id</code>.',
      opts: ['Composite index', 'Partial index', 'Other'] },
    ephem: { label: 'ephemeris-import', proj: 'almanac', sum: 'Importing the 2027 ephemeris tables', host: 'halyard', state: 'interrupted', age: '2h', sm: 'interrupted by a restart', ctx: [61000, 200000],
      text: 'orreryd restarted during the turn, and the turn stopped where it was.' },
    graft: { label: 'graft-parser', proj: 'orchard', sum: 'Parser tests for nested scion grafting', host: 'sextant', state: 'done', age: '20m', sm: 'parser tests pass', ctx: [112000, 200000],
      text: 'All 48 parser tests pass. The grafting rule for nested scions was the only real change; the rest was test data.' },
    moon: { label: 'moon-phase-api', proj: 'almanac', sum: 'Phase endpoint returns ISO dates', host: 'halyard', state: 'done', age: '39m', sm: 'phase endpoint returns ISO dates', ctx: [34000, 200000],
      text: 'The phase endpoint now returns ISO dates, and both clients that parsed the old format are updated.' },
    lamp: { label: 'lamp-telemetry', proj: 'lighthouse', sum: 'Retry backoff and jitter for the telemetry uploader', host: 'halyard', state: 'working', age: '1m', pane: 'lamp', sm: 'adding retry backoff', ctx: [152000, 1000000],
      tool: ['Bash', 'go test ./internal/telemetry/...', 42], text: 'adding retry backoff' },
    harvest: { label: 'harvest-report', proj: 'orchard', sum: 'CSV export for the harvest report', host: 'sextant', state: 'working', age: '3m', sm: 'rendering CSV export', ctx: [163000, 200000],
      tool: ['Edit', 'report/csv.go', 7], text: 'rendering CSV export' },
    docs: { label: 'docs-examples', proj: 'tidepool', sum: 'README examples fixed and run in CI', host: 'tarn', state: 'done', age: '5h', sm: 'README examples fixed', ctx: [22000, 200000], text: 'README examples fixed and run in CI.' },
    tidetables: { label: 'tide-tables', proj: 'tidepool', sum: 'Tide tables migration, merged', host: 'tarn', state: 'done', age: '2d', sm: 'migration merged', ctx: [97000, 200000], text: 'Migration merged; the old table is dropped.' },
    rotaplan: { label: 'rota-planning', proj: 'lighthouse', sum: 'Planned the rota export, and steers its epic', host: 'halyard', state: 'done', age: '1m', pane: 'director', epic: 'rota', sm: 'created rota-export', ctx: [128000, 200000],
      text: 'The contract for rota-export is accepted and the epic is created. I can read it or steer it from here whenever you ask.' },
    tideplan: { label: 'tide-planning', proj: 'tidepool', sum: 'Drafting the tide-alerts contract with you', host: 'tarn', state: 'waiting', age: '26m', pane: 'director', epic: 'tidealerts', sm: 'wrote the contract', ctx: [62000, 200000],
      text: 'The contract for tide-alerts is ready for you to read and accept.' },
    cache: { label: 'ci-cache', proj: 'quarry', sum: 'CI cache keys trimmed to the lockfile', host: 'halyard', state: 'done', age: '3d', sm: 'cache keys trimmed', ctx: [18000, 200000], text: 'Cache keys trimmed to the lockfile hash.' },
    // The conversations that created the other epics. They are quiet, so the list folds them away; an epic's page links to its own.
    fplan: { label: 'followups-planning', proj: 'orrery', sum: 'Planned two follow-ups from the O3 review', host: 'vermissian', state: 'done', age: '4h', epic: 'followups', ctx: [71000, 200000], text: 'The contract for orrery-followups is accepted and the epic is created.' },
    offplan: { label: 'offline-planning', proj: 'tidepool', sum: 'Planned tide tables that work with no network', host: 'halyard', state: 'done', age: '2h', epic: 'offline', ctx: [143000, 200000], text: 'The contract for offline-tables is accepted and the epic is created.' },
    orbplan: { label: 'orbit-planning', proj: 'orrery', sum: 'Planned the names on the epic chart', host: 'vermissian', state: 'done', age: '45m', epic: 'orbitlabels', ctx: [54000, 200000], text: 'The contract for orbit-labels is accepted and the epic is created.' },
    holdplan: { label: 'holder-planning', proj: 'orrery', sum: 'Planned a systemd scope for each holder', host: 'vermissian', state: 'done', age: '6h', epic: 'holder', ctx: [66000, 200000], text: 'The contract for holder-scopes is accepted and the epic is created.' },
  };
  // What waits on the owner. Each says what is waiting and why; the marker only tells the kinds apart.
  const KIND = {
    decision: { icon: 'decision', name: 'A decision only you can make', what: 'The planner raised it and paused one unit. Your answer is written to decisions.md, and the unit resumes.' },
    question: { icon: 'question', name: 'A session is asking you a question', what: 'It waits for your answer and does nothing else meanwhile. You can answer from this row.' },
    contract: { icon: 'contract', name: 'A contract to read and accept', what: 'Nothing is built until you accept what will be built and how it is proven.' },
    release: { icon: 'release', name: 'A release step failed', what: 'orrery runs a clean epic’s release by itself. It stops and asks only when a step fails, or when a step that cannot be undone would have to be repeated.' },
    interrupted: { icon: 'interrupted', name: 'A session was interrupted', what: 'Its turn was cut off, by a restart here. Resume continues the turn where it stopped.' },
  };
  // say is the whole sentence. The list draws the short form: what is asked, then where.
  const NEED = [
    { id: 'need-decision', kind: 'decision', where: 'rota-export', age: '2m', epic: 'rota', when: ['rota', 0],
      short: 'Bump the tz database?', sub: 'rota-export · the planner’s call',
      say: 'The planner wants your call: <b>bump the tz database?</b> It is outside tz-fixtures’ files, and the contract forbids new dependencies.', act: 'Decide' },
    { id: 'need-gap', kind: 'decision', where: 'orrery-followups', age: '1m', epic: 'followups', when: ['followups', 3],
      short: 'Keep judge-head-tail as a gap, or retry?', sub: 'orrery-followups · ruled unmet twice',
      say: '<b>judge-head-tail ended short.</b> Keep it as a gap, or try again? The judge ruled its goal unmet twice.', act: 'Decide' },
    { id: 'beacon', kind: 'question', session: 'beacon', where: 'lighthouse', age: '12m',
      short: 'Which index should I add?', sub: 'lighthouse · the nightly backfill',
      say: 'A session in <b>lighthouse</b> asks <b>which index to add</b>, because the nightly backfill scans every shift.' },
    { id: 'need-contract', kind: 'contract', where: 'tide-alerts', age: '26m', epic: 'tidealerts', session: 'tideplan', when: ['tidealerts', 0],
      short: 'A contract to read: tide-alerts', sub: 'tidepool · severe care · 1 decision open',
      say: 'A contract is ready for you to accept: <b>tide-alerts</b>, severe care, 1 decision open.', act: 'Read and accept' },
    { id: 'need-release', kind: 'release', where: 'offline-tables', age: '9m', epic: 'offline', when: ['offline', 0],
      short: 'Release step 2 failed: CI is red on the tag', sub: 'offline-tables · tried twice',
      say: '<b>Release step 2 failed:</b> CI is red on the tag v0.4.0. Retry, or stop the release?', rel: true },
    { id: 'ephem', kind: 'interrupted', session: 'ephem', where: 'almanac', age: '2h',
      short: 'Stopped mid-turn when orreryd restarted', sub: 'almanac · the 2027 ephemeris import',
      say: 'A session in <b>almanac</b> stopped mid-turn when orreryd restarted.', resume: 'ephem' },
  ];
  const GROUPS = { fresh: ['graft', 'moon'], working: ['lamp', 'harvest'], seen: ['rotaplan', 'docs', 'tidetables', 'cache'] };
  const FOLDERS = [
    ['~/Personal/orrery', 'vermissian', ['followups', 'orbitlabels', 'holder'], []],
    ['~/code/lighthouse', 'halyard', ['rota'], ['lamp', 'beacon', 'rotaplan']],
    ['~/code/orchard', 'sextant', [], ['graft', 'harvest']],
    ['~/code/almanac', 'halyard', [], ['moon', 'ephem']],
    ['~/code/tidepool', 'tarn', ['tidealerts', 'offline', 'tidecache'], ['tideplan', 'docs', 'tidetables']],
    ['~/code/quarry', 'halyard', [], ['cache']],
  ];
  // What each host's status command reports, as Host.Status sends it: a label, a value, a percent when the value is one,
  // and a level (warn, crit) when the host says so. The labels are the host's own, so the page draws whatever arrives.
  const HOSTS = {
    vermissian: [['cpu', '34%', 34], ['mem', '61%', 61], ['load', '2.1'], ['disk', '48%', 48]],
    halyard: [['cpu', '12%', 12], ['mem', '44%', 44], ['load', '0.6'], ['gpu', '8%', 8]],
    sextant: [['cpu', '71%', 71, 'warn'], ['mem', '58%', 58], ['load', '5.3', null, 'warn']],
    tarn: [['cpu', '3%', 3], ['mem', '22%', 22], ['load', '0.1']],
  };

  // ---------- state ----------
  const state = { moment: { followups: 2 } };
  const paused = {};                                      // epics the owner has paused on this page, and since when
  const armed = {};                                       // a Stop that is waiting for its confirmation
  const pick = {};                                        // which option a form's cursor is on
  const histOpen = {};                                    // epics whose history is opened in place
  const born = Date.now();
  const stamp = {};                                       // when each epic's state last changed on this page
  const elapsed = () => Math.floor((Date.now() - born) / 1000);
  const index = (key) => state.moment[key] || 0;
  const cur = (key) => EPICS[key].moments[index(key)];
  const mmss = (secs) => `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}`;
  // A clock that keeps counting across redraws: the page ticks the number, a redraw starts from the same second.
  const ticking = (base, tag = 'span') => `<${tag} data-clock="${base + elapsed()}">${mmss(base + elapsed())}</${tag}>`;
  const plain = (html) => html.replace(/<[^>]+>/g, '');
  // A need stands while the thing it asks about is open.
  const resumed = {};
  const needs = () => NEED.filter((n) => (n.when ? index(n.when[0]) === n.when[1] : true) && !(n.resume && resumed[n.resume] === 'gone'));

  // ---------- small pieces ----------
  const TONE = { now: 'run', held: 'mine', bad: 'bad', acc: 'wait', ok: 'ok', none: 'idle', out: 'idle', stop: 'idle' };
  const toneOf = (u) => TONE[u.state];
  const MARK = {
    run: '<i class="mk run spin"></i>', mine: '<i class="mk mine">Ⅱ</i>', bad: '<i class="mk bad">✗</i>',
    ok: '<i class="mk ok"><svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2.4 6.5l2.4 2.4 4.8-5.3"/></svg></i>',
    wait: '<i class="mk wait">✓</i>', idle: '<i class="mk idle">◌</i>',
  };
  // ---------- where each task is: its orbit, its tone, and whether something sent it back ----------
  // A task's track has six parts: implement, checks, gate, judge, review, integrate. They fall on four orbits and the
  // centre: implement, then checks, gate and judge as "checking", then review, then integrate.
  const BAND_OF = [1, 2, 2, 2, 3, 4];
  function place(u) {
    if (u.ghost) return { band: -1, tone: 'ghost', bounce: 0 };
    if (u.state === 'ok') return { band: 4, tone: 'ok', bounce: 0 };
    if (!u.track) return { band: 0, tone: 'plan', bounce: 0 };
    const t = u.track, at = t.search(/[nws]/);
    if (at >= 0) {
      const later = t.indexOf('b', at + 1);
      return { band: BAND_OF[at], tone: t[at] === 'n' ? 'run' : t[at] === 'w' ? 'mine' : 'stop', bounce: later > 0 ? BAND_OF[later] : 0 };
    }
    const bad = t.indexOf('b');
    if (bad >= 0) return { band: BAND_OF[bad], tone: 'bad', bounce: 0 };
    return { band: BAND_OF[Math.max(0, t.lastIndexOf('d'))], tone: 'acc', bounce: 0 };
  }
  const reqsOf = (u) => (u.req ? u.req.split(' ') : []);
  // One wedge per task. An epic with no tasks yet shows its requirements as empty wedges.
  function wedges(key) {
    const e = EPICS[key], m = cur(key);
    const units = m.units.length ? m.units : e.reqs.map((r) => ({ slug: '', req: r.id, ghost: true }));
    return units.map((u) => ({ u, ...place(u) }));
  }
  // A task's state in a word or two, as the list line and the cards say it.
  function word(w) {
    if (w.tone === 'ok') return 'integrated';
    if (w.tone === 'plan') return 'planned';
    if (w.tone === 'mine') return 'waiting on you';
    if (w.tone === 'bad') return 'ended short';
    if (w.tone === 'stop') return 'stopped';
    if (w.tone === 'acc') return 'waiting to integrate';
    return ['planned', 'implementing', 'being checked', 'in review', 'integrating'][w.band];
  }
  // A cell of a wedge: one task on one orbit.
  // Between the two, a phase it passed in an earlier round and will run again.
  const cellState = (b, w) => (w.tone === 'ghost' ? 'ghost' : w.tone === 'plan' ? (b === 0 ? 'plan' : 'ahead')
    : b < w.band ? 'trail' : b === w.band ? w.tone : b === w.bounce ? 'bounce' : b < w.bounce ? 'again' : 'ahead');

  // A requirement's state, read off the tasks that serve it. Proven means the evidence passed and the judge accepted it.
  function reqState(key, id) {
    const m = cur(key), W = wedges(key).filter((w) => !w.u.ghost && reqsOf(w.u).includes(id));
    if (W.length && W.every((w) => w.band >= 3 && ['run', 'acc', 'ok'].includes(w.tone))) return 'proven';
    if (m.ended && m.ended.kind === 'stopped') return 'dropped';
    if (W.some((w) => w.tone === 'bad')) return m.gaps ? 'gap' : 'unmet';
    if (!W.length || W.every((w) => w.band === 0)) return 'todo';
    return 'doing';
  }
  const REQ_WORD = { proven: 'proven ✓', gap: 'gap ✗', unmet: 'unmet ✗', dropped: 'dropped', doing: 'in progress', todo: 'not started' };
  const REQ_LONG = { proven: 'proven', gap: 'a gap: it will not be proven in this epic', unmet: 'not proven: the judge ruled it unmet', dropped: 'dropped when the epic stopped', doing: 'not proven yet', todo: 'not started' };
  function counts(key) {
    const e = EPICS[key], m = cur(key), W = wedges(key).filter((w) => !w.u.ghost);
    return { proven: e.reqs.filter((r) => reqState(key, r.id) === 'proven').length, total: e.reqs.length, gaps: m.gaps || 0,
      done: W.filter((w) => w.tone === 'ok').length, run: W.filter((w) => w.tone === 'run').length, tasks: W.length };
  }

  // ---------- geometry ----------
  const TAU = Math.PI * 2, rad = (deg) => (deg * Math.PI) / 180;
  function geo(C) {
    const at = (r, a) => [C + r * Math.sin(a), C - r * Math.cos(a)];
    const xy = (r, a) => at(r, a).map((v) => v.toFixed(1)).join(' ');
    const big = (sweep) => (sweep > Math.PI ? 1 : 0);
    // A piece of a ring between two radii and two angles, g units clear of its neighbours, corners rounded by cr.
    function sector(ri, ro, a0, a1, g, cr) {
      const o0 = a0 + g / 2 / ro, o1 = a1 - g / 2 / ro, i0 = a0 + g / 2 / ri, i1 = a1 - g / 2 / ri;
      if (!cr) return `M${xy(ro, o0)}A${ro} ${ro} 0 ${big(o1 - o0)} 1 ${xy(ro, o1)}L${xy(ri, i1)}A${ri} ${ri} 0 ${big(i1 - i0)} 0 ${xy(ri, i0)}Z`;
      const co = cr / ro, ci = cr / ri, f = cr / (ro - ri);
      const s1o = o1 + (i1 - o1) * f, s1i = i1 + (o1 - i1) * f, s0o = o0 + (i0 - o0) * f, s0i = i0 + (o0 - i0) * f;
      return `M${xy(ro, o0 + co)}A${ro} ${ro} 0 ${big(o1 - o0 - 2 * co)} 1 ${xy(ro, o1 - co)}Q${xy(ro, o1)} ${xy(ro - cr, s1o)}`
        + `L${xy(ri + cr, s1i)}Q${xy(ri, i1)} ${xy(ri, i1 - ci)}A${ri} ${ri} 0 ${big(i1 - i0 - 2 * ci)} 0 ${xy(ri, i0 + ci)}`
        + `Q${xy(ri, i0)} ${xy(ri + cr, s0i)}L${xy(ro - cr, s0o)}Q${xy(ro, o0)} ${xy(ro, o0 + co)}Z`;
    }
    // An arc for words to sit on. In the lower half it runs the other way, so the words stay upright.
    const arc = (r, a0, a1, back) => (back ? `M${xy(r, a1)}A${r} ${r} 0 ${big(a1 - a0)} 0 ${xy(r, a0)}` : `M${xy(r, a0)}A${r} ${r} 0 ${big(a1 - a0)} 1 ${xy(r, a1)}`);
    return { at, xy, sector, arc };
  }

  // ---------- what each epic carries beside its moments ----------
  // The summary paragraph, the contract, the base commit, each requirement's test and how it fails there, the release
  // plan with each step's undo, and the sessions that have touched the epic.
  const pre = (text) => `<pre>${text}</pre>`;
  const X = {
    followups: {
      base: '71c0d3e',
      para: 'Two follow-ups from the O3 review. The done-judge gets the start and the end of a long Task or Return section, with a line naming the bytes left out, where it used to get only the first 8 KiB. A planner turn freed by a usage limit that has no reset time says “reset time unknown”. The docs state both.',
      contract: { accepted: '10:19', care: 'limited', decided: 2, line: 'Approved: the judge reads the head and the tail of a long section, and a limit with no reset says so.',
        asked: 'The done-judge ruled on a return it had only half read, and a freed planner turn showed a reset time that did not exist.',
        fails: [['F1', 'The judge ruled on a return it had only half read', 'head and tail up to 16 KiB (R1)', 'limited'], ['F2', 'A limit with no reset showed a wrong time', 'store “reset time unknown” (R2)', 'limited']], added: [] },
      tests: { R1: ['TestJudgeInputHeadTail', 'stepjudge_test.go:61: want head, omission line and tail; got the first 8192 bytes'],
        R2: ['TestPlannerFreedByLimitNoReset', 'runner_test.go:412: want “reset time unknown”, got “resets 00:00”'] },
      rel: [['Merge the epic branch to main', 'revert the merge'], ['Push tag v0.47.1, signed', 'none once someone has fetched the tag'], ['CI green on the tag', 'rerun']],
      talk: [{ id: 'fplan', label: 'followups-planning', where: 'tmux pane · vermissian', last: 'created the epic', at: '10:19' }],
    },
    rota: {
      base: '3e1f2ab',
      para: 'A keeper’s week of shifts becomes a file for the harbour office. Shifts are stored per keeper and week, one endpoint serves a week, and <code>rota export</code> writes it as CSV, one row per shift. Export refuses to overwrite a file and writes every run to the audit log. Fixtures cover every port zone. No dependency changes.',
      contract: { accepted: '13:50', care: 'serious', decided: 6, line: 'Approved: export one week of the rota as CSV, without overwriting anything.',
        asked: 'A CSV of the week’s shifts that I can send to the harbour office, because they will not use the rota page.',
        fails: [['F1', 'An export replaced a file I had edited by hand', 'refuse to overwrite (R4)', 'serious'], ['F2', 'A shift was missing from the file', 'one row per stored shift (R1, R3)', 'serious'],
          ['F3', 'Times were wrong for a port in another zone', 'fixtures for every port zone (R5)', 'limited']],
        added: [['An audit-log line for every export', 'why: F1 · cost: one unit', 'keep']] },
      tests: { R1: ['TestShiftStorePerKeeperWeek', 'store_test.go:27: no such table: shifts'], R2: ['TestRotaEndpointServesWeek', 'handler_test.go:44: got 404, want 200'],
        R3: ['TestCSVOneRowPerShift', 'csv_test.go:19: got 0 rows, want 21'], R4: ['TestExportRefusesOverwrite', 'export_test.go:88: want ErrExists, got nil'],
        R5: ['TestFixturesCoverPortZones', 'fixtures_test.go:52: zone Europe/Lisbon: 2026 rule missing'] },
      rel: [['Merge the epic branch to main', 'revert the merge'], ['Deploy lighthouse to halyard', 'redeploy the previous build']],
      talk: [{ id: 'rotaplan', label: 'rota-planning', where: 'tmux pane · halyard', last: 'created the epic', at: '13:50', said: 'Why did csv-writer need a retry?', saidAt: '14:13',
        reply: 'Its first try, on haiku, made the same edit three times, so the monitor ended it at 13:58 and the planner retried on sonnet. The retry integrated, which proves R3.' },
      { id: 'lamp', label: 'lamp-telemetry', where: 'tmux pane · halyard', last: 'asked what it changes in internal/', at: '14:12', said: 'Does rota-export touch internal/telemetry?',
        reply: 'No. Its units own cli/, api/ and internal/rota/. Nothing in internal/telemetry is in any unit’s files.' }],
    },
    tidealerts: {
      para: 'A text the evening before any tide forecast above 4.5 m at the harbour, so the boat can be moved before the slipway floods. The number comes from config only, each tide sends one alert, and last year’s tables replay with none missed. A dry-run flag logs instead of sending, if you keep it.',
      contract: { accepted: '14:41', care: 'severe', decided: 1, steel: 'The steelman pass closed 3 findings.', line: 'Approved: a text the evening before any tide forecast above 4.5&nbsp;m at the harbour.',
        asked: 'A text the evening before a tide above 4.5&nbsp;m, because the slipway floods and the boat has to be moved.',
        fails: [['F1', 'An alert went to a number that is not mine', 'the number comes from config only (R3)', 'severe'], ['F2', 'A real high tide passed with no text', 'replay of last year\'s tables (R2)', 'serious'],
          ['F3', 'Three texts arrived for one tide', 'one alert per tide id (R4)', 'limited']],
        added: [['A dry-run flag that logs instead of sending', 'why: F1 · cost: one flag', 'keep']],
        ask: { id: 'D1', q: 'Which forecast counts as “above 4.5 m”?', opts: [
          { rec: 1, t: 'The harbour authority’s table', s: 'The slipway warden reads the same one, and tide-tables already fetches it every night.' },
          { t: 'The national model', s: 'Updated more often, but it reads 10 to 20 cm low at this harbour.' }] },
        prem: ['The forecast that tide-tables fetches each night is the one to read.', 'The SMS gateway account in config is yours and has credit.', '“The evening before” is 18:00 at the harbour, local time.'],
        wont: ['No alerts for any other port.', 'No second channel: no email and no push.'],
        alts: [['A nightly job on tarn reads the fetched forecast and sends through the SMS gateway.', 'chosen'], ['A push notification from the phone app.', 'not chosen: the phone is often off at the boat'],
          ['An email.', 'not chosen: you asked for a text']],
        chk: [['gate', '<code>just check</code> on every unit'], ['R2', 'last year’s tables replayed, with every tide above 4.5 m alerted once'], ['R3', 'a test that fails if a number is read from anywhere but config'],
          ['review', 'one reviewer per unit, and one over the whole epic']] },
      rel: [['Send texts through the SMS gateway', 'none, a sent text stays sent'], ['Deploy the alert job to tarn', 'disable the timer']],
      talk: [{ id: 'tideplan', label: 'tide-planning', where: 'tmux pane · tarn', last: 'wrote the contract', at: '14:15', said: 'Tighten F1: the number must come from config only.',
        reply: 'Done. R3 now says the number comes from config only, and F1 is severe. The contract is ready for you to accept.' }],
    },
    offline: {
      base: 'c2d90a4',
      para: 'Tide tables for the home port work with no signal. They are stored on the phone and refreshed when the network returns, by a worker that retries with backoff and never blocks the screen. The tables screen says how old its data is.',
      contract: { accepted: '12:08', care: 'serious', decided: 9, line: 'Approved: tide tables that work with no network, and say how old they are.',
        asked: 'The tables on the phone when there is no signal at the slipway.',
        fails: [['F1', 'The screen showed old tables as if they were current', 'the screen says how old its data is (R6)', 'serious'], ['F2', 'A sync froze the screen', 'sync off the main thread (R4)', 'limited']], added: [] },
      tests: { R1: ['storesTablesForHomePort', 'TableStoreTest.kt:33: expected 14 rows, found 0'], R2: ['refreshesWhenNetworkReturns', 'SyncTest.kt:58: no refresh after connectivity change'],
        R3: ['retriesWithBackoff', 'SyncTest.kt:91: 1 attempt, want 4'], R4: ['syncNeverBlocksMain', 'SyncTest.kt:120: ran on main'], R5: ['tablesScreenWorksOffline', 'ScreenTest.kt:41: empty state shown'] },
      findings: [{ id: 'I1', by: 'integration reviewer', verdict: 'closed', text: 'The table cache key leaves out the port, so two harbours share one cached table.',
        meta: ['R3', 'cache/key.go:41', 'verify: load two ports offline'], trail: ['found 13:40', 'confirmed 13:44', 'corrected 9be21f4', 'closure passed 14:02'] }],
      dropped: 'C2 conformance · dropped · the verifier could not reproduce it',
      talk: [{ id: 'offplan', label: 'offline-planning', where: 'headless · halyard', last: 'answered D9', at: '13:02' }],
    },
    orbitlabels: {
      base: '2f60d1c',
      para: 'Every part of the epic chart carries its name: stage names on the orbits, task names on the wedges, and a card for any body with its task, state and timing. The phone draws the same chart from the same cells, and the chart spec states the labels.',
      contract: { accepted: '13:56', care: 'limited', decided: 4, line: 'Approved: every orbit, body and requirement on the epic chart carries its name.',
        asked: 'I cannot tell what the rings mean without a key.',
        fails: [['F1', 'A label ran over its neighbour at phone width', 'the phone draws the same chart from the same cells (R3)', 'limited']], added: [] },
      tests: { R1: ['orbit names render', 'chart.test.js:14: expected 5 stage labels, found 0'], R2: ['hover shows task, state and timing', 'chart.test.js:40: no card'],
        R3: ['phone draws the same cells', 'ChartTest.kt:22: 0 cells, want 20'] },
      talk: [{ id: 'orbplan', label: 'orbit-planning', where: 'tmux pane · vermissian', last: 'created the epic', at: '13:56' }],
    },
    holder: {
      base: '0c7e4aa',
      para: 'Each holder runs in its own systemd scope, so a runaway one is killed alone and the terminal memory cap covers it. orreryd starts as Type=notify with a 900 s ceiling, and the devspaces doc no longer names an orrery version.',
      contract: { accepted: '08:04', care: 'serious', decided: 4, line: 'Approved: each holder in its own systemd scope, under the terminal memory cap.',
        asked: 'A runaway holder should not take the terminals down with it.',
        fails: [['F1', 'systemd killed orreryd while it was still starting', 'Type=notify with a 900 s ceiling (R3)', 'serious'], ['F2', 'The memory cap missed a holder', 'the cap covers holder scopes (R2)', 'serious']], added: [] },
      tests: { R1: ['TestHolderRunsInOwnScope', 'holder_test.go:71: unit holder-1.scope not found'], R2: ['TestMemoryCapCoversHolders', 'cap_test.go:38: MemoryMax unset on holder-1.scope'],
        R3: ['TestOrrerydNotifyStart', 'unit_test.go:19: Type=simple, want notify'] },
      relDone: [['Push tag v0.45.0, signed', 'github · 09:44 · signature good', 'none once someone has fetched the tag'], ['CI green on the tag', 'run 5490 · 09:51', 'rerun'],
        ['vermissian runs v0.45.0', '<code>orrery --version</code> · 09:52', 'switch to the previous generation']],
      talk: [{ id: 'holdplan', label: 'holder-planning', where: 'tmux pane · vermissian', last: 'created the epic', at: '08:04' }],
    },
    tidecache: {
      base: 'b7a3310',
      para: 'A disk cache for tide tables: tables are cached on disk, dropped after a week, and described in the docs.',
      contract: { accepted: '08:48', care: 'limited', decided: 1, line: 'Approved: a disk cache of tide tables, dropped after a week.', asked: 'Tables should load without the network.',
        fails: [['F1', 'A stale table was served', 'drop tables older than a week (R2)', 'limited']], added: [] },
      rel: [['Merge the epic branch to main', 'revert the merge']],
      talk: [],
    },
  };
  Object.entries(X).forEach(([key, x]) => {
    const e = EPICS[key];
    Object.assign(e, x);
    e.reqs.forEach((r) => { const t = (x.tests || {})[r.id]; if (t) { r.test = t[0]; r.red = t[1]; } });
  });

  // What a phase of a task put out, where the mockup has more than the task's own fields give: the gate's lines, the
  // judge's reason, earlier rounds. rows are [caption, body, tone, needs-data keys].
  const OUT = {
    rota: {
      'export-cli': {
        1: { sum: 'Round 3. The reviewer’s finding F1 came back into this session at 14:37, and it is adding the refuse-on-existing check and a test for it.',
          rows: [['Round 1 · 14:07 to 14:19 · returned done at 7c1e9aa', 'Sent back by the gate: exit 1.', 'bad'],
            ['Round 2 · 14:20 to 14:31 · returned done at e02b6c4', 'Passed the checks, the gate and the done-judge. Sent back by the reviewer at 14:37.', 'bad'],
            ['Round 3 · the build so far', pre('$ go test ./cli/... -run Export\n--- FAIL: TestExportRefusesSecondRun (0.00s)\n    export_test.go:121: second export replaced the file\nFAIL\tlighthouse/cli\t0.41s'), 'run', ['stage']]] },
        2: { sum: 'Round 1 failed the gate. Round 2 passed the five checks and the gate in 3:58, and the done-judge ruled the goal met.',
          gate: '$ just check\nok  \tlighthouse/cli\t0.44s\nok  \tlighthouse/internal/rota\t1.92s\ngolangci-lint: 0 issues',
          judge: '<code>TestExportRefusesOverwrite</code> fails at the parent commit and passes at <code>e02b6c4</code>, and <code>Export</code> returns ErrExists for a file that is there.',
          rows: [['Round 1 · gate <code>just check</code> · exit 1 · 14:19', pre('cli/export_test.go:88: want ErrExists, got nil\nFAIL\tlighthouse/cli\t0.38s\nerror: recipe `check` failed with exit code 1'), 'bad', ['detail']]] },
        3: { sum: 'The reviewer confirmed one finding and sent the task back at 14:37.',
          rows: [['Verifier · reproduced 14:36', 'Ran <code>rota export</code> twice into one path. The second run replaced the file.', '']] },
      },
      'tz-fixtures': { 1: { red: true, rows: [['Round 1 · 14:22 to 14:39 · paused, not returned', 'Fixtures for 11 of 12 port zones are written. Lisbon needs the 2026 rule, which the pinned tz database lacks.', 'mine']] } },
      'rota-handler': { 2: { gate: '$ just check\nok  \tlighthouse/internal/rota\t1.88s\nok  \tlighthouse/api\t0.71s\ngolangci-lint: 0 issues' },
        4: { sum: 'The engine is merging it onto the epic branch and running the gate there once more.' } },
      'csv-writer': { 1: { rows: [['Try 1 · haiku · ended looping at 13:58', 'The monitor saw the same edit three times. It was nudged, ended, and retried on sonnet.', 'bad']] } },
    },
  };

  // What an epic asks of the owner, by moment. A decision is a form: options, one recommended, the line that will be
  // recorded, one confirm button. A failed release step and a held step are buttons.
  const ASK = {
    rota: { 0: { kind: 'decision', head: 'tz-fixtures is paused on your decision', since: '2 min', title: 'Bump the embedded tz database?', id: 'D7', to: 1,
      body: 'The pinned database lacks the 2026 Lisbon rule, so one fixture cannot pass. A bump changes <code>go.mod</code>, which is outside this unit’s owned files, and the contract says no dependency changes.',
      bears: ['R5 fixtures cover every port zone', 'won’t do: dependency changes'], confirm: 'Record the decision and resume tz-fixtures', said: 'recorded in decisions.md. tz-fixtures is running again',
      opts: [{ rec: 1, t: 'Keep the pin and skip the Lisbon case', s: 'The contract holds. R5 is met for every port on today’s rota.', line: 'keep the tz pin; skip Lisbon 2026 (R5)' },
        { t: 'Allow the bump', s: 'A contract change. Adds go.mod to this unit and reruns the full gate.', line: 'allow the tz bump; go.mod joins tz-fixtures (contract change)' },
        { t: 'Something else', s: 'Type an answer for the planner.', line: '…' }] } },
    followups: {
      3: { kind: 'decision', head: 'judge-head-tail ended short and waits on your answer', since: '1 min', title: 'Keep it as a gap, or try again?', id: 'D3', to: 4,
        body: `The done-judge ruled its goal unmet twice. ${UNMET} The commit <code>166091a</code> is kept on the task’s branch.`,
        bears: ['R1 the judge sees a long section’s head and tail'], confirm: 'Record the answer', said: 'recorded in decisions.md. judge-head-tail is kept as gap G1',
        opts: [{ rec: 1, t: 'Keep it as a gap', s: 'R1 stays unproven and the epic ends with 1 gap. The other two tasks ship.', line: 'keep judge-head-tail as gap G1; no second try (R1)' },
          { t: 'Try again on sonnet', s: 'A new step with both verdicts as its prompt. About 15 min more.', line: 'retry judge-head-tail on sonnet (R1)' }] },
      6: { kind: 'held', head: 'Nothing is running', since: 'since 10:44', to: 2,
        rows: [['judge-head-tail', 'held by you at 10:44, mid-turn', 'Resume'], ['planner-reset-unknown', 'paused by a 5-hour usage limit, which resets at 11:30', 'Resume after 11:30']] },
    },
    offline: { 0: { kind: 'release', head: 'Release step 2 failed twice', since: '9 min', to: 1,
      body: 'CI is red on the tag v0.4.0: run 5519, <code>go vet: cache/key_test.go:41</code>. orrery retried once by itself at 14:21. A fix would move the tag, and that push cannot be undone.' } },
    tidealerts: { 0: { kind: 'contract', head: 'The contract is ready for you to accept', since: '26 min', body: 'Severe care. 1 decision and 1 added row are yours to settle first.' } },
  };
  const askOf = (key) => (ASK[key] || {})[index(key)];

  // Moments that exist so a form's answer has somewhere to go.
  {
    const m0 = EPICS.rota.moments[0];
    EPICS.rota.moments.push({ ...m0, at: '14:42', label: 'decided', now: 'Three units are being worked on.', exc: null,
      units: m0.units.map((u) => (u.slug !== 'tz-fixtures' ? u : { ...u, ask: false, pane: 'generic', state: 'now', stage: 'Implementing', round: 'round 1', clock: 6, when: '', whenLabel: '',
        phrase: 'implementing', tag: 'implementing', doing: 'Leaving the Lisbon 2026 case out, as you decided, and finishing the other fixtures.', track: 'n-----', times: { 0: '0:06' },
        sentence: 'You answered at 14:42: keep the tz pin and skip Lisbon 2026. The unit resumed in the same session.', next: 'Next: its return, then the checks and the gate.' })) });
    EPICS.rota.events.push(E('14:42', 'decision', 'tz-fixtures', 'You answered: keep the tz pin and skip Lisbon 2026, so the unit resumed.', 'Recorded as D7 in decisions.md.', { from: 1, tone: 'ok' }));
    EPICS.tidealerts.moments.push({ at: '14:42', phase: 'planning', word: 'Planning.', since: '1 min', now: 'You accepted the contract at 14:41, and the planner is outlining the units.',
      next: 'Each unit starts by writing a test that fails for its requirement.', units: [] });
    EPICS.tidealerts.events.push(E('14:41', 'you', '', 'You accepted the contract: 5 requirements, severe care.', '', { from: 1 }),
      E('14:42', 'planner', '', 'Planner turn 1 started, because the contract was accepted.', '', { from: 1 }));
    const step = (name, st, note, undo, clock) => ({ name, state: st, note, undo, clock });
    EPICS.offline.moments[0].release = { ask: true, steps: [step('Push main and tag v0.4.0, signed', 'done', 'github · 14:05 · signature good', 'none once someone has fetched the tag'),
      step('CI green on the tag', 'fail', 'red twice · run 5519 · 14:32 · go vet: cache/key_test.go:41', 'rerun'), step('Publish the APK to the release page', 'todo', 'after step 2', 'delete the release asset')] };
    EPICS.offline.moments[1].release = { steps: [step('Push main and tag v0.4.0, signed', 'done', 'github · 14:05 · signature good', 'none once someone has fetched the tag'),
      step('CI green on the tag', 'run', 'run 5524 · try 3, the one you asked for', 'rerun', 14), step('Publish the APK to the release page', 'todo', 'after step 2', 'delete the release asset')] };
    EPICS.orbitlabels.moments[0].release = { steps: [step('Merge the epic branch to main', 'done', 'fast-forward to 7ab4411 · 14:38', 'reset main to 2f60d1c, before anyone pulls'),
      step('Push tag v0.46.0, signed', 'run', 'github', 'none once someone has fetched the tag', 22), step('CI green on the tag', 'todo', 'after step 2', 'rerun')] };
  }

  // ---------- time, and the epic's state in a word ----------
  const mins = (hhmm) => { const [h, mm] = hhmm.split(':').map(Number); return h * 60 + mm; };
  const dur = (n) => (n < 60 ? `${n} min` : `${Math.floor(n / 60)} h ${n % 60} min`);
  const upper = (s) => s.charAt(0).toUpperCase() + s.slice(1);
  // The state, how long it has held, and what blocks it. The title row and the list line both say this.
  function status(key) {
    const e = EPICS[key], m = cur(key), x = m.exc, rel = m.release;
    if (m.ended) return { word: m.ended.head.replace(/\.$/, '').toLowerCase(), since: `${m.ended.when} · ran ${m.ended.took}`, tone: { released: 'done', gaps: 'bad', stopped: 'idle' }[m.ended.kind], block: null };
    if (paused[key]) return { word: 'paused by you', since: paused[key], tone: 'wait', block: null };
    const word = { contract: 'contract written', planning: 'planning', releasing: rel && rel.ask ? 'release stopped' : 'releasing' }[m.phase] || 'building';
    const since = m.since || (e.started ? dur(Math.max(1, mins(m.at) - mins(e.started))) : '');
    return { word, since, tone: '', block: x && x.tone === 'mine' ? { text: x.block, since: x.since } : null };
  }

  // ---------- the orrery ----------
  // Orbits are phases, outermost first: planned, implementing, checking, reviewing. The centre ring is integrated. A
  // wedge is a task and a slice of it is one phase of that task. A phase behind the task carries a tick, the phase it
  // is in is lit and says what it is doing, and a phase in progress glows. The outer ring is the requirements. Every
  // slice opens a card on hover and its phase's output on a click.
  const OG = { C: 270, hole: 60, donut: [64, 82], bands: [[182, 210], [150, 178], [118, 146], [86, 114]], name: 216, req: 237, reqText: 245 };
  const G = geo(OG.C);
  const CW = 0.6;                                         // Maple Mono's advance, in ems
  const PH = ['planned', 'implementing', 'checking', 'reviewing', 'integrating'];
  const SIGN = { run: '', mine: 'Ⅱ ', bad: '✗ ', acc: '✓ ', stop: '■ ' };
  let uid = 0;
  function orrery(key, o = {}) {
    const e = EPICS[key], m = cur(key), W = wedges(key), N = W.length, small = !!o.small, real = W.filter((w) => !w.u.ghost);
    const f = small ? { name: 16, stage: 15, cell: 13.5, req: 14.5 } : { name: 13, stage: 12, cell: 10.5, req: 12 };
    const GAP = rad(small ? 56 : 42), span = (TAU - GAP) / N, a0 = (i) => GAP / 2 + i * span, id = `or${uid += 1}`;
    const up = (a) => Math.cos(a) > 0;                    // words on this side read clockwise
    const fits = (text, font, room) => text.length * CW * font <= room;
    const defs = [`<filter id="${id}g" filterUnits="userSpaceOnUse" x="0" y="0" width="${OG.C * 2}" height="${OG.C * 2}"><feGaussianBlur stdDeviation="${small ? 9 : 7}"/></filter>`], out = [], glows = [], marks = [];
    // Words along an arc at radius r, centred between two angles.
    const along = (cls, text, r, font, s, t, html) => {
      const top = up((s + t) / 2), pid = `${id}p${defs.length}`;
      defs.push(`<path id="${pid}" d="${G.arc(top ? r : r + 0.72 * font, s, t, !top)}"/>`);
      return `<text class="${cls}" font-size="${font}"><textPath href="#${pid}" startOffset="50%" text-anchor="middle">${html || text}</textPath></text>`;
    };

    // The phase names, written on their orbits in the gap at the top.
    OG.bands.forEach(([ri, ro], b) => {
      const here = real.some((w) => w.band === b && w.tone === 'run'), mid = (ri + ro) / 2;
      out.push(`<g class="o-stg${here ? ' on' : ''}"><path class="o-key" d="${G.sector(ri, ro, -GAP / 2, GAP / 2, 3, 3)}"/>`
        + `<text class="o-st" x="${OG.C}" y="${(OG.C - mid + f.stage * 0.35).toFixed(1)}" font-size="${f.stage}">${PH[b]}</text></g>`);
    });

    // The wedges, slice by slice.
    W.forEach((w, i) => {
      const u = w.u, s = a0(i), t = a0(i + 1), am = (s + t) / 2, ref = u.ghost ? '' : `${key}:${u.slug}`;
      const slice = (b, ri, ro) => {
        const st = b === 4 ? (w.tone === 'ok' ? 'ok' : w.band === 4 ? w.tone : 'off') : cellState(b, w), d = G.sector(ri, ro, s, t, 3, 3), mid = (ri + ro) / 2;
        // The glow and the words sit above every slice, so a neighbour never covers them.
        if (st === 'run') glows.push(`<path class="o-glow" filter="url(#${id}g)" d="${d}"/>`);
        const h = `<path class="ocell ${b === 4 ? 'od' : 'oc'}-${st}" data-cell="${key}:${u.slug || i}:${b}" data-st="${st}"${ref ? ` data-oc="ph:${ref}:${b}" data-go="task:${ref}:${b}"` : ''} d="${d}"/>`;
        if (st === 'trail' || st === 'ok') { const [cx, cy] = G.at(mid, am); marks.push(`<g class="w-${w.tone}"><path class="o-tick${st === 'ok' ? ' on' : ''}" d="M${(cx - 3.6).toFixed(1)} ${(cy + 0.2).toFixed(1)}l2.6 2.7 4.5-5.5"/></g>`); }
        const room = span * mid - 14;
        if (b === w.band && st in SIGN) {
          // The lit slice says what the task is doing and for how long, as far as the slice has room.
          const words = `${SIGN[st]}${u.tag || u.phrase || word(w)}`, now = u.clock != null ? mmss(u.clock + elapsed()) : '';
          const tries = [now && [`${words} ${now}`, `${words} ${ticking(u.clock, 'tspan')}`], [words, words], now && [now, ticking(u.clock, 'tspan')], SIGN[st] && [SIGN[st].trim(), SIGN[st].trim()]];
          const hit = tries.find((x) => x && fits(x[0], f.cell, room));
          if (hit) marks.push(along(`o-ph on-${st}`, hit[0], mid - 0.36 * f.cell, f.cell, s, t, hit[1]));
        }
        if (st === 'bounce' && fits('sent back', f.cell, room)) marks.push(along('o-ph on-bounce', 'sent back', mid - 0.36 * f.cell, f.cell, s, t));
        return h;
      };
      let g = `<g class="ow w-${w.tone}">${OG.bands.map(([ri, ro], b) => slice(b, ri, ro)).join('')}${slice(4, OG.donut[0], OG.donut[1])}`;
      if (!u.ghost) {
        // The task's name, on the ring just outside its wedge. It leads where the task's lit slice leads.
        const room = Math.floor((span * OG.name - 12) / (CW * f.name));
        const name = u.slug.length > room ? `${u.slug.slice(0, Math.max(3, room - 1))}…` : u.slug;
        g += `<g class="o-nm" data-oc="ph:${ref}:${w.band}" data-go="task:${ref}:${w.band}">${along('o-name', name, OG.name, f.name, s, t)}</g>`;
      }
      out.push(`${g}</g>`);
    });
    out.push(glows.join(''), marks.join(''));

    // The outer ring: one arc per requirement, across the wedges of the tasks that prove it.
    const segs = [];
    W.forEach((w, i) => {
      const ids = reqsOf(w.u);
      ids.forEach((rid, j) => {
        const s = a0(i) + (span * j) / ids.length, t = a0(i) + (span * (j + 1)) / ids.length, last = segs[segs.length - 1];
        if (last && last.id === rid) last.t = t; else segs.push({ id: rid, s, t, w });
      });
    });
    segs.forEach(({ id: rid, s, t, w }) => {
      const st = reqState(key, rid), pad = rad(1.3), room = (t - s) * OG.reqText - 14;
      const label = [`${rid} ${REQ_WORD[st]}`, st === 'proven' ? `${rid} ✓` : /gap|unmet/.test(st) ? `${rid} ✗` : rid, rid].find((text) => fits(text, f.req, room)) || '';
      out.push(`<g class="oq s-${st}" data-oc="req:${key}:${rid}"${w.u.ghost ? '' : ` data-go="task:${key}:${w.u.slug}:${w.band}"`}><path class="hit" d="${G.arc(OG.reqText + 4, s, t)}"/>`
        + `<path class="q" data-cell="${key}:q:${rid}" data-st="${st}" d="${G.arc(OG.req, s + pad, t - pad)}"/>${label ? along('o-rq', label, OG.reqText, f.req, s, t) : ''}</g>`);
    });

    // The centre says what its numbers count.
    const c = counts(key), mine = real.filter((w) => w.tone === 'mine').length, bad = real.filter((w) => w.tone === 'bad').length, run = real.filter((w) => w.tone === 'run').length;
    const [big, l2, l3, tone] = !real.length ? [`0 ${e.noun}s`, m.phase === 'planning' ? 'the planner is' : 'nothing is built', m.phase === 'planning' ? 'outlining them' : 'until you accept', m.phase === 'planning' ? 'run' : 'mine']
      : [`${c.done} of ${c.tasks}`, `${e.noun}s integrated`,
        m.ended ? { released: 'released', gaps: `${c.gaps} gap`, stopped: 'stopped' }[m.ended.kind]
          : mine ? `${mine} ${mine > 1 ? 'wait' : 'waits'} on you` : bad ? `${bad} ended short` : run ? `${run} running` : m.release ? (m.release.ask ? 'release stopped' : 'releasing') : '',
        m.ended ? { released: 'ok', gaps: 'bad', stopped: 'dim' }[m.ended.kind] : mine ? 'mine' : bad || (m.release && m.release.ask) ? 'bad' : 'run'];
    const bf = Math.min(small ? 28 : 26, 104 / (CW * big.length));
    out.push(`<g class="o-core" data-oc="core:${key}"><circle class="hit" cx="${OG.C}" cy="${OG.C}" r="${OG.hole}"/>`
      + `<text class="big" x="${OG.C}" y="${OG.C - (small ? 6 : 5)}" font-size="${bf.toFixed(1)}">${big}</text>`
      + `<text class="dim" x="${OG.C}" y="${OG.C + 13}" font-size="${small ? 13.5 : 10.5}">${l2}</text>`
      + (l3 ? `<text class="t-${tone}" x="${OG.C}" y="${OG.C + (small ? 29 : 30)}" font-size="${small ? 13.5 : 10.5}">${l3}</text>` : '') + '</g>');

    const label = `${e.slug}: ${real.length ? `${c.done} of ${c.tasks} ${e.noun}s integrated. ${real.map((w) => `${w.u.slug} ${word(w)}`).join(', ')}.` : `no ${e.noun}s yet.`} ${c.proven} of ${c.total} requirements proven.`;
    return `<svg class="orr${small ? ' small' : ''}" viewBox="0 0 ${OG.C * 2} ${OG.C * 2}" role="img" aria-label="${label}"><defs>${defs.join('')}</defs>${out.join('')}</svg>`;
  }

  // The same shape, small, for a list line: a wedge per task, filled from the rim toward the centre as far as the task has come.
  const GG = geo(16);
  function glyph(key) {
    const W = wedges(key), N = W.length, span = TAU / N, r0 = 4.8, r1 = 15, step = (r1 - r0) / 4, m = cur(key);
    const mine = (m.exc && m.exc.tone === 'mine') || W.some((w) => w.tone === 'mine'), bad = W.some((w) => w.tone === 'bad') || (m.exc && m.exc.tone === 'bad');
    const hub = mine ? 'mine' : bad ? 'bad' : W.every((w) => w.tone === 'ok') ? 'ok' : W.some((w) => w.tone === 'run') ? 'run' : 'plan';
    return `<svg class="gly" viewBox="0 0 32 32" aria-hidden="true">${W.map((w, i) => {
      const s = i * span, t = (i + 1) * span - (N > 1 ? 0 : 0.001), g = N > 1 ? 1.5 : 0;
      return `<path class="g-track${w.tone === 'ghost' || w.tone === 'plan' ? ' plan' : ''}" d="${GG.sector(r0, r1, s, t, g, 0)}"/>`
        + (w.band > 0 ? `<path class="g-${w.tone}" d="${GG.sector(r1 - w.band * step, r1, s, t, g, 0)}"/>` : '');
    }).join('')}<circle class="g-hub ${hub}" cx="16" cy="16" r="3"/></svg>`;
  }

  // ---------- the list line ----------
  // Its state and how long, how far along, and what blocks it with how long that has stood.
  function line(key) {
    const e = EPICS[key], m = cur(key), c = counts(key), s = status(key);
    const base = { key, slug: e.slug, proj: e.proj, host: e.host, count: c, ended: !!m.ended, exc: null };
    if (m.ended) return { ...base, phrase: `${m.ended.words} <span class="at">${m.ended.when}</span>`, plain: m.ended.words };
    const rel = m.release, at = rel ? rel.steps.findIndex((x) => x.state !== 'done') + 1 : 0, head = `${upper(s.word)} ${s.since}`;
    const phrase = paused[key] ? `${head} · running steps finish their turn`
      : m.phase === 'contract' ? `${head} · nothing is built until you accept it`
        : m.phase === 'planning' ? `${head} · the planner is outlining the ${e.noun}s`
          : rel ? `${head} · ${rel.ask ? `step ${at} of ${rel.steps.length} failed` : `${m.act}, step ${at} of ${rel.steps.length}`}`
            : `${head} · <b>${c.done} of ${c.tasks}</b> ${e.noun}s integrated${c.run ? ` · ${c.run} running` : ''}`;
    return { ...base, phrase, plain: plain(phrase), exc: m.exc ? { tone: m.exc.tone, text: `${m.exc.short}${m.exc.since ? ` <span class="at">${m.exc.since}</span>` : ''}` } : null };
  }
  const countShort = (key) => { const c = counts(key); return `<b>${c.proven} of ${c.total}</b> proven`; };

  // ---------- routes ----------
  // A page is named by a route: home, epic:<key>, contract:<key>, task:<key>:<slug>:<phase>, session:<key>:<slug>:<phase>,
  // talk:<key>:<session>, sess:<id>. Anything with data-go leads to one. Back is the route's parent.
  const unit = (key, slug) => cur(key).units.find((u) => u.slug === slug);
  const wedge = (key, slug) => wedges(key).find((w) => w.u.slug === slug);
  // The phase a link lands on: the one asked for if the task has reached it, else the one it is in.
  function landing(key, slug, b) {
    const w = wedge(key, slug);
    if (!w) return 0;
    const p = phaseOf(key, slug, Number(b));
    return p && p.reached && Number(b) > 0 ? Number(b) : w.tone === 'ok' ? 4 : w.band;
  }
  const parent = (go) => {
    const [p, a, b, c] = go.split(':');
    return p === 'home' ? '' : p === 'contract' || p === 'task' || p === 'talk' ? `epic:${a}` : p === 'session' ? `task:${a}:${b}:${c}` : 'home';
  };
  // A session that touched the epic, by what it has been doing and where. The pane's label is kept for the phone.
  const talkOf = (key, sid) => {
    if (sid === 'new') return { id: 'new', label: 'new session', sum: 'New session', place: `${EPICS[key].folder} · ${EPICS[key].host}`, where: `${EPICS[key].folder} · ${EPICS[key].host}`, fresh: true };
    const t = EPICS[key].talk.find((s) => s.id === sid), s = SESS[sid];
    return { ...t, sum: s ? s.sum : t.label, place: s ? `${s.proj} · ${s.host}` : t.where };
  };
  function crumbsOf(go) {
    const [p, a, b, c] = go.split(':'), out = [{ t: 'orrery', go: 'home' }];
    if (p === 'sess') out.push({ t: SESS[a].label, go, sum: SESS[a].sum });
    else if (p !== 'home') {
      out.push({ t: EPICS[a].slug, go: `epic:${a}` });
      if (p === 'contract') out.push({ t: 'contract', go });
      if (p === 'talk') out.push({ t: `talk · ${talkOf(a, b).label}`, go });
      if (p === 'task' || p === 'session') { const at = landing(a, b, c); out.push({ t: `${b} · ${at === 4 && wedge(a, b).tone === 'ok' ? 'integrated' : PH[at]}`, go: `task:${a}:${b}:${at}` }); }
      if (p === 'session') out.push({ t: `${Number(c) === 3 ? 'reviewer' : 'implementer'} session`, go });
    }
    return out;
  }

  // ---------- the frame: every page's title row ----------
  // Back, where you are, the state and how long it has held, and the commands as buttons.
  const sess = { answered: 0 };                           // the answer given to beacon-schedule's question, on either page
  // A conversation that waited on its contract is done with its turn once the contract is accepted.
  const sessState = (id) => (resumed[id] ? 'working' : id === 'beacon' && sess.answered ? 'working'
    : SESS[id].pane === 'director' && SESS[id].state === 'waiting' && cur(SESS[id].epic).phase !== 'contract' ? 'done' : SESS[id].state);
  const btn = (text, attrs, cls = '') => `<button class="fr-btn ${cls}" ${attrs}>${text}</button>`;
  // A task's commands follow from its state: hold a turn that is running, resume what is held, retry what ended short.
  function taskActs(key, w) {
    const u = w.u, id = `${key}:${u.slug}`;
    if (armed[id]) return `<span class="fr-sure">Stop ${u.slug}? It ends as stopped, and its branch is kept.</span>` + btn(`Stop ${u.slug}`, `data-cmd="stop-step-yes" data-id="${id}"`, 'primary') + btn('Keep it', `data-cmd="stop-step-no" data-id="${id}"`);
    if (u.ask) return btn('Answer ›', `data-go="epic:${key}"`, 'primary');
    if (w.tone === 'mine') return btn(u.mine === 'limit' ? 'Resume after 11:30' : 'Resume', `data-cmd="resume-step" data-id="${id}"`, 'primary') + (u.mine === 'limit' ? '' : btn('Stop…', `data-cmd="stop-step" data-id="${id}"`));
    if (w.tone === 'run') return (w.band === 1 ? btn('Hold', `data-cmd="hold" data-id="${id}"`) : '') + btn('Stop…', `data-cmd="stop-step" data-id="${id}"`);
    if (w.tone === 'bad' && !cur(key).ended && !cur(key).gaps) return btn('Retry', `data-cmd="retry-step" data-id="${id}"`);
    return '';
  }
  function facts(go) {
    const [p, a, b, c] = go.split(':');
    if (p === 'sess') {
      const s = SESS[a], st = sessState(a);
      const text = st === 'working' ? (s.tool && !resumed[a] ? `working ${ticking(72)}<span class="tool">· <b>${s.tool[0]}</b> ${s.tool[1]} ${ticking(s.tool[2])}</span>` : 'working') : `${st} ${s.age}`;
      return { pills: [[text, st === 'working' ? '' : st === 'done' ? 'done' : 'wait']], acts: st === 'interrupted' ? btn('Resume', `data-resume="${a}"`, 'primary') + need(['resume']) : '' };
    }
    const e = EPICS[a], m = cur(a);
    if (p === 'epic') {
      const s = status(a), pills = [[`${s.word} · ${s.since}`, s.tone]];
      if (s.block) pills.push([`${s.block.text} · ${s.block.since}`, 'wait']);
      const acts = btn(`${icon('talk')}Talk`, `data-talk="${a}" aria-haspopup="menu"`) + (m.ended || m.phase === 'contract' ? ''
        : (paused[a] ? btn('Resume', `data-cmd="resume" data-id="${a}"`, 'primary') : btn('Pause', `data-cmd="pause" data-id="${a}"`)) + btn('Stop…', `data-cmd="stop" data-id="${a}"`));
      return { pills, acts: acts + need(['since', 'cmd']) };
    }
    if (p === 'contract') { const k = contractOf(a); return { pills: [k.waiting ? ['waiting for you · 26 min', 'wait'] : [`accepted ${k.accepted}`, 'done']], acts: '' }; }
    if (p === 'talk') return { pills: [], acts: '' };
    const w = wedge(a, b), u = w.u, ph = phaseOf(a, b, landing(a, b, c));
    const tone = { run: '', mine: 'wait', bad: 'bad', ok: 'done', acc: 'done' }[w.tone] || 'idle';
    const time = u.clock != null ? ticking(u.clock) : u.when || '';
    if (p === 'session') {
      const st = ph.session ? ph.session.state : 'ended';
      return { pills: [[`${st}${st !== 'ended' && time ? ` ${time}` : ''}`, st === 'ended' ? 'idle' : tone]], acts: '' };
    }
    return { pills: [[`${u.tag || u.phrase || word(w)}${time ? ` · ${time}` : ''}`, tone]], acts: taskActs(a, w) };
  }
  function frame(go) {
    const cs = crumbsOf(go), up = parent(go), f = facts(go);
    return `<button class="fr-back" data-go="${up}"${up ? ` aria-label="back to ${crumbsOf(up).at(-1).t}"` : ' disabled aria-label="back"'}>‹</button>`
      + `<nav class="fr-crumbs" aria-label="where you are">${cs.map((x, i) => (i === cs.length - 1 ? `<b>${x.t}</b>` : `<button data-go="${x.go}">${x.t}</button><i>›</i>`)).join('')}</nav>`
      + `<div class="fr-row">${f.pills.map(([text, tone]) => `<span class="b-pill ${tone}">${text}</span>`).join('')}<span class="fr-acts">${f.acts}</span></div>`;
  }

  // ---------- the web's title row: where you are, its state, its commands, and under them the page's own meters ----------
  // A commit is evidence, not a name. It is drawn as a small mark, and the short hash is its hover text.
  const refs = (html) => html.replace(/(?:\s(?:at|to))?\s?(?:<code>)?\b(?=[0-9a-f]*\d)(?=[0-9a-f]*[a-f])([0-9a-f]{7})\b(?:<\/code>)?(?![^<]*>)/g, ' <span class="ref" title="commit $1">⎇</span>');
  const kilo = (n) => (n >= 1e6 ? `${n / 1e6}M` : `${Math.round(n / 1000)}k`);
  // The context meter is the phone's: five blocks, each a fifth of the window, then the percent. Green below 40%, yellow
  // below 60%, peach below 80%, red from there.
  function ctxMeter(ctx) {
    if (!ctx) return '';
    const [tokens, window] = ctx, pct = Math.max(0, Math.min(100, Math.floor((100 * tokens) / window))), filled = Math.min(5, Math.floor((2 * pct + 20) / 40));
    const band = pct < 40 ? 'ok' : pct < 60 ? 'warm' : pct < 80 ? 'high' : 'full';
    return `<span class="cx ${band}" title="${kilo(tokens)} of ${kilo(window)} tokens of this session’s context are in use"><em>context</em><b class="blk">${'▰'.repeat(filled)}${'▱'.repeat(5 - filled)}</b><b>${pct}%</b><small>${kilo(tokens)} of ${kilo(window)}</small></span>`;
  }
  // The host's stats as it reports them: a label over nothing, a value, and a bar when the value is a percent.
  const hostMeters = (host) => `<span class="mt-host" title="the host this page runs on">${host}</span>` + (HOSTS[host] || []).map(([label, value, pct, level]) =>
    `<span class="mt${level ? ` ${level}` : ''}"><em>${label}</em>${pct != null ? `<i style="--v:${pct}%"></i>` : ''}<b>${value}</b></span>`).join('');
  // A conversation that carries an epic keeps a small line to it in the title row, wherever the conversation is scrolled.
  const epicChip = (key) => { const st = status(key); return `<button class="fr-epic" data-go="epic:${key}" title="open the epic">${glyph(key)}<b>${EPICS[key].slug}</b><span>${st.word}${st.block ? ` · ${st.block.text}` : ''}</span><i>›</i></button>`; };
  function instruments(go) {
    const [p, a, b] = go.split(':'), s = p === 'sess' ? SESS[a] : null;
    const ctx = s ? s.ctx : p === 'session' ? (unit(a, b) || {}).ctx || [58000, 200000] : null;
    return `<span class="fr-meters">${hostMeters(s ? s.host : EPICS[a].host)}${need(['hoststat'])}</span>${s && s.epic ? epicChip(s.epic) : ''}${ctxMeter(ctx)}`;
  }
  const MARK_SVG = '<svg class="mark" viewBox="18 18 72 72" aria-hidden="true"><use href="#mark"/></svg>';
  // The orrery mark is the first crumb: it stands for the list. Then the directory, then what the page is.
  function head(go) {
    const [p, a] = go.split(':'), cs = crumbsOf(go).slice(1), up = parent(go), f = facts(go), s = p === 'sess' ? SESS[a] : null;
    const where = s ? `<b class="fr-sum" title="${plain(s.sum)}">${s.sum}</b>${need(['title'])}`
      : cs.map((x, i) => (i === cs.length - 1 ? `<b>${x.t}</b>` : `<button data-go="${x.go}">${x.t}</button><i>›</i>`)).join('');
    return `<div class="fr-top"><button class="fr-home" data-inbox aria-label="the inbox">${MARK_SVG}<i class="fr-n" data-n="need"></i></button>`
      + (up && up !== 'home' ? `<button class="fr-back" data-go="${up}" aria-label="back to ${crumbsOf(up).at(-1).t}">‹</button>` : '')
      + `<nav class="fr-crumbs" aria-label="where you are"><span class="fr-dir">${s ? s.proj : EPICS[a].proj}</span>${where}</nav>`
      + `<div class="fr-row">${f.pills.map(([text, tone]) => `<span class="b-pill ${tone}">${text}</span>`).join('')}<span class="fr-acts">${f.acts}</span>`
      + '<button class="fr-btn ask" data-ask-open><b>+</b> Ask</button></div></div>'
      + `<div class="fr-strip" data-strip>${instruments(go)}</div>`;
  }

  // ---------- the epic view ----------
  // The marker beside the live sentence says how fresh it is.
  const agoText = (key) => {
    const secs = stamp[key] ? Math.floor((Date.now() - stamp[key]) / 1000) : 12 + elapsed();
    return secs < 4 ? 'just now' : secs < 90 ? `${secs} s ago` : `${Math.floor(secs / 60)} min ago`;
  };
  const live = (key) => {
    const m = cur(key);
    return m.ended ? '' : `<span class="live" data-live="${key}"><i></i><span data-xf="asof:${key}">as of ${m.at}</span> · updated <span data-ago="${key}">${agoText(key)}</span></span>`;
  };
  const sec = (key, id, state, title, count, body, keys) => `<section class="ev-sec ${id} ${state}" data-sec="${id}" data-flip="sec:${key}:${id}"><h3><b>${title}</b><span data-xf="h:${key}:${id}">${count}</span>${need(keys)}</h3>${body}</section>`;

  // The summary paragraph is the door to the contract, before and after it is accepted.
  function paraBlock(key) {
    const e = EPICS[key], k = contractOf(key);
    return `<button class="ev-para" data-go="contract:${key}" data-flip="para:${key}"><span class="p">${e.para}${need(['para'])}</span>`
      + `<span class="ev-ct">${icon('contract')}<b>Contract</b><span data-xf="ct:${key}">${k.waiting ? '<em>waiting for you to accept</em>' : `accepted ${k.accepted}`} · ${k.care} care · ${e.reqs.length} requirements</span><i>›</i></span></button>`
      + fromLine(key);
  }
  const fromOf = (key) => (EPICS[key].talk.find((x) => SESS[x.id] && SESS[x.id].epic === key) || {}).id || '';
  // Where the epic came from: the conversation that wrote its contract and created it.
  function fromLine(key) {
    const t = EPICS[key].talk.find((x) => x.id === fromOf(key));
    if (!t) return '';
    const s = SESS[t.id];
    return `<button class="ev-from" data-go="sess:${t.id}">${icon('talk')}<b>Conversation</b><span>${s.proj} · ${s.sum}</span><em>${t.last} ${t.at}</em><i>›</i>${need(['origin'])}</button>`;
  }

  // What the epic asks of the owner: a form for a question, buttons for a command.
  const askHead = (kind, a) => `<div class="ak-h"><i class="nk">${icon(KIND[kind] ? KIND[kind].icon : 'hold')}</i><b>${a.head}</b><span class="ak-t">${a.since}</span>${need(kind === 'held' ? ['limit', 'cmd'] : ['ask'])}</div>`;
  function ask(key) {
    const e = EPICS[key], m = cur(key), a = askOf(key), x = m.exc;
    if (armed[key]) {
      return `<div class="ev-ask k-release" data-flip="ask:${key}"><div class="ak-h"><i class="nk">${icon('end')}</i><b>Stop ${e.slug}?</b></div>`
        + `<p>Running steps are stopped and their branches kept. Nothing is released, and the epic ends as stopped.</p>`
        + `<div class="ak-acts"><button class="nd-act primary" data-cmd="stop-yes" data-id="${key}">Stop ${e.slug}</button><button class="nd-act" data-cmd="stop-no" data-id="${key}">Keep it running</button></div></div>`;
    }
    // A pause is the owner's own doing, and Resume undoes it. What the epic asks stays open under it.
    const held = paused[key] ? `<div class="ev-ask k-decision" data-flip="pause:${key}"><div class="ak-h"><i class="nk">${icon('hold')}</i><b>Paused by you</b><span class="ak-t">${paused[key]}</span></div>`
      + `<p>Running steps finish their turn. Nothing new starts.</p><div class="ak-acts"><button class="nd-act primary" data-cmd="resume" data-id="${key}">Resume</button></div></div>` : '';
    return held + asked(key, a, m, x);
  }
  function asked(key, a, m, x) {
    if (!a) {
      // Nothing is asked. Something worth knowing still gets a line, and it leads to the phase it is about.
      const w = x && x.unit ? wedge(key, x.unit) : null;
      return x && w ? `<button class="sm-exc ${x.tone}" data-flip="ask:${key}" data-go="task:${key}:${x.unit}:${w.bounce || w.band}"><span data-xf="exc:${key}">${x.short}${need(x.needs)}</span><i>${x.unit} · ${PH[w.bounce || w.band]} ›</i></button>` : '';
    }
    if (a.kind === 'decision') {
      const at = pick[key] == null ? a.opts.findIndex((o) => o.rec) : pick[key];
      return `<form class="ev-ask form k-decision" data-ask="${key}" data-flip="ask:${key}">${askHead('decision', a)}<div class="ak-q"><h4>${a.title}</h4><p>${a.body}</p>`
        + `<div class="bears">${a.bears.map((b) => `<span>${b}</span>`).join('')}</div></div>`
        + `<div class="ak-a"><div class="opts">${a.opts.map((o, i) => `<button type="button" class="opt${i === at ? ' sel' : ''}" data-opt="${i}"><kbd>${i + 1}</kbd><span>${o.rec ? '<em>recommended</em>' : ''}<b>${o.t}</b><small>${o.s}</small></span></button>`).join('')}</div>`
        + `<div class="dline"><span>decisions.md</span><code>${a.id} ${m.at} owner: ${a.opts[at].line}</code></div>`
        + `<div class="ak-acts"><button type="button" class="nd-act primary" data-confirm>${a.confirm}</button></div></div></form>`;
    }
    if (a.kind === 'release') {
      const acts = armed.rel
        ? '<span class="ak-sure">The epic ends as stopped. The tag v0.4.0 stays where it is.</span><button class="nd-act primary" data-rel="stop-yes">Stop the release</button><button class="nd-act" data-rel="stop-no">Keep it</button>'
        : '<button class="nd-act primary" data-rel="retry">Retry step 2</button><button class="nd-act" data-rel="stop">Stop the release…</button>';
      return `<div class="ev-ask k-release" data-flip="ask:${key}">${askHead('release', a)}<p>${a.body}</p><div class="ak-acts">${acts}</div></div>`;
    }
    if (a.kind === 'contract') {
      return `<div class="ev-ask k-contract" data-flip="ask:${key}">${askHead('contract', a)}<p>${a.body}</p><div class="ak-acts">${fromOf(key) ? `<button class="nd-act primary" data-go="sess:${fromOf(key)}">Read it in its conversation ›</button><button class="nd-act" data-go="contract:${key}">Open it as a page</button>` : `<button class="nd-act primary" data-go="contract:${key}">Read and accept ›</button>`}</div></div>`;
    }
    return `<div class="ev-ask k-decision" data-flip="ask:${key}">${askHead('held', a)}<div class="ak-rows">${a.rows.map(([slug, why, label]) =>
      `<div><b data-go="task:${key}:${slug}:1">${slug}</b><span>${why}</span><button class="nd-act primary" data-cmd="resume-step" data-id="${key}:${slug}">${label}</button></div>`).join('')}</div></div>`;
  }
  function nowBlock(key) {
    const m = cur(key);
    const text = m.ended ? `<span class="sm-ph">${m.ended.head}</span> ${m.ended.line}` : `<span class="sm-ph">${paused[key] ? 'Paused by you.' : m.word}</span> ${m.now}`;
    return `<div class="ev-now" data-flip="now:${key}"><p class="sm-now"><span data-xf="now:${key}">${text}</span></p>${live(key)}</div>${ask(key)}`;
  }

  // Red and green proof: one card per requirement. Red is its test failing at the base commit, before any change.
  // Green is the same test passing at the task's commit, accepted by the gate and the judge. A review requirement
  // has no test and is proven by a clean review.
  const testOf = (r) => r.test || `Test${r.id}`;
  const refOf = (u) => u.ref || (/at ([0-9a-f]{7})/.exec(u.line || '') || /<code>([0-9a-f]{7})<\/code>/.exec(`${u.sentence || ''} ${u.doing || ''}`) || [])[1] || '';
  const outOf = (key, slug, b) => ((OUT[key] || {})[slug] || {})[b] || {};
  const returned = (u) => u.state === 'ok' || !!u.track && (u.track[0] === 'd' || /[db]/.test(u.track.slice(1)));
  function proofs(key) {
    return EPICS[key].reqs.map((r) => {
      const W = wedges(key).filter((w) => !w.u.ghost && reqsOf(w.u).includes(r.id)), w = W.find((x) => x.tone !== 'ok') || W[0], u = w ? w.u : null;
      const red = r.kind === 'review' ? 'review' : W.some((x) => returned(x.u) || outOf(key, x.u.slug, 1).red) ? 'red' : W.some((x) => x.band >= 1) ? 'wait' : 'todo';
      return { r, u, w, red, green: reqState(key, r.id) };
    });
  }
  const RED_WORD = { red: '✗ fails at base', review: 'review', wait: 'not captured', todo: 'not captured' };
  const GREEN_WORD = { proven: '✓ proven', gap: '✗ gap', unmet: '✗ unmet', dropped: 'dropped', doing: 'not yet', todo: 'not yet' };
  function redCard(key, p) {
    const { r, u, red } = p;
    const l = red === 'red' ? `<code>${testOf(r)}</code><span>${r.red || 'fails before the change'}</span>` : red === 'review' ? '<span>proven by a review, not a test</span>'
      : red === 'wait' ? `<span>${u.slug} is writing the test</span>` : `<span>${u ? `${u.slug} has not started` : 'no task yet'}</span>`;
    return `<button class="pf s-${red}"${u ? ` data-go="task:${key}:${u.slug}:1"` : ' disabled'} data-flip="rp:${key}:${r.id}"><span class="pf-h"><b>${r.id}</b><i>${RED_WORD[red]}</i></span><span class="pf-t">${r.text}</span><span class="pf-l">${l}</span></button>`;
  }
  function greenCard(key, p) {
    const { r, u, w, green } = p, ref = u ? refOf(u) : '', to = !w ? 0 : green === 'proven' ? (r.kind === 'review' ? 3 : 2) : w.bounce || w.band;
    const l = green === 'proven' ? (r.kind === 'review' ? `<span>review clean · ${u.slug}</span>` : `<code>${testOf(r)}</code><span>passes${ref ? ` at ${ref}` : ''} · gate ✓ · judge met</span>`)
      : green === 'gap' || green === 'unmet' ? `<span>${u.slug}: the judge ruled its goal unmet</span>` : green === 'dropped' ? '<span>dropped when the epic stopped</span>'
        : green === 'doing' ? `<span>${u.slug} · ${u.tag || u.phrase || word(w)}</span>` : `<span>${u ? `${u.slug} has not started` : 'no task yet'}</span>`;
    return `<button class="pf s-${green}"${u ? ` data-go="task:${key}:${u.slug}:${to}"` : ' disabled'} data-flip="gp:${key}:${r.id}"><span class="pf-h"><b>${r.id}</b><i>${GREEN_WORD[green]}</i></span><span class="pf-t">${r.text}</span><span class="pf-l">${l}</span></button>`;
  }
  // The review of the whole epic branch closes the green proof.
  function epicReview(key) {
    const e = EPICS[key], m = cur(key), fin = (m.engine || []).find((s) => s.k === 'final review'), did = fin || m.release || (m.ended && m.ended.kind === 'released');
    const finding = (f) => `<div class="finding closed"><div class="fhd"><span class="fid">${f.id}</span><span>${f.by}</span><span class="grow"></span><span class="verdict ok">${f.verdict}</span></div><p>${f.text}</p>`
      + `<div class="fmeta">${f.meta.map((x) => `<span>${x}</span>`).join('')}</div><div class="trail">${f.trail.map((x) => `<span>${x}</span>`).join('')}</div></div>`;
    return `<div class="ev-rv ${did ? 'ok' : 'todo'}">${icon('review')}<b>Epic review</b><span>${fin ? fin.d : did ? 'clean' : m.ended ? 'not run' : `after the last ${e.noun} integrates`}</span>${fin ? `<span class="en-tier">${fin.tier}</span>` : ''}${need(['finding'])}</div>`
      + (did && e.findings ? e.findings.map(finding).join('') + (e.dropped ? `<p class="ev-drop">${e.dropped}</p>` : '') : '');
  }

  // Which history rows exist at this moment.
  function events(key) {
    const e = EPICS[key], m = cur(key), i = index(key);
    return (e.events || []).map((ev, n) => ({ ...ev, n })).filter((ev) => (m.alt ? !ev.noalt : !ev.alt) && ev.t <= m.at && (ev.from || 0) <= i);
  }
  const evRow = (key, ev) => `<li class="tl-row k-${ev.k}${ev.tone ? ` ${ev.tone}` : ''}" data-oc="ev:${key}:${ev.n}" data-flip="ev:${key}:${ev.n}"${ev.task && unit(key, ev.task) ? ` data-go="task:${key}:${ev.task}:0"` : ''}>`
    + `<time>${ev.t}</time><i class="tl-ic">${icon(ev.k)}</i><div><span class="tx">${ev.text}</span>${ev.task ? `<em>${ev.task}</em>` : ''}</div></li>`;
  // The last three rows of the history. The rest opens in place.
  function strip(key) {
    const all = events(key), open = !!histOpen[key], rest = all.length - 3;
    if (!all.length) return '';
    return `<ol class="tl mini" data-flip="jh:${key}">${(open ? all : all.slice(-3)).reverse().map((ev) => evRow(key, ev)).join('')}</ol>`
      + (rest > 0 ? `<button class="tl-more" data-hist="${key}" data-flip="jhm:${key}" aria-expanded="${open}">${open ? `− ${rest} earlier` : `+${rest} earlier`}</button>` : '');
  }
  // Under the orrery: the tasks in flight with how long each has been where it is, what is next, and what just happened.
  function under(key) {
    const m = cur(key), shown = m.units.filter((u) => u.live || u.fresh), rest = m.units.filter((u) => !u.live && !u.fresh);
    const tally = new Map();
    rest.forEach((u) => tally.set(u.phrase, (tally.get(u.phrase) || 0) + 1));
    const row = (u) => {
      const w = wedge(key, u.slug), t = toneOf(u);
      return `<button class="sm-u ${t}" data-go="task:${key}:${u.slug}:${w.band}" data-flip="u:${key}:${u.slug}" data-tone="${t}">${MARK[t]}<b>${u.slug}</b>`
        + `<span class="ph" data-xf="uph:${key}:${u.slug}">${u.stage ? u.stage.charAt(0).toLowerCase() + u.stage.slice(1) : u.phrase}</span><span class="tm">${u.clock != null ? ticking(u.clock) : u.when || ''}</span></button>`;
    };
    const eng = m.engine || [];
    const now = m.units.length ? `<div class="un-c" data-flip="unn:${key}"><h4>Now${need(['stage'])}</h4><div class="sm-units">${shown.map(row).join('')}`
      + (rest.length ? `<div class="sm-fold" data-flip="fold:${key}"><span data-xf="fold:${key}">${[...tally].map(([phrase, n]) => `${n} ${phrase}`).join(' · ')}</span></div>` : '') + '</div>'
      + (eng.length ? `<button class="tl-more" data-eng="${key}" aria-expanded="${!!histOpen[`eng:${key}`]}">${histOpen[`eng:${key}`] ? '−' : '+'}${eng.length} engine steps${need(['role'])}</button>`
        + (histOpen[`eng:${key}`] ? `<div class="en-es">${eng.map((s) => `<div class="es"><span class="k">${s.k}</span><span class="en-tier">${s.tier}</span><span class="e ${s.cls}">${s.e}</span><span class="d">${s.d}</span></div>`).join('')}</div>` : '') : '')
      + '</div>' : '';
    const next = m.next && !m.ended ? `<div class="un-c" data-flip="unx:${key}"><h4>Next${need(['next'])}</h4><p data-xf="next:${key}">${m.next}</p></div>` : '';
    const hist = strip(key);
    return `<div class="ev-under">${now}${next}${hist ? `<div class="un-c" data-flip="unh:${key}"><h4>Just happened${need(['history'])}</h4>${hist}</div>` : ''}</div>`;
  }

  // Release proof: the steps orrery runs by itself, each with its evidence and its undo.
  function releaseSteps(key) {
    const e = EPICS[key], m = cur(key);
    if (m.release) return m.release.steps;
    if (m.ended && m.ended.kind === 'released') return (e.relDone || []).map(([name, note, undo]) => ({ name, state: 'done', note, undo }));
    return (e.rel || []).map(([name, undo]) => ({ name, state: 'todo', note: '', undo }));
  }
  function releaseSec(key) {
    const m = cur(key), steps = releaseSteps(key), n = steps.length, at = steps.findIndex((s) => s.state !== 'done') + 1, x = m.ended;
    const count = m.release ? (m.release.ask ? `step ${at} of ${n} failed` : `step ${at} of ${n} · ${m.act}`)
      : x ? (x.kind === 'released' ? `${n} of ${n} steps done · ${x.when}` : `not run · ${x.kind === 'stopped' ? 'the epic was stopped' : 'the epic ended with gaps'}`) : `${n} steps · not started`;
    const body = `<ol class="rel-steps">${steps.map((s, i) => `<li class="${s.state}"><i>${s.state === 'done' ? '✓' : s.state === 'fail' ? '✗' : i + 1}</i><div><b>${s.name}</b>`
      + `<small>${s.state === 'run' ? `running ${ticking(s.clock)} · ` : ''}${s.note}</small><small class="undo">undo: ${s.undo}</small></div></li>`).join('')}</ol>`;
    return sec(key, 'rel', m.release ? 'now' : x && x.kind === 'released' ? 'done' : 'todo', 'Release proof', count, body, ['release']);
  }
  // The report: what shipped, what did not and why.
  function reportSec(key) {
    const m = cur(key), x = m.ended;
    if (!x) return sec(key, 'rep', 'todo', 'Report', 'after the release', '', ['report']);
    const row = (r, cls, mark) => `<div class="end-row ${cls}"${unit(key, r.slug) ? ` data-go="task:${key}:${r.slug}:4"` : ''}>${mark}<span class="rq">${r.req}</span><div><b>${r.slug}</b><span>${r.text}</span>${r.why ? `<p>${r.why}</p>` : ''}</div>${r.ref ? `<code>${r.ref}</code>` : ''}</div>`;
    const body = (x.shipped.length ? `<h4>Shipped</h4><div class="end-rows">${x.shipped.map((r) => row(r, 'ok', MARK.ok)).join('')}</div>` : '')
      + (x.missed.length ? `<h4>Not shipped</h4><div class="end-rows">${x.missed.map((r) => row(r, 'bad', MARK.bad)).join('')}</div>` : '');
    return sec(key, 'rep', 'done', 'Report', `${x.line} Ran ${x.took}.`, body, ['report']);
  }

  function view(key, o = {}) {
    const e = EPICS[key], m = cur(key), c = counts(key), P = proofs(key), W = wedges(key).filter((w) => !w.u.ghost);
    const checks = P.filter((p) => p.r.kind !== 'review'), nred = checks.filter((p) => p.red === 'red').length;
    const mine = W.filter((w) => w.tone === 'mine').length, bad = W.filter((w) => w.tone === 'bad').length;
    const tally = [c.run && `${c.run} running`, mine && `${mine} ${mine > 1 ? 'wait' : 'waits'} on you`, bad && `${bad} ended short`].filter(Boolean).join(' · ');
    const built = !!W.length, over = !!m.ended || !!m.release;
    return `<div class="ev${o.phone ? ' phone' : ''}" data-epic="${key}">${paraBlock(key)}${nowBlock(key)}`
      + sec(key, 'red', built ? 'done' : 'todo', 'Red proof', built ? `<b>${nred} of ${checks.length}</b> tests fail at base ${e.base}, before any change` : `<b>0 of ${checks.length}</b> tests written`, `<div class="pf-grid">${P.map((p) => redCard(key, p)).join('')}</div>`, ['proof'])
      + sec(key, 'build', built ? (over ? 'done' : 'now') : 'todo', 'Implementation', built ? `<b>${c.done} of ${c.tasks}</b> ${e.noun}s integrated${tally ? ` · ${tally}` : ''}` : `no ${e.noun}s yet`,
        `<figure class="ev-fig" data-flip="fig:${key}">${orrery(key, { small: o.phone })}</figure>${e.grew ? `<p class="grew"><b>+2</b>${e.grew}</p>` : ''}${under(key)}`, ['stage', 'req'])
      + sec(key, 'green', c.proven ? (c.proven === c.total ? 'done' : 'now') : 'todo', 'Green proof', `<b>${c.proven} of ${c.total}</b> requirements proven${c.gaps ? ` · ${c.gaps} gap` : ''}`,
        `<div class="pf-grid">${P.map((p) => greenCard(key, p)).join('')}</div>${epicReview(key)}`, ['proof'])
      + releaseSec(key) + reportSec(key) + '</div>';
  }

  // ---------- the phase output: one page per task, its phases in order, one open ----------
  const CHECKS = ['schema', 'head', 'ancestry', 'clean', 'owned'];
  const C3 = { d: 'ok', b: 'bad', n: 'run', s: 'bad' };
  const chips = (list) => `<div class="cks">${list.map(([name, st]) => `<span class="${st || ''}">${name}${st === 'ok' ? ' ✓' : st === 'bad' ? ' ✗' : st === 'run' ? ' …' : ''}</span>`).join('')}</div>`;
  // What one phase of one task is doing or did: its state, how long, the outcome, and what it put out.
  function phaseOf(key, slug, b) {
    const e = EPICS[key], w = wedge(key, slug);
    if (!w) return null;
    const u = w.u, done = w.tone === 'ok', t = u.track || (done ? 'dddddd' : '------'), tm = u.times || {}, ref = refOf(u), o = outOf(key, slug, b);
    const st = b === 0 ? (w.band === 0 ? 'plan' : 'trail') : b === 4 ? (done ? 'ok' : w.band === 4 ? w.tone : 'ahead') : done ? 'trail' : cellState(b, w);
    const reached = !['ahead', 'plan', 'ghost'].includes(st), here = !done && w.band === b;
    const time = here && u.clock != null ? ticking(u.clock) : here && u.when ? u.when : '';
    const tests = reqsOf(u).map((id) => e.reqs.find((r) => r.id === id)).filter((r) => r && r.kind !== 'review');
    const p = { b, name: b === 4 && done ? 'integrated' : PH[b], st, reached, here, took: time, label: reached ? '' : 'not reached', sum: o.sum || '', rows: [], session: null };
    const doing = u.tag || u.phrase || word(w);
    if (b === 0) { p.label = w.band === 0 ? (u.line || 'outlined, not started') : 'started'; return p; }
    if (b === 1) {
      const red = returned(u) || o.red, passed = done || t[0] === 'd';
      p.label = here ? [u.round, doing].filter(Boolean).join(' · ') : reached ? `returned done${ref ? ` at ${ref}` : ''}` : 'not started';
      p.took = p.took || tm[0] || '';
      p.sum = p.sum || (here ? u.doing : reached ? `It wrote the test first and saw it fail at the base commit. The change makes it pass${ref ? ` at <code>${ref}</code>` : ''}.` : '');
      if (reached) tests.forEach((r) => p.rows.push(red ? [`Red test · ${r.id} · fails at base ${e.base || ''}`, pre(`--- FAIL: ${testOf(r)}\n    ${r.red || 'fails before the change'}`), 'red', ['proof']] : [`Red test · ${r.id} · not written yet`, '', 'todo']));
      (o.rows || []).forEach((row) => p.rows.push(row));
      if (here && u.mon) p.rows.push(['Monitor', u.mon, '']);
      if (passed) tests.forEach((r) => p.rows.push([`Green · ${r.id} · passes${ref ? ` at ${ref}` : ''}`, pre(`ok  ${testOf(r)}`), 'green', ['proof']]));
      if (reached) p.session = { who: 'implementer', agent: u.tier || 'haiku', state: here ? (w.tone === 'run' ? 'working' : w.tone === 'mine' ? 'paused' : 'ended') : 'ended' };
    }
    if (b === 2) {
      const [ck, gate, judge] = [t[1], t[2], t[3]];
      p.label = st === 'trail' ? `checks ✓ · gate ✓${tm[2] ? ` ${tm[2]}` : ''} · judge met` : st === 'again' ? 'passed in an earlier round · runs again after this one' : st === 'bounce' ? (judge === 'b' ? 'judge: unmet, sent back' : 'gate failed, sent back') : here ? doing : p.label;
      p.sum = p.sum || (st === 'trail' || st === 'again' ? 'The return passed the five engine checks and the gate, and the done-judge ruled the goal met.' : (st === 'bounce' || st === 'bad') && u.reason ? u.reason.text : here ? u.doing : '');
      (o.rows || []).forEach((row) => p.rows.push(row));
      if (u.checkNote && !o.rows) p.rows.push(['An earlier round', u.checkNote, 'bad', ['detail']]);
      if (reached) {
        p.rows.push(['Engine checks', chips(CHECKS.map((name) => [name, C3[ck]])), '']);
        p.rows.push([`Gate <code>just check</code> · ${gate === 'd' ? `exit 0${tm[2] ? ` · ${tm[2]}` : ''}` : gate === 'n' ? `running ${time} of 30 min` : gate === 'b' ? 'exit 1' : 'not run yet'}`,
          gate === 'd' ? pre(o.gate || '$ just check\nexit 0') : '', C3[gate] || 'todo', ['gateout', 'gate']]);
        p.rows.push([`Done-judge · ${judge === 'd' ? 'met' : judge === 'b' ? (tm[3] || 'unmet') : judge === 'n' ? `deciding ${time}` : 'not asked yet'}`,
          judge === 'b' && u.reason ? u.reason.text : judge === 'd' ? (o.judge || 'The test fails at the base commit and passes after the change, and the diff does what the task asked.') : '', C3[judge] || 'todo', ['verdict']]);
      }
    }
    if (b === 3) {
      const eng = (cur(key).engine || []).find((s) => s.k === `review ${slug}`), by = `A read-only reviewer read the change against ${reqsOf(u).join(' and ')}`;
      p.label = st === 'trail' || st === 'acc' ? `clean${st === 'acc' ? ' · waits to integrate' : ''}` : st === 'bounce' ? `${tm[4] || 'a finding'} confirmed, sent back` : here ? doing : p.label;
      p.took = p.took || (st === 'trail' ? tm[4] || '' : '');
      p.sum = p.sum || (st === 'bounce' && u.reason ? u.reason.text : st === 'trail' || st === 'acc' ? `${by} and found nothing to fix${eng ? `: ${eng.d}` : ''}.` : here ? u.doing : '');
      if (st === 'bounce' && u.reason) p.rows.push([u.reason.cap, u.reason.text, 'bad', ['finding']]);
      (o.rows || []).forEach((row) => p.rows.push(row));
      if (reached) p.session = { who: 'reviewer', agent: eng ? eng.tier : 'sonnet-high-ro', state: here && w.tone === 'run' ? 'reading' : 'ended' };
    }
    if (b === 4) {
      p.label = done ? `merged${ref ? ` at ${ref}` : ''}` : here ? 'merging onto the epic branch' : p.label;
      p.took = p.took || tm[5] || '';
      p.sum = p.sum || (done ? `Merged onto the epic branch${ref ? ` at <code>${ref}</code>` : ''}.` : here ? u.doing : '');
    }
    return p;
  }
  const PMARK = { trail: '<i class="mk tl">✓</i>', again: '<i class="mk tl dim">✓</i>', ok: '<i class="mk tl ok">✓</i>', acc: '<i class="mk tl">✓</i>', run: MARK.run, mine: MARK.mine, bad: MARK.bad, bounce: '<i class="mk bad">↩</i>',
    stop: '<i class="mk idle">■</i>', ahead: MARK.idle, plan: MARK.idle, ghost: MARK.idle };
  function taskPage(key, slug, b, o = {}) {
    const w = wedge(key, slug);
    if (!w) return '';
    const u = w.u, open = landing(key, slug, b);
    const rows = [1, 2, 3, 4].map((n) => {
      const p = phaseOf(key, slug, n), on = n === open && p.reached;
      const head = `<button class="ph-h" data-go="task:${key}:${slug}:${n}" aria-expanded="${on}"${p.reached ? '' : ' disabled'}>${PMARK[p.st]}<b>${p.name}</b><span class="ph-sum" data-xf="ps:${key}:${slug}:${n}">${p.label}</span>`
        + `<span class="ph-t">${p.took}</span><i class="ch">${p.reached ? (on ? '▾' : '▸') : ''}</i></button>`;
      const body = on ? `<div class="ph-body" data-flip="pb:${key}:${slug}:${n}">${p.sum ? `<p class="ph-say">${p.sum}${need(p.here ? ['stage'] : ['summary'])}</p>` : ''}`
        + p.rows.map(([cap, html, tone, keys]) => `<div class="ph-blk ${tone || ''}"><span class="en-cap">${cap}${need(keys)}</span>${html ? `<div>${html}</div>` : ''}</div>`).join('')
        + (p.session ? `<div class="ph-sess"><span class="en-tier">${p.session.agent}</span><span>${p.session.who} session · ${p.session.state}${need(['role', 'readonly'])}</span>`
          + `<button class="nd-act primary" data-go="session:${key}:${slug}:${n}">Open the session ›</button></div>` : '') + '</div>' : '';
      return `<li class="ph-row st-${p.st}${on ? ' open' : ''}" data-flip="pr:${key}:${slug}:${n}">${head}${body}</li>`;
    }).join('');
    return `<div class="tk${o.phone ? ' phone' : ''}" data-epic="${key}"><p class="tk-sub">${reqsOf(u).map((id) => `<span class="rq">${id}</span>`).join('')}<span>${u.subject || ''}</span>${need(['req'])}</p>`
      + (u.sentence ? `<p class="tk-story">${u.sentence}</p>` : '') + `<ol class="tk-ph">${rows}</ol></div>`;
  }

  // ---------- conversations ----------
  // A worker's session: the implementer's or the reviewer's. export-cli's implementer has a full transcript.
  const STEP = '<div class="trow fold"><span class="car">▸</span><span class="grp">Brief</span><span class="in">from planner turn 2 · goal, 2 owned files, acceptance</span></div>'
    + '<div class="trow"><span class="car">▸</span><span class="grp">9 tool calls</span><span class="in">Read ×3, Write ×2, Edit ×2, Bash ×2</span><span class="ok">✓</span></div>'
    + '<div class="ret bad"><div class="rh"><span>RETURN · ROUND 1</span><span class="grow"></span><span>14:19</span></div><div class="rv"><b>done</b> at 7c1e9aa</div>'
    + '<div class="cks"><span class="ok">schema ✓</span><span class="ok">head ✓</span><span class="ok">ancestry ✓</span><span class="ok">clean ✓</span><span class="ok">owned ✓</span><span class="bad">gate ✗</span></div><div class="rn">Gate exit 1. Sent back with the gate\'s output.</div></div>'
    + '<div class="trow"><span class="car">▸</span><span class="grp">4 tool calls</span><span class="in">Read, Edit ×2, Bash</span><span class="ok">✓</span></div>'
    + '<div class="ret"><div class="rh"><span>RETURN · ROUND 2</span><span class="grow"></span><span>14:31</span></div><div class="rv"><b>done</b> at e02b6c4</div>'
    + '<div class="cks"><span class="ok">schema ✓</span><span class="ok">head ✓</span><span class="ok">ancestry ✓</span><span class="ok">clean ✓</span><span class="ok">owned ✓</span><span class="ok">gate ✓</span><span class="ok">judge: met ✓</span></div></div>'
    + '<div class="ret rev"><div class="rh"><span>REVIEW FINDING F1 · CONFIRMED</span><span class="grow"></span><span>14:37</span></div><div class="rv"><code>export</code> overwrites an existing file without asking. R4 says it must refuse.</div><div class="rn">cli/export.go:57 · sent back into this session.</div></div>'
    + '<div class="trow"><span class="car">▸</span><b>Edit</b><span class="in">cli/export.go</span><span class="ok">✓</span></div>'
    + `<div class="trow live"><span class="car">▸</span><b>Bash</b><span class="in">go test ./cli/... -run Export</span><span class="st-working">${ticking(8)}</span></div>`
    + '<div class="reply"><p><span>Export now checks for the file before it opens it, and returns ErrExists. Writing the test that proves a second export refus</span><span class="caret soft"></span></p></div>';
  // What a conversation page shows: its transcript, and what the send box does. send is [state, placeholder, what a send says].
  function convo(go) {
    const [p, a, b, c] = go.split(':');
    if (p === 'talk') return { body: talkConvo(a, b), send: ['idle', b === 'new' ? `Ask or tell it about ${EPICS[a].slug}` : 'Message this conversation', 'sent'] };
    const w = wedge(a, b), u = w.u, ph = phaseOf(a, b, Number(c)), reviewer = Number(c) === 3, st = ph.session ? ph.session.state : 'ended';
    const send = st === 'working' ? ['working', `Nudge ${b}`, `nudge sent to ${b}. It interrupts the turn, then prompts`] : st === 'paused' ? ['waiting', `Nudge ${b}`, `nudge sent to ${b}`] : ['off', 'This step has ended', ''];
    if (!reviewer && u.pane === 'step') return { body: STEP, send, mon: u.mon };
    const ref = refOf(u), back = !reviewer && returned(u);
    const body = `<div class="trow fold"><span class="car">▸</span><span class="grp">Brief</span><span class="in">${reviewer ? `read the change against ${u.req}, read-only` : `goal, owned files, acceptance · ${u.req}`}</span></div>`
      + `<div class="trow"><span class="car">▸</span><span class="grp">${reviewer ? 7 : 9} tool calls</span><span class="in">${reviewer ? 'Read ×5, Grep ×2' : 'Read ×3, Write ×2, Edit ×2, Bash ×2'}</span><span class="ok">✓</span></div>`
      + (back ? `<div class="ret"><div class="rh"><span>RETURN · ROUND 1</span></div><div class="rv"><b>done</b>${ref ? ` at ${ref}` : ''}</div>${chips(CHECKS.map((name) => [name, 'ok']))}</div>` : '')
      + (reviewer && st === 'ended' ? `<div class="ret${ph.st === 'bounce' ? ' rev' : ''}"><div class="rh"><span>REVIEW · ${ph.st === 'bounce' ? 'FINDING CONFIRMED' : 'CLEAN'}</span></div><div class="rv">${ph.st === 'bounce' && u.reason ? u.reason.text : ph.sum}</div></div>` : '')
      + `<div class="reply"><p><span>${st !== 'ended' ? u.doing : !reviewer ? ph.sum || u.doing : ph.st === 'bounce' ? 'One finding. A verifier reproduced it before it went back to the implementer.' : 'Nothing to fix.'}</span>${st === 'working' || st === 'reading' ? '<span class="caret soft"></span>' : ''}</p></div>`;
    return { body, send, mon: u.mon };
  }
  // Talk: a session with the epic in context. The menu offers the sessions that touched it and a new one in its repository.
  const talkLog = {};
  const talkPicker = (key) => `<div class="tp" data-tp role="menu">${EPICS[key].talk.map((s) => talkOf(key, s.id)).map((s) => `<button role="menuitem" data-talk-to="${key}:${s.id}"><b>${s.sum}</b><span>${s.place}</span><em>${s.last} · ${s.at}</em></button>`).join('')}`
    + `<button role="menuitem" class="new" data-talk-to="${key}:new"><b>+ New session in ${EPICS[key].folder}</b><span>${EPICS[key].host}</span><em>starts with ${EPICS[key].slug} in context</em></button>${need(['talk'])}</div>`;
  const tool = (name, slug, out) => `<div class="trow"><span class="car">▸</span><b>${name}</b><span class="in">${slug}</span><span class="ok">${out}</span></div>`;
  const saying = (key) => { const m = cur(key); return m.ended ? `${m.ended.head} ${m.ended.line}` : `${paused[key] ? 'Paused by you.' : m.word} ${m.now} ${m.next || ''}`; };
  function talkConvo(key, sid) {
    const e = EPICS[key], s = talkOf(key, sid);
    return `<div class="tk-ctx">${icon('contract')}<span>${e.slug} in context</span></div>`
      + (s.fresh ? '' : `<div class="you"><small>YOU · ${s.saidAt || s.at}</small>${s.said || `What is ${e.slug} doing?`}</div>${tool('epic_get', e.slug, '✓')}<div class="reply"><p>${s.reply || saying(key)}</p></div>`)
      + (talkLog[`${key}:${sid}`] || []).join('');
  }
  // The session answers through orrery's tools. A pause or a resume it makes shows on the epic view beside it.
  function talkSend(key, sid, text) {
    const e = EPICS[key], log = talkLog[`${key}:${sid}`] = talkLog[`${key}:${sid}`] || [], safe = text.replace(/[&<>]/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[ch]));
    log.push(`<div class="you"><small>YOU · ${cur(key).at}</small>${safe}</div>`);
    if (/\bpause\b/i.test(text) && !cur(key).ended) { paused[key] = 'just now'; log.push(`${tool('epic_pause', e.slug, '✓ paused')}<div class="reply"><p>Paused. Running steps finish their turn, and nothing new starts until you resume it.</p></div>`); }
    else if (/\bresume\b/i.test(text) && paused[key]) { delete paused[key]; log.push(`${tool('epic_resume', e.slug, '✓ resumed')}<div class="reply"><p>Resumed. ${cur(key).now}</p></div>`); }
    else log.push(`${tool('epic_get', e.slug, '✓')}<div class="reply"><p>${saying(key)}</p></div>`);
    if (api.changed) api.changed(key, false);
  }

  // ---------- the contract page ----------
  // The same page before and after acceptance. Before, the owner settles what is open and accepts. After, it is read-only.
  const ct = { kd: '', dec: -1, open: {}, at: 'asked', read: new Set(['asked']) };   // what the owner has settled, opened and read on the one contract still waiting
  function contractOf(key) {
    const waiting = cur(key).phase === 'contract';
    return { ...EPICS[key].contract, waiting, accepted: waiting ? '' : EPICS[key].contract.accepted };
  }
  // The sections in reading order: id, the name on the page, the name in the card's index.
  const CSEC = [['asked', 'What you asked for', 'What you asked for'], ['fails', 'What could go wrong, and how much we care', 'What could go wrong'], ['added', 'Things you did not ask for', 'What you did not ask for'],
    ['reqs', 'What will be true when done', 'True when done'], ['prem', 'What I’m assuming', 'What I’m assuming'], ['wont', 'What we won’t do', 'What we won’t do'],
    ['alts', 'How, and why not the other ways', 'How, and why not otherwise'], ['undo', 'What leaves this machine or can’t be undone', 'What can’t be undone'],
    ['dec', 'Decisions I need from you', 'Decisions for you'], ['chk', 'Checks the machines run', 'Checks the machines run']];
  const relOf = (key) => { const e = EPICS[key]; return e.rel || (e.relDone || []).map(([name, , undo]) => [name, undo]); };
  const cleft = (key) => { const k = contractOf(key); return k.waiting ? [ct.dec < 0 && k.ask && 'a decision', k.added.length && !ct.kd && 'an added row'].filter(Boolean) : []; };
  const csure = (key) => contractOf(key).care !== 'severe' || !!ct.open.undo;
  // One section's words. w says the contract is still waiting, so its choices can still be made.
  function csec(key, id, w) {
    const e = EPICS[key], k = contractOf(key);
    const list = (rows) => (rows ? `<ul class="cs-list">${rows.map((x) => `<li>${x}</li>`).join('')}</ul>` : '');
    if (id === 'asked') return `<p class="prose">${k.asked}</p>`;
    if (id === 'fails') return k.fails.map(([fid, text, prevent, care]) => `<div class="fr"><span class="id">${fid}</span><div><div>${text}</div><small>prevent: ${prevent}</small></div><span class="care ${care}">${care}</span></div>`).join('')
      + `<p class="small">Level of care: ${k.care}, set by ${k.fails[0][0]}.${k.steel ? ` ${k.steel}` : ''}</p>`;
    if (id === 'added') return k.added.map(([name, why, kept]) => `<div class="added"><div><b>${name}</b><small>${why}</small></div><div class="kd" data-keepdrop>`
      + ['keep', 'drop'].map((x) => `<button data-kd="${x}"${(w ? ct.kd : kept) === x ? ' class="sel"' : ''}${w ? '' : ' disabled'}>${upper(x)}</button>`).join('') + '</div></div>').join('');
    if (id === 'reqs') return e.reqs.map((r) => `<div class="fr"><span class="id">${r.id}</span><div><div>${r.text}</div><small>${r.kind === 'review' ? 'proven by a clean review' : 'proven by a test that fails before the change and passes after it'}</small></div></div>`).join('');
    if (id === 'prem') return list(k.prem);
    if (id === 'wont') return list(k.wont);
    if (id === 'alts') return k.alts ? k.alts.map(([text, why]) => `<div class="fr${why === 'chosen' ? ' on' : ''}"><span class="id">${why === 'chosen' ? '✓' : '·'}</span><div><div>${text}</div><small>${why}</small></div></div>`).join('') : '';
    if (id === 'undo') return relOf(key).map(([name, undo], i) => `<div class="cf-step${/^none/.test(undo) ? ' none' : ''}"><b>${i + 1} · ${name}</b><small>undo: ${undo}</small></div>`).join('');
    if (id === 'dec') return k.ask ? `<div class="cf-q"><span class="id">${k.ask.id}</span>${k.ask.q}</div><div class="opts">${k.ask.opts.map((o, i) =>
      `<button type="button" class="opt${(w ? ct.dec : k.ask.opts.findIndex((x) => x.rec)) === i ? ' sel' : ''}" data-cdec="${i}"${w ? '' : ' disabled'}><kbd>${i + 1}</kbd><span>${o.rec ? '<em>recommended</em>' : ''}<b>${o.t}</b><small>${o.s}</small></span></button>`).join('')}</div>` : '';
    return k.chk ? k.chk.map(([what, how]) => `<div class="fr wide"><span class="id">${what}</span><div><div>${how}</div></div></div>`).join('') : '';
  }
  // The line under a section's name: how much is in it, and what in it is still the owner's to settle.
  function cnote(key, id, w) {
    const e = EPICS[key], k = contractOf(key), steps = relOf(key), noUndo = steps.filter(([, undo]) => /^none/.test(undo)).length;
    return { asked: 'in your words', fails: `${k.fails.length} ways, care ${k.care}`,
      added: !k.added.length ? 'nothing added' : w && !ct.kd ? `<span class="st-waiting">${k.added.length} row to decide</span>` : `${k.added.length} row · ${w ? ct.kd : k.added[0][2]}`,
      reqs: `${e.reqs.length} requirements, each with its evidence`, prem: `${(k.prem || [0, 0, 0]).length} premises`, wont: `${(k.wont || [0, 0]).length} entries`, alts: `${(k.alts || [0, 0, 0]).length - 1} alternatives`,
      undo: `${steps.length} steps${noUndo ? ` · <span class="t-red">${noUndo} has no undo</span>` : ''}${w && !csure(key) ? ' · open this before Accept' : ''}`,
      dec: w ? (k.ask && ct.dec < 0 ? '<span class="st-waiting">1 open</span>' : `none open${k.ask ? ` · ${k.ask.id}: ${k.ask.opts[ct.dec].t}` : ''}`) : `none open · ${k.decided} recorded`, chk: 'gate and evidence' }[id];
  }
  function contract(key) {
    const k = contractOf(key), w = k.waiting, left = cleft(key), sure = csure(key);
    const fold = ([id, name]) => {
      const inner = csec(key, id, w), on = inner && ct.open[id];
      return `<div class="cfold${inner ? ' can' : ''}${on ? ' open' : ''}"${inner ? ` data-cf="${id}"` : ''}><span>${on ? '▾' : '▸'}</span><b>${name}</b><i>${cnote(key, id, w)}</i>${on ? `<div class="cf-in">${inner}</div>` : ''}</div>`;
    };
    const body = `<div class="scr-body pad" data-ckey="${key}"><div class="dl"><div class="cap">THE DECISION LINE</div><p>${k.line}</p><div class="dlm"><span class="care ${k.care}">care ${k.care}</span>`
      + (w ? `${ct.dec < 0 ? '<span class="st-waiting">1 decision open</span>' : ''}${k.added.length && !ct.kd ? '<span class="st-waiting">1 added row to decide</span>' : ''}` : `<span class="st-done">accepted by you ${k.accepted}</span>`) + `${need(['contract'])}</div></div>`
      + `<h5>What you asked for</h5>${csec(key, 'asked', w)}<h5>What could go wrong, and how much we care</h5>${csec(key, 'fails', w)}`
      + (k.added.length ? `<h5>Things you did not ask for${w ? ' <em>decide each row</em>' : ''}</h5>${csec(key, 'added', w)}` : '')
      + CSEC.slice(3).map(fold).join('') + '</div>';
    const leftLine = left.length ? `<span class="st-waiting">${left.length} ${left.length > 1 ? 'choices' : 'choice'} left</span> · ${left.join(', ')}`
      : sure ? '<span class="st-done">nothing left to decide</span>' : '<span class="st-waiting">1 section left to open</span> · what can’t be undone';
    const foot = w ? `<footer class="scr-foot"><div class="left" data-left>${leftLine}<span class="grow"></span>${left.length || !sure ? '<span class="jump">jump ↓</span>' : ''}</div>`
      + `<div class="sendrow"><div class="sbox"><input placeholder="Ask for a change" aria-label="ask for a change"><span class="go">↑</span></div><button class="btn primary" data-accept="${key}"${left.length || !sure ? ' disabled' : ''}>Accept</button></div></footer>` : '';
    return { body, foot };
  }

  // ---------- the cards a conversation can show ----------
  // A card is the thing itself, drawn where the session brought it up. It is not a picture of a moment: it changes when the
  // contract or the epic does, and what the owner does in it is the owner's word, as it is on the thing's own page.
  const wgHead = (ic, kind, name, bits, go, goText) => `<header class="wg-h"><i class="nk">${ic}</i><em>${kind}</em><b>${name}</b>${bits}<button class="wg-go" data-go="${go}">${goText} <i>›</i></button></header>`;
  // A contract, read a section at a time. The index says which sections hold a choice that is the owner's, and Accept
  // opens when they are settled and, at severe care, when what can't be undone has been read.
  function contractCard(key) {
    const e = EPICS[key], k = contractOf(key), w = k.waiting;
    if (!w) {
      const kept = k.added.length ? `<span>${k.added.length} added row, ${(ct.kd || k.added[0][2]) === 'keep' ? 'kept' : 'dropped'}</span>` : '';
      return `<div class="wg k-contract done" data-wg="contract" id="card-contract">${wgHead(icon('contract'), 'Contract', e.slug, `<span class="care ${k.care}">care ${k.care}</span><span class="wg-st st-done">accepted by you ${k.accepted}</span>`, `contract:${key}`, 'read it')}`
        + `<p class="wg-lede">${k.line}</p><div class="wg-facts"><span>${e.reqs.length} requirements</span><span>${k.fails.length} ways it could go wrong</span>${kept}<span>${k.decided} ${k.decided > 1 ? 'decisions' : 'decision'} recorded</span></div></div>`;
    }
    const at = Math.max(0, CSEC.findIndex(([id]) => id === ct.at)), [id, name] = CSEC[at], left = cleft(key), sure = csure(key), nread = CSEC.filter(([x]) => ct.read.has(x)).length;
    const mark = (x) => ((x === 'added' && k.added.length && !ct.kd) || (x === 'dec' && k.ask && ct.dec < 0) ? ['todo', '◆'] : x === 'undo' && !sure ? ['must', '!'] : ct.read.has(x) ? ['read', '✓'] : ['', '○']);
    const ix = CSEC.map(([x, , nm], i) => { const [cls, ch] = mark(x); return `<button class="${cls}${i === at ? ' on' : ''}" data-cs="${x}"${i === at ? ' aria-current="true"' : ''}><i>${ch}</i><span>${nm}</span></button>`; }).join('');
    const prev = CSEC[at - 1], next = CSEC[at + 1];
    const leftLine = `<span>${nread} of ${CSEC.length} read</span>` + (left.length ? `<span class="st-waiting">${left.length} ${left.length > 1 ? 'choices' : 'choice'} left: ${left.join(', ')}</span>`
      : sure ? '<span class="st-done">nothing left to decide</span>' : '<span class="st-waiting">read what can’t be undone before Accept</span>');
    return `<div class="wg k-contract" data-wg="contract" data-ckey="${key}" id="card-contract">${wgHead(icon('contract'), 'Contract', e.slug, `<span class="care ${k.care}">care ${k.care}</span><span class="wg-st st-waiting">waiting for you · ${cur(key).since}</span>`, `contract:${key}`, 'open as a page')}`
      + `<p class="wg-lede">${k.line}</p>${need(['widget', 'contract'])}`
      + `<div class="cc"><nav class="cc-ix" aria-label="the contract’s sections">${ix}</nav>`
      + `<section class="cc-sec"><div class="cc-h"><span>${at + 1} of ${CSEC.length}</span><h6>${name}</h6><i>${cnote(key, id, true)}</i></div><div class="cc-b">${csec(key, id, true)}</div>`
      + `<div class="cc-nav">${prev ? `<button data-cs="${prev[0]}"><kbd>[</kbd>${prev[2]}</button>` : ''}<span class="grow"></span>${next ? `<button class="nx" data-cs="${next[0]}">Next: ${next[2]}<kbd>]</kbd></button>` : ''}</div></section></div>`
      + `<footer class="wg-f"><div class="left">${leftLine}<span class="hint">To ask for a change, reply below.</span></div><span class="grow"></span>`
      + `<button class="nd-act primary" data-accept="${key}" data-stay${left.length || !sure ? ' disabled' : ''}>Accept the contract</button></footer></div>`;
  }
  // An epic, from the moment it exists: where it is, each requirement red or green, and what it asks of the owner.
  const PHASES = ['contract', 'planning', 'building', 'releasing', 'report'];
  function epicCard(key) {
    const e = EPICS[key], m = cur(key), st = status(key), c = counts(key), a = askOf(key);
    const at = m.ended ? PHASES.length : Math.max(0, PHASES.indexOf(m.phase)), held = !!st.block || !!paused[key] || (a && a.kind !== 'held');
    const track = PHASES.map((name, i) => `<li class="${i < at ? 'done' : i === at ? `now${held ? ' mine' : ''}` : ''}"><i>${i < at ? '✓' : ''}</i>${name}</li>`).join('');
    const req = (p) => {
      const [cls, ch, word] = p.green === 'proven' ? ['ok', '✓', 'proven'] : /gap|unmet/.test(p.green) ? ['bad', '✗', p.green] : p.green === 'dropped' ? ['idle', '–', 'dropped']
        : p.red === 'red' ? ['red', '●', 'red'] : p.red === 'review' ? ['idle', '○', 'by review'] : ['idle', '○', 'no test yet'];
      return `<div class="ec-r ${cls}"${p.u ? ` data-go="task:${key}:${p.u.slug}:0"` : ''}><i>${ch}</i><b>${p.r.id}</b><span>${p.r.text}</span><em>${word}</em></div>`;
    };
    const P = proofs(key), nred = P.filter((p) => p.green !== 'proven' && p.red === 'red').length;
    const open = !a ? '' : a.kind === 'decision' ? `<button class="ec-q" data-goto="card-ask"><i class="nk">${icon('decision')}</i><span><b>Open question</b>${a.title}</span><em>raised ${a.since} ago</em><u>answer it below ↓</u></button>`
      : `<button class="ec-q" data-go="epic:${key}"><i class="nk">${icon(KIND[a.kind] ? KIND[a.kind].icon : 'hold')}</i><span><b>Needs you</b>${a.head}</span><em>${a.since}</em><u>open the epic ›</u></button>`;
    const pills = `<span class="b-pill ${st.tone}">${st.word} · ${st.since}</span>`;
    return `<div class="wg k-epic" data-wg="epic" id="card-epic"><header class="wg-h"><span class="gl">${glyph(key)}</span><em>Epic</em><b>${e.slug}</b>${pills}<button class="wg-go" data-go="epic:${key}">open the epic <i>›</i></button></header>`
      + `<ol class="ec-ph" aria-label="phase">${track}</ol>`
      + `<p class="ec-now"><b>${m.ended ? m.ended.head : paused[key] ? 'Paused by you.' : m.word}</b> ${m.ended ? m.ended.line : m.now}${need(['widget'])}</p>`
      + `<div class="ec-h"><b>Requirements</b><span><b>${c.proven} of ${c.total}</b> proven${nred ? ` · ${nred} red: the test is written and fails, as it should until the fix lands` : ''}</span></div>`
      + `<div class="ec-reqs">${P.map(req).join('')}</div>${open}`
      + `<footer class="wg-f">${live(key)}<span class="grow"></span>${m.ended || m.phase === 'contract' ? '' : paused[key] ? `<button class="nd-act" data-cmd="resume" data-id="${key}">Resume</button>` : `<button class="nd-act" data-cmd="pause" data-id="${key}">Pause</button>`}</footer></div>`;
  }

  // The conversations that carry cards. Each is a session like any other; the cards are items in its transcript.
  const you = (t, text, o = {}) => `<div class="you${o.state ? ` ${o.state}` : ''}" data-t="${t}"><small>you${o.via ? ` · ${o.via}` : ''}${o.state === 'sending' ? '<span class="snd"><i class="spin"></i>sending</span>' : ''}</small>${text}</div>`;
  const reply = (t, html) => `<div class="reply" data-t="${t}">${html}</div>`;
  const group = (n, names) => `<div class="trow fold"><span class="car">▸</span><span class="grp">${n} tool calls</span><span class="in">${names}</span><span class="trail">${'<i></i>'.repeat(n)}</span></div>`;
  // What the epic did, told to the conversation that carries it: one line each, in the epic's own colours.
  const evLine = (key, ev) => `<div class="evl k-${ev.k}${ev.tone ? ` ${ev.tone}` : ''}" data-t="${ev.t}"${ev.task && unit(key, ev.task) ? ` data-go="task:${key}:${ev.task}:0"` : ''}><i class="evi">${icon(ev.k)}</i><span>${ev.text}</span><em>${EPICS[key].slug}${ev.task ? ` · ${ev.task}` : ''}</em></div>`;
  const DIRECTOR = {
    tideplan(key) {
      const w = cur(key).phase === 'contract';
      return you('13:52', 'The slipway floods on big tides and I keep finding out too late. Text me the evening before any tide above 4.5&nbsp;m at the harbour.')
        + group(7, 'Read ×4, Grep ×2, Bash')
        + reply('13:58', '<p><code>tide-tables</code> already fetches the harbour forecast every night, and nothing reads it yet. Two things decide how careful this has to be:</p><ol><li>Where does the phone number come from?</li><li>One text per tide, or one per evening?</li></ol>')
        + you('14:03', 'My number, from config only. One text per tide.', { via: 'from your phone' })
        + group(9, 'Read ×5, Write, Bash ×3')
        + reply('14:15', '<p>The contract is ready. It is <b>severe care</b>, because a text sent to the wrong number cannot be recalled. One decision and one added row are yours, and nothing is built until you accept.</p>')
        + contractCard(key)
        + (w ? '' : tool('epic_create', 'tide-alerts', '✓ created') + reply('14:41', '<p>Accepted and created. The planner’s first turn has started. This card follows the epic from here, and anything it asks of you arrives in this conversation.</p>')
          + epicCard(key) + events(key).filter((ev) => ev.from === 1 && ev.k !== 'you').map((ev) => evLine(key, ev)).join(''));
    },
    rotaplan(key) {
      const t = EPICS[key].talk[0], a = askOf(key), evs = events(key).filter((ev) => ['notice', 'integrate', 'decision'].includes(ev.k));
      return you('13:31', 'The harbour office will not use the rota page. I need each keeper’s week as a CSV I can send them.')
        + group(11, 'Read ×6, Grep ×3, Bash ×2')
        + reply('13:44', '<p>Shifts only exist in the page’s state today, so an export needs them stored first. I wrote a contract for it: storage, one endpoint, the CSV, and a refusal to overwrite.</p><p>It is <b>serious care</b>, because an export could replace a file you edited by hand.</p>')
        + contractCard(key)
        + tool('epic_create', 'rota-export', '✓ created')
        + reply('13:50', '<p>Created. This card follows the epic from here, and anything it asks of you arrives in this conversation.</p>')
        + epicCard(key)
        + evs.filter((ev) => ev.t < t.saidAt).map((ev) => evLine(key, ev)).join('')
        + you(t.saidAt, t.said) + tool('epic_get', 'rota-export', '✓') + reply(t.saidAt, `<p>${t.reply}</p>`)
        + evs.filter((ev) => ev.t >= t.saidAt).map((ev) => evLine(key, ev)).join('')
        + (a || paused[key] ? `<div class="wg-ask" id="card-ask">${ask(key)}</div>` : '')
        + you('14:41', 'Tell me when export-cli is through its review.', { state: 'sending' });
    },
  };
  // What a conversation with cards shows, and what its send box does.
  function director(id) {
    const s = SESS[id], w = cur(s.epic).phase === 'contract';
    return { body: refs(DIRECTOR[id](s.epic)), send: w ? ['waiting', 'Reply to ask for a change to the contract', 'sent. The contract is redrafted with your change'] : ['idle', 'Reply', 'sent'] };
  }

  // ---------- the card: on the web, hovering a slice says what that phase is doing or did ----------
  const kv = (k, v) => (v ? `<div class="oc-r"><span>${k}</span><div>${v}</div></div>` : '');
  const TCLS = { trail: 't-acc', again: 't-acc', acc: 't-acc', ok: 't-ok', run: 't-run', mine: 't-mine', bad: 't-bad', bounce: 't-bad' };
  const CARDS = {
    ph(key, slug, b) {
      const p = phaseOf(key, slug, Number(b)), u = unit(key, slug);
      if (!p) return '';
      return `<div class="oc-k">${slug}${u.req ? ` · ${u.req}` : ''}</div><div class="oc-t ${TCLS[p.st] || ''}">${p.name}<em>${[p.label, p.took].filter(Boolean).join(' · ')}</em></div>`
        + (p.sum ? `<p class="oc-what">${p.sum}</p>` : '') + p.rows.filter((r) => !/<pre>/.test(r[1]) && r[1] && !/class="cks"/.test(r[1])).slice(0, 2).map(([cap, html]) => kv(cap.split(' · ')[0], html)).join('');
    },
    req(key, id) {
      const r = EPICS[key].reqs.find((x) => x.id === id), st = reqState(key, id), W = wedges(key).filter((w) => !w.u.ghost && reqsOf(w.u).includes(id));
      return `<div class="oc-k">${id} · ${r.kind === 'review' ? 'proven by a review' : 'proven by a test'}</div><div class="oc-t s-${st}">${REQ_LONG[st]}</div><p class="oc-what">${r.text}</p>`
        + kv(W.length > 1 ? 'Tasks' : 'Task', W.map((w) => `<b>${w.u.slug}</b> ${w.u.tag || w.u.phrase || word(w)}`).join(', ')) + (r.test ? kv('Test', `<code>${r.test}</code>`) : '');
    },
    core(key) {
      const W = wedges(key).filter((w) => !w.u.ghost), c = counts(key), names = (list) => list.map((w) => `<b>${w.u.slug}</b>`).join(', ');
      if (!W.length) return '';
      return `<div class="oc-k">The epic branch</div><div class="oc-t">${c.done} of ${c.tasks} ${EPICS[key].noun}s integrated</div>`
        + kv('Integrated', names(W.filter((w) => w.tone === 'ok'))) + kv('On the way', W.filter((w) => w.tone !== 'ok' && w.band > 0).map((w) => `<b>${w.u.slug}</b> ${word(w)}`).join(', ')) + kv('Planned', names(W.filter((w) => w.band === 0)));
    },
    count(key) {
      const c = counts(key), by = (st) => EPICS[key].reqs.filter((r) => st.includes(reqState(key, r.id))).map((r) => r.id).join(', ');
      return `<div class="oc-k">${EPICS[key].slug}</div><div class="oc-t">${c.proven} of ${c.total} requirements proven</div>` + kv('Proven', by(['proven'])) + kv('Not yet', by(['doing', 'todo'])) + kv('Will not be', by(['gap', 'unmet', 'dropped']));
    },
    epic(key) {
      const W = wedges(key).filter((w) => !w.u.ghost);
      return `<div class="oc-k">${EPICS[key].slug}</div><div class="oc-t">${W.length ? `${W.length} ${EPICS[key].noun}${W.length > 1 ? 's' : ''}` : `No ${EPICS[key].noun}s yet`}</div>`
        + W.map((w) => `<div class="oc-r wide"><span class="t-${w.tone}">${word(w)}</span><div><b>${w.u.slug}</b></div></div>`).join('') + (W.length ? '' : kv('Now', line(key).plain));
    },
    ev(key, n) {
      const ev = EPICS[key].events[n];
      return ev.more ? `<div class="oc-k">${EV_KIND[ev.k]} · ${ev.t}</div><div class="oc-t">${ev.task || EPICS[key].slug}</div><p class="oc-what">${ev.more}</p>` : '';
    },
    kind(k) { return `<div class="oc-k">Needs you</div><div class="oc-t">${KIND[k].name}</div><p class="oc-what">${KIND[k].what}</p>`; },
  };
  let cardEl = null, cardRef = '', cardFrom = null;
  function hideCard() {
    if (!cardEl || cardEl.hidden) return;
    cardEl.hidden = true;
    cardRef = '';
    if (cardFrom) cardFrom.classList.remove('oc-on');
    cardFrom = null;
  }
  // The card sits beside the pointer. Over an orrery it sits beside the drawing instead, so the drawing stays in view.
  function placeCard(x, y) {
    const w = cardEl.offsetWidth, h = cardEl.offsetHeight, chart = cardFrom && cardFrom.closest('.orr');
    let left = x + 18, top = y + 14;
    if (left + w > innerWidth - 8) left = x - w - 18;
    if (chart) {
      const box = chart.getBoundingClientRect();
      if (box.right + w + 16 <= innerWidth) { left = box.right + 8; top = y - 48; } else if (box.left - w - 16 >= 0) { left = box.left - w - 8; top = y - 48; }
    }
    cardEl.style.left = `${Math.max(8, left)}px`;
    cardEl.style.top = `${Math.max(8, Math.min(top, innerHeight - h - 8))}px`;
  }
  function showCard(from, x, y) {
    const [type, a, b, c] = from.dataset.oc.split(':'), html = CARDS[type] ? CARDS[type](a, b, c) : '';
    if (!html) return;
    if (!cardEl) { cardEl = document.createElement('div'); cardEl.setAttribute('role', 'tooltip'); document.body.append(cardEl); }
    if (cardFrom) cardFrom.classList.remove('oc-on');
    cardEl.className = 'oc';
    cardEl.innerHTML = refs(html);
    cardEl.hidden = false;
    cardRef = from.dataset.oc;
    cardFrom = from;
    from.classList.add('oc-on');
    placeCard(x, y);
    window.Motion.enter(cardEl, { ms: window.Motion.T.press });
  }
  const ocOf = (target) => (target instanceof Element ? target.closest('[data-oc]') : null);
  // The phone has no pointer to hover with: a tap goes straight to the phase's output.
  document.addEventListener('mouseover', (ev) => {
    const from = ocOf(ev.target);
    if (!from || from.closest('.device') || from.dataset.oc === cardRef) return;
    showCard(from, ev.clientX, ev.clientY);
  });
  document.addEventListener('mousemove', (ev) => { if (cardEl && !cardEl.hidden) placeCard(ev.clientX, ev.clientY); });
  document.addEventListener('mouseout', (ev) => {
    const from = ocOf(ev.target);
    if (from && !from.contains(ev.relatedTarget)) hideCard();
  });
  document.addEventListener('click', hideCard, true);
  document.addEventListener('scroll', hideCard, true);

  // ---------- commands, forms and folds: the same on both pages ----------
  // Stopping an epic ends it where it stands: what is integrated stays on its branch, the rest is stopped.
  function stopEpic(key) {
    const e = EPICS[key], m = cur(key), c = counts(key), took = status(key).since;
    delete paused[key];
    e.moments.push({ at: m.at, phase: 'ended', label: 'stopped', gaps: 0,
      ended: { kind: 'stopped', words: 'Stopped by you.', head: 'Stopped by you.', when: m.at, took, line: `${c.done} of ${c.tasks} ${e.noun}s are on the epic branch. Nothing was released.`, shipped: [], missed: [] },
      units: m.units.map((u) => (u.state === 'ok' ? u : u.track ? { ...u, live: false, state: 'stop', clock: null, track: u.track.replace(/[nw]/, 's'), phrase: 'stopped', tag: 'stopped', line: 'stopped · branch kept' }
        : { ...u, live: false, state: 'none', phrase: 'never started', line: 'never started' })) });
    e.events.push(E(m.at, 'end', '', 'You stopped the epic. Its branches are kept, and nothing was released.', '', { from: e.moments.length - 1 }));
    api.setMoment(key, e.moments.length - 1);
  }
  const say = (text) => { if (api.said) api.said(text); };
  const redraw = (key) => { if (api.changed) api.changed(key, true); };
  document.addEventListener('click', (ev) => {
    const t = ev.target;
    if (!(t instanceof Element) || ev.defaultPrevented) return;
    const took = (fn) => { ev.preventDefault(); fn(); };
    const form = t.closest('[data-ask]'), opt = t.closest('[data-opt]'), cmd = t.closest('[data-cmd]'), rel = t.closest('[data-rel]');
    if (form && opt) return took(() => { pick[form.dataset.ask] = Number(opt.dataset.opt); redraw(form.dataset.ask); });
    if (form && t.closest('[data-confirm]')) return took(() => { const key = form.dataset.ask, a = askOf(key); delete pick[key]; say(a.said); api.setMoment(key, a.to); });
    if (rel) {
      return took(() => {
        const what = rel.dataset.rel;
        if (what === 'retry') { armed.rel = false; say('retry sent. orrery runs CI again on the tag and carries on by itself'); api.setMoment('offline', 1); }
        else if (what === 'stop-yes') { armed.rel = false; say('the release is stopped. offline-tables ended as stopped'); stopEpic('offline'); }
        else { armed.rel = what === 'stop'; redraw('offline'); }
      });
    }
    if (cmd) {
      return took(() => {
        const what = cmd.dataset.cmd, [key, slug] = cmd.dataset.id.split(':');
        if (what === 'pause') { paused[key] = 'just now'; say(`${EPICS[key].slug} paused. Running steps finish their turn`); if (api.changed) api.changed(key, false); }
        else if (what === 'resume') { delete paused[key]; say(`${EPICS[key].slug} resumed`); if (api.changed) api.changed(key, false); }
        else if (what === 'stop' || what === 'stop-no') { armed[key] = what === 'stop'; redraw(key); }
        else if (what === 'stop-yes') { armed[key] = false; say(`${EPICS[key].slug} stopped. Its branches are kept`); stopEpic(key); }
        else if (what === 'resume-step') { const a = askOf(key); say(`${slug} resumed`); if (a && a.kind === 'held') api.setMoment(key, a.to); }
        else if (what === 'hold') say(`${slug} held. Its turn is interrupted, and it waits for Resume or Stop`);
        else if (what === 'retry-step') say(`retry sent. ${slug} starts a new step with the verdicts as its prompt`);
        else if (what === 'stop-step-yes') { armed[cmd.dataset.id] = false; say(`stop sent. ${slug} ends as stopped, and its branch is kept`); redraw(key); }
        else { armed[cmd.dataset.id] = what === 'stop-step'; redraw(key); }
      });
    }
    const hist = t.closest('[data-hist]'), eng = t.closest('[data-eng]'), res = t.closest('[data-resume]');
    if (hist) return took(() => { histOpen[hist.dataset.hist] = !histOpen[hist.dataset.hist]; if (api.changed) api.changed(hist.dataset.hist, false); });
    if (eng) return took(() => { histOpen[`eng:${eng.dataset.eng}`] = !histOpen[`eng:${eng.dataset.eng}`]; redraw(eng.dataset.eng); });
    if (res) return took(() => { resumed[res.dataset.resume] = 'working'; say('resumed. It continues its turn where it stopped'); if (api.changed) api.changed('', false); });
    // The contract that is still waiting: its added row, its two sections that open, and Accept.
    const kd = t.closest('[data-kd]'), dec = t.closest('[data-cdec]'), cf = t.closest('[data-cf]'), acc = t.closest('[data-accept]'), cs = t.closest('[data-cs]');
    const ckey = (t.closest('[data-ckey]') || { dataset: {} }).dataset.ckey;
    if (kd && !kd.disabled) return took(() => { ct.kd = kd.dataset.kd; redraw(ckey); });
    if (dec && !dec.disabled) return took(() => { ct.dec = Number(dec.dataset.cdec); redraw(ckey); });
    if (cs) return took(() => api.section(cs.dataset.cs, ckey));
    if (cf && !t.closest('.cf-in')) return took(() => { ct.open[cf.dataset.cf] = !ct.open[cf.dataset.cf]; redraw(ckey); });
    // Accept on the contract's page returns to the epic view. Accept on the card stays in the conversation, where the epic's card arrives.
    if (acc && !acc.disabled) {
      return took(() => {
        const key = acc.dataset.accept, stay = acc.hasAttribute('data-stay');
        api.setMoment(key, 1, !stay);
        say(`${EPICS[key].slug} accepted. The planner starts`);
        if (!stay && api.go) api.go(`epic:${key}`, 'accepted');
      });
    }
    return undefined;
  });

  // The needs-data table under the mockup lists every field once.
  function table() {
    $$('[data-en-needs]').forEach((host) => {
      host.innerHTML = '<div class="hd">field</div><div class="hd">what it is for</div><div class="hd">where it is today</div>'
        + Object.values(NEEDS).map(([field, use, today]) => `<div><code>${field}</code></div><div>${use}</div><div>${today}</div>`).join('');
    });
  }

  // ---------- the wireframe's own controls ----------
  // The controls wrap on a narrow window, and the mockup under them is sized to what is left.
  function fit() {
    const wf = $('.wf-bar');
    if (wf) document.body.style.setProperty('--wf-h', `${wf.offsetHeight}px`);
  }

  const api = {
    EPICS, ORDER, SESS, NEED, KIND, GROUPS, FOLDERS, sess, resumed, armed, paused,
    moment: cur, index, line, status, frame, head, parent, crumbsOf, landing, contract, talkPicker, talkOf, talkSend, director,
    // Whatever is drawn from the epic's records goes through refs, so a commit is a mark and not a string of hex.
    view: (...args) => refs(view(...args)),
    taskPage: (...args) => refs(taskPage(...args)),
    convo: (go) => { const c = convo(go); return { ...c, body: refs(c.body) }; },
    // The waiting contract with its choices made and every section read: the state in which Accept opens.
    settle() { ct.kd = 'keep'; ct.dec = 0; ct.open.undo = true; CSEC.forEach(([id]) => ct.read.add(id)); ct.at = 'dec'; redraw('tidealerts'); },
    // A session's context meter, for a pane that draws its own head, such as the talk dock.
    ctxOf: (id) => (SESS[id] ? ctxMeter(SESS[id].ctx) : ''),
    // The card's open section. An empty key means the one contract that is waiting.
    section(id, key) {
      const step = id === 'next' || id === 'prev' ? CSEC[CSEC.findIndex(([x]) => x === ct.at) + (id === 'next' ? 1 : -1)] : CSEC.find(([x]) => x === id);
      if (!step) return;
      ct.at = step[0];
      ct.read.add(step[0]);
      if (step[0] === 'undo') ct.open.undo = true;
      redraw(key || 'tidealerts');
    },
    ticking, glyph, icon, needs, countShort, plain, unit, sessState,
    // The page sets changed(key, quiet) to redraw what shows that epic. quiet asks for no movement. An empty key is the list alone.
    changed: null,
    // The page sets go(route, how) to open a page.
    go: null,
    // The page sets said(text) to show what a command did.
    said: null,
    setMoment(key, i, quiet) {
      if (index(key) === i) return;
      state.moment[key] = i;
      stamp[key] = Date.now();
      hideCard();
      $$('[data-wf-moment]').forEach((sel) => { if (key === 'followups') sel.value = String(i); });
      if (api.changed) api.changed(key, !!quiet);
      // The marker shows that the words above it have just been rewritten.
      if (!quiet) $$(`[data-live="${key}"]`).forEach((el) => el.classList.add('fresh'));
    },
    // The page lists its replays as [id, label, run]; the bar's menu plays them.
    replays: [],
    async replay(id) {
      const all = id === 'all' ? api.replays : api.replays.filter(([name]) => name === id);
      for (const [, label, play] of all) {
        say(label);
        await play();
        if (id === 'all') await window.Motion.wait(1500);
      }
    },
  };
  window.EpicNow = api;

  document.documentElement.dataset.tags = 'off';
  document.addEventListener('change', (ev) => {
    const t = ev.target;
    if (t.matches('[data-wf-moment]')) api.setMoment('followups', Number(t.value));
    if (t.matches('[data-wf-tags]')) document.documentElement.dataset.tags = t.checked ? 'on' : 'off';
    if (t.matches('[data-wf-rm]')) window.Motion.force(t.checked);
    if (t.matches('[data-wf-replay]') && t.value) { const id = t.value; t.value = ''; t.blur(); api.replay(id); }
  });

  // Elapsed clocks tick so "working" reads as live, and the live sentence's marker counts up from its last change.
  setInterval(() => {
    $$('[data-clock]').forEach((el) => {
      const secs = Number(el.dataset.clock) + 1;
      el.dataset.clock = secs;
      el.textContent = mmss(secs);
    });
    $$('[data-ago]').forEach((el) => { el.textContent = agoText(el.dataset.ago); });
  }, 1000);

  addEventListener('resize', fit);
  addEventListener('DOMContentLoaded', () => {
    $$('[data-wf-moment]').forEach((sel) => {
      sel.innerHTML = EPICS.followups.moments.slice(0, 7).map((m, i) => `<option value="${i}">${m.at} · ${m.label}</option>`).join('');
      sel.value = String(index('followups'));
    });
    $$('[data-wf-replay]').forEach((sel) => {
      sel.innerHTML = '<option value="">replay a transition…</option>' + api.replays.map(([id, label]) => `<option value="${id}">${label}</option>`).join('')
        + '<option value="all">all of them, in turn</option>';
    });
    $$('[data-wf-rm]').forEach((box) => { box.checked = window.Motion.reduced(); });
    table();
    fit();
    // The face arrives after the first layout and can change how the controls wrap.
    document.fonts.ready.then(fit);
  });
})();
