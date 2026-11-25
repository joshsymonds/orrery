// The Use moves: the moves that set the account of several named contexts to one account, and the undo of a move. They write
// through the Routes client, one SetContextAccount per context, in order, and stop at the first call that does not answer.
// After such a failure they read Routes.Get and say which contexts stand at their wanted account and which do not, so a call
// that errored after it applied is reported as moved. They draw nothing; the page shows what they return.

import type { createClients } from "./client";
import { couldNotTell, nothingChanged, runWrite, type WriteOutcome } from "./accountswrite";
import type { RouteRegistry } from "./gen/orrery/v1/accounts_pb";

// RoutesClient is the Routes client the moves write through, as createClients makes it.
export type RoutesClient = ReturnType<typeof createClients>["routes"];

// Move is one context a move names, with the account it held before the move. An undo sets the context back to from.
export interface Move {
  context: string;
  from: string;
}

// MoveResult is how a move or an undo ended: done, with the contexts it set; partial, when a call failed, with the reason,
// the planned contexts Routes.Get shows at their wanted account (moved) and the rest (notMoved); or unknown, when the failed
// call could not be read back, so the page says it could not tell.
export type MoveResult =
  | { kind: "done"; moved: Move[] }
  | { kind: "partial"; reason: string; moved: Move[]; notMoved: Move[] }
  | { kind: "unknown"; reason: string };

// Failure is a call that did not answer with a result: a refusal, or a call with no answer.
type Failure = Exclude<WriteOutcome<unknown>, { kind: "done" }>;

// Step is one planned context of a move or an undo: the account it is to be set to, and whether it is skipped rather than
// called.
interface Step {
  move: Move;
  account: string;
  skipped: boolean;
}

// accountIn is the account the registry holds for the context with id, or undefined when it holds no such context.
function accountIn(registry: RouteRegistry, id: string): string | undefined {
  return registry.contexts.find((context) => context.id === id)?.account;
}

// stillOn is whether the context of move still holds the account move names: it has an account, and it is move's from.
// A context on its own login, which has no account, or one changed since the plan was made, is not moved.
function stillOn(latest: RouteRegistry, move: Move): boolean {
  const account = accountIn(latest, move.context);
  return account !== undefined && account !== "" && account === move.from;
}

// run makes the call of each step that is not skipped, in order, and stops at the first that does not answer. Without a
// failure it is done with the moves it made. After a failure it reads Routes.Get: each step's context counts as moved when
// its account there is the step's account, and as not moved otherwise. A read that fails, or answers no registry, is unknown.
async function run(routes: RoutesClient, steps: readonly Step[]): Promise<MoveResult> {
  const moved: Move[] = [];
  let failure: Failure | null = null;
  for (const step of steps) {
    if (step.skipped) {
      continue;
    }
    const outcome = await runWrite(() =>
      routes.setContextAccount({ context: step.move.context, account: step.account }),
    );
    if (outcome.kind !== "done") {
      failure = outcome;
      break;
    }
    moved.push(step.move);
  }
  if (failure === null) {
    return { kind: "done", moved };
  }
  const read = await runWrite(() => routes.get({}));
  if (read.kind !== "done" || read.answer.registry === undefined) {
    return { kind: "unknown", reason: couldNotTell };
  }
  const registry = read.answer.registry;
  const landed: Move[] = [];
  const notLanded: Move[] = [];
  for (const step of steps) {
    (accountIn(registry, step.move.context) === step.account ? landed : notLanded).push(step.move);
  }
  // A refusal names its reason. When the read shows that nothing moved, the reason also says nothing changed. A call with no
  // answer says it could not tell.
  const reason =
    failure.kind === "refused"
      ? landed.length === 0
        ? `${failure.reason} ${nothingChanged}`
        : failure.reason
      : couldNotTell;
  return { kind: "partial", reason, moved: landed, notMoved: notLanded };
}

// moveContexts sets each planned context to the account target, in plan order, with one SetContextAccount per context. It
// makes no call for a context that latest shows on its own login, or with an account other than its from. It stops at the
// first call that fails.
export async function moveContexts(
  routes: RoutesClient,
  plan: readonly Move[],
  target: string,
  latest: RouteRegistry,
): Promise<MoveResult> {
  return run(routes, plan.map((move) => ({ move, account: target, skipped: !stillOn(latest, move) })));
}

// undoMoves sets each moved context back to its own from, in order, with one SetContextAccount per context. It stops at the
// first call that fails, and reports as moveContexts does.
export async function undoMoves(routes: RoutesClient, moved: readonly Move[]): Promise<MoveResult> {
  return run(routes, moved.map((move) => ({ move, account: move.from, skipped: false })));
}
