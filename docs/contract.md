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
| Input | int64 | `input` | the input time of an input report, unix seconds, omitted at zero |

Wire format:

- One frame per connection.
- One JSON object, terminated by `\n`. The object is written on a single line.
- A frame with `seen` above zero and no `hook` is a seen report. orreryd sends no state change for it.
- A frame with `read` above zero and neither `hook` nor `seen` is a read report: serve's word that the phone read the pane at that time. orreryd sends no state change for it. It keeps the time as the pane's newest read (see [Notice routing](#notice-routing)), and it drops a report whose `tmuxPane` is empty.
- A frame with `input` above zero and neither `hook`, `seen` nor `read` is an input report: serve's word that the web view had input at that time. orreryd sends no state change for it, and it touches no pane. It keeps the newest input it receives (see [Desk stream](#desk-stream)). A zero `input` is absent, not an input report.

Functions:

- `func WriteFrame(w io.Writer, frame HookFrame) error` writes exactly that format.
- `func ReadFrame(r io.Reader) (HookFrame, error)` decodes one frame. It has no line-length limit, because hook payloads can be large. It returns an error on malformed JSON, on a frame with both `seen` above zero and `hook`, on a frame whose `read` is below zero, and on a frame with `read` above zero together with `hook` or `seen`. It also returns an error on a frame whose `input` is below zero, and on a frame with `input` above zero together with `hook`, a non-zero `seen` or a non-zero `read`. Otherwise it returns an error when `hook` is missing, is `null`, or is not a JSON object, unless the frame is a seen report, a read report or an input report.

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
  - Without `--url`, the verbs other than `watch` reach `orreryd` on the session socket, `sessions.sock` beside the daemon socket. The daemon socket path comes from `--socket`, then `ORRERY_SOCKET`, then `$XDG_RUNTIME_DIR/orrery/orreryd.sock`. When the socket cannot be dialed, the error reads `orreryd takes no session commands on SOCKET; start it with --sessions`, where `SOCKET` is the `sessions.sock` path.
  - No `session` verb reads a tmux server, so `-L` and `-S` have no effect on them.
  - A usage error exits 2. A failed call exits 1 and prints the Connect error message, as `session VERB: MESSAGE`.
  - `start`, `say` and `stop` retry an `Unavailable` answer, which includes a connection that cannot be made or is cut, for up to `rpcretry.Window`, 60 seconds, counted from the call's first attempt. The waits grow from 100 ms to 5 s (`internal/cli/session.go:150`, `:196`, `:245`; `internal/rpcretry/retry.go:16`, `:20`-`:24`, `:64`-`:88`). `start` sends a client-chosen id and `say` a client-chosen `prompt_id`, so a retry starts no second session and sends no second prompt (see [Lifecycle rules](#lifecycle-rules)). The other verbs, and `watch`, are sent once.
  - `session --url URL VERB ARGS` sends `start`, `say`, `answer`, `interrupt`, `stop` and `seen` to the `orrery.v1.Control` service of `serve` at `URL` (see [Control API](#control-api)), with the same arguments and output as the socket form, except that `DIR` is not made absolute (see below). Each call is sent once: an `Unavailable` answer exits 1 with no retry, and `start` sends no client-chosen id (`internal/cli/session_url.go:50`-`:68`). It does not dial the session socket, so the client needs no daemon socket and no `--sessions`. Each request carries the `X-Orrery-Client` header that the guard requires. The command sends no `Tailscale-User-Login` header, so the URL must lead through a proxy that sets it, such as `tailscale serve`. `--url` with no URL exits 2.
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
  - A call that `orreryd` answers `Unavailable`, or that a restart cuts, is sent again for up to 60 seconds (`internal/rpcretry/retry.go:16`, `:64`; `cmd/orrery/hold.go:70`-`:73`, `:80`-`:93`). A hold is idempotent, so a repeat is harmless. After a cut attempt, `--off` counts a later `not_found` answer that says `member not held` as freed (`cmd/orrery/hold.go:84`-`:92`).
- `holder --session NAME [--state-dir DIR] [--gate-limit SECONDS] -- PATH [ARGS...]` runs `PATH` with `ARGS` as one session's held child and serves the session's holder socket until the child ends (see [Holder](#holder)). `--session` names the session and is required. `--state-dir DIR` places the session under `DIR/holders` instead of the store's directory. `--gate-limit SECONDS` runs the child as a gate with that limit, a positive whole number of seconds (see [Holder](#holder)).
  - Exit statuses:
    - `0`: the child was stopped or ended by the idle timeout (`exit.json` reason `stopped`, `idle` or `limit`), or it ended on its own with status 0.
    - The child's exit code, when it ended on its own with a nonzero status. A child that a signal ended gives 128 plus the number of the signal that `exit.json`'s `signal` names, whose `code` is then -1 (`cmd/orrery/holder.go:82`-`:105`, `internal/holder/child.go:201`-`:207`).
    - `1`: the holder refused to start or failed, or the child ended on its own with no exit status. Its reason is on stderr as `orrery holder: MESSAGE`, or as `orrery: holder: MESSAGE` when no holders directory can be named (`cmd/orrery/holder.go:68`-`:74`, `:100`-`:101`).
    - `2`: a usage error. That is a missing or invalid `--session`, a flag other than `--session`, `--state-dir` and `--gate-limit`, a `--gate-limit` that is not a positive whole number of seconds, an argument before `--`, or no `PATH` after `--`.
    - A child that ends on its own with status 1 or 2 gives that status too, so the code alone does not say whether the holder or its child failed. The holder writes to stderr only for its own failure (`orrery holder: MESSAGE`, or `orrery: holder: MESSAGE`) and for a usage error (the usage text). An exit that passes on the child's status writes nothing to stderr, and `exit.json` then holds the child's `code` and `signal` (`cmd/orrery/holder.go:65`-`:75`).
- `gate [--gates FILE] NAME` runs the named gate `NAME` under a grant of its needs from `orreryd`. `NAME` is resolved as [Gate files](#gate-files) gives: from `FILE` first when `--gates` names one, then from the project file for the working directory. A relative `FILE` is read from the working directory. `gate` reads the pool files for the working directory, as `run` does (see [Pool files](#pool-files)), and reaches `orreryd` on `sessions.sock` as `run` does.
  - The argv runs through the same `RunWithGrant` path as `run`'s command (`cmd/orrery/gate.go:74`-`:89`). It runs without a shell, in the working directory, with `gate`'s standard input, output and error. Its environment is `gate`'s, plus `ORRERY_GRANT` and one `ORRERY_POOL_<POOL>` for each pool in its needs, as `run` sets them. Its limit counts from the start of each run under its grant, not from the queue (`internal/cli/run.go:70`-`:72`).
  - The lines `run` writes to stderr while its command queues, is stopped, loses its grant or passes its limit are written by `gate` too, with the prefix `orrery run:` (`internal/cli/run.go:189`, `:486`, `:492`, `:585`).
  - After the argv's run has ended or has failed to start, `gate` writes one line to stderr: `orrery gate: NAME from SOURCE FILE: argv ARGV needing POOL=N,...; exit STATUS`. `SOURCE` is `epic` or `project`, `FILE` is the file that defines the gate, `ARGV` is the argv as a JSON array, and `STATUS` is the status `gate` exits with (`cmd/orrery/gate.go:90`-`:91`).
  - Exit statuses:
    - `2`: a usage error. That is a `--gates` with no file, no `NAME`, more than one argument, or a flag other than `--gates`.
    - `75`: the gate did not run. Before the grant, that covers a `NAME` that no file defines, an epic gates file or project file that cannot be read or does not parse, a project that cannot be found (no `git` on `PATH`, or neither `XDG_CONFIG_HOME` nor `HOME` set), a need for a pool that the pool files do not declare, pool files that cannot be read, parsed or merged, and no daemon socket that can be named. `gate` writes `orrery gate: MESSAGE` to stderr for these. An agent gate is refused the same way, once its `NAME` resolves and before any pool file is read: `gate` writes `orrery gate: gate NAME is an agent gate; it runs only as a read-only step` to stderr, with `NAME` as given (`cmd/orrery/gate.go:43`-`:45`, `:120`-`:122`). After the grant request, it covers what `run` gives 75 for: a request `orreryd` refuses, an unreachable `orreryd`, a signal that arrives while the request waits, and a refused reclaim.
    - `124`: the gate's limit passed, and the argv was killed with `SIGKILL`.
    - Otherwise, the argv's own status, as `run` gives it for `CMD`: its exit code, or 128 plus the signal number when a signal ended it. `127` means `argv[0]` is not an executable file, and nothing is requested. `126` means it cannot start after the grant. `1` means its status cannot be read.
  - `SIGINT`, `SIGTERM` and `SIGHUP` stop `gate` as they stop `run`: after the argv has started, the argv gets `SIGTERM` and `gate` returns its status. While the request waits, the signal ends the run with 75 (`cmd/orrery/gate.go:71`-`:72`).

Exit codes:

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | error |
| 2 | usage error |
| 3 | question refused (`send --expect-question`: no question, hash mismatch, empty display fragment, or fragment not visible) |

`run` and `gate` exit with the statuses listed in their bullets above, which are not in this table. `run status` and `hold` use only the codes in this table.

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

`serve` reads the gambit record root, `$HOME/.gambit`, read-only, for the epic calls of [Epics and Steps](#epics-and-steps). It opens the records for reading and never creates, writes, renames or chmods anything under the root. It reaches orreryd's `orrery.engine.v1.Steps` and `orrery.engine.v1.Epics` services on the same session socket, `sessions.sock`, for the step and epic calls of [Epics and Steps](#epics-and-steps) and for the step refusal of [Control API](#control-api). When serve has no step client, the epic and step calls answer `unavailable` "step service unavailable" (see [Phone API errors](#phone-api-errors)), and the Control calls forward as before. When serve has no epic client, the epic calls read the records alone (`internal/server/epics.go:172`-`:214`; `cmd/orrery/main.go:446`-`:452`).

`serve` also serves the `orrery.v1.Pools` service of [Phone API](#phone-api). It forwards `Status`, `Hold` and `Free` to orreryd's session socket under the same daemon socket rule as Control, and it has no `Acquire`.

The picker call `Picker.Ask` (see [Phone API](#phone-api)) looks up `claude` on serve's PATH at each ask, not at start. A missing `claude` answers that ask `unavailable` and does not stop serve.

`serve` also sends `orreryd` a read report (section 9) with the pane and the time of each read it answers. It sends one after each successful `Panes.Conversation` or `Panes.Screen` answer. While a `Panes.WatchConversation` stream stays open, it sends one at the stream's first successful step, and again each time the keepalive interval has passed since the last report. That interval is 5 seconds by default. The report goes to the daemon socket (section 5). When no `orreryd` listens there, the report is dropped.

`serve` also sends `orreryd` an input report (section 9) for each `Presence.Report` it answers (see [Phone API](#phone-api)). The input is dated at serve's clock less the reported `input_age_ms`, in whole unix seconds. An age that is not below serve's clock reports nothing. The report goes to the daemon socket (section 5), and it is dropped when no `orreryd` listens there. Any other failure is written to standard error, and the call still answers.

`--listen` and at least one `--allow-login` are required. Without them `serve` exits 2.

`serve` is meant to sit behind `tailscale serve`, which sets the `Tailscale-User-Login` header that section 12 checks. Do not expose it through Tailscale Funnel.

`SIGINT` and `SIGTERM` stop `serve` and exit 0. Each open `orrery.v1.Events` stream ends first; the [Connect API](#connect-api) section gives the code it ends with.

Example. Every name and address is synthetic:

```
orrery serve --listen 127.0.0.1:8080 --allow-login user@example.test
```

### orreryd

```
orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]... [--push-host HOST]... [--sessions] [--session-cap N] [--session-shell PATH] [--holder-scope PATH] [--step-cap N]
```

- `-L`, `-S`, and `--socket` work as they do for `orrery`.
- `--claude PATH` sets the `claude` binary the summarizer runs, and the binary each headless session runs.
- `--summary-model CONTEXT=MODEL` maps a patchbay context (section 8) to a summary model. Repeat the flag for each context. A context with no mapping uses `claude-haiku-5-5`. The done judge (see [Notice routing](#notice-routing)) uses the same map.
- Each applied hook frame sets `@orrery_context` on the frame's pane to that frame's context (section 2).
- `--push-host HOST` names a push relay host that a Web Push subscription may use. Repeat the flag for each host. The default is `ntfy.sh`. A subscription whose endpoint is on another host is refused (section 12).
- `--sessions` serves the engine session service, the step service (see [Steps](#steps)), the epic service (see [Epics](#epics)) and the Pools service (see [Pools](#pools)) on `sessions.sock` beside the daemon socket. The headless session manager runs behind it when the store opens. `sessions.sock` listens only after the store open has ended, so after the `store open` line when the store opens. Without `--sessions`, no session socket exists. With it, `--session-shell` must be an absolute path, or the command exits 2.
- `--session-cap N` is the most live headless sessions at once. The default is 4. A value below 1 exits 2.
- `--session-shell PATH` is the absolute login shell that starts each session. The default is `$SHELL`.
- `--holder-scope PATH` is the absolute path of `systemd-run`, which runs each session's holder in its own user scope. The default is none, and the holder then starts plainly. A relative path exits 2 (`cmd/orreryd/main.go:231`-`:232`, `:189`). [Headless sessions](#headless-sessions) gives the start under the flag, and [Lifecycle rules](#lifecycle-rules) gives shutdown under it.
- `--step-cap N` is the most live step sessions at once. Steps count apart from `--session-cap`. The default is 6. A value below 1 exits 2.

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

`orreryd` keeps, per pane and per session, the time of the last notice it sent. With the store open, each kept time is also stored (see [Pushed notices in the store](#pushed-notices-in-the-store)), so it outlives a restart: a seen write after the restart clears the push as it would without the restart. Without the store, kept times live in memory only. A seen write made while `orreryd` is down clears nothing. When a seen write sets a seen time at or after that time, `orreryd` sends one clear push for that pane or session and forgets the kept time. Seen writes are `orrery seen` (both forms), the tree's switch and mark-read keys, autoseen, the `Panes.MarkSeen` call of [Phone API](#phone-api), and the `Seen` call of the session socket. `orrery seen`, the tree and `Panes.MarkSeen` send `orreryd` a seen report (section 9) with the time they wrote; autoseen and the session `Seen` call run inside `orreryd`. The clear payload is defined in [Clear payload](#clear-payload).

When neither variable is set, `orreryd` exits 1.

`orreryd` also keeps the store, which mirrors each pane's tmux options (see [Store](#store)). It opens the store after its sockets listen, and logs `store open` (info, with `path`) once the store is open and migrated. When the store cannot be opened, `orreryd` logs the error and serves hooks and notifications without it. It then mints no `@orrery_id` and syncs no pane. A failure of the push store still exits 1.

`SIGINT` and `SIGTERM` stop `orreryd`. With `--sessions`, the Pools broker closes first (see [Pools](#pools)), then the session socket closes. Then every live headless session is stopped, and a session whose turn is running is recorded `interrupted`. With `--holder-scope`, each live session is detached instead, and nothing is recorded for it (see [Lifecycle rules](#lifecycle-rules)). `orreryd` exits after those notices are routed, and after each pending notice is kept or ended, as [Notice routing](#notice-routing) says at shutdown. The desk stream closes last, after the notices that post to it. See [Headless sessions](#headless-sessions) and [Desk stream](#desk-stream).

#### Notice routing

`orreryd` routes each pane notice, a transition to `waiting` or `done`, and each headless session notice (see [Session notifications](#session-notifications)). A notice stays pending until one of the rules below ends it. The rules apply in this order, and each event writes one line to the [notice log](#notice-log).

1. **Background work.** A pane's `done` whose `Stop` hook has a `background_tasks` holding a subagent or workflow goes nowhere. It is posted to neither the desk stream nor Web Push, and the log records a silence with reason `background work`. A hook whose `background_tasks` does not decode is logged, and its notice routes by the rules below.
2. **Watching.** A pane notice is held while its pane is watched. The pane is watched when either of these holds:
   - a tmux client of the pane's server shows the pane's window and had input in the last 30 seconds;
   - a read report for the pane, sent by `serve` (see [orrery serve](#orrery-serve)), arrived in the last 30 seconds.

   A held notice goes to neither sink. The first hold writes a line with reason `watching`, and orreryd checks the watch again every 5 seconds. A failed check reads as not watched. A session notice is never watched, so it is never held.

**Done judge.** A pane's `done` that rule 1 does not silence is judged, with or without `--sessions`. The first time its pane is found unwatched, the notice waits 10 seconds, sending nothing and writing no line. A change, a seen write or a newer notice in that time ends it as rule 7 says. Shutdown keeps it or ends it as rule 7 says. Each later check is made as rule 2 makes it: a watched pane holds the notice with no judge call, and an unwatched pane is judged at once. The judge is Haiku. It reads the `last_assistant_message` of the `Stop` hook's input (section 9), runs through the pane's own `ANTHROPIC_BASE_URL` (section 9) with the summary model that section 8 gives its patchbay context, and answers `needed` (true or false) with a `why`. It must answer within 15 seconds, or it is late. A judge that errors fails, and so does a notice with no or an empty `last_assistant_message`. Waiting notices (questions and permission prompts) and session notices are never judged.

- **Unneeded.** The judge answers not needed, with no error and no late answer. At once, whether or not the desk is at hand, the desk stream gets the notice as a quiet notice and Web Push gets it as a silent notice. Rule 5 applies to the phone part. Nothing escalates the notice, and it ends with that line. Its route names the sinks it reached, as for `away`, and its reason is `unneeded`, with `: <why>` when the judge gave a why. A desk notice that cannot be built is not posted, and the reason gains `; desk notice failed` as rule 6 says, but the silent phone notice is still sent.
- **Needed, failed or late.** The notice routes by rules 3 to 6, and its reason gains `needed` (with its why), `judge failed: <error>` or `judge late`, whichever applies (see the [clauses](#notice-log)).

3. **At the desk.** The desk is at hand when the desk stream is present (see [Desk stream](#desk-stream)) and the newest web input is within the last 2 minutes. That input is the newest one the page reported through its input reports (see [orrery serve](#orrery-serve)). Terminal input does not count, so a needed notice while the owner types only in a terminal and the web view is idle is away, and reaches the phone at once. The notice then goes to the desk stream only, with route `desk` and reason `at the desk`, and stays pending. If it is still unseen and unchanged 5 minutes later, it goes to the phone, with route `phone` and reason `unseen after 5 minutes`, and ends.
4. **Away.** Otherwise the notice goes to the desk stream and to the phone at once, with route `desk+phone` and reason `away`.
5. **Push subscription.** The phone gets a notice only when the push store lists at least one subscription. Without one, the phone part is dropped: the route names what remains, and the reason gains `; no push subscription`. An escalation with no subscription ends the notice with route `none` and reason `unseen after 5 minutes; no push subscription`. A failed subscription check reads as a subscription, so the push is attempted.
6. **Desk notice failure.** A desk notice that cannot be built is not posted. The notice then routes as away, and its reason gains `; desk notice failed` before any `; no push subscription`.
7. **Ending.** A newer notice for the same pane or session ends the pending one, and so does a pane's or a session's transition to a state other than `waiting` or `done`. Each ends with route `none` and reason `changed`. A seen write at or after the notice's time ends it with `none` and `seen`, and the seen write still clears any notice already sent to the phone (see [orreryd](#orreryd)). At shutdown, a pending pane notice or headless session notice is kept when the store is open and its row is stored. Otherwise it ends with `none` and `shutdown`, and is not sent (see Restart below).

**Restart.** With the store open, a pane notice or a headless session notice still pending at shutdown is kept: no line is written and nothing is sent. One whose row could not be stored, even after shutdown retries the failed row writes, ends `shutdown` instead. At start, once the store opens and before the headless sessions start, `orreryd` stores a row for each pane notice and headless session notice already pending, then restores each stored row by its phase (see [Pending notices in the store](#pending-notices-in-the-store)):

- `deciding` is decided anew.
- `held` checks the watch at once.
- `delayed` is judged when its 10 seconds run out. A notice whose delay was served is never delayed again.
- `desk` is not posted again, and escalates when its 5 minutes run out.
- A row whose `due` has passed runs at once.
- `sending` is pushed again, with route `phone` and reason `resent after restart`. It is silent if its push was silent. It is not posted to the desk stream, and its row is deleted when that publish returns. It is not pending again: a seen write or a newer notice after the restore does not end it, and a seen write at or after the resend still clears the resent push on the phone. A notice for its key recorded before the restore, or a stored row for its key with a higher `id`, ends it `changed` with no resend, as for other rows.

Before a pane notice resumes, in any phase, `orreryd` reads its pane from the pane's tmux server once. A pane that is gone, either absent from its server or on a server that is not running, ends the notice with route `none` and reason `changed`. A pane whose state differs from the notice's state ends it the same way. A pane whose seen time is at or after the notice's time ends it with `none` and `seen`. Each of these sends nothing and writes one line. Any other tmux read failure is a restore error, as below.

Apart from `sending`, a restored notice keeps its original time, so a seen write at or after that time ends it with `none` and `seen`. A newer notice for the same pane or session ends it with `none` and `changed`, whether the newer notice is recorded before or after the restore. The sessions' start can record an `interrupted` notice that does this. A row that cannot be restored writes a line with route `none` and reason `not restored: <error>`, and the row is deleted. A stop during a publish can push the notice once more, and the phone alerts again for a resent waiting or failed push. A publish that finishes after shutdown still deletes its row, so a clean stop resends nothing. For the kept push times, see [Pushed notices in the store](#pushed-notices-in-the-store).

### Headless sessions

With `--sessions`, `orreryd` runs headless Claude Code sessions. Each session is one `claude` process in stream-json mode with `-p`, `--permission-mode bypassPermissions` and `--permission-prompt-tool stdio`. It starts through the login shell `--session-shell`, in the directory its row names, with `orreryd`'s environment. The environment drops `TMUX`, `TMUX_PANE`, `ORRERY_SUMMARIZER` and `ORRERY_SESSION`, and then gains `ORRERY_SESSION` set to the session id. A session id is 128 random bits in lowercase hex.

Each session's process starts under its holder (see [Holder](#holder)). `orreryd` runs `orrery holder --session ID --state-dir DIR -- SHELL ARGS...` in the session's directory and environment (`internal/holder/launch.go:97`-`:103`). `SHELL` is `--session-shell`, `ARGS` is the argument vector that `session.Command` builds to exec `claude` (`internal/engine/session/command.go:18`-`:25`, `:74`-`:81`), and `DIR` is the directory that holds the store file, so the session's holder files are in `DIR/holders/ID` (`internal/holder/launch.go:132`-`:140`, `cmd/orreryd/main.go:520`-`:521`). The `orrery` it runs is the one beside `orreryd`, or the one on `PATH` when none is beside it (`internal/holder/launch.go:172`-`:185`).

With `--holder-scope PATH`, the program is `PATH`, run with `--user --scope --collect -p OOMPolicy=continue --unit orrery-hold-ID` ahead of the same `orrery holder` argument vector, so the holder runs in its own user scope. Without the flag, the program is `orrery` itself (`internal/holder/launch.go:123`-`:129`). `orreryd` waits up to 30 seconds (`LaunchTimeout`, `internal/holder/launch.go:17`-`:18`) for the holder to serve its socket, then attaches at offset 0 (`internal/holder/launch.go:102`-`:108`). A holder that ends first fails the start with its exit status and its stderr (`internal/holder/launch.go:183`-`:192`). So a `systemd-run` failure fails the start with its reason, and no plain holder starts in its place (`internal/holder/launch.go:46`-`:50`, `internal/engine/session/command.go:120`-`:128`).

`orreryd` reads the session's stdout from the holder's `out` frames (see [Wire protocol](#wire-protocol)). After a read that returns bytes while the stream runs, it acks two offsets. `consumed` is the stdout the read has returned. `replayFrom` is the offset that `SetReplayFrom` last allowed, and the pump allows one only once the line that sets it is stored (see [Engine sessions in the store](#engine-sessions-in-the-store)). Neither offset goes below the attached frame's, and `replayFrom` never goes above `consumed` (`internal/holder/process.go:341`-`:365`, `:108`-`:114`; `internal/enginehost/manager.go:1287`-`:1289`).

`orreryd` writes stdin only as `write` frames. Each write carries the id of its kind, which the session sets before the write, and a write returns once the holder has written its data (`internal/engine/session/session.go:277`-`:282`, `internal/holder/process.go:398`-`:429`). A repeat of an id the holder has recorded is answered as a duplicate, and nothing is written (`internal/holder/holder.go:503`-`:534`). The ids are these:

- `initialize`, fixed, for the initialize request. Re-adoption sends no initialize (`internal/engine/session/session.go:17`-`:18`, `:101`-`:104`).
- A prompt's id is its stored `write_id`, 128 random bits in hex. The prompt is stored with its event before it is sent, for `Start`, `Say` and a step's first prompt (`internal/enginehost/manager.go:348`-`:356`, `:470`-`:486`, `:1114`-`:1121`; `internal/enginehost/stepsession.go:80`-`:89`).
- A host's answer to a control request is `response:` and the request id. When that passes 256 bytes it is `response:sha256:` and the hex SHA-256 of the request id (`internal/engine/session/commands.go:130`-`:139`).
- A write the session makes for an event, a refusal of a request, or a step's denial is `event:`, the line's start offset, and the event's index in the line (`internal/engine/session/commands.go:83`-`:95`, `:121`, `:241`-`:242`).
- An interrupt and a permission-mode change take a random `orrery-` id, which is also their request id (`internal/engine/session/session.go:227`-`:237`, `:265`-`:273`; `internal/engine/session/commands.go:271`-`:281`).

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
| `interrupted` | `orreryd` shuts down during a turn without `--holder-scope`, or reconcile settles a `working` or `waiting` row at start whose session is not re-adopted. The row's `reason` says which |

Idle stop writes no state, so a session it stops keeps the state it had.

#### Session control socket

The session socket is `sessions.sock` in the same directory as the daemon socket. `orreryd` creates it only with `--sessions`, and its mode is `0600`. It serves the `orrery.engine.v1.Engine` service (`proto/orrery/engine/v1/engine.proto`), the `orrery.engine.v1.Steps` service (`proto/orrery/engine/v1/step.proto`, see [Steps](#steps)), the `orrery.engine.v1.Epics` service (`proto/orrery/engine/v1/epic.proto`, see [Epics](#epics)) and the `orrery.engine.v1.Pools` service (`proto/orrery/engine/v1/pool.proto`, see [Pools](#pools)) over Connect.

| Call | Request | Precondition |
|---|---|---|
| `Start` | `dir`, `prompt`, `model`, `id` | `dir` is an absolute path to an existing directory, `prompt` is not empty, `model` is empty or 1 to 64 bytes of letters, digits and `. _ - [ ] :` not starting with `-`, `id` is empty or 32 lowercase hex digits, and the session cap has room. An `id` that has a row returns that id and starts nothing, unless the attempt for that `id` failed after storing its row, which returns that attempt's error and starts nothing while `orreryd` runs. Returns `id` |
| `Say` | `id`, `text`, `prompt_id` | the session has a row, and `text` is not empty. A `prompt_id` that an `engine_prompt` event of the session already carries makes the call succeed and send nothing, and a `prompt_id` whose send failed returns an error carrying that failure and sends nothing, also after a restart. A `prompt_id` whose attempt failed in another way after storing the prompt returns that attempt's error and sends nothing while `orreryd` runs. A session that is not live is resumed from its row first |
| `Answer` | `id`, `request_id`, `answer` | the session is live. `answer` is one of the open question's option labels, or `allow` or `deny` for a permission request |
| `Interrupt` | `id` | the session is live |
| `Stop` | `id` | the session is live |
| `Seen` | `id` | the session has a row |

A call that does not fit its precondition does not change the session. An `Answer` that fits neither the question's options nor `allow` or `deny` leaves the request open.

`Say`, `Answer`, `Interrupt` and `Stop` refuse an `id` that names a step, before their own checks. The refusal is `failed_precondition` with the message `<id> is a step: drive it through the Steps service`. A step lookup that fails is `internal`. With no step store, the check is skipped (`internal/enginehost/service.go:113`-`:129`, `internal/enginehost/service.go:157`, `:177`, `:194`, `:210`). A step runs its own loop, so a raw session command on it would drive the step around that loop (`internal/enginehost/service.go:110`-`:112`, and see [Steps](#steps)).

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

- A live session is one with a running process. Its slot under the cap is held from its start, resume or re-adoption until its process ends. A `Start` or resume past `--session-cap` is refused before anything is recorded. A re-adopted session takes its slot without the cap check, since its process already runs (`internal/enginehost/reconcile.go:170`-`:172`).
- `Start` checks an `id`'s form, then answers with an existing row's id without any other check, unless the attempt for that `id` failed after storing its row, which returns that attempt's error while `orreryd` runs. Otherwise it checks the directory, prompt and model before it writes anything. It records the row `working`, starts the process, records the prompt, and sends the prompt. If the process cannot start, or the prompt cannot be sent, the row is recorded `failed` with the reason, and `Start` returns the id with no error unless recording that failure fails.
- `Say` to a session that is not live resumes it from its row. The process resumes the row's Claude session when the row has one, and otherwise starts a new conversation in the row's directory. The resumed process runs claude's default model, whatever `model` its `Start` named. Concurrent calls for one id start one process.
- `Say` with a `prompt_id` that an `engine_prompt` event of the session already carries returns success before it resumes or sends anything. A `prompt_id` whose send failed returns an error carrying that failure, and sends nothing, also after a restart, because the failure is stored in `engine_prompt_failed` (see [Engine sessions in the store](#engine-sessions-in-the-store)). A `prompt_id` whose attempt failed in another way after it stored the prompt returns that attempt's error, and sends nothing, while `orreryd` runs. An empty `text` is refused before that check. A row whose directory is missing or is not a directory is recorded `failed` with the reason.
- Idle stop: every 30 seconds, a session that has not been busy for 10 minutes is stopped. A session is busy while a turn runs, a request is open, or a background task runs. The stop writes nothing, so the row and its events stay as they were. The next `Say` resumes the session.
- Stop signal: stopping a session's process sends its holder a `stop` frame on a new connection. The dial, the hello and the wait for the answer share one deadline of `holderStopTimeout`, 10 seconds (`internal/holder/process.go:188`-`:193`, `internal/holder/launch.go:23`-`:25`). The holder's child gets `SIGTERM` to its process group, then `SIGKILL` if it has not exited after 5 seconds (see [Child](#child)). When that stop fails, the holder's own process is stopped instead (`internal/holder/process.go:177`-`:186`). Once the holder's `exit` frame has arrived the child has ended, and the stop sends nothing and only waits for the holder to end (`internal/holder/process.go:142`-`:166`; `internal/holder/child.go:85`-`:95`, `:16`-`:17`).
- `Stop` records `stopped` unless the process's exit has already recorded a state. A process that exits without an error records `stopped`.
- Failed on death: a process that exits with an error records `failed`. The reason is the exit error and stream error, followed by the last non-blank line of its stderr, and the owner is notified. The exit of a process stopped for idle stop or shutdown records nothing itself.
- Shutdown: when `orreryd` receives `SIGINT` or `SIGTERM`, the session socket closes first. Then each live session is stopped, and a session whose turn is running is recorded `interrupted` with the reason `orreryd stopped during the turn` (`internal/enginehost/manager.go:870`-`:906`, `:1504`-`:1528`). With `--holder-scope`, each live session is detached instead: its holder and child keep running, and nothing is recorded for it, so its row keeps the state and unfinished text its events gave it (`internal/enginehost/manager.go:886`-`:890`, `:1508`-`:1509`). The next start's [Reconcile at start](#lifecycle-rules) re-adopts that holder. A step's session and its gate follow the same scope, as [Shutdown](#step-lifecycle) gives. These writes share one 5-second limit (`internal/enginehost/manager.go:895`-`:896`), and each interrupted session notifies its owner. The notices are routed before `orreryd` exits. A session notice still pending then is not sent: with the store open and its row stored it is kept for the next start, and otherwise it ends `shutdown` (see [Notice routing](#notice-routing)).
- Reconcile at start: before the session socket serves, `orreryd` goes through each holder under the state directory's `holders` subdirectory whose session has no live session in this manager (`internal/enginehost/reconcile.go:23`, `:72`-`:103`). A holder is re-adopted when its session's row is one that a launch has tracked, meaning its `consumed` offset is stored, and that row is neither `stopped` nor `failed`. The row is an engine row, or a step row whose run [Steps](#step-lifecycle) registered with the manager before this pass (`internal/enginehost/reconcile.go:105`-`:119`; `internal/store/engine_rows.go:82`). Re-adoption attaches the holder at the row's `replay_from`, replays its stdout to the row's `consumed` offset with writes off, and registers the session live (`internal/enginehost/reconcile.go:173`-`:207`, `internal/engine/session/session.go:145`-`:158`, `internal/enginehost/manager.go:751`-`:801`). A re-adopted `working` or `waiting` row has a running turn, and a re-adopted `waiting` row with no open request goes `working` with no question. Each stored prompt whose write id the holder has not recorded is sent once, in order (`internal/enginehost/manager.go:826`-`:862`). A holder whose session lock is free has ended. It is drained: its stdout from `replay_from` to `consumed` is replayed with nothing stored, each later line is recorded as the pump records it, and a child that the pids record shows still running is killed first. Its exit is recorded only when its exit record's reason is `exited`, and the row is then settled by the drain (`internal/holder/reattach.go:32`-`:47`, `internal/enginehost/drain.go:19`-`:58`, `internal/holder/orphan.go:64`-`:93`). A live holder that cannot be attached is stopped first, then drained the same way (`internal/enginehost/reconcile.go:137`-`:138`, `:148`-`:156`). A step's holder is never drained or stopped by this pass. One that has ended or cannot be attached is left for the clear (`internal/enginehost/reconcile.go:133`-`:134`). Each holder that was not re-adopted is then cleared: a holder still serving its socket is stopped, and its directory is emptied and removed once its lock is free. That covers a `stopped` or `failed` row, a step row that no [Steps](#step-lifecycle) run registered, an untracked row, and a holder with no row (`internal/enginehost/reconcile.go:91`-`:101`, `internal/holder/clear.go:51`-`:65`). Re-adoption, the stop of an unattachable holder and each clear are bounded by 10 seconds, and a failure is logged while the pass goes on (`internal/enginehost/reconcile.go:127`, `:149`, `:235`). Then each engine row with no live session loses its unfinished text. A row that is `working` or `waiting` is recorded `interrupted` with the reason `orreryd restarted during the turn`, its question is cleared, and its owner is notified. A drained row is settled by its drain. A step row is settled the same way, with no notice (`internal/enginehost/reconcile.go:282`). Other rows keep their state, and no row is removed. The first read or write error ends the pass. `orreryd` logs it and serves anyway (`internal/enginehost/reconcile.go:39`-`:64`, `:269`-`:286`; `cmd/orreryd/main.go:583`-`:585`).
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

`StartStep` checks each field as follows:

- `agent` is 1 to 64 bytes of letters, digits and `. _ - [ ] :`, not starting with `-`. Empty is refused, unless the start names an agent gate, whose agent the gate supplies (see `gate_name`). The agent must also resolve, as claude resolves `--agent` for the step. The check runs before claude starts, and a name that no place below defines is refused with an error that names the agent and each place checked (`internal/agents`, `Resolve` and `ErrUnresolved`). A named agent gate's agent gets the same check (`internal/enginehost/stepsession.go:105`-`:106`, `:119`).

  The built-in agents are `claude`, `Explore`, `general-purpose`, `Plan` and `statusline-setup`. `web-fetch` never resolves. The managed agents directory is `/etc/claude-code/.claude/agents`. The project agents are the `.claude/agents` directories of the step's directory and of each directory above it, up to its git work-tree root and never above `HOME`. The user config directory is `$CLAUDE_CONFIG_DIR` when that is set, else `$HOME/.claude`, read from the step's environment, and the user agents directory is `agents` in it. Each agents directory is scanned for `.md` files recursively, following symbolic links to directories and to files. A missing directory is skipped. Any other read error refuses the start, and the error is named. A user, project or managed file defines its agent by its frontmatter `name` and needs a `description` too. A name that contains `:` or starts with `-` defines nothing. Names match exactly, case included.

  The last place is the agents of enabled plugins. A plugin counts when `<config>/plugins/installed_plugins.json` lists it with scope user or managed; an install at project or local scope does not count. It is enabled when `enabledPlugins` is `true` in the highest-precedence settings file that names it, in this order: managed `/etc/claude-code/managed-settings.json`, project-local `.claude/settings.local.json` (at the main checkout's root for a linked worktree), project `.claude/settings.json`, and `settings.json` in the user config directory. A plugin's agent is named `<plugin>:<subfolders>:<name>`, from its frontmatter `name`, or from its filename when the file has no parsable `name`. A bare name matches a scoped name that ends in `:<name>`.
- `prompt` is not empty.
- `schema` is a JSON object schema. Its root `type` is `object`, its `properties` include `status` with type `string`, and every subschema uses only the keywords `type`, `enum`, `required`, `properties`, `items` and `additionalProperties`, or the annotations `title`, `description`, `$schema`, `default` and `examples`. A start that names an agent gate leaves `schema` empty and takes the gate's verdict schema, which this check also covers (`internal/enginehost/step.go:248`).
- `workspace` is an absolute path to an existing directory.
- `revision` is resolved to a commit when the step starts. Empty means `HEAD`.
- `read_only` false runs a writing step in `workspace`. The directory must be inside a linked git worktree. A directory outside any git work tree is refused, and so is one inside a repository's main working tree.
- `read_only` true runs the step in a detached worktree at its start commit. The worktree is added under `step-worktrees` beside the store file (see [Store](#store)), named by the step id, and removed when the step ends.
- `owned_paths` are paths relative to the repository. An empty path, an absolute path, or one that leaves the repository is refused.
- `gate_name` names the gate the step's return is checked against. Empty runs no gate. Otherwise the name is resolved once, when the step starts, as [Gate files](#gate-files) gives: from `gates_file` first when it is set, then from the project file for `workspace`. A name that neither file defines is refused. The step takes the gate's argv, limit and needs as its definition gives them then (`internal/enginehost/step.go:196`-`:199`, `:388`-`:427`).
- A `gate_name` that names an agent gate (see [Agent gates](#agent-gates)) runs no argv. The start must be read-only and must leave `agent`, `schema` and `done_value` empty, or it is refused with `invalid_argument`. The step then takes the gate's agent, the verdict schema, the done value `done`, and as its first prompt the gate's prompt built from the caller's `prompt` (`internal/enginehost/step.go:469`-`:487`). The gate's agent is checked as `agent` is above. The row stores the gate's name, source and limit, and no argv and no needs. A read-only step's done return runs no commit checks and no gate, so no argv runs (`internal/enginehost/steploop.go:431`).
- `gates_file` is the absolute path of the epic's gates file. Empty reads only the project file. It is refused when it is not absolute, when `gate_name` is empty, and when the file is missing or does not parse (`internal/enginehost/step.go:389`-`:402`).
- Each pool a gate needs must be declared by a pool file for `workspace`, or the start is refused (`internal/enginehost/step.go:408`-`:418`).
- `gate` and `gate_limit_seconds`, fields 8 and 9 of the request, are refused with `invalid_argument`. A step names its gate by `gate_name` (`internal/enginehost/stepservice.go:52`-`:55`).
- `done_value` empty means `done`. A start that names an agent gate leaves it empty, and the gate's done value is `done`.
- `model_limit_seconds` of zero means 60 minutes. It is the model time the step may use before the watch ends it `overtime` (see the Watch rule under [Step lifecycle](#step-lifecycle)).
- `epic` and `task` are stored as given, and nothing checks them. The format that `proto/orrery/engine/v1/step.proto:69`-`:70` names for `epic` is not enforced (`internal/enginehost/stepservice.go:68`-`:69`, `internal/enginehost/step.go:200`, `internal/store/steps.go:140`). An empty `epic` leaves the step unlabelled, and `ListSteps` never lists it (`internal/enginehost/step.go:48`, `internal/store/queries.sql:182`). An empty `task` means the step works on no one task.

A start refused by one of these checks, by the step cap, or by the workspace rules stores nothing and returns no id. A start that fails after its row is stored returns the error with no id, and the row ends `failed`. The id is not reported on that path.

`StepStatus`, `WaitSteps` and `ListSteps` return each step as a `Step` message: `id`, `agent`, `workspace`, `revision` (the resolved start commit), `read_only`, `worktree` (empty for a writing step), `end` (empty while the step runs), `status`, `result` (the latest return's JSON text), `checks`, `reason`, `started`, `ended` (unix seconds; `ended` is 0 while the step runs), `doing`, the latest doing line from the step's monitor, `epic` and `task`, the labels the step was started with, and `held`, which is true while an owner hold holds the step (`internal/enginehost/stepservice.go:186`-`:219`). Each `checks` entry is a `StepCheck`: `round`, `name`, `ok` and `detail`. The row's prompt, schema, done value, owned paths, gate, gate name, gate source, gate needs, gate limit, model limit and verdicts are stored (see [Steps in the store](#steps-in-the-store)) but not returned. `held` is loop state and has no column: it is read from the step's loop, and a step with no loop is not held (`internal/enginehost/stepowner.go:133`-`:139`).

`WaitSteps` returns every named row in the order `ids` gives. `ended` is the named step that ended first, by its end time, and a tie keeps the step listed first. `ended` is unset when the timeout passes before any of them ends. A timeout of zero returns at once. An id with no row fails the whole call with `not_found`.

`ListSteps` returns the rows whose `epic` is not empty, narrowed to rows whose `epic` is the request's `epic` when that is not empty, and to rows whose `task` is the request's `task` when that is not empty. They are ordered by start time, then id (`internal/store/queries.sql:169`-`:173`, `internal/store/steps.go:230`-`:238`).

`HoldStep` and `NudgeStep` return no fields. Their rules are in [Step lifecycle](#step-lifecycle).

The step tools of `orrery mcp` call these calls and retry them (`internal/stepmcp/tools.go`). `step_start` retries `StartStep` only when no connection was made, for up to 60 seconds, so a start cut mid-call is never sent twice (`internal/stepmcp/tools.go:178`-`:180`). `step_status` and `step_stop` retry an `Unavailable` answer for up to 60 seconds (`internal/stepmcp/tools.go:213`, `:234`). `step_wait` calls `WaitSteps` again after an `Unavailable` answer for as long as its own timeout has left, and asks each time for the remaining seconds, rounded up (`internal/stepmcp/tools.go:270`-`:298`). The waits between attempts grow from 100 ms to 5 s (`internal/rpcretry/retry.go:20`-`:24`, `:64`-`:88`).

Errors are Connect codes:

| Connect code | When |
|---|---|
| `unavailable` | `orreryd` has no step store. The message is `step store unavailable` |
| `resource_exhausted` | a `StartStep` would pass the step cap (`--step-cap`) |
| `not_found` | `StopStep`, `StepStatus`, `WaitSteps`, `HoldStep` or `NudgeStep` names an id with no row |
| `invalid_argument` | an empty `prompt`, a bad `schema`, a bad `agent`, an `agent` that does not resolve (the error names the agent and each place checked), a `workspace` that is not an absolute existing directory, a writing step's `workspace` outside a git work tree or in its main working tree, an `owned_paths` entry that is not a relative path inside the repository, a `gate` or `gate_limit_seconds` field, a `gate_name` that no file defines, a `gate_name` that names an agent gate on a writing step or with an `agent`, `schema` or `done_value` set, a `gates_file` that is not absolute, is set without `gate_name`, is missing or does not parse, a gate need for a pool that no pool file declares, or an empty `text` on `NudgeStep` (`internal/enginehost/stepservice.go:234`-`:237`) |
| `failed_precondition` | `HoldStep` finds the step already held or no turn running, or `HoldStep` or `NudgeStep` finds the step running its gate, ended, or with no loop, or `NudgeStep` finds no turn running and the step not held (`internal/enginehost/stepservice.go:231`-`:233`) |
| `internal` | any other failure, including a `revision` that does not resolve (for a read-only step this includes a `workspace` outside any git work tree), a `WaitSteps` call whose context ends first, and a `HoldStep` or `NudgeStep` call whose context ends before the step's loop takes it |

#### Step lifecycle

- **Launch.** A step's process starts through `--session-shell` as a session does (see [Headless sessions](#headless-sessions)). Its arguments are these, with `<agent>` and `<schema>` from the request:

  ```
  <session-shell> -l -c 'exec "$@"' orrery-session <claude> -p --input-format stream-json --output-format stream-json --include-partial-messages --verbose --permission-prompt-tool stdio --permission-mode bypassPermissions --agent <agent> --strict-mcp-config --json-schema <schema>
  ```

  No `--model` is passed, so the agent's model runs. A respawn adds `--resume` with the row's claude session id. The working directory is `workspace`, or the step's worktree for a read-only step. The environment is the session environment, with `ORRERY_SESSION` set to the step id.

  When claude emits its init event, its `agents` list must hold the step's agent, either as that name or as the part after `:` of a scoped entry. A `web-fetch` agent always fails this check, and so does a missing list. A step that fails it stops and ends `failed`, with a reason that names the agent. The check catches the claude settings that the resolution check does not read, and drift at a claude upgrade.
- **Kind and silence.** The row's `kind` is `step`. The typed `List` and the tidy read only pane rows, so step rows are never listed. A step sends no notice in any state, on the desk stream or Web Push. Its `engine_*` events are stored and pruned like other engine events, and `orrery.v1.Events.Watch` sends them as their engine members, as it does for engine sessions. A step takes a slot under `--step-cap`, not `--session-cap`.
- **Requests.** A question or permission request that the session did not refuse is denied at once with `The orrery host does not answer questions in a step. Do not ask; give your answer as the structured return.` The request never opens, so the row does not become `waiting`.
- **Returns.** A return is the `structured_output` of a turn's result. It is taken only from a turn that ends with no background task live, so a turn that ends while a background task runs is not a return. After a return the step is busy until `Continue` sends its next prompt or the step stops. A result with no structured output has no value, and fails the schema check.
- **Checks.** Each return is a round, numbered from 1, and its checks are stored with that round in the row's `checks`. The checks run in this order:
  1. `schema`: the value matches the step's schema. The failure names the first failing path, such as `$.status`.
  2. For a writing step whose return passes `schema` and whose `status` equals `done_value`, four commit checks run, in this order: `head` (the return's commit is the top-level `commit` string when the return names one, and HEAD must be that commit; when the return names none (missing, null, empty, or non-string), the return's commit is the workspace's HEAD if HEAD has moved past the start commit, and a return naming none while HEAD is the start commit fails `head`), `ancestry` (the start commit is an ancestor of HEAD), `clean` (no tracked change, staged or unstaged, and no untracked file that is not ignored), and `owned` (every path changed from the start commit to the return's commit is in `owned_paths` by exact match, with both sides of a rename checked).
  3. `gate`: runs only when all four commit checks pass and the step has a gate.
  4. `assertions`: runs only for an agent gate step, on every return that passes `schema`, whatever its `status`. The return's `results` must hold one entry per assertion of the gate, in the gate's order, each with the assertion's text as `assertion` and its kind as `kind`, and each `fails` result must have a finding whose `citation` is that text. The failure details are `results has N entries for M assertions`, `result I is for assertion "R", want "A"`, `result I of assertion "A" has kind "K", want "J"` and `result I fails assertion "A" and no finding cites it`. Here N is the number of results, M the number of assertions, I the result's place counted from 1, R the result's assertion text, A the assertion's text, K the result's kind and J the assertion's kind. A value that does not decode as a verdict fails with `read verdict: ` and the decoder's error (`internal/enginehost/stepverdict.go:43`-`:57`, `:88`-`:110`, `internal/enginehost/steploop.go:439`-`:443`).

  A read-only step, a return that fails `schema`, or a return whose `status` is not `done_value`, has the `schema` check alone, except that an agent gate step's return that passes `schema` also has `assertions`.
- **Gate.** A writing step's gate runs only on a done return whose commit checks all pass. It runs in the step's workspace, with its argv and no shell, and it holds the pool members its needs name. It queues through `orreryd`'s Pools service, runs once its grant holds those members, and keeps the grant until its process group has exited, or until its holder ends when a shutdown detaches the gate (see [Shutdown](#step-lifecycle)). Its limit counts from the grant, not from the queue. It runs with `orreryd`'s environment, minus `TMUX` and `TMUX_PANE`. Each attempt runs under its own holder: `orrery holder --gate-limit` with the session `<step id>-gate`, in the holder directory `holders/<session>` under the gate state directory, which `orreryd` sets to `gates` under the state directory (`cmd/orreryd/main.go:540`; `internal/enginehost/steploop.go:524`-`:537`, `internal/enginehost/gate.go:126`-`:146`, `:650`-`:659`). The holder's child is a gate (see [Holder](#holder)). The attempt's request names that directory, so the grant lasts with the holder as well as with `orreryd` (`internal/enginehost/gate.go:222`-`:246`; see [Pools](#pools)). Before the gate launches, the row stores the grant, the holder directory and the round in `gate_grant`, `gate_holder` and `gate_round`. The return that judges the gate clears them (`internal/enginehost/steploop.go:534`; `internal/store/queries.sql:197`-`:201`; see [Steps in the store](#steps-in-the-store)). A hold placed on one of its members (`orrery hold`) stops the gate. Its process group is killed, that run records nothing, and the grant is requeued. The gate runs again from its start once the broker grants it again. A requeued request waits like any other, and a held member is not granted until it is freed (`internal/enginehost/gate.go:100`-`:114`, `:511`-`:547`; `internal/grants/broker.go:579`-`:597`). The gate passes when it exits 0 without passing its limit. When the limit passes, its holder stops it as a stop does: `SIGTERM` to its process group, then `SIGKILL` once the grace has passed (see [Holder](#holder); `internal/holder/child.go:156`-`:172`, `:112`-`:121`), and the check fails with `gate ran past its limit of <limit> and was killed`. The check's detail opens with the record `gate <name> from <source> ran <argv as a JSON array> holding <POOL:MEMBER,...>; `. It then gives `gate exited <status>` or the limit text, and when the gate printed any output, `; output tail: ` and its last 8 KiB (`internal/enginehost/steploop.go:541`-`:551`, `:575`-`:586`). A gate that cannot start, or whose grant ends before or while it runs, is an error that ends the step `failed` (`internal/enginehost/gate.go:222`-`:246`, `:527`-`:531`, `:700`-`:713`).
  - **Re-adoption.** A restart re-adopts a gate that the row stores as running in its round (`internal/enginehost/steploop.go:524`-`:537`). If the gate's holder still serves, `orreryd` reclaims the grant by its id for a request that names that holder, and waits for the gate's end as an attempt does. Otherwise the holder's exit record is the outcome. An exit or a limit is the result, and nothing reruns. The broker may still keep the gate's grant for its ended holder until its next tick, so `orreryd` reclaims that grant by its id for a request naming the holder and ends it at once: the gate's members are free when the step's end is stored. A stop reruns the gate once on the grant the stop ended. A holder that cannot be attached but has written its record is read the same way. Any other outcome is lost: a reclaim that the broker refuses, a holder that ended with no record, or a record whose reason is neither an exit, a limit nor a stop. A lost gate ends its step `engine_restarted`, and its holder is ended and cleared (`internal/enginehost/gate.go:331`-`:358`, `:363`-`:390`, `:395`-`:450`, `:454`-`:467`; `internal/enginehost/steploop.go:511`-`:513`).
- **Judge.** A return that passes its checks and whose `status` equals `done_value` is judged by a Haiku call, except an agent gate step's return, which is never judged. Without a Haiku caller the step ends `done` with no call. The input is the step's first prompt and the return. A writing step adds the `git diff --stat` from the start commit to the return's commit, and a step with a gate adds the gate check's detail. The reply is `met` or `unmet` with a reason. `met` ends the step `done`. The first `unmet` sends the session back once with the reason, and the next return that passes its checks is judged again. A second `unmet` ends the step `goal_unmet`, and its return is kept. A failed diff stat, call or reply stores its error with no verdict and ends the step `done`. The judge's send-back is not one of the three send-backs.

  An agent gate step's return that passes its checks ends by its verdict, with no call. Its reasons are, in order: each result that is not `holds`, as `fails: A` or `undetermined: A` with A the assertion's text; then `status S` when the return's `status` S is not `done_value`; then `an earlier return held fails: A` when the return holds no `fails` result and an earlier return did. The reasons are joined with `; `. The step ends `done` with no reason, and `gate_failed` with them otherwise, and its return is kept. The first `fails` result of any schema-valid agent gate return is kept for the run, whether or not that return's check passed. A turn end an agent gate step takes as no round (held, owner nudge or watchdog nudge) also keeps the first `fails` result of its output when that output passes `schema`, though the output is not stored or checked, so no later return ends `done` (`internal/enginehost/stepverdict.go:43`-`:57`, `:60`-`:84`, `:115`-`:136`, `:140`-`:147`, `internal/enginehost/steploop.go:315`, `:326`, `:330`, `:458`-`:461`).
- **Watch.** An agent gate step also checks its gate's `limit` on each unheld tick, before the backstops and whether or not a turn runs. At the first tick at or past `limit` seconds of wall time from the run's start, the step ends `overtime` with the reason `agent gate ran past its limit of N seconds`, where N is the limit in seconds. No monitor call is made and no verdict is stored (`internal/enginehost/stepwatchloop.go:20`-`:27`, `internal/enginehost/steploop.go:173`, `internal/enginehost/steploop.go:230`-`:235`). A held tick is skipped before this check, as it is before the backstops. While a turn runs, the loop checks four backstops on each tick, the first one due in this order: `overtime` (model time reaches the step's model limit), `tool_stuck` (a main-conversation tool call open for 30 minutes of wall time, or 60 after its one extension), `silent` (10 minutes of model time with no event) and `periodic` (5 minutes of turn time since the turn began or the last verdict). Each trip makes one Haiku monitor call. Its verdict is `progressing`, `long_command`, `looping` or `wedged`, with a one-line `doing`, and a non-empty `doing` becomes the row's `doing`. The verdict is applied in this order: `overtime` ends the step `overtime`; `looping` or `wedged` nudges the step once, and then ends it with that verdict; a `long_command` on `tool_stuck` extends that call once, and any other verdict on `tool_stuck` ends the step `tool_stuck`; `silent` nudges once, and then ends the step `silent`; `periodic` does nothing. A nudge interrupts the running turn. The interrupted turn is no round: the step is continued with the nudge text instead of being checked, and an agent gate step keeps the first `fails` result of its output when that output passes `schema`. A nudge does not count toward the three send-backs. A failed monitor call stores its error with no verdict, and the trip acts without one: `overtime` and `tool_stuck` end the step, `silent` nudges on its first trip and ends on the next, and `periodic` does nothing. The monitor's argv, its cap and timeout, and the trip policy with its file and line citations are in [section 15 of the engine protocol](engine-protocol.md#15-the-step-watch-and-judge).
- **Send-back.** A return with a failed check is sent back by `Continue`, as a message that lists each failed check with its detail. The step may be sent back three times. A fourth failing return ends the step `checks_failed`. Nudges and the judge's send-back are counted apart, and they are not among these three.
- **Ends.** Each step ends once, with one of these end kinds:

  | End | Set when | `status` |
  |---|---|---|
  | `done` | a return passes its checks, its `status` equals `done_value`, and the judge says `met`, fails to give a verdict, or is not run; or, for an agent gate step, a return passes its checks with no verdict reason (see Checks) | `done_value` |
  | `returned` | a return passes its checks and its `status` is another value, except an agent gate step's, which ends `gate_failed` | that value |
  | `checks_failed` | a fourth return fails a check | empty |
  | `failed` | the process exits on its own (`the step's process exited` after a clean exit, otherwise the exit's reason), the first prompt cannot be sent, a return or verdict cannot be recorded, a gate cannot run or its grant ends before or while it runs, a `Continue` cannot be sent, a respawn whose agent no longer resolves, or a started step whose init agents list lacks its agent (see Launch and Restart) | empty |
  | `stopped` | `StopStep` ends the step | empty |
  | `engine_restarted` | `orreryd` starts with the step's row open and no loop for it, and the step is not re-adopted (see [Restart](#step-lifecycle)); or a re-adopted gate's outcome is lost (see [Gate](#step-lifecycle)); or the step runs an agent gate, which a restart never re-adopts (see [Restart](#step-lifecycle)) | empty |
  | `overtime` | the watch's `overtime` trip: model time reaches the step's model limit; or, for an agent gate step, wall time from the run's start reaches its gate's `limit` (see Watch) | empty |
  | `tool_stuck` | the watch's `tool_stuck` trip, when its verdict is neither `long_command` nor `looping` or `wedged`, or its call has had its extension | empty |
  | `silent` | the watch's `silent` trip with a verdict other than `looping` or `wedged`, once the step has been nudged | empty |
  | `looping` | a `looping` verdict, once the step has been nudged | empty |
  | `wedged` | a `wedged` verdict, once the step has been nudged | empty |
  | `goal_unmet` | the judge's second `unmet` verdict; the return is kept | empty |
  | `gate_failed` | an agent gate step's return passes its checks and a verdict reason applies (see Judge): a result is `fails` or `undetermined`, the `status` is not `done_value`, or an earlier return held a `fails` result; the return is kept | the return's `status` |

  The end is stored with the reason, and the step's session is stopped and its worktree removed, whatever ended it.
- **Stop.** `StopStep` cancels the step's loop, which kills a running gate, waits for the loop to return, and then stores `stopped` through the same end path. A step that has already ended is left as it is, and `StopStep` returns success. A held step ends `stopped` the same way (`internal/enginehost/stepctl.go:26`-`:46`). Under any holder scope the cancel kills the gate: its holder is stopped, its process group is killed and its directory is cleared (`internal/enginehost/gate.go:511`-`:547`, `:551`-`:555`).
- **Hold.** `HoldStep` interrupts the step's running turn and marks the step held (`internal/enginehost/stepowner.go:57`-`:72`, `:120`-`:131`). It is refused with `failed_precondition` when the step is already held, when no turn is running, and when the step is running its gate (`internal/enginehost/stepowner.go:58`-`:62`, `:36`-`:39`). A failed interrupt is returned as `internal`, and the step stays unheld. A landed hold drops any nudge pending for the turn, the watchdog's and the owner's (`internal/enginehost/stepowner.go:67`-`:68`). A held step's loop skips each tick, so the watch checks nothing (`internal/enginehost/steploop.go:229`-`:232`). A held step's turn end is no round, and it continues nothing, though an agent gate step keeps the first `fails` result of its output when that output passes `schema` (`internal/enginehost/steploop.go:314`-`:318`).
- **Owner nudge.** `NudgeStep` sends the owner's text to the step (`internal/enginehost/stepowner.go:141`-`:162`). Empty text is `invalid_argument`, and it is refused before the step is looked up (`internal/enginehost/stepowner.go:148`-`:150`). It is refused with `failed_precondition` when the step is running its gate, and when the step is not held and no turn is running (`internal/enginehost/stepowner.go:92`-`:94`, `:36`-`:39`). On a held step, the text resumes the step at once when the held turn has already ended. Otherwise it waits for that turn's end, which is no round, though an agent gate step keeps the first `fails` result of its output when that output passes `schema`, and is continued then (`internal/enginehost/stepowner.go:81`-`:91`, `internal/enginehost/steploop.go:314`-`:323`). On a running turn, the turn is interrupted, and its end is continued with any pending watchdog text followed by the owner's text, in place of a round, though an agent gate step keeps the first `fails` result of its output when that output passes `schema` (`internal/enginehost/stepowner.go:95`-`:105`, `internal/enginehost/steploop.go:325`-`:327`). Owner text sent while a nudge is pending is joined to it, one line per text (`internal/enginehost/stepowner.go:95`-`:99`, `internal/enginehost/steploop.go:399`-`:406`). A nudge neither spends nor replaces the watchdog's nudge, and an OK reply means the nudge landed (`internal/enginehost/stepowner.go:141`-`:146`).
- **Shutdown.** `Steps.Close` cancels each loop and waits until none runs. Without a gate scope, the cancel kills a running gate, as Stop does. With one, which `orreryd` sets from `--holder-scope` (`cmd/orreryd/main.go:541`), each loop is cancelled with `errGateDetach`. Its gate keeps running under its holder, and the step's row stays open for the next start's Restart. The grant stays with the gate, and the broker keeps it across a restart while its holder lives (see [Pools](#pools)) (`internal/enginehost/stepctl.go:253`-`:269`, `internal/enginehost/gate.go:66`-`:67`, `:533`-`:535`).
- **Restart.** At start, `orreryd` calls `Steps.Readopt` before the manager's Reconcile, and `Steps.Reconcile` after it (`cmd/orreryd/main.go:579`-`:589`). `Readopt` reads each open step row, and the turn end its session last stored after its last prompt, before any holder is re-attached. It rebuilds each row's run as `Start` builds one, restored from the row: the round, the continue count, the pending nudges, whether the step is held, whether the judge has sent it back, and whether its one nudge is spent. It restores the gate that the row stores as running, with its grant, holder and round. Each run is registered with the manager as the step's observer, so the manager re-adopts the step's holder when that holder is live and tracked. A row whose run cannot be rebuilt is logged and left unregistered (`internal/enginehost/stepctl.go:123`-`:143`, `:149`-`:196`). A step that runs an agent gate is never rebuilt: its gate's definition and the first `fails` it keeps live only in its run, so its row is left unregistered and ends `engine_restarted` (`internal/enginehost/stepctl.go:150`-`:152`, `:186`-`:196`). The manager settles each row it does not re-adopt as it settles other engine rows, with no notice for a step (see [Reconcile at start](#lifecycle-rules)). Then `Steps.Reconcile` starts the loop of each registered step that the manager reports live. Each other open row with no loop ends `engine_restarted`, its worktree is removed, and its gate holder is ended and cleared (`internal/enginehost/stepctl.go:202`-`:251`). A re-adopted loop resumes from what it stored. A stored turn end that the latest return covers resumes that return: failed checks are sent back again, a return with another status ends `returned`, and a done return is judged again. Any other stored turn end is handled as one the session has just given. With no stored turn end, the watch counts the turn as running (`internal/enginehost/steploop.go:250`-`:300`, `internal/enginehost/stepwatch.go:331`-`:340`). The loop stores its round, continue count, pending nudges, held flag, judge flag and nudged flag as each changes (`internal/enginehost/steploop.go:358`-`:371`). `Continue`, which the loop sends, respawns a step that is not live from its rows: it resumes the row's claude session with the same agent and schema flags, and it takes a step slot first. A respawn runs the `agent` rule of `StartStep` before claude starts. An agent that no longer resolves ends the step `failed` before claude starts, with a reason that names the agent and each place checked, and no claude process starts (`internal/enginehost/manager.go:504`-`:531`).


#### Epics

`orreryd` serves the `orrery.engine.v1.Epics` service (`proto/orrery/engine/v1/epic.proto`) on the session socket beside `Steps`, when it runs with `--sessions` (`internal/enginehost/service.go:64`-`:69`; `cmd/orreryd/main.go:415`-`:417`). Its epics are stored in orreryd's store (see [Epics in the store](#epics-in-the-store)). With no store open, the service has no store behind it, and every call answers `unavailable` (`cmd/orreryd/main.go:507`-`:508`, `:551`; `internal/enginehost/epicservice.go:34`-`:40`). `serve` calls its `ListEpics` and `GetEpic` to merge the epics with the records, and no other epic call (see [Epics and Steps](#epics-and-steps); `internal/server/server.go:118`-`:129`).

| Call | Request | Response | Effect |
|---|---|---|---|
| `CreateEpic` | `contract`, `decisions` | `epic` | checks the contract, stores it accepted with its seed decisions, and returns the stored epic |
| `GetEpic` | `id` | `epic` | returns the stored epic with that id |
| `ListEpics` | none | `epics` | returns a summary of each stored epic, ordered by `id` |
| `AnswerEpic` | `epic_id`, `id`, `text` | `decision` | records the answer as a decision on the epic |

The calls are declared in `proto/orrery/engine/v1/epic.proto:6`-`:18`, and implemented in `internal/enginehost/epicservice.go:42`-`:124`.

**Contract.** `CreateEpic` checks the contract before it stores anything. The checks run in the order below, and the first one that fails is the refusal (`internal/enginehost/epiccheck.go:45`-`:57`; `internal/enginehost/epicservice.go:51`-`:53`). A refusal is `invalid_argument` with the message `<field>: <reason>`. The field is named by its proto name, and a row of a section by its zero-based index, as in `release_steps[0].undo: empty` (`internal/enginehost/epiccheck.go:36`-`:41`, `:225`-`:227`). The checks, in order:

1. `contract: missing`, when the request has no contract (`epiccheck.go:46`-`:49`).
2. `id: bad form, want <repo-id>-<7 hex>/<slug>`, when the id does not match `^[A-Za-z0-9][A-Za-z0-9._-]*-[0-9a-f]{7}/[a-z0-9]+(-[a-z0-9]+)*$`. `id: over 128 bytes`, when it matches and is longer than 128 bytes (`epiccheck.go:13`-`:14`, `:29`, `:61`-`:67`).
3. `repository: not absolute`, when the path is not absolute. `repository: not inside a git work tree`, when `git rev-parse --is-inside-work-tree`, run in that directory, fails or does not print `true`. Any failure of the git call is this refusal (`epiccheck.go:80`-`:91`; `internal/enginehost/stepgit.go:31`-`:44`).
4. `intent: empty`, when the intent is empty after trimming white space (`epiccheck.go:71`-`:73`, `:230`-`:232`).
5. `care: not limited, serious or severe`, unless the care is `limited`, `serious` or `severe` (`epiccheck.go:74`-`:76`, `:235`-`:237`).
6. For each failure in order: `failures[i].id: unknown id`, unless the id is `F` followed by a number with no leading zero, and `failures[i].id: duplicate` when an earlier failure has the id. Then `failures[i].rating: not limited, serious or severe`, unless the rating is one of the three levels (`epiccheck.go:30`, `:111`-`:121`, `:211`-`:222`).
7. `requirements: none`, when the contract has no requirements. For each requirement in order: `requirements[i].id` is refused as `failures[i].id` is, with `R` in place of `F` (`epiccheck.go:31`, `:127`-`:134`, `:213`-`:222`). `requirements[i].evidence: empty`, when the evidence is empty after trimming. `requirements[i].kind: not check or review`, unless the kind is `check` or `review` (`epiccheck.go:137`-`:142`).
8. For each premise in order: `premises[i].id` is refused as a failure's id is, with `P` in place of `F` (`epiccheck.go:32`, `:148`-`:157`).
9. For each addition in order: `additions[i].location: unknown id`, when the location is an `F`, `R` or `P` id (`^[FPR][1-9][0-9]*$`) that names no failure, requirement or premise of the contract (`epiccheck.go:161`-`:179`).
10. For each release step in order: `release_steps[i].undo: empty`, when it is empty after trimming (`epiccheck.go:182`-`:189`).
11. For each seed decision in order: `decisions[i].id: empty`, or `decisions[i].id: duplicate` when an earlier decision has the id. Then `decisions[i].text: empty`, when the text is the empty string. The text is not trimmed (`epiccheck.go:193`-`:209`).

A contract that passes is stored in one write, with its sections and seed decisions (`internal/store/epics.go:102`-`:125`). Its `phase` is `accepted`, and its `created` and `changed` are both the time of the call. The response is the stored epic read back (`internal/enginehost/epicservice.go:54`-`:65`, `:163`-`:165`). An `id` that is already stored answers `already_exists`, and nothing is stored (`epicservice.go:55`-`:58`; `internal/store/epics.go:110`-`:112`).

**Phase and changed.** The create is the only writer of `phase`, and it sets `accepted`. The store has no query that updates the phase. `changed` is the time of the epic's last write: its creation, or its latest answer (`internal/store/queries.sql:294`-`:308`; `internal/store/epics.go:325`-`:329`).

**GetEpic and ListEpics.** `GetEpic` answers an unknown id with `not_found` (`epicservice.go:75`-`:78`). `ListEpics` answers each stored epic as an `EpicSummary`: `id`, `repository`, `title`, `phase`, `created` and `changed`, with no sections. The list is ordered by `id` (`epicservice.go:85`-`:97`, `:243`-`:253`; `internal/store/queries.sql:304`-`:305`).

**AnswerEpic.** Before the store is asked, `AnswerEpic` refuses an empty `epic_id`, `id` or `text`, with `invalid_argument`, naming the first one that is empty. The text is empty after trimming white space (`epicservice.go:126`-`:138`). The store then records the answer in one write (`internal/store/epics.go:292`-`:337`):

- An `epic_id` that names no epic answers `not_found` (`epicservice.go:113`-`:114`; `internal/store/epics.go:295`-`:301`).
- An `id` already recorded on the epic with other text answers `already_exists`. Text is compared exactly (`epicservice.go:115`-`:119`; `internal/store/epics.go:305`-`:308`).
- An `id` already recorded with equal text returns the stored decision, and writes nothing (`internal/store/epics.go:305`-`:310`).
- Otherwise the decision is appended with the next `seq`, counted from 1, dated at the call, and the epic's `changed` moves to that time (`internal/store/epics.go:315`-`:329`).

No call updates or deletes a decision. The store has no query that does, and the table's triggers abort any update or delete (`internal/store/queries.sql:352`-`:362`; `internal/store/migrations/00014_epics.sql:91`-`:103`).

**Refusals.** The Connect codes and messages:

| Connect code | When | Message |
|---|---|---|
| `unavailable` | any call with no store open | `epic store unavailable` |
| `invalid_argument` | `CreateEpic`, for each failed check above | `<field>: <reason>`, as the check gives it |
| `already_exists` | `CreateEpic` with an `id` already stored | `id: <id> is already in the database` |
| `invalid_argument` | `AnswerEpic` with an empty `epic_id`, `id` or `text` | `epic_id: empty`, `id: empty` or `text: empty` |
| `not_found` | `GetEpic` with an unknown `id` | `id: no epic <id>` |
| `not_found` | `AnswerEpic` with an unknown `epic_id` | `epic_id: no epic <id>` |
| `already_exists` | `AnswerEpic` with an `id` recorded with other text | `id: <id> was recorded with other text` |
| `internal` | a store failure | the store's error text |

(`internal/enginehost/epicservice.go:15`-`:16`, `:35`-`:40`, `:51`-`:58`, `:75`-`:78`, `:113`-`:119`, `:145`-`:148`)

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
- At start, `orreryd` restores the holds, then the stored grants, before it serves Pools. A grant whose `client_pid` has no process, and which names no holder that is alive, is ended in the store at once (`internal/grants/broker.go:248`-`:253`, `:375`-`:405`). A process is gone when a signal 0 to its pid fails with ESRCH. Each other grant keeps its members, and waits for its client or its holder to reclaim it. Its pools come back into use, so each of their members gets a status run. `orreryd` gives the broker that holder check (`cmd/orreryd/main.go:602`, `:618`-`:626`). Member commands keep these rules: the ones in flight are cancelled at shutdown, and a pool's members get a status run when the pool comes into use at start (`internal/grants/broker.go:696`-`:711`, `:815`-`:828`).
- On each 60-second tick, a kept grant whose client has no process and which names no live holder is ended the same way, and so is a stopped grant's request that waits for its client and names no live holder (`internal/grants/broker.go:347`-`:369`).
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

### Desk stream

`orreryd` serves the desk stream on `notices.sock`, beside the daemon socket (section 5). The socket's directory is created with mode 0700 when it is absent. `orrery notices` reads the stream, and `orrery desk` reads it on this machine and over ssh. Nothing here opens a network listener. `orreryd`, `orrery notices` and `orrery desk` use unix sockets, and a host's notices leave it only through the ssh session that `orrery desk` starts.

The stream is JSON lines. Each line is one object and ends with a newline.

- `hello` is the first line of every stream: `{"kind":"hello","host":"example-host"}`.
- `notice` carries one notice: `{"kind":"notice","host":"example-host","state":"waiting","title":"example-project · example-session:1","body":"Which file should I edit?"}`. `state`, `title` and `body` are left out when empty. `quiet` is `true` on a quiet notice, which the judge found unneeded (see [Notice routing](#notice-routing)), and absent otherwise. A quiet notice: `{"kind":"notice","host":"example-host","state":"done","title":"example-project · example-session:1","body":"Example summary of the work.","quiet":true}`.
- A line the desk sends up the stream is read and discarded. It has no effect on the desk's presence.

Each stream gets `hello` first, then each notice posted while it is open, in the order posted. The stream keeps no backlog, so a notice posted while no stream is open is dropped. A stream that falls 64 lines behind is closed. On shutdown, queued lines get up to 2 seconds to reach their streams, and then open streams are closed.

The desk is present while a stream is open. `orreryd` keeps the newest web input that `serve` reports to it (section 9).

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
- A project file may also hold a `[gates]` table, which [Gate files](#gate-files) defines. Here it is refused only when `gates` is not a table. The machine file's `[gates]` table is not read for gates.
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

A name resolves in this order (`internal/gates/resolve.go:36`-`:66`):

1. When an epic gates file is named, it is read first, in full. A missing, unreadable or invalid file is an error, even when the name is in the project file.
2. When that file does not define the name, or no file is named, the project file is read. A missing project file defines no gates. The project file is found as [Pool files](#pool-files) gives.
3. A name that neither file defines is refused. The error names every file searched, or says that there were none.

The gate's source is `epic` or `project`, the file that defines it.

Each argv gate's `[gates.NAME]` table has these keys. An agent gate's keys are in [Agent gates](#agent-gates):

| Key | Type | Meaning |
|---|---|---|
| `argv` | array of strings | required. The gate's program and its arguments, run without a shell. The array may not be empty, and no element may be empty. `argv[0]` must contain a `/`, and it is never looked up on `PATH` |
| `limit` | integer seconds, 1 to 9223372036 | required. How long the gate may run. The count starts when the gate's grant is granted, not when it queues |
| `needs` | table of pool names to integers | optional. The pool members the gate holds while it runs, as `POOL = COUNT`. A gate with no `needs` holds one member of the pool `gate`. The table may not be empty |

- A gate name matches `[a-z][a-z0-9_]*`, and so does each pool name in `needs`. Each count is 1 to 2147483647.
- Each pool in `needs` must be declared by a pool file for the directory the gate is resolved in: the working directory for `orrery gate`, the step's workspace for a step. Otherwise the gate is refused (see [Pool files](#pool-files)).
- Keys match exactly. A file with a top-level key other than `gates` (or `pools` in a project file) is refused. So is an argv gate with a key other than `argv`, `limit` and `needs`, or one without `argv` or `limit`. An agent gate's refusals are in [Agent gates](#agent-gates).

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
| `limit` | integer seconds, 1 to 9223372036 | required. The review's limit. It is stored with the step, the first prompt states it, and the watch enforces it: the step ends `overtime` at the first tick at or past the limit of wall time from the run's start (see [Step lifecycle](#step-lifecycle) and `internal/enginehost/stepwatchloop.go:20`-`:27`) |
| `must` | array of strings | optional. Each entry is an assertion that must hold |
| `must_not` | array of strings | optional. Each entry is an assertion that must not hold |

At least one entry is needed between `must` and `must_not`, and each entry is a non-empty string. The gate is refused, naming it, when it holds `argv` or `needs`, when its `agent` is an empty string, when it has no `limit`, when `must` and `must_not` have no entry between them, or when an entry is an empty string. An argv gate with `must` or `must_not` is refused too (`internal/gates/gates.go:173`-`:177`, `:192`-`:214`, `:226`-`:246`).

The gate's assertions are its `must` entries in file order, then its `must_not` entries in file order. A `must` entry has the kind `must`, and a `must_not` entry has the kind `must not` (`internal/gates/gates.go:28`-`:31`, `:226`-`:246`).

The agent's return must match `internal/gates/verdict.json`, the verdict schema, which is embedded as `gates.VerdictSchema` (`internal/gates/verdict.go:13`-`:14`). Its root requires `status`, `findings`, `observations`, `missing_evidence`, `test_runner_requests` and `results`, and each object admits only the keys it names. `status` is `done` or `blocked`. `results` has one entry per assertion, in declaration order. Each result requires `assertion`, `kind` (`must` or `must not`), `result` (`holds`, `fails` or `undetermined`) and `evidence`. A `findings` entry requires `id`, `claim`, `citation`, `location`, `evidence` and `verify_by`, and admits `precondition` and `consequence`. An `observations` entry requires `location`, `observation` and `reason`, and a `test_runner_requests` entry requires `command` and `revision`.

The first prompt is built by `gates.Prompt` (`internal/gates/verdict.go:37`-`:47`). Its sections are separated by a blank line, in this order: the fixed instruction (`internal/gates/verdict.go:18`-`:33`); `Gate: NAME` and `Limit: N seconds` on two lines; `Assertions:`, then one line per assertion, numbered from 1 as `N. KIND: TEXT`, in assertion order (`internal/gates/verdict.go:50`-`:57`); and the caller's prompt, when it is not empty.

An example, with synthetic names:

```toml
[gates.review]
agent = "example-reviewer"
limit = 900
must = ["The change keeps every example endpoint's response shape."]
must_not = ["The change edits the example vendor directory."]
```

### Holder

`orrery holder` runs one session's child and serves the session's holder socket (see [orrery](#orrery)). The holder is the package `internal/holder`. It spools the child's stdout to disk, keeps a tail of the child's stderr, and answers controllers on a unix socket. A controller attaches to read the stdout, writes to the child's stdin, trims the spool, or stops the child. `orreryd` is the controller of each session's holder, through its session process and through the re-adoption, drain and clear that reconcile runs at start (see [Headless sessions](#headless-sessions) and [Lifecycle rules](#lifecycle-rules)). A controller that detaches closes its connection and leaves the holder, its child and its directory in place. A gate's holder is controlled the same way, through the gate's launch, its re-adoption and its clear (see [Step lifecycle](#step-lifecycle)). Once the holder's process and stream have ended, a controller that did not detach removes the holder's directory (`internal/holder/process.go:211`-`:220`, `:233`-`:243`; `internal/holder/clear.go:60`).

#### Directory and files

- The holders directory is `holders` in the directory that holds the store file: `$XDG_STATE_HOME/orrery/holders` when `XDG_STATE_HOME` is set, else `$HOME/.local/state/orrery/holders`. `--state-dir DIR` names `DIR/holders` instead. A gate's holder is always launched with `--state-dir` naming its gate state directory (`internal/holder/launch.go:133`). When neither the store path nor `--state-dir` resolves, `orrery holder` exits 1 (`cmd/orrery/holder.go:38`-`:45`, `internal/holder/dir.go:40`-`:48`).
- A session's files are in `holders/SESSION`. `SESSION` is 1 to 64 characters: its first a letter or digit, and the rest letters, digits, `.`, `_` or `-`. Any other name is a usage error (`internal/holder/dir.go:38`, `:52`-`:57`; `cmd/orrery/holder.go:46`-`:49`).
- The holders directory is created with its parents at mode 0700 when it is missing, and an existing one is not checked. The session directory is created at mode 0700. An existing session directory must be a real directory, not a symbolic link, with mode exactly 0700 and owned by this user. Otherwise the holder refuses to start (`internal/holder/dir.go:72`-`:112`).
- `holder.lock` (0600) is the session's lock. The holder takes an exclusive, non-blocking `flock` on it and keeps it for its life. A holder that finds it held refuses with `session NAME: another holder holds the session's lock` (`internal/holder/lock.go:11`-`:33`, `internal/holder/holder.go:88`-`:91`).
- `holder.sock` (0600) is the socket (see [Socket](#socket)). `stdout-` segments (0600) hold the child's stdout (see [Spool](#spool)). `exit.json` (0600) is the exit record (see [Exit file](#exit-file)).
- `holder.json` (0600) is the pids record: the holder's process id and its child's, each with its start time from `/proc/PID/stat`, which is empty where `/proc` holds no stat. The holder writes it once the child has started, and never replaces an existing one. A record that cannot be written ends the child and the start, as a child that cannot start does (see [Exit file](#exit-file)) (`internal/holder/pids.go:16`-`:18`, `:26`-`:33`, `:37`-`:47`, `:67`-`:95`, `:97`-`:109`; `internal/holder/holder.go:131`-`:133`).
- Once it holds the lock, the holder refuses to start when `exit.json` exists, and when any `stdout-` segment exists. Both refusals exit 1 (`internal/holder/holder.go:102`-`:110`, `:126`-`:129`; `internal/holder/spool.go:62`-`:78`).

#### Socket

- The holder removes a socket file left in the session directory, listens on `holder.sock` and sets its mode to 0600. The path must be under 108 bytes, and a longer path is refused (`internal/holder/holder.go:111`-`:125`, `internal/holder/dir.go:24`-`:27`, `:59`-`:67`). The socket file is removed when the holder stops listening (`internal/holder/holder.go:237`-`:238`).
- Each connection's peer uid must equal the holder's uid. A peer of another user, or one whose uid cannot be read, is closed with no frame (`internal/holder/holder.go:311`-`:323`). The uid comes from `SO_PEERCRED` on Linux (`internal/holder/peercred_linux.go:12`-`:29`) and from `LOCAL_PEERCRED` on Darwin (`internal/holder/peercred_darwin.go:12`-`:29`).
- The first frame on a connection must be `hello`. Any other first frame closes the connection with no answer (`internal/holder/holder.go:274`-`:282`, `:326`-`:336`).

#### Spool

- The child's stdout is appended to segment files named `stdout-` followed by the segment's first offset as 20 decimal digits. Each segment is created with mode 0600. A segment is full at 4 MiB, and the next one opens when the current one is full (`internal/holder/spool.go:12`-`:14`, `:98`-`:105`, `:19`, `:251`-`:259`, `:277`-`:279`).
- An offset counts the child's stdout bytes, and the first byte is offset 0 (`internal/holder/spool.go:45`-`:60`, `:92`-`:119`). `start` is the first offset the spool still holds, and `end` is the offset just past the last byte it holds (`internal/holder/spool.go:188`-`:200`).
- An `ack` carries `consumed` and `replayFrom`. The spool refuses it unless `0 <= replayFrom <= consumed <= end` and neither offset moves backwards. It then deletes every segment whose end is at or below `replayFrom`. It never deletes the last segment, which is the one being written (`internal/holder/spool.go:154`-`:178`). A controller sets `replayFrom` to the offset that a later re-adoption replays from, so the spool keeps every byte from that offset on (see [Reconcile at start](#lifecycle-rules)).
- The holder reads no further child stdout once `end` is 64 MiB past `consumed`, so the child's writes back up in its pipe until an `ack` moves `consumed` (`internal/holder/spool.go:14`-`:16`, `:223`-`:249`; `internal/holder/child.go:149`-`:185`).
- Once the child is reaped, the spool is sealed: appends ignore the cap, and the holder reads the rest of the child's stdout to end of file. Those bytes are what the child left in its pipe (`internal/holder/spool.go:180`-`:186`, `internal/holder/child.go:149`-`:168`).

#### Exit file

- The holder writes `exit.json` once the child has ended, before it answers `stopped` (`internal/holder/holder.go:235`-`:243`). It writes a temporary file in the session directory and hard-links it to `exit.json`, so a reader sees either no file or the whole record. It never replaces an existing `exit.json` (`internal/holder/exitfile.go:15`-`:47`). If the file cannot be written, the holder sends no `stopped` and exits 1.
- The record is a JSON object with these keys (`internal/holder/wire.go:96`-`:108`):
  - `reason`: `exited` when the child ended on its own. `stopped` when a stop frame, or a SIGINT or SIGTERM to the holder, had arrived when the child ended. `idle` as [Idle timeout](#idle-timeout) gives (`internal/holder/wire.go:41`-`:49`, `internal/holder/holder.go:233`-`:235`, `:378`-`:387`). `limit` when a gate child passed its limit and the holder stopped it, as [Child](#child) gives (`internal/holder/wire.go:50`, `internal/holder/child.go:146`-`:148`).
  - `code`: the child's exit status, or -1 when a signal ended the child or its wait gave no status (`internal/holder/child.go:105`-`:116`).
  - `signal`: the name of the signal that ended the child. It is omitted when empty (`internal/holder/child.go:201`-`:207`).
  - `stdoutEnd`: the spool's end once the child is reaped (`internal/holder/child.go:107`).
  - `stderr`: the last 64 KiB of the child's stderr, as the child wrote it, trailing newlines included. A gate child's record keeps the newest 8 KiB of its stdout and stderr together instead (see [Child](#child)). A fault of the holder, such as a failed signal, is its last line, after a newline when the tail does not end in one (`internal/holder/child.go:117`-`:121`, `:209`-`:219`; `internal/engine/proc/proc.go:16`, `:145`-`:146`).
- A child that cannot start, or whose pids record cannot be written, also gets an `exit.json`, with reason `exited`, code -1 and the error as `stderr`. The holder then exits 1 (`internal/holder/holder.go:130`-`:136`).

Example. The record of a child that exited 0 with no stderr, synthetic:

```json
{"reason":"exited","code":0,"stdoutEnd":15,"stderr":""}
```

#### Child

- The child is `PATH` with `ARGS` as its argument vector, run without a shell (`cmd/orrery/holder.go:55`, `internal/engine/proc/proc.go:88`-`:97`). `PATH` must be absolute, and a relative `PATH` fails to start (`internal/engine/proc/proc.go:58`-`:61`).
- The child runs in the holder's working directory and with the holder's environment (`internal/holder/child.go:55`, `internal/engine/proc/proc.go:88`-`:92`).
- The child runs in its own process group, whose id is its process id (`internal/engine/proc/proc.go:96`, `internal/holder/child.go:79`-`:81`). When the child is reaped, any process left in its group is sent SIGKILL (`internal/engine/proc/proc.go:188`-`:191`).
- Its stdin is a pipe that controller `write` frames feed. Its stdout goes to the spool. Its stderr keeps a 64 KiB tail (`internal/holder/child.go:70`-`:76`, `internal/engine/proc/proc.go:62`-`:64`, `:118`).
- A gate child (`GateLimit` above zero, which `orrery holder --gate-limit` sets) differs. Its stdin is closed at start, so no `write` frame reaches it. Nothing is spooled, and its stdout and stderr share one tail, the newest 8 KiB, which the exit record carries. Once its limit has passed on the holder's clock from the child's start, it is stopped as a stop stops it, and its reason is `limit` (`internal/holder/child.go:19`-`:20`, `:32`-`:36`, `:76`-`:92`, `:156`-`:170`; `internal/engine/proc/proc.go:35`-`:37`).
- A stop sends SIGTERM to the child's group at once. If the child is not reaped within the grace, SIGKILL goes to the group. The grace is `ChildConfig.Grace`, and 5 s when it is zero. It is counted on the holder's clock (`internal/holder/child.go:16`-`:17`, `:83`-`:95`, `:124`-`:132`).

#### Wire protocol

The attached frame carries `consumed` and `replayFrom` as well. Those keys are additive, since a reader ignores unknown keys, so `WireVersion` stays 1 (see [Versioning](#versioning)).

Framing:

- A frame is one JSON object on one line, ending in LF. A frame, LF included, is at most 32 MiB (`internal/holder/wire.go:17`-`:19`, `:110`-`:128`, `:157`-`:179`).
- Every frame has a string `type`. Keys are camelCase. A field at its zero value is omitted from a frame. Byte fields (`data`) are base64 strings. A reader ignores unknown keys (`internal/holder/wire.go:59`-`:94`, `:140`-`:155`).
- A line that is not JSON, has no `type`, or is longer than the limit closes the connection (`internal/holder/wire.go:140`-`:155`, `internal/holder/holder.go:274`-`:309`).

Frozen in every version:

- `hello`. A controller sends `{"type":"hello","version":N}` first. The holder answers with its own version and session, `{"type":"hello","version":N,"session":"NAME"}`. The holder does not compare versions, so the controller decides what it attaches (`internal/holder/holder.go:326`-`:336`, `internal/holder/client.go:146`-`:160`).
- `stop`. After `hello`, any connection may send `{"type":"stop"}`. The holder stops the child as [Child](#child) says. It sends `{"type":"stopped"}` to each connection that sent stop once `exit.json` is written. A stop that arrives after that is answered at once (`internal/holder/holder.go:350`-`:376`, `:436`-`:451`, `:232`-`:235`).
- `stopped`. `{"type":"stopped"}` carries no fields.

Version 1 (`WireVersion` 1):

- `attach`. A controller sends `{"type":"attach","replayFrom":N}`. The holder accepts it when `N` is within the retained `[start, end]` and the child has not ended. It answers `{"type":"attached","start":S,"end":E,"consumed":C,"replayFrom":R,"written":[...]}`, where `consumed` and `replayFrom` are the spool's last acked offsets, so the controller acks at or above them, and `written` lists every write id recorded so far, in the order received. The stream then follows: `out` frames from `N` in order, then more as the child writes. An attach refused for its offset or because the child has ended gets an `error` frame and closes its connection, and the attached controller stays. An attach on the connection that is already the attached controller is refused the same way, and that connection detaches. A new attach replaces the attached controller, whose connection closes (`internal/holder/holder.go:453`-`:497`, `:614`-`:634`).
- `out`. `{"type":"out","offset":N,"data":"..."}` carries stdout from offset `N`, at most 64 KiB per frame (`internal/holder/wire.go:21`-`:22`, `internal/holder/holder.go:618`-`:634`).

Example, synthetic. The controller attaches from the start of the output, which is 15 bytes long:

```
controller  {"type":"hello","version":1}
holder      {"type":"hello","version":1,"session":"example-session"}
controller  {"type":"attach"}
holder      {"type":"attached","end":15}
holder      {"type":"out","data":"ZXhhbXBsZSBvdXRwdXQK"}
```

- `exit`. `{"type":"exit","exit":{...}}` carries the record of [Exit file](#exit-file). The holder sends it to the attached controller once that controller's `out` frames reach `stdoutEnd`, then ends the streams and quits (`internal/holder/holder.go:636`-`:647`, `:213`-`:249`).
- `ack`. From the attached controller only, `{"type":"ack","consumed":N,"replayFrom":M}`. It has no answer. It moves `consumed` and trims the spool (see [Spool](#spool)) (`internal/holder/holder.go:575`-`:591`).
- `write`. From the attached controller only, `{"type":"write","id":"ID","data":"..."}`. `ID` is 1 to 256 bytes. An `ID` already recorded is answered with `{"type":"written","id":"ID","duplicate":true}`, and nothing is written. A new `ID` is recorded when it arrives and queued. Its data goes to the child's stdin in the order recorded, and `{"type":"written","id":"ID"}` answers once the data is written. If the child's stdin refuses the data, the holder answers `{"type":"error","id":"ID","message":"..."}`. The connection stays open, and the `ID` stays recorded, so a repeat is a duplicate (`internal/holder/holder.go:499`-`:573`).
- `error`. `{"type":"error","message":"..."}` refuses a frame. The holder closes the connection after it refuses an attach, an ack, a write, or a frame it does not serve. It does not close after a failed stdin write (`internal/holder/holder.go:338`-`:348`, `:274`-`:309`, `:563`-`:573`).

#### Versioning

- `WireVersion` is 1 (`internal/holder/wire.go:13`-`:15`). It is raised only by an incompatible change. `hello`, `stop` and `stopped` keep their shape in every version (`internal/holder/wire.go:13`-`:14`).
- A controller attaches only a holder whose version is its own or one less. At version 1 that means version 1. Any other holder is refused before the controller sends `attach`. The refusal names the session and both versions, the connection closes, and the holder keeps running. Its text is `holder for session S speaks wire version H; this controller attaches versions C and C-1` (`internal/holder/client.go:93`-`:117`, `:11`-`:26`).
- `Dial` accepts a holder of any version, so `stop` works on every version (`internal/holder/client.go:40`-`:44`, `:70`-`:91`).

#### Idle timeout

- A holder with no attached controller for 10 minutes stops its child. `DefaultIdleTimeout` is `10 * time.Minute`. The timer is armed when the holder starts serving, and again when the attached controller's connection ends with no new attach. An attach disarms it. Connections that only send `hello` or `stop` are not controllers (`internal/holder/holder.go:28`-`:29`, `:213`-`:216`, `:389`-`:408`, `:482`, `:666`-`:670`).
- When the timer fires, the holder stops the child as [Child](#child) says, writes `exit.json` with reason `idle`, and quits. If a stop or a SIGINT or SIGTERM comes first, the reason is `stopped`. `Config.IdleTimeout` sets another timeout, and the timer runs on `Config.Clock` (`internal/holder/holder.go:43`-`:45`, `:60`-`:62`, `:397`, `:421`-`:433`).

### Store

`orreryd` keeps a SQLite store that mirrors each pane's tmux options, records each headless engine session (see [Engine sessions in the store](#engine-sessions-in-the-store)), keeps the live grants of the Pools service (see [Grants in the store](#grants-in-the-store)), keeps the pending pane and headless session notices (see [Pending notices in the store](#pending-notices-in-the-store)), keeps the kept push times (see [Pushed notices in the store](#pushed-notices-in-the-store)), keeps the owner holds of the Pools service (see [Holds in the store](#holds-in-the-store)), and keeps the epics that the Epics service stores (see [Epics in the store](#epics-in-the-store)). Outside the store's tests, `orreryd`'s own writes read it, `orrery serve` reads it for the Connect API (section 12), and `orrery open` and the tree's `N` read it read-only for the known-directory lookup (see `open` under [orrery](#orrery)). A sync reads the pane's row to compare it with the new snapshot, and a tidy also reads the server's rows to find the panes that are gone. `serve` only reads the store, and never writes it.

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

A failed `list-panes`, or a listing that does not parse, changes nothing. orreryd logs `tmux server unreachable` (warning) when a server's `list-panes` starts failing and `tmux server reachable again` (info) at its first success after that, not on every tidy. Other tidy failures are logged as `mirror tidy dropped` each time. A failed store write rolls back the whole tidy. A default socket skipped by the directory check logs `tmux default socket skipped` (warning, with `server` and `reason`) when the skip starts and again when its reason changes, not on every tidy.

#### Engine sessions in the store

An engine row is a `sessions` row with `kind` `engine`. Its columns hold these values:

- `state` is one of the session states of [Headless sessions](#headless-sessions).
- `changed` is the unix seconds of the last state change.
- `seen` is the unix seconds of the last `session seen`, or `""`.
- `question` is the open question while the state is `waiting`, or the tool name of an open permission request. An answer, a withdrawn request, a turn's end and reconcile set it to `""`. A row that becomes `stopped`, `failed` or `interrupted` while it waits keeps the question it had.
- `claude_session` is the Claude Code session id, once the process has reported it.
- `dir` is the directory the session runs in. `reason` is described in the table above.

The re-adoption columns hold the offsets that a restarted `orreryd` replays from (migration `00013_engine_readopt.sql`, `internal/store/migrations/00013_engine_readopt.sql:2`-`:6`):

- `consumed` is the stdout offset the pump has stored through. It is `NULL` until a launch tracks the row. A row that is `NULL` here, such as one whose holder started before the migration, is cleared at reconcile and not re-adopted. A launch sets it to `0`, and each stdout line, though not the exit line, sets it to that line's end (`internal/store/engine.go:96`-`:104`, `internal/enginehost/record.go:203`-`:206`, `internal/store/engine_rows.go:82`).
- `replay_from` is the offset of the last turn boundary. A launch sets it to `0`, and a line that holds a turn end sets it to that line's end (`internal/enginehost/record.go:208`-`:211`, `internal/enginehost/manager.go:1287`-`:1289`).
- `turn_output` is the structured output of the last turn end, stored with `replay_from` (`internal/enginehost/record.go:210`-`:212`).
- `background` is whether the last background-task report lists a task. A launch turns it off (`internal/enginehost/record.go:214`-`:216`).
- `prompts_from` is the newest event seq at the session's last launch. `HolderPrompts` lists the session's `engine_prompt` events above it that carry a write id, in seq order (`internal/store/queries.sql:122`-`:125`, `:166`-`:170`; `internal/store/engine_rows.go:37`-`:50`).

Each launch is committed before its holder starts, so a launch resets the offsets before the child runs (`internal/enginehost/manager.go:1147`-`:1149`; `internal/store/engine.go:89`-`:94`). Each stdout line is one write: its ops and an `EngineConsumed` for the line go in one `Put`, so a line's events and its offset are stored together. A line that holds a turn end is committed, and only then does the pump let the holder trim to its replay-from. A line with no events still advances `consumed`. The exit line stores no offset (`internal/enginehost/manager.go:1253`-`:1309`, `internal/enginehost/record.go:222`-`:229`). Replayed lines record nothing (`internal/enginehost/manager.go:805`-`:822`).

The writer keeps `EngineConsumed` outside its op limit. A new one merges into a queued `EngineConsumed` of the same session: the offset takes the later value, and each optional field takes the new value when it is set. The search stops at a queued `EngineAdd` or `EngineLaunch` of the session. The queue is one transaction, so the merged offset commits with its lines (`internal/enginehost/writer.go:247`-`:272`, `:375`-`:382`, `:283`-`:296`).

A prompt's write id is stored in its event's `write_id` column and kept out of the payload, so the payload is what it was (`internal/store/engine.go:77`-`:87`, `:346`-`:357`).

Tidy reads only pane rows, and the typed API's `List` returns only pane rows (see [Connect API](#connect-api)). The engine rows are reached through the Engine service, `session watch`, and the engine messages of the Events stream (see [Connect API](#connect-api)).

The engine kinds of the `events` table are these seven. Each payload is a JSON object with these keys, in camelCase. The examples are synthetic:

- `engine_added`: an engine row is inserted. Payload `{"dir":"/home/user/project","state":"working"}`.
- `engine_changed`: one or more columns change. Payload `{"fields":{"changed":"1700000300","state":"done"}}`. The keys of `fields` are `state`, `seen`, `question`, `claudeSession` and `reason`, for the columns that changed. `changed` is set with each `state` change.
- `engine_prompt`: a prompt is sent to the session. Payload `{"text":"Example prompt."}`. The text is not cut. A `Say` with a `prompt_id` adds the key `promptId` with that id. A `Start` prompt, and a `Say` without a `prompt_id`, have no `promptId` key. A step prompt adds the key `sender`: `brief` for the step's start prompt, `engine` for a send-back, the judge or the watchdog, and `owner` for an owner nudge, alone or joined to a watchdog text. A send-back also adds the key `round`, the check round it returns. Each of `sender` and `round` is omitted when it is empty or 0, and a session's `Say` stores neither.
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

Its primary key is `session_id`, `message_id`, `block_index` and `parent_tool_use_id`. A row is removed when its block finishes, and every row of a session is removed when a turn ends, the process exits, the session is stopped, or `orreryd` shuts down without `--holder-scope`, and reconcile at start removes the rows of a session it does not re-adopt. With `--holder-scope` a shutdown keeps the rows, and a re-adopted session keeps its rows, since its replay writes nothing (`internal/enginehost/manager.go:1508`-`:1509`, `:805`-`:822`; see [Lifecycle rules](#lifecycle-rules)). No event is appended for engine text. The Connect `Watch` call sends these rows as `EngineText` messages, which are not events (see [Connect API](#connect-api)).

The `engine_prompt_failed` table holds the failure of each `prompt_id` whose send failed, one row per session and `prompt_id`:

| Column | Type | Meaning |
|---|---|---|
| `session_id` | `TEXT` | the engine row's `id` |
| `prompt_id` | `TEXT` | the `prompt_id` of the `Say` |
| `reason` | `TEXT` | the error of the failed send |

Its primary key is `session_id` and `prompt_id`. A row is written in the same write as the session's `failed` state, and an insert for a key that already has a row keeps the first reason. No event is appended for it, and the Connect `Watch` call does not send it. The 14-day cutoff below removes its rows with their engine row.

The 14-day cutoff of the tidy applies to engine rows too. The cutoff is `now` less 14 days. Each engine row with no event at or after the cutoff is deleted with its `engine_text` and `engine_prompt_failed` rows, and no event is appended, unless its session is live. A session is live from its start, resume or re-adoption until it is stopped, idle-stopped, exits, or `orreryd` shuts down, and a live session keeps its row, its `engine_text` rows and its `engine_prompt_failed` rows through the prune. The same tidy pass prunes events older than the cutoff, so the pass runs only while a tmux server's `list-panes` succeeds. A session that is not live loses its row after 14 days with no event, and a live session that waits for an answer keeps its row.

#### Steps in the store

The `steps` table (migrations `00004_steps.sql`, `00005_step_watch.sql`, `00006_step_labels.sql`, `00009_step_gate.sql` and `00015_step_readopt.sql`) has one row per step. Its `id` is the id of the step's `sessions` row. A row is added when the step starts. Its columns are these:

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
| `gate` | `TEXT` | the gate argv as a JSON array, as its definition gives it; `null` for no gate, and for an agent gate, which has no argv |
| `gate_limit` | `INTEGER` | the gate limit in nanoseconds; 30 minutes for a step that names no gate |
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
| `loop_round` | `INTEGER` | the step loop's round, the number of its latest round; `0` at start (`internal/enginehost/steploop.go:358`-`:363`) |
| `loop_continues` | `INTEGER` | how many failing returns the session has been sent back to fix, up to three |
| `loop_nudge` | `TEXT` | the watchdog's nudge text that the next turn end is continued with; `""` when none is pending |
| `loop_owner_nudge` | `TEXT` | the owner's nudge text that the next turn end is continued with; `""` when none is pending |
| `loop_nudged` | `BOOLEAN` | whether the watch has spent the step's one nudge |
| `loop_held` | `BOOLEAN` | whether an owner hold holds the step |
| `loop_judge_sent` | `BOOLEAN` | whether the judge has sent the step back |
| `loop_seq` | `INTEGER` | the newest event seq of the step's session when its latest return was stored; a turn end at or before it is that return's (`internal/store/steps.go:299`-`:317`) |
| `gate_grant` | `TEXT` | the grant id of the gate that runs; `""` when none runs |
| `gate_holder` | `TEXT` | the holder directory of the gate that runs; `""` when none runs |
| `gate_round` | `INTEGER` | the round the gate that runs is in; `0` when none runs |
| `gate_name` | `TEXT` | the name the step's gate was resolved by, as migration `00009_step_gate.sql` adds it; `""` for a step that names no gate, and for a row added before that migration |
| `gate_source` | `TEXT` | `epic` or `project`, the kind of file the gate was defined in, as migration `00009_step_gate.sql` adds it; `""` for a step that names no gate, and for a row added before that migration |
| `gate_needs` | `TEXT` | the gate's needs as a JSON array of `{"pool", "count"}` objects, as migration `00009_step_gate.sql` adds it; `null` for a step that names no gate, and `[]` for a row added before that migration |

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

The `pending_notices` table (migration `00010_pending_notices.sql`) has one row per pending pane notice and per pending headless session notice (see [Notice routing](#notice-routing)):

| Column | Type | Meaning |
|---|---|---|
| `id` | `INTEGER`, primary key | the row's id. An id is never reused |
| `pane` | `TEXT` | the notice's pane; `""` for a session notice |
| `session` | `TEXT` | the notice's session id |
| `state` | `TEXT` | the notice's state |
| `time` | `INTEGER` | unix seconds when the notice was recorded |
| `phase` | `TEXT` | where the notice is: `deciding`, `held` (its pane is watched), `delayed` (the 10-second judge wait of a pane `done`), `desk` or `sending`. `sending` is a notice handed to the phone whose publish has not returned. A session notice is never held or judged, so its phase is `deciding`, `desk` or `sending` |
| `due` | `INTEGER` | unix milliseconds when the phase's wait runs out; `0` for none |
| `delay_served` | `INTEGER` | `1` once the judge wait started, else `0`; always `0` for a session notice |
| `watch_logged` | `INTEGER` | `1` once the `watching` line was written, else `0`; always `0` for a session notice |
| `source_kind` | `TEXT` | `pane` for a pane notice, `session` for a session notice |
| `source` | `BLOB` | for a pane notice, the pane's transition as JSON, with the hook input included, so a restored judge reads the stored `last_assistant_message`; for a session notice, the session notice as JSON: its session, directory, state and body |
| `silent` | `INTEGER` | `1` when the push handed off was silent, else `0`; always `0` for a session notice. Added by migration `00011_pending_notice_silent.sql` |

The `pending_notice_ids` table has one row, whose `high` is the highest `id` the table has ever stored. Deleting rows does not lower it, so ids keep rising.

| Column | Type | Meaning |
|---|---|---|
| `high` | `INTEGER` | the highest `id` stored |

A row is written when its notice is recorded, with phase `deciding`. A pane notice's row is also written at each hold, with phase `held`, and when its judge wait starts, with phase `delayed`. A row is written again before its desk post, with phase `desk`, which it keeps until it escalates. Each row write has a 200 ms limit. A write that fails or runs past it is logged by `orreryd`, and the notice routes as it would. Later row writes and the next stop retry it. The row is deleted when the notice ends, after its sinks have been handed the notice, except for a notice handed to the phone. That notice is written `sending` before its push, and its row is deleted when the publish returns, with or without an error.

#### Pushed notices in the store

The `pushed_notices` table (migration `00012_pushed_notices.sql`) has one row per kept push time (see [orreryd](#orreryd)):

| Column | Type | Meaning |
|---|---|---|
| `pane` | `TEXT`, primary key with `session` | the pane whose push is kept; `""` for a session |
| `session` | `TEXT`, primary key with `pane` | the session id whose push is kept; `""` for a pane |
| `time` | `INTEGER` | unix seconds of the kept push |

Before each push is published, `orreryd` writes the row of its pane or session with the push's time, so a newer push replaces the row. The clear that ends a kept push removes its row (see [Clear payload](#clear-payload)).

At start, once the store opens and before the router restores its rows, `orreryd` reads the table, and each row's time becomes that key's kept push time. Rows for gone panes or sessions are not pruned.

Until the store opens, `orreryd` holds the seen writes it receives in memory. When the store opens, it applies each held seen write to the kept pushes: a seen write clears each kept push at or before its time, once. A pane or session pushed before the store opened keeps only its new time: the row read back for it and any seen held for it are dropped.

Each read and write of a row, a delete included, has a 200 ms limit. A call that fails or runs past it is logged by `orreryd`, and the push or clear is still sent.

#### Epics in the store

Migration `00014_epics.sql` creates the epic tables and the triggers on them. It creates tables and triggers only, and it alters and drops nothing that exists (`internal/store/migrations/00014_epics.sql:1`-`:103`). Its down step drops them (`internal/store/migrations/00014_epics.sql:105`-`:116`). Nine tables hold the epics: `epics`, with one row per epic; seven section tables, with one row per entry of an ordered section; and `epic_decisions`, with one row per decision. None of them declares a foreign key.

The `epics` table has these columns:

| Column | Type | Meaning |
|---|---|---|
| `id` | `TEXT`, primary key | the epic's id, `<repo-id>/<epic-slug>` (`internal/enginehost/epiccheck.go:29`) |
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

(`internal/store/migrations/00014_epics.sql:2`-`:15`)

Each section table has an `epic` column and a `position` column, which counts from 1 in the contract's order. Its primary key is `(epic, position)`. The other columns are these:

| Table | Columns | Migration lines |
|---|---|---|
| `epic_failures` | `id`, `text`, `rating`, `response` | `:17`-`:25` |
| `epic_additions` | `location`, `what`, `why`, `cost` | `:27`-`:35` |
| `epic_requirements` | `id`, `text`, `evidence`, `kind` | `:37`-`:45` |
| `epic_premises` | `id`, `text`, `if_wrong` | `:47`-`:54` |
| `epic_exclusions` | `text` | `:56`-`:61` |
| `epic_alternatives` | `alternative`, `why_not`, `reconsider` | `:63`-`:70` |
| `epic_release_steps` | `step`, `target`, `undo` | `:72`-`:79` |

The `epic_decisions` table has `epic` (`TEXT`), `seq` (`INTEGER`), `client_id` (`TEXT`), `text` (`TEXT`) and `time` (`INTEGER`). Its primary key is `(epic, seq)`, and `(epic, client_id)` is unique (`internal/store/migrations/00014_epics.sql:81`-`:89`). `seq` counts from 1 in recording order. A create records its seed decisions as `seq` 1 to `n`, each dated at the epic's creation. `client_id` is the decision's id (`internal/store/epics.go:93`-`:94`, `:178`-`:184`).

Two triggers guard `epic_decisions`. `epic_decisions_no_update` runs before any `UPDATE`, and `epic_decisions_no_delete` runs before any `DELETE`. Each aborts with `epic decisions are append-only` (`internal/store/migrations/00014_epics.sql:91`-`:103`).

Each call that writes an epic runs in one `BEGIN IMMEDIATE` write transaction, and a failed one leaves no rows behind (`internal/store/sync.go:239`-`:247`). `CreateEpic` writes the head and every section in one transaction (`internal/store/epics.go:102`-`:125`). Times are stored at whole seconds (`internal/store/epics.go:22`-`:23`). The store checks no contract content. The checks of [Epics](#epics) run before a create reaches it (`internal/enginehost/epicservice.go:51`-`:53`).

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

Connect paths are those beginning `/orrery.v1.Sessions/`, `/orrery.v1.Events/`, `/orrery.v1.Control/`, `/orrery.v1.Panes/`, `/orrery.v1.Host/`, `/orrery.v1.Push/`, `/orrery.v1.Picker/`, `/orrery.v1.Epics/`, `/orrery.v1.Steps/`, `/orrery.v1.Pools/` or `/orrery.v1.Presence/` (see [Connect API](#connect-api), [Control API](#control-api) and [Phone API](#phone-api)). Every request to a Connect path, whatever its method, must also send a non-empty `X-Orrery-Client` header. Otherwise the response is 403. A POST to a Connect path must send `Content-Type` `application/proto`, `application/json`, `application/connect+proto`, or `application/connect+json`. Otherwise the response is 403.

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

A clear is posted when a seen write sets a seen time at or after the time of the kept notice for a pane or session (see [orreryd](#orreryd)). Once the clear is published, the row of that pane or session is deleted from the store (see [Pushed notices in the store](#pushed-notices-in-the-store)). Its headers are the same as a notice's, `Content-Encoding: aes128gcm` and `TTL: 3600`. Its plaintext is a JSON object of string values with exactly these keys:

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

The services are `orrery.v1.Sessions`, with three calls, `List`, `ListHeadless` and `WatchConversation`, `orrery.v1.Events`, with one call, `Watch`, `orrery.v1.Control`, with six calls, which [Control API](#control-api) describes, and the six phone services `orrery.v1.Panes`, `orrery.v1.Host`, `orrery.v1.Push`, `orrery.v1.Picker`, `orrery.v1.Pools` and `orrery.v1.Presence`, with seventeen calls, which [Phone API](#phone-api) describes. The services `orrery.v1.Epics`, with two calls, and `orrery.v1.Steps`, with three calls, which [Epics and Steps](#epics-and-steps) describes. Their schema is `proto/orrery/v1/epics.proto`. `orrery.v1.Steps` is not the engine's `orrery.engine.v1.Steps` service of [Steps](#steps).

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
| `Panes.PullQueued` | `POST /orrery.v1.Panes/PullQueued` | `PullQueuedRequest` | `PullQueuedResponse` |
| `Host.Status` | `POST /orrery.v1.Host/Status` | `HostStatusRequest`, empty | `HostStatusResponse` |
| `Push.Register` | `POST /orrery.v1.Push/Register` | `RegisterRequest` | `RegisterResponse` |
| `Push.Unregister` | `POST /orrery.v1.Push/Unregister` | `UnregisterRequest` | `UnregisterResponse` |
| `Picker.Ask` | `POST /orrery.v1.Picker/Ask` | `AskRequest` | `AskResponse` |
| `Picker.Run` | `POST /orrery.v1.Picker/Run` | `RunRequest` | `RunResponse` |
| `Epics.List` | `POST /orrery.v1.Epics/List` | `EpicsListRequest`, empty | `EpicsListResponse` |
| `Epics.Get` | `POST /orrery.v1.Epics/Get` | `EpicsGetRequest` | `EpicsGetResponse` |
| `Steps.Hold` | `POST /orrery.v1.Steps/Hold` | `StepsHoldRequest` | `StepsHoldResponse` |
| `Steps.Nudge` | `POST /orrery.v1.Steps/Nudge` | `StepsNudgeRequest` | `StepsNudgeResponse` |
| `Steps.Stop` | `POST /orrery.v1.Steps/Stop` | `StepsStopRequest` | `StepsStopResponse` |
| `Pools.Status` | `POST /orrery.v1.Pools/Status` | `PoolsStatusRequest`, empty | `PoolsStatusResponse` |
| `Pools.Hold` | `POST /orrery.v1.Pools/Hold` | `PoolsHoldRequest` | `PoolsHoldResponse` |
| `Pools.Free` | `POST /orrery.v1.Pools/Free` | `PoolsFreeRequest` | `PoolsFreeResponse` |
| `Presence.Report` | `POST /orrery.v1.Presence/Report` | `PresenceReportRequest` | `PresenceReportResponse` |

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

- `orrery events` (`internal/cli/events.go:117`-`:148`) opens the stream again after the last seq it wrote, for as long as 60 seconds pass with no message written. The 60 seconds count from its last message, or from the start of the watch while none has been written (`internal/rpcretry/retry.go:16`).
- `orrery session --url ... watch` (`internal/cli/session_url.go:166`-`:190`) opens the stream again at once after a message, from the last seq it wrote. An `Unavailable` end before any message is retried for 60 seconds from the call's first attempt (`internal/rpcretry/retry.go:16`, `:64`).
- The web page (`web/src/main.tsx:138`-`:165`) opens the stream again after `defaultRetryMs`, 1 second, from the last seq it delivered, with no time limit. Any other end of the stream is left to the poll, which runs every 5 seconds (`web/src/main.tsx:11`, `:14`, `:169`-`:173`). The next load then opens the stream from the newest seq it reads.

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
- `InterruptRequest`, `StopRequest` and `SeenRequest`: `id`. Their responses have no fields.

Rules:

- **Home rule.** `Start` admits `dir` only when it is an absolute path to an existing directory whose symlinks resolve to a path equal to the home directory or under it. `serve` forwards the resolved path. The home directory is `HOME` as `serve` reads it (see [Environment](#environment)). An empty, relative or `/` home, or one that does not resolve, refuses every start. The check runs before the forward, so a refused start never reaches orreryd.
- **Client-chosen ids.** `StartRequest.id` names the new session's id, 32 lowercase hex digits. Empty lets orreryd mint one. A start that names an existing session's id returns that id and starts nothing. `SayRequest.prompt_id` names the prompt. A say whose `prompt_id` an `engine_prompt` event of the session already carries succeeds and sends nothing. A call that names a key with a running attempt waits for that attempt and returns its result. A say whose send failed returns that failure to every later call with its key and sends nothing, also after a restart, because the failure is stored in `engine_prompt_failed`. Any other key whose attempt failed after storing its record returns that attempt's error to every later call with the key, and does nothing else, while `orreryd` runs. A key whose attempt failed before storing its record is free, so a retry runs, unless the record cannot be read after the failure, which keeps the error as a stored record does. Empty means no id and no check.
- **Model.** `StartRequest.model` is the model alias the started session's process runs, passed to claude as `--model`. Empty runs claude's default. A model outside the rule of [Session control socket](#session-control-socket) is refused with `invalid_argument`. A process that a later `Say` resumes runs claude's default model.
- **Forward.** Each call that passes the home rule is forwarded to the engine under a deadline. The deadline is 10 seconds, the default for `serve`, which sets no other. The forward reaches the session socket through a unix-socket transport, `sessions.sock` beside the daemon socket. When no daemon socket path resolves, `serve` has no engine and answers `unavailable` without dialing.
- **Step ids.** `Say`, `Answer`, `Interrupt` and `Stop` ask orreryd's `StepStatus` about their `id` before they forward. When the id names a step, the call answers `failed_precondition` and nothing is forwarded, so a session command cannot drive a step around its own loop. A `not_found` from `StepStatus` forwards as before, and any other `StepStatus` error answers as the forward's errors do. `Seen` and `Start` do not ask. With no step client, every call forwards as before.
- **Body bound.** A request body over 1 MiB (1048576 bytes) is refused with `resource_exhausted` before it reaches the engine.

Errors are Connect codes. The messages marked fixed are the same whatever orreryd said:

| Connect code | Message | When |
|---|---|---|
| `invalid_argument` | `session directory must be an existing directory under the home directory` (fixed) | `Start`'s `dir` fails the home rule. Answered before the forward |
| `unavailable` | `session service unavailable` (fixed) | no daemon socket path resolves; the session socket cannot be dialed, so orreryd is not taking session commands; or orreryd answers `unavailable`, which it does when it has no store |
| `deadline_exceeded` | `session command timed out` (fixed) | the forward outlives the 10-second deadline, or the client's own deadline ends the call first |
| `internal` | `session command failed` (fixed) | orreryd answers `internal` or `unknown`, or the forward fails with an error that is not a Connect error |
| `resource_exhausted` | the Connect library's message | the request body is over 1 MiB |
| `failed_precondition` | `<id> is a step: drive it through Steps`, where `<id>` is the id given | `Say`, `Answer`, `Interrupt` or `Stop` names a step (see Step ids above) |
| other codes | orreryd's message, passed through | `invalid_argument` (a bad `model`, `id`, `prompt` or `text`), `not_found` (no row, or an answer whose request is not open), `resource_exhausted` (the session cap) |

### Phone API

The phone's calls are the six services `orrery.v1.Panes`, `orrery.v1.Host`, `orrery.v1.Push`, `orrery.v1.Picker`, `orrery.v1.Pools` and `orrery.v1.Presence` of [Connect API](#connect-api), with seventeen calls. Each call is a Connect POST to its path in [Connect API](#connect-api), and the Connect rules of [Guard](#guard) apply to it. This section states each call's request meaning, answer and errors. The error table at the end gives each error code with its message. A successful `Panes.Screen` or `Panes.Conversation` answer reports the read to `orreryd`, and an open `Panes.WatchConversation` stream reports it at its first successful step and once per keepalive interval after that (see [orrery serve](#orrery-serve)).

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
    - `sender` is the sender of a step prompt (`brief`, `engine` or `owner`) on a `prompt` item of a headless session's stream, and empty on every other item. `round` is the check round of a send-back on such a `prompt` item, and 0 on every other item. Both are copied from the stored prompt (see [Headless session stream](#headless-session-stream)).
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

The phone's pane screen (`android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/PaneScreen.kt`) resumes this stream as follows. A stream that ends `UNAVAILABLE` is opened again 1 second later, from the transcript and seq the screen holds, and that end is not counted as a failure. Any other end is counted, except `NOT_FOUND`, which makes the screen poll at once. After 3 counted failures in a row, with no delta between them, the screen polls its pane until its next start (`android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/PaneScreen.kt:69`, `:74`, `:176`-`:200`).

#### Epics and Steps

`orrery.v1.Epics` reads the gambit epics of the host and the epics orreryd's database holds, and `orrery.v1.Steps` makes the owner's calls on one labelled step. `serve` answers both, and each call is a Connect POST to its path in [Connect API](#connect-api), under the rules of [Guard](#guard). `serve` never starts a step: it never calls `StartStep` or `WaitSteps`.

**Records.** `serve` reads the record root `$HOME/.gambit` read-only (see [orrery serve](#orrery-serve)). Each epic's head is `<root>/<repo-id>/<epic-slug>/state.json`. A non-directory entry, a directory without `state.json`, and a name starting with `.` are not epics. An effort's tasks are read from `<epic directory>/efforts/<n>/state.json`, where `<n>` is all decimal digits, under its `tasks` key. Any other entry under `efforts/` is skipped. The records are only read: `serve` never creates, writes, renames or chmods anything under the root.

- An epic's `id` is `<repo-id>/<epic-slug>`, the same text that a step's epic label carries. `repository` is `<repo-id>` and `slug` is `<epic-slug>`.
- Record text is the value of a JSON string. `null` or an absent key is `""`, and any other JSON value is its compact JSON text. `outcome` and each task's `slug`, `subject` and `status` are record text. A task's `status` is kept as written.
- `changed` is the head's modification time in unix seconds.
- A file that does not parse, or whose `tasks` is not an array, is skipped and is not an error. Its name goes into `problem`: the head as `<repo-id>/<epic-slug>/state.json: does not parse`, and an effort's file as `efforts/<n>/state.json: does not parse`. `problem` joins the names with `; `, and it is empty when everything read. Only an unexpected read failure is an error.

**Live.** A record epic that orreryd's database does not hold is live when its `outcome` is not exactly `released`, and either one of its labelled steps is running or its head was modified within the 24 hours before now. Every other such epic is folded: `live` is false, and it is still listed (`internal/server/records.go:73`-`:82`). An epic the database holds is live until its phase is `released`, as the next paragraph gives (`internal/server/epics.go:260`).

**Orreryd's epics.** `serve` also reads the epics that orreryd's database holds, through orreryd's `ListEpics` and `GetEpic` (see [Epics](#epics)). With no epic client, `serve` reads the records alone. It calls no other epic method (`internal/server/server.go:118`-`:129`; `cmd/orrery/main.go:446`-`:452`). Each of the two calls retries an `Unavailable` answer for up to 60 seconds, as the `ListSteps` call does, with each attempt under the call timeout (`internal/server/epics.go:172`-`:214`). An epic the database holds has `source` `orreryd`, and an epic the records alone hold has `source` `record` (`internal/server/epics.go:244`, `:264`; `proto/orrery/v1/epics.proto:91`-`:92`). Its `repository` and `slug` are the parts of its `id` before and after the `/` (`internal/server/epics.go:253`-`:257`). An `id` that both the database and the records hold is one epic: its `outcome` is the database's phase, its `changed` is the database's, and its `problem` is the record's head problem and task problems. An epic the database holds with no record has an empty `problem` (`internal/server/epics.go:75`-`:94`, `:260`, `:268`-`:275`).

`Epics.List` answers every epic under the record root and every epic the database holds, live or folded, sorted by `id`. Each epic carries `running_steps`, its labelled steps that have no end, held ones included, oldest first, and `ended_steps`, how many of its labelled steps have ended. It calls `ListSteps` once, and `ListEpics` once when serve has an epic client. An orreryd `ListEpics` that answers `unimplemented`, as an older orreryd does, lists the records as before. Any other failure of it answers `unavailable` "epic store unavailable" (`internal/server/epics.go:53`-`:69`, `:172`-`:192`).

`Epics.Get` answers one epic by an `id` that `Epics.List` gives. It returns the epic, its `tasks` in effort number order (file order within an effort), its `steps`, every labelled step of the epic, oldest first, and its `slices`, which the next paragraph describes. It calls `GetEpic` first when serve has an epic client, then `ListSteps` once, narrowed to the epic. An epic the database holds answers its `requirements` as well: the `id`, `text`, `evidence` and `kind` of each requirement of its contract, in contract order. Its `tasks` come from the record of the same id when one exists. A `GetEpic` that answers `not_found` or `unimplemented`, or a serve with no epic client, leaves the epic to the records, as before: the record answers with `source` `record` and no `requirements`, and an id with no record answers `not_found`. Any other failure answers `unavailable` "epic store unavailable" (`internal/server/epics.go:102`-`:149`, `:194`-`:214`, `:291`-`:303`; `proto/orrery/v1/epics.proto:56`-`:57`). An `id` that `Epics.List` does not give answers `not_found` "epic not found", and the id is never joined into a path.

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

`Steps.Hold`, `Steps.Nudge` and `Steps.Stop` act on one step. Each call first asks `StepStatus` about the `id`, and forwards only when the step has an epic label. A step that is absent, or that has no epic label, answers `not_found` "step not found" and nothing is forwarded, so an unlabelled step is never listed or driven. A forwarded call goes to orreryd's `HoldStep`, `NudgeStep` or `StopStep` under the 10-second deadline of [Control API](#control-api). `Steps.Hold` and `Steps.Stop` retry an `Unavailable` answer from `orreryd`, and `Steps.Nudge` retries only a call whose connection was never made, so a nudge cut mid-call is not sent again. The `StepStatus` lookup retries an `Unavailable` answer. These retries run for up to 60 seconds, and each attempt has the 10-second deadline (`internal/server/steps.go:33`-`:46`, `:50`-`:64`, `:67`-`:80`, `:84`-`:127`, `:132`-`:150`; `internal/server/control.go:22`). `Nudge` sends the owner's `text` to the step. A `failed_precondition` from orreryd passes through with its message, which is the reason a hold or nudge was refused. Any other error answers as the forward's errors do (see [Control API](#control-api)). With no step client, the three calls answer `unavailable` "step service unavailable". The three responses have no fields.

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
| `not_found` | `Epics.Get` with an id that `Epics.List` does not give | `epic not found` |
| `not_found` | `Steps.Hold`, `Steps.Nudge` or `Steps.Stop` with a step that is absent or has no epic label | `step not found` |
| `failed_precondition` | `Steps.Hold` or `Steps.Nudge` that orreryd refuses | the message orreryd gives |
| `unavailable` | `Epics.List` or `Epics.Get` when `ListSteps` cannot be read; any `Steps` call, or any epic call, with no step client | `step service unavailable` |
| `unavailable` | `Epics.List` when orreryd's `ListEpics` fails with a code other than `unimplemented`, or `Epics.Get` when its `GetEpic` fails with a code other than `not_found` or `unimplemented`, an `Unavailable` answer once its retries have run out | `epic store unavailable` |
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
