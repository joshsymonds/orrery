export type TaskState = "done" | "dispatched" | "gated" | "pending" | "gap" | "split" | "unknown";

// The recorded status words, lower case, each with its state. "integrated" counts as done. A Map, so that a
// property name such as "constructor" is not found on Object.prototype.
const STATES = new Map<string, TaskState>([
  ["done", "done"],
  ["integrated", "done"],
  ["dispatched", "dispatched"],
  ["gated", "gated"],
  ["pending", "pending"],
  ["gap", "gap"],
  ["split", "split"],
]);

// The state of status, compared without case. Anything else, the empty string and commit-suffixed forms such as
// "done at 3f9c2e1" included, is unknown; the text is not trimmed.
export function taskStateOf(status: string): TaskState {
  return STATES.get(status.toLowerCase()) ?? "unknown";
}
