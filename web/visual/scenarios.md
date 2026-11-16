# Orrery web parity scenarios: fixture specification

Frozen reference data. A fixture server implements section 1 (global rules) and section 2 (the one shared world);
section 3 maps each scenario to the checker's navigation; section 4 gives each scenario its wireframe source, the
answers it uses, the expected live render and its gaps.

Sources read: wireframe `/home/joshsymonds/Personal/pane-tree-wireframe/webview/{epicnow.js,app.js,index.html}`;
live client `/home/joshsymonds/Personal/orrery/web/src/*.ts(x)`; generated types
`web/src/gen/orrery/v1/{epics_pb,orrery_pb,notice_pb}.ts`; server guard `internal/server/server.go`.
Field names below are the generated TypeScript names, which are also the proto3-JSON names on the wire.

---

## 1. Global rules

### 1.1 Epoch, timezone, clock

| item | value |
|---|---|
| fixed instant | `2026-11-14T14:41:00Z` = unix `1794667260` |
| notation `@HH:MM[:SS]` | unix seconds of that time on 2026-11-14 UTC: `1794614400 + 3600·HH + 60·MM + SS` (e.g. `@13:52` = 1794664320, `@14:39` = 1794667140) |
| notation `@-Nd HH:MM` | the same time N days earlier (e.g. `@-2d 14:41` = 1794494460) |
| live browser | `timezoneId: 'UTC'`, `locale: 'en-US'`, `page.clock.install({ time: new Date('2026-11-14T14:41:00Z') })` before load. The client reads `Date.now()` for `now` (main.tsx:132,151,226), list ages (app.tsx `ageText`), pill clocks (workline.tsx), and the Talk dock's since-you-looked mark (talkdock.tsx:40), so the clock must be frozen there too. |
| wireframe browser | `timezoneId: 'UTC'`, `page.clock.install({ time: new Date('2026-11-14T14:41:00') })` (local = UTC), so wf clocks read the same as the live ones. |
| after each navigation | `clock.runFor(500)`, then capture. The poll interval is 5000 ms (main.tsx:16) and does not fire. |
| clock text | live `clock()` (reader.tsx:592) prints `HH:MM` h23 in the browser zone, so `@13:50` reads `13:50`. |

### 1.2 Transport and headers

| rule | detail |
|---|---|
| protocol | Connect, JSON codec (connect-web default). Paths `/orrery.v1.<Service>/<Method>`, e.g. `/orrery.v1.Epics/Get`. |
| unary | `POST`, request and response `application/json`. Errors: Connect JSON error body with HTTP status. |
| server streaming | response `application/connect+json`; each message is an envelope (flag byte `0x00`, 4-byte big-endian length, JSON). End-of-stream envelope is flag `0x02` with `{}`. |
| JSON values | int64/uint64 as decimal strings (`"1794667260"`); bools/strings at default may be omitted; `optional` fields (HostStat.percent, SessionChanged.*) omitted when unset; oneofs as the case name key (`{"time":"…","cursor":"…","tell":{…}}`). |
| `X-Orrery-Client` | The client sends `X-Orrery-Client: orrery web` on every call (client.ts:8-15). The real guard requires it non-empty on every Connect path (server.go:938-948 `guardAdmits`). The fixture accepts any non-empty value and may reject a missing one with 403. |
| `Tailscale-User-Login` | The real server answers 403 unless this header names a login in `--allow-login` (server.go:298-301). The browser does not send it by itself. The fixture accepts requests with or without it; a checker that wants the real-server shape sets `extraHTTPHeaders: { 'Tailscale-User-Login': 'fixture@example.invalid' }`. |
| content types | Accept `application/json`, `application/proto`, `application/connect+json`, `application/connect+proto` (server.go:950 `isConnectContentType`). Only the JSON pair is used by this client. |
| harness seam | Either serve the built web bundle (`scripts/build-web.sh` output) from the fixture origin, or mount a harness page without `#app` that calls `start(el, createClients(createTransport({ fetch: fixtureFetch })))` (main.tsx:68-73, client.ts:18-30, main.tsx:262-265). main.test.tsx:161 `fakeServer` is the pattern. |

### 1.3 Calls the fixture answers

| call | kind | answer |
|---|---|---|
| Panes/List | unary | §2.6 |
| Sessions/List | unary | `{ "sessions": [], "seq": "0" }`. seq 0 keeps the client from opening Events/Watch (main.tsx:152-157). |
| Sessions/ListHeadless | unary | §2.7 |
| Epics/List | unary | §2.2 |
| Epics/Get `{id}` | unary | §2.3-2.5 by id; unknown id → Connect `not_found` |
| Epics/History `{id, after, limit}` | unary | §2.8 by id, all entries in one page, `next: ""`; other ids → `{ "entries": [], "next": "" }` |
| Host/Status | unary | §2.9 |
| Evidence/Artifact `{run, name, stderr}` | unary | `{ "data": base64(<log text in §2.3 run table>), "mediaType": "text/plain" }`; only called when a stdout/stderr toggle is opened. |
| Panes/WatchConversation `{pane, transcript, after, limit}` | stream | one message (`reset: true`, full `items`, `state`, `unfinished`) from §2.10, then hold the stream open without ending it. A reconnect (non-empty `transcript` or `after > 0`) gets nothing and stays open. |
| Sessions/WatchConversation `{id, limit, after}` | stream | same rule, §2.10 |
| Notices/Watch | stream | hold open, send nothing (no banner) |
| Events/Watch | stream | not called while seq is "0"; if called, hold open, send nothing |

### 1.4 Calls absorbed silently (HTTP 200, body `{}` unless stated)

Presence/Report, Control/Seen, Panes/MarkSeen, Epics/Seen, Notices/Seen, every `/orrery.v1.Control/*`
(Control/Start answers `{ "id": "fixture-new-session" }`), every `/orrery.v1.Steps/*` (Hold, Nudge, Stop),
Epics/Pause, Epics/Resume (`{ "resumed": [], "stillLimited": [] }`), Epics/Stop, Epics/Retry, Epics/Answer
(`{ "warning": "" }`), Panes/SendText, Panes/SendKeys, Panes/PullQueued (`{ "text": "" }`).
Seen writes landing successfully make the client read the row or ending as seen (app.tsx `seenWritten`); the seen watch
writes only after a 1000 ms dwell following user input (seen.ts:18-19), so capture within 1 s of the last click.

---

## 2. The shared world (one dataset for every scenario)

The wireframe state is `state.moment = { followups: 2 }` (epicnow.js:599) and every other epic at moment 0. The
fixture serves that instant. Times in the wireframe's orrery-followups data are 10:xx; the fixture shifts that epic
by +4 h so its line reads the same durations at 14:41.

### 2.1 Ids

| wf key | Epic.id | slug | Epic.repository | EpicsGetResponse.repository |
|---|---|---|---|---|
| followups | `orrery/orrery-followups` | orrery-followups | orrery | `/home/owner/Personal/orrery` |
| rota | `lighthouse/rota-export` | rota-export | lighthouse | `/home/owner/code/lighthouse` |
| tidealerts | `tidepool/tide-alerts` | tide-alerts | tidepool | `/home/owner/code/tidepool` |
| offline | `tidepool/offline-tables` | offline-tables | tidepool | `/home/owner/code/tidepool` |
| orbitlabels | `orrery/orbit-labels` | orbit-labels | orrery | `/home/owner/Personal/orrery` |
| holder | `orrery/holder-scopes` | holder-scopes | orrery | `/home/owner/Personal/orrery` |
| tidecache | `tidepool/tide-cache-drop-tables-older-than-a-week` | `tide-cache-drop-tables-older-than-a-week` (40 chars; wf `tide-cache`, lengthened on purpose to exercise list truncation) | tidepool | `/home/owner/code/tidepool` |

Build task ids follow the documented `T<seq>` form (EpicBuildTask.id). Rota's are numbered in the wireframe's wedge
order so the orrery draws its wedges in the same order as epicnow.js:336-361:

| rota task | wf unit slug | title (EpicBuildTask.title) | requirements |
|---|---|---|---|
| T1 | tz-fixtures | Fixtures cover every port zone. | R5 |
| T2 | export-cli | Export refuses to overwrite an existing file. | R4 |
| T3 | rota-handler | The rota endpoint serves one week of shifts. | R2 |
| T4 | rota-store | Shifts are stored per keeper and week. | R1 |
| T5 | csv-writer | A week exports as CSV, one row per shift. | R3 |
| T6 | export-docs | The README documents the export command and its audit line. | R6 |
| T7 | rota-audit | Every export writes one line to the audit log. | R6 |

Commit ids (40 hex, used wherever a revision or base is a full id):

| short | full |
|---|---|
| 3e1f2ab (rota base) | 3e1f2ab6b681479f7e6dc3c139194a1ad6b8025b |
| a41b7d0 | a41b7d0a994984ee328f27586ece2fe411cb3a73 |
| 3f9c2e1 | 3f9c2e1d1ebb1664a69ab497310da60951dcb4be |
| 7c1e9aa | 7c1e9aae44b1b9c7c44bf9fc9e373fe744a0778d |
| e02b6c4 | e02b6c4eb44d9510b3215d974e1e03e5ada58a06 |
| 0c7e4aa (holder base) | 0c7e4aa22f3584d2a720b837a5507c36476852fe |
| b91e1ed | b91e1edfa49dfc460d8c98ff0e6685a01712e19b |
| 984ef49 | 984ef49c3bff6d10661e3548cdb3e04724b8873c |
| 9cc2be1 | 9cc2be1e8289c469a9f34e48ecabac2bcd4516d3 |
| 9593e0b | 9593e0b35d9438987e69889b8d9899fbc42bcaaa |
| 71c0d3e (followups base) | 71c0d3e663dbbf39268b60a9bb92268ffcec0890 |
| 2010f6f | 2010f6f6eba5809911e61e1d207c754ed44199d9 |
| c2d90a4 (offline base) | c2d90a4aed3e04b3cd7271c1af72975dbda701b6 |
| 2f60d1c (orbit-labels base) | 2f60d1cc6a590e15127dc0ae150b0b8ab279a40a |

Panes and sessions:

| wf key | row source | id | host | label | cwd / dir |
|---|---|---|---|---|---|
| lamp | Pane | `%11` | halyard | lamp-telemetry | `/home/owner/code/lighthouse` |
| beacon | Pane | `%12` | halyard | beacon-schedule | `/home/owner/code/lighthouse` |
| graft | Pane | `%13` | sextant | graft-parser | `/home/owner/code/orchard` |
| rotaplan | Pane | `%14` | halyard | rota-planning | `/home/owner/code/lighthouse` |
| moon | Pane | `%15` | halyard | moon-phase-api | `/home/owner/code/almanac` |
| harvest | Pane | `%16` | sextant | harvest-report | `/home/owner/code/orchard` |
| docs | Pane | `%17` | tarn | docs-examples | `/home/owner/code/tidepool` |
| tidetables | Pane | `%18` | tarn | tide-tables | `/home/owner/code/tidepool` |
| cache | Pane | `%19` | halyard | ci-cache | `/home/owner/code/quarry` |
| ephem | HeadlessSession | `4be0c6d2-8f3a-4c71-9e25-ephemeris001` | — | ephemeris-import | `/home/owner/code/almanac` |
| rota T2 implementer | EpicStep / Sessions.WatchConversation | `st-rota-t2-impl` | — | T2 | `/home/owner/code/lighthouse` |

Pane row keys are `pane:<host>:<id>` (rows.ts:57), e.g. `pane:halyard:%11`; epic line keys are `epic:<Epic.id>` (epics.ts:203).

### 2.2 Epics.List → `EpicsListResponse.epics` (this order; wf ORDER epicnow.js:537)

All: `source: "orreryd"`, `problem: ""`, `outcomeDetail: ""`, `waiting: false`.

| id | outcome | live | phaseAt | seen | changed | runningSteps | endedSteps |
|---|---|---|---|---|---|---|---|
| orrery/orrery-followups | `""` | true | @14:21 | 0 | @14:40 | [st-fu-t1-impl, st-fu-t2-review] (full EpicStep values from §2.5a) | 1 |
| lighthouse/rota-export | `""` | true | @13:52 | 0 | @14:40 | [st-rota-t2-impl, st-rota-t1-impl] (§2.3) | 5 |
| tidepool/tide-alerts | `"contract written"` | true | @14:15 | 0 | @14:15 | [] | 0 |
| tidepool/offline-tables | `"stopped"` | true | @14:32 | 0 | @14:32 | [] | 0 |
| orrery/orbit-labels | `"releasing"` | true | @14:37 | 0 | @14:40 | [] | 0 |
| orrery/holder-scopes | `"released"` | false | @09:52 | @09:55 | @09:52 | [] | 4 |
| tidepool/tide-cache-drop-tables-older-than-a-week | `"stopped"` | false | @09:12 | 0 | @09:12 | [] | 2 |

Outcome choices (see the Gaps of each scenario): a live building epic gets `""` so the client derives `building`
(status.ts `stateOf`) and shows Pause/Stop… (epicpage.tsx:89 `live = epic.live && epic.outcome === ""`); orreryd itself
would send its phase `accepted`. holder-scopes is `seen ≥ phaseAt` (ended earlier); the stopped tide-cache epic is
`seen < phaseAt` (Just ended).

### 2.3 Epics.Get `lighthouse/rota-export` (EpicsGetResponse)

| field | value |
|---|---|
| epic | the Epic row of §2.2 |
| repository | `/home/owner/code/lighthouse` |
| summary | `A keeper’s week of shifts becomes a file for the harbour office. Shifts are stored per keeper and week, one endpoint serves a week, and rota export writes it as CSV, one row per shift. Export refuses to overwrite a file and writes every run to the audit log. Fixtures cover every port zone. No dependency changes.` |
| paused / parked | false / `""` |
| tasks / nodes / ledger / releases / reviews | [] |
| report | absent |
| questions | `[{ id: "Q1", text: "Bump the embedded tz database? The pinned database lacks the 2026 Lisbon rule, so one fixture cannot pass. A bump changes go.mod, which is outside this unit’s owned files, and the contract says no dependency changes.", turn: 4, asked: @14:39 }]` |
| sessions | `[{ id: "sess-rotaplan", pane: "%14", label: "rota-planning", call: "epic_get", at: @13:50 }, { id: "sess-lamp", pane: "%11", label: "lamp-telemetry", call: "epic_get", at: @14:12 }]` |

**contract** (EpicContract):

| field | value |
|---|---|
| title | `rota-export` |
| approved | `Approved: export one week of the rota as CSV, without overwriting anything.` |
| intent | `A CSV of the week’s shifts that I can send to the harbour office, because they will not use the rota page.` |
| failures | F1 `An export replaced a file I had edited by hand` / rating `serious` / response `refuse to overwrite (R4)`; F2 `A shift was missing from the file` / `serious` / `one row per stored shift (R1, R3)`; F3 `Times were wrong for a port in another zone` / `limited` / `fixtures for every port zone (R5)` |
| care | `serious` |
| additions | `[{ location: "cli/export.go", what: "An audit-log line for every export", why: "F1", cost: "one unit" }]` |
| premises | P1 `The harbour office opens the CSV in a spreadsheet that reads UTF-8.` / ifWrong `Add a byte-order mark, or export XLSX instead.`; P2 `A keeper’s week starts on Monday at 00:00 in the port’s own zone.` / `The week boundary moves, and R2’s endpoint moves with it.`; P3 `Every stored shift already names its port, so its zone can be read from it.` / `A migration adds the port to every shift first.` |
| exclusions | `["No dependency changes.", "No change to the rota page."]` |
| approach | `Store shifts per keeper and week, serve one week from a new endpoint, and add rota export to the CLI, which writes the week as CSV and refuses a path that already exists.` |
| alternatives | `{ alternative: "Mail the CSV from the server every Monday", whyNot: "The office asked for a file it can send on, not a mailing.", reconsider: "If the office asks for a weekly mail." }`, `{ alternative: "A print view of the rota page", whyNot: "They will not use the rota page.", reconsider: "If the office starts using the page." }` |
| releaseSteps | `{ step: "Merge the epic branch to main", target: "lighthouse main", undo: "revert the merge" }`, `{ step: "Deploy lighthouse to halyard", target: "halyard", undo: "redeploy the previous build" }` (epicnow.js:729) |
| releaseThen | `Then halyard serves rota export, and the harbour office gets this week’s file.` |
| checks | `just check on every return (go test ./... and golangci-lint); a red and a green evidence run for each check requirement; a review for R6.` |
| base | `3e1f2ab6b681479f7e6dc3c139194a1ad6b8025b` |
| accepted | @13:50 |

**requirements** (EpicRequirement; texts epicnow.js:309-315, tests epicnow.js:726-728):

| id | kind | text | evidence | conclusion | runs |
|---|---|---|---|---|---|
| R1 | check | Shifts are stored per keeper and week. | `go test ./internal/rota/ -run TestShiftStorePerKeeperWeek` | `TestShiftStorePerKeeperWeek passes: a stored week reads back per keeper.` | red `ev-rota-r1-red`, green `ev-rota-r1-green` |
| R2 | check | The rota endpoint serves one week of shifts. | `go test ./api/ -run TestRotaEndpointServesWeek` | `TestRotaEndpointServesWeek passes: GET /rota/{week} returns that week’s shifts.` | red `ev-rota-r2-red`, green `ev-rota-r2-green` |
| R3 | check | A week exports as CSV, one row per shift. | `go test ./internal/rota/ -run TestCSVOneRowPerShift` | `TestCSVOneRowPerShift passes: 21 stored shifts give 21 rows.` | red `ev-rota-r3-red`, green `ev-rota-r3-green` |
| R4 | check | Export refuses to overwrite an existing file. | `go test ./cli/ -run TestExportRefusesOverwrite` | `TestExportRefusesOverwrite passes: Export returns ErrExists for a path that exists.` | red `ev-rota-r4-red` |
| R5 | check | Fixtures cover every port zone. | `go test ./internal/rota/ -run TestFixturesCoverPortZones` | `TestFixturesCoverPortZones passes for every zone on the rota.` | red `ev-rota-r5-red` |
| R6 | review | Every export is written to the audit log, and the docs say so. | `review of cli/export.go and README.md` | `""` | [] |

EvidenceRun values (`source: "captured"`, `state: "ended"`, `timedOut: false`, `notCaptured: ""`, `judgeReason: ""`,
`artifacts: []`, `stderrBytes: 0`, `item: ""`):

| id | phase | exit | verdict | passed | revision | created / ended | logBytes (= UTF-8 length of the log text; the column value is a guide) | log text (Evidence/Artifact stdout) |
|---|---|---|---|---|---|---|---|---|
| ev-rota-r1-red | red | 1 | fails | true | 3e1f2ab… (full) | @13:53:10 / @13:53:40 | 41 | `store_test.go:27: no such table: shifts` |
| ev-rota-r2-red | red | 1 | fails | true | 3e1f2ab… | @13:53:10 / @13:53:42 | 38 | `handler_test.go:44: got 404, want 200` |
| ev-rota-r3-red | red | 1 | fails | true | 3e1f2ab… | @13:53:10 / @13:53:41 | 35 | `csv_test.go:19: got 0 rows, want 21` |
| ev-rota-r4-red | red | 1 | fails | true | 3e1f2ab… | @14:08:00 / @14:08:30 | 42 | `export_test.go:88: want ErrExists, got nil` |
| ev-rota-r5-red | red | 1 | fails | true | 3e1f2ab… | @14:23:00 / @14:23:30 | 55 | `fixtures_test.go:52: zone Europe/Lisbon: 2026 rule missing` |
| ev-rota-r1-green | green | 0 | holds | true | a41b7d0… | @14:02:00 / @14:02:40 | 52 | `ok  	lighthouse/internal/rota	1.41s` |
| ev-rota-r2-green | green | 0 | holds | true | d84a2c96e1f0b3a75c2d9e04f6a81b3c7d5e9f20 (the rota-handler commit; the wf names none) | @14:29:00 / @14:29:35 | 40 | `ok  	lighthouse/api	0.71s` |
| ev-rota-r3-green | green | 0 | holds | true | 3f9c2e1… | @14:11:00 / @14:11:30 | 52 | `ok  	lighthouse/internal/rota	1.92s` |

**buildTasks** (EpicBuildTask; order T1..T7 per §2.1):

| id | status | stage | red | green | reviews |
|---|---|---|---|---|---|
| T1 | open | implement | ev-rota-r5-red | — | [] |
| T2 | open | implement | ev-rota-r4-red | — | `[{ attempt: 1, gate: "review", step: "st-rota-t2-review", verdict: "fails", findings: [{ id: "F1", claim: "export overwrites an existing file without asking. R4 says it must refuse.", location: "cli/export.go:57", step: "st-rota-t2-verify", result: "confirmed", reason: "Ran rota export twice into one path. The second run replaced the file." }] }]` |
| T3 | open | verify | ev-rota-r2-red | ev-rota-r2-green | `[{ attempt: 1, gate: "review", step: "st-rota-t3-review", verdict: "holds", findings: [] }]` |
| T4 | done | verify | ev-rota-r1-red | ev-rota-r1-green | `[{ attempt: 1, gate: "review", step: "", verdict: "holds", findings: [] }]` |
| T5 | done | verify | ev-rota-r3-red | ev-rota-r3-green | `[{ attempt: 2, gate: "review", step: "", verdict: "holds", findings: [] }]` |
| T6 | open | `""` | — | — | [] |
| T7 | open | `""` | — | — | [] |

**steps** (EpicStep, oldest first by `started`; common: `gateName: "check"`, `gateLimit: 1800`, `limited: false`,
`limitKind: ""`, `limitResets: 0`). The T2 reviewer `st-rota-t2-review` is deliberately absent (see epic-rota Gaps):

| id | task | agent | role | readOnly | end | status | held | started | ended | stage | stageStarted | doing | reason | summary |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| st-rota-t5-try1 | T5 | haiku | implementer | false | looping | ended | false | @13:52:40 | @13:58 | `""` | @13:52:40 | `""` | `The monitor saw the same edit three times, so it was nudged, ended and retried on sonnet.` | `""` |
| st-rota-t4-impl | T4 | haiku | implementer | false | done | ended | false | @13:52:41 | @14:01 | `""` | @14:00:10 | `""` | `""` | `Shifts are stored per keeper and ISO week in the shifts table.` |
| st-rota-t5-try2 | T5 | sonnet | implementer | false | done | ended | false | @13:58:30 | @14:11 | `""` | @14:10:20 | `""` | `""` | `A week exports as CSV with one row per stored shift.` |
| st-rota-t2-impl | T2 | sonnet | implementer | false | `""` | running | false | @14:07 | 0 | turn | @14:36:48 | `Adding the refuse-on-existing check and its test.` | `""` | `""` |
| st-rota-t3-impl | T3 | haiku | implementer | false | done | ended | false | @14:16 | @14:30 | `""` | @14:29:10 | `""` | `""` | `GET /rota/{week} serves one week of shifts from the store.` |
| st-rota-t1-impl | T1 | haiku | implementer | false | `""` | held | true | @14:22 | 0 | held | @14:39 | `It asked to bump the embedded tz database. That changes go.mod, which is outside its owned files.` | `Paused by planner turn 4 for Q1: bump the embedded tz database?` | `""` |
| st-rota-t3-review | T3 | sonnet | reviewer | true | done | ended | false | @14:31 | @14:33 | `""` | @14:31 | `""` | `""` | `Review clean: 0 findings.` |

checks and verdicts:
- st-rota-t4-impl, st-rota-t5-try2, st-rota-t3-impl: round 1 checks `schema, head, ancestry, clean, owned, gate` all `ok: true`, `detail: ""`, `stdout/stderr: ""`, `tailStdout` only on `gate`: `"$ just check\nok  \tlighthouse/internal/rota\t1.41s\ngolangci-lint: 0 issues"`, `tailStderr: ""`; verdicts `[{ round: 1, verdict: "met", reason: "The requirement’s test fails at the parent and passes at the commit." }]`.
- st-rota-t5-try1, st-rota-t3-review, st-rota-t1-impl: checks [], verdicts [].
- **st-rota-t2-impl** (the export-cli step every phase/worker scenario opens):
  - round 1: `schema`, `head`, `ancestry`, `clean`, `owned` each `{ ok: true, detail: "", tailStdout: "", tailStderr: "" }`; `gate`:
    ```
    { round: 1, name: "gate", ok: false,
      detail: "exit 1: cli/export_test.go:88: want ErrExists, got nil",
      stdout: <T>, tailStdout: <T>,
      stderr: "error: recipe `check` failed with exit code 1",
      tailStderr: "error: recipe `check` failed with exit code 1" }
    <T> = "$ just check\n--- FAIL: TestExportRefusesOverwrite (0.01s)\n    export_test.go:88: want ErrExists, got nil\n    export_test.go:91: the existing file was replaced: 312 bytes became 1,840 bytes\nFAIL\nFAIL\tlighthouse/cli\t0.38s\nok  \tlighthouse/internal/rota\t1.90s\nFAIL"
    ```
  - round 2: the same five checks ok; `gate` `{ ok: true, detail: "", stdout: "", stderr: "", tailStdout: "$ just check\nok  \tlighthouse/cli\t0.44s\nok  \tlighthouse/internal/rota\t1.92s\ngolangci-lint: 0 issues", tailStderr: "" }`.
  - verdicts: `[{ round: 2, verdict: "met", reason: "TestExportRefusesOverwrite fails at the parent commit and passes at e02b6c4, and Export returns ErrExists for a file that is there." }]` (OUT epicnow.js:808).
  - With checks up to round 2 and no end, the client reads it as round 3 (epics.ts:301 `roundOf`).

**slices** (EpicSlice, in order of each label's first start):

| task | hue | redTest | working | testsPass | checks | gateAccepted | at | held | errorAt |
|---|---|---|---|---|---|---|---|---|---|
| T5 | done | t | t | t | t | t | 0 | f | 0 |
| T4 | done | t | t | t | t | t | 0 | f | 0 |
| T2 | dispatched | t | t | t | t | f | 5 | f | 0 |
| T3 | done | t | t | t | t | t | 0 | f | 0 |
| T1 | dispatched | t | t | f | f | f | 3 | t | 0 |

**releaseSteps** (EpicReleaseProof): position 1 and 2 with the contract's step/target/undo, `state: "waiting"`,
`evidence: ""`, `reason: ""`, `attempt: 0`, `at: 0`.

### 2.4 Epics.Get `orrery/holder-scopes`

| field | value |
|---|---|
| epic | §2.2 row |
| repository | `/home/owner/Personal/orrery` |
| summary | `Each holder runs in its own systemd scope, so a runaway one is killed alone and the terminal memory cap covers it. orreryd starts as Type=notify with a 900 s ceiling, and the devspaces doc no longer names an orrery version.` (epicnow.js:772) |
| paused / parked | false / `""` |
| questions / tasks / nodes / ledger / releases | [] |
| sessions | `[{ id: "sess-holdplan", pane: "", label: "holder-planning", call: "epic_create", at: @08:04 }]` |
| reviews | `[{ position: 1, reviewer: "final review", step: "st-holder-final-review", verdict: "holds", reason: "0 findings." }]` |
| report | `{ outcome: "released", shipped: ["R1","R2","R3","R4"], notShipped: [], why: "All four requirements shipped. It ran 1 h 48 min, from acceptance at 08:04 to v0.45.0 running on vermissian at 09:52." }` |

contract: title `holder-scopes`; approved `Approved: each holder in its own systemd scope, under the terminal memory cap.`;
intent `A runaway holder should not take the terminals down with it.`; failures F1 `systemd killed orreryd while it was
still starting` / `serious` / `Type=notify with a 900 s ceiling (R3)`, F2 `The memory cap missed a holder` / `serious` /
`the cap covers holder scopes (R2)`; care `serious`; additions []; premises P1 `Every holder is started by orreryd, never
by hand.` / `A hand-started holder runs outside the cap.`, P2 `systemd on vermissian is 255 or newer.` / `Scopes need the
older property names.`; exclusions `["No change to how terminals are spawned."]`; approach `Start each holder in a
transient systemd scope under orrery.slice, and switch orreryd to Type=notify.`; alternatives `[{ alternative: "One
cgroup for all holders", whyNot: "A runaway holder would still take its siblings down.", reconsider: "If scopes cost too
much to start." }]`; base `0c7e4aa22f3584d2a720b837a5507c36476852fe`; accepted @08:04; checks `just check; a red and a
green run for R1-R3; a review for R4.`; releaseThen `Then vermissian runs v0.45.0 and every holder is scoped.`;
releaseSteps (step 1 is the full sentence; wf epicnow.js:778-779 has the short form `Push tag v0.45.0, signed`):

| # | step | target | undo |
|---|---|---|---|
| 1 | `Push the signed tag v0.45.0 to the orrery repository on GitHub, so the release workflow builds the binaries and attaches them to the v0.45.0 release page.` | `github, tag v0.45.0` | `none once someone has fetched the tag` |
| 2 | `CI green on the tag` | `github actions` | `rerun` |
| 3 | `vermissian runs v0.45.0` | `vermissian` | `switch to the previous generation` |

releaseSteps (EpicReleaseProof): `{1, <step 1>, …, state: "passed", evidence: "github · 09:44 · signature good", reason: "The signed tag v0.45.0 is on github.", attempt: 1, at: @09:44}`,
`{2, …, state: "passed", evidence: "run 5490 · 09:51", reason: "CI is green on the tag.", attempt: 1, at: @09:51}`,
`{3, …, state: "passed", evidence: "orrery --version: orrery 0.45.0", reason: "vermissian runs v0.45.0.", attempt: 1, at: @09:52}`.

requirements (epicnow.js:475-480, 776-777): R1 `Each holder runs in its own systemd scope.` (check),
R2 `The terminal memory cap covers holder scopes.` (check), R3 `orreryd starts as Type=notify with a 900 s ceiling.`
(check), R4 `The devspaces doc names no orrery version.` (review). evidence `go test ./internal/holder/ -run
TestHolderRunsInOwnScope` / `-run TestMemoryCapCoversHolders` / `go test ./internal/unit/ -run TestOrrerydNotifyStart`
/ `review of docs/devspaces.md`; conclusion `<test> passes.` (R4: `The doc names no orrery version.`). runs:
red R1-R3 (`exit 1, verdict fails, passed true, revision 0c7e4aa…`, created @08:10, logs `holder_test.go:71: unit
holder-1.scope not found`, `cap_test.go:38: MemoryMax unset on holder-1.scope`, `unit_test.go:19: Type=simple, want
notify`); green R1 at b91e1ed… (@08:40), R2 at 984ef49… (@09:02), R3 at 9cc2be1… (@09:15) (`exit 0, holds, passed
true`); green R4 `source: "supplied"`, `exit 0`, `verdict: "holds"`, `passed: true`, revision 9593e0b…, @09:30.

buildTasks T1 `Each holder runs in its own systemd scope.` [R1], T2 `The terminal memory cap covers holder scopes.` [R2],
T3 `orreryd starts as Type=notify with a 900 s ceiling.` [R3], T4 `The devspaces doc names no orrery version.` [R4]:
all `status: "done"`, `stage: "verify"`, red/green as above, reviews `[{ attempt: 1, gate: "review", verdict: "holds" }]`.
steps: `st-holder-t1..t4-impl` (T1..T4, haiku, implementer, end done, started @08:06/@08:20/@08:44/@09:18, ended
@08:40/@09:02/@09:15/@09:30, round 1 checks all ok, verdict met). slices: T1..T4 `hue: "done"`, all booleans true.

### 2.5 Epics.Get `tidepool/tide-alerts`

epic §2.2; repository `/home/owner/code/tidepool`; summary epicnow.js:736 (plain text, `4.5 m` with U+00A0);
paused false; parked ""; questions/tasks/buildTasks/steps/slices/nodes/ledger/releases/reviews `[]`; report absent;
sessions `[{ id: "sess-tideplan", pane: "", label: "tide-planning", call: "epic_create", at: @14:15 }]`;
releaseSteps the two contract steps with `state: "waiting"`.

contract:

| field | value |
|---|---|
| title | `tide-alerts` |
| approved | `Approved: a text the evening before any tide forecast above 4.5 m at the harbour.` (U+00A0 before `m`; epicnow.js:737) |
| intent | `A text the evening before a tide above 4.5 m, because the slipway floods and the boat has to be moved.` (epicnow.js:738) |
| failures | F1 `An alert went to a number that is not mine` / `severe` / `the number comes from config only (R3)`; F2 `A real high tide passed with no text` / `serious` / `replay of last year's tables (R2)`; F3 `Three texts arrived for one tide` / `limited` / `one alert per tide id (R4)` (epicnow.js:739-740) |
| care | `severe` |
| additions | `[{ location: "alerts/send.go", what: "A dry-run flag that logs instead of sending", why: "F1", cost: "one flag" }]` |
| premises | P1 `The harbour authority publishes tomorrow’s table by 17:00.` / `The evening text moves to the morning.`; P2 `The SMS gateway accepts one sender id for this account.` / `Texts arrive from a shared number.`; P3 `4.5 m is measured above chart datum.` / `The threshold is off by the datum offset.` |
| exclusions | `["No alerts by email or push.", "No alerts for any harbour but the home port."]` |
| approach | `A timer on tarn reads tomorrow’s forecast every evening at 18:00 and sends one text per tide above 4.5 m through the SMS gateway, keyed by tide id.` |
| alternatives | `{ "Subscribe to the harbour authority’s own alerts", "They warn at 5.0 m, after the slipway floods.", "If they add a 4.5 m threshold." }`, `{ "A calendar feed of high tides", "A calendar does not wake anyone.", "If texts cost too much." }` |
| releaseSteps | `{ "Send texts through the SMS gateway", "SMS gateway, the number in config", "none, a sent text stays sent" }`, `{ "Deploy the alert job to tarn", "tarn", "disable the timer" }` (epicnow.js:743) |
| releaseThen | `Then tarn texts you the evening before each tide above 4.5 m.` |
| checks | `just check on every return; a replay of last year’s tables as the evidence for R2.` |
| base | `""` |
| accepted | `0` |

requirements R1-R5 (epicnow.js:374-380), all `kind: "check"`, `evidence` `go test ./alerts/ -run TestR<n>`,
`conclusion: ""`, `runs: []`.

### 2.5a Epics.Get for the list-only epics (only the list reads these)

Fields not listed are empty/absent. `contract` absent.

**orrery/orrery-followups** (wf moment 2, epicnow.js:200-221, shifted +4 h):
requirements R1 (check, red run passed), R2 (check, red + green passed), R3 (review, no runs) with texts epicnow.js:123-127.
buildTasks T1 `The done-judge sees a long Task or Return section’s head and tail, with a line naming the bytes left out.`
[R1] open/implement; T2 `A planner turn freed by a limit with no reset says “reset time unknown”.` [R2] open/review;
T3 `The docs state the new judge input and the new limit text.` [R3] open/`""`.
steps (oldest first): `st-fu-t1-impl` T1 haiku implementer, running, started @14:21, stage `turn`, stageStarted @14:39,
doing `Reworking the invalid-UTF-8 test so that it fails against the old cut.`, checks round 1 all ok, verdicts
`[{1, "unmet", "The invalid-UTF-8 test passes before and after the fix, so the new cut is unproven on that path."}]`;
`st-fu-t2-impl` T2 haiku implementer, end done, @14:21:05-@14:40, round 1 checks ok, verdict met;
`st-fu-t2-review` T2 sonnet reviewer readOnly, running, started @14:40, stage `turn`, stageStarted @14:40, doing
`Reading runner.go:373 and the new limit test against R2.`.
slices: T1 dispatched (t,t,t,t,f, at 5); T2 done.

**tidepool/offline-tables** (epicnow.js:394-442, 747-759): requirements R1-R6 (R6 review) each with one green run
`passed: true` (R6 `source: "supplied"`); buildTasks T1 `Tide tables for the home port are stored on the phone.` [R1,R2],
T2 `The sync worker retries with backoff and never blocks the screen.` [R3,R4], T3 `The tables screen works with no
network and says how old its data is.` [R5,R6], all `done`; steps []; releaseSteps
`{1, "Push main and tag v0.4.0, signed", "github, tag v0.4.0", "none once someone has fetched the tag", state "passed", evidence "github · 14:05 · signature good", attempt 1, at @14:05}`,
`{2, "CI green on the tag", "github actions", "rerun", state "failed", evidence "run 5519", reason "CI is red on the tag v0.4.0 for the second time: go vet: cache/key_test.go:41.", attempt 2, at @14:32}`,
`{3, "Publish the APK to the release page", "github release page", "delete the release asset", state "waiting"}`.

**orrery/orbit-labels** (epicnow.js:444-471): requirements R1-R4 each with a green run `passed: true`; buildTasks
T1 `Every orbit carries its stage name.` [R1], T2 `A card for every body, on the web and the phone.` [R2,R3],
T3 `The chart spec states the new labels.` [R4], all `done`; releaseSteps `{1, "Merge the epic branch to main", …,
"passed", at @14:38}`, `{2, "Push tag v0.46.0, signed", "github, tag v0.46.0", "none once someone has fetched the tag",
"running"}`, `{3, "CI green on the tag", "github actions", "rerun", "waiting"}`.

**tidepool/tide-cache-drop-tables-older-than-a-week** (only asked for if clicked): requirements R1-R3 no runs;
buildTasks T1-T3 `open`; report `{ outcome: "stopped", shipped: [], notShipped: ["R1","R2","R3"], why: "You stopped it
at 09:12, because offline-tables covers the same ground. Two unit branches are kept, unmerged." }`.

### 2.6 Panes.List → `PanesListResponse.panes` (this order)

Common: `command: "claude"`, `window: 0`, `pane: <n>`, `context: ""`, `questionHash: ""` (beacon:
`"qh-beacon-1"`), `claudeSession: <uuid>`, `activity = changed`, `newestReply = changed`, `lastTurnEnd = changed`
(working panes: 0). `session` = the tmux session name = last path element of cwd.

| id | label | host | cwd | state | changed | seen | summary | question | tool |
|---|---|---|---|---|---|---|---|---|---|
| %12 | beacon-schedule | halyard | /home/owner/code/lighthouse | waiting | @14:29 | @14:28:30 | `asks which index to add` | Q-JSON below | `""` |
| %11 | lamp-telemetry | halyard | /home/owner/code/lighthouse | working | @14:31:20 | @14:31:30 | `adding retry backoff` | `""` | `{"id":"toolu_lamp_run","name":"Bash","input":"go test ./internal/telemetry/...","start":1794667218}` (@14:40:18) |
| %13 | graft-parser | sextant | /home/owner/code/orchard | done | @14:21 | @14:00 | `All 48 parser tests pass. The grafting rule for nested scions was the only real change; the rest was test data.` | `""` | `""` |
| %15 | moon-phase-api | halyard | /home/owner/code/almanac | done | @14:02 | @13:30 | `The phase endpoint now returns ISO dates, and both clients that parsed the old format are updated.` | `""` | `""` |
| %16 | harvest-report | sextant | /home/owner/code/orchard | working | @14:38 | @14:38:30 | `rendering CSV export` | `""` | `{"id":"toolu_hv","name":"Edit","input":"report/csv.go","start":1794667253}` (@14:40:53) |
| %14 | rota-planning | halyard | /home/owner/code/lighthouse | done | @13:50 | @13:51 | `The contract for rota-export is accepted and the epic is created. I can read it or steer it from here whenever you ask.` | `""` | `""` |
| %17 | docs-examples | tarn | /home/owner/code/tidepool | done | @09:41 | @09:42 | `README examples fixed and run in CI.` | `""` | `""` |
| %18 | tide-tables | tarn | /home/owner/code/tidepool | done | @-2d 14:41 | @-2d 14:42 | `Migration merged; the old table is dropped.` | `""` | `""` |
| %19 | ci-cache | halyard | /home/owner/code/quarry | done | @-3d 14:41 | @-3d 14:42 | `Cache keys trimmed to the lockfile hash.` | `""` | `""` |

Q-JSON (Pane.question for %12, an AskUserQuestion value read by question.ts):
`{"questions":[{"question":"Which index should I add?","header":"Index","multiSelect":false,"options":[{"label":"Composite index","description":"On (keeper_id, shift_start). Serves the backfill and the roster page."},{"label":"Partial index","description":"Open shifts only. Smaller, but the backfill still scans closed shifts."},{"label":"Something else","description":"Type an answer."}]}]}`

### 2.7 Sessions.ListHeadless

`{ sessions: [{ id: "4be0c6d2-8f3a-4c71-9e25-ephemeris001", dir: "/home/owner/code/almanac", label: "ephemeris-import", state: "interrupted", changed: @12:41, seen: @12:42, summary: "orreryd restarted during the turn, and the turn stopped where it was.", question: "", newestReply: @12:40:30 }] }`

### 2.8 Epics.History

`EpicHistoryEntry.cursor` = `c<n>` in order. Entries oldest first; the client shows the newest three and `+N earlier`.

**lighthouse/rota-export** (12 entries → three shown, `+9 earlier`; wf epicnow.js:316-328):

| # | time | case | value |
|---|---|---|---|
| 1 | @13:50 | tell | `{ seq: 1, text: "You accepted the contract: 6 requirements, serious care.", at: @13:50, told: true }` |
| 2 | @13:52 | turn | `{ seq: 1, step: "st-rota-plan-1", state: "succeeded", reason: "", outline: "rota-store, csv-writer, rota-handler, export-cli, tz-fixtures", outcome: "Outlined 5 units and started rota-store and csv-writer, because the others build on them.", decisions: [], started: @13:52, ended: @13:52:35 }` |
| 3 | @13:52:40 | step | st-rota-t5-try1 (§2.3 row) |
| 4 | @13:58:30 | step | st-rota-t5-try2 |
| 5 | @14:07 | step | st-rota-t2-impl |
| 6 | @14:10 | turn | `{ seq: 3, state: "succeeded", outcome: "Outlined 2 more units, export-docs and rota-audit: 5 to 7. The audit requirement needed its own unit.", started: @14:10, ended: @14:10:50 }` |
| 7 | @14:11 | evidence | ev-rota-r3-green with `item: "R3"` |
| 8 | @14:22 | step | st-rota-t1-impl |
| 9 | @14:31:30 | step | `{ id: "st-rota-t2-review", task: "T2", agent: "sonnet", role: "reviewer", readOnly: true, end: "gate_failed", status: "ended", started: @14:31:30, ended: @14:36:00, reason: "Finding F1: export overwrites an existing file; R4 says it must refuse.", summary: "1 finding, confirmed by a verifier." }` |
| 10 | @14:37 | tell | `{ seq: 2, text: "Review finding F1 confirmed: export overwrites an existing file, and R4 says it must refuse. Sent back.", at: @14:37, told: true }` |
| 11 | @14:39 | turn | `{ seq: 4, state: "succeeded", outcome: "Raised Q1 for you and paused tz-fixtures: bump the tz database?", decisions: [], started: @14:39, ended: @14:39 }` |
| 12 | @14:40 | tell | `{ seq: 3, text: "rota-handler: the engine started merging it onto the epic branch.", at: @14:40, told: true }` |

**orrery/holder-scopes** (6 tells → three shown, `+3 earlier`; wf epicnow.js:481-488): @08:04 `You accepted the
contract: 4 requirements, serious care.`; @09:31 `docs-devspaces integrated: the last task.`; @09:38 `Final review of the
epic branch: 0 findings.`; @09:39 `orrery started the release by itself, because the epic is clean.`; @09:44 `Tagged
v0.45.0 and pushed. CI went green on the tag.`; @09:52 `Released v0.45.0. vermissian runs it.` (seq 1-6, told true).

### 2.9 Host.Status

`{ host: "vermissian", stats: [{ label: "cpu", value: "34%", percent: 34, level: "ok" }, { label: "mem", value: "61%", percent: 61, level: "ok" }, { label: "load", value: "2.1", level: "" }, { label: "disk", value: "48%", percent: 48, level: "ok" }] }` (wf HOSTS epicnow.js:591-596, first host)

### 2.10 Conversations

All `transcript: "<id>.jsonl"`, `seq: "1"`, `reset: true`. Pane streams carry `state.pane` = the §2.6 Pane.
Item ids for tool calls are `toolu_<n>`; each tool call with a result is followed by `{ kind: "result", id: same,
time: same, text, error }`. Items not listed get `tool/input/notice/trigger: ""`, `tokens: 0`, `sender: ""`, `round: 0`.

**%11 lamp-telemetry** (Panes.WatchConversation; wf index.html:48-68). `state: { origin: "", goal: "Stop the telemetry
uploader from losing readings when the collector is unreachable.", waiting: [{ text: "Also log the batch id whenever a
batch is spooled.", time: @14:36:10, canceled: false }, { text: "Then put the 5-minute cap in the README.", time:
@14:38:40, canceled: false }], context: { tokens: 30000, window: 200000 }, pane: <%11> }`.

| # | kind | time | fields |
|---|---|---|---|
| 1 | compaction | @14:02 | trigger `auto`, tokens `611000`, text `The telemetry uploader lost readings whenever the collector was unreachable. Established so far: Flush is the only caller of send; a batch holds at most 500 readings; the spool directory exists but nothing writes to it. Agreed plan: retry with capped exponential backoff, then spool.` |
| 2-7 | tool ×6 + results | @14:03, @14:05, @14:08, @14:12, @14:18, @14:24 | Read `internal/telemetry/flush.go`; Write `internal/telemetry/backoff.go`; Bash `go test ./internal/telemetry/...` (result error true, `--- FAIL: TestBackoffSchedule … want 250ms, got 0s`); Write `internal/telemetry/backoff_test.go`; Bash `go test ./internal/telemetry/...` (ok); Edit `internal/telemetry/flush.go` |
| 8 | reply | @14:27 | text below (markdown with a fenced go block) |
| 9 | prompt | @14:31 | sender `owner`, text `Cap the total retry window at 5 minutes and add jitter.` |
| 10 | tool + result | @14:32 | Edit `internal/telemetry/backoff.go` → `Added 14 lines, removed 3 lines` |
| 11 | tool + result | @14:32:20 | Bash, input (long on purpose) `go test ./internal/telemetry/... -run 'Backoff\|Jitter\|RetryWindow\|SpoolAfterWindow\|FlushRetriesInPlace' -count=1 -race -timeout 120s` → `ok  \tlighthouse/internal/telemetry\t0.44s` |

Item 8 text:
````
Backoff is in and the schedule test passes. Delays double from 250 ms and stop growing at 30 s:

```go
func (b Backoff) Delay(n int) time.Duration {
	d := b.Base << n
	if d > b.Max || d <= 0 {
		return b.Max
	}
	return d
}
```

`Flush` now retries a failed batch in place. Still open: there is no jitter, so lamps that drop together retry together, and retries never give up.
````

`unfinished: [{ messageId: "msg_lamp_live", index: 0, parentToolUseId: "", kind: "text", text: "Full jitter is in: each delay is drawn uniformly between zero and the capped value. Now wiring the 5-minute window into Flush, so a batch that outlives it goes to the on-disk spool instead of being dro" }]`

**%12 beacon-schedule** (wf index.html:69-80). `state: { goal: "", waiting: [], context: { tokens: 18000, window: 200000 }, pane: <%12> }`, `unfinished: []`. No `kind: "question"` item: the card is drawn from `Pane.question`.

| # | kind | time | fields |
|---|---|---|---|
| 1 | prompt | @14:24 | sender `owner`, `The nightly backfill takes forty minutes. Find out why and fix it.` |
| 2-6 | tool ×5 + results | @14:24:30, @14:25, @14:26, @14:27, @14:28 | Grep `keeper_id`; Read `internal/rota/backfill.go`; Read `migrations/0007_rotation.sql`; Read `internal/rota/query.go`; Bash `psql -c "EXPLAIN ANALYZE SELECT … WHERE keeper_id = 42"` → `Seq Scan on rotation (cost=0.00..48210.00 rows=1 width=64)` |
| 7 | reply | @14:29 | ``The nightly backfill scans every shift because the rotation table has no index on `keeper_id`. Two ways to fix it:\n\n1. Composite index on `(keeper_id, shift_start)`\n2. Partial index on open shifts only`` |

**%14 rota-planning** (wf talk data epicnow.js:730-731, talkConvo 1371-1376). `state: { pane: <%14>, waiting: [] }`.

| # | kind | time | fields |
|---|---|---|---|
| 1 | prompt | @13:50 | sender `owner`, `Why did csv-writer need a retry?` |
| 2 | tool + result | @13:50:05 | tool `epic_get`, input `rota-export`, result `rota-export · building · 2 of 7 units integrated` |
| 3 | reply | @13:50:20 | `Its first try, on haiku, made the same edit three times, so the monitor ended it at 13:58 and the planner retried on sonnet. The retry integrated at 3f9c2e1, which proves R3.` |

**Other panes** (%13, %15-%19): one `reply` item at `changed` with text = Pane.summary; `state.pane` = the Pane.

**st-rota-t2-impl** (Sessions.WatchConversation; wf STEP epicnow.js:1339-1348).
`state: { goal: "", waiting: [], context: { tokens: 88000, window: 200000 }, session: { id: "st-rota-t2-impl", dir:
"/home/owner/code/lighthouse", label: "T2", state: "working", changed: @14:36:48, seen: 0, summary: "", question: "",
newestReply: @14:40:40 } }`.

| # | kind | time | fields |
|---|---|---|---|
| 1 | prompt | @14:07 | sender `brief`, round 0, text `Brief from planner turn 2.\nGoal: R4, export refuses to overwrite an existing file.\nOwned files: cli/export.go, cli/export_test.go.\nAcceptance: TestExportRefusesOverwrite fails at 3e1f2ab and passes at your commit, and just check exits 0.` |
| 2-10 | tool ×9 + results | @14:07:30 … @14:18:30 | in this order: Read `cli/export.go`, Read `cli/root.go`, Read `internal/rota/csv.go`, Write `cli/export_test.go`, Write `cli/export.go`, Edit `cli/export.go`, Edit `cli/root.go`, Bash `go test ./cli/...`, Bash `git commit -am "export: write a week as CSV"` |
| 11 | prompt | @14:19 | sender `engine`, round 1, text `Gate exit 1. Sent back with the gate's output.\n` + <T> from §2.3 + `\nerror: recipe \`check\` failed with exit code 1` |
| 12-15 | tool ×4 + results | @14:20 … @14:30 | Read `cli/export.go`, Edit `cli/export.go`, Edit `cli/export_test.go`, Bash `just check` |
| 16 | prompt | @14:36:48 | sender `engine`, round 0, text `Review finding F1, confirmed: export overwrites an existing file without asking. R4 says it must refuse.\ncli/export.go:57 · a verifier reproduced it at 14:36 and sent it back into this session.` |
| 17 | tool + result | @14:38:10 | Edit `cli/export.go` → `Added 9 lines, removed 1 line` |
| 18 | tool, no result | @14:40:52 | Bash, input (long on purpose) `go test ./cli/... ./internal/rota/... -run 'TestExportRefusesOverwrite\|TestExportRefusesSecondRun\|TestExportWritesAuditLine' -count=1 -v` |

`unfinished: [{ messageId: "msg_t2_live", index: 0, parentToolUseId: "", kind: "text", text: "Export now checks for the file before it opens it, and returns ErrExists. Writing the test that proves a second export refus" }]`

---

## 3. Navigation the checker performs

Wireframe: load `index.html#<hash>`, clip to `.mock.b` (hide `.pg-bar`/`.wf-bar`), set `.b-rows.scrollTop = 0`.
Live: load the client against the fixture; every step is a click; after the last, `clock.runFor(500)`.

| scenario | wireframe | live navigation | wait for |
|---|---|---|---|
| list-home | `#go=home` | none. The cursor rests on the first entry, the beacon row, so the reader beside the list shows %12. Compare `.list`. | Epics/Get ×5, Panes/List |
| epic-rota-decision | `#go=epic:rota` | click `.list .b-row.epic[data-key="epic:lighthouse/rota-export"]` | Epics/History `lighthouse/rota-export` |
| epic-holder-released | `#go=epic:holder` | click `.list .b-more` whose text is `1 ended earlier ›`; click `.b-row.epic[data-key="epic:orrery/holder-scopes"]` (not polled, so the client asks Epics/Get once, app.tsx:405-416) | Epics/Get + Epics/History `orrery/holder-scopes` |
| contract-tidealerts-open | `#go=contract:tidealerts` | click `[data-key="epic:tidepool/tide-alerts"]`; click `section.page button.summary` | `article.contract` |
| contract-rota-accepted | `#go=contract:rota` | click `[data-key="epic:lighthouse/rota-export"]`; click `section.page button.summary` | `article.contract` |
| phase-rota-export-cli-checking | `#go=task:rota:export-cli:2` | click the rota line; click `section.page svg.orrery path.cell[data-task="T2"][data-phase="checking"]` | `.phase-output .phase-row.open` |
| worker-rota-export-cli-implementer | `#go=session:rota:export-cli:1` | click the rota line; click `path.cell[data-task="T2"][data-phase="implementing"]`; click `.phase-output .phase-row.open button.act` (`Open the session ›`) | Sessions/WatchConversation `st-rota-t2-impl` |
| talk-rota-rotaplan-docked | `#go=epic:rota&talk=rotaplan` | click the rota line; click `section.page .title-row .acts button.act` with text `Talk`; click `ul.talk li:first-child button` | Panes/WatchConversation `%14` |
| talk-rota-menu | `#go=epic:rota`, then press `t` (app.js:642) | click the rota line; click the `Talk` button (the live client has no `t` key) | `ul.talk` |
| session-lamp-working | `#go=sess:lamp` | click `.list .b-row[data-key="pane:halyard:%11"]` | Panes/WatchConversation `%11` |
| session-beacon-question | `#go=sess:beacon` | click `.list .b-row[data-key="pane:halyard:%12"]` | Panes/WatchConversation `%12` |

---

## 4. Scenarios

### 4.1 list-home

**Wireframe data.** Needs: `NEED` epicnow.js:567-580 (need-decision `rota-export` age `2m` "The planner wants your call:
<b>bump the tz database?</b> …"; beacon question `12m`; need-contract `tide-alerts` `26m` "severe care, 1 decision
open"; need-release `offline-tables` `9m`; ephem interrupted `2h`; need-gap hidden at followups moment 2). Epic lines:
`line()` epicnow.js:990-1000 over ORDER epicnow.js:537 with followups at moment 2 (epicnow.js:200-221: "sent back",
`exc.tone: 'info'` "The judge sent judge-head-tail back: one test does not prove the fix."), rota moment 0
(epicnow.js:331-367), tidealerts (`since: '26 min'`), offline (`act: 'stopped at step 2 of 3'`), orbitlabels
(`act: 'pushing tag v0.46.0'`). Just ended tidecache (`Stopped: superseded by offline-tables.` `09:12`), `1 ended
earlier ›` holder. Sessions: `SESS` epicnow.js:540-558, `GROUPS` epicnow.js:581 (fresh graft, moon; working lamp,
harvest; seen rotaplan, docs, tidetables, cache). Footer: `HOSTS` epicnow.js:591-596. Brand counts 5/5/2/3.

**Calls.** Panes/List, Sessions/List, Sessions/ListHeadless, Epics/List, Epics/Get ×5 (the live epics), Host/Status,
Notices/Watch, Panes/WatchConversation `%12` (cursor), Presence/Report.

**Expected live render** (rows.ts `byAttention`, epics.ts `withEpicsGroup`, needs.ts `epicNeeds`):

| block | content |
|---|---|
| brand counts | `1 need you`, `2 working`, `✱ 3 new` (rows only: beacon; lamp, harvest; beacon, graft, moon) |
| Needs you (3) | row beacon-schedule: `QUESTION`, `lighthouse`, age `12m`, text `beacon-schedule asks: Which index should I add?`, why `asks which index to add`, chips `1 Composite index`, `2 Partial index`, `3 Something else`; need `rota-export asks: Bump the embedded tz database? The pinned database …` with answer form; need `offline-tables: release step 2 (CI green on the tag) failed: CI is red on the tag v0.4.0 for the second time: go vet: cache/key_test.go:41.` with Retry and Stop… |
| Epics (5) | orrery-followups `1 of 3 proven` / `building · 20 min · 0 of 3 units integrated` + step rows T1, T2 + `1 steps ended`; rota-export `3 of 6 proven` / `building · 49 min · 2 of 7 units integrated` / blocker `waits on your answer · 2 min` + step rows T2 (doing) and T1 (`held · Paused by planner turn 4 …`) + `5 steps ended`; tide-alerts `0 of 5 proven` / `contract written · 26 min`; offline-tables `6 of 6 proven` / `stopped · 9 min · 3 of 3 units integrated`; orbit-labels `4 of 4 proven` / `releasing · 4 min · 3 of 3 units integrated` |
| Just ended (1) | tide-cache-drop-tables-older-than-a-week / `stopped · 5 h 29 min ago`; `1 ended earlier ›` |
| New since you looked (2) | graft-parser `orchard` `20m`; moon-phase-api `almanac` `39m` |
| Working (2) | lamp-telemetry `lighthouse` `9m`; harvest-report `orchard` `3m` |
| Seen (5) | rota-planning `51m`; ephemeris-import `almanac` `2h` (session row, no host); docs-examples `5h`; tide-tables `2d`; ci-cache `3d` |
| footer | hosts `halyard`, `sextant`, `tarn`; meters button `vermissian cpu 34% m`; `? keys` |

**Gaps.**
- No "contract waiting to be accepted" state or need (wf need-contract). orreryd stores an epic only once its contract is accepted (EpicContract.accepted doc); nearest: none. The fixture invents outcome `contract written` so the line has a state word.
- No interrupted-session need: HeadlessSession.state `interrupted` exists, but the client ranks only `waiting` rows as needs (rows.ts:170-172). Client rule, not an API gap.
- Epic need age (`waiting 2m`): EpicQuestion.asked and EpicReleaseProof.at exist; the client draws neither on the need. Client.
- Epic line exception sentence for a non-blocking event (followups "The judge sent judge-head-tail back…", offline "Release step 2 failed… 9 min"): no field for an epic's current exception; nearest EpicStepVerdict (unmet) and EpicReleaseProof.reason.
- Release progress phrase (`pushing tag v0.46.0, step 2 of 3`): derivable from EpicReleaseProof.state; client.
- `N running` on the epic line: derivable from Epic.runningSteps; client.
- Stopped epic in Just ended: orreryd keeps a `stopped` epic live (internal/server/epics.go:369), so it never reaches Just ended; the fixture forces `live: false`. Its ended sentence (`Stopped: superseded by offline-tables.`) is EpicReport.why, available only from Get.
- Working row line `Bash go test ./internal/telemetry/... 0:42 · adding retry backoff`: Pane.tool carries name, input and start; the client draws only the summary. Client.
- Lamp age reads `9m` (wf `1m`): one Pane.seen drives both the row's unseen rank and the reader's since-you-looked rule, so a working row that shows the rule at 14:32 must have `changed ≤ seen ≤ 14:32`.
- Host footer: Host.Status describes only the serving host; wf shows stats for four hosts. Nearest: HostStatusResponse.host/stats.
- Headless session rows carry no host (HeadlessSession has `dir` only).
- Brand `epics` count and jump buttons: client.
- Live draws step rows and a `N steps ended` line under busy epics (epics.ts `blockOf`); the wf never draws them.
- tide-cache slug lengthened to 40 characters on purpose (wf `tide-cache`).

### 4.2 epic-rota-decision

**Wireframe data.** `EPICS.rota` epicnow.js:305-370 (reqs 308-315, events 316-328, moment[0] 331-367 with `exc`
335 and units 337-360); `X.rota` epicnow.js:718-734 (para 720, contract 721-725, tests 726-728, rel 729);
`ASK.rota[0]` epicnow.js:823-828 (`title: 'Bump the embedded tz database?'`, `id: 'D7'`, three opts, `rec: 1`,
`confirm: 'Record the decision and resume tz-fixtures'`); title pills from `facts()` epicnow.js:1056-1062
(`building · 49 min`, `waits on your decision · 2 min`, Talk, Pause, Stop…).

**Calls.** Epics/Get `lighthouse/rota-export` (polled), Epics/History `lighthouse/rota-export`.

**Expected live render** (epicpage.tsx):

| part | content |
|---|---|
| title row | `‹`, crumbs `orrery › rota-export`, state `building · 49 min`, blocker `waits on your answer · 2 min`, acts `Talk`, `Pause`, `Stop…` |
| summary | `button.summary` with EpicsGetResponse.summary |
| now block | `p.now` `T2 · implementing · Adding the refuse-on-existing check and its test.`; answer form: `p.question` Q1 text, textarea, `Answer`; `Stop…` |
| Red proof | 6 cards R1-R6 (`rid`, `text`, `kind`, `p.conclusion` except R6); R1-R5 each with one run head `red` `captured` `ended` `fails` `passed` `exit 1` `3e1f2ab6b681` + `stdout` toggle (no stderr toggle) |
| implementation | orrery 7 wedges (T1 mine implementing, T2 run implementing, T3 acc integrating, T4/T5 integrated, T6/T7 planned), centre `2 of 7` / `integrated`, arcs R5 R4 R2 R1 R3 R6; Now `T1 · implementing · 2 min`, `T2 · implementing · 4 min`; Next `T6 · The README documents …`, `T7 · Every export writes …`; Just happened `14:40 rota-handler: the engine started merging…`, `14:39 turn 4 · succeeded · Raised Q1 …`, `14:37 Review finding F1 confirmed …`, `+9 earlier` |
| Green proof | reached; R1-R3 cards with a green run each |
| Release proof | dim; two steps (`Merge the epic branch to main` / `lighthouse main` / `revert the merge`; `Deploy lighthouse to halyard` / `halyard` / `redeploy the previous build`), state `waiting`; `p.then` |
| Report | dim heading |

**Gaps.**
- Question options, the recommended option, the requirements it bears on, the decision id (D7) and the decisions.md line, the confirm label: EpicQuestion has `id, text, turn, asked` only. None.
- The unit a question paused (`tz-fixtures is paused on your decision`): none; nearest the held step's EpicStep.reason.
- State word: orreryd sends its phase (`accepted`) as Epic.outcome, which reads `accepted · 49 min` and hides Pause/Stop… (epicpage.tsx:89). The fixture sends `""`.
- Contract door line (`accepted 13:50 · serious care · 6 requirements`): contract.accepted, contract.care and requirements exist; client.
- Now sentence (`Building. Two units are being worked on, and one waits on you.`) and the live marker (`as of 14:41 · updated 13 s ago`): none.
- Per-card proof status (`✗ fails at base`, `✓ proven`, `not yet`, `review`) and the failing line (`store_test.go:27: no such table: shifts`): no per-requirement failing line; nearest EvidenceRun (log via Evidence/Artifact), EpicRequirement.evidence for the test name.
- Unit slugs (tz-fixtures, export-cli): EpicBuildTask.id is `T<seq>`; nearest EpicBuildTask.title.
- Unit phase phrase (`waiting on you: a decision`, `fixing a review finding`, `integrating`): none; nearest EpicStep.doing / reason.
- An integration in progress (rota-handler glowing, in Now): the client places a task whose last step ended `done` at integrating with tone `acc` (phases.ts:91); no field marks an engine merge running. None.
- Bounced slices (`sent back`) and phase labels on the ring: SliceState is passed/now/ahead only.
- A task resumed after a review: phases.ts `placementOf` reads the task's last step, and steps are oldest first, so a reviewer that started after the implementer would place T2 at reviewing. The fixture leaves `st-rota-t2-review` out of EpicsGetResponse.steps and shows it only in Epics.History.
- `Grew at 14:10: +2` units: none; nearest the turn 3 history entry.
- Next as a sentence (`export-docs and rota-audit are outlined and start after these.`): none (wf NEEDS `next`: Epic.next_actions).
- Just happened kinds (integrate, decision raised, review finding) and their icons: EpicHistoryEntry has turn, trigger, step, evidence, decision, tell; the fixture carries these rows as tells and a turn.
- Epic review line before the review (`after the last unit integrates`): EpicReviewVerdict exists only once a review ran.
- Section count phrases (`5 of 5 tests fail at base 3e1f2ab`, `2 steps · not started`): client.

### 4.3 epic-holder-released

**Wireframe data.** `EPICS.holder` epicnow.js:473-503 (reqs 475-480, events 481-488, `ended` 491-501: `Released
v0.45.0.`, `took: '1 h 48 min'`, `line: 'All four requirements shipped.'`, shipped rows with refs b91e1ed, 984ef49,
9cc2be1, 9593e0b); `X.holder` epicnow.js:770-781 (`relDone` 778-779 with notes `github · 09:44 · signature good`,
`run 5490 · 09:51`, `orrery --version · 09:52`); release cards via epicnow.js:1224; epic review `clean`.

**Calls.** Epics/Get `orrery/holder-scopes` (on cursor, once), Epics/History.

**Expected live render.** Title `orrery › holder-scopes`, state `released · 4 h 49 min`, no blocker, acts `Talk` only.
Summary button. No now sentence. Red proof (reached) R1-R4, R1-R3 with red runs. Orrery 4 integrated wedges, `4 of 4`;
Now and Next empty headings; Just happened `09:52 Released v0.45.0. vermissian runs it.`, `09:44 Tagged v0.45.0 …`,
`09:39 orrery started the release …`, `+3 earlier`. Green proof R1-R4 each with a passed green run (R4 supplied).
Handover section (handover.tsx; drawn because `reviews` is non-empty): `phase released`, `REVIEW 1`, one item with
span `final review`, span `holds`, `p` `0 findings.`, `p` `step st-holder-final-review`.
Release proof (reached): three `li.release-step` with step (step 1 the full sentence), target, undo, state `passed`,
evidence, reason; `p.then`. Report: outcome `released`, `shipped R1, R2, R3, R4`, why.

**Gaps.**
- Report rows per requirement (task slug, sentence, commit): EpicReport has requirement ids only; nearest EpicBuildTask.title and the green EvidenceRun.revision.
- Release version and duration (`Released v0.45.0`, `ran 1 h 48 min`): none; nearest EpicReport.outcome, contract.accepted and Epic.phaseAt.
- Now sentence for an ended epic (`Released v0.45.0. All four requirements shipped.`): none; nearest EpicReport.why.
- Epic review line: EpicReviewVerdict exists (verdict `holds`, wf `clean`); the client draws it in Handover, not under Green proof. Client.
- Release step mark (✓ circle) and step count phrase (`3 of 3 steps done · 09:52`): EpicReleaseProof.state/at exist; client.
- Release step 1 is a full sentence on purpose (wf `Push tag v0.45.0, signed`) to exercise the release-step grid.

### 4.4 contract-tidealerts-open

**Wireframe data.** `X.tidealerts.contract` epicnow.js:737-742 (`care: 'severe'`, `steel: 'The steelman pass closed 3
findings.'`, line, asked, fails F1-F3, `added: [['A dry-run flag that logs instead of sending', 'why: F1 · cost: one
flag', 'keep']]`, `ask: { id: 'D1', q: 'Which forecast counts as “above 4.5 m”?', opts: ['The harbour authority’s
table', 'The national model'] }`); rel epicnow.js:743; `contract()` epicnow.js:1394-1423 (decision line card, folds
with counts `5 requirements`, `3 premises`, `2 entries`, `2 alternatives`, `1 has no undo`, `1 open`, footer `2 choices
left`, `Ask for a change`, Accept); pill `waiting for you · 26 min` (epicnow.js:1063).

**Calls.** Epics/Get `tidepool/tide-alerts` (polled).

**Expected live render** (contract.tsx). Title row `orrery › tide-alerts › contract`, no state, no acts.
`h2 tide-alerts`; `p.approved`; sections in order with every list expanded: intent; failures F1-F3 with ratings and
responses, `p.care severe`; additions (one row: `alerts/send.go`, what, `F1`, `one flag`); requirements R1-R5 with
evidence; premises P1-P3; exclusions (2); approach + alternatives (2); release steps (2) + releaseThen; checks;
`p.accepted` reads `accepted ` with no time (clock(0) is empty).

**Gaps.**
- Unaccepted contract: no API state; EpicContract.accepted is when orreryd stored the epic. The fixture serves `accepted: 0`, and the page still says `accepted`.
- Contract decisions (D1 with options, `1 decision open`, `Decisions I need from you`): none on EpicContract; EpicQuestion is per epic, not per contract.
- Keep/Drop on an addition: none.
- Care set by which failure and the steelman note: none (EpicFailure.rating and EpicContract.care exist).
- Ask for a change and Accept: no RPC in the Epics service (Pause, Resume, Stop, Retry, Answer, Seen only).
- `waiting for you · 26 min` pill: Epic.phaseAt exists; the contract title row draws no state. Client.
- Folded sections with counts, `1 has no undo` (from EpicReleaseStep.undo): client.
- The wf draws no contract title; live `h2` reads EpicContract.title.

### 4.5 contract-rota-accepted

**Wireframe data.** `X.rota.contract` epicnow.js:721-725 (`accepted: '13:50'`, `care: 'serious'`, `decided: 6`,
`added: [['An audit-log line for every export', 'why: F1 · cost: one unit', 'keep']]`); rel epicnow.js:729;
`contract()` epicnow.js:1394-1423 in its read-only form (no footer, Keep selected and disabled, `none open · 6
recorded`); pill `accepted 13:50` (epicnow.js:1063).

**Calls.** Epics/Get `lighthouse/rota-export`.

**Expected live render.** As 4.4 with the rota contract of §2.3: `p.care serious`, one addition row, requirements
R1-R6, two release steps, `p.accepted` `accepted 13:50 · base 3e1f2ab6b681479f7e6dc3c139194a1ad6b8025b`.

**Gaps.**
- Keep chosen on the addition: none.
- Decisions recorded on the contract (`6 recorded`): none on EpicContract; nearest Epics.History `decision` entries.
- `accepted 13:50` as a title-row pill: EpicContract.accepted exists; live draws it as the article's last line. Client.

### 4.6 phase-rota-export-cli-checking

**Wireframe data.** export-cli unit epicnow.js:343-351 (`clock: 252`, `tag: 'fixing F1'`, `checkNote`, `reason` F1,
`mon: 'model time 22 of 60 min · last event 12 s ago'`); `OUT.rota['export-cli'][2]` epicnow.js:806-809 (`sum: 'Round 1
failed the gate. Round 2 passed the five checks and the gate in 3:58, and the done-judge ruled the goal met.'`, `gate`
pre, `judge` text, row `Round 1 · gate just check · exit 1 · 14:19` with pre); task acts `Hold`, `Stop…`
(epicnow.js:1040-1047); rows implementing (run), checking (open, `again`), reviewing (`bounce`), integrating (ahead).

**Calls.** Epics/Get (held), no new call.

**Expected live render** (phaseoutput.tsx). Title row `orrery › rota-export › T2 · checking`, acts `Hold`, `Stop…`
(st-rota-t2-impl runs, not held). `h2.task-title` `Export refuses to overwrite an existing file.`, `p.requirements`
`R4`. Rows: implementing `now` summary `Adding the refuse-on-existing check and its test.` time `4 min`; checking `ahead`
(open, disabled head, empty summary); reviewing `ahead`; integrating `ahead`. Open body (Checking): round 1, ran
`checks, gate`: five checks `✓`, gate `check failed`, `p.gate-detail` `exit 1: cli/export_test.go:88: want ErrExists, got
nil`, `pre.gate-stdout` = <T> (multi-line), `pre.gate-stderr`; round 2, ran `checks, gate, judge`: five `✓`, gate `check
passed` with its stdout pre, judge `met` + reason. Then `Open the session ›`.

**Gaps.**
- Task slug in the crumb (`export-cli · checking`): EpicBuildTask.id is `T2`.
- Phase sentence (`ph-say`) and task story (`tk-story`): none; nearest EpicStep.summary / doing.
- Phase states `passed in an earlier round · runs again` and `F1 confirmed, sent back`: SliceState is passed/now/ahead, derived from the task's single place. None.
- Reviewing summary: the reviewer step is not in EpicsGetResponse.steps (see 4.2), so the reviewing row is empty; the finding itself is in EpicBuildTask.reviews.
- Title-row pill with the running clock (`fixing F1 · 4:12`): EpicStep.stageStarted exists; the phase title row draws no state. Client.
- Gate block caption exit code and time (`exit 1 · 14:19`): EpicStepCheck has no exit code or time; nearest EpicStepCheck.detail.
- Engine checks as chips and the judge's code spans: data exists (EpicStepCheck, EpicStepVerdict.reason as plain text); client styling.
- Model-time monitor line: none.

### 4.7 worker-rota-export-cli-implementer

**Wireframe data.** `STEP` epicnow.js:1339-1348 (Brief fold `from planner turn 2 · goal, 2 owned files, acceptance`;
`9 tool calls` `Read ×3, Write ×2, Edit ×2, Bash ×2`; RETURN · ROUND 1 `done at commit 7c1e9aa` chips schema/head/
ancestry/clean/owned ✓, gate ✗, `Gate exit 1. Sent back with the gate's output.`; `4 tool calls` `Read, Edit ×2, Bash`;
RETURN · ROUND 2 `done at commit e02b6c4` chips all ✓ + `judge: met ✓`; REVIEW FINDING F1 · CONFIRMED 14:37; Edit
`cli/export.go`; live Bash `go test ./cli/... -run Export` with clock; streaming reply); send box `Nudge export-cli`
(`convo()` epicnow.js:1352-1356).

**Calls.** Sessions/WatchConversation `{ id: "st-rota-t2-impl" }`.

**Expected live render** (workersession.tsx, reader.tsx). Title row `orrery › rota-export › T2 · implementing ›
session`. Reader: `button.fold.brief` `Brief · from the engine`; `fold calls` `9 tool calls · Read ×3, Write ×2, Edit ×2,
Bash ×2 ✓`; `.card.bad` `Checks · round 1 · from the engine`, chips `schema ✓ head ✓ ancestry ✓ clean ✓ owned ✓ gate ✗`,
note `Gate exit 1. Sent back with the gate's output.` with the rest folded; `4 tool calls · Read, Edit ×2, Bash ✓`;
`button.fold.engine` `Engine · Review finding F1, confirmed: …`; call row Edit `cli/export.go` (button, has result);
call row Bash with the long input (no result); live row (`gut live`, reply text, caret). StepBox: `Nudge T2: it
interrupts the turn, then prompts`.

**Gaps.**
- The worker's own return (`RETURN · ROUND n`, `done at commit 7c1e9aa`): no structured field; the real transcript carries it as a JSON reply, which the live reader prints raw. The fixture omits it and relies on the engine send-back card.
- Round 2 card: a round that passes sends nothing back into the session, so no card exists for it.
- `judge: met ✓` chip: EpicStepVerdict exists; the card draws only checks. Client.
- Review finding as its own card: the send-back prompt has `sender: "engine"`, `round: 0`, which the client draws as an engine fold. No finding fields on ConversationItem.
- Brief origin (`from planner turn 2`): ConversationItem.sender is `brief` only; none.
- Running-call clock (`0:08`) and title pill (`working 4:18`): ConversationItem.time and EpicStep.stageStarted exist; client.
- Crumb `implementer session`: EpicStep.role `implementer` exists; client draws `session`.

### 4.8 talk-rota-rotaplan-docked

**Wireframe data.** `X.rota.talk[0]` epicnow.js:730-731 (`label: 'rota-planning'`, `where: 'tmux pane · halyard'`,
`at: '13:50'`, `said: 'Why did csv-writer need a retry?'`, `reply: …`); `talkConvo` epicnow.js:1371-1376 (context
chip `rota-export in context`, you message `YOU · 13:50`, tool row `epic_get rota-export ✓`, reply); dock header in
app.js:202 (`dk-h`: name, where, epic link chip, ×); composer placeholder `Message rota-planning`.

**Calls.** Epics/Get, Epics/History (rota page), Panes/WatchConversation `{ pane: "%14" }`.

**Expected live render** (talkdock.tsx). Epic page as 4.2 with class `docked`; `aside.dock`: `h1 rota-planning`,
close `×`; reader: `13:50` you card `YOU` + text; call row `epic_get` `rota-export` (button); reply prose; no
since-you-looked rule (the dock marks only what arrives after it opened); composer `Reply to rota-planning`
(pane state `done`).

**Gaps.**
- Dock where line (`tmux pane · halyard`): EpicSession.pane is the pane id; the host is on Pane.host via Panes.List. Client.
- Epic link chip: client.
- Context chip (`rota-export in context`): no field says a session holds the epic in context; nearest EpicSession.call.
- `YOU · 13:50` label: ConversationItem.time exists (drawn in the gutter). Client.

### 4.9 talk-rota-menu

**Wireframe data.** `talkPicker` epicnow.js:1367-1368 over `X.rota.talk` epicnow.js:730-733: `rota-planning` /
`tmux pane · halyard` / `created the epic · 13:50`; `lamp-telemetry` / `tmux pane · halyard` / `asked what it changes in
internal/ · 14:12`; `+ New session in ~/code/lighthouse` / `halyard` / `starts with rota-export in context`.

**Calls.** none beyond the epic page.

**Expected live render** (talk.tsx). `ul.talk` in the title row's acts: `rota-planning · epic_get · 13:50`,
`lamp-telemetry · epic_get · 14:12`, `New session in lighthouse`.

**Gaps.**
- Per-session where (`tmux pane · halyard`) and last action as a phrase (`created the epic`): EpicSession.call is a tool name; none for the phrase; host via Panes.List.
- New-session folder as a path and its host: EpicsGetResponse.repository holds the absolute path; the client draws its last element. Client.
- Starting a session with the epic in context: Control.Start takes `dir, prompt, model, id`, no epic. API gap (the client sends a prompt naming the epic instead, talk.tsx:6-8).
- No `t` key in the live client: the checker clicks Talk.

### 4.10 session-lamp-working

**Wireframe data.** `SESS.lamp` epicnow.js:550-551 (`tool: ['Bash', 'go test ./internal/telemetry/...', 42]`);
pill from `facts()` epicnow.js:1052-1053 (`working <72 s> · Bash go test … <42 s>`); pane HTML index.html:48-68
(compaction `compacted · auto · 611k`, `6 tool calls` with one failed dot, 14:27 reply with `pre.hl` Go block, 14:31
`YOU · FROM YOUR PHONE`, `since you looked`, 14:32 Edit + Bash calls with outputs, live stream line, queue of 2,
composer with `context 15%`).

**Calls.** Panes/WatchConversation `{ pane: "%11" }`.

**Expected live render** (app.tsx reader section). Head `h1 lamp-telemetry`, where `lighthouse · halyard`, pill
`working` + `Bash` + `0:42`. Reader: `14:02` compaction rule `compacted · auto · 611k` (summary folded); `6 tool calls ·
Read, Write ×2, Bash ×2, Edit ✗`; `14:27` reply with paragraph, `pre` Go block, paragraph with inline `Flush`; `14:31`
you card; `new` rule `since you looked`; `14:32` call Edit; `14:32` call Bash (long input); live row with caret.
Composer: queue `waiting` ×2, textarea `Queue for lamp-telemetry; it lands at Claude's next step`, edge working.

**Gaps.**
- Pill shows one clock: Pane.tool.start (tool) and Pane.changed (state) both exist; the client draws only the tool clock. Client.
- `YOU · FROM YOUR PHONE`: no field records where an owner prompt came from (ConversationItem.sender is `owner`). None.
- Per-call result dots on the fold: results carry `error`; client.
- Streaming text on a pane: WatchConversationResponse.unfinished exists on the type, but the server never sets it for a pane stream (conversation.ts:20-23). The fixture sets it.
- Queue header (`queued 2 · ↑ edits the newest`): ConversationState.waiting exists; client labels each `waiting`.
- `context 15%`: ConversationState.context exists; the composer does not draw it. Client.
- The 14:32 Bash input is lengthened on purpose (wf `go test ./internal/telemetry/... -run 'Backoff|Jitter'`).

### 4.11 session-beacon-question

**Wireframe data.** `SESS.beacon` epicnow.js:541-543 (`state: 'waiting'`, `age: '12m'`, `opts: ['Composite index',
'Partial index', 'Other']`); pane HTML index.html:69-80 (14:24 you, `5 tool calls` `Grep, Read ×3, Bash`, rule, 14:29
reply with list, `qcard` `CLAUDE IS ASKING` `Which index should I add?` options 1-3 with descriptions); pill `waiting
12m` (epicnow.js:1053).

**Calls.** Panes/WatchConversation `{ pane: "%12" }`.

**Expected live render.** Head `h1 beacon-schedule`, where `lighthouse · halyard`, pill `waiting`. Reader: `14:24` you
card; `5 tool calls · Grep, Read ×3, Bash ✓`; rule `since you looked`; `14:29` reply with `ol` of two items; question
card `Which index should I add?` with three option buttons (label + description). Composer `Answer beacon-schedule`,
edge waiting. In the list, the row's chips `1 Composite index`, `2 Partial index`, `3 Something else`.

**Gaps.**
- Pill age (`waiting 12m`): Pane.changed exists; the pill draws no age for a waiting row. Client.
- wf list chip 3 reads `Other` while the wf card reads `Something else`; the fixture serves one label, `Something else`.
- The card cap `CLAUDE IS ASKING`: client.

---

## 5. Selector anchors and checker notes (bind map.json)

map.json's live selectors depend on these fixture ids. Do not change one without changing the other.

| anchor in map.json | fixture object (section 2) | used by |
|---|---|---|
| `.list .b-row.epic[data-key^="epic:"][data-key$="rota-export"]` | Epic id `lighthouse/rota-export` (row key `epic:lighthouse/rota-export`) | list-home epic line components |
| `.list .b-row[data-key="pane:sextant:%13"]` | Pane `%13` graft-parser on sextant | list-home session row, label, text |
| `.list .b-row[data-key="pane:halyard:%12"]` | Pane `%12` beacon-schedule on halyard | list-home Needs-you option chip |

Other live selectors in map.json are structural (first matching item via `:nth-child(1 of …)`). They assume the
conversation and list order given in section 2: worker `st-rota-t2-impl` has exactly one `.card.bad` and its first tool
group is `9 tool calls`; the Talk dock conversation (`%14`) has one you message, one call and one reply; lamp (`%11`)
has its first timed item at 14:02 (the compaction) and its first reply at 14:27; only one Needs-you row carries options.

Checker notes:
- Wireframe fonts. Several wireframe values are resolved from `ch` of Atkinson Hyperlegible Next (summary paragraph
  max-width 861.976px, task story 732.883px, phase sentence 746.498px at 1600). index.html loads that face from
  fonts.googleapis.com; a checker must vendor it (and Maple Mono NF, already local) or those reference values drift.
- talk-rota-menu has no hash route for the open menu: load `#go=epic:rota`, then press `t`, then `clock.runFor(300)`.
- Grid columns (`grid-template-columns`) are only listed where every track is fixed px or a fraction of a container that
  parity also fixes. Content- or `ch`-sized tracks (phase head, timeline rows, unit rows, report rows, failure rows,
  added rows, Talk menu items) are left out because the live mono face has different metrics.
- `width`/`height` are listed only for fixed-size drawings and controls (kind icon 24, epic glyph 28, back button 28,
  dock close 26, timeline icon 20, release mark 22, phase marker width 14, the orrery drawing, the dock and the Talk
  menu, whose widths are set in wireframe CSS).
- Wireframe selectors were checked at 1600x1000 and 1000x1000 with `page.clock.install({ time: new Date('2026-11-14T14:41:00') })`
  and `clock.runFor(500)`: each matches exactly one rendered element inside `.mock.b` in its scenario.
