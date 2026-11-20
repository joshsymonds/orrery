// The fixture slices of the dock pages: each scenario of round 6's dock pages, as the fixture transport
// answers it. Each is the shipped slices with the one session or pane the scenario is about set to the facts the scenario
// gives, and nothing else changed. The shipped slices are never written to.

import { create } from "@bufbuild/protobuf";

import {
  ConversationItemSchema,
  PendingEventSchema,
  QueuedMessageSchema,
  SessionsWatchConversationResponseSchema,
  WaitingMessageSchema,
  type Pane,
  type PendingEvent,
  type QueuedMessage,
  type SessionsWatchConversationResponse,
  type WaitingMessage,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";
import { call, type ItemInit, prompt, reply } from "./panes";
import { type Slices, shippedSlices } from "./slices";

// warmupSession is the headless session of Tide table cache warm-up, which the dock pages mostly draw.
export const warmupSession = "9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90";
// gatesSession is the headless session of Merging main into the story-out client branch, whose question is put off; its
// slices are page/ask-later's, in fixtures/ask.ts.
export const gatesSession = "c0ffee00-1a2b-4c3d-8e4f-5a6b7c8d9e0f";
// almanacSession is the headless session of The 2027 ephemeris import, which the server cut off mid-turn.
const almanacSession = "4be0c6d2-8f3a-4c71-9e25-ephemeris001";

const q1 = "Also log the batch id whenever a batch is spooled.";
const q2 = "Then put the 5-minute cap in the README.";
const warmTool = JSON.stringify({
  id: "toolu_warm",
  name: "Bash",
  input: "just warm --port lisbon",
  start: Number(at("@14:36:50")),
});

// must returns value, or throws naming what the shipped slices do not hold.
function must<T>(value: T | undefined, what: string): T {
  if (value === undefined) {
    throw new Error(`fixtures: no ${what}`);
  }
  return value;
}

const shipped = shippedSlices;

// messages turns the item inits of a scenario into the conversation items a stream holds.
const messages = (inits: ItemInit[]) => inits.map((init) => create(ConversationItemSchema, init));

// queuedMessage is a message a session's queue holds, in the state it is in.
const queuedMessage = (promptId: string, text: string, state = "queued"): QueuedMessage =>
  create(QueuedMessageSchema, { promptId, text, state, waits: "turn" });

// waitingMessage is a message a pane's queue holds, canceled or not.
const waitingMessage = (text: string, time: bigint, canceled: boolean): WaitingMessage =>
  create(WaitingMessageSchema, { text, time, canceled });

// warmupFacts are the facts of the warmup session that a dock page sets: its session's state and when it changed, the
// reason its row gives, and what its stream holds beyond the shipped items.
interface WarmupFacts {
  state: string;
  changed: bigint;
  summary: string;
  turnSince?: bigint;
  interruptingSince?: bigint;
  extra?: ItemInit[];
  queued?: QueuedMessage[];
  pending?: PendingEvent[];
}

// warmupStream is the warmup session's conversation stream with the facts set, over the shipped stream's items.
function warmupStream(facts: WarmupFacts): SessionsWatchConversationResponse {
  const stream = must(shipped.sessionStreams.get(warmupSession), "warmup stream");
  const state = must(stream.state, "warmup state");
  const session = must(state.session, "warmup session");
  return {
    ...stream,
    items: [...stream.items, ...messages(facts.extra ?? [])],
    queued: facts.queued ?? [],
    state: {
      ...state,
      interrupting: facts.interruptingSince !== undefined,
      interruptingSince: facts.interruptingSince ?? 0n,
      turnSince: facts.turnSince ?? 0n,
      pending: facts.pending ?? [],
      session: { ...session, state: facts.state, changed: facts.changed, summary: facts.summary },
    },
  };
}

// warmupSlices is the shipped slices with the warmup session's stream and its headless row set to the facts. The row
// takes the stream's state, changed and reason.
function warmupSlices(facts: WarmupFacts): Slices {
  const sessions = shipped.headless.sessions.map((row) =>
    row.id === warmupSession ? { ...row, state: facts.state, changed: facts.changed, summary: facts.summary } : row,
  );
  return {
    ...shipped,
    headless: { ...shipped.headless, sessions },
    sessionStreams: new Map<string, SessionsWatchConversationResponse>([
      ...shipped.sessionStreams,
      [warmupSession, warmupStream(facts)],
    ]),
  };
}

// runningCall is the warmup's running call: a Bash call that no result answers yet.
const runningCall = call(5, at("@14:40"), "Bash", "just warm --port lisbon", null);

// interruptingWorking is the warmup mid-turn with a running call and two messages queued, before any interrupt.
const interruptingWorking: WarmupFacts = {
  state: "working",
  changed: at("@14:40"),
  summary: "",
  turnSince: at("@14:28:20"),
  extra: runningCall,
  queued: [queuedMessage("q1", q1), queuedMessage("q2", q2)],
};

// pane17Slices is the shipped slices with pane %17's list entry and its stream set to the facts: its state, when it
// changed, its tool JSON and the messages its queue holds.
interface PaneFacts {
  state: string;
  changed: bigint;
  tool: string;
  waiting: WaitingMessage[];
}

function pane17Slices(facts: PaneFacts): Slices {
  const paneOffline = must(
    shipped.panes.panes.find((pane) => pane.id === "%17"),
    "pane %17",
  );
  const pane: Pane = { ...paneOffline, state: facts.state, changed: facts.changed, tool: facts.tool };
  const stream = must(shipped.paneStreams.get("%17"), "pane %17 stream");
  const state = must(stream.state, "pane %17 state");
  return {
    ...shipped,
    panes: { ...shipped.panes, panes: shipped.panes.panes.map((listed) => (listed.id === "%17" ? pane : listed)) },
    paneStreams: new Map<string, WatchConversationResponse>([
      ...shipped.paneStreams,
      ["%17", { ...stream, state: { ...state, pane, waiting: facts.waiting } }],
    ]),
  };
}

// almanacSlices is the shipped slices with the cut-off session: its own stream and its headless row, interrupted by the
// server.
function almanacSlices(): Slices {
  const items: ItemInit[] = [
    prompt(at("@12:20"), "owner", "Import the 2027 ephemeris, and check the first week against the 2026 table."),
    ...call(1, at("@12:21"), "Read", "internal/almanac/ephemeris.go"),
    ...call(2, at("@12:24"), "Read", "internal/almanac/import.go"),
    ...call(3, at("@12:30"), "Bash", "just import --year 2027"),
    ...call(4, at("@12:36"), "Bash", "go test ./internal/almanac/... -run Ephemeris", "ok  \tlighthouse/internal/almanac\t0.218s"),
    reply(at("@12:40:30"), "The first 180 days are in and check against the 2026 table. The server restarted before the rest was written."),
  ];
  const stream = create(SessionsWatchConversationResponseSchema, {
    transcript: `${almanacSession}.jsonl`,
    seq: 1n,
    reset: true,
    items: items,
    state: {
      session: {
        id: almanacSession, dir: "/home/owner/code/almanac", label: "The 2027 ephemeris import", state: "interrupted",
        changed: at("@12:41"), seen: at("@12:42"), summary: "the server restarted mid-turn", question: "",
        newestReply: at("@12:40:30"),
      },
    },
  });
  return {
    ...shipped,
    headless: {
      ...shipped.headless,
      sessions: shipped.headless.sessions.map((row) =>
        row.id === almanacSession ? { ...row, changed: at("@12:41"), summary: "the server restarted mid-turn" } : row,
      ),
    },
    sessionStreams: new Map<string, SessionsWatchConversationResponse>([
      ...shipped.sessionStreams,
      [almanacSession, stream],
    ]),
  };
}

// dockSlices holds each dock page's slices, by its scenario id.
export const dockSlices = {
  "dock-idle": warmupSlices({ state: "done", changed: at("@14:37"), summary: "" }),
  "dock-queued": warmupSlices(interruptingWorking),
  "dock-draft": warmupSlices({ ...interruptingWorking, turnSince: at("@14:28:16"), queued: [queuedMessage("q1", q1)] }),
  "dock-interrupting": {
    ...warmupSlices(interruptingWorking),
    interrupt: {
      texts: [q1, q2],
      then: warmupStream({
        ...interruptingWorking,
        interruptingSince: at("@14:40:56"),
        queued: [queuedMessage("q1", q1, "canceled"), queuedMessage("q2", q2, "canceled")],
      }),
    },
  },
  "dock-interrupted": warmupSlices({
    state: "done",
    changed: at("@14:32"),
    summary: "",
    extra: [{ kind: "interrupted", time: at("@14:32") }],
    pending: [create(PendingEventSchema, { id: "ev-speak", kind: "step_end", waits: "speak" })],
  }),
  "dock-interrupt-queue": warmupSlices({
    state: "working",
    changed: at("@14:40:57"),
    summary: "",
    turnSince: at("@14:40:57"),
    extra: [{ kind: "interrupted", time: at("@14:32") }, prompt(at("@14:32"), "owner", q1)],
    queued: [queuedMessage("q2", q2)],
  }),
  "dock-cut-off": almanacSlices(),
  "dock-pane-queue": pane17Slices({
    state: "working",
    changed: at("@14:36:50"),
    tool: warmTool,
    waiting: [waitingMessage(q1, at("@14:36:10"), false), waitingMessage(q2, at("@14:38:40"), false)],
  }),
  "dock-waiting": warmupSlices({
    state: "waiting",
    changed: at("@14:12"),
    summary: "Its turn ended on a question in words",
    extra: [reply(at("@14:12"), "Should the warm-up also run on deploy, or only by hand?")],
  }),
  "dock-failed": warmupSlices({ state: "failed", changed: at("@-1d 14:41"), summary: "the API refused the request" }),
  "dock-stopped": warmupSlices({ state: "stopped", changed: at("@-2d 14:41"), summary: "You stopped it." }),
  "dock-send-error": { ...warmupSlices({ state: "done", changed: at("@14:37"), summary: "" }), say: "refused" as const },
  "dock-send-unknown": { ...warmupSlices({ state: "done", changed: at("@14:37"), summary: "" }), say: "unanswered" as const },
  "dock-cancelled": pane17Slices({
    state: "done",
    changed: at("@14:40"),
    tool: "",
    waiting: [waitingMessage(q2, at("@14:38:40"), true)],
  }),
};
