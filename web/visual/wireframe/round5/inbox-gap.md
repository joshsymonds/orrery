# The inbox against view B

View B is the wireframe's direction B, "Inbox": `webview/#b` (`index.html`, `style.css`, `app.js`). This file lists
where the built web page falls short of it, worst first, and what to build to match or beat it. The proposal is drawn in
`inbox-proposal.html` and set beside view B in `inbox-options.html`.

## What was compared

| | 1600 × 1000 | 412 × 915 |
|---|---|---|
| View B | `b-1600.png` | `b-412.png` |
| Built page, E27b on :8494, cursor on an epic | `live-1600.png` | `live-412.png` |
| Built page, cursor on a session | `live-row-1600.png` | |
| Proposal | `prop-1600.png` | `prop-412.png` |

The screenshots are in
`/tmp/claude-1000/-home-joshsymonds-nix-config/20e8fa96-aa89-4850-94f8-267561dce179/scratchpad/chartdesign/`. The three
of the built page show the owner's real sessions, so they stay there and are not in the served folder.

The reader rework in progress (W4a, W4b) sits on top of E27b and is not deployed on :8494. I read it from source at
`effort/orrery-web-reader-markdown-1` (656e95a) and did not render it. Where it already answers a gap, the gap says so;
those notes are from the CSS and markup, not from a picture.

One difference is deliberate and is not a gap: view B sets prose in Atkinson Hyperlegible, and the owner's later call is
Orrery Mono everywhere, which W4a applies. The proposal is mono throughout.

## The gaps, worst first

### 1. No frame

**View B:** three surfaces that fill the window and scroll apart: the list on crust, 440 wide, behind a surface0 rule;
the reader on base, fluid; the epic column on base behind a rule, 320 wide. Nothing spans them.

**Built:** a document. `body` has 16 px padding, `.app` is capped at 1200 px (a quarter of a 1600 window is empty),
everything is on crust, and the page scrolls as a whole. The brand, filter and view toggle span all columns above
them, so the filter is 1200 px wide and every column loses over 100 px of height.

**W4a:** from 760 px the page fills the window and the list, reader and epic column scroll apart. The cap is gone. Still
missing: the surfaces, the fixed widths (W4a's columns are `2fr 3fr 3fr`, so the list takes 40% of a two-column window),
and the top block is still above the columns.

**Build:**
- `.app` is the grid: `440px minmax(0, 1fr) 380px`, full height. At 1500 px and under `400px minmax(0, 1fr) 340px`. At
  1100 and under `380px minmax(0, 1fr)`, the epic column taking the reader's place when the cursor is on an epic, as
  today. Under 760, stacked, as today. With a pin: `440px 1fr 1fr`, and the epic column shows only from 1700.
- `body` has no padding and is on base. `.list` is on crust with `border-right: 1px solid var(--surface0)`. `.side` has
  `border-left: 1px solid var(--rule)`; add `--rule: #2a2b3c`, the wireframe's token.
- `.top` (brand, filter, toggle) moves inside `.list`, padded `12px 14px 10px`. The rows scroll under it.
- The epic column is 380, not view B's 320, because the chart has to hold 35 to 150 tasks. That is the one width the
  proposal changes.

### 2. The reader has no head and no foot

**View B:** a head (label at 17/24 bold, where it runs, a pill with the state, the tool and the timer, key hints at the
right) over a scroll area, over a composer pinned to the bottom with a send button.

**Built:** the conversation starts under the view toggle with no title. The working line and the composer come after
the last message, wherever that is. Reading a long conversation, nothing on screen says which one it is.

**W4a:** the conversation scrolls in its own box, so the composer stays in view. No head.

**Build:**
- `.read` is three rows: `auto minmax(0, 1fr) auto`.
- Head: padding `12px 28px 11px`, `border-bottom: 1px solid var(--rule)`. The row's label as `h1`; then project and host,
  or for a step `epic › agent · step · round n`, 12/24 in overlay1; then the working line as a pill (yellow text on
  yellow at 10%, a 30% ring, radius 99, the spinner glyph, the tool in blue, the clock). The pill is peach when waiting
  and green when done. The working line's note stays as a line above the composer.
- Scroll area: padding `18px 28px 10px`. Send area: padding `0 28px 14px`.
- Composer: one row, input and a mauve send button. For a step: Interrupt at the left (red on red at 10%), the nudge
  input, and Stop behind `⋯`, as P4 decided. The nudge sentence sits above the row in overlay0.

### 3. Tool output floods the reader

**View B:** a run of tool calls is one line: `9 tool calls  Read ×3, Write ×2, Edit ×2, Bash ×2  ✓`.

**Built:** every result is printed in full, monospace, at body size. One result filled the window in the screenshot.

**W4b:** a call is one line with its result folded behind it, failures marked, orphan results hidden, live thinking
folded. This closes most of it.

**Build, on top of W4b:** three or more calls in a row with no text between them fold to one line: the count in
subtext1, the tools with counts, and a ✓ in green or a ✗ in red at the right. It opens to W4b's one line per call. A
single call keeps its tool name in blue.

### 4. Epics crowd out the sessions

**View B:** three epics, each with its running steps, between Needs you and New since you looked.

**Built:** every live epic is a full row. On the day I looked there were 25, most of them finished (10/10, 17/17), and
the sessions started below the fold on the desktop and three screens down at 412.

**Build (the owner's call, see the questions at the end):** an epic with a running or held step is drawn in full. A live
epic with none is one line. Past six epics the rest fold into the `N folded` line the section already has.

### 5. An epic row says almost nothing

**View B:** `rota-export  lighthouse  build · 2 of 7 units`, a five-stage track, each running step as one line (glyph,
name, five pips, what it is doing), a paused step in peach, and a tail (`2 integrated · 2 outlined · 4 engine steps
ended`).

**Built:** a gauge, the slug, the repository, `16/20`. A step row is its label, `HELD`, and one line of reply. No pips.

**Build, from what the API serves:**
- Epic line: gauge, slug in bold, repository, and at the right `{outcome} · {done} of {total}`.
- **Task strip** under it: one segment per task in record order, 3 px tall, 2 px apart: green done, yellow running
  (glowing), peach held, red ended short, grey for a task with no slice. Past 60 tasks the gaps close. It stands where
  view B's stage track stands.
- Step row: `14px 13ch auto minmax(0, 1fr)`, 12/20: a braille spinner in yellow, the task label, **five pips**, the
  step's `doing`. The pips are the task's five cells, coloured as the chart colours them (`chart-spec.md`, section
  4): green passed, yellow now and glowing, peach held, red errored, grey not reached. A held step's row is peach with
  `‖` and `held · {reason}`.
- Tail in overlay0 at 11.5: `{n} gap · {n} not started · {ended_steps} steps ended`, each part left out at zero.

### 6. A step's conversation looks like the owner wrote the engine's half

**View B:** `Brief from planner turn 2` as a fold; each round as a card (`RETURN · ROUND 1`, the commit, a chip per
check, what happened next); a review finding as its own card.

**Built:** the brief and every "Your return failed these checks" message are `YOU` bubbles with a mauve edge. In a step
session the owner wrote none of them.

**Build:**
- In a step's reader, a message the engine sent is never `YOU`. The first is a fold: `Brief  from the engine`.
- A message that opens a check round is a card: caption `Checks · round n` and `from the engine` at the right; a chip
  per `EpicStepCheck` of that round, green `name ✓` or red `name ✗`; then the first line of the engine's text in
  overlay1, the rest folded. The card's left edge is red when any check failed, green when all passed.
- A nudge the owner sent stays a `YOU` bubble.

This needs the reader to know that a session is a step and which message opened which round. The checks themselves are
already in `EpicStep.checks`.

### 7. The epic column has no hierarchy

**View B:** an eyebrow, the name with its kind, the stage track, the chart with a count in its centre, a legend, the
units (glyph, name, model, pips, a status line, the cursor's unit ringed in mauve), a card for the monitor, the
requirements.

**Built:** slug, repository and outcome as three plain lines; the chart at 200 px; every task as a dot, a slug and a
status word; then every step, oldest first.

**Build:**
- Eyebrow `EPIC` (10.5/14 bold, .13em, overlay1). One line: slug in bold 14/20, repository in overlay1, and at the right
  `{outcome} · {done} of {total}`. The task strip, 4 px.
- The chart: Orbits, `chart-spec.md`, at the column's width. Its key line under it.
- `TASKS  7 · 6 tracked`. A task row is `14px auto 1fr auto`, 12/18: a mark (✓ green, a spinner, `‖` peach, ✗ red, ○
  for not started), the slug, the agent in overlay0, the five pips at the right, and under them one line at 11/15 in
  overlay1: the step's `doing` while it runs, `held · {reason}`, the end kind and reason when it ended short, or the
  recorded status. The cursor's task has surface0 behind it and a mauve inset ring.
- A card for the cursor's running step: caption `{task} · now`, its `doing` at 13/19, then `started {time} · round {n}`.
- The step list folds to one line, `15 steps · 9 ended`, and opens to today's rows.

### 8. Rows lost their detail

**View B:** the glyph carries a mark (`?` on peach, `✓` in a green ring, `!` in a maroon ring) and the working one
turns. Only rows that need you carry a tag. A new row's text runs two lines. Section heads carry a count, and Needs you
is peach, New since you looked lavender.

**Built:** bare discs, none turning. Every row leads with its state word. One line of text. Heads in overlay1 with no
count.

**Build:** the glyph marks and the `rot` animation from the wireframe. The tag only on waiting rows: `QUESTION` when the
row has options, else `WAITING`. `.l2` clamps to two lines on an unseen row and one otherwise, 12.5/19. Heads: the
title, then the count in overlay0 at normal weight; `.need` peach, `.fresh` lavender.

### 9. No counts and no mark

**View B:** the mark, `orrery`, and at the right `5 need you` in peach, `3 working` in yellow, `✱ 3 new` in lavender.

**Built:** the word `orrery`.

**Build:** the mark (the wireframe's `#mark` symbol, 22 px) and the three counts, each from the rows already loaded:
waiting rows, working rows, unseen rows. 11.5/22, 12 px apart. A zero count is left out.

### 10. The answer keys are blind

**View B:** a question's options are numbered chips in its row.

**Built:** `1`, `2`, `3` answer the cursor's question, but the options are only in the reader's card.

**Build:** chips under the row's text: a peach key cap with the number, the option's label, peach at 12% behind and a
35% ring, 11.5/18. They wrap. After an answer the chips give way to one green line naming the choice.

### 11. The clock overruns its gutter, and the text has no measure

**View B:** `14:19` in a five-character gutter; text stops at 68 characters.

**Built:** `03:26 PM` in the same five characters, so it touches the text; the body column is the pane's width, past 100
characters a line at 1600.

**Build:** a 24-hour `HH:MM`. `.reader .t { grid-template-columns: 5ch minmax(0, 68ch); }`.

### 12. Queued messages sit in the conversation

**View B, and decision Q6:** docked above the send box, stacked oldest first, dim, with `↑` to edit.

**Built:** rows in the conversation (`.reader .queued`).

**Build:** move them into the send area, above the composer, joined to it: `border-radius: 10px 10px 0 0` on the queue
and `0 0 10px 10px` on the composer.

### 13. The key list is at the bottom of the list column

**View B:** key caps where the action is, and the full list behind `?`.

**Built:** the full list printed under the last row.

**Build:** a footer on the list (`8px 16px`, a rule above, 11/16 in overlay0) with the hosts the rows come from at the
left and `? keys` at the right; `?` opens the list over the page.

## At 412

The built page at 412 is the list alone, and that is right. Gaps 4, 5, 8, 9 and 10 are what show there; 4 hurts most,
since the sessions are three screens down. The proposal at 412 is its list column at full width with nothing else
changed. View B was not designed for this width.

## Type, in Orrery Mono

| Where | Size/line | Weight, colour |
|---|---|---|
| Brand | 14/22 | bold |
| Header counts | 11.5/22 | state colours |
| Section head | 10.5/14, .13em, caps | bold, overlay1 (peach, lavender) |
| Row label | 13/20 | subtext0; bold text when unseen |
| Row project, age | 11.5/20 | overlay0; age lavender when unseen |
| Row text | 12.5/19 | overlay1; subtext1 when unseen |
| Row tag | 9.5/20, .1em | bold, peach |
| Step row | 12/20 | subtext1; `doing` overlay1 |
| Reader label | 17/24 | bold |
| Reader text | 14/1.6 | text |
| Fold line | 12/20 | subtext0 on mantle |
| Card caption | 10/16, .12em, caps | bold |
| Check chip | 11.5/18 | green or red on its own colour at 12% |
| Epic column heading | 10.5/14, .13em, caps | bold, overlay1 |
| Task row, its status line | 12/18, 11/15 | subtext1, overlay1 |

Row text is half a point under view B's 13.5 because mono runs wider than Atkinson; at 12.5 a row holds the same words.

## Where the proposal beats view B

- **One drawing of progress at three sizes.** The pips on a step row, the strip under an epic and the chart are the same
  cells from the same slices. View B's pips, stage track and rings are three different measures.
- **The task strip is real.** View B's stage track needs a stage orrery does not record.
- **The chart answers back.** Its centre reads the cursor's task and any slice pointed at.
- **Check rounds come from stored checks**, so a failed round names the check that failed without opening anything.
- **The epic column holds a long epic.** At 380 the chart reads at 35 tasks; view B's 320 fits its 7.

## What view B shows that has no data behind it

Neither the built page nor the proposal can draw these until the API serves them:

- the stage track: contract, build, review, release, report;
- requirements and their evidence dots;
- outlined units, and the note that the circle grew;
- the model beside each unit; the monitor's verdict and model time;
- decision, contract and release rows in Needs you (the web list has panes and sessions; a held step shows under its
  epic);
- per-phase state in the chart (teal passed, red failed): `EpicSlice` has one hue per task;
- host load in the footer, the context meter in the composer, and `⌘K`.

## Questions for the owner

1. **Which epics get a full row?** The proposal: only those with a running or held step; the rest one line each; past
   six, folded.
2. **Which count does the chart's centre show?** It counts slices, so it can read `2/6` beside a row that says `2 of 7`.
   The proposal captions it `tracked tasks done` when they differ.
3. **Engine messages as cards** (gap 6) changes what a step's reader is. It is the largest piece of work here.
4. **380 for the epic column**, against view B's 320.
5. **Stop behind `⋯`**, as P4 decided for the phone, in place of today's visible Stop button.

## Order to build in

1, 2, 9 and 13 are CSS and markup and change how the whole page reads. 8, 10 and 11 are small. 5 and 7 need the slices
in the list's rows and the new chart. 3 follows W4b. 6 and 12 change the reader's model. 4 waits on the owner.
