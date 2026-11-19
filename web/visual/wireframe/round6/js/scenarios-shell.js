// Round 6 wireframe: the inbox's scenarios, and the frame builders every other scenario file uses.
(() => {
  'use strict';
  const { W } = O;

  // desk builds a desk scenario: the inbox in a state, and pages beside it.
  O.desk = (o) => O.app({ inboxState: o.inboxState || 'docked', inbox: o.inbox === null ? null : W.inbox({ open: o.open, cur: o.cur, ...(o.inbox || {}) }), pages: o.pages, over: o.over, narrow: o.narrow, side: o.side });
  // lampPage is the conversation most scenarios stand beside: an ordinary working session.
  O.lampPage = (o = {}) => O.convoPage({ title: { ...W.lampTitle, cmds: O.convoCmds(), ...(o.title || {}) }, items: W.lamp, dock: { ...W.lampDock, ...(o.dock || {}) }, ...o.page });
  // blankPage is the page area with nothing open: one line saying so, and what to do.
  O.blankPage = (line, hint) => O.page({ label: 'nothing open', body: `<div class="blank">${line ? `<span class="t-body">${O.esc(line)}</span>` : ''}${hint ? `<span>${O.esc(hint)}</span>` : ''}</div>` });
  // home is the phone's first screen: the inbox as a page.
  O.home = (o = {}) => O.screen(O.inbox(W.inbox(o)), { over: o.over });

  O.scenario({
    id: 'inbox-home', group: 'Inbox', title: 'Inbox: grouped by directory',
    note: 'What you see at load. A project is a card: its head is its name and its path and opens the project\'s page. Under it hang its conversations, and under each conversation hang the epics it steers and the contract it wrote, each epic with its running steps. An epic belongs to exactly one conversation, so it is drawn once, under that conversation; one started from a shell has a conversation made for it. A row\'s title runs to two lines; what a conversation asks is the question itself, led by a question mark.',
    desk: () => O.desk({ open: 'convo-working', pages: [O.lampPage()] }),
    phone: () => O.home(),
    differs: ['Android adds **Pools** and **Settings** to the head. The web has neither: pools are an Android screen and the web has no settings.'],
  });

  O.scenario({
    id: 'project', group: 'Inbox', title: 'A project',
    note: 'Clicking a project\'s head opens everything working in that directory: its conversations, each with what it last said, its context and its age, and under each, the epics it steers as live cards. New session here starts one in it.',
    desk: () => O.desk({ open: 'project', pages: [O.projectPage(W.project)] }),
    phone: () => O.screen(O.projectPage(W.project)),
  });

  O.scenario({
    id: 'inbox-waiting', group: 'Inbox', title: 'Inbox: only what waits on you',
    note: 'The first chip narrows the same tree to what waits on you: questions, a contract to accept, a parked epic, a failed release step. A conversation whose epic or contract waits stays as that epic\'s parent, so you still see whose it is. It replaces the old Needs you section and the order switch.',
    desk: () => O.desk({ open: 'ask-session', inbox: { dirs: W.waitingDirs(), filter: 'waiting' }, pages: [O.blankPage('Six things wait on you.', 'Pick one on the left. Press w to step through them, oldest first.')] }),
    phone: () => O.home({ dirs: W.waitingDirs(), filter: 'waiting' }),
  });

  O.scenario({
    id: 'inbox-new', group: 'Inbox', title: 'Inbox: only what is new',
    note: 'The second chip narrows the tree to rows that changed since you looked. An unseen row has a dot, a bold title and a lavender age.',
    desk: () => O.desk({ open: 'dock-idle', inbox: { dirs: W.newDirs(), filter: 'new' }, pages: [O.blankPage('Two rows are new since you looked.', 'Press n to step through them; e marks one seen and goes to the next.')] }),
    phone: () => O.home({ dirs: W.newDirs(), filter: 'new' }),
  });

  O.scenario({
    id: 'inbox-search', group: 'Inbox', title: 'Inbox: search',
    note: 'Search opens from the head or with /. It looks at titles, directories, hosts and states, and it filters epics too (today it filters sessions only).',
    desk: () => O.desk({ open: 'convo-working', inbox: { search: 'tide', dirs: [W.dirs()[1]] }, pages: [O.lampPage()] }),
    phone: () => O.home({ search: 'tide', dirs: [W.dirs()[1]] }),
  });

  O.scenario({
    id: 'inbox-search-none', group: 'Empty, loading, error', title: 'Inbox: search finds nothing',
    note: 'Today an empty result is a blank list. It says what was searched.',
    desk: () => O.desk({ open: 'convo-working', inbox: { search: 'harbour-pilot', state: 'nomatch' }, pages: [O.lampPage()] }),
    phone: () => O.home({ search: 'harbour-pilot', state: 'nomatch' }),
  });

  O.scenario({
    id: 'inbox-rail', group: 'Inbox', title: 'The inbox at rest: a rail of projects',
    note: 'Opening a row folds the inbox to its rail: a tile per project, each with a count of what waits on you in it, a yellow dot when something there is working and a lavender one when something is new. The project of the open page is marked. Under them: New, search, notices, keys. Hovering the rail, or b, brings the inbox out over the page.',
    desk: () => O.desk({ inboxState: 'rail', open: 'convo-working', inbox: { showTip: true }, pages: [O.lampPage()] }),
    widths: [1600, 1000],
  });

  O.scenario({
    id: 'inbox-flyout', group: 'Inbox', title: 'Inbox popped out over the page',
    note: 'b, or resting on the rail, brings the inbox out over the page beside its rail. It moves nothing under it. Esc tucks it back; B pins it beside the page.',
    desk: () => O.desk({ inboxState: 'flyout', open: 'convo-working', cur: 'ask-session', pages: [O.lampPage({ title: { toggle: true, waiting: 6 } })] }),
  });

  O.scenario({
    id: 'inbox-row-menu', group: 'Inbox', title: 'A row\'s menu',
    note: 'More appears on the row under the pointer or the keyboard, with a tip that says what it holds. Every item names its key. A second conversation is a second browser window, not a column. On touch a long press opens the same items as a sheet.',
    desk: () => O.desk({ open: 'convo-working', cur: 'ask-session', inbox: { menu: 'ask-session' }, pages: [O.lampPage()] }),
    phone: () => O.home({ over: O.sheet(O.menu(O.rowMenuItems(W.rows.beacon).map((it) => (it === 'sep' ? it : { ...it, kbd: undefined }))), { title: W.rows.beacon.title, sub: 'tidepool · waiting on you · 4m' }) }),
    differs: ['The web under 760 opens the sheet from a More button on the row, since a long press selects text in a browser.'],
  });

  O.scenario({
    id: 'inbox-folded', group: 'Inbox', title: 'A directory folded, and what has ended opened',
    note: 'A folded directory still says how many things in it wait on you. Ended conversations, with the ended epics they steered under them, sit behind one line at the foot of their directory, never above live work. A conversation that ended while its epic is live stays in the tree.',
    desk: () => O.desk({ open: 'epic-shipped', inbox: { collapsed: true, endedOpen: true }, pages: [O.lampPage()] }),
    phone: () => O.home({ collapsed: true, endedOpen: true }),
  });

  O.scenario({
    id: 'inbox-archived', group: 'Inbox', title: 'Archived',
    note: 'Archived conversations, with what they steered, by the same directories. Reached from the foot of the inbox or v. Each row\'s menu holds Unarchive.',
    desk: () => O.desk({ open: 'epic-ended-gaps', inbox: { view: 'archived', count: 4, attn: {}, dirs: W.archivedDirs() }, pages: [O.blankPage('Archived rows keep their conversations.', 'Open one to read it. Unarchive is in its menu.')] }),
    phone: () => O.home({ view: 'archived', count: 4, attn: {}, dirs: W.archivedDirs() }),
  });

  O.scenario({
    id: 'inbox-narrow', group: 'Inbox', title: 'The desk at 1000 wide, inbox pinned',
    note: 'Under 1250 the inbox is 316 wide. The reading column narrows before anything wraps; the title row stays two lines.',
    desk: () => O.desk({ narrow: true, open: 'convo-working', inbox: { pinned: true }, pages: [O.lampPage()] }),
    widths: [1000],
  });

  O.scenario({
    id: 'inbox-loading', group: 'Empty, loading, error', title: 'Inbox: before the first answer',
    note: 'Today the list is a bare header until it loads.',
    desk: () => O.desk({ inbox: { state: 'loading', attn: {} }, pages: [O.blankPage('', '')] }),
    phone: () => O.home({ state: 'loading', attn: {} }),
  });

  O.scenario({
    id: 'inbox-empty', group: 'Empty, loading, error', title: 'Inbox: nothing is running',
    note: 'Today an empty list is blank on both clients.',
    desk: () => O.desk({ inbox: { state: 'empty', attn: {} }, pages: [O.blankPage('Nothing is open.', 'Start a session with New, or press N.')] }),
    phone: () => O.home({ state: 'empty', attn: {} }),
  });

  O.scenario({
    id: 'inbox-error', group: 'Empty, loading, error', title: 'Inbox: the server did not answer',
    note: 'Today the web prints "panes unavailable" in the list head and Android offers Retry. One state for both. Errors from a command sent from a page are drawn on that page, not here.',
    desk: () => O.desk({ inbox: { state: 'error', attn: {} }, pages: [O.blankPage('', '')] }),
    phone: () => O.home({ state: 'error', attn: {} }),
  });
})();
