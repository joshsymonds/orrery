// orrery phone mockup: one device. Home, and from it the same pages as the web view with the same title row: an epic's
// page, a phase's output, a worker's session, a contract, a conversation opened with Talk, an ordinary session. A tap
// goes one page forward and ‹ goes back. A notification lands on the page it is about. Nothing leaves the page.
(() => {
  const EN = window.EpicNow;
  const M = window.Motion;
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
  const device = $('.device');
  const homeScr = $('[data-s="home"]');
  const pageScr = $('[data-s="page"]');
  const body = $('[data-body]');
  const toastEl = $('[data-toast]');

  const P = {
    route: 'home', picker: false,
    scroll: {},                                             // where each page was left, so ‹ returns to the same place
    opened: new Set(), left: new Set(), endedOpen: false,   // just-ended epics: seen by the owner, then folded away
    gone: '',                                               // a needs-you card held back, so its arrival can be replayed
  };
  let toastTimer = 0;
  const ended = (key) => EN.line(key).ended;
  const epicOf = (route) => (/^(home|sess)/.test(route) ? '' : route.split(':')[1]);

  // What reaches the phone's notification shade, and the page each one lands on. Decisions and questions notify at once;
  // a contract, a failed release step and an ending notify quietly. Steps never notify.
  const NOTES = [
    { id: 'decision', t: 'rota-export needs a decision', b: 'Bump the tz database? tz-fixtures is paused until you answer.', go: 'epic:rota', on: () => EN.index('rota') === 0 },
    { id: 'question', t: 'beacon-schedule asks', b: 'Which index should I add?', go: 'sess:beacon', on: () => !EN.sess.answered },
    { id: 'contract', t: 'tide-alerts: contract ready', b: 'Severe care. 1 decision and 1 added row to settle.', go: 'contract:tidealerts', quiet: 1, on: () => EN.index('tidealerts') === 0 },
    { id: 'release', t: 'offline-tables: release step 2 failed', b: 'CI is red on the tag v0.4.0. Retry, or stop the release?', go: 'epic:offline', quiet: 1, on: () => EN.index('offline') === 0 },
    { id: 'ended', t: 'holder-scopes released v0.45.0', b: 'All four requirements shipped. Ran 1 h 48 min.', go: 'epic:holder', sec: 'rep', quiet: 1, on: () => true },
  ];

  function toast(words) {
    toastEl.textContent = words;
    toastEl.classList.add('on');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toastEl.classList.remove('on'), 2600);
  }

  // ---------- Home ----------
  // A needs-you card says what is waiting and why, and carries its action as a button. The marker tells the kinds apart.
  function needCard(n) {
    const s = n.session ? EN.SESS[n.session] : null;
    const answered = (n.id === 'beacon' && EN.sess.answered) || (n.resume && EN.resumed[n.resume]);
    const to = n.epic ? `epic:${n.epic}` : `sess:${n.session}`;
    const acts = answered ? ''
      : s && s.opts ? s.opts.map((o, i) => `<button class="nd-act" data-ph-answer="${i + 1}">${o}</button>`).join('')
        : n.rel ? (EN.armed.rel ? '<button class="nd-act primary" data-rel="stop-yes">Stop the release</button><button class="nd-act" data-rel="stop-no">Keep it</button>'
          : '<button class="nd-act primary" data-rel="retry">Retry step 2</button><button class="nd-act" data-rel="stop">Stop the release…</button>')
          : n.resume ? `<button class="nd-act primary" data-resume="${n.resume}">Resume</button>`
            : `<button class="nd-act primary" data-go="${n.kind === 'contract' ? `contract:${n.epic}` : to}">${n.act} ›</button>`;
    const said = !answered ? n.say : n.resume ? `<b>${s.label}</b> is working again. You resumed it.` : `<b>${s.label}</b> is working again. You answered: ${s.opts[EN.sess.answered - 1]}.`;
    return `<div class="nd k-${n.kind}${answered ? ' answered' : ''}" data-flip="n:${n.id}" data-go="${to}"><i class="nk">${EN.icon(EN.KIND[n.kind].icon)}</i><div>`
      + `<div class="say">${said}</div>`
      + `<div class="meta"><span>${n.where}</span><span class="age">${answered ? (n.resume ? 'resumed just now' : 'answered just now') : `waiting ${n.age}`}</span></div>${acts ? `<div class="nd-acts">${acts}</div>` : ''}</div></div>`;
  }
  // An epic's line: its orrery, small; its name; a count that says what it counts; its state and how long; what blocks it.
  function epicLine(key, old) {
    const L = EN.line(key);
    const cls = ['ep', L.exc ? L.exc.tone : '', L.ended ? 'ended' : '', old ? 'old' : '', L.ended && !P.opened.has(key) ? 'unseen' : ''].join(' ');
    return `<div class="${cls}" data-flip="e:${key}"><button class="ep-line" data-go="epic:${key}"><span class="gl">${EN.glyph(key)}</span><span class="tx">`
      + `<span class="l1"><b>${L.slug}</b><span class="pv">${EN.countShort(key)}</span></span><span class="ph" data-xf="p:${key}">${L.phrase}</span>`
      + `${L.exc ? `<span class="ex ${L.exc.tone}" data-xf="x:${key}">${L.exc.text}</span>` : ''}</span><i class="chev">›</i></button></div>`;
  }
  function sessLine(id) {
    const s = EN.SESS[id], st = EN.sessState(id), fresh = ['beacon', 'graft', 'moon'].includes(id);
    return `<button class="se s-${st}${fresh ? ' new' : ''}" data-flip="se:${id}" data-go="sess:${id}"><span class="dot${st === 'working' ? ' spin' : ''}"></span><div><div class="lb">${s.label}</div><div class="sm">${s.sm}</div></div>`
      + `<div class="rt"><span class="st">${st}</span><span class="age">${s.age}</span></div></button>`;
  }
  const open = (n) => !(n.id === 'beacon' && EN.sess.answered) && !(n.resume && EN.resumed[n.resume]);
  function homeHTML() {
    const need = EN.needs().filter((n) => n.id !== P.gone);
    const live = EN.ORDER.filter((k) => !ended(k));
    const just = EN.ORDER.filter((k) => ended(k) && !P.left.has(k));
    const earlier = EN.ORDER.filter((k) => P.left.has(k));
    return `<div class="chips"><span class="need" data-jump="need">Needs you <b data-xf="c-need">${need.filter(open).length}</b></span><span data-jump="epics">Epics ${live.length}</span><span data-jump="sess">Sessions 10</span></div>`
      + `<div class="sec need" data-sec="need" data-flip="h:need">Needs you <i>${need.filter(open).length}</i></div>${need.map(needCard).join('')}`
      + `<div class="sec" data-sec="epics" data-flip="h:epics">Epics <i>${live.length}</i></div>${live.map((k) => epicLine(k)).join('')}`
      + (just.length || earlier.length ? `${just.length ? `<div class="sec" data-flip="h:ended">Just ended <i>${just.length}</i></div>` : ''}${just.map((k) => epicLine(k)).join('')}`
        + (earlier.length ? `<button class="more" data-ended-toggle data-flip="h:earlier">${earlier.length} ended earlier ${P.endedOpen ? '▾' : '›'}</button>${P.endedOpen ? earlier.map((k) => epicLine(k, true)).join('') : ''}` : '') : '')
      + '<div class="sec" data-sec="sess" data-flip="h:sess">Sessions <i>10 · 3 new</i></div>'
      + EN.FOLDERS.slice(1, 4).map(([folder, , , ids]) => `<div class="fold" data-flip="f:${folder}">▾ ${folder.split('/').pop()}</div>${ids.map(sessLine).join('')}`).join('')
      + '<div class="more">+3 more sessions in 2 quiet folders</div>';
  }
  function drawHome(quiet) {
    const host = $('[data-home]');
    const draw = () => { host.innerHTML = homeHTML(); };
    if (quiet) draw(); else M.morph(host, draw);
  }

  // The notifications beside the phone, and the same list as a menu in the bar on a narrow screen.
  function drawNotes() {
    $('[data-notes]').innerHTML = NOTES.map((n) => `<button class="note${n.quiet ? ' quiet' : ''}${n.on() ? '' : ' gone'}" data-note="${n.id}"><svg viewBox="18 18 72 72" aria-hidden="true"><use href="#mark"/></svg>`
      + `<b>${n.t}</b><time>${n.quiet ? 'quiet' : 'now'}</time><span>${n.b}</span></button>`).join('');
    $('[data-wf-note]').innerHTML = '<option value="">a notification arrives…</option>' + NOTES.map((n) => `<option value="${n.id}">${n.t}</option>`).join('');
  }

  // ---------- a page ----------
  const sendBox = (state, placeholder, stop) => `<footer class="scr-foot"><div class="sendrow">${stop ? '<button class="btn stop" data-interrupt>■ Interrupt</button>' : ''}`
    + `<div class="sbox" data-state="${state}"><textarea rows="1" data-grow-box data-send placeholder="${placeholder}" aria-label="${placeholder}"${state === 'off' ? ' disabled' : ''}></textarea><button class="go" data-send-go aria-label="send">↑</button></div></div></footer>`;
  const monitor = (line) => `<div class="monitor"><div class="mh"><span>MONITOR</span><span class="v">progressing</span></div><div class="lim"><span>${line}</span></div></div>`;
  // What the page for a route holds: the body, a strip pinned above it, the foot, and how the body is padded.
  function parts(route) {
    const [p, a, b, c] = route.split(':');
    if (p === 'epic') return { html: EN.view(a, { phone: true }), cls: 'epic' };
    if (p === 'task') return { html: EN.taskPage(a, b, c, { phone: true }), cls: 'epic' };
    if (p === 'contract') { const k = EN.contract(a); return { html: k.body, foot: k.foot, cls: '' }; }
    if (p === 'session') {
      const k = EN.convo(route);
      return { html: `<div class="convo">${k.body}</div>`, strip: k.mon ? monitor(k.mon) : '', foot: sendBox(k.send[0], k.send[1], k.send[0] === 'working'), cls: 'talky', verb: k.send[2], end: true };
    }
    if (p === 'talk') {
      const k = EN.convo(route), L = EN.line(a);
      const strip = `<div class="ep"><button class="ep-line" data-go="epic:${a}"><span class="gl">${EN.glyph(a)}</span><span class="tx"><span class="l1"><b>${L.slug}</b><span class="pv">${EN.countShort(a)}</span></span>`
        + `<span class="ph">${L.phrase}</span></span><i class="chev">›</i></button></div>`;
      return { html: `<div class="convo">${k.body}</div>`, strip, foot: sendBox(k.send[0], k.send[1]), cls: 'talky', end: true };
    }
    const s = EN.SESS[a], st = EN.sessState(a), asks = a === 'beacon' && !EN.sess.answered;
    const q = a === 'beacon' ? `<div class="qcard${EN.sess.answered ? ' answered' : ''}"><div class="cap">CLAUDE IS ASKING</div><div class="title">Which index should I add?</div>`
      + s.opts.map((o, i) => `<div class="o${EN.sess.answered === i + 1 ? ' sel' : ''}" data-ph-answer="${i + 1}"><kbd>${i + 1}</kbd><span><b>${o}</b></span></div>`).join('') + '</div>' : '';
    const say = st === 'working' ? ['working', `Queue for ${s.label}, after this turn`, 'queued. It sends when this turn ends'] : asks ? ['waiting', `Answer ${s.label} in your own words`, `answer sent to ${s.label}`]
      : st === 'interrupted' ? ['off', `${s.label} is interrupted`, ''] : ['idle', `Reply to ${s.label}`, `sent to ${s.label}`];
    return { html: `<div class="convo"><div class="reply"><p>${s.text}</p></div>${q}</div>`, foot: sendBox(say[0], say[1], st === 'working'), cls: 'talky', verb: say[2], end: true };
  }
  let verb = '';
  function render(how = 'fade') {
    const route = P.route, home = route === 'home';
    homeScr.hidden = !home;
    pageScr.hidden = home;
    $$('.tp, .scrim', device).forEach((el) => el.remove());
    if (home) { drawHome(how !== 'state'); if (how === 'out') $('[data-home]').scrollTop = P.scroll.home || 0; }
    else {
      const k = parts(route);
      $('[data-frame]').innerHTML = EN.frame(route);
      $('[data-strip]').innerHTML = k.strip || '';
      $('[data-foot]').innerHTML = k.foot || '';
      body.className = `scr-body ${k.cls}`;
      verb = k.verb || '';
      if (how === 'state') M.morph(body, () => { body.innerHTML = k.html; }); else body.innerHTML = k.html;
      if (P.picker) device.insertAdjacentHTML('beforeend', `<div class="scrim" data-scrim></div>${EN.talkPicker(epicOf(route))}`);
      if (how !== 'state' && how !== 'quiet') body.scrollTop = how === 'out' && P.scroll[route] != null ? P.scroll[route] : k.end ? body.scrollHeight : 0;
    }
    // Going forward slides the page in from the right; going back, from the left.
    const scr = home ? homeScr : pageScr;
    if (how === 'in') M.enter(scr, { x: 24, ms: M.T.drill });
    else if (how === 'out') M.enter(scr, { x: -24, ms: M.T.drill });
    else if (how === 'fade') M.enter(scr, { ms: M.T.state });
    drawNotes();
  }
  const leave = () => { P.scroll[P.route] = P.route === 'home' ? $('[data-home]').scrollTop : body.scrollTop; };
  function go(route, how) {
    if (!route) return;
    const [p, a, b, c] = route.split(':');
    const to = p === 'task' ? `task:${a}:${b}:${EN.landing(a, b, c)}` : route;
    if (to === P.route) { if (P.picker) { P.picker = false; render('quiet'); } return; }
    leave();
    // An ended epic the owner has opened leaves Just ended once they are back on Home.
    if (to === 'home') P.opened.forEach((key) => P.left.add(key));
    if (p !== 'home' && p !== 'sess' && ended(a)) P.opened.add(a);
    const d = EN.crumbsOf(to).length - EN.crumbsOf(P.route).length;
    P.route = to;
    P.picker = false;
    render(how || (d > 0 ? 'in' : d < 0 ? 'out' : 'fade'));
  }
  const back = () => { const up = EN.parent(P.route); if (up) go(up); };
  // A notification lands on the page it is about, at the part it is about.
  function land(id) {
    const n = NOTES.find((x) => x.id === id);
    if (!n) return;
    go(n.go, 'in');
    if (n.sec) { const at = $(`[data-sec="${n.sec}"]`, body); if (at) body.scrollTop = at.offsetTop - body.offsetTop - 8; }
  }

  EN.changed = (key, quiet) => {
    if (key && !ended(key)) { P.opened.delete(key); P.left.delete(key); }
    const [p, a, b] = P.route.split(':');
    if (p === 'home') { drawHome(quiet); drawNotes(); return; }
    if (key ? p === 'sess' || a !== key : p !== 'sess') { drawNotes(); return; }
    if ((p === 'task' || p === 'session') && !EN.unit(a, b)) P.route = `epic:${a}`;
    render(quiet ? 'quiet' : 'state');
  };
  EN.said = toast;
  // Accepting a contract returns to the epic view, where the paragraph that opens the contract is marked once.
  EN.go = (route, how) => {
    go(route, how === 'accepted' ? 'out' : how);
    if (how !== 'accepted') return;
    body.scrollTop = 0;
    const para = $('.ev-para', body);
    if (para) para.classList.add('fresh');
  };

  // ---------- the transitions, replayable from the bar ----------
  const live = () => { if (ended('followups')) EN.setMoment('followups', 2, true); };
  const at = (route, i) => { if (i != null) EN.setMoment('followups', i, true); P.route = route; P.picker = false; render('fade'); };
  EN.replays = [
    ['phrase', 'a state phrase changes', async () => {
      at('home', 1);
      await M.wait(700);
      EN.setMoment('followups', 2);
    }],
    ['orbit', 'a task moves inward on the orrery', async () => {
      at('epic:followups', 0);
      body.scrollTop = $('[data-sec="build"]', body).offsetTop - body.offsetTop;
      await M.wait(900);
      EN.setMoment('followups', 1);
      await M.wait(1600);
      EN.setMoment('followups', 2);
    }],
    ['unit', 'a unit finishes', async () => {
      at('epic:followups', 3);
      body.scrollTop = $('.ev-under', body).offsetTop - body.offsetTop - 40;
      await M.wait(700);
      EN.setMoment('followups', 4);
    }],
    ['end', 'an epic ends', async () => {
      at('home', 4);
      await M.wait(700);
      EN.setMoment('followups', 5);
    }],
    ['open', 'line to epic view', async () => {
      live();
      at('home');
      await M.wait(700);
      go('epic:followups');
    }],
    ['deeper', 'one page deeper, and back', async () => {
      live();
      at('epic:followups');
      await M.wait(700);
      go('task:followups:judge-head-tail:2');
      await M.wait(1400);
      back();
    }],
    ['release', 'a release step failed: you retry, it carries on', async () => {
      EN.setMoment('offline', 0, true);
      at('home');
      await M.wait(1200);
      EN.setMoment('offline', 1);
    }],
    ['attn', 'an attention item arrives', async () => {
      EN.setMoment('rota', 0, true);
      P.gone = 'need-decision';
      at('home');
      await M.wait(700);
      P.gone = '';
      drawHome();
    }],
  ];

  function send(input) {
    const words = input.value.trim();
    if (!words) return;
    input.value = '';
    input.style.height = '';
    const [p, a, b] = P.route.split(':');
    // In a talk the message goes to the session, which answers through orrery's tools.
    if (p === 'talk') { EN.talkSend(a, b, words); body.scrollTop = body.scrollHeight; return; }
    if (verb) toast(verb);
  }

  document.addEventListener('click', (ev) => {
    const t = ev.target;
    if (ev.defaultPrevented || !(t instanceof Element)) return;   // a form or a command took this tap
    const note = t.closest('[data-note]');
    if (note) { land(note.dataset.note); return; }
    if (!device.contains(t)) return;
    // A session's question keeps its one tap.
    const answer = t.closest('[data-ph-answer]');
    if (answer) { if (!EN.sess.answered) { EN.sess.answered = Number(answer.dataset.phAnswer); toast(`answered beacon-schedule: ${EN.SESS.beacon.opts[EN.sess.answered - 1]}`); EN.changed('', false); } return; }
    if (t.closest('[data-ended-toggle]')) { P.endedOpen = !P.endedOpen; drawHome(); return; }
    const jump = t.closest('[data-jump]');
    if (jump) { const head = $(`[data-home] [data-sec="${jump.dataset.jump}"]`); if (head) head.scrollIntoView({ block: 'start', behavior: M.reduced() ? 'auto' : 'smooth' }); return; }
    if (t.closest('[data-interrupt]')) { toast('interrupted. The turn stops, and the step waits for a nudge'); return; }
    const sendGo = t.closest('[data-send-go]');
    if (sendGo) { send($('[data-send]', sendGo.parentElement)); return; }

    // Talk: a sheet with the sessions that touched this epic and a new one in its repository.
    if (t.closest('[data-talk]')) { P.picker = !P.picker; render('quiet'); return; }
    const to = t.closest('[data-talk-to]');
    if (to) { go(`talk:${to.dataset.talkTo}`); return; }
    if (t.closest('[data-scrim]')) { P.picker = false; render('quiet'); return; }

    const link = t.closest('[data-go]');
    if (link) go(link.dataset.go);
  });

  // The send box grows with its text, to eight lines.
  document.addEventListener('input', (ev) => {
    const box = ev.target;
    if (!box.matches('[data-grow-box]')) return;
    box.style.height = 'auto';
    const max = parseFloat(getComputedStyle(box).lineHeight) * 8;
    box.style.height = `${Math.min(box.scrollHeight, max)}px`;
    box.style.overflowY = box.scrollHeight > max ? 'auto' : 'hidden';
  });
  document.addEventListener('change', (ev) => {
    if (ev.target.matches('[data-wf-note]') && ev.target.value) { const id = ev.target.value; ev.target.value = ''; land(id); }
  });
  document.addEventListener('keydown', (ev) => {
    const typing = ev.target instanceof Element && ev.target.matches('input, textarea, select');
    if (typing) { if (ev.key === 'Enter' && !ev.shiftKey && ev.target.matches('[data-send]')) { ev.preventDefault(); send(ev.target); } return; }
    if (ev.key === 'ArrowLeft' || ev.key === 'Escape') { if (P.picker) { P.picker = false; render('quiet'); } else back(); }
  });

  // A link from the map names its page: #go=<route>, with &note=<id> for a notification and &m=<epic>.<moment> for a state.
  function fromHash() {
    const q = new URLSearchParams(location.hash.slice(1));
    if (q.get('m')) q.get('m').split(',').forEach((x) => { const [key, i] = x.split('.'); EN.setMoment(key, Number(i), true); });
    if (q.get('note')) land(q.get('note')); else if (q.get('go')) go(q.get('go'), 'fade');
  }
  render('quiet');
  fromHash();
  addEventListener('hashchange', fromHash);
})();
