# Usage ledger

This document specifies orrery's LLM ledger: the record of each LLM request that orrery carries, with the usage limit claims each answer reported. It also says how the reads that answer usage come from it. Four later epics use it. The gateway writes the ledger. The limits epic's probes write limit readings through the gateway. The inbox band reads readings. The session views read session totals.

The schema is the embedded migration [`00001_ledger.sql`](../internal/ledger/migrations/00001_ledger.sql). The Go types are in [`internal/ledger`](../internal/ledger/ledger.go): the writer's `Start` and `Finish` in [`write.go`](../internal/ledger/write.go), and the reader's `Readings`, `SessionTotals` and `Spend` in [`reader.go`](../internal/ledger/reader.go) and the files beside it.

Conventions used throughout:

- Times are unix seconds: `started_at`, `completed_at`, `observed_at` and `reset_at`.
- Costs are integers in nano US dollars, where one nano-USD is 10⁻⁹ USD. No cost is a float.
- NULL means unknown, never zero. A NULL cost is a cost the ledger does not know.

## 1. The file

The ledger is `ledger.db`, its own SQLite file in the directory of `orrery.db`. [`ledger.Path`](../internal/ledger/ledger.go) returns it from the store's path. The two files share no tables, and no statement reads or writes both.

- **Writer.** [`ledger.Open`](../internal/ledger/ledger.go) creates the file when it is absent and applies the embedded migrations in [`MigrationFS`](../internal/ledger/ledger.go). The ledger numbers its migrations from `00001` on its own sequence, apart from the store's.
- **Version refusal.** When the file records a migration newer than the binary embeds, `Open` returns [`ledger.ErrTooNew`](../internal/ledger/ledger.go) and migrates nothing. An older binary therefore refuses a newer ledger; it never migrates one.
- **Reader.** [`ledger.NewReader`](../internal/ledger/reader.go) opens nothing. Each read opens the file read-only, never creates it, and closes it again.
- **Absent ledger.** When the file is absent, each read returns nil with no error. An absent ledger reads as empty.

## 2. The request row

One request is one row in `requests`. [`Start`](../internal/ledger/write.go) inserts it when the request starts, and returns the row's `id`. [`Finish`](../internal/ledger/write.go) completes that row with the answer's values. A row that has not finished is in flight: its `completed_at` is NULL, its `resolved_model` is `''`, its tokens are 0 and its costs are NULL.

| Column | Set by | Meaning |
|---|---|---|
| `id` | Start | The row id that `Start` returns and `Finish` takes. |
| `started_at` | Start | When the request started. |
| `completed_at` | Finish | When the request completed. NULL while in flight. |
| `account` | Start | The account the request is billed under. See section 4. |
| `seat` | Start | The seat the request's spend counts to. Section 6 groups spend by it. |
| `context` | Start | The context the request arrived under. |
| `requested_model` | Start | The model the caller asked for. |
| `resolved_model` | Finish | The model that answered. `''` while in flight. |
| `claude_session` | Start | The Claude Code session id of the caller. Section 5 joins on it. |
| `agent_id` | Start | The id of the agent that made the request. Empty when there is none. |
| `agent_type` | Start | The type of that agent. Empty when there is none. |
| `input_tokens` | Finish | Input tokens. 0 while in flight. |
| `output_tokens` | Finish | Output tokens. 0 while in flight. |
| `cache_read_tokens` | Finish | Cache-read tokens. 0 while in flight. |
| `cache_write_tokens` | Finish | Cache-write tokens. 0 while in flight. |
| `billed_cost_nano_usd` | Finish | The billed cost. NULL when unknown. |
| `list_cost_nano_usd` | Finish | The cost at list price. NULL when unknown. |
| `list_input_nano_usd` | Finish | The input tokens' cost at list price. NULL when the request has no list cost. |
| `list_output_nano_usd` | Finish | The output tokens' cost at list price. NULL when the request has no list cost. |
| `list_cache_read_nano_usd` | Finish | The cache-read tokens' cost at list price. NULL when the request has no list cost. |
| `list_cache_write_nano_usd` | Finish | The cache-write tokens' cost at list price. NULL when the request has no list cost. |

**List cost by kind.** The four `list_*_nano_usd` columns split `list_cost_nano_usd` by token kind. Cache writes at the 5-minute price and at the 1-hour price are priced apart and both count in `list_cache_write_nano_usd`. For a request with a list cost, the four sum to `list_cost_nano_usd`. A request with no list cost has NULL in all five columns. [`ratecard`](../internal/ratecard/ratecard.go) computes the four from the Claude rate card, and `Finish` stores them as given.

`Start` also carries `CallerAccount`, which is not a column. It feeds the account when the context has none (section 4).

**Limit claims.** `request_claims` holds one row for each claim that the answer reported, keyed by `(request_id, claim)`. Its columns are `claim`, the claim's name; `utilization`, which is NULL when the claim gives none; `reset_at`, which is NULL when the claim names no reset; and `status`. A request whose answer reported no claims has no rows here.

**One transaction.** `Finish` writes the request's completion fields, its `request_claims` rows, and the changes to `limit_claim_models` and `limit_readings` that section 3 gives, in one transaction. A request and what it implies for the readings are never seen apart.

## 3. Limit readings

A limit reading is the latest state of one usage limit claim for one account. `limit_readings` has one row for each `(account, claim)`, and [`ledger.Reading`](../internal/ledger/read_readings.go) returns it with these fields:

| Field | Column | Meaning |
|---|---|---|
| `Account` | `account` | The account the reading belongs to. |
| `Claim` | `claim` | The claim's name, such as `5h`, `7d_oi` or `overage`. |
| `Utilization` | `utilization` | The utilization the answer reported. NULL when the claim gives none. |
| `Reset` | `reset_at` | The reset, in unix seconds. NULL when the claim names none. |
| `Status` | `status` | The status the answer reported, as the answer gave it. |
| `Observed` | `observed_at` | The `completed_at` of the answer that wrote the reading. |
| `Dropped` | `dropped` | The drop mark: 1 when a later answer omitted the claim. |

The row also keeps `request_id`, the request whose answer wrote it. `Readings` does not return that column.

### 3.1 Writing a reading

`Finish` applies these three steps for each answer, in the same transaction as the request row. Let *A* be the answer's account, *M* its resolved model, and *C* each claim it reports.

1. **Record the carrying model.** For each claim *C* the answer reports, `limit_claim_models` gains the row `(A, C, M)`. A model that has carried a claim stays in its set. The set is what makes a model's later omission count (step 3).
2. **Replace the reading.** For each claim *C* the answer reports, the row `(A, C)` is replaced with the answer's values, `observed_at` set to its `completed_at`, `request_id` set to this request, and `dropped` set to 0. The replacement happens only when the answer's `completed_at` is at or after the row's `observed_at`. An older answer leaves a newer row alone.
3. **Drop the readings it omits.** For each row `(A, C)` whose claim *C* the answer does not report, when *M* is in the carrying set for `(A, C)` and the answer's `completed_at` is after the row's `observed_at`, the row's `dropped` is set to 1. An answer from a model outside the carrying set never drops a reading.

The drop mark is cleared only by step 2, when a later answer reports the claim again.

### 3.2 When a reading is current

A reading is current when both of these hold:

- Its `dropped` mark is 0. No later answer from a model that has carried the claim omitted it.
- Its reset has not passed. A reading whose reset is NULL has no reset to pass, so it has not passed. A reading whose reset is later than the time of the read has not passed either.

[`Readings`](../internal/ledger/read_readings.go) takes no clock and filters nothing. It returns every row, with `Dropped` and `Reset` set as stored. The inbox band evaluates the two conditions at each read, with its own clock.

Three claims show how the rules apply:

- **`7d_oi` rides only Fable answers.** Only Fable models ever carry it, so its carrying set holds Fable models alone. Only a later Fable answer that omits `7d_oi` drops it. An answer from any other model neither carries the claim nor drops it, so its absence there says nothing.
- **`overage` is status-only.** Its reading has a status and no utilization and no reset. Its reset never passes, so it is current until a later carrying-model answer drops it.
- **`5h` comes and goes.** An answer from a carrying model can omit `5h`. That omission drops the `5h` reading, which then is not current until a later answer reports `5h` again.

## 4. The account

`requests.account` is the account the request is billed under. When the context names an account, that account is `Start.Account`. When the context has none, `Start.Account` is empty, and the row takes `Start.CallerAccount`, the caller's account uuid. Readings and `limit_claim_models` key on the same account value, so a request from a context with no account belongs to its caller's account.

## 5. The session join

Orrery sessions are the only sessions that the ledger joins. Each pane and headless session that orrery runs has a row in the `sessions` table of `orrery.db`, and that row's `claude_session` holds its Claude Code session id. A request joins to an orrery session when `requests.claude_session` equals a `sessions.claude_session`.

- **Orrery sessions only.** A Claude session that no `sessions` row carries has no session. No session view is built for it, and no read gives its requests per session. The ledger holds no per-session view of traffic that orrery did not start.
- **Empty ids join nothing.** A `sessions` row whose `claude_session` is empty joins no request. A session view must skip that row.
- **No SQL across files.** A session view reads the `sessions` row from `orrery.db`. It then calls [`SessionTotals`](../internal/ledger/read_session.go) on `ledger.db` with that `claude_session`. The join is the equality of the two strings.

`SessionTotals` returns one [`SessionTotal`](../internal/ledger/read_session.go) for each `(resolved_model, agent_type)` of the session's requests. A total holds the number of requests, the four token sums, and the billed and list cost sums. The cost sums skip rows whose cost is NULL. An in-flight request counts as a request with model `''` and zero tokens.

## 6. Spend per seat

`Spend(from, to)` answers what each seat spent over the half-open window `[from, to)`. A request is in the window when its `started_at` is at least `from` and less than `to`. Adjacent windows therefore share no request.

For each seat that has at least one request in the window, [`SeatSpend`](../internal/ledger/read_spend.go) holds:

- `BilledCostNanoUSD`: the sum of `billed_cost_nano_usd` over the seat's rows in the window, skipping NULL.
- `ListCostNanoUSD`: the sum of `list_cost_nano_usd` over the same rows, skipping NULL.
- `UnknownCostRequests`: the number of those rows whose billed cost or list cost is NULL. An in-flight request is one, since its costs are NULL.

A seat's totals are complete only when `UnknownCostRequests` is 0. Otherwise they are a lower bound.

## 7. Who writes and reads each part

| Part | Written by | Read by |
|---|---|---|
| The request row, from `Start` and `Finish` (section 2) | gateway | session views, through `SessionTotals`; the Usage API, through `Spend` |
| `request_claims` (section 2) | gateway, in `Finish` | none outside the ledger, which reads it to write readings |
| `limit_readings` (section 3) | gateway, in `Finish`. The limits epic's probes write their readings through the same path, so section 3 applies to a probe's answer too. | inbox band, through `Readings` |
| `limit_claim_models` (section 3) | gateway, in `Finish` | gateway, for the drop in step 3 |

The Usage API is the one the `Reader` comment names for spend. No epic in the list above reads spend.

## 8. Out of scope

This document does not build the gateway, the probes, the rate card, the inbox band or the session views. It says what each writes and reads. How a list-price figure is computed is outside this document: `Finish` takes the figure as given, and a NULL means it is unknown.
