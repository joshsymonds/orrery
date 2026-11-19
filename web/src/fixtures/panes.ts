// The panes the fixture slices hold: the pane list, and the conversation stream each pane's Panes.WatchConversation
// answers with. The conversation items are built here too, and sessions draws its own from the same helpers.

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  ConversationItemSchema,
  ConversationStateSchema,
  EngineTextSchema,
  PaneSchema,
  PanesListResponseSchema,
  WatchConversationResponseSchema,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";

// Plain is the field-object branch of a message's init shape: plain values for its fields, with no message type.
type Plain<Init> = Extract<Init, { readonly $typeName?: undefined }>;

type StateFields = Plain<MessageInitShape<typeof ConversationStateSchema>>;
type PaneInit = MessageInitShape<typeof PaneSchema>;
export type ItemInit = MessageInitShape<typeof ConversationItemSchema>;
type TextInit = MessageInitShape<typeof EngineTextSchema>;

// claudeSession is a fixed Claude session id per pane.
const claudeSession = (pane: number) => `5e0e0000-0000-4000-8000-${String(pane).padStart(12, "0")}`;

// askQuestion is a pane's stored question: an AskUserQuestion value with one question, and its options if it has any.
const askQuestion = (question: string, header: string, options: { label: string; description: string }[] = []) =>
  JSON.stringify({ questions: [{ question, header, multiSelect: false, options }] });

const beaconQuestion = askQuestion("Which fix should I make?", "Fix", [
  { label: "One query per week", description: "Join keepers in the week query. Smallest change; touches `query.go` only." },
  { label: "Cache keepers for the request", description: "Keeps the row code as it is. Adds a per-request map." },
  { label: "Paginate the page", description: "Twenty shifts a page. Changes what the harbour office sees." },
]);
const tideQuestion = askQuestion("Read and accept the tide-alerts contract?", "Contract");
const staleQuestion = askQuestion("The AWS fleet won’t launch. How should the gates run?", "Gates");
const lampTool = JSON.stringify({ id: "toolu_lamp_run", name: "Bash", input: "go test ./internal/telemetry/...", start: Number(at("@14:40:14")) });

// call is one tool call and, unless result is null, the result that follows it. A call's id is toolu_<n>.
export function call(n: number, time: bigint, tool: string, input: string, result: string | null = "", error = false): ItemInit[] {
  const id = `toolu_${n}`;
  const items: ItemInit[] = [{ kind: "tool", time, id, tool, input }];
  if (result !== null) {
    items.push({ kind: "result", time, id, text: result, error });
  }
  return items;
}

export const prompt = (time: bigint, sender: string, text: string, round = 0): ItemInit => ({ kind: "prompt", time, sender, text, round });
export const reply = (time: bigint, text: string): ItemInit => ({ kind: "reply", time, text });

// donePane is a pane whose turn has ended: its state is done, and its summary is the line its row draws as its title.
const donePane = (n: number, session: string, cwd: string, summary: string, changed: bigint, seen: bigint): PaneInit => ({
  id: `%${n}`, host: "halyard", session, pane: n, cwd, command: "claude", state: "done", summary, label: "",
  changed, seen, question: "", context: "", activity: changed, tool: "", claudeSession: claudeSession(n),
  questionHash: "", newestReply: changed, lastTurnEnd: changed,
});

const lampPrompt = "The uploader loses readings whenever the collector is unreachable. Make it retry, and spool to disk when retrying is not enough.";
const lampReply = [
  "Backoff is in and the schedule test passes. Delays double from 250 ms and stop growing at 30 s:",
  "",
  "```go",
  "func (b Backoff) Delay(n int) time.Duration {",
  "\td := b.Base << n",
  "\tif d > b.Max || d <= 0 {",
  "\t\treturn b.Max",
  "\t}",
  "\treturn d",
  "}",
  "```",
  "",
  "`Flush` now retries a failed batch in place. **Still open:** there is no jitter, so lamps that drop together retry together, and retries never give up.",
].join("\n");
const lampLive =
  "Full jitter is in: each delay is drawn uniformly between zero and the capped value. Now wiring the 5-minute window into `Flush`, so a batch that outlives it goes to the on-disk spool instead of being dro";

const lampItems: ItemInit[] = [
  prompt(at("@14:02"), "owner", lampPrompt),
  {
    kind: "compaction", time: at("@14:02"), trigger: "auto", tokens: 611000n,
    text: "The telemetry uploader lost readings whenever the collector was unreachable. Established so far: Flush is the only caller of send; a batch holds at most 500 readings; the spool directory exists but nothing writes to it. Agreed plan: retry with capped exponential backoff, then spool.",
  },
  ...call(1, at("@14:03"), "Read", "internal/telemetry/flush.go"),
  ...call(2, at("@14:05"), "Write", "internal/telemetry/backoff.go"),
  ...call(3, at("@14:08"), "Bash", "go test ./internal/telemetry/...",
    "--- FAIL: TestBackoffSchedule (0.00s)\n    backoff_test.go:22: want 250ms, got 0s\nFAIL", true),
  ...call(4, at("@14:12"), "Write", "internal/telemetry/backoff_test.go"),
  ...call(5, at("@14:18"), "Bash", "go test ./internal/telemetry/...", "ok  \tlighthouse/internal/telemetry\t0.31s"),
  ...call(6, at("@14:24"), "Edit", "internal/telemetry/flush.go"),
  reply(at("@14:27"), lampReply),
  prompt(at("@14:31"), "owner", "Cap the total retry window at 5 minutes and add jitter."),
  ...call(7, at("@14:32"), "Edit", "internal/telemetry/backoff.go", "Added 14 lines, removed 3 lines"),
  ...call(8, at("@14:32:20"), "Bash", "go test ./internal/telemetry/... -run 'Backoff|Jitter'", "ok  \tlighthouse/internal/telemetry\t0.412s"),
  ...call(9, at("@14:40:14"), "Bash", "go test ./internal/telemetry/...", null),
];

const beaconReply =
  "Each shift row loads its keeper with its own query, so a week of 44 shifts makes 45 queries. There are three ways to fix it, and they differ in how much they change.";

const beaconItems: ItemInit[] = [
  prompt(at("@14:30"), "owner", "Why is the roster page slow for weeks with more than forty shifts?"),
  ...call(1, at("@14:30:30"), "Read", "internal/roster/handler.go"),
  ...call(2, at("@14:31"), "Grep", "keeperFor\\(", "14 matches in 3 files"),
  ...call(3, at("@14:31:30"), "Read", "internal/roster/query.go"),
  ...call(4, at("@14:32"), "Read", "internal/roster/keeper.go"),
  ...call(5, at("@14:33"), "Bash", "go test ./internal/roster -run Slow -count=1",
    "--- FAIL: TestSlowWeek (4.21s)\n    roster_test.go:88: week of 44 shifts took 4.1s, want under 500ms\nFAIL", true),
  reply(at("@14:37"), beaconReply),
];

const orchardPrompt = [
  "Write up the grammar gaps you found, with **what breaks** and `where`.",
  "",
  "- Keep it short.",
  "- A table is fine.",
].join("\n");
const orchardReply = [
  "## Parser tests for nested scion grafting",
  "",
  "All 41 cases pass. Two gaps remain; neither blocks the release. *Both* are in `grammar/graft.peg`, and the upstream note is [here](https://example.invalid/graft).",
  "",
  "### What breaks",
  "",
  "- A scion nested three deep loses its rootstock label.",
  "- A comment between `graft` and its target is read as the target.",
  "",
  "1. Add a depth counter to `Scion`.",
  "2. Skip trivia before the target.",
  "3. Re-run the 41 cases.",
  "",
  "> A graft takes only where cambium meets cambium.",
  "",
  "| case | depth | result |",
  "| --- | --- | --- |",
  "| nested-2 | 2 | passes |",
  "| nested-3 | 3 | **label lost** |",
  "| comment-target | 1 | **wrong target** |",
  "",
  "```peg",
  'Graft  <- "graft" _ Target _ "onto" _ Rootstock',
  "Target <- Scion / Ident",
  "```",
  "",
  "---",
  "",
  "Struck text is drawn ~~like this~~ in the client; an image is drawn as its alt text.",
].join("\n");

const orchardItems: ItemInit[] = [
  prompt(at("@11:20"), "owner", orchardPrompt),
  reply(at("@11:26"), orchardReply),
];

const rotaPrompt = "Plan the rota export as an epic. Six requirements; fixtures must cover every port zone.";
const rotaPlanningItems: ItemInit[] = [
  prompt(at("@13:48"), "owner", rotaPrompt),
  ...call(1, at("@13:48:30"), "Read", "docs/rota-export.md"),
  ...call(2, at("@13:49"), "Read", "internal/rota/export.go"),
  ...call(3, at("@13:50"), "epic_create", "rota-export", "created"),
  reply(at("@13:51"), "The epic is building. I will tell you when it asks for anything."),
];

const tideItems: ItemInit[] = [
  prompt(at("@14:02"), "owner", "Alert the harbour office when a tide table changes after it was published."),
  ...call(1, at("@14:03"), "Read", "internal/tide/tables.go"),
  ...call(2, at("@14:05"), "Grep", "PublishedAt", "9 matches in 4 files"),
  ...call(3, at("@14:06"), "Read", "internal/tide/publish.go"),
  ...call(4, at("@14:08"), "Read", "docs/contract.md"),
  ...call(5, at("@14:10"), "Grep", "tide_table", "3 matches in 2 files"),
  ...call(6, at("@14:12"), "Read", "internal/alert/sms.go"),
  ...call(7, at("@14:14"), "Write", "docs/contracts/tide-alerts.md"),
  reply(at("@14:15"), "The contract is written. It has three ways this could go wrong, one row you did not ask for, and one decision that is yours. Nothing starts until you accept it."),
];

const followItems: ItemInit[] = [
  prompt(at("@13:02"), "owner", "Turn the round-5 review into epics: the judge follow-ups, orbit labels, notices, and the harbour map."),
  ...call(1, at("@13:03"), "Read", "docs/round-5-review.md"),
  ...call(2, at("@13:04"), "Read", "internal/judge/followups.go"),
  ...call(3, at("@13:10"), "epic_create", "orrery-followups", "created"),
  ...call(4, at("@13:20"), "epic_create", "orbit-labels", "created"),
  ...call(5, at("@13:30"), "epic_create", "notices-usable", "created"),
  ...call(6, at("@13:35"), "epic_create", "harbour-map", "created"),
  reply(at("@13:40"), "Four epics are out. notices-usable is parked on a question for you; the other three need nothing."),
];

const paneLamp: PaneInit = {
  id: "%11", host: "halyard", session: "lighthouse", pane: 11, cwd: "/home/owner/code/lighthouse", command: "claude",
  state: "working", summary: "Retry backoff and jitter for the telemetry uploader", label: "", changed: at("@14:40"),
  seen: at("@14:40:30"), question: "", context: "", activity: at("@14:40"), tool: lampTool,
  claudeSession: claudeSession(11), questionHash: "", newestReply: at("@14:27"), lastTurnEnd: 0n,
};
const paneBeacon: PaneInit = {
  id: "%12", host: "halyard", session: "tidepool", pane: 12, cwd: "/home/owner/code/tidepool", command: "claude",
  state: "waiting", summary: "Why the roster page is slow past forty shifts", label: "", changed: at("@14:37"),
  seen: at("@14:37:30"), question: beaconQuestion, context: "", activity: at("@14:37"), tool: "",
  claudeSession: claudeSession(12), questionHash: "qh-beacon-1", newestReply: at("@14:37"), lastTurnEnd: at("@14:37"),
};
const paneOrchard: PaneInit = {
  id: "%13", host: "halyard", session: "orchard", pane: 13, cwd: "/home/owner/code/orchard", command: "claude",
  state: "done", summary: "Parser tests for nested scion grafting", label: "", changed: at("@14:21"), seen: at("@14:00"),
  question: "", context: "", activity: at("@14:21"), tool: "", claudeSession: claudeSession(13), questionHash: "",
  newestReply: at("@11:26"), lastTurnEnd: at("@14:21"),
};
const paneRota: PaneInit = {
  id: "%14", host: "halyard", session: "lighthouse", pane: 14, cwd: "/home/owner/code/lighthouse", command: "claude",
  state: "done", summary: "Planned the rota export, and steers its epic", label: "", changed: at("@14:28"), seen: at("@14:28:30"),
  question: "", context: "", activity: at("@14:28"), tool: "", claudeSession: claudeSession(14), questionHash: "",
  newestReply: at("@13:51"), lastTurnEnd: at("@13:51"),
};
const panePhase = donePane(15, "almanac", "/home/owner/code/almanac", "Phase endpoint returns ISO dates", at("@14:02"), at("@13:30"));
const paneTide: PaneInit = {
  id: "%16", host: "halyard", session: "tidepool", pane: 16, cwd: "/home/owner/code/tidepool", command: "claude",
  state: "waiting", summary: "Wrote the tide-alerts contract", label: "", changed: at("@14:15"), seen: at("@14:15:30"),
  question: tideQuestion, context: "", activity: at("@14:15"), tool: "", claudeSession: claudeSession(16),
  questionHash: "qh-tide-1", newestReply: at("@14:15"), lastTurnEnd: at("@14:15"),
};
const paneOffline = donePane(17, "tidepool", "/home/owner/code/tidepool", "Planned offline tables", at("@12:41"), at("@12:42"));
const paneBuoy = donePane(18, "tidepool", "/home/owner/code/tidepool", "Started from the shell: buoy-feeds", at("@14:38"), at("@14:38:30"));
const paneHolder = donePane(19, "tidepool", "/home/owner/code/tidepool", "Planned holder scopes", at("@09:52"), at("@09:53"));
const paneCache = donePane(20, "tidepool", "/home/owner/code/tidepool", "Planned the tide cache", at("@-4d 14:41"), at("@-4d 14:42"));
const paneStale: PaneInit = {
  id: "%21", host: "halyard", session: "commonframe", pane: 21, cwd: "/home/owner/work/commonframe", command: "claude",
  state: "waiting", summary: "Merging main into the story-out client branch", label: "", changed: at("@-5d 14:41"),
  seen: at("@-5d 14:42"), question: staleQuestion, context: "", activity: at("@-5d 14:41"), tool: "",
  claudeSession: claudeSession(21), questionHash: "qh-stale-1", newestReply: at("@-5d 14:20"), lastTurnEnd: at("@-5d 14:20"),
};
const paneFollow = donePane(22, "orrery", "/home/owner/code/orrery", "Follow-ups from the round-5 review", at("@14:21"), at("@14:22"));
// A plain shell: a terminal pane with no Claude session, so it has no state and no conversation.
const paneShell: PaneInit = {
  id: "%23", host: "halyard", session: "orrery", pane: 23, cwd: "/home/owner/code/orrery", command: "zsh",
  state: "", summary: "zsh", label: "", changed: at("@11:41"), seen: at("@11:41"), question: "", context: "",
  activity: at("@11:41"), tool: "", claudeSession: "", questionHash: "", newestReply: 0n, lastTurnEnd: 0n,
};
const paneKelp: PaneInit = {
  id: "%24", host: "brine", session: "kelp", pane: 24, cwd: "/home/owner/code/kelp", command: "claude",
  state: "failed", summary: "Kelp bed survey importer", label: "", changed: at("@-1d 14:41"), seen: at("@-1d 14:42"),
  question: "", context: "", activity: at("@-1d 14:41"), tool: "", claudeSession: claudeSession(24), questionHash: "",
  newestReply: at("@-1d 14:41"), lastTurnEnd: at("@-1d 14:41"),
};

// paneStreams holds the Panes.WatchConversation answer of each pane, by pane id.
export const paneStreams = new Map<string, WatchConversationResponse>();
function paneStream(
  id: string,
  pane: PaneInit,
  items: ItemInit[],
  state: Omit<StateFields, "pane">,
  unfinished: TextInit[] = [],
): WatchConversationResponse {
  return create(WatchConversationResponseSchema, {
    transcript: `${id}.jsonl`, seq: 1n, reset: true, items, unfinished,
    state: { pane, ...state },
  });
}

paneStreams.set("%11", paneStream("%11", paneLamp, lampItems, {
  origin: lampPrompt,
  goal: "Stop the telemetry uploader from losing readings when the collector is unreachable.",
  waiting: [
    { text: "Also log the batch id whenever a batch is spooled.", time: at("@14:36:10"), canceled: false },
    { text: "Then put the 5-minute cap in the README.", time: at("@14:38:40"), canceled: false },
  ],
  context: { tokens: 152000n, window: 1000000n },
}, [{
  messageId: "msg_lamp_live", index: 0, parentToolUseId: "", kind: "text", text: lampLive,
}]));

paneStreams.set("%12", paneStream("%12", paneBeacon, beaconItems, {
  goal: "", waiting: [], context: { tokens: 640000n, window: 1000000n },
}));

paneStreams.set("%13", paneStream("%13", paneOrchard, orchardItems, {}));

paneStreams.set("%14", paneStream("%14", paneRota, rotaPlanningItems, { waiting: [] }));

paneStreams.set("%16", paneStream("%16", paneTide, tideItems, {}));

paneStreams.set("%22", paneStream("%22", paneFollow, followItems, {}));

paneStreams.set("%23", paneStream("%23", paneShell, [], {}));

// The other panes hold one reply each: the line their row draws, at the time it was given.
for (const [id, pane, time, text] of [
  ["%15", panePhase, at("@14:02"), "Done. `GET /phase` now answers ISO 8601 and the three callers are updated."],
  ["%17", paneOffline, at("@12:41"), "Release is running. I will tell you how it goes."],
  ["%18", paneBuoy, at("@14:38"), "Made for the epic you created with orrery epic create."],
  ["%19", paneHolder, at("@09:52"), "Shipped v0.45.0."],
  ["%20", paneCache, at("@-4d 14:41"), "Superseded by offline-tables."],
  ["%21", paneStale, at("@-5d 14:20"), "No fleet boxes are running at all, so the likely blocker is the budget pace check."],
  ["%24", paneKelp, at("@-1d 14:41"), "The session ended with an error: the API refused the request."],
] as const) {
  paneStreams.set(id, paneStream(id, pane, [reply(time, text)], {}));
}

// panes is the Panes.List answer: the panes, in the order the list draws them.
export const panes = create(PanesListResponseSchema, {
  panes: [paneLamp, paneBeacon, paneOrchard, paneRota, panePhase, paneTide, paneOffline, paneBuoy, paneHolder, paneCache, paneStale, paneFollow, paneShell, paneKelp],
});
