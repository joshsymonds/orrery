// The write protocol the accounts and routes page shares: how a write's answer reads, whether it landed, was refused, or has
// no answer. Serve passes the messages of InvalidArgument, NotFound and FailedPrecondition through, and replaces every other
// error's message with fixed text, so a refusal's reason is safe to show and any other failure is not known to have landed.

import { Code, ConnectError } from "@connectrpc/connect";

// WriteOutcome is how a write ended: done with its answer; refused by a code the server names, with the reason it gave, and
// nothing changed; or unknown, when no answer came and it is not known whether the write landed.
export type WriteOutcome<T> =
  | { kind: "done"; answer: T }
  | { kind: "refused"; code: Code; reason: string }
  | { kind: "unknown" };

// refusalCodes are the codes a write refused with, whose messages Serve passes through.
const refusalCodes: ReadonlySet<Code> = new Set([Code.InvalidArgument, Code.NotFound, Code.FailedPrecondition]);

// runWrite makes a write with call and reports how it ended. An error of a refusal code is refused with its message as the
// reason. Any other error is unknown.
export async function runWrite<T>(call: () => Promise<T>): Promise<WriteOutcome<T>> {
  let answer: T;
  try {
    answer = await call();
  } catch (error) {
    if (error instanceof ConnectError && refusalCodes.has(error.code)) {
      return { kind: "refused", code: error.code, reason: error.rawMessage };
    }
    return { kind: "unknown" };
  }
  return { kind: "done", answer };
}

// nothingChanged is what a refused write says the page shows: the write changed nothing.
export const nothingChanged = "Nothing changed.";

// couldNotTell is what a write with no answer says the page shows: whether it landed is not known, and the page shows what
// stands now.
export const couldNotTell =
  "Could not tell whether that landed: no answer came. This is what stands now.";

// usedBy lists the ids of the contexts whose account is accountId, in registry order. An empty id is no account, so it is
// used by no context.
export function usedBy(
  registry: { readonly contexts: readonly { readonly id: string; readonly account: string }[] },
  accountId: string,
): string[] {
  if (accountId === "") {
    return [];
  }
  return registry.contexts.filter((context) => context.account === accountId).map((context) => context.id);
}

// hashOf is the short form of a fingerprint the page shows: '#' and its first six characters.
export function hashOf(fingerprint: string): string {
  return `#${fingerprint.slice(0, 6)}`;
}
