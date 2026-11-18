# The orrery design language

Status: **draft, checkpointed before any mockup was drawn.** Parts 1 to 7 (the tokens and the rules for using them)
are written out and match `tokens.json`. Part 8 (the component inventory) names every component and state; the
anatomy is written for the shell and the conversation and is a sketch for the epic pages. Nothing here has been
rendered, so every number is a hypothesis until it is seen at 1600, 1000 and 412. Unfinished parts say so.

One spec, two implementations: Preact with CSS custom properties, and Compose with a generated token object. Lengths
are px on the web and dp (sp for type) on Android, with the same numbers.

## 0. The moves

1. **Four inks, five sizes, eight spaces, three radii.** Everything on screen picks from these. Nothing else is legal.
2. **Colour is a role, and roles are rationed.** Peach means "this waits on you" and nothing else on a surface. Success
   is quiet. An accent is only ever one thing on a row.
3. **Four shapes in a conversation**: Block, Line, Rule and Ask. Every item kind is one of them, so a new kind does
   not need a new look.
4. **The transcript is the past; the dock is the pending.** What is still running, still arriving or still queued sits
   on the composer, not in the scroll.
5. **The one bold thing is the Ask.** A page has at most one loud element, and it is the thing that needs the owner.
   It is the same component in the inbox row, the conversation, the epic page and on the phone.

## 1. Colour

Catppuccin Mocha, unchanged. The clients never name a hue; they name a role. `overlay2` joins both palettes; Android
gains `teal`, `maroon` and `overlay2`; the web loses `--rule` (not a Mocha colour) and `subtext1`.

### Surfaces
| token | Mocha | is |
|---|---|---|
| `surface.side` | crust | The docked inbox column. Log and output wells inside a well. |
| `surface.well` | mantle | Sunk: code blocks, tool results, a block from someone else, the composer's box. |
| `surface.page` | base | The page, on both clients, at every width. |
| `surface.raised` | surface0 | Lifted: your own block, the cursor row, buttons, chips, a picked option. |
| `surface.pressed` | surface1 | A pressed control. |
| `surface.overlay` | mantle | A panel over the page (palette, key list, menu, flyout, notice), always with a `line.firm` outline and `elevation.overlay`. |

A tint is a role colour at `alpha.tintFill` (0.10) over the surface under it, outlined with the same role at
`alpha.tintLine` (0.35). Those two alphas replace today's thirteen.

### Lines
`line.soft` (surface0) is every hairline and divider. `line.firm` (surface1) outlines a control or an overlay.
`line.glyph` (overlay0) is for strokes that are not text: an unlit dot, a dashed edge. It is never a text colour.

### Ink
| token | Mocha | on page | for |
|---|---|---|---|
| `ink.1` | text | 11.3:1 | Content: a reply, your words, a title, a row's title when unseen. |
| `ink.2` | subtext0 | 7.4:1 | Supporting words: a seen row's title, a second line, a description. |
| `ink.3` | overlay2 | 5.8:1 | Meta and machinery: tool lines, ages, counts, labels. |
| `ink.4` | overlay1 | 4.4:1 | Only what is repeated elsewhere or is not information: the gutter time, a placeholder, a disabled control. Never on `surface.raised`. |

Nothing is dimmed with opacity. "Pending" and "not reached" are drawn in `ink.3` with a dashed rail or a word, not at
55 % or 45 %.

### Roles
| role | Mocha | means | appears as |
|---|---|---|---|
| `you` | mauve | You said it. | The rail and label of your block; your queued messages. |
| `focus` | mauve | Your hand is here. | The focus ring; the cursor row's rail; a picked option's ring. |
| `primary` | mauve | The one action a form exists for. | Send, Answer, Accept. |
| `place` | mauve | Where: a directory or repository name in a title. | Title rows and row titles. |
| `attention` | peach | It waits on you. | The Ask, the "Needs you" head, the count on the mark, a waiting glyph, a blocker that is yours. |
| `live` | yellow | It is running now. | The spinner, a running tool's rail and clock, the composer's rail while working, the now cell. |
| `ok` | green | A verdict: passed, proven, done. | A done glyph, an ok chip, a passed cell. Not a tick on every successful tool. |
| `bad` | red | A verdict: failed. | A failed tool, a bad chip, an error line, a red rail on a failed round. |
| `halt` | maroon | You cut it short. | Interrupted, interrupting, a cancelled queued message, stderr. |
| `engine` | teal | The engine or another agent said it. | The rail and label of an event block; an accepted cell. |
| `ref` | blue | A name you can open. | A tool's name, a link, an epic's slug as a link. |
| `new` | lavender | New since you looked. | The unseen dot, the new rule, the age of an unseen row. |
| `mark` | yellow | A find match. | Find highlights only. |

Four roles share mauve on purpose (it is the app's own colour, and all four are "yours"). They are separate tokens so
one can move later.

### State, tone, level, band: the reconciled rules
**Session and step state** (`color.state.*`), one table for the glyph, the pill, the composer's rail and the phone:

| state | colour | glyph | word |
|---|---|---|---|
| working | `live` | spinner | working, then the tool and its clock |
| waiting | `attention` | filled `?` | waiting |
| held | `attention` | `‖` | held |
| done | `ok` | ringed `✓` | done |
| interrupted | `halt` | ringed `!` | interrupted |
| interrupting | `halt` | ringed `!`, pulsing | interrupting |
| failed | `bad` | ringed `✗` | failed |
| stopped | `ink.3` | `■` | stopped |
| idle, unknown, empty | `ink.3` | hollow ring | idle |

Changes from today: failed and stopped get glyphs (they have none); stopped is neutral, not red, because the owner
stopped it. Android's "background" word for a background wait is kept as a word, in `live`, not lavender.

**Epic tone** (`color.tone.*`), for a wedge's now cell, a strip segment, a pip and a Now row. Both clients already
agree: plan `line.firm`, run `live`, mine `attention`, bad `bad`, acc `engine`, ok `ok`. A passed cell is `ok`; a cell
ahead is `line.soft`.

**Host meter level** (`color.level.*`): ok lavender, warn peach, crit red. Both clients agree; kept.

**Context band** (`color.band.*`, `rule.band`): green under 40, yellow under 60, peach under 80, red from 80; five
blocks of 20. Both clients agree; kept.

Meters are the one place peach is a quantity and not a call on the owner. See DECISIONS.md, open item 3.

## 2. Type

One face on both clients: Orrery Mono (Maple Mono), regular and bold; italic only inside Markdown emphasis.

| token | size / line | weight | for |
|---|---|---|---|
| `type.label` | 11 / 16 | bold, caps, tracked 0.08em | Section heads, block labels (`YOU`), meter labels, tags. |
| `type.small` | 12 / 18 | regular | Machinery and meta: tool lines, second lines, chips, pills, buttons, the strip. |
| `type.body` | 14 / 22 | regular | Reading text, your words, a row's title, inputs, an option's name. |
| `type.title` | 16 / 24 | bold | The page's title; the epic's now sentence. |
| `type.display` | 22 / 28 | bold | The orrery's count; a contract's title. One per page at most. |

Bold is allowed at `small` and `body` for emphasis (an unseen row's title, an option's name, Markdown strong). There
are no other sizes: 19 become 5.

## 3. Space, size, radius

`space`: 2, 4, 8, 12, 16, 24, 32, 48. A component's padding and gaps come from this list.

Fixed uses: a block's padding is 8 by 12; a row's is 8 by 12; rows of machinery touch (gap 0); items in a turn are 12
apart; a turn starts 32 below the one before; a page's side padding is 24 at the desk and 16 on the phone.

`size`: a rail is 3, a thread is 1, a hairline is 1, a state glyph is 16, a control is 28 high at the desk and 48 on
touch.

`radius`: 4 for small things (a key cap, a chip, inline code), 8 for rows, blocks, buttons, fields and wells, 12 for
overlays, the Ask and the dock. `round` only for the pill and for dots. A block with a rail is square on the rail's
side and `radius.2` on the other.

## 4. Rails, threads and the spine

Every conversation item hangs from the left edge of the reading column: the **spine**. What is on the spine says what
kind of thing the item is, before any word is read.

| on the spine | means |
|---|---|
| nothing | The reply. It is the only plain full-width text, so it is the figure. |
| a 3 px rail in a role colour | A **Block**: someone said something. The rail and the block's label share one tone. The tone is the block's verdict when it has one (`ok`, `bad`), else `attention` when it asks, else its speaker (`you`, `engine`). |
| a 3 px `live` rail | The thing running now: a running tool, the composer while working. |
| a 1 px `line.soft` thread | A **Line**: machinery. Tools, thinking, tasks, notices, folded engine prompts. |
| a dashed 3 px rail | Pending: a queued message (`you`), an arriving event (`engine`). |

The same rail is the cursor row's mark in the inbox (`focus`), and the composer's state edge. A rail is never 2 px.

## 5. Elevation and layers

Flat everywhere except a panel over the page, which takes `surface.overlay`, a `line.firm` outline and
`elevation.overlay`; the inbox flyout takes `elevation.side`. On Android an overlay is a sheet with the outline and no
shadow. Layers, low to high: page, flyout, menu, notices, panel (`z.*`). The key list and the palette are panels, above
the notices.

## 6. Motion

The five moves both clients already share, unchanged: press 120, state 200, layout 280, drill 320, all ease-out
`(0.2, 0, 0, 1)`; leave 200 ease-in `(0.4, 0, 1, 1)`. A drill moves 16. Loops: spin 1100 linear, pulse 1800 between
full and a quarter opacity, blink 1100. With reduced motion every move is one 100 ms linear step (a snap on Android)
and loops hold still. No component uses a raw duration (the phone's header fold becomes `move.state`).

## 7. Layout

- **Desk** (760 and wider): the inbox column (340, or 316 under 1250) and the page. The page is a title row, one
  scrolling area, and a foot. Reading text is one column 88 characters wide with a 5-character time gutter on its
  left. Other pages keep to `layout.pageMeasure` (1120).
- **Phone** (under 760, and the Android app): one page at a time. The inbox is a page; opening a row drills to its
  page and back returns. There is no gutter: a turn's time moves into its first item's label line.
- **Time is written three ways and no more**: a clock `14:31` for when something happened today (a date before
  today), an age `5d` `3h` `12m` `40s` for how long ago in a list, a running clock `1:16` for something live.
  "29 h 32 min" and "2 min ago" go.

## 8. Component inventory

Names are the catalog's story names: `Component/State`. A state listed in *italics* is a flag that combines with the
others. "Phone" notes say what differs under 760 and in Compose; where there is no note, the two are the same.

Legend: **[A]** anatomy written here. **[S]** sketch: named, states listed, anatomy not yet settled.

### 8.1 Primitives

**Mark** [A]. The orrery drawing. `Plain`, `WithCount` (the count of what needs you beside it, `type.label`,
`attention`).

**Kbd** [A]. A key cap: `type.label` without caps, `radius.1`, `surface.raised`, `ink.2`. `Default`, `Active`
(`primary` fill, `ink.onAccent`), `Ask` (`attention` fill, `ink.onAccent`). Not drawn on touch.

**Button** [A]. `type.small`, height `size.controlDesk` (touch: `size.controlTouch`), padding 0 by 12, `radius.2`.
Variants: `Quiet` (no fill, `ink.2`), `Default` (`surface.raised`, `ink.1`), `Primary` (`primary` fill,
`ink.onAccent`, bold), `Danger` (`bad` tint and outline, `bad` text). States: `Rest`, `Hover` (desk), `Pressed`
(`surface.pressed`, scale 0.97 over `move.press`), `Focused` (1 px `focus` outline, offset 1), `Disabled` (`ink.4`,
no fill), `Flashing` (2 px `focus` outline for a moment after a keyboard use).

**ConfirmButton** [A]. A command that asks first. `Rest` (a Default or Danger button whose label ends in `…`),
`Asking` (the question in `ink.2`, then a Primary confirm and a Quiet keep, in the button's place).

**IconButton** [A]. Square, `size.controlDesk`. `Back`, `Close`, `More`, `Send` (Primary). Same states as Button.

**Field** [A]. One-line input: `surface.well`, `line.soft` outline, `radius.2`, `type.body`, padding 4 by 8, an
optional leading Kbd. `Empty`, `Filled`, `Focused` (`focus` outline). Used by Filter, Find and the palette.

**Segmented** [A]. The inbox's order switch: segments of `type.small` in a `surface.well` track, the chosen one
`surface.raised` and bold, each with its Kbd at the desk. `Attention`, `Project`, `Archived`. It is one line at 316.

**StateGlyph** [A]. 16 px, the table in part 1: `Working`, `Waiting`, `Held`, `Done`, `Interrupted`, `Interrupting`,
`Failed`, `Stopped`, `Idle`.

**Pill** [A]. The state as a word: `radius.round`, `type.small`, the state colour as text on its tint.
`Working` (spinner, word, tool name in `ref`, running clock), `Waiting`, `Held`, `Done`, `Interrupted`,
`Interrupting`, `Failed`, `Stopped`, `Idle`; for an epic: `Planning`, `Building`, `Releasing`, `Paused`, `Parked`,
`Ended`. Phone: the pill is drawn as a full-width **StateBar** at the top of the dock, because the header folds away.

**Chip** [A]. `type.small`, `radius.1`, padding 0 by 8, a role's tint. `Ok`, `Bad`, `Neutral` (`surface.raised`,
`ink.2`).

**Meter** [A]. One host stat. `Ok`, `Warn`, `Crit`; `WithBar`, `NoBar`. Desk: label (`type.label`, `ink.3`), a 32 px
bar (`size.meterBar`, track `line.soft`), value (`type.small`, `ink.2`) on one line. Phone: label over value over a
full-width bar, as `HostMeters.kt` draws it today.

**ContextMeter** [A]. Five blocks and a percent in the band colour, then `152k of 1M` in `ink.3` where there is room.
`Band1`, `Band2`, `Band3`, `Band4`, `Unknown` (not drawn).

**CommitMark** [A]. `⎇` in `ink.3`, the short hash as its hover text and accessible name. `Default`.

**Menu** [A]. An overlay list of Quiet buttons. `Closed`, `Open`.

**SectionHead** [A]. `type.label` plus a count in `type.small` `ink.3`. `Plain` (`ink.3`), `Attention`, `New`,
`Ahead` (a section not reached: the head and the words "not reached" on one line, `ink.4`, nothing under it).

**InlineNote** [A]. One line under a control saying what happened. `Error` (`bad`), `Ok` (`ok`), `Quiet` (`ink.3`).

### 8.2 Shell

**TitleRow** [A]. Line one: Back (only when there is a crumb to go back to), Mark with its count, the crumbs, the
Pill, an optional blocker Pill in `attention`, then the commands at the right. Line two: the MeterStrip. The last
crumb is the page and is `type.title`; earlier crumbs are `type.small` `ink.3`. A session's crumb is its directory in
`place` and its summary in `ink.1`. The state is drawn once, as the Pill. Variants: `Conversation`, `Epic`,
`EpicSubpage` (contract, phase), `WorkerSession`; *WithStrip*, *StripHidden*. Phone: back and the title (two lines at
most) with the ContextMeter at the right; the meters stacked under it; the commands move into the dock; the whole
header folds on scroll.

**MeterStrip** [A]. The host's name (`type.small` bold `ink.2`), its Meters, and at the right the ContextMeter.
`HostAndContext`, `HostOnly`, `ContextOnly`, `OtherHost` (name, no stats).

**Inbox** [A]. Head: Mark, "orrery", Ask; then one row holding the Filter and the Segmented. Then sections.
`Docked`, `Hidden`, `Flyout`, `Pinned`; `OrderAttention`, `OrderProject`, `OrderArchived`; `FilterNoMatch`, `Empty`,
`LoadError`. Sections by attention: Needs you, Epics, New since you looked, Working, Seen. Ended epics are a fold at
the foot of Epics showing at most three unseen ones, never a section of their own. Phone: it is the home page.

**InboxRow** [A]. A session. Columns: StateGlyph (16), then the main. Line one: directory (`place`, `type.small`),
summary (`type.body`, one line, ellipsis), age (`type.small`, `ink.3`) at the right, More on hover or cursor. Line two:
its last words, `type.small` `ink.3`, one line. Line three, only when it waits: the Ask at row density.
States: `Working`, `WaitingQuestion`, `WaitingPlain`, `Done`, `Interrupted`, `Failed`, `Stopped`, `Idle`; *Unseen*
(summary bold `ink.1`, a `new` dot before the glyph, age in `new`), *Cursor* (`surface.raised` and a `focus` rail;
no ring), *Hover*, *Answered* (line three is "You answered 1 · label" in `ok`), *MenuOpen*.

**EpicRow** [S]. Mini Orrery, slug (never cut before the repository is), count, state words, blocker, optional
ProgressStrip. No repository hash. Ended words are plain: "shipped", "superseded", "abandoned", "met at base".
`LiveFull`, `Live`, `Blocked`, `Ended`, `WaitingToStart`, `Unavailable`; *Cursor*.

**StepRow** [A]. Under its epic, indented: StateGlyph, label, Pips, what it is doing. `Running`, `Held`; *Cursor*.

**NeedRow** [S]. An epic's need in "Needs you", as a row: an `attention` glyph for its kind, one sentence (ellipsis,
whole text on hover, no hashes), the epic's slug, then one command. `Question`, `Park`, `Release`, `Contract`.

**NoticeStack** [S]. Overlay rows at the top right of the page area, below the title row, never over it. `One`,
`Three`, `ThreeAndMore`. A notice's title is a directory and summary, never an id.

**Palette** [S]. The Ask panel. `Empty`, `Results`, `StartForm`, `Error`.

**KeyList** [S]. Grouped: moving, the inbox, reading, answering, pages. `Open`.

**TalkMenu**, **TalkDock** [S]. `TalkMenu/Open`; `TalkDock/Loading`, `Conversation`, `Interrupted` (with Resume),
`Refused`.

**Split** [S]. Two pages side by side, each with its own TitleRow. `Two`.

### 8.3 Conversation: the four shapes

**Reader** [A]. `Loading`, `Empty`, `Following`, `ScrolledUp`, `Finding`.

#### Block: someone said something
Anatomy shared by every block: a 3 px rail on the spine, a surface, padding 8 by 12, `radius.2` on the far side. A
label line (`type.label` in the rail's tone, then optional words in `type.small` `ink.3`, then at the far right a time
on the phone or a link). Then the body.

**YouBlock** [A]. Rail `you`, `surface.raised`, label `YOU` and where it came from, body `type.body` `ink.1` as
Markdown. `Default`, `FromPhone`, `Sending` (dashed rail, label `SENDING`; full-strength text), *FindMatch*.

**EventBlock** [A]. Rail `engine`, `surface.well`, label `EVENT`. `Default`.

**StepEndBlock** [A]. Label `STEP ENDED`, the agent's name in bold, then one Chip with the end (`done` Ok, `failed`
Bad, `stopped` Neutral). The rail is the verdict. Body: the first line of the summary, `type.small` `ink.2`; the whole
summary when expanded. A reason is drawn only when the end was not good. No field names. `Ok`, `Failed`, `Stopped`;
*Expanded*, *OpensStep*.

**EpicEventBlock** [A]. One line: label `EPIC`, the event's sentence in `ink.1`, the slug in `ref` at the right. Rail
`attention` for a question or a park, `ok` for released, `engine` otherwise. `Question` (an Ask follows it),
`QuestionSettled`, `Parked`, `Released`, `Ended`.

**EpicCard** [S]. Rail `engine`. Head: slug, Pill, open link. A five-part track, the now sentence, a row per
requirement (`ok` proven, `bad` its test is written and fails, `ink.3` no test yet), the count, Pause or Resume.
`Loading`, `Live`, `QuestionOpen`, `Paused`, `Ended`, `Unavailable`.

**CheckBlock** [A]. A send-back round. Label `CHECKS · ROUND 2`, a Chip per check, a note. Rail is the verdict.
`Ok`, `Bad`; *NoteOpen*.

#### Line: machinery
Anatomy shared by every line: a 1 px thread on the spine, padding 0 by 12, one line of `type.small` `ink.3`; a name in
`ref`; a status at the far right only when it is not plain success. Lines in a run touch.

**ToolLine** [A]. Name, input (ellipsis), status. `Done` (no mark), `Failed` (`✗` in `bad`), `Running` (the thread
becomes a `live` rail; a running clock in `live`), `Open` (its result under it in a `surface.well`, `radius.2`),
`OpenFailed`.

**ToolRun** [A]. Three or more calls as one line: `▸`, "6 tool calls", the names with counts, then a dot per call:
`line.glyph` for one that succeeded, `bad` for one that failed, `live` for one running. `Closed`, `Open`, `Running`,
`HasFailure`.

**ThinkingLine** [A]. "thinking" in italics. `Live`, `LiveOpen`.

**TaskLine** [A]. A background task: its type in `ref` (`agent`, a sub-agent's name, or `shell`), its description in
`ink.2`, its latest activity in `ink.3`, its status at the right. `AgentRunning`, `ShellRunning`, `Done`, `Failed`,
`Stopped`; *Link* (opens its Agent call). The same line is "Agent finished" in the transcript (status `finished`,
or `failed` in `bad`, `stopped` in `ink.3`).

**NoticeLine** [A]. The notice's kind as a label, then its text. `Default`, `Error`.

**EngineFold** [A]. A step's brief or an engine prompt folded to a line. `BriefClosed`, `BriefOpen`, `EngineClosed`,
`EngineOpen`.

#### Rule: a mark across the column
Anatomy: a 16 px lead of line, a `type.label` word, optional detail in `type.small` `ink.3`, then line to the column's
edge. One line; never wraps.

**Rule** [A]. `Compaction` (`ink.3`, `line.soft`), `CompactionOpen` (its summary under it in a well), `New` (`new`,
"NEW SINCE YOU LOOKED"), `Interrupted` (`halt`, "INTERRUPTED BY YOU"), `Day` (a date).

#### Ask: it waits on you
**Ask** [A]. The one bold component. A block with an `attention` rail, an `attention` tint and outline, `radius.3`.
- Label line: `ASKS YOU`, who asks (`type.small` `ink.2`), how long ago.
- The question: `type.body`, regular weight, `ink.1`, Markdown. Never bold.
- Options: one per row, `surface.raised`, `radius.2`, 4 apart. Each: a Kbd `Ask` with its number, the option's name
  in bold `type.body`, its description in `type.small` `ink.2`, and a `recommended` Chip (Neutral) when it is.
- Foot (epic variant only): the Primary button "Answer with …", and the line that will be recorded.

Variants: `Session` (one press answers: keys 1 to 3, or a tap), `Epic` (pick, then confirm; a "Something else" option
opens a text box). States: `Open`, `Picked` (a `focus` ring on the option), `Sending`, `Answered` (folds to one line:
"You answered 1 · name", `ok`), `Settled` ("This question is settled", `ink.3`), `Error` (InlineNote; the pick and the
text are kept). Density: `Card` (conversation, epic page), `Row` (in an InboxRow: the question on one line and the
options as chips with peach caps). Phone: options are 48 high and have no key caps.

#### The dock
**Dock** [A]. One box at the foot of the page: `surface.well`, `line.firm` outline, `radius.3`, the state's rail on
its left edge. Top to bottom, each part only when it has rows, parts separated by `line.soft`:
1. `Running`: the background tasks, as TaskLines.
2. `Arriving`: events the session holds, one line each with a dashed `engine` rail: the event's title and, at the
   right, "arrives when you next speak" or "arrives when the session is free".
3. `Queued`: your messages, each with a dashed `you` rail, and "n queued · ↑ edits the newest".
4. The input: `type.body`; the placeholder says what a send will do in this state; the Send IconButton.

Each tray shows three rows and then "n more". States: `Idle`, `Working`, `Waiting`, `Held`; *Focused* (`focus`
outline; the rail stays), *WithRunning*, *WithArriving*, *WithQueued*, *QueuedCancelled* (struck through, `halt`
label), `Error`. Variant `Step`: Interrupt (Danger), the nudge input, More (Hold, Stop…). Phone: the StateBar first,
then the goal line, the trays, the input with a "Send" Primary button, and for a pane the quick keys.

**FindBar** [A]. A Field with a count at the right. `Empty`, `Matches`, `NoMatch`.

### 8.4 Epic pages

All [S]: named so the catalog and the scenario list can be laid out, anatomy still to be drawn.

**EpicPage**. Order: TitleRow; the summary at reading measure with two link lines (Contract, Conversation); the
NowBlock; the Orrery beside Now / Next / Just happened (stacked under 1250); Red proof; Green proof; Release; Report.
A section not reached is a `SectionHead/Ahead`, one line. `Planning`, `Building`, `BuildingBlocked`, `Parked`,
`Releasing`, `Ended`, `Unavailable`.

**NowBlock**. The now sentence in `type.title`, then what blocks it. `Running`, `Question` (an `Ask/Epic`), `Parked`
(a block with a `bad` rail, the reason without hashes, Retry and Stop…), `Held`, `Problem`.

**Orrery**. `Mini` (28, no text), `Full`. Past twelve wedges the full chart draws no labels and names the wedge under
the pointer in a caption below it. The ring names are a legend under the chart, not on it. The centre is the count in
`type.display` over "integrated" in `type.label`. With no tasks it is not drawn. `Empty`, `Planned`, `Running`,
`Mine`, `Bad`, `Accepted`, `Done`, `Dense`.

**ProgressStrip**, **Pips**. `Sparse`, `Dense`; a cell per tone.

**HappeningList**. `Now`, `Next`, `JustHappened`, `JustHappenedMore`. A row is a clock, a gap, then the words.

**ProofCard**. A card per requirement: its id, a verdict Chip, its text, its evidence. `RedHolds`, `RedMissing`,
`GreenProven`, `GreenOpen`, `ByReview`; *WithRuns*.

**EvidenceRun**. One line: phase, verdict Chip, commit mark; logs fold under it. `Passed`, `NotPassed`, `LogOpen`,
`LogPending`, `LogUnavailable`, `WithImage`.

**ReleaseStep**. `Pending`, `Done`, `Failed`, `Yours`.

**Report**, **Handover**, **WorkflowNode**. States not yet listed.

**PhasePage** and **PhaseRow**. `Passed`, `Now`, `Ahead`; *Open*. Inside: `Attempt`, `CheckRound` (a CheckBlock),
`GateTail`, `Judge`, `Review`, `Finding`.

**WorkerSession**. A Reader of blocks and lines with a `Dock/Step`. `Running`, `Held`.

**ContractPage**. Title in `type.display`, the accepted line, then sections with SectionHeads; a row per failure,
addition, requirement and assumption as a small table with real columns. `Accepted`, `ToAccept`, `SevereUnread`.
Round 5's contract card in a conversation is not built in the web today and is not specified here yet.

### 8.5 Only on one client

Android only, kept: **GoalLine** (`Closed`, `Open`), **QuickKeys** (`Default`, `NotSent`), Pools, Settings. The goal
line should come to the web's dock; see DECISIONS.md.

Web only: FindBar, KeyList, Split, the inbox flyout.

## 9. Not written yet

- Anatomy for every [S] component.
- Markdown inside a reply: headings, lists, code blocks, tables, quotes, links, and how inline code differs from a
  raised surface.
- The phone's home page and header in the same detail as the desk's.
- The scenario list (which fixture shows which story) and the mapping from today's class names and composables to
  these names.
- Checks that the five sizes and four inks hold up when drawn.
