# orrery flows (round 4, Nov 14 2026)

Seven pages, one frame. Every page has the same title row: a back button, a breadcrumb
(`orrery › rota-export › export-cli · checking`), the state with how long it has held, and the
commands as buttons. Back always goes to the crumb on the left. The web shows the list beside
every page; the phone shows one page at a time. Nothing else differs.

| page | what it answers |
|---|---|
| List (web) / Home (phone) | What is happening everywhere, and what needs me? |
| Epic view | What is this epic doing, for how long, and what is it blocked on? |
| Contract | What did I agree to (or what am I about to agree to)? |
| Phase output | Is this worker doing what was planned? Red test, gate, judge, review. |
| Worker session | What exactly did the worker do and say? |
| Talk | A conversation with a session that has this epic in context. |
| Session | An ordinary conversation that belongs to no epic. |

## 1. Seeing all epics

Question: what is happening, and does any of it need me?

- Starts: web desk (the list is always on the left); phone idle check (Home is the first screen).
- List / Home. Needs you first, each item a sentence with its action. Then one line per epic: its
  small orrery, `3 of 6 proven`, its state and how long (`Building 49 min · 2 of 7 units
  integrated`), and its blocker in colour (`tz-fixtures waits on your decision · 2 min`). Then
  Just ended, then sessions.
- The owner learns everything for this workflow without opening anything. The counts in the list's
  top row jump to their group.
- Back: there is nowhere to come back from. On the phone, `‹` on any page ends here.

## 2. Guiding an epic

Question: I want this epic to do something else, or I want to know why it did something.

There is no Director object. Three kinds of guiding, each with its own control:

- A command is a button in the epic view's title row: Pause, Resume, Stop… (asks first). Retry is
  a button on the thing that failed (a release step, a task that ended short).
- A question is a form at the top of the epic view: options, one recommended, the line that will
  be recorded, one confirm button. The same question sits in Needs you; the answer is the owner's
  word wherever it is given.
- Words go to a session. `Talk` in the title row opens a short menu: the sessions that touched this
  epic lately (`rota-planning · created the epic · 13:50`), and `New session in ~/code/lighthouse`.
  - Web: the conversation docks to the right of the epic view. The epic view stays, so a pause or
    a retry the session makes shows up beside the words that asked for it. `×` closes the dock.
  - Phone: the conversation is the next screen, `orrery › rota-export › talk · rota-planning`, with
    the epic's list line pinned at the top. Tapping that line, or `‹`, returns to the epic view.
- The session is an ordinary session. It is also in the list, under its own name.

## 3. What an epic is doing

Question: what is it doing, since when, and what is it blocked on?

- Starts: click an epic line (web), tap it (phone), or a notification.
- Epic view, the only epic page. Top to bottom:
  1. Title row: `orrery › rota-export`, `building · 49 min`, `waits on your decision · 2 min`, Talk, Pause, Stop….
  2. The summary paragraph, generated once from the contract. It is the door to the contract.
  3. Now: one live sentence. Under it, what blocks the epic, as a form or as buttons.
  4. Red proof: one card per requirement, its test failing at the base commit.
  5. Implementation: the orrery. A task is a wedge; a phase is a slice of it. The slice being
     worked on glows. Under it: the tasks in flight with their time in state, what is next, and
     what just happened (`+9 earlier` opens the rest in place).
  6. Green proof: the same cards, passing, then the epic review.
  7. Release proof: orrery's own steps, each with its evidence and its undo.
  8. Report.
- A section the epic has not reached is there, dim, showing what it will hold. An ended epic has
  the same page with the Report filled in.
- Back: `‹` or the `orrery` crumb.

## 4. Drilling into workers and sessions

Question: is this worker doing what was planned?

- Starts: the epic view. Hovering a slice (web) shows that phase's summary: what it did or is
  doing, how long, the outcome. Clicking or tapping it opens the phase output.
- Phase output, `orrery › rota-export › export-cli · checking`. One page per task: its four phases
  in order, the chosen one open, the others one line each (tap to open instead).
  - implementing: the red test and its failing output, each round, what it is doing now, the green run.
  - checking: the five engine checks, the gate's output, the done-judge's verdict and reason.
  - reviewing: the reviewer's findings, each with its file and what happened to it.
  - integrating: the merge onto the epic branch.
  - Hold, Resume and Stop… are buttons in the title row.
- Worker session, `… › export-cli · implementing › session`. `Open the session` on a phase that a
  session ran (implementer, reviewer). The conversation, with return cards; the send box nudges.
- Back: `‹` goes up one crumb each time; any crumb jumps straight there.

## Cross-links

- Where a contract lives after acceptance. In the epic view's summary paragraph. `Read and accept`
  opens `orrery › tide-alerts › contract`; Accept returns to the epic view, where the paragraph now
  reads `Contract · accepted 14:41` and opens the same page, read-only.
- How a slice leads to its phase output. Every slice, every proof card and every task row carries
  the same link: task and phase. They all open the phase output with that phase open.
- How a notification lands. On the page whose top block is the thing it is about:
  - Decision or question from an epic: the epic view; the form is the first block.
  - A release step failed: the epic view; Retry and Stop… are the first block.
  - Contract ready: the contract page; `‹` goes to its epic view.
  - A session asks: that session.
  - Released, ended with gaps, stopped (quiet): the epic view, scrolled to the Report.
  - Steps never notify.
- How the owner returns from a step's conversation. `‹` to the phase output, `‹` again to the
  epic view; or the epic's crumb in one click.
- How a just-ended epic stays reachable. It moves to Just ended and keeps the cursor. Once seen
  and left it folds into `2 ended earlier ›` in the same place. Its page is the epic view.

## Fewer screens than round 3

- Summary and Detail are one page. The tabs are gone: Tasks became the orrery's slices and the
  rows under it, Timeline is `Just happened` opened in place, Evidence and Requirements are the
  red and green proof.
- The decision page and the release page are blocks at the top of the epic view.
- The right-hand epic column is gone. Its content is under the orrery.
- The stage breadcrumb is gone. The page's own order, top to bottom, is the epic's order.
- The send box under an epic is gone. Words go to a session, through Talk.

## Needs data (hidden behind the wireframe bar's `needs-data tags` switch)

| field | used for |
|---|---|
| `Epic.summary` | The paragraph at the top of the epic view. Generated once from the accepted contract. |
| `Epic.phase_started` | `building · 49 min` in the title row and on the list line. `Epic.changed` is the last write, not the start of the phase. |
| `EpicsGetResponse.questions` | The form at the top: the open question, its options, the recommendation, when it was raised. `epic_answer` exists; nothing lists what is open. |
| `EpicsGetResponse.contract`, `accepted_at` | The contract page after acceptance. The store has every row; no call returns them. |
| `EpicRequirement.red`, `green` | Red and green proof: per requirement, the test, its output at the base commit and at the task's commit. Today `EpicSlice` has two booleans per task. |
| `EpicStep.stage`, `stage_started` | Which slice glows and for how long: turn, checks, gate, judge. |
| `EpicStep.gate_name`, `gate_limit` | `gate just check · 2:41 of 30 min`. |
| `StepGate.output` | The gate's last lines in the checking phase. |
| `EpicStep.verdicts` | The done-judge's verdict and reason, per round. |
| `EpicStepCheck.detail` | A failed check's own line. |
| `EpicStep.read_only`, `role` | Telling the reviewer's session from the implementer's. |
| `EpicsGetResponse.findings` | Review findings on a task and on the epic, with what happened to each. |
| `EpicStep.summary` | What an accepted step says it did. |
| `EpicStep.limited`, `limit_kind`, `limit_resets` | A step paused by a usage limit, and its Resume button. |
| `EpicTask.requirement` | Which requirement a task proves: the outer ring and the proof cards' links. |
| `Epic.next_actions` | `Next` under the orrery. |
| `Epics.History` (a new call) | `Just happened`, and the rest of it opened in place. |
| `Epic.release_steps` with state, evidence, undo | Release proof. |
| `Epic.report` | The Report section. |
| `Epic.sessions`, `Sessions.Start{cwd, epic}` | Talk: the sessions that called a tool on this epic, and a new session in its repository with it in context. |
| `Epics.Pause`, `Resume`, `Stop`, `Retry`, `Answer` | The buttons and the form. They exist as session tools (`epic_pause` …); the phone-facing service has only `Steps.Hold`, `Nudge`, `Stop`. |
| `Sessions.Resume` | Resume on an interrupted session. Today a message resumes it. |
