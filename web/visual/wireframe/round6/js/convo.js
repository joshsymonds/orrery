// Round 6 wireframe: the conversation. Four shapes hang from the reading column's left edge: a Block (someone said
// something), a Line (machinery), a Rule (a mark across the column) and the Ask (it waits on you). Under them, the
// Dock: the state, and everything that has not happened yet.
(() => {
  'use strict';
  const { esc, cls, attrs, each, when, icon, md } = O;

  // ---------- prose ----------

  // hl colours the code of a code block: comments, strings, numbers, keywords, types, calls and operators.
  const HL = /(\/\/[^\n]*)|("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')|\b(\d+(?:\.\d+)?)\b|\b(func|return|if|else|for|range|var|const|type|struct|package|import|defer|switch|case|default|break|continue|nil|true|false)\b|\b(int|string|bool|error|byte|float64|[A-Z][A-Za-z0-9_]*)\b|\b([a-z_][A-Za-z0-9_]*)(?=\()|(<-|:=|<<|>>|<=|>=|==|!=|&&|\|\||[+*<>=!|&])/g;
  O.hl = (code) => {
    let out = '';
    let at = 0;
    for (const m of String(code).matchAll(HL)) {
      const kind = m[1] ? 'c' : m[2] ? 's' : m[3] ? 'n' : m[4] ? 'k' : m[5] ? 't' : m[6] ? 'f' : 'o';
      out += `${esc(code.slice(at, m.index))}<span class="tok-${kind}">${esc(m[0])}</span>`;
      at = m.index + m[0].length;
    }
    return out + esc(code.slice(at));
  };

  // prose draws Markdown blocks. A block is a string (a paragraph of inline Markdown) or one of
  // { h, level }, { code, lang }, { ul }, { ol }, { quote }, { table: { head, rows } }, { hr }.
  O.prose = (blocks, o = {}) => {
    const list = Array.isArray(blocks) ? blocks : [blocks];
    const body = each(list, (b, i) => {
      const last = i === list.length - 1;
      if (typeof b === 'string') return `<p>${md(b)}${when(o.live && last, '<span class="caret"></span>')}</p>`;
      if (b.h) return `<h${b.level || 2}>${md(b.h)}</h${b.level || 2}>`;
      if (b.code !== undefined) return `<pre>${when(b.lang, () => `<span class="lang">${esc(b.lang)}</span>`)}<code>${O.hl(b.code)}</code></pre>`;
      if (b.ul) return `<ul>${each(b.ul, (li) => `<li>${md(li)}</li>`)}</ul>`;
      if (b.ol) return `<ol>${each(b.ol, (li) => `<li>${md(li)}</li>`)}</ol>`;
      if (b.quote) return `<blockquote>${md(b.quote)}</blockquote>`;
      if (b.table) return `<table><thead><tr>${each(b.table.head, (c) => `<th>${md(c)}</th>`)}</tr></thead><tbody>${each(b.table.rows, (r) => `<tr>${each(r, (c) => `<td>${md(c)}</td>`)}</tr>`)}</tbody></table>`;
      if (b.hr) return '<hr>';
      return '';
    });
    return `<div class="${cls('prose', o.class)}">${body}</div>`;
  };

  // ---------- blocks ----------

  // block draws the anatomy every Block shares: the rail, the label line, the body.
  // b: { tone, label, who, words, at, end, body, small, chips, pending, go, oneline }
  O.block = (b) => {
    const tag = b.go ? 'a' : 'div';
    const lead = b.gauge ? O.gauge(b.gauge) : b.icon ? icon(b.icon) : '';
    const head = `<div class="head">${lead}<span class="lbl">${esc(b.label)}</span>${when(b.who, () => `<span class="who">${esc(b.who)}</span>`)}${when(b.words, () => `<span>${esc(b.words)}</span>`)}${when(b.at, () => `<span class="at">${esc(b.at)}</span>`)}`;
    const end = when(b.end, () => `<span class="end">${b.end}</span>`);
    const open = `<${tag} class="${cls('block', b.oneline && 'oneline', b.tone, b.pending && 'pending')}"${attrs({ href: b.go ? O.go(b.go) : undefined })}>`;
    if (b.oneline) return `${open}${head}</div><span class="body">${b.body || ''}</span>${end}</${tag}>`;
    // meta: facts about the thing, each an icon and a few words: how many rounds, how long, what changed.
    const meta = when(b.meta || b.metaEnd, () => `<div class="meta">${each(b.meta, ([i, t]) => `<span>${icon(i)}${t}</span>`)}${b.metaEnd || ''}</div>`);
    return `${open}${head}${end}</div>${when(b.chips, () => `<div class="chips">${b.chips}</div>`)}${when(b.body, () => `<div class="${cls('body', b.small && 'small')}">${b.body}</div>`)}${meta}${b.after || ''}</${tag}>`;
  };

  const END_CHIP = {
    done: ['done', 'ok'], returned: ['returned', 'ok'], failed: ['failed', 'bad'], goal_unmet: ['goal not met', 'bad'],
    checks_failed: ['checks failed', 'bad'], gate_failed: ['gate failed', 'bad'], stopped: ['stopped', ''],
    engine_restarted: ['engine restarted', ''], overtime: ['ran over time', 'bad'], tool_stuck: ['a tool stuck', 'bad'],
    silent: ['went silent', 'bad'], looping: ['looping', 'bad'], wedged: ['wedged', 'bad'],
  };
  O.END_WORDS = END_CHIP;
  // The tone of a step's end: its rail is its verdict.
  const endTone = (end) => ({ ok: 'ok', bad: 'bad' }[(END_CHIP[end] || ['', ''])[1]] || 'quiet');

  const EVENT_ICON = { question: 'question', park: 'pause', release: 'open', end: 'stop', settled: 'check', created: 'plus', moved: 'split' };
  const DRAW = {
    // YouBlock: your words. Sending, its rail is dashed and its label says so; the text is at full strength.
    you: (it) => O.block({
      tone: 'you', icon: it.from ? 'phone' : 'person', pending: it.sending, label: it.sending ? 'Sending' : 'You', words: it.from ? `· ${it.from}` : '', at: it.at,
      body: O.prose(it.text),
    }),
    // EventBlock: the engine or another agent said it.
    event: (it) => O.block({ tone: 'engine', icon: 'gear', label: 'Event', words: it.from || '', at: it.at, body: O.prose(it.text) }),
    // StepEndBlock: a step ended. One chip says how; its facts are how many rounds it took, how long, what it
    // changed and how far its task got. A reason is drawn only when the end was not good.
    stepend: (it) => {
      const [word, tone] = END_CHIP[it.end] || [it.end, ''];
      const st = it.stats || {};
      const meta = [st.rounds && ['retry', esc(st.rounds)], st.time && ['clock', esc(st.time)],
        st.files && ['edit', `${esc(st.files)} <span class="plus">+${esc(st.plus)}</span> <span class="minus">−${esc(st.minus)}</span>`]].filter(Boolean);
      return O.block({
        tone: endTone(it.end), icon: tone === 'ok' ? 'check' : tone === 'bad' ? 'cross' : 'stop', pending: it.pending, label: 'Step ended', who: it.agent, at: it.at, go: it.go,
        end: O.chip(word, tone) + when(it.go, () => icon('chevronRight')),
        body: md(it.expanded ? it.summary : String(it.summary).split('\n')[0]) + when(it.reason, () => `<br><span class="is-bad">${md(it.reason)}</span>`),
        small: true, meta, metaEnd: when(it.pips, () => O.pips(it.pips)),
      });
    },
    // EpicEventBlock: one line from an epic, led by the epic's gauge when it has one. Its rail asks (attention),
    // reports a verdict (ok), or just speaks (engine).
    epicevent: (it) => {
      const tone = { question: 'attention', park: 'attention', release: 'ok', end: 'quiet', settled: 'quiet', created: 'engine' }[it.v] || 'engine';
      return O.block({ tone, oneline: true, gauge: it.gauge, icon: EVENT_ICON[it.v] || 'gear', label: 'Epic', go: it.go, body: esc(it.text), end: `<span class="link">${esc(it.slug)}</span> ${icon('chevronRight')}` });
    },
    // CheckBlock: a send-back round. The rail is the verdict; each check is a chip.
    checks: (it) => {
      const ok = it.checks.every((c) => c.ok);
      return O.block({
        tone: ok ? 'ok' : 'bad', icon: ok ? 'check' : 'cross', label: `Checks · round ${it.round}`, words: 'from the engine', at: it.at,
        chips: each(it.checks, (c) => O.chip(c.name, c.ok ? 'ok' : 'bad', c.ok ? 'check' : 'cross')),
        body: md(it.note) + when(it.more && it.open, () => `<div class="opened">${esc(it.more)}</div>`), small: true,
        end: when(it.more, () => icon(it.open ? 'chevronDown' : 'chevronRight')),
      });
    },
    // ContractBlock: where a contract was written, one line. The contract itself is a tile on the right rail beside the
    // conversation while it waits, and on its own page.
    contract: (it) => O.block({
      tone: it.accepted ? 'ok' : 'attention', icon: 'doc', oneline: true, label: 'Contract', go: it.go,
      body: `${esc(it.title)} <span class="ink-3">· ${it.accepted ? `accepted ${esc(it.accepted)} · ${esc(it.counts)}` : `${esc(it.care)} care · waits for you`}</span>`, end: icon('chevronRight'),
    }),
    // Reply: the only plain full-width text. No rail, no surface.
    reply: (it) => O.prose(it.md, { live: it.live }),
    // ToolLines: calls that touch, each a Line.
    tools: (it) => `<div class="lines">${each(it.list, O.toolLine)}</div>`,
    // ToolRun: three or more calls as one line, a dot per call: grey for one that worked, red for one that failed.
    run: (it) => `<div class="lines"><button class="${cls('line', it.running && 'running')}">${icon(it.open ? 'chevronDown' : 'chevronRight').replace('class="icon"', 'class="icon caret"')}`
      + `<span class="name plain">${it.n} tool calls</span><span class="arg">${esc(it.names)}</span>`
      + `<span class="end">${when(it.running, '<span>running</span>')}<span class="dots">${each(it.dots, (d) => `<i class="${d === 'f' ? 'bad' : d === 'r' ? 'live' : ''}"></i>`)}</span></span></button>`
      + `${when(it.open, () => each(it.open, O.toolLine))}</div>`,
    // ThinkingLine.
    thinking: (it) => `<div class="lines"><button class="line thinking">${icon(it.open ? 'chevronDown' : 'chevronRight').replace('class="icon"', 'class="icon caret"')}<span class="arg">thinking</span></button>${when(it.open, () => `<div class="opened prosey">${esc(it.text)}</div>`)}</div>`,
    // TaskLine: a background task that finished, in the transcript where it ended.
    task: (it) => `<div class="lines">${O.taskLine(it)}</div>`,
    // NoticeLine: the notice's kind as a label, then its text.
    notice: (it) => `<div class="lines"><div class="${cls('line', it.error && 'error')}"><span class="lbl${it.error ? ' is-bad' : ''}">${esc(it.kind)}</span><span class="arg strong">${esc(it.text)}</span></div></div>`,
    // EngineFold: a step's brief, or an engine prompt, folded to a line.
    fold: (it) => `<div class="lines"><button class="line">${icon(it.open ? 'chevronDown' : 'chevronRight').replace('class="icon"', 'class="icon caret"')}<span class="name plain">${it.v === 'brief' ? 'Brief' : 'Engine'}</span>`
      + `<span class="arg">${esc(it.v === 'brief' ? 'from the engine' : String(it.text).split('\n')[0])}</span></button>${when(it.open, () => `<div class="opened prosey">${O.prose(it.text)}</div>`)}</div>`,
    // Rule.
    rule: (it) => {
      const v = {
        new: ['new', 'New since you looked', ''],
        compaction: ['plain', 'Compacted', it.detail || ''],
        interrupted: ['halt', 'Interrupted by you', it.detail || ''],
        day: ['plain', it.detail || '', ''],
      }[it.v];
      return `<div class="${cls('rule', v[0])}" role="separator"><span class="lbl">${esc(v[1])}</span>${when(v[2], () => `<span class="detail">${esc(v[2])}</span>`)}${when(it.v === 'compaction', () => `<span class="detail">${icon(it.open ? 'chevronDown' : 'chevronRight')}</span>`)}</div>`
        + when(it.open, () => `<div class="opened prosey">${esc(it.text)}</div>`);
    },
    ask: (it) => O.ask(it),
  };

  // toolLine draws one ToolLine. t: { name, arg, status: '' | 'failed' | 'running', clock, open, result }
  // A tool's family gives its icon and the icon's colour: reading, changing, running, searching, delegating.
  const FAMILY = { Read: ['doc', 'read'], Write: ['edit', 'edit'], Edit: ['edit', 'edit'], Bash: ['shell', 'shell'], gate: ['shell', 'shell'], Grep: ['search', 'search'], Glob: ['search', 'search'], Agent: ['agent', 'agent'] };
  O.toolLine = (t) => {
    const tag = t.result ? 'button' : 'div';
    const [ic, family] = FAMILY[t.name] || ['gear', 'read'];
    const end = t.status === 'failed' ? `<span class="end">${icon('cross', 'failed')}</span>`
      : t.status === 'running' ? `<span class="end">${esc(t.clock || 'running')}</span>` : '';
    const note = t.diff ? `<span class="more"><span class="plus">+${esc(t.diff[0])}</span> <span class="minus">−${esc(t.diff[1])}</span></span>` : when(t.note, () => `<span class="more">${esc(t.note)}</span>`);
    return `<${tag} class="${cls('line', t.status)}">${icon(ic).replace('class="icon"', `class="icon tool f-${family}"`)}<span class="name">${esc(t.name)}</span><span class="arg">${esc(t.arg)}</span>${note}${end}</${tag}>`
      + when(t.open && t.result, () => `<div class="${cls('opened', t.status === 'failed' && 'bad')}">${esc(t.result)}</div>`);
  };

  // taskLine draws a TaskLine. t: { type: 'agent' | 'shell', agent, desc, activity, status, go }
  O.taskLine = (t) => {
    const status = t.status || 'running';
    const end = { running: '', done: 'finished', failed: 'failed', stopped: 'stopped' }[status];
    return `<${t.go ? 'a' : 'div'} class="${cls('line', status === 'failed' && 'failed', status === 'stopped' && 'stopped')}"${attrs({ href: t.go ? O.go(t.go) : undefined })}>${icon(t.type === 'shell' ? 'shell' : 'agent').replace('class="icon"', 'class="icon caret"')}`
      + `<span class="name">${esc(t.type === 'shell' ? 'shell' : (t.agent || 'agent'))}</span><span class="arg strong">${esc(t.desc)}</span>${when(t.activity, () => `<span class="more">${esc(t.activity)}</span>`)}`
      + `${when(end, () => `<span class="end">${esc(end)}</span>`)}</${t.go ? 'a' : 'div'}>`;
  };

  // The kinds that begin a turn: the gap above them is the turn gap.
  const TURN = new Set(['you', 'event', 'stepend', 'epicevent', 'contract']);

  // items draws a list of conversation items into the reading column.
  O.items = (list) => {
    let first = true;
    return each(list, (it) => {
      const draw = DRAW[it.k];
      if (!draw) throw new Error(`conversation: no drawing for item kind ${it.k}`);
      const turn = it.turn !== undefined ? it.turn : TURN.has(it.k) && !first;
      first = false;
      const at = it.live ? '<span class="at live">live</span>' : `<span class="at">${esc(it.k === 'rule' || it.pending ? '' : it.at || '')}</span>`;
      return `<div class="${cls('it', turn && 'turn')}" data-kind="${esc(it.k)}">${at}<div class="main">${draw(it)}</div></div>`;
    });
  };

  // reader draws a Reader. o.state: '' | 'loading' | 'empty'. The newest content is in view, as it is live.
  O.reader = (list, o = {}) => {
    if (o.state === 'loading') return `<div class="reader"><div class="blank"><span>loading the conversation…</span></div></div>`;
    if (o.state === 'empty') return `<div class="reader"><div class="blank"><span class="t-body">Nothing has been said yet.</span><span>${esc(o.hint || 'Say something below to begin.')}</span></div></div>`;
    return `<div class="reader" data-scroll="${o.scroll || 'end'}"${attrs({ 'data-scroll-to': o.scrollTo })}><div class="col">${O.items(list)}${o.after || ''}</div></div>`;
  };

  // ---------- the Ask ----------

  // ask draws the Ask. a: {
  //   variant: 'session' | 'epic', who, age, q (Markdown blocks), chips, opts: [{ name, desc, rec, why }],
  //   picked (index), other (true, or the typed text), record (the line that will be written),
  //   state: 'open' | 'sending' | 'answered' | 'settled' | 'error' | 'unanswerable', answer: { n, name }, error, stale }
  O.ask = (a) => {
    if (a.state === 'answered') return `<div class="ask folded"><span class="lbl">You answered</span>${O.kbd(a.answer.n)}<span>${esc(a.answer.name)}</span><span class="ink-3">· ${esc(a.who)}</span></div>`;
    if (a.state === 'settled') return `<div class="ask folded settled"><span class="lbl">Settled</span><span>This question is settled.</span><span class="ink-3">· ${esc(a.who)}</span></div>`;
    const epic = a.variant === 'epic';
    const opts = each(a.opts, (o, i) => {
      const picked = a.picked === i;
      return `<button class="${cls('opt', picked && 'picked')}" aria-pressed="${picked}">${O.kbd(i + 1, picked ? 'active' : 'attn')}<span class="nm">${md(o.name)}</span>${when(o.rec, () => O.chip('recommended'))}`
        + `${when(o.desc, () => `<span class="ds">${md(o.desc)}</span>`)}${when(o.rec && o.why, () => `<span class="ds">Why: ${md(o.why)}</span>`)}</button>`;
    });
    const otherAt = (a.opts || []).length;
    const otherPicked = a.other !== undefined && a.other !== false;
    const other = when(epic, () => `<div class="${cls('opt', otherPicked && 'picked')}" role="button" aria-pressed="${otherPicked}">${O.kbd(otherAt + 1, otherPicked ? 'active' : 'attn')}<span class="nm">Something else</span>`
      + `<span class="ds">Type an answer for the planner.</span>${when(otherPicked, () => `<div class="field area focused">${typeof a.other === 'string' ? esc(a.other) : '<span class="ink-4">Your answer</span>'}</div>`)}</div>`);
    const pickedName = otherPicked ? 'Something else' : a.picked !== undefined ? a.opts[a.picked].name : '';
    const foot = epic
      ? `<div class="foot">${O.btn(pickedName ? `Answer with ${pickedName}` : 'Answer', { variant: 'primary', state: !pickedName || a.state === 'sending' || (otherPicked && typeof a.other !== 'string') ? 'disabled' : '' })}`
        + `${when(a.state === 'sending', '<span class="inote">sending…</span>')}${when(a.record, () => `<div class="record grow"><span class="t-label">Will be recorded</span><code>${esc(a.record)}</code></div>`)}</div>`
      : when(a.state === 'sending', '<div class="foot"><span class="inote">sending…</span></div>');
    // Not now takes a session's question off your list and sends nothing: it stays waiting, here, quiet. Decline, which
    // denies the request and ends the turn, is in its More, named for what it does. An epic's question has neither.
    const later = a.later;
    const ends = when(!epic && a.state !== 'sending', () => (later
      ? O.ctl('Bring it back', 'retry', { tip: 'Back on your list' })
      : O.ctl('Not now', 'clock', { tip: 'Take it off your list. Nothing is sent: it stays waiting here, and anything new it asks comes back.' }))
      + O.ctl('More for this question', 'more', { only: true }) + when(a.menu, () => O.menu([{ label: 'Decline and end the turn', icon: 'stop', danger: true, sub: 'denies the request' }])));
    return `<section class="${cls('ask', later && 'later')}" aria-label="a question for you">`
      + `<div class="head"><span class="lbl">${later ? 'Put off' : 'Asks you'}</span><span class="cut">${esc(a.who)}</span>${when(a.age, () => `<span class="age">· ${esc(a.age)} ago</span>`)}${when(later, () => `<span class="age">· you put it off ${esc(later)} ago</span>`)}`
      + `<span class="end">${when(a.permission, () => O.chip('permission', 'attention'))}${ends}</span></div>`
      + `${O.prose(a.q, { class: 'q' })}`
      + `${when(a.chips, () => `<div class="ctx-chips">${each(a.chips, (c) => O.chip(c))}</div>`)}`
      + `${when(a.state === 'unanswerable', () => O.inote('This question cannot be answered from here. Reply in words below.'))}`
      + `${when(a.opts && a.opts.length, () => `<div class="opts">${opts}${other}</div>`)}`
      + `${foot}`
      + `${when(a.state === 'error', () => O.inote(a.error, 'error'))}`
      + `${when(a.stale, () => `<div class="foot"><span class="inote">Asked ${esc(a.stale)} ago and still waiting.</span>${O.btn('Archive this session', { variant: 'quiet', icon: 'archive', kbd: O.touch() ? '' : 'A' })}</div>`)}`
      + `</section>`;
  };

  // ---------- the dock ----------

  const PLACEHOLDER = {
    working: (n, pane) => (O.touch() ? `Queue for ${n}` : pane ? `Queue a message for ${n}. Claude Code takes it when it can` : `Queue a message for ${n}. It is sent when this turn ends`),
    interrupting: (n) => `Stopping ${n}…`,
    waiting: (n) => `Answer ${n}`,
    interrupted: (n) => `${n} was cut off. Resume it, or say what to do next`,
    stoppedByYou: (n) => `${n} stopped where it was. Say what to do next`,
    failed: (n) => `${n} failed. A message starts it again`,
    stopped: (n) => `${n} is stopped. A message starts it again`,
    shell: () => 'Type a command…',
  };

  // stateBar draws a StateBar. s: { state, word, tool, what, clock, controls }
  O.stateBar = (s) => `<div class="statebar">${O.stateGlyph(s.state)}<span class="word">${esc(s.word || O.STATE_WORDS[s.state] || s.state)}</span>`
    + `<span class="what">${when(s.tool, () => `<span class="name">${esc(s.tool)}</span> `)}${esc(s.what || '')}</span>${when(s.clock, () => `<span class="clock">${esc(s.clock)}</span>`)}`
    + `<span class="end">${s.controls || ''}</span></div>`;

  // dock draws a Dock: the state, your queue, what the message is about, the input. d: {
  //   state, word, tool, what, clock, name, byYou, pane, shell, queued: [{ text, cancelled }], queueWhy, about,
  //   draft, focused, error, note, controls, step: { label, held, confirm }, quickKeys, keyNote }
  O.dock = (d) => {
    const touch = O.touch();
    const state = d.state || 'idle';
    const controls = d.controls !== undefined ? d.controls
      : state === 'working' ? O.ctl('Interrupt', 'stop', { tone: 'bad', kbd: 'esc', tip: 'Stop this turn. Queued messages come back to the box.' })
        : state === 'interrupting' ? O.ctl('Interrupting…', '', { disabled: true })
          : state === 'interrupted' && !d.byYou ? O.ctl('Resume', 'play', { tone: 'go', tip: 'Carry on from where it was cut off' })
            : '';
    const more = d.more === false ? '' : O.ctl('More for this conversation', 'more', { only: true });
    // A session's queue is the server's: one message is sent at each turn's end, and any one can be taken back. A
    // pane's queue is Claude Code's own: it takes messages when it chooses, and they come back only all together.
    const list = d.queued || [];
    const queueWhy = d.queueWhy || (d.pane ? 'Claude Code takes them when it chooses' : list.length > 1 ? 'the first goes when this turn ends, then one a turn' : 'goes when this turn ends');
    const queueEnd = d.pane ? O.ctl('Take all back', 'edit') : when(state === 'working', () => O.ctl('Send now', 'send', { tip: 'Interrupts this turn; your first queued message runs next' }));
    const queued = when(list.length, () => `<div class="tray"><div class="tray-head"><span class="lbl">Queued ${list.length}</span><span class="why">${esc(queueWhy)}</span><span class="end">${queueEnd}</span></div>`
      + each(list.slice(0, 3), (q) => `<div class="${cls('tray-row', 'you', q.cancelled && 'cancelled')}">${when(q.cancelled, '<span class="lbl">not delivered</span>')}<span class="text wrap">${esc(q.text)}</span>`
        + `${when(!d.pane && !q.cancelled, () => `<span class="end">${O.ctl('Take this back into the box', 'edit', { only: true })}</span>`)}</div>`)
      + when(list.length > 3, () => `<div class="tray-more">${list.length - 3} more</div>`) + `</div>`);
    const about = when(d.about, () => `<div class="quote">${icon('chat')}<span class="text">about ${d.about}</span>${O.ctl('Not about this', 'close', { only: true })}</div>`);
    const placeholder = d.placeholder || (d.shell ? PLACEHOLDER.shell() : (PLACEHOLDER[d.byYou ? 'stoppedByYou' : state] || ((n) => `Message ${n}`))(d.name || 'this session', d.pane));
    const text = d.draft
      ? `<div class="text">${esc(d.draft)}${when(d.focused, '<span class="caret"></span>')}</div>`
      : `<div class="text placeholder">${when(d.focused, '<span class="caret"></span>')}${esc(placeholder)}</div>`;
    const send = O.btn('Send', { variant: 'primary', icon: 'send', iconOnly: true, state: d.draft ? '' : 'disabled', tip: touch ? undefined : 'Send', tipSide: 'left' });
    const bar = d.step ? O.stepBar(d) : O.stateBar({ state, word: d.word, tool: d.tool, what: d.what, clock: d.clock, controls: controls + more });
    // waits: events held for the session because your interrupt left it quiet (until you speak), or because a question
    // you put off is still open (on the question you put off). A running turn is too brief to show.
    const waits = when(d.waits, () => `<div class="dock-waits">${icon('bell')}<span>${d.waits === 1 ? '1 event waits' : `${d.waits} events wait`} ${esc(d.waitsWhy || 'until you speak')}</span></div>`);
    return `<div class="dock"><div class="col">`
      + `<div class="${cls('dock-box', d.focused && 'focused')}" data-state="${esc(state)}">${bar}${waits}${queued}${about}<div class="dock-input">${text}${send}</div></div>`
      + `${when(d.error, () => O.inote(d.error, 'error'))}${when(d.note, () => O.inote(d.note, d.noteKind || ''))}`
      + `${when(d.quickKeys, () => O.quickKeys(d))}`
      + `</div></div>`;
  };

  // stepBar draws the StateBar of a Dock/Step: Interrupt, and Stop behind More and a confirm.
  O.stepBar = (d) => {
    const s = d.step;
    const controls = s.confirm && !O.touch()
      ? O.confirm(`Stop ${s.label}?`, `Stop ${s.label}`, 'Keep it running')
      : `${when(!s.held, () => O.ctl('Interrupt', 'stop', { tone: 'bad', tip: 'Hold this step where it is' }))}${O.ctl('Stop this step…', 'more', { only: true })}`;
    return O.stateBar({ state: s.held ? 'held' : 'working', word: s.held ? 'held' : 'running', tool: d.tool, what: d.what, clock: d.clock, controls });
  };

  // quickKeys draws QuickKeys: Android only, for a terminal pane.
  O.quickKeys = (d) => `<div class="quickkeys">${each(['Esc', 'Ctrl-C', 'Enter', '←', '↑', '↓', '→'], (k) => O.btn(k, { class: d.keyFlash === k ? 'flash' : '' }))}</div>${when(d.keyNote, () => O.inote(d.keyNote, d.keyNoteKind || ''))}`;

  // findBar draws a FindBar. f: { value, count, state: '' | 'nomatch' }
  O.findBar = (f) => `<div class="findbar"><div class="col">${O.field({ icon: 'search', value: f.value, placeholder: 'Find in this conversation', state: 'focused', count: f.value ? (f.count || 'no matches') : '', trail: O.iconBtn('close', 'Close find', { kbd: '' }) })}</div></div>`;
})();
