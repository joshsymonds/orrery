# Round 6: adopted as the reference

Round 6 is one design language drawn for three clients: the web at the desk, the web under 760 px, and the Android app.
Round 6 was adopted as the reference with the epic `orrery-round6-yardstick`: the web catalog's names and fixtures follow
it, and the frontend epics are judged by it.

Open `index.html` (the board lists every scenario), `desk.html#<id>`, `phone.html#<id>` and `catalog.html`. Left and right
arrows page through the scenarios. `shoot.mjs` shoots every desk scenario at 1600 and 1000 wide and every phone
scenario at 412 into a temporary directory, and fails when a screen overflows its width or the page errors.

130 scenarios: 116 drawn for the desk, 121 for the phone. Every one is drawn by the same components
(`js/prims.js`, `js/shell.js`, `js/convo.js`, `js/epic.js`) from one stylesheet (`css/components.css`) that reads only
the variables `../../system/gen-tokens.mjs` writes from `../../system/tokens.json`. `lint-css.mjs` fails the sheet on a
raw colour, length, duration, font size or opacity.

It was drawn from three inventories made for it: what the web client draws today, what the Android app draws today, and
what the server can say. Where the drawing asks for something the server does not send, it is listed under
"Needs the server".

## Revision 7: hosts (2026-11-22)

The owner: every pane, session, epic and step shows the host it runs on, also when there is only one host. Where rows
are grouped by host and folder, the host is drawn once on the card heading; everywhere else it is drawn inline, as a
`HostLabel`. A card is one host and one folder, so the session rows under it name no host of their own.

| drawn | server field | epic |
|---|---|---|
| a card's head in the inbox, and a project page's head | the host of its headless sessions: each session row's host | orrery-first-class-hosts |
| a headless session's InboxRow, where it is not under a card | its session row's host | orrery-first-class-hosts |
| a headless session's title row | its session row's host | orrery-first-class-hosts |
| a terminal pane's title row | Pane.host | orrery-host-surfaces |
| an EpicRow, an EpicPin, a WorkCard | Epic.host | orrery-host-relay |
| an epic's page title row | Epic.host | orrery-host-relay |
| a StepRow, under an epic in the inbox or as a row of its Now table | EpicStep.host | orrery-host-relay |
| a step's page title row | EpicStep.host | orrery-host-relay |
| a contract's WorkCard | its session row's host: the session that wrote it | orrery-first-class-hosts |
| a session's InboxRow, its title row and its dock, on a host that does not answer | its session row's state reads `unreachable` (unchecked) | orrery-first-class-hosts |
| a pane's title row and its dock, on a host that does not answer | a pane listed from stored rows is marked unreachable: `Pane.unreachable` (unchecked) | orrery-host-surfaces |
| a card's head, on a host that does not answer | the host's `reachable` is false in `Host.Status` (unchecked) | orrery-host-surfaces |
| an epic's step, on a host that does not answer | the step's host is not reachable in `Host.Status` (unchecked) | orrery-host-surfaces |

The field names are the host epics' own, taken from their contracts and not checked against them. A scenario whose
drawing shows a host says so in its note, with the field and the epic. The world gains two hosts, `sextant` and
`lantern`, for `inbox-hosts` (four cards, one per host) and `epic-hosts` (an epic whose steps run on two hosts).

An unreachable host is grey (`ink.3`), not stopped. Its glyph is a dashed hollow ring, and its word is "unreachable"
beside the host, or on a row's line under a card; its dock reads "unreachable: `<host>` does not answer" and offers no
input, queue or control. The three unreachable fields above are read from orrery-first-class-hosts R4 and
orrery-host-surfaces R3 and R4, and their names are not checked against those contracts. `inbox-host-unreachable` and
`pane-host-unreachable` draw it.

## Revision 13: Not now, not Dismiss (2026-11-18)

Dismiss read as "off my screen", and declining the agent's question behind that word would surprise. It is two actions,
each named for what it does:

- **Not now**, the visible control on a session's question, takes it off your list (the count, the peach, the band)
  and sends nothing. It stays waiting, quiet, in its conversation; anything new it asks comes back, and Bring it back
  returns it. While it is open the session takes no events, so the dock says "1 event waits on the question you put
  off". `ask-later`
- **Decline and end the turn**, in the question's More, denies the request. `ask-decline`
- An epic's question has neither: its choices are an answer, the planner's recommendation, or Pause.

## Revision 12: the phone's header folds (2026-11-18)

The owner: the header should get out of the way while chatting, and be hideable for good.

| state | the header | scenario |
|---|---|---|
| open | title, meters, goal, ask lines, tiles; a chevron at the end of the tiles hides the header | `dock-full` |
| reading back | one line under the title: the rail's groups as small badges; back at the newest line, or a tap, opens it | `phone-head-scrolled` |
| hidden by you | the same line, drawn the same, kept in every conversation until you open it (its chevron, or Hide the header in More) | `phone-head-hidden` |
| typing | nothing: the keyboard hides the whole header, folded line and all | `android-keyboard` |

Folded or hidden, an ask is never hidden: the line turns peach, names what asks ("rota-export asks", or "2 ask you"),
and its badge shows the question mark. It does not open by itself.

## Revision 11: the band is what needs you; the rail is everything (2026-11-18)

The owner: a band listing what the conversation steers and a rail listing the same things felt duplicative, though
both matter. They now split by job.

- **The rail is the inventory**: the one place that lists everything the conversation steers, with state at a glance.
  The lit tile and its panel's coloured edge tie the open panel to the conversation, so the band no longer marks it.
- **The band is what needs you, in words, where you read**: the goal, then one line for each thing it steers that
  waits on you, with what it asks and the way to answer ("rota-export · tz-fixtures asks: bump the embedded tz
  database? · Answer"). With nothing asking, the band is the goal alone. Past three asks it folds to "and N more ask".
- The phone has the same band above its tile strip.

`convo-epic`, `convo-contract`, `dock-full`, `epic-question`

## Revision 10: incoming is not drawn (2026-11-18)

The owner: events for the session should reach the model as fast as possible and be all but invisible.

- **Nothing on the rail or in a panel for held events.** What they are about is drawn already: an epic's question is
  its tile's `?`, its line in the band and its Ask; a step's end is on its epic.
- **One case is drawn, in the dock**: after your interrupt the session is quiet (`ErrCallerQuiet`) and takes no events
  until you speak, so the dock says "1 event waits until you speak" under its state line. It goes when you send.
  `dock-interrupted`
- **A held event while a turn runs is not drawn.** It goes in when the turn ends. One held while a request is open is
  the Ask already.
- **A background task should not hold events.** Today `claimFree` treats a session as busy while a background task
  runs (`internal/enginehost/manager.go`), so a dev server left running holds every event for that conversation
  indefinitely. That is a server fix, in "Needs the server".
- The rail's groups are contract, epics, agents, running. `dock-arriving` is gone.

## Revision 9: the rail in named groups (2026-11-18)

The owner: one tile per thing is confusing, since it is unclear what the things are; group them, with dots when there
are too many, and label the groups. And "untold" was not a word anyone would read right.

- **The right rail is groups, each named once**: contract, epics, agents, running, incoming. An epic's or a contract's
  tile keeps its short name under it; a list's tile (agents, running, incoming) is named by its group.
- **A group of more than three is one tile of dots**, one per thing in its colour, with its count, or a question mark
  when one of them asks. It opens a picker naming each thing and its state; choosing one opens its panel.
  `work-rail-group`
- **Untold is now incoming**: events held for the conversation until it is free (a step that ended, an epic's
  question), each delivered as a block when it arrives.
- The phone's strip is the same groups, each its name over its tiles.

`work-rail`, `work-rail-group`, `convo-many-epics`

## Revision 8: a tile per thing, each with its own panel (2026-11-18)

The owner: tabs at the top and cards that fold down are two regimes; commit to one. Better, drop the Work as one pane
and give each thing its own button on the right, opening its own panel.

- **The right rail is a column of tiles, one per thing the conversation steers**: its contract, each epic, its
  sub-agents, what it left running, what is incoming. Each tile keeps its state at a glance (a gauge, a `?`,
  a count) and is named in a word. With nothing started the rail is empty and still there.
- **A tile opens its own panel beside the conversation.** Another tile switches the panel; the lit tile, or Close, or
  `]`, shuts it. One panel at a time, at one width, so the conversation narrows once, when a panel opens, and never
  moves between panels. The lit tile is marked on the edge it opens from, in its colour, and the panel's card takes
  the same colour on its top edge. Under 1250 the panel lies over the conversation.
- **The panel is the thing.** An epic's is its record, with its question at the top, so answering happens there. A
  contract's is the contract. The sub-agents' panel has each agent's task, what it is doing and a clock; left running
  and incoming are lists of their own.
- **The goal heads the band** under the title row: what the conversation is for, then what it steers. It is not a
  tile, since a sentence is not a panel, and not in the dock, which stays three things.
- **The phone has the same tiles as a strip under the title row**, under the goal. An epic's or a contract's tile opens
  it as the next page; a list opens as a sheet. The strip is at the top because the bottom is the dock's, and the
  keyboard lifts or covers whatever sits there.

Gone: the Work as a place and its name; its 420 list; the tab row and All; cards that unfold in place; the WorkBar and
the Work page on the phone; the rail's head tile.

`work-rail`, `epic-question`, `contract-to-accept`, `phase-implement`, `dock-running`, `dock-background`,
`dock-full`, `work-empty`

## Revision 7: the words never move (2026-11-18)

Revision 8 replaced the Work's list, tabs and unfolding cards described here with one panel per tile; the fixed
middle stands.

The owner found it confusing that the conversation jumped from the middle to the right when a contract or an epic was
opened from it. Now the middle is always the words and the right is always the work, at three depths:

| depth | the right pane | scenario |
|---|---|---|
| at rest | the Work's rail: a tile per thing the conversation steers | `work-rail` |
| the list | the Work, 420 wide: a card per thing. A card unfolds in place for what can be settled without the record: an epic's question with its options, a contract's open items. | `convo-epic`, `work-unfold`, `work-unfold-contract` |
| a record | the Work widened to about three fifths of the page around one record. The pane keeps the Work's frame: its ground, its head, a tab for each record it holds with All back to the list, and the record inset as a card. The open record's colour runs from its line in the conversation's band to its tab and the top edge of its card. The conversation stays in the middle, narrower, with its dock, so you can read the record and type about it at once. Its SubjectBand marks the record that is open on the right. | `epic-question`, `contract-to-accept`, `phase-implement` |

- Under 1250 there is no room for both, so a record lies over the conversation from the right edge, as the Work does;
  Back or `]` returns to the conversation, which never moved. `epic-talk`
- A record's own title row, inside its card, is its crumbs (up a level for a task), its commands, and its state on
  the line under them, where a conversation's meters are. The pane's controls (pin, tuck back) are on the Work's head.
- The owner's first drawing of this revision had the record as a plain page on the right; it read as unrelated to the
  conversation and to the Work it came from. Keeping the Work's frame is what makes it read as a child of both.
- Moving an epic is a control on its record ("Steer it from another conversation"). `epic-handover`
- From the inbox or a project's page, an epic or a contract opens the same way: its conversation in the middle, the
  record on the right.
- The phone is unchanged: one page at a time.

Rejected:
- Swapping the pair, with the record taking the middle and the conversation moving right (revisions 3 to 6).
- Records only as unfolded cards in the 420 pane. A contract is ten sections of tables and an epic has its chart, its
  tasks and its requirements; at 420 every table falls to its stacked phone form.

## Revision 6: one conversation per epic (2026-11-18)

Settled with the owner:

- **An epic is steered from exactly one conversation, always.** It is never without one: an epic created from a shell
  or the CLI gets a conversation made for it, and when its conversation ends the epic offers to continue in a new one
  that starts with the contract and the epic's log. It is never shared: other conversations may read it and talk about
  it, but to change it a conversation takes it over.
- **One conversation may steer several epics, with no cap.** The load is drawn instead: the SubjectBand shows three,
  the ones that ask first, and folds the rest; the rail has a tile each; the context meter is beside them. The relief
  is moving an epic to a new conversation.
- **Moving is deliberate and visible.** From the epic, a control on its record moves it to another
  conversation in the same project, or to a new one. From a conversation, asking it to take an epic over does it with
  a tool call. A move leaves one line on the epic's log, one in the conversation it left, and one in the conversation
  that took it; from then on its questions and its ending go to the new one. Moves stay inside a project, since the
  epic belongs to its repository and is drawn under its conversation in that project's card.
- **Your own actions on an epic's page are yours, not a conversation's.** An answer, a pause or a stop from the page
  goes on the epic's log and is told to its conversation as an event.
- **A contract belongs to the conversation that wrote it.** Accepted, the epic it becomes inherits that conversation.

| where | now |
|---|---|
| The inbox | A project's card holds conversations; under each hang the epics it steers and the contract it wrote. There is no Epics group. An ended conversation steering a live epic stays in the tree. `inbox-home`, `inbox-waiting`, `inbox-folded` |
| A project's page | Its conversations, each with what it last said and, under it, its epics as live cards. `project` |
| An epic's record | "Steer it from another conversation" opens "Steer rota-export from": the one that steers it now, the project's others, and a new one. `epic-handover`, `epic-moved` |
| A move | `convo-took-over`, `convo-handed-over`, `epic-moved` |
| A conversation that ended | Where its dock was: Continue in a new conversation, or Steer from another. `epic-convo-ended` |
| Many epics | `convo-many-epics` |

## Revision 5, after the owner's third review (2026-11-18)

| you said | now |
|---|---|
| Type: the larger scale. | Decided: 12, 13, 15, 18, 28. |
| Meters: the web's look is right; the phone should match. | Decided: each stat its own colour, then yellow and red, on both clients. Android drops lavender, peach, red. |
| Host meters stay in the phone's header. | Decided, as drawn. |
| Release and close need public calls. | Decided. The engine has them (`ReleaseEpic`, `CloseEpic` in `internal/enginehost/epicservice.go`); the public service passes them through. |
| Verify stays a word under review. | Decided, as drawn. |
| A contract must be a real record you can go back to, not part of a conversation. | A contract is kept from the moment it is proposed. It has versions, each with its time, its writer and what changed; a state (proposed, accepted, set aside); and the conversation that wrote it. It is found under its project, in its conversation's Work, and from its epic once accepted. `contract-versions`, `contract-set-aside`, `project` |
| It is unclear what the two rails are for. People expect navigation on the left; nobody expects work on the right. | The left rail's head is the Orrery mark, where the docked inbox has it. The right rail's head is the Work's own icon and the word "work"; every tile under it names itself in one word (goal, an epic's name, agents, running, incoming). Beside an epic or a contract the rail's head is "talk". `work-rail`, `inbox-rail` |
| The conversation beside a contract or an epic does not read as the same conversation; a conversation's identity is squishy. | A conversation is known by what it is steering. A SubjectBand under its title row says it, one line per open record: "Discussing the tide-alerts contract · proposed v2 · waits for you", "Steering rota-export · building · asks you". Beside the record the column carries the conversation's full title, its state, and the same band, with the record on the page marked "this" and pointed at. The Work does not stay open for it: a Director would then never have a rail, and the band says it where you are reading. `convo-contract`, `contract-to-accept`, `convo-epic`, `epic-question` |
| The ring names are too far from the rings. | Each name runs along its own ring through the gap and ends where that ring's first cell begins. `epic-building` |

## Revision 4: two rails (2026-11-17)

The owner's rule: a pane at rest is a small rail you can read at a glance, and it is always there. The inbox is the pane
on the left and a conversation's Work is the pane on the right; they are each other's mirror.

| | left | right |
|---|---|---|
| the pane | the inbox: a card per project, its sessions and epics under its head | the Work: a card per thing the conversation started, what belongs to it under its head. Beside an epic, a task or a contract it is the conversation that steers it. |
| at rest | a rail: a tile per project, with a count of what waits on you in it, a yellow dot when something there is working, a lavender dot when something is new; then New, search, notices, keys | a rail: a tile for the goal, each epic's gauge with a mark when it asks, sub-agents, what was left running, what has not been told, each with its count |
| brought out | hovering the rail, or `b`: over the page, beside the rail | hovering the rail, or `]`: over the page, beside the rail |
| pinned | `B`, or the pane's pin: beside the page | the pane's pin: beside the page |

- The Work pane is drawn in the inbox's language: the same ground, the same cards, the same rows.
- A conversation that has started nothing still has its rail, so the reading column is in the same place in every
  conversation. Brought out, it says what will show there. `work-empty`
- The title row no longer carries the inbox's control or the bell. They are on the left rail.
- An epic's, a task's and a contract's conversation is drawn pinned, since talking to it is the commonest thing done
  there; it rests as the same rail. A conversation's Work is drawn pinned in the Work scenarios so its content can be
  read, at rest in `work-rail`, and brought out in `work-flyout`.
- This replaces round 5's "the inbox totally hides": hidden became a rail.

`inbox-rail`, `inbox-flyout`, `work-rail`, `work-flyout`, `work-empty`, `convo-epic`

## Revision 3, after the owner's second review (2026-11-17)

| you said | now |
|---|---|
| A folder does not read as a folder with sessions and epics under it. It should be obvious what a project is, and that what follows is its children. | A project is a card. Its head is its name and its path; what works in it hangs from a line under the head, under its own small heads: Sessions, Epics. `inbox-home` |
| "asks" is confusing, and the summary line is too short to be useful. | A waiting row shows the question itself, led by a question mark, with no "asks:". A row's title runs to two lines. |
| How is the Work opened? It should perhaps always be open, or collapse to a toolbar on the right showing what is there. | Both. It is open beside its conversation by default. Folded (its own control, or `]`) it is a rail at the right edge: a mark for each thing it holds, with a count. Under 1250 the rail is the default and a mark opens the column over the page. The conversation beside an epic or a contract folds the same way. `work-rail`, `work-narrow` |
| Where does the conversation beside a contract come from? Does a Director summon the contract? | Yes. The session that ran Gambit's brainstorm (the Director) writes the contract and presents it for acceptance in its own conversation. That conversation is the one beside the contract. |
| The contract does not resemble a Gambit contract; care is per item. | It is redrawn from Gambit's template: the decision line (what is approved, level of care, decisions needed), then the ten sections in its order, with its column names. "How bad" is each failure row's own rating and the worst row sets the level of care; there is no likelihood. "Decisions I need from you" is the Ask. `contract-to-accept`, `contract-accepted` |
| Clicking a project should show all its epics and sessions with useful summaries. | A project's head opens its page: its epics as live cards, its sessions with what each last said, its context and its age, and New session here. `project` |
| Implement, check, review, integrate are not the names of the slices. | They were not. A task's build runs red, implement, green, review, and verify when a review fails with findings. The chart's rings, the pips and a task's page are now red, implement, green, review, integrate; verify is said in words under review, since most tasks never enter it. `phase-implement`, `phase-review` |
| How should moving between elements and talking to them work? | One rule, below. |

### Moving between a conversation and what it made

- **An element opens with the conversation that owns it.** Nothing ever opens with no way to talk about it. (Revision
  7 replaced the swap drawn here: the conversation stays in the middle and the element is the record on the right.)
- **The words follow the work down.** From an epic into a task, the epic's conversation stays in the middle, and the
  epic stays in sight as the band at the top of the task's record.
- **Talking about a thing is a message with that thing on it.** A row's control puts the row on the message.
- **A pane never disappears.** It is out, or it is a rail that says what it holds. See revision 4.
- **A second conversation is a second window.**

On the phone the same pairs are a line or a bar that opens the other half: a conversation's Work is one line under its
title row, an epic's conversation is the bar at its foot, and a contract's conversation is a sheet over the lower half.

## Revision 2, after the owner's first review (2026-11-17)

The owner looked at the first drawing and said, in short: too monochrome; the link between a conversation and its
Orrery work is missing; the dock is overloaded; notices are small and nowhere to be found; labels explain what should be
obvious. Round 6 was redrawn in place. What changed:

| you said | now |
|---|---|
| Colour and distinction are low; it should look like Catppuccin Mocha. Context and the machine meters are victims, and most of the chat. | Five more Mocha hues are in use. A directory has a colour (`place`). Each host stat has its own (`series`), and takes yellow or red at warn or crit. The context is green until it warns. Blocks are tinted in their rail's colour and carry an icon and facts. Headings, inline code and code syntax are coloured. Tool lines have an icon per kind of tool and a diff reads `+14 −3` in green and red. |
| How does Open beside work? It may be better as separate browser frames. | It is gone. A second conversation is "Open in a new window". |
| If a conversation starts an epic, what does that look like and where does it go? In-chat cards get lost. | Every page is a pair. Beside a conversation is its **Work**: the epics it started as live cards, the contract it wrote, its sub-agents, what it left running, the events held for it (incoming). The transcript keeps one line where each was created. `convo-epic`, `convo-contract`, `dock-full` |
| "6 wait on you" is in too many places. | It is the inbox's chip. With the inbox hidden it is a count on the control that brings the inbox back, and in that control's tip. |
| The epic view is nice but undifferentiated; the contract is thuddingly monochrome. | Sections have a mark and a hue. A contract's rows have ids coloured by kind, likelihood is a coloured chip, red and green proof are headed in red and green. |
| Epics do not link to the conversation they came from; tasks do not link back to their epic, and a task's page does not say what is going on or why. | Beside an epic is the conversation that steers it. A task's page opens with a band: its epic's line, every task of that epic with this one marked, the requirement it proves, and the planner's brief. `epic-question`, `phase-checking`, `worker-running` |
| "Rings, outside in:" and labels like it are failures. | The rings are named on the chart, once, in its gap. The legend, the hint under an Ask and the explanatory lines on the dock's trays are gone. |
| Talking to an epic should be hoisted, not hidden in a button. | The conversation is beside the epic's page by default, input ready. Its head moves the epic to another conversation or a new one (revision 6). `epic-talk`, `epic-handover` |
| How does interacting with a contract work, and how does the rest stay in view? | A contract is the work of the session that wrote it; that conversation is beside the contract. A row's control puts the row on the message as what it is about. On the phone the conversation is a sheet over the lower half. `contract-talk` |
| The Interrupt button is too big on both. | It is a control the height of the line it sits in, the same on both (`Ctl`). |
| Blocks are sterile; text needs more distinction. | Tinted grounds, icons, and facts on a step's end (rounds, how long, files and lines changed, how far the task got). Syntax colour in code. |
| Notices are small and mysterious, and it is unclear where they collect. | A notice is two lines led by what it is about, with when and what to do. A bell counts the unread and opens every notice by day. `convo-notices`, `notices-centre` |
| Sub-agents overload the dock; "everything" is unusable. | The dock is three things: the state, the queue, the input. The rest is in the Work. |

## What the owner said, and where it is answered

The notes are `../../system/OWNER-NOTES.md`.

| note | answer | scenario |
|---|---|---|
| The inbox is confused: old questions on top, a parked notice, then epics. | One tree, by directory. A directory holds its sessions, then its epics. Nothing sits above live work. | `inbox-home` |
| It should be organised by the directory the agent works in, with epics under the agents. | Each project holds its conversations, and each epic hangs under the one conversation that steers it (revision 6). | `inbox-home`, `inbox-folded` |
| Filtering and organising at the top is confusing and misplaced. | The order switch is gone. Two chips say what waits on you and what is new, and narrow the same tree. Search opens on demand. | `inbox-waiting`, `inbox-new`, `inbox-search` |
| The triple dots, the number next to Orrery, the carets are unclear. | More has a tip that names what it holds, and every item names its key. The number is a count on the inbox's control, whose tip says it in words. Crumbs are separated by `/`, and Back is a button with a tip. | `inbox-row-menu`, `inbox-hidden` |
| You want to dismiss an old agent's question. | Not now takes it off your list and sends nothing; Decline, in its More, denies it and ends the turn (revision 13). An old one says how long it has waited. | `ask-stale`, `ask-later`, `ask-decline` |
| Epic views are unhelpful; the orrery is hard to use, in the inbox and on the page. | In the inbox an epic is a one-ring gauge and plain words. On its page each ring names itself along the ring, and a task table beside the chart says the same thing in rows. | `epic-building`, `epic-dense` |
| Epics are colourless and undifferentiated. | Each epic's gauge and state take its state's colour; what blocks it is a line in attention. | `inbox-home` |
| Clicking an epic is anti-helpful: blocks of text about nothing. | The page has an order: what it is, where it is, what it is doing, what asks for you, every task, every requirement. A stage it has not reached is one cell of the track and nothing else. | `epic-building`, `epic-question`, `epic-planning` |
| "working" sits in the upper right, not over the conversation; Android is better. | The state is on the dock, on both clients, beside the control that changes it. | `convo-working` |
| Ask should be New. | It is New. | `new-empty` |
| A queued message: what is it waiting behind, when will it send, how is it edited? | The queue's tray says it in words, each message can be taken back, and Send now is a control. | `dock-queued`, `dock-pane-queue` |
| After interrupting, how do you know it stopped? | Three states with three wordings: interrupting, interrupted by you, cut off. | `dock-interrupting`, `dock-interrupted`, `dock-interrupt-queue`, `dock-cut-off` |
| The machine meters are confusing and undifferentiated. | Each is a label, a bar and one value in its own colour, and takes yellow or red at warn or crit. | `convo-working`, `convo-hot` |
| Everything is too small. | The type scale is one step larger: 12, 13, 15, 18, 28. The bar's "type" link sets the round-5 sizes for comparison. | every scenario |
| What each control does is unclear. | A label where there is room; a tip with the key where there is not. Every action has a pointer control. | `more-menu`, `keys` |
| The epic card sits mid-conversation and scrolls away. | The transcript keeps one line where the epic was created. The live epic is a card in the Work beside the conversation. | `convo-epic` |
| Pending step ends draw as cards of raw JSON. | A held event is not drawn: it goes in at the turn's end, and what it is about shows on its epic. After your interrupt the dock says how many wait until you speak (revision 10). | `dock-interrupted` |
| Background shells look like work in progress. | Sub-agents a turn waits on are live. A server left running is one grey folded line. | `dock-running`, `dock-background` |

## Decisions

Each says what is drawn, why, and what was rejected.

### 1. The inbox is a tree of directories, then conversations

A directory's head is its name and where it is. Under it: its conversations (what waits on you first, then new, then
working, then the rest), each with the epics it steers and the contract it wrote hanging under it, each epic with its
running steps; then one line for what has ended. Worktrees fold into their repository, as Android's tree already does.
A folded directory still says how many things in it wait on you.

A row is one line saying what the conversation has been doing, and its age; under it, its last words, or what it asks,
or the tool it is running. A row has no directory in it: the head says it. That gives a title about 30 characters at
340 wide, where a row that carries its directory has about 20.

Rejected:
- A "Needs you" section above the tree. It is how a five-day-old question came to sit above everything. The first chip
  gives the same list when asked for.
- Epics as their own group, under the directory or above it. An epic belongs to one conversation and is drawn under it.
- Android's one folder per path segment. `~/code/lighthouse` is one head, not three levels.

### 2. A row is raised when open and outlined under the keyboard; it never has a rail

Rails belong to the conversation, where they say who spoke. The audit found one mauve rail meaning both "the cursor is
here" and "you said this".

### 3. Every page is a pair: the work and the words

The middle is always a conversation. The right is its rail: a tile for each thing it steers, each opening its own
panel beside the conversation, one at a time and at one width (revision 8). Under 1250 the panel lies over the
conversation. On the phone the tiles are a strip under the title row, and an epic's conversation is the bar at its
foot.

Rejected:
- A card in the transcript where the epic was created. It scrolls away, and it was the owner's first complaint twice.
- Talk behind a button. Guiding an epic is the commonest thing done to it.
- Two conversations side by side. A second window does it without a second layout.
- Swapping the pair when a record opens. The conversation jumped from the middle to the right (revision 7).
- One Work pane holding everything, with tabs or cards that fold down. Two regimes for one job (revision 8).

### 4. State lives with its controls, and the dock is three things

A conversation's state is the first line of its dock, beside Interrupt or Resume. An epic's is in its title row, beside
Pause and Stop. The dock holds the state, the queue and the input, and nothing else.

- **Queue.** A session's queue says "Claude takes them at its next tool result", because since Say-now (origin main
  52d51dee) every owner Say is written to Claude at once and Claude takes it there. Each message can be taken back by itself. Send now interrupts the turn, and the first queued message
  runs next. A terminal pane's queue is Claude Code's own and says so.
- **A control in a line is the size of the line**, at the desk and on touch.

Rejected:
- The pill in the title row for a conversation (round 5). It is the furthest point from where the owner types.
- Sub-agents, background tasks and held events on the dock (this round's first drawing). With all of them it was the
  tallest thing on the page.

### 5. Interrupt has three wordings

`interrupting…` from the press until the turn ends, with a clock, since a tool call in flight can take a while.
`interrupted by you` when it has stopped: the server's state is `done` with an interrupted mark, and the session takes
no events until you speak, which the tray says. `cut off` for the server's `interrupted` state, which means the server
restarted mid-turn; it has Resume. If messages were queued, an interrupt runs the first one, and the transcript shows
the rule and then that message.

### 6. One Ask

A session's question, a tool permission, an epic's decision and an owner-held park are one component: who asks, the
question at reading size in regular weight, numbered options with what each does, the recommended one marked. A
session's answers on one press; an epic's is pick, then confirm, and shows the line that will be recorded. Every option
is answerable (keys run to 9; today only the first three answer). A session's question has Not now, which takes it
off your list and sends nothing, and in its More, Decline and end the turn (revision 13). A second question says "1 of 2".

Rejected:
- The raw engine sentence with a 40-character hash as a park's text. A park has choices; it is a question.
- Option chips in the inbox row. The reader follows the cursor, so the Ask is on screen beside the row; the keys still
  answer it. This changes how round 4's Q5 is drawn, not what it allows.

### 7. An epic's page has an order, and no empty sections

Summary at reading measure, with the doors to its contract and its conversation. The track: Contract, Build, Review,
Release, Report, each done, now or ahead. Now: one sentence, then what asks for you, then what runs. Just happened.
Tasks: the chart and the table. Requirements. Review, Release, Report as the epic reaches them.

- **Requirements are one table.** Red proof and green proof are two cells of a requirement's row, not two sections of
  cards.
- **An evidence run is a sentence and a verdict**: "Red run: the test fails before any change, as it should." and
  `holds`. Today it is five status words in a row.
- **The chart names its rings once, on the rings.** Each name runs along its ring through the gap at the top and ends
  where the ring begins; the wedge under the pointer is outlined and named in a caption. It holds 120 tasks.
- **Plain words.** `closed_shipped` is "shipped outside orrery", `met_at_base` is "already met", `clean` is "ready to
  release". No repository hash anywhere.

Rejected: tabs. Round 4 removed them and the reason holds.

### 8. Notices are two lines, and they collect

A notice is led by what it is about (an epic's gauge, a session's state), says who and what in two lines, when, and
what to do. Three show as they arrive. A bell, in the inbox's head and in the title row while the inbox is hidden,
counts the unread and opens all of them by day.

### 9. New replaces Ask

One field that takes a directory, a few words about a session, or what to start. Each result says in one word what
choosing it does.

### 10. Type is one step larger, and there are still five sizes

12 label, 13 small, 15 body, 18 title, 28 display. Controls are 32 high at the desk and 48 on touch.

### 11. Colour

Fourteen Mocha hues, in three kinds of job.

- **A role means one thing.** Peach waits on you; yellow is running; green passed; red failed; maroon was cut short;
  teal is the engine; blue opens; lavender is new; mauve is yours. Sapphire is a place: a directory's name.
- **A series tells things apart and means nothing else**: sky, pink, flamingo, sapphire, rosewater, in order. One host
  stat from the next, one kind of contract row from another, one kind of tool from another.
- **Prose and code** have their own: headings sky, inline code flamingo, and Catppuccin's usual syntax colours.

A quantity is still never peach: a stat is its series colour until warn (yellow) or crit (red), and the context is
green until 60 percent. Success is still quiet in the transcript: a tool that worked has no tick. Nothing is dimmed
with opacity.

Rejected: grey meters until warn (this round's first drawing). The owner called them victims.

### 12. The phone is the same pages, one at a time

The inbox is the home page. A page's header is its directory, its title and its context; on a conversation it folds
when the keyboard is up. Menus and confirms are sheets. Android alone has quick keys, Pools, Settings and
notifications; the web alone has find by key, the key list and two columns. A notification's title is a directory and
what is asked, and its actions are the options by name.

## What this changes in earlier decisions

| earlier decision | round 6 |
|---|---|
| Round 4 Q1: two list orders, by attention and by project. | One order, by directory. The chips narrow it. |
| Round 4 P1: epics get their own group, between Needs you and Sessions. | An epic hangs under the conversation that steers it. |
| Round 4 Q5: answer a waiting question from the list row. | Kept as keys. The option chips are not drawn in the row. |
| Round 5 ask 3: meters coloured as the phone's (lavender, peach, red; four context bands). | Each stat its own colour, then yellow, red. The context is green, yellow, red. Both clients change. |
| Round 5: the mark carries a peach number. | A control with a tip, and a pill with words. |
| Round 5: the state pill in the title row. | The state is on the dock. |
| Round 5: an epic card in the conversation. | A live card in the Work beside the conversation, and one line where it was created. |
| Round 4 Q4: one conversation with a single split. | No split. A second conversation is a second window. |
| Round 4: no send box under an epic; words go to a session through Talk. | Still a session's words: that session's conversation is in the middle, with the epic open on the right. |
| Round 5: `Ask`. | `New`. |

Kept as they were: Catppuccin Mocha; one mono face; a title is a directory and a summary; no ids or hashes; a commit is
a mark; queued messages on the composer; pick then confirm for an epic's decision; a message to a step is a nudge,
Interrupt is its own button and Stop asks first; the inbox hides once a page is open, pops out with `b` and pins with
`B`; the motion tokens.

## Needs the server

Drawn, and not available from the server today.

| drawn | what is missing |
|---|---|
| "Claude takes them at its next tool result" | Since Say-now every Say is written at once; the stream says per queued message whether it waits on the turn or the start (orrery-turn-facts). |
| Events not held by a background task | A fix, not a field: `claimFree` counts a running background task as busy, so a dev server left running holds every event for its session indefinitely. Deliver when no turn runs and no request is open. |
| "1 event waits until you speak" | `waits = speak` is wrong after the owner has spoken on a headless session, because its prompts carry no sender, so the count of events held by the owner's quiet is not reliable today. |
| Not now on an Ask | A per-question mark, kept on the server and keyed to the question (a pane's question is already hashed), so every client takes it off your list. Nothing is sent to the session. |
| Decline on an Ask | No call declines a question. For a session it is an interrupt today, which denies the request; for a pane it is Escape, which can leave the pane waiting. |
| Archive on a stale Ask | An archived session stays waiting and returns at its next state change. |
| A project's page | No call answers for a directory; it is every pane and session whose directory is the project's, and every epic whose repository is. |
| An epic under its conversation | `Epics.List` has no path and no creator. Placing it costs a `Epics.Get` per epic. Revision 6 needs one steering conversation per epic, always set, on `Epics.List`. |
| Moving an epic | No call moves an epic, and none makes a conversation for an epic created from a shell. Needs a move call (epic, to conversation, by whom), a tool for a conversation to take one over, a log entry for a move, and the events going to the new conversation. Today caller events go to the creator only. |
| Continue in a new conversation | Needs a call that starts a session in the epic's repository with the contract and the epic's log, and makes it the epic's conversation. |
| A contract under its conversation | The contract record (above) carries the conversation that wrote it. |
| A blocker and a count on an epic's row | The same: paused, parked and open questions are only on `Epics.Get`. |
| "paused by you" against a limit pause | The source of a pause is not public. |
| Release from step 2; Release it anyway; Close as… | No public call. The engine does them from a session or the CLI. |
| A contract as a record: proposed, its versions, accepted or set aside; Accept, Keep, Strike and the decision on it | Decided (revision 5), not built. Today Gambit's brainstorm settles these in the conversation and writes the contract only once accepted. It needs a contract record the brainstorm writes to when it proposes and at each change (body, time, writing session, what changed), calls to accept, set aside and reopen, and `epic_create` to take an accepted contract. |
| A contract Block in a conversation | No item kind marks a contract. |
| A session's meters on another host | `Host.Status` answers for the serving host. |
| Interrupting, with a clock | `interrupting` is a bare flag with no time. |
| Notices by day, read and unread | `Notices.Watch` is a stream of what is current; nothing lists what has passed. The client can keep what it has seen while it is open. |
| Open in a new window | The web has no address per page. The routes epic in flight gives it one. |
| Red, implement, green, review as a task's place | `EpicTask` carries a status and the build's `stage`; `orchestrate` and `verify` are stages too and are folded into implement and review here. |
| A step end's rounds, duration and changed lines | A pending `step_end` carries a status and a summary; the rest is on the step. |
| The Work beside a conversation | No call lists a session's epics. With one steering conversation per epic on `Epics.List`, it is a filter of that list. |
| Steps and Talk sessions kept out of the directory tree | Not settled from either client whether `Sessions.ListHeadless` returns them. If it does, a row needs a role to be filtered by. |
| A limit wait drawn grey | The server sends `limited` on a step; the web folds it into the same `mine` tone as an owner hold, so today both are peach. |
| A notification's actions by option name | The push carries the question; the app builds `1 2 3`. |

## Open for the owner

1. **After acceptance.** Answered: `web/src/catalog/round6.json` and the fixtures under `web/src/fixtures` now carry round 6's
   names and world. The scenario ids here became that list, and the catalogue's `Component/State` names the story names.
