import { PHASES, type Phase } from "./phases";

// The pure address of a web page: the view the list shows, the cursor's entry, and the sub-page of an epic. The hash form
// is '#/<view>[/<entry>[/<sub-page>]]'. Every id segment is encoded with encodeURIComponent, so a slash inside an id
// travels as %2F and a pane id that holds a percent sign travels with its percent sign escaped.

const VIEWS = ["inbox", "archived"] as const;
export type View = (typeof VIEWS)[number];

// The page an epic shows: the epic page itself, its contract, a task's phase output, or a worker session's step.
export type PageView =
  | { kind: "epic" }
  | { kind: "contract" }
  | { kind: "phase"; task: string; phase: Phase }
  | { kind: "session"; task: string; phase: Phase; step: string };

// The list panel open beside a session entry: the agents list or the running list.
export type ListPanelKind = "agents" | "running";

export type Entry =
  | { kind: "session"; id: string; panel?: ListPanelKind }
  | { kind: "pane"; host: string; pane: string }
  | { kind: "step"; id: string }
  | { kind: "epic"; id: string; page: PageView }
  | { kind: "project"; key: string };

export interface Address {
  view: View;
  entry: Entry | null;
}

// The address every unreadable hash parses to.
function inbox(): Address {
  return { view: "inbox", entry: null };
}

function isView(segment: string): segment is View {
  return (VIEWS as readonly string[]).includes(segment);
}

// The view a hash's first segment names. The attention and project orders are the inbox now: their addresses still
// open it, and no address is written in either form.
function viewOf(segment: string): View | undefined {
  if (segment === "attention" || segment === "project") {
    return "inbox";
  }
  return isView(segment) ? segment : undefined;
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

// A panel segment: exactly the name of a list panel.
function readPanel(segment: string): ListPanelKind | undefined {
  return segment === "agents" || segment === "running" ? segment : undefined;
}

// The entry from the segments after the view. It is undefined when the segments name no entry this router knows.
function readEntry(segments: readonly string[]): Entry | undefined {
  const [head, ...values] = segments;
  if (head === "session" && values.length === 1) {
    const id = readValue(values[0]);
    return id === undefined ? undefined : { kind: "session", id };
  }
  if (head === "session" && values.length === 2) {
    const id = readValue(values[0]);
    const panel = readPanel(values[1]);
    return id !== undefined && panel !== undefined ? { kind: "session", id, panel } : undefined;
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
  if (head === "project" && values.length === 1) {
    const key = readValue(values[0]);
    return key === undefined ? undefined : { kind: "project", key };
  }
  return undefined;
}

// parseAddress reads a hash with or without its leading '#'. It never throws: a hash it cannot read, such as an empty
// one, an unknown view or kind, a phase outside PHASES, a wrong segment count or a malformed percent escape, parses
// to the inbox with no entry.
export function parseAddress(hash: string): Address {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!path.startsWith("/")) {
    return inbox();
  }
  const [viewSegment, ...rest] = path.slice(1).split("/");
  const view = viewOf(viewSegment);
  if (view === undefined) {
    return inbox();
  }
  if (rest.length === 0) {
    return { view, entry: null };
  }
  const entry = readEntry(rest);
  return entry === undefined ? inbox() : { view, entry };
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
  const segments: string[] = [address.view];
  const entry = address.entry;
  if (entry !== null) {
    switch (entry.kind) {
      case "session":
        segments.push("session", encodeValue(entry.id));
        if (entry.panel !== undefined) {
          segments.push(entry.panel);
        }
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
      case "project":
        segments.push("project", encodeValue(entry.key));
        break;
    }
  }
  return `#/${segments.join("/")}`;
}
