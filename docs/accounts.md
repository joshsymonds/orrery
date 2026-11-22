# LLM accounts and routes

Status: **spec**, for the `llm-accounts` epic. It defines an account, a seat, a context and a binding, the rules a route set must pass before it is stored, and which later epic produces or consumes each part. Code is named by symbol only: a Go package and symbol, or a `.proto` path and its service, message or field.

Orrery stores LLM accounts and routes behind one interface. The gateway (`orrery gateway`, on 127.0.0.1:4110) routes each Anthropic API request to a seat by these rules, and forwards the account tokens these rules name.

The examples here use invented names only. Hosts end in `.example.test`, and the profile is `example-profile`. The one real host is `https://api.anthropic.com`, the only upstream a forward seat may use.

## Where the data lives

The store is `orrery.db`, a SQLite file. orreryd is its one writer. The tables below are the ones this epic's migration adds. As with every orrery table, none declares a foreign key; the rules in [Validation](#validation-routesreplace) and [Account use](#account-use-and-removal) keep the references whole.

| Table | Columns | Holds |
|---|---|---|
| `llm_accounts` | `id`, `name`, `provider`, `plan`, `token`, `fingerprint`, `created` | one row per account; primary key `id` |
| `llm_seats` | `id`, `auth_mode`, and one column per seat field below | one row per seat; a column the seat's mode does not take is NULL; primary key `id` |
| `llm_contexts` | `id`, `default_seat`, `account` | one row per context; `account` is NULL when unset; primary key `id` |
| `llm_context_subagents` | `context`, `default_seat` | one row for each context that has a subagent policy; primary key `context` |
| `llm_bindings` | `context`, `kind`, `selector`, `seat` | one row per binding; `kind` is `model`, `agent_type` or `subagent_model`; primary key `(context, kind, selector)` |
| `llm_default_context` | `context` | one row, naming the default context |

The interfaces that touch these tables are:

- `proto/orrery/v1/accounts.proto: Accounts` is the client service that `orrery serve` answers. It lists, adds and removes accounts. Its reads come from the store.
- `proto/orrery/v1/accounts.proto: Routes` is the client service that `orrery serve` answers for the route set. It reads the route set from the store, and `Routes.Replace` replaces the whole route set.
- `proto/orrery/engine/v1/accounts.proto: Accounts` is the service orreryd serves on its session socket. It carries every account and route write that `orrery serve` forwards. orreryd validates each write and applies it through `store.Store`, its one writer.
- `store.Reader.RoutesSnapshot` reads the seats, contexts, bindings, default context and account tokens in one read. It is the store's one token-bearing read. The gateway calls it, and so does any orreryd probe that needs a token. `orrery serve` never calls it.

`orrery serve` reads the store and never writes it. Each write it accepts goes to orreryd, which validates it and writes it. A read through `Accounts` or `Routes` never returns a token.

## Account

An account is one Anthropic login the operator has pasted in.

| Field | Rule |
|---|---|
| `id` | 32 lowercase hex characters, minted by orreryd when `Accounts.Add` stores the account. A client never supplies it. It is opaque. |
| `name` | Free text. Required, and not blank. |
| `provider` | `anthropic` only, for now. Any other value is refused. |
| `plan` | Free text, and it may be empty. |
| `token` | The bearer that `claude setup-token` prints, as pasted. Surrounding whitespace is trimmed before the token is stored. Required after trimming. It is stored in plain text in `orrery.db`. Encryption is deferred. |
| `fingerprint` | The first 12 lowercase hex characters of the SHA-256 digest of the stored token. orreryd computes it once, when the account is stored. It is the only part of the token a list shows. |

The token is the secret. No answer, error or log carries it, and no read except `store.Reader.RoutesSnapshot` returns it. A list shows `id`, `name`, `provider`, `plan` and `fingerprint`.

## Seat

A seat is a reusable upstream destination, with the policy applied when a request goes there. A seat has an `id` and an `auth_mode`, which is `forward`, `inject` or `sigv4`.

Each auth mode takes exactly the fields in its column below. A field the mode does not take is refused. A field the mode requires must be present.

| Field | `forward` | `inject` | `sigv4` |
|---|---|---|---|
| `upstream` | required; exactly `https://api.anthropic.com` | required; an absolute URL under the rules in [Field rules](#field-rules) | not taken; the endpoint derives from `aws_region` |
| `model` | optional | optional | optional |
| `cache_ttl` | optional; `1h` or unset | not taken | not taken |
| `max_input_tokens` | optional | optional | optional |
| `strict_structured_output` | optional boolean | not taken | not taken |
| `billing` | not taken | required; `subscription`, `metered` or `self_hosted` | not taken |
| `api_key_env_file` | not taken | required; an environment variable name | not taken |
| `speed` | not taken | optional; `fast` or unset | not taken |
| `insecure` | not taken | optional boolean | not taken |
| `strip_cache_control` | not taken | optional boolean | not taken |
| `aws_profile` | not taken | not taken | required; a profile name |
| `aws_region` | not taken | not taken | required; a region name |
| `model_map_env_file` | not taken | not taken | required; not blank |
| `strip_betas` | not taken | not taken | optional list of beta flag names |

What the fields mean:

- `forward` sends the caller's own credential upstream. When its context names an account, it sends that account's token instead.
- `inject` sends a key read from the environment variable named by `api_key_env_file`. Its `billing` names who pays for that key, since nothing downstream can tell.
- `sigv4` signs requests for AWS with the profile `aws_profile` in region `aws_region`. `strip_betas` names the beta flags it withholds.
- `model` is the upstream model a request is rewritten to, when it is set. An inject seat's model is subject to the rule below.

### Field rules

- **Upstream.** An upstream is an absolute URL with a scheme and a host. It has no user information, query or fragment. `https` is allowed. `http` is allowed only for the loopback hosts `127.0.0.1` and `localhost`, and only without `insecure`. `http` to any other host requires `insecure`. Any other scheme is refused. `insecure` is refused with `https`, and with a loopback `http` upstream.
- **Forward upstream.** It must be exactly `https://api.anthropic.com`. Because that is `https`, a forward seat cannot set `insecure`, and it does not take the field.
- **Inject model.** When set, it must not contain `claude`, in any letter case.
- **Inject key variable.** `api_key_env_file` must match `^[A-Za-z_][A-Za-z0-9_]*$`.
- **Billing.** `billing` is required on an inject seat, and refused on a forward or sigv4 seat, whose billing class comes from the caller's credential or the signing AWS account.
- **Speed.** `speed` takes only `fast`. It is not a Bedrock request field, so it is refused on a sigv4 seat.
- **Cache TTL.** `cache_ttl` takes only `1h`, the one prompt-cache lifetime the seat may impose. Only a forward seat takes it.
- **Strict structured output.** The value must be a JSON boolean. `null` is refused.
- **Profile and region.** `aws_region` must use only lowercase letters, digits and dashes. `aws_profile` must use only lowercase letters, digits, dashes, dots and underscores. Both are required and not blank.
- **Model map file.** `model_map_env_file` is required and not blank.
- **Strip betas.** Each entry must be a non-blank beta flag name with no surrounding whitespace. The names are compared exactly.
- **Input token limit.** `max_input_tokens` must not be negative.

## Context

A context is a named set of bindings: the seats one caller's requests may reach, and the default seat for the rest. A context has these fields:

| Field | Rule |
|---|---|
| `id` | Matches `^[a-z0-9][a-z0-9-]*$`. |
| `default_seat` | Required. Names a seat. Used when no binding matches. |
| `models` | The model bindings, in [Binding](#binding). Optional. |
| `subagents` | Optional. When set, it holds `default_seat` (required, names a seat), the `agent_type` bindings and the `subagent_model` bindings. |
| `account` | Optional. Names an account. When unset, the context's forward seats send the caller's own login. |

The `account` field is the one that makes a context a credential choice as well as a route. When it names an account, the context's forward seats send that account's token upstream in place of the caller's credential. Inject and sigv4 seats never send an account token. Their own configured credentials are the only ones they send.

## Binding

A binding maps a selector, within one context, to a seat id. There are three kinds:

| Kind | Selector | Matches |
|---|---|---|
| `model` | a model name, non-empty | a request whose model is that name |
| `agent_type` | an agent type, 1 to 256 printable ASCII bytes with no space | a marked subagent request whose agent type is that name, such as the built-in `Explore` |
| `subagent_model` | a model name, non-empty | a marked subagent request whose model is that name |

A context's `default_seat` and its `subagents.default_seat` are not bindings. They are the fallbacks.

A request takes the first seat that applies, in this order:

1. The `model` binding whose selector is the request's model.
2. For a marked subagent request: the `agent_type` binding for its agent type; then the `subagent_model` binding for its model; then `subagents.default_seat`.
3. The context's `default_seat`.

The `agent_type` binding ranks above the `subagent_model` binding because a built-in agent that inherits its session's model names that model on the wire, and the model alone cannot tell it apart.

## default_context

`default_context` names the context the gateway uses when a request names none. It is stored once, in `llm_default_context`. It must name a context that the route set holds.

## Validation: Routes.Replace

`Routes.Replace` replaces the whole route set: the default context, every seat, every context and every binding. It validates the whole set before it writes anything, and either stores all of it or none of it. The rules are ported from the gateway's registry validation, patchbay at commit 4c28c9f: its `registry.validateDocument` and the seat and context checks under it (`registry.validateSeat`, `registry.validateContext`, `registry.validateSubagents`, `registry.validateUpstream`, `registry.validateAuthFields`). Routes.Replace applies each rule below, and refuses the whole set on the first failure.

1. `default_context` names a context in the set.
2. Every seat id and every context id matches `^[a-z0-9][a-z0-9-]*$`.
3. Every seat's `auth_mode` is `forward`, `inject` or `sigv4`. Every seat field is one its mode takes, and every field the mode requires is present. An unknown field is refused.
4. Every seat passes its field rules in [Field rules](#field-rules).
5. Every context's `default_seat` names a seat.
6. Every `model` binding's selector is non-empty, and names a seat.
7. A `model` binding whose selector contains `claude`, in any letter case, may not name an inject seat.
8. When a context has `subagents`, its `default_seat` names a seat. Its `agent_type` keys are header-observable: 1 to 256 printable ASCII bytes with no space. Its `agent_type` and `subagent_model` values name seats. Its `subagent_model` selectors are non-empty.
9. Every seat a subagent binding or a subagent default names, if it is an inject seat, has a non-empty `model`, and that model does not contain `claude`.
10. A context's `account`, when set, names an account in the store. Routes.Replace refuses an account id that names none.

The gateway's `extra_body`, `max_output_tokens` and `capture_bodies` seat fields are not in orrery's seat, so these rules do not check them. The gateway also refuses, at request time, a model name that is another seat's upstream rewrite target. That refusal belongs to the gateway, and Routes.Replace does not make it.

## Account use and removal

- A context uses an account when its `account` field names that account.
- `Accounts.Remove` refuses an account that any context uses. Its refusal names the contexts, and never the token. To remove such an account, first replace the route set with that context's `account` unset.
- `Accounts.Remove` removes an account that no context uses, and its token goes with its row.

## Token handling

- Tokens are stored in plain text in `orrery.db`. Encryption is deferred.
- A token goes only to `https://api.anthropic.com`. The only seat that sends an account token is a forward seat, and Routes.Replace admits a forward seat only with that exact upstream. No inject or sigv4 seat sends an account token. A probe that uses a token, such as the limits probe below, sends it only there too.
- No answer, error or log carries a token. Lists show the fingerprint, and never the token.
- `store.Reader.RoutesSnapshot` is the only read that returns a token. Every consumer that needs one, the gateway and any orreryd probe, calls it.

## Who produces and consumes each part

| Part | Later epic | Role |
|---|---|---|
| Accounts: add, list and remove | accounts and routes screens | produces, through `Accounts` |
| The route set: seats, contexts, bindings and default context | accounts and routes screens | produces, through `Routes.Replace` |
| A context's `account` (the switch) | accounts and routes screens | produces. The switch sets the context's `account` with `Routes.Replace`. |
| Seats, contexts, bindings, default context and account tokens | gateway (`orrery gateway`, on 127.0.0.1:4110) | consumes, through `store.Reader.RoutesSnapshot` |
| Each account and its token | limits | consumes. It probes each account with that account's token, through `store.Reader.RoutesSnapshot`, and sends the token only to `https://api.anthropic.com`. |
| Usage per account | inbox band | consumes. It shows the usage the limits probes read, for each account, labelled with the account's name and fingerprint. |
| The gateway's routes file (`routes.json`) | cutover | consumes once. It imports the file into the store, checking it with the rules in [Validation](#validation-routesreplace), then moves the gateway's traffic from the file to the store. The file is not copied into the repository. |
