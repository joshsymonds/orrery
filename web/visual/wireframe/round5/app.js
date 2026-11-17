// orrery web view mockup: the inbox, and the page the cursor is on. A page is an epic view, a phase's output, a worker's
// session, a contract, or an ordinary session, and every one has the same title row with its own meters. The inbox is
// docked while you are choosing and hides once a page is open; the orrery mark in the title row pops it back out. Talk
// docks a conversation beside an epic view. Also here: the send box, Ask, and the transitions. No data leaves the page.
(() => {
  const EN = window.EpicNow;
  const M = window.Motion;
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
  const stage = $('.stage');
  const mock = $('.mock.b');
  const read = $('.b-read');
  const nav = $('.b-list');
  const listEl = $('[data-list]');
  const dock = $('[data-dock]');
  const pin = $('[data-pin]');
  const pal = $('[data-pal]');
  const askp = $('[data-askp]');
  const echoEl = $('[data-echo]');
  const HOVER_IN_MS = 120, HOVER_OUT_MS = 280, SEND_MS = 1100;
  const DWELL_MS = 1000;
  const SAME = ['state', 'in', 'out', 'fade'];

  const S = {
    view: 'attention', cur: 'followups', route: 'epic:followups', filter: '',
    unseen: new Set(['beacon', 'graft', 'moon', 'holder', 'tidecache']),
    talk: null, picker: false,                                // the docked conversation, and whether Talk's menu is open
    scroll: {},                                               // where each page was left, so Back returns to the same place
    opened: new Set(), left: new Set(), endedOpen: false,     // just-ended epics: seen by the owner, then folded away
    gone: '',                                                 // a needs-you row held back, so its arrival can be replayed
    sel: false,                                               // a page has been opened: until then the inbox is the page
    pin: false,                                               // the owner keeps the inbox docked beside the page
    fly: '',                                                  // the hidden inbox, popped out over the page: by 'hover' or by 'keys'
    flyFrom: null,                                            // where the reader was when the inbox popped out, for esc
  };
  let hoverTimer = 0;
  let pendingG = false;
  let dwellTimer = 0;
  let echoTimer = 0;

  // ---------- the rows ----------
  const need = (id) => EN.NEED.find((n) => n.id === id);
  // The page a row stands for. A needs-you row about a session stands for that conversation, and one about an epic for
  // that epic view: what it asks is the first block there. A contract is read in the conversation that wrote it.
  function baseRoute(id) {
    if (EN.EPICS[id]) return `epic:${id}`;
    const n = need(id);
    return n && n.session ? `sess:${n.session}` : n && n.epic ? `epic:${n.epic}` : `sess:${id}`;
  }
  // What a row is called in words: an epic by its name, a session by what it has been doing.
  const labelOf = (id) => { const x = EN.crumbsOf(baseRoute(id)).at(-1); return x.sum || x.t; };
  const ended = (key) => EN.line(key).ended;
  const epicOf = (route) => (route.startsWith('sess:') ? '' : route.split(':')[1]);
  // The list row a route belongs to: its own, or the needs-you row that stands for its session.
  const rowIdOf = (a) => (rowEl(a) ? a : (EN.needs().find((n) => n.session === a && rowEl(n.id)) || {}).id || '');

  // What waits on the owner: what is asked, in a line, then where, and its action as a button. The whole sentence is the row's hover text.
  function needRow(n) {
    const s = n.session ? EN.SESS[n.session] : null;
    const answered = (n.id === 'beacon' && EN.sess.answered) || (n.resume && EN.resumed[n.resume]);
    const cls = ['b-row', 'nd-row', `k-${n.kind}`, answered ? 'answered' : 'need', S.unseen.has(n.id) ? 'unseen' : ''].join(' ');
    const opts = !answered && s && s.opts ? `<div class="b-opts">${s.opts.map((o, i) => `<span class="b-opt" data-answer="${i + 1}"><kbd>${i + 1}</kbd>${o}</span>`).join('')}</div>` : '';
    const acts = answered || opts ? ''
      : n.rel ? (EN.armed.rel ? '<button class="nd-act primary" data-rel="stop-yes">Stop it</button><button class="nd-act" data-rel="stop-no">Keep</button>'
        : '<button class="nd-act primary" data-rel="retry">Retry</button><button class="nd-act" data-rel="stop">Stop…</button>')
        : n.resume ? `<button class="nd-act primary" data-resume="${n.resume}">Resume</button>`
          : `<button class="nd-act primary" data-go="${baseRoute(n.id)}">${n.kind === 'contract' ? 'Read' : n.act} ›</button>`;
    const said = !answered ? n.short : n.resume ? 'Working again. You resumed it.' : `Working again. You answered ${EN.sess.answered}: ${s.opts[EN.sess.answered - 1]}.`;
    return `<div class="${cls}" data-row data-id="${n.id}" data-flip="r:${n.id}" title="${EN.plain(n.say)}"><i class="nk" data-oc="kind:${n.kind}">${EN.icon(EN.KIND[n.kind].icon)}</i><div>`
      + `<div class="l1"><span class="say">${said}</span><span class="age">${answered ? 'now' : n.age}</span></div>`
      + `<div class="meta"><span class="sub">${n.sub}</span>${acts ? `<span class="acts">${acts}</span>` : ''}</div>${opts}</div></div>`;
  }
  // An epic's line: its orrery, small; its name; how many requirements are proven; its state and how long; and what blocks it.
  function epicRow(key, old) {
    const L = EN.line(key);
    const cls = ['b-row', 'epic', L.exc ? L.exc.tone : '', L.ended ? 'ended' : '', old ? 'old' : '', L.ended && !S.opened.has(key) ? 'unseen' : ''].join(' ');
    return `<div class="${cls}" data-row data-id="${key}" data-flip="r:${key}"><span class="gl" data-oc="epic:${key}">${EN.glyph(key)}</span><div>`
      + `<div class="l1"><span class="label">${L.slug}</span><span class="pv" data-oc="count:${key}">${EN.countShort(key)}</span></div>`
      + `<div class="ph" data-xf="p:${key}">${L.phrase}</div>`
      + (L.exc ? `<div class="ex ${L.exc.tone}" data-flip="x:${key}" data-xf="x:${key}" title="${EN.plain(L.exc.text)}">${L.exc.text}</div>` : '') + '</div></div>';
  }
  // A session's line: its directory, then what it has been doing. By project the directory is the group's head, so the line is the summary alone.
  function sessRow(id) {
    const s = EN.SESS[id], st = EN.sessState(id), n = EN.needs().find((x) => x.session === id && x.id !== S.gone);
    if (n && S.view === 'project') return needRow(n);
    const doing = st === 'working' && s.tool ? `<span class="st">${s.tool[0]} ${EN.ticking(s.tool[2])}</span>` : '';
    const top = S.view === 'project' ? `<span class="sum">${s.sum}</span>${doing}<span class="age">${s.age}</span>` : `<span class="proj">${s.proj}</span>${doing}<span class="age">${s.age}</span>`;
    return `<div class="b-row s-${st}${S.unseen.has(id) ? ' unseen' : ''}" data-row data-id="${id}" data-flip="r:${id}" title="${EN.plain(s.sum)}"><i class="dot${st === 'working' ? ' spin' : ''}"></i><div>`
      + `<div class="l1">${top}</div>${S.view === 'project' ? '' : `<div class="l2">${s.sum}</div>`}</div></div>`;
  }
  const sec = (name, n, id, cls = '') => `<div class="b-sec ${cls}" data-sec="${id}" data-flip="s:${name}">${name} <i>${n}</i></div>`;

  // The list is drawn from the data on every change, so a row that changes group is the same row in a new place.
  function listHTML() {
    const words = S.filter.toLowerCase().split(/\s+/).filter(Boolean);
    const hit = (hay) => words.every((w) => hay.toLowerCase().includes(w));
    const epicHit = (key) => { const L = EN.line(key); return hit(`${L.slug} ${L.proj} ${L.host} ${L.plain} epic`); };
    const sessHit = (id) => { const s = EN.SESS[id]; return hit(`${s.sum} ${s.proj} ${s.host} ${s.state}`); };
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
    // A conversation that carried a contract is an ordinary seen session once the contract is accepted.
    const seen = EN.GROUPS.seen.concat(Object.keys(EN.SESS).filter((id) => EN.SESS[id].pane === 'director' && !EN.GROUPS.seen.includes(id) && !EN.needs().some((n) => n.session === id)));
    return group((n) => sec('Needs you', n, 'need', 'need'), needs.map(needRow))
      + group((n) => sec('Epics', n, 'epics'), live.map((k) => epicRow(k)))
      + (just.length || earlier.length ? (just.length ? sec('Just ended', just.length, 'ended') : '') + just.map((k) => epicRow(k)).join('')
        + (earlier.length ? `<button class="b-more" data-ended-toggle data-flip="s:earlier">${earlier.length} ended earlier ${S.endedOpen ? '▾' : '›'}</button>`
          + (S.endedOpen ? earlier.map((k) => epicRow(k, true)).join('') : '') : '') : '')
      + group((n) => sec('New since you looked', n, 'new'), EN.GROUPS.fresh.filter(sessHit).map(sessRow))
      + group((n) => sec('Working', n, 'work'), EN.GROUPS.working.filter(sessHit).map(sessRow))
      + group((n) => sec('Seen', `${n} · 6 quiet`, 'seen'), seen.filter(sessHit).map(sessRow));
  }
  const open = (n) => !(n.id === 'beacon' && EN.sess.answered) && !(n.resume && EN.resumed[n.resume]);
  function renderList() {
    listEl.dataset.mode = S.view;
    listEl.innerHTML = listHTML();
    const row = rowEl(S.cur);
    if (row) row.classList.add('cur');
    badge();
  }
  // The mark in the title row carries how many things need the owner, so a hidden inbox still says so.
  function badge() {
    const n = EN.needs().filter((x) => x.id !== S.gone && open(x)).length;
    $$('[data-n="need"]').forEach((el) => { el.textContent = n || ''; el.closest('[data-inbox]').setAttribute('aria-label', `the inbox: ${n} need you`); });
  }
  // A redraw that carries the change across: rows glide, new ones arrive, changed phrases crossfade.
  const morphList = () => M.morph(nav, renderList);

  // Keeps a row in view by scrolling the list alone, so the window around the mockup never moves.
  function rowIntoView(row) {
    const a = row.getBoundingClientRect(), b = listEl.getBoundingClientRect();
    if (a.top < b.top) listEl.scrollTop -= b.top - a.top + 6;
    else if (a.bottom > b.bottom) listEl.scrollTop += a.bottom - b.bottom + 6;
  }
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

  // ---------- the inbox: docked while you choose, hidden once a page is open, popped out from the mark ----------
  const docked = () => S.pin || !S.sel;
  function drawInbox() {
    mock.classList.toggle('inbox-hide', !docked());
    mock.classList.toggle('inbox-fly', !docked() && !!S.fly);
    mock.dataset.fly = S.fly;
    // Popped out, the inbox hangs under the title row, so the page's title stays readable above it.
    const top = openPane() && $('.fr-top', openPane());
    if (top) mock.style.setProperty('--head-h', `${top.offsetHeight}px`);
    $$('[data-pin-toggle]').forEach((b) => { b.setAttribute('aria-pressed', String(S.pin)); $('span', b).textContent = S.pin ? 'pinned' : 'pin open'; });
    $$('[data-wf-inbox]').forEach((sel) => { sel.value = S.pin ? 'pin' : S.fly ? 'fly' : 'auto'; });
  }
  // Pops the hidden inbox out over the page. By keys it takes the keyboard and stays until it is dismissed.
  function openFly(how) {
    if (docked()) return;
    if (!S.fly) S.flyFrom = { cur: S.cur, route: S.route };
    S.fly = how === 'keys' ? 'keys' : S.fly || 'hover';
    drawInbox();
    // By keys the cursor's row is brought into view, since the keys move from it. By hover the list stays where it was left.
    const row = rowEl(S.cur);
    if (row && how === 'keys') rowIntoView(row);
  }
  // Tucks it away. back returns the reader to the page it was on when the inbox popped out.
  function closeFly(back) {
    clearTimeout(hoverTimer);
    if (!S.fly) return;
    const from = S.flyFrom;
    S.fly = '';
    S.flyFrom = null;
    drawInbox();
    if ($('[data-filter]') === document.activeElement) document.activeElement.blur();
    if (back && from && from.route !== S.route) { setCursor(from.cur); S.route = from.route; show('fade'); }
  }
  // Opening a page is choosing it: the inbox gets out of the way unless it is pinned.
  function select() {
    S.sel = true;
    closeFly(false);
    drawInbox();
  }
  function togglePin() {
    S.pin = !S.pin;
    S.fly = '';
    S.flyFrom = null;
    drawInbox();
    echo('B', S.pin ? 'the inbox is pinned open' : 'the inbox hides once a page is open. Hover the mark, or press b');
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
      box.innerHTML = '<div class="in"><kbd>i</kbd><textarea data-send rows="1"></textarea></div>'
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
    working: ['working', 'Queue a message. It is sent when this turn ends', 'queued. It sends when this turn ends'],
    waiting: ['waiting', 'Answer in your own words', 'answer sent'],
    interrupted: ['off', 'This session is interrupted. Resume it to write', ''],
    done: ['idle', 'Reply', 'sent'],
  };

  // ---------- the page beside the list ----------
  // Talk's conversation, docked beside the page of the epic it is about.
  function drawDock() {
    const on = !!S.talk && epicOf(S.route) === S.talk.key;
    read.classList.toggle('has-dock', on);
    if (!on) return;
    const { key, sid } = S.talk, t = EN.talkOf(key, sid), body = $('[data-dk-body]', dock), c = EN.convo(`talk:${key}:${sid}`);
    $('[data-dk-name]', dock).textContent = t.sum;
    $('[data-dk-where]', dock).innerHTML = `${t.place}${EN.ctxOf(sid)}`;
    $('[data-dk-link]', dock).innerHTML = `${EN.glyph(key)}<span>${EN.EPICS[key].slug}</span>`;
    body.innerHTML = `<div class="cv">${c.body}</div>`;
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
    head.innerHTML = EN.head(route);
    badge();
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
    // A conversation that carries cards. The cards are redrawn in place when their contract or epic changes.
    if (name === 'director') {
      const k = EN.director(a), host = $('[data-director]', pane);
      if (!switched && how === 'state') M.morph(host, () => { host.innerHTML = k.body; }); else host.innerHTML = k.body;
      composer(pane, ...k.send);
    }
    if (p === 'sess') {
      const s = EN.SESS[a], st = EN.sessState(a);
      if (name === 'generic') { $('[data-g-age]', pane).dataset.t = s.age || ''; $('[data-g-text]', pane).innerHTML = s.text; }
      if (name === 'question') {
        $$('[data-qcard]', pane).forEach((card) => {
          card.classList.toggle('answered', !!EN.sess.answered);
          $$('.o', card).forEach((o) => o.classList.toggle('sel', o.dataset.answer === String(EN.sess.answered)));
        });
      }
      if (name !== 'director') composer(pane, ...SAY[st]);
    }
    drawDock();
    drawInbox();
    // Back returns to where the page was left. Any other arrival starts at the top, or at the end of a conversation.
    const talky = name === 'convo' || name === 'lamp' || name === 'question' || name === 'director';
    if (how === 'quiet' || how === 'state') { if (switched && talky) box.scrollTop = box.scrollHeight; return; }
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
    if (row) { row.classList.add('cur'); rowIntoView(row); }
    clearTimeout(dwellTimer);
    if (isUnseen(id)) {
      dwellTimer = setTimeout(() => {
        if (S.cur !== id) return;
        setSeen(id, true);
        echo('1 s', `seen, here and on the phone · ${labelOf(id)}`);
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
    // Home is the inbox. Hidden, it pops out; docked, its crumb points at the row this page belongs to.
    if (route === 'home') {
      if (!docked()) { openFly('keys'); return; }
      const row = rowEl(S.cur);
      if (row) { rowIntoView(row); M.enter(row, { x: -8, ms: M.T.layout }); }
      return;
    }
    const [p, a, b, c] = route.split(':');
    if (p === 'talk') { openTalk(a, b); return; }
    const to = p === 'task' ? `task:${a}:${b}:${EN.landing(a, b, c)}` : route;
    if (to === S.route) { if (S.picker) { S.picker = false; show('quiet'); } return; }
    leave();
    const from = S.route, root = p === 'sess' ? to : `epic:${a}`;
    if (baseRoute(S.cur) !== root) {
      if (!rowEl(a) && S.left.has(a)) { S.left.delete(a); renderList(); }
      if (rowIdOf(a)) setCursor(rowIdOf(a));
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
    else rowIntoView(rowEl(S.cur));
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
    const draw = () => { renderList(); const row = rowEl(S.cur); if (row) rowIntoView(row); };
    if (quiet) draw(); else M.morph(nav, draw);
    // A needs-you row that has been dealt with is gone, so the cursor moves to the epic it was about.
    if (!rowEl(S.cur)) setCursor(rowEl(twin(S.cur)) ? twin(S.cur) : rows()[0].dataset.id);
    const [p, a, b] = S.route.split(':');
    // A conversation that carries this epic's cards is redrawn with them.
    if (p === 'sess' && key && EN.SESS[a].epic === key) { show(quiet ? 'quiet' : 'state'); return; }
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
    echo(String(number), `answered: ${EN.SESS.beacon.opts[number - 1]}. It is refused if the question changed meanwhile`);
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

  // ---------- Ask: a new session, from a directory and a prompt ----------
  // The directory starts as the one the page on screen runs in. The rows under it are the folders orrery knows.
  const folderOf = (route) => { const [p, a] = route.split(':'), proj = p === 'sess' ? EN.SESS[a].proj : EN.EPICS[a].proj; return EN.FOLDERS.find(([folder]) => folder.endsWith(`/${proj}`)) || EN.FOLDERS[0]; };
  function drawAsk() {
    const dir = $('[data-ask-dir]', askp).value;
    $('[data-ask-dirs]', askp).innerHTML = EN.FOLDERS.map(([folder, host, epics, sessions]) => `<button type="button" role="option" class="ask-dir${folder === dir ? ' sel' : ''}" data-ask-pick="${folder}" aria-selected="${folder === dir}">`
      + `<b>${folder}</b><span>${host}</span><em>${[sessions.length && `${sessions.length} ${sessions.length > 1 ? 'sessions' : 'session'}`, epics.length && `${epics.length} ${epics.length > 1 ? 'epics' : 'epic'}`].filter(Boolean).join(' · ')}</em></button>`).join('');
    const hit = EN.FOLDERS.find(([folder]) => folder === dir);
    $('[data-ask-host]', askp).textContent = hit ? hit[1] : 'new folder';
  }
  function openAsk(prompt = '') {
    $('[data-ask-dir]', askp).value = folderOf(S.route)[0];
    $('[data-ask-prompt]', askp).value = prompt;
    drawAsk();
    askp.classList.add('on');
    askp.setAttribute('aria-hidden', 'false');
    $('[data-ask-prompt]', askp).focus();
  }
  function closeAsk() {
    askp.classList.remove('on');
    askp.setAttribute('aria-hidden', 'true');
    if (askp.contains(document.activeElement)) document.activeElement.blur();
  }
  function startAsk() {
    const dir = $('[data-ask-dir]', askp).value.trim(), prompt = $('[data-ask-prompt]', askp).value.trim();
    if (!dir || !prompt) { echo('⌘⏎', 'a session needs a directory and a prompt'); return; }
    closeAsk();
    echo('⌘⏎', `started in ${dir} on ${$('[data-ask-host]', askp).textContent}. It opens here when its first turn begins`);
  }
  const toggleMeters = () => { const off = read.classList.toggle('nometers'); echo('m', off ? 'meters hidden' : 'meters shown under every title row'); };

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
    ['inbox', 'the inbox pops out and tucks away', async () => {
      S.pin = false;
      moveTo('lamp', 'quiet');
      select();
      await M.wait(700);
      openFly('keys');
      await M.wait(1600);
      closeFly(true);
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
    if (target === askp) { closeAsk(); return; }
    if (target.closest('[data-ask-open]')) { closeFly(false); openAsk(); return; }
    const pickDir = target.closest('[data-ask-pick]');
    if (pickDir) { $('[data-ask-dir]', askp).value = pickDir.dataset.askPick; drawAsk(); $('[data-ask-prompt]', askp).focus(); return; }
    if (askp.contains(target)) return;
    // The mark and the pin button keep the inbox open beside the page, or let it hide again.
    if (target.closest('[data-inbox], [data-pin-toggle]')) { togglePin(); return; }
    const viewButton = target.closest('[data-view]');
    if (viewButton) { setView(viewButton.dataset.view); return; }
    const ans = target.closest('[data-answer]');
    if (ans) { answer(Number(ans.dataset.answer)); return; }
    const comp = target.closest('[data-compaction]');
    if (comp) { comp.classList.toggle('open'); return; }
    if (target.closest('[data-ended-toggle]')) { S.endedOpen = !S.endedOpen; morphList(); return; }
    // A link inside a page to something further down it, such as an epic card's open question.
    const anchor = target.closest('[data-goto]');
    if (anchor) { const el = document.getElementById(anchor.dataset.goto); if (el) { reveal(el, true); el.classList.add('fresh'); } return; }

    // Talk: its menu, a session picked from it, and the dock.
    if (target.closest('[data-talk]')) { togglePicker(); return; }
    const to = target.closest('[data-talk-to]');
    if (to) { openTalk(...to.dataset.talkTo.split(':')); return; }
    if (target.closest('[data-dock-x]')) { closeTalk(); return; }
    if (target.closest('[data-dk-link]')) { go(`epic:${S.talk.key}`); return; }
    if (S.picker && !target.closest('[data-tp]')) { S.picker = false; show('quiet'); }

    // A link to a page. One inside a list row first puts the cursor on that row. Either way the page is chosen.
    const link = target.closest('[data-go]');
    const row = target.closest('[data-row]');
    if (link) {
      if (row && listEl.contains(row) && row.dataset.id !== S.cur) setCursor(row.dataset.id);
      go(link.dataset.go);
      if (link.dataset.go !== 'home') select();
      return;
    }
    if (row && listEl.contains(row)) {
      const fresh = row.dataset.id !== S.cur || S.route !== baseRoute(row.dataset.id);
      const el = moveTo(row.dataset.id, 'open');
      if (fresh && epicOf(S.route) && docked() && S.pin) M.expandFrom(el, $('.b-pane[data-pane="epic"]'));
      select();
      return;
    }
    // A click on the page under a popped inbox keeps the page it shows.
    if (S.fly && !nav.contains(target)) select();
  });

  // The mark in the title row: resting the pointer on it pops the inbox out, and leaving the inbox tucks it away.
  document.addEventListener('mouseover', (ev) => {
    if (!(ev.target instanceof Element) || docked()) return;
    if (ev.target.closest('.b-head [data-inbox]') || (S.fly && nav.contains(ev.target))) {
      clearTimeout(hoverTimer);
      if (!S.fly) hoverTimer = setTimeout(() => openFly('hover'), HOVER_IN_MS);
    }
  });
  document.addEventListener('mouseout', (ev) => {
    if (!(ev.target instanceof Element) || docked()) return;
    const leaving = ev.target.closest('.b-head [data-inbox]') || nav.contains(ev.target), into = ev.relatedTarget instanceof Element ? ev.relatedTarget : null;
    if (!leaving || (into && (nav.contains(into) || into.closest('.b-head [data-inbox]')))) return;
    clearTimeout(hoverTimer);
    if (S.fly === 'hover') hoverTimer = setTimeout(() => { if (S.fly === 'hover' && document.activeElement !== $('[data-filter]')) closeFly(false); }, HOVER_OUT_MS);
  });

  document.addEventListener('input', (ev) => {
    if (ev.target.matches('[data-filter]')) applyFilter(ev.target.value);
    if (ev.target.matches('[data-send]')) grow(ev.target);
    if (ev.target.matches('[data-ask-dir]')) drawAsk();
  });
  document.addEventListener('submit', (ev) => {
    if (!ev.target.matches('[data-ask-form]')) return;
    ev.preventDefault();
    startAsk();
  });
  document.addEventListener('change', (ev) => {
    if (!ev.target.matches('[data-wf-inbox]')) return;
    const want = ev.target.value;
    ev.target.blur();
    S.pin = want === 'pin';
    S.sel = true;
    S.fly = '';
    drawInbox();
    if (want === 'fly') openFly('keys');
  });

  // Brings something on the page into view by scrolling the page's own box, never the window around it.
  function reveal(el, smooth) {
    const box = el.closest('.b-scroll');
    if (!box) return;
    const top = box.scrollTop + el.getBoundingClientRect().top - box.getBoundingClientRect().top - 14;
    box.scrollTo({ top, behavior: smooth && !M.reduced() ? 'smooth' : 'auto' });
  }

  // A message sent to a session that is not mid-turn shows at once as sending, and settles when the session has it.
  function sendInto(pane, words) {
    const cv = $('.b-scroll .cv', pane);
    if (!cv) return false;
    const msg = document.createElement('div');
    msg.className = 'you sending';
    msg.dataset.t = 'now';
    msg.innerHTML = '<small>you<span class="snd"><i class="spin"></i>sending</span></small>';
    msg.append(words);
    cv.append(msg);
    M.enter(msg, { y: 6 });
    const box = cv.closest('.b-scroll');
    box.scrollTop = box.scrollHeight;
    setTimeout(() => { msg.classList.remove('sending'); $('.snd', msg).remove(); }, SEND_MS);
    return true;
  }

  document.addEventListener('keydown', (ev) => {
    const typing = ev.target instanceof Element && ev.target.matches('input, textarea, select');
    const key = ev.key;

    if ((ev.metaKey || ev.ctrlKey) && key.toLowerCase() === 'k') {
      ev.preventDefault();
      pal.classList.toggle('on');
      flash('pal');
      return;
    }
    if ((ev.metaKey || ev.ctrlKey) && key === 'Enter' && askp.classList.contains('on')) { ev.preventDefault(); startAsk(); return; }
    if (ev.metaKey || ev.ctrlKey || ev.altKey) return;

    if (key === 'Escape') {
      flash('Escape');
      if (pal.classList.contains('on')) { pal.classList.remove('on'); return; }
      if (askp.classList.contains('on')) { closeAsk(); return; }
      if (S.picker) { S.picker = false; show('quiet'); return; }
      if (typing) {
        if (ev.target.matches('[data-filter]')) { ev.target.value = ''; applyFilter(''); }
        ev.target.blur();
        return;
      }
      if (S.fly) { closeFly(true); return; }
      if (S.talk) { closeTalk(); return; }
      back();
      return;
    }
    if (askp.classList.contains('on')) {
      // In Ask, the arrows walk the directories from either field.
      if (key === 'ArrowDown' || key === 'ArrowUp') {
        const dirs = EN.FOLDERS.map(([folder]) => folder), input = $('[data-ask-dir]', askp), at = dirs.indexOf(input.value);
        if (ev.target === input || at >= 0 && !$('[data-ask-prompt]', askp).value) {
          ev.preventDefault();
          input.value = dirs[(at + (key === 'ArrowDown' ? 1 : dirs.length - 1)) % dirs.length];
          drawAsk();
        }
      }
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
        const box = input.closest('[data-composer]');
        if (queue && box.dataset.state === 'working') {
          const entry = document.createElement('div');
          entry.className = 'qi';
          entry.textContent = words;
          queue.append(entry);
          syncQueue(queue);
          M.enter(entry, { y: 6 });
        } else sendInto(input.closest('.b-pane'), words);
        echo('⏎', box.dataset.verb);
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
    // A key that moves the cursor needs the list in view, so a hidden inbox pops out first.
    const inList = () => openFly('keys');
    switch (key) {
      case '?': stage.classList.toggle('nokeys'); break;
      case 'b':
        if (docked()) echo('b', S.pin ? 'the inbox is pinned open. B unpins it' : 'the inbox is open. It hides once you open a page');
        else if (S.fly === 'keys') closeFly(true);
        else openFly('keys');
        break;
      case 'B': togglePin(); break;
      case 'j': case 'ArrowDown': inList(); step(1); break;
      case 'k': case 'ArrowUp': inList(); step(-1); break;
      case 'l': case 'ArrowRight': deeper(); break;
      case 'h': case 'ArrowLeft':
        if (!back() && !docked()) openFly('keys');
        break;
      case 't': togglePicker(); break;
      case 'g':
        if (wasG) { inList(); moveTo(rows()[0].dataset.id); } else pendingG = true;
        break;
      case 'G': inList(); moveTo(rows().at(-1).dataset.id); break;
      case 'a': inList(); setView('attention'); echo('a', 'by attention'); break;
      case 'p': inList(); setView('project'); echo('p', 'by project'); break;
      case '|': toggleSplit(); break;
      case 'm': toggleMeters(); break;
      case 'n':
        if (!nextUnseen()) echo('n', 'nothing new'); else if (!docked() && !S.fly) echo('n', labelOf(S.cur));
        break;
      case 'w': {
        const row = rows().find((r) => r.classList.contains('need'));
        if (row) { moveTo(row.dataset.id); if (!docked() && !S.fly) echo('w', labelOf(S.cur)); }
        break;
      }
      case 'Enter': {
        // Enter opens the row under the cursor, and the inbox gets out of the way. On a page that is already open it writes seen.
        clearTimeout(dwellTimer);
        setSeen(S.cur, true);
        const opening = !S.sel || !!S.fly;
        select();
        echo('⏎', `${opening ? 'opened, and seen' : 'seen,'} here and on the phone · ${labelOf(S.cur)}`);
        break;
      }
      case 'x':
        clearTimeout(dwellTimer);
        setSeen(S.cur, true);
        echo('x', `seen, here and on the phone · ${labelOf(S.cur)}`);
        break;
      case 'u':
        clearTimeout(dwellTimer);
        setSeen(S.cur, false);
        echo('u', `marked unseen · ${labelOf(S.cur)}`);
        break;
      case 'e':
        setSeen(S.cur, true);
        if (!nextUnseen()) echo('e', 'seen. Nothing new left');
        break;
      case '1': case '2': case '3':
        if (S.cur === 'beacon' && !EN.sess.answered) answer(Number(key));
        else echo(key, 'answers a question when the cursor is on its row');
        break;
      case '[': case ']':
        if ($('[data-wg="contract"][data-ckey]', openPane() || document)) EN.section(key === ']' ? 'next' : 'prev');
        else echo(key, 'steps through a contract card when one is on screen');
        break;
      case '/':
        ev.preventDefault();
        inList();
        $('[data-filter]').focus();
        break;
      case 'i': {
        const scope = S.talk && read.classList.contains('has-dock') ? dock : openPane();
        const input = scope && $('[data-send]:not(:disabled)', scope);
        if (input && !input.closest('[hidden]')) { ev.preventDefault(); closeFly(false); input.focus(); }
        break;
      }
      case 'o': $$('[data-compaction]', openPane() || document).forEach((c) => c.classList.toggle('open')); break;
      case 'N': ev.preventDefault(); closeFly(false); openAsk(); break;
      default: handled = false;
    }
    if (handled) { flash(key); if (key !== '/' && key !== 'i') ev.preventDefault(); }
  });

  // A link from the map or a notification names its page: #go=<route>, with &talk=<session> and &m=<epic>.<moment> for a
  // state. Round 5 adds &inbox=pin|fly|browse, &ask=<prompt>, &cs=<contract section>, &settle=1, &at=<element id> and &keys=1.
  function fromHash() {
    const q = new URLSearchParams(location.hash.slice(1));
    if (q.get('m')) q.get('m').split(',').forEach((x) => { const [key, i] = x.split('.'); EN.setMoment(key, Number(i), true); });
    if (q.get('settle')) EN.settle();
    if (q.get('cs')) EN.section(q.get('cs'));
    if (q.get('go')) { go(q.get('go'), 'quiet'); S.sel = q.get('go') !== 'home'; }
    if (q.get('talk')) openTalk(epicOf(S.route), q.get('talk'));
    S.pin = q.get('inbox') === 'pin';
    if (q.get('inbox') === 'browse') S.sel = false;
    S.fly = '';
    drawInbox();
    if (q.get('inbox') === 'fly') openFly('keys');
    if (q.get('ask') != null) openAsk(q.get('ask')); else closeAsk();
    if (q.get('keys')) stage.classList.remove('nokeys');
    land(q.get('at'));
    // The face arrives after the first layout and changes every height, so the page lands again once it has.
    document.fonts.ready.then(() => land(q.get('at')));
  }
  // Where a link lands: on the element it names, else at the end of a conversation.
  function land(id) {
    const pane = openPane(), el = id && document.getElementById(id), box = pane && $('.b-scroll[data-scroll]', pane);
    if (el) reveal(el, false);
    else if (box && pane.dataset.pane !== 'generic') box.scrollTop = box.scrollHeight;
  }

  if (innerWidth < 1680) stage.classList.add('nokeys');
  renderList();
  moveTo('followups', 'quiet');
  listEl.scrollTop = 0;                                     // the page opens on what needs you
  drawInbox();
  fromHash();
  addEventListener('hashchange', fromHash);
})();
