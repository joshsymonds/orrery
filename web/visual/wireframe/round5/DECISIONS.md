# Round 5: proposed 2026-11-17, waiting on the owner

Round 5 is a proposal drawn from the owner's six asks. Nothing below is decided until he says so.
Round 5 is the reference for the web client's look until the design system replaces it. The web view changed; the phone
pages (`phone.html`) are as round 4 left them, apart from sharing the new data.

Open `index.html#go=<route>`. Screenshots of every state below are in `shots/`, at 1600 and 1000 wide; `shoot.mjs`
retakes them and says what it needs.

## 1. The conversation has a hierarchy

Ask: "the chat window is just kind of punishingly monotone ... it's definitely something to do with the visual design
itself." Keep Catppuccin Mocha, and "all mono".

| change | where |
|---|---|
| One face. The web is Maple Mono throughout; Atkinson is no longer loaded. Sizes carry the levels: 15 bold for the title, 13.5 for words, 12 for tools, 10 tracked caps for labels. | everywhere on `index.html` |
| One centred column, 88 characters wide, with the time of each turn in a gutter on its left. | `#go=sess:lamp` |
| You: a mauve rail on a lifted surface, a `YOU` label, and where it came from (`from your phone`). A turn starts 30 px below the one before, so the gaps mark the turns. | `#go=sess:lamp` |
| The reply: no rail and no surface. It is the only plain full-brightness text, so it reads as the content. | `#go=sess:lamp` |
| Tools: 12 px grey on a hairline, the tool's name the only colour (blue). Rows in a run touch. A group folds to one line with a dot per call, red for one that failed. A running tool has a yellow rail and a clock. | `#go=sess:lamp` |
| Step events: a return or a review finding is a card on a green or red rail, with its checks as chips. | `#go=session:rota:export-cli:1` |
| Epic events: one line each, a ringed icon in the epic's colours (teal, green for proven, red for a failure, peach for what is yours). | `#go=sess:rotaplan` |
| Rules: compaction and "new since you looked" are thin rules across the column; the second is lavender. | `#go=sess:lamp` |
| Queued: docked above the send box as round 4 decided (Q6), now drawn as ghosts of your own messages: the same mauve, dashed. | `#go=sess:lamp` |
| Sending: your message appears at once with a thinner rail, a dimmer surface and a `sending` mark, and settles when the session has it. Type in any idle send box to see it. | `#go=sess:rotaplan` |
| The title row: the directory in mauve, the summary in bold, the state pill, then the commands. Under it a strip of meters. | every page |

Rejected:
- A second accent colour for the assistant's voice. Two coloured voices compete; leaving the reply bare makes it the figure and everything else the ground.
- Boxes around every tool row, as round 4 had. They were the same dark box as code and the queue, which is most of what read as monotone.
- Bubbles, left and right. They cost width and make code blocks ragged.
- A "turn ended" line after each reply. The next `YOU` block already marks it.

## 2. The inbox is narrower and gets out of the way

Ask: "the inbox on the left needs to be compressed a little more and there needs to be a 'mouseover' view for it such
that it totally hides, but I can mouse over the Orrery icon to see it pop out. (This maybe should be how it works by
default if a conversation has been selected.) With keyboard shortcuts to focus the bar and unpop it."

| change | where |
|---|---|
| Compressed: 340 px, from 440 (316 under 1250). A row is two lines; a third only for what blocks an epic or the answers to a question. Needs-you rows say what is asked and where, with the whole sentence as hover text. The counts row is gone: each group's head already has its count. The filter and the two orders share one row. | `#go=sess:lamp&inbox=pin` |
| Docked while nothing is open; hidden once a page is opened with a click or `⏎`. Hidden means gone: the page has the full width and nothing is left of the inbox but the orrery mark, which is the first crumb of the title row. | `#go=sess:lamp` |
| The mark carries the number of things that need you, in peach, so a hidden inbox still says so. | `#go=sess:lamp` |
| Hover the mark and the inbox pops out over the page, under the title row. It moves nothing. It tucks away when the pointer leaves it. | hover the mark; `shots/inbox-flyout-hover-*` |
| Click the mark, or `B`, to pin it open beside the page, which is round 4's layout. Again to unpin. | `#go=sess:lamp&inbox=pin` |

Keys:

| key | does |
|---|---|
| `b` | Pop the inbox out and put the keys in it. `b` again, or `esc`, tucks it away and returns the reader to the page it was on. |
| `j` `k` `g g` `G`, arrows | Move the cursor, the reader following, as before. With the inbox hidden they pop it out first, so the cursor never moves unseen. |
| `⏎` | Open the row under the cursor. The inbox hides and the row is marked seen. |
| `1` `2` `3` | Answer the question on the cursor row, as before. With the inbox hidden the cursor row is the page being read, so they answer the question card on screen. |
| `n` `w` `e` | Next new, oldest waiting, seen-and-next, as before. Hidden, they go straight there and a note says where. |
| `/` `a` `p` | Filter and the two orders, as before. Hidden, they pop the inbox out first. |
| `h` `←` | Back one crumb. From the first crumb, back is the inbox. |
| `B` | Pin or unpin. |

Rejected:
- A thin icon rail when collapsed. It is what most tools do, and it is not "totally hides": it keeps 44 px and a second column of marks.
- Pushing the page aside when the inbox pops out. Text reflowing under the pointer is worse than a panel over it.
- Covering the title row with the popped inbox. The title is how you know what you are about to leave.
- Moving the cursor blind with `j` `k` while the inbox is hidden.
- `Tab` or `` ` `` for the bar. `Tab` belongs to forms; `b` is free and is the bar's initial.

## 3. Meters and context belong to the page

Ask: "performance monitoring ... is just at the bottom of the inbox which is not useful. It should be per-pane ...
similar to how the android app does it. Ditto to showing context as the android app does."

The phone's pane header (`PaneHeader.kt`, `HostMeters.kt`) shows the host's stats as a label over a value with a thin
bar for a percent, coloured lavender, peach at warn, red at crit; and the context as five blocks and a percent, green
under 40, yellow under 60, peach under 80, red from there.

| change | where |
|---|---|
| The footer under the inbox is gone. | |
| Every page's title row has a strip under it: the host's name, then each stat as the host labels it, with the same bar and the same level colours as the phone. | every page |
| A session's strip ends with its context: the same five blocks, percent and colour bands as the phone, then tokens of window. | `#go=sess:lamp` (15%), `#go=sess:beacon` (44%), `#go=sess:rotaplan` (64%) |
| A split shows two strips, one per pane. The talk dock shows its session's context beside its name. | `#go=sess:lamp` then the `|` key; `#go=epic:rota&talk=rotaplan` |
| `m` shows or hides the strips. | |

Rejected:
- Meters inside the title row's first line. At 1000 wide they push the title out.
- Context in the send box, where round 4 had it. One place: the strip.
- A popover on hover. The owner asked to see them, not to go looking.

Needs data: `Host.Status` answers for the host that serves the page, so a session on another host has no meters today.

## 4. Titles are a directory and a summary

Ask: "I see the hash in a lot of places ... The title should be the directory and a Haiku summary of what the session
has been doing."

| change | where |
|---|---|
| A session's title is its directory, in mauve, then one line saying what it has been doing. In the title row, in the inbox, in Talk's menu, in the dock's head, in the palette. | everywhere |
| No pane label, session id or hash is drawn anywhere in the inbox, a title row, a card or a conversation. Notes that named a session ("lamp-telemetry seen") now give its summary. | |
| A commit is drawn as a small mark, `⎇`, with the short hash as its hover text. This applies on the epic's proof pages too, where a commit is evidence. | `#go=epic:rota`, `#go=session:rota:export-cli:1` |

Rejected:
- Keeping the pane's label as a third part of the title. It is a name someone has to invent, and the owner asked for two parts.
- Removing commits from the proof pages. They are what was proven; they just should not read as hex.

Needs data: the pane has an `@orrery_summary` option and nothing writes it from the conversation. Epics already get a
summary from Haiku (`internal/enginehost/epicsummary.go`); a session's would be made the same way and rewritten as it works.

## 5. Cards in a conversation

Ask: "I'm more and more envisioning the director receiving widgets it can show somehow" and "surface the contract UI
stuff through the director chat".

A card is an item in a conversation that names a kind and an epic. The page draws it live from the epic's own data, so
it is the thing itself and what the owner does in it is the owner's word, as on the thing's own page.

| change | where |
|---|---|
| (a) The contract card. The decision line, then an index of the ten sections beside the open one. The index marks each section read (green tick), yours to settle (peach diamond) or to be read before Accept (red mark). `[` and `]` step through. A decision is options as cards with one recommended; an added row is Keep or Drop. Accept opens when the choices are made and, at severe care, when what can't be undone has been read. A change is a reply. | `#go=sess:tideplan&cs=fails`, `#go=sess:tideplan&settle=1` |
| After Accept the card folds to its line and its counts, and stays where it was. | `#go=sess:tideplan&m=tidealerts.1` |
| (b) The epic card, which arrives when the session creates the epic. The phase on a five-part track, the live sentence, each requirement green (proven), red (its test is written and fails) or grey (no test yet), the open question, and Pause. Its head opens the epic. | `#go=sess:rotaplan&at=card-epic` |
| What the epic asks arrives in the same conversation when it is raised, as the same form the epic view has. The epic card points down to it. | `#go=sess:rotaplan` |
| The title row's strip carries a small line to the epic, wherever the conversation is scrolled. | `#go=sess:rotaplan` |
| (c) The epic view's summary has a `Conversation` line under the contract's, which opens the conversation that created it. | `#go=epic:rota` |
| `Read ›` on a contract's row in Needs you opens its conversation, not the contract page. The page is still there: the card's head opens it. | `#go=home`, the tide-alerts row |

Rejected:
- The whole contract in the card at once. It is a page of reading; a section at a time is what the owner asked for, and the index shows where the choices are.
- The decision form inside the epic card. The card sits where the epic was created and scrolls away; a question should arrive at the bottom, where the eye is.
- A separate director page. A director is a session, and round 4 already decided there is no Director object.
- A snapshot card that shows the epic as it was when posted. It would be wrong by the next turn.

Needs data: a conversation item that names a card (`ConversationItem.widget{kind, epic}`), and which session created an
epic (`Epic.sessions` keeps only each session's newest call).

## 6. The question card and Ask

Ask: keep round 4's question card and Ask if present; add Ask at the top if not.

| change | where |
|---|---|
| The question card is kept: options as cards, and now the recommended one is marked, as the epic's decision form does. | `#go=sess:beacon` |
| Round 4 had no Ask button, only `N` and the palette. Ask is now at the top: in the inbox's head while it is docked, at the right of the title row while it is hidden. `N` opens it too. It takes a directory, which starts as the one the page on screen runs in, and a prompt; `⌘⏎` starts the session. | `#go=sess:lamp&ask=` |

Rejected:
- The phone's two steps (ask, then pick a row, then a start form). On a desk both fields fit on one panel.
- Folding Ask into `⌘K`. The palette goes to things that exist; Ask makes a new one.

## Still to decide

1. `j` `k` with the inbox hidden: pop it out first (drawn), or move the reader without showing the list. Recommended: pop it out. Moving between conversations you cannot see is how the wrong one gets answered.
2. Where an epic's question goes in its conversation: as its own form at the bottom (drawn), or inside the epic card. Recommended: the bottom, with the card pointing to it.
3. Whether the phone pages follow: mono throughout, and titles as directory and summary. They were left alone this round because the ask was the web client.

---

# Web view: owner decisions (2026-11-11)

- Direction: B (Inbox), with the step view and its chart (rings, pips) on the right-hand side, not only the left.
- Q1 list order: two views, one by project and one by attention.
- Q2 font: proportional for replies only (agreed).
- Q3 reader: follows the cursor; seen on Enter or after the phone's one-second dwell (agreed).
- Q4 layout: B (one conversation with a single split; the deck later).
- Q5 answer a waiting question from the list row, guarded by the question hash: yes (B).
- Q6 queued messages: docked above the send box, stacked, up-arrow to edit; the phone matches (agreed).
- Q7 epic drawing: one hue per state, small orrery in the margin, pips in the list, full tile later: try it.
- Q8 terminal: no terminal views anywhere except the owner's terminal apps (conversation only on web; likely the phone too).

# Phone: owner decisions (2026-11-11)

All eight recommendations on phone.html accepted as they stand.

- P1 grouping: Epics get their own group, between Needs you and Sessions, on the phone and in the web inbox. A step is reached only through its epic.
- P2 steps on Home: only an epic's running or paused steps, at most four rows per epic; the rest fold into one line.
- P3 "Needs you": decisions, questions, contracts ready to accept, and release steps that are the owner's. Decisions and questions notify at once; contracts and release steps notify quietly; steps stay silent.
- P4 messaging a step: a message is a nudge (interrupt, then prompt, not a check round) and is labelled as one. Interrupt is its own button; Stop is in the menu behind a confirmation.
- P5 answering: pick then confirm for a decision, showing the decisions.md line first. A session's question keeps one tap.
- P6 accepting a contract: allowed from the phone. At severe care the "can't be undone" section must have been opened first.
- P7 release steps that are the owner's: the engine closes them from evidence when it can; the button is the fallback, recorded as the owner's word.
- P8 the engine's own steps: planner turns, reviewers and retries are one-line rows under the units, with the end kind in words; ended ones folded by default.

## Later owner decisions (2026-11-11, evening)
- "Flows" is "Epics" everywhere.
- The web inbox's left list carries the same Epics group as the phone (a group, not a third toggle).
- The epic circle draws outlined units as dashed rings outside the started ones; the centre shows units done over started plus outlined, with one dot per requirement, kept as two measures (the plan gives no blend formula); a note shows growth when a planner turn outlines more.
- The list glyph is one gauge in three zones (done, started, outlined) with the count beside it.
- Fable's three open web questions: keep `a`, `p`, `w`; epics sit inside their project folder in the project view, a step only under its epic; in a split the right column describes the cursor's pane (taken as recommended).
