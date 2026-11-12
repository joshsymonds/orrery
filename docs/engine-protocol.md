# Engine protocol findings

This page records the stream-json protocol observed in a live recording against Claude Code 2.1.321. Each factual claim cites a fixture line as `internal/engine/capture/testdata/<file>:<line>`, with 1-based line numbers. Source claims cite the Go file and line. Fixture values are sanitised: strings outside the allow-list read `redacted`, an unlisted number reads 0 and an unlisted boolean reads false (`internal/engine/capture/sanitise.go:167`, `:169` and `:171`), and ids read like `uuid_1`.

## 1. Recording

The init line of the two-turns probe reports `claude_code_version` 2.1.321 and `model` `claude-haiku-5-5` (`internal/engine/capture/testdata/two-turns.out.ndjson:4`). The argument vector asks for the `haiku` alias (`internal/engine/capture/probe.go:176`), and the init line shows the resolved model name.

The recording was made by running `enginecapture record -raw DIR`, then `enginecapture sanitize -raw DIR -out DIR` (`internal/engine/capture/cli.go:51` and `internal/engine/capture/cli.go:53`). These two steps are not visible in the fixtures.

Each probe runs with a fixed argument vector (`internal/engine/capture/probe.go:169-190`). It includes `--input-format stream-json`, `--output-format stream-json`, `--include-partial-messages`, `--model haiku`, `--max-turns 2`, `--settings {"disableAllHooks":true}`, `--setting-sources ""`, `--strict-mcp-config`, `--mcp-config {"mcpServers":{}}`, and `--no-session-persistence`. The probe's permission mode and tool list are passed as `--permission-mode` and `--tools`. A probe that answers permission requests adds `--permission-prompt-tool stdio` (`internal/engine/capture/probe.go:186-187`). The child runs in a new empty directory (`internal/engine/capture/probe.go:155`) and receives only the allow-listed environment names `HOME`, `PATH`, `USER`, `LANG`, `TERM`, `XDG_RUNTIME_DIR`, `XDG_CONFIG_HOME`, and `XDG_STATE_HOME` (`internal/engine/capture/probe.go:207`). `HOME` is passed through as the recording machine has it, so each probe runs with the real home directory and user-level plugins, skills, agents and commands load. The init line lists entries under `agents`, `skills`, `plugins` and `slash_commands` (`internal/engine/capture/testdata/two-turns.out.ndjson:4`), but the fixture keeps one redacted entry per key (`internal/engine/capture/sanitise.go:244`), so it shows neither how many entries loaded nor which of them are user-level.

The sanitiser keeps allow-listed values and replaces the rest. A string outside the allow-list becomes `redacted` (`internal/engine/capture/sanitise.go:167`), as in `"text":"redacted"` (`internal/engine/capture/testdata/two-turns.in.ndjson:2`). A number outside the allow-list becomes 0 (`internal/engine/capture/sanitise.go:169`), and a boolean outside it becomes false (`internal/engine/capture/sanitise.go:171`). Each id becomes `<prefix>_<n>`, numbered from 1 for each probe (`internal/engine/capture/sanitise.go:350`). One sanitiser serves every line of a probe (`internal/engine/capture/sanitise.go:14-15`), so the same id appears in the input and output files: `req_1` is in `internal/engine/capture/testdata/two-turns.in.ndjson:1` and `internal/engine/capture/testdata/two-turns.out.ndjson:1`. Keys of the answer object become `key_<n>` (`internal/engine/capture/sanitise.go:201`), as in `key_2` at `internal/engine/capture/testdata/question.in.ndjson:3`. A line whose `type` is `rate_limit_event` is dropped (`internal/engine/capture/sanitise.go:98`). No fixture line carries that type.

Five further rules shape the values. An array whose elements no allow-list entry names keeps only its first element, and the rest are consumed unwritten (`internal/engine/capture/sanitise.go:244`, with the named-element test at `internal/engine/capture/sanitise.go:311`). Durations, `total_cost_usd` and the usage token counts are not on the allow-list (`internal/engine/capture/sanitise.go:356`), so they read 0 (`internal/engine/capture/sanitise.go:169`), as `"duration_api_ms":0` and `"total_cost_usd":0` do at `internal/engine/capture/testdata/two-turns.out.ndjson:13`. `num_turns` stays on the list (`internal/engine/capture/sanitise.go:360`), so `"num_turns":1` at that same line is a recorded value. `credential_expires_at` is left out (`internal/engine/capture/sanitise.go:402`), and the `account` object is written `{}` (`internal/engine/capture/sanitise.go:403`), as at `internal/engine/capture/testdata/two-turns.out.ndjson:1`. Model and tool names are kept only from fixed lists (`internal/engine/capture/sanitise.go:411-413`, checked at `internal/engine/capture/sanitise.go:159`): the model `claude-haiku-5-5` and the tools `AskUserQuestion` and `Write`. So `"name":"AskUserQuestion"` at `internal/engine/capture/testdata/question.out.ndjson:22` is kept, and any other tool name reads `redacted`. `subagent_stats.by_type` is a data-keyed map (`internal/engine/capture/sanitise.go:397`), so its keys become `key_<n>`; the fixtures write it `{}`, as at `internal/engine/capture/testdata/two-turns.out.ndjson:13`, and `modelUsage` in that line shows `key_1`.

To rerun the check, run `just engine-accept` (`justfile:149`), and pass `-keep DIR` to keep the raw output (the recipe forwards the arguments at `justfile:149`, and the flag is at `internal/engine/capture/cli.go:119`). It runs `enginecapture accept`, which reruns the hooks-off probes live into a temporary directory and checks each probe's stdout (`internal/engine/capture/cli.go:116` and `:148`). It then runs the real-settings probes with the owner's settings (`internal/engine/capture/cli.go:156`, and the probes at `internal/engine/capture/settings.go:19`). With `-keep DIR` the raw output goes into DIR, which is never removed. Accept writes no fixtures. The fixtures come from `record` and `sanitize`.

## 2. Handshake

Go opens every probe with an initialize request. Its body keys are camelCase. `supportedDialogKinds` is null, `hooks` is an empty object, and `systemPrompt` is an empty array (`internal/engine/capture/testdata/two-turns.in.ndjson:1`):

```json
{"type":"control_request","request_id":"req_1","request":{"subtype":"initialize","supportedDialogKinds":null,"hooks":{},"systemPrompt":[]}}
```

The CLI answers with a success `control_response` under the same request id, before any turn line (`internal/engine/capture/testdata/two-turns.out.ndjson:1`). Its `response.response` object holds `current_permission_mode`, here `default`. The sibling keys `pending_permission_requests` and `pending_user_dialog_requests` sit one level up, at `response.`, and both are empty arrays on that line. The reply's own keys mix spellings: snake_case such as `current_permission_mode` beside camelCase such as `argumentHint` and `displayName` on the same line, so a decoder reads each field by the spelling the fixture shows.

## 3. Turns

Go writes each prompt as a user envelope with a `uuid`. The first prompt has `uuid_1` (`internal/engine/capture/testdata/two-turns.in.ndjson:2`), and the second has `uuid_15` (`internal/engine/capture/testdata/two-turns.in.ndjson:3`):

```json
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"redacted"}]},"uuid":"uuid_1"}
```

The CLI echoes that uuid as `command_uuid` in three `command_lifecycle` lines, with states `queued`, `started`, and `completed` (`internal/engine/capture/testdata/two-turns.out.ndjson:2`, `:3`, `:14`):

```json
{"type":"command_lifecycle","command_uuid":"uuid_1","state":"queued","uuid":"uuid_2","session_id":"session_1"}
```

The same uuid is also listed in `user_message_uuids` on the init, status, and result lines, each set to `["uuid_1"]` (`internal/engine/capture/testdata/two-turns.out.ndjson:4`, `:5`, `:13`).

Each turn emits, in order, a system init line, a system status line with `status` `requesting`, the stream events for `message_start`, `content_block_start` and `content_block_delta`, an assistant line, the stream events for `content_block_stop`, `message_delta` and `message_stop`, and a result line. The first turn spans lines 4 to 13 and the second spans lines 17 to 26 (`internal/engine/capture/testdata/two-turns.out.ndjson:4-13`, `:17-26`). All of them carry session `session_1`.

The second prompt's `queued` line is at line 15, after the first turn's result at line 13 (`internal/engine/capture/testdata/two-turns.out.ndjson:13`, `:15`). The input files carry no timing, so this output order is the only evidence in the recording for when prompt two was sent.

## 4. Interrupt

Go sends the interrupt as its own control request (`internal/engine/capture/testdata/interrupt.in.ndjson:3`). The output shows the first assistant text already streaming (`internal/engine/capture/testdata/interrupt.out.ndjson:6-8`) before the receipt arrives:

```json
{"type":"control_request","request_id":"req_2","request":{"subtype":"interrupt"}}
```

The receipt is a success `control_response` under the same id with body `{"still_queued":[]}` (`internal/engine/capture/testdata/interrupt.out.ndjson:9`).

The stream then carries a `user` line with text content (`internal/engine/capture/testdata/interrupt.out.ndjson:11`) and a `content_block_stop` (`:12`). The `message_stop` at line 14 has an `abandoned_blocks` object with two keys, `api_message_id` and `from_block_index`, as `{"api_message_id":"redacted","from_block_index":0}` (`internal/engine/capture/testdata/interrupt.out.ndjson:14`). The `from_block_index` value `0` is the sanitiser's 0 for an unlisted number (`internal/engine/capture/sanitise.go:169`), not a recorded value, because `abandoned_blocks.from_block_index` is not on the allow-list. The result at line 15 has `subtype` `error_during_execution`, `is_error` true, `terminal_reason` `aborted_streaming`, and `num_turns` 2 (`internal/engine/capture/testdata/interrupt.out.ndjson:15`). The command lifecycle ends with state `cancelled` (`internal/engine/capture/testdata/interrupt.out.ndjson:16`).

## 5. Mode change

Go sets the mode with a `set_permission_mode` request (`internal/engine/capture/testdata/mode-change.in.ndjson:2`), whose `mode` is `acceptEdits`. The CLI replies with body `{"mode":"acceptEdits"}` (`internal/engine/capture/testdata/mode-change.out.ndjson:2`). A system status line follows with `permissionMode` `acceptEdits` and `status` null (`internal/engine/capture/testdata/mode-change.out.ndjson:3`).

The next init line reports `permissionMode` `acceptEdits` (`internal/engine/capture/testdata/mode-change.out.ndjson:6`). The probe's argument vector says `default` (`internal/engine/capture/probe.go:91`), and the mode change is set in `internal/engine/capture/probe.go:93`.

## 6. Question under bypass

The question probe runs with `--permission-mode bypassPermissions`, `--permission-prompt-tool stdio`, and one tool, `AskUserQuestion` (`internal/engine/capture/probe.go:101-104`, `:186-187`). Its init line reports `permissionMode` `bypassPermissions` (`internal/engine/capture/testdata/question.out.ndjson:4`).

The assistant's `tool_use` for `AskUserQuestion`, with id `toolu_1`, is at `internal/engine/capture/testdata/question.out.ndjson:22`. After it, the CLI sends a `can_use_tool` request (`internal/engine/capture/testdata/question.out.ndjson:23`). That request has `tool_name` `AskUserQuestion`, `request_id` `req_3`, and `requires_user_interaction` true. It arrives before the stream's `content_block_stop` (`internal/engine/capture/testdata/question.out.ndjson:24`).

Go answers under the same id `req_3` (`internal/engine/capture/testdata/question.in.ndjson:3`). The response has `subtype` success, and its body has `behavior` `allow` and an `updatedInput`. The `updatedInput` holds the `questions` array, with the same four fields as the request input (`header`, `multiSelect`, `options`, `question`), and an `answers` object with one entry. The fixture stores that entry's key as the sanitised `key_2`, so the recording does not show the key text. Go builds the entry from the request input: the key is the question's `question` text and the value is the `label` of the option chosen, here the first (`internal/engine/capture/answer.go:69`). The snippet below shows `multiSelect` as `false`, which is the sanitiser's value for an unlisted boolean (`internal/engine/capture/sanitise.go:171`), not a recorded value. The `options` array is shown with one entry, its first, because the sanitiser keeps only the first element of an array that no allow-list entry names (`internal/engine/capture/sanitise.go:244` and `:311`). The probe's prompt asks for exactly two options (`internal/engine/capture/probe.go:63-64`), so the fixture cannot show whether the model offered a second.

```json
{"type":"control_response","response":{"subtype":"success","request_id":"req_3","response":{"behavior":"allow","updatedInput":{"answers":{"key_2":"redacted"},"questions":[{"question":"redacted","header":"redacted","options":[{"label":"redacted","description":"redacted"}],"multiSelect":false}]}}}}
```

The CLI then returns a `tool_result` for `toolu_1`, and its `tool_use_result` has the keys `answers` and `questions` (`internal/engine/capture/testdata/question.out.ndjson:27`). The turn ends with assistant text (`internal/engine/capture/testdata/question.out.ndjson:32`) and a result with `subtype` success, `is_error` false, and `terminal_reason` `completed` (`internal/engine/capture/testdata/question.out.ndjson:36`).

## 7. What reaches the host under bypass

In the question probe, the only `control_request` the CLI sends is the `AskUserQuestion` request at `internal/engine/capture/testdata/question.out.ndjson:23`. The probe's tool list holds only `AskUserQuestion` (`internal/engine/capture/probe.go:104`), so no bypass probe used an ordinary tool. Whether ordinary tools prompt under bypass is answered by the real-settings probes in section 12.

## 8. Permission allowed under default

The `Write` tool is requested under default mode. The assistant's `tool_use` has input fields `file_path` and `content` (`internal/engine/capture/testdata/permission-allow.out.ndjson:15`), and a `message_stop` follows at line 18. The CLI then sends this `can_use_tool` request (`internal/engine/capture/testdata/permission-allow.out.ndjson:19`):

```json
{"type":"control_request","request_id":"req_3","request":{"subtype":"can_use_tool","tool_name":"Write","display_name":"Write","input":{"file_path":"redacted","content":"redacted"},"description":"redacted","permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}],"tool_use_id":"toolu_1"}}
```

That request has no `requires_user_interaction` key. Go answers with `behavior` `allow` under `req_3` (`internal/engine/capture/testdata/permission-allow.in.ndjson:3`):

```json
{"type":"control_response","response":{"subtype":"success","request_id":"req_3","response":{"behavior":"allow","updatedInput":{"file_path":"redacted","content":"redacted"}}}}
```

The `updatedInput` has the same two fields as the request input. The fixture redacts their values, so the recording does not show whether the values changed.

The `tool_result` has no `is_error` key and carries a `tool_use_result` (`internal/engine/capture/testdata/permission-allow.out.ndjson:20`). The result at line 29 has `subtype` success, `is_error` false, and an empty `permission_denials` list (`internal/engine/capture/testdata/permission-allow.out.ndjson:29`).

## 9. Permission denied under default

The deny probe receives the same `can_use_tool` request for `Write`, with the same fields and no `requires_user_interaction` key (`internal/engine/capture/testdata/permission-deny.out.ndjson:19`). Go answers with `behavior` `deny` and a `message` (`internal/engine/capture/testdata/permission-deny.in.ndjson:3`):

```json
{"type":"control_response","response":{"subtype":"success","request_id":"req_3","response":{"behavior":"deny","message":"redacted"}}}
```

The `tool_result` has `is_error` true (`internal/engine/capture/testdata/permission-deny.out.ndjson:20`). The model continues: it produces a thinking block and then text (`internal/engine/capture/testdata/permission-deny.out.ndjson:29-44`). The result at line 48 has `subtype` success and one `permission_denials` entry, for `Write` (`internal/engine/capture/testdata/permission-deny.out.ndjson:48`).

## 10. Requests Go could not answer

This recording has none. Every request in it received a response under the same id:

- Go's `initialize` (`req_1`) is answered in each probe's output, on line 1 (`internal/engine/capture/testdata/two-turns.out.ndjson:1`, and the same line in the other five out files).
- Go's `set_permission_mode` (`req_2`) is answered at `internal/engine/capture/testdata/mode-change.out.ndjson:2`.
- Go's `interrupt` (`req_2`) is answered at `internal/engine/capture/testdata/interrupt.out.ndjson:9`.
- The CLI's `can_use_tool` requests (`req_3`) are answered in the input files at `internal/engine/capture/testdata/permission-allow.in.ndjson:3`, `internal/engine/capture/testdata/permission-deny.in.ndjson:3`, and `internal/engine/capture/testdata/question.in.ndjson:3`. Their requests are at `internal/engine/capture/testdata/permission-allow.out.ndjson:19`, `internal/engine/capture/testdata/permission-deny.out.ndjson:19`, and `internal/engine/capture/testdata/question.out.ndjson:23`.

## 11. Host notes

Control requests share the stream with events, so a host reads both from one stream. Write's `can_use_tool` arrives after `message_stop` (`internal/engine/capture/testdata/permission-allow.out.ndjson:18`, `:19`). AskUserQuestion's `can_use_tool` arrives before `content_block_stop` (`internal/engine/capture/testdata/question.out.ndjson:23`, `:24`).

The CLI chooses request ids and the host must echo them. The CLI's `req_3` at `internal/engine/capture/testdata/question.out.ndjson:23` is answered with `req_3` at `internal/engine/capture/testdata/question.in.ndjson:3`.

`orreryd --sessions` hosts each headless session as a manager, a recorder and a writer. The rules they follow are stated in [Headless sessions](contract.md#headless-sessions) of the shared contract, and this section names the code that applies them.

The manager (`internal/enginehost/manager.go`) owns the live sessions and their slots under `--session-cap`. `reserve` takes a slot before a start or resume, and the slot is freed when the session's pump ends (`manager.go:569`, `manager.go:652`). `spawn` starts the process through the login shell, with `bypassPermissions` and `--permission-prompt-tool stdio` (`manager.go:609`, `manager.go:42`, `internal/engine/session/command.go:43`). Each live session has one pump goroutine. The pump is the only reader of the session's events and the only user of its recorder (`manager.go:623`). For each event it first calls `noteEvent`, which tracks the running turn, the open requests, the background tasks and the exit (`manager.go:677`). A pump that records an event queues its ops in order through the writer, and then sends any notice the event calls for (`manager.go:636`, `manager.go:641`).

The recorder (`internal/enginehost/record.go`) turns each event into store ops. It holds the unfinished text of each streaming block in memory, and each delta writes that text to the `engine_text` table (`record.go:40`, `record.go:80`). A finished block writes `engine_block`, and in the same batch removes its unfinished row (`record.go:91`). A request that the session did not refuse writes `engine_request` and the `waiting` state with its question (`record.go:126`, `record.go:145`). A turn end writes `engine_turn_ended` and the `done` state (`record.go:62`). An exit writes `stopped` for a clean exit, and `failed` with the reason otherwise (`record.go:201`, `record.go:213`).

The writer (`internal/enginehost/writer.go`) is the one goroutine that stores engine ops. Each tick, which is 100 ms by default, it sends every queued op of every session to `ApplyEngine` in one transaction (`writer.go:18`, `writer.go:136`, `writer.go:262`). A transaction that fails on an `EngineSet` for a session whose row is gone drops that op with a warning, fails its `Commit`, and sends the rest of the batch again within the same tick (`writer.go:261`, `writer.go:264`, `writer.go:267`). Any other failure keeps all of its ops queued, in order, for the next tick (`writer.go:282`). A session may have 256 queued non-text ops that are not yet stored, and a `Put` beyond that waits (`writer.go:19`, `writer.go:176`). Text ops for one block replace the queued text op of that block (`writer.go:234`). `Commit` returns once its ops are stored (`writer.go:151`).

A pump's notices are posted once their event's ops are queued; the notices of `Commit` writes (interrupted, reconcile, a failed start or resume) are posted after the write is stored. `Notify` posts the desk notice and hands the Web Push to the notice record before it returns (`internal/enginehost/notify.go`). Web Push uses `boundedPayload`, which adds the `session` key when the notice names a session (`internal/push/publish.go:97`).

The rules, as the code applies them, are these. Each has a file and line where it is decided:

- Start records the row `working` before it spawns the process, and records `failed` when the spawn or the first prompt fails (`manager.go:179`, `manager.go:185`, `manager.go:193`).
- A Say to a session that is not live resumes it from its row, with the row's Claude session when it has one (`manager.go:362`, `manager.go:374`).
- Keyed Say retry: a `Say` with a `prompt_id` whose send fails stores an `engine_prompt_failed` row in the same write as the `failed` state (`manager.go:421`, `manager.go:1022`, `internal/store/engine.go:313`). A `Say` with that `prompt_id` reads the row before it checks for a sent prompt, so it returns the failure and sends nothing, also after a restart (`manager.go:374`, `manager.go:378`, `manager.go:381`, `internal/store/engine.go:96`). The insert keeps the first reason (`internal/store/queries.sql:110`). Any other keyed failure after the prompt is stored keeps its error only while `orreryd` runs (`manager.go:247`, `manager.go:280`, `manager.go:282`).
- Idle stop: `checkIdle` stops each session that has not been busy for `defaultIdleAfter`, 10 minutes, and is run every `idleCheckEvery`, 30 seconds (`manager.go:477`, `manager.go:51`, `manager.go:54`). The stop records nothing, because the pump does not record an exit of a session it stops for idleness (`manager.go:701`).
- Stop: the stop signal is `SIGINT` to the process group, and `SIGKILL` after 2 seconds (`internal/engine/proc/proc.go:17`, `internal/engine/proc/proc.go:162`). `Stop` then records `stopped` unless the exit already recorded a state (`manager.go:207`, `manager.go:734`).
- Failed on death: an exit with an error is recorded `failed`, with the errors and the last stderr line as the reason (`record.go:213`, `record.go:201`).
- Shutdown: `Close` stops each live session, records each running turn `interrupted` with the reason `orreryd stopped during the turn`, and waits for the pumps (`manager.go:426`, `manager.go:750`). `sessionHost.shutdown` calls it (`cmd/orreryd/main.go:433`), and `sessionHost.stop` calls `shutdown` after the session socket has stopped (`cmd/orreryd/main.go:419`, `cmd/orreryd/main.go:420`), which `serve` calls at `cmd/orreryd/main.go:331`.
- Reconcile at start: `Reconcile` clears the unfinished text of each row with no live process, and records each `working` or `waiting` row `interrupted` with the reason `orreryd restarted during the turn` (`internal/enginehost/reconcile.go:18`, `internal/enginehost/reconcile.go:39`). `orreryd` calls it before the session socket serves (`cmd/orreryd/main.go:394`, `cmd/orreryd/main.go:411`).
- Notices: `notifies` allows `waiting`, `done`, `failed` and `interrupted` only (`internal/enginehost/notify.go:80`). A turn that was interrupted sends no notice at its end (`record.go:167`).
- Retention: `SyncServer` reads the live engine session ids once per run, from the function that `orreryd` sets to the manager's `Live` (`internal/store/sync.go:113`, `cmd/orreryd/main.go:386`). It then runs `pruneRetention`, which deletes the engine rows with no event at or after the 14-day cutoff, and their unfinished text and `engine_prompt_failed` rows, except the live sessions (`internal/store/sync.go:144`, `internal/store/sync.go:159`). The tidy calls `SyncServer` only after its `list-panes` succeeds (`internal/daemon/mirror.go:224`). All three delete statements exclude the live ids, so a session that is not live and has no event for 14 days loses its row (`internal/store/queries.sql:40`, `internal/store/queries.sql:49`, `internal/store/queries.sql:58`).
- Errors on the session socket: `commandError` maps each error to its Connect code (`internal/enginehost/service.go:176`).

## 12. What the real-settings probes showed

The recorded run was on 2026-11-10 on vermissian, with Claude Code 2.1.321. `just engine-accept -keep DIR` exited 0. An earlier run that day failed on a probe defect (see Resume below), fixed before the recorded run. No output of either run is committed, so the values below are shapes with invented values, not recorded output.

### How the probes start

`enginecapture accept` runs the hooks-off probes, then five real-settings probes with the owner's settings: the guard, the resume pair (resume-plant and resume-ask, counted as one), interrupt-prompt, bypass-tool and background (`internal/engine/capture/settings.go:19`, SettingsProbes). All of them run in `bypassPermissions`.

Each one starts through the owner's login shell from a bare environment (`internal/engine/capture/daemon.go:115`, DaemonCommand). The argument vector is `<shell> -l -c 'exec "$@"' orrery-probe <claude> <claude args>` (`daemon.go:133`). The environment holds only PATH (the tmux and claude package directories, as the orreryd unit has them), HOME, USER, LANG, XDG_RUNTIME_DIR, and ORRERY_SUMMARIZER=1 (`daemon.go:166`), which keeps orrery's own hook silent. The claude arguments drop `--settings` and `--setting-sources`, so the owner's settings, hooks and plugins load. MCP servers stay off (`--strict-mcp-config` with an empty config).

If the guard misses in the login form, accept retries it once without `-l` and continues in the form the guard passed in. If neither form refuses, it stops (`internal/engine/capture/cli.go:250`, runProbes). In the recorded run the login form refused, so the fallback did not run.

`accept -keep DIR` keeps the raw output in DIR (`internal/engine/capture/cli.go:119`), and the recipe forwards arguments (`justfile:149`). Raw output of live runs is kept outside the repository.

### Did the guard run, and what does a refusal look like

Yes, in the login form. The tool call never ran. The stream shows the refusal as the tool's result:

```json
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_example1","is_error":true,"content":"PreToolUse:Bash hook error: `git stash drop/clear` is blocked by the destructive-command guard: it is irreversible and has no git or checkpoint to undo it. If the user explicitly wants this, ..."}]}}
```

The model then replies in text, and the result line has `subtype` success, `is_error` false, and one `permission_denials` entry: `{"tool_name":"Bash","tool_use_id":"toolu_example1","tool_input":{"command":"<the command>","description":"<text>"}}`. No `can_use_tool` request reaches the host for it. The probe passes only on the guard's own reason text (`internal/engine/capture/settings.go:11`, GuardRefusal), never on git's error.

### Do hook events appear in the stream

Yes, but only with `--include-hook-events`, which the guard probe alone passes. Each hook run is a pair of system lines:

```json
{"type":"system","subtype":"hook_started","hook_id":"hook_example1","hook_name":"PreToolUse:Bash","hook_event":"PreToolUse","session_id":"session_example","uuid":"uuid_example1"}
{"type":"system","subtype":"hook_response","hook_id":"hook_example1","hook_name":"PreToolUse:Bash","hook_event":"PreToolUse","exit_code":0,"outcome":"success","output":"...","stdout":"...","stderr":"","session_id":"session_example","uuid":"uuid_example2"}
```

Hook pairs appear for `UserPromptSubmit` (before the init line), for `PreToolUse:Bash` (three hooks start together for one Bash call; their responses can arrive after `message_stop`), and for `Stop` (two hooks, after the last `message_stop` and before the result). The guard's refusal is visible there too: its `hook_response` has `exit_code` 0, `outcome` success, and an `output` holding the guard's JSON `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"..."}}`. A refusal is a successful hook run whose output denies. It is not a hook failure, although the `tool_result` text begins `PreToolUse:Bash hook error:`; the guard's own label in that text (`` `git stash drop/clear` ``) names its rule, not the command it saw.

Without the flag, the other probes show no hook lines at all, with one exception: the resumed process printed a `hook_started`/`hook_response` pair for `SessionStart:resume` as its first two lines, before the initialize response.

### Does resume work, and does the id change

Yes. A second process started with `--resume <id>` in the first process's directory (persistence on) answered with the word the first one planted. Its init line carried the same `session_id` as the first process, so the id does not change.

Defect found and fixed on the way: the runner used to number prompt uuids from `...0001` in every process. A resumed session that already holds a prompt uuid treats a new prompt with that uuid as done. It printed only `{"type":"command_lifecycle","command_uuid":"<the reused uuid>","state":"completed",...}`, with no queued or started state, no init and no turn, and the process waited. A host must never reuse a uuid within a session. The runner now numbers a resuming probe's uuids from a separate range (`internal/engine/capture/run.go:22`).

### What follows an interrupt

The process lives and answers. The interrupt receipt is a `control_response` with success and `{"still_queued":[]}`. A `user` line carries the text `[Request interrupted by user]`. The result is `error_during_execution`, with `is_error` true and `terminal_reason` `aborted_streaming`, and the command_lifecycle state is `cancelled`. The next prompt then goes `queued`, `started`, a new init line, the turn, and a result `success`.

### Does an ordinary tool ask under bypass

No. With `--permission-mode bypassPermissions` and `--permission-prompt-tool stdio`, Bash running `git --version` sent no `can_use_tool` request. The tool simply ran (`tool_result` with `is_error` false), and the result was `success` with empty `permission_denials`. Together with section 7 of this page, under bypass only AskUserQuestion reached the host.

### What marks a live background task

Prompted to start `sleep 20` with `run_in_background: true`:

- The tool_use input carries `"run_in_background":true`.
- `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"task_example","run_id":"run_example","task_type":"local_bash","description":"<text>"}],...}` arrives, then `{"type":"system","subtype":"task_started","task_id":"task_example","run_id":"run_example","task_type":"local_bash","description":"<text>","is_backgrounded":true,"tool_use_id":"toolu_example2",...}`.
- The tool_result text says the command is running in the background, with its id and an output file under claude's own temp directory.
- The turn ends normally: result `success`, `result_index` 0, `queued_turn_count` 0. Nothing in the result names the live task, and command_lifecycle is `completed`.
- While the task lives (stdin kept open), no lines arrive.
- When it ends, `task_updated` arrives with `"patch":{"status":"completed","end_time":<ms>}`, then `task_notification` with `task_id`, `run_id`, `tool_use_id`, `status` completed, `summary`, and `output_file`, then `background_tasks_changed` with `"tasks":[]`.
- Claude then starts a turn by itself: a new init line, a status line `requesting` with no `user_message_uuids`, the stream, and a result `success` with `result_index` 1 and `"origin":{"kind":"task-notification","producer":"session-task","runId":"run_example"}`. No command_lifecycle lines belong to that turn.

So the live-task marker is the non-empty `tasks` list in `background_tasks_changed` (with `task_started`). The empty list marks the end.

## 13. Forced returns

The findings of this section come from live probes on Claude Code 2.1.321 with `claude-haiku-5-5`. The probe output is not committed, so these findings cite the code that relies on each one, not fixture lines. A step's structured return depends on them (see [Step lifecycle](contract.md#step-lifecycle) of the shared contract).

- **Structured output.** A `result` carries `structured_output` when the session runs with `--json-schema` and `StructuredOutput` is among its tools. The step engine passes `--json-schema` (`internal/engine/session/command.go:83`). It reads a result's `structured_output` into the turn's end, which is nil when the field is absent or `null` (`internal/engine/session/translate.go:86`, `internal/engine/session/translate.go:268`).
- **Repeated forcing.** A second user message after a forced return gets another forced return. `--resume` with the same flags is forced too. The step loop sends its second message through `Continue` (`internal/enginehost/steploop.go:238`), and a respawn resumes with the step's agent and schema flags (`internal/enginehost/manager.go:507`, `internal/enginehost/manager.go:508`, `internal/engine/session/command.go:74`).
- **Agent settings.** `--agent` applies the agent's model, tools and body. The effort is not visible in the init line, so this section cannot confirm the effort an agent runs at. The step engine adds `--agent` with `--strict-mcp-config` (`internal/engine/session/command.go:80`) and passes no `--model`, so the model comes from the agent.
- **The `StructuredOutput` tool.** An agent whose `tools:` allowlist omits `StructuredOutput` gets no `StructuredOutput` tool. Its turns then return text, with `structured_output` null. `--allowedTools` does not restore the tool, so such an agent must list `StructuredOutput` in `tools:`. An agent that uses `disallowedTools` keeps the tool.

What the step engine does with a null return: the value is nil, and the schema check fails it with `$: missing return value` (`internal/enginehost/steploop.go:97`, `internal/enginehost/schema.go:183`). The return is sent back like any other failed check, and a step whose agent never forces a return ends `checks_failed` after its three send-backs.

A return is taken only from a turn that ends with no background task live. The manager tracks the live background tasks of a session and hands the turn's end over only when none is live (`internal/enginehost/manager.go:1102`). The markers of a live task are in [What marks a live background task](#what-marks-a-live-background-task).

## 14. Step tools

`orrery [--socket PATH] mcp` serves the step tools as an MCP server on stdin and stdout. It serves until stdin closes and exits 0. Arguments after `mcp` exit 2. The transport is stdio only, so the process opens no listener and holds no database. It is a client of the `orrery.engine.v1.Steps` service (`proto/orrery/engine/v1/step.proto:7`) on `sessions.sock`, the socket beside the daemon socket (`internal/protocol/protocol.go:156`, `:159`; `cmd/orreryd/main.go:403`). Each tool call makes one `Steps` RPC: `step_start` calls `StartStep`, `step_status` calls `StepStatus`, `step_stop` calls `StopStep` and `step_wait` calls `WaitSteps` (`internal/stepmcp/tools.go:53`-`:77`).

The package imports the connect client, the generated `Steps` stubs and the MCP server, and no database or listener package (`internal/stepmcp/tools.go:4`-`:19`). The step rows live in the daemon, so a result a caller lost in transit is still in its row (`step.proto:15`).

### Protocol

- The server reports MCP revision `2025-11-25` and the `tools` capability with `listChanged` false (`internal/mcp/server.go:23`, `:434`-`:445`). Messages are newline-delimited JSON-RPC 2.0 (`internal/mcp/server.go:169`-`:170`).
- `tools/list` and `tools/call` need an initialized connection. Before `notifications/initialized`, a request gets `-32600` `Connection is not initialized` (`internal/mcp/server.go:456`-`:464`).
- A message longer than 1 MiB, the newline included, is refused with `-32700` and id `null`, and the connection stays open (`internal/mcp/server.go:46`, `:197`-`:198`, `:544`-`:548`).
- A connection runs at most 8 tool calls at once. A ninth is refused with `-32000` `Server already has 8 tool calls in flight` and is not queued (`internal/mcp/server.go:52`, `:499`-`:503`).
- An unknown tool name is `-32602` `Unknown tool`. Arguments that are not a JSON object are `-32602` (`internal/mcp/server.go:487`-`:497`). An unknown method is `-32601` (`internal/mcp/server.go:407`-`:410`).
- A handler error is not a JSON-RPC error. It is a tool result whose single text block is the error's message, with `isError` true (`internal/mcp/server.go:513`-`:517`). Argument errors, refused starts and daemon failures all arrive this way.
- The server has no handler for `notifications/cancelled`, and a notification gets no reply (`internal/mcp/server.go:407`-`:410`). A handler's context ends only with the connection (`internal/mcp/server.go:60`-`:62`), so a cancelled call keeps its slot until its handler returns.

### Arguments and results

Every tool result is one text block holding JSON. Every argument object refuses unknown keys, so a misspelt key is an error (`internal/stepmcp/tools.go:110`-`:117`).

#### step_start

Arguments (`internal/stepmcp/tools.go:26`-`:39`). Required: `agent`, `prompt`, `workspace`, `schema`. Optional:

- `revision`: the commit the step starts at. Empty means HEAD (`step.proto:49`).
- `doneValue`: the status the done return carries. Empty means `done` (`step.proto:64`).
- `readOnly`: a read-only step runs in a detached worktree removed when it ends (`step.proto:52`).
- `ownedPaths`: the repository-relative paths a writing step may change.
- `gate`: the gate program and its arguments. Empty runs no gate (`step.proto:58`).
- `gateLimitSeconds`: an integer of 0 or more. Zero means 30 minutes (`step.proto:61`).
- `epic`: the epic the step belongs to, passed as given. Empty leaves the step unlabelled, and an unlabelled step is never listed. The format `step.proto` names, `<repo-id>/<epic-slug>`, is not checked (`internal/stepmcp/tools.go:37`, `internal/stepmcp/tools.go:96`-`:97`, `internal/stepmcp/tools.go:175`; `step.proto:71`).
- `task`: the slug of the epic's task the step works on. Empty when it works on no one task (`internal/stepmcp/tools.go:38`, `internal/stepmcp/tools.go:97`, `internal/stepmcp/tools.go:176`; `step.proto:74`).

`schema` is a JSON object argument, not a string; the server sends its JSON text to `StartStep` (`internal/stepmcp/tools.go:128`-`:137`). `agent`, `prompt` and `workspace` must be non-empty (`internal/stepmcp/tools.go:151`-`:157`).

Result: `{"id": "<step id>"}`, returned once the step's row exists (`internal/stepmcp/tools.go:181`; `step.proto:8`). A refused start returns the refusal and no id.

#### step_status

Arguments: `id`, required and non-empty (`internal/stepmcp/tools.go:41`-`:42`, `:191`-`:193`). Result: the step object described below (`internal/stepmcp/tools.go:198`).

#### step_stop

Arguments: `id`, required and non-empty. Result: `{"id": "<step id>", "stopped": true}` (`internal/stepmcp/tools.go:214`). The result says `stopped: true` whenever the daemon accepted the call, since `StopStepResponse` has no fields (`step.proto:90`).

#### step_wait

Arguments (`internal/stepmcp/tools.go:44`-`:47`):

- `ids`: a non-empty array of step ids. An empty id is refused (`internal/stepmcp/tools.go:246`-`:251`).
- `timeoutSeconds`: optional. It must be a whole number of seconds, 0 or more. A fractional or negative value is an error with the text `timeoutSeconds must be a whole number of seconds, 0 or more` (`internal/stepmcp/tools.go:219`-`:227`).

The wait is clamped to `MaxWait`, 25 minutes or 1500 seconds (`internal/stepmcp/tools.go:21`-`:23`). An omitted timeout is 1500 seconds. A timeout at or above 1500 is 1500 (`internal/stepmcp/tools.go:219`-`:230`). The clamp exists so that the call returns inside the time Claude Code allows a tool call, and the caller asks again (`internal/stepmcp/tools.go:21`-`:22`). A timeout of 0 returns at once (`step.proto:109`-`:110`).

Result: `{"ended": <step or null>, "steps": [<step>, ...]}` (`internal/stepmcp/tools.go:263`-`:271`). `ended` is the first step to end and is `null` when the timeout passes first. `steps` lists every named step in the order the request named them (`step.proto:118`-`:119`). On a timeout the caller reads each step's state from `steps`.

#### The step object

Every tool that reports a step returns these fields (`internal/stepmcp/tools.go:300`-`:325`):

- `id`, `agent`, `workspace`, `revision`, `readOnly`, `worktree`: as the `Step` message defines them (`step.proto:123`-`:173`). `worktree` is empty for a writing step.
- `end`, `reason`: empty while the step runs (`step.proto:143`, `:155`).
- `status`: the done value the return carried, or the status the step ended with. `result`: the latest result the return carried (`step.proto:145`-`:149`).
- `checks`: each `{round, name, ok, detail}` from the rounds the return's checks ran in (`internal/stepmcp/tools.go:301`-`:309`; `step.proto:151`-`:152`).
- `started`, `ended`: unix seconds. `ended` is 0 while the step runs (`step.proto:157`-`:161`).

### Caller facts

These are Claude Code 2.1.321 behaviors that the tool shapes answer to. No code or fixture in this repository records them.

- A stdio tool call that stays silent is aborted after 30 minutes (`CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT`). The 25-minute clamp on `step_wait` keeps a wait inside that limit.
- In an interactive session, a call still running after 120 seconds is moved to the background, unless `CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0` is set. Step callers set it to 0.
- The server ignores `notifications/cancelled`, so a cancelled `step_wait` holds its call slot until its timeout (see Protocol above).

## 15. The step watch and judge

The findings of this section come from live probes on Claude Code 2.1.321 with `claude-haiku-5-5`, and from the code that runs them. The probe output is not committed, so each claim cites the code that relies on it. The watch and the judge run beside a step's loop, and [Step lifecycle](contract.md#step-lifecycle) gives the rules they follow.

- **Clocks.** The watch never reads the clock. Its loop passes each reading in (`internal/enginehost/stepwatch.go:91-92`, `internal/enginehost/steploop.go:113-116`). Model time and silence advance only while a turn runs with no main-conversation tool call open. Turn time advances for the whole turn (`internal/enginehost/stepwatch.go:117-131`). A turn start restarts turn time and clears the open calls, and a turn end or an exit clears them too. Any event restarts the silence count (`internal/enginehost/stepwatch.go:140-151`, `internal/enginehost/stepwatch.go:161`).
- **Trips.** The loop checks the watch on each tick, which is five seconds by default (`internal/enginehost/step.go:35`, `internal/enginehost/step.go:261`). A tick raises the first of four trips that is due, in this order (`internal/enginehost/stepwatch.go:213-252`). No trip is raised while no turn runs or while a trip waits for its verdict (`internal/enginehost/stepwatch.go:217`). A landed nudge turns off the periodic check until the next turn begins (`internal/enginehost/stepwatch.go:241`).
  - `overtime`: model time reaches the step's limit. The limit is `model_limit_seconds` on `StartStep`, 60 minutes when it is zero (`internal/enginehost/step.go:31-32`, `internal/enginehost/step.go:176-179`, `internal/enginehost/stepservice.go:57`).
  - `tool_stuck`: a main-conversation tool call has been open for 30 minutes of wall time, or 60 minutes after its extension (`internal/enginehost/stepwatch.go:17-18`, `internal/enginehost/stepwatch.go:256-276`).
  - `silent`: 10 minutes of model time with no event (`internal/enginehost/stepwatch.go:16`, `internal/enginehost/stepwatch.go:236-240`).
  - `periodic`: five minutes of turn time since the turn began or since the last verdict was applied (`internal/enginehost/stepwatch.go:15`, `internal/enginehost/stepwatch.go:241-245`, `internal/enginehost/stepwatch.go:285`).
- **Monitor call.** A trip makes one call on the step's Haiku caller, and the loop waits for it. It handles no return or exit until the call ends (`internal/enginehost/stepwatchloop.go:19-24`, `internal/enginehost/steploop.go:150-152`). The input is the monitor instruction, a `## Why` section with the trip's sentence, and a `## Recent events` section (`internal/enginehost/stepmonitor.go:43-45`). The recent events are the last 40 finished blocks and notices, oldest first, each with its age and its text cut to 300 bytes (`internal/enginehost/stepwatch.go:19-20`, `internal/enginehost/stepwatch.go:191-208`, `internal/enginehost/stepwatch.go:338-352`).
- **Argv and environment.** The call runs `claude -p --model claude-haiku-5-5 --settings {"disableAllHooks":true} --no-session-persistence --tools "" --max-turns 1 --output-format json --json-schema <schema>` in `/`. The model is `protocol.DefaultSummaryModel` (`internal/enginehost/haiku.go:174-184`, `internal/enginehost/haiku.go:94`, `internal/protocol/protocol.go:175`). Its environment is orreryd's, without `ANTHROPIC_BASE_URL` and `ORRERY_SUMMARIZER`, with `ORRERY_SUMMARIZER=1` added (`internal/enginehost/haiku.go:187-196`).
- **Cap and timeout.** orreryd builds one Haiku caller with the defaults for the whole process (`cmd/orreryd/main.go:449`, `internal/enginehost/haiku.go:18-21`). Two calls run at once, shared by every step's monitor and judge calls. Each call has 60 seconds, which covers the wait for a slot and the run together (`internal/enginehost/haiku.go:70-82`). At the timeout the claude process group is killed, and the call waits at most 2 seconds for its output pipes to close (`internal/enginehost/haiku.go:24`, `internal/enginehost/haiku.go:99`, `internal/enginehost/haiku.go:113-119`).
- **Reply.** The call reads the one JSON result line claude prints. It succeeds only for a `result` of subtype `success` that is not `is_error` and carries `structured_output` (`internal/enginehost/haiku.go:129-158`).
- **Measured time.** Probes of this argv measured 3.2, 4.0 and 4.0 seconds of wall time per call, with `duration_ms` from 2371 to 2686. The probes also found that the call ends `success` with `structured_output` set under `--max-turns 1`, and that a result with `num_turns` 2 still succeeds under that flag. The reply is read from `structured_output` (`internal/enginehost/haiku.go:153-157`).
- **Monitor verdicts.** The reply must match `stepMonitorSchema`, with a `verdict` and a `doing` string (`internal/enginehost/stepmonitor.go:14-22`). The verdicts are:
  - `progressing`: real progress since the last check.
  - `long_command`: one long command that is still plausibly working, such as tests or a build.
  - `looping`: repeated actions without progress.
  - `wedged`: stuck, with no sign of recovery.

  The instruction defines each one (`internal/enginehost/stepmonitor.go:28-34`), and the constants are at `internal/enginehost/stepwatch.go:58-61`. A verdict outside the four, or a blank `doing` after trimming, fails the parse (`internal/enginehost/stepmonitor.go:59-69`). The `doing` line is the reply's first line, trimmed and cut to 200 bytes at a rune boundary (`internal/enginehost/stepmonitor.go:63-76`). The instruction asks for one line under 100 characters, and the code keeps up to 200 bytes (`internal/enginehost/stepmonitor.go:25`, `internal/enginehost/stepmonitor.go:34`).
- **Verdict policy.** `apply` acts on a verdict in this order (`internal/enginehost/stepwatch.go:280-317`):
  1. A trip whose turn is no longer the running turn acts as `none`. So does a `tool_stuck` trip whose call has closed (`internal/enginehost/stepwatch.go:286-292`).
  2. `overtime` ends the step `overtime`, whatever the verdict (`internal/enginehost/stepwatch.go:293-295`).
  3. `looping` or `wedged` nudges the step if it has not been nudged. Otherwise it ends the step with that verdict as its end (`internal/enginehost/stepwatch.go:296-301`).
  4. `tool_stuck` with `long_command` extends the call once. Any other verdict ends the step `tool_stuck` (`internal/enginehost/stepwatch.go:303-308`).
  5. `silent` nudges the step if it has not been nudged. Otherwise it ends the step `silent` (`internal/enginehost/stepwatch.go:309-313`).
  6. `periodic` takes `none` (`internal/enginehost/stepwatch.go:314-315`).

  Every apply restarts the periodic count, including an apply that acts as `none` (`internal/enginehost/stepwatch.go:283-285`).
- **Failed call.** A monitor call that fails, or whose reply does not parse, stores its error with an empty verdict and an empty doing line (`internal/enginehost/stepwatchloop.go:51-60`). The verdict is then empty, so the trip takes its mechanical action: `overtime` and `tool_stuck` end the step, `silent` nudges on its first trip and ends on the next, and `periodic` takes `none` (`internal/enginehost/stepwatch.go:55-56`). A run with no Haiku caller does the same, with its own error (`internal/enginehost/stepwatchloop.go:52-54`).
- **Nudge.** The first nudge interrupts the running turn with `Interrupt` (`internal/enginehost/stepwatchloop.go:65-70`, `internal/enginehost/manager.go:600`). It sets the text the next turn end is continued with: the trip's summary and doing line, then `Continue the step where it stopped, or return now if it is finished.` (`internal/enginehost/stepwatchloop.go:71-73`). The interrupt ends the turn with `error_during_execution` (see [What follows an interrupt](#what-follows-an-interrupt)). That turn end is no round. The loop neither checks nor stores it, and it sends the nudge text with `Continue` (`internal/enginehost/steploop.go:154-163`). A turn end that is not an error drops the nudge and is an ordinary round (`internal/enginehost/steploop.go:155-156`, `internal/enginehost/steploop.go:164`).
- **Nudge spending.** A landed nudge spends the step's one nudge, restarts the silence count, and turns off the periodic check until the next turn begins. Until then each open call's 30 minutes, or 60 after its extension, count from the nudge. The silent, tool_stuck and overtime trips keep running in the interrupted turn (`internal/enginehost/stepwatch.go:319-327`, `internal/enginehost/stepwatch.go:264-267`, `internal/enginehost/stepwatch.go:143`, `internal/enginehost/stepwatch.go:241`). A nudge does not count toward the three send-backs. Only `retry` counts those (`internal/enginehost/steploop.go:264-280`).
- **Failed interrupt.** A failed `Interrupt` is logged, the action is `none`, and the nudge is undone. The step's nudge is not spent, open calls count from their start again, and the watch keeps watching the running turn (`internal/enginehost/stepwatchloop.go:66-70`, `internal/enginehost/stepwatch.go:329-336`). A failed `Continue` of a nudge ends the step `failed` (`internal/enginehost/steploop.go:158-160`).
- **Extension.** A `long_command` verdict on a `tool_stuck` trip extends that call once, from 30 minutes to 60 minutes of wall time. The extension belongs to that call, so a later call has its own (`internal/enginehost/stepwatch.go:79-83`, `internal/enginehost/stepwatch.go:260-263`, `internal/enginehost/stepwatch.go:304-307`).
- **Watch ends.** A watch end stores its verdict first. A failure to store it ends the step `failed` (`internal/enginehost/stepwatchloop.go:38-41`). The step then ends with the verdict's end kind, no status, and the reason `<trip> trip: <why>`, followed by ` Doing: <doing>` when the doing line is not empty (`internal/enginehost/stepwatchloop.go:42-44`, `internal/enginehost/stepwatchloop.go:77-83`). The watch ends the step `overtime`, `tool_stuck`, `silent`, `looping` or `wedged` (`internal/enginehost/stepwatch.go:50-52`, `internal/enginehost/stepwatch.go:293-313`). The judge ends the step `goal_unmet` (`internal/enginehost/stepjudgeloop.go:23`).
- **Verdicts in the store.** Each verdict is appended to the row's `verdicts` with its time, its kind (`monitor` or `judge`), its trip, its round, its verdict, its detail, its error and its action (`internal/store/steps.go:27-35`, `internal/store/steps.go:146-160`). A monitor's detail is its doing line (`internal/enginehost/stepwatchloop.go:31`). A judge's detail is its reason (`internal/enginehost/stepjudgeloop.go:49`). A non-empty doing line replaces the row's `doing` (`internal/store/steps.go:162-168`).
- **Judge.** A return that passes every check and whose `status` equals `done_value` is judged. A return that fails a check is sent back and is not judged (`internal/enginehost/steploop.go:203-208`). Without a Haiku caller the judge makes no call, and the step ends `done` (`internal/enginehost/stepjudgeloop.go:35-37`).
- **Judge input.** The input is the judge instruction, then `## Task` with the step's first prompt, then `## Return` with the return. A writing step adds `## Diff stat`, the `git diff --stat` from the start commit to the return's commit. A step with a gate adds `## Gate output tail`, the gate check's detail (`internal/enginehost/stepjudge.go:31-34`, `internal/enginehost/stepjudge.go:45-57`, `internal/enginehost/stepjudgeloop.go:83-98`, `internal/enginehost/stepjudgeloop.go:101-121`). A blank diff stat or gate detail is omitted (`internal/enginehost/stepjudge.go:50`, `internal/enginehost/stepjudge.go:53`). Each section keeps its first 8 KiB, and the gate output keeps its last 8 KiB (`internal/enginehost/stepjudge.go:29`, `internal/enginehost/stepjudge.go:59-81`).
- **Judge verdicts.** The reply is `met` or `unmet`, with a one-line reason (`internal/enginehost/stepjudge.go:18-26`, `internal/enginehost/stepjudge.go:85-107`).
  - `met` ends the step `done` (`internal/enginehost/stepjudgeloop.go:50-56`).
  - The first `unmet` stores its verdict with action `continue`, sends the session back once with the reason, and waits for the next return. That return is judged again when it passes its checks (`internal/enginehost/stepjudgeloop.go:59-76`, `internal/enginehost/stepjudgeloop.go:26-27`). The send-back does not count toward the three send-backs (`internal/enginehost/steploop.go:264-272`).
  - A second `unmet` ends the step `goal_unmet`, and the step's return is kept (`internal/enginehost/stepjudgeloop.go:60-67`).
  - A failed diff stat, a failed call or an unparsable reply stores its error with no verdict and action `none`, and ends the step `done` (`internal/enginehost/stepjudgeloop.go:40-48`, `internal/enginehost/stepjudgeloop.go:86-98`).
  - A failed `Continue` of the judge's send-back ends the step `failed` (`internal/enginehost/stepjudgeloop.go:73-75`).
