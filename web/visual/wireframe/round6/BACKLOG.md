# Round 6: the epics that build it

Round 6 (`DECISIONS.md`, `flows.md`, `../../system/SYSTEM.md`) is built as two tracks of epics, one epic at a time in
each. The server track changes what Orrery does; the frontend track changes what the clients draw. The tracks own
different files and run side by side.

Every decision in `DECISIONS.md` and every row of its "Needs the server" table is assigned below to exactly one epic, or
marked done or not planned. Each epic is judged against the round-6 scenarios it names.

## In flight first

| epic | why it comes first |
|---|---|
| `orrery-no-step-wait` (accepted, 11 of 14 tasks done) | It rebuilds caller-event delivery (one wake channel, no 5 s tick) and treats a step with running children like a session with a background task, so it stays busy. S1 and S2 change the same code; both start after it releases. |
| `orrery-web-catalog` (accepted, moving again) | Stories beside each component, fixtures out of `world.ts`, `just shoot` on the catalog. Every frontend epic builds on it. **Its contract names round 5 the reference and deletes `map.json`; round 6 must become the reference instead** (F0). |
| `orrery-task-spec` (accepted) | No overlap with round 6. |

## Server track

### S1. Events reach the model, and the dock tells the truth

Held events reach a session as soon as no turn runs and no request is open. The dock's words about delivery are true.

- `claimFree` stops counting a running background task as busy for delivery. The idle check keeps counting it (a
  session with a dev server running is not idle), and so does `orrery-no-step-wait`'s rule for a step with running
  children: "busy for the idle check" and "free to take an event" become two questions.
- `waits` (`internal/server/sessionwatch.go:212-260`) is right after the owner has spoken on a headless session: it
  compares against owner prompts, which headless Says never carry.
- The stream says why each queued message is held and which turn it waits behind, and whether a Say was written or
  held.
- `interrupting` carries the time it started.

Rows: "Events not held by a background task", "1 event waits until you speak", "the first is sent when this turn
ends", "Interrupting, with a clock". Code: `internal/enginehost/manager.go` (`claimFree`, `busy`, `checkIdle`),
`internal/server/sessionwatch.go`, `internal/planner/callerevents.go`. Scenarios: `dock-interrupted`, `dock-queued`,
`dock-interrupting`. After `orrery-no-step-wait`.

### S2. One conversation per epic

An epic is steered from exactly one conversation, always (revision 6).

- `Epics.List` carries each epic's steering conversation (always set), its path, and what the inbox row draws without
  a `Get`: paused and why (by you or a usage limit), parked, open question count.
- A move call (epic, to conversation, by whom) and a tool for a conversation to take an epic over (`epic_steer`); a
  move is an entry on the epic's log, and caller events go to the new conversation from then on.
- An epic created from a shell or the CLI gets a conversation made for it; "Continue in a new conversation" starts a
  session in the epic's repository with the contract and the epic's log and makes it the steering one.
- Each decision on the epic's log names the conversation that made it. Actions from the epic's page are the owner's,
  logged, and told to its conversation as an event.
- Moves stay inside a project.

Rows: "An epic under its conversation", "Moving an epic", "Continue in a new conversation", "A blocker and a count on
an epic's row", "paused by you against a limit pause", "The Work beside a conversation". Code:
`proto/orrery/v1/epics.proto` (`Epic`, `EpicSession`, `EpicDecision`), `internal/store/epicsessions.go`,
`internal/stepmcp/epictools.go`, `internal/enginehost/epiccalls.go`, `internal/planner/callerevents.go`,
`cmd/orrery/epic.go`. Scenarios: `epic-handover`, `epic-moved`, `epic-convo-ended`, `convo-took-over`,
`convo-handed-over`. After `orrery-no-step-wait`.

### S3. A contract is a record

A contract is kept from the moment it is proposed (revision 5): versions with their time, writer and what changed; a
state (proposed, accepted, set aside); accept, set aside and reopen calls; `epic_create` takes an accepted contract. The
Director's brainstorm proposes and revises through the record instead of only in its conversation, so it is two
repositories: Orrery's store, protos and tools, and Gambit's `skills/brainstorming`.

Rows: "A contract as a record…", "A contract Block in a conversation", "A contract under its conversation". Scenarios:
`contract-to-accept`, `contract-versions`, `contract-set-aside`, `contract-ready`, `convo-contract`. After S2 (the
contract carries its conversation).

### S4. The owner's epic controls are public

`ReleaseEpic` and `CloseEpic` exist on the engine (`internal/enginehost/epicservice.go:527`, `:562`) and the CLI; the
public `Epics` service passes them through, so a release that stopped at a step, or an epic that ended with gaps, can
be finished from the clients.

Rows: "Release from step 2; Release it anyway; Close as…". Scenarios: `epic-release-failed`, `epic-ended-gaps`. Can
start now.

### S5. Not now and Decline

Revision 13. Not now is a per-question mark kept on the server, keyed to the question (a pane's question is already
hashed), so every client takes it off your list; nothing is sent to the session, and anything new it asks comes back.
Decline denies the request and ends the turn, as one call for a headless session and a pane alike; a pane declined with
Escape stops reading as `waiting`.

Rows: "Not now on an Ask", "Decline on an Ask". Open in it: whether archiving a session should stop it un-archiving at
its next state change ("Archive on a stale Ask"). Code: `proto/orrery/v1/orrery.proto` (`Answer`),
`internal/server/panes.go`. Scenarios: `ask-later`, `ask-decline`, `ask-stale`. Can start now.

### S6. Facts the clients draw

Small additions the drawings need, none of them a behaviour change:

- `Notices` lists what has passed, by day, with read and unread ("Notices by day").
- A pending `step_end` carries its rounds, duration and changed lines ("A step end's rounds…").
- A notification's actions are its options by name ("A notification's actions by option name").
- `Sessions.ListHeadless` says each session's role, so steps and Talk sessions stay out of the tree ("Steps and Talk
  sessions kept out of the directory tree").

Scenarios: `notices-centre`, `convo-epic` (step-end block), `android-notifications`. After S1.

### Not planned, or done

| row | why |
|---|---|
| "Open in a new window" | Done: `orrery-web-routes` gave every page an address. |
| "A project's page" | Composed by the client from the lists it already reads, filtered by directory; no call needed. |
| "Red, implement, green, review as a task's place" | Client-only: `orchestrate` folds into implement and `verify` into review (F4). |
| "A limit wait drawn grey" | Client-only: a `limited` tone apart from the owner hold (F4). |
| "A session's meters on another host" | Done: `Host.Status`'s per-host list is served, and the Android hosts view draws it. |

## Frontend track

The web at the desk and under 760 px are one codebase and are built together; Android follows.

### F0. Round 6 is the yardstick

The catalog's stories are round 6's `Component/State` names (`catalog.html`), and its fixtures are round 6's world, so
a frontend epic is judged by its scenarios. Best done as an amendment to `orrery-web-catalog`, which today names round 5
the reference; otherwise straight after it.

### F1. The language

`tokens.json` generates the web's tokens (`gen-tokens.mjs` into `web/src`) and a lint fails `style.css` on any raw
colour, length, duration, font size or opacity, as `lint-css.mjs` does for the wireframe. The type scale (12, 13, 15, 18,
28), Catppuccin Mocha roles and series, rails and radii are applied to every existing component, with nothing moved.
Decisions 10, 11. Scenarios: `catalog.html` throughout.

### F2. The inbox and the left rail

The tree by project, then conversation, each epic and contract hanging under the conversation that steers it (decision
1, revision 6); project cards; the two chips; search and archived; the left rail of project tiles with the Orrery mark
and the flyout; the project page. Uses S2's steering conversation; until it lands, the creator. Scenarios: `inbox-*`,
`project`.

### F3. The conversation

Blocks, lines, rules and the Ask (decisions 2, 6; revision 13, Not now and Decline from S5); the dock as state, queue
and input, with the three interrupt wordings and the waits line (decisions 4, 5; revision 10, from S1); Not now and Decline land here completely: the controls on the question, the count, the peach and the band skipping a put-off question on the web and on Android, and Android leaving archived rows out of what waits on you; the band as the
goal and what asks (revision 11); the title row and meters. Scenarios: `convo-*`, `dock-*`, `ask-*`.

### F4. The right rail and its panels

The rail in named groups, dots past three and the picker (revisions 8, 9); a panel per thing, one at a time at one
width, the lit tile; the epic's record (decision 7, ring names on the rings, the track, now, tasks, requirements,
review, release, report), the task's, and the contract's with its versions (S3); the handover menu and the ended
conversation's bar (S2). Scenarios: `work-rail*`, `epic-*`, `contract-*`, `phase-*`, `worker-*`, `dock-running`,
`dock-background`.

### F5. Notices and New

Notices two lines with an action, and the notice centre by day (decision 8; S6); New as one field whose results say
what choosing does (decision 9); the key list. Scenarios: `notices-*`, `new-*`, `keys`.

### F6. Android

The same language and screens in Compose: a Kotlin token generator from `tokens.json`, then F2 to F5 on the phone
(revision 12's folding header, the tile strip and its sheets, notifications with named actions). Code:
`android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/`. Scenarios: every scenario's phone drawing and
`android-*`. It can run as a third track once F1 lands, since it shares no files with the web.

## Order

| | server | frontend |
|---|---|---|
| now | S5, then S4 | `orrery-web-catalog`, with F0 amended in |
| after `orrery-no-step-wait` | S1, then S2 | F1 |
| then | S3, S6 | F2, F3, F4, F5; F6 from F1 on |

## The epics as contracted (2026-11-18)

Every section above is written as one or more accepted contracts under `~/.gambit/orrery-0668c87/<slug>/` (Gambit's
side of S3 under `~/.gambit/gambit-a6d4d02/`), split where a contract ran past forty lines. They run one at a time per
track, in this order, each after the epics it names; `~/.gambit/orrery-0668c87/watch/epics.json` is the queue, and the
`orrery-round6-watch` timer creates each when its turn comes.

| | server | frontend |
|---|---|---|
| S5 | `orrery-not-now-2`, `orrery-decline` | |
| S4 | `orrery-release-public` | |
| S1 | `orrery-events-arrive`, `orrery-turn-facts` | |
| S2 | `orrery-one-steerer`, `orrery-steer-move` | |
| S6 | `orrery-client-facts`, `orrery-step-end-facts` | |
| S3 | `orrery-contract-record`, `orrery-contract-epic`, then by hand `gambit-contract-record` | |
| F0 | | `orrery-round6-yardstick` (after `orrery-web-catalog`, and design/system merged into main) |
| F1 | | `orrery-design-language`, with the Kotlin token generator |
| F2 | | `orrery-inbox-tree`, `orrery-inbox-rail` (with the web's archive toast) |
| F5 | | `orrery-new-keys`, `orrery-notices` |
| F3 | | `orrery-conversation-ask`, `orrery-conversation-dock`, `orrery-conversation-head`, `orrery-conversation` |
| F4 | | `orrery-right-rail`, `orrery-epic-record`, `orrery-epic-steering`, `orrery-task-record`, `orrery-epic-ending`, `orrery-contract-panel` |
| F6 | | `orrery-android-only`, `orrery-android-touch` (quick keys, the keyboard fold and the toast on Android) |

Not drawn by any epic: Drop on a park (no public call).
