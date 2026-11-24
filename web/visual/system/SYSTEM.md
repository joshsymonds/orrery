# The orrery design language

Status: **drawn, adopted as the reference.** Every rule here is drawn in `../wireframe/round6`, whose catalogue
(`catalog.html`) shows each component in each state and whose stylesheet reads only the generated token variables. Round 6
was adopted as the reference with the epic `orrery-round6-yardstick`; the decisions and what was rejected are in
`../wireframe/round6/DECISIONS.md`.

One spec, three clients: the web at the desk, the web under 760 px, and the Android app. Lengths are px on the web and
dp (sp for type) on Android, with the same numbers. `tokens.json` is the source; `gen-tokens.mjs` writes `tokens.css`.
`just tokens` runs it and writes four files: `tokens.css`, `web/src/tokens.css`, `web/src/tokens/rules.ts` and
`OrreryTokens.kt` in the Android theme package.

## 0. The moves

1. **Four inks, five sizes, eight spaces, three radii.** Everything on screen picks from these.
2. **A colour has one of three jobs.** A role means one thing (peach: this waits on you). A series tells things
   apart and means nothing. Prose and code have their own. A quantity is never peach.
3. **Four shapes in a conversation**: Block, Line, Rule and Ask. A new item kind is one of them.
4. **Every page is a pair, and the words never move.** The middle is a conversation; the right is its rail, a tile per
   thing it steers, and each tile opens its own panel beside it. The transcript is the past and keeps one line for each.
   An epic is steered from exactly one conversation and is drawn under it everywhere; it moves only when you move it.
5. **State lives with its controls**, and is drawn once: on the dock for a conversation, in the title row for an epic.
   The dock is three things: the state, your queue, the input.
6. **One loud thing.** A page has at most one, and it is the Ask.
7. **A page has an order and no empty sections.** What does not exist yet is not drawn.
8. **Every control says what it does, and an element names itself once.** A label where there is room; a tip with
   its key where there is not. No caption explains another element.

## 1. Colour

Catppuccin Mocha, unchanged, fourteen of its hues in use. A component names a role, a series or a prose colour, never
a hue.

### Surfaces and lines

| token | Mocha | is |
|---|---|---|
| `surface.side` | crust | The inbox column. A log inside a well. |
| `surface.well` | mantle | Sunk: code, a tool's result, a block from someone else, the dock. |
| `surface.page` | base | The page, at every width, on every client. |
| `surface.raised` | surface0 | Lifted: your block, the open row, a button, a chip, an option. |
| `surface.pressed` | surface1 | A pressed control; a tip. |
| `surface.overlay` | mantle | A panel, a menu, a notice, a sheet: always with a `line.firm` outline. |
| `line.soft` | surface0 | Hairlines and dividers. |
| `line.firm` | surface1 | A control's or an overlay's outline; a Line's thread. |
| `line.glyph` | overlay0 | Strokes that are not text. Never a text colour. |

A tint is a role or a series colour at `alpha.tintFill` (0.10), outlined at `alpha.tintLine` (0.35). A Block's ground
is its rail's tint over a well.

### Ink

| token | Mocha | on the page | for |
|---|---|---|---|
| `ink.1` | text | 11.3:1 | Content: a reply, your words, a title. |
| `ink.2` | subtext0 | 7.4:1 | Supporting words: a seen row's title, a description. |
| `ink.3` | overlay2 | 5.8:1 | Meta and machinery: a tool line, an age, a count, a label. |
| `ink.4` | overlay1 | 4.4:1 | Only what repeats or is not information: the gutter's time, a placeholder, a disabled control. Never on `surface.raised`. |

Nothing is dimmed with opacity. Pending is a dashed rail; not reached is not drawn.

### Roles

| role | Mocha | means | appears as |
|---|---|---|---|
| `you`, `focus`, `primary` | mauve | Yours: you said it, your hand is here, the one action. | Your block's rail; the focus outline; Send, Answer, Accept. |
| `attention` | peach | It waits on you. | The Ask; a waiting glyph; what blocks an epic; "6 wait on you". |
| `live` | yellow | Running now. | The spinner; a running call's rail and clock; the dock's rail while working. |
| `ok` | green | A verdict: passed, proven. | A done glyph, an ok chip, a passed cell. Never a tick per successful tool. |
| `bad` | red | A verdict: failed. | A failed tool, a bad chip, an error line. |
| `halt` | maroon | Cut short. | Interrupting, interrupted by you, cut off; stderr. |
| `engine` | teal | The engine said it. | An event block's rail; an accepted cell; releasing. |
| `ref` | blue | A name you can open. | A tool's name; a link. |
| `new` | lavender | New since you looked. | The unseen dot, the new rule, an unseen row's age. |
| `mark` | yellow | A find match. | Find highlights only. |
| `place` | sapphire | A directory. | A directory's name in the inbox, a title row, a notice. |

### Series, prose, code

| token | Mocha | for |
|---|---|---|
| `series.1` to `series.5` | sky, pink, flamingo, sapphire, rosewater | Telling things apart, in order, and nothing else: one host stat from the next, the kinds of contract row (`R`, `F`, `P`, `A`), the kinds of tool (search, change, run, read, delegate), a section's mark. |
| `prose.heading`, `prose.marker` | sky | A heading, a list's marker and a quote's bar in a reply. |
| `prose.code` | flamingo | Inline code. |
| `syntax.*` | mauve keyword, green string, peach number, blue call, yellow type, sky operator, overlay2 comment | Code in a code block. Roles do not apply inside one. |

### State

One table for the glyph, the dock and the phone.

| state | colour | glyph | the dock says |
|---|---|---|---|
| working | `live` | spinner | working, the tool, a clock |
| waiting | `attention` | filled `?` | waiting on you |
| held | `attention` | `‖` | held |
| limited | `ink.3` | `‖` | waiting on a limit, and when it resets |
| done | `ok` | ringed tick | done, and when |
| interrupting | `halt` | ringed `!`, pulsing | interrupting…, a clock |
| interrupted (the server's state) | `halt` | ringed `!` | cut off: the server restarted mid-turn |
| done, after your interrupt | `halt` | ringed `!` | interrupted by you, and when |
| failed | `bad` | ringed cross | failed, and why |
| stopped | `ink.3` | square | stopped |
| idle | `ink.3` | hollow ring | nothing |
| unreachable | `ink.3` | dashed hollow ring | unreachable: `<host>` does not answer |

Stopped is grey because you stopped it. Unreachable is not stopped: nothing was stopped, the host does not answer, so
what the session is doing is not known, and it is drawn as a dashed ring worded for the host. Held and limited share a
drawing and differ in colour: held waits on you; limited waits on a usage limit and asks nothing. The web's `mine`
tone covers both today.

**Tone** (a task's cell): plan `line.firm`, run `live`, mine `attention`, bad `bad`, acc `engine`, ok `ok`. A task's
cells are its stages in order: red, implement, green, review, integrate. Verify and orchestrate are said in words
under review and implement.

**How bad** (a contract's failure row): limited grey, serious yellow, severe red. The worst row sets the level of care.

**Level** (a quantity): ok green, warn yellow, crit red. A host stat's level is the server's; at ok it keeps its
series colour, and at warn or crit its value and bar take the level's. The context is ok below 60 percent, warn below
80, crit from there, drawn as five blocks of twenty.

**An epic's state** takes a role, not a hue of its own: building and planning `live`; anything that waits on you
`attention`; releasing `engine`; released `ok`; paused, waiting its turn, a limit wait and every ending that is not a
release, grey.

## 2. Type

One face: Orrery Mono (Maple Mono), regular and bold; italic only for emphasis and a thought.

| token | size / line | weight | for |
|---|---|---|---|
| `type.label` | 12 / 16 | bold, caps, tracked 0.06em | Section heads, a block's label, a meter's name. |
| `type.small` | 13 / 20 | regular | Machinery and meta: a tool line, a second line, a chip, a button, the strip. |
| `type.body` | 15 / 24 | regular | Reading text, your words, a row's title, an input, an option's name. |
| `type.title` | 18 / 28 | bold | The page's title; an epic's now sentence. |
| `type.display` | 28 / 36 | bold | The chart's count; a contract's title. One per page. |

Bold is allowed at `small` and `body` for emphasis. There are no other sizes.

## 3. Space, size, radius

`space`: 2, 4, 8, 12, 16, 24, 32, 48.

`size`: a hairline is 1; a rail is 3, always; a glyph and an icon are 16; a control is 32 high at the desk and 48 on
touch; a control inside a line (a `Ctl`) is 24 on both; a gauge is 24; a meter's bar is 48 by 4.

`radius`: 4 for a key cap, a chip, inline code; 8 for a row, a block, a button, a field, a well; 12 for an overlay, the
Ask, the dock. `round` for a pill, a dot, a filter chip. A block is square on its rail's side.

Fixed uses: a block's padding is 8 by 12; lines in a run touch; items in a turn are 12 apart; a turn starts 32 below
the one before; a page's side padding is 24 at the desk and 16 on the phone.

## 4. The spine

Every conversation item hangs from the reading column's left edge.

| on the spine | means |
|---|---|
| nothing | The reply: the only plain full-width text. |
| a 3 px rail in a role's colour | A Block: someone said something. The rail and the label share a tone: the verdict when there is one, else `attention` when it asks, else the speaker. |
| a 3 px `live` rail | The thing running now. |
| a 1 px thread | A Line: machinery. |
| a dashed 3 px rail | Not yet: a queued message, an event still held, a message being sent. |

The dock's left edge is the same rail in the state's colour. A Block's ground is tinted in its rail's colour, and its
label is led by an icon for its kind. An inbox row never has a rail.

## 5. Elevation and layers

Flat, except an overlay, which takes `surface.overlay`, a `line.firm` outline and `elevation.overlay`. Layers, low to
high: page, sticky, flyout, menu, notices, panel. Notices sit under the title row and never over it.

## 6. Motion

Press 120, state 200, layout 280, drill 320, all ease-out `(0.2, 0, 0, 1)`; leave 200 ease-in `(0.4, 0, 1, 1)`. A drill
moves 16. Loops: spin 1100 linear, pulse 1800 between full and a quarter, blink 1100. With reduced motion every move
is one 100 ms step and loops hold still. No component uses a raw duration.

## 7. Layout

- **Desk** (760 and wider): the inbox, the conversation, a rail. The inbox is expanded (340, or 316 under 1250) or
  collapsed to a rail 64 wide, as you leave it, and nothing of it lies over the page; its head is the Orrery mark.
  Collapsed, each project is a tile with its name set sideways, and a tile opens its project's flyout beside the rail. The
  right rail, 64 wide, is what the conversation steers, in groups each named
  once (contract, epics, agents, running); a tile opens its own panel between the conversation and the rail, one at a time and
  at one width, and is lit while it is open. Under 1250 a panel lies over the conversation. A page is a title row, a scrolling area and a foot. Reading text is one column 88 characters wide with a 6-character time gutter. Other pages keep to
  1120. A conversation's head is its title row, with the context before Find and More, then the machine band on
  surface.side, then the goal; under a 900 head (breakpointHeadNarrow) the context's name drops to its tooltip.
- **Phone** (under 760, and Android): one page at a time. A conversation's header (title, goal, ask lines,
  tiles) folds to one line of badges while you read back, stays folded when you hide it, and is gone while you type;
  folded, it turns peach when something asks. No gutter: a turn's time is in its block's label line.
  Controls are 48 high and key caps are not drawn. A menu or a confirm is a sheet. A conversation's tiles are a strip
  under its title row, each opening its thing as a page or a list as a sheet; an epic's conversation is the bar at its foot; a contract's is a sheet.
- **Time is written three ways**: a clock `14:31` for when something happened today (a date before today), an age `5d`
  `3h` `12m` for how long ago, a running clock `1:16` for something live.

## 8. Components

Names are story names: `Component/State`. The catalogue draws each component in each state; the components that are a whole page or panel (Inbox, Reader, Panel, CommandBar) are drawn by the scenarios.

### Primitives

| component | states |
|---|---|
| Kbd | Default, Active, Attention. Not drawn on touch. |
| Button | Default, Quiet, Primary, Danger; Hover, Pressed, Focused, Disabled |
| IconButton | Back, Close, More, Send; WithTip |
| ConfirmButton | Rest, Asking. On touch, Asking is a Sheet. |
| Tip | Default |
| Field | Empty, Filled, Focused, Error |
| Chip | Neutral, Ok, Bad, Attention, Live, Engine |
| FilterChip | Off, On |
| StateGlyph | Working, Waiting, Held, Limited, Done, Interrupted, Interrupting, Failed, Stopped, Unreachable, Idle |
| Pill | Neutral, Live, Attention, Ok, Engine; WithGlyph |
| Meter | Ok, Warn, Crit; NoBar; Stacked (touch) |
| ContextMeter | Ok, Warn, Crit; Bare |
| Gauge | Building, AsksYou, NoTasks, Paused, Releasing, Shipped, EndedShort |
| Pips | Running, Held, Limited, Failed, Accepted, Done, NotStarted. Five cells: red, implement, green, review, integrate. |
| CommitMark | Default |
| SectionHead | Plain, Attention, WithRest |
| InlineNote | Quiet, Error, Ok |
| Ctl | Interrupt, Resume, SendNow, IconOnly, Disabled. A control inside a line, 24 high on both clients. |
| IdChip | by kind: R, F, P, A, other |
| Menu | Open; Two (each item a conversation: its title, then what it is doing) |
| Switch | Off, On |
| ListItem, Sheet, Toast | touch |

### Shell

| component | states |
|---|---|
| Inbox | Expanded, Collapsed, Flyout; FilterWaiting, FilterNew, Search, Archived; Loading, Empty, Error, NoMatch |
| Dir | a project: Open, Folded, FoldedWaiting, OtherHost, Unreachable. A card: its head is the project's name and path and opens its page; its conversations hang from a line under it. A card on a host that does not answer reads unreachable, and so do its conversations. |
| ConvoRow | Plain, SteersEpic, WroteContract, SteersSeveral, FromShell. A conversation's row, with the epics it steers and the contract it wrote hanging from a line under it. An epic belongs to exactly one conversation. |
| Project | the page behind a Dir's head: its conversations with what each last said, each with its epics as WorkCards under it |
| Rail | InboxRail (a tile per project, and a project's flyout beside it), WorkRail (named groups of what the conversation steers) |
| Tile | Mark, Lit, Project, ProjectWaiting, ProjectNew, Current, EpicAsks, SubAgents, LeftRunning, New |
| InboxRow | Working, WaitingQuestion, Done, CutOff, Failed, Stopped, Unreachable, Idle; Unseen, Open, Cursor, Hover, MenuOpen |
| EpicRow | Building, Asking, ContractProposed, ReleaseFailed, Releasing, Parked, Paused, Planning, Shipped, EndedShort, StepsOnTwoHosts |
| StepRow | Running, Held |
| TitleRow | Conversation, InboxHidden, Epic, Subpage, Pane, WithError, UnreachablePane; phone: Folded |
| MeterStrip | HostAndContext, HostOnly, UnderLoad, OtherHost. Superseded by HostsView, to be removed by the build epic. The head keeps its host, as a HostLabel, and the context; it draws no meters |
| HostsView | AllReachable, UnderLoad, OneUnreachable. Opened from the inbox head. Every host the server lists, one row each: its name as a HostLabel, its load and its memory as Meters, and whether it answers. A host that does not answer reads unreachable beside its name, with its glyph, and draws no numbers |
| HostLabel | Inline, CardHeading. The host a pane, a session, an epic or a step runs on, with the host's icon: inline on its own row, or once on a card's heading. Beside it the word unreachable when the host does not answer |
| WorkRail | the right rail, in groups each named once: contract, epics, agents, running. Up to three things show as tiles; more is one tile of dots (one per thing, in its colour) that opens a RailPicker. Empty when nothing is started |
| RailPicker | what a tile of dots opens: each thing in the group and its state; choosing one opens its panel |
| SidePanel | one thing opened from its tile, beside the conversation: an epic's or a contract's record, or a list (Agents, Running); the thing inset as a card with its colour on the top edge; over the conversation under 1250 |
| TileStrip | touch: the same tiles under the title row |
| CompactHead | touch: the header folded to one line of the rail's badges, the same whether scrolling folded it or you did: Quiet, Asking (peach, names what asks) |
| WorkCard | EpicAsks, EpicBuilding, ContractWaiting: on a project's page |
| Contract | Proposed, Versions, Ready, Accepted, SetAside. A record from the moment it is proposed: versions with time, writer and what changed. |
| EndedBar | where a conversation's dock was when it ended while steering a live epic: Continue in a new conversation, Steer from another |
| SubjectBand | under a conversation's title row: Goal; then one line per thing it steers that waits on you (Asks, Contract), in words, with the way to answer; OpenOnRight when that thing is in the panel. Everything else it steers is on the rail only |
| ContextBand | a task's epic line and its sibling tasks |
| EpicPin | Asking, Quiet. In a ContextBand. |
| Notice | AsksYou, EpicAsks, Shipped, Failed; InTheCentre |
| NoticeCentre | Days, Empty |
| Panel | New (Empty, Working, Results, Form, NoResults, Error), KeyList |
| Starter | Prompt, Loading, Several, None, Failed, TypedFolder. Starting a session takes over from New: a prompt first, then where it runs. Its ranked proposals are a dropdown under the prompt, the top one preselected, each a folder with its host; or a host and a folder typed, the host chosen from the reachable hosts only. A panel on the desk, a sheet on touch. |

Every pane, session, epic and step draws its host, also when there is only one host: where rows are grouped by host and folder it is drawn once on the card heading, and everywhere else inline as a HostLabel.

### Conversation

| component | states |
|---|---|
| Reader | Loading, Empty, Following, ScrolledUp, Finding |
| YouBlock | Default, FromPhone, Sending |
| EventBlock | Default |
| StepEndBlock | Ok, Failed, Stopped |
| EpicEventBlock | Created, Question, Parked, Released, Ended |
| CheckBlock | Ok, Bad; NoteOpen |
| ContractBlock | Waiting, Accepted. One line; the contract itself is a tile on the rail and its panel. |
| Reply | Default, Live |
| ToolLine | Done, Failed, Running, Open, OpenFailed |
| ToolRun | Closed, Open, Running |
| ThinkingLine | Closed, Open |
| TaskLine | Finished, Failed, Stopped |
| NoticeLine | Default, Error |
| EngineFold | BriefClosed, BriefOpen, EngineClosed |
| Rule | Day, New, Interrupted, Compaction, CompactionOpen |
| Ask | Session, Permission, Epic, EpicSomethingElse; Sending, Error, Stale, Unanswerable, Answered, Settled; Later (put off with Not now), Decline (its More) |
| Dock | Done, Working, Waiting, Interrupting, InterruptedByYou, EventsWait (after your interrupt, how many events wait until you speak), CutOff, Failed, Stopped, Shell; Focused, WithQueued, PaneQueue, QueuedCancelled, About, SendError; StepRunning, StepHeld, StepConfirm |
| FindBar | Matches, NoMatch |
| QuickKeys | Default, Flash, NotSent. Android only. |

### Epic

| component | states |
|---|---|
| Track | a stage is Done, Now (with a tone) or Ahead |
| Orrery | Running, Mine, Done, Dense. Five rings, outside in: red, implement, green, review, integrate, each named once along its own ring, ending where the ring begins. Not drawn with no tasks. |
| TaskTable | Default |
| RequirementTable | Default, WithRuns |
| EvidenceRun | RedHolds, RedMetAtBase, GreenPasses, Judging, NoVerdict, LogOpen, LogUnavailable |
| ReleaseTable | Running, Failed |
| HappeningList | Default, WithTail |
| PhaseRow | one of a task's five stages: Passed, Now, NowOpen, Ahead |
| Contract | The contract document: DecisionLine, then its ten sections in its order; Accepted, ToAccept, Ready |
| CommandBar | touch: an epic's or a step's state and commands at the foot |

## 9. From today's clients to these components

For whoever builds it. Web names are classes in `web/src/style.css` or components under `web/src`; Android names are
composables under `android/.../ui`.

| component | web today | Android today |
|---|---|---|
| Inbox, Dir | `.list`, the three views of `App`, `rows.ts` sections | `PaneListScreen`, `treeRows` in `PaneTree.kt`, `EpicsGroup` |
| InboxRow | `.b-row`, `TitleParts` | the pane row of `PaneListScreen` |
| EpicRow, StepRow, Gauge | `EpicLine` and its mini orrery, `.b-row.step` | `EpicsGroup` lines, `EpicLine.kt` |
| TitleRow, MeterStrip | `TitleRow`, `Strip` | `TitleRow.kt`, `PaneHeader`, `HostMeters` |
| Dock | `Composer` with its queue, `StepBox`, the pill of `workline.tsx` | `ComposeBox`, `StatusLine`, `SessionControls`, `StepControls`, `QuickKeys` |
| WorkRail, SidePanel | `.epic-card`, waiting cards, background rows, all in the transcript today | `GoalLine`; the rest is not drawn |
| SidePanel, SubjectBand | `TalkDock` and the Talk menu are what it replaces | `TalkScreen`, `TalkSessionScreen` are what it replaces |
| Notice, NoticeCentre | `notices.tsx` (no centre) | system notifications only |
| Block | `.you`, `.event`, `.card`, `.step-card`, `.event-ring`, `.epic-card` | the prompt entry of `ConversationView` |
| Line | `.call`, `.fold`, `.notice`, background rows | the tool box of `ConversationView` |
| Rule | the compaction, new, interrupted and agent-finished rules | the new and compaction rows |
| Ask | `.question-card`, the answer form of `blocker.tsx`, list-row chips | `QuestionCard`, the request card of `SessionControls`, `AnswerForm` |
| Epic page | `EpicPage`, `proof.tsx`, `happenings.tsx`, `orrery.tsx` | `EpicScreen` and its parts |
| Contract, Phase | `contract.tsx`, `phaseoutput.tsx` | `ContractScreen`, `PhaseOutputScreen` |
| New | `picker.tsx` | `PickerPanel` |
| Starter | `picker.tsx`, its start form | `PickerPanel`, its start form |
