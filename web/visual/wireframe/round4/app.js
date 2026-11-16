// orrery web view mockup: the list, and beside it the page the cursor is on. A page is an epic view, a phase's output,
// a worker's session, a contract, or an ordinary session, and every one has the same title row. Talk docks a conversation
// beside an epic view. Also here: the send box, the host meters, and the transitions. No data leaves the page.
(() => {
  const EN = window.EpicNow;
  const M = window.Motion;
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
  const stage = $('.stage');
  const read = $('.b-read');
  const nav = $('.b-list');
  const listEl = $('[data-list]');
  const dock = $('[data-dock]');
  const pin = $('[data-pin]');
  const pal = $('[data-pal]');
  const echoEl = $('[data-echo]');
  const DWELL_MS = 1000;
  const SAME = ['state', 'in', 'out', 'fade'];

  const S = {
    view: 'attention', cur: 'followups', route: 'epic:followups', filter: '',
    unseen: new Set(['beacon', 'graft', 'moon', 'holder', 'tidecache']),
    talk: null, picker: false,                                // the docked conversation, and whether Talk's menu is open
    scroll: {},                                               // where each page was left, so Back returns to the same place
    opened: new Set(), left: new Set(), endedOpen: false,     // just-ended epics: seen by the owner, then folded away
    gone: '',                                                 // a needs-you row held back, so its arrival can be replayed
  };
  let pendingG = false;
  let dwellTimer = 0;
  let echoTimer = 0;

  // ---------- the rows ----------
  const need = (id) => EN.NEED.find((n) => n.id === id);
  // The page a row stands for. A needs-you row about an epic stands for that epic view: what it asks is the first block there.
  function baseRoute(id) {
    if (EN.EPICS[id]) return `epic:${id}`;
    const n = need(id);
    return n && n.epic ? `epic:${n.epic}` : `sess:${n && n.session ? n.session : id}`;
  }
  const labelOf = (id) => EN.crumbsOf(baseRoute(id)).at(-1).t;
  const ended = (key) => EN.line(key).ended;
  const epicOf = (route) => (route.startsWith('sess:') ? '' : route.split(':')[1]);

  // What waits on the owner says what is waiting and why, and carries its action as a button.
  function needRow(n) {
    const s = n.session ? EN.SESS[n.session] : null;
    const answered = (n.id === 'beacon' && EN.sess.answered) || (n.resume && EN.resumed[n.resume]);
    const cls = ['b-row', 'nd-row', `k-${n.kind}`, answered ? 'answered' : 'need', S.unseen.has(n.id) ? 'unseen' : ''].join(' ');
    // A question's answers get their own line. Any other action sits at the end of the line that says where and how long.
    const opts = !answered && s && s.opts ? `<div class="b-opts">${s.opts.map((o, i) => `<span class="b-opt" data-answer="${i + 1}"><kbd>${i + 1}</kbd>${o}</span>`).join('')}</div>` : '';
    const acts = answered || opts ? ''
      : n.rel ? (EN.armed.rel ? '<button class="nd-act primary" data-rel="stop-yes">Stop the release</button><button class="nd-act" data-rel="stop-no">Keep it</button>'
        : '<button class="nd-act primary" data-rel="retry">Retry step 2</button><button class="nd-act" data-rel="stop">Stop the release…</button>')
        : n.resume ? `<button class="nd-act primary" data-resume="${n.resume}">Resume</button>`
          : `<button class="nd-act primary" data-go="${n.kind === 'contract' ? 'contract' : 'epic'}:${n.epic}">${n.act} ›</button>`;
    const said = !answered ? n.say : n.resume ? `<b>${s.label}</b> is working again. You resumed it.` : `<b>${s.label}</b> is working again. You answered ${EN.sess.answered}: ${s.opts[EN.sess.answered - 1]}.`;
    return `<div class="${cls}" data-row data-id="${n.id}" data-flip="r:${n.id}"><i class="nk" data-oc="kind:${n.kind}">${EN.icon(EN.KIND[n.kind].icon)}</i><div>`
      + `<div class="say">${said}</div>`
      + `<div class="meta"><span>${n.where}</span><span class="age">${answered ? (n.resume ? 'resumed just now' : 'answered just now') : `waiting ${n.age}`}</span>${acts ? `<span class="acts">${acts}</span>` : ''}</div>${opts}</div></div>`;
  }
  // An epic's line: its orrery, small; its name; a count that says what it counts; its state and how long; and what blocks it.
  function epicRow(key, old) {
    const L = EN.line(key);
    const cls = ['b-row', 'epic', L.exc ? L.exc.tone : '', L.ended ? 'ended' : '', old ? 'old' : '', L.ended && !S.opened.has(key) ? 'unseen' : ''].join(' ');
    return `<div class="${cls}" data-row data-id="${key}" data-flip="r:${key}"><span class="gl" data-oc="epic:${key}">${EN.glyph(key)}</span><div>`
      + `<div class="l1"><span class="label">${L.slug}</span><span class="pv" data-oc="count:${key}">${EN.countShort(key)}</span></div>`
      + `<div class="ph" data-xf="p:${key}">${L.phrase}</div>`
      + (L.exc ? `<div class="ex ${L.exc.tone}" data-flip="x:${key}" data-xf="x:${key}">${L.exc.text}</div>` : '') + '</div></div>';
  }
  function sessRow(id) {
    const s = EN.SESS[id], st = EN.sessState(id);
    if (id === 'beacon' && S.view === 'project') return needRow(need('beacon'));
    const body = s.tool ? `<span class="m">${s.tool[0]} ${s.tool[1]} ${EN.ticking(s.tool[2])}</span> · ${s.text}` : s.text;
    return `<div class="b-row s-${st}${S.unseen.has(id) ? ' unseen' : ''}" data-row data-id="${id}" data-flip="r:${id}"><i class="dot${st === 'working' ? ' spin' : ''}"></i><div>`
      + `<div class="l1"><span class="label">${s.label}</span><span class="proj">${s.proj}</span><span class="age">${s.age}</span></div><div class="l2">${body}</div></div></div>`;
  }
  const sec = (name, n, id, cls = '') => `<div class="b-sec ${cls}" data-sec="${id}" data-flip="s:${name}">${name} <i>${n}</i></div>`;

  // The list is drawn from the data on every change, so a row that changes group is the same row in a new place.
  function listHTML() {
    const words = S.filter.toLowerCase().split(/\s+/).filter(Boolean);
    const hit = (hay) => words.every((w) => hay.toLowerCase().includes(w));
    const epicHit = (key) => { const L = EN.line(key); return hit(`${L.slug} ${L.proj} ${L.host} ${L.plain} epic`); };
    const sessHit = (id) => { const s = EN.SESS[id]; return hit(`${s.label} ${s.proj} ${s.host} ${s.state}`); };
    const group = (head, rows) => (rows.length ? head(rows.length) + rows.join('') : '');
    if (S.view === 'project') {
      return EN.FOLDERS.map(([folder, host, epics, sessions]) => group(
        () => `<div class="b-sec proj" data-flip="s:${folder}">${folder} <i>${host}</i></div>`,
        epics.filter((k) => !S.left.has(k) && epicHit(k)).map((k) => epicRow(k)).concat(sessions.filter(sessHit).map(sessRow)),
      )).join('') + (words.length ? '' : '<div class="b-more">6 quiet projects</div>');
    }
    const live = EN.ORDER.filter((k) => !ended(k) && epicHit(k));
    const just = EN.ORDER.filter((k) => ended(k) && !S.left.has(k) && epicHit(k));
    const earlier = EN.ORDER.filter((k) => S.left.has(k) && epicHit(k));
    const needs = EN.needs().filter((n) => n.id !== S.gone && hit(`${n.kind} ${EN.plain(n.say)} ${n.where} waiting`));
    return group((n) => sec('Needs you', n, 'need', 'need'), needs.map(needRow))
      + group((n) => sec('Epics', n, 'epics'), live.map((k) => epicRow(k)))
      + (just.length || earlier.length ? (just.length ? sec('Just ended', just.length, 'ended') : '') + just.map((k) => epicRow(k)).join('')
        + (earlier.length ? `<button class="b-more" data-ended-toggle data-flip="s:earlier">${earlier.length} ended earlier ${S.endedOpen ? '▾' : '›'}</button>`
          + (S.endedOpen ? earlier.map((k) => epicRow(k, true)).join('') : '') : '') : '')
      + group((n) => sec('New since you looked', n, 'new'), EN.GROUPS.fresh.filter(sessHit).map(sessRow))
      + group((n) => sec('Working', n, 'work'), EN.GROUPS.working.filter(sessHit).map(sessRow))
      + group((n) => sec('Seen', `${n} · 6 quiet`, 'seen'), EN.GROUPS.seen.filter(sessHit).map(sessRow));
  }
  const open = (n) => !(n.id === 'beacon' && EN.sess.answered) && !(n.resume && EN.resumed[n.resume]);
  function renderList() {
    listEl.dataset.mode = S.view;
    listEl.innerHTML = listHTML();
    const row = rowEl(S.cur);
    if (row) row.classList.add('cur');
    const set = (name, n) => $$(`[data-n="${name}"]`).forEach((el) => { el.textContent = n; });
    set('need', EN.needs().filter((n) => n.id !== S.gone && open(n)).length);
    set('epics', EN.ORDER.filter((k) => !ended(k)).length);
    set('work', EN.GROUPS.working.length + (EN.sess.answered ? 1 : 0) + (EN.resumed.ephem ? 1 : 0));
    set('new', [...S.unseen].filter((id) => EN.SESS[id]).length);
  }
  // A redraw that carries the change across: rows glide, new ones arrive, changed phrases crossfade.
  const morphList = () => M.morph(nav, renderList);

  const rows = () => $$('[data-row]', listEl);
  const rowEl = (id) => $(`[data-row][data-id="${id}"]`, listEl);
  const openPane = () => $$('.b-panes > .b-pane').find((p) => !p.hidden);

  function echo(key, words) {
    echoEl.innerHTML = `<kbd>${key}</kbd><span>${words}</span>`;
    echoEl.classList.add('on');
    clearTimeout(echoTimer);
    echoTimer = setTimeout(() => echoEl.classList.remove('on'), 2600);
  }
  function flash(key) {
    $$('.keys .k').forEach((k) => {
      if (!(k.dataset.keys || '').split(' ').includes(key)) return;
      $$('kbd', k).forEach((cap) => {
        cap.classList.add('hit');
        setTimeout(() => cap.classList.remove('hit'), 420);
      });
    });
  }

  function setSeen(id, seen) {
    if (EN.EPICS[id]) {
      if (!ended(id)) return;
      if (seen) S.opened.add(id); else S.opened.delete(id);
    }
    if (seen) S.unseen.delete(id); else S.unseen.add(id);
    renderList();
  }
  const isUnseen = (id) => (EN.EPICS[id] ? ended(id) && !S.opened.has(id) : S.unseen.has(id));

  // The live reply types itself out once, so the streaming state reads as streaming.
  function stream(pane) {
    $$('[data-stream]', pane).forEach((el) => {
      if (el.dataset.played || M.reduced()) return;
      el.dataset.played = '1';
      const full = el.dataset.stream;
      let at = Math.floor(full.length * 0.45);
      el.textContent = full.slice(0, at);
      const timer = setInterval(() => {
        at = Math.min(full.length, at + 2);
        el.textContent = full.slice(0, at);
        const box = el.closest('[data-scroll]');
        if (box) box.scrollTop = box.scrollHeight;
        if (at >= full.length) clearInterval(timer);
      }, 45);
    });
  }

  // ---------- the send box ----------
  // Its edge and its placeholder say what state the conversation is in and what sending does. Nothing is written under it.
  function composer(scope, state, placeholder, verb) {
    const box = $('[data-composer]', scope);
    if (!box.firstChild) {
      box.innerHTML = `<div class="in"><kbd>i</kbd><textarea data-send rows="1"></textarea>${box.dataset.ctx ? `<span class="ctx">${box.dataset.ctx}</span>` : ''}</div>`
        + '<div class="foot"><span>⏎ send</span><span>⇧⏎ newline</span><span>esc esc interrupt</span></div>';
    }
    box.dataset.state = state;
    box.dataset.verb = verb || 'sent';
    const input = $('textarea', box);
    input.placeholder = placeholder;
    input.setAttribute('aria-label', placeholder);
    input.disabled = state === 'off';
  }
  function grow(input) {
    input.style.height = 'auto';
    const max = parseFloat(getComputedStyle(input).lineHeight) * 8;
    input.style.height = `${Math.min(input.scrollHeight, max)}px`;
    input.style.overflowY = input.scrollHeight > max ? 'auto' : 'hidden';
  }
  const SAY = {
    working: (name) => ['working', `Queue for ${name}, after this turn`, 'queued. It sends when this turn ends'],
    waiting: (name) => ['waiting', `Answer ${name}`, `answer sent to ${name}`],
    interrupted: (name) => ['off', `${name} is interrupted`, ''],
    done: (name) => ['idle', `Reply to ${name}`, `sent to ${name}`],
  };

  // ---------- the page beside the list ----------
  // Talk's conversation, docked beside the page of the epic it is about.
  function drawDock() {
    const on = !!S.talk && epicOf(S.route) === S.talk.key;
    read.classList.toggle('has-dock', on);
    if (!on) return;
    const { key, sid } = S.talk, t = EN.talkOf(key, sid), body = $('[data-dk-body]', dock), c = EN.convo(`talk:${key}:${sid}`);
    $('[data-dk-name]', dock).textContent = t.label;
    $('[data-dk-where]', dock).textContent = t.where;
    $('[data-dk-link]', dock).innerHTML = `${EN.glyph(key)}<span>${EN.EPICS[key].slug}</span>`;
    body.innerHTML = c.body;
    body.scrollTop = body.scrollHeight;
    composer(dock, ...c.send);
  }
  function openTalk(key, sid) {
    S.picker = false;
    S.talk = { key, sid };
    if (epicOf(S.route) !== key) go(`epic:${key}`); else show('quiet');
    const input = $('[data-send]', dock);
    if (sid === 'new') input.focus();
  }
  function closeTalk() {
    S.talk = null;
    drawDock();
  }

  // Shows the page S.route names. how names the change, which picks the motion.
  function show(how = 'move') {
    const route = S.route, [p, a, b, c] = route.split(':');
    const name = p === 'sess' ? EN.SESS[a].pane || 'generic' : p === 'session' ? 'convo' : p === 'contract' ? 'contract' : 'epic';
    const was = openPane();
    let pane = null;
    $$('.b-panes > .b-pane').forEach((el) => {
      el.hidden = el.dataset.pane !== name;
      if (!el.hidden) pane = el;
    });
    const switched = was !== pane;
    const head = $('[data-frame]', pane);
    head.innerHTML = EN.frame(route);
    if (S.picker && p !== 'sess') $('.fr-acts', head).insertAdjacentHTML('beforeend', EN.talkPicker(a));
    const box = $('.b-scroll', pane);
    if (name === 'epic') {
      const host = $('[data-en-host]', pane), html = p === 'task' ? EN.taskPage(a, b, c) : EN.view(a);
      if (!switched && how === 'state') M.morph(host, () => { host.innerHTML = html; });
      else if (!switched && SAME.includes(how)) M.swap(host, html, how);
      else host.innerHTML = html;
    }
    if (name === 'contract') {
      const k = EN.contract(a), foot = $('[data-contract-foot]', pane);
      $('[data-contract-body]', pane).innerHTML = k.body;
      foot.innerHTML = k.foot;
      foot.closest('.b-send').hidden = !k.foot;
    }
    if (name === 'convo') {
      const k = EN.convo(route);
      $('[data-convo]', pane).innerHTML = k.body;
      composer(pane, ...k.send);
    }
    if (p === 'sess') {
      const s = EN.SESS[a], st = EN.sessState(a);
      if (name === 'generic') { $('[data-g-age]', pane).textContent = s.age || ''; $('[data-g-text]', pane).innerHTML = s.text; }
      if (name === 'question') {
        $$('[data-qcard]', pane).forEach((card) => {
          card.classList.toggle('answered', !!EN.sess.answered);
          $$('.o', card).forEach((o) => o.classList.toggle('sel', o.dataset.answer === String(EN.sess.answered)));
        });
      }
      composer(pane, ...(a === 'beacon' && !EN.sess.answered ? ['waiting', 'Answer beacon-schedule in your own words', 'answer sent to beacon-schedule'] : SAY[st](s.label)));
    }
    drawDock();
    if (how === 'quiet' || how === 'state') return;
    // Back returns to where the page was left. Any other arrival starts at the top, or at the end of a conversation.
    const talky = name === 'convo' || name === 'lamp' || name === 'question';
    box.scrollTop = how === 'out' && S.scroll[route] != null ? S.scroll[route] : talky ? box.scrollHeight : 0;
    if (switched) stream(pane);
    if (!switched && SAME.includes(how)) return;
    if (how === 'in') M.enter(box, { x: 24, ms: M.T.drill });
    else if (how === 'out') M.enter(box, { x: -24, ms: M.T.drill });
    else if (how === 'open') M.enter(box, { y: 8, ms: M.T.drill });
    else M.enter(box, { ms: M.T.press });
  }

  // ---------- moving ----------
  function setCursor(id) {
    const prev = S.cur;
    // An ended epic the owner has opened leaves Just ended once the cursor moves on.
    const leaves = prev !== id && EN.EPICS[prev] && ended(prev) && S.opened.has(prev) && !S.left.has(prev);
    S.cur = id;
    if (leaves) { S.left.add(prev); morphList(); }
    $$('[data-row].cur', listEl).forEach((r) => r.classList.remove('cur'));
    const row = rowEl(id);
    if (row) { row.classList.add('cur'); row.scrollIntoView({ block: 'nearest' }); }
    clearTimeout(dwellTimer);
    if (isUnseen(id)) {
      dwellTimer = setTimeout(() => {
        if (S.cur !== id) return;
        setSeen(id, true);
        echo('1 s', `${labelOf(id)} seen, here and on the phone`);
      }, DWELL_MS);
    }
    return row;
  }
  const leave = () => { const box = openPane() && $('.b-scroll', openPane()); if (box) S.scroll[S.route] = box.scrollTop; };
  // The cursor moves to a row and the page follows it.
  function moveTo(id, how = 'move') {
    if (!id || !(EN.EPICS[id] || EN.SESS[id] || need(id))) return null;
    leave();
    const row = setCursor(id);
    S.route = baseRoute(id);
    S.picker = false;
    if (S.talk && S.talk.key !== epicOf(S.route)) S.talk = null;
    show(how);
    return row;
  }
  // Opens a page by its route. The cursor follows to the row the page belongs to.
  function go(route, how) {
    if (!route) return;
    // The list is always open on the web, so its crumb points at the row this page belongs to.
    if (route === 'home') { const row = rowEl(S.cur); if (row) { row.scrollIntoView({ block: 'nearest' }); M.enter(row, { x: -8, ms: M.T.layout }); } return; }
    const [p, a, b, c] = route.split(':');
    if (p === 'talk') { openTalk(a, b); return; }
    const to = p === 'task' ? `task:${a}:${b}:${EN.landing(a, b, c)}` : route;
    if (to === S.route) { if (S.picker) { S.picker = false; show('quiet'); } return; }
    leave();
    const from = S.route, root = p === 'sess' ? to : `epic:${a}`;
    if (baseRoute(S.cur) !== root) {
      if (!rowEl(a) && S.left.has(a)) { S.left.delete(a); renderList(); }
      if (rowEl(a)) setCursor(a);
      if (S.talk && S.talk.key !== a) S.talk = null;
    }
    const d = EN.crumbsOf(to).length - EN.crumbsOf(from).length;
    S.route = to;
    S.picker = false;
    show(how || (epicOf(from) !== epicOf(to) ? 'open' : d > 0 ? 'in' : d < 0 ? 'out' : 'fade'));
  }
  function step(delta) {
    const all = rows();
    const at = all.findIndex((r) => r.dataset.id === S.cur);
    const next = all[Math.max(0, Math.min(all.length - 1, at + delta))];
    if (next && next.dataset.id !== S.cur) moveTo(next.dataset.id);
  }
  function nextUnseen() {
    const all = rows();
    const at = all.findIndex((r) => r.dataset.id === S.cur);
    for (let i = 1; i <= all.length; i += 1) {
      const row = all[(at + i) % all.length];
      if (isUnseen(row.dataset.id)) return moveTo(row.dataset.id);
    }
    return null;
  }
  // A needs-you row has no twin in the project view. The row it is about stands in.
  const twin = (id) => { const n = need(id); return n ? n.epic || n.session : ''; };
  function setView(next) {
    S.view = next;
    $$('[data-view]').forEach((b) => b.classList.toggle('on', b.dataset.view === S.view));
    renderList();
    if (!rowEl(S.cur)) moveTo(rowEl(twin(S.cur)) ? twin(S.cur) : rows()[0].dataset.id);
    else rowEl(S.cur).scrollIntoView({ block: 'nearest' });
  }
  function applyFilter(words) {
    S.filter = words;
    renderList();
    if (!rowEl(S.cur) && rows()[0]) moveTo(rows()[0].dataset.id);
  }

  // ---------- one crumb deeper, one crumb back ----------
  function deeper() {
    const [p, a, b, c] = S.route.split(':');
    if (p === 'epic') {
      const m = EN.moment(a), u = m.units.find((x) => x.live) || m.units[0];
      if (u) go(`task:${a}:${u.slug}:0`);
    } else if (p === 'task' && $('.ph-sess [data-go]', openPane())) go(`session:${a}:${b}:${c}`);
  }
  function back() {
    const up = EN.parent(S.route);
    if (!up || up === 'home') return false;
    go(up);
    return true;
  }
  function togglePicker() {
    if (!epicOf(S.route)) { echo('t', 'Talk is for an epic. This page is a session already'); return; }
    S.picker = !S.picker;
    show('quiet');
    if (S.picker) { const first = $('[data-tp] button', openPane()); if (first) first.focus(); }
  }

  // A change in an epic redraws the list and the page, if the page shows that epic. The cursor stays on its row wherever the row goes.
  EN.changed = (key, quiet) => {
    if (key && !ended(key)) { S.opened.delete(key); S.left.delete(key); }
    const draw = () => { renderList(); const row = rowEl(S.cur); if (row) row.scrollIntoView({ block: 'nearest' }); };
    if (quiet) draw(); else M.morph(nav, draw);
    // A needs-you row that has been dealt with is gone, so the cursor moves to the epic it was about.
    if (!rowEl(S.cur)) setCursor(rowEl(twin(S.cur)) ? twin(S.cur) : rows()[0].dataset.id);
    const [p, a, b] = S.route.split(':');
    if (key ? p === 'sess' || a !== key : p !== 'sess') return;
    if ((p === 'task' || p === 'session') && !EN.unit(a, b)) S.route = `epic:${a}`;
    if (p === 'contract' && EN.moment(a).phase !== 'contract' && !quiet) S.route = `epic:${a}`;
    show(quiet ? 'quiet' : 'state');
    clearTimeout(dwellTimer);
    if (S.cur === key && ended(key)) dwellTimer = setTimeout(() => { if (S.cur === key && ended(key)) setSeen(key, true); }, DWELL_MS);
  };
  EN.said = (words) => echo('▸', words);
  // Accepting a contract returns to the epic view, where the paragraph that opens the contract is marked once.
  EN.go = (route, how) => {
    go(route, how === 'accepted' ? 'out' : how);
    if (how !== 'accepted') return;
    const para = $('.ev-para', openPane());
    if (para) { openPane().querySelector('.b-scroll').scrollTop = 0; para.classList.add('fresh'); }
  };

  function answer(number) {
    if (EN.sess.answered) return;
    EN.sess.answered = number;
    S.unseen.delete('beacon');
    renderList();
    if (S.route === 'sess:beacon') show('quiet');
    echo(String(number), `answered beacon-schedule: ${EN.SESS.beacon.opts[number - 1]}. Sent with the question hash`);
  }

  function queueOf(input) {
    const pane = input.closest('.b-pane');
    return pane ? $('[data-queue]', pane) : null;
  }
  function syncQueue(queue) {
    const n = $$('.qi', queue).length;
    $('[data-qn]', queue).textContent = n;
    queue.hidden = n === 0;
    queue.nextElementSibling.classList.toggle('q', n > 0);
  }

  function toggleSplit() {
    if (read.classList.contains('split')) {
      read.classList.remove('split');
      pin.hidden = true;
      pin.replaceChildren();
      echo('|', 'split closed');
      return;
    }
    const pane = openPane();
    if (!pane) return;
    const copy = pane.cloneNode(true);
    $$('[data-flip], [data-xf], [data-en-host], [data-go]', copy).forEach((el) => { ['data-flip', 'data-xf', 'data-en-host', 'data-go'].forEach((name) => el.removeAttribute(name)); });
    pin.replaceChildren(copy);
    pin.hidden = false;
    read.classList.add('split');
    $$('[data-stream]', copy).forEach((el) => { el.textContent = el.dataset.stream; });
    $$('[data-scroll]', copy).forEach((box) => { box.scrollTop = box.scrollHeight; });
    echo('|', `${EN.crumbsOf(S.route).at(-1).t} pinned on the right. The cursor drives the left`);
  }

  // ---------- host meters ----------
  function meters() {
    $('[data-meters]').innerHTML = '<div class="mt-panel" data-mt-panel hidden><div class="mt-in">'
      + EN.HOSTS.map(([host, vals]) => `<div class="mt-host"><b>${host}</b>${vals.map(([k, v]) =>
        `<span><em>${k}</em>${v}${v.endsWith('%') ? `<i style="--v:${parseInt(v, 10)}%"></i>` : ''}</span>`).join('')}</div>`).join('') + '</div></div>'
      + `<button class="mt-line" data-mt-toggle aria-expanded="false" aria-label="host meters"><span class="hs">${EN.HOSTS.map(([host, vals]) => `<span><b>${host}</b> ${vals[0][0]} ${vals[0][1]}</span>`).join('')}</span><kbd>m</kbd></button>`;
  }
  function toggleMeters() {
    const panel = $('[data-mt-panel]'), line = $('[data-mt-toggle]');
    const opening = panel.hidden;
    line.setAttribute('aria-expanded', String(opening));
    if (opening) { panel.hidden = false; M.open(panel); } else M.close(panel, () => { panel.hidden = true; });
  }

  // ---------- the transitions, replayable from the bar ----------
  const live = () => { if (ended('followups')) EN.setMoment('followups', 2, true); };
  EN.replays = [
    ['phrase', 'a state phrase changes', async () => {
      EN.setMoment('followups', 1, true);
      await M.wait(700);
      EN.setMoment('followups', 2);
    }],
    ['orbit', 'a task moves inward on the orrery', async () => {
      EN.setMoment('followups', 0, true);
      moveTo('followups', 'quiet');
      await M.wait(900);
      EN.setMoment('followups', 1);
      await M.wait(1600);
      EN.setMoment('followups', 2);
    }],
    ['unit', 'a unit finishes', async () => {
      EN.setMoment('followups', 3, true);
      moveTo('followups', 'quiet');
      await M.wait(700);
      EN.setMoment('followups', 4);
    }],
    ['end', 'an epic ends', async () => {
      EN.setMoment('followups', 4, true);
      moveTo('followups', 'quiet');
      await M.wait(700);
      EN.setMoment('followups', 5);
    }],
    ['open', 'line to epic view', async () => {
      live();
      moveTo('lamp', 'quiet');
      await M.wait(600);
      M.expandFrom(moveTo('followups', 'open'), $('.b-pane[data-pane="epic"]'));
    }],
    ['deeper', 'one crumb deeper, and back', async () => {
      live();
      moveTo('followups', 'quiet');
      await M.wait(600);
      go('task:followups:judge-head-tail:2');
      await M.wait(1400);
      back();
    }],
    ['dock', 'the talk dock', async () => {
      moveTo('rota', 'quiet');
      await M.wait(600);
      openTalk('rota', 'rotaplan');
      await M.wait(1400);
      closeTalk();
    }],
    ['release', 'a release step failed: you retry, it carries on', async () => {
      EN.setMoment('offline', 0, true);
      moveTo('need-release', 'quiet');
      await M.wait(1200);
      EN.setMoment('offline', 1);
    }],
    ['attn', 'an attention item arrives', async () => {
      EN.setMoment('rota', 0, true);
      S.gone = 'need-decision';
      if (S.cur === S.gone) moveTo('rota', 'quiet');
      renderList();
      await M.wait(700);
      S.gone = '';
      morphList();
    }],
  ];

  // ---------- input ----------
  document.addEventListener('click', (ev) => {
    const target = ev.target;
    if (ev.defaultPrevented || !(target instanceof Element)) return;   // a form or a command took this click
    if (target.closest('[data-keys-toggle]')) { stage.classList.toggle('nokeys'); return; }
    if (target === pal) { pal.classList.remove('on'); return; }
    const viewButton = target.closest('[data-view]');
    if (viewButton) { setView(viewButton.dataset.view); return; }
    const ans = target.closest('[data-answer]');
    if (ans) { answer(Number(ans.dataset.answer)); return; }
    const comp = target.closest('[data-compaction]');
    if (comp) { comp.classList.toggle('open'); return; }
    if (target.closest('[data-mt-toggle]')) { toggleMeters(); return; }
    if (target.closest('[data-ended-toggle]')) { S.endedOpen = !S.endedOpen; morphList(); return; }
    const jump = target.closest('[data-jump]');
    if (jump) { const head = $(`[data-sec="${jump.dataset.jump}"]`, listEl); if (head) head.scrollIntoView({ block: 'start', behavior: M.reduced() ? 'auto' : 'smooth' }); return; }

    // Talk: its menu, a session picked from it, and the dock.
    if (target.closest('[data-talk]')) { togglePicker(); return; }
    const to = target.closest('[data-talk-to]');
    if (to) { openTalk(...to.dataset.talkTo.split(':')); return; }
    if (target.closest('[data-dock-x]')) { closeTalk(); return; }
    if (target.closest('[data-dk-link]')) { go(`epic:${S.talk.key}`); return; }
    if (S.picker && !target.closest('[data-tp]')) { S.picker = false; show('quiet'); }

    // A link to a page. One inside a list row first puts the cursor on that row.
    const link = target.closest('[data-go]');
    const row = target.closest('[data-row]');
    if (link) {
      if (row && listEl.contains(row) && row.dataset.id !== S.cur) setCursor(row.dataset.id);
      go(link.dataset.go);
      return;
    }
    if (row && listEl.contains(row)) {
      const fresh = row.dataset.id !== S.cur || S.route !== baseRoute(row.dataset.id);
      const el = moveTo(row.dataset.id, 'open');
      if (fresh && epicOf(S.route)) M.expandFrom(el, $('.b-pane[data-pane="epic"]'));
    }
  });

  document.addEventListener('input', (ev) => {
    if (ev.target.matches('[data-filter]')) applyFilter(ev.target.value);
    if (ev.target.matches('[data-send]')) grow(ev.target);
  });

  document.addEventListener('keydown', (ev) => {
    const typing = ev.target instanceof Element && ev.target.matches('input, textarea, select');
    const key = ev.key;

    if ((ev.metaKey || ev.ctrlKey) && key.toLowerCase() === 'k') {
      ev.preventDefault();
      pal.classList.toggle('on');
      flash('pal');
      return;
    }
    if (ev.metaKey || ev.ctrlKey || ev.altKey) return;

    if (key === 'Escape') {
      flash('Escape');
      if (pal.classList.contains('on')) { pal.classList.remove('on'); return; }
      if (S.picker) { S.picker = false; show('quiet'); return; }
      if (typing) {
        if (ev.target.matches('[data-filter]')) { ev.target.value = ''; applyFilter(''); }
        ev.target.blur();
        return;
      }
      if (S.talk) { closeTalk(); return; }
      back();
      return;
    }
    if (typing) {
      const input = ev.target;
      if (!input.matches('[data-send]')) { if (key === 'Enter') input.blur(); return; }
      const queue = queueOf(input);
      if (key === 'Enter' && !ev.shiftKey) {
        ev.preventDefault();
        const words = input.value.trim();
        if (!words) return;
        input.value = '';
        grow(input);
        // In the dock the message goes to the session, which answers through orrery's tools.
        if (input.closest('[data-dock]')) { EN.talkSend(S.talk.key, S.talk.sid, words); return; }
        if (queue) {
          const entry = document.createElement('div');
          entry.className = 'qi';
          entry.textContent = words;
          queue.append(entry);
          syncQueue(queue);
          M.enter(entry, { y: 6 });
        }
        echo('⏎', input.closest('[data-composer]').dataset.verb);
      } else if (key === 'ArrowUp' && !input.value && queue) {
        const entries = $$('.qi', queue);
        const newest = entries[entries.length - 1];
        if (newest) {
          input.value = newest.textContent;
          newest.remove();
          syncQueue(queue);
          grow(input);
          echo('↑', 'the newest queued message is back in the box');
          ev.preventDefault();
        }
      }
      return;
    }
    if (pal.classList.contains('on')) return;

    const wasG = pendingG;
    pendingG = false;
    let handled = true;
    switch (key) {
      case '?': stage.classList.toggle('nokeys'); break;
      case 'j': case 'ArrowDown': step(1); break;
      case 'k': case 'ArrowUp': step(-1); break;
      case 'l': case 'ArrowRight': deeper(); break;
      case 'h': case 'ArrowLeft': back(); break;
      case 't': togglePicker(); break;
      case 'g':
        if (wasG) moveTo(rows()[0].dataset.id); else pendingG = true;
        break;
      case 'G': moveTo(rows().at(-1).dataset.id); break;
      case 'a': setView('attention'); echo('a', 'by attention'); break;
      case 'p': setView('project'); echo('p', 'by project'); break;
      case '|': toggleSplit(); break;
      case 'm': toggleMeters(); break;
      case 'n':
        if (!nextUnseen()) echo('n', 'nothing new');
        break;
      case 'w': {
        const row = rows().find((r) => r.classList.contains('need'));
        if (row) moveTo(row.dataset.id);
        break;
      }
      case 'x': case 'Enter':
        clearTimeout(dwellTimer);
        setSeen(S.cur, true);
        echo(key === 'x' ? 'x' : '⏎', `${labelOf(S.cur)} seen, here and on the phone`);
        break;
      case 'u':
        clearTimeout(dwellTimer);
        setSeen(S.cur, false);
        echo('u', `${labelOf(S.cur)} marked unseen`);
        break;
      case 'e':
        setSeen(S.cur, true);
        if (!nextUnseen()) echo('e', 'seen. Nothing new left');
        break;
      case '1': case '2': case '3':
        if (S.cur === 'beacon' && !EN.sess.answered) answer(Number(key));
        else echo(key, 'answers a question when the cursor is on its row');
        break;
      case '/':
        ev.preventDefault();
        $('[data-filter]').focus();
        break;
      case 'i': {
        const scope = S.talk && read.classList.contains('has-dock') ? dock : openPane();
        const input = scope && $('[data-send]:not(:disabled)', scope);
        if (input && !input.closest('[hidden]')) { ev.preventDefault(); input.focus(); }
        break;
      }
      case 'o': $$('[data-compaction]', openPane() || document).forEach((c) => c.classList.toggle('open')); break;
      case 'N': pal.classList.add('on'); break;
      default: handled = false;
    }
    if (handled) { flash(key); if (key !== '/' && key !== 'i') ev.preventDefault(); }
  });

  // A link from the map or a notification names its page: #go=<route>, with &talk=<session> and &m=<epic>.<moment> for a state.
  function fromHash() {
    const q = new URLSearchParams(location.hash.slice(1));
    if (q.get('m')) q.get('m').split(',').forEach((x) => { const [key, i] = x.split('.'); EN.setMoment(key, Number(i), true); });
    if (q.get('go')) go(q.get('go'), 'quiet');
    if (q.get('talk')) openTalk(epicOf(S.route), q.get('talk'));
  }

  if (innerWidth < 1680) stage.classList.add('nokeys');
  meters();
  renderList();
  moveTo('followups', 'quiet');
  listEl.scrollTop = 0;                                     // the page opens on what needs you
  fromHash();
  addEventListener('hashchange', fromHash);
})();
