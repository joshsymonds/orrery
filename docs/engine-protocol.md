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
