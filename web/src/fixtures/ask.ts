// The fixture slices of round 6's ask-* pages: each Ask scenario as the fixture transport answers it. Each is the shipped
// slices with the one pane the scenario is about set to the facts the scenario gives, or with the procedure it is about
// held or failed, and nothing else changed. The shipped slices are never written to.

import { create } from "@bufbuild/protobuf";

import {
  ConversationItemSchema,
  ConversationStateSchema,
  HeadlessSessionSchema,
  OpenRequestSchema,
  PendingEventSchema,
  SessionsWatchConversationResponseSchema,
  type ListHeadlessResponse,
  type Pane,
  type SessionsWatchConversationResponse,
  WatchConversationResponseSchema,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";
import { gatesSession } from "./dock";
import { call, reply } from "./panes";
import { type Slices, shippedSlices } from "./slices";

const shipped = shippedSlices;

// must returns value, or throws naming what the shipped slices do not hold.
function must<T>(value: T | undefined, what: string): T {
  if (value === undefined) {
    throw new Error(`fixtures: no ${what}`);
  }
  return value;
}

// withPane is the shipped slices with pane id's list entry and the pane its stream holds set to change of it.
function withPane(id: string, change: (pane: Pane) => Pane): Slices {
  const listed = must(
    shipped.panes.panes.find((pane) => pane.id === id),
    `pane ${id}`,
  );
  const pane = change(listed);
  const stream = must(shipped.paneStreams.get(id), `pane ${id} stream`);
  const state = must(stream.state, `pane ${id} state`);
  return {
    ...shipped,
    panes: { ...shipped.panes, panes: shipped.panes.panes.map((entry) => (entry.id === id ? pane : entry)) },
    paneStreams: new Map<string, WatchConversationResponse>([
      ...shipped.paneStreams,
      [id, { ...stream, state: { ...state, pane } }],
    ]),
  };
}

// askUser is a pane's stored question as an AskUserQuestion value: each question with its options.
function askUser(...questions: { question: string; options: { label: string; description: string }[] }[]): string {
  return JSON.stringify({ questions });
}

// The options of tidepool's question on pane %12, which round 6 gives in scenarios-convo.js and world.js.
const beaconOptions = [
  { label: "One query per week", description: "Join keepers in the week query. Smallest change; touches `query.go` only." },
  { label: "Cache keepers for the request", description: "Keeps the row code as it is. Adds a per-request map." },
  { label: "Paginate the page", description: "Twenty shifts a page. Changes what the harbour office sees." },
];

// The two further options of ask-many, which round 6 gives in the same scenario.
const manyOptions = [
  ...beaconOptions,
  { label: "Precompute the week nightly", description: "Fast reads; the page can be a day stale." },
  { label: "Leave it", description: "Forty shifts is rare. Close this out." },
];

// The question commonframe's session asks, which round 6 gives in scenarios-convo.js for ask-stale and ask-later.
const gatesQuestion =
  "The AWS fleet won’t launch: the budget pace check puts November spend ahead of today’s line, so it refuses every gate box. How should the gates run?";

// The stale question's options, asked by commonframe, which round 6 gives in scenarios-convo.js.
const staleOptions = [
  { label: "Raise the budget line by $100", description: "Wakes resume now; covers the remaining three gates." },
  { label: "Raise it by $50", description: "Covers this gate and some of the next." },
  { label: "Wait for the 14th", description: "No spend. Nothing gates until the line catches up." },
];

// askMany's second question. Round 6 gives its title as only "question 1 of 2", so its words are a placeholder: only its
// presence matters, since the Ask draws the first question.
const secondQuestion = { question: "Ship the fix behind a flag?", options: [{ label: "Yes", description: "" }, { label: "No", description: "" }] };

// The Bash permission ask-permission asks: git push with a lease, as round 6 gives it.
const pushPermission = JSON.stringify({
  tool: "Bash",
  input: { command: "git push origin epic/rota-export --force-with-lease" },
});

// sendKeys is the procedure an answer to a pane's question is sent as.
const sendKeys = "orrery.v1.Panes/SendKeys";

// answeredFrame is tidepool's next frame once its answer is sent: the question is gone, and the session works on an Edit.
const beaconPane = must(shipped.panes.panes.find((pane) => pane.id === "%12"), "pane %12");
const answeredFrame = create(WatchConversationResponseSchema, {
  transcript: "%12.jsonl",
  seq: 2n,
  items: call(6, at("@14:38"), "Edit", "internal/roster/query.go", null),
  state: {
    pane: {
      ...beaconPane,
      state: "working",
      changed: at("@14:38"),
      question: "",
      questionHash: "",
      tool: JSON.stringify({ id: "toolu_6", name: "Edit", input: "internal/roster/query.go", start: Number(at("@14:38")) }),
    },
    context: { tokens: 640000n, window: 1000000n },
  },
});

// askLater is ask-later's session: Merging main into the story-out client branch, whose question was asked five days ago and
// is put off. The session takes no events until the question is answered.
function askLater(): Slices {
  const session = create(HeadlessSessionSchema, {
    id: gatesSession,
    dir: "/home/owner/code/commonframe",
    label: "Merging main into the story-out client branch",
    headline: "Merging main into the story-out client branch",
    state: "waiting",
    changed: at("@-5d 14:41"),
    seen: at("@-5d 14:42"),
    summary: "",
    question: "",
    newestReply: at("@-5d 14:41"),
    putOff: true,
  });
  const headless: ListHeadlessResponse = {
    ...shipped.headless,
    sessions: [...shipped.headless.sessions, session],
  };
  const stream = create(SessionsWatchConversationResponseSchema, {
    transcript: `${gatesSession}.jsonl`,
    seq: 1n,
    reset: true,
    items: [
      create(
        ConversationItemSchema,
        reply(at("@-5d 14:41"), "No fleet boxes are running at all, so the likely blocker is the budget pace check."),
      ),
    ],
    state: create(ConversationStateSchema, {
      session,
      request: create(OpenRequestSchema, {
        id: "req-gates",
        kind: "question",
        question: gatesQuestion,
        options: ["Raise the budget line by $100", "Run the gates locally"],
        putOff: true,
      }),
      pending: [create(PendingEventSchema, { id: "ev-gates", kind: "question", waits: "request" })],
    }),
  });
  return {
    ...shipped,
    headless,
    sessionStreams: new Map<string, SessionsWatchConversationResponse>([...shipped.sessionStreams, [gatesSession, stream]]),
  };
}

// askSlices holds each Ask page's slices, by its scenario id. ask-session is the shipped beacon, and ask-decline draws the
// same question, so neither has slices of its own.
export const askSlices = {
  "ask-answered": { ...shipped, keysThen: new Map([["%12", answeredFrame]]) },
  "ask-permission": withPane("%12", (pane) => ({ ...pane, question: pushPermission })),
  "ask-many": withPane("%12", (pane) => ({
    ...pane,
    question: askUser({ question: "Which fix should I make?", options: manyOptions }, secondQuestion),
  })),
  "ask-sending": { ...shipped, hold: new Set([sendKeys]) },
  "ask-error": { ...shipped, fail: new Set([sendKeys]) },
  "ask-later": askLater(),
  "ask-stale": withPane("%21", (pane) => ({
    ...pane,
    orreryId: "sess-stale-21",
    question: askUser({
      question: gatesQuestion,
      options: staleOptions,
    }),
  })),
  "ask-unanswerable": withPane("%12", (pane) => ({
    ...pane,
    question: askUser({ question: "Paste the harbour office’s API token so I can test the alert end to end.", options: [] }),
  })),
};
