// Round 6 wireframe: the scenario registry and the frame that shows one. A scenario is one screen in one state. It
// has a drawing for the desk, for the phone, or for both; the board lists every one, and shoot.mjs photographs them.
(() => {
  'use strict';
  const { esc, each, when } = O;

  O.scenarios = [];
  // scenario registers one: { id, group, title, note, desk, phone, differs, widths }.
  //   desk, phone: functions that return the screen's HTML. Leave one out when that client has no such screen.
  //   differs: where the web under 760 differs from the Android app on this screen, as a list of sentences.
  //   widths: the desk widths worth a screenshot, when not both 1600 and 1000.
  O.scenario = (s) => {
    if (O.scenarios.some((have) => have.id === s.id)) throw new Error(`scenario ${s.id} is registered twice`);
    O.scenarios.push(s);
  };
  O.GROUPS = ['Inbox', 'Conversation', 'The right rail', 'The dock', 'Asks', 'Epic', 'Contract', 'Phase and worker', 'Talk', 'New and find', 'Notices', 'Android only', 'Web only', 'Empty, loading, error'];

  const params = () => {
    const [id, ...rest] = location.hash.replace(/^#/, '').split('&');
    return { id: decodeURIComponent(id || ''), flags: new Set(rest) };
  };

  const twin = (frame) => (frame === 'desk' ? 'phone' : 'desk');

  // bar draws the wireframe bar: where you are, the scenario picker, and the other pages.
  const bar = (frame, current) => {
    const withDrawing = O.scenarios.filter((s) => s[frame]);
    const at = withDrawing.indexOf(current);
    const prev = withDrawing[(at - 1 + withDrawing.length) % withDrawing.length];
    const next = withDrawing[(at + 1) % withDrawing.length];
    const groups = O.GROUPS.map((g) => [g, withDrawing.filter((s) => s.group === g)]).filter(([, list]) => list.length);
    const other = twin(frame);
    const scale = document.documentElement.dataset.scale === 'round5';
    return `<span class="wf-l">WIREFRAME · ROUND 6</span>`
      + `<a href="index.html">board</a>`
      + `<a class="${frame === 'desk' ? 'on' : ''}" href="desk.html#${esc(current && current.desk ? current.id : '')}">desk</a>`
      + `<a class="${frame === 'phone' ? 'on' : ''}" href="phone.html#${esc(current && current.phone ? current.id : '')}">phone</a>`
      + `<a href="catalog.html">catalogue</a>`
      + `<span class="wf-step"><a href="#${esc(prev ? prev.id : '')}" aria-label="previous scenario">‹</a><a href="#${esc(next ? next.id : '')}" aria-label="next scenario">›</a></span>`
      + `<select data-wf-pick aria-label="scenario">${each(groups, ([g, list]) => `<optgroup label="${esc(g)}">${each(list, (s) => `<option value="${esc(s.id)}"${s === current ? ' selected' : ''}>${esc(s.title)}</option>`)}</optgroup>`)}</select>`
      + `<span class="wf-note">${esc(current ? `${at + 1} of ${withDrawing.length} · ${current.note || ''}` : '')}</span>`
      + `<span class="wf-right">${when(current && !current[other], () => `<span>no ${other} drawing</span>`)}<a href="#" data-wf-scale>${scale ? 'type: round-5 sizes' : 'type: proposed sizes'}</a></span>`;
  };

  const none = (frame, id) => `<div class="wf-none"><p>${id ? `No ${frame} drawing is registered for “${esc(id)}”.` : 'Pick a scenario.'}</p><p><a href="index.html">The board lists every one.</a></p></div>`;

  // sideNotes draws what stands beside the phone: the scenario, and where the narrow web differs.
  const sideNotes = (s) => `<h1>${esc(s.title)}</h1><p>${esc(s.note || '')}</p>`
    + `<h2>Android and the web under 760</h2>`
    + (s.differs && s.differs.length
      ? `<ul>${each(s.differs, (d) => `<li>${O.md(d)}</li>`)}</ul>`
      : `<p>The same screen on both. Android adds the system's back gesture; the web has a Back button in the title row.</p>`)
    + when(s.desk, () => `<p><a class="link" href="desk.html#${esc(s.id)}">The same scenario at the desk ›</a></p>`);

  // show draws the scenario the hash names into the frame's stage.
  O.show = () => {
    const frame = O.frame;
    const { id, flags } = params();
    document.body.classList.toggle('bare', flags.has('bare'));
    if (flags.has('round5')) document.documentElement.dataset.scale = 'round5';
    const withDrawing = O.scenarios.filter((s) => s[frame]);
    const current = O.scenarios.find((s) => s.id === id) || (id ? null : withDrawing[0]);
    const stage = document.querySelector('[data-stage]');
    document.querySelector('[data-wf-bar]').innerHTML = bar(frame, current);
    stage.innerHTML = current && current[frame] ? current[frame]() : none(frame, id);
    const side = document.querySelector('[data-wf-notes]');
    if (side) side.innerHTML = current ? sideNotes(current) : '';
    document.title = `${current ? current.title : 'orrery'} · round 6 ${frame}`;
    // A scenario that names an element to scroll to the end shows its newest content, as a live reader does.
    stage.querySelectorAll('[data-scroll="end"]').forEach((el) => { el.scrollTop = el.scrollHeight; });
    stage.querySelectorAll('[data-scroll-to]').forEach((el) => { const to = el.querySelector(el.dataset.scrollTo); if (to) el.scrollTop = to.offsetTop - 24; });
  };

  O.start = () => {
    window.addEventListener('hashchange', O.show);
    document.addEventListener('change', (e) => { if (e.target.matches('[data-wf-pick]')) location.hash = e.target.value; });
    document.addEventListener('click', (e) => {
      const scale = e.target.closest('[data-wf-scale]');
      if (scale) {
        e.preventDefault();
        const root = document.documentElement;
        if (root.dataset.scale === 'round5') delete root.dataset.scale; else root.dataset.scale = 'round5';
        O.show();
        return;
      }
      // A control with nothing to open does nothing; the wireframe is a drawing, not the app.
      const dead = e.target.closest('a[href="#"], button');
      if (dead && dead.tagName === 'A') e.preventDefault();
    });
    // Left and right step through the scenarios, so the whole set can be paged like slides.
    document.addEventListener('keydown', (e) => {
      if (e.target.matches('input, textarea, select') || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
      const step = document.querySelectorAll('.wf-step a')[e.key === 'ArrowLeft' ? 0 : 1];
      if (step) location.hash = step.getAttribute('href').slice(1);
    });
    O.show();
  };
})();
