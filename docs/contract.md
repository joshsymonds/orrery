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

A headless engine session also has the states `stopped`, `failed` and `interrupted`. They are not pane states, and `ParseState` refuses them. See [Headless sessions](#headless-sessions).

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

The desk stream listens on `notices.sock` in the same directory as the daemon socket. See [Desk stream](#desk-stream).

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
| Seen | int64 | `seen` | the seen time of a seen report, omitted at zero |
| Read | int64 | `read` | the read time of a read report, omitted at zero |

Wire format:

- One frame per connection.
- One JSON object, terminated by `\n`. The object is written on a single line.
- A frame with `seen` above zero and no `hook` is a seen report. orreryd sends no state change for it.
- A frame with `read` above zero and neither `hook` nor `seen` is a read report: serve's word that the phone read the pane at that time. orreryd sends no state change for it. It keeps the time as the pane's newest read (see [Notice routing](#notice-routing)), and it drops a report whose `tmuxPane` is empty.

Functions:

- `func WriteFrame(w io.Writer, frame HookFrame) error` writes exactly that format.
- `func ReadFrame(r io.Reader) (HookFrame, error)` decodes one frame. It has no line-length limit, because hook payloads can be large. It returns an error on malformed JSON, on a frame with both `seen` above zero and `hook`, on a frame whose `read` is below zero, and on a frame with `read` above zero together with `hook` or `seen`. Otherwise it returns an error when `hook` is missing, is `null`, or is not a JSON object, unless the frame is a seen report or a read report.

Example frame. Every value is synthetic. The wire form is this object on one line, terminated by `\n`:

```json
{"hook":{"hook_event_name":"Stop","session_id":"example-session"},"tmuxPane":"%1","tmux":"/tmp/example-tmux,1234,0","cwd":"/home/user/project","anthropicBaseUrl":"https://patchbay.example.com/ctx/example-context"}
```

Example read report. The value is synthetic. The wire form is this object on one line, terminated by `\n`:

```json
{"tmuxPane":"%1","tmux":"","cwd":"","anthropicBaseUrl":"","read":1700000000}
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
- `notices` relays the desk stream of `orreryd`: it prints each line on stdout and sends each stdin line to the stream. See [orrery notices](#orrery-notices).
- `desk --sound FILE [--host HOST]...` shows the notices of this machine and of each `HOST` as desktop popups. See [orrery desk](#orrery-desk).
- `list --json` prints the pane list defined in section 3.
- `seen --pane ID` marks the pane as seen.
- `seen --pane ID --focus` marks the pane as seen only when a client showing the pane's window had input within the last 30 seconds. Otherwise nothing is marked.
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
  - Each event is one line: a compact JSON object of the `WatchResponse` message (see [Connect API](#connect-api)), with the JSON names `seq`, `sessionId`, `at`, and one member of its `event` oneof: `sessionAdded`, `sessionChanged`, `sessionRemoved`, `engineAdded`, `engineChanged`, `enginePrompt`, `engineBlock`, `engineRequest`, `engineRequestCanceled`, `engineTurnEnded`, or `engineText`. 64-bit numbers are JSON strings. A field at its zero value is left out, except a `sessionChanged` field the event set, which appears even when it is `""`. Example line:

```json
{"seq":"3","sessionId":"example-session","at":"1700000300","sessionChanged":{"state":"working"}}
```

  - Engine session events (the `engine_*` kinds of [Store](#store)) are printed as the engine members of the stream. The stream also carries `engineText` messages, which are not stored events (see [Connect API](#connect-api)).
  - Usage errors (no `--url`, an unknown flag, an extra argument) exit 2.
- `session [--url URL] VERB ARGS` drives the headless sessions that `orreryd --sessions` runs (see [Headless sessions](#headless-sessions)). Without `--url`, the verbs reach orreryd's session socket. With `--url URL`, they reach `serve` at `URL`, as the bullets below describe. Its verbs are:
  - `session start [--model MODEL] DIR PROMPT` makes `DIR` absolute on the client, starts a session there with `PROMPT` as its first message, and prints the new session id on its own line. The session id is the only output. `--model MODEL` is the model alias the session's process runs, as `claude --model`. Without it, claude's default model runs. The engine refuses a model outside its rule (see [Session control socket](#session-control-socket)). A process that a later `say` resumes runs claude's default model.
  - `session say ID TEXT`, `session answer ID REQUEST_ID ANSWER`, `session interrupt ID`, `session stop ID` and `session seen ID` each make one call of the `orrery.engine.v1.Engine` service (see [Session control socket](#session-control-socket)). They print nothing on success.
  - Without `--url`, `session watch ID` reads the session's events from the store, not from the socket. It prints each event as one JSON line with the keys `seq`, `kind`, `at` and `payload`, the payload being the stored JSON object. It polls the store every 250 ms. It returns with exit 0 once the session's state is `stopped` or `failed` and every stored event has been printed. In any other state it keeps polling.
  - Without `--url`, the verbs other than `watch` reach `orreryd` on the session socket, `sessions.sock` beside the daemon socket. The daemon socket path comes from `--socket`, then `ORRERY_SOCKET`, then `$XDG_RUNTIME_DIR/orrery/orreryd.sock`. When the socket cannot be dialed, the error reads `orreryd takes no session commands on SOCKET; start it with --sessions`, where `SOCKET` is the `sessions.sock` path.
  - No `session` verb reads a tmux server, so `-L` and `-S` have no effect on them.
  - A usage error exits 2. A failed call exits 1 and prints the Connect error message, as `session VERB: MESSAGE`.
  - `session --url URL VERB ARGS` sends `start`, `say`, `answer`, `interrupt`, `stop` and `seen` to the `orrery.v1.Control` service of `serve` at `URL` (see [Control API](#control-api)), with the same arguments and output as the socket form, except that `DIR` is not made absolute (see below). It does not dial the session socket, so the client needs no daemon socket and no `--sessions`. Each request carries the `X-Orrery-Client` header that the guard requires. The command sends no `Tailscale-User-Login` header, so the URL must lead through a proxy that sets it, such as `tailscale serve`. `--url` with no URL exits 2.
  - With `--url`, `DIR` is sent as given, not made absolute. `serve` admits only an absolute directory under its home directory, so a relative `DIR` is refused.
  - `session --url URL watch ID` lists `serve`'s newest seq with `Sessions.List`, then reads the `orrery.v1.Events` stream from seq 0. It writes each message whose `session_id` is `ID` as one line, in the format of `orrery events`, including the `engineText` messages of that session. If the stream answers `out_of_range` before any message, it reads again from the newest seq. The watch exits 0 once a message whose seq is at or past that newest seq leaves the session `stopped` or `failed`. A stream that ends before then exits 1 with `session watch: stream ended before session ID stopped`.

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
- `--host-status-cmd PATH` sets the command that `Host.Status` runs (section 12). `PATH` must be absolute; a relative `PATH` exits 2 before anything is bound. Without the flag, `Host.Status` answers `not_found`. The command runs at `PATH` with no arguments and no shell. Its stdout is read up to 64 KiB, and its stderr is discarded. A run is limited to 2 seconds, and a child that outlives it and holds stdout open is cut off 500 ms later. Each run's outcome is cached for 5 seconds (section 12).

Registrations are written to the push store that `orreryd` reads (see `### orreryd`). When neither `XDG_STATE_HOME` nor `HOME` is set, `serve` exits 1.

`serve` also reads the store for the Connect API of section 12. It opens the store file read-only on each call. The rules are in [Connect API](#connect-api).

`serve` also answers the web page of section 12 at `/`, from the page embedded in the binary.

`serve` also serves the `orrery.v1.Control` API of [Control API](#control-api), forwarding each call to orreryd's session socket. The daemon socket path comes from `--socket`, then `ORRERY_SOCKET`, then `$XDG_RUNTIME_DIR/orrery/orreryd.sock`, and the session socket is `sessions.sock` beside it. When no daemon socket path resolves, `serve` has no engine and answers `unavailable` to each Control call that reaches the forward; a `Start` the home rule refuses still answers `invalid_argument`. `serve` also reads `HOME`, the directory that `Control.Start` admits directories under.

The picker call `Picker.Ask` (see [Phone API](#phone-api)) looks up `zoxide` and `claude` on serve's PATH at each ask, not at start. A missing one answers that ask `unavailable` and does not stop serve.

`serve` also sends `orreryd` a read report (section 9) with the pane and the time of each read it answers. It sends one after each successful `Panes.Conversation` or `Panes.Screen` answer. While a `Panes.WatchConversation` stream stays open, it sends one at the stream's first successful step, and again each time the keepalive interval has passed since the last report. That interval is 5 seconds by default. The report goes to the daemon socket (section 5). When no `orreryd` listens there, the report is dropped.

`--listen` and at least one `--allow-login` are required. Without them `serve` exits 2.

`serve` is meant to sit behind `tailscale serve`, which sets the `Tailscale-User-Login` header that section 12 checks. Do not expose it through Tailscale Funnel.

`SIGINT` and `SIGTERM` stop `serve` and exit 0. Each open `orrery.v1.Events` stream ends first; the [Connect API](#connect-api) section gives the code it ends with.

Example. Every name and address is synthetic:

```
orrery serve --listen 127.0.0.1:8080 --allow-login user@example.test
```

### orreryd

```
orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]... [--push-host HOST]... [--sessions] [--session-cap N] [--session-shell PATH] [--step-cap N] [--gate-lock PATH]...
```

- `-L`, `-S`, and `--socket` work as they do for `orrery`.
- `--claude PATH` sets the `claude` binary the summarizer runs, and the binary each headless session runs.
- `--summary-model CONTEXT=MODEL` maps a patchbay context (section 8) to a summary model. Repeat the flag for each context. A context with no mapping uses `claude-haiku-5-5`. The done judge (see [Notice routing](#notice-routing)) uses the same map.
- Each applied hook frame sets `@orrery_context` on the frame's pane to that frame's context (section 2).
- `--push-host HOST` names a push relay host that a Web Push subscription may use. Repeat the flag for each host. The default is `ntfy.sh`. A subscription whose endpoint is on another host is refused (section 12).
- `--sessions` serves the engine session service and the step service (see [Steps](#steps)) on `sessions.sock` beside the daemon socket. The headless session manager runs behind it when the store opens. Without `--sessions`, no session socket exists. With it, `--session-shell` must be an absolute path, or the command exits 2.
- `--session-cap N` is the most live headless sessions at once. The default is 4. A value below 1 exits 2.
- `--session-shell PATH` is the absolute login shell that starts each session. The default is `$SHELL`.
- `--step-cap N` is the most live step sessions at once. Steps count apart from `--session-cap`. The default is 6. A value below 1 exits 2.
- `--gate-lock PATH` names a lock file that a step's gate holds while it runs. Repeat the flag for each lock. A gate takes the first lock that is free, in the order given, and when every lock is held it tries again every second. Without the flag, the locks are `/tmp/claude-<uid>/orrery-heavy.lock` and `/tmp/claude-<uid>/orrery-heavy-2.lock`, where `<uid>` is the user id. A path that is not absolute exits 2.

`orreryd` listens on unix sockets only. It opens no network listener. `--sessions` adds the session socket. The desk stream is served on `notices.sock` beside the daemon socket, as [Desk stream](#desk-stream) describes.

Summaries run when a session enters `working` (throttled) and when it becomes `done`.

Each pane transition to `waiting` or `done` makes a pane notice. [Notice routing](#notice-routing) sends it to the desk stream, to Web Push, to both, or to neither. The desk notice has these parts:

- On `waiting`, the body is the question.
- On `done`, the body is the latest summary.
- The title is `<project> · <session>:<window>`, as [Desk stream](#desk-stream) gives it.
- The line's `host` is the machine host name.

Headless session notices (`--sessions`) are routed the same way, with the title and body that [Headless sessions](#headless-sessions) gives.

When a pane enters `done` while an attached client shows the pane's window and had input in the last 30 seconds, `orreryd` sets `@orrery_seen` on the pane to the current Unix time.

When the router sends a notice to the phone, `orreryd` posts it as an encrypted Web Push to every registered subscription. Section 12 defines the payload. The subscriptions are kept in the push store:

- `$XDG_STATE_HOME/orrery/push.json` when `XDG_STATE_HOME` is set.
- Otherwise `$HOME/.local/state/orrery/push.json`.

`orreryd` keeps, per pane and per session, the time of the last notice it sent. When a seen write sets a seen time at or after that time, `orreryd` sends one clear push for that pane or session and forgets the kept time. Seen writes are `orrery seen` (both forms), the tree's switch and mark-read keys, autoseen, the `Panes.MarkSeen` call of [Phone API](#phone-api), and the `Seen` call of the session socket. `orrery seen`, the tree and `Panes.MarkSeen` send `orreryd` a seen report (section 9) with the time they wrote; autoseen and the session `Seen` call run inside `orreryd`. The clear payload is defined in [Clear payload](#clear-payload).

When neither variable is set, `orreryd` exits 1.

`orreryd` also keeps the store, which mirrors each pane's tmux options (see [Store](#store)). When the store cannot be opened, `orreryd` logs the error and serves hooks and notifications without it. It then mints no `@orrery_id` and syncs no pane. A failure of the push store still exits 1.

`SIGINT` and `SIGTERM` stop `orreryd`. With `--sessions`, the session socket closes first. Then every live headless session is stopped, and its turn, if one is running, is recorded `interrupted`. `orreryd` exits after those notices are routed (see [Notice routing](#notice-routing)). The desk stream closes last, after the notices that post to it. See [Headless sessions](#headless-sessions) and [Desk stream](#desk-stream).

#### Notice routing

`orreryd` routes each pane notice, a transition to `waiting` or `done`, and each headless session notice (see [Session notifications](#session-notifications)). A notice stays pending until one of the rules below ends it. The rules apply in this order, and each event writes one line to the [notice log](#notice-log).

1. **Background work.** A pane's `done` whose `Stop` hook has a `background_tasks` holding a subagent or workflow goes nowhere. It is posted to neither the desk stream nor Web Push, and the log records a silence with reason `background work`. A hook whose `background_tasks` does not decode is logged, and its notice routes by the rules below.
2. **Watching.** A pane notice is held while its pane is watched. The pane is watched when either of these holds:
   - a tmux client of the pane's server shows the pane's window and had input in the last 30 seconds;
   - a read report for the pane, sent by `serve` (see [orrery serve](#orrery-serve)), arrived in the last 30 seconds.

   A held notice goes to neither sink. The first hold writes a line with reason `watching`, and orreryd checks the watch again every 5 seconds. A failed check reads as not watched. A session notice is never watched, so it is never held.

**Done judge.** A pane's `done` that rule 1 does not silence is judged, with or without `--sessions`. The first time its pane is found unwatched, the notice waits 10 seconds, sending nothing and writing no line. A change, a seen write, a newer notice or shutdown in that time ends it as rule 7 says. Each later check is made as rule 2 makes it: a watched pane holds the notice with no judge call, and an unwatched pane is judged at once. The judge is Haiku. It reads the `last_assistant_message` of the `Stop` hook's input (section 9), runs through the pane's own `ANTHROPIC_BASE_URL` (section 9) with the summary model that section 8 gives its patchbay context, and answers `needed` (true or false) with a `why`. It must answer within 15 seconds, or it is late. A judge that errors fails, and so does a notice with no or an empty `last_assistant_message`. Waiting notices (questions and permission prompts) and session notices are never judged.

- **Unneeded.** The judge answers not needed, with no error and no late answer. At once, whether or not the desk is at hand, the desk stream gets the notice as a quiet notice and Web Push gets it as a silent notice. Rule 5 applies to the phone part. Nothing escalates the notice, and it ends with that line. Its route names the sinks it reached, as for `away`, and its reason is `unneeded`, with `: <why>` when the judge gave a why. A desk notice that cannot be built is not posted, and the reason gains `; desk notice failed` as rule 6 says, but the silent phone notice is still sent.
- **Needed, failed or late.** The notice routes by rules 3 to 6, and its reason gains `needed` (with its why), `judge failed: <error>` or `judge late`, whichever applies (see the [clauses](#notice-log)).

3. **At the desk.** The desk is at hand when the desk stream is present (see [Desk stream](#desk-stream)) and either the desk's newest input or this host's newest tmux client input is within the last 2 minutes. A failed input read reads as no local input. The notice then goes to the desk stream only, with route `desk` and reason `at the desk`, and stays pending. If it is still unseen and unchanged 5 minutes later, it goes to the phone, with route `phone` and reason `unseen after 5 minutes`, and ends.
4. **Away.** Otherwise the notice goes to the desk stream and to the phone at once, with route `desk+phone` and reason `away`.
5. **Push subscription.** The phone gets a notice only when the push store lists at least one subscription. Without one, the phone part is dropped: the route names what remains, and the reason gains `; no push subscription`. An escalation with no subscription ends the notice with route `none` and reason `unseen after 5 minutes; no push subscription`. A failed subscription check reads as a subscription, so the push is attempted.
6. **Desk notice failure.** A desk notice that cannot be built is not posted. The notice then routes as away, and its reason gains `; desk notice failed` before any `; no push subscription`.
7. **Ending.** A newer notice for the same pane or session ends the pending one, and so does a pane's or a session's transition to a state other than `waiting` or `done`. Each ends with route `none` and reason `changed`. A seen write at or after the notice's time ends it with `none` and `seen`, and the seen write still clears any notice already sent to the phone (see [orreryd](#orreryd)). At shutdown, each pending notice ends with `none` and `shutdown`, and is not sent.

### Headless sessions

With `--sessions`, `orreryd` runs headless Claude Code sessions. Each session is one `claude` process in stream-json mode with `-p`, `--permission-mode bypassPermissions` and `--permission-prompt-tool stdio`. It starts through the login shell `--session-shell`, in the directory its row names, with `orreryd`'s environment. The environment drops `TMUX`, `TMUX_PANE`, `ORRERY_SUMMARIZER` and `ORRERY_SESSION`, and then gains `ORRERY_SESSION` set to the session id. A session id is 128 random bits in lowercase hex.

Each session has one row and an event log in the store (see [Engine sessions in the store](#engine-sessions-in-the-store)). Changes to a session go through the Engine service on the session socket, and the session's requests are answered through it.

#### States

A session is in one of these states. `working`, `waiting` and `done` are also pane states. `stopped`, `failed` and `interrupted` are for sessions only.

| State | Set when |
|---|---|
| `working` | the session starts, a prompt is sent, an answer is accepted, or the session withdraws its open request |
| `waiting` | the session sends a request the host must answer. A refused request leaves the state as it was |
| `done` | a turn ends. A turn that was interrupted with `Interrupt` also ends `done` |
| `stopped` | the `Stop` call ends the process, or the process exits without an error |
| `failed` | the process exits with an error, its directory is unusable, its process cannot start, or its prompt cannot be sent. The row's `reason` says why |
| `interrupted` | `orreryd` shuts down during a turn, or reconcile finds a `working` or `waiting` row at start. The row's `reason` says which |

Idle stop writes no state, so a session it stops keeps the state it had.

#### Session control socket

The session socket is `sessions.sock` in the same directory as the daemon socket. `orreryd` creates it only with `--sessions`, and its mode is `0600`. It serves the `orrery.engine.v1.Engine` service (`proto/orrery/engine/v1/engine.proto`) and the `orrery.engine.v1.Steps` service (`proto/orrery/engine/v1/step.proto`, see [Steps](#steps)) over Connect.

| Call | Request | Precondition |
|---|---|---|
| `Start` | `dir`, `prompt`, `model`, `id` | `dir` is an absolute path to an existing directory, `prompt` is not empty, `model` is empty or 1 to 64 bytes of letters, digits and `. _ - [ ] :` not starting with `-`, `id` is empty or 32 lowercase hex digits, and the session cap has room. An `id` that has a row returns that id and starts nothing, unless the attempt for that `id` failed after storing its row, which returns that attempt's error and starts nothing while `orreryd` runs. Returns `id` |
| `Say` | `id`, `text`, `prompt_id` | the session has a row, and `text` is not empty. A `prompt_id` that an `engine_prompt` event of the session already carries makes the call succeed and send nothing, and a `prompt_id` whose send failed returns an error carrying that failure and sends nothing, also after a restart. A `prompt_id` whose attempt failed in another way after storing the prompt returns that attempt's error and sends nothing while `orreryd` runs. A session that is not live is resumed from its row first |
| `Answer` | `id`, `request_id`, `answer` | the session is live. `answer` is one of the open question's option labels, or `allow` or `deny` for a permission request |
| `Interrupt` | `id` | the session is live |
| `Stop` | `id` | the session is live |
| `Seen` | `id` | the session has a row |

A call that does not fit its precondition does not change the session. An `Answer` that fits neither the question's options nor `allow` or `deny` leaves the request open.

`Say`, `Answer`, `Interrupt` and `Stop` refuse an `id` that names a step, before their own checks. The refusal is `failed_precondition` with the message `<id> is a step: drive it through the Steps service`. A step lookup that fails is `internal`. With no step store, the check is skipped (`internal/enginehost/service.go:104`-`:120`, `internal/enginehost/service.go:148`, `:168`, `:185`, `:201`). A step runs its own loop, so a raw session command on it would drive the step around that loop (`internal/enginehost/service.go:101`-`:103`, and see [Steps](#steps)).

Errors are Connect codes:

| Connect code | When |
|---|---|
| `unavailable` | `orreryd` has no store, so the service has no manager. The message is `session store unavailable` |
| `resource_exhausted` | a `Start`, or a `Say` that resumes a session, would pass the session cap |
| `not_found` | `id` has no row, or `Answer`, `Interrupt` or `Stop` names a session that is not live. Also an `Answer` whose `request_id` is not open |
| `invalid_argument` | a bad `dir`, an empty `prompt` or `text`, a `model` or `id` that does not fit its rule, or an answer that does not fit the open request |
| `failed_precondition` | `Say`, `Answer`, `Interrupt` or `Stop` names a step id (see the paragraph above the table) |
| `internal` | any other failure, including a call made while `orreryd` is shutting down |

#### Lifecycle rules

- A live session is one with a running process. Its slot under the cap is held from its start or resume until its process ends. A `Start` or resume past `--session-cap` is refused before anything is recorded.
- `Start` checks an `id`'s form, then answers with an existing row's id without any other check, unless the attempt for that `id` failed after storing its row, which returns that attempt's error while `orreryd` runs. Otherwise it checks the directory, prompt and model before it writes anything. It records the row `working`, starts the process, records the prompt, and sends the prompt. If the process cannot start, or the prompt cannot be sent, the row is recorded `failed` with the reason, and `Start` returns the id with no error unless recording that failure fails.
- `Say` to a session that is not live resumes it from its row. The process resumes the row's Claude session when the row has one, and otherwise starts a new conversation in the row's directory. The resumed process runs claude's default model, whatever `model` its `Start` named. Concurrent calls for one id start one process.
- `Say` with a `prompt_id` that an `engine_prompt` event of the session already carries returns success before it resumes or sends anything. A `prompt_id` whose send failed returns an error carrying that failure, and sends nothing, also after a restart, because the failure is stored in `engine_prompt_failed` (see [Engine sessions in the store](#engine-sessions-in-the-store)). A `prompt_id` whose attempt failed in another way after it stored the prompt returns that attempt's error, and sends nothing, while `orreryd` runs. An empty `text` is refused before that check. A row whose directory is missing or is not a directory is recorded `failed` with the reason.
- Idle stop: every 30 seconds, a session that has not been busy for 10 minutes is stopped. A session is busy while a turn runs, a request is open, or a background task runs. The stop writes nothing, so the row and its events stay as they were. The next `Say` resumes the session.
- Stop signal: stopping a process sends `SIGINT` to its process group, then `SIGKILL` if it has not exited after 2 seconds.
- `Stop` records `stopped` unless the process's exit has already recorded a state. A process that exits without an error records `stopped`.
- Failed on death: a process that exits with an error records `failed`. The reason is the exit error and stream error, followed by the last non-blank line of its stderr, and the owner is notified. The exit of a process stopped for idle stop or shutdown records nothing itself.
- Shutdown: when `orreryd` receives `SIGINT` or `SIGTERM`, the session socket closes first. Then each live session is stopped, and a session whose turn is running is recorded `interrupted` with the reason `orreryd stopped during the turn`. These writes share one 5-second limit, and each interrupted session notifies its owner. The notices are routed before `orreryd` exits, and a notice still pending then is not sent (see [Notice routing](#notice-routing)).
- Reconcile at start: before the session socket serves, each engine row with no live process loses its unfinished text. A row that is `working` or `waiting` is recorded `interrupted` with the reason `orreryd restarted during the turn`, its question is cleared, and its owner is notified. Other rows keep their state, and no row is removed. The first read or write error ends the pass. `orreryd` logs it and serves anyway.
- A turn that `Interrupt` stops ends `done` with no notice. `Interrupt` does not stop the process.
- `reason` keeps its last value. A later state does not clear it, so a session resumed after a failure shows the old reason until a new failure or interruption sets another.

#### Session notifications

A session notice is routed when a session enters `waiting`, `done`, `failed` or `interrupted`. None is routed for `stopped`, for a refused request, for an idle stop, or for the end of an interrupted turn. A step session sends no notice in any state (see [Step lifecycle](#step-lifecycle)). A notice for `waiting`, `done`, or a process that exits with an error is routed once its event is queued for the store. A notice for `interrupted`, or for a `failed` start, resume or prompt, is routed after its write is stored. Routing runs in the background, and shutdown waits for it. A session notice follows the rules of [Notice routing](#notice-routing), except that it is never watched and never judged.

On the desk stream (see [Desk stream](#desk-stream)), the line's `state` is the session's state, and its `title` is `<project> · session <first 8 characters of the id>`, where `project` is the base name of the directory. The body is:

| State | Body |
|---|---|
| `waiting` | the question, or `waiting for input` when there is none |
| `done` | the text of the turn's last main-conversation text block, or `done` when there is none |
| `failed` | the reason |
| `interrupted` | the reason |

When the router sends the notice to the phone, Web Push goes to every subscription, with the payload of [Push payload](#push-payload). The payload's `session` key holds the full session id. `pane` and `label` are `""`. `project` is the base name of the directory. `state` is the session's state. The body is `question` when the state is `waiting`, and `summary` in every other case.

A session notice clears on the session's own seen: the `Seen` call for that session clears it, as [orreryd](#orreryd) describes.

#### Steps

`orreryd` serves the `orrery.engine.v1.Steps` service (`proto/orrery/engine/v1/step.proto`) on the session socket, beside `Engine`, with the same socket mode and Connect rules. It is not on `serve`. A step is an agent loop that ends with a checked status. It runs as one claude process of kind `step` (see [Steps in the store](#steps-in-the-store)), and [Step lifecycle](#step-lifecycle) gives its rules.

| Call | Request | Response | Effect |
|---|---|---|---|
| `StartStep` | `agent`, `prompt`, `schema`, `workspace`, `revision`, `read_only`, `owned_paths`, `gate`, `gate_limit_seconds`, `done_value`, `model_limit_seconds`, `epic`, `task` | `id` | checks the request, starts the step and returns its id |
| `StopStep` | `id` | none | ends the step as `stopped`, whether it is held or running |
| `StepStatus` | `id` | `step` | returns the step's row |
| `WaitSteps` | `ids`, `timeout_seconds` | `ended`, `steps` | blocks until the first named step ends or the timeout passes |
| `ListSteps` | `epic`, `task` | `steps` | lists the labelled steps, oldest first, narrowed to `epic` and to `task` when each is not empty |
| `HoldStep` | `id` | none | interrupts the step's running turn and holds the step (see [Step lifecycle](#step-lifecycle)) |
| `NudgeStep` | `id`, `text` | none | sends the owner's text to the step (see [Step lifecycle](#step-lifecycle)) |

`StartStep` checks each field as follows:

- `agent` is 1 to 64 bytes of letters, digits and `. _ - [ ] :`, not starting with `-`. Empty is refused.
- `prompt` is not empty.
- `schema` is a JSON object schema. Its root `type` is `object`, its `properties` include `status` with type `string`, and every subschema uses only the keywords `type`, `enum`, `required`, `properties`, `items` and `additionalProperties`, or the annotations `title`, `description`, `$schema`, `default` and `examples`.
- `workspace` is an absolute path to an existing directory.
- `revision` is resolved to a commit when the step starts. Empty means `HEAD`.
- `read_only` false runs a writing step in `workspace`. The directory must be inside a linked git worktree. A directory outside any git work tree is refused, and so is one inside a repository's main working tree.
- `read_only` true runs the step in a detached worktree at its start commit. The worktree is added under `step-worktrees` beside the store file (see [Store](#store)), named by the step id, and removed when the step ends.
- `owned_paths` are paths relative to the repository. An empty path, an absolute path, or one that leaves the repository is refused.
- `gate` is an argv that runs without a shell. Its program is looked up on `PATH` when the step starts, and the start is refused when it is not found. Empty runs no gate.
- `gate_limit_seconds` of zero means 30 minutes.
- `done_value` empty means `done`.
- `model_limit_seconds` of zero means 60 minutes. It is the model time the step may use before the watch ends it `overtime` (see the Watch rule under [Step lifecycle](#step-lifecycle)).
- `epic` and `task` are stored as given, and nothing checks them. The format that `proto/orrery/engine/v1/step.proto:69`-`:70` names for `epic` is not enforced (`internal/enginehost/stepservice.go:63`-`:64`, `internal/enginehost/step.go:177`, `internal/store/steps.go:122`). An empty `epic` leaves the step unlabelled, and `ListSteps` never lists it (`internal/enginehost/step.go:38`-`:39`). An empty `task` means the step works on no one task.

A start refused by one of these checks, by the step cap, or by the workspace rules stores nothing and returns no id. A start that fails after its row is stored returns the error with no id, and the row ends `failed`. The id is not reported on that path.

`StepStatus`, `WaitSteps` and `ListSteps` return each step as a `Step` message: `id`, `agent`, `workspace`, `revision` (the resolved start commit), `read_only`, `worktree` (empty for a writing step), `end` (empty while the step runs), `status`, `result` (the latest return's JSON text), `checks`, `reason`, `started`, `ended` (unix seconds; `ended` is 0 while the step runs), `doing`, the latest doing line from the step's monitor, `epic` and `task`, the labels the step was started with, and `held`, which is true while an owner hold holds the step (`internal/enginehost/stepservice.go:194`-`:199`). Each `checks` entry is a `StepCheck`: `round`, `name`, `ok` and `detail`. The row's prompt, schema, done value, owned paths, gate, gate limit, model limit and verdicts are stored (see [Steps in the store](#steps-in-the-store)) but not returned. `held` is loop state and has no column: it is read from the step's loop, and a step with no loop is not held (`internal/enginehost/stepowner.go:125`-`:131`).

`WaitSteps` returns every named row in the order `ids` gives. `ended` is the named step that ended first, by its end time, and a tie keeps the step listed first. `ended` is unset when the timeout passes before any of them ends. A timeout of zero returns at once. An id with no row fails the whole call with `not_found`.

`ListSteps` returns the rows whose `epic` is not empty, narrowed to rows whose `epic` is the request's `epic` when that is not empty, and to rows whose `task` is the request's `task` when that is not empty. They are ordered by start time, then id (`internal/store/queries.sql:127`-`:131`, `internal/store/steps.go:212`-`:220`).

`HoldStep` and `NudgeStep` return no fields. Their rules are in [Step lifecycle](#step-lifecycle).

Errors are Connect codes:

| Connect code | When |
|---|---|
| `unavailable` | `orreryd` has no step store. The message is `step store unavailable` |
| `resource_exhausted` | a `StartStep` would pass the step cap (`--step-cap`) |
| `not_found` | `StopStep`, `StepStatus`, `WaitSteps`, `HoldStep` or `NudgeStep` names an id with no row |
| `invalid_argument` | an empty `prompt`, a bad `schema`, a bad `agent`, a `workspace` that is not an absolute existing directory, a writing step's `workspace` outside a git work tree or in its main working tree, an `owned_paths` entry that is not a relative path inside the repository, a `gate` program not on `PATH`, or an empty `text` on `NudgeStep` (`internal/enginehost/stepservice.go:229`-`:232`) |
| `failed_precondition` | `HoldStep` finds the step already held or no turn running, or `HoldStep` or `NudgeStep` finds the step running its gate, ended, or with no loop, or `NudgeStep` finds no turn running and the step not held (`internal/enginehost/stepservice.go:226`-`:228`) |
| `internal` | any other failure, including a `revision` that does not resolve (for a read-only step this includes a `workspace` outside any git work tree), a `WaitSteps` call whose context ends first, and a `HoldStep` or `NudgeStep` call whose context ends before the step's loop takes it |

#### Step lifecycle

- **Launch.** A step's process starts through `--session-shell` as a session does (see [Headless sessions](#headless-sessions)). Its arguments are these, with `<agent>` and `<schema>` from the request:

  ```
  <session-shell> -l -c 'exec "$@"' orrery-session <claude> -p --input-format stream-json --output-format stream-json --include-partial-messages --verbose --permission-prompt-tool stdio --permission-mode bypassPermissions --agent <agent> --strict-mcp-config --json-schema <schema>
  ```

  No `--model` is passed, so the agent's model runs. A respawn adds `--resume` with the row's claude session id. The working directory is `workspace`, or the step's worktree for a read-only step. The environment is the session environment, with `ORRERY_SESSION` set to the step id.
- **Kind and silence.** The row's `kind` is `step`. The typed `List` and the tidy read only pane rows, so step rows are never listed. A step sends no notice in any state, on the desk stream or Web Push. Its `engine_*` events are stored and pruned like other engine events, and `orrery.v1.Events.Watch` sends them as their engine members, as it does for engine sessions. A step takes a slot under `--step-cap`, not `--session-cap`.
- **Requests.** A question or permission request that the session did not refuse is denied at once with `The orrery host does not answer questions in a step. Do not ask; give your answer as the structured return.` The request never opens, so the row does not become `waiting`.
- **Returns.** A return is the `structured_output` of a turn's result. It is taken only from a turn that ends with no background task live, so a turn that ends while a background task runs is not a return. After a return the step is busy until `Continue` sends its next prompt or the step stops. A result with no structured output has no value, and fails the schema check.
- **Checks.** Each return is a round, numbered from 1, and its checks are stored with that round in the row's `checks`. The checks run in this order:
  1. `schema`: the value matches the step's schema. The failure names the first failing path, such as `$.status`.
  2. For a writing step whose return passes `schema` and whose `status` equals `done_value`, four commit checks run, in this order: `head` (HEAD is the commit the return's top-level `commit` string names; a missing or non-string `commit` fails it), `ancestry` (the start commit is an ancestor of HEAD), `clean` (no tracked change, staged or unstaged, and no untracked file that is not ignored), and `owned` (every path changed from the start commit to the return's commit is in `owned_paths` by exact match, with both sides of a rename checked).
  3. `gate`: runs only when all four commit checks pass and the step has a gate.

  A read-only step, a return that fails `schema`, or a return whose `status` is not `done_value`, has the `schema` check alone.
- **Gate.** The gate runs in the step's workspace, with its argv and no shell. It holds the first free `--gate-lock` file, taking it with an exclusive non-blocking `flock`. When every lock is held, it tries them again every second. It releases the lock when the gate ends. The gate runs with `orreryd`'s environment, minus `TMUX` and `TMUX_PANE`. It passes when it exits 0 without passing its limit. When the limit passes, its process group is killed with `SIGKILL`, and the check fails with `gate ran past its limit of <limit> and was killed`. A failing check's detail carries the gate's exit status and the last 8 KiB of its output. A gate that cannot start, or cannot take a lock, is an error that ends the step `failed`.
- **Judge.** A return that passes its checks and whose `status` equals `done_value` is judged by a Haiku call. Without a Haiku caller the step ends `done` with no call. The input is the step's first prompt and the return. A writing step adds the `git diff --stat` from the start commit to the return's commit, and a step with a gate adds the gate check's detail. The reply is `met` or `unmet` with a reason. `met` ends the step `done`. The first `unmet` sends the session back once with the reason, and the next return that passes its checks is judged again. A second `unmet` ends the step `goal_unmet`, and its return is kept. A failed diff stat, call or reply stores its error with no verdict and ends the step `done`. The judge's send-back is not one of the three send-backs.
- **Watch.** While a turn runs, the loop checks four backstops on each tick, the first one due in this order: `overtime` (model time reaches the step's model limit), `tool_stuck` (a main-conversation tool call open for 30 minutes of wall time, or 60 after its one extension), `silent` (10 minutes of model time with no event) and `periodic` (5 minutes of turn time since the turn began or the last verdict). Each trip makes one Haiku monitor call. Its verdict is `progressing`, `long_command`, `looping` or `wedged`, with a one-line `doing`, and a non-empty `doing` becomes the row's `doing`. The verdict is applied in this order: `overtime` ends the step `overtime`; `looping` or `wedged` nudges the step once, and then ends it with that verdict; a `long_command` on `tool_stuck` extends that call once, and any other verdict on `tool_stuck` ends the step `tool_stuck`; `silent` nudges once, and then ends the step `silent`; `periodic` does nothing. A nudge interrupts the running turn. The interrupted turn is no round: the step is continued with the nudge text instead of being checked. A nudge does not count toward the three send-backs. A failed monitor call stores its error with no verdict, and the trip acts without one: `overtime` and `tool_stuck` end the step, `silent` nudges on its first trip and ends on the next, and `periodic` does nothing. The monitor's argv, its cap and timeout, and the trip policy with its file and line citations are in [section 15 of the engine protocol](engine-protocol.md#15-the-step-watch-and-judge).
- **Send-back.** A return with a failed check is sent back by `Continue`, as a message that lists each failed check with its detail. The step may be sent back three times. A fourth failing return ends the step `checks_failed`. Nudges and the judge's send-back are counted apart, and they are not among these three.
- **Ends.** Each step ends once, with one of these end kinds:

  | End | Set when | `status` |
  |---|---|---|
  | `done` | a return passes its checks, its `status` equals `done_value`, and the judge says `met`, fails to give a verdict, or is not run | `done_value` |
  | `returned` | a return passes its checks and its `status` is another value | that value |
  | `checks_failed` | a fourth return fails a check | empty |
  | `failed` | the process exits on its own (`the step's process exited` after a clean exit, otherwise the exit's reason), the first prompt cannot be sent, a return or verdict cannot be recorded, a gate cannot run, or a `Continue` cannot be sent | empty |
  | `stopped` | `StopStep` ends the step | empty |
  | `engine_restarted` | `orreryd` starts with the step's row open and no loop for it | empty |
  | `overtime` | the watch's `overtime` trip: model time reaches the step's model limit | empty |
  | `tool_stuck` | the watch's `tool_stuck` trip, when its verdict is neither `long_command` nor `looping` or `wedged`, or its call has had its extension | empty |
  | `silent` | the watch's `silent` trip with a verdict other than `looping` or `wedged`, once the step has been nudged | empty |
  | `looping` | a `looping` verdict, once the step has been nudged | empty |
  | `wedged` | a `wedged` verdict, once the step has been nudged | empty |
  | `goal_unmet` | the judge's second `unmet` verdict; the return is kept | empty |

  The end is stored with the reason, and the step's session is stopped and its worktree removed, whatever ended it.
- **Stop.** `StopStep` cancels the step's loop, which kills a running gate, waits for the loop to return, and then stores `stopped` through the same end path. A step that has already ended is left as it is, and `StopStep` returns success. A held step ends `stopped` the same way (`internal/enginehost/stepctl.go:25`-`:45`).
- **Hold.** `HoldStep` interrupts the step's running turn and marks the step held (`internal/enginehost/stepowner.go:55`-`:69`, `:112`-`:123`). It is refused with `failed_precondition` when the step is already held, when no turn is running, and when the step is running its gate (`internal/enginehost/stepowner.go:56`-`:60`, `:34`-`:37`). A failed interrupt is returned as `internal`, and the step stays unheld. A landed hold drops any nudge pending for the turn, the watchdog's and the owner's (`internal/enginehost/stepowner.go:65`-`:66`). A held step's loop skips each tick, so the watch checks nothing (`internal/enginehost/steploop.go:170`-`:173`). A held step's turn end is no round, and it continues nothing (`internal/enginehost/steploop.go:192`-`:196`).
- **Owner nudge.** `NudgeStep` sends the owner's text to the step (`internal/enginehost/stepowner.go:133`-`:154`). Empty text is `invalid_argument`, and it is refused before the step is looked up (`internal/enginehost/stepowner.go:140`-`:142`). It is refused with `failed_precondition` when the step is running its gate, and when the step is not held and no turn is running (`internal/enginehost/stepowner.go:86`-`:88`, `:34`-`:37`). On a held step, the text resumes the step at once when the held turn has already ended. Otherwise it waits for that turn's end, which is no round, and is continued then (`internal/enginehost/stepowner.go:77`-`:85`, `internal/enginehost/steploop.go:192`-`:199`). On a running turn, the turn is interrupted, and its end is continued with any pending watchdog text followed by the owner's text, in place of a round (`internal/enginehost/stepowner.go:89`-`:97`, `internal/enginehost/steploop.go:201`-`:203`). Owner text sent while a nudge is pending is joined to it, one line per text (`internal/enginehost/stepowner.go:89`-`:92`, `internal/enginehost/steploop.go:255`-`:256`). A nudge neither spends nor replaces the watchdog's nudge, and an OK reply means the nudge landed (`internal/enginehost/stepowner.go:133`-`:138`).
- **Restart.** At start, `orreryd` settles each step's engine row as it settles other engine rows: a row that was `working` or `waiting` is recorded `interrupted` with the reason `orreryd restarted during the turn`, and no notice is sent. Each open step row with no loop then ends `engine_restarted`, and its worktree is removed. Nothing resumes a step after a restart. `Continue`, which the loop sends, respawns a step that is not live from its rows: it resumes the row's claude session with the same agent and schema flags, and it takes a step slot first.


### Desk stream

`orreryd` serves the desk stream on `notices.sock`, beside the daemon socket (section 5). The socket's directory is created with mode 0700 when it is absent. `orrery notices` reads the stream, and `orrery desk` reads it on this machine and over ssh. Nothing here opens a network listener. `orreryd`, `orrery notices` and `orrery desk` use unix sockets, and a host's notices leave it only through the ssh session that `orrery desk` starts.

The stream is JSON lines. Each line is one object and ends with a newline.

- `hello` is the first line of every stream: `{"kind":"hello","host":"example-host"}`.
- `notice` carries one notice: `{"kind":"notice","host":"example-host","state":"waiting","title":"example-project · example-session:1","body":"Which file should I edit?"}`. `state`, `title` and `body` are left out when empty. `quiet` is `true` on a quiet notice, which the judge found unneeded (see [Notice routing](#notice-routing)), and absent otherwise. A quiet notice: `{"kind":"notice","host":"example-host","state":"done","title":"example-project · example-session:1","body":"Example summary of the work.","quiet":true}`.
- The desk sends activity lines, which hold only `input`: `{"input":1700000000}`. `input` is the unix time in seconds of the desk's newest input. A line whose `input` is zero or less is ignored, and so is a line that is not JSON.

Each stream gets `hello` first, then each notice posted while it is open, in the order posted. The stream keeps no backlog, so a notice posted while no stream is open is dropped. A stream that falls 64 lines behind is closed. On shutdown, queued lines get up to 2 seconds to reach their streams, and then open streams are closed.

The desk is present while a stream is open and an activity line arrived within the last 90 seconds. `orreryd` keeps the input of the last activity line it received.

The notices on the stream are these:

- A pane notice, on a pane's transition to `waiting` or `done` (section 11, orreryd). Its title is `<project> · <session>:<window>`, where `project` is the base name of the pane's directory, `session` is the tmux session's name, and `window` is the window's index.
- A session notice, on a headless session's transition to `waiting`, `done`, `failed` or `interrupted` (see [Session notifications](#session-notifications)).

The stream carries a pane or session notice only when [Notice routing](#notice-routing) posts it there. A `done` whose `Stop` hook holds a subagent or workflow is never posted, on the stream or to Web Push.

### orrery notices

```
orrery [--socket PATH] notices
```

- `notices` takes no arguments. An argument exits 2.
- It dials `notices.sock` at the path section 5 gives, from `--socket`, then `ORRERY_SOCKET`, then the default. It needs no tmux server, so `-L` and `-S` have no effect on it.
- Every byte the socket sends goes to stdout. Each line from stdin goes to the socket, newline included.
- When stdin ends, it closes the socket and exits 0.
- When the stream ends first, or the socket stops taking lines, it prints `notices: stream ended` and exits 1.
- When the socket cannot be dialed, it prints `notices: orreryd serves no notices on SOCKET` and exits 1, where `SOCKET` is the `notices.sock` path.
- A failed write to stdout prints `notices: write stdout: ...` and exits 1.

### orrery desk

```
orrery [-L NAME | -S PATH] [--socket PATH] desk --sound FILE [--host HOST]...
```

- `-L NAME` and `-S PATH` select the desk's tmux server, whose clients' input the activity lines carry, as section 6 defines.

- `--sound FILE` is required. Without it, `desk` exits 2. `FILE` is the sound that each popup plays, except a quiet notice's popup (see [Popups](#orrery-desk)).
- `--host HOST` adds a link to `HOST`. Repeat the flag for each host. Without it, the desk has only the local link.
- `desk` runs until `SIGINT` or `SIGTERM`. It then exits 0 once every link has returned.

Each link runs one command, starts it again when it ends, and keeps it running until the desk stops:

- The local link runs the `orrery` binary as `orrery notices`, or as `orrery --socket PATH notices` when `desk` was given `--socket PATH`.
- Each `--host` link runs this command, with no shell:

```
ssh -T -o BatchMode=yes -o ControlMaster=no -o ControlPath=none -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -- HOST orrery notices
```

  `BatchMode=yes` stops ssh from prompting, so a connection that would prompt fails. `ControlMaster=no` and `ControlPath=none` give each link its own connection. ssh ends the connection when three keepalives, 15 seconds apart, go unanswered.

A link reads the lines its command prints, and it writes activity lines to the command's stdin:

- A `hello` makes the stream live. The link writes an activity line at once and then every 30 seconds while the stream is live. The line holds the newest input of the clients attached to the desk's tmux server, in unix seconds. When no client is attached, no line is written.
- A `notice` pops up (see Popups).
- When the command exits, or fails to start, the link waits and then runs the command again. The wait starts at 1 second and doubles after each restart, up to 60 seconds. A `hello` resets it to 1 second.
- When a host has no live stream for 5 minutes, `desk` pops up a lost notice for it. The five minutes start when `desk` starts, or when a live stream ends. A `hello` before they end cancels them. Each stretch without a live stream gets one lost notice.

Popups:

- A notice pops up with `notify-send -a "Coding Agent" -u URGENCY -h boolean:suppress-sound:true -- TITLE BODY`. `URGENCY` is `critical` when the state is `waiting`, and `normal` otherwise. Then `pw-play FILE` plays the sound. The sound plays even when the notice fails. A quiet notice's popup runs `notify-send` as any other and does not run `pw-play`.
- The lost notice has the title `orrery desk`, the body `HOST unreachable for 5 minutes`, `critical` urgency, and the same sound.
- Both programs run from `PATH` with no shell, and each is limited to 10 seconds.
- Popups run one at a time, in the order received. A popup that is running holds up no link.

Example. Every name and path is synthetic:

```
orrery desk --sound /example/sounds/notice.oga --host example-host
```

### Environment

| Variable | Read by | Meaning |
|---|---|---|
| `ORRERY_SOCKET` | `orrery`, `orreryd` | daemon socket path (section 5) |
| `XDG_RUNTIME_DIR` | `orrery`, `orreryd` | default socket directory when no path is set (section 5) |
| `XDG_STATE_HOME` | `orrery` (`serve`), `orreryd` | base directory of the push store, `$XDG_STATE_HOME/orrery/push.json` (section 11, orreryd), of the notice log, `$XDG_STATE_HOME/orrery/notices.jsonl` (see [Notice log](#notice-log)), and of the store, `$XDG_STATE_HOME/orrery/orrery.db` (see [Store](#store)) |
| `HOME` | `orrery` (`serve`), `orreryd` | used for the push store when `XDG_STATE_HOME` is unset, at `$HOME/.local/state/orrery/push.json`, and for the notice log and the store, at `$HOME/.local/state/orrery/notices.jsonl` and `$HOME/.local/state/orrery/orrery.db`. `serve` also uses it as the home directory that `Control.Start` admits directories under (see [Control API](#control-api)) |
| `ANTHROPIC_BASE_URL` | `orrery` (`hook`, `debug summarize`) | the session's base URL. The hook sends it in each frame (section 9), and each summary and each done judge (see [Notice routing](#notice-routing)) runs through its own session's base URL. |
| `ORRERY_SUMMARIZER` | `orrery` (`hook`) | summarizer sentinel (section 7). `orreryd` sets it on each summary run. Summaries also run with hooks disabled. |
| `SHELL` | `orreryd` | the login shell that starts each headless session, when `--session-shell` is not given (see [Headless sessions](#headless-sessions)) |
| `HOME` | `orrery` (`tree`, `open`) | `$HOME/Personal` is the default parent directory of new projects. `$HOME` is also the directory that roots are relative to, and the roots file falls back to `$HOME/.config/orrery/roots` (see [Roots](#roots)) |
| `XDG_STATE_HOME` | `orrery` (`tree`, `open`) | base directory of the archive file (see [Archive](#archive)) |
| `XDG_CONFIG_HOME` | `orrery` (`tree`, `open`) | base directory of the roots file, `$XDG_CONFIG_HOME/orrery/roots` (see [Roots](#roots)) |
| `PATH` | `orrery` (`tree`, `open`, `desk`) | finds `zoxide` and `claude` for the picker and `open`, and `ssh`, `notify-send` and `pw-play` for `desk` (see [orrery desk](#orrery-desk)) |

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

### Notice log

`orreryd` appends one JSON line for each event in the routing of a notice (see [Notice routing](#notice-routing)): a silence, the start of a hold, a route to a sink, or an end that sends nothing. The file is `$XDG_STATE_HOME/orrery/notices.jsonl` when `XDG_STATE_HOME` is set, and `$HOME/.local/state/orrery/notices.jsonl` otherwise. It sits beside the push store (section 11, orreryd). A log that cannot be opened makes `orreryd` exit 1, as a push store failure does.

- The directory is created with mode `0700` and the file with mode `0600` when absent. `orreryd` appends to the file, and never rewrites or deletes a line.
- The file is JSON lines: one object per line, with these keys in this order:

| Key | Type | Meaning |
|---|---|---|
| `time` | number | unix seconds of the event |
| `host` | string | the machine host name |
| `pane` | string | the tmux pane id, for a pane notice; absent for a session notice |
| `session` | string | the session id, for a session notice; absent for a pane notice |
| `state` | string | the notice's state: `waiting`, `done`, `failed` or `interrupted` |
| `route` | string | where the notice went, as below |
| `reason` | string | why, as below |

Example line. Every value is synthetic:

```json
{"time":1700000000,"host":"example-host","pane":"%1","state":"waiting","route":"desk+phone","reason":"away"}
```

The `route` values are these:

- `none`: nothing was sent.
- `desk`: posted to the desk stream.
- `phone`: sent to Web Push.
- `desk+phone`: posted to the desk stream and sent to Web Push.

A route names what `orreryd` handed to each sink. The desk stream drops a notice posted while no stream is open (see [Desk stream](#desk-stream)), so `desk` does not show that anyone read it. A notice at the desk writes two lines: `desk` with `at the desk` when it is posted, and the line that ends it.

The `reason` values are these. A reason that gains a clause adds it after `; `:

| Reason | Route | When |
|---|---|---|
| `background work` | `none` | a `done` whose `Stop` hook holds a subagent or workflow |
| `watching` | `none` | the notice is held; written the first time it is held |
| `at the desk` | `desk` | posted to the desk stream; the notice stays pending |
| `unseen after 5 minutes` | `phone`, or `none` with `no push subscription` | escalated after 5 minutes at the desk, unseen and unchanged |
| `away` | `desk+phone`, `desk`, `phone` or `none` | routed away from the desk |
| `unneeded` | `desk+phone`, `desk`, `phone` or `none` | the judge found the notice unneeded: posted to the desk stream as a quiet notice and sent to Web Push as a silent notice, then ended (see [Notice routing](#notice-routing)) |
| `changed` | `none` | a newer notice for its key, or the pane or session moved to another state |
| `seen` | `none` | a seen write at or after the notice's time |
| `shutdown` | `none` | `orreryd` shut down while the notice was pending |

The clauses are these:

- `needed`: the judge found the notice needed. It takes `: <why>` when the judge gave a why.
- `judge failed: <error>`: the judge failed, or the notice had no or an empty `last_assistant_message`. `<error>` is the judge's error text.
- `judge late`: the judge did not answer within 15 seconds.
- `desk notice failed`: the desk notice could not be built, so none was posted.
- `no push subscription`: the push store lists no subscription, so the phone part was dropped.

The judge clauses follow the route word, and `desk notice failed` and `no push subscription` follow them, so a reason can read `away; needed: <why>; no push subscription`.

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

`orreryd` keeps a SQLite store that mirrors each pane's tmux options, and records each headless engine session (see [Engine sessions in the store](#engine-sessions-in-the-store)). Outside the store's tests, `orreryd`'s own writes read it, and `orrery serve` reads it for the Connect API (section 12). A sync reads the pane's row to compare it with the new snapshot, and a tidy also reads the server's rows to find the panes that are gone. `serve` only reads the store, and never writes it.

The store file is `$XDG_STATE_HOME/orrery/orrery.db` when `XDG_STATE_HOME` is set, else `$HOME/.local/state/orrery/orrery.db`. When neither variable is set, no path resolves.

- The `orrery` directory is created with mode `0700` when absent. The database file is created with mode `0600` when absent. SQLite creates its `-wal` and `-shm` files beside it with the database file's mode, `0600`.
- The database runs in WAL mode. Each connection waits at most 200 ms for a lock.
- Opening the store applies the migrations embedded in `orreryd`. A database that records a newer migration than the binary embeds fails with `store.ErrTooNew`. It is not migrated, and `orreryd` serves without the store.

The `sessions` table has one row per pane and one row per headless engine session. A pane row has `kind` `""`. An engine row has `kind` `engine`, `server` and `pane` `""`, and `id` the session id. The engine columns are described in [Engine sessions in the store](#engine-sessions-in-the-store). A step has a row too, with `kind` `step` (see [Steps in the store](#steps-in-the-store)).

| Column | Type | Meaning |
|---|---|---|
| `id` | `TEXT`, primary key | the pane's `@orrery_id`, or the session id of an engine row |
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
| `kind` | `TEXT` | `""` for a pane row, `engine` for an engine row, `step` for a step row |
| `dir` | `TEXT` | the absolute directory of an engine or step row; `""` on a pane row |
| `reason` | `TEXT` | the reason of an engine row's last `failed` or `interrupted` state; `""` when none |

The ten option columns are `NOT NULL` and default to `''`. Each holds the option's value as tmux reports it, and `""` means unset. `kind`, `dir` and `reason` are `NOT NULL` and default to `''` too.

The `events` table is a log of row changes. Only pruning deletes from it:

| Column | Type | Meaning |
|---|---|---|
| `seq` | `INTEGER`, primary key, autoincrement | the order of the event |
| `session_id` | `TEXT` | the `id` of the row that changed |
| `kind` | `TEXT` | `session_added`, `session_changed`, or `session_removed`, or one of the engine kinds of [Engine sessions in the store](#engine-sessions-in-the-store) |
| `at` | `INTEGER` | unix seconds of the sync |
| `payload` | `TEXT` | JSON for the kind |

The three pane kinds and their payloads. The examples are synthetic:

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
- deletes the server's pane rows whose id is not among the listed panes, with one `session_removed` event for each;
- deletes every event older than 14 days, across all servers, except the newest event, which it keeps so the newest seq is never lost;
- deletes each engine row (`kind` `engine`) that has no event at or after the same 14-day cutoff and is not live, with its unfinished text and its `engine_prompt_failed` rows, and appends no event. Step rows are not deleted (see [Steps in the store](#steps-in-the-store)). See [Engine sessions in the store](#engine-sessions-in-the-store).

A failed `list-panes`, or a listing that does not parse, changes nothing. orreryd logs `tmux server unreachable` (warning) when a server's `list-panes` starts failing and `tmux server reachable again` (info) at its first success after that, not on every tidy. Other tidy failures are logged as `mirror tidy dropped` each time. A failed store write rolls back the whole tidy. A default socket skipped by the directory check logs `tmux default socket skipped` (warning, with `server` and `reason`) when the skip starts and again when its reason changes, not on every tidy.

#### Engine sessions in the store

An engine row is a `sessions` row with `kind` `engine`. Its columns hold these values:

- `state` is one of the session states of [Headless sessions](#headless-sessions).
- `changed` is the unix seconds of the last state change.
- `seen` is the unix seconds of the last `session seen`, or `""`.
- `question` is the open question while the state is `waiting`, or the tool name of an open permission request. An answer, a withdrawn request, a turn's end and reconcile set it to `""`. A row that becomes `stopped`, `failed` or `interrupted` while it waits keeps the question it had.
- `claude_session` is the Claude Code session id, once the process has reported it.
- `dir` is the directory the session runs in. `reason` is described in the table above.

Tidy reads only pane rows, and the typed API's `List` returns only pane rows (see [Connect API](#connect-api)). The engine rows are reached through the Engine service, `session watch`, and the engine messages of the Events stream (see [Connect API](#connect-api)).

The engine kinds of the `events` table are these seven. Each payload is a JSON object with these keys, in camelCase. The examples are synthetic:

- `engine_added`: an engine row is inserted. Payload `{"dir":"/home/user/project","state":"working"}`.
- `engine_changed`: one or more columns change. Payload `{"fields":{"changed":"1700000300","state":"done"}}`. The keys of `fields` are `state`, `seen`, `question`, `claudeSession` and `reason`, for the columns that changed. `changed` is set with each `state` change.
- `engine_prompt`: a prompt is sent to the session. Payload `{"text":"Example prompt."}`. The text is not cut. A `Say` with a `prompt_id` adds the key `promptId` with that id. A `Start` prompt, and a `Say` without a `prompt_id`, have no `promptId` key.
- `engine_block`: a finished content block. Payload keys: `kind`, `role`, `messageId`, `parentToolUseId`, `text`, `toolUseId`, `name`, `input`, `content`, `isError`, and `truncated`. `truncated` is present only when true. `text`, `input` and `content` are each cut to 32768 bytes at a UTF-8 boundary, and `truncated` is true when one of them was cut. `kind` is `text`, `thinking`, `tool_call` or `tool_result`.
- `engine_request`: the session sends a request the host must answer. Payload keys: `kind` (`question`, `permission` or `other`), `requestId`, `toolUseId`, `toolName`, `subtype`, `input` and `refused`. The input is not cut.
- `engine_request_canceled`: the session withdraws a request. Payload keys `requestId` and `toolUseId`. The kind is spelled with one `l`.
- `engine_turn_ended`: a turn ends. Payload keys `subtype`, `isError`, `terminalReason`, `result` and `origin`. The result is not cut.

Engine events are kept and pruned like pane events. The Connect `Watch` call sends each one as its engine member (see [Connect API](#connect-api)).

The `engine_text` table holds the unfinished text of each streaming block, one row per block:

| Column | Type | Meaning |
|---|---|---|
| `session_id` | `TEXT` | the engine row's `id` |
| `message_id` | `TEXT` | the Claude Code message id of the block |
| `block_index` | `INTEGER` | the block's index in its message |
| `parent_tool_use_id` | `TEXT` | the tool use the block belongs to, or `""` for the main conversation |
| `kind` | `TEXT` | `text` or `thinking` |
| `text` | `TEXT` | the text streamed so far, cut to 32768 bytes |

Its primary key is `session_id`, `message_id`, `block_index` and `parent_tool_use_id`. A row is removed when its block finishes, and every row of a session is removed when a turn ends, the process exits, the session is stopped, orreryd shuts down, or reconcile runs at start. No event is appended for engine text. The Connect `Watch` call sends these rows as `EngineText` messages, which are not events (see [Connect API](#connect-api)).

The `engine_prompt_failed` table holds the failure of each `prompt_id` whose send failed, one row per session and `prompt_id`:

| Column | Type | Meaning |
|---|---|---|
| `session_id` | `TEXT` | the engine row's `id` |
| `prompt_id` | `TEXT` | the `prompt_id` of the `Say` |
| `reason` | `TEXT` | the error of the failed send |

Its primary key is `session_id` and `prompt_id`. A row is written in the same write as the session's `failed` state, and an insert for a key that already has a row keeps the first reason. No event is appended for it, and the Connect `Watch` call does not send it. The 14-day cutoff below removes its rows with their engine row.

The 14-day cutoff of the tidy applies to engine rows too. The cutoff is `now` less 14 days. Each engine row with no event at or after the cutoff is deleted with its `engine_text` and `engine_prompt_failed` rows, and no event is appended, unless its session is live. A session is live from its start or resume until it is stopped, idle-stopped, exits, or `orreryd` shuts down, and a live session keeps its row, its `engine_text` rows and its `engine_prompt_failed` rows through the prune. The same tidy pass prunes events older than the cutoff, so the pass runs only while a tmux server's `list-panes` succeeds. A session that is not live loses its row after 14 days with no event, and a live session that waits for an answer keeps its row.

#### Steps in the store

The `steps` table (migrations `00004_steps.sql`, `00005_step_watch.sql` and `00006_step_labels.sql`) has one row per step. Its `id` is the id of the step's `sessions` row. A row is added when the step starts. Its columns are these:

| Column | Type | Meaning |
|---|---|---|
| `id` | `TEXT`, primary key | the step id |
| `agent` | `TEXT` | the agent the step runs as |
| `prompt` | `TEXT` | the step's first prompt |
| `output_schema` | `TEXT` | the schema the return must match |
| `workspace` | `TEXT` | the directory the step was started in |
| `revision` | `TEXT` | the start commit |
| `worktree` | `TEXT` | the read-only step's worktree path; `""` for a writing step |
| `done_value` | `TEXT` | the status that ends the step `done` |
| `read_only` | `BOOLEAN` | whether the step is read-only |
| `owned` | `TEXT` | the `owned_paths` as a JSON array; `null` when there are none |
| `gate` | `TEXT` | the gate argv as a JSON array, with its program resolved on `PATH`; `null` for no gate |
| `gate_limit` | `INTEGER` | the gate limit in nanoseconds |
| `model_limit` | `INTEGER` | the model-time limit in nanoseconds; `0` for a row added before migration `00005` |
| `started` | `INTEGER` | unix seconds when the row was added |
| `checks` | `TEXT` | the checks of every return so far, as a JSON array of `round`, `name`, `ok` and `detail`; `[]` at start |
| `result` | `TEXT` | the latest return's value, as JSON text |
| `doing` | `TEXT` | the latest doing line from the step's monitor; `""` until one is given |
| `verdicts` | `TEXT` | every monitor and judge verdict so far, as a JSON array of `at`, `kind`, `trip`, `round`, `verdict`, `detail`, `error` and `action`; `[]` at start |
| `status` | `TEXT` | the status the step ended with; `""` while it runs and for the ends that carry none |
| `end_value` | `TEXT` | the end kind of [Step lifecycle](#step-lifecycle); `""` while the step runs |
| `reason` | `TEXT` | why the step ended; `""` while it runs |
| `ended` | `INTEGER` | unix seconds when the step ended; `0` while it runs |
| `epic` | `TEXT` | the epic the step was started with, as migration `00006_step_labels.sql` adds it; `""` for an unlabelled step, and for a row added before that migration |
| `task` | `TEXT` | the task slug the step was started with, as migration `00006_step_labels.sql` adds it; `""` when it works on no one task, and for a row added before that migration |

The `sessions` row of a step has `kind` `step`, its `dir` (the workspace, or the worktree of a read-only step), and the same state, `reason` and `claude_session` columns as an engine row (see [Engine sessions in the store](#engine-sessions-in-the-store)). Its engine events are the engine kinds of the `events` table.

The `steps` table is never pruned. The 14-day tidy deletes only engine rows (`kind` `engine`), and the unfinished text and `engine_prompt_failed` rows of those rows, so a step's `sessions` row, its `engine_text` rows and its `engine_prompt_failed` rows are kept too. Events of step rows are pruned with the other events, under the same rule.


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
- runs `orrery seen --pane ID --focus` for the focused pane on `window-pane-changed`, `session-window-changed`, and `client-session-changed`, only while a client is attached to the session. A focus change therefore marks the pane seen only under the `--focus` rule of section 11.

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

`orrery serve` (section 11) serves the Connect API of this section over HTTP at the address given by `--listen`. Every example is synthetic.

### Guard

The guard runs before routing. It checks every request:

- The `Tailscale-User-Login` header must equal one of the `--allow-login` values. Otherwise the response is 403.

Connect paths are those beginning `/orrery.v1.Sessions/`, `/orrery.v1.Events/`, `/orrery.v1.Control/`, `/orrery.v1.Panes/`, `/orrery.v1.Host/`, `/orrery.v1.Push/` or `/orrery.v1.Picker/` (see [Connect API](#connect-api), [Control API](#control-api) and [Phone API](#phone-api)). Every request to a Connect path, whatever its method, must also send a non-empty `X-Orrery-Client` header. Otherwise the response is 403. A POST to a Connect path must send `Content-Type` `application/proto`, `application/json`, `application/connect+proto`, or `application/connect+json`. Otherwise the response is 403.

The web page is the one exception to the 404 rule. After the login check, GET and HEAD of `/` answer `index.html`, and GET and HEAD of any other file path of the page answer that file. A path the page lacks, a directory, and any other method answer 404. Each page answer carries `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'` and `X-Content-Type-Options: nosniff`. The page needs no client header. Any other path that is not a Connect path answers 404, after the login check. The server sends no CORS headers.

### Push payload

When [Notice routing](#notice-routing) sends a pane's `waiting` or `done` notice to the phone, `orreryd` posts it to each registered subscription. A headless session notice (see [Session notifications](#session-notifications)) is posted to each subscription the same way when it goes to the phone.

- The body is one RFC 8291 `aes128gcm` record. The record is at most 4096 bytes.
- The headers are `Content-Encoding: aes128gcm` and `TTL: 3600`.
- The plaintext is a JSON object. Every value is a string. The keys are `pane`, `host`, `project`, `state`, `summary`, `label`, `question`, `question_hash`, and `time`. `time` is the notice's time, as decimal unix seconds. A headless session notice also has the key `session`, holding the session id. A pane notice has no `session` key. A silent notice (see [Notice routing](#notice-routing)) also has the key `silent`, holding `"true"`. No other notice has a `silent` key.
- The plaintext is at most 3993 bytes. When it is longer, the question is cut first, then the summary.
- `question_hash` always covers the full question, even when the question is cut.

Example plaintext. The question and its hash are the fixed vector of section 10:

```json
{"pane":"%1","host":"example-host","project":"example-project","state":"waiting","summary":"Example summary of the pane's work.","label":"example-label","question":"Example question for the user?","question_hash":"7f593eeb8a031e6a99b02a9b65b8e03a17a82a20ca4862ecda37bd06c479a5eb","time":"1700000000"}
```

#### Clear payload

A clear is posted when a seen write sets a seen time at or after the time of the kept notice for a pane or session (see [orreryd](#orreryd)). Its headers are the same as a notice's, `Content-Encoding: aes128gcm` and `TTL: 3600`. Its plaintext is a JSON object of string values with exactly these keys:

- `kind`, which is `clear`.
- `host`, the machine host name.
- `pane`, the pane id, for a pane notice, or `session`, the full session id, for a session notice. Only the one that applies is set.
- `seen`, the seen time, as decimal unix seconds.

Example clear plaintext. Every value is synthetic:

```json
{"kind":"clear","host":"example-host","pane":"%1","seen":"1700000300"}
```

On the phone, the notices of a host are shown as follows:

- One notification per host and pane or session.
- A replacement is silent unless its state is `waiting` or `failed`.
- A clear removes only the notices at or before its `seen` time.
- Session notices carry no actions.
- A silent notice shows on the app's silent channel, with no sound or vibration.

### Connect API

`orrery serve` also serves a typed API over the Connect protocol. The schema is `proto/orrery/v1/orrery.proto`, package `orrery.v1`. Generated Go lives in `internal/gen`. `just generate` writes it, and `just check` fails when it drifts from the schema. The same `just generate` also writes the generated Kotlin into `android/app/src/main/java`, from the template `proto/buf.gen.android.yaml`. `just gen-diff` diffs that directory and `internal/gen`.

The services are `orrery.v1.Sessions`, with three calls, `List`, `ListHeadless` and `WatchConversation`, `orrery.v1.Events`, with one call, `Watch`, `orrery.v1.Control`, with six calls, which [Control API](#control-api) describes, and the four phone services `orrery.v1.Panes`, `orrery.v1.Host`, `orrery.v1.Push` and `orrery.v1.Picker`, with twelve calls, which [Phone API](#phone-api) describes.

| Call | Path | Request | Response |
|---|---|---|---|
| `List` | `POST /orrery.v1.Sessions/List` | `ListRequest`, empty | `ListResponse` |
| `ListHeadless` | `POST /orrery.v1.Sessions/ListHeadless` | `ListHeadlessRequest`, empty | `ListHeadlessResponse` |
| `WatchConversation` (server-streaming) | `POST /orrery.v1.Sessions/WatchConversation` | `SessionsWatchConversationRequest` | stream of `SessionsWatchConversationResponse` |
| `Watch` (server-streaming) | `POST /orrery.v1.Events/Watch` | `WatchRequest` | stream of `WatchResponse` |
| `Control.Start` | `POST /orrery.v1.Control/Start` | `StartRequest` | `StartResponse` |
| `Control.Say` | `POST /orrery.v1.Control/Say` | `SayRequest` | `SayResponse` |
| `Control.Answer` | `POST /orrery.v1.Control/Answer` | `AnswerRequest` | `AnswerResponse` |
| `Control.Interrupt` | `POST /orrery.v1.Control/Interrupt` | `InterruptRequest` | `InterruptResponse` |
| `Control.Stop` | `POST /orrery.v1.Control/Stop` | `StopRequest` | `StopResponse` |
| `Control.Seen` | `POST /orrery.v1.Control/Seen` | `SeenRequest` | `SeenResponse` |
| `Panes.List` | `POST /orrery.v1.Panes/List` | `PanesListRequest`, empty | `PanesListResponse` |
| `Panes.Screen` | `POST /orrery.v1.Panes/Screen` | `ScreenRequest` | `ScreenResponse` |
| `Panes.Conversation` | `POST /orrery.v1.Panes/Conversation` | `ConversationRequest` | `ConversationResponse` |
| `Panes.WatchConversation` (server-streaming) | `POST /orrery.v1.Panes/WatchConversation` | `WatchConversationRequest` | stream of `WatchConversationResponse` |
| `Panes.SendText` | `POST /orrery.v1.Panes/SendText` | `SendTextRequest` | `SendTextResponse` |
| `Panes.SendKeys` | `POST /orrery.v1.Panes/SendKeys` | `SendKeysRequest` | `SendKeysResponse` |
| `Panes.MarkSeen` | `POST /orrery.v1.Panes/MarkSeen` | `MarkSeenRequest` | `MarkSeenResponse` |
| `Host.Status` | `POST /orrery.v1.Host/Status` | `HostStatusRequest`, empty | `HostStatusResponse` |
| `Push.Register` | `POST /orrery.v1.Push/Register` | `RegisterRequest` | `RegisterResponse` |
| `Push.Unregister` | `POST /orrery.v1.Push/Unregister` | `UnregisterRequest` | `UnregisterResponse` |
| `Picker.Ask` | `POST /orrery.v1.Picker/Ask` | `AskRequest` | `AskResponse` |
| `Picker.Run` | `POST /orrery.v1.Picker/Run` | `RunRequest` | `RunResponse` |

`ListResponse` has these fields:

- `sessions` holds one `Session` per pane row of the `sessions` table (see [Store](#store)), in `id` order. Engine rows are not listed. Each `Session` carries the pane row's thirteen columns under the same names: `id`, `server`, `pane`, `state`, `changed`, `seen`, `question`, `summary`, `label`, `context`, `tool`, `claude_session`, and `transcript`.
- `seq` is the largest `seq` in the `events` table when the rows were read, in the same read. It is 0 when there are no events.

Two sessions can share `server` and `pane` for up to 10 s after a tmux server restart. `id` is unique.

`serve` reads the store for `List` at the path that the [Store](#store) section names:

- It opens the file read-only on each call and holds no connection between calls. A file that was replaced, or one that has appeared since the last call, is read as it is now.
- It never writes the database and never creates the file or its directory. SQLite may create the `-wal` and `-shm` files beside an existing store file.
- When the file is missing, the database is busy, or the file is removed while it is read, `List` answers with the Connect code `unavailable`. Any other read failure answers `internal`.

| Connect code | When |
|---|---|
| `unavailable` | the store file is missing, the database is busy, or the file is removed while it is read |
| `internal` | any other read failure |

`orrery.v1.Events.Watch` streams the events table (see [Store](#store)), and the `EngineText` messages described below. `orrery events` and `orrery session --url ... watch` (section 11) read this call.

`WatchRequest` has `after`, the seq the stream resumes after. It is a cursor, and the cursor rules below apply to it.

Each `WatchResponse` has `seq`, `session_id` (the pane's `@orrery_id`, or the headless session's id), `at` (unix seconds when the event was stored), and one member of its `event` oneof. The store kinds map to the members as follows:

| Store kind | `WatchResponse` member | Payload message |
|---|---|---|
| `session_added` | `session_added` | `SessionAdded`: `server`, `pane` |
| `session_changed` | `session_changed` | `SessionChanged` (below) |
| `session_removed` | `session_removed` | `SessionRemoved`, no fields |
| `engine_added` | `engine_added` | `EngineAdded`: `dir`, `state` |
| `engine_changed` | `engine_changed` | `EngineChanged`: `state`, `changed`, `seen`, `question`, `claude_session`, `reason`, each set only when its column changed |
| `engine_prompt` | `engine_prompt` | `EnginePrompt`: `text`, `prompt_id` |
| `engine_block` | `engine_block` | `EngineBlock`: `kind`, `role`, `message_id`, `parent_tool_use_id`, `text`, `tool_use_id`, `name`, `input`, `content`, `is_error`, `truncated` |
| `engine_request` | `engine_request` | `EngineRequest`: `kind`, `request_id`, `tool_use_id`, `tool_name`, `subtype`, `input`, `refused` |
| `engine_request_canceled` | `engine_request_canceled` | `EngineRequestCanceled`: `request_id`, `tool_use_id` |
| `engine_turn_ended` | `engine_turn_ended` | `EngineTurnEnded`: `subtype`, `is_error`, `terminal_reason`, `result`, `origin` |

An engine kind (`engine_*`, see [Engine sessions in the store](#engine-sessions-in-the-store)) maps to its member in this table. `Watch` sends one message per stored engine event, with that event's `seq`, `session_id` and `at`, and the payload of its kind decoded into its member (the payload keys of each kind are in [Engine sessions in the store](#engine-sessions-in-the-store)). A stored payload that does not decode into its member ends the stream with `internal`.

`EngineText` is not an event and has no store kind. It is the text so far of one unfinished content block, a row of `engine_text` (see [Engine sessions in the store](#engine-sessions-in-the-store)). It has the fields `message_id`, `index`, `parent_tool_use_id`, `kind` (`text` or `thinking`) and `text`, and its `WatchResponse` has the `session_id` of the block's session. Its `seq` is the newest event seq of the read it came from, and its `at` is 0. A stream sends it as follows:

- After each read that returns fewer than 100 events, the stream sends an `EngineText` for each unfinished block whose text differs from the text that stream last sent for that block. A block whose text is unchanged is not sent again. A block that has left the unfinished set is forgotten, so a block that returns is sent again.
- Those sends follow the events of that read, and come before the stream waits for its next tick. A read of 100 events sends no `EngineText`.
- A stream that starts while a block is unfinished sends that block's text on its first caught-up read.

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

#### Headless session list

`Sessions.ListHeadless` answers the headless engine sessions with an event in the last 14 days. `ListHeadlessRequest` has no fields. `ListHeadlessResponse` has `sessions`, one `HeadlessSession` for each row, in `id` order.

The rows are the `sessions` rows of kind `engine` that have an event whose `at` is at or after the time of the call less 14 days. Pane rows and step rows are not listed. The call reads the store in one read transaction, and it never writes it.

Each `HeadlessSession` has these fields:

- `id` and `dir` are the row's `id` and `dir`.
- `label` is the base name of `dir`, then ` · `, then the first 8 characters of `id`.
- `state`, `changed` and `seen` are the row's state and its unix-seconds times. `changed` and `seen` are 0 when the row has none. `seen` is 0 when the session was never seen.
- `question` is the row's `question` column, not cut.
- `summary` is the first of these that applies: the `question` while `state` is `waiting` and the question is not empty; the `reason` while `state` is `failed` or `interrupted` and the reason is not empty; the text of the newest main-conversation reply. The newest reply is the text of the assistant's newest text block with no parent tool use. A `summary` is cut to 300 runes. It is `""` when the session has no reply and neither of the other two applies.
- `newest_reply` is the unix seconds of the newest main-conversation reply or unrefused request, or 0 when there is none.

A row whose `changed` or `seen` is not unix seconds fails the whole call with `internal`. Its errors are in the [Phone API errors](#phone-api-errors) table.

The guard covers `/orrery.v1.Sessions/ListHeadless` as it covers every Connect path (see [Guard](#guard)).

#### Headless session stream

`Sessions.WatchConversation` streams the conversation of one headless engine session. It is a server-streaming call. Its request is a `SessionsWatchConversationRequest`, and each message it sends is a `SessionsWatchConversationResponse`, which is either a delta or a keepalive. Its timing and end rules are those of the [Conversation stream](#conversation-stream), and its reset rules and messages differ as set out below.

The request fields are these:

- `id` is the session's id. An id that is missing, or that names a step or a pane, ends the stream with `not_found` and `session not found`, at start or mid-stream.
- `limit` is the number of newest items a reset holds. It follows `ConversationRequest`: 0 means 30, 1 to 100 is that many, and above 100 is `invalid_argument` with `bad request query`. The check runs before any read.
- `after` is 0 to ask for a reset, or the seq the client holds to ask for the items after it.

Reset and resume:

- When `after` is 0, the first delta is a reset. It holds the newest `limit` main-thread items, oldest first, and the whole state, with `reset` true.
- When `after` is not 0, the first delta holds every main-thread item with a seq above `after`, oldest first, with `reset` false. `limit` does not apply to it. A negative `after` holds every item. An `after` past the newest event holds no items.
- No delta after the first is a reset. A stream never replaces what the client holds after its first delta.

Each message has these fields:

- `transcript` is the session's id.
- `seq` is the seq of the newest session event the stream has read, or 0 when it has read none. A later request resumes after it.
- `reset` is true only on a reset (see above).
- `items` holds the main-thread items of the delta. An event makes an item only when it has no parent tool use. A prompt is a `prompt` item with `text`. An assistant text block is a `reply` item with `text`. A tool call is a `tool` item with `tool` set to the tool name, `input` set to the tool input as text, and `id` set to the tool use id. A tool result is a `result` item with `id` set to the tool use id, `text` set to the result's content, and `error` set to whether the result failed. A block of any other kind, and a text block of any other role, make no item. Each item's `time` is the time of its event.
- `state` is a `ConversationState`. It is set on the first delta and when `origin`, `session` or `request` changed, and it is unset otherwise. It has `origin`, the text of the session's first prompt cut to 300 runes, or `""` before a prompt. It has `session`, the `HeadlessSession` of the session with `id`, `dir`, `label`, `state`, `changed`, `seen` and `question` set and `summary` and `newest_reply` unset. It has `request` only while `state` is `waiting`, and then it is the newest unrefused permission or question request the stream has read. `goal`, `waiting`, `context` and `pane` are unset.
- `request` is an `OpenRequest` with `id`, `kind` (`permission` or `question`), and `question`. For a permission, `question` is the tool name and `options` is `allow` and `deny`. For a question, `question` is the question's text, and `options` holds the labels when the input has one question that is not `multiSelect`, and no options otherwise.
- `unfinished` holds the text so far of each unfinished block of the main conversation, as an `EngineText` with `message_id`, `index`, `kind` and `text`. It is sent with every message, keepalives included. The blocks are in message id order, then block index order.

Timing:

- The stream checks the session every 500 ms. A delta is sent when a read finds a main-thread item, when `state` changed, or when `unfinished` changed. A read that moves `seq` without any of these sends no delta.
- After 5 seconds without a message, the stream sends a keepalive: a message with `transcript`, `seq` and `unfinished` set, and no items or state. While nothing changes, it sends one about every 5 seconds, on the 500 ms ticks.

The stream ends in one of these ways:

- When the client leaves the stream, by cancelling the call or closing the connection. This is not an error.
- With an error from the error table, when the session is missing or is not an engine session, or when the store is unavailable or a read or decode fails. This applies at start and mid-stream.
- When serve shuts down. A blocked send of the stream gets 1 second, and so does the stream's end. A stream waiting for its next tick or send at shutdown ends with `unavailable`, with the message `serve is shutting down`.
- When a send stalls. A send still blocked after 10 seconds ends that stream only, with an error outside the error table.

The guard covers `/orrery.v1.Sessions/WatchConversation` as it covers every Connect path (see [Guard](#guard)).

### Control API

`orrery.v1.Control` steers the headless sessions of [Headless sessions](#headless-sessions). `serve` answers each call by forwarding it to the Engine service on orreryd's session socket (see [Session control socket](#session-control-socket)). Each call is a Connect POST to the path of [Connect API](#connect-api), with the guard's Connect rules of [Guard](#guard).

The request and response messages are these:

- `StartRequest`: `dir`, `prompt`, `model`, `id`. `StartResponse`: `id`.
- `SayRequest`: `id`, `text`, `prompt_id`. `SayResponse` has no fields.
- `AnswerRequest`: `id`, `request_id`, `answer`. `AnswerResponse` has no fields.
- `InterruptRequest`, `StopRequest` and `SeenRequest`: `id`. Their responses have no fields.

Rules:

- **Home rule.** `Start` admits `dir` only when it is an absolute path to an existing directory whose symlinks resolve to a path equal to the home directory or under it. `serve` forwards the resolved path. The home directory is `HOME` as `serve` reads it (see [Environment](#environment)). An empty, relative or `/` home, or one that does not resolve, refuses every start. The check runs before the forward, so a refused start never reaches orreryd.
- **Client-chosen ids.** `StartRequest.id` names the new session's id, 32 lowercase hex digits. Empty lets orreryd mint one. A start that names an existing session's id returns that id and starts nothing. `SayRequest.prompt_id` names the prompt. A say whose `prompt_id` an `engine_prompt` event of the session already carries succeeds and sends nothing. A call that names a key with a running attempt waits for that attempt and returns its result. A say whose send failed returns that failure to every later call with its key and sends nothing, also after a restart, because the failure is stored in `engine_prompt_failed`. Any other key whose attempt failed after storing its record returns that attempt's error to every later call with the key, and does nothing else, while `orreryd` runs. A key whose attempt failed before storing its record is free, so a retry runs, unless the record cannot be read after the failure, which keeps the error as a stored record does. Empty means no id and no check.
- **Model.** `StartRequest.model` is the model alias the started session's process runs, passed to claude as `--model`. Empty runs claude's default. A model outside the rule of [Session control socket](#session-control-socket) is refused with `invalid_argument`. A process that a later `Say` resumes runs claude's default model.
- **Forward.** Each call that passes the home rule is forwarded to the engine under a deadline. The deadline is 10 seconds, the default for `serve`, which sets no other. The forward reaches the session socket through a unix-socket transport, `sessions.sock` beside the daemon socket. When no daemon socket path resolves, `serve` has no engine and answers `unavailable` without dialing.
- **Body bound.** A request body over 1 MiB (1048576 bytes) is refused with `resource_exhausted` before it reaches the engine.

Errors are Connect codes. The messages marked fixed are the same whatever orreryd said:

| Connect code | Message | When |
|---|---|---|
| `invalid_argument` | `session directory must be an existing directory under the home directory` (fixed) | `Start`'s `dir` fails the home rule. Answered before the forward |
| `unavailable` | `session service unavailable` (fixed) | no daemon socket path resolves; the session socket cannot be dialed, so orreryd is not taking session commands; or orreryd answers `unavailable`, which it does when it has no store |
| `deadline_exceeded` | `session command timed out` (fixed) | the forward outlives the 10-second deadline, or the client's own deadline ends the call first |
| `internal` | `session command failed` (fixed) | orreryd answers `internal` or `unknown`, or the forward fails with an error that is not a Connect error |
| `resource_exhausted` | the Connect library's message | the request body is over 1 MiB |
| other codes | orreryd's message, passed through | `invalid_argument` (a bad `model`, `id`, `prompt` or `text`), `not_found` (no row, or an answer whose request is not open), `resource_exhausted` (the session cap) |

### Phone API

The phone's calls are the four services `orrery.v1.Panes`, `orrery.v1.Host`, `orrery.v1.Push` and `orrery.v1.Picker` of [Connect API](#connect-api), with twelve calls. Each call is a Connect POST to its path in [Connect API](#connect-api), and the Connect rules of [Guard](#guard) apply to it. This section states each call's request meaning, answer and errors. The error table at the end gives each error code with its message. A successful `Panes.Screen` or `Panes.Conversation` answer reports the read to `orreryd`, and an open `Panes.WatchConversation` stream reports it at its first successful step and once per keepalive interval after that (see [orrery serve](#orrery-serve)).

Request fields and what their zero and presence values mean:

- `PanesListRequest` and `HostStatusRequest` have no fields.
- `ScreenRequest`: `pane` is the pane ID, `%` followed by digits, such as `%3`. Any other value is a malformed pane ID. `ansi` false gives plain text, and `ansi` true keeps the color escape sequences. `history` 0 adds no scrollback. A value from 1 to 2000 adds that many lines above the visible screen, and a value above 2000 is `invalid_argument`. The `ansi` and `history` checks run before the pane ID check, so a `history` above 2000 is `invalid_argument` even for an absent pane.
- `ConversationRequest`: `pane` is as for `ScreenRequest`. `limit` 0 means the default of 30. A value from 1 to 100 is that many items, and a value above 100 is `invalid_argument`. The `limit` check runs before the pane ID check.
- `WatchConversationRequest`: `pane` is as for `ScreenRequest`. `limit` is as for `ConversationRequest`, and it is the number of newest items a reset sends. `transcript` is the id of the transcript the client holds, or empty when it holds none. `after` is the `seq` the client holds for that transcript. The `limit` check runs before the pane ID check.
- `SendTextRequest`: `pane` is as for `ScreenRequest`, and `text` is the text sent.
- `SendKeysRequest`: `pane` is as for `ScreenRequest`, and `keys` is the list of keys sent, in order. `expect_question` is present when the field is set. When it is present, the question check applies.
- `MarkSeenRequest`: `pane` is as for `ScreenRequest`. `read_up_to` absent means now. When present, it must be 0 or more, or the call is `invalid_argument`.
- `RegisterRequest`: `endpoint`, `p256dh` and `auth` are the subscription. `p256dh` and `auth` are the subscription's keys. The endpoint must be `https` and must be on a host named by `--push-host` (section 11).
- `UnregisterRequest`: `endpoint` is the endpoint of the subscription to remove.
- `AskRequest`: `text` is the text to look up. Blank text, meaning empty or only white space, is `invalid_argument`.
- `RunRequest`: `token` is the token to run. A blank token is `invalid_argument`.

The answers:

- `PanesListResponse` carries the tracked panes of `orrery list --json` (section 3), in order. Each pane has the seventeen keys of section 3, then `question_hash` and `newest_reply`. A tmux failure is `internal`. With no tmux server running, the list is empty.
  - `question_hash` is the question hash of section 10 for the pane's `question`.
  - `newest_reply` is the unix seconds of the newest reply or question in the transcript that the pane's `@orrery_transcript` option names (section 2). It counts the `reply` and `question` items of `Panes.Conversation` (below), with their line timestamps, over the whole transcript file rather than the conversation's 4 MiB window. Tool calls, prompts, results, notices and compactions do not count, and neither do sidechain or meta lines. `newest_reply` is 0 when the pane has no `@orrery_state`, when `@orrery_transcript` is unset or empty, when the transcript file does not exist, or when it has no reply or question with a timestamp. Any other transcript read error is `internal`, as is a tmux failure. Serve remembers, for each transcript path, how far it has read and the newest time that read left. A later request reads only the complete lines appended since, and reads the file again from its start when it shrank or was replaced.
- `ScreenResponse` carries the pane's screen as plain text, the output of `capture-pane -p -J`, with `-e` added when `ansi` is true and `-S -N` added when `history` is N. The `screen` bytes are exactly that text. The `ansi` and `history` values are validated as described under `ScreenRequest` above.
- `ConversationResponse` carries the conversation of the pane's Claude transcript, with the fields `origin`, `items`, `goal`, `waiting` and `context`. The transcript is the path in the pane's `@orrery_transcript` option (section 2); no request value names a file. The pane must have `@orrery_state` set, or the answer is `not_found` "no live session". `@orrery_transcript` must be set and non-empty, or the answer is `not_found` "no transcript". A transcript file that does not exist is `not_found` "transcript not found"; any other read failure is `internal`.
  - The reader takes the newest `limit` items from the last 4 MiB of the transcript, oldest first. Only whole lines count, so a partial line at either end of that window is skipped. The reader can return fewer than `limit` items. `items` is empty when there are none.
  - `origin` is the text of the first prompt that the item rules below give within the transcript's first 1 MiB, cut to 300 runes, or empty when there is none. Each line is classified without seeing later lines. A line that the window ends inside is ignored.
  - `goal` is the `condition` of the transcript's newest goal record, or empty. A goal record is an `attachment` line whose `attachment.type` is `goal_status`. The newest goal record in the whole transcript decides, not only the items in the 4 MiB window. When that record has `met` false, `goal` is its `condition`, cut to 300 runes. When it has `met` true, which Claude Code writes both when a goal is met and when it is cleared, or when the transcript has no goal record, `goal` is empty. Only whole lines count, and the `isMeta` and `isSidechain` marks do not apply to goal records. Serve remembers, for each transcript path, how far it has read and the last goal state. The first read scans the whole file. Later reads read only the bytes appended since the last read. A file that is shorter than the part already read, or that was replaced, is read again from the start.
  - Lines of a type other than `assistant`, `user`, or `attachment`, lines marked `isMeta` or `isSidechain`, and lines that are not JSON give no items.
  - An `assistant` line gives one item per content block, in order. A `text` block gives a `reply`. A `tool_use` block gives a `question` when its `name` is `AskUserQuestion`, and a `tool` otherwise. Other blocks, such as thinking blocks, give none.
  - A `user` line whose `isCompactSummary` is true gives one `compaction` item, as described below. Any other `user` line gives items by the first of these rules that applies:
    1. It has one or more `tool_result` blocks. It gives one `result` per such block, and nothing else.
    2. It is a notice, as described below. It gives one `notice`.
    3. Its whole string content is a teammate message, as described below. It gives one `notice` of kind `teammate`, or no item when every block is an idle ping.
    4. Its whole string content is command output or a command record, as described below. Command output gives no item. A command record gives one `prompt`.
    5. Otherwise its text gives one `prompt`, when the text is not empty. The text of a line with string content is that string. The text of a line with array content is its `text` blocks joined with newlines.
  - A typed line is a `user` line whose `promptSource` is `typed`, `queued`, or `suggestion_accepted` and whose `origin.kind` is `human`. Rules 2 to 4 never apply to a typed line, so it gives its `prompt` under rule 5, unless rule 1 applies first.
  - A notice is a `user` line of one of these kinds, checked in this order:
    - `task` when `origin.kind` is `task-notification` and `promptSource` is `system`. Its `text` is the content of the line's first `summary` element, cut to 300 runes, or `""` when that element is absent or not closed. Its `error` is true when the first `status` element is `failed` or `killed`.
    - `interrupted` when its content is one `text` block whose text is exactly `[Request interrupted by user]` or `[Request interrupted by user for tool use]`. Its `text` is `""`.
  - A teammate message is a `user` line whose whole string content is the fixed lead-in `Another Claude session sent a message:` and a newline, then one or more `<teammate-message teammate_id="…">` blocks, each with an optional `color` and an optional `summary` attribute, separated by a blank line, then the fixed trailer Claude Code writes after the last block, which starts with a blank line and the sentence `This came from another Claude session`. A block body starts and ends with a newline. The notice's `text` is the `summary` attribute values of its blocks, in order, joined with a newline, with the XML entities `&lt;`, `&gt;`, `&quot;`, `&apos;`, and `&amp;` decoded. A block without a summary, or with an empty one, adds nothing. The text is cut to 300 runes. A block whose body is a JSON object with type `idle_notification` is an idle ping. A message whose blocks are all idle pings gives no item. Any other message gives one notice, and the summaries of its idle pings still count.
  - Command output is a `user` line whose whole string content is one `<local-command-stdout>` element, with no second closing tag inside it. It gives no item.
  - A command record is a `user` line whose whole string content holds a `<command-message>` element and a `<command-name>` element, in either order, separated only by whitespace, followed by an optional `<command-args>` element. The name must start with `/`. Its `prompt` reads the name, then one space and the args when the args are not empty, so `/name args`, or `/name` alone.
  - Before a `prompt`'s text is kept, each matched `<pasted_content id="X">` … `</pasted_content id="X">` pair in it is removed, keeping the text between its tags. An unpaired tag stays. This applies to every prompt, typed or not. The text is then cut to 4096 bytes. A prompt whose text is empty gives no item.
  - An `attachment` line whose `attachment` has `type` `queued_command`, `commandMode` `prompt`, and `origin.kind` `human` gives one `prompt` with the attachment's `prompt` text, treated as above, in the line's place in the order. It always gives its prompt, even when a later `user` line in the transcript gives a prompt with the same text. Any other attachment gives no item.
  - Each item has these fields, always set, with zero values where the kind has no meaning for them: `kind`, `time`, `text`, `tool`, `input`, `id`, `error`, `notice`, `trigger`, `tokens`.
    - `kind` is `prompt`, `reply`, `tool`, `question`, `result`, `notice`, or `compaction`.
    - `time` is the unix seconds of the line's RFC 3339 `timestamp`, or 0 when it is absent or does not parse.
    - `text` is cut at a UTF-8 boundary. A prompt, question, or result holds at most 4096 bytes, a reply or a compaction at most 32768 bytes, and a notice at most 300 runes. A question's text is its questions joined by newlines. A tool item's `text` is empty.
    - `tool` is the tool name of a `tool` or `question` item, and empty otherwise.
    - `input` is the display text of the tool's input (section 2) for a `tool` item. It is empty for every other kind, including `question`.
    - `id` is the `tool_use_id` of a `tool`, `question`, or `result` item, and empty otherwise.
    - `error` is the `is_error` flag of a `result` item, true for a `task` notice whose status is `failed` or `killed`, and false otherwise.
    - `notice` is the kind of a `notice` item: `task`, `teammate`, or `interrupted`. It is empty on every other kind.
    - `trigger` is the `compactMetadata.trigger` of the boundary of a `compaction` item, and empty on every other kind or when no boundary is read.
    - `tokens` is the `compactMetadata.preTokens` of that boundary, and 0 on every other kind or when no boundary is read.
  - A `compaction` item is a `user` line whose `isCompactSummary` is true, and it gives that item whatever else the line holds. Its `text` is the line's string content, or its `text` blocks joined with newlines, cut to 32768 bytes. Pasted content tags are not removed, and an empty text still gives the item. Its `time` is the line's own timestamp. Its `trigger` and `tokens` come from the nearest `system` line of subtype `compact_boundary` before it in the transcript that is neither `isMeta` nor `isSidechain`. The reader keeps reading past the item limit while a compaction waits for its boundary, within the 4 MiB window. A boundary gives no item.
  - `waiting` is the list of messages that Claude Code's queue holds for the transcript, oldest first. It is empty when there are none, never null. Each row has `text` (the message text, cut to 4096 bytes), `time` (the unix seconds of the RFC 3339 `timestamp` of its opening record, or 0 when that is absent or does not parse), and `canceled` (a boolean).
  - A message opens at an `enqueue` queue-operation record in the transcript whose `content` is its text. An enqueue whose text starts with `<task-notification>`, `<agent-message`, or `<cross-session-message` opens nothing. A message closes at the first of these, matched to the oldest open row with the same full text: a `remove` or `popAll` record with that `content` (a `remove` with reason `cleared_on_cancel` cancels the row instead), or a user line that the person sent from the queue (`promptSource` `queued` and `origin.kind` `human`) with that text. A closed row is gone from the answer.
  - A canceled row stays in the answer, with `canceled` true, until the next `enqueue` record that opens a row or the next user line that the person typed, queued, or accepted from a suggestion. That line drops every canceled row.
  - When the pane's `@orrery_state` is `done`, the answer is idle: every row that is not canceled is dropped for good, so only canceled rows are listed, and a later read of a working pane does not bring them back.
  - The rows are read forward from the offset remembered for the transcript path, as `goal` is. The first read scans the whole file, and later reads read only the bytes appended since. A file that is shorter than the part already read, or that was replaced, is read again from the start. Only whole lines count.
  - `context` is always set. Its `window` is 1000000. Its `tokens` is the figure of the newest qualifying line in the 4 MiB window, searched past the item limit: a `system` line of subtype `compact_boundary` gives its `compactMetadata.postTokens`, and an `assistant` line gives the sum of its `message.usage` `input_tokens`, `cache_creation_input_tokens` and `cache_read_input_tokens`, when that sum is above 0. A missing key counts 0. A line marked `isMeta` or `isSidechain` does not qualify. `tokens` is 0 when no line qualifies.

Example `Panes.Conversation` answer, written with the field names of `ConversationResponse` and shown as JSON. Every value is synthetic:

```json
{"origin":"Example opening prompt.","items":[{"kind":"prompt","time":1700000000,"text":"Example opening prompt.","tool":"","input":"","id":"","error":false,"notice":"","trigger":"","tokens":0},{"kind":"compaction","time":1700000050,"text":"Example summary.","tool":"","input":"","id":"","error":false,"notice":"","trigger":"auto","tokens":611000},{"kind":"reply","time":1700000100,"text":"Example reply.","tool":"","input":"","id":"","error":false,"notice":"","trigger":"","tokens":0},{"kind":"tool","time":1700000200,"text":"","tool":"Bash","input":"example command","id":"example-tool-use","error":false,"notice":"","trigger":"","tokens":0},{"kind":"result","time":1700000210,"text":"Example output.","tool":"","input":"","id":"example-tool-use","error":false,"notice":"","trigger":"","tokens":0},{"kind":"notice","time":1700000300,"text":"Example task finished.","tool":"","input":"","id":"","error":false,"notice":"task","trigger":"","tokens":0}],"goal":"Example goal.","waiting":[{"text":"Example queued message.","time":1700000250,"canceled":false}],"context":{"tokens":240000,"window":1000000}}
```

- `WatchConversationResponse` is one message of the stream. The stream and its rules are in [Conversation stream](#conversation-stream).
- `HostStatusResponse` carries `host` and `stats`. `host` is the machine's host name, and `stats` holds the command's validated stats in order. Without `--host-status-cmd`, the answer is `not_found` "404 page not found".
  - Each stat has the fields `label`, `value`, `percent` (unset when the command gave none), and `level`.
  - The command's output must be a JSON array of at most 12 objects. Each object has a non-empty string `label`, a string `value`, a `level` of `ok`, `warn`, or `crit`, and an optional `percent` that is a number from 0 to 100 (`null` counts as absent). An empty array is valid. Any other output is invalid: a top-level value that is not an array, data after the array, a stat with an unknown key, or a stat with a missing or mistyped field.
  - `label` is cut to 32 runes, and `value` to 64 runes.
  - A run that exits with an error, runs past 2 seconds, or prints more than 64 KiB, and a run whose output is invalid, answers `unavailable` "host status unavailable" with no stats.
  - The outcome of each run, success or failure, is kept for 5 seconds and served to every request in that window. A run whose own request context ends before the command finishes is not kept.
  - A host name failure is `internal`.

Example `Host.Status` answer, shown as JSON. Every value is synthetic:

```json
{"host":"example-host","stats":[{"label":"example-cpu","value":"12%","percent":12,"level":"ok"}]}
```

- `SendTextResponse` and `SendKeysResponse` have no fields. `Panes.SendText` checks the pane first, then sends `text` the way `orrery send --text` does (section 11). The server holds a lock for each pane, so two text sends to one pane run one after the other. The `orrery` CLI is a separate process and is not covered by that lock.
- `Panes.SendKeys` checks the pane first, then sends the keys to the pane in order. An empty `keys` list is `invalid_argument` "bad request body". When `expect_question` is present, the keys are refused with `aborted` "question refused" unless the question check that `orrery send --expect-question` applies (section 11) passes. `expect_question` takes the question hash H from section 10.
- `MarkSeenResponse` has no fields. `Panes.MarkSeen` checks the pane first, then sets the pane's `@orrery_seen` (section 2). Without `read_up_to`, the time is now. With `read_up_to`, a non-negative integer in unix seconds, the time is the later of the stored time and `read_up_to`, so the seen time never moves back. An unset stored time counts as 0. A negative `read_up_to` is `invalid_argument` "bad request body". The server holds a lock for each pane across the read and the write. The `orrery` CLI is a separate process and is not covered by that lock. After the write, the seen report is sent with the stored time. A failed write, or a failed report after the write, is `internal`, and the mark is kept.
- `Push.Register` registers a Web Push subscription, and `RegisterResponse` has no fields. The endpoint is checked first: an endpoint that is not `https`, or whose host is not on a host named by `--push-host` (section 11), is `invalid_argument` "endpoint not allowed". Keys that do not decode to a valid P-256 public key or to the auth secret size are `invalid_argument` "bad request body". A store failure is `internal`.
- `Push.Unregister` removes the subscription with that endpoint, and `UnregisterResponse` has no fields. An endpoint with no stored subscription is `not_found` "endpoint not registered". A store failure is `internal`.
- `AskResponse` carries `rows`, the rows that `orrery open TEXT` would offer for the text (see [Picker](#picker)). The rows come in rank order. Each row has the fields `token`, `kind`, `dir`, `pane`, `label`, `note`, and `prompt`, all always set. `kind` is `jump`, `pane`, `resume`, `continue`, or `fresh`. A `jump` row takes `dir` from its pane's directory, `pane` as the pane ID, and `label` from the pane's label. Other rows give `""` for `pane` and `label`. `note` is the archived summary of a `resume` row, and `""` otherwise. `prompt` is true when the row's action carries a first prompt. A `jump` row is left out when tmux does not list its pane. The answer can have no rows.
  - Each `token` is 32 hex characters and is one-time: its action runs at most once, and it stays valid for 5 minutes from the ask. Tokens are held in serve's memory. Expired tokens are dropped by the next ask that answers without error, and a restart of serve voids every token. Tokens from earlier asks stay valid until their own expiry.
  - The ask runs no action. `new NAME` and `.` are plain text for the intent lookup, and no folder or session is created.
  - Blank `text` is `invalid_argument` "bad request". With no picker configured, the answer is `not_found` "404 page not found". A failure of the picker's configuration, of the text lookup (zoxide, tmux or the intent model), or of the tmux pane listing is `unavailable` with the failure's reason as the message.
- `RunResponse` carries `pane`: the pane the action opened or jumped to, or else the active pane of the session it created. While the token is valid, a repeat run of the same token answers the same pane and runs nothing. Runs of one token are serial.
  - A blank token is `invalid_argument` "bad request". A token no ask issued, or one lost by a restart, is `not_found` "unknown token". A token at or past its expiry is `failed_precondition` "expired token" until an ask that answers without error drops it, and `not_found` "unknown token" after that. Each of these refusals runs nothing.
  - When tmux reports no server running, the answer is `failed_precondition` with the tmux reason as the message, and no action starts.
  - A run whose action fails is `unavailable` with the failure's reason as the message, and records nothing, so a later run of the same token runs the action again.
  - A run whose action succeeded but whose pane lookup then failed is `unavailable` with the failure's reason as the message. It keeps the action's target, so a later run of the same token runs no action and looks the pane up again.

#### Conversation stream

`Panes.WatchConversation` streams the conversation of one pane. It is a server-streaming call, and each message it sends is a `WatchConversationResponse`, which is either a delta or a keepalive. The stream starts with a reset or with the deltas after the client's `seq`, as the reset and resume rules below say, and then sends a delta for each change. The items of a delta follow the item rules of `ConversationResponse` above.

The fields of a `WatchConversationResponse` are these:

- `transcript` is the id of the transcript the delta belongs to. It is the base name of the pane's `@orrery_transcript` path (section 2), so no file path is sent.
- `seq` is a byte offset into that transcript, not a line number. It is the position just past the last complete line the delta covers. A later request resumes with the `transcript` and `seq` that it holds.
- `reset` is true when the delta replaces what the client holds. A reset holds the newest `limit` items, oldest first, and the whole state. A delta that is not a reset holds the items of the lines it covers, oldest first, and that can be none.
- `items` holds the items of the delta, under the item rules of `ConversationResponse`.
- `state` is a `ConversationState`. It has `origin`, `goal`, `waiting` and `context`, as `ConversationResponse` has them, and `pane`, the pane as `Panes.List` gives it. `state` is set on the first delta of every stream and on each delta where a state field changed. Otherwise it is unset.
- `unfinished` holds the text so far of each unfinished content block, as an `EngineText` has it. It is kept for headless sessions (see [Engine sessions in the store](#engine-sessions-in-the-store)). A pane stream never sets `unfinished`.

Reset and resume:

- The first delta is a reset when `transcript` is empty or differs from the id of the pane's transcript, and when `after` is negative, past the end of that transcript, or neither 0 nor just after a newline.
- Otherwise the first delta holds the complete lines after `after`, with `reset` false. An `after` of 0 sends every complete line of the transcript.
- Later in the stream, a reset is sent when the pane's `@orrery_transcript` names another transcript, and when the stream's `seq` is no longer just after a newline of its transcript because the file shrank or was replaced. That reset holds the newest `limit` items of the pane's transcript at that time.

Timing:

- The stream checks the pane every 500 ms. A delta is sent when the transcript has gained complete lines or the state has changed.
- After 5 seconds without a message, the stream sends a keepalive: a message with only `transcript` and `seq` set. While nothing changes, it sends one about every 5 seconds, on the 500 ms ticks.

The stream ends in one of these ways:

- When the client leaves the stream, by cancelling the call or closing the connection. This is not an error.
- With an error from the error table, when the pane is absent, has no live session, has no transcript, or has a transcript file that is missing. This applies at start and mid-stream.
- When serve shuts down. On serve's shutdown, a blocked send of the stream gets 1 second, and so does the stream's end. A stream waiting for its next tick or send at shutdown ends with `unavailable`, with the message `serve is shutting down`.
- When a send stalls. A send of the stream still blocked after 10 seconds ends that stream only, with an error outside the error table.

The guard covers the stream. The rules of [Guard](#guard) apply to `/orrery.v1.Panes/WatchConversation` as they do to every Connect path. A GET to the stream answers 405.

#### Phone API errors

Errors are Connect codes. The message column gives the text of each error exactly. Where the message is "the failure's reason", the text is the reason of the failure as the server gives it, and the message carries it unchanged.

| Connect code | When | Message |
|---|---|---|
| `invalid_argument` | a malformed pane ID | `malformed pane id` |
| `invalid_argument` | `Panes.Screen` with a `history` above 2000; `Panes.Conversation` or `Panes.WatchConversation` with a `limit` above 100 | `bad request query` |
| `invalid_argument` | `Panes.SendKeys` with no keys; `Panes.MarkSeen` with a negative `read_up_to`; `Push.Register` with keys that do not decode | `bad request body` |
| `invalid_argument` | `Push.Register` with an endpoint that is not allowed | `endpoint not allowed` |
| `invalid_argument` | `Picker.Ask` with blank text; `Picker.Run` with a blank token | `bad request` |
| `not_found` | an absent pane, including a pane that goes away during `Panes.WatchConversation` | `pane not found` |
| `not_found` | `Panes.Conversation` with no live session, no transcript, or a transcript file that is missing; `Panes.WatchConversation` with the same cases, at start or mid-stream | `no live session`, `no transcript`, or `transcript not found` |
| `not_found` | `Host.Status` with no `--host-status-cmd`; `Picker.Ask` with no picker configured | `404 page not found` |
| `not_found` | `Push.Unregister` for an endpoint that is not registered | `endpoint not registered` |
| `not_found` | `Picker.Run` with a token no ask issued, or one lost by a restart | `unknown token` |
| `aborted` | `Panes.SendKeys` with `expect_question` that the question check refuses | `question refused` |
| `failed_precondition` | `Picker.Run` with no tmux server running | the tmux reason |
| `failed_precondition` | `Picker.Run` with an expired token | `expired token` |
| `unavailable` | `Host.Status` with a status command that failed or gave invalid stats | `host status unavailable` |
| `unavailable` | `Panes.WatchConversation` while serve shuts down | `serve is shutting down` |
| `unavailable` | `Picker.Ask` with a picker configuration, text lookup, or pane listing failure | the failure's reason |
| `unavailable` | `Picker.Run` with a failed action, or an action whose pane lookup failed | the failure's reason |
| `invalid_argument` | `Sessions.WatchConversation` with a `limit` above 100 | `bad request query` |
| `not_found` | `Sessions.WatchConversation` with an id that is missing or is not an engine session, including a step, at start or mid-stream | `session not found` |
| `unavailable` | `Sessions.ListHeadless` or `Sessions.WatchConversation` with the store file missing, the database busy, the file removed while it is read, or no store reader | `store unavailable` |
| `unavailable` | `Sessions.WatchConversation` while serve shuts down | `serve is shutting down` |
| `internal` | `Sessions.ListHeadless` with a read failure, or a row whose `changed` or `seen` is not unix seconds | `read headless sessions` |
| `internal` | `Sessions.WatchConversation` with a read failure, or a stored value that does not decode | `read sessions` |
| `internal` | a tmux failure, a store failure, a transcript read failure, a host name failure, or a failed seen report | `internal server error` |

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

orrery headless engine sessions (engine lifecycle) added, additively: the `orreryd` flags `--sessions`, `--session-cap` and `--session-shell`, and the `SHELL` read; the session socket `sessions.sock`, its `orrery.engine.v1.Engine` service and its Connect codes (see [Session control socket](#session-control-socket)); the `orrery session` verbs; the `kind`, `dir` and `reason` columns of `sessions`; the `engine_text` table; the seven `engine_*` event kinds and their payloads; the states `stopped`, `failed` and `interrupted`; the session notices on ntfy and Web Push; and the `session` key of the Push payload; the lifecycle rules of [Lifecycle rules](#lifecycle-rules); the Connect error codes of the session socket; and the removal of engine rows after 14 days. The engine kinds are skipped by `orrery.v1.Events.Watch` and by `orrery events`. No existing name or value changes.

orrery E16b (read-up-to seen time) added the optional `read_up_to` body field to the seen route, additively. With it the stored seen time becomes the later of the stored time and `read_up_to`, so it never moves back. No existing name or value changes.

The picker routes `POST /v1/picker/ask` and `POST /v1/picker/run` were added additively, with their tokens held in serve's memory. No existing name or value changes.

orrery engine API added, additively: the `orrery.v1.Control` service and its six calls (see [Control API](#control-api)), and `serve`'s reads of `HOME` and the session socket for it; the `WatchResponse` members `engine_added`, `engine_changed`, `engine_prompt`, `engine_block`, `engine_request`, `engine_request_canceled`, `engine_turn_ended` and `engine_text`, and the `EngineText` rule (see [Connect API](#connect-api)); the `model`, `id` and `prompt_id` fields of the engine socket's `Start` and `Say` (see [Session control socket](#session-control-socket)); the `promptId` key of the `engine_prompt` payload; the `engine_prompt_failed` table; and the `orrery session --url` form and `session start --model`. Watch and `orrery events` now send and print the engine events they skipped before. No existing name or value changes.

orrery E19 (pane tree list) added the `newest_reply` key to each pane of `GET /v1/panes`, additively. It is a key of serve's answer only: `orrery list --json` (section 3) is unchanged. No existing name or value changes.

orrery E21 (compaction item) changed the conversation route, and the change is not additive. A compaction is now a `compaction` item, with the `trigger` and `tokens` keys of its boundary, where it was a `notice` item of kind `compaction`. The `compaction` notice is no longer sent, so a reader that matched it must read the item instead. The item keys `trigger` and `tokens` and the answer's `context` key were added additively. No other existing name or value changes.

orrery notify quiet added, additively: the `seen` key of HookFrame and the seen-report frame (section 9); the `seen --pane ID --focus` form (section 11); the `time` key of the notice payload and the clear payload (section 12); the clear push and the kept notice times (section 11, orreryd); the session notice clear on the session's own seen; and the suppression of Web Push for a `Stop` whose `background_tasks` holds a subagent or workflow. No existing name or value changes, except the focus-mark change below. The tmux focus hook used to run `seen --pane` with no input check. It now runs `seen --pane ID --focus`, so a focus change marks the pane seen only with input in the last 30 seconds. This is a change, not an addition.

orrery H3-0a (phone API) added, additively: the `orrery.v1.Panes`, `orrery.v1.Host`, `orrery.v1.Push` and `orrery.v1.Picker` services and their eleven calls (see [Connect API](#connect-api) and [Phone API](#phone-api)); the Connect path prefixes of [Guard](#guard); and the generated Kotlin in `android/app/src/main/java` (see [Connect API](#connect-api)). No existing name or value changes.

The picker run's failed-run rule changed in the same effort, and this is a change, not an addition. A run whose action succeeded but whose pane lookup then failed used to record nothing, so a later run ran the action again. Such a run now answers 502 and keeps the action's target, so a later run of the same token runs no action and looks the pane up again. The status and reason of a failed run are unchanged.

orrery O1a (steps) added, additively: the `orreryd` flags `--step-cap` and `--gate-lock`, and the `step-worktrees` directory beside the store file; `--sessions` now also serves the step service; the `orrery.engine.v1.Steps` service on `sessions.sock`, its four calls and their Connect codes (see [Steps](#steps)); the `step` value of the `kind` column of `sessions`; the `steps` table (migration `00004_steps.sql`, see [Steps in the store](#steps-in-the-store)); the step end kinds `done`, `returned`, `checks_failed`, `failed`, `stopped` and `engine_restarted`; and the step rules of [Step lifecycle](#step-lifecycle). Step rows are not listed by the typed `List`; their `engine_*` events reach `orrery.v1.Events.Watch` as other engine events do. No existing name or value changes.

orrery removed the REST routes of the phone API: `GET /v1/panes`, `POST /v1/panes`, `GET /v1/panes/{id}/screen`, `GET /v1/panes/{id}/conversation`, `GET /v1/host`, `POST /v1/panes/{id}/text`, `POST /v1/panes/{id}/keys`, `POST /v1/panes/{id}/seen`, `POST /v1/push/register`, `DELETE /v1/push/register`, `POST /v1/picker/ask` and `POST /v1/picker/run`. `POST /v1/panes`, which opened a window, is removed with them and has no replacement. Each other route is answered by the Connect call of [Phone API](#phone-api) that replaced it, and that section states the rules the route had. The guard's rule for non-Connect POST and DELETE paths went with the routes, so every path other than a Connect path answers 404 after the login check (see [Guard](#guard)). This is a removal, and it changes serve's HTTP surface. No Connect name or value changes.

orrery H3b (conversation stream) added, additively: the `orrery.v1.Panes.WatchConversation` call, its messages `WatchConversationRequest`, `WatchConversationResponse` and `ConversationState`, and the `unfinished` field of `WatchConversationResponse`, which holds `EngineText` values that pane streams never set (see [Conversation stream](#conversation-stream)). The phone API now has twelve calls where it had eleven (see [Phone API](#phone-api)). The guard's Connect prefixes already covered the stream, so the guard is unchanged. No existing name or value changes.

orrery desk notices (effort 1) added, additively: the desk stream on `notices.sock` beside the daemon socket, with its `hello`, `notice` and activity lines (see [Desk stream](#desk-stream)); the `orrery notices` and `orrery desk` commands and the `--sound` and `--host` flags of `desk` (see [orrery notices](#orrery-notices) and [orrery desk](#orrery-desk)); the desk presence of the stream; the pane notices and the session notices on the stream; and the `PATH` lookups of `ssh`, `notify-send` and `pw-play` by `desk`. No existing name or value changes, except the removal below.

orrery desk notices (effort 1) removed the ntfy topic. The variables `ORRERY_NTFY_URL_FILE` and `ORRERY_NTFY_TOKEN_FILE`, their constants `protocol.EnvNtfyURLFile` and `protocol.EnvNtfyAuthFile`, the `internal/ntfy` package, and every ntfy post of `orreryd`, including the session notices, are gone. The notices those posts carried are on the desk stream, which `orrery notices` reads, and the `orrery desk` popups show them. `--push-host` and its default `ntfy.sh` are unchanged, because they name the Web Push relay (section 11). This is a removal, not an addition: a setup that set either variable receives no ntfy notice, and reads the desk stream instead.

orrery notice routing (effort 1, N1b2a) added, additively: the `read` key of HookFrame and the read report (section 9); the read reports that `serve` sends (section 11, `orrery serve`) and the Phone API reads that carry them; and the notice log, its keys, route values and reasons (see [Notice log](#notice-log)). No existing name or value changes.

Notice routing (see [Notice routing](#notice-routing)) is a change, not an addition. Before it, every pane notice of a transition to `waiting` or `done` was posted to the desk stream, every headless session notice went to the desk stream and to Web Push, and every notice went to Web Push unless its `Stop` held a subagent or workflow. A `Stop` whose `background_tasks` holds a subagent or workflow now goes nowhere, where the desk stream used to carry it. The routing rules now decide each notice: a notice held for a watched pane reaches neither sink until the watch lapses, and then goes to the desk stream, to Web Push, or to both, by where the user is.

orrery done judge (effort 1, N1b2b) added, additively: the `quiet` key of a desk stream notice line (see [Desk stream](#desk-stream)); the `silent` key of the Push payload (see [Push payload](#push-payload)); and the `unneeded` reason and the `needed`, `judge failed` and `judge late` clauses of the notice log (see [Notice log](#notice-log)). No existing name or value changes.

Plain pane dones now wait 10 seconds once they are unwatched, and an unneeded one may arrive quiet on the desk and silent on the phone. That is a change, not an addition, and [Notice routing](#notice-routing) states the rules.
