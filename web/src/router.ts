import { PHASES, type Phase } from "./phases";

// The pure address of a web page: the inbox order, the cursor's entry, and the sub-page of an epic. The hash form is
// '#/<order>[/<entry>[/<sub-page>]]'. Every id segment is encoded with encodeURIComponent, so a slash inside an id
// travels as %2F and a pane id that holds a percent sign travels with its percent sign escaped.

const ORDERS = ["attention", "project", "archived"] as const;
export type Order = (typeof ORDERS)[number];

// The page an epic shows: the epic page itself, its contract, a task's phase output, or a worker session's step.
export type PageView =
  | { kind: "epic" }
  | { kind: "contract" }
  | { kind: "phase"; task: string; phase: Phase }
  | { kind: "session"; task: string; phase: Phase; step: string };

export type Entry =
  | { kind: "session"; id: string }
  | { kind: "pane"; host: string; pane: string }
  | { kind: "step"; id: string }
  | { kind: "epic"; id: string; page: PageView };

export interface Address {
  order: Order;
  entry: Entry | null;
}

// The address every unreadable hash parses to.
function inbox(): Address {
  return { order: "attention", entry: null };
}

function isOrder(segment: string): segment is Order {
  return (ORDERS as readonly string[]).includes(segment);
}

function isPhase(segment: string): segment is Phase {
  return (PHASES as readonly string[]).includes(segment);
}

// An id or name segment, decoded once. It is undefined when its percent escapes are malformed or it is empty.
function readValue(segment: string): string | undefined {
  let value: string;
  try {
    value = decodeURIComponent(segment);
  } catch {
    return undefined;
  }
  return value === "" ? undefined : value;
}

// A phase segment: a name read as a value that PHASES holds.
function readPhase(segment: string): Phase | undefined {
  const value = readValue(segment);
  return value !== undefined && isPhase(value) ? value : undefined;
}

// The sub-page under an epic, from the segments after the epic's id. An empty list is the epic page itself.
function readPage(segments: readonly string[]): PageView | undefined {
  if (segments.length === 0) {
    return { kind: "epic" };
  }
  const [head, ...values] = segments;
  if (head === "contract" && values.length === 0) {
    return { kind: "contract" };
  }
  if (head === "phase" && values.length === 2) {
    const [taskSegment, phaseSegment] = values;
    const task = readValue(taskSegment);
    const phase = readPhase(phaseSegment);
    return task !== undefined && phase !== undefined ? { kind: "phase", task, phase } : undefined;
  }
  if (head === "session" && values.length === 3) {
    const [taskSegment, phaseSegment, stepSegment] = values;
    const task = readValue(taskSegment);
    const phase = readPhase(phaseSegment);
    const step = readValue(stepSegment);
    return task !== undefined && phase !== undefined && step !== undefined
      ? { kind: "session", task, phase, step }
      : undefined;
  }
  return undefined;
}

// The entry from the segments after the order. It is undefined when the segments name no entry this router knows.
function readEntry(segments: readonly string[]): Entry | undefined {
  const [head, ...values] = segments;
  if (head === "session" && values.length === 1) {
    const id = readValue(values[0]);
    return id === undefined ? undefined : { kind: "session", id };
  }
  if (head === "pane" && values.length === 2) {
    const host = readValue(values[0]);
    const pane = readValue(values[1]);
    return host !== undefined && pane !== undefined ? { kind: "pane", host, pane } : undefined;
  }
  if (head === "step" && values.length === 1) {
    const id = readValue(values[0]);
    return id === undefined ? undefined : { kind: "step", id };
  }
  if (head === "epic" && values.length >= 1) {
    const id = readValue(values[0]);
    const page = readPage(values.slice(1));
    return id !== undefined && page !== undefined ? { kind: "epic", id, page } : undefined;
  }
  return undefined;
}

// parseAddress reads a hash with or without its leading '#'. It never throws: a hash it cannot read, such as an empty
// one, an unknown order or kind, a phase outside PHASES, a wrong segment count or a malformed percent escape, parses
// to the attention inbox with no entry.
export function parseAddress(hash: string): Address {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!path.startsWith("/")) {
    return inbox();
  }
  const [orderSegment, ...rest] = path.slice(1).split("/");
  if (!isOrder(orderSegment)) {
    return inbox();
  }
  if (rest.length === 0) {
    return { order: orderSegment, entry: null };
  }
  const entry = readEntry(rest);
  return entry === undefined ? inbox() : { order: orderSegment, entry };
}

function encodeValue(value: string): string {
  return encodeURIComponent(value);
}

// The sub-page segments an epic entry carries. The epic page itself has none.
function formatPage(page: PageView): string[] {
  switch (page.kind) {
    case "epic":
      return [];
    case "contract":
      return ["contract"];
    case "phase":
      return ["phase", encodeValue(page.task), encodeValue(page.phase)];
    case "session":
      return ["session", encodeValue(page.task), encodeValue(page.phase), encodeValue(page.step)];
  }
}

// formatAddress writes an address as its hash, with the leading '#'. parseAddress reads the result back to the same
// address.
export function formatAddress(address: Address): string {
  const segments: string[] = [address.order];
  const entry = address.entry;
  if (entry !== null) {
    switch (entry.kind) {
      case "session":
        segments.push("session", encodeValue(entry.id));
        break;
      case "pane":
        segments.push("pane", encodeValue(entry.host), encodeValue(entry.pane));
        break;
      case "step":
        segments.push("step", encodeValue(entry.id));
        break;
      case "epic":
        segments.push("epic", encodeValue(entry.id), ...formatPage(entry.page));
        break;
    }
  }
  return `#/${segments.join("/")}`;
}
