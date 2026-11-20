import { Code, ConnectError } from "@connectrpc/connect";

export type Refusal = { outcome: "refused"; message: string } | { outcome: "unknown" };

const refusedCodes: ReadonlySet<Code> = new Set([Code.FailedPrecondition, Code.NotFound, Code.InvalidArgument]);

// outcomeOf sorts a failed call. A refused code means the server turned the call down, and its raw message is the
// reason. Any other failure leaves the outcome unknown: the call may have landed.
export function outcomeOf(error: unknown): Refusal {
  const connectError = ConnectError.from(error);
  if (refusedCodes.has(connectError.code)) {
    return { outcome: "refused", message: connectError.rawMessage };
  }
  return { outcome: "unknown" };
}
