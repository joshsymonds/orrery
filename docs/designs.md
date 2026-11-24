# Orrery design record (frozen)

Status: **frozen**. Task T1 of epic `orrery-0668c87/orrery-design-record` defines this record. Later epics build on the names and rules below. A change to any name or rule here is a change to the record and needs its own decision.

A design is the set of contracts that one conversation works, in the order they start. A contract can start after another contract of its design, and its epic then waits, held, until that contract ships. This file defines the design, its membership, its starts-after edges, the held epic, the state phrase that tells a design's progress, where each piece is reached, and which later epic uses each. It defines no screen: the stage's client contracts draw the tree.

This record builds on the contract record of [docs/contract.md](contract.md): its `contracts` table, its states `proposed`, `accepted` and `set_aside`, and the epic phases named in its Epics section. This record uses those names as they are.

## 1. Designs

A design has four parts:

| Part | Value |
|---|---|
| `id` | non-empty text. A contract or a design holds it, and no two hold the same id (section 2). |
| `title` | non-empty single-line text. |
| `conversation` | the sessions row of the conversation that called `design_create`. |
| `contracts` | the design's contracts, in position order (section 2). They may sit in more than one repository. Each contract keeps its own repository, and the design has none. |

`design_create` takes `id` and `title`. It stores the design with the caller's sessions row as its `conversation`, and it answers the design read back (section 6). Its refusals are checked in the order shown, and nothing is stored when one holds:

| Code | When | Message |
|---|---|---|
| `permission_denied` | the caller resolves to no session | `caller: resolves to no session` |
| `invalid_argument` | `id` is empty | `id: empty` |
| `invalid_argument` | `title` is empty | `title: empty` |
| `already_exists` | a contract or a design holds `id` | `id: <id> is already held by a contract or a design` |

## 2. Membership

- A contract belongs to at most one design. It joins a design when `contract_propose` names that design in its `design` argument. No other call joins a contract to a design or moves it, so membership is set once, at proposal.
- A contract's position is the order in which it joined its design, counted from 1. The first contract to join a design has position 1. Contracts do not leave a design, so a design's positions run 1, 2, ... N with no gaps.
- A contract proposed with no design reads as a design of one. That design is not stored. Its `id` is the contract's id, its `title` is the contract's title in its newest version, its `conversation` is the sessions row that proposed the contract, and its `contracts` is that one contract at position 1.
- Contracts and designs share one id namespace. `design_create` refuses an id that a contract holds. `contract_propose` refuses an id that a design holds. A design of one has its contract's id, so no explicit design can take it.

`contract_propose` takes an optional `design`, the id of an explicit design. An empty `design` means no design, so the contract reads as a design of one. It is refused as `ProposeContract` refuses any proposal (see [docs/contract.md](contract.md), Epics, Contracts), and also by these checks, in this order:

| Code | When | Message |
|---|---|---|
| `not_found` | `design` names a design that is not stored as an explicit design, which includes a contract's id | `design: no design <design>` |
| `already_exists` | `id` is held by a design | `id: <id> is already held by a contract, an epic or a design` |

## 3. Starts-after edges

The edge "contract C starts after contract A" is stored as the pair (C, A). A is a predecessor of C. An edge joins two contracts of one design. Both contracts may sit in different repositories.

`design_order` takes `design`, `contract` (C) and `after` (A). It refuses the edge in this order, and stores nothing when one holds:

| Code | When | Message |
|---|---|---|
| `not_found` | `design` names no explicit design | `design: no design <design>` |
| `failed_precondition` | `contract` or `after` is not a member of `design`. An edge across designs is refused here, and so is an edge to a contract that is a design of one. | `contract: <id> is not in design <design>` |
| `invalid_argument` | `contract` and `after` are the same contract, a self edge | `contract: self edge` |
| `failed_precondition` | `after` already starts after `contract`, directly or through a chain of edges, so the new edge would close a cycle | `order: <after> already starts after <contract>` |

- Adding an edge that is already stored changes nothing. The call succeeds and writes nothing.
- No call removes an edge.
- An edge takes effect at epic creation only (section 4). It changes nothing about an epic already created.
- `design_order` answers the design read back (section 6), with the edge in its contracts' `predecessors`.

## 4. Held epics

### 4.1 Predecessors and their state

The predecessors of contract C are the contracts A with an edge "C starts after A". Each predecessor is in one of three states, read at each check:

| State | When |
|---|---|
| shipped | its contract's epic (the epic with the same id) has phase `released`, `done` or `closed_shipped` |
| will not ship | its contract is `set_aside`, its epic is closed unshipped (phase `closed_superseded` or `closed_abandoned`), or its epic ended with gaps (phase `ended_with_gaps`) |
| not yet | anything else: no epic yet, the contract proposed or accepted, or its epic in any other phase. A `clean` epic has not shipped until it is released. |

A predecessor that will not ship can ship later, for example when an epic that ended with gaps is released. The state is read again at each check.

### 4.2 Epic creation

`epic_create` of an accepted contract C runs the checks it runs for any epic, except that the request's `base` is not stored on a held epic (see below). Then:

- When C has no predecessor, or every predecessor is shipped, the epic starts exactly as it does now: phase `accepted`, its created trigger runs a planner turn, and nothing in that path changes.
- Otherwise the epic is stored in phase `held`, with `created` and `changed` the time of the call, and `phase_at` set as for any phase write. A held epic:
  - stores no base. Its base is set when it starts (4.4).
  - settles its steerer as `CreateEpic` settles it.
  - keeps its created trigger pending. No planner turn runs on it, and no build runs, since a build needs a base.
  - gets the question of 4.3 at once when a predecessor will not ship.

### 4.3 The held question

When a held epic has a predecessor that will not ship, its steering conversation gets one question, with label `held` and two choices:

- `start` starts the epic now (4.4), whether or not its predecessors have shipped.
- `close` closes the epic abandoned, as `CloseEpic` with kind `abandoned` closes it (`closed_abandoned`), with an empty `by`.

The question is asked once per epic. It is asked when the epic first has a predecessor that will not ship, at creation or at a later check. A later check that finds the same state asks nothing more. When the epic leaves `held` by any other write, such as its start or a `CloseEpic`, its open held question is closed unanswered.

### 4.4 Start

A held epic starts, moving from `held` to `accepted`, in any of these cases:

- Its last predecessor ships. The shipping write runs a check, and the check starts the epic.
- Its held question is answered `start`.
- A check at orreryd's start finds that every predecessor has shipped, which happens when a predecessor shipped while orreryd was down.

To start, orreryd fetches origin main, so the epic's base is the newest origin main: the commit that origin main points to when the start runs. The base, the phase `accepted` and `phase_at` are written in one write, and the created trigger then runs its planner turn as it does for any epic that has a base. A `start` answer and an automatic start use the same fetch. If the fetch fails, nothing is written. The epic stays held, and a `start` answer that could not start it is kept and tried again at the next check.

### 4.5 Survival

The hold, its held question and each start are stored. An orreryd restart leaves a held epic held, with its question open if it is open. At orreryd's start, each held epic gets a check, so a held epic whose predecessors all shipped while orreryd was down starts, and a held epic with a will-not-ship predecessor and no question yet gets its question. A started epic stays started.

### 4.6 Epics with no predecessor

An epic with no predecessor starts exactly as before. The held phase, the check at each predecessor's change, and the start rule apply only to an epic whose contract has predecessors.

## 5. The state phrase

Each design has one phrase, which serve gives. The phrase reads the design's **live** contracts: those whose state is `proposed` or `accepted`, not `set_aside`. A contract's epic is the epic with its id. An epic is **ended** when its phase is one of `met_at_base`, `clean`, `ended_with_gaps`, `released`, `done`, `stopped`, `closed_superseded`, `closed_shipped` or `closed_abandoned`.

The counts are:

- N: the live contracts.
- A: the live contracts whose state is `accepted`.
- R: the live contracts whose epic is shipped (4.1).
- H: the live contracts whose epic is held.
- E: the live contracts that have an epic.

The phrase is the first row that applies, checked in this order:

| # | Phrase | Applies when |
|---|---|---|
| 1 | `no contracts yet` | the design has no contracts |
| 2 | `set aside` | every contract is `set_aside` |
| 3 | `held: a predecessor will not ship` | some live contract's epic is held and has a predecessor that will not ship |
| 4 | `drafting: A of N accepted` | some live contract is `proposed` |
| 5 | `released` | every live contract's epic is shipped |
| 6 | `building: R of N released` | some live contract's epic exists and is neither held nor ended |
| 7 | `waiting: H held` | some live contract's epic is held |
| 8 | `accepted: E of N started` | anything else |

The phrase is computed from the stored state at each read, so the same stored state always gives the same phrase.

## 6. Where each piece is reached

### 6.1 The design read back

Every call that answers a design answers this shape: `id`, `title`, `conversation`, `phrase` (section 5), and `contracts`. Each contract, in position order, has `id`, `repository`, `position`, `state` (`proposed`, `accepted` or `set_aside`), `epic` (its epic's phase, or empty when it has none), and `predecessors` (the ids of its predecessors by edge).

### 6.2 MCP tools

| Tool | Arguments | Answers | Engine call |
|---|---|---|---|
| `design_create` | `id`, `title` | the design read back | `Designs.CreateDesign` |
| `design_get` | `id` | the design read back. The id of a contract answers that contract's design of one. An unknown id is `not_found`: `design: no design <id>`. | `Designs.GetDesign` |
| `design_list` | none | every design, explicit and of one, ordered by `id` | `Designs.ListDesigns` |
| `design_order` | `design`, `contract`, `after` | the design read back, after the edge is stored (section 3) | `Designs.OrderDesign` |
| `contract_propose` | its arguments, with `design` | as `ProposeContract` answers (section 2) | `Contracts.ProposeContract` |

### 6.3 Engine Designs service

`orrery.engine.v1.Designs` is declared in `proto/orrery/engine/v1/design.proto`. It is served on the session socket beside `Epics` and `Contracts`, with the same socket mode and Connect rules, and implemented in `enginehost.designService`. Its calls are `CreateDesign`, `GetDesign`, `ListDesigns` and `OrderDesign`, each with the arguments and answer of section 6.2. `CreateDesign` reads the caller's sessions row as `CreateEpic` does, for `conversation`.

### 6.4 Serve

Serve's Connect API has the service `orrery.v1.Designs` with two calls, `List` (`POST /orrery.v1.Designs/List`) and `Get` (`POST /orrery.v1.Designs/Get`). `List` answers every design, explicit and of one, ordered by `id`. `Get` answers one design by its id, with the read back of section 6.1, and an unknown id is `not_found`. Both read through the store's read-only reader, as `Contracts.List` and `Contracts.Get` do.

### 6.5 The `designs` fields

- `Sessions.ListHeadless` answers each headless session with a `designs` field. It holds the designs whose `conversation` is that session, explicit and of one, each as a design summary: `id`, `title` and `phrase`.
- The Epics list (`Epics.List`) answers each epic with a `designs` field. It holds one entry, the design that the epic's contract belongs to, as a design summary.

## 7. Which later epic uses each piece

| Epic | Uses |
|---|---|
| `orrery-stage-tools` | designs (`design_create`, `design_get`, `design_list`, `design_order`), membership (`contract_propose`'s `design`), and starts-after edges |
| `orrery-stage-records` | designs, membership and starts-after edges, as the rows of section 8 |
| `orrery-stage-web-tree` | the Designs list and get, the `designs` fields, and the state phrase |
| `orrery-stage-android-tree` | the Designs list and get, the `designs` fields, and the state phrase |
| the held and pages successors of `orrery-stage-web-tree` and `orrery-stage-android-tree` | the Designs list and get, the `designs` fields, and the state phrase |
| the next design | held epics (section 4). It is the first design whose epics are held. |

This design's own epics are started by hand by the Director, so they are not held, and none of them uses the held phase.

## 8. Stored rows

- `designs`: `id` (the primary key), `title`, `conversation` (the sessions row id) and `created` (unix seconds).
- `design_contracts`: `contract` (the primary key, the contract's id) and `design`, with `position`, unique within `design`. A contract with no row here is a design of one.
- `design_edges`: `contract` and `after`, the primary key. Each row joins two contracts of one design. The write that adds it checks membership and the cycle rule of section 3 in the same transaction.
- A held epic is an `epics` row with phase `held` and no base. Its held question is an epic question with label `held`.
- Each write that adds a design, a member or an edge is one transaction.
