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

No hook fires when the person interrupts a turn, so serve ends that turn. When serve lists panes (`Panes.List`, and the pane read of `Panes.WatchConversation`), each `working` pane whose `@orrery_transcript` file exists is checked for an interruption line, `[Request interrupted by user]`, written after its `@orrery_changed`. A pane with one becomes `done`, with `@orrery_changed` set to the time of the listing and `@orrery_tool` and `@orrery_question` unset, as a `Stop` leaves it. The listing shows the pane after that change. A transcript file that does not exist is skipped, and any other read or tmux error fails the listing.

The `StopFailure` hook event ends a pane's turn when an API error does. It makes the pane `done`, as a `Stop` does (`state.targetState`). Its late-frame window and its unset of `@orrery_tool` are the same as `Stop`'s (`state.isLateToolEvent`, `state.writeTool`).

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
- `Stop`, `StopFailure` and `SessionEnd` unset it.
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
| Input | int64 | `input` | the input time of an input report, unix seconds, omitted at zero |
| Ask | string | `ask` | the question a sender puts to orreryd: `"sessions"` in a sessions question, omitted when empty (`protocol.HookFrame.Ask`, `protocol.SessionsAsk`) |

Wire format:

- One frame per connection.
- One JSON object, terminated by `\n`. The object is written on a single line.
- A frame with `seen` above zero and no `hook` is a seen report. orreryd sends no state change for it.
- A frame with `read` above zero and neither `hook` nor `seen` is a read report: serve's word that the phone read the pane at that time. orreryd sends no state change for it. It keeps the time as the pane's newest read (see [Notice routing](#notice-routing)), and it drops a report whose `tmuxPane` is empty.
- A frame with `input` above zero and neither `hook`, `seen` nor `read` is an input report: serve's word that the web view had input at that time. orreryd sends no state change for it, and it touches no pane. It keeps the newest input it receives (see [Desk stream](#desk-stream)). A zero `input` is absent, not an input report.
- A frame with `ask` set to `"sessions"` and none of `hook`, `seen`, `read` or `input` is a sessions question. orreryd writes one JSON line on the same connection, `{"sessions":true}` when its `--sessions` flag is set and `{"sessions":false}` when it is not, then closes the connection. The write has a 1 s deadline. orreryd writes nothing for any other frame (`daemon.answerTimeout`, `daemon.frameHandler.serveConn`, `daemon.frameHandler.answerSessions`; `cmd/orreryd.serve`).

Functions:

- `func WriteFrame(w io.Writer, frame HookFrame) error` writes exactly that format.
- `func ReadFrame(r io.Reader) (HookFrame, error)` decodes one frame. It has no line-length limit, because hook payloads can be large. It returns an error on malformed JSON, on a frame with both `seen` above zero and `hook`, on a frame whose `read` is below zero, and on a frame with `read` above zero together with `hook` or `seen`. It also returns an error on a frame whose `input` is below zero, and on a frame with `input` above zero together with `hook`, a non-zero `seen` or a non-zero `read`. It also returns an error on a frame whose `ask` is not `"sessions"`, and on a frame with `ask` together with `hook`, a non-zero `seen`, a non-zero `read` or a non-zero `input` (`protocol.checkSessionsQuestion`, `protocol.ReadFrame`). Otherwise it returns an error when `hook` is missing, is `null`, or is not a JSON object, unless the frame is a seen report, a read report, an input report or a sessions question.
- `func WriteSessionsAnswer(w io.Writer, on bool) error` writes `{"sessions":true}` or `{"sessions":false}` as one JSON object terminated by `\n` (`protocol.WriteSessionsAnswer`).
- `func ReadSessionsAnswer(r io.Reader) (bool, error)` decodes one answer. It returns an error on end of input, on malformed JSON, and on an object whose `sessions` key is missing or is not a boolean (`protocol.ReadSessionsAnswer`).

Example frame. Every value is synthetic. The wire form is this object on one line, terminated by `\n`:

```json
{"hook":{"hook_event_name":"Stop","session_id":"example-session"},"tmuxPane":"%1","tmux":"/tmp/example-tmux,1234,0","cwd":"/home/user/project","anthropicBaseUrl":"https://patchbay.example.com/ctx/example-context"}
```

Example read report. The value is synthetic. The wire form is this object on one line, terminated by `\n`:

```json
{"tmuxPane":"%1","tmux":"","cwd":"","anthropicBaseUrl":"","read":1700000000}
```

Example input report. The value is synthetic. The wire form is this object on one line, terminated by `\n`:

```json
{"tmuxPane":"","tmux":"","cwd":"","anthropicBaseUrl":"","input":1700000000}
```

Example sessions question. The wire form is this object on one line, terminated by `\n`:

```json
{"tmuxPane":"","tmux":"","cwd":"","anthropicBaseUrl":"","ask":"sessions"}
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
- `debug queue-replay --before TIME [--root DIR]` replays the queue rule over the transcripts under `DIR`, which defaults to `$HOME/.claude/projects`, and writes nine `name count` lines to stdout. `TIME` is an RFC 3339 time and is required. The counts hold no message text.
- `shell --dir DIR --client TTY` opens the popup shell for `DIR` on the client at `TTY`. `DIR` must be absolute.
  - The shell session is named `_orrery-shell-` followed by the first 12 hex digits of the SHA-256 of the cleaned `DIR`. It is created detached in `DIR`, with `@orrery_dir` set to `DIR`, unless it already exists. The popup attaches it.
  - Shell sessions are left out of the tree and of `open`.
- `tree --client TTY` shows the pane tree on the client at `TTY`. The keys are in [Tree keys](#tree-keys). `N` runs the picker for the row's directory, and the picker's choice switches the same client. The command exits 0 when the tree closes.
- `tmux-conf` prints the tmux plugin for this binary's own path (see [tmux plugin](#tmux-plugin)). It takes no flags and needs no tmux server. It exits 1 when the path holds a byte outside `[A-Za-z0-9/._+-]`.
- `help [--html]` prints the help card. It has the same content as the `Daily use` card in the README, built from the keymap table ([Tree keys](#tree-keys) lists its tree rows). When a tmux server can be read, the tmux heading names the prefix key, as `tmux (prefix is C-b)`; otherwise it reads `tmux`. `--html` writes the same content to stdout as one static HTML page instead.
- `open [--remote] [--intent TEXT] [ARGS...]` attaches a session for a project, or starts one.
  - No argument: attaches the most recently active non-shell session, and the tree opens on that client after it attaches.
  - `.`: the session whose `@orrery_dir` is the current directory. If none exists, the picker opens for the directory, as below. `--remote` refuses `.`.
  - A path: an argument that contains `/` or is `..`. It is taken against the current directory and cleaned. An existing directory opens as `.` does: its session, else the picker with its resume rows. A missing path, or one that is not a directory, fails naming it with exit 1, creating nothing and opening no picker. Under `--remote`, a relative path is refused with exit 1, and an absolute path is taken on the remote host.
  - One word: a session with that exact name; else the known directories whose base name is exactly the word. One such directory opens as a path does. Several open the picker with one `pane DIR` row each, in known-directory order; Enter opens a new Claude pane there. None makes the word intent text for the picker.
  - Known directories: each directory once, and only those that exist now, in this order: the `@orrery_dir` of each live session; the record directories of the archive, newest first; the directories of orreryd's engine sessions and step workspaces, newest first, read from the store (see [Store](#store)) read-only. A missing or busy store adds none, and any other store error fails the lookup. Then the children of `$HOME/Personal`; then the roots' directories, as the [picker](#picker) lists them.
  - `new NAME [--root DIR]`: creates the directory `DIR/NAME` and a session for it. `DIR` must be absolute and defaults to `$HOME/Personal`. `NAME` cannot be `.`, `..`, or contain `/`. A taken session name gets `-2`, `-3`, and so on. `.`, `:` and `#` in a name become `_`.
  - Any other arguments: joined with spaces, they are intent text for the picker.
  - A project directory with no live session is not given a new session at once. The picker opens with its resume rows: one `resume` row per archived Claude session for the directory whose transcript still exists, newest first. Without any, it opens with a `continue latest` row. A `start fresh` row always comes last. See [Picker](#picker).
  - `--intent TEXT`: opens the picker with `TEXT` at once. It takes no ARGS; with ARGS it exits 2. Flags come before ARGS.
  - With `TMUX` set, the command switches the invoking client (tmux's current client, by its tty). Otherwise it attaches.
  - `claude` is found on `PATH`.
  - A pane command with arguments (resume, continue, a first prompt) starts as `<default-shell> -c 'exec "$@"' orrery claude <arguments...>` when the base name of tmux's default-shell is sh, bash, zsh, dash or ksh. The pane gets that shell's environment, and each argument reaches claude unchanged. Under any other shell, or when default-shell cannot be read, the command starts directly. `claude` alone runs as tmux runs any one-word command, through default-shell.
  - Exit codes are those in the table above. A refused `open .` with `--remote` exits 1.
- `serve --listen ADDR --allow-login LOGIN` serves the HTTP API of section 12. See `### orrery serve`.
- `events --url URL [--after N] [--follow]` prints the event stream of `serve` as JSON lines. `--url` is the base URL of that `serve`. The command sends no `Tailscale-User-Login` header, so the URL must lead through a proxy that sets it, such as `tailscale serve`.
  - Without `--after`, the cursor is the newest seq that `serve` reports in its `List` response (see [Connect API](#connect-api)). With `--after N`, the cursor is `N`.
  - A cursor past the newest seq is an error. The command exits 1 before any `Watch` call and writes nothing.
  - Without `--follow`, the command writes events through the newest seq that `serve` reported at start, then exits 0. When that seq is not past the cursor, it writes nothing and makes no `Watch` call. If the stream ends before that seq, it exits 1.
  - With `--follow`, it writes events until the stream ends, then exits 1 with the stream's error. An `Unavailable` end is retried first, as [Connect API](#connect-api) gives.
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
  - Without `--url`, the verbs other than `watch` reach `orreryd` on the session socket, `sessions.sock` beside the daemon socket. The daemon socket path comes from `--socket`, then `ORRERY_SOCKET`, then `$XDG_RUNTIME_DIR/orrery/orreryd.sock`. When the socket cannot be dialed, or when `start`, `say` or `stop` gets an answer of off from the daemon (see the retry bullet below), the error reads `orreryd takes no session commands on SOCKET; start it with --sessions`, where `SOCKET` is the `sessions.sock` path (`cli.sessionFailure`).
  - No `session` verb reads a tmux server, so `-L` and `-S` have no effect on them.
  - A usage error exits 2. A failed call exits 1 and prints the Connect error message, as `session VERB: MESSAGE`.
  - `start`, `say` and `stop` retry an `Unavailable` answer, which includes a connection that cannot be made or is cut, for up to `rpcretry.Window`, 60 seconds, counted from the call's first attempt. After each attempt that never connected (no socket file, or the connection was refused), the client asks the daemon on the daemon socket whether sessions are on, under a 1 s deadline for the whole question (see [Hook-to-daemon frame](#9-hook-to-daemon-frame)). An answer of `{"sessions":false}` ends the retry at once, with the `--sessions` refusal of the bullet above. An answer of `{"sessions":true}`, no answer (an older orreryd closes the connection with none), or any failure of the question keeps the retry within `rpcretry.Window`. A connection cut after it was made is retried without a question. The calls are at `cli.sessionStart`, `cli.sessionSay`, `cli.sessionStop`; the question is at `cli.askingAttempt` (the attempt), `cli.sessionAskTimeout` (its deadline), `cli.errSessionsOff` (the off error) and `cli.askSessions`. The waits grow from 100 ms to 5 s (`rpcretry.Window`, `rpcretry.initialBackoff`, `rpcretry.backoffGrowth`, `rpcretry.maxBackoff`, `rpcretry.Do`). `start` sends a client-chosen id and `say` a client-chosen `prompt_id`, so a retry starts no second session and sends no second prompt (see [Lifecycle rules](#lifecycle-rules)). The other verbs, and `watch`, are sent once.
  - `session --url URL VERB ARGS` sends `start`, `say`, `answer`, `interrupt`, `stop` and `seen` to the `orrery.v1.Control` service of `serve` at `URL` (see [Control API](#control-api)), with the same arguments and output as the socket form, except that `DIR` is not made absolute (see below). Each call is sent once: an `Unavailable` answer exits 1 with no retry, and `start` sends no client-chosen id (`cli.sessionStartURL`). It does not dial the session socket, so the client needs no daemon socket and no `--sessions`. Each request carries the `X-Orrery-Client` header that the guard requires. The command sends no `Tailscale-User-Login` header, so the URL must lead through a proxy that sets it, such as `tailscale serve`. `--url` with no URL exits 2.
  - With `--url`, `DIR` is sent as given, not made absolute. `serve` admits only an absolute directory under its home directory, so a relative `DIR` is refused.
  - `session --url URL watch ID` lists `serve`'s newest seq with `Sessions.List`, then reads the `orrery.v1.Events` stream from seq 0. It writes each message whose `session_id` is `ID` as one line, in the format of `orrery events`, including the `engineText` messages of that session. If the stream answers `out_of_range` before any message, it reads again from the newest seq. The watch exits 0 once a message whose seq is at or past that newest seq leaves the session `stopped` or `failed`. A stream that ends before then exits 1 with `session watch: stream ended before session ID stopped`. A stream that ends with `Unavailable` is reopened as [Connect API](#connect-api) gives.
- `run --needs POOL=N [--needs POOL=N]... -- CMD [ARGS]` asks `orreryd` for members of pools and runs `CMD` under that grant. Every flag comes before `--`. `--needs` is required, and a pool may be named once.
  - `POOL` is a pool name as [Pool files](#pool-files) spells it. `N` is a whole number of 1 or more. A pool that the project's pool files do not declare exits 75.
  - `run` reads the pool files for the working directory (see [Pool files](#pool-files)) and sends the project's name and the merged pool definitions with the request. It reaches `orreryd` on `sessions.sock` beside the daemon socket, as the session verbs do. `-L` and `-S` have no effect on `run`.
  - `CMD` is looked up on `PATH` before the request is sent. It runs without a shell, in its own process group, with `run`'s standard input, output and error. Its environment is `run`'s, plus `ORRERY_GRANT`, the grant id, and one `ORRERY_POOL_<POOL>` for each pool named. `<POOL>` is the pool name in upper case, and its value is the granted members, comma-separated. For pool `builds` with members `build-1` and `build-2`: `ORRERY_POOL_BUILDS=build-1,build-2`.
  - While the request waits, `run` writes `orrery run: queued at position N` to stderr when it queues.
  - When `run` receives SIGINT, SIGTERM or SIGHUP after `CMD` has started, it sends SIGTERM to `CMD`'s process group. It returns `CMD`'s status once `CMD` exits. A signal that arrives while the request waits ends the run with 75, and `CMD` does not run.
  - `CMD` must stop its remote work when it gets SIGTERM. `run` sends the signal and stops nothing else.
  - If `run` is killed with SIGKILL, the kernel sends SIGKILL to `CMD`. Processes that `CMD` left in its group are not signalled when `CMD` exits on its own, or when `run` is killed, so they can outlive the grant.
  - When the grant's stream breaks while `CMD` runs, `run` asks for the grant by its id once a second. `CMD` keeps running only when the reclaim's first answer is a `granted` event with the same grant id. Any other first answer loses the grant (see the lost-grant bullet below). A reclaim that fails with `not_found` or `failed_precondition` is a refusal: `run` stops `CMD` as a hold does (see the hold bullet below), sending SIGTERM to `CMD`'s process group and SIGKILL to the group 30 seconds after SIGTERM if it is still running, then exits 75 once `CMD` has exited and the group is empty. Any other failure, such as `orreryd` being unreachable, or a stream that ends before its first event, is tried again. `orreryd` answers a reclaim for a grant it does not keep with `not_found` (see [Pools](#pools)).
  - When a hold stops the grant, `orreryd` sends `stop` on the grant's stream, and `run` stops `CMD` at once. It sends SIGTERM to `CMD`'s process group, sends SIGKILL to the group if `CMD` or a process in the group is still running 30 seconds after SIGTERM, and waits for `CMD` to exit and for the group to be empty. It writes `orrery run: members M were held: stopped CMD` to stderr, where `M` lists the held members, closes the stream, and reclaims the grant by its id. The members are freed once `CMD` has exited. The reclaim waits in line at the request's place, and writes `queued at position N` when it queues. When orreryd grants it, `CMD` runs again under the new grant, with the new `ORRERY_GRANT` and `ORRERY_POOL_<POOL>` values.
  - The stop waits for the whole group. A process that `CMD` left in its group gets SIGTERM with `CMD`, and SIGKILL with the group when any process in it is still running 30 seconds after SIGTERM. The reclaim of a stopped grant is sent only once the group is empty. See [Owner holds](#owner-holds).
  - When the reclaim of a broken stream answers first with `queued`, or with `granted` for another grant id, the grant is lost. `run` stops `CMD` as a hold does, before it waits further, and writes `orrery run: the grant was not reclaimed intact: stopped CMD` to stderr. The reclaim's stream keeps the place in line it took, and `run` does not reclaim again while that stream waits. When the stream grants, `CMD` runs again under the new grant, with the new `ORRERY_GRANT` and `ORRERY_POOL_<POOL>` values. If the stream ends before a grant, `run` reclaims the grant by its id, as the stop path does.
  - Exit statuses:
    - `CMD`'s status. When a signal ended `CMD`, the status is 128 plus the signal number.
    - `75`: `CMD` did not run for want of a grant. That covers a request that `orreryd` refuses, an unreachable `orreryd`, no daemon socket that can be named, a need for a pool that the files do not declare, pool files that cannot be read, do not parse or do not merge, a pool whose cap is too large to send, neither `XDG_CONFIG_HOME` nor `HOME` set, no `git` on `PATH`, a stream that ends or a signal that arrives while the request waits, and a refused reclaim.
    - `2`: a usage error. That is a missing `--needs`, a `--needs` that is not `POOL=N` with a lowercase pool name, a pool named twice, an argument before `--`, or no `CMD` after `--`.
    - `127`: `CMD` is not on `PATH`. Nothing is requested.
    - `126`: `CMD` is on `PATH` but cannot start after the grant. The grant ends with the run.
    - `1`: the exit status of `CMD` cannot be read.
- `run status` writes orreryd's pool status to stdout (see [Status](#pools)). It takes no argument and reads no pool files. The layout is these lines:
  - Each pool in use is a line `NAME (cap N)`. Pools are separated by a blank line.
  - Each member is a line with its name, its state, and its status: `free`, `granted PROJECT pid PID COMMAND AGE`, `held AGE`, or the last two joined by `; `. `AGE` is the time since the grant or hold, in the largest whole unit: `Ns`, `Nm`, `Nh` or `Nd`. `COMMAND` is empty when the client sent none.
  - The CLI does not print `stopping`. A grant that a hold is stopping shows `granted` on each of its members, and the held member also shows `held`.
  - A `waiters` line introduces the requests waiting for the pool, each as `POSITION  PROJECT  COMMAND  count N  AGE`. The position counts over the whole queue.
  - `holds outside pools in use` introduces each hold on a member of no pool in use, as `MEMBER held AGE`.
  - With no pool in use, the first line is `no pools in use`.
  - Exit statuses: `0` after it writes; `1` when `orreryd` cannot be reached, answers an error, or the output cannot be written, with the reason on stderr; `2` for an argument.
- `hold [--off] MEMBER` places an owner hold on `MEMBER` through `orreryd`, or with `--off` frees it (see [Owner holds](#owner-holds)). Flags come before `MEMBER`. Success writes nothing. `hold` reads no pool files: `orreryd` alone judges whether `MEMBER` is a name it accepts.
  - Exit statuses: `0` on success; `1` when `orreryd` refuses or cannot be reached, with the reason on stderr. A refusal includes a malformed name and a `--off` of a member that is not held. `2` for a usage error: any argument count other than one `MEMBER`, or an unknown flag.
  - A call that `orreryd` answers `Unavailable`, or that a restart cuts, is sent again for up to 60 seconds (`rpcretry.Window`, `rpcretry.Do`; `cmd/orrery.placeHold`, `cmd/orrery.freeHold`). A hold is idempotent, so a repeat is harmless. After a cut attempt, `--off` counts a later `not_found` answer that says `member not held` as freed (`cmd/orrery.freeHold`).
- `step resume ID` resumes the step with id `ID` that a usage limit holds (see [Step lifecycle](#step-lifecycle), Usage limit). It calls `Steps.ResumeStep` on `sessions.sock`, beside the daemon socket (`cmd/orrery.runStepWith`). Success writes nothing.
  - Exit statuses: `0` on success; `1` when `orreryd` refuses or cannot be reached, with `orrery step: MESSAGE` on stderr, where `MESSAGE` is the Connect message, such as `failed_precondition: step is not limited` (`cmd/orrery.notLimitedMessage`, `cmd/orrery.isNotLimited`, `cmd/orrery.refuseStep`); `2` for a usage error, which is any argument list other than `resume ID` (`cmd/orrery.runStepWith`).
  - A call that `orreryd` answers `Unavailable`, or that a restart cuts, is sent again for up to 60 seconds (`rpcretry.Window`, `rpcretry.Do`; `cmd/orrery.resumeStep`). A cut attempt may have resumed the step before the connection dropped. When a later attempt answers `step is not limited` after such a cut, the step counts as resumed and the command exits 0 (`cmd/orrery.resumeStep`).
- `evidence run EPIC ITEM REVISION PHASE` asks `orreryd` to run an item's evidence at `REVISION` for `PHASE`, which is `red` or `green`, through the Evidence service on `sessions.sock` (see [Evidence](#evidence)). On success it writes the stored run on one line: its id, item, phase and state, `capturing` at that point. The run goes on in `orreryd` after the command returns. The call is sent again only when it never connected, as `rpcretry.NeverConnected` reports it, since a run is not idempotent (`cmd/orrery.evidenceRun`; `rpcretry.NeverConnected`).
- `evidence list EPIC` writes each run of the epic on one line: its id, item, phase, source, state, verdict (`-` when there is none), `passed` or `not passed`, and the first 12 characters of its revision. Each artifact follows on an indented line: its path, its state and its size (`cmd/orrery.evidenceList`, `cmd/orrery.verdictOrDash`, `cmd/orrery.passedWord`, `cmd/orrery.shortRevision`). A read that answers `Unavailable` or is cut is sent again for up to 60 seconds (`cmd/orrery.evidenceList`; `rpcretry.Window`).
- `evidence get RUN [NAME]` writes the bytes of one artifact, named by its path, or of the run's stdout log when `NAME` is omitted, to stdout unchanged. The stderr log is not read by this command; the APIs read it (see [Epics and Steps](#epics-and-steps)). A read is retried as `evidence list` retries (`cmd/orrery.evidenceGet`).
  - `evidence` takes one subcommand, `run`, `list` or `get`, and then its arguments. Each wrong argument count, and an unknown subcommand, is a usage error.
  - Exit statuses: `0` on success; `1` when `orreryd` refuses the call or cannot be reached, or the output cannot be written, with `orrery evidence VERB: MESSAGE` on stderr (`cmd/orrery.refuseEvidence`; `cmd/orrery.exitFailure`); `2` for a usage error (`cmd/orrery.runEvidenceWith`; `cmd/orrery.report`).
- `holder --session NAME [--state-dir DIR] [--gate-limit SECONDS [--gate-tail BYTES]] -- PATH [ARGS...]` runs `PATH` with `ARGS` as one session's held child and serves the session's holder socket until the child ends (see [Holder](#holder)). `--session` names the session and is required. `--state-dir DIR` places the session under `DIR/holders` instead of the store's directory. `--gate-limit SECONDS` runs the child as a gate with that limit, a positive whole number of seconds (see [Holder](#holder)). `--gate-tail BYTES` sets how many of the gate's newest bytes each of its stdout and stderr keeps in its exit record, from 1 to 16 MiB, and the default is 8 KiB. It needs `--gate-limit`, and it is a usage error alone (`cmd/orrery.parseHolder`; `holder.MaxGateTail`, `holder.ChildConfig.GateTail`).
  - Exit statuses:
    - `0`: the child was stopped or ended by the idle timeout (`exit.json` reason `stopped`, `idle` or `limit`), or it ended on its own with status 0.
    - The child's exit code, when it ended on its own with a nonzero status. A child that a signal ended gives 128 plus the number of the signal that `exit.json`'s `signal` names, whose `code` is then -1 (`cmd/orrery.childStatus`, `holder.exitStatus`).
    - `1`: the holder refused to start or failed, or the child ended on its own with no exit status. Its reason is on stderr as `orrery holder: MESSAGE`, or as `orrery: holder: MESSAGE` when no holders directory can be named (`cmd/orrery.runHolder`, `cmd/orrery.childStatus`).
    - `2`: a usage error. That is a missing or invalid `--session`, a flag other than `--session`, `--state-dir`, `--gate-limit` and `--gate-tail`, a `--gate-limit` that is not a positive whole number of seconds, a `--gate-tail` that is not a whole number of bytes from 1 to 16 MiB or comes without `--gate-limit`, an argument before `--`, or no `PATH` after `--`.
    - A child that ends on its own with status 1 or 2 gives that status too, so the code alone does not say whether the holder or its child failed. The holder writes to stderr only for its own failure (`orrery holder: MESSAGE`, or `orrery: holder: MESSAGE`) and for a usage error (the usage text). An exit that passes on the child's status writes nothing to stderr, and `exit.json` then holds the child's `code` and `signal` (`cmd/orrery.runHolder`).
    - The status comes from the exit record the holder wrote, as `holder.Run` returns it, and not from `exit.json` read back. So a stopper that takes the session lock once the holder releases it, and removes the session's directory, as `holder.Clear` does, leaves the status unchanged (`cmd/orrery.holdSession`, `holder.Run`).
- `gate [--gates FILE] NAME` runs the named gate `NAME` under a grant of its needs from `orreryd`. `NAME` is resolved as [Gate files](#gate-files) gives: from `FILE` first when `--gates` names one, then from the project file for the working directory. A relative `FILE` is read from the working directory. `gate` reads the pool files for the working directory, as `run` does (see [Pool files](#pool-files)), and reaches `orreryd` on `sessions.sock` as `run` does.
  - The argv runs through the same `RunWithGrant` path as `run`'s command (`cmd/orrery.runGate`). It runs without a shell, in the working directory, with `gate`'s standard input, output and error. Its environment is `gate`'s, plus `ORRERY_GRANT` and one `ORRERY_POOL_<POOL>` for each pool in its needs, as `run` sets them. Its limit counts from the start of each run under its grant, not from the queue (`cli.RunConfig.Limit`).
  - The lines `run` writes to stderr while its command queues, is stopped, loses its grant or passes its limit are written by `gate` too, with the prefix `orrery run:` (`cli.nextEvent`, `cli.runOnce`, `cli.commandEnd.report`).
  - After the argv's run has ended or has failed to start, `gate` writes one line to stderr: `orrery gate: NAME from SOURCE FILE: argv ARGV needing POOL=N,...; exit STATUS`. `SOURCE` is `epic` or `project`, `FILE` is the file that defines the gate, `ARGV` is the argv as a JSON array, and `STATUS` is the status `gate` exits with (`cmd/orrery.runGate`).
  - Exit statuses:
    - `2`: a usage error. That is a `--gates` with no file, no `NAME`, more than one argument, or a flag other than `--gates`.
    - `75`: the gate did not run. Before the grant, that covers a `NAME` that no file defines, an epic gates file or project file that cannot be read or does not parse, a project that cannot be found (no `git` on `PATH`, or neither `XDG_CONFIG_HOME` nor `HOME` set), a need for a pool that the pool files do not declare, pool files that cannot be read, parsed or merged, and no daemon socket that can be named. `gate` writes `orrery gate: MESSAGE` to stderr for these. An agent gate is refused the same way, once its `NAME` resolves and before any pool file is read: `gate` writes `orrery gate: gate NAME is an agent gate; it runs only as a read-only step` to stderr, with `NAME` as given (`cmd/orrery.runGate`, `cmd/orrery.refuseGate`). After the grant request, it covers what `run` gives 75 for: a request `orreryd` refuses, an unreachable `orreryd`, a signal that arrives while the request waits, and a refused reclaim.
    - `124`: the gate's limit passed, and the argv was killed with `SIGKILL`.
    - Otherwise, the argv's own status, as `run` gives it for `CMD`: its exit code, or 128 plus the signal number when a signal ended it. `127` means `argv[0]` is not an executable file, and nothing is requested. `126` means it cannot start after the grant. `1` means its status cannot be read.
  - `SIGINT`, `SIGTERM` and `SIGHUP` stop `gate` as they stop `run`: after the argv has started, the argv gets `SIGTERM` and `gate` returns its status. While the request waits, the signal ends the run with 75 (`cmd/orrery.runGate`).
- `epic FORM` makes one call of the Epics service on `sessions.sock` (see [Epics](#epics)) and prints its result. Each form is the call of the MCP tool of the same name that `orrery mcp` serves (`cmd/orrery.dispatch`, `cmd/orrery.runEpic`; `stepmcp.epicTools`). The forms are these (`cmd/orrery.epicCommand`):
  - `epic create FILE|-` creates an epic. The JSON of `FILE`, or of stdin for `-`, is the arguments of `epic_create`.
  - `epic get ID` prints the stored epic, as `epic_get` returns it.
  - `epic list` prints each stored epic as a summary, as `epic_list` returns it.
  - `epic answer EPIC --id ID [--question QN] [--choice C] [TEXT]` records a decision on `EPIC` with the id `ID`. `--question QN` names the question the decision answers, as `Q<n>`, and is left out for a note. `--choice C` names the choice the answer gives the question: a declared choice's id, or `other`. `TEXT` may be left out only with `--choice`. `--id` is required, and the flags come after `EPIC` and before `TEXT` (`cmd/orrery.epicAnswer`, `cmd/orrery.epicAnswerForm`).
  - `epic follow` makes the call `epic_follow` and prints the follow count of each question label, as `epic_follow` returns it (see [FollowEpics](#epics)). It takes no argument (`cmd/orrery.epicCommand`).
  - `epic pause ID`, `epic resume ID` and `epic stop ID` make the calls `epic_pause`, `epic_resume` and `epic_stop`.
  - `epic retry ID [NODE]` makes the call `epic_retry`. Without `NODE` it ends the epic's park. With `NODE` it reruns the parked run of that workflow node under its next attempt, as [Epics](#epics) gives. `epic retry ID ""` is a usage error: an empty `NODE` names no node, and sending it would end the park (`cmd/orrery.epicRetry`, `cmd/orrery.epicForm`).
  - `epic release ID [--undo-none N]...` makes the call `epic_release`. Each `N` is the 1-based position of an undo-none release step to run again, and the positions are sent in the order given. A missing `ID`, an `ID` that begins with `-`, and an `N` that is not a positive integer are usage errors (`cmd/orrery.epicRelease`, `cmd/orrery.epicForm`; see [Release](#release)).
  - The result text is printed on stdout with a newline. For `answer`, a non-empty warning in the result is also printed on stderr.
  - Exit statuses: `0` after the result is printed. `1` when the call is refused, with its text on stderr as `orrery: TEXT`, or when the session socket cannot be reached. `2` for a usage error: a missing or unknown verb, a form with the wrong number of arguments, or an `answer` without `--id`, with neither `TEXT` nor `--choice`, or with more than one `TEXT` (`cmd/orrery.report`, `cmd/orrery.epicAnswer`).

Exit codes:

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | error |
| 2 | usage error |
| 3 | question refused (`send --expect-question`: no question, hash mismatch, empty display fragment, or fragment not visible) |

`run` and `gate` exit with the statuses listed in their bullets above, which are not in this table. `run status`, `hold` and `epic` use only the codes in this table.

### orrery serve

```
orrery [-L NAME | -S PATH] serve --listen ADDR --allow-login LOGIN [--allow-login LOGIN]... [--push-host HOST]... [--host-status-cmd PATH] [--metrics-listen ADDR]
```

- `-L NAME` and `-S PATH` select the tmux server, as section 6 defines.
- `--listen ADDR` sets the address to serve on. ADDR is a loopback IP literal with a port, such as `127.0.0.1:8080` or `[::1]:8080`; a port of 0 lets the kernel choose one. Any other ADDR exits 1 before anything is bound. Once the address is bound, `serve` writes `orrery serve: listening on ADDR` to stderr, with the address it bound (`cmd/orrery.serveListening`).
- `--allow-login LOGIN` admits one Tailscale login. Repeat the flag for each login. At least one is required.
- `--push-host HOST` names a push relay host, as `orreryd` does. Repeat the flag for each host. The default is `ntfy.sh`.
- `--host-status-cmd PATH` sets the command that `Host.Status` runs (section 12). `PATH` must be absolute; a relative `PATH` exits 2 before anything is bound. Without the flag, `Host.Status` answers `not_found`. The command runs at `PATH` with no arguments and no shell. Its stdout is read up to 64 KiB, and its stderr is discarded. A run is limited to 2 seconds, and a child that outlives it and holds stdout open is cut off 500 ms later. Each run's outcome is cached for 5 seconds (section 12).
- `--metrics-listen ADDR` serves `/metrics` on a second loopback address, ADDR, in the form `--listen` takes, and port 0 there too lets the kernel choose. Once it is bound, `serve` writes `orrery serve: metrics listening on ADDR` to stderr, before the `--listen` line. Without the flag, `serve` records and serves no metrics. [Metrics](#metrics) gives the listener, the series and the alert rules.

Registrations are written to the push store that `orreryd` reads (see `### orreryd`). When neither `XDG_STATE_HOME` nor `HOME` is set, `serve` exits 1.

`serve` also reads the store for the Connect API of section 12. It opens the store file read-only on each call. The rules are in [Connect API](#connect-api).

`serve` also answers the web page of section 12 at `/`, from the page embedded in the binary.

`serve` also serves the `orrery.v1.Control` API of [Control API](#control-api), forwarding each call to orreryd's session socket. The daemon socket path comes from `--socket`, then `ORRERY_SOCKET`, then `$XDG_RUNTIME_DIR/orrery/orreryd.sock`, and the session socket is `sessions.sock` beside it. When no daemon socket path resolves, `serve` has no engine and answers `unavailable` to each Control call that reaches the forward; a `Start` the home rule refuses still answers `invalid_argument`. `serve` also reads `HOME`, the directory that `Control.Start` admits directories under.

`serve` reads the gambit record root, `$HOME/.gambit`, read-only, for the epic calls of [Epics and Steps](#epics-and-steps). It opens the records for reading and never creates, writes, renames or chmods anything under the root. It reaches orreryd's `orrery.engine.v1.Steps` and `orrery.engine.v1.Epics` services on the same session socket, `sessions.sock`, for the step and epic calls of [Epics and Steps](#epics-and-steps) and for the step refusal of [Control API](#control-api). When serve has no step client, `Epics.List`, `Epics.Get`, `Epics.History` and the step calls answer `unavailable` "step service unavailable" (see [Phone API errors](#phone-api-errors)), and the Control calls forward as before. When serve has no epic client, `Epics.List`, `Epics.Get` and `Epics.History` read the records alone (`server.epicsServer.listStored`, `server.epicsServer.getStored`, `server.epicsServer.getStoredHistory`; `cmd/orrery.runServe`), and `Epics.Pause`, `Resume`, `Stop`, `Retry` and `Answer` answer `unavailable` "epic command service unavailable" (`server.epicsServer.forwardCommand`).

`serve` also serves the `orrery.v1.Pools` service of [Phone API](#phone-api). It forwards `Status`, `Hold` and `Free` to orreryd's session socket under the same daemon socket rule as Control, and it has no `Acquire`.

The picker call `Picker.Ask` (see [Phone API](#phone-api)) looks up `claude` on serve's PATH at each ask, not at start. A missing `claude` answers that ask `unavailable` and does not stop serve.

`serve` also sends `orreryd` a read report (section 9) with the pane and the time of each read it answers. It sends one after each successful `Panes.Conversation` or `Panes.Screen` answer. While a `Panes.WatchConversation` stream stays open, it sends one at the stream's first successful step, and again each time the keepalive interval has passed since the last report. That interval is 5 seconds by default. The report goes to the daemon socket (section 5). When no `orreryd` listens there, the report is dropped.

`serve` also sends `orreryd` an input report (section 9) for each `Presence.Report` it answers (see [Phone API](#phone-api)). The input is dated at serve's clock less the reported `input_age_ms`, in whole unix seconds. An age that is not below serve's clock reports nothing. The report goes to the daemon socket (section 5), and it is dropped when no `orreryd` listens there. Any other failure is written to standard error, and the call still answers.

`serve` also serves the `orrery.v1.Notices` service of [Connect API](#connect-api). It forwards `Watch` and `Seen` to orreryd's `orrery.engine.v1.Notices` on `sessions.sock`, and it relays each engine message as its `orrery.v1` twin. `Seen` runs under the control timeout of 10 seconds. When the engine stream ends or fails, the page's stream ends as `unavailable`, unless `serve` is shutting down, so the page reopens (`server.noticesServer.Watch`, `server.noticesServer.Seen`, `server.noticesEnd`, `server.noticeWatchMessage`; `server.registerNotices`; `server.defaultControlTimeout`). With no engine client, both calls answer `unavailable` (`server.noticesServer.Seen`, `server.noticesServer.watch`).

`--listen` and at least one `--allow-login` are required. Without them `serve` exits 2.

`serve` is meant to sit behind `tailscale serve`, which sets the `Tailscale-User-Login` header that section 12 checks. Do not expose it through Tailscale Funnel.

`SIGINT` and `SIGTERM` stop `serve` and exit 0. Each open `orrery.v1.Events` stream ends first; the [Connect API](#connect-api) section gives the code it ends with.

Example. Every name and address is synthetic:

```
orrery serve --listen 127.0.0.1:8080 --allow-login user@example.test
```

### orreryd

```
orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]... [--push-host HOST]... [--sessions] [--session-cap N] [--session-shell PATH] [--holder-scope PATH] [--step-cap N] [--metrics-listen ADDR] [--flow-ticks PATH] [--judge-delay DURATION] [--write-tick DURATION] [--due-at SECONDS] [--host NAME]
```

- `-L`, `-S`, and `--socket` work as they do for `orrery`.
- `--claude PATH` sets the `claude` binary the summarizer runs, and the binary each headless session runs.
- `--summary-model CONTEXT=MODEL` maps a patchbay context (section 8) to a summary model. Repeat the flag for each context. A context with no mapping uses `claude-haiku-5-5`. The done judge (see [Notice routing](#notice-routing)) uses the same map.
- Each applied hook frame sets `@orrery_context` on the frame's pane to that frame's context (section 2).
- `--push-host HOST` names a push relay host that a Web Push subscription may use. Repeat the flag for each host. The default is `ntfy.sh`. A subscription whose endpoint is on another host is refused (section 12).
- `--sessions` serves the engine session service, the step service (see [Steps](#steps)), the epic service (see [Epics](#epics)), the Pools service (see [Pools](#pools)), the evidence service (see [Evidence](#evidence)) and the in-app notice service (see [Notices](#notices)) on `sessions.sock` beside the daemon socket. The headless session manager runs behind it when the store opens. `sessions.sock` listens only after the store open has ended, so after the `store open` line when the store opens. Without `--sessions`, no session socket exists. With it, `--session-shell` must be an absolute path, or the command exits 2. The daemon socket answers the sessions question whether or not the flag is set, with `{"sessions":true}` when it is and `{"sessions":false}` when it is not (see [Hook-to-daemon frame](#9-hook-to-daemon-frame); `cmd/orreryd.serve`, `daemon.frameHandler.serveConn`).
- `orreryd` sends one `READY=1` datagram to the `NOTIFY_SOCKET` it started with, once it serves. Without `--sessions` the send comes once the store open has returned, opened or not. With `--sessions` it comes once the sessions start, after `sessions.sock` listens, so a failed start sends nothing. A failed send is logged as `notify systemd, serving anyway`, and serving goes on. An empty `NOTIFY_SOCKET` sends nothing. The socket is a unixgram path, or an abstract name that starts with `@` (`cmd/orreryd.serve`, `cmd/orreryd.notifyReady`; `sdnotify.Ready`). `orreryd` reads `NOTIFY_SOCKET` and removes it from its environment at start, before any child environment is captured (`cmd/orreryd.run`, `cmd/orreryd.startSessions`). `READY=1` only takes effect under a `Type=notify` unit; under `Type=simple` it has no effect.
- `--session-cap N` is the most live headless sessions at once. The default is 4. A value below 1 exits 2.
- `--session-shell PATH` is the absolute login shell that starts each session. The default is `$SHELL`.
- `--holder-scope PATH` is the absolute path of `systemd-run`, which runs each session's holder in its own user scope. The default is none, and the holder then starts plainly. A relative path exits 2 (`cmd/orreryd.parseFlags`, `cmd/orreryd.report`). [Headless sessions](#headless-sessions) gives the start under the flag, and [Lifecycle rules](#lifecycle-rules) gives shutdown under it.
- `--step-cap N` is the most live step sessions at once. Steps count apart from `--session-cap`. The default is 6. A value below 1 exits 2.
- `--metrics-listen ADDR` serves `/metrics` on ADDR, a loopback IP literal with a port, and takes Alertmanager's webhook at `POST /alerts` (see [Alert intake](#alert-intake)). Without the flag, `orreryd` records and serves no metrics and takes no alerts. [Metrics](#metrics) gives the listener, the series and the alert rules.
- `--flow-ticks PATH` is a test seam. With it, the flow passes once at start and once per `SIGUSR1` in place of its five-second ticks, and appends one line to PATH after each pass. Without it, the flow passes every five seconds and `SIGUSR1` keeps its default action (`cmd/orreryd.flowSeam`, `cmd/orreryd.forwardSignals`, `cmd/orreryd.appendPassLine`).
- `--host NAME` is a test seam. With it, `orreryd` uses NAME as its host name where it would read the machine's: the `host` of each desk line, the notices and the host the grants broker serves (`cmd/orreryd.hostName`). Without it, the host name is the machine's.
- `--due-at SECONDS` is a test seam. With it, the due pass that resumes limited steps reads the time as the unix second SECONDS, so a step whose reset is after it is never due, whatever the date. Without it, or with zero, the due pass reads the system clock. A negative value exits 2 (`cmd/orreryd.dueClock`, `cmd/orreryd.sessionHost.startDue`).
- `--write-tick DURATION` is a test seam. It replaces the 100 ms tick at which the engine writer stores the engine ops every session has queued with DURATION, a Go duration of zero or more; zero keeps 100 ms. A wait for a stored op, such as a step loop's record of its round, then waits at most DURATION for its tick. A negative value exits 2 (`enginehost.WriterConfig.Tick`).
- `--judge-delay DURATION` is a test seam. It replaces the 10-second wait of an unwatched pane's `done` before its judge (see [Notice routing](#notice-routing)) with DURATION, a Go duration of zero or more; zero judges at once. Without it, the wait is 10 seconds (`route.JudgeDelay`, `route.Config.JudgeDelay`, `route.NoJudgeDelay`). A negative value exits 2.

`orreryd` listens on unix sockets only, apart from the loopback listener that `--metrics-listen` adds, which serves `/metrics` and `POST /alerts` ([Metrics](#metrics), [Alert intake](#alert-intake)). `--sessions` adds the session socket. The desk stream is served on `notices.sock` beside the daemon socket, as [Desk stream](#desk-stream) describes.

With `--sessions` and an open store, `orreryd` runs every epic through the interpreter, after the step reconcile (`cmd/orreryd.startSessions`, `cmd/orreryd.sessionHost.startEvidence`, `cmd/orreryd.sessionHost.startFlow`). [Workflows](#workflows) gives the interpreter's passes, and the planner and the build Runner are its planner and builder. [Planner turns](#planner-turns) gives the planner's turns, and its attention loop, which runs beside the interpreter and routes the epic notices. The planner's agent is the `planner` key of the role file, `$XDG_CONFIG_HOME/orrery/roles.toml`, or `$HOME/.config/orrery/roles.toml` when `XDG_CONFIG_HOME` is unset. The file's top-level keys are role names and their values are agent names. A missing file, a file that does not parse, a `planner` key with no agent, and a non-string value are each an error that names the file (`roles.Agent`, `roles.rolePath`; `cmd/orreryd.sessionHost.startFlow`). The nix module writes this file from `programs.orrery.roles`, and writes none when that option is empty (`nix/home-manager.nix: options.programs.orrery.roles`, `nix/home-manager.nix: xdg.configFile."orrery/roles.toml"`).

With `--sessions` and an open store, the build Runner runs under the interpreter, which hands it the tasks of each build child run that runs (see [Task builds](#task-builds); `cmd/orreryd.sessionHost.startFlow`). `orreryd` stops the flow before the evidence runner closes, and the flow returns once its current pass ends and the planner's turns have settled (`cmd/orreryd.sessionHost.shutdown`; `interpret.Runner.Run`).

With `--sessions` and an open store, `orreryd` runs the due pass after the flow starts. It passes once at start and then every `limitTick`, and it stops with the other loops (`cmd/orreryd.startSessions`, `cmd/orreryd.sessionHost.startLimits`, `cmd/orreryd.sessionHost.startIdle`, `cmd/orreryd.sessionHost.startDue`; see [Due resumes in the store](#due-resumes-in-the-store) and [Step lifecycle](#step-lifecycle), Usage limit).

Each automatic action is keyed by its cause, and a repeat under the same cause finds the first one rather than repeating it. The planner's step is keyed by its epic and turn (see [Planner turns](#planner-turns)). An evidence judge is keyed by its run and judge number, and a build's steps and runs by `StepID` and `RunID` (see [Evidence](#evidence) and [Task builds](#task-builds)). An interpreter's node run is keyed by its path and its `NodeRunID`, and a later plan's trigger by its `workflow:` id (see [Workflows](#workflows)). A gate's holder is named for its step, `<step id>-gate`, or for its evidence run, `evidence-<run id>` (`enginehost.stepRun.runDoneGate`; `enginehost.evidenceCapture.captureGate`). An automatic resume's prompt is keyed by its step and reset (see [Step lifecycle](#step-lifecycle)). An epic notice is keyed by its cause (see [Planner turns](#planner-turns)), and a limit notice by its kind and reset (see [Step lifecycle](#step-lifecycle)).

Each automatic action is one point where `orreryd` can be killed: a store write, a step start, a prompt send, a git ref write, a notice route, and each outside effect that the crash-point harness counts. The interpreter's pass, the planner runner's `PassOnly`, the attention loop and the due pass each run one pass per call (`interpret.Runner.Pass`; `planner.Runner.PassOnly`; `planner.Attention.Pass`; `enginehost.DuePass.Pass`), and the build's git writes go through the `Git` field of `build.Config` (`build.Git`, `build.Config`).

The crash-point test, `internal/build/crashpoint_test.go`, drives one epic through 12 rounds of a fake clock, over a store that `storetest.Seed` creates in a scratch repository. Round 0 is the epic's creation, and each round is 1800 seconds after the one before (`build.crashRounds`, `build.crashEpoch`, `build.crashRoundSeconds`, `build.roundAt`, `build.newCrashWorld`). A round runs the due pass, the finish of each open step that no limit holds, the planner, the attention pass and the build runner, in that order (`build.crashWorld.incarnation`; `build.crashWorld.finishOpen`). The router's store-call bound is `StoreAfter`, a timer that never fires, so no store call reads as late (`build.crashWorld.incarnation`, `build.crashStoreAfter`).

An incarnation builds fresh components over the same store. Before its first round it attaches the router and recovers the planner's running turns (`build.crashWorld.incarnation`). An uninterrupted run is one incarnation to its last round. A killed run is one incarnation that stops after action N, then a restart from the round the kill stopped (`build.crashRunKilledAt`).

An action is each store write that the components make through `crashStore`, and each outside effect the fakes perform through `act` or `effect`: a git ref write, a step start, a resume prompt or clear, a desk post, a phone notify (`build.crashWorld`, `build.crashWorld.act`, `build.crashWorld.effect`, `build.crashStore`, `build.crashStore.ClaimTurn`). A step start is one action that writes the step's row with its limit or its return. An automatic resume is two actions, its prompt and then one write that clears the limit and closes its due rows (`build.crashWorld.startStep`, `build.crashResume.AutoResume`).

A kill is a gate with a limit of N. The first N actions are performed. The next action is refused without running and leaves no label, and it cancels the incarnation's context, so every later action is refused too (`build.gate`, `build.crashWorld.act`, `build.crashWorld.killed`). The incarnation then stops at its next stage or round boundary. It has no shutdown path. It waits for the planner's turns and the router's decisions, so none is left running (`build.crashWorld.incarnation`).

Each kill point is compared with the uninterrupted run (`build.crashCompare`). The restarted run's end rows must equal the uninterrupted run's. Those rows are the epic with its turns, tasks and evidence runs, the due rows, the notice rows, the labelled steps and the attention of each epic (`build.crashWorld.endRows`, `build.crashWorld.dueRows`). Its first N action labels must equal the uninterrupted run's. Each started id, a step or an evidence run, must have started once, and the starts and the resume prompts sent per prompt id must equal the uninterrupted run's (`build.crashCompare`). Each notice id must be routed once, with the same counts as the uninterrupted run. A notice counts as routed when a stored row carries its id after its route (`build.crashCompare`; `build.crashRouter.Notify`, `build.crashWorld.noticeStored`).

The uninterrupted run must do the work the run is about before its kill points count: two turns, a limited free turn 1 and a succeeded turn 2, one accepted task, five starts, one resume prompt, two routed notices and a pin under `refs/orrery` (`build.crashUninterrupted`). Four windows must restart to the uninterrupted rows from each kill point inside them: a planner step's start to its turn record (`SetTurnStep`), a limit notice's route to its told mark (`MarkTold`), a tell notice's route to its told mark (`MarkTellTold`), and a resume prompt to its cleared limit (`resume clear`). Each window is a subtest that fails when a kill point inside it fails (`TestCrashPointRestartsToTheUninterruptedRows`).

Summaries run when a session enters `working` (throttled) and when it becomes `done`.

Each pane transition to `waiting` or `done` makes a pane notice. [Notice routing](#notice-routing) sends it to the desk stream, to Web Push, to both, or to neither. The desk notice has these parts:

- On `waiting`, the body is the question.
- On `done`, the body is the latest summary. The notice is posted and pushed only once the `Stop`'s summary run has written the summary or failed, so the body is the summary of the turn the `done` ends, and not the summary before it. A `done` from a `StopFailure` hook does not wait (`cmd/orreryd.relay.paneNotice`, `cmd/orreryd.awaitSummary`, `summary.Summarizer.Request`).
- The title is `<project> · <session>:<window>`, as [Desk stream](#desk-stream) gives it.
- The line's `host` is the machine host name.

Headless session notices (`--sessions`) are routed the same way, with the title and body that [Headless sessions](#headless-sessions) gives. Epic notices are routed the same way, with the title and body that [Planner turns](#planner-turns) gives.

When a pane enters `done` while an attached client shows the pane's window and had input in the last 30 seconds, `orreryd` sets `@orrery_seen` on the pane to the current Unix time.

When the router sends a notice to the phone, `orreryd` posts it as an encrypted Web Push to every registered subscription. Section 12 defines the payload. The subscriptions are kept in the push store:

- `$XDG_STATE_HOME/orrery/push.json` when `XDG_STATE_HOME` is set.
- Otherwise `$HOME/.local/state/orrery/push.json`.

`orreryd` keeps, per pane, per session, per epic, per usage limit and per alert, the time of the last notice it sent. With the store open, each kept time is also stored (see [Pushed notices in the store](#pushed-notices-in-the-store)), so it outlives a restart: a seen write after the restart clears the push as it would without the restart. Without the store, kept times live in memory only. A seen write made while `orreryd` is down clears nothing. When a seen write sets a seen time at or after that time, `orreryd` sends one clear push for that pane, session, epic, usage limit or alert and forgets the kept time. Seen writes are `orrery seen` (both forms), the tree's switch and mark-read keys, autoseen, the `Panes.MarkSeen` call of [Phone API](#phone-api), the `Seen` call of the session socket, and the attention loop's write for an epic that needs nothing (see [Planner turns](#planner-turns)). `orrery seen`, the tree and `Panes.MarkSeen` send `orreryd` a seen report (section 9) with the time they wrote; autoseen and the session `Seen` call run inside `orreryd`. The clear payload is defined in [Clear payload](#clear-payload).

When neither variable is set, `orreryd` exits 1.

`orreryd` also keeps the store, which mirrors each pane's tmux options (see [Store](#store)). It opens the store after its sockets listen, and logs `store open` (info, with `path`) once the store is open and migrated. When the store cannot be opened, `orreryd` logs the error and serves hooks and notifications without it. It then mints no `@orrery_id` and syncs no pane. A failure of the push store still exits 1.

`SIGINT` and `SIGTERM` stop `orreryd`. With `--sessions`, the Pools broker closes first (see [Pools](#pools)), then the session socket closes. Then every live headless session is stopped, and a session whose turn is running is recorded `interrupted`. With `--holder-scope`, each live session is detached instead, and nothing is recorded for it (see [Lifecycle rules](#lifecycle-rules)). `orreryd` exits after those notices are routed, and after each pending notice is kept or ended, as [Notice routing](#notice-routing) says at shutdown. The desk stream closes last, after the notices that post to it. See [Headless sessions](#headless-sessions) and [Desk stream](#desk-stream).

#### Notice routing

`orreryd` routes each pane notice, a transition to `waiting` or `done`, each headless session notice (see [Session notifications](#session-notifications)), each epic notice (see [Planner turns](#planner-turns)), each usage-limit notice (see [Step lifecycle](#step-lifecycle), Usage limit), and each alert notice (see [Alert intake](#alert-intake)). A notice stays pending until one of the rules below ends it. The rules apply in this order, and each event writes one line to the [notice log](#notice-log).

1. **Background work.** A pane's `done` whose `Stop` hook has a `background_tasks` holding a subagent or workflow goes nowhere. It is posted to neither the desk stream nor Web Push, and the log records a silence with reason `background work`. A hook whose `background_tasks` does not decode is logged, and its notice routes by the rules below.
2. **Watching.** A pane notice is held while its pane is watched. The pane is watched when either of these holds:
   - a tmux client of the pane's server shows the pane's window and had input in the last 30 seconds;
   - a read report for the pane, sent by `serve` (see [orrery serve](#orrery-serve)), arrived in the last 30 seconds.

   A held notice goes to neither sink. The first hold writes a line with reason `watching`, and orreryd checks the watch again every 5 seconds. A failed check reads as not watched. A session notice, an epic notice and an alert notice are never watched, so they are never held.

**Done judge.** A pane's `done` that rule 1 does not silence is judged, with or without `--sessions`, unless it comes from a `StopFailure` hook (see **StopFailure** below). The first time its pane is found unwatched, the notice waits 10 seconds, sending nothing and writing no line. A change, a seen write or a newer notice in that time ends it as rule 7 says. Shutdown keeps it or ends it as rule 7 says. Each later check is made as rule 2 makes it: a watched pane holds the notice with no judge call, and an unwatched pane is judged at once. The judge is Haiku. It reads the `last_assistant_message` of the `Stop` hook's input (section 9), runs through the pane's own `ANTHROPIC_BASE_URL` (section 9) with the summary model that section 8 gives its patchbay context, and answers `needed` (true or false) with a `why`. It must answer within 15 seconds, or it is late. A judge that errors fails, and so does a notice with no or an empty `last_assistant_message`. Waiting notices (questions and permission prompts), session notices, epic notices, usage-limit notices and alert notices are never judged.

- **Unneeded.** The judge answers not needed, with no error and no late answer. At once, whether or not the desk is at hand, the desk stream gets the notice as a quiet notice and Web Push gets it as a silent notice. Rule 5 applies to the phone part. Nothing escalates the notice, and it ends with that line. Its route names the sinks it reached, as for `away`, and its reason is `unneeded`, with `: <why>` when the judge gave a why. A desk notice that cannot be built is not posted, and the reason gains `; desk notice failed` as rule 6 says, but the silent phone notice is still sent.
- **Needed, failed or late.** The notice routes by rules 3 to 6, and its reason gains `needed` (with its why), `judge failed: <error>` or `judge late`, whichever applies (see the [clauses](#notice-log)).

3. **At the desk.** The desk is at hand when the newest web input is within the last 2 minutes. That input is the newest one the page reported through its input reports (see [orrery serve](#orrery-serve)). Whether a desk link is open does not matter: a web page's input alone makes the desk at hand. The phone app never counts, since it reports no input. Terminal input does not count, so a needed notice while the owner types only in a terminal and the web view is idle is away, and reaches the phone at once. The notice then goes to the desk stream, which posts it to each open desk link, and it shows in-app on every open web page (see [In-app notices](#in-app-notices)). Its route is `desk` and its reason is `at the desk`, and it stays pending. If it is still unseen and unchanged 5 minutes later, it goes to the phone, with route `phone` and reason `unseen after 5 minutes`, and ends.
4. **Away.** Otherwise the notice goes to the desk stream and to the phone at once, with route `desk+phone` and reason `away`.
5. **Push subscription.** The phone gets a notice only when the push store lists at least one subscription. Without one, the phone part is dropped: the route names what remains, and the reason gains `; no push subscription`. An escalation with no subscription ends the notice with route `none` and reason `unseen after 5 minutes; no push subscription`. A failed subscription check reads as a subscription, so the push is attempted.
6. **Desk notice failure.** A desk notice that cannot be built is not posted. The notice then routes as away, and its reason gains `; desk notice failed` before any `; no push subscription`.
7. **Ending.** A newer notice for the same pane, session or epic ends the pending one, and so does a pane's or a session's transition to a state other than `waiting` or `done`. Each ends with route `none` and reason `changed`. A seen write at or after the notice's time ends it with `none` and `seen`, and the seen write still clears any notice already sent to the phone (see [orreryd](#orreryd)). At shutdown, a pending pane notice or headless session notice is kept when the store is open and its row is stored. Otherwise it ends with `none` and `shutdown`, and is not sent (see Restart below).

**StopFailure.** A pane's `done` from a `StopFailure` hook is never judged. Its desk body and its push summary are `API error: <error>`, where `<error>` is the hook's `error` field, or `unknown` when that field is empty, absent or not a string (`cmd/orreryd.relay.stopFailureText`). Like any pane notice, it is held while its pane is watched. Otherwise it routes by rules 3 to 6 as soon as it is decided (`cmd/orreryd.relay.paneNotice`; `route.Router.decide`).

**Restart.** With the store open, a pane notice, a headless session notice, an epic notice or a usage-limit notice still pending at shutdown is kept: no line is written and nothing is sent. One whose row could not be stored, even after shutdown retries the failed row writes, ends `shutdown` instead. At start, once the store opens and before the headless sessions start, `orreryd` stores a row for each pending notice that has a source, then restores each stored row by its phase (see [Pending notices in the store](#pending-notices-in-the-store)):

- `deciding` is decided anew.
- `held` checks the watch at once.
- `delayed` is judged when its judge delay runs out. A notice whose delay was served is never delayed again.
- `desk` is not posted again, is shown on open web pages again at once, and escalates when its 5 minutes run out (see [In-app notices](#in-app-notices)).
- A row whose `due` has passed runs at once.
- `sending` is pushed again, with route `phone` and reason `resent after restart`. It is silent if its push was silent. It is not posted to the desk stream, and its row is deleted when that publish returns. It is not pending again: a seen write or a newer notice after the restore does not end it, and a seen write at or after the resend still clears the resent push on the phone. A notice for its key recorded before the restore, or a stored row for its key with a higher `id`, ends it `changed` with no resend, as for other rows.

Before a pane notice resumes, in any phase, `orreryd` reads its pane from the pane's tmux server once. A pane that is gone, either absent from its server or on a server that is not running, ends the notice with route `none` and reason `changed`. A pane whose state differs from the notice's state ends it the same way. A pane whose seen time is at or after the notice's time ends it with `none` and `seen`. Each of these sends nothing and writes one line. Any other tmux read failure is a restore error, as below. An epic notice and a usage-limit notice have no pane, so no tmux read is made for them: each is restored from its stored data alone, and its restore fails only when that data does not decode.

Apart from `sending`, a restored notice keeps its original time, so a seen write at or after that time ends it with `none` and `seen`. A newer notice for the same pane, session or epic ends it with `none` and `changed`, whether the newer notice is recorded before or after the restore. The sessions' start can record an `interrupted` notice that does this. A row that cannot be restored writes a line with route `none` and reason `not restored: <error>`, and the row is deleted. A stop during a publish can push the notice once more, and the phone alerts again for a resent waiting or failed push. A kill between a push publish and its sent record can resend that push once. The push payload carries no id a phone could drop (`push.Notification`), so this window is accepted. A publish that finishes after shutdown still deletes its row, so a clean stop resends nothing. For the kept push times, see [Pushed notices in the store](#pushed-notices-in-the-store).

### Headless sessions

With `--sessions`, `orreryd` runs headless Claude Code sessions. Each session is one `claude` process in stream-json mode with `-p`, `--permission-mode bypassPermissions` and `--permission-prompt-tool stdio`. It starts through the login shell `--session-shell`, in the directory its row names, with `orreryd`'s environment. The environment drops `TMUX`, `TMUX_PANE`, `ORRERY_SUMMARIZER` and `ORRERY_SESSION`, and then gains `ORRERY_SESSION` set to the session id. It has no `NOTIFY_SOCKET`, which `orreryd` removes from its own environment at start (`cmd/orreryd.run`). A session id is 128 random bits in lowercase hex.

Each session's process starts under its holder (see [Holder](#holder)). `orreryd` runs `orrery holder --session ID --state-dir DIR -- SHELL ARGS...` in the session's directory and environment (`holder.launchIn`). `SHELL` is `--session-shell`, `ARGS` is the argument vector that `session.Command` builds to exec `claude` (`session.ShellArgs`, `session.Command`), and `DIR` is the directory that holds the store file, so the session's holder files are in `DIR/holders/ID` (`holder.holderCommand`, `cmd/orreryd.startSessions`). The `orrery` it runs is the one beside `orreryd`, or the one on `PATH` when none is beside it (`holder.defaultOrrery`).

With `--holder-scope PATH`, the program is `PATH`, run with `--user --scope --collect -p OOMPolicy=continue --unit orrery-hold-ID` ahead of the same `orrery holder` argument vector, so the holder runs in its own user scope. Without the flag, the program is `orrery` itself (`holder.holderCommand`). `orreryd` waits up to 30 seconds (`LaunchTimeout`, `holder.LaunchTimeout`) for the holder to serve its socket, then attaches at offset 0 (`holder.launchIn`). A holder that ends first fails the start with its exit status and its stderr (`holder.endedBeforeServing`). So a `systemd-run` failure fails the start with its reason, and no plain holder starts in its place (`holder.Launch`, `session.Start`).

`orreryd` reads the session's stdout from the holder's `out` frames (see [Wire protocol](#wire-protocol)). After a read that returns bytes while the stream runs, it acks two offsets. `consumed` is the stdout the read has returned. `replayFrom` is the offset that `SetReplayFrom` last allowed, and the pump allows one only once the line that sets it is stored (see [Engine sessions in the store](#engine-sessions-in-the-store)). Neither offset goes below the attached frame's, and `replayFrom` never goes above `consumed` (`holder.Process.readStdout`, `holder.Process.ackReplayFrom`, `holder.Process.SetReplayFrom`; `enginehost.Manager.recordLine`).

`orreryd` writes stdin only as `write` frames. Each write carries the id of its kind, which the session sets before the write, and a write returns once the holder has written its data (`session.Session.write`, `holder.Process.writeStdin`). A repeat of an id the holder has recorded is answered as a duplicate, and nothing is written (`holder.holder.write`). The ids are these:

- `initialize`, fixed, for the initialize request. Re-adoption sends no initialize (`session.initializeRequestID`, `session.initializeWriteID`, `session.New`).
- A prompt's id is its stored `write_id`, a random version-4 UUID. The prompt is sent under that id as the `uuid` of its user message, so a turn's result that answers the prompt lists the id. The prompt is stored with its event before it is sent, for `Start`, `Say` and a step's first prompt (`enginehost.Manager.start`, `enginehost.Manager.send`, `enginehost.newWriteID`; `enginehost.Manager.StartStep`; `session.Session.Prompt`; `transport.Transport.SendUserMessageWithUUID`, `transport.RandomUUID`).
- A host's answer to a control request is `response:` and the request id. When that passes 256 bytes it is `response:sha256:` and the hex SHA-256 of the request id (`session.responseWriteID`).
- A write the session makes for an event, a refusal of a request, or a step's denial is `event:`, the line's start offset, and the event's index in the line (`session.Session.observeLine`, `session.Session.observeRequest`, `session.Session.DenyEvent`).
- An interrupt and a permission-mode change take a random `orrery-` id, which is also their request id (`session.Session.Interrupt`, `session.newRequestID`; `session.Session.SetPermissionMode`).

Each session has one row and an event log in the store (see [Engine sessions in the store](#engine-sessions-in-the-store)). Changes to a session go through the Engine service on the session socket, and the session's requests are answered through it.

#### States

A session is in one of these states. `working`, `waiting` and `done` are also pane states. `stopped`, `failed` and `interrupted` are for sessions only.

| State | Set when |
|---|---|
| `working` | the session starts, a prompt is sent, an answer is accepted, the session withdraws its open request, or claude opens a turn by itself while no turn runs, as when a background task's end starts one: the first event that only a running turn gives (its init, its content, a tool call, an assistant error or a request) records `working` in that event's write, and the owner is told the session changed (`enginehost.opensTurn`, `enginehost.Manager.noteEvent`, `enginehost.recorder.turnOpened`) |
| `waiting` | the session sends a request the host must answer. A refused request leaves the state as it was |
| `done` | a turn ends with every prompt the session sent answered and no send under way. A turn that was interrupted with `Interrupt` also ends `done`. A turn end after which a prompt is still unanswered, as one sent during the turn is, or while a send is under way, keeps the session `working`: claude runs that prompt as the next turn. Its `engine_turn_ended` is stored, but no notice is sent for it and a step's observer is not handed it (`enginehost.Manager.noteEvent`, `enginehost.recorder.followedTurnEnd`; `session.TurnEnded.Unanswered`) |
| `stopped` | the `Stop` call ends the process, or the process exits without an error |
| `failed` | the process exits with an error, its directory is unusable, its process cannot start, or its prompt cannot be sent. The row's `reason` says why |
| `interrupted` | `orreryd` shuts down during a turn without `--holder-scope`, or reconcile settles a `working` or `waiting` row at start whose session is not re-adopted. A `working` row that reconcile resumes is `working` again (see [Reconcile at start](#lifecycle-rules)). The row's `reason` says which |

Idle stop writes no state, so a session it stops keeps the state it had.

#### Session control socket

The session socket is `sessions.sock` in the same directory as the daemon socket. `orreryd` creates it only with `--sessions`, and its mode is `0600`. It serves the `orrery.engine.v1.Engine` service (`proto/orrery/engine/v1/engine.proto`), the `orrery.engine.v1.Steps` service (`proto/orrery/engine/v1/step.proto`, see [Steps](#steps)), the `orrery.engine.v1.Epics` service (`proto/orrery/engine/v1/epic.proto`, see [Epics](#epics)), the `orrery.engine.v1.Evidence` service (`proto/orrery/engine/v1/epic.proto`, see [Evidence](#evidence)), the `orrery.engine.v1.Pools` service (`proto/orrery/engine/v1/pool.proto`, see [Pools](#pools)) and the `orrery.engine.v1.Notices` service (`proto/orrery/engine/v1/notice.proto`, see [Notices](#notices)) over Connect.

| Call | Request | Precondition |
|---|---|---|
| `Start` | `dir`, `prompt`, `model`, `id` | `dir` is an absolute path to an existing directory, `prompt` is not empty, `model` is empty or 1 to 64 bytes of letters, digits and `. _ - [ ] :` not starting with `-`, `id` is empty or 32 lowercase hex digits, and the session cap has room. An `id` that has a row returns that id and starts nothing, unless the attempt for that `id` failed after storing its row, which returns that attempt's error and starts nothing while `orreryd` runs. Returns `id` |
| `Say` | `id`, `text`, `prompt_id` | the session has a row, and `text` is not empty. A `prompt_id` that an `engine_prompt` event of the session already carries makes the call succeed and send nothing, and a `prompt_id` whose send failed returns an error carrying that failure and sends nothing, also after a restart. A `prompt_id` whose attempt failed in another way after storing the prompt returns that attempt's error and sends nothing while `orreryd` runs. A session that is not live is resumed from its row first |
| `Answer` | `id`, `request_id`, `answer` | the session is live. `answer` is one of the open question's option labels, or `allow` or `deny` for a permission request |
| `Interrupt` | `id` | the session is live |
| `Stop` | `id` | the session is live |
| `Seen` | `id` | the session has a row |
| `Resume` | `id` | the session has a row in state `interrupted` and a Claude session. The call sends `Resume working.` as the session's next prompt, through `Say`, so the turn continues on that Claude session |

A call that does not fit its precondition does not change the session. An `Answer` that fits neither the question's options nor `allow` or `deny` leaves the request open.

`Say`, `Answer`, `Interrupt`, `Stop` and `Resume` refuse an `id` that names a step, before their own checks. The refusal is `failed_precondition` with the message `<id> is a step: drive it through the Steps service`. A step lookup that fails is `internal`. With no step store, the check is skipped (`enginehost.service.refuseStep`, `enginehost.service.Say`, `enginehost.service.Answer`, `enginehost.service.Interrupt`, `enginehost.service.Stop`, `enginehost.service.Resume`). `Resume` is refused as `failed_precondition` when its row is not `interrupted`, or has no Claude session; the message names which, as `resume session <id>: session cannot be resumed: its state is <state>, not interrupted` or `resume session <id>: session cannot be resumed: it has no Claude session` (`enginehost.Manager.Resume`, `enginehost.ErrNotResumable`). A step runs its own loop, so a raw session command on it would drive the step around that loop (`enginehost.service.ready`, and see [Steps](#steps)).

Errors are Connect codes:

| Connect code | When |
|---|---|
| `unavailable` | `orreryd` has no store, so the service has no manager. The message is `session store unavailable` |
| `resource_exhausted` | a `Start`, or a `Say` that resumes a session, would pass the session cap |
| `not_found` | `id` has no row, or `Answer`, `Interrupt` or `Stop` names a session that is not live. Also an `Answer` whose `request_id` is not open |
| `invalid_argument` | a bad `dir`, an empty `prompt` or `text`, a `model` or `id` that does not fit its rule, or an answer that does not fit the open request |
| `failed_precondition` | `Say`, `Answer`, `Interrupt`, `Stop` or `Resume` names a step id (see the paragraph above the table), or `Resume` names a session that is not `interrupted` or has no Claude session |
| `internal` | any other failure, including a call made while `orreryd` is shutting down |

#### Lifecycle rules

- A live session is one with a running process. Its slot under the cap is held from its start, resume or re-adoption until its process ends. A `Start` or resume past `--session-cap` is refused before anything is recorded. A re-adopted session takes its slot without the cap check, since its process already runs (`enginehost.Manager.readopt`). A step that [Reconcile at start](#lifecycle-rules) drained at its hand-off keeps its step slot after its process ends, until its next `Continue` takes that slot back or the step is stopped, released or ends (`enginehost.Manager.retireLocked`, `enginehost.Manager.stopDrained`, `enginehost.Manager.dropKeptLocked`; `enginehost.Manager.Release`).
- `Start` checks an `id`'s form, then answers with an existing row's id without any other check, unless the attempt for that `id` failed after storing its row, which returns that attempt's error while `orreryd` runs. Otherwise it checks the directory, prompt and model before it writes anything. It records the row `working`, starts the process, records the prompt, and sends the prompt. If the process cannot start, or the prompt cannot be sent, the row is recorded `failed` with the reason, and `Start` returns the id with no error unless recording that failure fails.
- `Say` to a session that is not live resumes it from its row. The process resumes the row's Claude session when the row has one, and otherwise starts a new conversation in the row's directory. The resumed process runs claude's default model, whatever `model` its `Start` named. Concurrent calls for one id start one process.
- `Say` with a `prompt_id` that an `engine_prompt` event of the session already carries returns success before it resumes or sends anything. A `prompt_id` whose send failed returns an error carrying that failure, and sends nothing, also after a restart, because the failure is stored in `engine_prompt_failed` (see [Engine sessions in the store](#engine-sessions-in-the-store)). A `prompt_id` whose attempt failed in another way after it stored the prompt returns that attempt's error, and sends nothing, while `orreryd` runs. An empty `text` is refused before that check. A row whose directory is missing or is not a directory is recorded `failed` with the reason.
- Idle stop: every 30 seconds, a session that has not been busy for 10 minutes is stopped. A session is busy while a turn runs, a request is open, or a background task runs. The stop writes nothing, so the row and its events stay as they were. The next `Say` resumes the session. A session that [Reconcile at start](#lifecycle-rules) drained is stopped in the same way, with nothing recorded, once it runs no turn, request or background task and no prompt sent to it, or owed to it by its re-adoption, is unanswered (`enginehost.Manager.retireLocked`).
- Stop signal: stopping a session's process sends its holder a `stop` frame on a new connection. The dial, the hello and the wait for the answer share one deadline of `holderStopTimeout`, 10 seconds (`holder.stopOverSocket`, `holder.holderStopTimeout`). The holder's child gets `SIGTERM` to its process group, then `SIGKILL` if it has not exited after 5 seconds (see [Child](#child)). When that stop fails, the holder's own process is stopped instead (`holder.Process.stopHolder`). Once the holder's `exit` frame has arrived the child has ended, and the stop sends nothing and only waits for the holder to end (`holder.Process.Stop`, `holder.Process.stop`; `holder.Child.Stop`, `holder.DefaultGrace`).
- `Stop` records `stopped` unless the process's exit has already recorded a state. A process that exits without an error records `stopped`.
- Failed on death: a process that exits with an error records `failed`. The reason is the exit error and stream error, followed by the last non-blank line of its stderr, and the owner is notified. The exit of a process stopped for idle stop or shutdown records nothing itself.
- Shutdown: when `orreryd` receives `SIGINT` or `SIGTERM`, the session socket closes first. Then each live session is stopped, and a session whose turn is running is recorded `interrupted` with the reason `orreryd stopped during the turn` (`enginehost.Manager.Close`, `enginehost.Manager.recordClosed`). With `--holder-scope`, each live session is detached instead: its holder and child keep running, and nothing is recorded for it, so its row keeps the state and unfinished text its events gave it (`enginehost.Manager.Close`, `enginehost.Manager.recordClosed`). The next start's [Reconcile at start](#lifecycle-rules) re-adopts that holder. A step's session and its gate follow the same scope, as [Shutdown](#step-lifecycle) gives. These writes share one 5-second limit (`enginehost.Manager.Close`), and each interrupted session notifies its owner. The notices are routed before `orreryd` exits. A session notice still pending then is not sent: with the store open and its row stored it is kept for the next start, and otherwise it ends `shutdown` (see [Notice routing](#notice-routing)).
- Reconcile at start: before the session socket serves, `orreryd` goes through each holder under the state directory's `holders` subdirectory whose session has no pump in this manager (`enginehost.holdersSubdir`, `enginehost.Manager.reconcileHolders`). A session whose pump in this manager still runs, live or retired with its pump not yet ended, is left to that pump: Reconcile neither re-adopts, drains nor clears its holder, nor settles or resumes its row, since that pump records the session's state itself (`enginehost.Manager.Reconcile`, `enginehost.Manager.pendingRows`, `enginehost.Manager.reconcileHolders`, `enginehost.Manager.pumping`). A holder is re-adopted when its session's row is one that a launch has tracked, meaning its `consumed` offset is stored, and that row is neither `stopped` nor `failed`. The row is an engine row, or a step row whose run [Steps](#step-lifecycle) registered with the manager before this pass. The pass reads the row of each holder it finds on its own, and reads all the rows once, after the pass, to settle them (`enginehost.Manager.reconcileHolders`, `enginehost.Manager.readoptable`; `store.Store.EngineSession`, `store.Store.EngineSessions`). Re-adoption attaches the holder at the row's `replay_from`, replays its stdout to the row's `consumed` offset with writes off, and registers the session live (`enginehost.Manager.readopt`, `session.Readopt`, `enginehost.Manager.register`, `enginehost.Manager.prime`). A re-adopted `working` or `waiting` row has a running turn, and a re-adopted `waiting` row with no open request goes `working` with no question. Each stored prompt whose write id the holder has not recorded is sent once, in order (`enginehost.Manager.sendUnwritten`). A holder one wire version older than the controller's is re-adopted as well, and its session drains. It runs as a live session until no turn, request or background task runs, no send is pending, and every prompt sent to it, or owed to it, has been answered by a turn's result. A re-adopted session owes every prompt the previous controller wrote to its holder that no later stored turn end answers, less the unwritten ones that Reconcile sends above, which it counts as its own sends. A turn end answers a prompt when its `answered` column lists the prompt's write id, and a turn end whose `answered` column is `NULL`, stored before that column existed, answers every earlier prompt (see [Engine sessions in the store](#engine-sessions-in-the-store)). So a prompt sent before the restart is answered on the old holder before a draining session stops (`enginehost.Manager.readopt`, `enginehost.Manager.owedPrompts`; `store.Store.HolderPromptsUnanswered`; `internal/store/queries.sql: CountHolderPromptsUnanswered`; `session.ReadoptOptions.Owed`, `session.unansweredPrompts.answer`). That is checked at re-adoption, at each turn's end and at each send's end. Then it is retired: it leaves the live set, and its process is stopped with nothing recorded, since `noteEvent` ignores the exit of a retired session (`enginehost.Manager.readopt`; `enginehost.Manager.register`, `enginehost.Manager.endSend`, `enginehost.Manager.noteEvent`, `enginehost.Manager.retireLocked`). A background task still running at a turn's end keeps the session until a later turn ends with none, or until the idle stop takes it. The next `Say`, or `Continue` for a step, waits until the retired holder's pump has ended and its stop has returned, which is once its holder has ended and its directory is removed, then resumes the row's Claude session with `--resume` in a new holder (`enginehost.Manager.resumeSession`, `enginehost.Manager.awaitStopping`, `enginehost.Manager.resume`, `enginehost.Manager.stopRetired`). A draining step is retired when its turn's end hands it over with no background task listed, and it keeps its step slot for its id (`enginehost.Manager.noteEvent`, `enginehost.Manager.retireLocked`). Its next `Continue` respawns it with `--resume` through that slot, with no cap check, and a stop, release or end of the step frees the slot (`enginehost.Manager.respawnStep`, `enginehost.Manager.stopDrained`, `enginehost.Manager.dropKeptLocked`; `enginehost.Manager.reserveStep`). A holder two versions older than the controller's, or one newer, is refused at attach (`holder.Conn.Attach`) and is handled as a live holder that cannot be attached, below. A holder whose session lock is free has ended. It is drained: its stdout from `replay_from` to `consumed` is replayed with nothing stored, each later line is recorded as the pump records it, and a child that the pids record shows still running is killed first. Its exit is recorded only when its exit record's reason is `exited`, and the row is then settled by the drain (`holder.reattach`, `enginehost.Manager.drain`, `holder.StopOrphan`). A live holder that cannot be attached is stopped first, then drained the same way (`enginehost.Manager.readoptHolder`, `enginehost.Manager.stopThenDrain`). A holder that gave no answer in time, its dial, hello or attach running out of the attach's 5-second bound, is not asked again: its holder and then its child are sent `SIGKILL` from its pids record, once `/proc` confirms each recorded start, and the stop returns once its session lock is free (`holder.AttachTimeout`, `holder.ErrNoAnswer`, `holder.Kill`). Any other holder that cannot be attached is asked to stop on its socket, and is killed the same way when it does not answer (`holder.Stop`). A step's holder is never drained or stopped by this pass. One that has ended or cannot be attached is left for the clear (`enginehost.Manager.readoptHolder`). One that gave no answer is killed first, as above, so the clear finds it ended and asks it nothing (`enginehost.Manager.killUnanswered`, `holder.Kill`). Each holder with no pump that was not re-adopted is then cleared: a holder still serving its socket is stopped, and its directory is emptied and removed once its lock is free. That covers a `stopped` or `failed` row, a step row that no [Steps](#step-lifecycle) run registered, an untracked row, and a holder with no row (`enginehost.Manager.reconcileHolders`, `enginehost.Manager.readoptable`, `holder.Clear`). Re-adoption, the stop of an unattachable holder and each clear are bounded by 10 seconds, and a failure is logged while the pass goes on (`enginehost.clearTimeout`, `enginehost.Manager.readoptHolder`, `enginehost.Manager.stopThenDrain`, `enginehost.Manager.clearHolder`). Then each engine row with no pump in this manager that has unfinished text or a running turn is settled, and a row with neither is not written (`enginehost.Manager.pendingRows`). Settling removes the unfinished text. A row that is `working` or `waiting` is recorded `interrupted` with the reason `orreryd restarted during the turn`, its question is cleared, and its owner is notified once that row's write is stored, unless it is resumed. A `working` engine row whose session has a Claude id, and whose last prompt is not `Resume working.`, is resumed instead: `Say` sends it `Resume working.`, and its owner gets one notice, `orreryd restarted during the turn; resumed it`, in place of the interruption's. The row is `working` again and keeps the reason `orreryd restarted during the turn`. A `waiting` row, a row with no Claude id, a row whose last prompt is already `Resume working.`, and a resume that the session cap refuses stay `interrupted`, with the reason and the notice above. A resume whose spawn or send fails is recorded `failed` with its own notice (`enginehost.restartedReason`, `enginehost.resumePrompt`, `enginehost.resumedBody`, `enginehost.Manager.resumeOrNotify`, `enginehost.Manager.resumable`, `enginehost.Manager.resumeInterrupted`). The settles are queued together and stored in one store transaction (`enginehost.Manager.Reconcile`, `enginehost.Manager.pendingRows`, `enginehost.settleOps`; `store.Store.ApplyEngine`), and the notices follow the stored writes (`enginehost.Manager.notifySettled`). A drained row is settled by its drain, and resumed or notified the same way (`enginehost.Manager.reconcileRow`). A step row is settled the same way, with no notice (`enginehost.Manager.resumeOrNotify`). Other rows keep their state, and no row is removed. A failed read of the session rows still ends the pass. A failed read of the unfinished-text set does not: each row with no live session is then settled, as if it held text. A dropped write, whose row is gone, does not stop the other rows: they are settled, and the first error is returned. `orreryd` logs it and serves anyway (`enginehost.Manager.Reconcile`, `enginehost.Manager.notifySettled`; `enginehost.Writer.flush`; `cmd/orreryd.sessionHost.reconcile`).
- A turn that `Interrupt` stops ends `done` with no notice. `Interrupt` does not stop the process.
- `reason` keeps its last value. A later state does not clear it, so a session resumed after a failure shows the old reason until a new failure or interruption sets another.

#### Session notifications

A session notice is routed when a session enters `waiting`, `done`, `failed` or `interrupted`. None is routed for `stopped`, for a refused request, for an idle stop, or for the end of an interrupted turn. A step session sends no notice in any state, except the usage-limit notice of a limited step (see [Step lifecycle](#step-lifecycle), Usage limit) and the interrupted notice of a step that a restart ends with no epic (see [Step lifecycle](#step-lifecycle), Restart). A notice for `waiting`, `done`, or a process that exits with an error is routed once its event is queued for the store. A notice for `interrupted`, or for a `failed` start, resume or prompt, is routed after its write is stored. Routing runs in the background, and shutdown waits for it. A session notice follows the rules of [Notice routing](#notice-routing), except that it is never watched and never judged.

On the desk stream (see [Desk stream](#desk-stream)), the line's `state` is the session's state, and its `title` is `<project> · session <first 8 characters of the id>`, where `project` is the base name of the directory. The body is:

| State | Body |
|---|---|
| `waiting` | the question, or `waiting for input` when there is none |
| `done` | the text of the turn's last main-conversation text block, or `done` when there is none |
| `failed` | the reason |
| `interrupted` | the reason, or `orreryd restarted during the turn; resumed it` when reconcile resumes the turn (see [Reconcile at start](#lifecycle-rules)), or `orreryd restarted during the step` for a step that a restart ends with no epic (see [Step lifecycle](#step-lifecycle), Restart) |

When the router sends the notice to the phone, Web Push goes to every subscription, with the payload of [Push payload](#push-payload). The payload's `session` key holds the full session id. `pane` and `label` are `""`. `project` is the base name of the directory. `state` is the session's state. The body is `question` when the state is `waiting`, and `summary` in every other case.

A session notice clears on the session's own seen: the `Seen` call for that session clears it, as [orreryd](#orreryd) describes.

#### Steps

`orreryd` serves the `orrery.engine.v1.Steps` service (`proto/orrery/engine/v1/step.proto`) on the session socket, beside `Engine`, with the same socket mode and Connect rules. `serve` does not serve it. `serve` calls its `ListSteps` and `StepStatus` to read steps, and forwards `HoldStep`, `NudgeStep` and `StopStep` to it for the phone's `orrery.v1.Steps` calls (see [Epics and Steps](#epics-and-steps)). `serve` never calls `StartStep` or `WaitSteps`. A step is an agent loop that ends with a checked status. It runs as one claude process of kind `step` (see [Steps in the store](#steps-in-the-store)), and [Step lifecycle](#step-lifecycle) gives its rules.

| Call | Request | Response | Effect |
|---|---|---|---|
| `StartStep` | `agent`, `prompt`, `schema`, `workspace`, `revision`, `read_only`, `owned_paths`, `gate_name`, `gates_file`, `done_value`, `model_limit_seconds`, `epic`, `task` | `id` | checks the request, starts the step and returns its id |
| `StopStep` | `id` | none | ends the step as `stopped`, whether it is held or running |
| `StepStatus` | `id` | `step` | returns the step's row |
| `WaitSteps` | `ids`, `timeout_seconds` | `ended`, `steps` | blocks until the first named step ends or the timeout passes |
| `ListSteps` | `epic`, `task` | `steps` | lists the labelled steps, oldest first, narrowed to `epic` and to `task` when each is not empty |
| `HoldStep` | `id` | none | interrupts the step's running turn and holds the step (see [Step lifecycle](#step-lifecycle)) |
| `NudgeStep` | `id`, `text` | none | sends the owner's text to the step (see [Step lifecycle](#step-lifecycle)) |
| `ResumeStep` | `id` | none | resumes a step that a usage limit holds, by sending the owner's resume prompt (see [Step lifecycle](#step-lifecycle), Usage limit) |

`StartStep` checks each field as follows:

- `agent` is 1 to 64 bytes of letters, digits and `. _ - [ ] :`, not starting with `-`. Empty is refused, unless the start names an agent gate, whose agent the gate supplies (see `gate_name`). The agent must also resolve, as claude resolves `--agent` for the step. The check runs before claude starts, and a name that no place below defines is refused with an error that names the agent and each place checked (`internal/agents`, `Resolve` and `ErrUnresolved`). A named agent gate's agent gets the same check (`enginehost.Manager.StartStep`, `enginehost.Manager.resolveStepAgent`). The inline agents of `StepStart.Agents`, which is Go-only, resolve without a file read, except `web-fetch` (`enginehost.inlineAgents`, `enginehost.Manager.resolveStepAgent`).

  The built-in agents are `claude`, `Explore`, `general-purpose`, `Plan` and `statusline-setup`. `web-fetch` never resolves. The managed agents directory is `/etc/claude-code/.claude/agents`. The project agents are the `.claude/agents` directories of the step's directory and of each directory above it, up to its git work-tree root and never above `HOME`. The user config directory is `$CLAUDE_CONFIG_DIR` when that is set, else `$HOME/.claude`, read from the step's environment, and the user agents directory is `agents` in it. Each agents directory is scanned for `.md` files recursively, following symbolic links to directories and to files. A missing directory is skipped. Any other read error refuses the start, and the error is named. A user, project or managed file defines its agent by its frontmatter `name` and needs a `description` too. A name that contains `:` or starts with `-` defines nothing. Names match exactly, case included.

  The last place is the agents of enabled plugins. A plugin counts when `<config>/plugins/installed_plugins.json` lists it with scope user or managed; an install at project or local scope does not count. It is enabled when `enabledPlugins` is `true` in the highest-precedence settings file that names it, in this order: managed `/etc/claude-code/managed-settings.json`, project-local `.claude/settings.local.json` (at the main checkout's root for a linked worktree), project `.claude/settings.json`, and `settings.json` in the user config directory. A plugin's agent is named `<plugin>:<subfolders>:<name>`, from its frontmatter `name`, or from its filename when the file has no parsable `name`. A bare name matches a scoped name that ends in `:<name>`.
- `prompt` is not empty.
- `schema` is a JSON object schema. Its root `type` is `object`, its `properties` include `status` with type `string`, and every subschema uses only the keywords `type`, `enum`, `required`, `properties`, `items` and `additionalProperties`, or the annotations `title`, `description`, `$schema`, `default` and `examples`. A start that names an agent gate leaves `schema` empty and takes the gate's verdict schema, which this check also covers (`enginehost.Steps.resolveStart`).
- `workspace` is an absolute path to an existing directory.
- `revision` is resolved to a commit when the step starts. Empty means `HEAD`.
- `read_only` false runs a writing step in `workspace`. The directory must be inside a linked git worktree. A directory outside any git work tree is refused, and so is one inside a repository's main working tree.
- `read_only` true runs the step in a detached worktree at its start commit. The worktree is added under `step-worktrees` beside the store file (see [Store](#store)), named by the step id, and removed when the step ends.
- `owned_paths` are paths relative to the repository. An empty path, an absolute path, or one that leaves the repository is refused.
- `gate_name` names the gate the step's return is checked against. Empty runs no gate. Otherwise the name is resolved once, when the step starts, as [Gate files](#gate-files) gives: from `gates_file` first when it is set, then from the project file for `workspace`. A name that neither file defines is refused. The step takes the gate's argv, limit and needs as its definition gives them then (`enginehost.Steps.Start`, `enginehost.Steps.resolveStepGate`, `enginehost.stepGateSourceData`, `enginehost.Steps.dataCommandGate`, `enginehost.dataAgentGate`, `enginehost.stepGrant`).
- A `gate_name` that names an agent gate (see [Agent gates](#agent-gates)) runs no argv. The start must be read-only and must leave `agent`, `schema` and `done_value` empty, or it is refused with `invalid_argument`. The step then takes the gate's agent, the verdict schema, the done value `done`, and as its first prompt the gate's prompt built from the caller's `prompt` (`enginehost.agentGateStart`). The gate's agent is checked as `agent` is above. The row stores the gate's name, source and limit, and no argv and no needs. A read-only step's done return runs no commit checks and no gate, so no argv runs (`enginehost.stepRun.judge`). A read-only start also records its checkout's state before the agent runs, and a start whose state cannot be read is refused (see **Checks** under [Step lifecycle](#step-lifecycle); `enginehost.Steps.addReadOnlyStep`).
- `gates_file` is the absolute path of the epic's gates file. Empty reads only the project file. It is refused when it is not absolute, when `gate_name` is empty, and when the file is missing or does not parse (`enginehost.Steps.resolveStepGate`).
- Each pool a gate needs must be declared by a pool file for `workspace`, or the start is refused (`enginehost.stepGrant`, `enginehost.agentGateStart`, `enginehost.stepNeeds`).
- `gate` and `gate_limit_seconds`, fields 8 and 9 of the request, are refused with `invalid_argument`. A step names its gate by `gate_name` (`enginehost.stepService.StartStep`).
- `done_value` empty means `done`. A start that names an agent gate leaves it empty, and the gate's done value is `done`.
- `model_limit_seconds` of zero means 60 minutes. It is the model time the step may use before the watch ends it `overtime` (see the Watch rule under [Step lifecycle](#step-lifecycle)).
- `epic` and `task` are stored as given, and nothing checks them. The format that `proto/orrery/engine/v1/step.proto: StartStepRequest` names for `epic` is not enforced (`enginehost.stepService.StartStep`, `enginehost.Steps.Start`, `store.StepAdd.applyEngine`). An empty `epic` leaves the step unlabelled, and `ListSteps` never lists it (`enginehost.StepStart`, `internal/store/queries.sql: ListLabelledSteps`). An empty `task` means the step works on no one task.

A start refused by one of these checks, by the step cap, or by the workspace rules stores nothing and returns no id. A start that fails after its row is stored ends the row `failed`. `StartStep` then returns the error with no id (`enginehost.stepService.StartStep`); only the in-process `Steps.Start`, which the planner calls, returns the id with the error (`enginehost.Steps.Start`).

`StepStatus`, `WaitSteps` and `ListSteps` return each step as a `Step` message: `id`, `agent`, `workspace`, `revision` (the resolved start commit), `read_only`, `worktree` (empty for a writing step), `end` (empty while the step runs), `status`, `result` (the latest return's JSON text), `checks`, `reason`, `started`, `ended` (unix seconds; `ended` is 0 while the step runs), `doing`, the latest doing line from the step's monitor, `epic` and `task`, the labels the step was started with; `stage` and `stage_started`, the stage the step's loop is in and the unix seconds it started, which are empty and `0` for a row that stores none; `gate_name`, the name the step's gate was resolved by; `gate_limit_seconds`, the gate's limit in whole seconds, which is 1800 for a step that names no gate; `verdicts`, each `StepVerdict` as the loop recorded it, with `round`, `kind` (`monitor` or `judge`), `verdict` (empty when the call failed) and `detail`; `held`, which is true while an owner hold holds the step; and `limited`, `limit_kind` and `limit_resets`, which report the usage-limit hold of the step: whether a limit holds it, the kind of that limit, and its reset in unix seconds, which is 0 when the reset is unknown (`enginehost.stepMessage`; `proto/orrery/engine/v1/step.proto: Step`). Each `checks` entry is a `StepCheck`: `round`, `name`, `ok`, `detail`, `stdout` and `stderr`, the redacted tails that a gate check keeps, passing or failed, and `log`, the file a failed gate check's output was kept in (see **Gate** in [Step lifecycle](#step-lifecycle)), empty for any other check. No other check keeps tails (`enginehost.stepMessage`). The `step_status` tool of the step MCP server shows each check with the same `stdout`, `stderr` and `log`, each left out when empty (`stepmcp.checkJSON`). The row's prompt, schema, done value, owned paths, gate, gate source, gate needs and model limit are stored (see [Steps in the store](#steps-in-the-store)) but not returned. `held` is loop state and has no column: it is read from the step's loop, and a step with no loop is not held (`enginehost.stepRun.resumeWith`).

`WaitSteps` returns every named row in the order `ids` gives. `ended` is the named step that ended first, by its end time, and a tie keeps the step listed first. `ended` is unset when the timeout passes before any of them ends. A timeout of zero returns at once. An id with no row fails the whole call with `not_found`.

`ListSteps` returns the rows whose `epic` is not empty, narrowed to rows whose `epic` is the request's `epic` when that is not empty, and to rows whose `task` is the request's `task` when that is not empty. They are ordered by start time, then id (`internal/store/queries.sql: UpdateStepReturn`, `store.Store.LabelledSteps`).

`HoldStep` and `NudgeStep` return no fields. Their rules are in [Step lifecycle](#step-lifecycle).

The step tools of `orrery mcp` call these calls and retry them (`internal/stepmcp/tools.go`). `step_start` retries `StartStep` only when no connection was made, for up to 60 seconds, so a start cut mid-call is never sent twice (`stepmcp.startHandler`). `step_status`, `step_stop` and `step_resume` retry an `Unavailable` answer for up to 60 seconds (`stepmcp.statusHandler`, `stepmcp.idCallHandler`, `stepmcp.stopHandler`, `stepmcp.resumeHandler`). Unlike `orrery step resume`, `step_resume` does not count a retry's `step is not limited` answer as a resume, so a cut attempt that resumed the step makes the tool answer that refusal (`stepmcp.idCallHandler`, `stepmcp.resumeHandler`; `cmd/orrery.resumeStep`). `step_wait` calls `WaitSteps` again after an `Unavailable` answer for as long as its own timeout has left, and asks each time for the remaining seconds, rounded up (`stepmcp.waitSteps`, `stepmcp.roundUpSeconds`). The waits between attempts grow from 100 ms to 5 s (`rpcretry.initialBackoff`, `rpcretry.backoffGrowth`, `rpcretry.maxBackoff`, `rpcretry.Do`).

Errors are Connect codes:

| Connect code | When |
|---|---|
| `unavailable` | `orreryd` has no step store. The message is `step store unavailable` |
| `resource_exhausted` | a `StartStep` would pass the step cap (`--step-cap`), or a `ResumeStep` would pass it when the resume respawns the step's process; the step stays limited (`enginehost.stepCommandError`; `enginehost.stepRun.resumeWith`) |
| `not_found` | `StopStep`, `StepStatus`, `WaitSteps`, `HoldStep`, `NudgeStep` or `ResumeStep` names an id with no row |
| `invalid_argument` | an empty `prompt`, a bad `schema`, a bad `agent`, an `agent` that does not resolve (the error names the agent and each place checked), a `workspace` that is not an absolute existing directory, a writing step's `workspace` outside a git work tree or in its main working tree, an `owned_paths` entry that is not a relative path inside the repository, a `gate` or `gate_limit_seconds` field, a `gate_name` that no file defines, a `gate_name` that names an agent gate on a writing step or with an `agent`, `schema` or `done_value` set, a `gates_file` that is not absolute, is set without `gate_name`, is missing or does not parse, a gate need for a pool that no pool file declares, or an empty `text` on `NudgeStep` (`enginehost.stepCommandError`) |
| `failed_precondition` | `HoldStep` finds the step already held or no turn running, or `HoldStep` or `NudgeStep` finds the step running its gate, ended, or with no loop, or `NudgeStep` finds no turn running and the step not held, or `ResumeStep` finds a step that no limit holds, or a step that has ended or has no loop, or a step running its gate (`enginehost.stepCommandError`; `enginehost.stepRun.resumeLimited`, `enginehost.Steps.Resume`, `enginehost.Steps.AutoResume`, `enginehost.Steps.runOf`) |
| `internal` | any other failure, including a `revision` that does not resolve (for a read-only step this includes a `workspace` outside any git work tree), a `WaitSteps` call whose context ends first, a `HoldStep`, `NudgeStep` or `ResumeStep` call whose context ends before the step's loop takes it, and a `ResumeStep` whose send fails for another reason, which ends the step `failed` (`enginehost.stepRun.resumeWith`) |

#### Step lifecycle

- **Launch.** A step's process starts through `--session-shell` as a session does (see [Headless sessions](#headless-sessions)). Its arguments are these, with `<agent>` and `<schema>` from the request:

  ```
  <session-shell> -l -c 'exec "$@"' orrery-session <claude> -p --input-format stream-json --output-format stream-json --include-partial-messages --verbose --permission-prompt-tool stdio --permission-mode bypassPermissions --agent <agent> --strict-mcp-config --json-schema <schema>
  ```

  No `--model` is passed, so the agent's model runs. When the step has inline agents, `--agents` with their JSON comes before `--agent` (`session.Command`, `enginehost.Manager.spawn`). A respawn adds `--resume` with the row's claude session id and passes the row's inline agents again (`enginehost.Manager.respawnStep`). The working directory is `workspace`, or the step's worktree for a read-only step. The environment is the session environment, with `ORRERY_SESSION` set to the step id.

  When claude emits its init event, its `agents` list must hold the step's agent, either as that name or as the part after `:` of a scoped entry. A `web-fetch` agent always fails this check, and so does a missing list. A step that fails it stops and ends `failed`, with a reason that names the agent. The check catches the claude settings that the resolution check does not read, and drift at a claude upgrade.
- **Kind and silence.** The row's `kind` is `step`. The typed `List` and the tidy read only pane rows, so step rows are never listed. A step sends no notice in any state, on the desk stream or Web Push, except the usage-limit notice of a limited step (see Usage limit below). Its `engine_*` events are stored and pruned like other engine events, and `orrery.v1.Events.Watch` sends them as their engine members, as it does for engine sessions. A step takes a slot under `--step-cap`, not `--session-cap`.
- **Requests.** A question or permission request that the session did not refuse is denied at once with `The orrery host does not answer questions in a step. Do not ask; give your answer as the structured return.` The request never opens, so the row does not become `waiting`.
- **Returns.** A return is the `structured_output` of a turn's result. It is taken from a turn that ends with no background task listed, and from a turn that ends while a background task is listed only when its return's `status` equals `done_value`. Any other turn that ends while a background task is listed is not a return, and the step keeps waiting, so a not-done return stays unread for as long as a task is listed (`enginehost.Manager.noteEvent`; `enginehost.stepRun.Done`; `enginehost.StepObserver.Done`). After a return the step is busy until `Continue` sends its next prompt or the step stops. A result with no structured output has no value, and fails the schema check.
- **Checks.** Each return is a round, numbered from 1, and its checks are stored with that round in the row's `checks`. The checks run in this order:
  1. `schema`: the value matches the step's schema. The failure names the first failing path, such as `$.status`.
  2. `checkout`: a read-only step only, on every return, whether or not `schema` passed. It compares the checkout with the state the step started in, which is HEAD and a SHA-256 of `git status` and `git diff HEAD`, with git-ignored files in neither, and it fails when the checkout moved. Its detail names the move: `HEAD moved from A to B` when HEAD moved, then `changed paths:` with the paths the commits since the start changed and the paths that differ from HEAD now, at most 20 and then a count; or `working tree differs from the state it started in` when HEAD did not move and no path can be named. A git error reading the checkout fails the check, with the error as its detail. The check is stored only when it fails. A failed one ends the step `checks_failed` at once, with the return stored, nothing sent back and no judge call. A read-only step with no recorded state, which a step started before this check has, is not compared, and a restart keeps the state a step recorded (`enginehost.stepRun.judge`, `enginehost.stepRun.checkoutMove`, `enginehost.stepRun.endCheckoutMoved`; `enginehost.stepGitCheckout`, `enginehost.stepGitCheckoutCheck`, `enginehost.stepGitCheckoutMove`; `enginehost.Steps.readoptRun`).
  3. For a writing step whose return passes `schema` and whose `status` equals `done_value`, four commit checks run, in this order: `head` (the return's commit is the top-level `commit` string when the return names one, and HEAD must be that commit; when the return names none (missing, null, empty, or non-string), the return's commit is the workspace's HEAD if HEAD has moved past the start commit, and a return naming none while HEAD is the start commit fails `head`), `ancestry` (the start commit is an ancestor of HEAD), `clean` (no tracked change, staged or unstaged, and no untracked file that is not ignored), and `owned` (every path changed from the start commit to the return's commit is in `owned_paths` by exact match, with both sides of a rename checked). A writing step started unowned, which a release step is, runs the first three alone and no `owned` check, since it may change any path (`enginehost.commitChecksWithoutOwned`, `enginehost.stepRun.doneChecks`; `enginehost.StepStart.Unowned`).
  4. `gate`: runs only when all four commit checks pass and the step has a gate.
  5. `assertions`: runs only for an agent gate step, on every return that passes `schema`, whatever its `status`. The return's `results` must hold one entry per assertion of the gate, in the gate's order, each with the assertion's text as `assertion` and its kind as `kind`, and each `fails` result must have a finding whose `citation` is that text. The failure details are `results has N entries for M assertions`, `result I is for assertion "R", want "A"`, `result I of assertion "A" has kind "K", want "J"` and `result I fails assertion "A" and no finding cites it`. Here N is the number of results, M the number of assertions, I the result's place counted from 1, R the result's assertion text, A the assertion's text, K the result's kind and J the assertion's kind. A value that does not decode as a verdict fails with `read verdict: ` and the decoder's error (`enginehost.stepRun.assertionsCheck`, `enginehost.assertionsMismatch`, `enginehost.citesAssertion`, `enginehost.stepRun.valueChecks`).
  6. `return`: runs only for a step with a return check (`StepStart.ReturnCheck`, which only an in-process caller sets), on every return that passes `schema`, whatever its `status`. Its detail is the check's failure lines joined with `; `, and it passes when there are none (`enginehost.stepRun.valueChecks`, `enginehost.stepLoopReturnRecord`; `enginehost.StepStart`, `enginehost.StepStart.ReturnCheck`).

  A read-only step, a return that fails `schema`, or a return whose `status` is not `done_value`, has the `schema` check alone, except that an agent gate step's return that passes `schema` also has `assertions`, and a step with a return check adds `return` to each return that passes `schema`.
- **Gate.** A writing step's gate runs only on a done return whose commit checks all pass. It runs in the step's workspace, with its argv and no shell, and it holds the pool members its needs name. It queues through `orreryd`'s Pools service, runs once its grant holds those members, and keeps the grant until its process group has exited, or until its holder ends when a shutdown detaches the gate (see [Shutdown](#step-lifecycle)). Its limit counts from the grant, not from the queue. It runs with `orreryd`'s environment, minus `TMUX` and `TMUX_PANE`. Each attempt runs under its own holder: `orrery holder --gate-limit` with the session `<step id>-gate`, in the holder directory `holders/<session>` under the gate state directory, which `orreryd` sets to `gates` under the state directory (`cmd/orreryd.startSessions`; `enginehost.stepRun.runDoneGate`, `enginehost.planGate`, `enginehost.gateStateDir`). The holder's child is a gate (see [Holder](#holder)). The attempt's request names that directory, so the grant lasts with the holder as well as with `orreryd` (`enginehost.gateAttempt`; see [Pools](#pools)). Before the gate launches, the row stores the grant, the holder directory and the round in `gate_grant`, `gate_holder` and `gate_round`. The return that judges the gate clears them (`enginehost.stepRun.runDoneGate`; `internal/store/queries.sql: UpdateStepReturn`; see [Steps in the store](#steps-in-the-store)). A hold placed on one of its members (`orrery hold`) stops the gate. Its process group is killed, that run records nothing, and the grant is requeued. The gate runs again from its start once the broker grants it again. A requeued request waits like any other, and a held member is not granted until it is freed (`enginehost.runGrantedGate`, `enginehost.gateWatch`, `enginehost.gateLeftRunning`; `grants.Broker.hold`). The gate passes when it exits 0 without passing its limit. When the limit passes, its holder stops it as a stop does: `SIGTERM` to its process group, then `SIGKILL` once the grace has passed (see [Holder](#holder); `holder.Child.limitAfter`, `holder.Child.Stop`), and the check fails with `gate ran past its limit of <limit> and was killed`. The check's detail opens with the record `gate <name> from <source> ran <argv as a JSON array> holding <POOL:MEMBER,...>; `, then gives `gate exited <status>` or the limit text. The gate keeps the newest 1 MiB of each of its stdout and stderr (`enginehost.stepGateLogTail`), redacted (see **Redaction** below). The check keeps the newest 8 KiB of each as its `stdout` and `stderr` tails, from the start of the first whole line in those bytes, or of the first whole character when they hold no line break; the tails are not in the detail (`enginehost.stepGateRecord`, `enginehost.stepLoopGateCheck`, `enginehost.stepCheckTail`, `enginehost.stepGateCheckTail`). A failed gate check also keeps the whole of both kept streams in a log that outlives the gate's holder, whose directory is cleared once the gate's result is read: `logs/<step id>/round-<round>.log` under the gate state directory, which holds `== stdout ==` and the stdout, then `== stderr ==` and the stderr, each ending in a newline when it is not empty. The check's `log` names that file. A log that cannot be written leaves `log` empty and adds `; gate output not kept: <error>` to the detail. A passing gate check keeps no log, and the engine removes no log (`enginehost.stepRun.doneChecks`, `enginehost.stepRun.keepGateLog`, `enginehost.gateLogsName`). A gate that cannot start, or whose grant ends before or while it runs, is an error that ends the step `failed` (`enginehost.gateAttempt`, `enginehost.gateWatch`, `enginehost.gateGrantGone`).
  - **Re-adoption.** A restart re-adopts a gate that the row stores as running in its round (`enginehost.stepRun.runDoneGate`). If the gate's holder still serves, `orreryd` reclaims the grant by its id for a request that names that holder, and waits for the gate's end as an attempt does. Otherwise the holder's exit record is the outcome. An exit or a limit is the result, and nothing reruns. The broker may still keep the gate's grant for its ended holder until its next tick, so `orreryd` reclaims that grant by its id for a request naming the holder and ends it at once: the gate's members are free when the step's end is stored. A stop reruns the gate once on the grant the stop ended. A holder that cannot be attached but has written its record is read the same way. Any other outcome is lost: a reclaim that the broker refuses, a holder that ended with no record, or a record whose reason is neither an exit, a limit nor a stop. A lost gate ends its step `engine_restarted`, and its holder is ended and cleared (`enginehost.readoptGrantedGate`, `enginehost.readoptServed`, `enginehost.readoptEnded`, `enginehost.gateEndKept`, `enginehost.gateRerun`; `enginehost.stepRun.writingChecks`).
- **Judge.** A return that passes its checks and whose `status` equals `done_value` is judged by a Haiku call, except an agent gate step's return, which is never judged, and a return of a step with a return check, which ends the step `done` with no call (`enginehost.stepRun.judge`, `enginehost.stepRun.endDoneReturn`). Without a Haiku caller the step ends `done` with no call. The input is the step's first prompt and the return. A writing step adds the `git diff --stat` from the start commit to the return's commit, and a step with a gate adds the gate check's `stdout` and `stderr` tails, each under its own heading when it is not blank, without the check's record or exit status (`enginehost.stepJudgeInput`). The reply is `met` or `unmet` with a reason. `met` ends the step `done`. The first `unmet` sends the session back once with the reason, and the next return that passes its checks is judged again. A second `unmet` ends the step `goal_unmet`, and its return is kept. A failed diff stat, call or reply stores its error with no verdict and ends the step `done`. The judge's send-back is not one of the three send-backs.

  An agent gate step's return that passes its checks ends by its verdict, with no call. Its reasons are, in order: each result that is not `holds`, as `fails: A` or `undetermined: A` with A the assertion's text; then `status S` when the return's `status` S is not `done_value`; then `an earlier return held fails: A` when the return holds no `fails` result and an earlier return did. The reasons are joined with `; `. The step ends `done` with no reason, and `gate_failed` with them otherwise, and its return is kept. The first `fails` result of any schema-valid agent gate return is kept for the run, whether or not that return's check passed. A turn end an agent gate step takes as no round (held, owner nudge or watchdog nudge) also keeps the first `fails` result of its output when that output passes `schema`, though the output is not stored or checked, so no later return ends `done` (`enginehost.stepRun.assertionsCheck`, `enginehost.stepRun.latchFirstFails`, `enginehost.stepRun.latchNoRound`, `enginehost.stepRun.verdictReasons`, `enginehost.stepRun.endVerdict`, `enginehost.stepRun.turnEnded`, `enginehost.stepRun.judge`).
- **Watch.** An agent gate step also checks its gate's `limit` on each unheld tick, before the backstops and whether or not a turn runs. At the first tick at or past `limit` seconds of wall time from the run's start, or from its latest resume from a usage limit (see Usage limit below), the step ends `overtime` with the reason `agent gate ran past its limit of N seconds`, where N is the limit in seconds. No monitor call is made and no verdict is stored (`enginehost.stepRun.watchGateLimit`, `enginehost.newStepRun`, `enginehost.stepRun.tick`). A held tick is skipped before this check, as it is before the backstops. While a turn runs, the loop checks four backstops on each tick, the first one due in this order: `overtime` (model time reaches the step's model limit), `tool_stuck` (a main-conversation tool call open for 30 minutes of wall time, or 60 after its one extension), `silent` (10 minutes of model time with no event) and `periodic` (5 minutes of turn time since the turn began or the last verdict). Each trip makes one Haiku monitor call. Its verdict is `progressing`, `long_command`, `looping` or `wedged`, with a one-line `doing`, and a non-empty `doing` becomes the row's `doing`. The verdict is applied in this order: `overtime` ends the step `overtime`; `looping` or `wedged` nudges the step once, and then ends it with that verdict; a `long_command` on `tool_stuck` extends that call once, and any other verdict on `tool_stuck` ends the step `tool_stuck`; `silent` nudges once, and then ends the step `silent`; `periodic` does nothing. A nudge interrupts the running turn. The interrupted turn is no round: the step is continued with the nudge text instead of being checked, and an agent gate step keeps the first `fails` result of its output when that output passes `schema`. A nudge does not count toward the three send-backs. A failed monitor call stores its error with no verdict, and the trip acts without one: `overtime` and `tool_stuck` end the step, `silent` nudges on its first trip and ends on the next, and `periodic` does nothing. The monitor's argv, its cap and timeout, and the trip policy with its file and line citations are in [section 15 of the engine protocol](engine-protocol.md#15-the-step-watch-and-judge).
- **Send-back.** A return with a failed check is sent back by `Continue`, as a message that lists each failed check with its detail and, for a check that kept them, its `stdout` and `stderr` tails, each labelled (`enginehost.stepLoopFailures`). A failed `checkout` check is not sent back; it ends the step (see **Checks**). The step may be sent back three times. A fourth failing return ends the step `checks_failed`, with the same failure lines, tails included, as its reason (`enginehost.stepRun.retry`). Nudges and the judge's send-back are counted apart, and they are not among these three.
- **Redaction.** Gate and capture tails are redacted before anything stores, shows, judges or sends them. `internal/redact` replaces each credential of these shapes with `[redacted]` and leaves other bytes as they were: the value of an `Authorization`, `Proxy-Authorization` or `x-api-key` header written as a `Name: value` line, from the value to the end of the line, with the name kept; the value of a JSON-quoted `Authorization`, `Proxy-Authorization` or `x-api-key` member, its name quoted with the quote right after the name, where a quoted value is replaced up to its closing unescaped quote with both quotes kept, and any other value to the end of its line, with the name and its quotes kept; the value of an `aws_secret_access_key` assignment; an `sk-` key of 20 or more characters, `sk-ant-` keys included, unless a letter comes just before the `sk-`; a GitHub token with the prefix `ghp_`, `gho_`, `ghu_`, `ghs_` or `ghr_`, or the prefix `github_pat_`, followed by 20 or more letters, digits or underscores; an AWS access key id, `AKIA` or `ASIA` followed by 16 uppercase letters or digits and a word boundary; a Slack token, `xox`, a lowercase letter, `-`, and 10 or more letters, digits, underscores or hyphens; and a PEM or PGP private key block, replaced whole from its BEGIN line to its END line, or up to the cut at either end of a tail (`redact.Text`). A tail at least its capacity long is taken as cut at its start: a gate run's `Tail`, 8 KiB when it names none (`enginehost.gateTailDefault`), a step gate's 1 MiB (`enginehost.stepGateLogTail`), or a capture's 1 MiB (`enginehost.evidenceCaptureLogTail`). A step gate re-adopted after a restart is redacted as having the 8 KiB capacity, since a holder an older `orreryd` launched keeps that and its exit record does not say which it kept (`enginehost.stepRun.runDoneGate`). The text before its first newline is replaced by `[redacted]`, and a tail with no newline is `[redacted]` alone (`redact.CutTail`). The lost first line is the accepted cost: a credential cut in half at the start cannot be matched, so the whole line goes. The redaction runs where a gate's exit record becomes its result, on both tails (`enginehost.gateExitResult`, `enginehost.gateRedactTail`), and on every read of a capture's stdout or stderr log, which applies the cut form to a stored log of at least 1 MiB (`enginehost.Evidence.readLog`). Nothing else is redacted by the engine: not kept artifacts, transcripts, owner prompts or push text. `serve` redacts the epic text it reads, as [Epics and Steps](#epics-and-steps) gives it, and that redaction is on the read alone, so the stored text stays as the engine wrote it. A log stored before this release is redacted when it is read, but the judge reads the stored file, so that log reaches the judge as stored (`enginehost.judgePrompt`).
- **Ends.** Each step ends once, with one of these end kinds:

  | End | Set when | `status` |
  |---|---|---|
  | `done` | a return passes its checks, its `status` equals `done_value`, and the judge says `met`, fails to give a verdict, or is not run (always, for a step with a return check); or, for an agent gate step, a return passes its checks with no verdict reason (see Checks) | `done_value` |
  | `returned` | a return passes its checks and its `status` is another value, except an agent gate step's, which ends `gate_failed` | that value |
  | `checks_failed` | a fourth return fails a check, or a read-only step's return finds its checkout moved | empty |
  | `failed` | the process exits on its own (`the step's process exited` after a clean exit, otherwise the exit's reason), the first prompt cannot be sent, a return or verdict cannot be recorded, a gate cannot run or its grant ends before or while it runs, a `Continue` cannot be sent, a respawn whose agent no longer resolves, or a started step whose init agents list lacks its agent (see Launch and Restart) | empty |
  | `stopped` | `StopStep` ends the step | empty |
  | `engine_restarted` | `orreryd` starts with the step's row open and no loop for it, and the step is not re-adopted (see [Restart](#step-lifecycle)); or a re-adopted gate's outcome is lost (see [Gate](#step-lifecycle)); or the step runs an agent gate that a restart does not re-adopt (see [Restart](#step-lifecycle)) | empty |
  | `overtime` | the watch's `overtime` trip: model time reaches the step's model limit; or, for an agent gate step, wall time from the run's start, or from its latest resume from a usage limit, reaches its gate's `limit` (see Watch) | empty |
  | `tool_stuck` | the watch's `tool_stuck` trip, when its verdict is neither `long_command` nor `looping` or `wedged`, or its call has had its extension | empty |
  | `silent` | the watch's `silent` trip with a verdict other than `looping` or `wedged`, once the step has been nudged | empty |
  | `looping` | a `looping` verdict, once the step has been nudged | empty |
  | `wedged` | a `wedged` verdict, once the step has been nudged | empty |
  | `goal_unmet` | the judge's second `unmet` verdict; the return is kept | empty |
  | `gate_failed` | an agent gate step's return passes its checks and a verdict reason applies (see Judge): a result is `fails` or `undetermined`, the `status` is not `done_value`, or an earlier return held a `fails` result; the return is kept | the return's `status` |

  The end is stored with the reason, and the step's session is stopped and its worktree removed, whatever ended it.
- **Stage.** The loop is in one stage at a time: `turn`, `checks`, `gate`, `judge`, `held` or `limited`. The row stores the stage and the unix seconds it started, in `loop_stage` and `loop_stage_from` (see [Steps in the store](#steps-in-the-store)). A new step starts in `turn` at its start time (`store.StepAdd.applyEngine`). A stage the loop is already in keeps its start, so the start moves only on a change (`enginehost.stepRun.setStage`). The loop writes each stage at these points:
  - `turn` is stored before each prompt that continues the session: a send-back, a judge send-back, a nudge, or an owner's text to a held step. A usage-limit resume stores it in the write that clears the limit, after its `Continue` is sent (`enginehost.stepRun.continueStep`, `enginehost.stepRun.resumeWith`).
  - `checks` is stored before a return's checks run (`enginehost.stepRun.enterChecks`). The return that judges a gate stores it too, since the gate's end sets it (`enginehost.stepRun.doneChecks`, `enginehost.stepRun.commitReturn`).
  - `gate` is stored in the write that records the gate's grant, so a gate that waits in the queue leaves the stage at `checks`, with its start unchanged (`enginehost.stepRun.runDoneGate`, `enginehost.gateAttempt`).
  - `judge` is stored before the judge is called (`enginehost.stepRun.enterJudge`).
  - `held` is queued when an owner hold lands. The queue is a `Put` that does not wait for the store, so a failed put is logged and the next change stores the state again (`enginehost.stepRun.hold`, `enginehost.stepRun.saveLoop`).
  - `limited` is stored in a limited turn's write, before the step's process is released (`enginehost.stepRun.limitTurn`).

  A restart rebuilds the run with both columns, so each stage and its start are kept (`enginehost.stepRun.resume`, `enginehost.Steps.readoptRun`). A row added before migration `00032` has the stage `""` and the start `0` until the loop next stores a stage.
- **Stop.** `StopStep` cancels the step's loop, which kills a running gate, waits for the loop to return, and then stores `stopped` through the same end path. A step that has already ended is left as it is, and `StopStep` returns success. A held step ends `stopped` the same way (`enginehost.Steps.Stop`). Under any holder scope the cancel kills the gate: its holder is stopped, its process group is killed and its directory is cleared (`enginehost.gateWatch`, `enginehost.gateLeftRunning`, `enginehost.gateEnd`).
- **Hold.** `HoldStep` interrupts the step's running turn and marks the step held (`enginehost.stepRun.hold`, `enginehost.Steps.Hold`). It is refused with `failed_precondition` when the step is already held, when no turn is running, and when the step is running its gate (`enginehost.stepRun.hold`, `enginehost.stepRun.do`). A failed interrupt is returned as `internal`, and the step stays unheld. A landed hold drops any nudge pending for the turn, the watchdog's and the owner's (`enginehost.stepRun.hold`). A held step's loop skips each tick, so the watch checks nothing (`enginehost.stepRun.run`). A held step's turn end is no round, and it continues nothing, though an agent gate step keeps the first `fails` result of its output when that output passes `schema` (`enginehost.stepRun.turnEnded`).
- **Owner nudge.** `NudgeStep` sends the owner's text to the step (`enginehost.Steps.Nudge`). Empty text is `invalid_argument`, and it is refused before the step is looked up (`enginehost.Steps.Nudge`). It is refused with `failed_precondition` when the step is running its gate, and when the step is not held and no turn is running (`enginehost.stepRun.nudgeOwner`, `enginehost.stepRun.do`). On a held step, the text resumes the step at once when the held turn has already ended. Otherwise it waits for that turn's end, which is no round, though an agent gate step keeps the first `fails` result of its output when that output passes `schema`, and is continued then (`enginehost.stepRun.nudgeOwner`, `enginehost.stepRun.turnEnded`). On a running turn, the turn is interrupted, and its end is continued with any pending watchdog text followed by the owner's text, in place of a round, though an agent gate step keeps the first `fails` result of its output when that output passes `schema` (`enginehost.stepRun.nudgeOwner`, `enginehost.stepRun.turnEnded`). Owner text sent while a nudge is pending is joined to it, one line per text (`enginehost.stepRun.nudgeOwner`, `enginehost.joinNudges`). A nudge neither spends nor replaces the watchdog's nudge, and an OK reply means the nudge landed (`enginehost.Steps.Nudge`).
- **Usage limit.** A turn that ends on a usage limit limits its step (`enginehost.stepRun.limitTurn`).
  - Cause. The turn is limited when the store holds a rejected rate limit for the step that was stored at or after the step's newest prompt (`store.Store.StepRateLimit`; `internal/store/queries.sql: GetStepRateLimit`), or when the session handed the run an assistant message whose error is `rate_limit` since the previous turn end (`enginehost.stepRun.Event`, `enginehost.stepRun.limitTurn`; `session.assistantEvents`). The stored rate limit is the session's latest `rejected` rate limit event (`enginehost.recorder.ops`; see [Planner turns](#planner-turns), The kept rate limit).
  - No round. A limited turn is no round. The step is not checked, judged or sent back for it, and it counts toward none of the send-back, nudge or judge counts. The run drops its pending nudges and its hold (`enginehost.stepRun.turnEnded`, `enginehost.stepRun.limitTurn`).
  - Release. The run stores its loop state with the limit (`enginehost.stepRun.limitTurn`, `enginehost.stepRun.loopState`), then releases the step's process (`enginehost.stepRun.limitTurn`; `enginehost.Manager.Release`). Release stops the process and records nothing, and the observer is told of no exit (`enginehost.Manager.Release`; `enginehost.Manager.recordLine`, `enginehost.Manager.noteEvent`). The step's slot is freed when the process's pump ends (`enginehost.Manager.pump`).
  - Spends nothing. While the step is limited, its loop skips each tick, so the watch makes no trip, monitor call or nudge (`enginehost.stepRun.tick`). Its exits are ignored (`enginehost.stepRun.exited`), and its session receives nothing until a resume (`enginehost.stepRun.resumeLimited`, `enginehost.stepRun.autoResumeLimited`, `enginehost.stepRun.resumeWith`).
  - Open. The step stays open and limited through exits and restarts, and nothing ends it but a resume or a stop. A restart rebuilds the step's run from its row, restores it as limited, and does not handle the row's stored turn end again (`enginehost.stepRun.resume`; `enginehost.Steps.readoptRun`). `Steps.Reconcile` starts the loop of a limited row with no live process (`enginehost.Steps.Reconcile`). The step's holder has ended, so the restart clears its holder directory (`enginehost.Manager.reconcileHolders`, `enginehost.Manager.readoptHolder`).
  - Reset. A limit with a known reset stores the step's due row in the same write that records the limit. The row's id is the lowercase hex SHA-256 of `orrery-due`, the kind `step`, the step id and the reset in unix seconds, joined with NUL bytes, so one reset has one row. A limit with no known reset stores none, so the step waits for a command (`store.StepLoop.applyEngine`; `store.dueNamespace`, `store.dueKindStep`, `store.insertDueResume`, `store.dueID`). `StepEnd` closes the step's rows, so a limit that arrives after the end stores none (`store.StepEnd.applyEngine`). The due pass runs at start and then every `limitTick`, 5 seconds (`cmd/orreryd.brokerTick`, `cmd/orreryd.sessionHost.startLimits`, `cmd/orreryd.sessionHost.startIdle`, `cmd/orreryd.sessionHost.startDue`). A `due` row is resumed once the clock is at least 60 seconds past its reset, oldest reset first (`enginehost.dueGrace`, `enginehost.DuePass.Pass`). The row resumes its step through `Steps.AutoResume` with that reset (`enginehost.DuePass.resumeStep`; `enginehost.Steps.AutoResume`). A refusal by the step cap or the step's gate leaves the row `due` for the next pass. A step that is not limited at that reset, has ended, or has no row closes the row with no resume (`enginehost.DuePass.resumeStep`). A resume closes its step's rows in the write that clears the limit (see Resume below). The reset is reported on the step (`enginehost.stepMessage`), grouped and worded in the limit notice (`enginehost.LimitNotices.Run`, `enginehost.LimitNotices.update`), and worded in the planner's turn reason (`planner.Runner.settle`).
  - Resume. `Steps.Resume` runs in the step's loop (`enginehost.Steps.Resume`; `enginehost.stepRun.resumeLimited`). `resumeLimited` sends the owner's prompt "The usage limit has reset. Carry on with your task from where you stopped." with no PromptID (`enginehost.stepResumePrompt`, `enginehost.stepRun.resumeLimited`). The automatic resume sends the same text under the PromptID that `resumePromptID` derives from the step id and the reset (`enginehost.resumeIDNamespace`, `enginehost.resumePromptID`, `enginehost.stepRun.autoResumeLimited`). `Continue` returns nil without sending when an engine prompt of the step already carries that PromptID, so an automatic resume that was sent before a restart is not sent again (`enginehost.Manager.Continue`). Continue respawns the released process with `--resume` (`enginehost.Manager.Continue`, `enginehost.Manager.respawnStep`). Then the step's limit and its stored rate limit are cleared in one write, which also closes the step's due rows (`enginehost.stepRun.resumeWith`; `store.EngineRateLimitCleared.applyEngine`; `store.StepDueResumesClosed`, `store.StepDueResumesClosed.applyEngine`). An agent gate step's gate `limit` counts again from the resume, which that write stores in `loop_gate_from`, so the wait for the reset never ends the step `overtime`, and an evidence judge's resume spends no judge (`enginehost.stepRun.resumeWith`; `enginehost.stepRun.watchGateLimit`). A restart restores it from the row (`enginehost.stepRun.resume`).
  - Refusals. `ErrStepNotLimited` refuses a step that no limit holds (`enginehost.ErrStepNotLimited`, `enginehost.stepRun.resumeLimited`). `ErrStepNotRunning` refuses a step with no loop or one that has ended (`enginehost.Steps.runOf`). A Continue that the step cap refuses returns an error matching `ErrCapReached` and leaves the step limited (`enginehost.stepRun.resumeWith`). Any other Continue error ends the step `failed` and is returned (`enginehost.stepRun.resumeWith`). The Connect codes are those of [Steps](#steps) (`enginehost.stepCommandError`).
  - Stop. `StopStep` ends a limited step `stopped`, and finish stops no session for it, since its process was released (`enginehost.Steps.Stop`; `enginehost.stepRun.finish`).
  - Planner. A planner turn's limited step is stopped by its runner, as [Planner turns](#planner-turns) gives (`planner.Runner.await`).
  - Agent gate. An agent gate step is rebuilt at a restart only when the Steps' `ReadoptGate` rebuilds its gate, as Restart below gives. The evidence judge's `evidence_judge` gate is rebuilt from its run's stored item. A limited agent gate step that is rebuilt stays limited, and `Steps.Reconcile` starts its loop as it starts any limited row's. Any other agent gate step ends `engine_restarted` there (`enginehost.Steps.readoptRun`, `enginehost.Steps.readoptAgentGate`, `enginehost.Steps.Reconcile`; `enginehost.EvidenceReadoptGate`).
  - Limit notice. `LimitNotices` checks the limited step rows every `limitTick`, which is 5 seconds (`cmd/orreryd.limitTick`, `cmd/orreryd.sessionHost.startLimits`). It groups them by key, `<kind>@<reset>`, where `<reset>` is the reset in unix seconds, or `0` when the reset is unknown (`enginehost.limitGroups`, `enginehost.limitKeyOf`). A group is told as one notice when its step ids differ from its told mark, and the mark is then stored with those ids. The marks are read before the first check, so a limit told before a restart is not told again unless its step ids change (`enginehost.LimitNotices.check`, `enginehost.LimitNotices.readMarks`, `enginehost.LimitNotices.update`; `store.Store.TellLimit`). A limit with no mark, open at start, is told as any untold limit is (`enginehost.LimitNotices.update`). A marked limit with no open group is seen at the check's time, and its mark is forgotten, so a seen write clears its key (`enginehost.LimitNotices.update`; `route.Router.Changed`). The marks are described in [Told limits in the store](#told-limits-in-the-store).
  - The notice's desk body is `N steps held by the KIND limit, reset RESET: IDS`, where `N` is `1 step` or `N steps`, `RESET` is RFC 3339 in UTC or `unknown`, and `IDS` lists the step ids (`enginehost.limitBody`). Its desk state is `limited` and its title is `usage limit`. Its push has project `usage limit`, state `limited` and the same body as its summary (`enginehost.limitNotice`).
  - The notice is never watched and never judged, so it is routed by rules 3 to 7 of [Notice routing](#notice-routing) (`route.Router.finish`, `route.Router.decide`, `route.Router`). It has a stored row with the `limit` source, so a restart restores it and a shutdown keeps it (`enginehost.RestoreLimitNotice`; `route.Router.rowLocked`; `route.Router.Wait`). Its phone push and its clear carry the `limit` key (`push.clearPayload`, `push.boundedPayload`; see [Push payload](#push-payload)). A kept push for its key is stored in `pushed_limit_notices` and deleted by its clear (`notice.Notices.putRow`, `notice.Notices.deleteRow`; see [Pushed notices in the store](#pushed-notices-in-the-store)).
- **Shutdown.** `Steps.Close` cancels each loop and waits until none runs. Without a gate scope, the cancel kills a running gate, as Stop does. With one, which `orreryd` sets from `--holder-scope` (`cmd/orreryd.startSessions`), each loop is cancelled with `errGateDetach`. Its gate keeps running under its holder, and the step's row stays open for the next start's Restart. The grant stays with the gate, and the broker keeps it across a restart while its holder lives (see [Pools](#pools)) (`enginehost.Steps.cleanRestartedStep`, `enginehost.Steps.Close`, `enginehost.errGateDetach`, `enginehost.gateWatch`).
- **Restart.** At start, `orreryd` calls `Steps.Readopt` before the manager's Reconcile, and `Steps.Reconcile` after it (`cmd/orreryd.sessionHost.reconcile`). `Readopt` reads each open step row, and the turn end its session last stored after its last prompt, before any holder is re-attached. It rebuilds each row's run as `Start` builds one, restored from the row: the round, the continue count, the pending nudges, whether the step is held, whether the judge has sent it back, and whether its one nudge is spent. It restores the gate that the row stores as running, with its grant, holder and round. Each run is registered with the manager as the step's observer, so the manager re-adopts the step's holder when that holder is live and tracked. A row whose run cannot be rebuilt is logged and left unregistered (`enginehost.earliestEnded`, `enginehost.Steps.Readopt`, `enginehost.Steps.readoptRun`). A step that runs an agent gate is re-adopted only when the Steps' `ReadoptGate` rebuilds its gate: the agent gate of the row's gate name, with an agent (`enginehost.StepsConfig.GateScope`; `enginehost.stepRowRunsAgentGate`, `enginehost.Steps.readoptAgentGate`). `orreryd` sets `ReadoptGate` to the evidence readopt, which rebuilds an evidence judge step's `evidence_judge` gate from its run's stored item (`cmd/orreryd.startSessions`; `enginehost.EvidenceReadoptGate`; see [Evidence](#evidence)). A row whose `ReadoptGate` is unset, returns no gate or returns a gate of another name is left unregistered and ends `engine_restarted`, as is a row whose stored turn end is a return, since resume would take that return up as an ordinary step's and its verdict would never be read (`enginehost.Steps.readoptRun`, `enginehost.stepRowRunsAgentGate`, `enginehost.Steps.readoptAgentGate`). The first `fails` result the gate keeps restarts empty (`enginehost.Steps.readoptRun`). The manager settles each row it does not re-adopt as it settles other engine rows, with no notice for a step (see [Reconcile at start](#lifecycle-rules)). Then `Steps.Reconcile` starts the loop of each registered step that the manager reports live, and of each registered step whose row is limited, an agent gate step among them (`enginehost.Steps.Reconcile`). Each other open row with no loop ends `engine_restarted`. A step that a restart ends this way, with no epic, is then told as an interrupted session notice, with the body `orreryd restarted during the step`. A step that names an epic is not told (`enginehost.Steps.endRestarted`). The ends are queued together and stored in one store transaction. A dropped end, whose step row is gone, does not stop the rest: they are stored, and each stored row has its worktree removed and its gate holder ended and cleared. The dropped row is not cleaned up, and the first dropped end is returned (`enginehost.Steps.readoptAgentGate`, `enginehost.Steps.Reconcile`, `enginehost.Steps.endRestarted`; `enginehost.Writer.flush`). A re-adopted loop resumes from what it stored. A stored turn end that the latest return covers resumes that return: failed checks are sent back again, a return with another status ends `returned`, and a done return is judged again. Any other stored turn end is handled as one the session has just given. With no stored turn end, the watch counts the turn as running (`enginehost.stepRun.tick`, `enginehost.stepRun.resume`, `enginehost.stepRun.resumeReturn`, `enginehost.stepWatch.restore`). The loop stores its round, continue count, pending nudges, held flag, judge flag and nudged flag as each changes (`enginehost.stepRun.continueStep`). `Continue`, which the loop sends, respawns a step that is not live from its rows: it first waits for a drained holder of the step to end, with its directory removed (see [Reconcile at start](#lifecycle-rules)), then takes a step slot, the slot a drained step kept for its id with no cap check or else one under the step cap, and only then resumes the row's claude session with the same agent and schema flags. A respawn runs the `agent` rule of `StartStep` before claude starts. An agent that no longer resolves ends the step `failed` before claude starts, with a reason that names the agent and each place checked, and no claude process starts (`enginehost.Manager.Continue`, `enginehost.Manager.respawnStep`).


#### Epics

`orreryd` serves the `orrery.engine.v1.Epics` service (`proto/orrery/engine/v1/epic.proto`) on the session socket beside `Steps`, when it runs with `--sessions` (`enginehost.Serve`; `cmd/orreryd.serve`). Its epics are stored in orreryd's store (see [Epics in the store](#epics-in-the-store)). With no store open, the service has no store behind it, and every call answers `unavailable` (`cmd/orreryd.startSessions`; `enginehost.epicService.ready`). `serve` calls its `ListEpics` and `GetEpic` to merge the epics with the records, and `Epics.History` calls `GetEpic` with `history` set (see [Epics and Steps](#epics-and-steps); `server.EpicStoreClient`). `serve`'s `PauseEpic`, `ResumeEpic`, `StopEpic`, `RetryEpic` and `AnswerEpic` calls go to the client that `server.Config.EpicCommands` names, and it calls no other epic method (`server.epicsServer.forwardCommand`; `cmd/orrery.runServe`).

| Call | Request | Response | Effect |
|---|---|---|---|
| `CreateEpic` | `contract`, `decisions`, `workflow` | `epic` | checks the contract, pins the workflow it names at the base (see [Workflows](#workflows)), stores it accepted with its seed decisions and its `created` trigger, and returns the stored epic |
| `GetEpic` | `id`, `history` | `epic` | returns the stored epic with that id, with its summary, its planner state, its pinned workflow and its workflow node runs; with `history`, also its triggers and tells (see **GetEpic and ListEpics** below) |
| `ListEpics` | none | `epics` | returns a summary of each stored epic, ordered by `id` |
| `AnswerEpic` | `epic_id`, `id`, `text`, `question`, `choice` | `decision`, `warning` | records the answer as a decision on the epic: tied to the question `question` names, with the choice `choice` gives it, or a note when `question` is empty (see **AnswerEpic** below) |
| `PauseEpic` | `id` | `epic` | pauses the epic, so the planner starts no turn for it (see **Controls** below) |
| `ResumeEpic` | `id` | `epic`, `resumed`, `still_limited` | clears the pause of the epic, resumes the epic's limited task steps, and lists the steps resumed and the steps a full step cap left limited; a park stays |
| `StopEpic` | `id` | `epic` | pauses the epic, flags its running turn to stop, and stops that turn's step; with no running turn, an epic that holds its repository's release slot frees the slot and stops its open release step instead (see **Controls** below and [Release](#release)) |
| `RetryEpic` | `id`, `node` | `epic` | ends the park of the epic, answers its open park question `retry`, and grants each parked loop of the epic one more round; with `node`, reruns the parked run of that workflow node under its next attempt, which also ends the park and answers the open park question `retry` (see **Controls**) |
| `FollowEpics` | none | `follows` | returns, for each question label across all epics, how many answered questions had a recommendation and how many were answered with it (see **FollowEpics** below) |
| `ReleaseEpic` | `id`, `undo_none` | `epic` | resumes the stopped release of the epic at its first unfinished step, running again each undo-none step that `undo_none` names (see [Release](#release)) |

The calls are declared in `proto/orrery/engine/v1/epic.proto: Epics`, `proto/orrery/engine/v1/epic.proto: Evidence`, and implemented in `enginehost.epicService`.

**Contract.** `CreateEpic` checks the contract before it stores anything. The checks run in the order below, and the first one that fails is the refusal (`enginehost.checkEpic`; `enginehost.epicService.CreateEpic`). A refusal is `invalid_argument` with the message `<field>: <reason>`. The field is named by its proto name, and a row of a section by its zero-based index, as in `release_steps[0].undo: empty` (`enginehost.epicFieldError`, `enginehost.epicFieldError.Error`, `enginehost.epicRowField`). The checks, in order:

1. `contract: missing`, when the request has no contract (`enginehost.checkEpic`).
2. `id: bad form, want <repo-id>-<7 hex>/<slug>`, when the id does not match `^[A-Za-z0-9][A-Za-z0-9._-]*-[0-9a-f]{7}/[a-z0-9]+(-[a-z0-9]+)*$`. `id: over 128 bytes`, when it matches and is longer than 128 bytes (`enginehost.epicIDMaxBytes`, `enginehost.epicIDPattern`, `enginehost.checkEpicHead`).
3. `repository: not absolute`, when the path is not absolute. `repository: not inside a git work tree`, when `git rev-parse --is-inside-work-tree`, run in that directory, fails or does not print `true`. Any failure of the git call is this refusal (`enginehost.checkEpicRepository`; `enginehost.stepGit`).
4. `intent: empty`, when the intent is empty after trimming white space (`enginehost.checkEpicHead`).
5. `care: not limited, serious or severe`, unless the care is `limited`, `serious` or `severe` (`enginehost.checkEpicHead`).
6. `base: empty`, when the base is blank after trimming white space. `base: not a commit in the repository`, when `git rev-parse --verify --quiet --end-of-options <base>^{commit}`, run in the repository, fails. `CreateEpic` then stores the full commit that name resolves to (`enginehost.checkEpicBase`; `enginehost.epicService.CreateEpic`).
7. For each gate in order: `gates[i]: <reason>`, when `gates.Define` refuses the gate, with the reason it gives, under the same rules as a gates file's table (see [Gate files](#gate-files)). `gates[i].name: duplicate`, when an earlier gate has the name (`enginehost.checkEpicGates`; `gates.Define`).
8. For each done gate in order: `done_gates[i]: <reason>`, when the name resolves neither from the contract's gates nor from the repository's project file, or `done_gates[i]: not a command gate` when it resolves to an agent gate. A done gate's name resolves as [Gate files](#gate-files) gives. Then `review_gates: refused, the reviewers fill the review list`, when the contract names any review gate. The review list is filled from the reviewers, as [Reviewers](#reviewers) gives (`enginehost.checkEpicGateRefs`; `gates.ResolveDefined`).
9. For each failure in order: `failures[i].id: unknown id`, unless the id is `F` followed by a number with no leading zero, and `failures[i].id: duplicate` when an earlier failure has the id. Then `failures[i].rating: not limited, serious or severe`, unless the rating is one of the three levels (`enginehost.epicFailureIDPattern`, `enginehost.checkEpicFailures`, `enginehost.checkEpicRowID`).
10. `requirements: none`, when the contract has no requirements. For each requirement in order: `requirements[i].id` is refused as `failures[i].id` is, with `R` in place of `F` (`enginehost.epicRequirementIDPattern`, `enginehost.checkEpicRequirements`, `enginehost.checkEpicRowID`). Then `requirements[i].item: missing`, when the requirement names no evidence item. Otherwise the first line that `evidence.Check` gives for the item, named as `requirements[i].item.` and that line, such as `requirements[i].item.judge: empty or blank`. The kind and tests lines are `kind: none, name test, capture or supplied`, `kind: "<kind>" is not test, capture or supplied`, `kind: test without a capture`, `kind: capture without a capture`, `kind: supplied without supplied files`, `tests: none, a test item lists its test files`, `tests: set on a capture item` and `tests: set on a supplied item`, each named under the item as the other lines are (`evidence.kindLines`, `evidence.testsLines`). The form of an item is in [Evidence](#evidence) (`enginehost.checkEpicRequirementItem`; `evidence.Check`). A new epic's requirement that also names the legacy `evidence` or `kind` is refused with `requirements[i].evidence: set beside an item` or `requirements[i].kind: set beside an item` (`enginehost.checkEpicRequirementItem`).
11. For each premise in order: `premises[i].id` is refused as a failure's id is, with `P` in place of `F` (`enginehost.epicPremiseIDPattern`, `enginehost.checkEpicPremises`).
12. For each addition in order: `additions[i].location: unknown id`, when the location is an `F`, `R` or `P` id (`^[FPR][1-9][0-9]*$`) that names no failure, requirement or premise of the contract (`enginehost.epicRowIDPattern`, `enginehost.checkEpicAdditions`).
13. For each release step in order: `release_steps[i].undo: empty`, when it is empty after trimming (`enginehost.checkEpicReleaseSteps`).
14. For each seed decision in order: `decisions[i].id: empty`, or `decisions[i].id: duplicate` when an earlier decision has the id. Then `decisions[i].text: empty`, when the text is the empty string. The text is not trimmed (`enginehost.checkEpicDecisions`).

**Reviewers.** After the checks, `CreateEpic` merges the reviewers of three layers, orrery's defaults, the repository's project file and the contract's `reviewers`, and generates the conformance assertions from the contract's `exclusions` (see [Reviewers](#reviewers); `enginehost.epicReviewers`). A reviewer that no layer can hold is refused as `invalid_argument`, and so is an enabled reviewer the layers leave incomplete. The message is `read the reviewers of <repo>: <file>: reviewers[<index>] <name>: <key>: <reason>` when a project file's entry or syntax is refused as it is read, and `merge the reviewers of <repo>: <source>: reviewers[<index>] <name>: <key>: <reason>` otherwise. `<source>` is the project file, or `epic_create` for the contract's own entries, and `<index>` is the entry's position in its layer. The reasons are those of [Reviewers](#reviewers) (`enginehost.epicService.CreateEpic`; `reviewers.Error`). Nothing is stored then.

**Workflow.** After the checks, the reviewers and the base, `CreateEpic` pins the workflow that `workflow` names, or `epic` when it names none (`enginehost.epicService.CreateEpic`). The workflow is loaded as the base commit holds it. One that does not load there is refused as `invalid_argument` with the message `workflow: at base <commit>: <refusal>`, where `<commit>` is the full commit the base names and `<refusal>` is the refusal of [Workflows](#workflows). Nothing is stored then (`enginehost.pinWorkflow`).

A contract that passes is stored in one write, with its sections, its seed decisions, its pinned workflow and its `created` trigger (`store.Store.CreateEpic`). The trigger has the id `created` and the cause `epic created`, and it is `pending` (`store.Store.CreateEpic`, `store.createdTriggerID`, `store.createdTriggerCause`). Its `phase` is `accepted`, and its `created` and `changed` are both the time of the call. The response is the stored epic read back (`enginehost.epicService.CreateEpic`). An `id` that is already stored answers `already_exists`, and nothing is stored (`enginehost.epicService.CreateEpic`; `store.Store.CreateEpic`).

**Summary.** `CreateEpic` stores the epic's `summary` empty, and orreryd's summary loop writes it once. The loop runs when `orreryd` starts with its store open, and then at each `limitTick`, which is 5 seconds (`cmd/orreryd.startSessions`, `cmd/orreryd.sessionHost.startSummaries`, `cmd/orreryd.limitTick`, `enginehost.EpicSummaries.Run`). Each pass reads the epics whose summary is empty, in id order, and calls Haiku for each one that this start has not tried. A start tries each epic once, and the calls run one at a time (`enginehost.EpicSummaries.pass`, `enginehost.EpicSummaries.summarize`). A call runs the model `protocol.DefaultSummaryModel` as one structured `claude -p` turn, with `--json-schema` set to `enginehost.epicSummarySchema`, which requires a `summary` string. Its input is the instruction and the contract's text, as `enginehost.epicContractText` renders it (`enginehost.haikuArgv`, `enginehost.Haiku.call`, `enginehost.epicSummaryOf`). The reply's summary is trimmed. A call that fails, a reply that does not decode and a blank summary are each logged, and they leave the summary empty until a later start tries the epic again (`enginehost.EpicSummaries.summarize`, `enginehost.epicSummaryOf`). The write runs under a context that ignores the loop's cancellation, so a reply the call returned is stored as the loop stops (`enginehost.EpicSummaries.summarize`). The store keeps the first summary only, as [Epics in the store](#epics-in-the-store) gives (`store.Store.SetEpicSummary`).

**Epics stored before O3d2.** An epic stored before O3d2 stays readable. Its requirements keep their `evidence` and `kind` and carry no `item`, so `GetEpic` answers them with no item, and an evidence run of one of them is refused (`enginehost.requirementItemMessage`; `enginehost.evidenceRunRequirementItem`). Its tasks have no gate, and the migration gives each one the evidence `{"conclusion": <its old check>, "judge": ""}` (`internal/store/migrations/00020_evidence.sql: +goose Up`). Such a task has no judge, so no judge step starts for it. Nothing in O3d2 builds such an epic, and the build of O3d3 does not either, since it lists only epics with a base (`internal/store/queries.sql: ListBuildEpics`).

**Phase and changed.** The create sets `phase` to `accepted`. Later writes set it, and no other write does: `reviewing` when the end-of-epic review freezes (`store.Store.FreezeReview`), `correcting` when the review ends (`store.Store.EndReview`), `built` when a plan that holds an `integrate` node ends built while the phase is `accepted` (`store.Store.EndPlanBuilt`, `store.endAndBuild`), `clean` or `ended_with_gaps` when the conclude node ends the epic (`store.Store.EndEpic`, through `interpret.Ending`), `releasing` when the epic takes the release slot from `clean` and when `ReleaseEpic` resumes it (`store.Store.TakeReleaseSlot`, `store.Store.ReleaseEpic`), `stopped` when a release stop ends it (`store.Store.StopRelease`), `released` or `done` when the release node ends the epic (`store.Store.EndEpic`, through `interpret.Ending`; see [Release](#release)), and `met_at_base` when an evidence node at base parks the epic for good (`store.Store.ParkNodeFinal`). All use `store.setEpicPhase` (`internal/store/queries.sql: SetEpicPhase`). The ended phases are `met_at_base`, `clean`, `ended_with_gaps`, `released`, `done` and `stopped`, and every other phase is not ended (`kinds.Ended`). A task's build runs while its epic has a base and its phase is not ended (`kinds.Build.Decide`). `changed` is the time of the epic's last write: its creation, or its latest answer (`internal/store/queries.sql: UpdateEpicChanged`; `store.appendDecision`). The planner's writes and the controls do not move `changed`. They change the planner tables and the epic's `paused` and `parked` columns (`store.Store.FreeTurn`, `store.setParked`; `store.Store.PauseEpic`, `store.Store.ResumeEpic`, `store.Store.ResumeDueEpic`, `store.Store.CloseDueResume`, `store.Store.StopEpic`, `store.Store.RetryEpic`, `store.Store.RetryNode`). The interpreter's writes change the node runs, their checks and their triggers, the integrate writes also change the epic's bases and each task's batch, the review writes change the review's rows and the phase, and the release writes change the release runs, the slot and the phase (see [Epics in the store](#epics-in-the-store)). A loop's park or a node's park sets `parked`, and so does a release stop, which also adds its reason as a tell. None of them moves `changed` either, and no release write does, except a `ReleaseEpic` that names Undo-none steps: each owner decision it appends moves `changed`, as any answer does (`store.recordNamedReleases`, `store.appendDecision`) (see [Node runs in the store](#node-runs-in-the-store) and [Node checks in the store](#node-checks-in-the-store)).

**GetEpic and ListEpics.** `GetEpic` answers an unknown id with `not_found` (`enginehost.epicService.GetEpic`). The stored epic it returns also carries its planner state: `paused` and `parked`, then `turns`, `tasks` and `questions`, each in order, and its `branch`, the state of the epic's branch: `''`, `creating` or `created` (`enginehost.storedEpicMessage`; `proto/orrery/engine/v1/epic.proto: StoredEpic`). It also carries its pinned workflow, as `workflow`: the workflow's `name`, its canonical `text` and the `sha256` of that text. The pinned workflow's node rows are not carried (`enginehost.storedEpicMessage`, `enginehost.workflowMessage`; `proto/orrery/engine/v1/epic.proto: EpicWorkflow`, `proto/orrery/engine/v1/epic.proto: StoredEpic.workflow`). It also carries `reviewers`, the epic's merged reviewers in the order their names first appear, as [Reviewers](#reviewers) describes them. Its `contract` has no `reviewers` and no `review_gates`, since `enginehost.contractMessage` leaves both empty (`proto/orrery/engine/v1/epic.proto: StoredEpic.reviewers`; `enginehost.storedEpicMessage`, `enginehost.epicReviewerMessage`). A turn has `seq`, `step`, `state`, `reason`, `outline`, `outcome`, `decisions`, `started` and `ended`. A task has the id `T<seq>` and has `turn`, `title`, `deliverable`, `files`, `requirements`, `contributes`, `gate`, `evidence` and `status`. Its `check` is empty since O3d2, and the evidence conclusion carries the check. Its build is carried as well: `stage`, `attempt`, `step`, `base`, `revision`, `failure`, `review`, `held`, the `red` and `green` evidence runs its build names (`null` until it names one), and `reviews`. Each review has `attempt`, `gate`, `step`, `verdict` and `findings`, and each finding has `id`, `claim`, `location`, `step`, `result`, `reason` and `unverified`, which is true when the finding was settled as confirmed without a verifier; a finding's citation, evidence and verify-by are not carried. `attempt` and `turn` are 64-bit integers, which protojson writes as strings. The fields are described in [Task builds](#task-builds) (`enginehost.taskMessage`, `enginehost.taskReviewMessage`, `enginehost.taskFindingMessage`; `proto/orrery/engine/v1/epic.proto: EpicTask`, `proto/orrery/engine/v1/epic.proto: EpicTaskReview`, `proto/orrery/engine/v1/epic.proto: EpicTaskFinding`; `enginehost.evidenceServiceRunMessage`). A requirement carries its `item`, or none for a requirement stored before O3d2, and its `owner`, `contributors` and `state`. `owner` is the `T<seq>` of the lowest-seq task not failed that claims it, empty when none does. `contributors` are the `T<seq>` of each task not failed that contributes to it, in seq order. `state` is `green` when a green run of its current item passed, `red` when a red run of it passed and no green one did, and `pending` otherwise, where a run passes as `evidence.Passed` gives (`enginehost.requirementClaims`, `enginehost.requirementOwnership`, `enginehost.requirementState`; `proto/orrery/engine/v1/epic.proto: EpicRequirement`). It also carries `nodes`, the epic's workflow node runs, in the order of the pinned workflow's nodes, then by path, with a run of a node the workflow does not hold after the others. Each run has its `path`, `node`, `kind`, `state`, `outcome`, `round`, `loop_round`, `attempt`, `output` and `revision`, and the rows its kind holds: a plan's `turns` and `tasks` above its mark, a build's `tasks`, an evidence node's `runs` for its attempt, a gate's or command's `check` and an agent's `step` id. A check holds its `state`, `exit`, `timed_out`, `not_run`, `stdout` and `stderr`, and is unset until it is stored. The stored epic that `CreateEpic`, `GetEpic`, `PauseEpic`, `ResumeEpic`, `StopEpic` and `RetryEpic` return carries the same nodes, since each reads them in one read with the epic (`enginehost.epicService.storedEpic`, `enginehost.nodeMessages`, `enginehost.epicNodeMessage`, `enginehost.storedCheckOf`, `enginehost.nodeCheckMessage`; `proto/orrery/engine/v1/epic.proto: StoredEpic.nodes`, `proto/orrery/engine/v1/epic.proto: EpicNode`, `proto/orrery/engine/v1/epic.proto: EpicNodeCheck`). It also carries the end-of-epic review: `frozen`, the revision the review froze, `clean`, the revision the epic ended clean at, and `ledger`, an `EpicLedger` with the reviewers' `verdicts`, the ledger's `findings` and the correction `rounds`. Each verdict has `position`, `reviewer`, `step`, `verdict` (`holds`, `fails` or `gap`) and `reason`. Each finding has `id`, `position`, `reviewer`, `finding`, `claim`, `citation`, `location`, `evidence`, `verify_by`, `step`, `result`, `unverified` and `reason`, and its `closures`, each with `round`, `step`, `result` and `reason`. Each round has `round`, `revision`, `tasks` and `outcome`. A ledger finding carries its citation, evidence and verify-by, which a task's finding does not. The three fields are empty before the review starts, and `position`, `round` and each task seq are 64-bit integers, which protojson writes as strings (`enginehost.storedEpicMessage`, `enginehost.reviewLedgerMessage`, `enginehost.reviewVerdictMessage`, `enginehost.reviewFindingMessage`, `enginehost.reviewClosureMessage`, `enginehost.reviewRoundMessage`; `proto/orrery/engine/v1/epic.proto: StoredEpic.ledger`, `proto/orrery/engine/v1/epic.proto: StoredEpic.frozen`, `proto/orrery/engine/v1/epic.proto: StoredEpic.clean`, `proto/orrery/engine/v1/epic.proto: EpicLedger`). It also carries `releases`, the epic's release runs in the order the store reads them, by attempt, then part, then position, and `release_slot`, the holder of its repository's release slot, empty when the slot is free (see [Release](#release); `enginehost.epicService.storedEpic`, `enginehost.releaseRunMessage`; `proto/orrery/engine/v1/epic.proto: StoredEpic.releases`, `proto/orrery/engine/v1/epic.proto: StoredEpic.release_slot`). A question has the id `Q<seq>` and has `turn`, `text` and `state`, which is `open`, `answered` or `replaced`. It has its `label`, its `choices`, each with `id` and `does`, and its `recommendation`, with `choice`, `why` and `confidence`, which is unset when the question recommends none. Its `replaces` and `replaced_by` are the `Q<n>` of the question it replaces and of the question that replaced it, empty when there is none. Its `choice` is the choice that answered it, `other` for words, and is empty while the question is open or replaced (`enginehost.questionMessage`, `enginehost.choiceMessage`, `enginehost.recommendationMessage`, `enginehost.questionRef`; `proto/orrery/engine/v1/epic.proto: EpicQuestion`, `proto/orrery/engine/v1/epic.proto: EpicQuestionChoice`, `proto/orrery/engine/v1/epic.proto: EpicRecommendation`; `store.loadPlanSections`). With `history` set, `GetEpic` also carries `triggers`, oldest first by creation and then id, and `tells`, in seq order. Without it, both are empty (`enginehost.epicService.GetEpic`, `enginehost.epicService.fillHistory`, `store.Store.EpicHistory`, `enginehost.triggerMessage`, `enginehost.tellMessage`; `proto/orrery/engine/v1/epic.proto: GetEpicRequest.history`, `proto/orrery/engine/v1/epic.proto: StoredEpic.triggers`, `proto/orrery/engine/v1/epic.proto: StoredEpic.tells`). It carries the epic's `summary`, which is empty until the summary loop writes it (see **Summary** above; `enginehost.storedEpicMessage`). `ListEpics` answers each stored epic as an `EpicSummary`: `id`, `repository`, `title`, `phase`, `created` and `changed`, with no sections and no workflow. The list is ordered by `id` (`enginehost.epicService.ListEpics`, `enginehost.summaryMessage`; `internal/store/queries.sql: ListEpics`).

**AnswerEpic.** Before the store is asked, `AnswerEpic` refuses an empty `epic_id` or `id` with `invalid_argument`, naming the first one that is empty (`enginehost.checkEpicAnswer`, `enginehost.epicService.AnswerEpic`). Then it checks the shape of the answer, in the order below. Each refusal is `invalid_argument`, except the last:

- A note, which is an answer with no `question`, with a `choice`: `choice: a note answers no question`. A note whose text is empty after trimming white space: `text: empty`.
- An answer to a question with no `choice`: `choice: empty`.
- A `question` that is not `Q` followed by a positive decimal number: `not_found` with `question: no question <question> on epic <epic_id>`, before the store is asked (`enginehost.epicService.AnswerEpic`, `enginehost.questionNotFound`, `enginehost.questionSeq`).

`text` is required for a note and for the choice `other`, and optional for a declared choice (`enginehost.checkEpicAnswer`).

The store then records the answer in one write. A note is `AnswerEpic` (`store.Store.AnswerEpic`). An answer that names a question is `AnswerQuestion` (`store.Store.AnswerQuestion`):

- An `epic_id` that names no epic answers `not_found` (`enginehost.answerError`; `store.requireEpic`).
- An `id` already recorded on the epic with other text, or with equal text and another choice, answers `already_exists`. Text is compared exactly, and a note's choice is empty (`enginehost.answerError`; `store.replayAnswer`).
- An `id` already recorded with equal text and the same choice returns the stored decision, and writes nothing. This check runs first, so a repeat that names a question its first submission closed still returns the stored decision (`store.Store.AnswerQuestion`, `store.replayAnswer`).
- A question the epic does not hold answers `not_found`. A question that a later question replaced answers `failed_precondition` with `question: Qn was replaced by Qm`, and an answered question answers `failed_precondition` with `question: <question> is closed`. Each writes nothing (`enginehost.answerError`, `enginehost.questionNotFound`; `store.requireAnswerable`).
- A `choice` the question does not offer answers `invalid_argument` with `choice: <question> offers no choice <choice>`. A question offers each of its declared choices and `other`, which every question offers. `other` with text that is blank after trimming white space answers `invalid_argument` with `text: empty`. Both write nothing (`enginehost.answerError`; `store.requireAnswerable`, `store.offers`).
- Otherwise an answer that names a question is written as one unit: the question becomes `answered` with the `choice`, a pending trigger with the id `answer:<id>` and the cause `answer <id> to Q<n>` is inserted, and the decision is appended with its `question` set to that question's seq. A declared choice keeps its text as given, which may be empty. An answer to a park question that is not final ends the park whatever its choice, and an answer to a final park leaves the park as it is (see **Park questions**) (`store.recordQuestionAnswer`, `store.endParkByAnswer`).
- Otherwise a note is appended with `question` `0`. A note closes nothing and adds no trigger.
- Either way the decision is appended with the next `seq`, counted from 1, dated at the call, and the epic's `changed` moves to that time (`store.appendDecision`).

The response carries the decision, whose `question` is `Q<n>` or empty for a note. While questions are open, a note also gets a `warning` that names them in seq order: `recorded as a note: it answers no question, and Q1, Q2 are open`. A repeat of a note gets the warning too, when questions are open at the repeat (`enginehost.epicService.answerNote`, `enginehost.noteWarning`).

No call updates or deletes a decision. The store has no query that does, and the table's triggers abort any update or delete (`internal/store/queries.sql: InsertEpicDecision`, `internal/store/queries.sql: GetEpicDecisionByClientID`, `internal/store/queries.sql: MaxEpicDecisionSeq`, `internal/store/queries.sql: ListEpicDecisions`; `internal/store/migrations/00014_epics.sql: CREATE TRIGGER epic_decisions_no_update`, `internal/store/migrations/00014_epics.sql: CREATE TRIGGER epic_decisions_no_delete`).

**FollowEpics.** `FollowEpics` takes no fields and answers one `EpicFollow` for each question label, in label order, across all epics: its `label`, its `answered` count and its `followed` count. A label appears when one of its answered questions had a recommendation. `answered` counts those questions, and `followed` counts the ones whose `choice` is the recommended choice. Notes, open and replaced questions, and questions with no label or no recommendation are never counted (`enginehost.epicService.FollowEpics`, `enginehost.followMessage`, `store.Store.Follows`; `internal/store/queries.sql: ListEpicQuestionFollows`). Park questions count under the label `park`. A non-final park answered with `retry`, which `RetryEpic` and `RetryNode` answer, is followed, and one answered with other is answered and not followed. A final park recommends nothing, so it is never counted (see **Park questions**). The planner's prompt shows these counts as Advice followed (see [Planner turns](#planner-turns)).

**Controls.** `PauseEpic`, `StopEpic` and `RetryEpic` each take `id`, return the stored epic read back, and make one store write. `ResumeEpic` takes `id` and returns the stored epic read back. It makes one store write, except for an epic that is not paused and holds a limited task step, which it resumes with no store write (see **ResumeEpic** below). An empty `id` is `invalid_argument` with `id: empty`. An unknown epic is `not_found` with `id: no epic <id>`. A store refusal is `failed_precondition` with its text, and nothing is written (`enginehost.epicService.PauseEpic`, `enginehost.epicControlError`; `store.Store.PauseEpic`, `store.Store.ResumeEpic`, `store.Store.ResumeDueEpic`, `store.Store.CloseDueResume`, `store.Store.StopEpic`). The refusals are these:

- `PauseEpic` of an epic the owner paused, or paused with no recorded source: `epic <id> is already paused`. An epic a usage limit paused is accepted (see the effects below).
- `ResumeEpic` of an epic that is not paused and holds no limited task step: `epic <id> is parked, not paused; retry ends a park` when it is parked, or `epic <id> is not paused` otherwise.
- `StopEpic` of an epic with no running turn that holds no release slot: `epic <id> has no running turn`.
- `RetryEpic` of an epic that is not parked: `epic <id> is not parked`.
- `RetryEpic` of an epic in phase `met_at_base`, which **Park for good** under [Workflows](#workflows) parked for good, and `RetryNode` of such an epic naming its parked node: `epic <id> is parked for good: <its park reason>`. `RetryNode` naming a node with no parked run is refused as the next refusal says first.
- `RetryEpic` with a `node` that has no parked run, a loop id included: `node <node> of epic <id> is not parked`.
- `RetryNode` naming the release node: `node <node> of epic <id> is the release; resume it with orrery epic release <id>`. Only `ReleaseEpic` resumes a stopped release, since it moves the phase back to `releasing`; a plain retry would rerun the node while the phase stays `stopped` (`store.Store.RetryNode`; see [Release](#release)).

The effects are these (`store.Store.PauseEpic`, `store.Store.ResumeEpic`, `store.Store.ResumeDueEpic`, `store.Store.CloseDueResume`, `store.Store.StopEpic`, `store.pauseAsOwner`, `store.resumeEpic`, `store.closeEpicDueResumes`; `internal/store/queries.sql: PauseEpic`, `internal/store/queries.sql: SetEpicPauseSource`, `internal/store/queries.sql: ResumeEpic`):

- `PauseEpic` sets `paused` and records the owner as the pause source. It closes the epic's due rows in the same write, so no reset that is still due resumes the epic (`store.Store.PauseEpic`, `store.pauseAsOwner`; `internal/store/queries.sql: PauseEpic`, `internal/store/queries.sql: SetEpicPauseSource`). On an epic a usage limit paused, it takes over the pause as the owner's, so the epic stays paused past the reset (`store.Store.PauseEpic`). A running turn finishes, and the planner starts no turn for the epic.
- `ResumeEpic` clears `paused` and the pause source when the epic is paused, closes the epic's due rows, and resumes its limited task steps. It does not clear a park. The steps are described below (`store.Store.ResumeEpic`, `store.resumeEpic`; `internal/store/queries.sql: ResumeEpic`).
- `StopEpic` sets `paused` as the owner's pause, closes the epic's due rows, and flags the running turn's `stop`, in one write, and returns the turn's step, which is empty while the turn has none. The service then stops that step when it has one (`enginehost.epicService.StopEpic`). A step that has already ended is left as it is, so a result it stored stays. A failure of that stop is `internal` after the pause and the flag are stored. The turn ends as [Planner turns](#planner-turns) gives. An epic with no running turn that holds its repository's release slot is not refused in that case: one write frees the slot and returns the id of its open release step, if one runs, and the service stops that step as above. The epic keeps its phase and is not paused (`store.Store.StopEpic`, `store.stopReleaseSlot`; see [Release](#release)).
- `RetryEpic` clears `parked` and stores the epic's highest turn seq in `retried`, so the failed turns up to that seq stop counting toward a park. Its triggers stay pending. Each open park question of the epic is answered `retry` (`store.closeParkQuestions`). Each parked loop of the epic also turns running with one more granted round, so its next round runs, in the same write (`store.Store.RetryEpic`; `store.grantParkedLoops`). A loop has its `max` rounds plus the rounds granted to it (see **Running**). A parked node run is not rerun by this, so it stays parked until `RetryNode` reruns it.
- `RetryEpic` with `node` reruns each parked run of that workflow node id under its next attempt, through `RetryNode`, and ends the park in the same write. The run's outcome, output, revision, refs, refs-checked flag, result and end are cleared, so its work is prepared and started again. The epic's `retried` is set as above, but no loop is granted a round, and a loop is not a node a retry names. Each open park question of the epic is answered `retry` in the same write, as it is without `node` (`store.closeParkQuestions`). Its attempt counts up by one from `0`, where a run starts (`store.Store.RetryNode`, `store.Store.RetryEpic`, `enginehost.epicService.retryPark`; `internal/store/queries.sql: RetryEpicNode`).
- `ResumeDueEpic`, which the due pass calls for an epic's row, resumes the epic only when the row is `due` and the pause is a usage limit's, as `ResumeEpic` does. It closes the row in every case (`store.Store.ResumeDueEpic`).
- `StopTask`, which a build's stopped step calls, stores the task's build and pauses the epic as the owner's in one write, closing the epic's due rows as `StopEpic` does. A task that is not open, or whose stored build is not from, pauses nothing (`store.Store.StopTask`; `store.pauseAsOwner`). No reset resumes an epic that a build stop paused.

**ResumeEpic.** `ResumeEpic` reads the epic, then its limited task steps: the open steps that a usage limit holds and that carry a task label, oldest first. A planner turn's step carries no label, so it is not one of them. A build step's label is `T<seq>`, an evidence judge step's is `evidence-<run id>`, and a review or closure step's is the path of its node run (`enginehost.epicService.limitedTaskSteps`; `interpret.Runner.startReviewUnits`; `build.Runner.implementerStart`, `build.Runner.reviewStart`, `build.Runner.verifierStart`; `enginehost.judgeStart`). The store's `ResumeEpic` runs when the epic is paused or holds no such step. When the epic is paused, it clears `paused` and its pause source, and closes its due rows. When the epic is not paused and holds no such step, the store refuses the call with the message above. An epic that is not paused and holds such a step is not refused, and the store is not called (`enginehost.epicService.ResumeEpic`). Each limited step is then resumed with `Steps.Resume`, which sends the owner's resume prompt in the step's loop (`enginehost.epicService.ResumeEpic`; `enginehost.Steps.Resume`). A resume that the step cap refuses leaves the step limited, and the step is listed in `stillLimited`. A step that is no longer limited, or has ended, is in neither list (`enginehost.epicService.ResumeEpic`). Any other error from a step's resume is `internal` with its text. The call stops there, the steps resumed before it stay resumed, and a pause the store cleared stays cleared (`enginehost.epicService.ResumeEpic`). The response is the stored epic read back, with `resumed` and `stillLimited` (`enginehost.epicService.ResumeEpic`). Resuming a step continues a step that already exists, so it runs whether or not the epic is parked. A park stays until `RetryEpic` ends it.

**Refusals.** The Connect codes and messages:

| Connect code | When | Message |
|---|---|---|
| `unavailable` | any call with no store open | `epic store unavailable` |
| `invalid_argument` | `CreateEpic`, for each failed check above | `<field>: <reason>`, as the check gives it |
| `invalid_argument` | `CreateEpic` with a reviewer no layer can hold, or an enabled reviewer the layers leave incomplete | `read the reviewers of <repo>: ...` or `merge the reviewers of <repo>: ...`, as the **Reviewers** paragraph above gives it |
| `already_exists` | `CreateEpic` with an `id` already stored | `id: <id> is already in the database` |
| `invalid_argument` | `CreateEpic` with a workflow that does not load at the base | `workflow: at base <commit>: <refusal>`, where the refusal is one of [Workflows](#workflows) |
| `invalid_argument` | `AnswerEpic` with an empty `epic_id` or `id`, a note with a `choice` or with blank text, or a question with no `choice` | `epic_id: empty`, `id: empty`, `choice: a note answers no question`, `text: empty` or `choice: empty` |
| `invalid_argument` | `AnswerEpic` with a `choice` the question does not offer, or `other` with blank text | `choice: <question> offers no choice <choice>`, or `text: empty` |
| `not_found` | `GetEpic` with an unknown `id` | `id: no epic <id>` |
| `not_found` | `AnswerEpic` with an unknown `epic_id` | `epic_id: no epic <id>` |
| `not_found` | `AnswerEpic` with a `question` that is not `Q<n>`, or that the epic does not hold | `question: no question <question> on epic <epic_id>` |
| `failed_precondition` | `AnswerEpic` with a `question` that a later question replaced, or that is answered | `question: Qn was replaced by Qm`, or `question: <question> is closed` |
| `already_exists` | `AnswerEpic` with an `id` recorded with other text or another choice | `id: <id> was recorded with other text` |
| `invalid_argument` | `PauseEpic`, `ResumeEpic`, `StopEpic`, `RetryEpic` or `ReleaseEpic` with an empty `id` | `id: empty` |
| `invalid_argument` | `ReleaseEpic` with a position in `undo_none` below 1 | `undo_none: position <n> is below 1` |
| `not_found` | `PauseEpic`, `ResumeEpic`, `StopEpic`, `RetryEpic` or `ReleaseEpic` with an unknown `id` | `id: no epic <id>` |
| `failed_precondition` | a control refused by the state of its epic | the refusal text (see **Controls**, and [Release](#release) for `ReleaseEpic`) |
| `internal` | a store failure, or a failed `Steps.Stop` in `StopEpic` | the error text |

(`enginehost.epicUnavailableMessage`, `enginehost.epicService.ready`, `enginehost.epicService.CreateEpic`, `enginehost.epicService.GetEpic`, `enginehost.epicService.AnswerEpic`, `enginehost.answerError`, `enginehost.questionNotFound`, `enginehost.checkEpicAnswer`, `enginehost.epicControlError`, `enginehost.refuseEpic`, `enginehost.epicStoreError`)

#### Release

A clean epic is released by the `release` node of its workflow (see [Workflows](#workflows)). The node's run takes the repository's release slot, prepares a candidate, runs the epic's release steps in order, judges the result from the stored evidence, and then ends the epic `released`, or `done` when the epic has no release steps. `kinds.Release.Decide` decides each step from one read of the epic's rows (`store.Store.EpicFlow`), and `interpret.Runner.release` carries out the action it returns. The run ends `passed`. A stop parks the run and the epic, with its reason, and `orrery epic release` resumes the release (see **ReleaseEpic** below).

**Slot and phases (R1).** A release phase is `clean`, `releasing`, `stopped`, `released` or `done` (`store.PhaseClean`, `store.PhaseReleasing`, `store.PhaseStopped`). The review that O3e2 adds is to set `clean`, and nothing on this tree sets it. A clean epic with release steps takes its repository's release slot and moves to `releasing` in one write (`kinds.releaseClean`, `interpret.Runner.takeSlot`, `store.Store.TakeReleaseSlot`). The slot is the one row of `epic_release_slots` for the repository, so one epic holds it at a time (see [Epics in the store](#epics-in-the-store)). A clean epic while another epic holds the slot takes no action and waits, and its `release_slot` names the holder (`kinds.releaseClean`, `enginehost.epicService.storedEpic`). A clean epic with no release steps takes no slot and reaches its ending at once (`kinds.releaseClean`). A `releasing` epic keeps the slot through a stop. A stopped epic keeps it until `StopEpic` frees it, and `ReleaseEpic` takes it back when it is free (`store.resumeRelease`; see **Controls** under [Epics](#epics)). A released or done epic frees the slot it holds, and then its run ends `passed` (`kinds.releaseEnded`, `interpret.Runner.freeSlot`, `store.Store.FreeReleaseSlot`).

**Candidate (R2).** `interpret.Runner.prepareRelease` makes the candidate without a checkout. It reads origin's default branch, the one `git ls-remote --symref origin HEAD` names, and fetches its commit into `refs/orrery/<slug>/release/origin` (`interpret.fetchOrigin`, `interpret.defaultBranch`, `build.Slug`). When that commit is an ancestor of the epic's branch tip, the candidate is the tip, and no Done gate runs (`interpret.decideRelease`, `interpret.isAncestor`). Otherwise origin's commit is merged into the tip with `git merge-tree --write-tree` and `git commit-tree`, which honors `commit.gpgSign`, and the merge is pinned at `refs/orrery/<slug>/release/<run id>-<attempt>` (`interpret.mergeOrigin`, `interpret.mergeTree`, `interpret.mergedTree`, `build.Pin`). A merge that conflicts is an error that names the conflicted paths. A merged candidate runs the epic's Done gates in order at the candidate, walked as an integrate run walks them (see **Batch integration** under [Workflows](#workflows); `kinds.doneGates`, `kinds.behindAction`). A gate that does not pass stops the release with `done gate <gate> did not pass at <candidate>`. A gate lost at a restart twice stops it with the park reason of that walk. When every gate passes, the candidate is recorded with the merge as its one commit (`interpret.Runner.recordCandidate`). Any error of the prepare stops the release with its text, and the prepare waits while the epic is paused or parked (`kinds.prepareRelease`, `kinds.prepare`). The prepare writes its result, `{error, behind, candidate, tip}`, before it sets the run's revision, so a restart between the two writes keeps whether the candidate was behind (`interpret.Runner.prepareRelease`, `store.Store.RecordNodeResult`, `store.Store.PrepareNode`, `kinds.ReleaseResult`).

**Release steps (R3).** After the candidate passes, the release steps run in order. A step that has a passed run, at any attempt, is passed over. Each other step runs as one writing step (`kinds.walkSteps`, `kinds.stepAction`, `interpret.Runner.startReleaseStep`):

- Its role is `release` in the machine's role file (`roles.Agent`). It runs in a worktree detached at its start, which is the candidate for the first step and the head the last passed step left for each later one. The worktree is `wt`, inside a directory named by the step's id, under `release-worktrees` in the state directory. The id is `store.NodeWorkID` of the run, its attempt and `release <n>` (`interpret.Runner.releaseStepDir`, `build.AddWorktree`). A runner with no state directory cannot start a step.
- It starts as a `StepStart` with `Unowned` set, its done value `done` and the epic as its label. Its done return runs the head, ancestry and clean checks and no owned check, and it has no gate (`enginehost.commitChecksWithoutOwned`, `enginehost.StepStart.Unowned`; see **Checks** under [Step lifecycle](#step-lifecycle)).
- Its prompt names only that step, its target and its undo, its start commit, and the one rerun of CI on the same commit that the step may make. Its return is `status`, which is `done` or `blocked`, `commit` (HEAD), `commits` (each commit it added on top of its start) and `evidence` (`interpret.releaseStepPrompt`, `interpret.releaseStepSchema`).
- Once the step's end is stored, the release stores a `ReleaseRun` from it, before any later step starts. The run passes only when the end is `done`, HEAD descends from the step's start, no commit between the start and HEAD is a merge, and the commits the step listed are exactly the commits between them (`interpret.Runner.recordReleaseStep`, `interpret.releaseStepReason`, `store.Store.RecordRelease`). The step's worktree is removed once its run is stored (`build.RemoveWorktrees`). A passing step's HEAD is the start of the next step.

**Stops (R4).** Every failure the release finds stops it. `interpret.Runner.stopRelease` makes one write (`store.Store.StopRelease`): the release node run parks with outcome `failed` and the reason as its output, the epic parks with the reason, the reason is added as a tell, and the epic moves from `releasing` to `stopped`. The slot stays held. The tell is routed as the epic's own notice (see **Tells** under [Planner turns](#planner-turns)), so each stop makes a notice of its own. The run's output is cut to `NodeOutputMax` bytes (`store.capNodeOutput`), and the epic's `parked` text and the tell to `TurnReasonMax` bytes (`store.capTurnText`). The stop reasons are these (`kinds.Release.Decide`):

- the error of a candidate prepare, or `release result is not a JSON object: <error>` when the run's stored result does not decode;
- a Done gate that did not pass, or a Done gate lost at a restart twice, as above;
- `release step <n> (<step>) failed: <reason>`, where the reason is the step's end, such as `step ended stopped`, with its status, its stored reason and its first failed check (`interpret.releaseEndReason`), or the reason its run recorded, such as `HEAD <head> does not descend from the step's start <start>`, `the step added a merge commit: <commits>`, or `the step did not start: <cause>` (`interpret.Runner.failReleaseStep`);
- `release step <n> (<step>) did not start: this epic does not hold the release slot`, and `the judge did not start: this epic does not hold the release slot` for the judge;
- `release step <n> (<step>) failed at an earlier attempt with undo none, so it runs again only when named`;
- the judge's failure, as below.

A step that a restart ends `engine_restarted` has a failed end, and the release stops at it like any other. The step's prompt lets it rerun CI once on the same commit. The release never reruns a failed step by itself, and a further attempt is the owner's call through `ReleaseEpic`.

**Judge (R5).** After the last release step passes, a judge runs (`kinds.judgeAction`, `interpret.Runner.startJudge`). It is a read-only step with role `judge`, run at the candidate in the repository and not at the head the steps left. Its prompt holds `release_then`, and for each passed release step the step's name, its commits and its evidence, all taken from the stored release runs. The prompt tells it to run no command. Its return is `status` (`done`), `released` and `uncovered`, the postconditions the evidence does not cover (`interpret.releaseJudgePrompt`, `interpret.releaseJudgeSchema`). The judge passes when its end is `done`, `released` is true and `uncovered` is empty. Otherwise the release stops with the reason: its end, its decode error, `uncovered postconditions: <list>`, or `the judge did not release the epic` (`interpret.releaseJudgeReason`, `interpret.Runner.recordJudge`).

**Ending.** A passed judge ends the epic. `interpret.Runner.endEpic` calls the `Ending` of `interpret.Config` with the phase `released` (or `done` for an epic with no release steps) and the body `Epic <id> ended <phase>.` (`interpret.Ending`, `interpret.Runner.endEpic`). The next pass frees the slot and ends the run `passed` (`kinds.releaseEnded`). orreryd sets the store as its `Ending`: `store.Store.EndEpic` sets the phase and adds the body as the epic's next tell in one write, and an epic already in that phase changes nothing, so a restart ends it once (`cmd/orreryd`; `store.Store.EndEpic`). A `Runner` with no `Ending` fails a release that reaches its ending with `no Ending configured`; the epic then keeps its phase and whatever slot it holds, and that error is returned again on each pass.

**StopEpic.** `StopEpic` on an epic with no running turn that holds the release slot, whether it is releasing or stopped, frees the slot and stops its open release step, as **Controls** under [Epics](#epics) gives. The stopped step's end is recorded as a failed run, and a releasing epic stops on that run. A releasing epic with no open step stops at its next step or judge decision, since that decision finds the slot not held (`store.stopReleaseSlot`, `store.openReleaseStep`).

**ReleaseEpic (R6).** `orrery epic release ID [--undo-none N]...` and the `epic_release` tool make the call `ReleaseEpic` of the `Epics` service on the session socket (`enginehost.epicService.ReleaseEpic`). `serve` forwards no `ReleaseEpic` call (`server.epicsServer`). The call resumes a stopped release in one write (`store.Store.ReleaseEpic`, `store.resumeRelease`). It takes the slot when the slot is free, and it:

- for each distinct position that `undo_none` names, in the order given, stores a `named` release run for the parked run's next attempt, and appends an owner decision with the id `release-undo-none-<position>-<attempt>` (`store.recordNamedReleases`);
- moves the parked release node run to its next attempt, clearing its outcome, output, revision, refs, result and end (`store.retryNode`), unparks the epic, moves it to `releasing`, and adds the tell `release resumed at step <n>, <step>`, or `release resumed at the judge` when every step has passed (`store.resumedReleaseText`).

The release resumes at the first step with no passed run (`store.unfinishedReleaseStep`). A step that failed at an earlier attempt runs again at the next one, except an undo-none step, which runs again only when the call names its position (`kinds.undoNoneStopped`). A named position whose step has passed is recorded as the owner's decision, and nothing runs for it, since the walk starts only steps with no passed run. The call is refused, and writes nothing, when (`store.checkReleaseResume`, `store.namedUndoNone`):

- the epic is not stopped: `epic <id> is not stopped`;
- it has no parked release run: `epic <id> has no parked release run`;
- another epic holds the release slot of its repository: `epic <holder> holds the release slot of <repository>`;
- a position is below 1, above the number of steps, or of a step that is not undo-none: `position <n> of epic <id> is not an undo-none release step`;
- the first unfinished step is undo-none, has a failed run and is not named: `release step <n> of epic <id> is undo-none and failed; name it to run it again`.

The service refuses an empty `id` and a position below 1 before the store is asked, and an unknown epic is `not_found`. The refusals are those of [Epics](#epics) (`enginehost.epicService.ReleaseEpic`).

#### Workflows

A workflow is a YAML file that names the nodes an epic's run is made of, in order. This subsection gives the file, its format, the load check that refuses a file, the built-in `epic`, what an epic keeps, and how `orreryd` runs a pinned workflow (see **Running**).

**The file.** A workflow is `.orrery/workflows/<name>.yaml` in the repository, or the built-in of that name. A workflow name, a node id and an include each match `^[a-z][a-z0-9_-]*$` (`workflow.namePattern`). The file is one YAML document. Its top level is a mapping of exactly two keys: `name`, required and equal to the file's name without `.yaml`, and `nodes`, required, a non-empty sequence of nodes. Any other key is refused (`workflow.Parse`, `workflow.Check`, `workflow.document`, `workflow.readWorkflow`, `workflow.readTop`).

**Nodes.** A node is a mapping with exactly one of `kind`, `loop` or `include`. A node with none of them, or with two, is refused (`workflow.formOf`). The forms are these:

- A kind node has `id`, `kind` and the kind's settings, which sit beside `id` and `kind`. The registered kinds are these (`workflow.kinds`):
  - `plan` sits at the top level and in a loop body. Its setting is `body`, a non-empty sequence of `build`, `integrate` and the check kinds below. The load check requires neither a `build` nor an `integrate` in it. It reports the outcome `complete`.
  - `build` sits only in a plan body. It takes no settings and reports no outcome.
  - `integrate` sits only in a plan body. It takes no settings and no body, and reports the outcomes `passed` and `failed`. Its run is decided as **Batch integration** below gives (`workflow.kinds`, `kinds.Integrate`).
  - The check kinds `gate`, `evidence`, `owned`, `command` and `agent` sit at the top level, in a loop body and in a plan body. Each takes no body and reports the outcomes `passed` and `failed`. Their settings are these (`workflow.readGateSettings`, `workflow.readEvidenceSettings`, `workflow.readOwnedSettings`, `workflow.readCommandSettings`, `workflow.readAgentSettings`):
    - `gate` takes `gate`, a gate's name matching `^[a-z][a-z0-9_]*$`, and `at`.
    - `evidence` takes `at`.
    - `owned` takes `paths`, a non-empty sequence of repository-relative paths. No element is empty, `.` or `..`, and none starts with `/`. A path that ends in `/` names a directory and everything under it.
    - `command` takes `argv`, a non-empty sequence of non-empty strings whose first item contains a `/`, `limit`, an integer of 1 or more that counts seconds, `needs`, an optional mapping from a pool name matching `^[a-z][a-z0-9_]*$` to an integer of 1 or more, and `at`.
    - `agent` takes `agent` and `prompt`, each required and not blank, `schema`, a required mapping, and `at`.
    - `at` is `base` or `head`, and is required by `gate`, `evidence`, `command` and `agent`. `base` is the epic's base, and `head` is the head of the epic's branch `epic/<slug>` (`workflow.readAt`, `interpret.revisionOf`).
  - The epic kinds `review`, `conclude` and `release` sit only at the top level, and `closure` sits only in a loop body. Each takes no settings and no body. `review` and `conclude` report the outcome `passed`, and `closure` and `release` report `passed` and `failed`. The format registers them so a workflow can name them. The interpreter always registers `release`, whose run is [Release](#release), and registers `review`, `closure` and `conclude` only when it has both `Reviewers` and `Ending` (see **End-of-epic review**); without them a node of one of those starts and then waits (see **Running** and **Reserved names**; `interpret.New`).
- A loop node has `id`, `loop` and `body`. `loop` is a mapping of `max`, `until` and `spent`, and no other key (`workflow.readLoop`, `workflow.readLoopSettings`, `workflow.readLoopFields`, `workflow.readMax`, `workflow.readUntil`, `workflow.readSpent`):
  - `max` is required: an integer of 1 or more.
  - `until` is required: `<node id>.<outcome>`, naming a direct child of the loop's body whose kind reports that outcome.
  - `spent` is `park` or `continue`, and `park` when it is absent.

  A loop sits at the top level and in a loop body. Its `body` is a non-empty sequence that holds what the top level holds, `plan`, `loop`, `include` and the check kinds, and `closure`, but not `review`, `conclude` or `release`.
- An include node has the single key `include`, the name of another workflow. It sits at the top level and in a loop body, and expansion replaces it with that workflow's nodes.

The load check validates `max`, `until` and `spent`, and the interpreter enforces them when it runs a loop (see **Running**).

**Check kinds.** Each check kind's node run is decided on each pass by its kind, which asks the interpreter for work: `prepare` records the revision the work runs at, `start` starts the work, and `refs` compares the refs (see **Running**). Every check kind but `owned` is prepared first, and `prepare` waits while the epic is paused or parked, so no work starts then. Once the revision is recorded, the work runs to its end whatever the pause (`kinds.prepare`, `interpret.Runner.prepare`). The rules by kind are these (`kinds.Gate.Decide`, `kinds.Command.Decide`, `kinds.Evidence.Decide`, `kinds.Owned.Decide`, `kinds.Agent.Decide`):

- `gate` and `command` run one check. It ends `passed` when it exits 0 without timing out, and `failed` otherwise, with its stdout and then its stderr as output (`kinds.checkEnd`, `kinds.streams`). A check that did not run ends `failed` with the reason it did not run. `gate` runs the gate of its name, resolved from the epic's gates and then the repository's project file as the plan check resolves them. A gate that does not resolve, and an agent gate, do not run (`interpret.Runner.withGate`, `build.ResolveGate`). `command` runs its `argv` with no shell, and `argv[0]` is a path that is never looked up on `PATH` (`enginehost.gateProgram`). A command's `limit` and `needs` apply as a gate's do, so a command with no `needs` draws one member of the `gate` pool (`interpret.Runner.startCheck`, `enginehost.NodeChecks.request`, `gates.DefaultPool`). The check runs in a detached worktree at the run's revision, under the grant of its needs (`enginehost.NodeChecks.run`).
- `evidence` runs each Requirement that names an evidence item, in contract order, as `Evidence.RunItem` runs an item, under an id derived from the node run and its attempt. Its phase is `red` at `base` and `green` at `head` (`interpret.Runner.startEvidence`, `enginehost.Evidence.RunItem`). It ends `passed` when every such run passed, with one output line per Requirement. An epic whose Requirements name no evidence item ends `passed` with a note, and runs nothing (`kinds.Evidence.Decide`, `kinds.evidenceEnd`).
- `owned` records, as a JSON list, the paths the epic's branch changes over its base. An owned node records `[]` when nothing changed (`interpret.Runner.recordOwned`). It ends `passed` when every listed path is owned by `paths`, and `failed` with the paths that are not as output. A result that is not a JSON list of paths ends `failed` (`kinds.Owned.Decide`, `kinds.owns`). It records no revision.
- `agent` runs one read-only step of its `agent` with its `prompt` and `schema`, at the run's revision, labelled with the epic and no task (`interpret.Runner.startAgent`). It ends `passed` when the step ends `done`, and `failed` otherwise. Its output is the step's end with its result when it ends `done`, and with its reason otherwise (`kinds.Agent.Decide`).

**Failed checks.** A check node run that ends `failed` is decided by where it sits (`interpret.pass.failed`):

- At the top level it parks with the reason `workflow node <path> failed: <output>`. The park parks the epic and is also a tell (`store.Store.ParkNode`). The walk stops at the parked run, so nothing after it runs until `epic retry ID NODE` reruns it (see [Epics](#epics)). An evidence node at base that found a Requirement met parks for good instead (see **Park for good**).
- In a loop body it ends `failed` with its output. The walk takes that end as the end of the round, so the loop decides the round as it decides any other: it ends when its `until` names this outcome, and otherwise it moves on or spends its rounds (`interpret.pass.walk`, `interpret.pass.loop`).
- In a plan body it ends `failed` with its output, and it writes the trigger `workflow:` with a derived id and the cause `workflow node <path> failed: <output>`, so the planner plans a turn. A passing check in a plan body wakes nobody (`store.Store.EndNodesWaking`).

An integrate run is not decided by these rules. Its end is its batch, and a failed batch writes its trigger (see **Batch integration**).

**Ref compare.** A `gate`, `command`, `evidence` or `integrate` run snapshots the refs when its work is prepared, and compares a new snapshot with that one once its work has ended, before its outcome counts (`interpret.Runner.prepare`, `interpret.Runner.prepareBatch`, `interpret.Runner.refSnapshot`, `interpret.Runner.checkRefs`, `build.Snapshot`, `build.Compare`). An integrate run's snapshot is taken when its batch is claimed, and its compare comes after its Done gates and captures (see **Batch integration**). A snapshot holds the primary checkout's HEAD and every ref of the repository, except `refs/orrery/` and each epic's branch in that repository. A move parks when it changes `refs/heads/main`, the branch the primary checkout had checked out at the snapshot, or `HEAD`. The first parking move parks the run and the epic, at any position, with the reason `workflow node <path> moved <ref> (<from> to <to>)`. Every other move is a tell with that text. The compare is recorded once, so a repeat adds no tell (`store.Store.CheckNodeRefs`, `store.parkCheckedRun`). `owned` and `agent` take no compare.

**Loading.** `Load` takes a workflow name and a reader of repository paths (`workflow.Reader`, `workflow.Load`). It reads `.orrery/workflows/<name>.yaml` first, and a repository file replaces the built-in of the same name. When that path is absent, `Load` uses the built-in of that name, which is `epic` alone. A name that is neither is refused, naming both (`workflow.Load`, `workflow.builtinText`, `workflow.resolve`). Each include is expanded in place by the same rule, and an include cycle is refused, naming the chain (`workflow.expand`, `workflow.expandBody`, `workflow.expandInclude`). A load includes each workflow name at most once: a second include of a name is refused wherever it sits. The expanded workflow is then checked (`workflow.Load`).

**The check.** `Check` refuses an expanded workflow that has an include left, an id held by more than one node anywhere in the workflow, or a loop whose `until` does not name a direct child of its body whose kind reports that outcome. It returns the first refusal (`workflow.Check`, `workflow.checkNodes`, `workflow.checkUntil`).

**The built-in `epic`.** The built-in is a `red` node, an `evidence` node at `base`; then a `plan` holding a `build` and an `integrate`; then a `review`; then a loop `correct` (`max` 2, `until` `closure.passed`, `spent` `continue`) whose body is a `plan` `fix` holding a `rebuild` and a `reintegrate`, then a `closure`; then a `conclude` and a `release` (`workflow.epicText`). Its canonical text is:

```yaml
name: epic
nodes:
  - id: red
    kind: evidence
    at: base
  - id: plan
    kind: plan
    body:
      - id: build
        kind: build
      - id: integrate
        kind: integrate
  - id: review
    kind: review
  - id: correct
    loop:
      max: 2
      until: closure.passed
      spent: continue
    body:
      - id: fix
        kind: plan
        body:
          - id: rebuild
            kind: build
          - id: reintegrate
            kind: integrate
      - id: closure
        kind: closure
  - id: conclude
    kind: conclude
  - id: release
    kind: release
```

Its sha256 is `635c8150d032f47b09ad509c244e39db5d3c098f1dd18b379cc1eb3cb4c6b7ba`. An epic created before this text keeps the text it pinned, a `plan` holding a `build` alone with sha256 `8e56cd7c52ea852c4deb42ce575ba11e4e6c0e0ca4f201ff03cf24ba0afd8e1e`, as **Storage** gives.

**Canonical text.** `Load` returns the expanded workflow in canonical form, and the lowercase hex sha256 of that text (`workflow.Load`). The text is indented two spaces. Its keys come in a fixed order: `name`, then `nodes`. A node's keys come in the order `id`, then `kind`, or `loop` with `max`, `until` and `spent` (which is always written), then `body`. A check kind's settings come after `kind`, in the order `at`, `gate`, `argv`, `limit`, `needs`, `paths`, `agent`, `prompt` and `schema`, and only the settings the kind takes are written (`workflow.canonDoc`, `workflow.canonNode`, `workflow.canonSettingsOf`, `workflow.canonLoop`, `workflow.canonical`, `workflow.canonNodes`).

**Refusals.** A refusal names the file, the node and the key: `<file>:<line>: node <id>: key <key>: <reason>` (`workflow.site.refusef`). A node with no `id` is named `#<n>`, its 1-based position in its list. A refusal about the workflow as a whole has no node part. A YAML syntax error is `<file>: <error>` (`workflow.document`). The file is `.orrery/workflows/<name>.yaml` for a repository file, `built-in epic` for the built-in, and the included file for a node an include brought in (`workflow.resolve`). The refusals are these:

| Refusal | When | Code |
|---|---|---|
| `key kind: unknown kind "<kind>"` | a `kind` names no registered kind | `workflow.readKind` |
| `key kind: <kind> cannot sit <where>` | a kind sits where it may not: `build` or `integrate` outside a plan body, `plan` in a plan body, `review`, `conclude` or `release` anywhere but the top level, or `closure` anywhere but a loop body. `<where>` is `at the top level`, `in a loop body` or `in a plan body` | `workflow.readKind`, `workflow.kinds` |
| `key loop: cannot sit <where>`, `key include: cannot sit <where>` | a loop or an include in a plan body | `workflow.readLoop`, `workflow.readInclude` |
| `key kind: one of kind, loop or include is required` | a node with none of the three | `workflow.formOf` |
| `key <form>: cannot sit beside <form>` | a node with two of the three; the second is named | `workflow.formOf` |
| `key <key>: unknown key` | a key the node or its kind does not take, or a top-level key other than `name` and `nodes`. A loop setting other than `max`, `until` and `spent` is named `loop.<key>` | `workflow.checkKeys`, `workflow.readLoopFields` |
| `key <key>: required` | `name`, `nodes`, `id`, a `body`, a setting its check kind requires (`at`, `gate`, `paths`, `argv`, `limit`, `agent`, `prompt` or `schema`, as the kind needs it), `loop.max` or `loop.until` is absent | `workflow.readTop`, `workflow.idOf`, `workflow.readKindBody`, `workflow.readLoop`, `workflow.readMax`, `workflow.readUntil`, `workflow.requireKey` |
| `key at: "<value>" must be base or head` | `at` is neither `base` nor `head` | `workflow.readAt` |
| `key gate: "<value>" must match ^[a-z][a-z0-9_]*$` | a `gate` name does not match the pattern | `workflow.snakeOf`, `workflow.readGateSettings` |
| `key agent: must not be blank`, or `key prompt: must not be blank` | `agent` or `prompt` is blank | `workflow.readText` |
| `key argv: must be a sequence of strings`, `must hold at least one string`, `item <n> must be a non-empty string`, or `item 1 "<value>" must contain a /` | `argv` is not a non-empty sequence of non-empty strings, or its first item has no `/` | `workflow.readArgv` |
| `key limit: must be an integer of 1 or more`, or `key needs.<pool>: must be an integer of 1 or more` | a `limit` or a pool count is not an integer of 1 or more | `workflow.countOf`, `workflow.readLimit`, `workflow.readNeeds` |
| `key needs: must be a mapping`, or `key needs.<pool>: "<pool>" must match ^[a-z][a-z0-9_]*$` | `needs` is not a mapping, or a pool name does not match the pattern | `workflow.readNeeds` |
| `key paths: must be a sequence of paths`, `must hold at least one path`, `item <n> must be a single value`, or `item <n> "<value>" must be a repository-relative path` | `paths` is not a non-empty sequence of paths, or a path is empty, absolute, or has a `.` or `..` element | `workflow.readPaths`, `workflow.isRepoPath` |
| `key schema: must be a mapping` | `schema` is not a mapping | `workflow.readSchema` |
| `key loop.max: must be an integer of 1 or more` | `max` is not an integer, or is below 1 | `workflow.readMax` |
| `key loop.until: must be <node id>.<outcome>` | `until` is not a node id and an outcome joined by a dot | `workflow.readUntil` |
| `key loop.spent: must be park or continue` | `spent` is another value | `workflow.readSpent` |
| `key loop: must be a mapping` | `loop` is not a mapping | `workflow.readLoopSettings` |
| `key nodes: must be a sequence of nodes`, `must hold at least one node`, or `item <n> must be a mapping` | a list of nodes that is not a non-empty sequence of mappings; `body` is named the same way | `workflow.readList` |
| `key <key>: must be a single value`, or `key <key>: appears twice` | a value that is a sequence or a mapping, or a key given twice | `workflow.pairsOf`, `workflow.text` |
| `key <key>: "<value>" must match ^[a-z][a-z0-9_-]*$` | a `name`, `id` or `include` that does not match the pattern; `<key>` is the key that holds it | `workflow.nameOf` |
| `key name: "<value>" differs from the file's name "<name>"` | `name` is not the file's name | `workflow.workflowName` |
| `key id: already used at <file>:<line>` | a node id that a node earlier in the workflow holds | `workflow.checkNodes` |
| `key loop.until: <node> has no outcome "<outcome>"` | the named child's kind does not report the outcome | `workflow.checkUntil` |
| `key loop.until: "<node>" is not a node in the loop body` | no direct child of the body has that id | `workflow.checkUntil` |
| `key include: include cycle <a> -> <b> -> <a>` | an include names a workflow the chain already holds | `workflow.expandInclude` |
| `key include: "<name>" is included more than once` | an include of a name the load has already included | `workflow.expandInclude` |
| `key include: no file .orrery/workflows/<name>.yaml and no built-in "<name>"` | an include names neither a file nor a built-in | `workflow.expandInclude` |
| `workflow "<name>": no file .orrery/workflows/<name>.yaml and no built-in "<name>"` | the workflow `CreateEpic` names is neither a file nor a built-in | `workflow.Load` |
| `workflow name "<name>" must match ^[a-z][a-z0-9_-]*$` | the name `CreateEpic` names is malformed | `workflow.Load` |
| `.orrery/workflows/<name>.yaml: not a regular file (mode <mode>)` | the base holds the path as anything but a regular file, such as a symlink | `enginehost.baseReader`; `workflow.resolve` |
| `<file>: empty file: a workflow is one YAML document` | the file has no document | `workflow.document` |
| `<file>: <error>` | a YAML syntax error | `workflow.document` |
| `<file>:<line>: a workflow is one YAML document` | a second document | `workflow.document` |
| `<file>:<line>: a workflow is a mapping of name and nodes` | the document is not a mapping | `workflow.readWorkflow` |

**Pinning.** `CreateEpic` loads the workflow at the base commit it resolves (`enginehost.pinWorkflow`). Each file is read through git: `ls-tree` finds the path at that commit, and `cat-file` returns its exact bytes (`enginehost.baseReader`). The working tree is never read. A path the commit does not hold falls through to the built-in.

**Storage.** The pinned name, canonical text, sha256 and node rows are written by the create (see [Epics in the store](#epics-in-the-store)). No query updates them, so an edit to a workflow file, committed or not, changes neither. An epic stored before migration `00022_workflow.sql` pins `epic`.

**Running.** `orreryd` runs every epic through the interpreter, with `--sessions` and an open store (see [orreryd](#orreryd)). Each pass reads every epic and loads its flow. An epic with no pinned text runs the built-in `epic`. Otherwise the pinned text must hash to the pinned sha256, and it is parsed as `pinned/<name>.yaml` and checked (`interpret.loadFlow`, `interpret.parsePinned`). A flow that does not load parks its epic with the reason `workflow <name>: <error>`, unless the epic is parked already (`interpret.Runner.parkUnloaded`). Each decision on an epic is made from one read of its rows: `EpicFlow` reads the epic, its node runs, its pending trigger count, its node checks, its evidence runs, its agent steps, its review and the steps of its review and closure runs in one read-only transaction, so they describe one state (`store.Store.EpicFlow`, `store.readReviewSteps`; `interpret.Runner.decide`). The pass carries out each structural action the decision asks for through the store, then decides again from a new read, at most 16 times (`interpret.Runner.passFlow`).

A node run is one instance of a workflow node on an epic, stored as described in [Node runs in the store](#node-runs-in-the-store). A top-level node's path is its id. A node in a loop body in round `r` has the path `<loop path>@<r>/<id>`, and a build child of a plan run has the path `<plan path>/<build id>/T<seq>` (`interpret.pass.walk`, `interpret.pass.loop`, `interpret.pass.fanOut`). Its id is `NodeRunID` of the epic, the path, the round and a cause: the first 32 hex digits of the lowercase SHA-256 of `orrery-workflow`, the epic, the path, the round in decimal and the cause, joined with NUL bytes. The cause is `start` for a node or loop that starts, and `turn <turn> task <position>` for a build child, with the position the task's 1-based place among its turn's tasks in seq order. A later plan's trigger id is `workflow:` and the id for the cause `trigger` (`store.NodeRunID`; `interpret.pass.start`, `interpret.pass.causeOf`). The same inputs give the same id, and a start whose path or trigger id is stored already changes nothing, so a restart that decides the same rows again starts nothing twice (see [Node runs in the store](#node-runs-in-the-store)). The work of a check node run has its own ids, as `NodeWorkID` gives them: the first 32 hex digits of the lowercase SHA-256 of `orrery-workflow-work`, the node run's id, its attempt and a part, joined with NUL bytes. A gate's or command's check is named `check`, an agent's step `step`, and an evidence item by its Requirement's id. An integrate run's Done gate checks are named `done 1`, `done 2` and so on, with `done <i> rerun` for a rerun, and its captures `capture <R>`, with `capture <R> rerun` for a rerun. A node run starts at attempt `0`, and `epic retry ID NODE` reruns a parked node at its next attempt (`store.NodeWorkID`, `kinds.checkRun`, `kinds.Agent.Decide`, `kinds.Evidence.Decide`).

The walk takes the top-level nodes in order. A node with no run starts, and the walk stops there, so a node starts when the one before it has ended. A parked run stops the walk. Once every top-level node has ended, the pass returns no action, so the epic keeps its phase (`interpret.Decide`, `interpret.pass.walk`). A running node is decided by its kind, and a node whose kind the interpreter does not register, such as `release`, or a `review` when the interpreter has no `Reviewers` and `Ending`, gets no action: its run stays running, parks nothing, and the walk waits there (`interpret.pass.running`). A registered kind decides: an end ends its run and every running run under it, and a run hands the node on to the planner or the build (see [Planner turns](#planner-turns) and [Task builds](#task-builds); `interpret.pass.running`). A check node's kind asks for its `prepare`, `start` or `refs` work instead, which the interpreter carries out (see **Check kinds**; `interpret.Runner.work`).

A loop starts at round 1. Each round walks the loop's body, and once every node of the round has ended, the loop decides from that round. It ends when its `until` node ended with the until outcome in that round. Otherwise it moves to the next round while its rounds, `max` plus the rounds granted to it, are not spent. When they are spent, it ends if `spent` is `continue` and parks if `spent` is `park` (`interpret.pass.loop`). A park parks the loop at its round, and parks the epic with the reason `loop <id> spent its <n> rounds without <node>.<outcome>`, which is also written as a tell (`store.Store.ParkLoop`).

**Plan bodies.** A plan run's body holds its `build` nodes, its `integrate` nodes and its check nodes. Its tasks start under its first `build` node, as build child runs (see [Task builds](#task-builds)). Its other body nodes, the check nodes and the `integrate` nodes, walk as a settle walk under `<plan path>/S<seq>/`, where `<seq>` is the seq of a task, so an integrate run's path is `<plan path>/S<seq>/integrate`. A settle point is the seq of a task of a turn above the plan's mark. The highest such seq starts its walk only when no task of the epic is open, and each walk already started is decided on each pass, so an older walk finishes while a newer one waits (`interpret.pass.settle`, `interpret.pass.settlePoint`). The plan ends only once its kind ends it and its latest walk has ended (`interpret.pass.plan`), and, where its body holds an `integrate` node, as **Built or park** below gives. A failed check, or a failed batch, ends its walk and writes its trigger, and the plan does not end while a trigger is pending, so the planner plans again first (`kinds.planComplete`).

**Fan-out hold.** While an integrate run under the plan is running or parked, the fan-out starts no build child, so no task starts until its batch is green or red. Build children already running are still decided (`interpret.pass.fanOut`, `interpret.pass.batchOpen`).

**Batch integration.** An integrate run takes the accepted tasks that no run has claimed, composes them into a candidate, runs the epic's Done gates and captures at the candidate, and then advances the epic's branch to it when it is green. Its work runs in the order below (`kinds.Integrate.Decide`, `interpret.Runner.work`).

- **Batch.** The batch is the tasks the run has claimed, or else every task that is `done` with no batch, in seq order (`kinds.batchOf`). A paused or parked epic prepares no batch (`kinds.prepare`). A batch with no task ends the run `passed` at once, with the output `no accepted task to integrate`, and with no revision (`kinds.prepareBatch`).
- **Compose.** The batch is composed on the epic's candidate when it has one, and on its stored base otherwise (`interpret.Runner.prepareBatch`, `compose.Compose`). The tasks are taken in seq order. A task built on another base than that commit is left out, with the reason `T<seq> was built on <base>, not on the batch base <base>`. A task whose changed paths equal, contain or lie under a path that an earlier composed task changed is left out, with the reason `T<seq> overlaps T<k> at <path>` (`compose.composeTasks`, `compose.overlapReason`, `compose.pathsOverlap`). Each other task becomes one commit on the previous one, the first on the base, with its revision's author, committer, dates and message, so the same inputs give the same commits. The candidate is the last commit, or the base when none composed. With any composed, the candidate is pinned create-only at `refs/orrery/<slug>/candidate/<run id>-<attempt>`: a ref already at the candidate passes, and a ref at another commit fails the compose (`compose.pinCandidate`, `interpret.candidateRef`). Nothing reads or writes the repository's working tree or index (`compose.Compose`, `compose.composeTask`).
- **Claim.** The run claims its batch in one write. Its revision becomes the candidate, its refs are snapshotted, and its result is the compose result as JSON, `{"base", "candidate", "composed": [{"task", "commit"}], "leftOut": [{"task", "reason"}]}` (`store.Store.ClaimBatch`, `interpret.encodeBatch`). Every task of the batch is claimed with the run's path, whether it composed or was left out (`store.claimBatchTasks`). A left-out task therefore stays claimed by that run, and no later batch takes it, since a batch takes only the tasks it claims and the `done` tasks with no batch (`kinds.batchOf`). A retry of the same run takes the same tasks again.
- **Nothing composed.** A batch in which nothing composed ends `failed` at once, with its left-out reasons as its output. It runs no Done gate and no capture, and takes no refs compare (`kinds.Integrate.Decide`).
- **Done gates.** Otherwise each Done gate of the epic (`done_gates`) runs in order as a check at the candidate, under the id `NodeWorkID` gives for `done <i>` (`kinds.gateCheck`, `interpret.Runner.startCheck`). A gate's check starts only once the gate before it has passed. A check that did not run, timed out or exited non-zero makes the batch red: the later gates do not start, and no capture does (`kinds.doneGates`, `kinds.checkPassed`). A check whose reason is `lost at a restart` reruns once, under `done <i> rerun`. A lost rerun parks the run and the epic with `integrate <path>: Done gate <gate> lost at a restart twice` (`kinds.gateCheck`, `interpret.pass.running`).
- **Captures.** With every Done gate passed, each Requirement that names an evidence item is captured at the candidate in the green phase, under the id `NodeWorkID` gives for `capture <R>` (`kinds.captures`, `interpret.Runner.capture`, `interpret.Runner.runItems`). The captures that have no run start together, and any still running makes the run wait. A capture lost at a restart reruns once, under `capture <R> rerun`. A lost rerun parks the run and the epic with `integrate <path>: capture <R> lost at a restart twice` (`kinds.captureFor`).
- **Verdict.** Once the Done gates and captures have settled, the run's refs are compared as **Ref compare** gives, whatever the result (`interpret.Runner.checkRefs`, `kinds.settle`). The batch is red when a Done gate failed, when a capture did not run or has no verdict, or when a Requirement that was green at the stored base has a capture that no longer passes. A capture that ran and found its Requirement not met is not red by itself, and its line reads `<R> not yet green`. A red batch ends `failed` with one line per cause, then the left-out lines. The causes are `Done gate <gate> failed`, `<R> capture failed` and `<R> regressed from green at the stored base <base>` (`kinds.captureCauses`, `kinds.outputOf`).
- **Advance.** A batch that is not red advances the epic's branch from the stored base to the candidate, expecting the branch to be at the stored base (`compose.Advance`, `interpret.Runner.advance`). A branch already at the candidate is left as it is, which is the state a restart leaves when it cut an advance, and the run ends `passed`. A branch at another commit parks the run and the epic with `epic/<slug> moved: at <tip>, not the stored base <base>`. A branch checked out in a worktree parks them with `epic/<slug> is checked out in a worktree, so it was not moved` (`interpret.advanceBlock`, `interpret.Runner.parkBatch`). Otherwise the run ends `passed`, with one line per Requirement, `<R> green` or `<R> not yet green`, then the left-out lines (`kinds.requirementLines`).
- **Batch writes.** Ending the run is one write. `EndBatch` ends it with its outcome and output, and adds its trigger (`store.Store.EndBatch`). A passed batch moves the stored base to its candidate and clears the candidate. A failed batch sets the candidate to its revision, unless the revision is the stored base (`store.settleBatchBase`). A batch with a revision adds its trigger, `workflow:` with a derived id, pending, with the cause `workflow node <path> <passed|failed>: <output>`, so the planner plans the epic again. A batch with no revision, which is an empty one, writes no trigger (`interpret.pass.batch`, `interpret.pass.batchTrigger`). A parked batch writes no trigger, since its park adds the reason as a tell (see **Pause and park** under [Planner turns](#planner-turns)).
- **Retry.** `RetryNode` reruns a parked integrate run at its next attempt. Its revision, refs, result and end are cleared, so it composes again on the candidate or the stored base, and its candidate ref is named for the new attempt (`store.Store.RetryNode`, `interpret.candidateRef`).

**Built or park.** Where a plan body holds an `integrate` node and the plan would end complete, the plan ends `built` when `kinds.Unbuilt` lists nothing, and otherwise the epic parks (`interpret.pass.complete`). A `built` end ends the plan run and each running run under it, and sets the epic's phase to `built` when the phase is `accepted`. Any other phase stays, so a fix plan that ends built in `correcting` leaves the phase `correcting` (`store.Store.EndPlanBuilt`, `store.endAndBuild`). Otherwise the epic parks with the reason `epic not built: <missing>`, where the lines of `kinds.Unbuilt` are joined with `; `. Each line is `R<n> names no evidence item`, `R<n> has no red at the epic's base <base>` or `R<n> is not green at the stored base <stored base>`, in contract order. The park writes the trigger `workflow:` with a derived id, pending, with the same text as its cause, and adds the reason as a tell (`store.Store.ParkEpicWaking`). The derived id comes from the highest turn seq, so a repeat with the same highest turn changes nothing. A parked epic is not planned, so the trigger waits until `RetryEpic` ends the park (see [Planner turns](#planner-turns)).

**Park for good.** A top-level `evidence` node at `base` that ends `failed` with a Requirement whose red run was judged `holds` parks for good: the run and the epic park, and the phase becomes `met_at_base` (`interpret.pass.failed`, `kinds.MetAtBase`, `store.Store.ParkNodeFinal`). Its reason is `workflow node <path>: <ids> met at the epic's base <base>; parked for good, and a corrected contract is a new epic`, where `<ids>` are those Requirements in contract order, joined with `, `. `RetryEpic` and `RetryNode` refuse an epic in that phase and change nothing (`store.metAtBaseRefusal`; **Controls** under [Epics](#epics)).

The interpreter adds no pause or park rule of its own, except that a check's `prepare` waits while the epic is paused or parked (see **Check kinds**), and a stopped review step pauses the epic once (see **End-of-epic review**). A paused or parked epic's node runs start and end as usual, and the planner and the build keep their own rules (`planner.Decide`; see [Planner turns](#planner-turns) and [Task builds](#task-builds)).

**Restart of check nodes.** Before the interpreter's first pass, `orreryd` takes up the open checks (`cmd/orreryd.sessionHost.startNodeChecks`, `cmd/orreryd.startSessions`; `enginehost.NodeChecks.Resume`). A check stored with a grant re-adopts its gate under that grant, and ends `failed` with `lost at a restart` when the gate cannot be re-adopted (`enginehost.NodeChecks.readoptGranted`, `enginehost.NodeChecks.readoptLost`). A check with no grant runs from the start, after its worktree is removed. A check cut before its gate launched is left running for the next start (`enginehost.NodeChecks.readoptUnlaunched`, `enginehost.NodeChecks.notStarted`). `Resume` re-reads each check before driving it, so a check that a drive ended after `Resume` listed is left alone (`enginehost.NodeChecks.Resume`, `enginehost.NodeChecks.readoptStart`). A gate that detaches under `--holder-scope` keeps running under its holder and records nothing, so the next start readopts it. An agent node's step is re-adopted when its holder is live, as any step is. Otherwise the step ends `engine_restarted` (`enginehost.Steps.Readopt`, `enginehost.Steps.Reconcile`, `enginehost.Steps.endRestarted`), and its node fails with that end as its output, so it parks as a failed top-level check does. The writes of a node run's work and outcome each change only a run still in the state they need, so a repeat changes nothing: a repeated `Run` returns the stored check, and `PrepareNode`, `RecordNodeResult`, `CheckNodeRefs`, `ParkNode` and `EndNodesWaking` each skip a run that is past their step (`enginehost.NodeChecks.Run`, `store.Store.PrepareNode`, `store.Store.RecordNodeResult`, `store.Store.CheckNodeRefs`, `store.Store.ParkNode`, `store.Store.EndNodesWaking`). An integrate run's Done gate and capture checks restart the same way, and a lost one reruns once and then parks (see **Batch integration**).

**End-of-epic review.** The `review`, `closure` and `conclude` kinds are registered only when the interpreter's `Config` has both `Reviewers` and `Ending` (`interpret.New`, `interpret.Config`). `orreryd` passes `interpret.NewEpicReviewers` as `Reviewers` and the store as `Ending` (`cmd/orreryd.sessionHost.startFlow`), and `store.Store.EndEpic` satisfies `interpret.Ending`. Without both, a `review` node starts and waits, so no epic ends clean (`interpret.pass.running`).

**Freeze.** A review run with no revision is prepared while its epic is neither paused nor parked (`kinds.Review.Decide`, `kinds.prepare`). Its preparation freezes the review at the epic's stored base (`interpret.storedBaseOf`, `interpret.Runner.prepareReview`). The run takes that revision, the review stores it as `frozen`, and the phase becomes `reviewing` (`store.Store.FreezeReview`). The frozen revision is stored once, so a repeat changes nothing (`store.frozenRevision`). Reviewers run at the frozen revision, and verifiers run over the epic's base to it, so a commit that lands after the freeze is not reviewed (`interpret.EpicReviewers.Start`, `interpret.Runner.reviewUnitStart`, `interpret.epicVerifierStart`).

**Parts and tries.** The review has one part for each enabled epic-stage reviewer, `reviewer <n>`, where `n` is the reviewer's 1-based position among the epic's enabled epic-stage reviewers, in merged order; a disabled or task-stage reviewer takes no position (`reviewers.ForStage`). It has one part for each finding of a reviewer with verify on, `verify <n> <k>`, where `n` is that reviewer's position and `k` is the finding's 1-based place among its findings (`interpret.EpicReviewers.Epic`, `kinds.reviewPass.settleReviewer`, `kinds.reviewPass.settleFinding`). A part's tries run in order: try 1 has the part's id, and try `t` has `<part> try <t>`, each id made by `store.NodeWorkID` from the run and its attempt (`kinds.reviewTryID`, `kinds.walkTries`). The walk starts the first try with no step and waits while a try runs. The first try that gives a verdict settles the part (`kinds.walkTries`). A start that the step cap refuses starts nothing more in that pass, and it is no error (`interpret.Runner.startReviewUnits`, `enginehost.ErrCapReached`). No try starts while the epic is paused or parked (`kinds.Review.Decide`).

**Ends that cost nothing or cost one.** An `engine_restarted` end is free, and the next try starts. A `stopped` end is free once its step is in the review's holds. A `stopped` end whose step is not held yet is a hold: `store.Store.HoldReviewStep` stores it, pauses the epic as its owner's and adds the tell `<part> stopped: <step reason>`, once per step (`kinds.walkTries`, `kinds.reviewPass.pending`, `interpret.Runner.carryReview`, `store.Store.HoldReviewStep`). A hold is decided even while the epic is paused or parked. Any other end without a verdict costs one try, and the second costly end settles the part as a gap with the reason `<part> gave no verdict twice: <end>: <reason>` (`kinds.walkTries`).

**Verdicts.** A reviewer's step gives `holds` when it ends `done`. A `gate_failed` end gives `fails`, with its findings, when its return is a gate return with status `done`, no assertion `undetermined` and at least one finding (`kinds.reviewIsReviewerVerdict`, `kinds.reviewFailedFindings`). A verifier's or a closure verifier's step gives the result of the first result of its return when it ends `done`, and the evidence of that result is the reason (`kinds.reviewVerifierVerdict`, `kinds.closureVerdict`). A reviewer whose part is a gap gets the verdict `gap` with the gap's reason, and its step is the last try's (`kinds.reviewPass.settleReviewer`).

**Verifiers.** A finding of a reviewer with verify on is settled by its verifier chain: a verdict gives its result, and a gap gives `undetermined` with the gap's reason (`kinds.reviewPass.settleFinding`). A finding of a reviewer with verify off is confirmed without a verifier and marked unverified, with the reason `unverified: reviewer <name> has verify off` (`kinds.reviewPass.settleFinding`). A verifier step is read-only and runs the `verifier` role's agent with the build's verifier schema and return check (`interpret.Runner.verifierAgent`, `roles.Agent`, `build.VerifierStart`).

**Review end.** The ledger holds the findings in the order the reviewers return them, each with the id `<reviewer>-<k>` (`kinds.reviewPass.ledger`). The review run ends `passed` once every part has a verdict, with one output line per reviewer, `<name>: <verdict>` (`kinds.Review.Decide`, `kinds.reviewLines`). `store.Store.EndReview` then ends the run, stores the verdicts and the ledger, and sets the phase to `correcting` (`interpret.Runner.carryReview`, `store.storeEndedReview`). Those rows are written only there, so no finding is added after the review ends. The `review` kind is not in a loop body, so no reviewer runs again after corrections (`workflow.kinds`).

**Corrections.** While the epic is `correcting`, a plan that starts writes the ledger trigger in place of its started trigger. Its id is `ledger:` and `store.NodeRunID` of the epic, the plan's path, the round and `ledger`. Its cause is `kinds.LedgerCause` of the round (`interpret.pass.planTrigger`). The round's open findings are the confirmed findings that no closure in an earlier round closed or left undetermined (`kinds.OpenFindings`, `kinds.closureSettled`). Round 1 with none has the cause `ledger round 1: no confirmed findings; nothing to correct` (`kinds.LedgerCause`). The round's correction tasks are built and integrated as the plan's tasks are (see **Batch integration**).

**Closure.** A `closure` node in the loop body runs once in each round, after the round's fix plan (`workflow.kinds`). For each open finding it runs one closure verifier, part `close <id>`, as a read-only step at the closure run's revision, which is the epic's stored base, over the frozen revision to that revision (`interpret.Runner.prepareClosure`, `interpret.Runner.reviewUnitStart`, `interpret.closureStart`). The step has the role text `internal/interpret/closure.md`, the schema `interpret.ClosureSchema`, and the return check `interpret.closureCheck`, which accepts exactly one result, `closed`, `open` or `undetermined`. A gap is `undetermined` (`kinds.reviewPass.settleClose`). The closure run ends `passed` when no closure is `open` and `failed` otherwise. It stores its round with the revision, the seqs of the round's tasks, its outcome and its closures, and an undetermined closure leaves its finding out of later rounds (`kinds.Closure.Decide`, `kinds.closureTasks`, `kinds.closureMark`, `kinds.closureSettled`, `store.Store.EndClosure`, `store.insertClosureRound`). The round's tasks are those of the turns above the mark of its fix plan (`kinds.closureTasks`). A failed closure writes no trigger, so the loop's next round starts with its fix plan, which writes the ledger trigger (`interpret.pass.end`, `interpret.pass.loop`).

**Conclude.** The `conclude` node has no prepare and no work, so its run ends the epic as soon as the run starts, whether or not the epic is paused (`kinds.Conclude.Decide`, `interpret.Runner.conclude`). The epic ends `clean` when no gap is left and `ended_with_gaps` when one is. `store.Store.EndEpic` sets the phase and adds the ending body as a tell: `epic <id> ended clean at <stored base>`, or `epic <id> ended with gaps: ` followed by the gaps joined with `; ` (`kinds.Conclude.Decide`, `store.Store.EndEpic`). For `clean`, it also stores the stored base as the review's clean revision (`store.Store.EndEpic`; `internal/store/queries.sql: SetEpicReviewClean`). The conclude run then ends `passed` (`interpret.Runner.conclude`).

**Gaps.** A conclusion's gaps are, in order: `no review ran`, when the review never froze; `reviewer <name>: <reason>`, for each reviewer with a gap verdict; and, for each ledger finding, `<id>: verifier undetermined: <reason>` when its verifier is undetermined, `<id>: closure undetermined in round <r>: <reason>` for each undetermined closure, and `<id>: open after <n> rounds` when it is confirmed and every round left it open, where `<n>` is the number of rounds run (`kinds.concludeGaps`, `kinds.OpenFindings`).

**Reserved names.** These names are reserved for the engine epics that fill them, so the epics can be built in parallel without colliding. Each is contract now and has no behavior until its epic lands. Migration numbers are assigned in merge order; the names stay.

| Name | What | Epic |
|---|---|---|
| `integrate` | kind, top level | O3d4 |
| `review`, `conclude` | kinds, top level; registered by the format, and by the interpreter with `Reviewers` and `Ending` | O3e2 |
| `closure` | kind, loop body; registered by the format, and by the interpreter with `Reviewers` and `Ending` | O3e2 |
| `release` | kind, top level; registered by the format and by the interpreter (see [Release](#release)) | O3e3 |
| `built`, `met_at_base` | phases | O3d4 |
| `reviewing`, `correcting`, `clean`, `ended_with_gaps` | phases | O3e2 |
| `releasing`, `released`, `done`, `stopped` | phases (see [Release](#release)) | O3e3 |
| `00033_integration` | migration | O3d4 |
| `guards` | migration | O3d5b |
| `review_ledger` | migration | O3e2 |
| `release` | migration `00038_release.sql` (see [Epics in the store](#epics-in-the-store)) | O3e3 |
| `repro <R>` | work part | O3d5b |
| `release <pos>`, `judge` | work parts (see [Release](#release)) | O3e3 |
| `ReleaseEpic` | RPC of `orrery.engine.v1.Epics` (see [Release](#release)) | O3e3 |
| `EpicTask` 23 `test_changes`, 24 `guard` | reserved proto fields (this table) | O3d5b |
| `EpicRequirement` 9 `disagreement` | reserved proto field (this table) | O3d5b |
| `StoredEpic` 18 `releases`, 19 `release_slot` | proto fields, declared (see [Release](#release)) | O3e3 |
| `orrery.v1.EpicsGetResponse` 13 `ledger`, 14 `releases`, 15 `reviews` | reserved proto fields (this table) | O3e4 |

The attention epic has declared its names, so they are not in the table above. Migration `00039_questions` adds the question's shape and the answer's choice (see [Epics in the store](#epics-in-the-store)). `EpicQuestion` fields 5 to 10 are `label`, `choices`, `recommendation`, `replaces`, `replaced_by` and `choice` (`proto/orrery/engine/v1/epic.proto: EpicQuestion`). `AnswerEpicRequest` field 5 is `choice` (`proto/orrery/engine/v1/epic.proto: AnswerEpicRequest`). `FollowEpics` is an RPC of `orrery.engine.v1.Epics`, with its messages `FollowEpicsRequest`, `FollowEpicsResponse` and `EpicFollow` (`proto/orrery/engine/v1/epic.proto: Epics`; see [Epics](#epics)).

The built-in workflow takes its final shape in O3d4: a red evidence check at base, `plan` holding `build` and `integrate`, `review`, a loop `correct` (`max` 2, `until` `closure.passed`, `spent` `continue`) over a `plan` and `closure`, `conclude`, then `release`. An epic on it waits at `review` while the interpreter has no `Reviewers` and `Ending` (see **End-of-epic review**). The proto fields above are reserved by this table only, not by `reserved` statements: buf counts deleting a reserved name as a breaking change even when the same field is then declared, so each epic adds its field at the number named here when it builds. The Go name `interpret.Ending` is declared now and follows the same rule.

#### Planner turns

The planner plans an epic in a turn. A turn is a read-only step (see [Steps](#steps)) whose return is checked before it is stored. The interpreter passes every 5 seconds, its default tick (`interpret.defaultTick`, `interpret.New`), and passes the planner for each epic whose plan node run it gives a run (see **Running**; `interpret.Runner.Pass`).

**Triggers.** A trigger is a reason to plan. `CreateEpic` writes the first one, `created`, in its write (`store.Store.CreateEpic`). Each answer that names a question writes one more, with the id `answer:<id>` and the cause `answer <id> to Q<n>` (`store.recordQuestionAnswer`). A trigger is `pending`, `claimed` by a turn, or `consumed` by that turn's return (`internal/store/migrations/00016_planner.sql: CREATE TABLE epic_triggers`). A turn that ends without a return sets its claimed triggers back to `pending` with turn 0 (`internal/store/queries.sql: ReleaseEpicTriggers`). A note writes no trigger. A task that its build fails writes one more, with the id `T<seq>-failed` and the cause `task T<seq> failed: <reason>` (see [Task builds](#task-builds)). A later plan node's start writes one more, when the epic already has a plan run: its id is `workflow:` and a derived id, and its cause is `workflow node <path> started` (see **Running**; `interpret.pass.start`). While the epic is `correcting`, that trigger is the ledger trigger instead, as **End-of-epic review** under [Workflows](#workflows) gives (`interpret.pass.planTrigger`). A check node that fails in a plan body writes one more, with the same prefix and a derived id, and the cause `workflow node <path> failed: <output>` (see **Failed checks**; `store.Store.EndNodesWaking`). An integrate run's end writes one more when it claimed a batch, with its derived id and the cause `workflow node <path> <passed|failed>: <output>` (see **Batch integration** under [Workflows](#workflows); `store.Store.EndBatch`). A plan that ends unbuilt writes one more, with the cause `epic not built: <missing>` (see **Built or park** under [Workflows](#workflows); `store.Store.ParkEpicWaking`). Nothing else inserts one.

**The plan node.** The first plan node run of an epic starts with mark 0 and no trigger. A later one starts with its mark at the epic's highest turn seq, and with the trigger above (`interpret.pass.start`). While it runs, its kind decides it on each pass. It ends `complete` when the epic's highest turn seq is above its mark, that turn succeeded with outcome `complete`, no turn is running, no task is open and no trigger is pending (`kinds.Plan.Decide`), and its latest settle walk has ended (see **Plan bodies** under [Workflows](#workflows)). With an `integrate` node in its body, what follows the end is decided as **Built or park** there gives. Otherwise it runs, and the interpreter passes the planner for the epic (`interpret.pass.running`). Its turns are the planner turns this section describes, and the tasks of each turn start build child runs (see [Task builds](#task-builds)).

**Decide.** Each pass lists the epics with a pending trigger (`store.Store.PlanStates`), then reads the role file once (`planner.Runner.pass`). Under the interpreter's pass, it decides only the epics the interpreter named (`planner.Runner.PassOnly`). `Decide` returns one of three actions for each of them (`planner.Decide`):

- None, when the epic is paused, parked or running, or has no pending trigger (`planner.Decide`).
- Park, with the role file's error, when that read failed (`planner.Decide`; `planner.Runner.pass`). A park is a turn. `parkTurn` claims one and frees it at once, with the error as both its reason and its park, so the turn records the park. The turn starts no step, and its triggers return to `pending` (`planner.Runner.parkTurn`).
- Claim a turn (`planner.Decide`; `planner.Runner.pass`).

**Claim.** `ClaimTurn` runs in one write (`store.Store.ClaimTurn`). It refuses an unknown epic, a paused or parked epic, an epic with a running turn, and an epic with no pending trigger. A refusal changes nothing (`store.checkClaimable`). Otherwise it inserts the next turn, numbered after the epic's highest turn, with `seen` set to the epic's highest decision seq. It then claims every pending trigger for that turn (`store.startTurn`; `internal/store/queries.sql: ClaimEpicTriggers`). A partial unique index allows one `running` turn per epic (`internal/store/migrations/00016_planner.sql: CREATE UNIQUE INDEX epic_turns_one_running`).

**The step.** The runner reads the epic and the turn's triggers, then starts the step (`planner.Runner.startStep`). The step has these settings:

- Id: `turnStepID`, the first 32 hex digits of the lowercase SHA-256 of `orrery-planner`, the epic and the turn's seq, joined with NUL bytes. The same epic and seq give the same id, and a start under an id that a stored step already has is refused, so a restart finds the step rather than starting another (`planner.plannerNamespace`, `planner.turnStepIDLength`, `planner.turnStepID`, `planner.Runner.startStep`; `enginehost.Steps.startID`).
- Agent: the planner role's agent (`planner.Runner.startStep`).
- Workspace: the epic's repository, read-only, with no revision, so the step starts at HEAD (`planner.Runner.startStep`; `enginehost.Steps.Start`).
- Label: the epic's id, and no task (`planner.Runner.startStep`).
- Schema: `Schema`. Its `status` must be `done`, and its `outcome` must be `work`, `wait` or `complete`. Each task requires `contributes`, and each `evidence` object requires `kind` and `tests` (`planner.Runner.startStep`; `planner.Schema`).
- Prompt: the role text (`internal/planner/role.md`), then these sections, each under a `## ` heading in this order. A section with no lines says `None.` (`planner.Prompt`, `planner.writeSection`).
  - `Contract`: the contract's fields in order, with each list as its items (`planner.contractLines`).
  - `Decisions`: each epic decision as `- id: text`, as `- id (answers Qn): text` when it answers question n, or as `- id (answers Qn with choice): text` when the answer gave a choice. The last form has no `: text` when its text is empty. Then the decisions of each earlier turn as `- turn n: text` (`planner.decisionLines`, `planner.decisionLine`).
  - `New notes`: the notes, the epic decisions that answer no question, recorded after the last earlier turn that is not free had seen (`planner.lastTurn`, `planner.newNoteLines`).
  - `Open questions`: each open question as `- Qn (turn n, label): text`, as `- Qn (park): text` for a park question, or as `- Qn (turn n): text` for a question with no label. Each declared choice follows as `  id: does`, then `  recommended: id (confidence): why` when the question recommends one (`planner.questionLines`, `planner.questionHead`).
  - `Advice followed`: each label's counts as `- label: F of A answers took the recommendation`, where `F` is the followed count and `A` the answered count, in label order, from `store.Store.Follows` (`planner.followLines`, `planner.Runner.startStep`).
  - `Tasks`: each task as `- Tn [status] title`, then its deliverable, files, requirements, contributes, gate, and its evidence item as compact JSON (`planner.taskLines`).
  - `Triggers`: the turn's claimed triggers as `- id: cause` (`planner.triggerLines`).
  - `Last failure`: the reason of the last earlier turn that is not free, when that turn failed with a reason (`planner.failureLines`).
- Return check: the planner's return check, described below (`planner.Runner.startStep`, `planner.planOf`).

**The forced return and its check.** Each return that passes `schema` runs the plan check (`enginehost.stepRun.valueChecks`). A return that does not parse is one line: its parse error (`planner.returnCheck`). Any other return is checked by `Check` against the epic's Requirement ids, the files of its open tasks, as the epic stood when the step started (`planner.returnCheck`), and each task's gate and evidence judge, and each question's shape. The gate and the judge are resolved for the epic by `EnvResolver`, which `orreryd` builds from its environment and the managed settings root (`planner.Check`; `planner.returnCheck`; `planner.EnvResolver`, `planner.NewResolver`). Each task refusal is one line: `tasks[i] "<title>" <field>: <reason>`, with `files[j]`, `requirements[k]` or `contributes[k]` for a list item, and `evidence.<field>` for a line the evidence item's check gives. Each question refusal is one line of its own, below. The task reasons are these (`planner.reasonFileForm`, `planner.reasonFileTwice`, `planner.reasonFileOwned`, `planner.reasonGitDir`, `planner.reasonRequirement`, `planner.reasonEmpty`, `planner.reasonNoFiles`, `planner.checkTask`):

- `not a normalized repository-relative file path`, for a file that is empty, has surrounding white space, starts with `/`, contains `\`, is not clean, or has a `.` or `..` part (`planner.fileFormOK`).
- `inside a .git directory`, for a file with a `.git` part in any letter case, such as a hook under `.git/hooks`, which could run code on later git operations (`planner.fileReason`, `planner.inGitDir`).
- `named twice in the plan`, for a file that an earlier file of the return already names (`planner.fileReason`).
- `owned by an unfinished task`, for a file that an open task owns (`planner.fileReason`). A task whose status is `done` or `failed` owns none of its files, so its files plan again (`planner.openFiles`).
- `not a Requirement of the epic`, for a Requirement id the contract does not list (`planner.checkTask`).
- `empty`, for a blank title or deliverable, and `no files`, for a task with no files (`planner.checkTask`).
- `gate: empty`, for a blank gate (`planner.checkTask`).
- `gate: ` and the error of resolving the gate, when the name does not resolve to a command gate for the epic. The error names the gate and the sources it searched, or says that the gate is an agent gate. The epic's gates are checked first, then the repository's project file (see [Gate files](#gate-files); `planner.checkTask`; `planner.EnvResolver.Gate`, `planner.DefineEpicGates`; `gates.ResolveDefined`).
- `evidence.` and a line of the evidence item's check, for each refusal `evidence.Check` gives: `evidence.capture: none, name a capture or supplied files`, `evidence.limit: ...`, `evidence.artifacts[0]: ...` and so on, as [Evidence](#evidence) lists them (`planner.checkTask`; `evidence.Check`).
- `evidence.judge: ` and the error of resolving the judge, when the judge is not empty and claude would not resolve it as an agent for the epic's repository, under the environment and the managed settings. The error wraps `agents.ErrUnresolved` (`planner.checkTask`; `planner.EnvResolver.Judge`).
- `questions[i] "<label>" <line>`, one line for each rule a question breaks, in the order `store.QuestionRefusals` states them. Each line is one of `label: empty`, `label: not kebab-case`, `label: park is reserved for orrery`, `text: empty`, `choices: fewer than two choices`, `choices[j].id: empty`, `choices[j].id: other is offered by orrery`, `choices[j].id: named twice`, `choices[j].does: empty`, `recommended: names no declared choice`, `why: empty`, `confidence: not low, medium or high`, or `replaces: not empty or Q<n> with n >= 1`. Whether a `replaces` names a question the epic holds is not checked here; the store checks it when it writes the turn (see **Ends**, item 2) (`planner.checkQuestion`, `store.QuestionRefusals`).

`Coverage` then checks the same return against the epic's tasks, as the step started, and its refusals follow the lines of `Check` (`planner.returnCheck`; `planner.Coverage`). A stored task counts toward coverage unless its status is `failed` (`planner.taskFailed`, `planner.storedClaims`). Each line has the task's index and title, as the lines above do, except the outcome line:

- `requirements[k]: R<n> is claimed by T<seq>`, when a live stored task already claims the Requirement, and `requirements[k]: R<n> is claimed by tasks[j]`, when an earlier task of the return claims it (`planner.coverage.claimLines`, `planner.returnClaims`).
- `contributes[k]: not a Requirement of the epic`, when the id is not a Requirement of the contract, `contributes[k]: R<n> is claimed by this task`, when the task claims it itself, and `contributes[k]: R<n> is claimed by no task`, when neither a live stored task nor a task of the return claims it (`planner.coverage.contributeLines`).
- `outcome complete: R<n> is claimed by no task`, for each Requirement that neither a live stored task nor a task of the return claims, when the outcome is `complete` (`planner.coverage.outcomeLines`).

The role text (`internal/planner/role.md`) states these rules to the planner: the item's `kind` and `tests`, one task that has not failed claiming each Requirement, the Requirements a task contributes to, and every Requirement claimed at `complete`.

A failed check is sent back to the session as `return: <lines>`, its lines joined with `; `, and a failed `schema` check is sent back the same way under `schema` (`enginehost.stepLoopReturnRecord`, `enginehost.stepRun.retry`). A failed `checkout` check of a read-only planner step is not sent back: it ends the step `checks_failed` (see [Step lifecycle](#step-lifecycle)). The step is sent back three times, and a fourth failing return ends it `checks_failed` (`enginehost.stepLoopMaxContinues`, `enginehost.stepRun.retry`). A return that passes its checks ends the step `done` with no judge call (`enginehost.stepRun.endDoneReturn`).

**Ends.** The runner waits for the step in one-minute waits (`planner.waitInterval`, `planner.Runner.resume`, `planner.Runner.finish`, `planner.Runner.await`). A `Wait` that fails is logged and repeated after a tick. It never ends a running turn, since the step may still be running (`planner.Runner.await`). A timed-out wait whose row shows the step limited stops the step (`planner.Runner.await`; `planner.rowLimited`). A stop that succeeds is not repeated, and one that fails is tried again at the next timed-out wait that still shows the step limited (`planner.Runner.await`). The step then ends `stopped`, and the turn is freed and the epic paused, whether the kept rate limit or the `stopped` end settles it (see [Step lifecycle](#step-lifecycle), Usage limit). When the step ends, `settle` checks these cases in order (`planner.Runner.settle`):

1. A rejected rate limit kept for the step. The turn is freed as a limit with the reason `<kind> rate limit, resets <time>`, where the time is RFC 3339 in UTC, or `<kind> rate limit, reset time unknown` when the limit names no reset time. The turn stores the reset time, or `-1` when the limit names none, and the epic is paused by the limit. When the reset is known, the epic's due row for it is stored in the same write, unless the owner's pause stands, in which case no row is stored. This applies whatever the end (`planner.Runner.settle`; `store.pauseFreed`, `store.pauseLimitedEpic`, `store.Store.FreeTurn`).
2. `done`. In one write the turn stores its decisions, its tasks with their files, Requirements, contributes, gate and evidence item (each `open`), its questions (each `open`, with its label, choices, recommendation, why, confidence and `replaces`), its outline and its outcome. The turn becomes `succeeded`, and its triggers become `consumed` (`planner.Runner.settle`; `store.Store.CompleteTurn`). A question whose `replaces` names an open question closes it: that question becomes `replaced`, and its `replaced_by` is the seq of the new question. A `replaces` that names an answered or replaced question closes nothing, and the new question is still stored (`store.insertQuestions`, `store.replaceQuestion`). A result that does not parse fails the turn (`planner.Runner.complete`). So does a plan with a question that `store.QuestionRefusals` refuses, or whose `replaces` names a question the epic does not hold. Then `CompleteTurn` writes nothing, and the turn fails with the store's `ErrBadQuestion` text as its reason. A shape refusal reads `complete turn <seq> of epic <epic>: bad planner question: question <n> "<label>": <first refusal>`, naming the question by its position and label. A `replaces` the epic does not hold reads `complete turn <seq> of epic <epic>: questions of turn <seq> of epic <epic>: question "<label>": bad planner question: replaces Q<n>: no such question` (`planner.Runner.complete`, `store.checkQuestions`, `store.insertQuestions`; `store.ErrBadQuestion`).
3. `stopped`. The turn is freed with the reason `stopped`, and the epic is paused (`planner.Runner.settle`).
4. `engine_restarted`. The turn is freed with the reason `engine_restarted`, and the epic is not paused (`planner.Runner.settle`).
5. Any other end. The turn fails with `<end>: <reason>` (`planner.Runner.settle`).

The read of the step's kept rate limit, and each write that ends a turn (`CompleteTurn`, `FailTurn` or `FreeTurn`), is retried each tick until it lands, until the store answers `ErrTurnNotRunning` (which is logged, and the write is not retried), or until the context ends. A turn whose context ends is left running for the next start (`planner.Runner.settle`, `planner.Runner.release`, `planner.Runner.failTurn`, `planner.Runner.freeTurn`).

**Stop.** `StopEpic` flags the running turn's `stop` in the same write that pauses the epic (`store.Store.StopEpic`). The turn runs on until its step ends. When the turn already has its step, the service stops that step (`enginehost.epicService.StopEpic`). When the turn has no step yet, the runner checks the flag as it records the step, and stops the step then (`planner.Runner.startStep`, `planner.Runner.stopIfFlagged`). Either way the step ends `stopped`, and the turn is freed with the reason `stopped`, as Ends gives. A step that has already ended `done` keeps its result, and the turn completes with it.

A freed turn returns its triggers to `pending`, and it is not counted toward a park (`store.endTurn`; `internal/store/queries.sql: ReleaseEpicTriggers`). A failed turn's reason is cut to 1024 bytes at a UTF-8 boundary (`store.TurnReasonMax`, `store.capTurnText`). After a failure, when the three latest turns that are not free, and that are later than the epic's `retried` seq, have all failed, the same write parks the epic with `3 planner turns failed in a row: ` and the stored reason (`store.Store.FailTurn`, `store.failedInARow`; `internal/store/queries.sql: ReleaseEpicTriggers`).

**Refusals at the start.** A start refused before its row is written settles the turn as follows (`planner.Runner.refuse`):

- The step cap is full (`enginehost.ErrCapReached`, `enginehost.Manager.reserveStep`). The turn is freed with the reason `step cap full`, with no pause and no park (`planner.Runner.refuse`).
- The agent does not resolve (`agents.ErrUnresolved`), or its name is not valid (`enginehost.ErrBadAgent`, `enginehost.ErrBadAgent`). The turn is freed with the error's text, and the epic is parked with the same text (`planner.Runner.refuse`; `enginehost.Manager.StartStep`, `enginehost.Manager.resolveStepAgent`).
- Any other start error fails the turn with its text (`planner.Runner.refuse`).

**Pause and park.** A free that pauses sets the epic's `paused` to 1 (`store.Store.FreeTurn`; `internal/store/queries.sql: PauseEpic`). A park sets `parked` to its reason (`store.setParked`; `internal/store/queries.sql: SetEpicParked`), and so does a workflow node's park, which the reason of **Failed checks** and **Ref compare** under [Workflows](#workflows) gives (`store.Store.ParkNode`, `store.parkCheckedRun`). The parks of **Batch integration**, **Built or park** and **Park for good** under [Workflows](#workflows) park the same way (`store.Store.ParkEpicWaking`, `store.Store.ParkNodeFinal`). A free that is not a usage limit records the owner as the pause's source. A usage limit records `limit`, and the due pass lifts its pause when the row resumes, 60 seconds past the reset, through `ResumeDueEpic` (`store.pauseFreed`, `store.pauseEpicAs`; see [Epics](#epics) and [Due resumes in the store](#due-resumes-in-the-store)). An owner's pause lasts until `ResumeEpic` clears it. A park lasts until `RetryEpic` ends it (see [Epics](#epics)). A paused or parked epic is not claimed (`store.checkClaimable`), and `Decide` does nothing for it (`planner.Decide`).

**Park questions.** Every park opens a question in the write that parks the epic. A park question has turn `0`, label `park`, the park reason as its text, and state `open` (`store.setParked`, `store.insertParkQuestion`, `store.parkQuestion`). A park that is not final declares one choice, `retry`, which does "end the park and start a turn on the waiting triggers, as orrery epic retry does". It recommends `retry`, with why "no model judges a park; retry runs the epic again on its waiting triggers" and confidence `low`. A final park sets the epic's phase to `met_at_base` before it parks, and its question declares no choice and recommends none, so only `other` is offered (`store.parkQuestion`, `store.parkFinal`). A park while a park question is open replaces it: the open question becomes `replaced` with `replaced_by` set to the new seq, and the new question's `replaces` names it (`store.insertParkQuestion`). An answer to a park question that is not final ends the park, whatever its choice, and an answer to a final park leaves the park in place. `RetryEpic` and `RetryNode` answer each open park question `retry` (see **AnswerEpic** and **Controls** under [Epics](#epics); `store.endParkByAnswer`, `store.closeParkQuestions`). A node's park also adds a tell that carries the seq of the park question (see [Workflows](#workflows)), and a planner turn's park writes no tell, so its owner reads the question in the `park` notice (see **Notices** below) (`store.parkWithTell`, `store.insertTell`).

**The kept rate limit.** Each `rate_limit_event` line becomes a `RateLimit` event, with its status, its rate-limit type and its reset time (`session.reportEvents`, `session.rateLimitEvent`; `session.RateLimit`, `session.RateLimit.isEvent`). The recorder stores a `rejected` status only, in `engine_rate_limits` under the session's id (`enginehost.recorder.ops`). A step's turn reads that row when the step ends (`planner.Runner.settle`). [Engine sessions in the store](#engine-sessions-in-the-store) gives the table.

**Restart.** At start, `orreryd` runs `Steps.Readopt`, then the manager's Reconcile, then `Steps.Reconcile` (`cmd/orreryd.sessionHost.reconcile`), and it starts the interpreter after them (`cmd/orreryd.startSessions`). `Steps.Readopt` rebuilds the run of each open step row it can and registers it with the manager, which re-adopts the step's holder when that holder is live and tracked. `Steps.Reconcile` then starts the loop of each rebuilt step the manager reports live, and ends each other open row `engine_restarted` (`enginehost.Steps.Readopt`, `enginehost.Steps.Reconcile`; see [Restart](#step-lifecycle)). Then the interpreter's `Run` has the planner's `Recover` settle each running turn before the interpreter's first pass (`interpret.Runner.Run`, `planner.Runner.Recover`, `planner.Runner.recoverTurns`, `planner.Runner.recoverTurn`, `planner.Runner.restart`):

- A turn with no recorded step reads the step stored under its `turnStepID` (`planner.Runner.recoverTurn`). A step found there is recorded on the turn, and an open one is stopped when the turn was flagged to stop (`planner.Runner.recoverTurn`, `planner.Runner.recordFound`). A turn with no step under that id has its step started under that id, as a claimed turn's is, and the step is waited on like any open step (`planner.Runner.restart`). The agent is read as a pass reads it: a role error frees the turn and parks the epic with its text, and a refused start settles the turn as a refused start does (see **Refusals at the start**). The started step is recorded on the turn, and stopped at once when the turn was flagged to stop (`planner.Runner.startStep`, `planner.Runner.stopIfFlagged`).
- A turn whose step row cannot be read is logged. It stays running, so its epic gets no new turn (`planner.Runner.recoverTurn`).
- A turn whose step row is still open is waited on in its own goroutine (`planner.Runner.recoverTurn`).
- A turn whose step row has ended is settled by that end (`planner.Runner.recoverTurn`).

A turn whose step `Reconcile` ended `engine_restarted` is freed with that reason, and its epic runs the turn again. A turn whose step was re-adopted waits for that step like any open step, and is settled by its end. The re-adopted step keeps its plan check: `StepsConfig.ReadoptCheck` supplies the check that `planner.ReadoptCheck` gives its epic, built with the same resolver as a new turn's check (`enginehost.Steps.readoptRun`; `enginehost.StepsConfig.GateStateDir`; `planner.ReadoptCheck`; `cmd/orreryd.startSessions`). So its return is checked against the plan and ends `done` with no judge call, as a new planner step's does (`enginehost.stepRun.endDoneReturn`). A turn whose context ends when `orreryd` stops writes nothing, and it stays running (`planner.Runner.await`, `planner.rowLimited`, `planner.Runner.release`, `planner.Runner.failTurn`, `planner.Runner.freeTurn`).

**Notices.** The planner's attention loop tells the owner when an epic needs them. `planner.NewAttention` runs beside the interpreter, under the same flow context, and starts when `orreryd` has the store open, `--sessions` and a router (`cmd/orreryd.sessionHost.startEvidence`, `cmd/orreryd.sessionHost.startFlow`; `planner.NewAttention`). It passes at once and then once per tick, 5 seconds by default (`planner.Attention.Run`; `planner.defaultTick`).

Each pass reads what each epic with a turn needs, in this order, and the first that holds is the need (`planner.needOf`; `store.Store.Attention`, `store.Store.attention`):

- `park`: the epic is parked.
- `limit`: the epic is paused, and its latest ended turn is `free` as a usage limit.
- `questions`: the epic has open questions.
- `tell`: the epic's newest tell is newer than its latest ended turn, or than any turn when none has ended.
- `none`: otherwise.

For an epic with the need `none`, the pass makes a seen write for the epic's key at the pass time, which clears a notice still out (`planner.Attention.attend`). For any epic whose latest ended turn is not told, the pass sends one notice when `tellsOwner` names its need: `park`, `limit`, or `questions` when that turn succeeded and asked at least one question (`planner.Attention.attend`, `planner.tellsOwner`). The notice's id is `turnNoticeID`, `<epic>:turn:<seq>:<need>`, and it is routed only when no stored epic notice carries that id (`planner.Attention.storedIDs`, `planner.turnNoticeID`). Then it marks that turn told (`planner.Attention.attend`; `store.Store.MarkTold`). The notice carries the turn's told mark, and the first write of its row applies the mark in that same write. A restart after the write finds the turn told, so the attention pass never creates the notice again, whether the row is still pending or has ended. The router restores a stored row as it restores any row (`planner.Attention.attend`, `planner.Attention.notify`; `route.Router.Notify`; `route.rowOf`; `store.Store.PutNotice`). The pass also marks the turn told after the route, as a separate write (`planner.Attention.attend`; `store.Store.MarkTold`). One window is left. A first row write that fails is logged, and a kill before the separate mark lands leaves no row and an untold turn, so the restart routes the notice again (`route.Router.putLocked`).

**Tells.** Each untold tell of an epic is routed as a notice of its own, whose body is the tell's text. A tell that belongs to a park question has that question's head, `Qn park:`, and its advice lines after its text (`planner.tellBody`, `planner.adviceHead`, `planner.adviceLines`; see **Park questions** above). The tells come in seq order, and each is routed for every epic with a turn, whatever its need (`planner.Attention.attend`). Its id is `tellNoticeID`, `<epic>:tell:<seq>`, and it is routed only when no stored epic notice carries that id. The tell's notice carries the tell's mark, which the first write of its row applies, so a restart finds the tell told once its row is written. The pass also marks the tell told after its route, as a separate write, and a tell whose notice is stored is marked without a second route (`planner.Attention.tell`, `planner.tellNoticeID`; `store.untoldTellsOf`, `store.Store.MarkTellTold`). A `tell` need sends no notice of its own. The latest turn is announced only for the needs that `tellsOwner` names (`planner.tellsOwner`).

The notice is keyed by its epic, is in state `waiting`, and is routed by the rules of [Notice routing](#notice-routing) with no watch and no judge (`planner.Attention.notify`, `planner.attentionNotice`; `route.Router.decide`). Its desk notice has the epic's id as its title and the body as its body. Its push has the epic's title as `project` and the body as `summary` (`planner.attentionNotice`). The body is `parked: <reason>`, or `usage limit, resets <time>` with the time in RFC 3339 UTC or, when the limit names no reset time, `usage limit, reset time unknown`, or the open questions. A park's body adds the head `Qn park:` of its open park question, then that question's advice lines, when it has one. Each open question gives its head, `Qn <label>: <text>`, then its advice lines. The advice lines are each declared choice as `  <id>: <does>`, then `  other: answer in your own words`, then `  recommended: <id> (<confidence>): <why>` when the question recommends one (`planner.bodyOf`, `planner.parkBody`, `planner.noticeHead`, `planner.adviceHead`, `planner.adviceLines`). A question stored before migration `00039_questions.sql` has no label, so its head is `Qn: <text>`, and its advice is the `other` line alone (`planner.noticeHead`). Its stored source is the notice's id, the epic's id, title and body as JSON, and a restart rebuilds the same notice from it (`planner.RestoreNotice`).

The notice clears when the epic needs nothing: when its last open question is answered, when `ResumeEpic` clears a limit pause, or when `RetryEpic` ends a park, as the seen write of the next pass gives (see [Notice routing](#notice-routing) and [orreryd](#orreryd)).

#### Evidence

An evidence item shows that a Requirement or a planned task holds. Its evidence is either a capture that `orreryd` runs or files that the agent supplied, which `orreryd` reads from the commit and marks `supplied`, and an agent judges the kept output against the item's conclusion. `orreryd` runs each evidence run in the background, and the runs and their bytes are kept, never pruned. The verdict comes only from the judge step's end (`enginehost.Evidence.recordVerdict`).

The Evidence service is `orrery.engine.v1.Evidence` (`proto/orrery/engine/v1/epic.proto: Evidence`). It has three calls, `RunEvidence`, `ListEvidence` and `GetEvidenceArtifact`, on the session socket beside `Epics` (`enginehost.Serve`). Its consumers are the `orrery evidence` subcommands (see [orrery](#orrery)), the `evidence_*` tools of `orrery mcp` (see [Step and epic tools](engine-protocol.md#14-step-and-epic-tools)), and `serve`, which merges each Requirement's runs into `Epics.Get` and answers artifacts from `GetEvidenceArtifact` (see [Epics and Steps](#epics-and-steps)). The service answers with these Connect codes, and `unavailable` with `evidence unavailable` when orreryd has no runner (`enginehost.evidenceService.ready`):

| Connect code | When | Message |
|---|---|---|
| `invalid_argument` | `RunEvidence` with an empty `epic`, `item`, `revision` or `phase`, in that order | `<field>: empty` (`enginehost.evidenceServiceCheckRun`) |
| `invalid_argument` | `RunEvidence` refused for its item, phase or revision | `evidence run is refused: <reason>` (`enginehost.ErrEvidenceRefused`, `enginehost.Evidence.Run`, `enginehost.evidenceRunItemOf`, `enginehost.evidenceRunRequirementItem`; `enginehost.evidenceServiceRunError`) |
| `not_found` | `RunEvidence` of an epic that is not stored | `epic: no epic <id>` (`enginehost.evidenceServiceRunError`) |
| `invalid_argument` | `ListEvidence` with an empty `epic`; `GetEvidenceArtifact` with an empty `run` | `epic: empty`, `run: empty` (`enginehost.evidenceService.ListEvidence`, `enginehost.evidenceService.GetEvidenceArtifact`) |
| `not_found` | `GetEvidenceArtifact` of a run that is not stored, or of a name the run does not keep | `run: no run <id>`, or `name: no kept artifact <name>` (`enginehost.evidenceService.GetEvidenceArtifact`) |
| `internal` | a store or blob failure | the error's text (`enginehost.evidenceServiceStoreError`) |

**The item.** An item is an `evidence.Item` (`evidence.Item`). Its fields are `capture`, the argv of the capture; `limit`, the capture's limit in seconds; `artifacts`, the repository paths the capture keeps; `supplied`, the repository files the agent supplied instead, read from the commit; `conclusion`, what the evidence must show; `judge`, the agent that judges it; `kind`, how the item shows its evidence; and `tests`, the test files of a test item. `evidence.Item.Kind` and `evidence.Item.Tests` hold them, and both are left out of the stored form when empty (`evidence.Item`). `evidence.Check` returns one line per refusal, in field order (`evidence.Check`):

- Exactly one of `capture` and `supplied` is set. Neither gives `capture: none, name a capture or supplied files`, and both give `supplied: set beside a capture` (`evidence.captureLines`, `evidence.suppliedLines`).
- Each `capture` element is non-empty, and `capture[0]` contains a `/`, since it is never looked up on `PATH` (`evidence.captureLines`).
- A `limit` is set only with a capture, and a capture's limit is a whole number of seconds from 1 to the largest duration (`evidence.limitLines`).
- `artifacts` are set only with a capture, and `supplied` only without one. Each path of either list is a normalized repository-relative file path, is not inside a `.git` directory at any depth or case, and is not named twice in its list (`evidence.artifactLines`, `evidence.suppliedLines`, `evidence.pathLines`, `evidence.fileFormOK`, `evidence.inGitDir`).
- `conclusion` and `judge` are not blank after trimming white space (`evidence.Check`).
- `kind` is `test`, `capture` or `supplied` (`evidence.KindTest`, `evidence.KindCapture`, `evidence.KindSupplied`). A `test` item runs its capture beside the test files in `tests`, a `capture` item runs a capture alone, and a `supplied` item is files a person supplies. No kind is refused as `kind: none, name test, capture or supplied`, and another kind as `kind: "<kind>" is not test, capture or supplied`. A `test` or `capture` item without a capture is refused as `kind: test without a capture` or `kind: capture without a capture`, and a `supplied` item without supplied files as `kind: supplied without supplied files` (`evidence.kindLines`).
- `tests` is set only on a `test` item, and a `test` item lists at least one test file. Otherwise the refusals are `tests: none, a test item lists its test files`, `tests: set on a capture item` and `tests: set on a supplied item`. Each element is a path that is checked as `artifacts` and `supplied` are, and a bad one is `tests[j]: <reason>` (`evidence.testsLines`, `evidence.pathLines`).

Stored items are not checked. The store checks no item, and `evidence.Check` runs only in `enginehost.checkEpicRequirementItem` and `planner.checkTask`, so an item stored before kinds keeps the fields it was stored with. Migration `00037_assertions.sql` sets the kind of a stored item that has a capture or supplied files (see [Epics in the store](#epics-in-the-store)). An item with neither stays kindless and reads back as stored (`store.decodeItem`; `internal/store/migrations/00037_assertions.sql: UPDATE epic_requirements`).

**Phases and passing.** A run is for one phase, `red` or `green` (`evidence.PhaseRed`, `evidence.PhaseGreen`). A run whose capture captured nothing passes neither phase. Red passes when the verdict is `fails`, and green passes when the verdict is `holds` and the capture exited 0 (`evidence.Passed`).

**Runs.** `RunEvidence` names an item by `R<n>`, a Requirement's id, which must carry an item, and refuses any other name (`enginehost.evidenceRunItemOf`, `enginehost.evidenceRunRequirementItem`). It resolves the revision to a commit, and it stores the run in state `capturing`, with its item, phase, revision and source, before anything runs. A run's source is `captured` when its item names a capture, and `supplied` otherwise (`enginehost.Evidence.Run`, `enginehost.evidenceRunSource`). The drive then runs in the background, under a context that `Close` cancels (`enginehost.Evidence.start`).

**Task runs.** `Evidence.RunTask` runs a task's evidence for its build. Its run's item is `T<seq>`, its spec is the item the task names, and its id is the caller's, which the build derives (see [Task builds](#task-builds)). An id that is not 32 lowercase hex digits, a task the epic does not have, a task whose item is the zero value, and a phase other than `red` or `green` are refused with an error matching `ErrEvidenceRefused`, and store no run. An id that is already stored returns that run and starts nothing, whatever the other arguments are. Otherwise the revision is resolved, and the run is stored and started as `RunEvidence` starts it (`enginehost.Evidence.RunTask`). `RunTask` is a Go call. The Evidence service does not carry it.

**Current runs.** A run counts only while its kept spec is current with the item now stored under its Item, which holds when the two encode to the same JSON (`evidence.Current`). `store.currentRuns` applies this when `store.Store.EpicFlow` reads an epic, so `EpicFlow.Evidence` holds only current runs, and a run whose item names nothing stored is not current. A Requirement's run is named by its id and a task's run by `T<seq>` (`store.currentRuns`, `store.Store.EpicFlow`). `store.taskOf` leaves `EpicTask.Red` and `EpicTask.Green` nil when their run is not current (`store.taskOf`). `store.Store.EvidenceRuns` and `store.Store.EvidenceRun` return every run, current or not (`store.Store.EvidenceRuns`, `store.Store.EvidenceRun`). The interpreter reads its rows from `EpicFlow` (`interpret.Runner.decide`), so `kinds.Evidence.Decide`, `kinds.GreenAt`, `kinds.Unbuilt` and `kinds.MetAtBase` read a stale run as missing. A Requirement whose red run at the base is stale is therefore named by `kinds.Unbuilt` as `R<n> has no red at the epic's base <base>` (see **Built or park** under [Workflows](#workflows)). The `state` that `epic_get` reports for a Requirement reads the current runs as well (`enginehost.requirementState`; see [Epics](#epics)).

**Captured runs.** A capture runs at the run's revision in a detached worktree at `evidence-worktrees/<run id>` beside the store file (`enginehost.evidenceCapture.run`; `enginehost.NewEvidence`; `cmd/orreryd.startSessions`). It queues for one member of the `gate` pool, under the pool files of that worktree, and its gate runs in a holder named `evidence-<run id>` (`enginehost.evidenceCapture.captureRequest`, `enginehost.evidenceCapture.captureGate`; `gates.DefaultPool`). Its limit is the item's limit, and each of its stdout and stderr tails is 1 MiB (`enginehost.evidenceCaptureLogTail`, `enginehost.evidenceCapture.captureGate`). The grant and the holder are stored once the grant holds, before the gate launches (`enginehost.Evidence.capture`; `enginehost.evidenceCapture.run`).

When the gate ends, the run keeps its exit status and whether it timed out, its stdout and stderr logs, and its artifacts (`enginehost.evidenceCapture.end`):

- Each log is the newest 1 MiB of one of the capture's two streams, stored as `<sha256>.log`. The stdout log's hash and size are `log_hash` and `log_bytes`, and the stderr log's are `stderr_hash` and `stderr_bytes` (`enginehost.evidenceCapture.end`, `enginehost.evidenceCapture.keepLog`). An empty stream stores no log.
- An artifact is read through an `os.Root` of the worktree. A regular file up to 10 MiB is kept as a blob, with its hash and size. A larger file is `over_cap`, with its size recorded and nothing stored. Anything else at the path is `missing`. A symbolic link is not a regular file, so it is never followed (`enginehost.evidenceCapture.keepArtifacts`, `enginehost.evidenceCapture.keepArtifact`, `enginehost.captureRegularFile`; `enginehost.evidenceCaptureArtifactCap`).

The worktree and the gate's holder directory are removed on every path (`enginehost.evidenceCapture.end`).

**Not captured.** A capture that captured nothing records one of four reasons, and the run then ends with no judge. `could not start: <error>` is for a worktree, grant or gate that did not start (`enginehost.captureNotStarted`). `killed at its limit` is for a gate the limit stopped (`enginehost.evidenceCapture.end`). `could not finish: <error>` is for what the gate left that could not be kept or cleaned up, or a run whose context ended after its gate launched (`enginehost.evidenceCapture.run`, `enginehost.captureNotFinished`). `lost at a restart` is for a gate that a restart could not re-adopt (`enginehost.evidenceCapture.readoptLost`).

**Supplied runs.** A supplied run reads each file of `supplied` from the commit at its revision with `git cat-file`, never from the working tree (`enginehost.readSupplied`, `enginehost.keepSupplied`). A path the commit holds no blob for is `missing`. A blob over 10 MiB is `over_cap`, and any other blob is kept under its sha256 (`enginehost.evidenceMaxKeptBytes`, `enginehost.keepSupplied`). The run's exit status is 0 only when every file is kept, and 1 otherwise (`enginehost.readSupplied`). A revision that does not resolve is `could not start: <error>`, and a file that cannot be read is `could not keep <file>`, each recorded as not captured. Nothing is written to the repository (`enginehost.readSupplied`).

**The judge.** A run whose evidence was captured or read, with no not-captured reason, is judged by a judge step. The step is read-only, runs at the run's revision in the epic's repository, and is labelled with the epic and the task `evidence-<run id>`. Its step id is `evidenceJudgeID`, the first 32 hex digits of the lowercase SHA-256 of `orrery-evidence-judge`, the run id and the judge number, joined with NUL bytes, and `startJudge` starts the judge under that id (`enginehost.evidenceJudgeNamespace`, `enginehost.evidenceJudgeIDLen`, `enginehost.evidenceJudgeID`; `enginehost.Evidence.startJudge`). Its agent gate is `evidence_judge`, given as data (see [Agent gates](#agent-gates)). Its agent is the item's judge, it has one `must` assertion, which is the conclusion, and its limit is 20 minutes (`enginehost.evidenceJudgeGate`, `enginehost.evidenceJudgeLimit`, `enginehost.judgeGate`, `enginehost.judgeStart`). Its prompt names the run's capture command as a JSON array of its argv, on a `Command:` line before the exit status and whether it timed out. A supplied run has no capture, so its prompt names no command. The prompt then names the blob of each log it has, labelled `Stdout log` or `Stderr log`, and the blob of each artifact, with `missing` for one not kept and `over the 10 MiB cap` for one over it. It never names the phase, and it tells the judge to read only the listed files, with repository files as reference only (`enginehost.judgePrompt`, with the command at `enginehost.judgePrompt`, and `enginehost.judgeArtifactWhere`).

The verdict follows the step's end. A step that ends `done` holds the conclusion. A step that ends `gate_failed` whose kept return's first result is `fails` fails it. Any other end gives no verdict, and its reason is the end, followed by the step's reason when it has one (`enginehost.judgeVerdict`; see [Step lifecycle](#step-lifecycle) for the ends).

A run with no verdict is judged again on the same kept output, up to three judge steps in all, and the third end is the run's reason (`enginehost.evidenceJudgeTries`, `enginehost.Evidence.judgeRun`). The store counts the judges of a run (`internal/store/queries.sql: SetEvidenceRunJudge`). Each judge number has its own id, so a judge that a restart finds under the next number is not started again (see Restart below). The verdict, its reason and whether the run passes are recorded once, while the run is judging (`enginehost.Evidence.recordVerdict`; `store.Store.RecordEvidenceVerdict`).

**Restart.** `orreryd` resumes the open runs after its step reconcile and before its planner starts (`cmd/orreryd.startSessions`, `cmd/orreryd.sessionHost.startEvidence`; `enginehost.Evidence.Resume`). A judging run waits for the judge step it holds. A judge step stored under the id of the run's next judge number is adopted and counted as that judge, so a judge that a restart finds is waited for and not started again. Otherwise the run starts its next judge (`enginehost.Evidence.readoptJudge`, `enginehost.Evidence.readoptJudgeStep`). A capturing run with a stored grant re-adopts its gate, and a gate that cannot be re-adopted is `lost at a restart` (`enginehost.Evidence.readoptCapture`; `enginehost.evidenceCapture.readopt`, `enginehost.evidenceCapture.readoptRequest`, `enginehost.evidenceCapture.readoptLost`). A capturing run with no stored grant starts its capture again, after its worktree is removed (`enginehost.Evidence.readoptUnlaunched`). The judge step is re-adopted with its gate rebuilt from the run's stored item (`enginehost.EvidenceReadoptGate`; see [Steps](#steps)).

A shutdown under `--holder-scope` detaches each running capture gate, which then keeps running under its holder for the next start. Without that flag, the shutdown kills the gate. A detached gate records nothing, and the next start re-adopts it. A capture that ended before or during the shutdown is recorded all the same, and its run is left judging with no judge step for the next start to judge. A capture cut short before its gate launched, and a supplied read cut short, record nothing, so the next start runs them again (`enginehost.Evidence.Close`, `enginehost.Evidence.drive`, `enginehost.Evidence.capture`; `enginehost.Evidence.readoptCapture`; `enginehost.evidenceCapture.run`; `cmd/orreryd.sessionHost.shutdown`).

**Storage.** A run and its artifacts are rows of `evidence_runs` and `evidence_artifacts` (see [Epics in the store](#epics-in-the-store)). Each write after a run is added needs the state the run is in, and any other state is `ErrEvidenceState`, so each record lands once (`store.Store.AddEvidenceRun`, `store.Store.SetEvidenceGrant`, `store.Store.RecordEvidenceCapture`, `store.Store.SetEvidenceJudge`, `store.Store.RecordEvidenceVerdict`). Bytes are files named by their sha256 under `evidence/blobs` beside the store file. The directory is created at mode 0700, each name takes its extension lowercased when it is up to eight characters of `[a-z0-9]`, and no file is pruned (`enginehost.evidenceDirMode`, `enginehost.evidenceHashLen`, `enginehost.evidenceMaxExt`, `enginehost.evidenceExtChars`, `enginehost.errBlobName`, `enginehost.evidenceBlobs`, `enginehost.evidenceExt`, `enginehost.evidenceBlobs.put`; `enginehost.NewEvidence`; `cmd/orreryd.startSessions`).

#### Task builds

An epic's open tasks are built by the build Runner (`internal/build`) while the epic has a base and its phase is not ended (`kinds.Build.Decide`, `kinds.Ended`), which the interpreter drives with `--sessions` and an open store (see [orreryd](#orreryd) and **Running**). A task's build runs its units in order: red at its base, one implementer step, green at the implementer's commit, one review step for each review gate of the epic (its enabled task-stage reviewers, see [Reviewers](#reviewers)), and one verifier step for each finding a review raises, unless the reviewer has verify off. The next action is decided from the task's stored build, its step or evidence run, and its epic (`build.StageRed`, `build.StageImplement`, `build.StageGreen`, `build.StageReview`, `build.StageVerify`; `build.Decide`). A write to a task's build is compare-and-set: it lands only while the task is `open` and its stored build is the one the pass read (`store.Store.SetTaskBuild`; `store.moveTask`).

**Passes.** The interpreter gives the build the task seqs of its build child runs that run, through `PassTasks`, once a decision has no structural action (`interpret.Runner.deliver`; `build.Runner.PassTasks`). `PassTasks` ensures the epic's branch, then carries out the actions `Decide` gives each of those open tasks, one at a time. It reads the epic again after each action that changed something, up to eight actions for a task (`build.maxActions`, `build.Runner.passEpic`, `build.Runner.passTask`). An action that changes nothing ends the task's pass for that interpreter pass. A failed read of a task's view is logged, and so is a failed write; the next pass tries the same action again (`build.Runner.passTask`; `build.Runner.report`, `build.Runner.written`). `Run`, the build's own loop, passes at once and then once per tick, five seconds by default (`build.Runner.Run`; `build.defaultTick`), and `orreryd` does not start it.

**Children.** A task's build is a child run of the plan node run that planned it (see **Running**). `CompleteTurn` writes a running child in the turn's write for each task it stores, under the first build node of the epic's running plan run, with the path `<plan path>/<build id>/T<seq>` and the cause `turn <turn> task <position>` (`store.Store.CompleteTurn`, `store.planFanOutOf`, `store.insertTasks`). The pass also starts a child for each open task whose turn is above its plan run's mark and that has none, so a task planned before the plan node ran gets one (`interpret.pass.startTasks`). A child runs while its task is open on an epic that has a base and is accepted, waits while the epic lacks either, and ends once its task is no longer open (`kinds.Build.Decide`).

**Branch.** The epic's branch `epic/<slug>` is created once, at the epic's base, with `update-ref` and an empty old value, so an existing branch is never moved (`build.CreateBranch`; `build.BranchRef`; `build.Runner.create`). Its state, `''`, `creating` or `created`, moves from `creating` to `created` around the create (`build.Runner.create`, `build.Runner.markCreated`; `store.Store.SetEpicBranch`). `CreateBranch` creates the branch. Afterwards it moves only in an integrate batch's advance, from the stored base to the batch's candidate, which is the one write orreryd makes to it after the create (see **Batch integration** under [Workflows](#workflows); `build.Runner.create`, `interpret.Runner.advance`). A paused or parked epic with no created branch creates nothing (`build.Runner.branchTip`). When the state is `''` or `creating` and the branch exists, the branch is adopted only if the state is `creating`, the branch still names the base, and no worktree has it checked out. Otherwise the epic parks with `epic/<slug> exists` (`build.Runner.branchTip`, `build.Runner.adopt`). A create that fails parks the epic with `epic/<slug> exists: <error>` (`build.Runner.create`). A `created` epic whose branch is gone parks with `epic/<slug> is gone`, and a parked epic is not parked again, so the owner is told once (`build.Runner.createdTip`).

**Starts.** A task with no stage starts at red once the epic is neither paused nor parked and its branch has a tip. Its `base` is the epic's candidate when the epic has one, so a red batch's fixes start from it, and the branch tip otherwise (`build.decideFresh`, `build.Runner.view`; see **Batch integration** under [Workflows](#workflows)). A paused or parked epic starts nothing new. The implementer's step runs the task's command gate, each review step runs its reviewer's agent gate, and a verifier step names no gate. The red and green runs are evidence runs (`build.decideRed`, `build.decideImplement`; `build.decideGreen`, `build.decideReview`, `build.decideVerify`). Accepting, failing, pinning, checking ref moves and storing a finding are not starts, so they run while the epic is paused or parked (`build.decideReview`, `build.settledFindings`; `build.decideImplement`). A pause does not stop a running step. The step runs to its end, and the build acts on that end (`store.Store.PauseEpic`).

**Units.**

- Red (`build.decideRed`). The unit's evidence run is `RunID(epic, seq, red, 0, rerun)` at the base, started when the unit has no run (`build.decideRed`; `build.RunID`). A run that is not captured fails the task with `red not captured: <reason>`. A run with no verdict is a no-verdict. A run whose conclusion holds at the base fails the task with `red: the conclusion holds at the base`. A run that passes moves the build to implement at attempt 1 and stores the run as `red_run` (`build.decideRed`).
- Implement (`build.decideImplement`). The implementer step is `StepID(epic, seq, implement, attempt, rerun, 0, 0)`. Before it starts, the refs are snapshotted (see **Ref moves**). The step is a writing step in a worktree under the task's directory, detached at the base. It runs the `implementer` agent of the role file with the brief, which carries the end of the red run's stdout log and, when the red run has a stderr log, the end of that too (`build.Brief`, `build.Runner.redEvidence`), the task's files as its owned paths, and the command gate the epic names for the task, which an agent gate fails (`build.Runner.implementerStart`; `build.ResolveGate`; `build.Runner.snapshotRefs`).
- Green (`build.decideGreen`). The evidence run is `RunID(epic, seq, green, attempt, rerun)` at the implementer's commit. A run that is not captured or not met spends the attempt. A run with no verdict is a no-verdict. A met run is stored as `green_run`, and the build moves to review (`build.decideGreen`).
- Review (`build.decideReview`). For each review gate, in the order the epic names them, the review step is `StepID(epic, seq, review, attempt, rerun, gate_pos, 0)`: a read-only step at the commit, in the epic's repository, that runs the reviewer's agent gate (`build.Runner.reviewStart`). A model reviewer runs its inline agent, whose definition the step stores and passes again at a respawn. An agent reviewer runs the agent it names, and its prompt, when it has one, heads the review prompt. The step's assertions and limit are the gate's (see [Reviewers](#reviewers) and [Agent gates](#agent-gates)). Past the last gate, the task is accepted as `reviewed`, or as `unreviewed` when the epic has no review gates, which is when no task-stage reviewer is enabled (`build.decideReview`).
- Verify (`build.decideVerify`). For each finding of the review, the verifier step is `StepID(epic, seq, verify, attempt, rerun, gate_pos, finding_pos)`: a read-only step of the verifier agent on that one finding (`build.Runner.verifierStart`; `build.VerifierCheck`). Once every finding is verified, any confirmed finding spends the attempt. Otherwise the build moves to the next gate (`build.settledFindings`). The reviewer's `verify` key is the switch. With it off, `build.decideReview` stores the review's findings as confirmed and marked unverified, no verifier step starts, and the attempt is spent, since a confirmed finding spends it (`build.noVerify`, `build.settleUnverified`). `build.UnverifiedFindings` gives the same settlement for an epic-stage verdict. No epic-stage review runs at this base, so nothing calls it.

**Derived ids.** `StepID` is the first 32 hex digits of the SHA-256 of `orrery-build`, the epic, `T<seq>`, the unit, and the attempt, rerun, gate position and finding position, joined with NUL bytes. `RunID` hashes the same kind of parts for a run, with the phase in place of the unit and no gate or finding (`build.idNamespace`, `build.idLength`, `build.taskDirLength`, `build.StepID`, `build.RunID`, `build.digest`). The same inputs give the same id. The step id is `StepStart.ID`, which the Steps refuse when a stored step has the id or a start holds it (`enginehost.StepStart`; `enginehost.Steps.startID`). The run id is the id `RunTask` stores the run under (`enginehost.Evidence.RunTask`). The build finds a unit's step or run by its derived id before it starts one, so a step that started before the build recorded its id is found, not started again (`build.Runner.view`, `build.unitIDs`). The build writes the step's id into the task's `step` after the start (`build.Runner.startUnit`).

**Attempts and reruns.** The build is on attempt 1 once red passes, and attempt 2 is the last (`build.lastAttempt`; `build.decideRed`). A unit that spends the attempt on attempt 1 moves the build to implement at attempt 2, with the reason as `failure`, and clears the step, the revision, the refs and the green run. The red run is kept. A spend on attempt 2 fails the task (`build.spend`). `rerun` counts the restarts and reruns of the current unit. It is part of each unit's id, so a rerun is a unit under a new id. Reruns and restarts spend no attempt (`build.noVerdict`; `build.restartUnit`; `build.releaseHold`). A unit that gives no verdict reruns, and its second in a row parks the task with `T<seq> <unit> gave no verdict twice: <reason>` (`build.noVerdict`).

**Step ends.** The build acts on a unit's step as the table gives. The refs check comes first, for an implementer step with a snapshot open (`build.decideImplement`).

| Unit | End | Action |
|---|---|---|
| implement | any end, with a refs snapshot open | the ref moves are checked first (see **Ref moves**) |
| implement | `done` or `goal_unmet` | the build moves to green at the commit the return names, or at the worktree's HEAD when it names none. A return that does not parse gives no verdict (`build.decideImplement`) |
| implement | `returned` | the task fails with `returned <status>: <summary>`, with no second attempt. A return that does not parse gives no verdict (`build.decideImplement`) |
| implement | `stopped` | the build holds `stopped`, and the epic is paused (`build.holdStopped`; `build.Runner.execute`) |
| implement | `engine_restarted` | the unit reruns (`build.restartUnit`) |
| implement | any other end | the attempt is spent with `<end>: <reason>` (`build.decideImplement`; `build.endText`) |
| review | `done` | the review holds, and the build moves to the next gate (`build.decideReview`) |
| review | `gate_failed`, with status `done`, no `undetermined` result, and at least one finding | the review fails and stores its findings, and the build moves to verify; with verify off they are stored confirmed and unverified first (`build.decideReview`) |
| review | `stopped` or `engine_restarted` | as for implement (`build.decideReview`) |
| review | any other end | no verdict, with the unit `review <gate>` (`build.decideReview`) |
| verify | `done` with at least one result | the first result is stored: `confirmed` or `dropped` as given, anything else `undecided` (`build.decideVerify`, `build.verifiedResult`) |
| verify | `returned` | stored as `undecided`, with the reason `blocked: <reason>` (`build.decideVerify`) |
| verify | `stopped` or `engine_restarted` | as for implement (`build.decideVerify`) |
| verify | any other end, or `done` with no result | no verdict, with the unit `verify <finding id>` (`build.decideVerify`) |

**Evidence run ends.** A red or green run is judged as any evidence run is (see [Evidence](#evidence)). A run that is not captured fails red with `red not captured: <reason>`, and spends the green attempt with `green not captured: <reason>`. A run with no verdict is a no-verdict, with the run's judge reason. A red run that passes moves the build to implement, and one that fails the task with `red: the conclusion holds at the base`. A green run that is met moves the build to review. One that is not met spends the attempt with `green not met: exit <n>, verdict <v>` (`build.decideRed`; `build.decideGreen`).

**Start errors.** A start the step cap refuses (`ErrCapReached`) writes nothing, and the next pass tries it again (`build.Runner.startUnit`). A role file error, which is a role that names no agent, parks the task held `parked` and the epic with the error's text (`build.RoleError`, `build.RoleError.Error`, `build.RoleError.Unwrap`; `build.Runner.startUnit`). A red or green run that the evidence refuses (`ErrEvidenceRefused`), and any other start error of a step, are decided by `DecideStartError`. Red fails with `red not captured: <error>`, green spends the attempt with `green not captured: <error>`, and any other unit fails with `<unit> did not start: <error>` (`build.DecideStartError`; `build.Runner.startRun`, `build.Runner.fail`). Any other evidence start error is logged and tried again on the next pass (`build.Runner.startRun`).

**Holds.** A build holds `stopped` when its step ends `stopped`. It is released when the epic is neither paused nor parked, and the unit reruns (`build.Decide`, `build.releaseHold`). A build holds `parked` when a role file error occurs at start, or a unit gives no verdict twice. It is released when the epic is no longer parked, and its no-verdict count starts again at zero (`build.Decide`; `build.Runner.startUnit`; `build.noVerdict`). An owner releases a stopped hold by resuming the epic, and a parked hold by retrying it (see [Epics](#epics)).

**Ref moves.** Before an implementer step starts, the build records a snapshot of the primary checkout's HEAD, its branch and its commit, and of every ref of the epic's repository, except `refs/orrery/` and the branch of each epic in that repository (`build.Runner.snapshotRefs`; `build.snapshotOf`; `build.Snapshot`). When the step ends, the build compares the snapshot with a new one. Each ref added, removed or changed is a move, and HEAD is one more move when its branch or commit changed (`build.Runner.refMoves`; `build.Compare`). A move parks when it changes `refs/heads/main`, the branch the primary checkout had when the snapshot was taken, or HEAD (`build.Compare`). The first parking move parks the task and the epic with `T<seq> implementer moved <ref> (<from> to <to>)`. Each move that does not park is a tell with the same text, and the move's own write carries its tells. When a move parks, the write is `ParkTask`, which parks the epic with the first parking move's text and then adds the other moves' tells. When no move parks, the write is `SetTaskBuildTelling`, which stores the build and adds each tell (`build.implementerMoves`; `build.Runner.checkRefs`; `store.Store.SetTaskBuildTelling`, `store.Store.ParkTask`).

**Failed and accepted tasks.** A failed task gets the status `failed` and its `failure`, the reason cut to 1024 bytes. A pending trigger `T<seq>-failed` with the cause `task T<seq> failed: <reason>` is added, so the planner plans the epic again (`store.Store.FailTask`; `store.TurnReasonMax`; `store.capTurnText`). An accepted task gets the status `done` and its `review`. Before that, its green commit is pinned at `refs/orrery/<slug>/T<seq>` (`build.Runner.accept`; `build.Pin`; `store.Store.AcceptTask`). A pin that fails leaves the task open, and the next pass tries again (`build.Runner.accept`). A failed or accepted task then has its worktrees removed (`build.Runner.fail`, `build.Runner.accept`, `build.Runner.removeWorktrees`).

**Tells.** A tell is text the owner is told as an epic notice (see [Planner turns](#planner-turns)). The build writes one for each move that does not park, in the move's own write (see **Ref moves**). A park adds its reason as a tell in the write that parks the epic: a parking move, a second no-verdict, a role error at start, and each branch park. `ParkTask` writes the park, the reason tell and the tells after it in one write, and `ParkEpic` writes the park with its reason tell (`store.Store.ParkTask`; `store.Store.ParkEpic`; `store.parkWithTell`; `build.Runner.createdTip`, `build.Runner.adopt`, `build.Runner.create`, `build.Runner.markCreated`, `build.Runner.park`). A failure is not a tell.

**Worktrees.** The task's directory, `TaskDir`, is `<state dir>/task-worktrees/<first 16 hex digits of the SHA-256 of epic NUL "T<seq>">`. An implementer step's worktree is `<TaskDir>/<step id>`. It is added detached at the base (`build.TaskDir`; `build.Runner.implementerStart`; `build.AddWorktree`). A read-only step runs in a detached worktree at its revision, which the step removes when it ends (`enginehost.StepStart`). Failing or accepting a task removes every worktree under its directory, and then the directory. A removal that fails is logged, and the task keeps its state (`build.RemoveWorktrees`; `build.Runner.removeWorktrees`).

**Restart.** At start, `orreryd` runs `Steps.Readopt` and `Steps.Reconcile` as [Restart](#step-lifecycle) gives, and the build's first pass comes with the interpreter's first pass, which follows the planner's recovery of its turns (`interpret.Runner.Run`). An open step that is not re-adopted ends `engine_restarted`, and its unit reruns (`build.decideReview`, `build.decideVerify`; `build.decideImplement`). A review step is re-adopted only when `ReadoptGate` rebuilds its agent gate from its epic, and a verifier step with `ReadoptCheck`, which gives it its return check (`build.ReadoptCheck`, `build.ReadoptGate`; `cmd/orreryd.startSessions`, `cmd/orreryd.readoptCheck`, `cmd/orreryd.readoptGate`).

**Wiring.** `startFlow` builds the Runner over the store, the step service and the evidence runner, with the state directory and the environment that roles and gates read, and gives it to the interpreter as its builder (`cmd/orreryd.sessionHost.startFlow`; `build.Config`). Steps start through the Steps service (`build.Runner.startStep`), and evidence runs through the evidence runner (`build.Runner.startRun`).

#### Pools

`orreryd` serves the `orrery.engine.v1.Pools` service on the session socket, beside `Engine`, `Steps` and `Epics`, with the same socket mode and Connect rules. It is not on `serve` itself, but `serve` forwards three of its calls as the phone's `orrery.v1.Pools` service (see [Phone API](#phone-api)). The service has four calls:

- `Acquire` asks for members. Its response stream sends the request's place in line, then its grant, and a `stop` event if a hold stops the grant.
- `Status` reports the pools in use and the owner holds (see Status below).
- `Hold` and `Free` place and remove an owner hold (see [Owner holds](#owner-holds)).

How members are chosen and granted is in [Pool members](#pool-members).

`Acquire` takes these fields:

| Field | Meaning |
|---|---|
| `project` | the project's absolute path, as [Pool files](#pool-files) names it |
| `pools` | the pool definitions of the project's merged files. Each limit is in whole seconds |
| `needs` | one entry per pool, with `pool` and `count`. Each `pool` names one of `pools` |
| `client_pid` | the process id of the client that holds the grant |
| `grant_id` | empty for a new request. Set only to reclaim a grant: one that a restarted `orreryd` kept (see Restart below), or one that a hold stopped (see [Owner holds](#owner-holds)) |
| `command` | the argv of the command the grant is for, as `orrery run status` shows it. Empty when the client sends none |

A new request, with no `grant_id`:

- It is checked before it queues. A request that fails a check queues nothing and changes nothing. Its refusal is one of the codes below.
- It queues. The stream sends `queued` with the request's place in the queue, counted from 1. The place is sent once, when the request queues, even when the request is granted at once.
- The stream sends `granted` once the grant is stored. That message has the grant's id, and its members for each pool a need named.
- After `granted`, the stream sends `stop` at most once, when a hold stops the grant. It names the grant's members that are held (see [Owner holds](#owner-holds)).
- A request that waits has no time limit. It waits until it is granted or until its stream ends.

| Connect code | When |
|---|---|
| `failed_precondition` | a needed pool whose host is not `orreryd`'s host; a needed pool defined differently from the pool of that name in use; a needed pool that shares a member with another pool in use |
| `invalid_argument` | a request with no needs; a need for a pool the request does not define; a need whose `count` is below 1, above the pool's cap, or above its member count; a `client_pid` below 1 |
| `not_found` | a `grant_id` that `orreryd` did not keep for that `client_pid` |
| `unavailable` | `orreryd` has no store; its restore at start failed; or `orreryd` is shutting down |
| `internal` | `orreryd` cannot store the grant |

A request with a `grant_id` does not check its needs and pools. Two kinds of grant can be reclaimed:

- A grant that `orreryd` kept at restart, for that `client_pid`. The stream sends `granted` at once, with the grant's id and members.
- A grant that a hold stopped, whose request is waiting for its client, for that `client_pid`. The stream sends `queued` with the request's place in line, then `granted` when the request is granted again, under a new grant id.

Otherwise the call is `not_found`.

A grant lasts while its response stream is open. When the stream ends, because the client closes it or its connection breaks, `orreryd` deletes the grant's rows and frees its members, and then runs the pass of [Pool members](#pool-members) again. A grant that a hold stopped is handled as [Owner holds](#owner-holds) describes.

Status:

`Status` reads the broker and changes nothing. Its answer has these parts:

- `pools` holds each pool in use, sorted by name. A pool is in use while a request queues for it, a grant holds one of its members, or a member command runs for it. Each pool has its `cap`, its `members` in declared order, and its `waiters`.
- Each member has its `name`, its `state` (see [Pool members](#pool-members)), its `holder` when a grant holds it, and `held_since`, the unix seconds of its owner hold, or 0. A held member reports its own state. Plan reads it as `unavailable`, as [Owner holds](#owner-holds) says.
- A `holder` has the grant's `grant_id`, `project`, `client_pid`, `command` (the argv, empty when none was sent), `since` (the unix seconds the grant was stored), and `stopping`, which is true while a hold stops the grant.
- Each waiter has its `position` in the whole queue, counted from 1, its `project`, its `command`, its `count` of members for this pool, and its `since`, the unix seconds it first queued. A requeued request keeps its first `since`.
- `holds` lists every owner hold, sorted by member, with `member` and `since`. It covers holds on members of no pool in use.

The CLI (`orrery run status`), the tree's pools view and the phone's Pools calls show this answer. Their forms are in [orrery](#orrery), [Tree keys](#tree-keys) and [Phone API](#phone-api).

Restart:

- On shutdown, the Pools broker closes before the session socket closes. Each open `Acquire` ends `unavailable`, and each stored grant stays for the next start. Member commands in flight are cancelled.
- At start, `orreryd` restores the holds, then the stored grants, before it serves Pools. A grant whose `client_pid` has no process, and which names no holder that is alive, is ended in the store at once (`grants.Broker.lasts`, `grants.Broker.Restore`). A process is gone when a signal 0 to its pid fails with ESRCH. Each other grant keeps its members, and waits for its client or its holder to reclaim it. Its pools come back into use, so each of their members gets a status run. `orreryd` gives the broker that holder check: a holder is alive while it holds its session lock, `holder.lock` in its directory, which it takes before it starts its child and keeps for its life, so a holder whose child has started before its pids record is written still counts (`cmd/orreryd.sessionHost.startBroker`, `cmd/orreryd.holderLives`). Member commands keep these rules: the ones in flight are cancelled at shutdown, and a pool's members get a status run when the pool comes into use at start (`grants.Broker.Close`, `grants.Broker.useLocked`).
- On each 60-second tick, a kept grant whose client has no process and which names no live holder is ended the same way, and so is a stopped grant's request that waits for its client and names no live holder (`grants.Broker.endDeadLocked`).
- If the restore fails, `orreryd` logs the error, and Pools refuses every call as `unavailable` until `orreryd` restarts. No grant is made from a partly restored broker.

#### Pool members

A pool in use has a state for each member:

| State | Meaning |
|---|---|
| `unknown` | no member command has set the state since the pool came into use. Unknown members are never reserved |
| `asleep` | the last status exited nonzero |
| `awake` | the last status or wake exited 0 |
| `waking` | a wake command is running |
| `admitting` | an admit command is running |
| `admitted` | an admit exited 0, or a status exited 0 while the member was admitted |
| `unavailable` | a command failed to start or passed its limit, or a wake or admit exited nonzero. Only a status that exits 0 leaves this state |

Member commands:

- Each pool's `wake`, `status` and `admit` argv (see [Pool files](#pool-files)) runs the same way. It runs without a shell: the first element is found on the `PATH` of `orreryd`, and the rest are its arguments. It runs in its own process group, with no standard input, and its output is discarded.
- The environment is `orreryd`'s own, plus `ORRERY_POOL=<pool>` and `ORRERY_MEMBER=<member>`. The argv is never changed, so the member is named only by `ORRERY_MEMBER`.
- Each command runs under its limit. A command that passes its limit has its process group killed.
- A member runs at most one command at a time.

Outcomes:

- `status` exits 0: the member is `awake`, or stays `admitted` when it was admitted.
- `status` exits nonzero: the member is `asleep`. An `unavailable` member stays `unavailable`.
- `status` fails to start or passes its limit: `unavailable`.
- `wake` exits 0: `awake`. Any other end: `unavailable`.
- `admit` exits 0: `admitted`. Any other end: `unavailable`.

Status runs:

- Each member gets a status run when a request first brings its pool into use, and when the restore at start brings a pool into use.
- Every 60 seconds, each member with no command running gets a status run.
- A pool that no grant holds, no waiting request needs, and no command runs for is forgotten. When it is used again, its members start at `unknown`.

Grant order:

A change to the broker runs the pass again: a request queues or leaves, a member command ends, or a dead client's grant ends. The pass goes through the waiting requests in arrival order.

1. A request whose project would pass a pool's cap reserves nothing. The project's count for a pool is the members it holds from that pool, plus the members that earlier waiting requests of the same project reserve in this pass, plus the request's own need for that pool.
2. Otherwise, for each need in order, the request reserves free members of the pool that no earlier request reserved. It takes admitted members first, then waking and admitting members, then awake members, then asleep members. Within each state, members keep their declared order. Unknown and unavailable members are never reserved, and a held member is read as unavailable (see [Owner holds](#owner-holds)).
3. A request whose every need is covered by admitted members is granted all its reserved members at once, in need order. Its grant is stored before its client is told.
4. Any other request keeps its reservations, so a later request cannot take them. Each of its reserved awake members is admitted, and each reserved asleep member is woken. A member whose command is still running is left alone. When that command ends, the pass runs again, and a woken member is admitted if a waiting request still needs it.

#### Owner holds

An owner hold keeps a member out of every grant until it is freed. A hold names a member only. It applies to that member wherever the member's pool is defined, and it can name a member of no pool in use.

- `Hold` takes a member name of the form `[A-Za-z0-9][A-Za-z0-9._-]*`. Hold of a member that is already held succeeds and keeps the first hold's time.
- `Free` ends the hold on a member. `Free` of a member that is not held is `not_found`.
- A held member is never reserved or granted, in any pool in use. Plan reads it as `unavailable`, so the grant order passes it over (see [Pool members](#pool-members)). Its state in Status stays its own.
- A hold on a member of no pool in use is accepted. It applies once that member's pool comes into use, and it outlives the pool's being forgotten.
- Each hold is stored in the `holds` table before it takes effect (see [Holds in the store](#holds-in-the-store)). At start, `orreryd` restores the holds before the grants, so a restored grant that holds a held member is stopping from its restore.

| Call | Connect code | When |
|---|---|---|
| `Status` | `unavailable` | `orreryd` has no store, or its restore at start failed |
| `Hold`, `Free` | `unavailable` | the same, or the broker has closed at shutdown |
| `Hold`, `Free` | `invalid_argument` | a member name outside the pattern |
| `Free` | `not_found` | the member is not held |
| `Hold`, `Free` | `internal` | a store write fails |

A hold on a granted member stops that grant:

1. The grant is marked stopping. Its `Acquire` stream sends `stop` once, naming the grant's members that are held. An attached grant gets it at once. A grant restored at start gets it right after its `granted` event, when its client reclaims it. A later hold on another member of a grant that is stopping sends nothing more.
2. The client stops its command, and closes the stream only after the command has exited (see [orrery](#orrery)).
3. When the stream ends, the grant's rows are deleted and its members are freed. Its request is queued again at its arrival place, unattached and keyed by the grant's id, with its client's start time kept for liveness. Requests queued behind it are not granted while it waits. The 60-second tick ends it if its client is gone.
4. The client's reclaim, by the grant's id, attaches to that request. It is queued at its place, and is then granted under a new grant id.

A requeued request is kept in memory only. A restart of `orreryd` ends it, and a reclaim after that answers `not_found`.

#### Notices

`orreryd` serves the `orrery.engine.v1.Notices` service (`proto/orrery/engine/v1/notice.proto`) on the session socket, beside `Steps`, with the same socket mode and Connect rules. `serve` forwards its calls as the page's `orrery.v1.Notices` service (see [orrery serve](#orrery-serve)). The service has two calls:

- `Watch` streams the in-app notices (see [In-app notices](#in-app-notices)). It sends each notice showing now as a `shown` message. Then it sends each change: a `shown` message for a notice that shows, or replaces the one under its key, and an `ended` message for the key of a notice that ends. The stream ends when the call ends (`enginehost.noticesService.Watch`; `inapp.Hub.Watch`). With no notice source, `Watch` answers `unavailable` (`enginehost.noticesService.Watch`).
- `Seen` names the key of a notice the owner opened. A key that sets none of its fields, or more than one, answers `invalid_argument` (`enginehost.noticeKeyOf`). A valid key goes to the router as seen at orreryd's clock (`enginehost.PageNotices.Seen`, `enginehost.noticesService.Seen`). The router ends the pending notice for that key when the notice was recorded at or before the seen time (`route.Router.Seen`). With no notice source, `Seen` answers `unavailable`.

A `NoticeKey` (`proto/orrery/engine/v1/notice.proto: NoticeKey`) sets one of five fields: `pane`, `session`, `epic`, `limit`, or `alert`, the name of an alert (see [Alert intake](#alert-intake)). `Seen` takes exactly one. `serve` copies the key field for field (`server.noticeKeyMessage`, `server.engineNoticeKey`), and the page keys each notice by all five fields (`web/src/notices.tsx: noticeKey`).

### Metrics

`orreryd` and `orrery serve` each export Prometheus metrics on the loopback address that `--metrics-listen` names. A scraper reads `GET /metrics`. Nothing else is exported: no OpenTelemetry traces, no remote write, and no push to a collector (`internal/telemetry/telemetry.go: package telemetry`). Without `--metrics-listen`, the process records no metrics and serves none.

#### Listeners

The address is a loopback IP literal with a port, such as `127.0.0.1:9464`. `server.Listen` (`server.Listen`) refuses every other host before it binds anything, including an empty host, a wildcard address, a hostname and a non-loopback address. The listener is local to the host.

- `orreryd`: `run` calls `startMetrics` before `serve` creates any socket (`cmd/orreryd.run`). A refused address exits 1 (`cmd/orreryd.report`). If the metrics server later fails, `orreryd` logs it as `serve metrics` and keeps serving.
- `orrery serve`: `serveListeners` binds the metrics address before the API address (`cmd/orrery.serveListeners`), so a refused metrics address binds nothing and `serve` exits 1 (`cmd/orrery.report`). A failed API bind closes the metrics listener. In `serveWithMetrics` (`cmd/orrery.serveWithMetrics`), a failure of either server ends the other, and `serve` returns both errors.

Both listeners answer `GET /metrics` and no other path, which answers 404 (`telemetry.Provider.Serve`). The one other route is `POST /alerts` on `orreryd`'s listener, the Alertmanager intake of [Alert intake](#alert-intake) (`telemetry.Provider.ServeWithAlerts`). `orrery serve`'s listener does not answer it, and answers 404.

#### Series

Apart from the `le` label of histogram buckets, no series carries a label other than the `job` and `instance` labels that the scrape adds and the labels named here. The OpenTelemetry exporter writes no `target_info` series and no `otel_scope_*` labels (`telemetry.New`).

Each process registers the standard process collector (`telemetry.New`) and no Go runtime collector. Its `process_*` series carry no labels, and include `process_cpu_seconds_total`.

Both processes export `rpc_server_call_duration_seconds`, a histogram with `_bucket`, `_sum` and `_count` series. Its labels are exactly these two:

- `rpc_method` is the procedure without its leading slash, such as `orrery.engine.v1.Pools/Status`.
- `rpc_response_status_code` is `OK` on success, and otherwise the Connect code name in capitals, such as `UNIMPLEMENTED`.

`orrery serve` gives these options to every Connect handler that `server.New` mounts (`server.Config.HandlerOptions`). `orreryd` gives them only to the six services that `enginehost.Serve` mounts on `sessions.sock` (`enginehost.Serve`; `cmd/orreryd.startSessions`).

`orreryd` also registers seven `orrery_*` series in `NewDaemon` (`telemetry.NewDaemon`). Every one is present at 0 from `NewDaemon`, except `orrery_start_timestamp_seconds`, which holds the start time. The four `class` series of `orrery_push_failures_total` are present at 0 from the start too.

| Series | Type | Labels | Recorded when |
|---|---|---|---|
| `orrery_start_timestamp_seconds` | gauge | none | Set once to the Unix time that `run` passes to `startMetrics` (`cmd/orreryd.run`). |
| `orrery_ready_timestamp_seconds` | gauge | none | Set to the Unix time at READY, and 0 until then. `notifyReady` (`cmd/orreryd.notifyReady`) records it before it checks `NOTIFY_SOCKET`, so it is set without `NOTIFY_SOCKET` too. |
| `orrery_push_failures_total` | counter | `class`: `transport`, `4xx`, `5xx` or `other` | Each failed push. `transport` is a push with no reply. `4xx` and `5xx` are those reply ranges. `other` is any other reply that is not 2xx. `post` counts it (`push.Publisher.post`). |
| `orrery_holder_losses_total` | counter | none | Each holder that has ended or cannot be attached when the sessions reconcile re-adopts it. `readoptHolder` counts it (`enginehost.Manager.readoptHolder`). Only the start-time sessions reconcile moves it. |
| `orrery_writer_wait_seconds_total` | counter | none | Seconds that callers wait on the writer: a `Put`, `Commit` or `CommitAll` waiting for room under its session's op limit, and a `Commit` or `CommitAll` waiting for its ops to be stored. Each wait adds its whole duration, including a wait that ends with an error (`enginehost.Writer.putAll`, `enginehost.Writer.waitForRoom`, `enginehost.Writer.recordWait`, `enginehost.Writer.await`). |
| `orrery_writer_backlog_age_seconds` | gauge | none | Age in seconds of the oldest op in the writer's queue, and 0 when the queue is empty. The writer sets it at the end of each flush, so a flush that has not returned leaves the last value in place (`enginehost.Writer.flush`, `enginehost.Writer.recordBacklog`). |
| `orrery_store_slow_calls_total` | counter | none | Each store call that takes longer than 100 ms, from the call until the store's one connection is free again: an Exec, a query until its rows close, a read transaction until it ends, or a write. The store logs each one as `slow store call` with its `queries`, its `wait` for the connection and its `run` on it (`store.callTimer.finish`, `store.WithSlowCallCounter`, `cmd/orreryd.openStore`). |

#### Alert rules

`observability/orrery-rules.yml` holds five alerts over these series. Each alert's labels are `job`, `instance` and `severity: warning`. Each summary is a fixed string, except `OrreryCPUHigh`'s, which names the process by its `job` label and gives the rate in cores with two decimals, such as `An orrery process (orreryd) has used 1.62 CPU cores for 15 minutes.` `observability/orrery-rules.test.yml` replays synthetic series through the rules with promtool. `just alert-check` runs that test (`justfile: alert-check`), and `just check` runs `alert-check` (`justfile: check`).

| Alert | Fires when | Window |
|---|---|---|
| `OrreryPushFailing` | the summed increase of `orrery_push_failures_total`, over all classes, is more than 5 | 10 minutes |
| `OrreryStartSlow` | READY came more than 10 s after start and less than 600 s ago, or no READY has come and more than 10 s have passed since start | 600 s after READY |
| `OrreryHolderLost` | the summed increase of `orrery_holder_losses_total` is above 0 | 15 minutes |
| `OrreryWriterStalled` | the summed rate of `orrery_writer_wait_seconds_total` is above 0.5 per second, or the largest `orrery_writer_backlog_age_seconds` is above 10 s | 5 minutes for the rate |
| `OrreryCPUHigh` | the summed rate of `process_cpu_seconds_total` is above 0.3 of a CPU core | 10 minutes for the rate, held for 15 minutes |

Only `OrreryCPUHigh` has a `for` clause. The other four fire at the first evaluation where their expression holds.

#### Flight traces

Only `orreryd` runs a flight recorder, and `orrery serve` does not. The recorder starts with `orreryd` whether or not `--metrics-listen` is set, and it keeps a window of about 10 MiB of execution trace (`telemetry.flightMaxBytes`).

When the start-to-READY time or the sessions reconcile takes longer than 10 s, `orreryd` writes that window as one trace for that phase, named `start` or `reconcile`. The sessions reconcile is `sessionHost.reconcile`, which `startSessions` calls. A phase that takes 10 s or less writes nothing. A recorder that cannot start is logged, and `orreryd` serves on.

The traces go in a `traces` directory beside the store (see [Store](#store)). That is `$XDG_STATE_HOME/orrery/traces` when `XDG_STATE_HOME` is set, and `$HOME/.local/state/orrery/traces` otherwise. The directory is created with mode `0700`, and each trace file with mode `0600` (`telemetry.StartFlight`, `telemetry.Flight.writeTrace`). A file is named `<UTC write time to the nanosecond>-<phase>.trace`, such as `20260114T093015.123456789Z-start.trace`, so names sort in write order. After each write, the newest 10 trace files stay and older ones are removed. Other files in the directory are left alone (`telemetry.Flight.prune`). A trace that fails to write is removed, so it takes no kept slot (`telemetry.Flight.writeTrace`).

Traces are local files. Nothing exports them.

### Alert intake

`orreryd` takes Alertmanager's webhook on the `--metrics-listen` listener and makes a notice of each firing alert that has a name. The notice routes like any other (see [Notice routing](#notice-routing)). The intake is `alerts.Webhook.ServeHTTP`, which `cmd/orreryd.startMetrics` mounts with `telemetry.Provider.ServeWithAlerts`. `orrery serve` has no intake: its listener is `telemetry.Provider.Serve`, which answers `GET /metrics` only (`cmd/orrery.serveWithMetrics`).

- `POST /alerts` is the only route. Any other method on `/alerts` answers 405 (`telemetry.Provider.serve`).
- Until `serve` attaches the notice router, the intake answers 503. `serve` attaches it when it builds its relay (`alerts.Webhook.Attach`, `cmd/orreryd.newRelay`).
- The body is Alertmanager's version 4 webhook, a JSON object whose `version` is `"4"`. A body over 1 MiB (1,048,576 bytes) answers 400, and a body of exactly 1 MiB is read. A body that cannot be read or does not decode as that shape answers 400, and so does one with a label or annotation value, under any key, that is neither a string nor `null`. A `null` value reads as empty, so a `null` name makes no notice and a `null` summary gives an empty body. Otherwise the intake answers 200, including for a body that makes no notice (`alerts.Webhook.ServeHTTP`).
- Two webhook fields reach a notice. The `alertname` label of an alert is the notice's name, and its `summary` annotation is the notice's body. An alert with no name, or an empty one, makes no notice. No other field of the body or of an alert reaches a notice (`alerts.alertNotice`).
- Only an alert whose `status` is `firing` makes a notice, one for each, in body order. A resolved alert makes no notice and ends none. A notice that routes at the desk stays pending until a seen write, a newer notice of its name, or the escalation of rule 3 of [Notice routing](#notice-routing) ends it. One that routes away ends at once.
- The notice's key is its `alert` field, the name, and its state is `firing` (`alerts.alertNotice`). A newer notice for a name ends the pending one with reason `changed` (`route.Router.Notify`). So two firing alerts that share a name in one body leave only the later one pending.
- The desk notice's title is the name and its body is the summary. It has no state, so its desk line and its in-app notice carry none (`alerts.alertNotice`).
- An alert notice is never watched and never judged. It routes at once by rules 3 to 6 of [Notice routing](#notice-routing) (`route.Router.decide`), so it shows in-app at the desk like any notice (see [In-app notices](#in-app-notices)).
- The phone's push is the Push payload of this key. Its `project` and its `alert` are the name, and its `state` is `firing` (`push.boundedPayload`). It is never silent. On the phone, the notification is titled `<name> · firing`, its text is the summary, and its tag is `alert:<name>`. It carries no actions, and a tap opens the app without naming a screen (`android/app/src/main/kotlin/io/github/joshsymonds/orrery/push/PushNotifier.kt: PushNotifier.show`, `android/app/src/main/kotlin/io/github/joshsymonds/orrery/push/PushNotifier.kt: PushNotifier.tagOf`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/MainActivity.kt: MainActivity.openExtra`).
- An alert notice has no source, so it has no pending row (`route.Router.rowLocked`). At shutdown a pending alert notice ends `shutdown` and is not sent, and a restart does not restore it (`route.Router.Close`). Its push time is kept in [Pushed notices in the store](#pushed-notices-in-the-store) (`notice.Notices.putRow`).
- Only the `Notices.Seen` call names an alert key, so only that call clears an alert. The clear carries the `alert` key (see [Clear payload](#clear-payload)).
- The notice log line of an alert names no alert: its `pane`, `session` and `epic` keys are absent, and its `state` is `firing` (see [Notice log](#notice-log)).

### Desk stream

`orreryd` serves the desk stream on `notices.sock`, beside the daemon socket (section 5). The socket's directory is created with mode 0700 when it is absent. `orrery notices` reads the stream, and `orrery desk` reads it on this machine and over ssh. Nothing here opens a network listener. `orreryd`, `orrery notices` and `orrery desk` use unix sockets, and a host's notices leave it only through the ssh session that `orrery desk` starts.

The stream is JSON lines. Each line is one object and ends with a newline.

- `hello` is the first line of every stream: `{"kind":"hello","host":"example-host"}`.
- `notice` carries one notice: `{"kind":"notice","host":"example-host","state":"waiting","title":"example-project · example-session:1","body":"Which file should I edit?"}`. `state`, `title` and `body` are left out when empty. `quiet` is `true` on a quiet notice, which the judge found unneeded (see [Notice routing](#notice-routing)), and absent otherwise. A quiet notice: `{"kind":"notice","host":"example-host","state":"done","title":"example-project · example-session:1","body":"Example summary of the work.","quiet":true}`.
- A line the desk sends up the stream is read and discarded. It has no effect on whether the desk is at hand.

Each stream gets `hello` first, then each notice posted while it is open, in the order posted. The stream keeps no backlog, so a notice posted while no stream is open is dropped. A stream that falls 64 lines behind is closed. On shutdown, queued lines get up to 2 seconds to reach their streams, and then open streams are closed.

The desk is at hand on web input alone, with or without a stream open (see [Notice routing](#notice-routing), rule 3). `orreryd` keeps the newest web input that `serve` reports to it (section 9). The desk popup still reaches only open links, since the stream posts only to open streams (`deskstream.Hub.Post`, `deskstream.Hub.Desk`).

The notices on the stream are these:

- A pane notice, on a pane's transition to `waiting` or `done` (section 11, orreryd). Its title is `<project> · <session>:<window>`, where `project` is the base name of the pane's directory, `session` is the tmux session's name, and `window` is the window's index.
- A session notice, on a headless session's transition to `waiting`, `done`, `failed` or `interrupted` (see [Session notifications](#session-notifications)).
- An epic notice, on an epic that needs its owner (see [Planner turns](#planner-turns)). Its state is `waiting`, its title is the epic's id, and its body is the body that Planner turns gives.
- A usage-limit notice, on a change to the steps a usage limit holds (see [Step lifecycle](#step-lifecycle), Usage limit). Its state is `limited` and its title is `usage limit`.
- An alert notice, on a firing alert that a webhook names (see [Alert intake](#alert-intake)). Its title is the alert's name and its body is the alert's summary. It has no state.

The stream carries a pane, session, epic or alert notice only when [Notice routing](#notice-routing) posts it there. A `done` whose `Stop` hook holds a subagent or workflow is never posted, on the stream or to Web Push. The in-app notices of [In-app notices](#in-app-notices) are not on this stream.

### In-app notices

While a notice routes at the desk (rule 3 of [Notice routing](#notice-routing)), `orreryd` also shows it on every open web page, until it ends. It is put in the in-app hub under its key, and each page's watch receives it in the order first shown (`route.Router.route`, `route.Router.atDesk`; `inapp.Hub.Show`). A notice that routes away is not shown in-app (`route.Router.hold`, `route.Router.route`). The pages reach the hub only through the `Notices` service on the session socket, so without `--sessions` no page shows an in-app notice, and the notice still routes by rule 3 (`cmd/orreryd.serve`; see [Notices](#notices)).

- A notice leaves the pages when it ends, whatever its route: a seen write, a newer notice for its key, the escalation to the phone, or a shutdown. A shutdown ends the notice on the pages even when the router keeps it for a restart (`route.Router.stopLocked`; `inapp.Hub.End`).
- A notice that a restart restores at the desk is shown again at once (`route.Router.resumeLocked`, `route.Router.showRestored`).
- A page's watch receives the notices showing when it starts, then each change. A watch that falls 64 changes behind is closed (`inapp.queueLen`, `inapp.Hub.Watch`). A page clears its banner and reopens its watch when its stream ends or fails (`web/src/notices.tsx: watchNotices`).
- Each notice is one button in the page's banner, with its title and body. The banner makes no sound (`web/src/notices.tsx: NoticeBanner`; `web/src/app.tsx: App`).
- A click sends `orrery.v1.Notices.Seen` for the notice's key and moves the page's cursor to its entry (`web/src/app.tsx: App`). `orreryd` counts the notice as seen at its own clock, and the notice ends as `seen` (see [Notices](#notices); `route.Router.Seen`).
- The phone app makes no `Notices` call, so it shows no in-app notice.

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
orrery [--socket PATH] desk --sound FILE [--host HOST]...
```

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

A link reads the lines its command prints. It keeps the command's stdin open and writes nothing to it, because `orrery notices` exits when its stdin ends:

- A `hello` makes the stream live.
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
| `HOME` | `orrery` (`tree`, `open`, `run`) | `$HOME/Personal` is the default parent directory of new projects. `$HOME` is also the directory that roots are relative to, and the roots file falls back to `$HOME/.config/orrery/roots` (see [Roots](#roots)). The machine pool file falls back to `$HOME/.config/orrery/pools.toml` (see [Pool files](#pool-files)). The store file falls back to `$HOME/.local/state/orrery/orrery.db` when `XDG_STATE_HOME` is unset, and `orrery open` and the tree's `N` read it read-only for the known-directory lookup (see [Store](#store)) |
| `XDG_STATE_HOME` | `orrery` (`tree`, `open`) | base directory of the archive file (see [Archive](#archive)) and of the store file, `$XDG_STATE_HOME/orrery/orrery.db`, which `orrery open` and the tree's `N` read read-only for the known-directory lookup (see [Store](#store)) |
| `XDG_CONFIG_HOME` | `orrery` (`tree`, `open`, `run`) | base directory of the roots file, `$XDG_CONFIG_HOME/orrery/roots` (see [Roots](#roots)), and of the machine pool file, `$XDG_CONFIG_HOME/orrery/pools.toml` (see [Pool files](#pool-files)) |
| `PATH` | `orrery` (`tree`, `open`, `desk`, `run`), `orreryd` | finds `claude` for the picker and `open`, and `ssh`, `notify-send` and `pw-play` for `desk` (see [orrery desk](#orrery-desk)). `run` finds `CMD` and `git`, and `orreryd` finds the program of each member command (see [Pool members](#pool-members)) |
| `ORRERY_GRANT` | set by `orrery run` for `CMD` | the id of the grant `CMD` runs under (see [orrery](#orrery)) |
| `ORRERY_POOL_<POOL>` | set by `orrery run` for `CMD`, once for each pool named | the members granted from pool `<POOL>`, comma-separated |
| `ORRERY_POOL` | set by `orreryd` for each member command | the pool the member command runs for |
| `ORRERY_MEMBER` | set by `orreryd` for each member command | the member the command runs for |

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
| `pane` | string | the tmux pane id, for a pane notice; absent for a session, epic, usage-limit or alert notice |
| `session` | string | the session id, for a session notice; absent for a pane, epic, usage-limit or alert notice |
| `epic` | string | the epic id, for an epic notice; absent for a pane, session, usage-limit or alert notice |
| `state` | string | the notice's state: `waiting`, `done`, `failed`, `interrupted` or `limited`, `limited` for a usage-limit notice (see [Step lifecycle](#step-lifecycle), Usage limit), and `firing` for an alert notice (see [Alert intake](#alert-intake)) |
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
| `changed` | `none` | a newer notice for its key, the pane or session moved to another state, or a restored pane notice's pane is gone |
| `seen` | `none` | a seen write at or after the notice's time, or, for a restored pane notice, a pane already seen at or after that time |
| `shutdown` | `none` | `orreryd` shut down while the notice was pending and does not keep it: a notice whose row could not be stored, or any pending notice when `orreryd` serves without the store |
| `not restored: <error>` | `none` | a stored pending notice could not be restored at start, and its row is deleted. `<error>` is the restore's error text (see [Notice routing](#notice-routing)) |
| `resent after restart` | `phone` | a `sending` row restored at start is pushed again, silent if its push was silent (see [Notice routing](#notice-routing)) |

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

### Pool files

`orrery run` and `orrery gate` read two TOML files that declare pools: a project file and a machine file. Both are read on each run. A file that does not exist declares no pools. A step's start reads the same two files for the pools its gate needs (see [Steps](#steps)). Where a rule below names `orrery run`, `orrery gate` follows it too.

- The project file is `.orrery/project.toml`. Inside a git work tree, it is at the top of the work tree the command runs in, so each worktree of a repository reads its own file. Outside git, it is the nearest `.orrery/project.toml` at or above the working directory.
- The machine file is `$XDG_CONFIG_HOME/orrery/pools.toml` when `XDG_CONFIG_HOME` is set and not empty. Otherwise it is `$HOME/.config/orrery/pools.toml`. When neither variable is set, `orrery run` exits 75.
- A project is named by its absolute directory path. Inside git, that is the parent of the repository's common git directory, so every worktree of one repository names the same project. Outside git, it is the directory that holds the project file, or the working directory when there is no project file.
- `orrery run` needs `git` on `PATH` for every run, because it asks git for the project's name. Without it, the run exits 75.

Each file holds `[pools.NAME]` tables. Every table has all nine keys below, and each key is required:

| Key | Type | Meaning |
|---|---|---|
| `host` | string | the machine that owns the pool. `orreryd` refuses a pool whose `host` is not its own host name |
| `cap` | integer, 1 or more | the most members one project may hold from the pool at once |
| `members` | array of strings | the member names. The list may not be empty, and may not repeat a name |
| `wake`, `status`, `admit` | array of strings | the argv of each member command, run without a shell. The first element is the program and the rest are its arguments. The list may not be empty, and no element may be empty |
| `wake_limit`, `status_limit`, `admit_limit` | integer seconds, 1 or more | how long the matching command may run |

- A pool name matches `[a-z][a-z0-9_]*`. A member name matches `[A-Za-z0-9][A-Za-z0-9._-]*`.
- A member belongs to one pool. `orrery run` refuses a run whose merged files put one member in two pools.
- Keys match exactly. A file with an unknown key, an unknown table, or a missing required key is refused.
- A project file may also hold a `[gates]` table, which [Gate files](#gate-files) defines. Here it is refused only when `gates` is not a table. The machine file's `[gates]` table is not read for gates. A project file may also hold `[[reviewers]]` tables, which [Reviewers](#reviewers) defines. The pool reader passes that table in either file, and only the project file's is read, by `reviewers.Project`.
- `orrery run` merges the two files. A pool defined the same way in both is kept once. A pool defined differently in the two files refuses the run with exit 75.

Example. Every name in it is synthetic:

```toml
[pools.builds]
host = "box"
cap = 1
members = ["build-1", "build-2"]
wake = ["example-wake"]
wake_limit = 300
status = ["example-status"]
status_limit = 30
admit = ["example-admit"]
admit_limit = 30
```

### Gate files

A gate is a named `[gates.NAME]` table in one of two TOML files. `orrery gate` runs an argv gate it resolves, and a step runs the gate it names (see [orrery](#orrery) and [Step lifecycle](#step-lifecycle)). An agent gate is run only by a read-only step (see [Agent gates](#agent-gates)).

- The epic's gates file is named by the caller: `gates_file` in `StartStep` (see [Steps](#steps)), or `--gates FILE` in `orrery gate`. By convention it is `gates.toml` in the epic's record directory, `~/.gambit/<repo-id>/<slug>/`. It holds a `[gates]` table, which may hold no gates, and no other top-level key.
- The project file is `.orrery/project.toml`, the file of [Pool files](#pool-files). Its `[gates]` table is optional.
- The machine file, `pools.toml`, holds no gates. Its `[gates]` table, if it has one, is not read for gates.

A name resolves in this order (`gates.Resolve`, `gates.ResolveDefined`, `gates.resolveProject`):

1. When an epic gates file is named, it is read first, in full. A missing, unreadable or invalid file is an error, even when the name is in the project file.
2. When that file does not define the name, or no file is named, the project file is read. A missing project file defines no gates. The project file is found as [Pool files](#pool-files) gives.
3. A name that neither file defines is refused. The error names every file searched, or says that there were none.

The gate's source is `epic` or `project`, the file that defines it.

An epic's gates may also be stored as data in the epic, as the `gates` of its contract (see [Epics](#epics)). `CreateEpic` builds each one with the same checks a file's table gets (`gates.Define`; `enginehost.checkEpicGates`). A done gate of the epic, and a planned task's gate, resolve from those data first, with no file read, and then from the project file for the epic's repository, in the order above. The epic's review list is not named here; it comes from its reviewers (see [Reviewers](#reviewers)). A gate the data define has the source `epic` and names no file (`gates.ResolveDefined`; `planner.EnvResolver.Gate`; `enginehost.checkEpicGateRefs`).

Each argv gate's `[gates.NAME]` table has these keys. An agent gate's keys are in [Agent gates](#agent-gates):

| Key | Type | Meaning |
|---|---|---|
| `argv` | array of strings | required. The gate's program and its arguments, run without a shell. The array may not be empty, and no element may be empty. `argv[0]` must contain a `/`, and it is never looked up on `PATH` |
| `limit` | integer seconds, 1 to 9223372036 | required. How long the gate may run. The count starts when the gate's grant is granted, not when it queues |
| `needs` | table of pool names to integers | optional. The pool members the gate holds while it runs, as `POOL = COUNT`. A gate with no `needs` holds one member of the pool `gate`. The table may not be empty |

- A gate name matches `[a-z][a-z0-9_]*`, and so does each pool name in `needs`. Each count is 1 to 2147483647.
- Each pool in `needs` must be declared by a pool file for the directory the gate is resolved in: the working directory for `orrery gate`, the step's workspace for a step. Otherwise the gate is refused (see [Pool files](#pool-files)).
- Keys match exactly. A file with a top-level key other than `gates` (or `pools` and `reviewers` in a project file) is refused. So is an argv gate with a key other than `argv`, `limit` and `needs`, or one without `argv` or `limit`. An agent gate's refusals are in [Agent gates](#agent-gates).

A changed file reaches only steps started after it. A step resolves its gate once, when it starts, and keeps the argv, limit and needs that resolution gave, or for an agent gate its agent, verdict schema and prompt (see [Steps in the store](#steps-in-the-store)). `orrery gate` reads both files on each run.

Example. Every name in them is synthetic. The epic's gates file, `gates.toml`:

```toml
[gates]

[gates.check]
argv = ["/opt/example/bin/check"]
limit = 600
needs = { builds = 1 }

[gates.lint]
argv = ["/opt/example/bin/lint", "--strict"]
limit = 120
```

The project file, `.orrery/project.toml`, which may also hold `[pools]` tables:

```toml
[gates.integration]
argv = ["/opt/example/bin/integration"]
limit = 1800
needs = { builds = 1 }

[gates.smoke]
argv = ["/opt/example/bin/smoke"]
limit = 300  # no needs: one member of the pool gate
```

#### Agent gates

A `[gates.NAME]` table with an `agent` key is an agent gate. An agent gate checks its assertions with an agent. Only a read-only step runs it: `orrery gate` refuses it (see [orrery](#orrery)), and a writing step cannot name it (see [Steps](#steps)). Its table holds these keys:

| Key | Type | Meaning |
|---|---|---|
| `agent` | string | required. The agent that checks the assertions. It is not empty, and it is checked as a step's `agent` is (see [Steps](#steps)) |
| `limit` | integer seconds, 1 to 9223372036 | required. The review's limit. It is stored with the step, the first prompt states it, and the watch enforces it: the step ends `overtime` at the first tick at or past the limit of wall time from the run's start (see [Step lifecycle](#step-lifecycle) and `enginehost.stepRun.watchGateLimit`) |
| `must` | array of strings | optional. Each entry is an assertion that must hold |
| `must_not` | array of strings | optional. Each entry is an assertion that must not hold |

At least one entry is needed between `must` and `must_not`, and each entry is a non-empty string. The gate is refused, naming it, when it holds `argv` or `needs`, when its `agent` is an empty string, when it has no `limit`, when `must` and `must_not` have no entry between them, or when an entry is an empty string. An argv gate with `must` or `must_not` is refused too (`gates.toArgvGate`, `gates.toAgentGate`, `gates.toAssertions`).

The gate's assertions are its `must` entries in file order, then its `must_not` entries in file order. A `must` entry has the kind `must`, and a `must_not` entry has the kind `must not` (`gates.KindMust`, `gates.KindMustNot`, `gates.toAssertions`).

The agent's return must match `internal/gates/verdict.json`, the verdict schema, which is embedded as `gates.VerdictSchema` (`gates.VerdictSchema`). Its root requires `status`, `findings`, `observations`, `missing_evidence`, `test_runner_requests` and `results`, and each object admits only the keys it names. `status` is `done` or `blocked`. `results` has one entry per assertion, in declaration order. Each result requires `assertion`, `kind` (`must` or `must not`), `result` (`holds`, `fails` or `undetermined`) and `evidence`. A `findings` entry requires `id`, `claim`, `citation`, `location`, `evidence` and `verify_by`, and admits `precondition` and `consequence`. An `observations` entry requires `location`, `observation` and `reason`, and a `test_runner_requests` entry requires `command` and `revision`.

The first prompt is built by `gates.Prompt` (`gates.Prompt`). Its sections are separated by a blank line, in this order: the fixed instruction (`gates.verdictInstruction`); `Gate: NAME` and `Limit: N seconds` on two lines; `Assertions:`, then one line per assertion, numbered from 1 as `N. KIND: TEXT`, in assertion order (`gates.assertionList`); and the caller's prompt, when it is not empty.

An example, with synthetic names:

```toml
[gates.review]
agent = "example-reviewer"
limit = 900
must = ["The change keeps every example endpoint's response shape."]
must_not = ["The change edits the example vendor directory."]
```

An agent gate can also be given as data to a read-only step, with no gate name and no gates file. `StepStart.AgentGate` carries the gate, which the caller builds with `gates.Define`, and the step runs it as a named agent gate does. The gate needs a name, an agent, at least one assertion, no argv and no needs, and a limit of at least a second. The start must leave `agent`, `schema` and `done_value` empty, as a named agent gate's start does (`enginehost.StepStart.AgentGate`; `enginehost.dataAgentGate`, `enginehost.agentGateStart`). The row stores the gate's name, the source `data` and its limit (`enginehost.stepGateSourceData`). The field is Go-only: the `Steps` service has no request field for it, so only an in-process caller sets it (`proto/orrery/engine/v1/step.proto`). The evidence judge and a task's reviewer are such steps (see [Evidence](#evidence) and [Reviewers](#reviewers)).

#### Reviewers

A reviewer is one `[[reviewers]]` entry: a named check that runs at the task stage, in a task's review step, or at the epic stage. This subsection gives where entries come from, the keys they hold, how the layers merge, the refusals, orrery's default reviewers, the conformance assertions, and what a reviewer runs as. The review steps are in [Task builds](#task-builds).

**Layers.** Three layers declare entries. They merge in this order (`reviewers.Merge`, `enginehost.epicReviewers`):

1. orrery's defaults (`reviewers.Defaults`).
2. The project file's `[[reviewers]]` tables, in the project file [Pool files](#pool-files) gives (`reviewers.Project`, `reviewers.ParseProject`).
3. The contract's `reviewers`, given to `epic_create` (see [Epics](#epics)). Each is an `EpicReviewer` message, whose keys are named in protobuf JSON, such as `mustNot` (`proto/orrery/engine/v1/epic.proto: EpicReviewer`).

**Keys.** An entry holds these keys. In the project file they are written as TOML names, such as `must_not`. A key an entry does not set is unset in its layer.

| Key | Type | Meaning |
|---|---|---|
| `name` | string | required. It matches `[a-z][a-z0-9_]*` and is at most 48 bytes, so the inline agent name `orrery-reviewer-<name>` stays within the 64-byte agent limit of [Steps](#steps) |
| `enabled` | boolean | whether it runs. A disabled reviewer stays in the list and runs at no stage. Default `true` |
| `stage` | array of `task` and `epic` | the stages it runs at, each named once. An enabled reviewer needs one |
| `must` | array of strings | statements its return must show hold |
| `must_not` | array of strings | statements its return must show do not hold |
| `prompt` | string | the role it runs under. Required when `model` is set, and not empty |
| `agent` | string | an agent claude already has. Not empty. Never set beside `model`, `tools` or `effort` |
| `model` | string | the model of its inline agent. Not empty |
| `tools` | array of strings | the tools of its inline agent. No element is empty |
| `effort` | string | `low`, `medium`, `high`, `xhigh` or `max` |
| `limit` | integer seconds | how long its review may run, from 1 to 9223372036. Default 1800 |
| `verify` | boolean | whether its findings go to a verifier. Default `true` |

An enabled reviewer needs a `stage`, an `agent` or a `model`, a `prompt` when it has a `model`, and a `must` or a `must_not`, once the layers merge (`reviewers.incomplete`).

**Merge.** `reviewers.Merge` applies the layers in order, and entries with the same name merge into one reviewer (`reviewers.pending.apply`).

- A key an entry sets replaces the value so far. A key it does not set keeps that value.
- A list an entry sets replaces the earlier list. Lists do not append. In the project file an empty array is set. In the contract an empty repeated field is unset (`enginehost.epicList`).
- `agent` displaces `model`, `tools` and `effort`, and each of those displaces `agent`. One entry may not set `agent` beside them.
- A name appears once in each layer. Reviewers come in the order their names first appear across the layers. Orrery's defaults come first, so an override keeps its place.
- An unset `enabled` is `true`, an unset `verify` is `true`, and an unset `limit` is 1800 seconds.

**Refusals.** `reviewers.Merge` refuses the first entry a layer cannot hold, and then the first enabled reviewer the layers leave incomplete. A `reviewers.Error` names the file or `epic_create`, the entry's index in its layer, the name, the key and the reason, in the form `<source>: reviewers[<index>] <name>: <key>: <reason>`. The entry checks of `reviewers.checkEntry` give these reasons:

- `name`: `must match [a-z][a-z0-9_]*`, `is over 48 bytes`, or `appears twice in one layer`.
- `stage`: `is empty`, `element "<x>" must be task or epic`, or `repeats "<x>"`.
- `must`, `must_not` and `tools`: `has an empty element`.
- `agent`: `is empty`. `model`, `tools` or `effort` set beside `agent`: `cannot sit beside agent`.
- `model` or `prompt`: `is empty`.
- `effort`: `"<x>" is not low, medium, high, xhigh or max`.
- `limit`: `must be at least 1 second`, or `must be at most 9223372036 seconds`.

A project file that does not read gives its own reasons (`reviewers.ParseProject`, `reviewers.toEntry`). A key that is not a key of a reviewer is refused with `is not a key of a reviewer`, a value of the wrong type with `has the wrong type`, and a `reviewers` that is not an array of tables with `must be an array of tables`. A file TOML cannot parse is refused with its parse error. An enabled reviewer the layers leave incomplete is refused with `needs a stage`, `needs an agent or a model`, `is required when model is set` on `prompt`, or `needs a must or must_not` (`reviewers.incomplete`).

**Defaults.** orrery's five defaults are the first layer (`reviewers.Defaults`). Each is enabled, verifies its findings, has a limit of 1800 seconds, and may use the tools `Read`, `Grep`, `Glob` and `Bash`. Each runs on the model its entry names, and its prompt is its role text, embedded from `internal/reviewers/roles`. Each role text says the reviewer never edits.

- `floor`: stage `task`, model `opus`. Must: `Each capture for the task's Requirements would fail if the behavior it proves were removed.` Must not: `The change suppresses, skips or disables a check.`, `The change weakens a test or makes one tautological.`, `The change adds dead code.`, `The change leaves an error unhandled.`, `The change does something the task did not ask for.`, and `The change introduces a security or data-loss failure with a reachable precondition.`
- `conformance`: stage `epic`, model `sonnet`. Must not: `The change does something the contract did not ask for.`, then the generated entries below.
- `integration`: stage `epic`, model `sonnet`. Must not: `A task's change breaks an interface, ordering, lifecycle, configuration or data format that another task's change relies on.`
- `security`: stage `epic`, model `sonnet`. Must not: `The change introduces a security, data-loss or resource failure with a reachable precondition.`
- `test_parsimony`: stage `task` and `epic`, model `sonnet`. Must not: `The change adds a test that uses a real wait or a real clock.`, `The change adds a test that starts its own real resource where a shared one exists.`, `The change adds a test in a package that unrelated changes will select.`, `The change adds a redundant or tautological test.`, `The change adds an unjustified golden suite.`, `The change adds a test that asserts wall-clock timing.`, and `The change adds a test that checks model output deterministically.`

**Conformance assertions.** After the merge, `reviewers.Generate` appends one `must_not` to the enabled `conformance` reviewer for each entry of the contract's `exclusions`, in order, after its own `must_not` entries. Each is `The change does what this entry rules out: ` followed by the entry. A disabled conformance reviewer gets none (`enginehost.epicReviewers`).

**What a reviewer runs as.** `reviewers.Reviewer.AgentName` is the agent the reviewer names, or else `orrery-reviewer-<name>`. `reviewers.Reviewer.Gate` is the reviewer's agent gate: its name, that agent, its limit in seconds, and its assertions, the `must` entries and then the `must_not` entries. A model reviewer also has an inline agent, `reviewers.Reviewer.Agents`: one JSON object keyed by that agent name, holding `description` (`orrery reviewer <name>`), `prompt`, `model`, and `tools` and `effort` when they are set. A set `tools` list stays when it is empty. An agent reviewer has no inline agent, since it runs an agent claude already has. A review step carries the gate and, for a model reviewer, the inline agent, as [Task builds](#task-builds) gives.

**Per-task list.** The per-task review list is the names of the enabled task-stage reviewers, in merged order (`reviewers.ForStage`). `CreateEpic` stores that list as the epic's review gate list (`enginehost.taskReviewNames`, `store.insertGateList`), and the build runs its review steps in that order.

**Epic stage.** The epic-stage reviewers are merged, stored with the epic and shown by `epic_get`. The end-of-epic review runs each of them at the frozen revision, under its try chain (see **End-of-epic review** under [Workflows](#workflows)).

### Holder

`orrery holder` runs one session's child and serves the session's holder socket (see [orrery](#orrery)). The holder is the package `internal/holder`. It spools the child's stdout to disk, keeps a tail of the child's stderr, and answers controllers on a unix socket. A controller attaches to read the stdout, writes to the child's stdin, trims the spool, or stops the child. `orreryd` is the controller of each session's holder, through its session process and through the re-adoption, drain and clear that reconcile runs at start (see [Headless sessions](#headless-sessions) and [Lifecycle rules](#lifecycle-rules)). A controller that detaches closes its connection and leaves the holder, its child and its directory in place. A gate's holder is controlled the same way, through the gate's launch, its re-adoption and its clear (see [Step lifecycle](#step-lifecycle)). Once the holder's process and stream have ended, a controller that did not detach removes the holder's directory (`holder.Process.awaitHolder`; `holder.Clear`).

#### Directory and files

- The holders directory is `holders` in the directory that holds the store file: `$XDG_STATE_HOME/orrery/holders` when `XDG_STATE_HOME` is set, else `$HOME/.local/state/orrery/holders`. `--state-dir DIR` names `DIR/holders` instead. A gate's holder is always launched with `--state-dir` naming its gate state directory (`holder.holderCommand`). When neither the store path nor `--state-dir` resolves, `orrery holder` exits 1 (`cmd/orrery.runHolder`, `holder.HoldersDir`).
- A session's files are in `holders/SESSION`. `SESSION` is 1 to 64 characters: its first a letter or digit, and the rest letters, digits, `.`, `_` or `-`. Any other name is a usage error (`holder.sessionPattern`, `holder.SessionDir`; `cmd/orrery.runHolder`).
- The holders directory is created with its parents at mode 0700 when it is missing, and an existing one is not checked. The session directory is created at mode 0700. An existing session directory must be a real directory, not a symbolic link, with mode exactly 0700 and owned by this user. Otherwise the holder refuses to start (`holder.PrepareDir`, `holder.checkPrivateDir`).
- `holder.lock` (0600) is the session's lock. The holder takes an exclusive, non-blocking `flock` on it and keeps it for its life. A holder that finds it held refuses with `session NAME: another holder holds the session's lock` (`holder.ErrLocked`, `holder.lockFileMode`, `holder.Lock`, `holder.Run`).
- `holder.sock` (0600) is the socket (see [Socket](#socket)). `stdout-` segments (0600) hold the child's stdout (see [Spool](#spool)). `exit.json` (0600) is the exit record (see [Exit file](#exit-file)).
- `holder.json` (0600) is the pids record: the holder's process id and its child's, each with its start time from `/proc/PID/stat`, which is empty where `/proc` holds no stat. The holder writes it once the child has started, and never replaces an existing one. A record that cannot be written ends the child and the start, as a child that cannot start does (see [Exit file](#exit-file)) (`holder.PidsName`, `holder.Pids`, `holder.recordChild`, `holder.WritePids`, `holder.procStartTime`; `holder.openHolder`).
- Once it holds the lock, the holder refuses to start when `exit.json` exists, and when any `stdout-` segment exists. Both refusals exit 1 (`holder.openHolder`; `holder.NewSpool`).

#### Socket

- The holder removes a socket file left in the session directory, listens on `holder.sock` and sets its mode to 0600. The path must be under 108 bytes, and a longer path is refused (`holder.openHolder`, `holder.maxSocketPath`, `holder.holderDirMode`, `holder.SocketPath`). The socket file is removed when the holder stops listening (`holder.holder.run`).
- Each connection's peer uid must equal the holder's uid. A peer of another user, or one whose uid cannot be read, is closed with no frame (`holder.holder.admit`). The uid comes from `SO_PEERCRED` on Linux (`holder.PeerUID`) and from `LOCAL_PEERCRED` on Darwin (`holder.PeerUID`).
- The first frame on a connection must be `hello`. Any other first frame closes the connection with no answer (`holder.holder.serve`, `holder.holder.greet`).

#### Spool

- The child's stdout is appended to segment files named `stdout-` followed by the segment's first offset as 20 decimal digits. Each segment is created with mode 0600. A segment is full at 4 MiB, and the next one opens when the current one is full (`holder.DefaultSegmentBytes`, `holder.Spool.Append`, `holder.segmentMode`, `holder.Spool.createSegment`, `holder.segmentName`).
- An offset counts the child's stdout bytes, and the first byte is offset 0 (`holder.segment.end`, `holder.Spool`, `holder.Spool.Append`). `start` is the first offset the spool still holds, and `end` is the offset just past the last byte it holds (`holder.Spool.Start`, `holder.Spool.End`).
- An `ack` carries `consumed` and `replayFrom`. The spool refuses it unless `0 <= replayFrom <= consumed <= end` and neither offset moves backwards. It then deletes every segment whose end is at or below `replayFrom`. It never deletes the last segment, which is the one being written (`holder.Spool.Ack`). A controller sets `replayFrom` to the offset that a later re-adoption replays from, so the spool keeps every byte from that offset on (see [Reconcile at start](#lifecycle-rules)).
- The holder reads no further child stdout once `end` is 64 MiB past `consumed`, so the child's writes back up in its pipe until an `ack` moves `consumed` (`holder.DefaultSegmentBytes`, `holder.DefaultCapBytes`, `holder.Spool.Changed`, `holder.Spool.Close`, `holder.Spool.roomLocked`, `holder.Spool.signal`; `holder.Child.copyStdout`).
- Once the child is reaped, the spool is sealed: appends ignore the cap, and the holder reads the rest of the child's stdout to end of file. Those bytes are what the child left in its pipe (`holder.Spool.Seal`, `holder.Child.copyStdout`).

#### Exit file

- The holder writes `exit.json` once the child has ended, before it answers `stopped` (`holder.holder.run`). It writes a temporary file in the session directory and hard-links it to `exit.json`, so a reader sees either no file or the whole record. It never replaces an existing `exit.json` (`holder.WriteExit`). If the file cannot be written, the holder sends no `stopped` and exits 1.
- The record is a JSON object with these keys (`holder.Exit`):
  - `reason`: `exited` when the child ended on its own. `stopped` when a stop frame, or a SIGINT or SIGTERM to the holder, had arrived when the child ended. `idle` as [Idle timeout](#idle-timeout) gives (`holder.ReasonExited`, `holder.ReasonStopped`, `holder.ReasonIdle`, `holder.holder.run`, `holder.holder.exitReason`). `limit` when a gate child passed its limit and the holder stopped it, as [Child](#child) gives (`holder.ReasonLimit`, `holder.Child.Exit`).
  - `code`: the child's exit status, or -1 when a signal ended the child or its wait gave no status (`holder.Child.Exit`).
  - `signal`: the name of the signal that ended the child. It is omitted when empty (`holder.exitStatus`).
  - `stdoutEnd`: the spool's end once the child is reaped (`holder.Child.Exit`).
  - `stdout`: the newest tail of the child's stdout. Only a gate child keeps one, the newest 8 KiB unless `--gate-tail` names another size (see [Child](#child)); any other child's record carries the empty string, and its stdout is in the spool (`holder.Exit`; `holder.Child.Exit`).
  - `stderr`: the last 64 KiB of the child's stderr, as the child wrote it, trailing newlines included. A gate child's record keeps the newest tail of its stderr here instead, 8 KiB unless `--gate-tail` names another size, and its stdout tail is in `stdout` (see [Child](#child)). A fault of the holder, such as a failed signal, is its last line, after a newline when the tail does not end in one (`holder.Child.Exit`, `holder.Child.signal`, `holder.Child.fail`, `holder.stderrWith`; `proc.defaultStderrCapacity`).
- A child that cannot start, or whose pids record cannot be written, also gets an `exit.json`, with reason `exited`, code -1 and the error as `stderr`. The holder then exits 1 (`holder.openHolder`).

Example. The record of a child that exited 0 with no output, synthetic:

```json
{"reason":"exited","code":0,"stdoutEnd":15,"stdout":"","stderr":""}
```

#### Child

- The child is `PATH` with `ARGS` as its argument vector, run without a shell (`cmd/orrery.runHolder`, `proc.Start`). `PATH` must be absolute, and a relative `PATH` fails to start (`proc.Start`).
- The child runs in the holder's working directory and with the holder's environment (`holder.StartChild`, `proc.Start`).
- The child runs in its own process group, whose id is its process id (`proc.Start`, `holder.Child.Pid`). When the child is reaped, any process left in its group is sent SIGKILL (`proc.Process.run`).
- Its stdin is a pipe that controller `write` frames feed. Its stdout goes to the spool. Its stderr keeps a 64 KiB tail (`holder.Child.Write`, `proc.Start`).
- A gate child (`GateLimit` above zero, which `orrery holder --gate-limit` sets) differs. Its stdin is closed at start, so no `write` frame reaches it. Nothing is spooled. Its stdout and its stderr each keep their own tail, the newest `GateTail` bytes, which the exit record carries as `stdout` and `stderr`. `GateTail` is 8 KiB unless the caller names another size: `orrery holder --gate-tail` sets it, from 1 to 16 MiB, and a step's gate and an evidence capture each keep 1 MiB (`holder.MaxGateTail`, `holder.ChildConfig.GateTail`, `holder.StartChild`; `holder.LaunchConfig.GateTail`, `holder.holderCommand`; `enginehost.gateRun.Tail`, `enginehost.gateHeld`; `enginehost.stepGateLogTail`, `enginehost.evidenceCaptureLogTail`, `enginehost.evidenceCapture.captureGate`). Once its limit has passed on the holder's clock from the child's start, it is stopped as a stop stops it, and its reason is `limit` (`holder.gateTailBytes`, `holder.ChildConfig.GateLimit`, `holder.StartChild`, `holder.Child.limitAfter`; `proc.Config.StdoutCapacity`).
- A stop sends SIGTERM to the child's group at once. If the child is not reaped within the grace, SIGKILL goes to the group. The grace is `ChildConfig.Grace`, and 5 s when it is zero. It is counted on the holder's clock (`holder.DefaultGrace`, `holder.Child.Stop`, `holder.Child.killAfterGrace`).

#### Wire protocol

The attached frame carries `consumed` and `replayFrom` as well. Those keys are additive, since a reader ignores unknown keys, so `WireVersion` stays 1 (see [Versioning](#versioning)).

Framing:

- A frame is one JSON object on one line, ending in LF. A frame, LF included, is at most 32 MiB (`holder.MaxFrameBytes`, `holder.WriteFrame`, `holder.FrameReader.readLine`).
- Every frame has a string `type`. Keys are camelCase. A field at its zero value is omitted from a frame. Byte fields (`data`) are base64 strings. A reader ignores unknown keys (`holder.Frame`, `holder.FrameReader.Read`).
- A line that is not JSON, has no `type`, or is longer than the limit closes the connection (`holder.FrameReader.Read`, `holder.holder.serve`).

Frozen in every version:

- `hello`. A controller sends `{"type":"hello","version":N}` first. The holder answers with its own version and session, `{"type":"hello","version":N,"session":"NAME"}`. The holder does not compare versions, so the controller decides what it attaches (`holder.holder.greet`, `holder.Conn.greet`).
- `stop`. After `hello`, any connection may send `{"type":"stop"}`. The holder stops the child as [Child](#child) says. It sends `{"type":"stopped"}` to each connection that sent stop once `exit.json` is written. A stop that arrives after that is answered at once (`holder.holder.stop`, `holder.holder.requestStop`, `holder.holder.answerStops`, `holder.holder.answerStopped`, `holder.holder.run`).
- `stopped`. `{"type":"stopped"}` carries no fields.

Version 1 (`WireVersion` 1):

- `attach`. A controller sends `{"type":"attach","replayFrom":N}`. The holder accepts it when `N` is within the retained `[start, end]` and the child has not ended. It answers `{"type":"attached","start":S,"end":E,"consumed":C,"replayFrom":R,"written":[...]}`, where `consumed` and `replayFrom` are the spool's last acked offsets, so the controller acks at or above them, and `written` lists every write id recorded so far, in the order received. The stream then follows: `out` frames from `N` in order, then more as the child writes. An attach refused for its offset or because the child has ended gets an `error` frame and closes its connection, and the attached controller stays. An attach on the connection that is already the attached controller is refused the same way, and that connection detaches. A new attach replaces the attached controller, whose connection closes (`holder.holder.attach`, `holder.holder.streamTo`).
- `out`. `{"type":"out","offset":N,"data":"..."}` carries stdout from offset `N`, at most 64 KiB per frame (`holder.ChunkBytes`, `holder.holder.streamTo`).

Example, synthetic. The controller attaches from the start of the output, which is 15 bytes long:

```
controller  {"type":"hello","version":1}
holder      {"type":"hello","version":1,"session":"example-session"}
controller  {"type":"attach"}
holder      {"type":"attached","end":15}
holder      {"type":"out","data":"ZXhhbXBsZSBvdXRwdXQK"}
```

- `exit`. `{"type":"exit","exit":{...}}` carries the record of [Exit file](#exit-file). The holder sends it to the attached controller once that controller's `out` frames reach `stdoutEnd`, then ends the streams and quits (`holder.holder.streamTo`, `holder.holder.run`).
- `ack`. From the attached controller only, `{"type":"ack","consumed":N,"replayFrom":M}`. It has no answer. It moves `consumed` and trims the spool (see [Spool](#spool)) (`holder.holder.ack`).
- `write`. From the attached controller only, `{"type":"write","id":"ID","data":"..."}`. `ID` is 1 to 256 bytes. An `ID` already recorded is answered with `{"type":"written","id":"ID","duplicate":true}`, and nothing is written. A new `ID` is recorded when it arrives and queued. Its data goes to the child's stdin in the order recorded, and `{"type":"written","id":"ID"}` answers once the data is written. If the child's stdin refuses the data, the holder answers `{"type":"error","id":"ID","message":"..."}`. The connection stays open, and the `ID` stays recorded, so a repeat is a duplicate (`holder.holder.write`, `holder.holder.writeStdin`, `holder.holder.deliver`).
- `error`. `{"type":"error","message":"..."}` refuses a frame. The holder closes the connection after it refuses an attach, an ack, a write, or a frame it does not serve. It does not close after a failed stdin write (`holder.holder.reject`, `holder.holder.refuse`, `holder.holder.serve`, `holder.holder.deliver`).

#### Versioning

- `WireVersion` is 1 (`holder.WireVersion`). It is raised only by an incompatible change. `hello`, `stop` and `stopped` keep their shape in every version (`holder.WireVersion`).
- A controller attaches only a holder whose version is its own or one less. At version 1 that means version 1, so no holder is one version older yet, and the drain of a one-older holder (see [Reconcile at start](#lifecycle-rules)) applies once `WireVersion` is raised above 1. The controller's version is `holder.WireVersion`, which the manager takes when its `Config.HolderVersion` is zero (`enginehost.Config.HolderVersion`, `enginehost.NewManager`; `holder.Reattach`, `holder.attachBounded`). Any other holder is refused before the controller sends `attach`. The refusal names the session and both versions, the connection closes, and the holder keeps running. Its text is `holder for session S speaks wire version H; this controller attaches versions C and C-1` (`holder.Conn.Attach`, `holder.VersionError`, `holder.VersionError.Error`).
- `Dial` accepts a holder of any version, so `stop` works on every version (`holder.Dial`, `holder.Conn.Stop`).

#### Idle timeout

- A holder with no attached controller for 10 minutes stops its child. `DefaultIdleTimeout` is `10 * time.Minute`. The timer is armed when the holder starts serving, and again when the attached controller's connection ends with no new attach. An attach disarms it. Connections that only send `hello` or `stop` are not controllers (`holder.DefaultIdleTimeout`, `holder.holder.run`, `holder.holder.armIdleLocked`, `holder.holder.disarmIdleLocked`, `holder.holder.attach`, `holder.holder.drop`).
- When the timer fires, the holder stops the child as [Child](#child) says, writes `exit.json` with reason `idle`, and quits. If a stop or a SIGINT or SIGTERM comes first, the reason is `stopped`. `Config.IdleTimeout` sets another timeout, and the timer runs on `Config.Clock` (`holder.Config.IdleTimeout`, `holder.Config.withDefaults`, `holder.holder.armIdleLocked`, `holder.holder.idleExpired`).

### Store

`orreryd` keeps a SQLite store that mirrors each pane's tmux options, records each headless engine session (see [Engine sessions in the store](#engine-sessions-in-the-store)), keeps the live grants of the Pools service (see [Grants in the store](#grants-in-the-store)), keeps the pending pane, headless session, usage-limit and epic notices (see [Pending notices in the store](#pending-notices-in-the-store)), keeps the marks of the usage limits told (see [Told limits in the store](#told-limits-in-the-store)), keeps the kept push times (see [Pushed notices in the store](#pushed-notices-in-the-store)), keeps the owner holds of the Pools service (see [Holds in the store](#holds-in-the-store)), keeps the epics that the Epics service stores (see [Epics in the store](#epics-in-the-store)), and keeps the node runs of each epic, which the interpreter writes (see [Node runs in the store](#node-runs-in-the-store)). Outside the store's tests, `orreryd`'s own writes read it, `orrery serve` reads it for the Connect API (section 12), and `orrery open` and the tree's `N` read it read-only for the known-directory lookup (see `open` under [orrery](#orrery)). A sync reads the pane's row to compare it with the new snapshot, and a tidy also reads the server's rows to find the panes that are gone. `serve` only reads the store, and never writes it.

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
| `write_id` | `TEXT` | the write id of an `engine_prompt` event, `""` on every other event (see [Engine sessions in the store](#engine-sessions-in-the-store)) |
| `answered` | `TEXT` | on an `engine_turn_ended` event, the JSON array of the user-message uuids its turn's result listed, in that order, `[]` when it lists none; `NULL` on every other event, and on a turn end stored before migration `00027_turn_answered.sql` (see [Engine sessions in the store](#engine-sessions-in-the-store)) |

The three pane kinds and their payloads. The examples are synthetic:

- `session_added`: a pane's row is inserted. Payload `{"server":"-L example-server","pane":"%1"}`.
- `session_changed`: fields of a row differ from the snapshot. The payload holds only the differing fields, under the column names above. Payload `{"fields":{"changed":"1700000300","state":"working"}}`. A new row appends `session_changed` after `session_added` when any of its fields is non-empty.
- `session_removed`: a tidy deletes the row of a pane that is gone. Payload `{}`.

Each sync writes in one transaction. Three things start a sync:

- An applied hook frame. Its pane is synced after the frame's tmux writes and observers, outside the frame mutex. A frame whose state change fails syncs nothing.
- A summary run that writes its summary or label. The pane is synced after the write.
- A tidy.

A sync reads the pane's `@orrery_id` and mirrored options from tmux in one command. When the pane has no `@orrery_id`, the sync mints one and writes the row. It sets `@orrery_id` in tmux only after the store write commits.

One lock covers each sync's tmux read, id mint and store write. A sync waits at most 200 ms for a store still opening and then at most 200 ms for the lock, so a hook never waits on the store longer. A sync that waits longer is deferred: orreryd logs `mirror sync deferred` (warning, with `pane` and `reason`, `store still opening` or `lock wait exceeded`), the hook returns, and the pane is marked. One background drain syncs every marked pane once the store is open and the lock is free, reading the pane's options from tmux as they are then. A pane marked again before the drain reaches it is synced once. At shutdown, orreryd waits for the drain after its hook socket and summaries stop and before it closes the store, so every deferred sync and tidy lands. After a failed open, a sync returns at once, silently, and the marks are discarded.

The store write waits at most 200 ms for SQLite's write lock. A write that does not get it fails, rolls back, and is logged as `mirror sync dropped`, as is a sync whose tmux read fails. A dropped sync writes no row or event and sets no id. tmux keeps its options as they were set. The next sync or tidy of the pane writes the difference from the stored row. Events for the values the dropped sync carried are not written, and a deferred sync writes one event for the difference it finds, not one for each hook it stood for. A pane whose id was minted by a dropped sync gets another id on its next sync.

The tidy runs at orreryd start and every 10 s. The first tidy runs once the store is open and, with `--sessions`, the sessions have started, so it follows the `store open` log line. Opening a new store applies every migration, and under heavy disk load that can take seconds. It tidies:

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

A failed `list-panes`, or a listing that does not parse, changes nothing. orreryd logs `tmux server unreachable` (warning) when a server's `list-panes` starts failing and `tmux server reachable again` (info) at its first success after that, not on every tidy. A tidy that waits more than 200 ms for a store still opening or for the lock is deferred as a sync is: orreryd logs `mirror tidy deferred` (warning, with `server` and `reason`), and the drain runs the server's tidy once the store is open and the lock is free. Other tidy failures are logged as `mirror tidy dropped` each time. A failed store write rolls back the whole tidy. A default socket skipped by the directory check logs `tmux default socket skipped` (warning, with `server` and `reason`) when the skip starts and again when its reason changes, not on every tidy.

#### Engine sessions in the store

An engine row is a `sessions` row with `kind` `engine`. Its columns hold these values:

- `state` is one of the session states of [Headless sessions](#headless-sessions).
- `changed` is the unix seconds of the last state change.
- `seen` is the unix seconds of the last `session seen`, or `""`.
- `question` is the open question while the state is `waiting`, or the tool name of an open permission request. An answer, a withdrawn request, a turn's end and reconcile set it to `""`. A row that becomes `stopped`, `failed` or `interrupted` while it waits keeps the question it had.
- `claude_session` is the Claude Code session id, once the process has reported it.
- `dir` is the directory the session runs in. `reason` is described in the table above.

The re-adoption columns hold the offsets that a restarted `orreryd` replays from (migration `00013_engine_readopt.sql`, `internal/store/migrations/00013_engine_readopt.sql: +goose Up`):

- `consumed` is the stdout offset the pump has stored through. It is `NULL` until a launch tracks the row. A row that is `NULL` here, such as one whose holder started before the migration, is cleared at reconcile and not re-adopted. A launch sets it to `0`, and each stdout line, though not the exit line, sets it to that line's end (`store.EngineConsumed`, `enginehost.consumedOf`, `store.toEngineRow`).
- `replay_from` is the offset of the last turn boundary. A launch sets it to `0`, and a line that holds a turn end sets it to that line's end (`enginehost.consumedOf`, `enginehost.Manager.recordLine`).
- `turn_output` is the structured output of the last turn end, stored with `replay_from` (`enginehost.consumedOf`).
- `background` is whether the last background-task report lists a task. A launch turns it off (`enginehost.consumedOf`).
- `prompts_from` is the newest event seq at the session's last launch. `HolderPrompts` lists the session's `engine_prompt` events above it that carry a write id, in seq order (`internal/store/queries.sql: UpdateEngineLaunch`, `internal/store/queries.sql: ListHolderPrompts`; `store.Store.HolderPrompts`).

Each launch is committed before its holder starts, so a launch resets the offsets before the child runs (`enginehost.Manager.spawn`; `store.EngineLaunch`). Each stdout line is one write: its ops and an `EngineConsumed` for the line go in one `Put`, so a line's events and its offset are stored together. A line that holds a turn end is committed, and only then does the pump let the holder trim to its replay-from. A line with no events still advances `consumed`. The exit line stores no offset (`enginehost.Manager.recordLine`, `enginehost.Manager.writeLine`, `enginehost.holdsExit`). Replayed lines record nothing (`enginehost.Manager.replay`).

The writer keeps `EngineConsumed` outside its op limit. A new one merges into a queued `EngineConsumed` of the same session: the offset takes the later value, and each optional field takes the new value when it is set. The search stops at a queued `EngineAdd` or `EngineLaunch` of the session. The queue is one transaction, so the merged offset commits with its lines (`enginehost.Writer.coalesceConsumedLocked`, `enginehost.countsAgainstLimit`, `enginehost.Writer.flush`).

A prompt's write id is stored in its event's `write_id` column and kept out of the payload, so the payload is what it was (`store.EnginePrompt`, `store.EnginePrompt.applyEngine`).

Tidy reads only pane rows, and the typed API's `List` returns only pane rows (see [Connect API](#connect-api)). The engine rows are reached through the Engine service, `session watch`, and the engine messages of the Events stream (see [Connect API](#connect-api)).

The engine kinds of the `events` table are these seven. Each payload is a JSON object with these keys, in camelCase. The examples are synthetic:

- `engine_added`: an engine row is inserted. Payload `{"dir":"/home/user/project","state":"working"}`.
- `engine_changed`: one or more columns change. Payload `{"fields":{"changed":"1700000300","state":"done"}}`. The keys of `fields` are `state`, `seen`, `question`, `claudeSession` and `reason`, for the columns that changed. `changed` is set with each `state` change.
- `engine_prompt`: a prompt is sent to the session. Payload `{"text":"Example prompt."}`. The text is not cut. A `Say` with a `prompt_id` adds the key `promptId` with that id. A `Start` prompt, and a `Say` without a `prompt_id`, have no `promptId` key. The automatic resume of a limited step goes through `Continue`, and it is stored with sender `engine` and the `promptId` that `resumePromptID` derives from the step id and the usage limit's reset (`enginehost.resumePromptID`, `enginehost.stepRun.autoResumeLimited`). `Continue` returns nil and sends nothing for a `promptId` that an `engine_prompt` of the step already carries, so a resume stored before a restart is not sent again (`enginehost.Manager.Continue`). A step prompt adds the key `sender`: `brief` for the step's start prompt, `engine` for a send-back, the judge, the watchdog or the automatic resume, and `owner` for an owner nudge, alone or joined to a watchdog text. A send-back also adds the key `round`, the check round it returns. Each of `sender` and `round` is omitted when it is empty or 0, and a session's `Say` stores neither.
- `engine_block`: a finished content block. Payload keys: `kind`, `role`, `messageId`, `parentToolUseId`, `text`, `toolUseId`, `name`, `input`, `content`, `isError`, and `truncated`. `truncated` is present only when true. `text`, `input` and `content` are each cut to 32768 bytes at a UTF-8 boundary, and `truncated` is true when one of them was cut. `kind` is `text`, `thinking`, `tool_call` or `tool_result`.
- `engine_request`: the session sends a request the host must answer. Payload keys: `kind` (`question`, `permission` or `other`), `requestId`, `toolUseId`, `toolName`, `subtype`, `input` and `refused`. The input is not cut.
- `engine_request_canceled`: the session withdraws a request. Payload keys `requestId` and `toolUseId`. The kind is spelled with one `l`.
- `engine_turn_ended`: a turn ends. Payload keys `subtype`, `isError`, `terminalReason`, `result` and `origin`. The result is not cut. The uuids the turn answered are not in the payload: they are in the event's `answered` column (`store.EngineTurnEnded.applyEngine`, `internal/store/queries.sql: InsertEngineTurnEndedEvent`).

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

Its primary key is `session_id`, `message_id`, `block_index` and `parent_tool_use_id`. A row is removed when its block finishes, and every row of a session is removed when a turn ends, the process exits, the session is stopped, or `orreryd` shuts down without `--holder-scope`, and reconcile at start removes the rows of a session it does not re-adopt. With `--holder-scope` a shutdown keeps the rows, and a re-adopted session keeps its rows, since its replay writes nothing (`enginehost.Manager.recordClosed`, `enginehost.Manager.replay`; see [Lifecycle rules](#lifecycle-rules)). No event is appended for engine text. The Connect `Watch` call sends these rows as `EngineText` messages, which are not events (see [Connect API](#connect-api)).

The `engine_prompt_failed` table holds the failure of each `prompt_id` whose send failed, one row per session and `prompt_id`:

| Column | Type | Meaning |
|---|---|---|
| `session_id` | `TEXT` | the engine row's `id` |
| `prompt_id` | `TEXT` | the `prompt_id` of the `Say` |
| `reason` | `TEXT` | the error of the failed send |

Its primary key is `session_id` and `prompt_id`. A row is written in the same write as the session's `failed` state, and an insert for a key that already has a row keeps the first reason. No event is appended for it, and the Connect `Watch` call does not send it. The 14-day cutoff below removes its rows with their engine row.

The 14-day cutoff of the tidy applies to engine rows too. The cutoff is `now` less 14 days. Each engine row with no event at or after the cutoff is deleted with its `engine_text` and `engine_prompt_failed` rows, and no event is appended, unless its session is live. A session is live from its start, resume or re-adoption until it is stopped, idle-stopped, exits, or `orreryd` shuts down, and a live session keeps its row, its `engine_text` rows and its `engine_prompt_failed` rows through the prune. The same tidy pass prunes events older than the cutoff, so the pass runs only while a tmux server's `list-panes` succeeds. A session that is not live loses its row after 14 days with no event, and a live session that waits for an answer keeps its row.

The `engine_rate_limits` table holds the latest rejected rate limit of each engine session, keyed by `session_id`. Its columns are `status`, `kind` (the rate-limit type, empty when the event gives none), `resets_at` and `at`, both unix seconds (`internal/store/migrations/00016_planner.sql: CREATE TABLE engine_rate_limits`). The recorder stores only a `rejected` status (`enginehost.recorder.ops`). A later one for the same session replaces the row, with `at` set to the op's time (`store.EngineRateLimited`, `store.EngineRateLimited.applyEngine`; `internal/store/queries.sql: UpsertEngineRateLimit`). The store appends no event for it. The row is deleted only by `EngineRateLimitCleared`, which a limited step's resume commits, and no prune deletes it (`store.EngineRateLimitCleared`, `store.EngineRateLimitCleared.applyEngine`; `internal/store/queries.sql: DeleteEngineRateLimit`; `enginehost.stepRun.resumeWith`). A step's planner turn reads the row when the step ends, and a limited step reads it at each turn end (see [Planner turns](#planner-turns) and [Step lifecycle](#step-lifecycle), Usage limit).

#### Steps in the store

The `steps` table (migrations `00004_steps.sql`, `00005_step_watch.sql`, `00006_step_labels.sql`, `00009_step_gate.sql`, `00015_step_readopt.sql`, `00017_step_limit.sql`, `00020_evidence.sql`, `00023_checkout_tails.sql`, `00032_step_stage.sql`, `00035_reviewers.sql` and `00038_release.sql`) has one row per step. Its `id` is the id of the step's `sessions` row. A row is added when the step starts. Its columns are these:

| Column | Type | Meaning |
|---|---|---|
| `id` | `TEXT`, primary key | the step id |
| `agent` | `TEXT` | the agent the step runs as |
| `agents` | `TEXT` | the JSON object of inline agent definitions the step is started with, `''` for none; a respawn passes it again (`internal/store/migrations/00035_reviewers.sql: ALTER TABLE steps ADD COLUMN agents`) |
| `prompt` | `TEXT` | the step's first prompt |
| `output_schema` | `TEXT` | the schema the return must match |
| `workspace` | `TEXT` | the directory the step was started in |
| `revision` | `TEXT` | the start commit |
| `worktree` | `TEXT` | the read-only step's worktree path; `""` for a writing step |
| `checkout_head` | `TEXT` | the HEAD a read-only step's checkout had before its agent ran; `""` for a writing step, and for a row added before migration `00023` |
| `checkout_hash` | `TEXT` | the SHA-256 of a read-only step's checkout state, `git status` and `git diff HEAD`, before its agent ran; `""` for the same rows |
| `done_value` | `TEXT` | the status that ends the step `done` |
| `read_only` | `BOOLEAN` | whether the step is read-only |
| `owned` | `TEXT` | the `owned_paths` as a JSON array; `null` when there are none |
| `unowned` | `INTEGER` | `1` for a writing step started unowned, whose done return runs no `owned` check (see [Step lifecycle](#step-lifecycle)); `0` otherwise, and for a row added before migration `00038_release.sql` (`store.StepAdd.applyEngine`, `store.toStepRow`) |
| `gate` | `TEXT` | the gate argv as a JSON array, as its definition gives it; `null` for no gate, and for an agent gate, which has no argv |
| `gate_limit` | `INTEGER` | the gate limit in nanoseconds; 30 minutes for a step that names no gate |
| `model_limit` | `INTEGER` | the model-time limit in nanoseconds; `0` for a row added before migration `00005` |
| `started` | `INTEGER` | unix seconds when the row was added |
| `checks` | `TEXT` | the checks of every return so far, as a JSON array of `round`, `name`, `ok` and `detail`, with `stdout` and `stderr` where a check kept tails (omitted otherwise); `[]` at start |
| `result` | `TEXT` | the latest return's value, as JSON text |
| `doing` | `TEXT` | the latest doing line from the step's monitor; `""` until one is given |
| `verdicts` | `TEXT` | every monitor and judge verdict so far, as a JSON array of `at`, `kind`, `trip`, `round`, `verdict`, `detail`, `error` and `action`; `[]` at start |
| `status` | `TEXT` | the status the step ended with; `""` while it runs and for the ends that carry none |
| `end_value` | `TEXT` | the end kind of [Step lifecycle](#step-lifecycle); `""` while the step runs |
| `reason` | `TEXT` | why the step ended; `""` while it runs |
| `ended` | `INTEGER` | unix seconds when the step ended; `0` while it runs |
| `epic` | `TEXT` | the epic the step was started with, as migration `00006_step_labels.sql` adds it; `""` for an unlabelled step, and for a row added before that migration |
| `task` | `TEXT` | the task slug the step was started with, as migration `00006_step_labels.sql` adds it; `""` when it works on no one task, and for a row added before that migration |
| `loop_round` | `INTEGER` | the step loop's round, the number of its latest round; `0` at start (`enginehost.stepRun.loopState`) |
| `loop_continues` | `INTEGER` | how many failing returns the session has been sent back to fix, up to three |
| `loop_nudge` | `TEXT` | the watchdog's nudge text that the next turn end is continued with; `""` when none is pending |
| `loop_owner_nudge` | `TEXT` | the owner's nudge text that the next turn end is continued with; `""` when none is pending |
| `loop_nudged` | `BOOLEAN` | whether the watch has spent the step's one nudge |
| `loop_held` | `BOOLEAN` | whether an owner hold holds the step |
| `loop_judge_sent` | `BOOLEAN` | whether the judge has sent the step back |
| `loop_seq` | `INTEGER` | the newest event seq of the step's session when its latest return was stored; a turn end at or before it is that return's (`store.Store.StepTurn`, `store.Store.OpenSteps`) |
| `gate_grant` | `TEXT` | the grant id of the gate that runs; `""` when none runs |
| `gate_holder` | `TEXT` | the holder directory of the gate that runs; `""` when none runs |
| `gate_round` | `INTEGER` | the round the gate that runs is in; `0` when none runs |
| `gate_name` | `TEXT` | the name the step's gate was resolved by, as migration `00009_step_gate.sql` adds it; `""` for a step that names no gate, and for a row added before that migration |
| `gate_source` | `TEXT` | `epic` or `project`, the kind of file the gate was defined in, as migration `00009_step_gate.sql` adds it; `data` for an agent gate given as data (see [Agent gates](#agent-gates)); `""` for a step that names no gate, and for a row added before that migration |
| `gate_needs` | `TEXT` | the gate's needs as a JSON array of `{"pool", "count"}` objects, as migration `00009_step_gate.sql` adds it; `null` for a step that names no gate, and `[]` for a row added before that migration |
| `loop_limited` | `BOOLEAN` | whether a usage limit holds the step's loop, so that it waits for a resume; `0` at start and for a row added before migration `00017_step_limit.sql` (`store.StepLoop.applyEngine`; `internal/store/queries.sql: ListLimitedSteps`) |
| `loop_limit_kind` | `TEXT` | the kind of the usage limit that holds the loop; `""` when none holds it or the kind is unknown, as migration `00017_step_limit.sql` adds it (`store.StepLoop.applyEngine`) |
| `loop_limit_resets` | `INTEGER` | when the usage limit that holds the loop resets, in unix seconds; `0` when none holds it or the reset is unknown, as migration `00017_step_limit.sql` adds it (`store.StepLoop.applyEngine`, `store.toStepRow`) |
| `loop_gate_from` | `INTEGER` | for an agent gate step, when the owner last resumed it from a usage limit, in unix seconds; its gate's `limit` counts from it. `0` when it was never resumed, and the limit counts from the step's start. Migration `00020_evidence.sql` adds it (`store.StepLoop.applyEngine`, `store.toStepRow`) |
| `loop_stage` | `TEXT` | the stage the step's loop is in: `turn`, `checks`, `gate`, `judge`, `held` or `limited`, as [Step lifecycle](#step-lifecycle) gives when each is written; `""` for a row added before migration `00032_step_stage.sql`, which adds it (`store.StepLoop.applyEngine`, `store.toStepRow`) |
| `loop_stage_from` | `INTEGER` | unix seconds when `loop_stage` started; `0` when none was stored, and for a row added before migration `00032_step_stage.sql`, which adds it (`store.StepLoop.applyEngine`, `store.toStepRow`) |

The `sessions` row of a step has `kind` `step`, its `dir` (the workspace, or the worktree of a read-only step), and the same state, `reason` and `claude_session` columns as an engine row (see [Engine sessions in the store](#engine-sessions-in-the-store)). Its engine events are the engine kinds of the `events` table.

The `steps` table is never pruned. The 14-day tidy deletes only engine rows (`kind` `engine`), and the unfinished text and `engine_prompt_failed` rows of those rows, so a step's `sessions` row, its `engine_text` rows and its `engine_prompt_failed` rows are kept too. Events of step rows are pruned with the other events, under the same rule.


#### Grants in the store

The `grants` and `grant_members` tables (migration `00007_grants.sql`, and `grants.holder` from `00015_step_readopt.sql`) hold the live grants of the Pools service (see [Pools](#pools)). A grant's rows are written before its client is told of the grant, and ending the grant deletes them. The tidy does not delete grants.

`grants` has these columns:

| Column | Type | Meaning |
|---|---|---|
| `id` | `TEXT`, primary key | the grant id |
| `project` | `TEXT` | the project's absolute path |
| `client_pid` | `INTEGER` | the process id of the client that holds the grant |
| `client_start` | `INTEGER` | the client's start time from `/proc/<pid>/stat`, which tells whether a restored grant's client is still that process |
| `pools` | `TEXT` | the definitions of the pools the request needed, as a JSON array. Each has the keys `name`, `host`, `cap`, `members`, `wake`, `wakeLimit`, `status`, `statusLimit`, `admit` and `admitLimit`. The limits are in nanoseconds |
| `granted` | `INTEGER` | unix seconds when the grant was stored |
| `command` | `TEXT` | the argv of the grant's command as a JSON array, `[]` when the client sent none. Default `'[]'`, so a row added before migration `00008_holds.sql` reads `[]` |
| `holder` | `TEXT` | the holder directory the grant lasts with, as its request named it; `""` when it names none. Default `''`, so a row added before migration `00015_step_readopt.sql` reads `""` |

`grant_members` has these columns:

| Column | Type | Meaning |
|---|---|---|
| `member` | `TEXT`, primary key | the member's name |
| `grant_id` | `TEXT` | the grant the member belongs to |
| `pool` | `TEXT` | the pool the member belongs to |
| `position` | `INTEGER` | the member's place in its grant, counted from 0 |

The primary key of `grant_members` is the member's name, so a member is in at most one stored grant. The store also refuses a grant that includes a member a live grant already has.

#### Holds in the store

The `holds` table (migration `00008_holds.sql`) has one row per owner hold (see [Owner holds](#owner-holds)):

| Column | Type | Meaning |
|---|---|---|
| `member` | `TEXT`, primary key | the held member's name |
| `since` | `INTEGER` | unix seconds when the hold was placed |

`orreryd` reads the table at start, before it reads the grants. A hold is stored before it takes effect, and freeing one deletes its row.

#### Pending notices in the store

The `pending_notices` table (migration `00010_pending_notices.sql`) has one row per pending pane notice, per pending headless session notice, per pending usage-limit notice and per pending epic notice (see [Notice routing](#notice-routing), [Step lifecycle](#step-lifecycle) and [Planner turns](#planner-turns)):

| Column | Type | Meaning |
|---|---|---|
| `id` | `INTEGER`, primary key | the row's id. An id is never reused |
| `pane` | `TEXT` | the notice's pane; `""` for a session or epic notice |
| `session` | `TEXT` | the notice's session id; `""` for a pane or epic notice |
| `state` | `TEXT` | the notice's state |
| `time` | `INTEGER` | unix seconds when the notice was recorded |
| `phase` | `TEXT` | where the notice is: `deciding`, `held` (its pane is watched), `delayed` (the 10-second judge wait of a pane `done`), `desk` or `sending`. `sending` is a notice handed to the phone whose publish has not returned. A session notice, an epic notice and a usage-limit notice are never held or judged, so their phase is `deciding`, `desk` or `sending` |
| `due` | `INTEGER` | unix milliseconds when the phase's wait runs out; `0` for none |
| `delay_served` | `INTEGER` | `1` once the judge wait started, else `0`; always `0` for a session or epic notice |
| `watch_logged` | `INTEGER` | `1` once the `watching` line was written, else `0`; always `0` for a session or epic notice |
| `source_kind` | `TEXT` | `pane` for a pane notice, `session` for a session notice, `epic` for an epic notice, `limit` for a usage-limit notice |
| `source` | `BLOB` | for a pane notice, the pane's transition as JSON, with the hook input included, so a restored judge reads the stored `last_assistant_message`; for a session notice, the session notice as JSON: its session, directory, state and body; for an epic notice, the epic notice as JSON: its epic, title and body; for a usage-limit notice, its kind, reset and step ids as JSON |
| `silent` | `INTEGER` | `1` when the push handed off was silent, else `0`; always `0` for a session or epic notice. Added by migration `00011_pending_notice_silent.sql` |
| `epic` | `TEXT` | the epic's id, for an epic notice; `""` otherwise. Added by migration `00019_planner_controls.sql` |
| `limit_key` | `TEXT` | the usage limit's key, `<kind>@<reset>`, for a usage-limit notice; `""` otherwise. Added by migration `00026_limit_told.sql` |

The `pending_notice_ids` table has one row, whose `high` is the highest `id` the table has ever stored. Deleting rows does not lower it, so ids keep rising.

| Column | Type | Meaning |
|---|---|---|
| `high` | `INTEGER` | the highest `id` stored |

A row is written when its notice is recorded, with phase `deciding`. A pane notice's row is also written at each hold, with phase `held`, and when its judge wait starts, with phase `delayed`. A row is written again before its desk post, with phase `desk`, which it keeps until it escalates. Each row write runs under the router's store-call bound. The router arms it through `route.Config.StoreAfter`, an injected timer: it is called with the 200 ms bound and a cancel func, and it returns a stop func. `orreryd` leaves `StoreAfter` nil, which uses `time.AfterFunc` (`route.Config`, `route.New`, `route.afterFunc`; `route.storeDeadline`, `route.Router.armed`). A write whose bound fires before it returns reads as failed, with an error that wraps `context.DeadlineExceeded` (`route.Router.bounded`). A write that fails or runs past it is logged by `orreryd`, and the notice routes as it would. Later row writes and the next stop retry it. The row of an epic notice carries the told mark of its turn or tell, which its first write applies (see [Planner turns](#planner-turns)). The row is deleted when the notice ends, after its sinks have been handed the notice, except for a notice handed to the phone. That notice is written `sending` before its push, and its row is deleted when the publish returns, with or without an error.

#### Pushed notices in the store

The `pushed_notices` table (migration `00012_pushed_notices.sql`) has one row per kept push time (see [orreryd](#orreryd)):

| Column | Type | Meaning |
|---|---|---|
| `pane` | `TEXT`, primary key with `session` | the pane whose push is kept; `""` for a session |
| `session` | `TEXT`, primary key with `pane` | the session id whose push is kept; `""` for a pane |
| `time` | `INTEGER` | unix seconds of the kept push |

Before each push is published, `orreryd` writes the row of its pane or session with the push's time, so a newer push replaces the row. The clear that ends a kept push removes its row (see [Clear payload](#clear-payload)).

The `pushed_epic_notices` table (migration `00019_planner_controls.sql`) has one row per epic whose push is kept:

| Column | Type | Meaning |
|---|---|---|
| `epic` | `TEXT`, primary key | the epic whose push is kept |
| `time` | `INTEGER` | unix seconds of the kept push |

An epic's push is written and cleared with the same rules as a pane's or session's. `pushed_notices`, `pushed_epic_notices`, `pushed_limit_notices` and `pushed_alert_notices` are read in one read transaction, so they come from one snapshot (`store.Store.Pushed`).

At start, once the store opens and before the router restores its rows, `orreryd` reads the push tables, and each row's time becomes that key's kept push time. Rows for gone panes, sessions or epics are not pruned.

The `pushed_limit_notices` table (migration `00018_pushed_limit.sql`) has one row per kept usage-limit push (see [Step lifecycle](#step-lifecycle), Usage limit):

| Column | Type | Meaning |
|---|---|---|
| `limit_key` | `TEXT`, primary key | the limit key of the push, `<kind>@<reset>` |
| `time` | `INTEGER` | unix seconds of the kept push |

`PutPushed` writes the row for a limit key, `DeletePushedLimit` deletes it, and `Pushed` reads it with the rows of the `pushed_notices` table (`internal/store/migrations/00018_pushed_limit.sql: CREATE TABLE pushed_limit_notices`; `store.Store.PutPushed`, `store.Store.DeletePushedLimit`, `store.Store.Pushed`; `notice.Notices.putRow`, `notice.Notices.deleteRow`).

The `pushed_alert_notices` table (migration `00031_pushed_alert.sql`) has one row per kept alert push (see [Alert intake](#alert-intake)):

| Column | Type | Meaning |
|---|---|---|
| `alert` | `TEXT`, primary key | the alert's name, the key of the push |
| `time` | `INTEGER` | unix seconds of the kept push |

`PutPushed` writes the row for an alert, `DeletePushedAlert` deletes it, and `Pushed` reads it with the rows of the other push tables (`internal/store/migrations/00031_pushed_alert.sql: CREATE TABLE pushed_alert_notices`; `store.Store.PutPushed`, `store.Store.DeletePushedAlert`, `store.Store.Pushed`; `notice.Notices.putRow`, `notice.Notices.deleteRow`).

Until the store opens, `orreryd` holds the seen writes it receives in memory. When the store opens, it applies each held seen write to the kept pushes: a seen write clears each kept push at or before its time, once. A pane or session pushed before the store opened keeps only its new time: the row read back for it and any seen held for it are dropped.

Each read and write of a row, a delete included, has a 200 ms limit. A call that fails or runs past it is logged by `orreryd`, and the push or clear is still sent.

#### Told limits in the store

The `told_limit_steps` table (migration `00026_limit_told.sql`) holds the told mark of each usage limit. It has one row for each step id that the limit's last told notice named (see [Step lifecycle](#step-lifecycle), Usage limit).

| Column | Type | Meaning |
|---|---|---|
| `limit_key` | `TEXT`, primary key with `step` | the limit key, `<kind>@<reset>` |
| `step` | `TEXT`, primary key with `limit_key` | a step id that the limit's last told notice named |

A limit with no row has no mark. `ToldLimits` reads every mark, `TellLimit` replaces the rows of one limit in one write, and `ForgetLimit` deletes them (`store.Store.ToldLimits`, `store.Store.TellLimit`, `store.Store.ForgetLimit`; `internal/store/queries.sql: InsertToldLimitStep`, `internal/store/queries.sql: DeleteToldLimit`, `internal/store/queries.sql: ListToldLimitSteps`).

#### Epics in the store

Migration `00014_epics.sql` creates the epic tables and the triggers on them. It creates tables and triggers only, and it alters and drops nothing that exists (`internal/store/migrations/00014_epics.sql: +goose Up`). Its down step drops them (`internal/store/migrations/00014_epics.sql: +goose Down`). Nine tables hold the epics: `epics`, with one row per epic; seven section tables, with one row per entry of an ordered section; and `epic_decisions`, with one row per decision. None of them declares a foreign key.

The `epics` table has these columns:

| Column | Type | Meaning |
|---|---|---|
| `id` | `TEXT`, primary key | the epic's id, `<repo-id>/<epic-slug>` (`enginehost.epicIDPattern`) |
| `repository` | `TEXT` | the absolute path of the git repository |
| `title` | `TEXT` | the epic's name |
| `approved` | `TEXT` | the sentence of the decision line that approved the contract |
| `intent` | `TEXT` | what the owner asked for |
| `care` | `TEXT` | the level of care: `limited`, `serious` or `severe` |
| `approach` | `TEXT` | how the epic will be done |
| `release_then` | `TEXT` | the Then line that closes the release |
| `checks` | `TEXT` | the checks the machines run |
| `phase` | `TEXT` | the phase, `accepted` from creation |
| `created` | `INTEGER` | unix seconds of creation |
| `changed` | `INTEGER` | unix seconds of the last write: the creation, then each answer |
| `base` | `TEXT` | the full commit the epic starts from; `''` for an epic stored before migration `00020_evidence.sql` (`internal/store/migrations/00020_evidence.sql: ALTER TABLE epics ADD COLUMN base`) |
| `summary` | `TEXT` | the epic's summary, written once from its contract by the summary loop of [Epics](#epics); `''` until it is written (`internal/store/migrations/00034_epic_summary.sql: ALTER TABLE epics ADD COLUMN summary`) |

(`internal/store/migrations/00014_epics.sql: CREATE TABLE epics`)

Each section table has an `epic` column and a `position` column, which counts from 1 in the contract's order. Its primary key is `(epic, position)`. The other columns are these:

| Table | Columns | Migration lines |
|---|---|---|
| `epic_failures` | `id`, `text`, `rating`, `response` | `internal/store/migrations/00014_epics.sql: CREATE TABLE epic_failures` |
| `epic_additions` | `location`, `what`, `why`, `cost` | `internal/store/migrations/00014_epics.sql: CREATE TABLE epic_additions` |
| `epic_requirements` | `id`, `text`, `evidence`, `kind`, and `item`, the evidence item as JSON, which `00020_evidence.sql` adds (`internal/store/migrations/00014_epics.sql: CREATE TABLE epic_failures`) | `internal/store/migrations/00014_epics.sql: CREATE TABLE epic_requirements` |
| `epic_premises` | `id`, `text`, `if_wrong` | `internal/store/migrations/00014_epics.sql: CREATE TABLE epic_premises` |
| `epic_exclusions` | `text` | `internal/store/migrations/00014_epics.sql: CREATE TABLE epic_exclusions` |
| `epic_alternatives` | `alternative`, `why_not`, `reconsider` | `internal/store/migrations/00014_epics.sql: CREATE TABLE epic_alternatives` |
| `epic_release_steps` | `step`, `target`, `undo` | `internal/store/migrations/00014_epics.sql: CREATE TABLE epic_release_steps` |

The `epic_decisions` table has `epic` (`TEXT`), `seq` (`INTEGER`), `client_id` (`TEXT`), `text` (`TEXT`) and `time` (`INTEGER`), and from migration 00019 the `question` column (see below). Its primary key is `(epic, seq)`, and `(epic, client_id)` is unique (`internal/store/migrations/00014_epics.sql: CREATE TABLE epic_decisions`). `seq` counts from 1 in recording order. A create records its seed decisions as `seq` 1 to `n`, each dated at the epic's creation. `client_id` is the decision's id (`store.insertEpicSections`, `store.appendDecision`).

Two triggers guard `epic_decisions`. `epic_decisions_no_update` runs before any `UPDATE`, and `epic_decisions_no_delete` runs before any `DELETE`. Each aborts with `epic decisions are append-only` (`internal/store/migrations/00014_epics.sql: CREATE TRIGGER epic_decisions_no_update`, `internal/store/migrations/00014_epics.sql: CREATE TRIGGER epic_decisions_no_delete`).

Migration `00016_planner.sql` adds two columns to `epics`: `paused`, an `INTEGER` that is `1` while the epic is paused and `0` otherwise, and `parked`, a `TEXT` that is `''` when the epic is not parked (`internal/store/migrations/00016_planner.sql: ALTER TABLE epics ADD COLUMN paused`, `internal/store/migrations/00016_planner.sql: ALTER TABLE epics ADD COLUMN parked`). It creates the tables below and `engine_rate_limits` (`internal/store/migrations/00016_planner.sql: CREATE TABLE engine_rate_limits`, see [Engine sessions in the store](#engine-sessions-in-the-store)). Its down step drops them and both columns (`internal/store/migrations/00016_planner.sql: +goose Down`). Every table below has an `epic` column, and none declares a foreign key.

| Table | Columns | Migration lines |
|---|---|---|
| `epic_triggers` | `id`, `cause`, `state` (`pending`, `claimed` or `consumed`), `turn` (`0` while pending), `created`; primary key `(epic, id)` | `internal/store/migrations/00016_planner.sql: CREATE TABLE epic_triggers` |
| `epic_turns` | `seq`, `step`, `state` (`running`, `succeeded`, `failed` or `free`), `reason`, `seen`, `outline`, `outcome`, `started`, `ended` (`0` while running); primary key `(epic, seq)`. A partial unique index allows one `running` turn per epic. | `internal/store/migrations/00016_planner.sql: CREATE TABLE epic_turns`, `internal/store/migrations/00016_planner.sql: CREATE UNIQUE INDEX epic_turns_one_running` |
| `epic_turn_decisions` | `turn`, `position`, `text`; primary key `(epic, turn, position)` | `internal/store/migrations/00016_planner.sql: CREATE TABLE epic_turn_decisions` |
| `epic_tasks` | `seq`, `turn`, `title`, `deliverable`, `status` (`open` when created), and `gate` and `evidence` (the evidence item as JSON), which `00020_evidence.sql` adds in place of `task_check` (`internal/store/migrations/00016_planner.sql: CREATE TABLE epic_questions`); primary key `(epic, seq)` | `internal/store/migrations/00016_planner.sql: CREATE TABLE epic_tasks` |
| `epic_task_files` | `task`, `position`, `path`; primary key `(epic, task, position)` | `internal/store/migrations/00016_planner.sql: CREATE TABLE epic_task_files` |
| `epic_task_requirements` | `task`, `position`, `requirement`; primary key `(epic, task, position)` | `internal/store/migrations/00016_planner.sql: CREATE TABLE epic_task_requirements` |
| `epic_task_contributes` | `task`, `position`, `requirement`, the Requirements a task contributes to without claiming them; primary key `(epic, task, position)`; written by `store.insertTasks` and read by `store.loadPlanSections` | `internal/store/migrations/00037_assertions.sql: CREATE TABLE epic_task_contributes`; `internal/store/queries.sql: InsertEpicTaskContribute`, `internal/store/queries.sql: ListEpicTaskContributes` |
| `epic_questions` | `seq`, `turn`, `text`, `state` (`open` when created, `answered` when a decision answers it, `replaced` when a later question replaces it), and from `00039_questions.sql` `label`, `choices` (JSON), `recommended`, `why`, `confidence`, `replaces`, `replaced_by` and `choice`; primary key `(epic, seq)` | `internal/store/migrations/00016_planner.sql: CREATE TABLE epic_questions` |

Migration `00019_planner_controls.sql` adds the columns and the table below, and changes nothing else (`internal/store/migrations/00019_planner_controls.sql: +goose Up`). Its down step drops them (`internal/store/migrations/00019_planner_controls.sql: +goose Down`).

- `epics.retried` (`INTEGER`, `0`): the highest turn seq at the last retry. A failed turn at or below it does not count toward a park (see [Planner turns](#planner-turns)).
- `epic_turns.told`, `epic_turns.stop` and `epic_turns.resets` (`INTEGER`, `0`): the told marker of a turn, the flag of a stop asked while the turn runs, and the unix reset time of the usage limit that freed the turn, `-1` for a limit that names no reset time, and `0` for none.
- `epic_decisions.question` (`INTEGER`, `0`): the seq of the question the decision answers, `0` for a note.
- `pending_notices.epic` (`TEXT`, `""`): the epic of an epic notice (see [Pending notices in the store](#pending-notices-in-the-store)).
- The table `pushed_epic_notices`, with the columns of [Pushed notices in the store](#pushed-notices-in-the-store).

Migration `00020_evidence.sql` adds the gate and evidence tables below and the columns that name them. It alters `epics`, `epic_requirements` and `epic_tasks`, and it drops `epic_tasks.task_check`. It also adds `steps.loop_gate_from` (see [Steps in the store](#steps-in-the-store)). Its down step reverses each change (`internal/store/migrations/00020_evidence.sql: ALTER TABLE epics ADD COLUMN base`, `internal/store/migrations/00020_evidence.sql: ALTER TABLE epic_requirements ADD COLUMN item`, `internal/store/migrations/00020_evidence.sql: +goose Up`, `internal/store/migrations/00020_evidence.sql: ALTER TABLE steps ADD COLUMN loop_gate_from`; `internal/store/migrations/00020_evidence.sql: +goose Down`). Each stored task's check becomes its evidence, `{"conclusion": <check>, "judge": ""}`, so the check's text is kept (`internal/store/migrations/00020_evidence.sql: UPDATE epic_tasks SET evidence`).

| Table | Columns | Migration lines |
|---|---|---|
| `epic_gates` | `epic`, `position`, `name`, `argv` (JSON), `limit_seconds`, `needs` (JSON list of `{pool, count}`), `agent`, `must` (JSON) and `must_not` (JSON); primary key `(epic, position)` | `internal/store/migrations/00020_evidence.sql: CREATE TABLE epic_gates` |
| `epic_gate_lists` | `epic`, `list` (`done` or `review`), `position`, `gate`; primary key `(epic, list, position)` | `internal/store/migrations/00020_evidence.sql: CREATE TABLE epic_gate_lists` |
| `evidence_runs` | `id`, `epic`, `item`, `revision`, `phase` (`red` or `green`), `source` (`captured` or `supplied`), `state` (`capturing`, `judging` or `ended`), `spec` (the item as JSON), `grant_id`, `holder`, `exit`, `timed_out`, `not_captured`, `log_hash`, `log_bytes` (the stdout log), `stderr_hash`, `stderr_bytes` (the stderr log), `judge_step`, `judges`, `verdict` (`''`, `holds` or `fails`), `judge_reason`, `passed`, `created` and `ended`; primary key `id`, and an index on `(epic, created)` | `internal/store/migrations/00020_evidence.sql: CREATE TABLE evidence_runs`, `internal/store/migrations/00020_evidence.sql: CREATE INDEX evidence_runs_epic_created` |
| `evidence_artifacts` | `run`, `position`, `path`, `state` (`kept`, `missing` or `over_cap`), `hash`, `blob` and `size`; primary key `(run, position)` | `internal/store/migrations/00020_evidence.sql: CREATE TABLE evidence_artifacts` |

The `state` and `source` columns of `evidence_runs` and the `state` column of `evidence_artifacts` take only the values listed, by `CHECK` (`internal/store/migrations/00020_evidence.sql: CREATE TABLE evidence_runs`, `internal/store/migrations/00020_evidence.sql: CREATE TABLE evidence_artifacts`). `judges` counts each judge step a run starts, and `judge_step` holds the latest of them (`internal/store/queries.sql: SetEvidenceRunJudge`).
Migration `00021_task_build.sql` adds the build's state and tables, and alters nothing else. Its down step drops them (`internal/store/migrations/00021_task_build.sql: ALTER TABLE epics ADD COLUMN branch`, `internal/store/migrations/00021_task_build.sql: +goose Up`, `internal/store/migrations/00021_task_build.sql: CREATE TABLE epic_task_reviews`, `internal/store/migrations/00021_task_build.sql: CREATE TABLE epic_task_findings`, `internal/store/migrations/00021_task_build.sql: CREATE TABLE epic_tells`, `internal/store/migrations/00021_task_build.sql: +goose Down`).

- `epics.branch` (`TEXT`, `''`): the state of the epic's branch, `''`, `creating` or `created` (`internal/store/migrations/00021_task_build.sql: ALTER TABLE epics ADD COLUMN branch`).
- `epic_tasks` gains these columns, each `NOT NULL` with the default shown (`internal/store/migrations/00021_task_build.sql: +goose Up`): `stage` (`''`, `red`, `implement`, `green`, `review` or `verify`), `base` (`''`), `attempt` (`0`), `rerun` (`0`), `no_verdict` (`0`), `held` (`''`, `stopped` or `parked`), `step`, `red_run`, `green_run`, `revision`, `failure`, `review` (`''`, `unreviewed` or `reviewed`), `refs`, `gate_pos` (`0`) and `finding_pos` (`0`). The migration leaves `status` as it was: a task is `open` until its build accepts it (`done`) or fails it (`failed`), as [Task builds](#task-builds) gives. A task stored before O3d3 has every default, so on an accepted epic with a base, its build starts at red like a new task.
- `epic_task_reviews` (`epic`, `task`, `attempt`, `position`, `gate`, `step` and `verdict`, which is `holds` or `fails`), with primary key `(epic, task, attempt, position)` (`internal/store/migrations/00021_task_build.sql: CREATE TABLE epic_task_reviews`).
- `epic_task_findings` (`epic`, `task`, `attempt`, `review`, `position`, `id`, `claim`, `citation`, `location`, `evidence`, `verify_by`, `step` (`''`), `result` (`''`, `confirmed`, `dropped` or `undecided`) and `reason` (`''`)), with primary key `(epic, task, attempt, review, position)` (`internal/store/migrations/00021_task_build.sql: CREATE TABLE epic_task_findings`).
- `epic_tells` (`epic`, `seq`, `text`, `at` (unix seconds) and `told` (`0`)), with primary key `(epic, seq)` (`internal/store/migrations/00021_task_build.sql: CREATE TABLE epic_tells`).

The Go types are `TaskBuild`, `TaskReview`, `TaskFinding`, `TaskFindingResult` and `EpicTell` (`store.TaskBuild`, `store.TaskFinding`, `store.TaskReview`, `store.TaskFindingResult`, `store.EpicTell`). `BuildEpics` lists, in id order, the epics that have a base, whose phase is not ended, and hold an open task (`internal/store/queries.sql: ListBuildEpics`; `store.Store.BuildEpics`).

Migration `00022_workflow.sql` adds the pinned workflow and backfills the epics stored before it. It changes no other column or table. Its down step drops them (`internal/store/migrations/00022_workflow.sql: ALTER TABLE epics ADD COLUMN workflow`, `internal/store/migrations/00022_workflow.sql: ALTER TABLE epics ADD COLUMN workflow_text`, `internal/store/migrations/00022_workflow.sql: ALTER TABLE epics ADD COLUMN workflow_sha256`, `internal/store/migrations/00022_workflow.sql: CREATE TABLE epic_workflow_nodes`, `internal/store/migrations/00022_workflow.sql: +goose Down`).

- `epics.workflow`, `epics.workflow_text` and `epics.workflow_sha256` (`TEXT`, `''`): the workflow's name, its canonical text and the lowercase hex sha256 of that text (`internal/store/migrations/00022_workflow.sql: ALTER TABLE epics ADD COLUMN workflow`, `internal/store/migrations/00022_workflow.sql: ALTER TABLE epics ADD COLUMN workflow_text`, `internal/store/migrations/00022_workflow.sql: ALTER TABLE epics ADD COLUMN workflow_sha256`).
- `epic_workflow_nodes` (`epic`, `position`, `id`, `parent` and `kind`, with `position` an `INTEGER`), with primary key `(epic, position)`: one row for each node of the pinned workflow, in preorder, with `position` counted from 0. `parent` is the id of the node that encloses it, `''` at the top level, and `kind` is the kind's name or `loop`. Node rows carry no settings (`internal/store/migrations/00022_workflow.sql: CREATE TABLE epic_workflow_nodes`; `store.EpicWorkflow`, `store.EpicWorkflowNode`).
- The migration gives each epic stored before it the workflow `epic`, the built-in text and its sha256, and two node rows, `(0, plan, '', plan)` and `(1, build, plan, build)` (`internal/store/migrations/00022_workflow.sql: UPDATE epics SET workflow`, `internal/store/migrations/00022_workflow.sql: INSERT INTO epic_workflow_nodes`).

`CreateEpic` writes the workflow columns with the head, and the node rows after the sections, in the same write as the head, the sections and the `created` trigger (`store.Store.CreateEpic`, `store.insertWorkflowNodes`). `Epic` and `Epics` read the columns back, and the node rows in position order (`store.Store.Epic`, `store.loadEpicWorkflowNodes`, `store.Store.Epics`, `store.epicHeadOf`; `internal/store/queries.sql: InsertEpicWorkflowNode`, `internal/store/queries.sql: ListEpicWorkflowNodes`). The store has no query that updates the workflow columns or the node rows (`internal/store/queries.sql: InsertEpic`, `internal/store/queries.sql: InsertEpicWorkflowNode`, `internal/store/queries.sql: ListEpicWorkflowNodes`).

Migration `00023_checkout_tails.sql` adds two columns to `steps`, `checkout_head` and `checkout_hash`, and two to `evidence_runs`, `stderr_hash` and `stderr_bytes`, and alters nothing else. Its down step drops them (`internal/store/migrations/00023_checkout_tails.sql: ALTER TABLE steps ADD COLUMN checkout_head`, `internal/store/migrations/00023_checkout_tails.sql: ALTER TABLE steps ADD COLUMN checkout_hash`, `internal/store/migrations/00023_checkout_tails.sql: ALTER TABLE evidence_runs ADD COLUMN stderr_hash`, `internal/store/migrations/00023_checkout_tails.sql: ALTER TABLE evidence_runs ADD COLUMN stderr_bytes`, `internal/store/migrations/00023_checkout_tails.sql: +goose Down`).

Migration `00024_turn_end_index.sql` adds the partial index `events_turn_end` on `events` over `session_id` and `at`, for the `session_changed` events whose `state` field is `done`, and alters nothing else. Its down step drops the index (`internal/store/migrations/00024_turn_end_index.sql: CREATE INDEX events_turn_end`, `internal/store/migrations/00024_turn_end_index.sql: DROP INDEX events_turn_end`).

Migration `00027_turn_answered.sql` adds the `answered` column to `events` and alters nothing else. Its down step drops it (`internal/store/migrations/00027_turn_answered.sql: ALTER TABLE events ADD COLUMN answered`, `internal/store/migrations/00027_turn_answered.sql: ALTER TABLE events DROP COLUMN answered`).

Migration `00033_integration.sql` adds the batch columns and alters nothing else. Its down step drops them (`internal/store/migrations/00033_integration.sql: +goose Up`, `internal/store/migrations/00033_integration.sql: +goose Down`).

- `epics.stored_base` (`TEXT`, `''`): the commit `epic/<slug>` was last moved to. The migration sets it to each epic's `base` (`internal/store/migrations/00033_integration.sql: UPDATE epics SET stored_base`), `CreateEpic` sets it to the base (`store.Store.CreateEpic`), and a passed integrate run moves it (`store.settleBatchBase`). The epic's `base` does not change.
- `epics.candidate` (`TEXT`, `''`): the last candidate a failed integrate run made, cleared when a run passes (`store.settleBatchBase`; `internal/store/queries.sql: MoveEpicStoredBase`, `internal/store/queries.sql: SetEpicCandidate`).
- `epic_tasks.batch` (`TEXT`, `''`): the path of the integrate run that claimed the task, `''` while none has (`store.EpicTask`; `internal/store/queries.sql: ClaimEpicTaskBatch`).

The `phase` column takes the values `built`, `met_at_base`, `reviewing`, `correcting`, `clean` and `ended_with_gaps` as well, and `releasing`, `stopped`, `released` and `done` as [Release](#release) gives them, since it has no `CHECK` (`internal/store/migrations/00014_epics.sql: CREATE TABLE epics`; see **Phase and changed** above).

Migration `00035_reviewers.sql` adds the `steps.agents` column, the table `epic_reviewers` and the column `epic_task_findings.unverified`, and alters nothing else. Its down step drops them (`internal/store/migrations/00035_reviewers.sql: +goose Up`, `internal/store/migrations/00035_reviewers.sql: +goose Down`).

- `steps.agents` (`TEXT`, `''`): the JSON object of inline agent definitions the step is started with (see [Steps in the store](#steps-in-the-store)).
- `epic_reviewers` (`epic`, `position`, `name`, `enabled`, `stage`, `must`, `must_not`, `prompt`, `agent`, `model`, `tools`, `effort`, `limit_seconds` and `verify`), with primary key `(epic, position)` and `(epic, name)` unique. It holds the epic's merged reviewers in position order, each as its merged value, with each list stored as its JSON array (`internal/store/migrations/00035_reviewers.sql: CREATE TABLE epic_reviewers`; `store.insertReviewer`, `store.reviewerOf`; `internal/store/queries.sql: InsertEpicReviewer`, `internal/store/queries.sql: ListEpicReviewers`).
- `epic_task_findings.unverified` (`INTEGER`, `0`): whether the finding was settled as confirmed without a verifier (`store.TaskFinding.Unverified`).

Migration `00037_assertions.sql` does three things. It sets the `kind` of each stored evidence item by one rule, in `epic_requirements.item`, `epic_tasks.evidence` and `evidence_runs.spec`: an item whose `capture` is not empty gets `capture`, otherwise an item whose `supplied` is not empty gets `supplied`, and any other item is left as it is. The same rule on each table keeps a run's spec current with its item (`internal/store/migrations/00037_assertions.sql: UPDATE epic_requirements`, `internal/store/migrations/00037_assertions.sql: UPDATE epic_tasks`, `internal/store/migrations/00037_assertions.sql: UPDATE evidence_runs`). It creates `epic_task_contributes`, which the table above lists. It then moves each claim that a task not failed repeats, whether within its own list or in an earlier task that is not failed, from `epic_task_requirements` to `epic_task_contributes`, so the first live claim of a Requirement stays a claim and each later one becomes a contribution (`internal/store/migrations/00037_assertions.sql: INSERT INTO epic_task_contributes`, `internal/store/migrations/00037_assertions.sql: DELETE FROM epic_task_requirements`). Its down step restores each contribution to `epic_task_requirements` after the task's own claims, drops the table, and removes the `kind` of each item (`internal/store/migrations/00037_assertions.sql: +goose Down`).

Migration `00039_questions.sql` adds the question's shape and the answer's choice, and alters nothing else. Its down step drops them in reverse order (`internal/store/migrations/00039_questions.sql: +goose Up`, `internal/store/migrations/00039_questions.sql: +goose Down`).

- `epic_questions` gains these columns, each `NOT NULL` with the default shown: `label` (`''`), `choices` (`'[]'`, a JSON array of `{"id": ..., "does": ...}`), `recommended` (`''`), `why` (`''`), `confidence` (`''`), `replaces` (`0`), `replaced_by` (`0`) and `choice` (`''`) (`internal/store/migrations/00039_questions.sql: ALTER TABLE epic_questions ADD COLUMN label`). A question stored before the migration reads with no label, no choices and no recommendation.
- `epic_tells.question` (`INTEGER`, `0`): the seq of the park question the tell belongs to, `0` for none (`internal/store/migrations/00039_questions.sql: ALTER TABLE epic_tells ADD COLUMN question`).

The `state` column of `epic_questions` takes `replaced` as well, since it has no `CHECK`. `InsertEpicQuestion` writes the shape, `SetEpicQuestionAnswered` writes the `choice`, and `SetEpicQuestionReplaced` moves an open question to `replaced` (`internal/store/queries.sql: InsertEpicQuestion`, `internal/store/queries.sql: SetEpicQuestionAnswered`, `internal/store/queries.sql: SetEpicQuestionReplaced`).

Each call that writes an epic runs in one `BEGIN IMMEDIATE` write transaction, and a failed one leaves no rows behind (`store.Store.write`, `store.runWrite`). `CreateEpic` writes the head, every section, the pinned workflow and the `created` trigger in one transaction (`store.Store.CreateEpic`). Times are stored at whole seconds (`store.EpicRow`). The store checks no contract content. The checks of [Epics](#epics) run before a create reaches it (`enginehost.epicService.CreateEpic`).

Migration `00034_epic_summary.sql` adds the `summary` column to `epics` and alters nothing else. Its down step drops it (`internal/store/migrations/00034_epic_summary.sql: ALTER TABLE epics ADD COLUMN summary`, `internal/store/migrations/00034_epic_summary.sql: +goose Down`). The column is `TEXT` and `NOT NULL`, with the default `''`. `SetEpicSummary` stores a summary only over an empty one, so a stored summary is kept, and an unknown epic is `store.ErrNoEpic` (`store.Store.SetEpicSummary`; `internal/store/queries.sql: SetEpicSummary`). `UnsummarizedEpics` reads each epic whose summary is `''`, in id order, with its contract (`store.Store.UnsummarizedEpics`; `internal/store/queries.sql: ListUnsummarizedEpics`). Setting a summary does not move `changed`.

Migration `00036_review_ledger.sql` creates the six tables of the end-of-epic review and alters nothing that exists. Its down step drops them (`internal/store/migrations/00036_review_ledger.sql: +goose Up`, `internal/store/migrations/00036_review_ledger.sql: +goose Down`). Every column is `NOT NULL`, and no table declares a foreign key.

| Table | Columns | Migration lines |
|---|---|---|
| `epic_reviews` | `epic`, primary key; `frozen`; `clean`, `''` until the epic ends clean | `internal/store/migrations/00036_review_ledger.sql: CREATE TABLE epic_reviews` |
| `epic_review_verdicts` | `epic`, `position`, `reviewer`, `step`, `verdict`, `reason`; primary key `(epic, position)` | `internal/store/migrations/00036_review_ledger.sql: CREATE TABLE epic_review_verdicts` |
| `epic_ledger` | `epic`, `id`, `position`, `reviewer`, `finding`, `claim`, `citation`, `location`, `evidence`, `verify_by`, `step`, `result`, `unverified` (`INTEGER`), `reason`; primary key `(epic, id)` | `internal/store/migrations/00036_review_ledger.sql: CREATE TABLE epic_ledger` |
| `epic_review_rounds` | `epic`, `round`, `revision`, `tasks` (a JSON list of task seqs), `outcome`; primary key `(epic, round)` | `internal/store/migrations/00036_review_ledger.sql: CREATE TABLE epic_review_rounds` |
| `epic_closures` | `epic`, `round`, `finding`, `step`, `result`, `reason`; primary key `(epic, round, finding)` | `internal/store/migrations/00036_review_ledger.sql: CREATE TABLE epic_closures` |
| `epic_review_holds` | `epic`, `step`; primary key `(epic, step)` | `internal/store/migrations/00036_review_ledger.sql: CREATE TABLE epic_review_holds` |

`epic_review_verdicts` and `epic_ledger` are written once, when the review run ends (`store.Store.EndReview`; `internal/store/queries.sql: InsertEpicReviewVerdict`, `internal/store/queries.sql: InsertEpicLedgerFinding`). `epic_review_rounds` and `epic_closures` are written when a closure run ends, and `epic_review_holds` when a review step is held (`store.Store.EndClosure`, `store.Store.HoldReviewStep`; `internal/store/queries.sql: InsertEpicReviewRound`, `internal/store/queries.sql: InsertEpicClosure`, `internal/store/queries.sql: InsertEpicReviewHold`). `epic_reviews.frozen` is set at the freeze, and `epic_reviews.clean` when the epic ends clean (`store.Store.FreezeReview`, `store.Store.EndEpic`; `internal/store/queries.sql: InsertEpicReview`, `internal/store/queries.sql: SetEpicReviewClean`). `store.Store.EpicReview` reads the six tables in one read transaction (`store.readReview`).

Migration `00038_release.sql` adds the column `steps.unowned` and the tables `epic_release_runs` and `epic_release_slots`, and alters nothing else. Its down step drops them (`internal/store/migrations/00038_release.sql: ALTER TABLE steps ADD COLUMN unowned`, `internal/store/migrations/00038_release.sql: CREATE TABLE epic_release_runs`, `internal/store/migrations/00038_release.sql: CREATE TABLE epic_release_slots`, `internal/store/migrations/00038_release.sql: +goose Down`).

- `steps.unowned` (`INTEGER NOT NULL`, `0`): whether the writing step was started unowned (see [Steps in the store](#steps-in-the-store)).
- `epic_release_runs` (`epic`, `part`, `position`, `attempt`, `step`, `start`, `head`, `finish`, `commits`, `evidence`, `passed`, `reason` and `at`), with primary key `(epic, part, position, attempt)`. `part` is `candidate`, `release`, `judge` or `named`, and `position` is `0` for a candidate and for the judge. `start`, `head` and `finish` are the revisions the run recorded and the end of its step, `commits` is a JSON list, and `at` is unix seconds. A run whose key is stored already changes nothing (`store.ReleaseRun`, `store.recordRelease`; `internal/store/queries.sql: InsertEpicReleaseRun`, `internal/store/queries.sql: ListEpicReleaseRuns`).
- `epic_release_slots` (`repository`, the primary key, and `epic`, unique): the release slot of each repository, held by at most one epic (`store.Store.TakeReleaseSlot`; `internal/store/queries.sql: InsertEpicReleaseSlot`, `internal/store/queries.sql: GetEpicReleaseSlotHolder`, `internal/store/queries.sql: FreeEpicReleaseSlot`).

#### Node runs in the store

Migration `00028_node_runs.sql` creates the table `epic_node_runs` and its unique index, and alters nothing that exists. Its down step drops the table (`internal/store/migrations/00028_node_runs.sql`).

`epic_node_runs` has one row for each node run, with these columns, each `NOT NULL`:

- `epic` (`TEXT`), `path` (`TEXT`) and `id` (`TEXT`), with primary key `(epic, path)` and the unique index `epic_node_runs_id` on `(epic, id)`. `path` is the run's instance path and `id` its derived id (`store.NodeRunID`).
- `node` (`TEXT`) and `kind` (`TEXT`): the workflow node's id, and the node's kind or `loop`.
- `task` and `round` (`INTEGER`, `0`): a build child's task seq, and the round of the innermost enclosing loop, `0` outside a loop.
- `state` (`TEXT`): `running`, `ended` or `parked` (`store.NodeRunning`, `store.NodeEnded`, `store.NodeParked`).
- `outcome` (`TEXT`, `''`): the outcome an end reports.
- `loop_round`, `granted` and `mark` (`INTEGER`, `0`): a loop's current round, counted from 1, the rounds a retry granted it, and a plan's highest turn seq when it started.
- `started` and `ended` (`INTEGER`, `0`): the unix seconds the run started and ended, with `ended` `0` while it runs.

Migration `00029_node_work.sql` adds six columns to `epic_node_runs`, each `NOT NULL`, and alters nothing else. Its down step drops them (`internal/store/migrations/00029_node_work.sql: +goose Up`, `internal/store/migrations/00029_node_work.sql: +goose Down`):

- `attempt` (`INTEGER`, `0`): the attempt the run runs at. A run starts at `0`, and a retry moves it on by one (`store.NodeRun`).
- `revision` and `refs` (`TEXT`, `''`): the revision a check node run's work runs at, and the refs snapshot taken with it (`store.Store.PrepareNode`; `internal/store/queries.sql: PrepareEpicNodeRun`).
- `refs_checked` (`INTEGER`, `0`): whether that snapshot has been compared (`store.Store.CheckNodeRefs`; `internal/store/queries.sql: CheckEpicNodeRefs`).
- `result` (`TEXT`, `''`): an owned run's result, a JSON list of the paths its branch changed (`store.Store.RecordNodeResult`; `internal/store/queries.sql: RecordEpicNodeResult`).
- `output` (`TEXT`, `''`): the output the run ended or parked with, cut to its first `NodeOutputMax` bytes (4096) at a rune start (`store.NodeOutputMax`, `store.capNodeOutput`).

The Go type is `NodeRun` (`store.NodeRun`). Each write below is one store write. `StartNodes`, `EndNodes`, `NextRound`, `ParkLoop` and the work writes below change nothing when repeated:

- `EpicFlow` reads an epic, its node runs by path, its pending trigger count, its node checks, its evidence runs, its review, the step of each agent run and of each release run's release steps and judge, the steps of each review and closure run, its release runs and the holder of its repository's release slot, all in one read-only transaction (`store.Store.EpicFlow`, `store.readRunSteps`, `store.workParts`, `store.readReviewSteps`, `store.readReview`, `store.readReleaseRuns`, `store.releaseSlotHolder`; `internal/store/queries.sql: ListEpicNodeRuns`, `internal/store/queries.sql: CountPendingEpicTriggers`, `internal/store/queries.sql: ListEpicReleaseRuns`, `internal/store/queries.sql: GetEpicReleaseSlotHolder`).
- `StartNodes` inserts each run whose path is absent as `running`, and inserts the trigger as `pending` when its id is absent (`store.Store.StartNodes`; `internal/store/queries.sql: InsertEpicNodeRun`, `internal/store/queries.sql: InsertEpicTriggerOnce`).
- `EndNodes` ends each running run at a path with its outcome (`store.Store.EndNodes`; `internal/store/queries.sql: EndEpicNodeRun`).
- `NextRound` moves a running loop at its round on to the next round (`store.Store.NextRound`; `internal/store/queries.sql: MoveEpicLoopRound`).
- `ParkLoop` parks a running loop at its round, parks the epic, and adds the reason as a tell (`store.Store.ParkLoop`; `internal/store/queries.sql: ParkEpicLoopRun`).
- `RetryEpic` grants each parked loop of the epic one more round, in the write that ends the park (`store.Store.RetryEpic`; `internal/store/queries.sql: GrantEpicParkedLoops`).
- `CompleteTurn` inserts one running build child for each task it stores, in the turn's write (`store.insertTasks`).
- `PrepareNode` stores a running run's revision and refs at its attempt, once the revision is unset (`store.Store.PrepareNode`; `internal/store/queries.sql: PrepareEpicNodeRun`).
- `RecordNodeResult` stores an owned run's result at its attempt, once (`store.Store.RecordNodeResult`; `internal/store/queries.sql: RecordEpicNodeResult`).
- `CheckNodeRefs` marks a running run's refs checked, once. In the same write a parking move parks the run and the epic, and each told move is added as a tell (`store.Store.CheckNodeRefs`, `store.parkCheckedRun`; `internal/store/queries.sql: CheckEpicNodeRefs`).
- `ParkNode` parks a running run at its attempt with its outcome and output, and parks the epic with the reason, which it adds as a tell, in one write. The reason is cut to `TurnReasonMax` bytes (`store.Store.ParkNode`, `store.capTurnText`; `internal/store/queries.sql: ParkEpicNodeRun`).
- `EndNodesWaking` ends the running runs of `EndNodes` and adds a trigger, pending, in the same write (`store.Store.EndNodesWaking`).
- `RetryNode` sets each parked run of a node id running at its next attempt, clears its outcome, output, revision, refs, refs checked, result and end, and ends the epic's park in the same write, as [Epics](#epics) gives (`store.Store.RetryNode`; `internal/store/queries.sql: RetryEpicNode`).
- `ClaimBatch` stores a running integrate run's revision, which is its candidate, with its refs and result at its attempt, once the revision is unset. In the same write it claims each task it names that is `done` and has no batch or the run's path (`store.Store.ClaimBatch`; `internal/store/queries.sql: ClaimEpicNodeRunWork`, `internal/store/queries.sql: ClaimEpicTaskBatch`). A run that already has a revision changes nothing.
- `EndBatch` ends the running run at its attempt with its outcome and output, moves the stored base or the candidate as **Batch integration** under [Workflows](#workflows) gives, and adds its trigger pending when it has one (`store.Store.EndBatch`; `internal/store/queries.sql: EndEpicNodeRunAttempt`). A run at another attempt, or one not running, changes nothing.
- `ParkEpicWaking` parks the epic with its reason, adds the reason as a tell and adds its trigger pending, unless the trigger's id is stored in any state (`store.Store.ParkEpicWaking`).
- `EndPlanBuilt` ends each of its ends, and sets the epic's phase to `built` when the first end's run is still running and the phase is `accepted`; any other phase is left as it is (`store.Store.EndPlanBuilt`, `store.endAndBuild`; `internal/store/queries.sql: CountRunningEpicNodeRun`).
- `ParkNodeFinal` parks a running run at its attempt as `ParkNode` does, and sets the epic's phase to `met_at_base` in the same write, when the park changes the run (`store.Store.ParkNodeFinal`).
- `FreezeReview` sets the revision of a running review run at its attempt to the frozen revision, once, stores the review's frozen revision if none is stored, and sets the epic's phase to `reviewing` when the run takes the revision, in one write (`store.Store.FreezeReview`; `internal/store/queries.sql: FreezeEpicNodeRun`, `internal/store/queries.sql: InsertEpicReview`).
- `EndReview` ends a running review run at its attempt as `passed` with its output, stores its verdicts and ledger, and sets the epic's phase to `correcting`, in one write. A run at another attempt, or one not running, changes nothing (`store.Store.EndReview`; `internal/store/queries.sql: EndEpicNodeRunAttempt`, `internal/store/queries.sql: InsertEpicLedgerFinding`).
- `EndClosure` ends a running closure run at its attempt with its outcome and output, and stores its round and closures in the same write, unless a round with its number is stored already (`store.Store.EndClosure`; `internal/store/queries.sql: EndEpicNodeRunAttempt`, `internal/store/queries.sql: InsertEpicClosure`).
- `RecordRelease` stores a release run in its own write, and a run whose key is stored already changes nothing (`store.Store.RecordRelease`; `internal/store/queries.sql: InsertEpicReleaseRun`).
- `TakeReleaseSlot` takes the slot of the epic's repository when it is free, and moves a clean epic that holds the slot to `releasing`, in one write. It returns the slot's holder after the write (`store.Store.TakeReleaseSlot`; `internal/store/queries.sql: InsertEpicReleaseSlot`, `internal/store/queries.sql: StartEpicRelease`). `FreeReleaseSlot` deletes the slot the epic holds, and leaves another epic's slot as it is (`store.Store.FreeReleaseSlot`; `internal/store/queries.sql: FreeEpicReleaseSlot`).
- `StopRelease` parks the running release run at its attempt with outcome `failed` and its reason as output, parks the epic with the reason, adds the reason as a tell, and moves the epic from `releasing` to `stopped`, in one write. A run at another attempt, or one that is not running, changes nothing (`store.Store.StopRelease`, `store.stopEpicRelease`; `internal/store/queries.sql: ParkEpicNodeRun`, `internal/store/queries.sql: StopEpicRelease`).
- `ReleaseEpic` and the release case of `StopEpic` write as [Release](#release) and **Controls** under [Epics](#epics) give (`store.Store.ReleaseEpic`, `store.Store.StopEpic`; `internal/store/queries.sql: ResumeEpicRelease`, `internal/store/queries.sql: FreeEpicReleaseSlot`).

#### Node checks in the store

Migration `00030_node_checks.sql` creates the table `epic_node_checks` and its index `epic_node_checks_epic_created`, on `(epic, created)`, and alters nothing that exists. Its down step drops them (`internal/store/migrations/00030_node_checks.sql: CREATE TABLE epic_node_checks`, `internal/store/migrations/00030_node_checks.sql: CREATE INDEX epic_node_checks_epic_created`, `internal/store/migrations/00030_node_checks.sql: +goose Down`).

`epic_node_checks` has one row for each gate or command check, with these columns:

- `id` (`TEXT`, primary key): the check's id, `NodeWorkID` of its run's id, attempt and the part `check` (`store.NodeCheck`, `kinds.checkRun`).
- `epic`, `path` and `revision` (`TEXT`): the epic, the path of the node run the check belongs to, and the revision the check runs at.
- `argv` and `needs` (`TEXT`, JSON), and `limit_seconds` (`INTEGER`): the command, the pools it draws on, and its limit in seconds (`store.Store.AddNodeCheck`).
- `state` (`TEXT`): `running` or `ended`, by `CHECK`.
- `grant_id` and `holder` (`TEXT`, `''`): the pool grant and the holder the check runs under, set once the grant holds (`store.Store.SetNodeCheckGrant`; `internal/store/queries.sql: SetEpicNodeCheckGrant`).
- `exit` (`INTEGER`), `timed_out` (`INTEGER`), and `not_run`, `stdout` and `stderr` (`TEXT`): how the check ended. Stdout and stderr keep their newest `NodeOutputMax` bytes (`store.Store.EndNodeCheck`, `store.tailBytes`; `internal/store/queries.sql: EndEpicNodeCheck`).
- `created` and `ended` (`INTEGER`): the unix seconds the check was added and ended, with `ended` `0` while it runs.

`AddNodeCheck` stores a check running, or ended when it has a `not_run` reason, and a check whose id is stored already is left as it is (`store.Store.AddNodeCheck`; `internal/store/queries.sql: InsertEpicNodeCheck`). `NodeCheck` reads a check by id, `NodeChecks` reads an epic's checks by `created`, then `id`, and `OpenNodeChecks` reads the running checks of every epic (`store.Store.NodeCheck`, `store.Store.NodeChecks`, `store.Store.OpenNodeChecks`; `internal/store/queries.sql: GetEpicNodeCheck`, `internal/store/queries.sql: ListEpicNodeChecks`, `internal/store/queries.sql: ListOpenEpicNodeChecks`). `EpicFlow` carries an epic's checks to the kinds that decide its node runs (see [Node runs in the store](#node-runs-in-the-store)).

#### Due resumes in the store

Migration `00025_due_resumes.sql` adds the table `due_resumes`, its two indexes and the column `epics.pause_source` (`internal/store/migrations/00025_due_resumes.sql: CREATE TABLE due_resumes`, `internal/store/migrations/00025_due_resumes.sql: CREATE INDEX due_resumes_target`, `internal/store/migrations/00025_due_resumes.sql: CREATE INDEX due_resumes_due`, `internal/store/migrations/00025_due_resumes.sql: ALTER TABLE epics ADD COLUMN pause_source`). `due_resumes_target` indexes a row's kind, target and reset, for the insert guard and `CloseDueResumesOf`, and the partial index `due_resumes_due` holds the `due` rows by reset, for `ListDueResumes`, so neither scans the rows the table keeps closed (`TestDueResumeQueryPlans`, `TestHolderPromptQueryPlans`). Its down step drops them (`internal/store/migrations/00025_due_resumes.sql: ALTER TABLE epics DROP COLUMN pause_source`, `internal/store/migrations/00025_due_resumes.sql: DROP TABLE due_resumes`).

- `due_resumes` has `id` (`TEXT`, the primary key), `kind` (`step` or `epic`), `target` (the step id or the epic id), `resets` (`INTEGER`, unix seconds) and `state` (`due` or `done`). Its id is the lowercase hex SHA-256 of `orrery-due`, the kind, the target and the reset, joined with NUL bytes (`store.dueNamespace`, `store.insertDueResume`, `store.dueID`). A limit's write inserts the row (see [Step lifecycle](#step-lifecycle) and [Planner turns](#planner-turns)).
- The insert stores nothing when a row of the same kind and target already has a reset at or after `resets`, whatever its state (`internal/store/queries.sql: InsertDueResume`; `store.insertDueResume`).
- `ListDueResumes` returns the `due` rows, oldest reset first (`internal/store/queries.sql: ListDueResumes`; `store.Store.DueResumes`). `CloseDueResume` marks one row `done`, and `CloseDueResumesOf` marks every row of a kind and target `done` (`internal/store/queries.sql: CloseDueResume`, `internal/store/queries.sql: CloseDueResumesOf`).
- `epics.pause_source` (`TEXT`, `''`) is `owner` for a pause the owner made, `limit` for a pause a usage limit made, and `''` when no pause is recorded, which includes a pause made before this migration. `ResumeEpic` sets it back to `''` (`internal/store/queries.sql: PauseEpic`, `internal/store/queries.sql: SetEpicPauseSource`, `internal/store/queries.sql: ResumeEpic`; `store.dueKindEpic`, `store.pauseOwner`, `store.pauseLimit`).

### Claude Code hook settings

Register `orrery hook` for each hook event that reports state, `StopFailure` among them (see section 1). The path below is synthetic:

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
    "StopFailure": [{ "hooks": [{ "type": "command", "command": "/usr/local/bin/orrery hook" }] }],
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
| `p` | show the pools view: each member's state and holder, the waiters, and the holds; `p` again returns to the panes |
| `h` | in the pools view, hold the member under the cursor, or free it when held |
| `X` | close the row's pane after a y/N prompt, asking twice for a working Claude pane |
| `D` | close every session of the row's project and its shell session after a prompt listing them |
| `?` | show every key |
| `Esc`, `q` | close the tree |

The tree lists the active projects first. A project is quiet when none of its panes is waiting or unseen (its state is set and its `changed` is later than its `seen`) and its newest pane `activity` is more than 7 days old. A project whose panes all have no activity stays active. Quiet projects sit under one row, `Quiet (N)` with N the count, which stays collapsed. Enter on that row opens the section and moves the cursor to its first pane. Quieting a project closes and drops nothing.

`X` and `D` archive Claude panes before they close (see [Archive](#archive)).

`p` replaces the tree with the pools view, which reads the same status as [Status](#pools). For each pool in use it shows the pool's name and cap, then each member in declared order, then the requests waiting for the pool under `waiting`. The holds on members of no pool in use follow under `holds`. A member row shows its name, its state, a status word, its holder's project and command, and the age of its hold or grant. The status word is `held` when a hold keeps the member, `free` when no grant holds it, `stopping` when its grant is being stopped by a hold on another member, and `granted` otherwise.

In the pools view:

- `j` and `k` (or `down` and `up`) move the cursor over the member rows and the hold rows.
- `h` holds the member under the cursor, or frees it when it is held. A failed call shows its error on the status line.
- `p` returns to the tree. `?` shows help, and `q` or `Esc` closes the tree.
- Every other key does nothing.

The view reads the pools once a second while it is open. Without a daemon socket path, it shows `pools unavailable`.

### Picker

The picker is shown by `orrery open` with text, and by the tree's `N` in directory mode.

- With no text, a one-line `text:` prompt asks for it. Enter submits non-empty text, Backspace deletes, and Esc cancels.
- The model sees at most 300 directories, numbered `d1`, `d2`, and so on, in this order, and each directory once. First come the first 30 of the live sessions', archive and store directories, in known-directory order (see [open](#orrery)). Then the children of `$HOME/Personal`. Then each root of the [roots file](#roots), in file order, adds its directories at levels 1 to `DEPTH` below its path, sorted by path. A directory is written relative to `$HOME` when it lies strictly inside `$HOME`, and by its absolute path otherwise, including `$HOME` itself. Hidden names (beginning with `.`) are left out with everything beneath them, as are symlinks and any directory that cannot be read, with everything beneath it.
- The text is ranked into actions, best first, and the first row is selected. Each row reads `jump PANE`, `pane DIR`, `resume SESSION`, `continue latest in DIR`, or `start fresh in DIR`, with ` (prompt)` added when the action carries a first prompt. A resume row adds `: NOTE` after its session when the archived record has a summary.
- The resume rows are the actions that `open` builds for the directory (see its bullet above). A `resume` action runs `claude --resume SESSION`, a `continue` action runs `claude --continue`, and a `start fresh` action runs `claude`. Each opens a Claude pane in the directory, as a `pane` action does.
- The text picker's last row is `pane ROOT`, where ROOT is `$HOME/Personal`. It carries the text as its first prompt when the model calls the text a task, as every model `pane` row does. It is left out when another row already opens a pane in ROOT. When the model's reply has no valid line, this row is the only row. When `HOME` is not absolute, there is no root row, and a reply with no valid line is an error. A failed model call, a store read that fails (see [open](#orrery)) or a failed tmux call is an error. The rows that `open` builds for a directory get no root row.
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

Connect paths are those beginning `/orrery.v1.Sessions/`, `/orrery.v1.Events/`, `/orrery.v1.Control/`, `/orrery.v1.Panes/`, `/orrery.v1.Host/`, `/orrery.v1.Push/`, `/orrery.v1.Picker/`, `/orrery.v1.Epics/`, `/orrery.v1.Evidence/`, `/orrery.v1.Steps/`, `/orrery.v1.Pools/`, `/orrery.v1.Presence/` or `/orrery.v1.Notices/` (see [Connect API](#connect-api), [Control API](#control-api) and [Phone API](#phone-api)). Every request to a Connect path, whatever its method, must also send a non-empty `X-Orrery-Client` header. Otherwise the response is 403. A POST to a Connect path must send `Content-Type` `application/proto`, `application/json`, `application/connect+proto`, or `application/connect+json`. Otherwise the response is 403.

The web page is the one exception to the 404 rule. After the login check, GET and HEAD of `/` answer `index.html`, and GET and HEAD of any other file path of the page answer that file. A path the page lacks, a directory, and any other method answer 404. Each page answer carries `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'` and `X-Content-Type-Options: nosniff`. The page needs no client header. Any other path that is not a Connect path answers 404, after the login check. The server sends no CORS headers.

### Push payload

When [Notice routing](#notice-routing) sends a pane's `waiting` or `done` notice to the phone, `orreryd` posts it to each registered subscription. A headless session notice (see [Session notifications](#session-notifications)) and an epic notice (see [Planner turns](#planner-turns)) are posted to each subscription the same way when they go to the phone. A usage-limit notice (see [Step lifecycle](#step-lifecycle), Usage limit) is posted the same way too, and so is an alert notice (see [Alert intake](#alert-intake)).

- The body is one RFC 8291 `aes128gcm` record. The record is at most 4096 bytes.
- The headers are `Content-Encoding: aes128gcm` and `TTL: 3600`.
- The plaintext is a JSON object. Every value is a string. The keys are `pane`, `host`, `project`, `state`, `summary`, `label`, `question`, `question_hash`, and `time`. `time` is the notice's time, as decimal unix seconds. A headless session notice also has the key `session`, holding the session id. An epic notice also has the key `epic`, holding the epic's id, with `pane`, `label` and `question` empty and `project` set to the epic's title (`push.boundedPayload`). A usage-limit notice also has the key `limit`, holding its limit key `<kind>@<reset>` (`push.boundedPayload`). An alert notice also has the key `alert`, holding the alert's name, and its `project` is that name too (`push.boundedPayload`). A pane notice has none of `session`, `epic`, `limit` and `alert`, and only the notice of each kind has its key. A silent notice (see [Notice routing](#notice-routing)) also has the key `silent`, holding `"true"`. No other notice has a `silent` key.
- The plaintext is at most 3993 bytes. When it is longer, the question is cut first, then the summary.
- `question_hash` always covers the full question, even when the question is cut.

Example plaintext. The question and its hash are the fixed vector of section 10:

```json
{"pane":"%1","host":"example-host","project":"example-project","state":"waiting","summary":"Example summary of the pane's work.","label":"example-label","question":"Example question for the user?","question_hash":"7f593eeb8a031e6a99b02a9b65b8e03a17a82a20ca4862ecda37bd06c479a5eb","time":"1700000000"}
```

#### Clear payload

A clear is posted when a seen write sets a seen time at or after the time of the kept notice for a pane, a session, an epic, a usage limit or an alert (see [orreryd](#orreryd) and [Step lifecycle](#step-lifecycle), Usage limit). Once the clear is published, the row of that pane, session, epic, limit or alert is deleted from the store (see [Pushed notices in the store](#pushed-notices-in-the-store)). Its headers are the same as a notice's, `Content-Encoding: aes128gcm` and `TTL: 3600`. Its plaintext is a JSON object of string values with exactly these keys:

- `kind`, which is `clear`.
- `host`, the machine host name.
- `pane`, the pane id, for a pane notice, `session`, the full session id, for a session notice, `epic`, the epic's id, for an epic notice, `limit`, the limit key, for a usage-limit notice, or `alert`, the alert's name, for an alert notice. Only the one that applies is set (`push.clearPayload`).
- `seen`, the seen time, as decimal unix seconds.

Example clear plaintext. Every value is synthetic:

```json
{"kind":"clear","host":"example-host","pane":"%1","seen":"1700000300"}
```

On the phone, the notices of a host are shown as follows:

- One notification per host and pane, session, epic, limit or alert (`android/app/src/main/kotlin/io/github/joshsymonds/orrery/push/PushNotifier.kt: PushNotifier.tagOf`).
- A replacement is silent unless its state is `waiting` or `failed`.
- A clear removes only the notices at or before its `seen` time.
- Session notices, epic notices and alert notices carry no actions.
- An epic notice is tagged by the epic's id. A tap on it opens that epic's screen (`android/app/src/main/kotlin/io/github/joshsymonds/orrery/push/PushNotifier.kt: PushNotifier.show`, `android/app/src/main/kotlin/io/github/joshsymonds/orrery/push/PushNotifier.kt: PushNotifier.contentIntent`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/MainActivity.kt: MainActivity.openExtra`).
- An alert notice is titled `<name> · firing`, its text is its summary, and a tap on it opens the app without naming a screen (see [Alert intake](#alert-intake)).
- A silent notice shows on the app's silent channel, with no sound or vibration.

### Connect API

`orrery serve` also serves a typed API over the Connect protocol. The schema is `proto/orrery/v1/orrery.proto`, package `orrery.v1`. Generated Go lives in `internal/gen`. `just generate` writes it, and `just check` fails when it drifts from the schema. The same `just generate` also writes the generated Kotlin into `android/app/src/main/java`, from the template `proto/buf.gen.android.yaml`. `just gen-diff` diffs that directory and `internal/gen`.

The services are `orrery.v1.Sessions`, with three calls, `List`, `ListHeadless` and `WatchConversation`, `orrery.v1.Events`, with one call, `Watch`, `orrery.v1.Control`, with seven calls, which [Control API](#control-api) describes, and the six phone services `orrery.v1.Panes`, `orrery.v1.Host`, `orrery.v1.Push`, `orrery.v1.Picker`, `orrery.v1.Pools` and `orrery.v1.Presence`, with seventeen calls, which [Phone API](#phone-api) describes. The services `orrery.v1.Epics`, with eight calls, `orrery.v1.Steps`, with three calls, and `orrery.v1.Evidence`, with one call, which [Epics and Steps](#epics-and-steps) describes. Their schema is `proto/orrery/v1/epics.proto`. `orrery.v1.Steps` is not the engine's `orrery.engine.v1.Steps` service of [Steps](#steps). The service `orrery.v1.Notices`, with two calls, `Watch` and `Seen`, serves the web page's in-app notices, which [In-app notices](#in-app-notices) describes. Its schema is `proto/orrery/v1/notice.proto`.

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
| `Control.Resume` | `POST /orrery.v1.Control/Resume` | `ResumeRequest` | `ResumeResponse` |
| `Panes.List` | `POST /orrery.v1.Panes/List` | `PanesListRequest`, empty | `PanesListResponse` |
| `Panes.Screen` | `POST /orrery.v1.Panes/Screen` | `ScreenRequest` | `ScreenResponse` |
| `Panes.Conversation` | `POST /orrery.v1.Panes/Conversation` | `ConversationRequest` | `ConversationResponse` |
| `Panes.WatchConversation` (server-streaming) | `POST /orrery.v1.Panes/WatchConversation` | `WatchConversationRequest` | stream of `WatchConversationResponse` |
| `Panes.SendText` | `POST /orrery.v1.Panes/SendText` | `SendTextRequest` | `SendTextResponse` |
| `Panes.SendKeys` | `POST /orrery.v1.Panes/SendKeys` | `SendKeysRequest` | `SendKeysResponse` |
| `Panes.MarkSeen` | `POST /orrery.v1.Panes/MarkSeen` | `MarkSeenRequest` | `MarkSeenResponse` |
| `Panes.PullQueued` | `POST /orrery.v1.Panes/PullQueued` | `PullQueuedRequest` | `PullQueuedResponse` |
| `Host.Status` | `POST /orrery.v1.Host/Status` | `HostStatusRequest`, empty | `HostStatusResponse` |
| `Push.Register` | `POST /orrery.v1.Push/Register` | `RegisterRequest` | `RegisterResponse` |
| `Push.Unregister` | `POST /orrery.v1.Push/Unregister` | `UnregisterRequest` | `UnregisterResponse` |
| `Picker.Ask` | `POST /orrery.v1.Picker/Ask` | `AskRequest` | `AskResponse` |
| `Picker.Run` | `POST /orrery.v1.Picker/Run` | `RunRequest` | `RunResponse` |
| `Epics.List` | `POST /orrery.v1.Epics/List` | `EpicsListRequest`, empty | `EpicsListResponse` |
| `Epics.Get` | `POST /orrery.v1.Epics/Get` | `EpicsGetRequest` | `EpicsGetResponse` |
| `Epics.History` | `POST /orrery.v1.Epics/History` | `EpicsHistoryRequest` | `EpicsHistoryResponse` |
| `Epics.Pause` | `POST /orrery.v1.Epics/Pause` | `EpicsPauseRequest` | `EpicsPauseResponse` |
| `Epics.Resume` | `POST /orrery.v1.Epics/Resume` | `EpicsResumeRequest` | `EpicsResumeResponse` |
| `Epics.Stop` | `POST /orrery.v1.Epics/Stop` | `EpicsStopRequest` | `EpicsStopResponse` |
| `Epics.Retry` | `POST /orrery.v1.Epics/Retry` | `EpicsRetryRequest` | `EpicsRetryResponse` |
| `Epics.Answer` | `POST /orrery.v1.Epics/Answer` | `EpicsAnswerRequest` | `EpicsAnswerResponse` |
| `Evidence.Artifact` | `POST /orrery.v1.Evidence/Artifact` | `EvidenceArtifactRequest` | `EvidenceArtifactResponse` |
| `Steps.Hold` | `POST /orrery.v1.Steps/Hold` | `StepsHoldRequest` | `StepsHoldResponse` |
| `Steps.Nudge` | `POST /orrery.v1.Steps/Nudge` | `StepsNudgeRequest` | `StepsNudgeResponse` |
| `Steps.Stop` | `POST /orrery.v1.Steps/Stop` | `StepsStopRequest` | `StepsStopResponse` |
| `Pools.Status` | `POST /orrery.v1.Pools/Status` | `PoolsStatusRequest`, empty | `PoolsStatusResponse` |
| `Pools.Hold` | `POST /orrery.v1.Pools/Hold` | `PoolsHoldRequest` | `PoolsHoldResponse` |
| `Pools.Free` | `POST /orrery.v1.Pools/Free` | `PoolsFreeRequest` | `PoolsFreeResponse` |
| `Presence.Report` | `POST /orrery.v1.Presence/Report` | `PresenceReportRequest` | `PresenceReportResponse` |
| `Notices.Watch` (server-streaming) | `POST /orrery.v1.Notices/Watch` | `NoticesWatchRequest` | stream of `NoticesWatchResponse` |
| `Notices.Seen` | `POST /orrery.v1.Notices/Seen` | `NoticesSeenRequest` | `NoticesSeenResponse` |

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

`orrery.v1.Events.Watch` streams the events table (see [Store](#store)), and the `EngineText` messages described below. `orrery events` and `orrery session --url ... watch` (section 11) read this call, and so does the web page's events stream (`web/src/main.tsx`). Each reader resumes a stream that ends with `Unavailable` from the last seq it wrote:

- `orrery events` (`cli.watchEvents`) opens the stream again after the last seq it wrote, for as long as 60 seconds pass with no message written. The 60 seconds count from its last message, or from the start of the watch while none has been written (`rpcretry.Window`).
- `orrery session --url ... watch` (`cli.urlWatch.run`, `cli.urlWatch.read`) opens the stream again at once after a message, from the last seq it wrote. An `Unavailable` end before any message is retried for 60 seconds from the call's first attempt (`rpcretry.Window`, `rpcretry.Do`).
- The web page (`web/src/main.tsx: start`) opens the stream again after `defaultRetryMs`, 1 second, from the last seq it delivered, with no time limit. Any other end of the stream is left to the poll, which runs every 5 seconds (`web/src/main.tsx: defaultIntervalMs`, `web/src/main.tsx: defaultRetryMs`, `web/src/main.tsx: start`). The next load then opens the stream from the newest seq it reads.

`WatchRequest` has `after`, the seq the stream resumes after. It is a cursor, and the cursor rules below apply to it.

Each `WatchResponse` has `seq`, `session_id` (the pane's `@orrery_id`, or the headless session's id), `at` (unix seconds when the event was stored), and one member of its `event` oneof. The store kinds map to the members as follows:

| Store kind | `WatchResponse` member | Payload message |
|---|---|---|
| `session_added` | `session_added` | `SessionAdded`: `server`, `pane` |
| `session_changed` | `session_changed` | `SessionChanged` (below) |
| `session_removed` | `session_removed` | `SessionRemoved`, no fields |
| `engine_added` | `engine_added` | `EngineAdded`: `dir`, `state` |
| `engine_changed` | `engine_changed` | `EngineChanged`: `state`, `changed`, `seen`, `question`, `claude_session`, `reason`, each set only when its column changed |
| `engine_prompt` | `engine_prompt` | `EnginePrompt`: `text`, `prompt_id`, `sender`, `round`, the last two empty or 0 when the payload has no such key |
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

`Sessions.WatchConversation` streams the conversation of one headless engine session, and also of one step with an epic label. A step's session id is its step `id`. Before its first delta, the stream asks orreryd's `StepStatus` once about a step, and admits it only when the step has an epic label (see [Epics and Steps](#epics-and-steps)). It is a server-streaming call. Its request is a `SessionsWatchConversationRequest`, and each message it sends is a `SessionsWatchConversationResponse`, which is either a delta or a keepalive. Its timing and end rules are those of the [Conversation stream](#conversation-stream), and its reset rules and messages differ as set out below.

The request fields are these:

- `id` is the session's id, or a step's id. An id that is missing, or that names a pane, ends the stream with `not_found` and `session not found`, at start or mid-stream, and so does a step with no epic label.
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
- `items` holds the main-thread items of the delta. An event makes an item only when it has no parent tool use. A prompt is a `prompt` item with `text`, and with `sender` and `round` as its stored prompt has them (see [Engine sessions in the store](#engine-sessions-in-the-store)). An assistant text block is a `reply` item with `text`. A tool call is a `tool` item with `tool` set to the tool name, `input` set to the tool input as text, and `id` set to the tool use id. A tool result is a `result` item with `id` set to the tool use id, `text` set to the result's content, and `error` set to whether the result failed. A block of any other kind, and a text block of any other role, make no item. Each item's `time` is the time of its event.
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
- `InterruptRequest`, `StopRequest`, `SeenRequest` and `ResumeRequest`: `id`. Their responses have no fields.

Rules:

- **Home rule.** `Start` admits `dir` only when it is an absolute path to an existing directory whose symlinks resolve to a path equal to the home directory or under it. `serve` forwards the resolved path. The home directory is `HOME` as `serve` reads it (see [Environment](#environment)). An empty, relative or `/` home, or one that does not resolve, refuses every start. The check runs before the forward, so a refused start never reaches orreryd.
- **Client-chosen ids.** `StartRequest.id` names the new session's id, 32 lowercase hex digits. Empty lets orreryd mint one. A start that names an existing session's id returns that id and starts nothing. `SayRequest.prompt_id` names the prompt. A say whose `prompt_id` an `engine_prompt` event of the session already carries succeeds and sends nothing. A call that names a key with a running attempt waits for that attempt and returns its result. A say whose send failed returns that failure to every later call with its key and sends nothing, also after a restart, because the failure is stored in `engine_prompt_failed`. Any other key whose attempt failed after storing its record returns that attempt's error to every later call with the key, and does nothing else, while `orreryd` runs. A key whose attempt failed before storing its record is free, so a retry runs, unless the record cannot be read after the failure, which keeps the error as a stored record does. Empty means no id and no check.
- **Model.** `StartRequest.model` is the model alias the started session's process runs, passed to claude as `--model`. Empty runs claude's default. A model outside the rule of [Session control socket](#session-control-socket) is refused with `invalid_argument`. A process that a later `Say` resumes runs claude's default model.
- **Forward.** Each call that passes the home rule is forwarded to the engine under a deadline. The deadline is 10 seconds, the default for `serve`, which sets no other. The forward reaches the session socket through a unix-socket transport, `sessions.sock` beside the daemon socket. When no daemon socket path resolves, `serve` has no engine and answers `unavailable` without dialing.
- **Step ids.** `Say`, `Answer`, `Interrupt`, `Stop` and `Resume` ask orreryd's `StepStatus` about their `id` before they forward. When the id names a step, the call answers `failed_precondition` and nothing is forwarded, so a session command cannot drive a step around its own loop. A `not_found` from `StepStatus` forwards as before, and any other `StepStatus` error answers as the forward's errors do. `Seen` and `Start` do not ask. With no step client, every call forwards as before.
- **Body bound.** A request body over 1 MiB (1048576 bytes) is refused with `resource_exhausted` before it reaches the engine.

Errors are Connect codes. The messages marked fixed are the same whatever orreryd said:

| Connect code | Message | When |
|---|---|---|
| `invalid_argument` | `session directory must be an existing directory under the home directory` (fixed) | `Start`'s `dir` fails the home rule. Answered before the forward |
| `unavailable` | `session service unavailable` (fixed) | no daemon socket path resolves; the session socket cannot be dialed, so orreryd is not taking session commands; or orreryd answers `unavailable`, which it does when it has no store |
| `deadline_exceeded` | `session command timed out` (fixed) | the forward outlives the 10-second deadline, or the client's own deadline ends the call first |
| `internal` | `session command failed` (fixed) | orreryd answers `internal` or `unknown`, or the forward fails with an error that is not a Connect error |
| `resource_exhausted` | the Connect library's message | the request body is over 1 MiB |
| `failed_precondition` | `<id> is a step: drive it through Steps`, where `<id>` is the id given, or orreryd's message | `Say`, `Answer`, `Interrupt`, `Stop` or `Resume` names a step (see Step ids above), or `Resume` names a session that orreryd refuses, which it does when the session is not `interrupted` or has no Claude session |
| other codes | orreryd's message, passed through | `invalid_argument` (a bad `model`, `id`, `prompt` or `text`), `not_found` (no row, or an answer whose request is not open), `resource_exhausted` (the session cap) |

### Phone API

The phone's calls are the six services `orrery.v1.Panes`, `orrery.v1.Host`, `orrery.v1.Push`, `orrery.v1.Picker`, `orrery.v1.Pools` and `orrery.v1.Presence` of [Connect API](#connect-api), with seventeen calls. Each call is a Connect POST to its path in [Connect API](#connect-api), and the Connect rules of [Guard](#guard) apply to it. This section states each call's request meaning, answer and errors. The error table at the end gives each error code with its message. A successful `Panes.Screen` or `Panes.Conversation` answer reports the read to `orreryd`, and an open `Panes.WatchConversation` stream reports it at its first successful step and once per keepalive interval after that (see [orrery serve](#orrery-serve)). The phone app makes no `orrery.v1.Notices` call: the in-app notices are for web pages (see [In-app notices](#in-app-notices)).

Request fields and what their zero and presence values mean:

- `PanesListRequest` and `HostStatusRequest` have no fields.
- `ScreenRequest`: `pane` is the pane ID, `%` followed by digits, such as `%3`. Any other value is a malformed pane ID. `ansi` false gives plain text, and `ansi` true keeps the color escape sequences. `history` 0 adds no scrollback. A value from 1 to 2000 adds that many lines above the visible screen, and a value above 2000 is `invalid_argument`. The `ansi` and `history` checks run before the pane ID check, so a `history` above 2000 is `invalid_argument` even for an absent pane.
- `ConversationRequest`: `pane` is as for `ScreenRequest`. `limit` 0 means the default of 30. A value from 1 to 100 is that many items, and a value above 100 is `invalid_argument`. The `limit` check runs before the pane ID check.
- `WatchConversationRequest`: `pane` is as for `ScreenRequest`. `limit` is as for `ConversationRequest`, and it is the number of newest items a reset sends. `transcript` is the id of the transcript the client holds, or empty when it holds none. `after` is the `seq` the client holds for that transcript. The `limit` check runs before the pane ID check.
- `SendTextRequest`: `pane` is as for `ScreenRequest`, and `text` is the text sent.
- `SendKeysRequest`: `pane` is as for `ScreenRequest`, and `keys` is the list of keys sent, in order. `expect_question` is present when the field is set. When it is present, the question check applies.
- `MarkSeenRequest`: `pane` is as for `ScreenRequest`. `read_up_to` absent means now. When present, it must be 0 or more, or the call is `invalid_argument`.
- `PullQueuedRequest`: `pane` is as for `ScreenRequest`.
- `PresenceReportRequest`: `input_age_ms` is the milliseconds since the page's newest input, by the page's own clock. A negative value is `invalid_argument`. The page reports only while it is visible, and serve dates each input as [orrery serve](#orrery-serve) says.
- `RegisterRequest`: `endpoint`, `p256dh` and `auth` are the subscription. `p256dh` and `auth` are the subscription's keys. The endpoint must be `https` and must be on a host named by `--push-host` (section 11).
- `UnregisterRequest`: `endpoint` is the endpoint of the subscription to remove.
- `AskRequest`: `text` is the text to look up. Blank text, meaning empty or only white space, is `invalid_argument`.
- `RunRequest`: `token` is the token to run. A blank token is `invalid_argument`.
- `PoolsStatusRequest` has no fields. `PoolsHoldRequest` and `PoolsFreeRequest` have `member`, the member's name, which orreryd checks against `[A-Za-z0-9][A-Za-z0-9._-]*`.

The answers:

- `PanesListResponse` carries the tracked panes of `orrery list --json` (section 3), in order. Each pane has the seventeen keys of section 3, then `question_hash`, `newest_reply` and `last_turn_end`. A tmux failure is `internal`. With no tmux server running, the list is empty.
  - `question_hash` is the question hash of section 10 for the pane's `question`.
  - `newest_reply` is the unix seconds of the newest reply or question in the transcript that the pane's `@orrery_transcript` option names (section 2). It counts the `reply` and `question` items of `Panes.Conversation` (below), with their line timestamps, over the whole transcript file rather than the conversation's 4 MiB window. Tool calls, prompts, results, notices and compactions do not count, and neither do sidechain or meta lines. `newest_reply` is 0 when the pane has no `@orrery_state`, when `@orrery_transcript` is unset or empty, when the transcript file does not exist, or when it has no reply or question with a timestamp. Any other transcript read error is `internal`, as is a tmux failure. Serve remembers, for each transcript path, how far it has read and the newest time that read left. A later request reads only the complete lines appended since, and reads the file again from its start when it shrank or was replaced.
  - `last_turn_end` is the unix seconds of the newest store event that set the pane's `state` to `done`, within the store's 14-day event retention. It is 0 when serve has no store, when the store cannot be read, or when there is no such event.
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
  - Each item has these fields, always set, with zero values where the kind has no meaning for them: `kind`, `time`, `text`, `tool`, `input`, `id`, `error`, `notice`, `trigger`, `tokens`, `sender`, `round`.
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
    - `sender` is the sender of a step prompt (`brief`, `engine` or `owner`, where `engine` includes the automatic resume) on a `prompt` item of a headless session's stream, and empty on every other item. `round` is the check round of a send-back on such a `prompt` item, and 0 on every other item. Both are copied from the stored prompt (see [Headless session stream](#headless-session-stream)).
  - A `compaction` item is a `user` line whose `isCompactSummary` is true, and it gives that item whatever else the line holds. Its `text` is the line's string content, or its `text` blocks joined with newlines, cut to 32768 bytes. Pasted content tags are not removed, and an empty text still gives the item. Its `time` is the line's own timestamp. Its `trigger` and `tokens` come from the nearest `system` line of subtype `compact_boundary` before it in the transcript that is neither `isMeta` nor `isSidechain`. The reader keeps reading past the item limit while a compaction waits for its boundary, within the 4 MiB window. A boundary gives no item.
  - `waiting` is the list of messages that Claude Code's queue holds for the transcript, oldest first. It is empty when there are none, never null. Each row has `text` (the message text, cut to 4096 bytes), `time` (the unix seconds of the RFC 3339 `timestamp` of its opening record, or 0 when that is absent or does not parse), and `canceled` (a boolean).
  - A message opens at an `enqueue` queue-operation record in the transcript whose `content` is its text. An enqueue whose text starts with `<task-notification>`, `<agent-message`, or `<cross-session-message` opens nothing. A message closes at the first of these, matched to the oldest open row with the same full text: a `remove` or `popAll` record with that `content` (a `remove` with reason `cleared_on_cancel` cancels the row instead), or a user line that the person sent from the queue (`promptSource` `queued` and `origin.kind` `human`) with that text. A closed row is gone from the answer.
  - A `dequeue` record marks the rows waiting when it comes. The first user line after it with non-empty text decides those marks. When that line is a notice (its text starts with `<task-notification>`, `<agent-message`, `<cross-session-message`, `<teammate-message`, or is the stopped-agents notice, or its `origin.kind` is `task-notification`), each marked row that leads it closes and the other marks are cleared; a row leads a notice when the notice's text starts with the row's text followed by a newline or the end of the text. Any other line settles the marks. A `turn_duration` record closes the rows whose marks are settled. A marked row also closes at a later user line with its exact text.
  - A canceled row stays in the answer, with `canceled` true, until the next `enqueue` record that opens a row or the next user line that the person typed, queued, or accepted from a suggestion. That line drops every canceled row.
  - When the pane's `@orrery_state` is `done`, the answer is idle. A row that is not canceled is dropped for good only when a turn end has come after the newest user line with non-empty text, no dequeue has come after that line, and the row's enqueue came before the newest turn end. With no such user line read, nothing is dropped. Canceled rows stay as the canceled rule above describes, and a later read of a working pane does not bring dropped rows back.
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
- `PullQueuedResponse` carries `text`: the messages that Claude Code's queue holds for the pane, taken back into Claude's input box as Claude Code's own up-arrow takes them. The messages come oldest first, joined by a newline. `Panes.PullQueued` checks the pane first, as the other pane calls do: a malformed pane ID is `invalid_argument` "malformed pane id", and an absent pane is `not_found` "pane not found". It then finds the pane's live session and transcript as `Panes.Conversation` does, so the `not_found` cases of no live session, no transcript and a missing transcript file apply. It holds the pane's text lock, which `Panes.SendText` also takes, from the queue read to the end of the pull, so no text is sent to the pane in between. The queue read is the one `Panes.Conversation` uses, with the same handling of a pane whose state is `done`. When no message that the person did not cancel is waiting, the answer is `failed_precondition` "nothing queued", and no key is sent. Next the input box must be idle: the pane is not in a tmux mode, its input box is on screen, and the box holds only Claude Code's dim placeholder text. Otherwise the answer is `aborted` "input not empty", and no key is sent. The call then sends Up and reads the popAll records that Up appends to the transcript. It waits up to 2 seconds for the first record and then 300 milliseconds for the rest of its batch. Once Up is sent, the call runs to its end even when the request is cancelled, and each of its later key sends and reads is bounded by 10 seconds. When no popAll record came, the call reads the pane's screen. If the screen shows Claude Code's History indicator, Up has loaded a history entry, so the call sends one Down to restore the box. The answer is then `failed_precondition` "nothing queued". When records came, the call sends one Backspace per rune of the text, which deletes the text from Claude's box, and answers with `text`. The call sends no message. If the Backspace step fails, the taken text stays in Claude's box, and the answer is `aborted` "input not empty" with no `text`. A tmux failure, a transcript read failure other than a missing file, or a failed Down is `internal`. The guard covers the call: the rules of [Guard](#guard) apply to `/orrery.v1.Panes/PullQueued` as they do to every Connect path.
- `Push.Register` registers a Web Push subscription, and `RegisterResponse` has no fields. The endpoint is checked first: an endpoint that is not `https`, or whose host is not on a host named by `--push-host` (section 11), is `invalid_argument` "endpoint not allowed". Keys that do not decode to a valid P-256 public key or to the auth secret size are `invalid_argument` "bad request body". A store failure is `internal`.
- `Push.Unregister` removes the subscription with that endpoint, and `UnregisterResponse` has no fields. An endpoint with no stored subscription is `not_found` "endpoint not registered". A store failure is `internal`.
- `AskResponse` carries `rows`, the rows that `orrery open TEXT` would offer for the text (see [Picker](#picker)). The rows come in rank order. Each row has the fields `token`, `kind`, `dir`, `pane`, `label`, `note`, and `prompt`, all always set. `kind` is `jump`, `pane`, `resume`, `continue`, or `fresh`. A `jump` row takes `dir` from its pane's directory, `pane` as the pane ID, and `label` from the pane's label. Other rows give `""` for `pane` and `label`. `note` is the archived summary of a `resume` row, and `""` otherwise. `prompt` is true when the row's action carries a first prompt. A `jump` row is left out when tmux does not list its pane. The answer can have no rows.
  - Each `token` is 32 hex characters and is one-time: its action runs at most once, and it stays valid for 5 minutes from the ask. Tokens are held in serve's memory. Expired tokens are dropped by the next ask that answers without error, and a restart of serve voids every token. Tokens from earlier asks stay valid until their own expiry.
  - The ask runs no action. `new NAME` and `.` are plain text for the intent lookup, and no folder or session is created.
  - A text that is one path word gets the rows that `open` would offer for that path, with no current directory. A text that is one word with several known directories of that base name gets one `pane` row for each, in known-directory order (see [open](#orrery)).
  - Blank `text` is `invalid_argument` "bad request". With no picker configured, the answer is `not_found` "404 page not found". A failure of the picker's configuration, of the text lookup (the store read, tmux or the intent model), or of the tmux pane listing is `unavailable` with the failure's reason as the message. A relative path word is `unavailable` in the same way.
- `RunResponse` carries `pane`: the pane the action opened or jumped to, or else the active pane of the session it created. While the token is valid, a repeat run of the same token answers the same pane and runs nothing. Runs of one token are serial.
  - A blank token is `invalid_argument` "bad request". A token no ask issued, or one lost by a restart, is `not_found` "unknown token". A token at or past its expiry is `failed_precondition` "expired token" until an ask that answers without error drops it, and `not_found` "unknown token" after that. Each of these refusals runs nothing.
  - When tmux reports no server running, the answer is `failed_precondition` with the tmux reason as the message, and no action starts.
  - A run whose action fails is `unavailable` with the failure's reason as the message, and records nothing, so a later run of the same token runs the action again.
  - A run whose action succeeded but whose pane lookup then failed is `unavailable` with the failure's reason as the message. It keeps the action's target, so a later run of the same token runs no action and looks the pane up again.
- `PoolsStatusResponse` is the answer of `Pools.Status` (see [Status](#pools)): the same pools, members, waiters and holds, with one difference. A holder carries only `project`, `command`, `since` and `stopping`, and never a `grant_id` or `client_pid`. A member no grant holds has no `holder`.
- `PoolsHoldResponse` and `PoolsFreeResponse` have no fields. `Pools.Hold` and `Pools.Free` answer as orreryd does (see [Owner holds](#owner-holds)), with the errors below. `Pools` has no `Acquire`, so the phone holds no grant. Each `Pools` call is forwarded to orreryd under the 10-second control deadline.
- The pane list's `Pools` button opens the phone's Pools screen, and back returns to the pane list. The screen shows each pool: each member with its state, then `granted` with the project, command, age and, when set, `stopping`, or `held` with its age, or `free`, and a Hold or Free action for the member. Each waiter shows `#` and its position, project, command and `needs N`. The holds on members of no pool in use are listed under `Holds outside any pool`, each with a Free action. A failed read shows `could not load pools` with a Retry action, and a failed action shows `could not hold NAME` or `could not free NAME`.

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

The phone's pane screen (`android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/PaneScreen.kt`) resumes this stream as follows. A stream that ends `UNAVAILABLE` is opened again 1 second later, from the transcript and seq the screen holds, and that end is not counted as a failure. Any other end is counted, except `NOT_FOUND`, which makes the screen poll at once. After 3 counted failures in a row, with no delta between them, the screen polls its pane until its next start (`android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/PaneScreen.kt: STREAM_RECONNECT_MILLIS`, `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/PaneScreen.kt: STREAM_FAILURES_BEFORE_POLL`, `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/PaneScreen.kt: PaneScreen`).

#### Epics and Steps

`orrery.v1.Epics` reads the gambit epics of the host and the epics orreryd's database holds, and forwards the owner's commands on those epics; `orrery.v1.Steps` makes the owner's calls on one labelled step, and `orrery.v1.Evidence` reads the artifacts that evidence runs kept. `serve` answers all three, and each call is a Connect POST to its path in [Connect API](#connect-api), under the rules of [Guard](#guard). `serve` never starts a step: it never calls `StartStep` or `WaitSteps`.

**Records.** `serve` reads the record root `$HOME/.gambit` read-only (see [orrery serve](#orrery-serve)). Each epic's head is `<root>/<repo-id>/<epic-slug>/state.json`. A non-directory entry, a directory without `state.json`, and a name starting with `.` are not epics. An effort's tasks are read from `<epic directory>/efforts/<n>/state.json`, where `<n>` is all decimal digits, under its `tasks` key. Any other entry under `efforts/` is skipped. The records are only read: `serve` never creates, writes, renames or chmods anything under the root.

- An epic's `id` is `<repo-id>/<epic-slug>`, the same text that a step's epic label carries. `repository` is `<repo-id>` and `slug` is `<epic-slug>`.
- Record text is the value of a JSON string. `null` or an absent key is `""`, and any other JSON value is its compact JSON text. `outcome` and each task's `slug`, `subject` and `status` are record text. A task's `status` is kept as written.
- `changed` is the head's modification time in unix seconds.
- A task's `requirements` are the requirement ids its `requirement` text names: the text split on commas, each part trimmed, empty parts dropped, in order. A `requirement` that is missing, empty or not a JSON string names none (`server.recordRequirements`).
- A file that does not parse, or whose `tasks` is not an array, is skipped and is not an error. Its name goes into `problem`: the head as `<repo-id>/<epic-slug>/state.json: does not parse`, and an effort's file as `efforts/<n>/state.json: does not parse`. `problem` joins the names with `; `, and it is empty when everything read. Only an unexpected read failure is an error.

**Live.** A record epic that orreryd's database does not hold is not live when the leading letters of its `outcome` are `released`. Otherwise it is live when either one of its labelled steps is running or its head was modified within the 24 hours before now. Such an epic that is not live is folded: `live` is false, and it is still listed (`server.epicLive`). The leading letters are the run of ASCII letters at the start of `outcome`, compared case-sensitively, so `released-2` has the leading letters `released`, while `Released` and `releasedX` do not (`server.leadingLetters`). An epic the database holds is live until its phase is `released`, as the next paragraph gives (`server.storedEpicMessage`).

**Orreryd's epics.** `serve` also reads the epics that orreryd's database holds, through orreryd's `ListEpics` and `GetEpic` (see [Epics](#epics)). With no epic client, `serve` reads the records alone. `Epics.History` reads the same `GetEpic`, with `history` set, and the five command calls forward to orreryd's `PauseEpic`, `ResumeEpic`, `StopEpic`, `RetryEpic` and `AnswerEpic` (see **History** and **Commands** below). It calls no other epic method (`server.EpicStoreClient`; `cmd/orrery.runServe`). Each call that reads orreryd's `ListEpics` or `GetEpic` retries an `Unavailable` answer for up to 60 seconds, as the `ListSteps` call does, with each attempt under the call timeout (`server.epicsServer.listStored`, `server.epicsServer.getStored`, `server.epicsServer.getStoredHistory`). An epic the database holds has `source` `orreryd`, and an epic the records alone hold has `source` `record` (`server.epicMessage`, `server.storedEpicMessage`; `orrery.v1.Epic.source`). Its `repository` and `slug` are the parts of its `id` before and after the `/` (`server.storedEpicMessage`). An `id` that both the database and the records hold is one epic: its `outcome` is the database's phase, its `changed` is the database's, and its `problem` is the record's head problem and task problems. An epic the database holds with no record has an empty `problem` (`server.epicsServer.List`, `server.storedEpicMessage`).

`Epics.List` answers every epic under the record root and every epic the database holds, live or folded, sorted by `id`. Each epic carries `running_steps`, its labelled steps that have no end, held ones included, oldest first, and `ended_steps`, how many of its labelled steps have ended. In `List` and in `Get`, each epic also carries `outcome_detail`, the head's `outcome_detail` record text, which is empty for an orreryd epic (`server.decodeHead`, `server.epicMessage`; `proto/orrery/v1/epics.proto: Epic.outcome_detail`), and `waiting`, which is true for a live record epic with an empty `outcome`, no running labelled step and no effort task, and false for an orreryd epic (`server.epicMessage`, `server.storedEpicMessage`; `proto/orrery/v1/epics.proto: Epic.waiting`). Each running step is an `EpicStep` with the fields that `Epics.Get` gives in `steps` (see Step fields under `Epics.Get`), except that `List` reads no stored epic, so a read-only step's `role` is empty. It calls `ListSteps` once, and `ListEpics` once when serve has an epic client. An orreryd `ListEpics` that answers `unimplemented`, as an older orreryd does, lists the records as before. Any other failure of it answers `unavailable` "epic store unavailable" (`server.epicsServer.List`, `server.epicsServer.listStored`).

`Epics.Get` answers one epic by an `id` that `Epics.List` gives. It returns the epic, its `tasks` in effort number order (file order within an effort), its `steps`, every labelled step of the epic, oldest first, and its `slices`, which the next paragraph describes. It calls `GetEpic` first when serve has an epic client, then `ListSteps` once, narrowed to the epic. Each step's checks carry their `round`, `name`, `ok`, `detail`, `tail_stdout` and `tail_stderr`, redacted as the Redaction paragraph below gives (`server.epicStepMessage`). A failed `gate` check also carries its `stdout` and `stderr` tails, redacted by the engine, and no other check carries those two (`server.epicStepMessage`). An epic the database holds answers its `requirements` as well, in contract order. Each requirement carries its `id`, `text`, `evidence` and `kind`, which an epic stored before O3d2 fills and a new epic leaves empty. A requirement that names an evidence item also carries its `conclusion`, which the judge checks the evidence against, and its `runs`: the evidence runs whose item is the requirement's id, in the order `ListEvidence` lists them. A requirement stored before O3d2 carries neither (`server.epicRequirementMessages`). An epic the database holds answers its `nodes` as well, in the order `GetEpic` gives them, each run with the rows its kind holds. An evidence node's `runs` are the evidence runs its ids name, with their artifacts, and a gate's or command's `check` carries its output (`server.epicNodeMessages`, `server.epicNodeMessage`, `server.epicNodeCheckMessage`; `orrery.v1.EpicsGetResponse.nodes`). A record epic answers no `nodes`. Each run carries its `id`, `phase`, `source`, `state`, `exit`, `timed_out`, `not_captured`, `verdict`, `judge_reason`, `passed`, `revision`, `created`, `ended`, `log_bytes` and `stderr_bytes`, and its artifacts, each with its `path`, `state`, `size` and `media_type`. The run's log hashes, its judge step and each artifact's blob hash stay with orreryd (`server.epicRunMessages`). An epic the database holds also answers its `contract`, with its sections in the contract's order and `accepted`, the time the epic was created, and its `questions`, the open ones in seq order. Each question has its `id`, `text`, `turn` and `asked`, where `asked` is the end of the turn that asked it, in unix seconds, and 0 while that turn runs or when no turn has its seq (`server.epicContractMessage`, `server.epicQuestionMessages`; `proto/orrery/v1/epics.proto: EpicsGetResponse.contract`, `proto/orrery/v1/epics.proto: EpicsGetResponse.questions`, `proto/orrery/v1/epics.proto: EpicQuestion.asked`). It answers `paused`, whether the epic is paused, `parked`, why it is parked, empty when it is not, and `summary`, which is empty until the summary loop writes it (`server.epicsServer.Get`; `proto/orrery/v1/epics.proto: EpicsGetResponse.paused`, `proto/orrery/v1/epics.proto: EpicsGetResponse.parked`, `proto/orrery/v1/epics.proto: EpicsGetResponse.summary`). A record epic answers none of these: its `contract` is absent, its `questions` are empty, `paused` is false, and `parked` and `summary` are empty. Its `tasks` come from the record of the same id when one exists. A `GetEpic` that answers `not_found` or `unimplemented`, or a serve with no epic client, leaves the epic to the records, as before: the record answers with `source` `record` and no `requirements`, and an id with no record answers `not_found`. Any other failure answers `unavailable` "epic store unavailable" (`server.epicsServer.Get`, `server.epicsServer.getStored`, `server.storedEpicMessage`; `orrery.v1.EpicsGetResponse.requirements`). An `id` that `Epics.List` does not give answers `not_found` "epic not found", and the id is never joined into a path.

**History.** `Epics.History` answers one page of an epic's history, oldest first. Its entries come from three reads. The epic's turns, triggers, decisions and tells come from orreryd's `GetEpic` with `history` set, when the database holds the epic. Its evidence runs come from `ListEvidence`, and its labelled steps from `ListSteps`, with the roles `Get` gives them for an epic the database holds (`server.epicsServer.History`, `server.epicsServer.getStoredHistory`, `server.storedHistoryEntries`, `server.runHistoryEntries`, `server.stepHistoryEntries`, `server.applyStoredRoles`). A `ListEvidence` that still fails after its retries adds no run entries (`server.epicsServer.listEvidence`). A record-only epic has its labelled steps alone (`server.epicsServer.History`). Each entry holds one row, which is a `turn`, `trigger`, `step`, `evidence`, `decision` or `tell`, and a `time`, which is the turn's start, the trigger's creation, the step's start, the run's creation, the decision's recording or the tell's writing. An evidence entry carries the run's `item`, which `Get` leaves empty (`proto/orrery/v1/epics.proto: EpicHistoryEntry`, `proto/orrery/v1/epics.proto: EvidenceRun.item`). The entries sort by `time`, then by kind in the order just given, then by the kind's own key: the seq of a turn, a decision or a tell, and the id of a trigger, a step or a run. Two entries never share a key (`server.historyKey.before`). `after` is the cursor of the last entry the caller read. The page holds the entries strictly after it, and an empty `after` starts at the oldest entry. A cursor is the unpadded base64url of the entry's key, which a client passes back as given (`server.historyKey.cursor`, `server.parseHistoryCursor`). A `limit` of 0 or less gives a page of at most 50 entries, and a larger one gives at most 200 (`server.historyLimit`, `server.defaultHistoryLimit`, `server.maxHistoryLimit`). `next` is the cursor of the page's last entry when an entry follows the page, and empty otherwise (`server.historyPage`). A cursor that does not parse answers `invalid_argument`, checked before the epic is read, and an id that neither the database nor the record root lists answers `not_found` (see [Phone API errors](#phone-api-errors); `server.epicsServer.History`).

**Commands.** `Epics.Pause`, `Epics.Resume`, `Epics.Stop`, `Epics.Retry` and `Epics.Answer` forward their request to orreryd's `PauseEpic`, `ResumeEpic`, `StopEpic`, `RetryEpic` and `AnswerEpic`, passing its fields unchanged: `id` for the four epic controls, `node` as well for `Retry`, and `epic_id`, `id`, `text` and `question` for `Answer` (`server.epicsServer.Pause`, `server.epicsServer.Resume`, `server.epicsServer.Stop`, `server.epicsServer.Retry`, `server.epicsServer.Answer`). Their answers are orreryd's: `Resume` answers `resumed` and `still_limited`, `Answer` answers the decision and its warning, and the other three carry no fields (`server.epicsServer.Resume`, `server.epicsServer.Answer`, `server.epicDecisionMessage`). The forward goes to the client that `server.Config.EpicCommands` names. With no such client, the call answers `unavailable` with `epic command service unavailable` (`server.epicsServer.forwardCommand`, `server.epicCommandsUnavailable`). A call is retried only while its connection was never made, as `rpcretry.NeverConnected` gives it, for up to 60 seconds, and each attempt has the 10-second call timeout (`server.retryUnder`, `rpcretry.Window`, `server.defaultControlTimeout`). A call cut after its connection was made is not sent again, since orreryd may already have taken it (`server.epicsServer.forwardCommand`). orreryd's error passes through `server.translateError`: `deadline_exceeded` answers `session command timed out`, `unavailable` answers `session service unavailable`, `internal` and `unknown` answer `session command failed`, and any other code passes with orreryd's message, which is how a refusal of [Epics](#epics) reaches the caller.

**Step fields.** Each `EpicStep`, in `steps` and in an epic's `running_steps`, carries `id`, `task`, `agent`, `end`, `status`, `held`, `started`, `ended`, `doing`, `checks` and `reason`, and also `stage` and `stage_started`, the engine step's stage and its start, `gate_name`, `gate_limit`, `limited`, `limit_kind`, `limit_resets`, `read_only`, `role`, `summary` and `verdicts` (`server.epicStepMessage`; `proto/orrery/v1/epics.proto: EpicStep`). `gate_limit` is the gate's limit in whole seconds, which is 1800 for a step that names no gate. `role` is `implementer` for a writing step. A read-only step's role is `evidence` when the step is an evidence run's judge step, else `reviewer` when it is a task review's step, else `planner` when it is a planner turn's step, and empty otherwise. `Get` takes these roles from the stored epic it reads, and `List` takes none, so a read-only step's role is empty in `List` and in a record epic's `Get`. A stored epic whose `ListEvidence` fails has no `evidence` role, as it has no runs (`server.storedStepRoles`, `server.applyStoredRoles`). `summary` is the `summary` string of the step's latest return. It is empty when that return is not a JSON object, or when its `summary` is not a string (`server.stepResultSummary`). `verdicts` are the step's judge verdicts in the order the loop recorded them. Each has `round`, `verdict` (`met`, `unmet`, or empty when the call failed) and `reason`. A monitor's verdict is left out (`server.epicStepVerdictMessages`).

**Build tasks.** For an epic that `Get` reads from orreryd's database, `build_tasks` holds each task of its plan, in plan order. A record epic answers it empty (`server.epicBuildTaskMessages`; `proto/orrery/v1/epics.proto: EpicBuildTask`). Each build task has `id`, `title`, `status`, `stage`, `requirements`, `red`, `green` and `reviews`. `stage` is empty before the task's first unit, then `red`, `implement`, `green`, `review` or `verify`. `requirements` are the requirement ids the task covers. `red` and `green` are the evidence runs its build names, absent until it names one. `reviews` are in attempt then position order, and each has `attempt`, `gate`, `step`, `verdict` and `findings`. Each finding has `id`, `claim`, `location`, `step`, `result` and `reason`. Its `result` is `confirmed`, `dropped` or `undecided`, and is empty until the finding is verified (`server.epicTaskReviewMessages`).

The web page lists an epic's `build_tasks` in place of the record's tasks when the epic has any, and its TASKS heading counts them. Each row shows the task's `id`, `stage`, `status` and `title`, and its `red` and `green` runs under it, drawn with the run views of the Requirements, images inline. A task is passed when its `status` is `done` (`web/src/epicview.tsx: EpicView`, `web/src/epicview.tsx: BuildTaskRow`; `web/src/evidence.tsx: EvidenceRuns`). An epic with no build tasks lists its record's tasks as before. The phone's epic screen does the same: its Tasks list shows each build task's `id`, `title`, `status` and `stage` with its `red` and `green` runs under it, drawn with the Requirements' run views, images inline, and the record's tasks when the epic has no build tasks (`android/app/src/main/kotlin/io/github/joshsymonds/orrery/api/OrreryClient.kt: EpicBuildTaskRow`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EpicScreen.kt: BuildTaskRow`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EvidenceRuns.kt: EvidenceRuns`).

**Redaction.** `serve` redacts each new free-text field on read with `redact.Text`. The redacted fields are each check's `detail`, `tail_stdout` and `tail_stderr`; the step's `summary`; each judge verdict's `reason`; and, in `build_tasks`, each task's `title`, each red and green run's `judge_reason` and `not_captured`, and each finding's `id`, `claim`, `location` and `reason` (`server.epicStepMessage`, `server.epicStepVerdictMessages`, `server.epicBuildTaskMessages`, `server.epicBuildRunMessage`, `server.epicTaskReviewMessages`). The other new fields are names and identifiers that serve passes unchanged: `stage`, `gate_name`, `limit_kind`, `role`, the `verdict`, `result` and `status` words, `gate`, `step`, `id`, and the requirement ids. The `stdout` and `stderr` of a failed `gate` check keep their meaning. They are the engine's redacted tails of that check, and serve passes them unchanged (`server.epicStepMessage`).

`Evidence.Artifact` answers the bytes of one artifact of a run, or of the run's stdout log when `name` is empty. With `stderr` set and `name` empty, it answers the run's stderr log instead; orreryd refuses `stderr` with a `name` as `invalid_argument` (`enginehost.evidenceService.GetEvidenceArtifact`). Its `media_type` comes from the artifact's path, compared without case: `image/png` for `.png`, `image/jpeg` for `.jpg` and `.jpeg`, `image/gif`, `image/webp`, `text/plain; charset=utf-8` for `.txt` and `.log` (and both of the run's logs), `application/json` for `.json`, and `application/octet-stream` for any other extension (`server.artifactMediaType`). The call asks orreryd's `GetEvidenceArtifact` once, under the 10-second call timeout, and is not retried. The request is read up to 1 MiB (`server.newEvidenceHandler`, `server.evidenceServer.Artifact`; `server.defaultControlTimeout`, `server.maxControlBody`). With no daemon socket that can be named, serve has no evidence client, and the call answers `unavailable` (`cmd/orrery.runServe`; `server.evidenceServer.Artifact`).

The web page and the phone app show these runs under each requirement of an epic the database holds. Each requirement shows its `id` and `text`, its `kind` only when it is a requirement stored before O3d2, its `conclusion`, and its runs in the order `Epics.Get` gives them (`web/src/epicview.tsx: Requirements`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EpicScreen.kt: RequirementRow`). A run shows its `phase`, `agent-supplied` when its `source` is `supplied`, `running` until its `state` is `ended`, its `verdict` or `no verdict` with the judge's reason, whether it `passed`, its `exit` and the first 12 characters of its `revision` (`web/src/evidence.tsx: EvidenceRunRow`, `web/src/evidence.tsx: sourceOf`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EvidenceRuns.kt: EvidenceRunView`). Its log toggles ask `Evidence.Artifact` for the run's stdout log, or for its stderr log with `stderr` set, each with name `""`, only when opened, and show the text as text. The stderr toggle appears only for a run with stderr bytes (`web/src/evidence.tsx: EvidenceRunRow`, `web/src/evidence.tsx: LogToggle`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EvidenceRuns.kt: EvidenceRunView`, `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EvidenceRuns.kt: RunLog`). Each artifact shows its path, and `missing` or `over cap` for an artifact the run did not keep. A kept artifact whose `media_type` is an image is fetched through `Evidence.Artifact` and drawn inline: the web page from an object URL it revokes when the row goes, the phone from the bytes `BitmapFactory` decodes (`web/src/evidence.tsx: ArtifactRow`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EvidenceRuns.kt: ArtifactView`, `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EvidenceRuns.kt: fetchImage`). Neither starts an evidence run. The web page reads the artifacts through its `Evidence` client, and the phone through `OrreryClient.artifact` (`web/src/app.tsx: App`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/api/OrreryClient.kt: OrreryClient.artifact`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EpicScreen.kt: EpicScreen`).

The web page and the phone app show a failed gate check's two tails under its step, each under its label, and show none for a check that passed or is not a gate check (`web/src/epicview.tsx: EpicView`, `web/src/epicview.tsx: carriesTails`; `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EpicScreen.kt: StepLine`, `android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/EpicScreen.kt: CheckTail`).

`slices` holds one slice per distinct `task` label among the epic's writing steps, in order of each label's first start. A writing step is a labelled step that is not `read_only` and has a non-empty `task`; read-only and untasked steps add no slice. A slice reads all its cells from its label's latest writing step (latest `started`), and from no record.

- `task` is the label.
- `red_test` and `tests_pass` are whether the step's `result`, a JSON object, carries `red_evidence` and `green_evidence` respectively, each as a string that, trimmed, is non-empty and does not start with `NOT RUN` in any case. A `result` that does not parse as a JSON object, and a value that is not a string, is no evidence; neither is an error.
- `working` is always true, since a listed step has run.
- `checks` is whether every check of the step's highest round is `ok` and one of them is named `gate`. With no checks it is false.
- `gate_accepted` is whether the step's `end` is `done`.
- `hue` is `dispatched` while the step's `end` is empty (running or held), `done` when the `end` is `done`, and `gap` for any other end.
- `at` is, while `end` is empty, the 1-based index of the first ring not reached, counting from `red_test` (1), the outermost, inward through `working` (2), `tests_pass` (3), `checks` (4) to `gate_accepted` (5), the innermost. It is the ring a running or held step is working toward, and 0 once the step ended. `gate_accepted` is true only when `end` is `done`, so a running or held slice's `at` is 1 to 5.
- `held` is whether the step is held, while `end` is empty. It is false once the step ended.
- `error_at` is 0 while `end` is empty and when `end` is `done`. It is 4 (`checks`) for `checks_failed`, 5 (`gate_accepted`) for `goal_unmet`, and for any other end the first ring not reached, numbered as `at` numbers it.

When `ListSteps` cannot be read, or serve has no step client, `Epics.List` and `Epics.Get` answer `unavailable` "step service unavailable".

`Steps.Hold`, `Steps.Nudge` and `Steps.Stop` act on one step. Each call first asks `StepStatus` about the `id`, and forwards only when the step has an epic label. A step that is absent, or that has no epic label, answers `not_found` "step not found" and nothing is forwarded, so an unlabelled step is never listed or driven. A forwarded call goes to orreryd's `HoldStep`, `NudgeStep` or `StopStep` under the 10-second deadline of [Control API](#control-api). `Steps.Hold` and `Steps.Stop` retry an `Unavailable` answer from `orreryd`, and `Steps.Nudge` retries only a call whose connection was never made, so a nudge cut mid-call is not sent again. The `StepStatus` lookup retries an `Unavailable` answer. These retries run for up to 60 seconds, and each attempt has the 10-second deadline (`server.stepsServer.Hold`, `server.stepsServer.Nudge`, `server.stepsServer.Stop`, `server.stepsServer.forward`, `server.stepsServer.labeled`, `server.retryUnder`; `server.defaultControlTimeout`). `Nudge` sends the owner's `text` to the step. A `failed_precondition` from orreryd passes through with its message, which is the reason a hold or nudge was refused. Any other error answers as the forward's errors do (see [Control API](#control-api)). With no step client, the three calls answer `unavailable` "step service unavailable". The three responses have no fields.

A step's conversation opens through `Sessions.WatchConversation` with the step's `id`, which admits it when it has an epic label (see [Headless session stream](#headless-session-stream)).

#### Phone API errors

Errors are Connect codes. The message column gives the text of each error exactly. Where the message is "the failure's reason", the text is the reason of the failure as the server gives it, and the message carries it unchanged.

| Connect code | When | Message |
|---|---|---|
| `invalid_argument` | a malformed pane ID | `malformed pane id` |
| `invalid_argument` | `Panes.Screen` with a `history` above 2000; `Panes.Conversation` or `Panes.WatchConversation` with a `limit` above 100 | `bad request query` |
| `invalid_argument` | `Panes.SendKeys` with no keys; `Panes.MarkSeen` with a negative `read_up_to`; `Push.Register` with keys that do not decode | `bad request body` |
| `invalid_argument` | `Push.Register` with an endpoint that is not allowed | `endpoint not allowed` |
| `invalid_argument` | `Picker.Ask` with blank text; `Picker.Run` with a blank token | `bad request` |
| `invalid_argument` | `Presence.Report` with a negative `input_age_ms` | `input age is negative` |
| `not_found` | an absent pane, including a pane that goes away during `Panes.WatchConversation` | `pane not found` |
| `not_found` | `Panes.Conversation` with no live session, no transcript, or a transcript file that is missing; `Panes.WatchConversation` with the same cases, at start or mid-stream; `Panes.PullQueued` with the same cases | `no live session`, `no transcript`, or `transcript not found` |
| `not_found` | `Host.Status` with no `--host-status-cmd`; `Picker.Ask` with no picker configured | `404 page not found` |
| `not_found` | `Push.Unregister` for an endpoint that is not registered | `endpoint not registered` |
| `not_found` | `Picker.Run` with a token no ask issued, or one lost by a restart | `unknown token` |
| `aborted` | `Panes.SendKeys` with `expect_question` that the question check refuses | `question refused` |
| `aborted` | `Panes.PullQueued` with the input box not idle (text in the box, a tmux mode, or no input box on screen), or with the pulled text left in the box when deleting it failed | `input not empty` |
| `failed_precondition` | `Panes.PullQueued` with no waiting message that is not canceled, or with no popAll record after Up | `nothing queued` |
| `failed_precondition` | `Picker.Run` with no tmux server running | the tmux reason |
| `failed_precondition` | `Picker.Run` with an expired token | `expired token` |
| `unavailable` | `Host.Status` with a status command that failed or gave invalid stats | `host status unavailable` |
| `unavailable` | `Panes.WatchConversation` while serve shuts down | `serve is shutting down` |
| `unavailable` | `Picker.Ask` with a picker configuration, text lookup, or pane listing failure | the failure's reason |
| `unavailable` | `Picker.Run` with a failed action, or an action whose pane lookup failed | the failure's reason |
| `invalid_argument` | `Sessions.WatchConversation` with a `limit` above 100 | `bad request query` |
| `not_found` | `Sessions.WatchConversation` with an id that is missing or is not an engine session, and a step that is unknown, has no epic label, or is served with no step client, at start or mid-stream | `session not found` |
| `unavailable` | `Sessions.WatchConversation` on a step whose `StepStatus` fails | `step service unavailable` |
| `unavailable` | `Sessions.ListHeadless` or `Sessions.WatchConversation` with the store file missing, the database busy, the file removed while it is read, or no store reader | `store unavailable` |
| `unavailable` | `Sessions.WatchConversation` while serve shuts down | `serve is shutting down` |
| `internal` | `Sessions.ListHeadless` with a read failure, or a row whose `changed` or `seen` is not unix seconds | `read headless sessions` |
| `internal` | `Sessions.WatchConversation` with a read failure, or a stored value that does not decode | `read sessions` |
| `not_found` | `Epics.Get` or `Epics.History` with an id that `Epics.List` does not give | `epic not found` |
| `invalid_argument` | `Epics.History` with an `after` cursor that does not parse | `epic history cursor does not parse` |
| `not_found` | `Steps.Hold`, `Steps.Nudge` or `Steps.Stop` with a step that is absent or has no epic label | `step not found` |
| `failed_precondition` | `Steps.Hold` or `Steps.Nudge` that orreryd refuses | the message orreryd gives |
| `unavailable` | `Epics.List`, `Epics.Get` or `Epics.History` when `ListSteps` cannot be read, or serve has no step client; any `Steps` call with no step client | `step service unavailable` |
| `unavailable` | `Epics.List` when orreryd's `ListEpics` fails with a code other than `unimplemented`, or `Epics.Get` or `Epics.History` when its `GetEpic` fails with a code other than `not_found` or `unimplemented`, an `Unavailable` answer once its retries have run out | `epic store unavailable` |
| `unavailable` | `Epics.Pause`, `Epics.Resume`, `Epics.Stop`, `Epics.Retry` or `Epics.Answer` with no epic command client | `epic command service unavailable` |
| `unavailable` | the same five calls, when orreryd answers `unavailable`, or when its connection is never made and the retries run out | `session service unavailable` |
| `deadline_exceeded` | the same five calls, when an attempt outlives the 10-second call timeout, or orreryd answers `deadline_exceeded` | `session command timed out` |
| `internal` | the same five calls, when orreryd answers `internal` or `unknown` | `session command failed` |
| orreryd's code | the same five calls, when orreryd refuses them with another code, such as `not_found`, `invalid_argument`, `already_exists` or `failed_precondition` | orreryd's message, passed through |
| `unavailable` | `Evidence.Artifact` with no evidence client, or whose orreryd answer is `Unavailable` | `evidence service unavailable`, or `session service unavailable` (`server.translateError`) |
| `invalid_argument` | `Evidence.Artifact` with an empty `run`, as orreryd refuses it | `run: empty`, passed through from orreryd (`server.translateError`) |
| `not_found` | `Evidence.Artifact` with a run orreryd does not hold, or with a name the run does not keep | `run: no run <id>`, or `name: no kept artifact <name>`, passed through from orreryd (`server.translateError`) |
| `deadline_exceeded` | `Evidence.Artifact` that outlives the 10-second call timeout | `session command timed out` (`server.controlTimedOut`) |
| `internal` | `Evidence.Artifact` with any other orreryd failure | `session command failed` (`server.controlFailed`) |
| `internal` | a tmux failure, a store failure, a transcript read failure, a host name failure, or a failed seen report | `internal server error` |
| `invalid_argument` | `Pools.Hold` or `Pools.Free` with a member name outside `[A-Za-z0-9][A-Za-z0-9._-]*` | orreryd's message, passed through |
| `not_found` | `Pools.Free` of a member that is not held | orreryd's message, passed through |
| `unavailable` | `Pools.Status`, `Pools.Hold` or `Pools.Free` with no daemon socket path, a session socket that cannot be dialed, or orreryd with no store or a failed restore; `Pools.Hold` or `Pools.Free` also when orreryd's broker has closed at shutdown | `session service unavailable` |
| `deadline_exceeded` | a `Pools` call that outlives the 10-second forward | `session command timed out` |
| `internal` | `Pools.Hold` or `Pools.Free` with a failed store write, or any other internal answer of orreryd | `session command failed` |

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

orrery E27a (turn end field) added the `last_turn_end` key to each pane of `GET /v1/panes`, additively. It is a key of serve's answer only: `orrery list --json` (section 3) is unchanged. No existing name or value changes.

orrery E27a (interrupt end) changed serve's answer, and the change is not additive. A `working` pane whose transcript holds an interruption line after its `@orrery_changed` now lists as `done`, with no tool and a new `@orrery_changed`, and serve writes those options when it lists panes (section 1). No existing name or value changes.

orrery E21 (compaction item) changed the conversation route, and the change is not additive. A compaction is now a `compaction` item, with the `trigger` and `tokens` keys of its boundary, where it was a `notice` item of kind `compaction`. The `compaction` notice is no longer sent, so a reader that matched it must read the item instead. The item keys `trigger` and `tokens` and the answer's `context` key were added additively. No other existing name or value changes.

orrery notify quiet added, additively: the `seen` key of HookFrame and the seen-report frame (section 9); the `seen --pane ID --focus` form (section 11); the `time` key of the notice payload and the clear payload (section 12); the clear push and the kept notice times (section 11, orreryd); the session notice clear on the session's own seen; and the suppression of Web Push for a `Stop` whose `background_tasks` holds a subagent or workflow. No existing name or value changes, except the focus-mark change below. The tmux focus hook used to run `seen --pane` with no input check. It now runs `seen --pane ID --focus`, so a focus change marks the pane seen only with input in the last 30 seconds. This is a change, not an addition.

orrery H3-0a (phone API) added, additively: the `orrery.v1.Panes`, `orrery.v1.Host`, `orrery.v1.Push` and `orrery.v1.Picker` services and their eleven calls (see [Connect API](#connect-api) and [Phone API](#phone-api)); the Connect path prefixes of [Guard](#guard); and the generated Kotlin in `android/app/src/main/java` (see [Connect API](#connect-api)). No existing name or value changes.

The picker run's failed-run rule changed in the same effort, and this is a change, not an addition. A run whose action succeeded but whose pane lookup then failed used to record nothing, so a later run ran the action again. Such a run now answers 502 and keeps the action's target, so a later run of the same token runs no action and looks the pane up again. The status and reason of a failed run are unchanged.

orrery O1a (steps) added, additively: the `orreryd` flags `--step-cap` and `--gate-lock`, and the `step-worktrees` directory beside the store file; `--sessions` now also serves the step service; the `orrery.engine.v1.Steps` service on `sessions.sock`, its four calls and their Connect codes (see [Steps](#steps)); the `step` value of the `kind` column of `sessions`; the `steps` table (migration `00004_steps.sql`, see [Steps in the store](#steps-in-the-store)); the step end kinds `done`, `returned`, `checks_failed`, `failed`, `stopped` and `engine_restarted`; and the step rules of [Step lifecycle](#step-lifecycle). Step rows are not listed by the typed `List`; their `engine_*` events reach `orrery.v1.Events.Watch` as other engine events do. No existing name or value changes.

orrery removed the REST routes of the phone API: `GET /v1/panes`, `POST /v1/panes`, `GET /v1/panes/{id}/screen`, `GET /v1/panes/{id}/conversation`, `GET /v1/host`, `POST /v1/panes/{id}/text`, `POST /v1/panes/{id}/keys`, `POST /v1/panes/{id}/seen`, `POST /v1/push/register`, `DELETE /v1/push/register`, `POST /v1/picker/ask` and `POST /v1/picker/run`. `POST /v1/panes`, which opened a window, is removed with them and has no replacement. Each other route is answered by the Connect call of [Phone API](#phone-api) that replaced it, and that section states the rules the route had. The guard's rule for non-Connect POST and DELETE paths went with the routes, so every path other than a Connect path answers 404 after the login check (see [Guard](#guard)). This is a removal, and it changes serve's HTTP surface. No Connect name or value changes.

orrery H3b (conversation stream) added, additively: the `orrery.v1.Panes.WatchConversation` call, its messages `WatchConversationRequest`, `WatchConversationResponse` and `ConversationState`, and the `unfinished` field of `WatchConversationResponse`, which holds `EngineText` values that pane streams never set (see [Conversation stream](#conversation-stream)). The phone API now has twelve calls where it had eleven (see [Phone API](#phone-api)). The guard's Connect prefixes already covered the stream, so the guard is unchanged. No existing name or value changes.

orrery desk notices (effort 1) added, additively: the desk stream on `notices.sock` beside the daemon socket, with its `hello`, `notice` and activity lines (see [Desk stream](#desk-stream)); the `orrery notices` and `orrery desk` commands and the `--sound` and `--host` flags of `desk` (see [orrery notices](#orrery-notices) and [orrery desk](#orrery-desk)); the desk presence of the stream; the pane notices and the session notices on the stream; and the `PATH` lookups of `ssh`, `notify-send` and `pw-play` by `desk`. No existing name or value changes, except the removal below.

orrery desk notices (effort 1) removed the ntfy topic. The variables `ORRERY_NTFY_URL_FILE` and `ORRERY_NTFY_TOKEN_FILE`, their constants `protocol.EnvNtfyURLFile` and `protocol.EnvNtfyAuthFile`, the `internal/ntfy` package, and every ntfy post of `orreryd`, including the session notices, are gone. The notices those posts carried are on the desk stream, which `orrery notices` reads, and the `orrery desk` popups show them. `--push-host` and its default `ntfy.sh` are unchanged, because they name the Web Push relay (section 11). This is a removal, not an addition: a setup that set either variable receives no ntfy notice, and reads the desk stream instead.

orrery L1a1 (pools) added, additively: the `orrery run` subcommand, its `--needs` flags, its environment variables and its exit statuses (see [orrery](#orrery)); the project file, the machine file and the project naming rule (see [Pool files](#pool-files)); the `orrery.engine.v1.Pools` service on `sessions.sock`, its `Acquire` call and its Connect codes, and the broker's restore at start and its 60-second status tick (see [Pools](#pools)); the member states, the grant order and the member commands, with their `ORRERY_POOL` and `ORRERY_MEMBER` variables (see [Pool members](#pool-members)); and the `grants` and `grant_members` tables (migration `00007_grants.sql`, see [Grants in the store](#grants-in-the-store)). `--sessions` now also serves the Pools service. No existing name or value changes.

orrery notice routing (effort 1, N1b2a) added, additively: the `read` key of HookFrame and the read report (section 9); the read reports that `serve` sends (section 11, `orrery serve`) and the Phone API reads that carry them; and the notice log, its keys, route values and reasons (see [Notice log](#notice-log)). No existing name or value changes.

Notice routing (see [Notice routing](#notice-routing)) is a change, not an addition. Before it, every pane notice of a transition to `waiting` or `done` was posted to the desk stream, every headless session notice went to the desk stream and to Web Push, and every notice went to Web Push unless its `Stop` held a subagent or workflow. A `Stop` whose `background_tasks` holds a subagent or workflow now goes nowhere, where the desk stream used to carry it. The routing rules now decide each notice: a notice held for a watched pane reaches neither sink until the watch lapses, and then goes to the desk stream, to Web Push, or to both, by where the user is.

orrery done judge (effort 1, N1b2b) added, additively: the `quiet` key of a desk stream notice line (see [Desk stream](#desk-stream)); the `silent` key of the Push payload (see [Push payload](#push-payload)); and the `unneeded` reason and the `needed`, `judge failed` and `judge late` clauses of the notice log (see [Notice log](#notice-log)). No existing name or value changes.

Plain pane dones now wait 10 seconds once they are unwatched, and an unneeded one may arrive quiet on the desk and silent on the phone. That is a change, not an addition, and [Notice routing](#notice-routing) states the rules.

orrery H3e2 (epics and steps) added, additively: the `orrery.v1.Epics` and `orrery.v1.Steps` services and their five calls (see [Epics and Steps](#epics-and-steps)); the Connect path prefixes `/orrery.v1.Epics/` and `/orrery.v1.Steps/` of [Guard](#guard); serve's read of the record root `$HOME/.gambit`; and serve's calls of orreryd's `ListSteps`, `StepStatus`, `HoldStep`, `NudgeStep` and `StopStep`. `Sessions.WatchConversation` also streams a step with an epic label, through serve's `StepStatus` call (see [Headless session stream](#headless-session-stream)). A change, not an addition: `Say`, `Answer`, `Interrupt` and `Stop` on a step id now answer `failed_precondition` (see [Control API](#control-api)), where they forwarded the call before. No other existing name or value changes.

orrery W1b (web reader) added, additively: the `orrery.v1.Panes.PullQueued` call and its messages `PullQueuedRequest` and `PullQueuedResponse` (see [Phone API](#phone-api)). The phone API now has thirteen calls where it had twelve. The guard's Connect prefixes already covered the call, so the guard is unchanged. No existing name or value changes.

orrery L1a2 (pool holds) added, additively: the `orrery hold [--off] MEMBER` and `orrery run status` subcommands, with their exit statuses (see [orrery](#orrery)); the stop path of `orrery run`, with its stderr line and its reclaim that queues again at the request's place (see [orrery](#orrery) and [Pools](#pools)); the `orrery.engine.v1.Pools` calls `Status`, `Hold` and `Free`, the `command` field of `AcquireRequest` and the `stop` event of `AcquireResponse` (see [Pools](#pools) and [Owner holds](#owner-holds)); the `holds` table (migration `00008_holds.sql`, see [Holds in the store](#holds-in-the-store)) and the `command` column of `grants`; the tree's `p` and `h` keys (see [Tree keys](#tree-keys)); and the phone's `orrery.v1.Pools` service with its three calls, served through `serve` behind the guard (see [Phone API](#phone-api) and [Guard](#guard)). The phone API now has sixteen calls where it had thirteen. No existing name or value changes.

orrery named gates added, additively: the `orrery gate` subcommand and its exit statuses (see [orrery](#orrery)); the gates file and the `[gates]` table of the project file (see [Gate files](#gate-files)); the `gate_name` and `gates_file` fields of `StartStep` (`proto/orrery/engine/v1/step.proto` fields 14 and 15) and the `gateName` and `gatesFile` arguments of `step_start` (see [Steps](#steps) and [step_start](engine-protocol.md#step_start)); and the `gate_name`, `gate_source` and `gate_needs` columns of the `steps` table (migration `00009_step_gate.sql`, see [Steps in the store](#steps-in-the-store)). This is a removal too, and it is not additive: the `orreryd` flag `--gate-lock` is gone, and the `gate` and `gate_limit_seconds` fields of `StartStep` are refused with `invalid_argument`; the `gate` and `gateLimitSeconds` arguments of `step_start` are refused as unknown keys.

orrery E31a1 (holder) added, additively: the `orrery holder` subcommand and its exit statuses (see [orrery](#orrery)); the holder directory, its files, socket, spool, exit file, child and idle timeout (see [Holder](#holder)); and the holder's wire protocol at version 1, with its `hello`, `stop` and `stopped` frames frozen across versions and the versioning rule of [Versioning](#versioning) (see [Holder](#holder)). No existing name or value changes.

orrery E31a2 (hold scope) added, additively: the `orreryd` flag `--holder-scope PATH` (see [orreryd](#orreryd)), and each headless session's start through its holder, with its stdout and stdin paths (see [Headless sessions](#headless-sessions)). Three values change, none of them additive: `orrery holder` exits with the child's status when the child ended on its own, where E31a1 exited 0 (see [orrery](#orrery)); the exit record's `stderr` keeps the child's raw tail, where it was trimmed (see [Exit file](#exit-file)); and a session's stop sends `SIGTERM` to the child, where it sent `SIGINT` (see [Lifecycle rules](#lifecycle-rules)). No existing name changes.

orrery W3a (epic chart) added, additively: the `slices` field of `EpicsGetResponse` and the `EpicSlice` message (see [Epics and Steps](#epics-and-steps)). No existing name or value changes.

orrery sent-push-restart added, additively: the `pushed_notices` table, its migration `00012_pushed_notices.sql` and its columns (see [Pushed notices in the store](#pushed-notices-in-the-store)); the read of the kept push times at start (see [orreryd](#orreryd)). No existing name or value changes.

orrery agent gates (effort 1) added, additively: the `agent`, `must` and `must_not` keys of a `[gates.NAME]` table, with the agent gate's shape, refusals and assertion order (see [Agent gates](#agent-gates)); the verdict schema, `internal/gates/verdict.json`, and the first prompt of an agent gate (see [Agent gates](#agent-gates)); a read-only start naming an agent gate, which takes the gate's agent, verdict schema and done value `done` (see [Steps](#steps)); and the refusal of an agent gate by `orrery gate`, with exit 75 (see [orrery](#orrery)). The `required` list of `step_start`'s MCP schema drops `agent` and `schema`. The service still refuses an empty one unless the start names an agent gate (see [step_start](engine-protocol.md#step_start)). No existing name changes. That `required` list is the only existing value that changes, and an argv gate's keys, shape and refusals are unchanged.

orrery step prompt sender and round added, additively: the `sender` and `round` keys of the `engine_prompt` payload (see [Engine sessions in the store](#engine-sessions-in-the-store)); the `sender` and `round` fields of `EnginePrompt` and of the conversation item (see [Connect API](#connect-api) and [Headless session stream](#headless-session-stream)); and the copy of both onto the `prompt` item of a headless session's stream. No existing name or value changes. An older build cannot decode a step prompt stored with them (accepted, DL35).

orrery E31b (session re-adoption) added, additively: the `sessions` columns `consumed`, `replay_from`, `background`, `turn_output` and `prompts_from`, the `events` column `write_id`, and their migration `00013_engine_readopt.sql` (see [Engine sessions in the store](#engine-sessions-in-the-store)); the `consumed` and `replayFrom` keys of the holder's `attached` frame, with `WireVersion` still 1, and the holder's `holder.json` pids record (see [Holder](#holder)); and the re-adoption, drain and stop-then-drain of each session's holder at start (see [Lifecycle rules](#lifecycle-rules)). Four values change, none of them additive: with `--holder-scope`, a shutdown records nothing for a running session, where E31a2 recorded it `interrupted` with `orreryd stopped during the turn`; a restart re-adopts a session whose holder is live, where E31a2 stopped the holder and recorded the session `interrupted` with `orreryd restarted during the turn`; a session whose holder ended while `orreryd` was down records the child's own exit, where it was recorded `interrupted`; and each stdin write carries the write id of its kind, where every write carried a fresh random id (see [Headless sessions](#headless-sessions)). No existing name changes.

orrery agent gates (verdict) added, additively: the `assertions` check of an agent gate step and its verdict rules, with the first `fails` kept for the run, also from a turn end that is no round; the `gate_failed` step end kind; and the `overtime` end of an agent gate step at its gate's `limit` (see [Step lifecycle](#step-lifecycle) and [Agent gates](#agent-gates)). An agent gate step's return is never judged. This is the second half of agent gates: the first half, recorded under orrery agent gates (effort 1) above, defines and starts an agent gate, and the two halves are released together. No existing name or value changes, and an argv gate's steps and every ordinary step's checks, judge and ends are unchanged.

orrery O3a (epic store) added, additively: the `orrery.engine.v1.Epics` service on `sessions.sock`, its four calls and their Connect codes (see [Epics](#epics)); the `epics`, `epic_failures`, `epic_additions`, `epic_requirements`, `epic_premises`, `epic_exclusions`, `epic_alternatives`, `epic_release_steps` and `epic_decisions` tables and the two triggers on `epic_decisions`, from migration `00014_epics.sql` (see [Epics in the store](#epics-in-the-store)); serve's `ListEpics` and `GetEpic` calls of that service, and its merge of the epics it holds with the records (see [Epics and Steps](#epics-and-steps)); the `epic_create`, `epic_get`, `epic_list` and `epic_answer` tools of `orrery mcp` (see [Step and epic tools](engine-protocol.md#14-step-and-epic-tools)); and the phone's `Epic.source` field (field 10), the `EpicsGetResponse.requirements` field (field 5) and the `EpicRequirement` message of `orrery.v1.Epics` (see [Epics and Steps](#epics-and-steps)). No existing name or value changes.

orrery E31c (step and gate re-adoption) added, additively: the `steps` columns `loop_round`, `loop_continues`, `loop_nudge`, `loop_owner_nudge`, `loop_nudged`, `loop_held`, `loop_judge_sent`, `loop_seq`, `gate_grant`, `gate_holder` and `gate_round`, the `grants.holder` column, and their migration `00015_step_readopt.sql` (see [Steps in the store](#steps-in-the-store) and [Grants in the store](#grants-in-the-store)); the `--gate-limit` flag of `orrery holder`, its `limit` exit reason and the gate child's rules (see [orrery](#orrery) and [Holder](#holder)); the re-adoption, gate, shutdown and restart rules of [Step lifecycle](#step-lifecycle); and the client retries and watch resumes of [orrery](#orrery), [Connect API](#connect-api), [Steps](#steps) and [Epics and Steps](#epics-and-steps). Five values change, none of them additive: a restart re-adopts a step whose holder is live, where E31b ended each open step `engine_restarted`; a restart keeps a stored grant while its holder directory is alive, where E31b kept it only while its client pid lived, and a reclaim may name the holder; `session start`, `say` and `stop` on the session socket, `orrery hold` and `hold --off`, the step tools of `orrery mcp` and serve's `Steps` calls retry an `Unavailable` answer for up to 60 seconds, where they failed at once; `orrery events --follow`, `session --url ... watch`, the web page's events stream and the phone's pane screen reopen after an `Unavailable` end, where they ended with it; and a shutdown under `--holder-scope` detaches a running gate, where E31b's `Steps.Close` killed it. Merged after the agent gate verdict and the O3a epic store, a step that runs an agent gate is not re-adopted and ends `engine_restarted` at a restart, as every step did before (see [Restart](#step-lifecycle)), and serve's `ListEpics` and `GetEpic` calls retry an `Unavailable` answer as its `ListSteps` call does (see [Epics and Steps](#epics-and-steps)). No existing name changes.

orrery web presence (effort 1) added, additively: the `orrery.v1.Presence` service and its `Report` call (see [Connect API](#connect-api) and [Phone API](#phone-api)); the `/orrery.v1.Presence/` Connect path prefix of [Guard](#guard); the `input` key of HookFrame and the input report (section 9); serve's input reports to `orreryd` (see [orrery serve](#orrery-serve)); and the desk's presence from the newest web input while a stream is open (see [Desk stream](#desk-stream)). The removals are a change, not an addition: the desk's activity reports on the stream and its idle window, the desk link's periodic activity reports, the input of the desk's tmux clients and of orreryd's local tmux clients as desk signals, and `orrery desk`'s use of the tmux server that `-L` and `-S` name, which those reports came from (the global flags of section 6 are still accepted). Watching (rule 2 of [Notice routing](#notice-routing)) is unchanged. No other existing name or value changes.

orrery restart-cost added, additively: the readiness signal of `orreryd`, one `READY=1` datagram to `NOTIFY_SOCKET` once it serves, and its read and removal of `NOTIFY_SOCKET` at start (see [orreryd](#orreryd)). Three values change, none of them additive: a restart writes nothing for an engine row with no unfinished text and no running turn, where it wrote every row with no live session, and `Steps.Reconcile` stores its ends in one store transaction, where it stored each in its own (see [Reconcile at start](#lifecycle-rules) and [Step lifecycle](#step-lifecycle)); a dropped settle write or step end no longer ends the pass, the other rows settle or end and the first error is returned, where the first read or write error ended it (see [Reconcile at start](#lifecycle-rules) and [Step lifecycle](#step-lifecycle)); and `orreryd`'s children no longer inherit `NOTIFY_SOCKET`, which `orreryd` removes from its environment at start, where they inherited it (see [Headless sessions](#headless-sessions)). No existing name changes.

orrery O3d1 (usage-limit hold) added, additively: the `orrery step resume ID` subcommand and its exit statuses (see [orrery](#orrery)); the `ResumeStep` call of the `orrery.engine.v1.Steps` service and its Connect codes (see [Steps](#steps)); the `step_resume` tool (see [step_resume](engine-protocol.md#step_resume)); the `limited`, `limit_kind` and `limit_resets` fields of the `Step` message, which are `step.proto` fields 18 to 20 (see [Steps](#steps)); the `loop_limited`, `loop_limit_kind` and `loop_limit_resets` columns of the `steps` table, from migration `00017_step_limit.sql`, and the `pushed_limit_notices` table, from migration `00018_pushed_limit.sql` (see [Steps in the store](#steps-in-the-store) and [Pushed notices in the store](#pushed-notices-in-the-store)); and the usage-limit notice, with its `limited` state in the desk stream and the notice log and its `limit` key in the Push payload and the Clear payload (see [Step lifecycle](#step-lifecycle), [Desk stream](#desk-stream), [Notice log](#notice-log) and [Push payload](#push-payload)). Three values change, none of them additive: a step sends no notice except its usage-limit notice, where it sent none (see [Session notifications](#session-notifications) and [Step lifecycle](#step-lifecycle)); a limited step's resume deletes its `engine_rate_limits` row, where no query deleted those rows (see [Engine sessions in the store](#engine-sessions-in-the-store)); and `Steps.Reconcile` starts the loop of a registered step whose row is limited, where it ended each open row with no live loop `engine_restarted` (see [Step lifecycle](#step-lifecycle)).

orrery O3c2 (planner controls) added, additively: the `orrery epic` subcommand and its forms (see [orrery](#orrery)); the `PauseEpic`, `ResumeEpic`, `StopEpic` and `RetryEpic` calls of the Epics service, the `question` field of `AnswerEpicRequest`, the `warning` field of `AnswerEpicResponse`, the `question` field of `EpicDecision`, and the refusals of [Epics](#epics); the `epic_pause`, `epic_resume`, `epic_stop` and `epic_retry` tools of `orrery mcp`; the answers tied to a question, the notes that answer none, and the `answer:<id>` trigger (see [Planner turns](#planner-turns)); the epic notices, their attention loop, and their lines in the notice log and on the desk stream (see [Planner turns](#planner-turns), [Notice log](#notice-log) and [Desk stream](#desk-stream)); the `epic` key of the Push payload and of the clear payload, and the `pushed_epic_notices` table (see [Push payload](#push-payload) and [Pushed notices in the store](#pushed-notices-in-the-store)); the `epic` key of the notice log line; migration `00019_planner_controls.sql` (see [Epics in the store](#epics-in-the-store)); and the phone's tag and tap for an epic push (see [Push payload](#push-payload)). One value changes, and it is not additive: a role-file error that parks an epic now claims a planner turn and frees it with the error, where it parked the epic with no turn (see [Planner turns](#planner-turns)). No existing name changes.

orrery O3d2 (evidence) added, additively: the `orrery.engine.v1.Evidence` service on `sessions.sock` with its three calls, and the `orrery.v1.Evidence` service with its `Artifact` call and the `/orrery.v1.Evidence/` Connect path prefix of [Guard](#guard) (see [Evidence](#evidence) and [Epics and Steps](#epics-and-steps)); the `base`, `gates`, `done_gates` and `review_gates` fields of the engine `EpicContract`, the `item` field of `EpicRequirement`, the `gate` and `evidence` fields of `EpicTask`, and the messages `EpicGate`, `EvidenceItem`, `EvidenceRun` and `EvidenceArtifact` (see [Epics](#epics)); the `runs` and `conclusion` fields of the public `EpicRequirement` (see [Epics and Steps](#epics-and-steps)); the `evidence run`, `evidence list` and `evidence get` subcommands with their exit statuses, and the `--gate-tail` flag of `orrery holder` (see [orrery](#orrery)); the `evidence_run`, `evidence_list` and `evidence_get` tools of `orrery mcp` (see [Step and epic tools](engine-protocol.md#14-step-and-epic-tools)); the Go-only `StepStart.AgentGate` field (see [Agent gates](#agent-gates)); the tables `epic_gates`, `epic_gate_lists`, `evidence_runs` and `evidence_artifacts`, and the columns `epics.base`, `epic_requirements.item`, `epic_tasks.gate`, `epic_tasks.evidence` and `steps.loop_gate_from`, from migration `00020_evidence.sql` (see [Epics in the store](#epics-in-the-store)); and the `evidence/blobs` and `evidence-worktrees` directories beside the store file (see [Evidence](#evidence)). No existing name or value changes, apart from the changes below.

orrery O3d2 (evidence) changed, and these changes are not additive. A new epic's requirement names its evidence as an `item`, and `epic_create` refuses a requirement with no item, or one that also names the legacy `evidence` or `kind`, where a requirement needed an evidence text and a kind (see [Epics](#epics)). A planned task names a `gate` and an `evidence` item, its `check` is empty, and the planner's return schema refuses a task that still names `check` (see [Planner turns](#planner-turns) and [The planner's return](engine-protocol.md#the-planners-return)). The plan check refuses a task whose gate or judge does not resolve (see [Planner turns](#planner-turns)). The planner's prompt and role text describe the new task fields. The `task_check` column of `epic_tasks` is dropped, and each stored task's check moves into its evidence (see [Epics in the store](#epics-in-the-store)). A step that runs an agent gate is re-adopted at a restart when its gate is rebuilt, where it always ended `engine_restarted` (see [Restart](#step-lifecycle)). `Epics.Get` answers each requirement's `runs` and `conclusion` (see [Epics and Steps](#epics-and-steps)).

orrery O3d3 (task build) added, additively: the `branch` field of `StoredEpic`; the `stage`, `attempt`, `step`, `base`, `revision`, `failure`, `review`, `held`, `red`, `green` and `reviews` fields of `EpicTask`; and the `EpicTaskReview` and `EpicTaskFinding` messages (see [Epics](#epics) and [epic_get](engine-protocol.md#epic_get)); the `resumed` and `still_limited` fields of `ResumeEpicResponse` (see [Epics](#epics) and [epic_resume](engine-protocol.md#epic_resume)); the `epics.branch` column, the build columns of `epic_tasks`, and the tables `epic_task_reviews`, `epic_task_findings` and `epic_tells`, from migration `00021_task_build.sql` (see [Epics in the store](#epics-in-the-store)); the `tell` notices of the attention loop, one for each untold tell (see [Planner turns](#planner-turns)); the build Runner, with its passes, branches, units, holds, ref moves, pins and worktrees (see [Task builds](#task-builds)); and the Go-only `StepStart.ID`, `StepStart.CommandGate` and `Evidence.RunTask`, which no socket carries (`enginehost.StepStart`; `enginehost.Evidence.RunTask`). No existing name or value changes, apart from the changes below.

orrery O3d3 (task build) changed, and these changes are not additive. The plan check owns the files of open tasks only, where it owned the files of every task whose status was not `done`, so a failed task's files plan again (see [Planner turns](#planner-turns)). `ResumeEpic` resumes the epic's limited task steps, and it refuses only an epic that is not paused and holds no such step, where it refused every epic that was not paused (see [Epics](#epics)). An open task of an accepted epic with a base starts its build at red, where no build ran before O3d3 (see [Task builds](#task-builds)).

orrery sessions question (effort 1) added, additively: the `ask` key of HookFrame and the sessions question, with its wire form, its two ReadFrame errors and its answer (see [Hook-to-daemon frame](#9-hook-to-daemon-frame)); the `WriteSessionsAnswer` and `ReadSessionsAnswer` functions (see section 9); the answer that orreryd writes on the daemon socket from `--sessions` (see [orreryd](#orreryd)); and the question that `orrery session` start, say and stop put to the daemon before they retry an attempt that never connected (see [orrery](#orrery)). One value changes, and it is not additive: those three verbs against a daemon with sessions off end at once with the `--sessions` refusal, where they retried for up to `rpcretry.Window` (see [orrery](#orrery)). No existing name changes.

orrery O3f1 (workflow format) added, additively: the `workflow` field of `CreateEpicRequest` and the `workflow` argument of `epic_create` (see [Epics](#epics) and [epic_create](engine-protocol.md#epic_create)); the `workflow` field of `StoredEpic`, its `EpicWorkflow` message and the `workflow` field that `epic_get` returns (see [Epics](#epics) and [epic_get](engine-protocol.md#epic_get)); the workflow file format, its load check and the built-in `epic` (see [Workflows](#workflows)); and the `workflow`, `workflow_text` and `workflow_sha256` columns of `epics` and the `epic_workflow_nodes` table, from migration `00022_workflow.sql` (see [Epics in the store](#epics-in-the-store)). A `CreateEpic` that names no workflow is also refused when the base holds a `.orrery/workflows/epic.yaml` that does not load. No existing name or value changes.

orrery followups 2b (checkout and output tails) added, additively: the `checkout` check of a read-only step and its `checks_failed` end (see [Step lifecycle](#step-lifecycle)); the `stdout` and `stderr` keys of a stored step check, the `stdout` key of the holder's exit record, the `stdout` and `stderr` fields of `StepCheck` and `EpicStepCheck`, the `stderr_hash` and `stderr_bytes` fields of `EvidenceRun`, the `stderr` field of the artifact requests, and the `checkout_head`, `checkout_hash`, `stderr_hash` and `stderr_bytes` columns, from migration `00023_checkout_tails.sql` (see [Steps in the store](#steps-in-the-store) and [Epics in the store](#epics-in-the-store)). orrery followups 2b changed, and these changes are not additive: a gate check's detail no longer carries its output tail, and its judge input and send-back carry the stdout and stderr tails apart; a gate child's exit record keeps its stdout and stderr tails apart, where it held one merged tail; the `log_hash` and `log_bytes` of an evidence run hold the stdout log, where they held the merged output; and gate and capture tails, and every read of a capture log, are redacted (see [Step lifecycle](#step-lifecycle)).

orrery attention B (notices follow the web view) added, additively: the `orrery.engine.v1.Notices` service on `sessions.sock` and the `orrery.v1.Notices` service, each with its `Watch` and `Seen` calls (see [Notices](#notices), [In-app notices](#in-app-notices) and [Connect API](#connect-api)); serve's forward of `orrery.v1.Notices` (see [orrery serve](#orrery-serve)); the `/orrery.v1.Notices/` Connect path prefix of [Guard](#guard); the in-app notices on open web pages; the `StopFailure` hook event (see section 1) and its entry in the [hook settings](#claude-code-hook-settings); the `limit_key` column of `pending_notices` and the `told_limit_steps` table, from migration `00026_limit_told.sql` (see [Pending notices in the store](#pending-notices-in-the-store) and [Told limits in the store](#told-limits-in-the-store)); and the interrupted notice of a step that a restart ends with no epic (see [Session notifications](#session-notifications)). Three values change, none of them additive: the desk is at hand on web input alone, where it also needed an open desk stream (see [Notice routing](#notice-routing), rule 3); a `StopFailure` hook moves its pane to `done`, where orrery ignored it; and a limit open at start is told when it has no mark, where it was remembered and not told, and a pending limit notice is kept across a restart, where it ended `shutdown`. No existing name changes.

orrery followups 3b (turn answers and the judge's command) added, additively: the `answered` column of the `events` table, which an `engine_turn_ended` event fills with the uuids its turn answered, from migration `00027_turn_answered.sql` (see [Engine sessions in the store](#engine-sessions-in-the-store)). orrery followups 3b changed, and these changes are not additive: a turn that ends while a background task is listed is a return when its return's `status` is the step's `done_value`, where it was never a return while a task was listed (see [Step lifecycle](#step-lifecycle)); a prompt is sent under its write id, a random version-4 UUID, as its user-message uuid, where the write id was 128 random bits in hex and the uuid was generated apart from it (see [Engine sessions in the store](#engine-sessions-in-the-store) and [Engine protocol](engine-protocol.md#3-turns)); a re-adopted session owes every prompt that no later stored turn end answers, where it owed the prompts written after its newest turn end (see [Reconcile at start](#lifecycle-rules)); and the evidence judge's prompt names a capture's command, which a supplied run does not name (see [Evidence](#evidence)).

orrery O3f2 (workflow interpreter) added, additively: the `epic_node_runs` table and its migration `00028_node_runs.sql` (see [Node runs in the store](#node-runs-in-the-store)); the node runs of each epic, with their paths, ids, rounds and loop state, and the interpreter that runs every epic's workflow over them (see [Workflows](#workflows) and [Planner turns](#planner-turns)); the build child runs of a plan node run (see [Task builds](#task-builds)); the `workflow:` trigger that a later plan node's start writes (see [Planner turns](#planner-turns)); and the grant of a parked loop's round by `RetryEpic` and `epic_retry`, in the write that ends the park (see [Epics](#epics) and [epic_retry](engine-protocol.md#epic_retry)). One value changes, and it is not additive: an epic whose pinned text does not hash to its pinned sha256, or does not parse and check, is parked with `workflow <name>: <error>`, where the planner and the build ran that epic before (see [Workflows](#workflows)). No existing name changes.

orrery O3f3 (workflow nodes) added, additively: the `gate`, `evidence`, `owned`, `command` and `agent` workflow kinds, with their settings, their positions and their pass rules (see [Workflows](#workflows)); the `attempt`, `revision`, `refs`, `refs_checked`, `result` and `output` columns of `epic_node_runs` from migration `00029_node_work.sql`, and the table `epic_node_checks` from migration `00030_node_checks.sql` (see [Node runs in the store](#node-runs-in-the-store) and [Node checks in the store](#node-checks-in-the-store)); the `node` field of `RetryEpicRequest`, the `node` argument of `epic_retry` and the `NODE` form of `orrery epic retry`, which reruns a parked node (see [Epics](#epics), [epic_retry](engine-protocol.md#epic_retry) and [orrery](#orrery)); the `nodes` field of `StoredEpic`, of `EpicsGetResponse` and of the `epic_get` result, with the node runs each carries (see [Epics](#epics) and [Epics and Steps](#epics-and-steps)). No existing name or value changes.

orrery ops alert notices (effort 1) added, additively: the `POST /alerts` route of `orreryd`'s `--metrics-listen` listener, which takes Alertmanager's webhook and reads its `alertname` label and its `summary` annotation (see [Alert intake](#alert-intake)); the alert notice, with its `firing` state, its desk notice and its routing like any other notice (see [Alert intake](#alert-intake) and [Notice routing](#notice-routing)); the `alert` key of the Push payload and of the Clear payload, and the phone's notification for it (see [Push payload](#push-payload) and [Clear payload](#clear-payload)); the `alert` field of `NoticeKey` (see [Notices](#notices)); and the `pushed_alert_notices` table, from migration `00031_pushed_alert.sql` (see [Pushed notices in the store](#pushed-notices-in-the-store)). One value changes, and it is not additive: `orreryd`'s `--metrics-listen` listener answers `POST /alerts`, where it answered 404 for every path but `GET /metrics` (see [Listeners](#listeners)). No existing name changes.

orrery style A1a (step data) added, additively: the `loop_stage` and `loop_stage_from` columns of the `steps` table, from migration `00032_step_stage.sql` (see [Steps in the store](#steps-in-the-store)) and the stage rules of [Step lifecycle](#step-lifecycle); the `stage`, `stage_started`, `gate_name`, `gate_limit_seconds` and `verdicts` fields of the engine `Step` message, which are `step.proto` fields 21 to 25, and its `StepVerdict` message (see [Steps](#steps)); the `EpicStep` fields 12 to 22, the `EpicStepCheck` fields 6 to 8, the `EpicStepVerdict` message, the `requirements` field (5) of `EpicTask`, the `EpicBuildTask`, `EpicTaskReview` and `EpicTaskFinding` messages, and the `build_tasks` field (7) of `EpicsGetResponse` of `orrery.v1.Epics` (see [Epics and Steps](#epics-and-steps)). Serve's redaction of the new free-text fields on read is part of the same change. No existing name or value changes.

orrery O3e1 (reviewers) added, additively: the `reviewers` field of `EpicContract` (21) and of `StoredEpic` (14), the `EpicReviewer` message, and the `unverified` field (7) of `EpicTaskFinding` (see [Epics](#epics) and [epic_get](engine-protocol.md#epic_get)); the `[[reviewers]]` tables of the project file (see [Reviewers](#reviewers)); the Go-only `StepStart.Agents` field, which defines a step's inline agents (see [Steps](#steps)); and the `steps.agents` column, the table `epic_reviewers` and the column `epic_task_findings.unverified`, from migration `00035_reviewers.sql` (see [Epics in the store](#epics-in-the-store)). orrery O3e1 changed, and these changes are not additive: `epic_create` refuses a `review_gates` that names any gate, where it stored the gates as the epic's review list; every new epic runs the five default reviewers, so its tasks run `floor` and `test_parsimony` at review by default, where a task of an epic with no review gate was accepted unreviewed (see [Reviewers](#reviewers)); and an epic stored before `00035_reviewers.sql` that names review gates keeps them with no reviewers stored, so each of its review starts fails its task with `review did not start: review gate <name> of epic <id> is not a task-stage reviewer` (see [Task builds](#task-builds)).

orrery style C (lists) added, additively: the `outcome_detail` and `waiting` fields of `orrery.v1.Epic` (see [Epics and Steps](#epics-and-steps)). The released rule changed in the same effort, and this is a change, not an addition: a record epic whose outcome's leading ASCII letters are `released` is not live, where before only an outcome of exactly `released` made an epic not live whatever its steps and head (see [Epics and Steps](#epics-and-steps)). No existing name changes.

orrery step gate logs added, additively: the `log` key of a stored step check and the `log` field of `StepCheck`, which is `step.proto` field 7, naming the file a failed gate check's output is kept in under the gate state directory; and the `stdout`, `stderr` and `log` keys of each check that the step MCP server's `step_status` shows (see [Steps](#steps) and **Gate** in [Step lifecycle](#step-lifecycle)). One value changes, and it is not additive: a step's gate keeps the newest 1 MiB of each stream where it kept 8 KiB, and its check's 8 KiB tails start at a whole line. No existing name changes.

orrery O3e3 (release) added, additively: the `release` kind, which the interpreter now registers, with the phases `releasing`, `stopped`, `released` and `done`, and its node-run parts `release <n>` and `judge` (see [Release](#release)); the `ReleaseEpic` call of the Epics service, its `undo_none` field, the `epic_release` tool and the `orrery epic release` form (see [Epics](#epics), [epic_release](engine-protocol.md#epic_release) and [orrery](#orrery)); the `releases` and `release_slot` fields of `StoredEpic` and the `EpicReleaseRun` message (see [Release](#release)); the Go-only `StepStart.Unowned` field and the `steps.unowned` column; and the tables `epic_release_runs` and `epic_release_slots`, from migration `00038_release.sql` (see [Epics in the store](#epics-in-the-store)). orrery O3e3 changed, and these changes are not additive: a `release` node, which the interpreter started and left waiting, now runs (see [Workflows](#workflows)); and `StopEpic` on an epic with no running turn that holds its repository's release slot frees the slot and stops its open release step, where it was refused (see [Epics](#epics)).

orrery attention A (questions and follows) added, additively: the `label`, `choices`, `recommendation`, `replaces`, `replaced_by` and `choice` fields of `EpicQuestion` (fields 5 to 10), the `choice` field of `AnswerEpicRequest` (field 5), the `FollowEpics` call of the `orrery.engine.v1.Epics` service with its `FollowEpicsRequest`, `FollowEpicsResponse` and `EpicFollow` messages, the `follow` form and the `--choice` flag of `orrery epic`, the `epic_follow` tool, the `replaced` value of a question's `state`, and migration `00039_questions.sql` with its `epic_questions` columns and the `question` column of `epic_tells` (see [Epics](#epics), [Epics in the store](#epics-in-the-store), [orrery](#orrery) and [epic_follow](engine-protocol.md#epic_follow)). It changes, and these changes are not additive: an answer to a question with no `choice` is refused, and `other` needs text that is not blank (see **AnswerEpic** under [Epics](#epics)); a planner return's `questions` are objects with `label`, `text`, `choices`, `recommended`, `why`, `confidence` and `replaces`, where they were text, and a question that breaks the rules of the return check is sent back (see [Planner turns](#planner-turns)); every park opens a park question, and a node's park writes that question's seq on its tell (see **Park questions** under [Planner turns](#planner-turns)); and the notices of a park and of a question carry the advice lines, while the planner's prompt shows each decision's choice and the Advice followed section (see **Notices** under [Planner turns](#planner-turns)). No existing name changes.
