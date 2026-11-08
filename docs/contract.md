# Orrery shared contract (frozen)

Status: **frozen**. Effort 1 defines this contract. Later efforts and the effort-4 golden test build on the names and values below. A change to any name or value here is a contract change and needs its own decision.

Module path: `github.com/joshsymonds/orrery`.

The Go code that implements each part lives in one of two packages:

- `internal/protocol`: pane state, tmux option names, the `orrery list --json` element, environment names, the daemon socket path, the summarizer sentinel, the summary model map, and the hook-to-daemon frame.
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

Options are set at pane scope with tmux 3.8 `set-option -p`.

| Go constant | Option name | Value |
|---|---|---|
| `protocol.OptionState` | `@orrery_state` | `working`, `waiting`, or `done` |
| `protocol.OptionChanged` | `@orrery_changed` | decimal unix seconds of the last state change |
| `protocol.OptionSeen` | `@orrery_seen` | decimal unix seconds the user last looked at the pane |
| `protocol.OptionQuestion` | `@orrery_question` | single-line text |
| `protocol.OptionSummary` | `@orrery_summary` | single-line text |
| `protocol.OptionLabel` | `@orrery_label` | single-line text |

Rules for every option value:

- Values are single-line. Writers replace newlines with spaces before they set an option.
- Timestamps are decimal unix seconds.

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

There are thirteen fields. The `state` value is one of the strings in section 1, or `""` when the option is unset.

Example output with one pane. Every value is synthetic:

```json
[{"id":"%1","host":"example-host","session":"example-session","window":0,"pane":0,"cwd":"/home/user/project","command":"claude","state":"waiting","summary":"Example summary of the pane's work.","label":"example-label","changed":1700000000,"seen":1700000100,"question":"Example question for the user?"}]
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

## 10. Freeze

The names and values in this document are frozen as of effort 1. Later efforts, including the effort-4 golden test, build on them.
