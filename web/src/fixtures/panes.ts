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

const beaconQuestion = JSON.stringify({
  questions: [{
    question: "Which index should I add?",
    header: "Index",
    multiSelect: false,
    options: [
      { label: "Composite index", description: "On (keeper_id, shift_start). Serves the backfill and the roster page." },
      { label: "Partial index", description: "Open shifts only. Smaller, but the backfill still scans closed shifts." },
      { label: "Something else", description: "Type an answer." },
    ],
  }],
});
const lampTool = JSON.stringify({ id: "toolu_lamp_run", name: "Bash", input: "go test ./internal/telemetry/...", start: Number(at("@14:40:18")) });
const harvestTool = JSON.stringify({ id: "toolu_hv", name: "Edit", input: "report/csv.go", start: Number(at("@14:40:53")) });

const paneBeacon: PaneInit = {
  id: "%12", host: "halyard", session: "lighthouse", pane: 12, cwd: "/home/owner/code/lighthouse", command: "claude",
  state: "waiting", summary: "asks which index to add", label: "beacon-schedule", changed: at("@14:29"),
  seen: at("@14:28:30"), question: beaconQuestion, context: "", activity: at("@14:29"), tool: "",
  claudeSession: claudeSession(12), questionHash: "qh-beacon-1", newestReply: at("@14:29"), lastTurnEnd: at("@14:29"),
};
const paneLamp: PaneInit = {
  id: "%11", host: "halyard", session: "lighthouse", pane: 11, cwd: "/home/owner/code/lighthouse", command: "claude",
  state: "working", summary: "adding retry backoff", label: "lamp-telemetry", changed: at("@14:31:20"),
  seen: at("@14:31:30"), question: "", context: "", activity: at("@14:31:20"), tool: lampTool,
  claudeSession: claudeSession(11), questionHash: "", newestReply: at("@14:31:20"), lastTurnEnd: 0n,
};
const paneGraft: PaneInit = {
  id: "%13", host: "sextant", session: "orchard", pane: 13, cwd: "/home/owner/code/orchard", command: "claude",
  state: "done",
  summary: "All 48 parser tests pass. The grafting rule for nested scions was the only real change; the rest was test data.",
  label: "graft-parser", changed: at("@14:21"), seen: at("@14:00"), question: "", context: "", activity: at("@14:21"),
  tool: "", claudeSession: claudeSession(13), questionHash: "", newestReply: at("@14:21"), lastTurnEnd: at("@14:21"),
};
const paneMoon: PaneInit = {
  id: "%15", host: "halyard", session: "almanac", pane: 15, cwd: "/home/owner/code/almanac", command: "claude",
  state: "done",
  summary: "The phase endpoint now returns ISO dates, and both clients that parsed the old format are updated.",
  label: "moon-phase-api", changed: at("@14:02"), seen: at("@13:30"), question: "", context: "", activity: at("@14:02"),
  tool: "", claudeSession: claudeSession(15), questionHash: "", newestReply: at("@14:02"), lastTurnEnd: at("@14:02"),
};
const paneHarvest: PaneInit = {
  id: "%16", host: "sextant", session: "orchard", pane: 16, cwd: "/home/owner/code/orchard", command: "claude",
  state: "working", summary: "rendering CSV export", label: "harvest-report", changed: at("@14:38"),
  seen: at("@14:38:30"), question: "", context: "", activity: at("@14:38"), tool: harvestTool,
  claudeSession: claudeSession(16), questionHash: "", newestReply: at("@14:38"), lastTurnEnd: 0n,
};
const paneRota: PaneInit = {
  id: "%14", host: "halyard", session: "lighthouse", pane: 14, cwd: "/home/owner/code/lighthouse", command: "claude",
  state: "done",
  summary: "The contract for rota-export is accepted and the epic is created. I can read it or steer it from here whenever you ask.",
  label: "rota-planning", changed: at("@13:50"), seen: at("@13:51"), question: "", context: "", activity: at("@13:50"),
  tool: "", claudeSession: claudeSession(14), questionHash: "", newestReply: at("@13:50"), lastTurnEnd: at("@13:50"),
};
const paneDocs: PaneInit = {
  id: "%17", host: "tarn", session: "tidepool", pane: 17, cwd: "/home/owner/code/tidepool", command: "claude",
  state: "done", summary: "README examples fixed and run in CI.", label: "docs-examples", changed: at("@09:41"),
  seen: at("@09:42"), question: "", context: "", activity: at("@09:41"), tool: "", claudeSession: claudeSession(17),
  questionHash: "", newestReply: at("@09:41"), lastTurnEnd: at("@09:41"),
};
const paneTide: PaneInit = {
  id: "%18", host: "tarn", session: "tidepool", pane: 18, cwd: "/home/owner/code/tidepool", command: "claude",
  state: "done", summary: "Migration merged; the old table is dropped.", label: "tide-tables", changed: at("@-2d 14:41"),
  seen: at("@-2d 14:42"), question: "", context: "", activity: at("@-2d 14:41"), tool: "",
  claudeSession: claudeSession(18), questionHash: "", newestReply: at("@-2d 14:41"), lastTurnEnd: at("@-2d 14:41"),
};
const paneCi: PaneInit = {
  id: "%19", host: "halyard", session: "quarry", pane: 19, cwd: "/home/owner/code/quarry", command: "claude",
  state: "done", summary: "Cache keys trimmed to the lockfile hash.", label: "ci-cache", changed: at("@-3d 14:41"),
  seen: at("@-3d 14:42"), question: "", context: "", activity: at("@-3d 14:41"), tool: "",
  claudeSession: claudeSession(19), questionHash: "", newestReply: at("@-3d 14:41"), lastTurnEnd: at("@-3d 14:41"),
};

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
const reply = (time: bigint, text: string): ItemInit => ({ kind: "reply", time, text });

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
  "`Flush` now retries a failed batch in place. Still open: there is no jitter, so lamps that drop together retry together, and retries never give up.",
].join("\n");

const lampLongBash =
  "go test ./internal/telemetry/... -run 'Backoff|Jitter|RetryWindow|SpoolAfterWindow|FlushRetriesInPlace' -count=1 -race -timeout 120s";

const lampItems: ItemInit[] = [
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
  ...call(8, at("@14:32:20"), "Bash", lampLongBash, "ok  \tlighthouse/internal/telemetry\t0.44s"),
];

const beaconReply = [
  "The nightly backfill scans every shift because the rotation table has no index on `keeper_id`. Two ways to fix it:",
  "",
  "1. Composite index on `(keeper_id, shift_start)`",
  "2. Partial index on open shifts only",
].join("\n");

const beaconItems: ItemInit[] = [
  prompt(at("@14:24"), "owner", "The nightly backfill takes forty minutes. Find out why and fix it."),
  ...call(1, at("@14:24:30"), "Grep", "keeper_id"),
  ...call(2, at("@14:25"), "Read", "internal/rota/backfill.go"),
  ...call(3, at("@14:26"), "Read", "migrations/0007_rotation.sql"),
  ...call(4, at("@14:27"), "Read", "internal/rota/query.go"),
  ...call(5, at("@14:28"), "Bash", 'psql -c "EXPLAIN ANALYZE SELECT * FROM rotation WHERE keeper_id = 42"',
    "Seq Scan on rotation (cost=0.00..48210.00 rows=1 width=64)"),
  reply(at("@14:29"), beaconReply),
];

const rotaPlanningItems: ItemInit[] = [
  prompt(at("@13:50"), "owner", "Why did csv-writer need a retry?"),
  ...call(1, at("@13:50:05"), "epic_get", "rota-export", "rota-export · building · 2 of 7 units integrated"),
  reply(at("@13:50:20"),
    "Its first try, on haiku, made the same edit three times, so the monitor ended it at 13:58 and the planner retried on sonnet. The retry integrated at 3f9c2e1, which proves R3."),
];

// paneStreams holds the Panes.WatchConversation answer of each pane that has a conversation, by pane id.
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
  origin: "",
  goal: "Stop the telemetry uploader from losing readings when the collector is unreachable.",
  waiting: [
    { text: "Also log the batch id whenever a batch is spooled.", time: at("@14:36:10"), canceled: false },
    { text: "Then put the 5-minute cap in the README.", time: at("@14:38:40"), canceled: false },
  ],
  context: { tokens: 30000n, window: 200000n },
}, [{
  messageId: "msg_lamp_live", index: 0, parentToolUseId: "", kind: "text",
  text: "Full jitter is in: each delay is drawn uniformly between zero and the capped value. Now wiring the 5-minute window into Flush, so a batch that outlives it goes to the on-disk spool instead of being dro",
}]));

paneStreams.set("%12", paneStream("%12", paneBeacon, beaconItems, {
  goal: "", waiting: [], context: { tokens: 18000n, window: 200000n },
}));

paneStreams.set("%14", paneStream("%14", paneRota, rotaPlanningItems, { waiting: [] }));

// The other panes hold one reply: their summary, at the time they changed.
for (const [id, pane, changed] of [
  ["%13", paneGraft, at("@14:21")],
  ["%15", paneMoon, at("@14:02")],
  ["%16", paneHarvest, at("@14:38")],
  ["%17", paneDocs, at("@09:41")],
  ["%18", paneTide, at("@-2d 14:41")],
  ["%19", paneCi, at("@-3d 14:41")],
] as const) {
  paneStreams.set(id, paneStream(id, pane, [reply(changed, pane.summary ?? "")], {}));
}

// panes is the Panes.List answer: the panes, in the order the list draws them.
export const panes = create(PanesListResponseSchema, {
  panes: [paneBeacon, paneLamp, paneGraft, paneMoon, paneHarvest, paneRota, paneDocs, paneTide, paneCi],
});
