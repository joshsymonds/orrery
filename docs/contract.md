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
- `debug summarize --pane ID --transcript PATH [--claude PATH]` runs one synchronous summary for the pane. The base URL comes from `ANTHROPIC_BASE_URL`. The command writes `@orrery_summary` and `@orrery_label` on the pane.
- `serve --listen ADDR --allow-login LOGIN` serves the HTTP API of section 12. See `### orrery serve`.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | error |
| 2 | usage error |
| 3 | question refused (`send --expect-question`: no question, hash mismatch, empty display fragment, or fragment not visible) |

### orrery serve

```
orrery [-L NAME | -S PATH] serve --listen ADDR --allow-login LOGIN [--allow-login LOGIN]... [--push-host HOST]...
```

- `-L NAME` and `-S PATH` select the tmux server, as section 6 defines.
- `--listen ADDR` sets the address to serve on. ADDR is a loopback IP literal with a port, such as `127.0.0.1:8080` or `[::1]:8080`. Any other ADDR exits 1 before anything is bound.
- `--allow-login LOGIN` admits one Tailscale login. Repeat the flag for each login. At least one is required.
- `--push-host HOST` names a push relay host, as `orreryd` does. Repeat the flag for each host. The default is `ntfy.sh`.

Registrations are written to the push store that `orreryd` reads (see `### orreryd`). When neither `XDG_STATE_HOME` nor `HOME` is set, `serve` exits 1.

`--listen` and at least one `--allow-login` are required. Without them `serve` exits 2.

`serve` is meant to sit behind `tailscale serve`, which sets the `Tailscale-User-Login` header that section 12 checks. Do not expose it through Tailscale Funnel.

`SIGINT` and `SIGTERM` stop `serve` and exit 0.

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
- `--push-host HOST` names a push relay host that a Web Push subscription may use. Repeat the flag for each host. The default is `ntfy.sh`. A subscription whose endpoint is on another host is refused (section 12).

`orreryd` listens on the unix socket only. It opens no network listener.

Summaries run when a session enters `working` (throttled) and when it becomes `done`.

ntfy notifications are sent only when `ORRERY_NTFY_URL_FILE` is set:

- On `waiting`, the body is the question.
- On `done`, the body is the latest summary.
- The access token comes from `ORRERY_NTFY_TOKEN_FILE`.
- The host tag is the machine host name.

Web Push is sent on the same two transitions. On `waiting` and on `done`, `orreryd` posts an encrypted Web Push to every registered subscription. Section 12 defines the payload. The subscriptions are kept in the push store:

- `$XDG_STATE_HOME/orrery/push.json` when `XDG_STATE_HOME` is set.
- Otherwise `$HOME/.local/state/orrery/push.json`.

When neither variable is set, `orreryd` exits 1.

`SIGINT` and `SIGTERM` stop `orreryd`.

### Environment

| Variable | Read by | Meaning |
|---|---|---|
| `ORRERY_SOCKET` | `orrery`, `orreryd` | daemon socket path (section 5) |
| `XDG_RUNTIME_DIR` | `orrery`, `orreryd` | default socket directory when no path is set (section 5) |
| `XDG_STATE_HOME` | `orrery` (`serve`), `orreryd` | base directory of the push store, `$XDG_STATE_HOME/orrery/push.json` (section 11, orreryd) |
| `HOME` | `orrery` (`serve`), `orreryd` | used for the push store when `XDG_STATE_HOME` is unset, at `$HOME/.local/state/orrery/push.json` |
| `ORRERY_NTFY_URL_FILE` | `orreryd` | file holding the ntfy topic URL; enables ntfy when set |
| `ORRERY_NTFY_TOKEN_FILE` | `orreryd` | file holding the ntfy access token |
| `ANTHROPIC_BASE_URL` | `orrery` (`hook`, `debug summarize`) | the session's base URL. The hook sends it in each frame (section 9), and each summary runs through its own session's base URL. |
| `ORRERY_SUMMARIZER` | `orrery` (`hook`) | summarizer sentinel (section 7). `orreryd` sets it on each summary run. Summaries also run with hooks disabled. |

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

## 12. HTTP API

`orrery serve` (section 11) serves this API over HTTP at the address given by `--listen`. Bodies are JSON unless the table says otherwise. Every example is synthetic.

### Guard

The guard runs before routing. It checks every request:

- The `Tailscale-User-Login` header must equal one of the `--allow-login` values. Otherwise the response is 403.

Every POST and DELETE must also send:

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
| `GET /v1/panes/{id}/screen` | none | 200, `text/plain; charset=utf-8` | 400 malformed pane ID, 404 absent pane, 500 tmux failure |
| `POST /v1/panes/{id}/text` | `{"text": "example input"}` | 204 | 400 malformed pane ID or bad body, 404 absent pane, 500 tmux failure |
| `POST /v1/panes/{id}/keys` | `{"keys": ["Enter"], "expect_question": "H"}`; `expect_question` is optional | 204 | 400 malformed pane ID or bad body, 404 absent pane, 409 question refused, 500 tmux failure |
| `POST /v1/panes/{id}/seen` | none | 204 | 400 malformed pane ID, 404 absent pane, 500 tmux failure |
| `POST /v1/push/register` | `{"endpoint": "https://push.example.test/sub/example", "p256dh": "example-key", "auth": "example-auth"}` | 204 | 400 bad body or keys, 422 endpoint not https on a `--push-host` host, 500 store failure |
| `DELETE /v1/push/register` | `{"endpoint": "https://push.example.test/sub/example"}` | 204 | 400 bad body, 404 not registered, 500 store failure |

Details for each route:

- `GET /v1/panes` returns the pane list of section 3. Each pane also has `question_hash`, the question hash of section 10 for the pane's `question`.
- `POST /v1/panes` opens a window in the session and directory of the pane named by `pane`. The window runs `claude` for kind `claude`, and the default shell for kind `shell`. The 201 response names the new pane.
- `GET /v1/panes/{id}/screen` returns the pane's screen as plain text, the output of `capture-pane -p -J`. It is not JSON.
- `POST /v1/panes/{id}/keys` sends the keys to the pane. When `expect_question` is present, the send is refused with 409 unless the question check passes. That check is the one `orrery send --expect-question` applies (section 11). `expect_question` takes the question hash H from section 10.
- `POST /v1/push/register` registers a Web Push subscription. `p256dh` and `auth` are the subscription's keys. The endpoint must be `https` and must be on a host named by `--push-host` (section 11).
- `DELETE /v1/push/register` removes the subscription with that endpoint.

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

## 13. Freeze

The names and values in this document are frozen as of effort 1. Later efforts, including the effort-4 golden test, build on them.
