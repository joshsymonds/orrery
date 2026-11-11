# Orrery shared contract (frozen)

Status: **frozen**. Effort 1 defines this contract. Later efforts and the effort-4 golden test build on the names and values below. A change to any name or value here is a contract change and needs its own decision.

Module path: `github.com/joshsymonds/orrery`.

The Go code that implements each part lives in one of two packages:

- `internal/protocol`: pane state, tmux option names, the `orrery list --json` element, environment names, the daemon socket path, the summarizer sentinel, the summary model map, the question hash, and the hook-to-daemon frame.
- `internal/tmuxctl`: the tmux client that runs the `tmux` binary and reads and writes the per-pane options.

## 1. Pane state

The state of a pane is one of three strings:

| Go constant | Value |
|---|---|
| `protocol.StateWorking` | `working` |
| `protocol.StateWaiting` | `waiting` |
| `protocol.StateDone` | `done` |

Go type: `type State string`. `func ParseState(s string) (State, error)` returns the State for exactly those three strings. It returns an error for any other input, including the empty string.

## 2. Per-pane tmux options

Options are set at pane scope with tmux 3.8 `set-option -p`, except `@orrery_dir`, which is the one session-scoped option (see below).

| Go constant | Option name | Value |
|---|---|---|
| `protocol.OptionState` | `@orrery_state` | `working`, `waiting`, or `done` |
| `protocol.OptionChanged` | `@orrery_changed` | decimal unix seconds of the last state change |
| `protocol.OptionSeen` | `@orrery_seen` | decimal unix seconds the user last looked at the pane |
| `protocol.OptionQuestion` | `@orrery_question` | single-line text |
| `protocol.OptionSummary` | `@orrery_summary` | single-line text |
| `protocol.OptionLabel` | `@orrery_label` | single-line text |
| `protocol.OptionContext` | `@orrery_context` | the patchbay context name from the pane's last applied hook frame, the `SummaryContext` (section 8) of its `anthropicBaseUrl`; `""` for the default route |
| `protocol.OptionDir` | `@orrery_dir` | the absolute project directory after `filepath.Clean`, symlinks not resolved; session scope, see below |
| `protocol.OptionClaudeSession` | `@orrery_claude_session` | the Claude Code session id, the hook payload's `session_id` |
| `protocol.OptionTranscript` | `@orrery_transcript` | the Claude Code transcript path, the hook payload's `transcript_path` |
| `protocol.OptionTool` | `@orrery_tool` | compact single-line JSON of the tool a `PreToolUse` started that no end event has cleared, with the keys `id`, `name`, `input`, `start` (see below) |
| `protocol.OptionID` | `@orrery_id` | the pane's row id in the store: 32 lowercase hex characters from 128 random bits. orreryd mints it once, when the pane has none, and keeps it. It is the `id` key of the `sessions` table (see [Store](#store)) |

orreryd writes each of the two new options from every hook frame whose hook decodes, when the payload value is non-empty. SessionEnd keeps both options, except SessionEnd with reason `clear`, which unsets both. `@orrery_transcript` is not a list field.

`@orrery_tool` is written from the hook events of its pane:

- `PreToolUse` with a non-empty `tool_use_id` sets it. A `PreToolUse` without one leaves it unchanged.
- `PostToolUse` and `PostToolUseFailure` unset it when their `tool_use_id` equals the stored `id`. When their `tool_use_id` is absent or empty, differs from the stored `id`, or the stored value does not decode, they leave it unchanged.
- `Stop` and `SessionEnd` unset it.
- Any other event leaves it unchanged.
- While the pane is `done`, a tool event less than 5 seconds after `@orrery_changed` is a late frame. It leaves the pane's state, `@orrery_changed`, `@orrery_question`, and `@orrery_tool` unchanged. `@orrery_context`, `@orrery_claude_session`, and `@orrery_transcript` are still written from it as from any frame.
- Apart from a late frame, a `PreToolUse`, `PostToolUse`, or `PostToolUseFailure` whose `tool_use_id` is present, not `null`, and not a JSON string is an error, and so is a `PreToolUse` with a non-empty `tool_use_id` whose `tool_name` is present, not `null`, and not a JSON string. Other events never read either field. orreryd writes no option from an erroring event.

The value is compact single-line JSON with the keys `id`, `name`, `input`, and `start`, in that order:

- `id` is the `tool_use_id`, `name` is the `tool_name`, and `start` is the unix seconds of the `PreToolUse`.
- `input` is the display text of `tool_input`. When `tool_input` is an object, it is the first non-empty string value among `command`, `file_path`, `path`, `pattern`, `url`, `query`, `prompt`, and `description`, tried in that order. Otherwise it is the compact JSON of `tool_input`. Absent, `null`, or invalid input gives `""`. Line breaks become spaces, and the text is cut to 200 runes.

Example value. Every value is synthetic: `{"id":"example-tool-use","name":"Bash","input":"example command","start":1700000250}`.

`@orrery_dir` is a tmux user option set on the session with `set-option -t <session>`, without `-p`. It is not a field of `orrery list --json`.

- `orrery open` writes it whenever it creates a session, including `new`. Sessions whose names begin with `_orrery-shell-` carry it too.
- `orrery open` reads it to find a directory's session before it falls back to the session name.
- Its absence means the session is not an orrery project session.

Example value. The path is synthetic: `/home/user/project`.

Rules for every option value:

- Values are single-line. Writers replace newlines with spaces before they set an option.
- Timestamps are decimal unix seconds.

`protocol.ShellSessionPrefix` is `_orrery-shell-`. A session whose name begins with that prefix is a shell session. The pane tree leaves shell sessions out.

## 3. `orrery list --json`

`orrery list --json` prints a JSON array of pane objects. When no pane exists it prints `[]`.

Each element is a `protocol.Pane`. Every field is always present, with the exact JSON key shown below.

| Go field | Go type | JSON key | JSON type | Source | Unset value |
|---|---|---|---|---|---|
| ID | string | `id` | string | tmux `#{pane_id}`, for example `%3` | none, always set |
| Host | string | `host` | string | host name of the machine running the tmux server | none, always set |
| Session | string | `session` | string | `#{session_name}` | none, always set |
| WindowIndex | int | `window` | number | `#{window_index}` | none, always set |
| PaneIndex | int | `pane` | number | `#{pane_index}` | none, always set |
| Cwd | string | `cwd` | string | `#{pane_current_path}` | none, always set |
| Command | string | `command` | string | `#{pane_current_command}` | none, always set |
| State | State | `state` | string | `@orrery_state` | `""` |
| Summary | string | `summary` | string | `@orrery_summary` | `""` |
| Label | string | `label` | string | `@orrery_label` | `""` |
| Changed | int64 | `changed` | number | `@orrery_changed` | `0` |
| Seen | int64 | `seen` | number | `@orrery_seen` | `0` |
| Question | string | `question` | string | `@orrery_question` | `""` |
| Context | string | `context` | string | `@orrery_context` | `""` |
| Activity | int64 | `activity` | number | tmux `#{session_activity}`, unix seconds of the pane's session's last activity | none, always set |
| Tool | string | `tool` | string | `@orrery_tool` (section 2), as the JSON text of that value | `""` |
| ClaudeSession | string | `claude_session` | string | `@orrery_claude_session` | `""` |

There are seventeen fields. The `state` value is one of the strings in section 1, or `""` when the option is unset.

- `claude_session` is the Claude Code session id of the pane (section 2). The archive records it (section 11), and `orrery open` passes it to `claude --resume`.
- `activity` is tmux's `session_activity` for the pane's session: unix seconds of that session's last activity. The tree's quiet fold (section 11, Tree keys) reads the newest `activity` of a project's panes.
- `tool` is the `@orrery_tool` value of section 2 as one JSON string that holds that JSON text. A consumer decodes it again to read `id`, `name`, `input`, and `start`. It is `""` when the option is unset.

Example output with one pane. Every value is synthetic:

```json
[{"id":"%1","host":"example-host","session":"example-session","window":0,"pane":0,"cwd":"/home/user/project","command":"claude","state":"waiting","summary":"Example summary of the pane's work.","label":"example-label","changed":1700000000,"seen":1700000100,"question":"Example question for the user?","context":"example-context","activity":1700000300,"tool":"{\"id\":\"example-tool-use\",\"name\":\"Bash\",\"input\":\"example command\",\"start\":1700000250}","claude_session":"example-claude-session"}]
```

## 4. Environment variables

| Go constant | Variable | Meaning |
|---|---|---|
| `protocol.EnvSocket` | `ORRERY_SOCKET` | daemon socket path override |
| `protocol.EnvNtfyURLFile` | `ORRERY_NTFY_URL_FILE` | path of a file holding the ntfy topic URL |
| `protocol.EnvNtfyAuthFile` | `ORRERY_NTFY_TOKEN_FILE` | path of a file holding the ntfy access token |
| `protocol.EnvSummarizer` | `ORRERY_SUMMARIZER` | summarizer sentinel, see section 7 |

## 5. Daemon socket path

The daemon listens on a unix socket. Its default path is:

```
$XDG_RUNTIME_DIR/orrery/orreryd.sock
```

`func SocketPath(flagValue string, getenv func(string) string) (string, error)` picks the path with this precedence:

1. A non-empty `flagValue` wins. The CLI passes the `--socket <path>` value here.
2. Otherwise a non-empty `ORRERY_SOCKET` wins.
3. Otherwise the default `$XDG_RUNTIME_DIR/orrery/orreryd.sock` is used.
4. If all three are empty, `SocketPath` returns an error.

`orrery` and `orreryd` both accept `--socket <path>`, resolved through `protocol.SocketPath`.

## 6. tmux server override

`orrery` and `orreryd` both accept `-L <name>` and `-S <path>`. These map to `tmuxctl.Server`:

```go
type Server struct { Name string; Socket string }
```

- `Name` adds `-L <name>` to every tmux invocation.
- `Socket` adds `-S <path>` to every tmux invocation.
- Both empty means the default tmux server.
- Both set is an error. `func New(server Server) (*Client, error)` validates the Server and returns that error.

`tmuxctl.Client` runs the `tmux` binary found on PATH with an argv list. It never uses a shell. Its API is:

- `func (c *Client) Run(ctx context.Context, args ...string) (string, error)` runs `tmux <server flags> <args...>` and returns stdout. On failure it returns a wrapped error that carries tmux's stderr.
- `func (c *Client) SetPaneOption(ctx context.Context, paneID, name, value string) error`
- `func (c *Client) UnsetPaneOption(ctx context.Context, paneID, name string) error`
- `func (c *Client) PaneOption(ctx context.Context, paneID, name string) (string, bool, error)` returns `ok` false with a nil error when the option is unset, and `ok` true when it is set, including when it is set to `""`.
- `func (c *Client) ListPanes(ctx context.Context) ([]protocol.Pane, error)` returns every pane of every session on the server. It fills every Pane field except `Host`, which it leaves `""` for the caller. Unset options give zero values. A non-numeric `@orrery_changed` or `@orrery_seen`, or an invalid `@orrery_state`, is an error. Values that contain spaces and tabs round-trip unchanged.

## 7. Summarizer sentinel

`ORRERY_SUMMARIZER=1` marks a process as the summarizer's own `claude` subprocess.

- Constants: `protocol.EnvSummarizer = "ORRERY_SUMMARIZER"` and `protocol.SummarizerValue = "1"`.
- `func IsSummarizer(getenv func(string) string) bool` returns true exactly when `ORRERY_SUMMARIZER` equals `"1"`.
- The summarizer sets this variable on its own `claude` subprocess. The hook drops events that come from such a process.

## 8. Summary model map

- `const DefaultSummaryModel = "claude-haiku-5-5"`
- `func SummaryContext(baseURL string) string` returns the `<name>` from the first `/ctx/<name>` path segment pair of `baseURL`, which is a patchbay context route. It returns `""` when there is no such pair or when the URL does not parse.
- `func SummaryModel(baseURL string, models map[string]string) string` returns `models[SummaryContext(baseURL)]` when the context is non-empty and maps to a non-empty model. Otherwise it returns `DefaultSummaryModel`.

Example context routes. Both URLs are synthetic:

- `https://patchbay.example.com/ctx/example-context` gives the context `example-context`.
- `https://patchbay.example.com/v1` gives no context, so the default model applies.

The caller in effort 3 owns the contents of the `models` map.

## 9. Hook-to-daemon frame

Claude Code hooks send their events to the daemon as frames.

`type HookFrame struct` has these JSON keys:

| Go field | Go type | JSON key | Source |
|---|---|---|---|
| Hook | `json.RawMessage` | `hook` | the Claude Code hook's stdin JSON object, verbatim |
| TmuxPane | string | `tmuxPane` | `$TMUX_PANE` |
| Tmux | string | `tmux` | `$TMUX` |
| Cwd | string | `cwd` | the hook process working directory |
| AnthropicBaseURL | string | `anthropicBaseUrl` | `$ANTHROPIC_BASE_URL` |

Wire format:

- One frame per connection.
- One JSON object, terminated by `\n`. The object is written on a single line.

Functions:

- `func WriteFrame(w io.Writer, frame HookFrame) error` writes exactly that format.
- `func ReadFrame(r io.Reader) (HookFrame, error)` decodes one frame. It has no line-length limit, because hook payloads can be large. It returns an error on malformed JSON, and when `hook` is missing, is `null`, or is not a JSON object.

Example frame. Every value is synthetic. The wire form is this object on one line, terminated by `\n`:

```json
{"hook":{"hook_event_name":"Stop","session_id":"example-session"},"tmuxPane":"%1","tmux":"/tmp/example-tmux,1234,0","cwd":"/home/user/project","anthropicBaseUrl":"https://patchbay.example.com/ctx/example-context"}
```

## 10. Question hash

H is the question hash. R8's `--expect-question H` compares H against the value this section defines.

- `func QuestionHash(question string) string` returns the lowercase hex SHA-256 of the raw `@orrery_question` option value's bytes, exactly as stored. It applies no trimming and no other normalization.
- An unset question hashes as the empty string, so `QuestionHash("")` is the hash of the empty input.
- `protocol.QuestionHash` is the only function that computes H. Every consumer calls it.

Fixed vectors. Both inputs are synthetic:

| Input | H |
|---|---|
| `Example question for the user?` | `7f593eeb8a031e6a99b02a9b65b8e03a17a82a20ca4862ecda37bd06c479a5eb` |
| `""` (unset) | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |

## 11. Commands and configuration

This section documents the command lines and configuration that ship with `orrery` and `orreryd`. Every name is synthetic in the examples.

### orrery

```
orrery [-L NAME | -S PATH] [--socket PATH] SUBCOMMAND
```

- `-L NAME` and `-S PATH` select the tmux server, as section 6 defines.
- `--socket PATH` sets the daemon socket, as section 5 defines.

Subcommands:

- `hook` reads one Claude Code hook JSON object on stdin, sends it to `orreryd` as a frame (section 9), and always exits 0 silently.
- `list --json` prints the pane list defined in section 3.
- `seen --pane ID` marks the pane as seen.
- `send --pane ID [--expect-question H] (--text T | --keys K...)` sends text or keys to the pane. Put the flags before `--keys`. A key that spells a send flag is refused.
  - `--expect-question H` sends only when the pane has a question, H equals its question hash (section 10), and the question's display fragment is visible on the pane. Otherwise nothing is sent and the command exits 3.
    - The display fragment is derived from the stored `@orrery_question` value. For an AskUserQuestion value it is `questions[0].question`. Otherwise, when `input` is an object, it is the first non-empty of `command`, `file_path`, `url`, then the first string field of `input` in document order. Otherwise it is the `tool` value. A stored value that is not a JSON object is its own fragment.
    - The fragment is its first non-blank line, with whitespace runs collapsed to one space, trimmed, cut to its first 40 runes, and trimmed again. The screen from `capture-pane -p -J` is compared with its whitespace runs collapsed to one space. An empty fragment refuses.
  - `--text T` types T into the pane when all of these hold. Otherwise T is pasted.
    - The pane's `@orrery_state` is `working` or `done`, and `#{pane_in_mode}` is 0.
    - Claude Code's input box is on the visible screen, as `capture-pane -p` shows it. The lower rule is the lowest line made only of `─` (U+2500) that is exactly as wide as the pane. The upper rule is the nearest line above it that is either a plain rule, made only of `─` and exactly as wide as the pane, or a titled rule. A titled rule is one or more `─`, a space, text of at least one character with no `─`, a space, and exactly one `─`. Its width and the width of a plain rule are counted in runes. The line directly under the upper rule starts with `❯` (U+276F) followed by a space, a no-break space (U+00A0), or nothing. Apart from the prompt line, the lines between the rules are blank or indented by two spaces. Below the lower rule, trailing blank lines are dropped first. An overflow row is a line that starts with two spaces, then `↓` (U+2193), a space, one or more ASCII digits, and ` more`. If the last remaining line is an overflow row directly under an agent row, that overflow row is left out. An overflow row anywhere else below the lower rule does not match. An agent row is a line that starts with two spaces, then `●` (U+25CF) or `◯` (U+25EF), then a space, then at least one more character. If the last remaining line is an agent row, the trailing agent rows must have exactly one blank line above them, and a non-blank line above that blank line, both below the lower rule. The footer is then the lines below the lower rule down to that blank line, which leaves out the lower rule and the agent rows. Otherwise the footer is every line below the lower rule. The footer holds at most 3 non-blank lines and no agent row or overflow row. A selected row starts with `❯` and a space in place of an agent row's two leading spaces. It is not an agent row, so a screen with a selected row anywhere below the lower rule does not match.
    - Claude Code's feedback survey draws its question and the options keyed `1`, `2`, `3` and `0` above the input box. The input-box rule above still matches while the survey is on. With the draft empty, a typed reply whose first character is one of those digits has that key taken as a survey answer. A pasted reply is not affected. Set `CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1`, for example in the `env` block of Claude Code's settings, to turn the survey off; typed delivery expects it off. orrery does not detect the survey.
    - The text, after CRLF and then CR become LF, is valid UTF-8. It holds no control character other than LF, so no tab. It needs at most 200 `send-keys` calls before Enter, and each line break is one of them.
    - Text sent to a `working` pane is typed in the same way. Claude Code queues it until the reply ends.
  - Typing sends each line in pieces of at most 400 bytes, cut on character boundaries. Each piece is one `send-keys -l` call. A piece that ends in `;` gets a backslash before the `;`, because tmux drops a trailing `;`. Each line break is `C-j`, and a blank line is only `C-j`. The calls are about 20 ms apart. Enter is sent as its own key after a 300 ms pause.
  - Once typing starts, it runs to its end under a 15-second limit, even if the caller has gone away.
  - Otherwise T is pasted. It goes through a buffer that only this call uses, with `paste-buffer -p`, and Enter is then sent as its own key. tmux replaces each LF with CR and sanitizes other control characters with vis(3).
- `debug summarize --pane ID --transcript PATH [--claude PATH]` runs one synchronous summary for the pane. The base URL comes from `ANTHROPIC_BASE_URL`. The command writes `@orrery_summary` and `@orrery_label` on the pane.
- `shell --dir DIR --client TTY` opens the popup shell for `DIR` on the client at `TTY`. `DIR` must be absolute.
  - The shell session is named `_orrery-shell-` followed by the first 12 hex digits of the SHA-256 of the cleaned `DIR`. It is created detached in `DIR`, with `@orrery_dir` set to `DIR`, unless it already exists. The popup attaches it.
  - Shell sessions are left out of the tree and of `open`.
- `tree --client TTY` shows the pane tree on the client at `TTY`. The keys are in [Tree keys](#tree-keys). `N` runs the picker for the row's directory, and the picker's choice switches the same client. The command exits 0 when the tree closes.
- `tmux-conf` prints the tmux plugin for this binary's own path (see [tmux plugin](#tmux-plugin)). It takes no flags and needs no tmux server. It exits 1 when the path holds a byte outside `[A-Za-z0-9/._+-]`.
- `help [--html]` prints the help card. It has the same content as the `Daily use` card in the README, built from the keymap table ([Tree keys](#tree-keys) lists its tree rows). When a tmux server can be read, the tmux heading names the prefix key, as `tmux (prefix is C-b)`; otherwise it reads `tmux`. `--html` writes the same content to stdout as one static HTML page instead.
- `open [--remote] [--intent TEXT] [ARGS...]` attaches a session for a project, or starts one.
  - No argument: attaches the most recently active non-shell session, and the tree opens on that client after it attaches.
  - `.`: the session whose `@orrery_dir` is the current directory. If none exists, the picker opens for the directory, as below. `--remote` refuses `.`.
  - One word: a session with that exact name, else a zoxide fast match, else the word is intent text for the picker. A fast match is one zoxide result, or several when the best one's basename is the word.
  - `new NAME [--root DIR]`: creates the directory `DIR/NAME` and a session for it. `DIR` must be absolute and defaults to `$HOME/Personal`. `NAME` cannot be `.`, `..`, or contain `/`. A taken session name gets `-2`, `-3`, and so on. `.`, `:` and `#` in a name become `_`.
  - Any other arguments: joined with spaces, they are intent text for the picker.
  - A project directory with no live session is not given a new session at once. The picker opens with its resume rows: one `resume` row per archived Claude session for the directory whose transcript still exists, newest first. Without any, it opens with a `continue latest` row. A `start fresh` row always comes last. See [Picker](#picker).
  - `--intent TEXT`: opens the picker with `TEXT` at once. It takes no ARGS; with ARGS it exits 2. Flags come before ARGS.
  - With `TMUX` set, the command switches the invoking client (tmux's current client, by its tty). Otherwise it attaches.
  - `claude` and `zoxide` are found on `PATH`.
  - A pane command with arguments (resume, continue, a first prompt) starts as `<default-shell> -c 'exec "$@"' orrery claude <arguments...>` when the base name of tmux's default-shell is sh, bash, zsh, dash or ksh. The pane gets that shell's environment, and each argument reaches claude unchanged. Under any other shell, or when default-shell cannot be read, the command starts directly. `claude` alone runs as tmux runs any one-word command, through default-shell.
  - Exit codes are those in the table above. A refused `open .` with `--remote` exits 1.
- `serve --listen ADDR --allow-login LOGIN` serves the HTTP API of section 12. See `### orrery serve`.
- `events --url URL [--after N] [--follow]` prints the event stream of `serve` as JSON lines. `--url` is the base URL of that `serve`. The command sends no `Tailscale-User-Login` header, so the URL must lead through a proxy that sets it, such as `tailscale serve`.
  - Without `--after`, the cursor is the newest seq that `serve` reports in its `List` response (see [Connect API](#connect-api)). With `--after N`, the cursor is `N`.
  - A cursor past the newest seq is an error. The command exits 1 before any `Watch` call and writes nothing.
  - Without `--follow`, the command writes events through the newest seq that `serve` reported at start, then exits 0. When that seq is not past the cursor, it writes nothing and makes no `Watch` call. If the stream ends before that seq, it exits 1.
  - With `--follow`, it writes events until the stream ends, then exits 1 with the stream's error.
  - Each event is one line: a compact JSON object of the `WatchResponse` message (see [Connect API](#connect-api)), with the JSON names `seq`, `sessionId`, `at`, and one of `sessionAdded`, `sessionChanged`, or `sessionRemoved`. 64-bit numbers are JSON strings. A field at its zero value is left out, except a `sessionChanged` field the event set, which appears even when it is `""`. Example line:

```json
{"seq":"3","sessionId":"example-session","at":"1700000300","sessionChanged":{"state":"working"}}
```

  - Usage errors (no `--url`, an unknown flag, an extra argument) exit 2.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | error |
| 2 | usage error |
| 3 | question refused (`send --expect-question`: no question, hash mismatch, empty display fragment, or fragment not visible) |

### orrery serve

```
orrery [-L NAME | -S PATH] serve --listen ADDR --allow-login LOGIN [--allow-login LOGIN]... [--push-host HOST]... [--host-status-cmd PATH]
```

- `-L NAME` and `-S PATH` select the tmux server, as section 6 defines.
- `--listen ADDR` sets the address to serve on. ADDR is a loopback IP literal with a port, such as `127.0.0.1:8080` or `[::1]:8080`. Any other ADDR exits 1 before anything is bound.
- `--allow-login LOGIN` admits one Tailscale login. Repeat the flag for each login. At least one is required.
- `--push-host HOST` names a push relay host, as `orreryd` does. Repeat the flag for each host. The default is `ntfy.sh`.
- `--host-status-cmd PATH` sets the command that `GET /v1/host` runs (section 12). `PATH` must be absolute; a relative `PATH` exits 2 before anything is bound. Without the flag, `GET /v1/host` answers 404. The command runs at `PATH` with no arguments and no shell. Its stdout is read up to 64 KiB, and its stderr is discarded. A run is limited to 2 seconds, and a child that outlives it and holds stdout open is cut off 500 ms later. Each run's outcome is cached for 5 seconds (section 12).

Registrations are written to the push store that `orreryd` reads (see `### orreryd`). When neither `XDG_STATE_HOME` nor `HOME` is set, `serve` exits 1.

`serve` also reads the store for the Connect API of section 12. It opens the store file read-only on each call. The rules are in [Connect API](#connect-api).

`--listen` and at least one `--allow-login` are required. Without them `serve` exits 2.

`serve` is meant to sit behind `tailscale serve`, which sets the `Tailscale-User-Login` header that section 12 checks. Do not expose it through Tailscale Funnel.

`SIGINT` and `SIGTERM` stop `serve` and exit 0. Each open `orrery.v1.Events` stream ends first; the [Connect API](#connect-api) section gives the code it ends with.

Example. Every name and address is synthetic:

```
orrery serve --listen 127.0.0.1:8080 --allow-login user@example.test
```

### orreryd

```
orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]... [--push-host HOST]...
```

- `-L`, `-S`, and `--socket` work as they do for `orrery`.
- `--claude PATH` sets the `claude` binary the summarizer runs.
- `--summary-model CONTEXT=MODEL` maps a patchbay context (section 8) to a summary model. Repeat the flag for each context. A context with no mapping uses `claude-haiku-5-5`.
- Each applied hook frame sets `@orrery_context` on the frame's pane to that frame's context (section 2).
- `--push-host HOST` names a push relay host that a Web Push subscription may use. Repeat the flag for each host. The default is `ntfy.sh`. A subscription whose endpoint is on another host is refused (section 12).

`orreryd` listens on the unix socket only. It opens no network listener.

Summaries run when a session enters `working` (throttled) and when it becomes `done`.

ntfy notifications are sent only when `ORRERY_NTFY_URL_FILE` is set:

- On `waiting`, the body is the question.
- On `done`, the body is the latest summary.
- The access token comes from `ORRERY_NTFY_TOKEN_FILE`.
- The host tag is the machine host name.

When a pane enters `done` while an attached client shows the pane's window and had input in the last 30 seconds, `orreryd` sets `@orrery_seen` on the pane to the current Unix time.

Web Push is sent on the same two transitions. On `waiting` and on `done`, `orreryd` posts an encrypted Web Push to every registered subscription. Section 12 defines the payload. The subscriptions are kept in the push store:

- `$XDG_STATE_HOME/orrery/push.json` when `XDG_STATE_HOME` is set.
- Otherwise `$HOME/.local/state/orrery/push.json`.

When neither variable is set, `orreryd` exits 1.

`orreryd` also keeps the store, which mirrors each pane's tmux options (see [Store](#store)). When the store cannot be opened, `orreryd` logs the error and serves hooks and notifications without it. It then mints no `@orrery_id` and syncs no pane. A failure of the push store still exits 1.

`SIGINT` and `SIGTERM` stop `orreryd`.

### Environment

| Variable | Read by | Meaning |
|---|---|---|
| `ORRERY_SOCKET` | `orrery`, `orreryd` | daemon socket path (section 5) |
| `XDG_RUNTIME_DIR` | `orrery`, `orreryd` | default socket directory when no path is set (section 5) |
| `XDG_STATE_HOME` | `orrery` (`serve`), `orreryd` | base directory of the push store, `$XDG_STATE_HOME/orrery/push.json` (section 11, orreryd), and of the store, `$XDG_STATE_HOME/orrery/orrery.db` (see [Store](#store)) |
| `HOME` | `orrery` (`serve`), `orreryd` | used for the push store when `XDG_STATE_HOME` is unset, at `$HOME/.local/state/orrery/push.json`, and for the store, at `$HOME/.local/state/orrery/orrery.db` |
| `ORRERY_NTFY_URL_FILE` | `orreryd` | file holding the ntfy topic URL; enables ntfy when set |
| `ORRERY_NTFY_TOKEN_FILE` | `orreryd` | file holding the ntfy access token |
| `ANTHROPIC_BASE_URL` | `orrery` (`hook`, `debug summarize`) | the session's base URL. The hook sends it in each frame (section 9), and each summary runs through its own session's base URL. |
| `ORRERY_SUMMARIZER` | `orrery` (`hook`) | summarizer sentinel (section 7). `orreryd` sets it on each summary run. Summaries also run with hooks disabled. |
| `HOME` | `orrery` (`tree`, `open`) | `$HOME/Personal` is the default parent directory of new projects. `$HOME` is also the directory that roots are relative to, and the roots file falls back to `$HOME/.config/orrery/roots` (see [Roots](#roots)) |
| `XDG_STATE_HOME` | `orrery` (`tree`, `open`) | base directory of the archive file (see [Archive](#archive)) |
| `XDG_CONFIG_HOME` | `orrery` (`tree`, `open`) | base directory of the roots file, `$XDG_CONFIG_HOME/orrery/roots` (see [Roots](#roots)) |
| `PATH` | `orrery` (`tree`, `open`) | finds `zoxide` and `claude` for the picker and `open` |

### Archive

The archive keeps each closed Claude pane so `orrery open` can resume its conversation. Its file is `${XDG_STATE_HOME:-~/.local/state}/orrery/archive.jsonl`. When `XDG_STATE_HOME` is unset, the file is `$HOME/.local/state/orrery/archive.jsonl`. When neither variable is set, no path resolves.

- The file is JSON lines: one record per line, with no other format.
- The tree writes it. Its `X` and `D` keys archive each Claude pane they close, before the client moves or any pane closes. A Claude pane is one that has `@orrery_claude_session` set; other panes close with no record. If the write for a pane fails, that pane stays open and nothing moves or closes.
- Each write appends one line. The parent directory is created with mode `0700` and the file with mode `0600` when absent. Existing lines are never rewritten, and nothing deletes an entry.
- `orrery open` reads the file for a directory's resume rows (see [Picker](#picker)). A malformed line is an error that names its line number.

Each record has these keys, in this order. The example is synthetic:

```json
{"dir":"/home/user/project","session":"example-session","transcript":"/home/user/.claude/projects/example/example-claude-session.jsonl","label":"example-label","summary":"Example summary of the pane's work.","closed":1700000400,"claude_session":"example-claude-session"}
```

| Key | Type | Meaning |
|---|---|---|
| `dir` | string | the project directory: the pane's directory for `X`, the project's directory for `D` |
| `session` | string | the tmux session name of the pane |
| `transcript` | string | the Claude Code transcript path (`@orrery_transcript`); `""` when unset |
| `label` | string | the pane's label |
| `summary` | string | the pane's summary |
| `closed` | number | unix seconds when the record was written |
| `claude_session` | string | the Claude Code session id (`@orrery_claude_session`), written last |

### Roots

The roots file declares the directory trees whose directories the picker's model sees (see [Picker](#picker)). Its file is `$XDG_CONFIG_HOME/orrery/roots` when `XDG_CONFIG_HOME` is set to a non-empty value. Otherwise it is `$HOME/.config/orrery/roots`. When neither variable is set, no path resolves and no roots are read.

- `orrery open` and the tree's `N` read the file each time they run. A missing file declares no roots. Any other read failure is an error.
- Each line that is not blank is `DEPTH PATH`. `DEPTH` is a positive decimal integer with no sign. One space follows it, and `PATH` is the rest of the line, unchanged. `PATH` is relative to `$HOME`, which must be absolute, and must stay inside it: an absolute `PATH`, or one that climbs out with `..`, is an error.
- Any other line is an error that names its line number, and `open` fails with it. The tree's `N` returns it too.
- The [home-manager module](#home-manager-module) writes this file from `programs.orrery.roots`.

Example. The names are synthetic:

```
2 Work
1 Scratch
```

### Store

`orreryd` keeps a SQLite store that mirrors each pane's tmux options. Outside the store's tests, `orreryd`'s own writes read it, and `orrery serve` reads it for the Connect API (section 12). A sync reads the pane's row to compare it with the new snapshot, and a tidy also reads the server's rows to find the panes that are gone. `serve` only reads the store, and never writes it.

The store file is `$XDG_STATE_HOME/orrery/orrery.db` when `XDG_STATE_HOME` is set, else `$HOME/.local/state/orrery/orrery.db`. When neither variable is set, no path resolves.

- The `orrery` directory is created with mode `0700` when absent. The database file is created with mode `0600` when absent. SQLite creates its `-wal` and `-shm` files beside it with the database file's mode, `0600`.
- The database runs in WAL mode. Each connection waits at most 200 ms for a lock.
- Opening the store applies the migrations embedded in `orreryd`. A database that records a newer migration than the binary embeds fails with `store.ErrTooNew`. It is not migrated, and `orreryd` serves without the store.

The `sessions` table has one row per pane:

| Column | Type | Meaning |
|---|---|---|
| `id` | `TEXT`, primary key | the pane's `@orrery_id` |
| `server` | `TEXT` | `-L <name>`, `-S <path>`, or `""` for the default server |
| `pane` | `TEXT` | the tmux pane id |
| `state` | `TEXT` | `@orrery_state` |
| `changed` | `TEXT` | `@orrery_changed` |
| `seen` | `TEXT` | `@orrery_seen` |
| `question` | `TEXT` | `@orrery_question` |
| `summary` | `TEXT` | `@orrery_summary` |
| `label` | `TEXT` | `@orrery_label` |
| `context` | `TEXT` | `@orrery_context` |
| `tool` | `TEXT` | `@orrery_tool` |
| `claude_session` | `TEXT` | `@orrery_claude_session` |
| `transcript` | `TEXT` | `@orrery_transcript` |

The ten option columns are `NOT NULL` and default to `''`. Each holds the option's value as tmux reports it, and `""` means unset.

The `events` table is a log of row changes. Only pruning deletes from it:

| Column | Type | Meaning |
|---|---|---|
| `seq` | `INTEGER`, primary key, autoincrement | the order of the event |
| `session_id` | `TEXT` | the `id` of the row that changed |
| `kind` | `TEXT` | `session_added`, `session_changed`, or `session_removed` |
| `at` | `INTEGER` | unix seconds of the sync |
| `payload` | `TEXT` | JSON for the kind |

The three kinds and their payloads. The examples are synthetic:

- `session_added`: a pane's row is inserted. Payload `{"server":"-L example-server","pane":"%1"}`.
- `session_changed`: fields of a row differ from the snapshot. The payload holds only the differing fields, under the column names above. Payload `{"fields":{"changed":"1700000300","state":"working"}}`. A new row appends `session_changed` after `session_added` when any of its fields is non-empty.
- `session_removed`: a tidy deletes the row of a pane that is gone. Payload `{}`.

Each sync writes in one transaction. Three things start a sync:

- An applied hook frame. Its pane is synced after the frame's tmux writes and observers, outside the frame mutex. A frame whose state change fails syncs nothing.
- A summary run that writes its summary or label. The pane is synced after the write.
- A tidy.

A sync reads the pane's `@orrery_id` and mirrored options from tmux in one command. When the pane has no `@orrery_id`, the sync mints one and writes the row. It sets `@orrery_id` in tmux only after the store write commits.

One lock covers each sync's tmux read, id mint and store write. A sync waits at most 200 ms for the lock, and a sync that waits longer is logged and dropped. The store write waits at most 200 ms for SQLite's write lock. A write that does not get it fails, rolls back, and is logged.

A dropped sync writes no row or event and sets no id. tmux keeps its options as they were set. The next sync or tidy of the pane writes the difference from the stored row. Events for the values the dropped sync carried are not written. A pane whose id was minted by a dropped sync gets another id on its next sync.

The tidy runs at orreryd start and every 10 s. It tidies:

- the server named by `-L` or `-S`, when either is given;
- otherwise, the default socket, from the first tidy at start, and every server that a hook frame has named since orreryd started.

The default socket is `-S <dir>/tmux-<uid>/default`, where `dir` is `TMUX_TMPDIR` with symlinks resolved when it is set and resolves, else `/tmp` (resolved). This is the path tmux puts in a pane's `$TMUX`, so a frame from that server names the same server and its panes keep one row.

Before each tidy of the default socket, orreryd applies tmux's own check to `<dir>/tmux-<uid>`: it must be a directory, not a symlink, owned by orreryd's uid, with no permission bits for other users. Group bits are allowed. A directory that fails the check, or is missing, is skipped, and no tmux command runs on that socket. The check runs on every tidy, so a directory that changes after orreryd starts is seen. Configured servers (`-L` or `-S`) and servers a frame has named are not checked.

For each server, the tidy runs one `list-panes -a` that reads every pane. It then:

- mints an `@orrery_id` for each pane that has none, and sets it in tmux after the write commits;
- syncs the row of each listed pane from its snapshot;
- deletes the server's rows whose id is not among the listed panes, with one `session_removed` event for each;
- deletes every event older than 14 days, across all servers, except the newest event, which it keeps so the newest seq is never lost.

A failed `list-panes`, or a listing that does not parse, changes nothing. orreryd logs `tmux server unreachable` (warning) when a server's `list-panes` starts failing and `tmux server reachable again` (info) at its first success after that, not on every tidy. Other tidy failures are logged as `mirror tidy dropped` each time. A failed store write rolls back the whole tidy. A default socket skipped by the directory check logs `tmux default socket skipped` (warning, with `server` and `reason`) when the skip starts and again when its reason changes, not on every tidy.

### Claude Code hook settings

Register `orrery hook` for each hook event that reports state. The path below is synthetic:

```json
{
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }],
    "PreToolUse": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }],
    "PostToolUse": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }],
    "PostToolUseFailure": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }],
    "PermissionRequest": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }],
    "Notification": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }],
    "SessionEnd": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }]
  }
}
```

### Tree keys

`orrery tree` (prefix `Space` in the tmux plugin) reads these keys. With no row shown, the pane keys do nothing.

| Key | Action |
|---|---|
| `j`, `down` | move the cursor one row down |
| `k`, `up` | move the cursor one row up |
| `n` | move to the next unseen row after the cursor, wrapping around |
| `/` | start filter input. Typed text sets the filter. Backspace deletes its last character, Enter keeps the filter, and Esc clears it |
| `Enter` | switch the client to the row's pane, mark the pane seen, and close the tree |
| `x` | mark the row's pane seen; the tree stays open |
| `c` | add a window running `claude` to the row's session, started in the row's directory; the tree stays open |
| `s` | open the shell popup for the row's directory, and close the tree |
| `N` | close the tree and run the picker for the row's directory; the picker's choice switches the client |
| `X` | close the row's pane after a y/N prompt, asking twice for a working Claude pane |
| `D` | close every session of the row's project and its shell session after a prompt listing them |
| `?` | show every key |
| `Esc`, `q` | close the tree |

The tree lists the active projects first. A project is quiet when none of its panes is waiting or unseen (its state is set and its `changed` is later than its `seen`) and its newest pane `activity` is more than 7 days old. A project whose panes all have no activity stays active. Quiet projects sit under one row, `Quiet (N)` with N the count, which stays collapsed. Enter on that row opens the section and moves the cursor to its first pane. Quieting a project closes and drops nothing.

`X` and `D` archive Claude panes before they close (see [Archive](#archive)).

### Picker

The picker is shown by `orrery open` with text, and by the tree's `N` in directory mode.

- With no text, a one-line `text:` prompt asks for it. Enter submits non-empty text, Backspace deletes, and Esc cancels.
- The model sees at most 300 directories, numbered `d1`, `d2`, and so on, in this order. First come zoxide's top 30. Then each root of the [roots file](#roots), in file order, adds its directories at levels 1 to `DEPTH` below its path, sorted by path, skipping any directory already listed. A directory is written relative to `$HOME` when it lies strictly inside `$HOME`, and by its absolute path otherwise, including `$HOME` itself. Hidden names (beginning with `.`) are left out with everything beneath them, as are symlinks and any directory that cannot be read, with everything beneath it.
- The text is ranked into actions, best first, and the first row is selected. Each row reads `jump PANE`, `pane DIR`, `resume SESSION`, `continue latest in DIR`, or `start fresh in DIR`, with ` (prompt)` added when the action carries a first prompt. A resume row adds `: NOTE` after its session when the archived record has a summary.
- The resume rows are the actions that `open` builds for the directory (see its bullet above). A `resume` action runs `claude --resume SESSION`, a `continue` action runs `claude --continue`, and a `start fresh` action runs `claude`. Each opens a Claude pane in the directory, as a `pane` action does.
- The text picker's last row is `pane ROOT`, where ROOT is `$HOME/Personal`. It carries the text as its first prompt when the model calls the text a task, as every model `pane` row does. It is left out when another row already opens a pane in ROOT. When the model's reply has no valid line, this row is the only row. When `HOME` is not absolute, there is no root row, and a reply with no valid line is an error. A failed model, zoxide or tmux call is an error. The rows that `open` builds for a directory get no root row.
- In directory mode, the first row is `pane DIR`, and any other row for the same directory is dropped. The prompt is the text when any action has one.
- Keys typed before the prompt or the list is drawn are discarded.
- A `pane` action opens a Claude pane in the directory: a new window in the directory's session, or a new session when there is none.

| Key | Action |
|---|---|
| `j`, `down` | move the cursor down, stopping at the last row |
| `k`, `up` | move the cursor up, stopping at the first row |
| `Enter` | run the selected action and switch the client to its target. Keys are ignored while it runs |
| `Esc`, `q` | close the picker without running anything |

### tmux plugin

`orrery tmux-conf` prints the plugin for the binary's own path. The home-manager module sources it (see below). The plugin:

- binds prefix `Space` to `orrery tree` in a popup on the invoking client. This replaces tmux's default `Space` binding.
- binds prefix `s` to `orrery shell` for the current pane's directory, on the invoking client.
- appends to `status-right` the counts of waiting panes and new panes, as ` orrery N waiting M new `. Shell sessions are left out.
- sets the top pane border, showing each pane's state, label, and summary.
- runs `orrery seen --pane` for the focused pane on `window-pane-changed`, `session-window-changed`, and `client-session-changed`, only while a client is attached to the session.

### Home-manager module

The flake exports `homeManagerModules.default`. Setting `programs.orrery.enable = true` installs the package and `p`, and sources the tmux plugin through `programs.tmux.extraConfig`.

| Option | Type | Default | Meaning |
|---|---|---|---|
| `programs.orrery.enable` | bool | `false` | install orrery, `p`, and the tmux plugin |
| `programs.orrery.package` | package | the flake's `packages.<system>.default` | the orrery package to install and run |
| `programs.orrery.remoteHost` | nullOr string | `null` | SSH host that `p` opens sessions on |
| `programs.orrery.roots` | listOf submodule | `[ ]` | directory trees the picker lists, each with `path` (relative to home) and `depth` (a positive integer); written to the roots file (see [Roots](#roots)). An empty list writes no file |

`p` runs as follows:

- With `remoteHost = null`, `p ARGS...` runs `orrery open ARGS...` locally.
- Otherwise, `p .` alone prints `p: . is local only; run it on the host itself` and exits 2. Any other `p ARGS...` runs `ssh -t HOST "orrery open --remote ARGS..."`, with each argument quoted for the remote shell.

## 12. HTTP API

`orrery serve` (section 11) serves this API over HTTP at the address given by `--listen`. Bodies are JSON unless the table says otherwise. Every example is synthetic.

### Guard

The guard runs before routing. It checks every request:

- The `Tailscale-User-Login` header must equal one of the `--allow-login` values. Otherwise the response is 403.

Connect paths are those beginning `/orrery.v1.Sessions/` or `/orrery.v1.Events/` (see [Connect API](#connect-api)). Every request to a Connect path, whatever its method, must also send a non-empty `X-Orrery-Client` header. Otherwise the response is 403. A POST to a Connect path must send `Content-Type` `application/proto`, `application/json`, `application/connect+proto`, or `application/connect+json`. Otherwise the response is 403.

Every POST and DELETE on any other path must also send:

- `Content-Type: application/json`, and
- a non-empty `X-Orrery-Client` header.

Otherwise the response is 403. The server sends no CORS headers. An unknown path gets 404.

### Pane IDs in paths

A pane ID is `%` followed by digits, such as `%3`. The `{id}` segment of a path takes the pane ID with its `%` written as `%25`. Pane `%3` is therefore `/v1/panes/%253/screen`.

### Routes

| Method and path | Body | Success | Other status codes |
|---|---|---|---|
| `GET /v1/panes` | none | 200, JSON array of panes | 500 tmux failure |
| `POST /v1/panes` | `{"pane": "%3", "kind": "claude"}` or `{"pane": "%3", "kind": "shell"}` | 201, `{"pane": "%7"}` | 400 bad body, 404 absent pane, 500 tmux failure |
| `GET /v1/panes/{id}/screen` | none; optional query `ansi`, `history` | 200, `text/plain; charset=utf-8` | 400 malformed pane ID or bad query, 404 absent pane, 500 tmux failure |
| `GET /v1/panes/{id}/conversation` | none; optional query `limit` | 200, JSON conversation | 400 bad `limit` or malformed pane ID, 404 absent pane, 404 no live session, 404 no transcript, 404 transcript not found, 500 tmux failure or transcript read failure |
| `GET /v1/host` | none | 200, JSON host status | 404 no `--host-status-cmd`, 500 host name failure, 502 status command failed or invalid |
| `POST /v1/panes/{id}/text` | `{"text": "example input"}` | 204 | 400 malformed pane ID or bad body, 404 absent pane, 500 tmux failure |
| `POST /v1/panes/{id}/keys` | `{"keys": ["Enter"], "expect_question": "H"}`; `expect_question` is optional | 204 | 400 malformed pane ID or bad body, 404 absent pane, 409 question refused, 500 tmux failure |
| `POST /v1/panes/{id}/seen` | none, or `{"read_up_to": 1700000100}`; `read_up_to` is optional | 204 | 400 malformed pane ID or bad body, 404 absent pane, 500 tmux failure |
| `POST /v1/push/register` | `{"endpoint": "https://push.example.test/sub/example", "p256dh": "example-key", "auth": "example-auth"}` | 204 | 400 bad body or keys, 422 endpoint not https on a `--push-host` host, 500 store failure |
| `DELETE /v1/push/register` | `{"endpoint": "https://push.example.test/sub/example"}` | 204 | 400 bad body, 404 not registered, 500 store failure |

Details for each route:

- `GET /v1/panes` returns the pane list of section 3. Each pane also has `question_hash`, the question hash of section 10 for the pane's `question`.
- `POST /v1/panes` opens a window in the session and directory of the pane named by `pane`. The window runs `claude` for kind `claude`, and the default shell for kind `shell`. The 201 response names the new pane.
- `GET /v1/panes/{id}/screen` returns the pane's screen as plain text, the output of `capture-pane -p -J`. It is not JSON. The query is checked before the pane ID, so a bad query answers 400 even for an absent pane.
  - `ansi` absent or `0` gives plain text. `1` adds `-e`, which keeps the color escape sequences. Any other value, or a repeated key, answers 400.
  - `history` absent gives no scrollback. An integer from 0 to 2000 adds `-S -N`, which includes N lines above the visible screen. Any other value, including a sign, an empty value, a number over 2000, or a repeated key, answers 400.
- `POST /v1/panes/{id}/text` sends the body's `text` the way `orrery send --text` does (section 11). The server holds a lock for each pane, so two text sends to one pane run one after the other. The `orrery` CLI is a separate process and is not covered by that lock.
- `POST /v1/panes/{id}/keys` sends the keys to the pane. When `expect_question` is present, the send is refused with 409 unless the question check passes. That check is the one `orrery send --expect-question` applies (section 11). `expect_question` takes the question hash H from section 10.
- `POST /v1/panes/{id}/seen` sets the pane's `@orrery_seen` (section 2). Without `read_up_to`, the time is now. An empty body, `{}`, and an object without that field all count as without it. With `read_up_to`, a non-negative integer in unix seconds, the time is the later of the stored time and `read_up_to`, so the seen time never moves back. An unset stored time counts as 0. The server holds a lock for each pane across the read and the write. The `orrery` CLI is a separate process and is not covered by that lock. A body that is not a JSON object, or a `read_up_to` that is not a non-negative integer, answers 400 "bad request body". The pane checks run first.
- `POST /v1/push/register` registers a Web Push subscription. `p256dh` and `auth` are the subscription's keys. The endpoint must be `https` and must be on a host named by `--push-host` (section 11).
- `DELETE /v1/push/register` removes the subscription with that endpoint.
- `GET /v1/panes/{id}/conversation` returns the conversation of the pane's Claude transcript as `{"origin": string, "items": [item...], "goal": string, "waiting": [waiting...]}`. The transcript is the path in the pane's `@orrery_transcript` option (section 2); no request value names a file. The pane must have `@orrery_state` set, or the answer is 404 "no live session". `@orrery_transcript` must be set and non-empty, or the answer is 404 "no transcript". A transcript file that does not exist answers 404; any other read failure answers 500.
  - The query `limit` is absent for 30, or one decimal integer from 1 to 100. Any other value, including 0, a value over 100, a sign, an empty value, a non-number, or a repeated key, answers 400. The `limit` check runs before the pane ID check.
  - The reader takes the newest `limit` items from the last 4 MiB of the transcript, oldest first. Only whole lines count, so a partial line at either end of that window is skipped. The reader can return fewer than `limit` items. `items` is `[]` when there are none.
  - `origin` is the text of the first prompt that the item rules below give within the transcript's first 1 MiB, cut to 300 runes, or `""` when there is none. Each line is classified without seeing later lines. A line that the window ends inside is ignored.
  - `goal` is the `condition` of the transcript's newest goal record, or `""`. A goal record is an `attachment` line whose `attachment.type` is `goal_status`. The newest goal record in the whole transcript decides, not only the items in the 4 MiB window. When that record has `met` false, `goal` is its `condition`, cut to 300 runes. When it has `met` true, which Claude Code writes both when a goal is met and when it is cleared, or when the transcript has no goal record, `goal` is `""`. Only whole lines count, and the `isMeta` and `isSidechain` marks do not apply to goal records. Serve remembers, for each transcript path, how far it has read and the last goal state. The first read scans the whole file. Later reads read only the bytes appended since the last read. A file that is shorter than the part already read, or that was replaced, is read again from the start.
  - Lines of a type other than `assistant`, `user`, or `attachment`, lines marked `isMeta` or `isSidechain`, and lines that are not JSON give no items.
  - An `assistant` line gives one item per content block, in order. A `text` block gives a `reply`. A `tool_use` block gives a `question` when its `name` is `AskUserQuestion`, and a `tool` otherwise. Other blocks, such as thinking blocks, give none.
  - A `user` line gives items by the first of these rules that applies:
    1. It has one or more `tool_result` blocks. It gives one `result` per such block, and nothing else.
    2. It is a notice, as described below. It gives one `notice`.
    3. Its whole string content is a teammate message, as described below. It gives one `notice` of kind `teammate`, or no item when every block is an idle ping.
    4. Its whole string content is command output or a command record, as described below. Command output gives no item. A command record gives one `prompt`.
    5. Otherwise its text gives one `prompt`, when the text is not empty. The text of a line with string content is that string. The text of a line with array content is its `text` blocks joined with newlines.
  - A typed line is a `user` line whose `promptSource` is `typed`, `queued`, or `suggestion_accepted` and whose `origin.kind` is `human`. Rules 2 to 4 never apply to a typed line, so it gives its `prompt` under rule 5, unless rule 1 applies first.
  - A notice is a `user` line of one of these kinds, checked in this order:
    - `compaction` when `isCompactSummary` is true. Its `text` is `""`.
    - `task` when `origin.kind` is `task-notification` and `promptSource` is `system`. Its `text` is the content of the line's first `summary` element, cut to 300 runes, or `""` when that element is absent or not closed. Its `error` is true when the first `status` element is `failed` or `killed`.
    - `interrupted` when its content is one `text` block whose text is exactly `[Request interrupted by user]` or `[Request interrupted by user for tool use]`. Its `text` is `""`.
  - A teammate message is a `user` line whose whole string content is the fixed lead-in `Another Claude session sent a message:` and a newline, then one or more `<teammate-message teammate_id="…">` blocks, each with an optional `color` and an optional `summary` attribute, separated by a blank line, then the fixed trailer Claude Code writes after the last block, which starts with a blank line and the sentence `This came from another Claude session`. A block body starts and ends with a newline. The notice's `text` is the `summary` attribute values of its blocks, in order, joined with a newline, with the XML entities `&lt;`, `&gt;`, `&quot;`, `&apos;`, and `&amp;` decoded. A block without a summary, or with an empty one, adds nothing. The text is cut to 300 runes. A block whose body is a JSON object with type `idle_notification` is an idle ping. A message whose blocks are all idle pings gives no item. Any other message gives one notice, and the summaries of its idle pings still count.
  - Command output is a `user` line whose whole string content is one `<local-command-stdout>` element, with no second closing tag inside it. It gives no item.
  - A command record is a `user` line whose whole string content holds a `<command-message>` element and a `<command-name>` element, in either order, separated only by whitespace, followed by an optional `<command-args>` element. The name must start with `/`. Its `prompt` reads the name, then one space and the args when the args are not empty, so `/name args`, or `/name` alone.
  - Before a `prompt`'s text is kept, each matched `<pasted_content id="X">` … `</pasted_content id="X">` pair in it is removed, keeping the text between its tags. An unpaired tag stays. This applies to every prompt, typed or not. The text is then cut to 4096 bytes. A prompt whose text is empty gives no item.
  - An `attachment` line whose `attachment` has `type` `queued_command`, `commandMode` `prompt`, and `origin.kind` `human` gives one `prompt` with the attachment's `prompt` text, treated as above, in the line's place in the order. It always gives its prompt, even when a later `user` line in the transcript gives a prompt with the same text. Any other attachment gives no item.
  - Each item has these keys, always present and in this order: `kind`, `time`, `text`, `tool`, `input`, `id`, `error`, `notice`.
    - `kind` is `prompt`, `reply`, `tool`, `question`, `result`, or `notice`.
    - `time` is the unix seconds of the line's RFC 3339 `timestamp`, or 0 when it is absent or does not parse.
    - `text` is cut at a UTF-8 boundary. A prompt, question, or result holds at most 4096 bytes, a reply at most 32768 bytes, and a notice at most 300 runes. A question's text is its questions joined by newlines. A tool item's `text` is `""`.
    - `tool` is the tool name of a `tool` or `question` item, and `""` otherwise.
    - `input` is the display text of the tool's input (section 2) for a `tool` item. It is `""` for every other kind, including `question`.
    - `id` is the `tool_use_id` of a `tool`, `question`, or `result` item, and `""` otherwise.
    - `error` is the `is_error` flag of a `result` item, true for a `task` notice whose status is `failed` or `killed`, and `false` otherwise.
    - `notice` is the kind of a `notice` item: `compaction`, `task`, `teammate`, or `interrupted`. It is `""` on every other kind.
  - `waiting` is the list of messages that Claude Code's queue holds for the transcript, oldest first. It is `[]` when there are none, never `null`. Each row has `text` (the message text, cut to 4096 bytes), `time` (the unix seconds of the RFC 3339 `timestamp` of its opening record, or 0 when that is absent or does not parse), and `canceled` (a boolean).
  - A message opens at an `enqueue` queue-operation record in the transcript whose `content` is its text. An enqueue whose text starts with `<task-notification>`, `<agent-message`, or `<cross-session-message` opens nothing. A message closes at the first of these, matched to the oldest open row with the same full text: a `remove` or `popAll` record with that `content` (a `remove` with reason `cleared_on_cancel` cancels the row instead), or a user line that the person sent from the queue (`promptSource` `queued` and `origin.kind` `human`) with that text. A closed row is gone from the answer.
  - A canceled row stays in the answer, with `canceled` true, until the next `enqueue` record that opens a row or the next user line that the person typed, queued, or accepted from a suggestion. That line drops every canceled row.
  - When the pane's `@orrery_state` is `done`, the answer is idle: every row that is not canceled is dropped for good, so only canceled rows are listed, and a later read of a working pane does not bring them back.
  - The rows are read forward from the offset remembered for the transcript path, as `goal` is. The first read scans the whole file, and later reads read only the bytes appended since. A file that is shorter than the part already read, or that was replaced, is read again from the start. Only whole lines count.

Example response. Every value is synthetic:

```json
{"origin":"Example opening prompt.","items":[{"kind":"prompt","time":1700000000,"text":"Example opening prompt.","tool":"","input":"","id":"","error":false,"notice":""},{"kind":"notice","time":1700000050,"text":"","tool":"","input":"","id":"","error":false,"notice":"compaction"},{"kind":"reply","time":1700000100,"text":"Example reply.","tool":"","input":"","id":"","error":false,"notice":""},{"kind":"tool","time":1700000200,"text":"","tool":"Bash","input":"example command","id":"example-tool-use","error":false,"notice":""},{"kind":"result","time":1700000210,"text":"Example output.","tool":"","input":"","id":"example-tool-use","error":false,"notice":""},{"kind":"notice","time":1700000300,"text":"Example task finished.","tool":"","input":"","id":"","error":false,"notice":"task"}],"goal":"Example goal.","waiting":[{"text":"Example queued message.","time":1700000250,"canceled":false}]}
```

- `GET /v1/host` runs the `--host-status-cmd` command (section 11) and returns `{"host": string, "stats": [stat...]}`. `host` is the machine's host name, and `stats` holds the command's validated stats in order. Without the flag, the route answers 404.
  - Each stat has the keys `label`, `value`, `percent` (omitted when the command gave none), and `level`.
  - The command's output must be a JSON array of at most 12 objects. Each object has a non-empty string `label`, a string `value`, a `level` of `ok`, `warn`, or `crit`, and an optional `percent` that is a number from 0 to 100 (`null` counts as absent). An empty array is valid. Any other output is invalid: a top-level value that is not an array, data after the array, a stat with an unknown key, or a stat with a missing or mistyped field.
  - `label` is cut to 32 runes, and `value` to 64 runes.
  - A run that exits with an error, runs past 2 seconds, or prints more than 64 KiB, and a run whose output is invalid, answers 502 with no stats.
  - The outcome of each run, success or failure, is kept for 5 seconds and served to every request in that window. A run whose own request context ends before the command finishes is not kept.

Example response. Every value is synthetic:

```json
{"host":"example-host","stats":[{"label":"example-cpu","value":"12%","percent":12,"level":"ok"}]}
```

### Push payload

When a pane enters `waiting` or `done`, `orreryd` posts to each registered subscription:

- The body is one RFC 8291 `aes128gcm` record. The record is at most 4096 bytes.
- The headers are `Content-Encoding: aes128gcm` and `TTL: 3600`.
- The plaintext is a JSON object. Every value is a string. The keys are `pane`, `host`, `project`, `state`, `summary`, `label`, `question`, and `question_hash`.
- The plaintext is at most 3993 bytes. When it is longer, the question is cut first, then the summary.
- `question_hash` always covers the full question, even when the question is cut.

Example plaintext. The question and its hash are the fixed vector of section 10:

```json
{"pane":"%1","host":"example-host","project":"example-project","state":"waiting","summary":"Example summary of the pane's work.","label":"example-label","question":"Example question for the user?","question_hash":"7f593eeb8a031e6a99b02a9b65b8e03a17a82a20ca4862ecda37bd06c479a5eb"}
```

### Connect API

`orrery serve` also serves a typed API over the Connect protocol. The routes above are unchanged by it. The schema is `proto/orrery/v1/orrery.proto`, package `orrery.v1`. Generated Go lives in `internal/gen`. `just generate` writes it, and `just check` fails when it drifts from the schema.

The services are `orrery.v1.Sessions`, with one call, `List`, and `orrery.v1.Events`, with one call, `Watch`.

| Call | Path | Request | Response |
|---|---|---|---|
| `List` | `POST /orrery.v1.Sessions/List` | `ListRequest`, empty | `ListResponse` |
| `Watch` (server-streaming) | `POST /orrery.v1.Events/Watch` | `WatchRequest` | stream of `WatchResponse` |

`ListResponse` has these fields:

- `sessions` holds one `Session` per row of the `sessions` table (see [Store](#store)), in `id` order. Each `Session` carries the row's thirteen columns under the same names: `id`, `server`, `pane`, `state`, `changed`, `seen`, `question`, `summary`, `label`, `context`, `tool`, `claude_session`, and `transcript`.
- `seq` is the largest `seq` in the `events` table when the rows were read, in the same read. It is 0 when there are no events.

Two sessions can share `server` and `pane` for up to 10 s after a tmux server restart. `id` is unique.

`serve` reads the store for `List` at the path that the [Store](#store) section names:

- It opens the file read-only on each call and holds no connection between calls. A file that was replaced, or one that has appeared since the last call, is read as it is now.
- It never writes the database and never creates the file or its directory. SQLite may create the `-wal` and `-shm` files beside an existing store file.
- When the file is missing, the database is busy, or the file is removed while it is read, `List` answers with the Connect code `unavailable`. Any other read failure answers `internal`.
- The `/v1/...` routes never read the store.

| Connect code | When |
|---|---|
| `unavailable` | the store file is missing, the database is busy, or the file is removed while it is read |
| `internal` | any other read failure |

`orrery.v1.Events.Watch` streams the events table (see [Store](#store)). `orrery events` (section 11) reads this call.

`WatchRequest` has `after`, the seq the stream resumes after. It is a cursor, and the cursor rules below apply to it.

Each `WatchResponse` has `seq`, `session_id` (the pane's `@orrery_id`), `at` (unix seconds when the event was stored), and one member of its `event` oneof. The store kinds map to the members as follows:

| Store kind | `WatchResponse` member | Payload message |
|---|---|---|
| `session_added` | `session_added` | `SessionAdded`: `server`, `pane` |
| `session_changed` | `session_changed` | `SessionChanged` (below) |
| `session_removed` | `session_removed` | `SessionRemoved`, no fields |

`SessionChanged` has ten optional fields: `state`, `changed`, `seen`, `question`, `summary`, `label`, `context`, `tool`, `claude_session`, and `transcript`. An event sets a field only when it changed that option. A set field with the value `""` means the option was unset. A field the event did not change is absent.

Watch behavior:

- Each store read returns up to 100 events with seq above the cursor, in seq order. After a read of 100, the next read follows at once. After a shorter read, the stream waits for the next 250 ms tick, then reads again.
- Each read opens the store read-only and closes it before the send. Nothing is held between reads or across a send.
- The cursor is out of range when `after` is above the newest seq. When the log holds events, it is also out of range when `after` is below the oldest kept seq minus one. Events older than 14 days are pruned, except the newest (see [Store](#store)), so cursor 0 is out of range once the oldest kept seq is 2 or more.
- Each send must complete within 10 seconds. A send that does not ends that stream only.
- After serve's shutdown signal, a blocked send gets 1 second, and so does the stream's end. A stream that is waiting for its next read or send ends with `unavailable`, with the message `serve is shutting down`.

| Connect code | When |
|---|---|
| `out_of_range` | the cursor rule above |
| `unavailable` | the store file is missing, the database is busy, or the file is removed while it is read; or serve shuts down while the stream waits for its next read or send |
| `internal` | any other store read failure, a row that does not decode, or an event kind the server does not know |

A stream whose send fails, at the 10-second deadline or during a blocked send at shutdown, ends with an error outside this table.

The guard's rule for Connect paths is in [Guard](#guard). A request the guard refuses gets 403 before any Connect response.

## 13. Freeze

The names and values in this document are frozen as of effort 1. Later efforts, including the effort-4 golden test, build on them.

orrery v0.2.1 (E2b) added the `@orrery_claude_session` and `@orrery_transcript` options and the `claude_session` and `activity` fields additively.

orrery v0.6.0 (E7a) added the `@orrery_tool` option, the `tool` list field, the `ansi` and `history` screen parameters, the `--host-status-cmd` serve flag, and the `GET /v1/panes/{id}/conversation` and `GET /v1/host` routes, all additively. No existing default response changes.

orrery v0.7.0 (H1a) added the store file (see [Store](#store)) and the `@orrery_id` option, both additively. No existing name or value changes.

The typed API (the `orrery.v1.Sessions` service, see [Connect API](#connect-api)) was added additively. No existing name or value changes.

The event stream (the `orrery.v1.Events` service with `Watch`, see [Connect API](#connect-api)), the `orrery events` command, and serve's shutdown of open streams were added additively. The `List` call also answers `unavailable` for a store removed while it is read, and pruning keeps the newest event. No existing name or value changes.

orrery E12 (conversation items) added the `notice` kind and the `notice` item key to the conversation route, additively. No existing name changes. The same change alters the route's prompt rules, its reply cap, and its origin rule, which are changes and not additive: a user line that was a prompt may now be a notice, a teammate message, command output, or no item; a slash command reads `/name args`; pasted content tags are removed from prompts; queued messages give prompts; a reply keeps up to 32768 bytes where it kept 4096; and `origin` follows the new prompt rules.

orrery E13 (conversation goal) added the `goal` key to the conversation route's answer, additively. No existing name or value changes.

orrery E16a (queued messages) added the `waiting` key to the conversation route's answer, additively. No existing name or value changes.

orrery E16b (read-up-to seen time) added the optional `read_up_to` body field to the seen route, additively. With it the stored seen time becomes the later of the stored time and `read_up_to`, so it never moves back. No existing name or value changes.
