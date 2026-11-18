# Audit: what the two clients draw today

Status: **complete as a first pass for the web, partial for Android.** Checked against the web build at
`origin/main` (96b916fd) through the read-only proxy at 1600, 1000 and 412 wide, and against the Compose source (the
Android app was read, not run). Sections marked *unverified* at the end were not looked at live.

Citations are `file:line`. `css:` is `web/src/style.css`. Kotlin files are under
`android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/`.

Problems are ranked by how much they cost the owner at the desk. Each has an id (`A1`…) that SYSTEM.md and
DECISIONS.md refer to.

## The short version

1. The web has no system under it. `style.css` holds 95 `font:` shorthands in 45 distinct forms, 19 font sizes from 8
   to 34 px, 14 line heights, 19 radius values, 46 distinct paddings and 16 gap values. Nothing is named, so each epic
   that added a component picked new numbers.
2. Whole surfaces shipped with no styles at all: the contract page, the picker, the Talk menu and dock head, and the
   epic needs in the inbox. 59 class names used in markup have no rule in the sheet.
3. The page has no hierarchy above the reply text. The page title is 12 px, the same size as a tool line. Nearly half of
   the grey text is below 4.5:1 contrast, while yellow, green and teal are as bright as body text or brighter.
4. One idea is drawn several ways. A question has four looks on the web and three on Android. A "block from someone" has
   seven. State has three vocabularies.
5. Phone width on the web is not a layout: the list and the conversation stack into one page that measured 124,722 px
   tall.
6. Android is two apps in one theme: the conversation screen is hand-set Mocha; every epic screen is Material defaults.
   Seven conversation item kinds the web draws are dropped or misdrawn on the phone.

## A. Structural problems (the cause of "ad hoc")

### A1. No type scale
- 19 sizes: 8, 8.5, 9, 9.5, 10, 10.5, 11, 11.5, 12, 12.5, 13, 13.5, 14, 14.5, 15, 15.5, 16, 17, 34. The half-pixel
  steps are single-use decisions: `css:207` (9.5), `css:664` (10.5), `css:218` (11.5), `css:460` (13.5), `css:449`
  (15), `css:609` (15.5).
- Line heights are both px and unitless in the same component: the reader body is `14px/1.6` (`css:392`), its tool
  lines `12px/20px` (`css:435`), results `12px/1.45`, the question `700 15px/1.45` (`css:449`).
- Caps labels are tracked four ways: `.08em`, `.1em`, `.12em`, `.13em` (`css:178`, `css:207`, `css:420`, `css:664`,
  `css:737`).
- Android: `Type.kt:16-33` is Material's default scale with the mono family swapped in. Screens then either hand-set
  `sp` (9, 10, 11, 11.5, 12, 12.5, 14, 15, 15.5, 17, 24 across `ConversationView.kt`, `PaneHeader.kt`, `HostMeters.kt`,
  `StatusLine.kt`, `GoalLine.kt`) or use Material roles (`bodyLarge` 16, `titleLarge` 22, `labelSmall` 11) on the list
  and every epic screen. Neither set matches the web's.

### A2. No spacing scale, no radii, no surfaces
- Gaps in use: 0, 2, 4, 5, 6, 7, 8, 9, 10, 12, 14, 16, 20. Paddings: 46 distinct shorthands, mostly one-offs such as
  `9px 12px 10px` (`css:448`, `css:480`), `8px 14px 10px` (`css:419`), `8px 10px 9px 6px` (`css:188`).
- Radii: 1, 1.5, 2, 3/10 (asymmetric), 4, 6, 7, 8, 10, 12, 14, 99, 999. The question card is 14 (`css:448`), its
  options 10, the composer 10 (`css:597`), a fold 7 (`css:466`), a button 6 (`css:699`), the epic option card 12
  (`css:725`). Android: 8, 10, 14 and 4/12 asymmetric (`ConversationView.kt:333`), composer 14 (`ComposeBox.kt:68`).
- Surfaces are picked per rule. A "card" is mantle with a rail (`css:480`), mantle with an inset ring (`css:778`),
  mantle alone (`css:811`), surface0 (`css:419`), or a 12-step family of `color-mix` tints at 8, 10, 12, 14, 22, 30,
  35, 40, 45, 50, 60, 75 and 80 percent.
- `--rule: #2a2b3c` (`css:7`) is not a Mocha colour; it exists only on the web.
- z-order is 1, 5, 5, 20, 30. The key list is `z-index: 1` (`css:331`), under the notices (20) and level with nothing,
  so the notice stack draws over it.

### A3. No control primitives
- `appearance: none; -webkit-appearance: none` is repeated 24 times: every button resets the browser by hand.
- Buttons that forgot are browser-default grey: `+ Ask` (`app.tsx:1452`), the picker's input, textarea and Start
  (`picker.tsx:79`, `picker.tsx:145-146`), the dock's close (`talkdock.tsx:113`), every item in the Talk menu
  (`talk.tsx:86-108`).
- The order switch is three `<span>`s with click handlers (`app.tsx:1466-1475`), not a control: no focus, no role.
- Android has three button looks: Material `TextButton` (title rows, list header, epic commands: `TitleRow.kt:56`,
  `PaneListScreen.kt:219`, `EpicScreen.kt:175`), a hand-made surface0 button with a surface1 border
  (`SessionControls.kt:214`, `StepControls.kt`), and the filled mauve Send (`ComposeBox.kt:257`).

### A4. Unstyled surfaces (class names with no rule)
59 classes used in `web/src/*.tsx` have no selector in `style.css`. The ones that are whole surfaces:

| surface | what the owner sees | code |
|---|---|---|
| Contract page | Browser-default headings and bullets. Each row's id, text, rating and response are adjacent spans with no gap, so they run together as one word. | `contract.tsx:13-123`; only `css:834` exists |
| Picker / Ask | A transparent native input over the conversation, no panel, no rows styling. It is hard to see that it opened. | `picker.tsx:70-107`, `143-147`; only `css:347` |
| Epic need in the inbox | A raw paragraph of the engine's sentence, flush left outside the row grid, with a full 40-character commit hash, then a button. It is the top of "Needs you". | `needentry.tsx:22-23`; only `css:322` (a fade) |
| Talk menu | Native buttons in a bulleted list inside the title row's command group. | `talk.tsx:86` |
| Talk dock head | A 17 px heading (`css:354`) beside a native close button; "loading…" flush to the dock's edge. | `talkdock.tsx:111-116` |
| Just happened | Time and text touch with no gap: `.at` has no rule. | `happenings.tsx:154` |
| Handover, evidence verdict, review gate, judge reason | Inherit whatever is around them. | `handover.tsx:39`, `evidence.tsx:40-44`, `phaseoutput.tsx:221-267` |

### A5. One idea, several drawings
- **A question** is drawn four ways on the web: chips in the list row (`css:254`, `app.tsx:1958`), the reader's card
  with the whole question in bold 15 px (`css:448-449`, `reader.tsx:390`), the epic form with key caps, a
  `recommended` tag and a confirm step (`css:725-744`, `blocker.tsx:136`), and that same form re-hosted under an event
  line by wrapping it in a `.now-block` to borrow its styles (`eventring.tsx:248-256`). On Android: a card labelled
  "CLAUDE IS ASKING" (`QuestionCard.kt:96-108`), a copy of it (`SessionControls.kt:115-127`), and for an epic a bare
  `OutlinedTextField` with an Answer button and no options (`EpicBlocker.kt:80-105`).
- **A block from someone** has seven looks in one conversation: `.you` (surface0, mauve rail, `css:419`), `.event`
  (mantle, teal rail, `css:422`), `.card` (mantle, green/red rail, `css:480`), `.epic-card` (mantle, mauve rail,
  `css:495`), `.step-card` (mantle, teal rail, `css:549`), `.event-ring` (a teal-bordered pill, `css:528`),
  `.question-card` (peach tint, peach ring, 14 px radius, `css:448`).
- **A tool call** has two looks: one call is grey text on a hairline (`css:435`); three or more are a mantle box with
  green dots (`css:466-478`). On Android both are mantle boxes with a bold blue name (`ConversationView.kt:433`, `470`).
- **Rail colour** means four different things: who spoke (mauve = you, teal = event), a verdict (green/red on check
  cards), liveness (yellow on a running call, `css:441`), and nothing in particular (the epic card's mauve rail,
  `css:497`, which is not the owner's).
- **Time** is `5d` in a row (`app.tsx:2434`), `29 h 32 min` in an epic's state, `2 min ago` on an ended epic
  (`epics.ts:183`), `1:16` in the pill (`workline.tsx:109`), `14:31` in the gutter, and on Android also `just now` and
  `N min ago` (`ConversationView.kt:267-279`).

### A6. Three state vocabularies
| state | web glyph (`css:197-205`) | web pill (`css:368-374`) | Android list (`PaneListScreen.kt:97`) | Android status line (`StatusLine.kt:84-109`) |
|---|---|---|---|---|
| working | yellow spinner | yellow, tool, clock | yellow word | yellow tint, "working" or lavender "background" |
| waiting | peach `?` | peach | peach word | peach tint, "waiting for you" |
| done | green ring `✓` | green | green word | green dot, "done" |
| interrupted | maroon ring `!` | maroon | plain word | nothing |
| interrupting | none | maroon | not drawn | not drawn |
| failed, stopped | **no glyph**: the class is `g-failed`/`g-stopped` and no rule exists (`app.tsx:1607`) | red, as "ended" (`workline.tsx:88-90`) | plain word | nothing |
| idle / empty | **no glyph** (`g-`) | grey | plain word | nothing |
| held (step) | peach `‖` (`css:319`; the yellow at `css:314` is never seen) | peach | — | — |

Android's palette has no maroon and no teal (`Color.kt:5-23`); the chart adds teal privately (`Orrery.kt:84`).

## B. Web, page by page

### B1. Inbox (all three orders)
1. **Dead epics outrank live sessions.** "Just ended" drew 24 rows, then "167 ended earlier", between Epics and "New
   since you looked" (`app.tsx:1535-1563`). At 412 wide that is two full screens before the first session.
2. **Epic needs are unstyled** (A4): the most urgent thing in the inbox is the least designed.
3. **Epic rows cut the wrong thing.** The slug ellipsises to four letters and an ellipsis while the repository name with its
   hash suffix and the count keep their width (`css:272-274`, `epicline.tsx:88-90`). Two epics become
   indistinguishable. The repository-hash suffix is drawn on every epic row, against the standing "no hashes".
4. **Raw enums as words**: `closed_shipped`, `met_at_base` (`epics.ts:183-185`; same on Android,
   `EpicsGroup.kt:131`).
5. **The order switch wraps.** "By attention" breaks onto two lines at 340 px, at both 1600 and 1000 wide
   (`css:46-55`). Round 5 put the filter and the orders on one row; the build has three stacked rows plus a `? keys`
   line, 150 px of chrome before the first row.
6. **By project** headings are absolute paths (`/home/…/project`) at 12.5 px (`css:187`), and each row under a heading
   still repeats the directory in mauve (`css:177` hides `.proj`, not `.title-dir`).
7. **Archived** rows are a slug and a repository-hash and nothing else: no outcome, no date.
8. **Rows with no state glyph** (A6) leave a hole where the glyph column is; eight such rows were on screen.
9. **Contrast**: age and repository text are overlay0 at 11.5 px (`css:210`, `css:218`), 3.4:1 on base, 3.8:1 on
   crust.
10. The cursor row is a surface0 fill, a mauve ring and a mauve rail outside the row (`css:248-252`): three signals
    for one fact, and the same mauve rail means "you said this" in the reader.
11. Round 5's hide-on-open was not observed: after opening a row the inbox stayed docked. (*Unverified* whether this
    is a remembered pin; the flyout and pinned shots were identical.)

### B2. Title row and meter strip
1. **The title is 12 px** (`css:669-671`), the same size as a tool line and the pill. Round 5 specified 15 bold. The
   dock uses 17 (`css:354`), the phase page 17 (`css:842`), so three pages have three title sizes.
2. **State is drawn twice**: the bold word (`titlerow.tsx:81`, fed by `reader.tsx:370`) and the pill right after it
   (`app.tsx:1652-1659`). Live: "waiting" then a "waiting" pill.
3. **The strip is ragged.** Each stat is a 104 px grid with the bar under label and value (`css:684-688`). A bar at
   6 % is a short dash under the label only, so it reads as an underline on the label; a two-word label overflows its cell
   and collides with its value.
4. **The notice stack covers the title row.** It is absolutely placed at the top right of the panes (`css:77`), over
   the pill and the epic's Talk / Pause / Stop.
5. At 1000 wide, and in each pane of a split, the row wraps into four or five lines: back alone, then crumbs, then
   state and pill, then the strip on two lines (`css:668` is one wrapping flex row).
6. The blocker is drawn in the title row (`epicpage.tsx:140`) and again as the now block's first line
   (`blocker.tsx:222`), both in full, including a 40-character hash from the engine's sentence.

### B3. Conversation reader
The reply text itself is fine: 14/1.6, full-brightness, flush left (`css:392`). Everything around it competes with it.

| item | today | problem |
|---|---|---|
| YOU | surface0, mauve rail, 3/10 radius (`css:419`) | Fine. |
| Reply | plain (`css:392`) | Inline code is surface0 chips (`css:411`) at the same brightness as the cursor row and YOU. |
| Tool call | hairline line (`css:435`) | A second look for runs (below). |
| Tool run | mantle box, green dot per call (`css:466-478`) | Success is the loudest thing in the transcript: a row of green dots on every run. |
| Running call | yellow 2 px rail + "running" (`css:441`) | Rail thinner than every other rail (3 px). |
| Thinking | button line (`css:445`) | No look of its own. |
| Question call line | peach 12 px text (`css:443`) | Duplicates the question card that follows it. |
| Question card | peach tint and ring, radius 14, whole question bold 15 px (`css:448-449`) | A paragraph of bold is the hardest text on the page to read. No key caps, no "recommended" mark, unlike the epic form (A5). |
| Notice | 13.5 px overlay1 with a caps kind (`css:460-462`) | A size used nowhere else. |
| Compaction | rule, 11.5 px overlay0 (`css:424`) | 3.4:1 contrast. |
| New since you looked | lavender rule (`css:427`) | Fine. |
| Interrupted by you | maroon rule (`css:429`) | Fine. |
| Agent finished | a rule whose text wraps (`css:432`) | A rule that wraps is a paragraph with a line after it. |
| EVENT prompt | mantle, teal rail (`css:422`) | One of seven block looks (A5). |
| Step-end card | mantle, teal rail; head reads `end done status ok reason …` (`eventring.tsx:175-177`) | Field names are printed as words. Nothing says whether the end was good. |
| Epic event line | teal-bordered pill, round icon (`css:528-547`) | The only pill-shaped row in the app. |
| Epic card | mantle, mauve rail (`css:495`) | Mauve says "you". |
| Check card | mantle, green/red rail, chips (`css:480-491`) | Fine; the chip is 11.5/18, another one-off. |
| Waiting cards | the same cards at `opacity: .55` (`css:561`) | Opacity dims 12 px overlay1 text to about 2:1. They sit in the transcript after live text (`reader.tsx:315-322`) though they have not happened yet. |
| Background task rows | a line per task after the waiting cards (`css:566-571`) | Same: live state drawn as if it were history, and out of sight when scrolled up. |
| Sending | YOU at `opacity: .55` (`css:391`) | Same contrast loss. |
| Find marks | yellow 30 % / solid (`css:594-595`) | Fine. |

Other reader problems:
- The gutter time is overlay0 11 px (`css:386`).
- The transcript's end is a pile in a fixed order of question, live, waiting, background, sending
  (`reader.tsx:315-322`) with nothing separating "happened" from "pending".
- Interrupt has no visible control and is not in the key list (`app.tsx:202-229`); it is Esc in the composer
  (`app.tsx:851-884`). Android has a button (`SessionControls.kt:163`).

### B4. Composer and queue
- The queue docks onto the composer and reads as one box (`css:577-584`): this works and is worth keeping.
- State is a 2 px inner left edge (`css:604-605`), thinner than the reader's rails, and it disappears under the focus
  ring's colour change.
- The textarea is 15.5 px (`css:609`): the largest running text in the app is the box you type in.
- The send button is 24 px (`css:615`).
- The step box (`css:627-649`) has its own Interrupt, More and menu button looks, none shared with `button.act`.

### B5. Epic page
Live, it is a column of unrelated fragments with no measure:
1. The summary is one paragraph the full width of the track (about 1,200 px, 150 characters a line) in a mantle
   button (`css:745`).
2. Section heads are 10.5 px caps (`css:664`); a section not reached is the same head at 45 % opacity (`css:793`), so
   an idle epic is five grey words down the left edge. "Now" and "Next" draw their heads with nothing under them
   (`happenings.tsx:73`).
3. **The orrery is unreadable past about a dozen tasks.** Requirement labels overlap each other and are clipped by the
   svg's edge; the five ring names are drawn over the rings at 12 o'clock (`css:763`, 8.5 px); the centre count (34 px,
   `css:764`) overlaps its "integrated" caption. With no tasks it draws `0 of 0` in 400 px of empty space.
4. Proof cards are three-column text grids with no card (`css:794-800`); evidence runs are mantle boxes inside them
   (`css:811`). A red run that correctly failed reads `red captured ended fails passed exit 0` with "passed" in green:
   five status words in a row.
5. The now block stacks the blocker sentence, Retry and Stop… as separate full-width rows (`css:717`), and the blocker
   repeats the title row (B2.6).
6. The page beside a docked talk has no minimum: the chart recentres and the labels clip further.

### B6. Phase output and worker session
*Unverified live* (no running step in the owner's data during the audit). From the sheet: phase rows are mantle with
an inset ring (`css:846`), rounds are mantle (`css:875`), findings are a surface1 2 px rail (`css:896`): a fourth rail
width and colour. The task title is 17 px (`css:842`).

### B7. Notices, key list, picker
- A notice's title drew a session id hash, and its body drew raw Markdown asterisks (`notices.tsx:159-163`), against
  the standing "no ids or hashes".
- The key list is 30 rows of equal weight with no grouping (`app.tsx:202-229`, `css:331-340`).
- The picker: A4.

### B8. Phone width (under 760)
- `.panes` is a one-column grid and every column stays in the flow (`css:64-70`); the 760 media block is where the
  app becomes an app (`css:102`). Opening a row scrolls nothing and navigates nowhere: the conversation is under the
  whole list. One page measured 412 × 124,722 px.
- The epic page is below that again.
- The title row and strip wrap as in B2.5.

## C. Android

Read from source; not run.

### C1. Two visual systems in one app
- Hand-set Mocha: `PaneHeader.kt`, `HostMeters.kt`, `ConversationView.kt`, `ComposeBox.kt`, `QuestionCard.kt`,
  `StatusLine.kt`, `GoalLine.kt`, `Orrery.kt`.
- Material defaults with no Mocha reference at all: `ContractScreen.kt` (31 typography uses), `EpicHandover.kt` (25),
  `PhaseOutputScreen.kt` (24), `PoolsScreen.kt` (20), `EvidenceRuns.kt` (18), `WorkflowNodes.kt` (17),
  `EpicProof.kt` (16), `EpicScreen.kt`, `EpicBlocker.kt`, and the whole list (`PaneListScreen.kt:507-520`,
  `EpicsGroup.kt:78-268`).
- So a session row's title is `bodyLarge` 16 sp and the conversation it opens is 14 sp; a section head is
  `titleSmall` 14 sp with no caps, where the web's is 10.5 px caps.
- `MochaColorScheme` sets Material `surface` to Surface0 (`Theme.kt:10`), so any Material surface is a raised grey.
- Step output uses the system monospace, not the app's font (`EpicHappenings.kt:150`, `154`).

### C2. The conversation is behind the web
`foldConversation` (`ConversationModel.kt:45-67`) knows prompt, reply, tool, question, result, notice and compaction.
- `interrupted` and `agent_finished` items fall through the inner `when` and draw nothing.
- A prompt's sender is not read (`ConversationModel.kt:56-58`): a step-end event and an epic event draw as a **YOU**
  block holding the raw `<orrery-event …>` text.
- No step-end card, no waiting cards for held events, no background-task rows, no epic card, no "interrupting" state.
- Queued messages draw in the transcript as dimmed YOU blocks (`ConversationView.kt:362`), where the web docks them on
  the composer. Round 4's Q6 says the phone matches; it does not.

### C3. Same thing, different on each client
| thing | web | Android |
|---|---|---|
| Session title | directory in mauve + summary (`titleparts.tsx:31-43`) | `pane.label` or the command, bold 15.5 sp; the directory is on a second line with the opening prompt (`PaneHeader.kt:85-89`, `127-133`) |
| List grouping | attention / project / archived (`app.tsx:1466-1475`) | All (a folder tree) / New / Waiting (`PaneListScreen.kt:62-66`); no archive |
| List header | mark, "orrery", Ask | "Panes" at 22 sp with New, Pools, Settings as text buttons (`PaneListScreen.kt:212-229`) |
| Unseen | lavender dot, bold title (`css:193`, `css:209`) | mauve dot (`PaneListScreen.kt:500`) |
| Row state | glyph | coloured word at the right (`PaneListScreen.kt:515-518`) |
| Tool row | 12 px on a hairline, regular blue name | mantle box, radius 8, bold blue name, 12.5 sp (`ConversationView.kt:433-466`) |
| Tool result | inline under the line | crust box, unwrapped, pans sideways (`ConversationView.kt:677`) |
| New rule | left-labelled "since you looked" | centred "new" between two lines, plus a 6 % lavender tint on every later row (`ConversationView.kt:80`, `296`) |
| Time | `HH:MM` in a left gutter | centred row before each prompt and reply, relative under an hour (`ConversationView.kt:282`) |
| Question card | no label, peach tint 8 %, ring 40 %, radius 14 | "CLAUDE IS ASKING", tint 6 %, solid peach border, radius 14 (`QuestionCard.kt:96-108`) |
| Epic question | option cards, recommended, confirm | free text only (`EpicBlocker.kt:80-105`) |
| Composer | arrow button 24 px, radius 10, state as a 2 px left edge | "Send" text button, radius 14, state as the whole outline (`ComposeBox.kt:146-148`, `257`) |
| Context | five squares + percent in the strip | `▰▱` text glyphs + percent in the title line (`PaneHeader.kt:154`) |
| Host meters | label beside value, lower case, 11 px | label over value, upper case 9 sp (`HostMeters.kt:46-47`) |
| Blocker text | peach (`css:673`) | error red (`TitleRow.kt:99`, `EpicBlocker.kt:56`) |
| Working word | "working" | "working", or "background" in lavender for a background wait (`StatusLine.kt:89-91`) |
| Header fold | — | 180 ms raw tween (`PaneHeader.kt:38`), not a `Move` |

What already agrees and should be kept as the base: the motion tokens (`Motion.kt:15-21` and `css:11-13`), the meter
level rule (`HostMeters.kt:82-86` and `css:325-327`), the context band thresholds (`PaneHeader.kt:148-153` and
`strip.tsx:64-75`), the YOU block (`ConversationView.kt:327` and `css:419`), the wedge tone colours (`Orrery.kt:221-227`
and `css:773-777`).

### C4. Only on one client
- Android only: the goal line (`GoalLine.kt:44`; the field is in the proto, the web draws nothing), quick keys
  (`QuickKeys.kt:40`), visible Interrupt / Stop / Resume (`SessionControls.kt:163`), Pools, Settings.
- Web only: find, the key list, split, the inbox flyout, the Talk dock, archive, every item in C2.

## D. Legibility numbers

Contrast of each grey on each ground (WCAG ratio; 4.5 is the floor for text):

| ink | on crust | on mantle | on base | on surface0 | used as text colour in `style.css` |
|---|---|---|---|---|---|
| text | 13.0 | 12.1 | 11.3 | 8.7 | 34 rules |
| subtext1 | 10.6 | 9.9 | 9.3 | 7.1 | 19 |
| subtext0 | 8.4 | 7.9 | 7.4 | 5.7 | 35 |
| overlay2 | 6.6 | 6.2 | 5.8 | 4.5 | 0 (not in either palette) |
| overlay1 | 5.1 | 4.8 | **4.4** | **3.4** | 54 |
| overlay0 | **3.8** | **3.6** | **3.4** | **2.6** | 25 |

79 of the 167 text-colour rules that use these greys are below the floor on the page ground, and they are the 11 to 12.5 px ones. On
top of that, three rules dim with `opacity` (`css:391`, `css:561`, `css:793`).

Accents against base: yellow 12.9, green 11.0, teal 11.0, peach 9.3, lavender 9.2, mauve 8.1, blue 7.8, red 7.1. Body
text is 11.3. So a yellow word is brighter than the reply around it, a green or teal one is level with it, and a row
of green tool dots competes with a paragraph.

## E. Not audited

- Phase output page and worker session, live (B6): read from the sheet only.
- Handover, report, release proof with real content.
- The inbox flyout's hover behaviour and the pinned state (B1.11).
- Android at run time: touch targets, the header fold, the keyboard.
- Whether the "Needs data" items in round 5's DECISIONS.md have since landed.
