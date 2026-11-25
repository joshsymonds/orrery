# LLM accounts and routes

This document specifies the LLM accounts orrery routes requests through, and the route registry the gateway serves: the seats a request may be sent to, the contexts that pick a seat, the bindings inside each context, and the default context. It is the reference the later llm-accounts epics build against. It defines what is stored, who writes and who reads it, which rules a registry must pass before it is stored, and which later epic produces or consumes each part.

It does not define how the gateway handles a request, how the screens look, or how the limits probe works. Those belong to the epics that build them. Code is named by symbol, never by line.

## Terms

- **Account**: one LLM account and the token that authenticates requests sent under it.
- **Seat**: one destination a request may be sent to, with the way it authenticates.
- **Context**: a routing table a request names. It holds a default seat, the model bindings, optional subagent routing, and an optional account.
- **Binding**: one selector mapped to a seat inside a context. The selector is a model name, a subagent's agent type, or a model name a subagent uses.
- **Route registry**: the seats, the contexts and the default context, stored and replaced as one unit.

## Owners and interfaces

`orreryd` is the one writer of the store. Every write to an account or to the route registry runs in orreryd, and nothing else opens the store for writing.

`orrery serve` reads accounts and routes from the store through `store.Reader`. It forwards each write to orreryd over the engine `Accounts` service on orreryd's session socket, and waits for orreryd's answer before it replies. Serve never writes the store.

`orrery gateway` on 127.0.0.1:4110 reads the route registry and the account tokens through `store.Reader.RoutesSnapshot`. That is the gateway's one read, and the only read in this spec that returns a token.

| Interface | Symbol | Used by |
| --- | --- | --- |
| Public accounts | `proto/orrery/v1/accounts.proto: Accounts`, with `Add`, `List`, `Rename` and `Remove` | clients of `orrery serve`, through the accounts screen |
| Public routes | `proto/orrery/v1/accounts.proto: Routes`, with `Get`, `Replace` and `SetContextAccount` | clients of `orrery serve`, through the routes screen and the switch |
| Writer | `proto/orrery/engine/v1/accounts.proto: Accounts`, with `AddAccount`, `RenameAccount`, `RemoveAccount`, `ReplaceRoutes` and `SetContextAccount` | `orrery serve` only, on orreryd's session socket; never on the public orrery serve API |
| Tables | `internal/store/migrations/00096_llm_accounts.sql`: `llm_accounts`, `route_registry`, `route_seats`, `route_contexts`, `route_bindings` | orreryd writes them; `store.Reader` reads them |
| Token read | `store.Reader.RoutesSnapshot` | the gateway |

The engine service has no read rpc. Reads come from the store, and writes go to orreryd.

## Account

An account has these fields.

- **id**: opaque, 32 lowercase hex characters, minted by orreryd when the account is added. A caller never chooses it. Stored as `llm_accounts.id`.
- **name**: as the owner gave it. Required. Rename changes it and nothing else.
- **provider**: `anthropic`, and nothing else for now. Add refuses any other provider. A second provider needs a change to this section first.
- **plan**: free text. It may be empty, and is stored as `''` when none is given.
- **token**: the bearer a `claude setup-token` run prints, pasted by the owner. It must start `sk-ant-oat`, which is what `claude setup-token` prints. An `sk-ant-api` API key is refused as one, and any other token that does not start `sk-ant-oat` is refused as not a setup-token. Required, and kept as given. It is stored in plain text in the `token` column of `llm_accounts` in orrery.db. Encryption is deferred. When it lands, this section changes with it.
- **fingerprint**: the first 12 lowercase hex characters of the SHA-256 of the token, computed by orreryd when the account is added. It is the only thing any list or answer shows of the token. Stored as `llm_accounts.fingerprint`.
- **created**: the unix second orreryd stored the account, as `llm_accounts.created`.

The rules that keep the token where it belongs are these.

1. No rpc returns a token. Add, List and Rename, and their engine forms, carry the account without its token. Remove answers with no account.
2. No error and no log line carries a token. An error names an account by its id and its name.
3. The one token read this spec defines is `store.Reader.RoutesSnapshot`, and only the gateway calls it.
4. A stored token goes only to `https://api.anthropic.com`. Only a forward seat sends an account's token, and Routes.Replace admits a forward seat only when its upstream is exactly that URL. The limits probe, when it needs a token, sends it to that URL and nowhere else.

The account operations are these.

- **Add** checks that the name is present, that the provider is `anthropic`, and that the token is present with no interior whitespace. Then it checks that the trimmed token starts `sk-ant-oat`: a token that starts `sk-ant-api` is refused as an API key, and any other token is refused as not a setup-token. The token check runs after the name, provider and whitespace checks. orreryd mints the id, computes the fingerprint, stores the row and returns the account without its token.
- **List** returns every account, created ascending and then by id. Serve reads it from the store.
- **Rename** gives the named account a new name and returns it as it stands. An id that names no account is refused.
- **Remove** deletes the named account. It refuses an account that a context uses, which means a context whose `account` names it. The refusal names the context ids, and the account stays stored. A removal therefore never leaves a context with a dangling account.

## Seat

A seat has an `id` and an `auth_mode`. The id follows the id grammar in the validation section. The auth mode is `forward`, `inject` or `sigv4`. Each mode takes only some fields.

| Field | forward | inject | sigv4 |
| --- | --- | --- | --- |
| `upstream` | yes, exactly `https://api.anthropic.com` | yes | no |
| `model` | yes | yes | yes |
| `cache_ttl` | yes | no | no |
| `max_input_tokens` | yes | yes | yes |
| `strict_structured_output` | yes | no | no |
| `billing` | no | yes | no |
| `api_key_env_file` | no | yes | no |
| `speed` | no | yes | no |
| `insecure` | no | yes | no |
| `strip_cache_control` | no | yes | no |
| `aws_profile` | no | no | yes |
| `aws_region` | no | no | yes |
| `model_map_env_file` | no | no | yes |
| `strip_betas` | no | no | yes |

A field a mode does not take must be empty, `false` or `0` when a registry is replaced. A registry that sets one is refused.

What the fields mean:

- **upstream**: the URL a seat's requests are sent to.
- **model**: the model the seat sends for. On an inject seat it is the model the request is rewritten to, and it is required on an inject seat that is a subagent destination.
- **cache_ttl**: the prompt-cache lifetime a forward seat asks for, `1h` or empty.
- **max_input_tokens**: the largest input the seat sends, in tokens. `0` when none is set. Never negative.
- **strict_structured_output**: `true` when a forward seat enforces strict structured output.
- **billing**: the billing class an inject seat declares, `subscription`, `metered` or `self_hosted`. Forward and sigv4 seats derive their class from the credential or the AWS account, so they take no billing field.
- **api_key_env_file**: the name the gateway validates against the environment-variable grammar, `^[A-Za-z_][A-Za-z0-9_]*$`. It is taken only by an inject seat.
- **speed**: the speed tier an inject seat asks for, `fast` or empty.
- **insecure**: `true` allows an inject seat's upstream to be plain http on a host that is not loopback.
- **strip_cache_control**: `true` removes the cache-control markers from an inject seat's requests.
- **aws_profile**: the AWS profile a sigv4 seat signs with. Required on sigv4. Lowercase letters, digits, dashes, dots and underscores only. An example is `example-profile`.
- **aws_region**: the AWS region a sigv4 seat signs for. Required on sigv4. Lowercase letters, digits and dashes only.
- **model_map_env_file**: the env file that maps a sigv4 seat's model names. Required on sigv4.
- **strip_betas**: the anthropic-beta flags a sigv4 seat removes from its requests. A JSON array in the store, `[]` when it removes none.

Examples, with invented hosts and names:

```json
{"id": "example-forward", "auth_mode": "forward",
 "upstream": "https://api.anthropic.com", "model": "", "cache_ttl": "1h"}

{"id": "example-inject", "auth_mode": "inject",
 "upstream": "https://models.example.test", "model": "example-model",
 "billing": "metered", "api_key_env_file": "EXAMPLE_API_KEY"}

{"id": "example-sigv4", "auth_mode": "sigv4", "aws_profile": "example-profile",
 "aws_region": "example-region-1", "model_map_env_file": "EXAMPLE_MODEL_MAP",
 "strip_betas": ["example-beta"]}
```

## Context

A context has these fields.

- **id**: the id a request names. It follows the id grammar.
- **default_seat**: the seat the context's requests go to when no binding names another. Required, and it names a seat.
- **models**: the model bindings, a map from a model selector to a seat id. Each selector is non-empty and each seat exists.
- **subagents**: optional. Unset means the context keeps no subagent routing. Set, it has its own `default_seat` (required, naming a seat), an `agent_types` map from an agent type to a seat id, and a `models` map from a model selector to a seat id. The gateway applies these to the requests it marks as subagent traffic.
- **account**: optional, the id of a stored account. Unset, empty, means the caller's own login. Set, the context's forward seats send that account's token, and no other seat sends it. A context that names an account but has no forward seat keeps the account and sends nothing with it.

## Binding

A binding is one selector mapped to one seat inside one context. A binding has a scope, which says what the selector names.

- `model`: a model name. It is a key of the context's `models`.
- `subagent_agent_type`: a subagent's agent type. It is a key of the subagents' `agent_types`. It must be header-observable: 1 to 256 bytes of printable, non-space ASCII.
- `subagent_model`: a model name for subagent traffic. It is a key of the subagents' `models`.

The store keeps a binding as one row of `route_bindings`, keyed by context, scope and selector. A context keeps one binding per scope and selector, so a repeated binding is the same row. The seat a binding names must exist.

## default_context

The registry's `default_context` is the id of the context a request names by none. It must name a context in the same registry, so Routes.Replace refuses a registry that names none. It is stored in the one row of `route_registry`.

Before any registry is stored, `route_registry` has no row and the registry reads empty: no seats, no contexts, and `default_context` set to `''`. Routes.Get returns that empty registry.

## Validation Routes.Replace applies

Routes.Replace stores the whole registry or refuses it whole. The rules below are ported from the gateway's registry validation in patchbay, `internal/registry/validate.go` at 4c28c9f, and the registry rules run in the order listed. The first failed rule refuses the registry. Each rule that is not a port from the gateway is marked (orrery).

1. **Ids.** A context id, a seat id and the default context id each match `^[a-z0-9][a-z0-9-]*$`. An id is refused when it is empty, and a seat id or context id that appears twice is refused. (orrery: the duplicate rule, because the store keys each by id.)
2. **Default context.** `default_context` names a context in the registry.
3. **Seat fields by mode.** Each seat's `auth_mode` is `forward`, `inject` or `sigv4`. Each field the seat sets is one its mode takes, as the table in the seat section says. (orrery: the mode rule as a whole. The gateway refuses these pairings one field at a time.)
4. **Upstream.** For a forward or inject seat, `upstream` is an absolute URL with a scheme and a host. It has no user information, no query and no fragment. Its scheme is `https`, or `http` only for the host `127.0.0.1` or `localhost`. An `http` upstream on any other host needs `insecure`, and `insecure` is refused for an `https` upstream and for a loopback `http` upstream. A sigv4 seat sets no `upstream`, because its endpoint comes from `aws_region`.
5. **Forward seats.** `upstream` is exactly `https://api.anthropic.com`. The seat sets no `api_key_env_file` and no AWS field.
6. **Inject seats.** `api_key_env_file` is set and matches `^[A-Za-z_][A-Za-z0-9_]*$`. `model`, when set, does not contain `claude` in any letter case. `billing` is set, and it is one of `subscription`, `metered` or `self_hosted`.
7. **Sigv4 seats.** `upstream` is empty, `api_key_env_file` is empty, and `insecure` is false. `aws_profile`, `aws_region` and `model_map_env_file` are each set and non-empty after trimming. `aws_region` uses only lowercase letters, digits and dashes. `aws_profile` uses only lowercase letters, digits, dashes, dots and underscores.
8. **Speed and billing.** `speed` is `fast` or empty, and only an inject seat sets it. `billing` is only an inject seat's field, and an inject seat sets it.
9. **strip_betas.** Only a sigv4 seat sets it.
10. **cache_ttl.** `cache_ttl` is `1h` or empty, and only a forward seat sets it. The gateway also refuses it alongside `strip_cache_control`. Here a forward seat takes no `strip_cache_control`, so rule 3 refuses that pairing.
11. **max_input_tokens.** Never negative.
12. **Context models.** Each `models` selector is non-empty and names a seat. An inject seat is never bound to a selector that contains `claude` in any letter case.
13. **Context default seat.** `default_seat` names a seat.
14. **Subagents.** When a context has `subagents`, its `default_seat` names a seat, and each seat it routes to is a valid subagent destination. Each `agent_types` key is a header-observable agent type, 1 to 256 bytes of printable, non-space ASCII, and names a seat. Each `models` selector is non-empty and names a seat.
15. **Subagent destinations.** A subagent destination that is an inject seat needs a non-empty `model`, and that `model` does not contain `claude` in any letter case.
16. **Context account.** When a context's `account` is set, it names a stored account. (orrery: the reference rule. It keeps a context from naming an account that does not exist, and Accounts.Remove keeps a stored account from being removed under a context.)

A refusal names the context, the seat or the selector at fault, and never a token. Because the registry carries no token, a refusal cannot carry one.

Routes.SetContextAccount sets only one context's `account`. It refuses an unknown context, and an account that is neither empty nor a stored account. Its answer is the registry as it stands.

## Importing a routes file

`orrery routes import FILE` stores a routes file as the route registry. The file is the registry JSON document that patchbay reads: `default_context`, and `seats` and `contexts` as objects keyed by id. Its seat and context members are the ones this document defines, and a context's `subagents` takes `default_seat`, `agent_types` and `models`. A context names an account by its id, and a routes file carries no token.

The command parses the file, then calls `Accounts.ReplaceRoutes` on orreryd's session socket. orreryd holds the parsed registry to the rules above, and replaces every stored route in one write. A file the parser or those rules refuse stores nothing, and the stored routes stay as they were. The parser refuses the whole file when:

- a member name repeats within one object, so a repeated name cannot silently shadow the value above it;
- data follows the registry document;
- a required member is missing;
- a member has the wrong type; or
- a member orrery does not store is present. That covers `extra_body`, `max_output_tokens` and `capture_bodies`, which patchbay reads and orrery does not keep, and any other member that is not one of this document's.

On success the command prints the number of seats and contexts the store holds, and exits 0. A refusal prints its error and exits 1.

## Tokens and the gateway

`store.Reader.RoutesSnapshot` reads, in one read transaction that writes nothing, the route registry and the token of each account a context names. The gateway serves each request from that one snapshot. A context whose `account` is unset carries no token, and its forward seats send the caller's own login.

The gateway sends each token it reads only to `https://api.anthropic.com`, as the forward rule requires. `RoutesSnapshot` is the one token-bearing read this spec defines. A later epic that needs a token another way must add that read to this document before it ships, and it holds that read to the token rules above.

The limits probe is such a case. It probes each account, and `RoutesSnapshot` returns a token only for an account a context names, so an account no context uses has no token in that read. The limits epic adds its token read to this document before it ships.

## Who produces and consumes each part

| Part | Produced by | Consumed by |
| --- | --- | --- |
| Account (id, name, provider, plan, fingerprint) | accounts and routes screens, through Accounts.Add, Rename and Remove | inbox band, which shows usage per account by id, name and fingerprint and never by token; limits, which probes each account |
| Account token | accounts and routes screens, through Accounts.Add | gateway, through `store.Reader.RoutesSnapshot`; limits, which needs a token read this spec does not yet define (see Tokens and the gateway) and sends the token only to `https://api.anthropic.com` |
| Seat | accounts and routes screens, through Routes.Replace; cutover, which imports routes.json once through the same rules | gateway, through `store.Reader.RoutesSnapshot` |
| Context and binding | accounts and routes screens, through Routes.Replace; cutover, which imports routes.json once through the same rules | gateway, through `store.Reader.RoutesSnapshot` |
| Default context | accounts and routes screens, through Routes.Replace; cutover | gateway, through `store.Reader.RoutesSnapshot` |
| Context account | the switch in the accounts and routes screens, through Routes.SetContextAccount | gateway, which uses the token for the forward seats of that context |

Cutover leaves every context's account unset when it imports routes, so each forward seat sends the caller's own login until the switch sets an account. Cutover then moves traffic to the gateway.

## Out of scope

This document is the spec only. It leaves to the later epics the gateway's request handling, the accounts and routes screens, the switch button, the limits probe, the inbox band and the cutover import. Token encryption is deferred, and this spec changes no patchbay code.
