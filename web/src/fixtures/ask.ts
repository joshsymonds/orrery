// The fixture slices of round 6's ask-* pages: each Ask scenario as the fixture transport answers it. Each is the shipped
// slices with the one pane the scenario is about set to the facts the scenario gives, or with the procedure it is about
// held or failed, and nothing else changed. The shipped slices are never written to.

import { create } from "@bufbuild/protobuf";

import { type Pane, WatchConversationResponseSchema, type WatchConversationResponse } from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";
import { call } from "./panes";
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
  { label: "One query per week (Recommended)", description: "Join keepers in the week query. Smallest change; touches `query.go` only." },
  { label: "Cache keepers for the request", description: "Keeps the row code as it is. Adds a per-request map." },
  { label: "Paginate the page", description: "Twenty shifts a page. Changes what the harbour office sees." },
];

// The two further options of ask-many, which round 6 gives in the same scenario.
const manyOptions = [
  ...beaconOptions,
  { label: "Precompute the week nightly", description: "Fast reads; the page can be a day stale." },
  { label: "Leave it", description: "Forty shifts is rare. Close this out." },
];

// The stale question's options, asked by commonframe, which round 6 gives in scenarios-convo.js.
const staleOptions = [
  { label: "Raise the budget line by $100 (Recommended)", description: "Wakes resume now; covers the remaining three gates." },
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
  "ask-stale": withPane("%21", (pane) => ({
    ...pane,
    orreryId: "sess-stale-21",
    question: askUser({
      question:
        "The AWS fleet won’t launch: the budget pace check puts November spend ahead of today’s line, so it refuses every gate box. How should the gates run?",
      options: staleOptions,
    }),
  })),
  "ask-unanswerable": withPane("%12", (pane) => ({
    ...pane,
    question: askUser({ question: "Paste the harbour office’s API token so I can test the alert end to end.", options: [] }),
  })),
};
