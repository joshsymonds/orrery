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

A pump's notices are posted once their event's ops are queued; the notices of `Commit` writes (interrupted, reconcile, a failed start or resume) are posted after the write is stored. `Notify` posts the desk notice and hands the Web Push to the notice record before it returns (`internal/enginehost/notify.go`). Web Push uses `boundedPayload`, which adds the `session` key when the notice names a session (`internal/push/publish.go:100`).

The rules, as the code applies them, are these. Each has a file and line where it is decided:

- Start records the row `working` before it spawns the process, and records `failed` when the spawn or the first prompt fails (`manager.go:179`, `manager.go:185`, `manager.go:193`).
- A Say to a session that is not live resumes it from its row, with the row's Claude session when it has one (`manager.go:362`, `manager.go:374`).
- Keyed Say retry: a `Say` with a `prompt_id` whose send fails stores an `engine_prompt_failed` row in the same write as the `failed` state (`manager.go:421`, `manager.go:1022`, `internal/store/engine.go:343`). A `Say` with that `prompt_id` reads the row before it checks for a sent prompt, so it returns the failure and sends nothing, also after a restart (`manager.go:374`, `manager.go:378`, `manager.go:381`, `internal/store/engine.go:124`). The insert keeps the first reason (`internal/store/queries.sql:152`-`:154`). Any other keyed failure after the prompt is stored keeps its error only while `orreryd` runs (`manager.go:247`, `manager.go:280`, `manager.go:282`).
- Idle stop: `checkIdle` stops each session that has not been busy for `defaultIdleAfter`, 10 minutes, and is run every `idleCheckEvery`, 30 seconds (`manager.go:477`, `manager.go:51`, `manager.go:54`). The stop records nothing, because the pump does not record an exit of a session it stops for idleness (`manager.go:701`).
- Stop: the stop signal is `SIGINT` to the process group, and `SIGKILL` after 2 seconds (`internal/engine/proc/proc.go:17`, `internal/engine/proc/proc.go:162`). `Stop` then records `stopped` unless the exit already recorded a state (`manager.go:207`, `manager.go:734`).
- Failed on death: an exit with an error is recorded `failed`, with the errors and the last stderr line as the reason (`record.go:213`, `record.go:201`).
- Shutdown: `Close` stops each live session, records each running turn `interrupted` with the reason `orreryd stopped during the turn`, and waits for the pumps (`manager.go:426`, `manager.go:750`). `sessionHost.shutdown` calls it (`cmd/orreryd/main.go:685`), and `sessionHost.stop` calls `shutdown` after the session socket has stopped (`cmd/orreryd/main.go:667`-`:669`), which `serve` calls at `cmd/orreryd/main.go:407`.
- Reconcile at start: `Reconcile` clears the unfinished text of each row with no live process, and records each `working` or `waiting` row `interrupted` with the reason `orreryd restarted during the turn` (`internal/enginehost/reconcile.go:19`, `internal/enginehost/reconcile.go:50`). `orreryd` calls it before the session socket serves (`cmd/orreryd/main.go:592`, `cmd/orreryd/main.go:619`).
- Notices: `notifies` allows `waiting`, `done`, `failed` and `interrupted` only (`internal/enginehost/notify.go:80`). A turn that was interrupted sends no notice at its end (`record.go:167`).
- Retention: `SyncServer` reads the live engine session ids once per run, from the function that `orreryd` sets to the manager's `Live` (`internal/store/sync.go:113`, `cmd/orreryd/main.go:582`). It then runs `pruneRetention`, which deletes the engine rows with no event at or after the 14-day cutoff, and their unfinished text and `engine_prompt_failed` rows, except the live sessions (`internal/store/sync.go:144`, `internal/store/sync.go:159`). The tidy calls `SyncServer` only after its `list-panes` succeeds (`internal/daemon/mirror.go:224`). All three delete statements exclude the live ids, so a session that is not live and has no event for 14 days loses its row (`internal/store/queries.sql:40`, `internal/store/queries.sql:49`, `internal/store/queries.sql:58`).
- Errors on the session socket: `commandError` maps each error to its Connect code (`internal/enginehost/service.go:232`).

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

- **Structured output.** A `result` carries `structured_output` when the session runs with `--json-schema` and `StructuredOutput` is among its tools. The step engine passes `--json-schema` (`internal/engine/session/command.go:83`). It reads a result's `structured_output` into the turn's end, which is nil when the field is absent or `null` (`internal/engine/session/translate.go:87`, `internal/engine/session/translate.go:284`).
- **Repeated forcing.** A second user message after a forced return gets another forced return. `--resume` with the same flags is forced too. The step loop sends its second message through `Continue` (`internal/enginehost/steploop.go:737`), and a respawn resumes with the step's agent and schema flags (`internal/enginehost/manager.go:521`, `internal/enginehost/manager.go:522`, `internal/engine/session/command.go:74`).
- **Agent settings.** `--agent` applies the agent's model, tools and body. The effort is not visible in the init line, so this section cannot confirm the effort an agent runs at. The step engine adds `--agent` with `--strict-mcp-config` (`internal/engine/session/command.go:80`) and passes no `--model`, so the model comes from the agent.
- **The `StructuredOutput` tool.** An agent whose `tools:` allowlist omits `StructuredOutput` gets no `StructuredOutput` tool. Its turns then return text, with `structured_output` null. `--allowedTools` does not restore the tool, so such an agent must list `StructuredOutput` in `tools:`. An agent that uses `disallowedTools` keeps the tool.

What the step engine does with a null return: the value is nil, and the schema check fails it with `$: missing return value` (`internal/enginehost/steploop.go:401`, `internal/enginehost/steploop.go:518`, `internal/enginehost/schema.go:183`). The return is sent back like any other failed check, and a step whose agent never forces a return ends `checks_failed` after its three send-backs.

A return is taken only from a turn that ends with no background task live. The manager tracks the live background tasks of a session and hands the turn's end over only when none is live (`internal/enginehost/manager.go:1102`). The markers of a live task are in [What marks a live background task](#what-marks-a-live-background-task).

## 14. Step and epic tools

`orrery [--socket PATH] mcp` serves the step, epic and evidence tools as an MCP server on stdin and stdout. It serves until stdin closes and exits 0. Arguments after `mcp` exit 2. The transport is stdio only, so the process opens no listener and holds no database. It is a client of the `orrery.engine.v1.Steps` service (`proto/orrery/engine/v1/step.proto:8`), of the `orrery.engine.v1.Epics` service (`proto/orrery/engine/v1/epic.proto:6`) and of the `orrery.engine.v1.Evidence` service (`proto/orrery/engine/v1/epic.proto:21`) on `sessions.sock`, the socket beside the daemon socket (`internal/protocol/protocol.go:156`, `:157`; `cmd/orreryd/main.go:606`). Each step tool call makes one `Steps` RPC: `step_start` calls `StartStep`, `step_status` calls `StepStatus`, `step_stop` calls `StopStep`, `step_resume` calls `ResumeStep` and `step_wait` calls `WaitSteps` (`internal/stepmcp/tools.go:69`-`:103`). There are eight epic tools, and each call makes one `Epics` RPC: `epic_create` calls `CreateEpic`, `epic_get` calls `GetEpic`, `epic_list` calls `ListEpics`, `epic_answer` calls `AnswerEpic`, `epic_pause` calls `PauseEpic`, `epic_resume` calls `ResumeEpic`, `epic_stop` calls `StopEpic` and `epic_retry` calls `RetryEpic`. The tool list is `internal/stepmcp/epictools.go:36`-`:96`, and the RPC of each handler is at `:139`, `:153`, `:167`, `:181`, `:195`, `:209`, `:223` and `:237`, in that order. Each evidence tool call makes one `Evidence` RPC: `evidence_run` calls `RunEvidence`, `evidence_list` calls `ListEvidence` and `evidence_get` calls `GetEvidenceArtifact` (`internal/stepmcp/evidencetools.go:30`-`:94`). `orrery mcp` builds the three clients and passes them to `stepmcp.NewServer`, which takes the steps, epics and evidence clients in that order (`cmd/orrery/main.go:407`-`:409`; `internal/stepmcp/tools.go:53`-`:59`).

`orrery epic` runs the same handlers. Its forms `create`, `get`, `list`, `answer`, `pause`, `resume`, `stop` and `retry` each make one call to the epic tool `epic_<verb>` through `CallEpicTool`, on the same session socket (`cmd/orrery/main.go:239`-`:240`, `:430`-`:439`; `cmd/orrery/epic.go:49`-`:74`, `:179`-`:191`; `internal/stepmcp/epictools.go:100`-`:113`). So a command and its tool make the same call. The forms are `create FILE|-`, `get ID`, `list`, `answer EPIC --id ID [--question QN] TEXT`, `pause ID`, `resume ID`, `stop ID` and `retry ID` (`cmd/orrery/epic.go:22`-`:27`). `create` reads the tool's JSON arguments from FILE, or from stdin when FILE is `-`, and passes them as given (`cmd/orrery/epic.go:81`-`:109`). `answer` turns EPIC, `--id`, `--question` and TEXT into the tool's `epicId`, `id`, `question` and `text`, and leaves `question` out when `--question` is not given (`cmd/orrery/epic.go:111`-`:133`). A wrong form is a usage error naming the form it wants (`cmd/orrery/epic.go:76`-`:79`). The command prints the tool's result text on stdout, and `answer` also prints a non-empty warning on stderr (`cmd/orrery/epic.go:147`-`:150`). A refusal exits 1 with `orrery: ` and the tool's text on stderr (`cmd/orrery/epic.go:187`-`:189`; `cmd/orrery/main.go:194`-`:196`). A handler error reaches the command with the prefix `epic: run TOOL: `, so an argument that does not decode reads differently there than in the tool's text (`cmd/orrery/epic.go:182`-`:184`; `internal/stepmcp/epictools.go:116`-`:121`).

The package imports the connect client, the generated `Steps`, `Epics` and `Evidence` stubs, protobuf's JSON encoding and the MCP server, and no database or listener package (`internal/stepmcp/tools.go:4`-`:20`; `internal/stepmcp/epictools.go:3`-`:15`). The step rows live in the daemon, so a result a caller lost in transit is still in its row (`step.proto:16`-`:17`, `:160`-`:161`).

### Protocol

- The server reports MCP revision `2025-11-25` and the `tools` capability with `listChanged` false (`internal/mcp/server.go:23`, `:434`-`:445`). Messages are newline-delimited JSON-RPC 2.0 (`internal/mcp/server.go:169`-`:170`).
- `tools/list` and `tools/call` need an initialized connection. Before `notifications/initialized`, a request gets `-32600` `Connection is not initialized` (`internal/mcp/server.go:456`-`:464`).
- A message longer than 1 MiB, the newline included, is refused with `-32700` and id `null`, and the connection stays open (`internal/mcp/server.go:46`, `:197`-`:198`, `:544`-`:548`).
- A connection runs at most 8 tool calls at once. A ninth is refused with `-32000` `Server already has 8 tool calls in flight` and is not queued (`internal/mcp/server.go:52`, `:499`-`:503`).
- An unknown tool name is `-32602` `Unknown tool`. Arguments that are not a JSON object are `-32602` (`internal/mcp/server.go:487`-`:497`). An unknown method is `-32601` (`internal/mcp/server.go:407`-`:410`).
- A handler error is not a JSON-RPC error. It is a tool result whose single text block is the error's message, with `isError` true (`internal/mcp/server.go:513`-`:517`). Argument errors, refused starts and daemon failures all arrive this way.
- The server has no handler for `notifications/cancelled`, and a notification gets no reply (`internal/mcp/server.go:407`-`:410`). A handler's context ends only with the connection (`internal/mcp/server.go:60`-`:62`), so a cancelled call keeps its slot until its handler returns.

### Arguments and results

Every tool result is one text block holding JSON. Every argument object refuses unknown keys, so a misspelt key is an error (`internal/stepmcp/tools.go:130`-`:137`; `internal/stepmcp/epictools.go:116`-`:121`).

#### step_start

Arguments (`internal/stepmcp/tools.go:105`-`:118`). Required: `prompt` and `workspace` (`internal/stepmcp/tools.go:40`). Optional:

- `revision`: the commit the step starts at. Empty means HEAD (`step.proto:54`).
- `doneValue`: the status the done return carries. Empty means `done` (`step.proto:69`).
- `readOnly`: a read-only step runs in a detached worktree removed when it ends (`step.proto:57`).
- `ownedPaths`: the repository-relative paths a writing step may change.
- `gateName`: the name of the gate the step's return is checked against. It is resolved once, when the step starts, from `gatesFile` and then the project file. Empty runs no gate (`internal/stepmcp/tools.go:114`; `step.proto:82`; see [Gate files](contract.md#gate-files)).
- `gatesFile`: the absolute path of the epic's gates file, read before the project file. Empty reads only the project file. It is refused when it is not absolute, and when it is set without `gateName` (`internal/stepmcp/tools.go:115`; `step.proto:85`; `internal/enginehost/step.go:430`-`:438`).
- `epic`: the epic the step belongs to, passed as given. Empty leaves the step unlabelled, and an unlabelled step is never listed. The format `step.proto` names, `<repo-id>/<epic-slug>`, is not checked (`internal/stepmcp/tools.go:116`, `internal/stepmcp/tools.go:197`; `step.proto:76`).
- `task`: the slug of the epic's task the step works on. Empty when it works on no one task (`internal/stepmcp/tools.go:117`, `internal/stepmcp/tools.go:198`; `step.proto:79`).

`gate` and `gateLimitSeconds` are not arguments. A call that names either one is refused as an unknown key (`internal/stepmcp/tools.go:130`-`:137`). The gate's limit is the one its definition gives (see [Gate files](contract.md#gate-files)).

`schema` is a JSON object argument, not a string; the server sends its JSON text to `StartStep` (`internal/stepmcp/tools.go:149`-`:158`). `prompt` and `workspace` must be non-empty (`internal/stepmcp/tools.go:172`-`:177`). `agent` and `schema` may be left out of the call. The service refuses an empty `agent` or `schema` unless the start names an agent gate (`internal/enginehost/stepsession.go:131`-`:132`, `internal/enginehost/step.go:286`). A start that names an agent gate by `gateName` must be `readOnly` and leave `agent`, `schema` and `doneValue` empty, since the gate supplies them (`internal/enginehost/step.go:483`-`:501`; see [Agent gates](contract.md#agent-gates)).

Result: `{"id": "<step id>"}`, returned once the step's row exists (`internal/stepmcp/tools.go:205`; `step.proto:9`-`:10`). A refused start returns the refusal and no id.

#### step_status

Arguments: `id`, required and non-empty (`internal/stepmcp/tools.go:42`-`:43`, `:215`-`:217`). Result: the step object described below (`internal/stepmcp/tools.go:227`).

#### step_stop

Arguments: `id`, required and non-empty. Result: `{"id": "<step id>", "stopped": true}` (`internal/stepmcp/tools.go:248`). The result says `stopped: true` whenever the daemon accepted the call, since `StopStepResponse` has no fields (`step.proto:101`-`:102`).

#### step_resume

Arguments: `id`, required and non-empty (`internal/stepmcp/tools.go:42`-`:43`, `:239`-`:241`). Result: `{"id": "<step id>", "resumed": true}` (`internal/stepmcp/tools.go:233`-`:250`). The result says `resumed: true` whenever the daemon accepted the call, since `ResumeStepResponse` has no fields (`step.proto:241`). The call resumes a step that a usage limit holds, through `ResumeStep` (see [Usage-limit hold](#usage-limit-hold)); `ResumeStep` refuses a step that no limit holds with `failed_precondition`.

A refused call is a tool error whose text is the Connect error's text, such as `failed_precondition: step is not limited` (`internal/stepmcp/tools.go:233`-`:250`; `internal/enginehost/stepservice.go:242`-`:258`). Its codes are those of `ResumeStep` in [Steps](contract.md#steps). A call answered `Unavailable` is sent again for up to 60 seconds (`internal/stepmcp/tools.go:233`-`:250`; `internal/rpcretry/retry.go:16`, `:64`). The retry does not treat `step is not limited` as a resume. After a cut attempt that resumed the step, the retry answers that refusal, where `orrery step resume` exits 0 (`internal/stepmcp/tools.go:233`-`:250`; `cmd/orrery/step.go:57`-`:73`).

#### step_wait

Arguments (`internal/stepmcp/tools.go:45`-`:47`):

- `ids`: a non-empty array of step ids. An empty id is refused (`internal/stepmcp/tools.go:334`-`:339`).
- `timeoutSeconds`: optional. It must be a whole number of seconds, 0 or more. A fractional or negative value is an error with the text `timeoutSeconds must be a whole number of seconds, 0 or more` (`internal/stepmcp/tools.go:267`-`:275`).

The wait is clamped to `MaxWait`, 25 minutes or 1500 seconds (`internal/stepmcp/tools.go:22`-`:24`). An omitted timeout is 1500 seconds. A timeout at or above 1500 is 1500 (`internal/stepmcp/tools.go:267`-`:278`). The clamp exists so that the call returns inside the time Claude Code allows a tool call, and the caller asks again (`internal/stepmcp/tools.go:22`-`:23`). A timeout of 0 returns at once (`step.proto:121`).

Result: `{"ended": <step or null>, "steps": [<step>, ...]}` (`internal/stepmcp/tools.go:348`-`:356`). `ended` is the first step to end and is `null` when the timeout passes first. `steps` lists every named step in the order the request named them (`step.proto:127`-`:130`). On a timeout the caller reads each step's state from `steps`.

#### The step object

Every tool that reports a step returns these fields (`internal/stepmcp/tools.go:361`-`:377`, `:388`-`:416`):

- `id`, `agent`, `workspace`, `revision`, `readOnly`, `worktree`: as the `Step` message defines them (`step.proto:136`-`:152`). `worktree` is empty for a writing step.
- `end`, `reason`: empty while the step runs (`step.proto:154`, `:166`).
- `status`: the done value the return carried, or the status the step ended with. `result`: the latest result the return carried (`step.proto:157`-`:161`).
- `checks`: each `{round, name, ok, detail}` from the rounds the return's checks ran in (`internal/stepmcp/tools.go:389`-`:397`; `step.proto:163`-`:164`).
- `started`, `ended`: unix seconds. `ended` is 0 while the step runs (`step.proto:169`-`:173`).
- `limited`, `limitKind`, `limitResets`: whether a usage limit holds the step, the kind of that limit, and its reset in unix seconds. The kind is empty when it is unknown, and the reset is 0 when it is unknown. Both are empty or 0 when no limit holds the step (`step.proto:187`-`:195`; `internal/enginehost/stepservice.go:226`-`:238`).

#### epic_create

Arguments (`internal/stepmcp/epictools.go:18`-`:25`). Required: `contract`, the accepted contract as an object in protobuf JSON names, such as `releaseSteps`, with the fields of `EpicContract` (`proto/orrery/engine/v1/epic.proto:33`-`:93`). The contract also names its `base`, its `gates` (each with `name`, `argv`, `limit`, `needs`, `agent`, `must` and `mustNot`, as `EpicGate` gives them, `epic.proto:154`-`:175`), its `doneGates` and its `reviewGates`. A requirement names its evidence as `item`, with `capture`, `limit`, `artifacts`, `supplied`, `conclusion` and `judge` (`proto/orrery/engine/v1/epic.proto:178`-`:196`, `:126`-`:142`). The schema's description lists these names (`internal/stepmcp/epictools.go:18`-`:24`). Optional: `decisions`, an array of objects, each with a required `id` and `text`, which seed the epic's decisions in order. The arguments are decoded with protojson, which refuses a key that names no field (`internal/stepmcp/epictools.go:116`-`:121`, `:136`).

Result: the stored epic, a `StoredEpic` message in protobuf JSON (`proto/orrery/engine/v1/epic.proto:263`-`:294`). Unpopulated fields are emitted, so an empty section reads as `[]` (`internal/stepmcp/epictools.go:123`-`:131`, `:139`-`:143`). protojson writes each 64-bit integer as a JSON string, so `created`, `changed`, `seq` and `time` are strings (`go.mod:12`).

Refusals: a refused call is a tool error. Its text is the Connect error's text, which is its code, a colon and its message, such as `invalid_argument: id: bad form, want <repo-id>-<7 hex>/<slug>` (`internal/stepmcp/epictools.go:140`-`:142`; `internal/stepmcp/tools.go:160`-`:164`; `go.mod:7`). The refusals and their messages are those of [Epics](contract.md#epics). A call whose arguments do not decode is a tool error whose text is `decode arguments:` and the protojson error (`internal/stepmcp/epictools.go:116`-`:121`, `:136`; see [Protocol](#protocol)).

#### epic_get

Arguments: `id`, required (`internal/stepmcp/tools.go:42`-`:43`; `internal/stepmcp/epictools.go:49`). Result: the stored epic, as `epic_create` returns it (`internal/stepmcp/epictools.go:147`-`:159`). It also carries the epic's planner state: `paused`, `parked` (the park reason, empty when the epic is not parked), `turns`, `tasks` and `questions`, each in order (`proto/orrery/engine/v1/epic.proto:280`-`:293`; `internal/enginehost/epicservice.go:385`-`:397`). Each turn has `seq`, `step`, `state`, `reason`, `outline`, `outcome`, `decisions`, `started` and `ended`. Each task has the id `T<seq>`, and each question has the id `Q<seq>` (`proto/orrery/engine/v1/epic.proto:191`-`:219`, `:221`-`:246`, `:248`-`:261`; `internal/enginehost/epicservice.go:445`-`:485`). A turn's `decisions` are its return's decisions. Its refusals are those of `GetEpic`: `not_found` with `id: no epic <id>` (`internal/enginehost/epicservice.go:88`-`:90`), and the others of [Epics](contract.md#epics).

#### epic_list

Arguments: none. The schema is an object with no properties, and any key is refused (`internal/stepmcp/epictools.go:27`; `internal/stepmcp/epictools.go:116`-`:121`, `:164`). Result: `{"epics": [...]}`, the `ListEpicsResponse` in protobuf JSON, with an `EpicSummary` for each stored epic. Each summary has `id`, `repository`, `title`, `phase`, `created` and `changed`, and no sections (`internal/stepmcp/epictools.go:161`-`:173`; `proto/orrery/engine/v1/epic.proto:296`-`:315`). With no epics, `epics` reads as `[]`. Its refusals are `unavailable` with `epic store unavailable` when orreryd has no store, and `internal` when the list fails (see [Epics](contract.md#epics)).

#### epic_answer

Arguments: `epicId`, `id` and `text`, required, and `question`, optional: the `Q<n>` the decision answers. Leave `question` out for a note (`internal/stepmcp/epictools.go:29`-`:32`). Result: `{"decision": <EpicDecision>, "warning": <text>}`, the `AnswerEpicResponse` (`internal/stepmcp/epictools.go:175`-`:187`; `proto/orrery/engine/v1/epic.proto:368`-`:375`). The `decision` is an `EpicDecision` with `seq`, `id`, `text`, `time` and `question`, where `question` is empty for a note (`proto/orrery/engine/v1/epic.proto:173`-`:189`; `internal/enginehost/epicservice.go:435`-`:441`). `warning` is empty unless a note is recorded while questions are open. It then names them in `seq` order, as `recorded as a note: it answers no question, and Q1, Q2 are open` (`internal/enginehost/epicservice.go:141`-`:159`, `:200`-`:204`). An answer with a `question` marks that question `answered`, and its `warning` is empty (`internal/enginehost/epicservice.go:125`-`:139`; `internal/store/epics.go:380`-`:391`; `internal/store/queries.sql:503`-`:504`). A decision id already recorded with the same text returns the stored decision, as the tool's description says (`internal/stepmcp/epictools.go:59`-`:63`).

Its refusals are those of `AnswerEpic`, as [Epics](contract.md#epics) gives them, and the question's refusals (`internal/enginehost/epicservice.go:115`-`:139`, `:164`-`:177`):

- `invalid_argument` with `epic_id: empty`, `id: empty` or `text: empty` (`internal/enginehost/epicservice.go:208`-`:218`).
- `not_found` with `epic_id: no epic <id>`.
- `already_exists` with `id: <id> was recorded with other text`.
- `not_found` with `question: no question <question> on epic <epicId>`, when `question` is not `Q<n>` for a positive `n`, and when the epic holds no such question (`internal/enginehost/epicservice.go:128`-`:131`, `:170`-`:171`, `:180`-`:185`, `:188`-`:198`).
- `failed_precondition` with `question: <question> is closed`, when the question is no longer open (`internal/enginehost/epicservice.go:172`-`:173`).

The four control tools take `id`, and each returns the stored epic, read back after the write (`internal/enginehost/epicservice.go:236`-`:240`, `:258`-`:262`, `:287`-`:291`, `:309`-`:313`). An empty `id` is `invalid_argument` with `id: empty`, and an unknown epic is `not_found` with `id: no epic <id>` (`internal/enginehost/epicservice.go:229`-`:232`, `:316`-`:328`). A refusal is a tool error with the Connect error's text, and a refused control writes nothing (`internal/store/controls.go:15`-`:16`).

#### epic_pause

Arguments: `id`, required (`internal/stepmcp/epictools.go:71`). Result: the stored epic, with `paused` true (`internal/stepmcp/epictools.go:189`-`:200`). The write sets `paused`, so the planner starts no turn for the epic (`internal/store/controls.go:24`-`:40`; `internal/store/queries.sql:464`-`:465`). An epic that is already paused is refused with `failed_precondition` and the text `epic <id> is already paused` (`internal/store/controls.go:32`-`:33`; `internal/enginehost/epicservice.go:316`-`:328`).

#### epic_resume

Arguments: `id`, required (`internal/stepmcp/epictools.go:78`). Result: the stored epic, with `paused` false (`internal/stepmcp/epictools.go:203`-`:215`). The write clears `paused` and leaves any park in place, so an epic that is paused and parked resumes and stays parked (`internal/store/controls.go:42`-`:59`; `internal/store/queries.sql:467`-`:468`). An epic that is not paused is refused with `failed_precondition`: `epic <id> is not paused`, or, when the epic is parked, `epic <id> is parked, not paused; retry ends a park` (`internal/store/controls.go:50`-`:55`).

#### epic_stop

Arguments: `id`, required (`internal/stepmcp/epictools.go:85`). Result: the stored epic, with `paused` true (`internal/stepmcp/epictools.go:217`-`:229`). One write pauses the epic and flags its running turn to stop. The store returns the turn's step, which is empty while the turn has none (`internal/store/controls.go:63`-`:92`; `internal/store/queries.sql:416`-`:417`). The service then stops that step, as `StopStep` does (`internal/enginehost/epicservice.go:267`-`:292`). A turn with no step is stopped when its step's id is recorded (`internal/planner/runner.go:193`-`:194`). An epic with no running turn is refused with `failed_precondition` and the text `epic <id> has no running turn`, and nothing is stopped (`internal/store/controls.go:73`-`:74`). A failed step stop comes after the write, so the epic stays paused and flagged, and its error is `internal` with the error's text (`internal/enginehost/epicservice.go:282`-`:285`, `:336`-`:338`).

#### epic_retry

Arguments: `id`, required (`internal/stepmcp/epictools.go:92`). Result: the stored epic, with `parked` empty (`internal/stepmcp/epictools.go:231`-`:243`). One write ends the park and sets the epic's `retried` to its highest turn seq, so the failed turns up to that seq stop counting toward a park. Its triggers stay pending, and `paused` is unchanged (`internal/store/controls.go:94`-`:96`, `:110`; `internal/store/queries.sql:470`-`:471`). An epic that is not parked is refused with `failed_precondition` and the text `epic <id> is not parked` (`internal/store/controls.go:103`-`:104`).

#### Evidence tools

The three evidence tools call the `orrery.engine.v1.Evidence` service on `sessions.sock` (`proto/orrery/engine/v1/epic.proto:21`-`:30`). Their schemas are `evidenceRunSchema`, `evidenceListSchema` and `evidenceGetSchema` (`internal/stepmcp/evidencetools.go:14`-`:27`). Every argument object refuses unknown keys, as the other tools do. A refusal is a tool error whose text is the Connect error's code and message (see [Protocol](#protocol)).

`evidence_run` runs an item's evidence. Required: `epic`, `item`, `revision` and `phase`, all strings. `item` is a Requirement's id, `R<n>`, and any other name is refused. `phase` is `red` or `green`. `revision` is any revision git resolves (`internal/stepmcp/evidencetools.go:15`-`:18`; `internal/enginehost/evidencerun.go:119`-`:136`). Result: the `EvidenceRun` the call stored, in state `capturing`, with no verdict yet. The run is driven in the background, and [Evidence](contract.md#evidence) gives what happens next. Its refusals are those of `RunEvidence`: `invalid_argument` with `epic: empty`, `item: empty`, `revision: empty` or `phase: empty` (`internal/enginehost/evidenceservice.go:109`-`:123`), and with the reason of a refused item, phase or revision (`internal/enginehost/evidencerun.go:129`, `:133`-`:135`, `:211`); `not_found` with `epic: no epic <id>` (`internal/enginehost/evidenceservice.go:127`-`:136`).

`evidence_list` lists an epic's runs. Required: `epic`. Result: the `ListEvidenceResponse` in protobuf JSON, `{"runs": [...]}`, with each run's artifacts. The runs come in the order the store gives them, by creation and then id (`internal/stepmcp/evidencetools.go:20`-`:21`; `internal/store/evidence.go:200`-`:208`). An empty `epic` is `invalid_argument` with `epic: empty` (`internal/enginehost/evidenceservice.go:70`-`:72`).

`evidence_get` returns one artifact of a run. Required: `run`. Optional: `name`, the artifact's repository-relative path, which is omitted for the run's log. Result: the `GetEvidenceArtifactResponse` in protobuf JSON, with `data` as base64 and `hash` (`internal/stepmcp/epictools.go:79`-`:85`; `internal/enginehost/evidenceservice.go:94`-`:106`). Its refusals: `invalid_argument` with `run: empty`, `not_found` with `run: no run <id>` for an unknown run, and `not_found` with `name: no kept artifact <name>` for a name the run does not keep (`internal/enginehost/evidenceservice.go:94`-`:102`).

### The planner's return

A planner turn's step returns through a forced return, as [section 13](#13-forced-returns) describes. Its schema is `Schema` in `internal/planner/plan.go`, and the step starts with it (`internal/planner/runner.go:191`; `internal/planner/plan.go:12`-`:51`). The schema requires `status`, `decisions`, `tasks`, `outline`, `questions` and `outcome`, and refuses any other key at the top level. `status` must be `done`, and `outcome` must be `work`, `wait` or `complete`. Each task requires `title`, `deliverable`, `files`, `requirements`, `gate` and `evidence`, and refuses any other key. The `evidence` object requires `capture`, `limit`, `artifacts`, `supplied`, `conclusion` and `judge`, and refuses any other key, so a task that still names `check` fails the schema (`internal/planner/plan.go:19`-`:44`, `:27`-`:39`). A return that passes the schema is then checked by the plan check, and a refusal is sent back to the session (see [Planner turns](contract.md#planner-turns)).

### Usage-limit hold

A usage limit holds a step until the step is resumed or stopped. [Step lifecycle](contract.md#step-lifecycle) of the shared contract, under Usage limit, gives the rules. This subsection gives what the tools and the protocol show.

- A turn is limited by a rejected rate limit stored at or after its prompt, or by an assistant error `rate_limit` (`internal/enginehost/steploop.go:404`-`:422`; `internal/enginehost/steploop.go:205`-`:208`; `internal/engine/session/translate.go:295`-`:310`). A limited turn is no round, and the step is released: its process is stopped and its slot is freed, and the step stays open (`internal/enginehost/steploop.go:373`-`:375`, `:431`; `internal/enginehost/stepsession.go:100`-`:118`).
- A limited step spends nothing. Its ticks check nothing, so the watch makes no monitor call, and its exits are ignored (`internal/enginehost/steploop.go:266`-`:274`, `:298`-`:303`). The step stays limited through exits and restarts, and nothing resumes it at its reset time (`internal/enginehost/steploop.go:324`-`:327`; `internal/enginehost/stepctl.go:227`-`:229`; `internal/enginehost/limitnotices.go:97`-`:100`).
- `step_status` and `step_wait` report the hold in the step object's `limited`, `limitKind` and `limitResets` fields (see [The step object](#the-step-object)). `step_resume` resumes the step (see [step_resume](#step_resume)).
- A planner turn's limited step is stopped at the runner's first timed-out wait that shows it limited (`internal/planner/runner.go:298`-`:324`; see [Planner turns](contract.md#planner-turns)).
- An agent gate step that is limited at a restart stays limited when the restart rebuilds its gate, as it rebuilds an evidence judge's (see [Evidence](contract.md#evidence)); any other agent gate step ends `engine_restarted` (`internal/enginehost/stepctl.go:152`-`:154`, `:201`-`:205`, `:227`-`:231`).
- The owner gets one limit notice for each limit key, `<kind>@<reset>`. It is updated when the steps it names change, and cleared when no step holds that key (`internal/enginehost/limitnotices.go:93`-`:141`). Its push carries the `limit` key, and so does its clear (`internal/push/publish.go:145`-`:147`, `:179`-`:181`).

### Caller facts

These are Claude Code 2.1.321 behaviors that the tool shapes answer to. No code or fixture in this repository records them.

- A stdio tool call that stays silent is aborted after 30 minutes (`CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT`). The 25-minute clamp on `step_wait` keeps a wait inside that limit.
- In an interactive session, a call still running after 120 seconds is moved to the background, unless `CLAUDE_CODE_MCP_AUTO_BACKGROUND_MS=0` is set. Step callers set it to 0.
- The server ignores `notifications/cancelled`, so a cancelled `step_wait` holds its call slot until its timeout (see Protocol above).

## 15. The step watch and judge

The findings of this section come from live probes on Claude Code 2.1.321 with `claude-haiku-5-5`, and from the code that runs them. The probe output is not committed, so each claim cites the code that relies on it. The watch and the judge run beside a step's loop, and [Step lifecycle](contract.md#step-lifecycle) gives the rules they follow.

- **Clocks.** The watch never reads the clock. Its loop passes each reading in (`internal/enginehost/stepwatch.go:91-92`, `internal/enginehost/steploop.go:209`). Model time and silence advance only while a turn runs with no main-conversation tool call open. Turn time advances for the whole turn (`internal/enginehost/stepwatch.go:117-131`). A turn start restarts turn time and clears the open calls, and a turn end or an exit clears them too. Any event restarts the silence count (`internal/enginehost/stepwatch.go:140-151`, `internal/enginehost/stepwatch.go:161`).
- **Trips.** The loop checks the watch on each tick, which is five seconds by default (`internal/enginehost/step.go:43`, `internal/enginehost/step.go:342`). A tick raises the first of four trips that is due, in this order (`internal/enginehost/stepwatch.go:213-252`). No trip is raised while no turn runs or while a trip waits for its verdict (`internal/enginehost/stepwatch.go:217`). A landed nudge turns off the periodic check until the next turn begins (`internal/enginehost/stepwatch.go:241`).
  - `overtime`: model time reaches the step's limit. The limit is `model_limit_seconds` on `StartStep`, 60 minutes when it is zero (`internal/enginehost/step.go:38-39`, `internal/enginehost/step.go:215-218`, `internal/enginehost/stepservice.go:66`).
  - `tool_stuck`: a main-conversation tool call has been open for 30 minutes of wall time, or 60 minutes after its extension (`internal/enginehost/stepwatch.go:17-18`, `internal/enginehost/stepwatch.go:256-276`).
  - `silent`: 10 minutes of model time with no event (`internal/enginehost/stepwatch.go:16`, `internal/enginehost/stepwatch.go:236-240`).
  - `periodic`: five minutes of turn time since the turn began or since the last verdict was applied (`internal/enginehost/stepwatch.go:15`, `internal/enginehost/stepwatch.go:241-245`, `internal/enginehost/stepwatch.go:285`).
- **Gate limit.** An agent gate step's unheld tick checks its gate's `limit` before the trips, whether or not a turn runs. At the first tick at or past the limit of wall time from the run's start, or from its latest resume from a usage limit (`internal/enginehost/stepowner.go:134`-`:136`), the step ends `overtime` with the reason `agent gate ran past its limit of N seconds`, where N is the limit in seconds. No monitor call is made and no verdict is stored (`internal/enginehost/stepwatchloop.go:20`-`:27`, `internal/enginehost/steploop.go:193`, `internal/enginehost/steploop.go:255`-`:255`).
- **Monitor call.** A trip makes one call on the step's Haiku caller, and the loop waits for it. It handles no return or exit until the call ends (`internal/enginehost/stepwatchloop.go:43-48`, `internal/enginehost/steploop.go:255`). The input is the monitor instruction, a `## Why` section with the trip's sentence, and a `## Recent events` section (`internal/enginehost/stepmonitor.go:43-45`). The recent events are the last 40 finished blocks and notices, oldest first, each with its age and its text cut to 300 bytes (`internal/enginehost/stepwatch.go:19-20`, `internal/enginehost/stepwatch.go:191-208`, `internal/enginehost/stepwatch.go:338-352`).
- **Argv and environment.** The call runs `claude -p --model claude-haiku-5-5 --settings {"disableAllHooks":true} --no-session-persistence --tools "" --max-turns 1 --output-format json --json-schema <schema>` in `/`. The model is `protocol.DefaultSummaryModel` (`internal/enginehost/haiku.go:174-184`, `internal/enginehost/haiku.go:94`, `internal/protocol/protocol.go:175`). Its environment is orreryd's, without `ANTHROPIC_BASE_URL` and `ORRERY_SUMMARIZER`, with `ORRERY_SUMMARIZER=1` added (`internal/enginehost/haiku.go:187-196`).
- **Cap and timeout.** orreryd builds one Haiku caller with the defaults for the whole process (`cmd/orreryd/main.go:489`, `internal/enginehost/haiku.go:18-21`). Two calls run at once, shared by every step's monitor and judge calls. Each call has 60 seconds, which covers the wait for a slot and the run together (`internal/enginehost/haiku.go:70-82`). At the timeout the claude process group is killed, and the call waits at most 2 seconds for its output pipes to close (`internal/enginehost/haiku.go:24`, `internal/enginehost/haiku.go:99`, `internal/enginehost/haiku.go:113-119`).
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
- **Failed call.** A monitor call that fails, or whose reply does not parse, stores its error with an empty verdict and an empty doing line (`internal/enginehost/stepwatchloop.go:63-72`). The verdict is then empty, so the trip takes its mechanical action: `overtime` and `tool_stuck` end the step, `silent` nudges on its first trip and ends on the next, and `periodic` takes `none` (`internal/enginehost/stepwatch.go:55-56`). A run with no Haiku caller does the same, with its own error (`internal/enginehost/stepwatchloop.go:64-66`).
- **Nudge.** The first nudge interrupts the running turn with `Interrupt` (`internal/enginehost/stepwatchloop.go:89-94`, `internal/enginehost/manager.go:614`). It sets the text the next turn end is continued with: the trip's summary and doing line, then `Continue the step where it stopped, or return now if it is finished.` (`internal/enginehost/stepwatchloop.go:95-97`). The interrupt ends the turn with `error_during_execution` (see [What follows an interrupt](#what-follows-an-interrupt)). That turn end is no round. The loop neither checks nor stores it, though an agent gate step keeps the first `fails` result of its output when that output passes `schema` (`internal/enginehost/steploop.go:397`, `internal/enginehost/stepverdict.go:72`-`:84`), and it sends the nudge text with `Continue` (`internal/enginehost/steploop.go:396`-`:398`). A turn end that is not an error drops the nudge and is an ordinary round (`internal/enginehost/steploop.go:377`, `internal/enginehost/steploop.go:396`, `internal/enginehost/steploop.go:400`-`:400`).
- **Nudge spending.** A landed nudge spends the step's one nudge, restarts the silence count, and turns off the periodic check until the next turn begins. Until then each open call's 30 minutes, or 60 after its extension, count from the nudge. The silent, tool_stuck and overtime trips keep running in the interrupted turn (`internal/enginehost/stepwatch.go:319-327`, `internal/enginehost/stepwatch.go:264-267`, `internal/enginehost/stepwatch.go:143`, `internal/enginehost/stepwatch.go:241`). A nudge does not count toward the three send-backs. Only `retry` counts those (`internal/enginehost/steploop.go:729`-`:733`).
- **Failed interrupt.** A failed `Interrupt` is logged, the action is `none`, and the nudge is undone. The step's nudge is not spent, open calls count from their start again, and the watch keeps watching the running turn (`internal/enginehost/stepwatchloop.go:90-94`, `internal/enginehost/stepwatch.go:329-336`). A failed `Continue` of a nudge ends the step `failed` (`internal/enginehost/steploop.go:449`-`:451`).
- **Extension.** A `long_command` verdict on a `tool_stuck` trip extends that call once, from 30 minutes to 60 minutes of wall time. The extension belongs to that call, so a later call has its own (`internal/enginehost/stepwatch.go:79-83`, `internal/enginehost/stepwatch.go:260-263`, `internal/enginehost/stepwatch.go:304-307`).
- **Watch ends.** A watch end stores its verdict first. A failure to store it ends the step `failed` (`internal/enginehost/stepwatchloop.go:50-53`). The step then ends with the verdict's end kind, no status, and the reason `<trip> trip: <why>`, followed by ` Doing: <doing>` when the doing line is not empty (`internal/enginehost/stepwatchloop.go:54-56`, `internal/enginehost/stepwatchloop.go:97-103`). The watch ends the step `overtime`, `tool_stuck`, `silent`, `looping` or `wedged` (`internal/enginehost/stepwatch.go:50-52`, `internal/enginehost/stepwatch.go:293-313`). The judge ends the step `goal_unmet` (`internal/enginehost/stepjudgeloop.go:23`). The gate limit's `overtime` (see Gate limit) makes no monitor call and stores no verdict row, so the first sentence does not apply to it.
- **Verdicts in the store.** Each verdict is appended to the row's `verdicts` with its time, its kind (`monitor` or `judge`), its trip, its round, its verdict, its detail, its error and its action (`internal/store/steps.go:27-35`, `internal/store/steps.go:265`-`:267`). A monitor's detail is its doing line (`internal/enginehost/stepwatchloop.go:43`). A judge's detail is its reason (`internal/enginehost/stepjudgeloop.go:50`). A non-empty doing line replaces the row's `doing` (`internal/store/steps.go:272`-`:275`).
- **Judge.** A return that passes every check and whose `status` equals `done_value` is judged, except an agent gate step's return, which is never judged: its verdict ends it `done` or `gate_failed`, as [Checks](contract.md#step-lifecycle) gives (`internal/enginehost/steploop.go:560`-`:563`, `internal/enginehost/stepverdict.go:140`-`:147`). A return that fails a check is sent back and is not judged (`internal/enginehost/steploop.go:557`-`:559`). Without a Haiku caller the judge makes no call, and the step ends `done` (`internal/enginehost/stepjudgeloop.go:35-37`).
- **Judge input.** The input is the judge instruction, then `## Task` with the step's first prompt, then `## Return` with the return. A writing step adds `## Diff stat`, the `git diff --stat` from the start commit to the return's commit. A step with a gate adds `## Gate output tail`, the gate check's detail (`internal/enginehost/stepjudge.go:31-34`, `internal/enginehost/stepjudge.go:45-57`, `internal/enginehost/stepjudgeloop.go:88-103`, `internal/enginehost/stepjudgeloop.go:106-126`). A blank diff stat or gate detail is omitted (`internal/enginehost/stepjudge.go:50`, `internal/enginehost/stepjudge.go:53`). Each section keeps its first 8 KiB, and the gate output keeps its last 8 KiB (`internal/enginehost/stepjudge.go:29`, `internal/enginehost/stepjudge.go:59-81`).
- **Judge verdicts.** The reply is `met` or `unmet`, with a one-line reason (`internal/enginehost/stepjudge.go:18-26`, `internal/enginehost/stepjudge.go:85-107`).
  - `met` ends the step `done` (`internal/enginehost/stepjudgeloop.go:50-56`).
  - The first `unmet` stores its verdict with action `continue`, sends the session back once with the reason, and waits for the next return. That return is judged again when it passes its checks (`internal/enginehost/stepjudgeloop.go:59-79`, `internal/enginehost/stepjudgeloop.go:26-27`). The send-back does not count toward the three send-backs (`internal/enginehost/steploop.go:729`-`:733`).
  - A second `unmet` ends the step `goal_unmet`, and the step's return is kept (`internal/enginehost/stepjudgeloop.go:60-67`).
  - A failed diff stat, a failed call or an unparsable reply stores its error with no verdict and action `none`, and ends the step `done` (`internal/enginehost/stepjudgeloop.go:41-49`, `internal/enginehost/stepjudgeloop.go:91-103`).
  - A failed `Continue` of the judge's send-back ends the step `failed` (`internal/enginehost/stepjudgeloop.go:77-79`).
