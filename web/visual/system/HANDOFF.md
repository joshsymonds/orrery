# Handoff: design system, checkpoint of 2026-11-17

This is a checkpoint, not a finished epic. The work stopped after the audit and the first draft of the language,
before anything was drawn.

## What is here

| file | state |
|---|---|
| `AUDIT.md` | First pass done for the web, from the live build at 1600, 1000 and 412 and from the source. Android is from source only. Section E lists what was not looked at. |
| `SYSTEM.md` | Draft. Parts 0 to 7 (tokens and rules) are written. Part 8 names every component and its states; anatomy is written for primitives, the shell rows, the title row and the conversation (marked [A]) and is a sketch for the epic pages, notices, palette, key list and talk dock (marked [S]). Part 9 lists what is missing. |
| `tokens.json` | Draft, complete in shape, and consistent with SYSTEM.md. Never run through a generator and never rendered. |
| `DECISIONS.md` | Draft. Ten decisions with what was rejected, ten open items with a recommendation each. |

## What is not here

- **Mockups: none.** No HTML page, no stylesheet, no screenshots, no `shots/` directory. Deliverable 4 has not been
  started.
- No generator and no generated CSS or Kotlin. The plan was a small script that turns `tokens.json` into
  `tokens.css`, with every mockup style reading only those variables, so the tokens are proven sufficient.
- No scenario list mapping stories to fixture data, and no mapping from today's class names and composables to the new
  component names.
- The epic pages' anatomy, Markdown inside a reply, and the phone's home page and header at the same detail as the
  desk's.

## Working hypotheses, none of them seen on screen

1. **The cause is the missing system more than round 5's drawing.** Round 5's conversation and epic page still look
   right beside the live build. The build drifted because nothing was named: 19 font sizes, 19 radii, 59 unstyled
   classes. So the language is mostly rationing, and only a few things move.
2. **Legibility is a contrast problem first.** Most small grey text is under 4.5:1, and success is drawn brighter than
   content. Four inks with overlay2 as the floor, and quiet success, should fix more than any layout change.
3. **Four shapes** (Block, Line, Rule, Ask) cover all sixteen conversation item kinds. Untested risk: the epic card is
   a large block and may not sit well as a variant of the same shape as a one-line event.
4. **The dock.** Running tasks, held events and the queue belong on the composer. Untested risk: height on a busy
   session, and whether a one-line arriving event says enough before it arrives.
5. **One Ask** at two densities (card and row) and two behaviours (one press for a session, pick then confirm for an
   epic). Untested risk: whether the row density fits long option labels at 340 wide.
6. **Five type sizes with rows at 14.** Untested: the inbox loses about two characters a row.
7. **The orrery needs its own pass.** Past about a dozen tasks its labels collide; the draft only says "no labels when
   dense, legend below". That is a rule, not a design.

## What I would do next, in order

1. Write the token generator and a stylesheet that uses only token variables.
2. Draw the conversation with every item kind at 1600 and 412, and check hypotheses 2 to 5 by eye.
3. Draw the inbox, then the epic page, then the component inventory page with one cell per `Component/State` name.
4. Correct SYSTEM.md and tokens.json from what the drawings show; finish the [S] anatomy.
5. Screenshots at 1600, 1000 and 412 under `shots/`.

## Names and the catalog

Component and state names in SYSTEM.md part 8 are written as `Component/State` so they can be story names in the
foundation epic's `*.stories.tsx` and targets for its `just shoot`. A state in italics is a flag that combines with
others; those will need a decision on which combinations get a story of their own.

## Scripts and live shots

Nothing under this directory was produced by a script. The scripts used for looking at the live build are outside the
repository and are not needed to read these files:

- `/tmp/orrery-design/tools/live.mjs`: a copy of `/tmp/orrery-look/shoot.mjs`.
- `/tmp/orrery-design/tools/live2.mjs`: the same with steps. Usage:
  `node live2.mjs <out-prefix> <width> <step>...` where a step is text to click, `key:K`, `css:SELECTOR`,
  `hover:SELECTOR`, `scroll:SELECTOR@PIXELS`, `wait:MS`, `shot:NAME`, `full:NAME` (whole page) or `outline:SELECTOR`
  (prints a count of each element and class under it). Env `H` is the height and `MOBILE=1` is a touch phone.
- Live screenshots are in `/tmp/orrery-design/live/`. They hold the owner's real data and are not to be committed.

Both scripts resolve `playwright-core` from `/home/joshsymonds/Personal/talks/node_modules/` and use the system
chromium. `/tmp` does not survive a reboot; the scripts are about thirty lines and are quick to rewrite from
`/tmp/orrery-look/shoot.mjs`.

Other figures in the audit came from one-line shell counts over `web/src/style.css` (distinct `font:` shorthands,
sizes, radii, paddings, gaps, text colours) and a small Node calculation of WCAG contrast for each Mocha grey on each
surface.
