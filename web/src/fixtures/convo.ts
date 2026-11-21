// The fixture slices of the conversation page stories: round 6's conversation scenarios, each drawn as the conversation the
// server sends for it, with the item kinds and senders the server sends. convoSlices is the shipped slices with the panes
// and streams these scenarios add, and the shipped panes and streams are never written to.

import { clone, create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  ConversationItemSchema,
  type ConversationItem,
  type Pane,
  PaneSchema,
  PanesListResponseSchema,
  StepFactsSchema,
  WatchConversationResponseSchema,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";
import { call, type ItemInit, prompt, reply } from "./panes";
import { type Slices, shippedSlices } from "./slices";

// conversationCall is the procedure a conversation is streamed by, which the loading story holds.
const conversationCall = "orrery.v1.Panes/WatchConversation";

const rota = "lighthouse/rota-export";

// epicEvent is an epic event orreryd writes into a transcript, with its kind, the epic it is about and its body.
const epicEvent = (time: bigint, kind: string, epic: string, body: string): ItemInit =>
  prompt(time, "event", `<orrery-event kind="${kind}" id="epic:${epic}:A1">\n${body}\n</orrery-event>`);

// StepFactsInit is the facts of a step's end, as the server attaches them to the end's item, as a plain object.
type StepFactsInit = MessageInitShape<typeof StepFactsSchema>;

// stepEnd is a step's end: the step_end block the step's caller is told in, with its facts on the item, which carries the step
// as its prompt id.
function stepEnd(time: bigint, step: string, attrs: { agent: string; end: string; status: string }, body: string, facts: StepFactsInit): ItemInit {
  const text = `<orrery-event kind="step_end" id="step:${step}" agent="${attrs.agent}" end="${attrs.end}" status="${attrs.status}" epic="${rota}">\n${body}\n</orrery-event>`;
  return { ...prompt(time, "event", text), promptId: `step:${step}`, stepFacts: create(StepFactsSchema, facts) };
}

// rotaCreated is the call that created rota-export, and its result: an epic_create call that names the epic it creates.
const rotaCreated: ItemInit[] = [
  { kind: "tool", time: at("@-2d 13:50"), id: "toolu_epic_rota", tool: "mcp__orrery__epic_create", input: "rota-export", epic: rota },
  { kind: "result", time: at("@-2d 13:50"), id: "toolu_epic_rota", text: "created", error: false },
];

// blocksItems is round 6's Every Block transcript: the rota export planned, its steps ended, two check rounds, and the epic's
// lines.
const blocksItems: ItemInit[] = [
  prompt(at("@-2d 13:48"), "owner", "Plan the rota export as an epic. Six requirements; fixtures must cover every port zone."),
  ...rotaCreated,
  prompt(at("@-2d 13:58"), "event", "The planner outlined 7 tasks for **rota-export**. Two start now; five wait on them."),
  stepEnd(at("@-2d 14:06"), "st-rota-csv", { agent: "csv-writer", end: "done", status: "done" },
    "Wrote the CSV writer and its row-per-shift test. 21 rows for the fixture week.\nMore: the full return is in the step's log.",
    { rounds: 1, seconds: 480n, counted: true, files: 2n, added: 96n, removed: 4n }),
  stepEnd(at("@-2d 14:11"), "st-rota-store", { agent: "shift-store", end: "checks_failed", status: "failed" },
    "Stored shifts per keeper and week.\nThe gate failed: `just check` exited 1 in store_test.go:27.\nMore: the full return is in the step's log.",
    { rounds: 3, seconds: 780n, counted: true, files: 4n, added: 141n, removed: 12n }),
  stepEnd(at("@-2d 14:12"), "st-rota-audit", { agent: "audit-log", end: "stopped", status: "stopped" },
    "Stopped by you before its first check round.\nMore: the full return is in the step's log.",
    { rounds: 0, seconds: 60n, counted: false, files: 0n, added: 0n, removed: 0n }),
  prompt(at("@-2d 14:14"), "engine", "owned files: the change touches go.mod, which this task does not own.\nRevert go.mod, or ask the planner to widen the task.", 2),
  prompt(at("@-2d 14:19"), "engine", "All three checks pass.", 3),
  epicEvent(at("@-2d 14:21"), "park", "tidepool/offline-tables", "Parked: the release gate needs a decision only you can make."),
  epicEvent(at("@-2d 14:22"), "release", "orrery/holder-scopes", "Released v0.45.0. All four requirements are proven."),
  epicEvent(at("@-2d 14:23"), "end", "tidepool/tide-cache", "Ended: superseded by offline-tables."),
  epicEvent(at("@-2d 14:24"), "question", rota, "tz-fixtures asks: bump the embedded tz database?"),
];

// linesItems is round 6's Every Line and Rule transcript: a thought, tool calls with a failed one, a run of five, three notices,
// three agent finishes, an interrupt, a compaction, the brief and engine folds, and the reply.
const linesItems: ItemInit[] = [
  prompt(at("@09:12"), "owner", "Find out why the roster page is slow for weeks with more than forty shifts."),
  ...call(1, at("@09:13"), "Read", "internal/roster/handler.go"),
  ...call(2, at("@09:14"), "Grep", "keeperFor\\(", "14 matches in 3 files"),
  ...call(3, at("@09:20"), "Bash", "go test ./internal/roster -run Slow -count=1",
    "--- FAIL: TestSlowWeek (4.21s)\n    roster_test.go:88: week of 44 shifts took 4.1s, want under 500ms\nFAIL", true),
  ...call(4, at("@09:30"), "Read", "internal/roster/query.go"),
  ...call(5, at("@09:31"), "Read", "internal/roster/keeper.go"),
  ...call(6, at("@09:32"), "Read", "internal/roster/roster_test.go"),
  ...call(7, at("@09:35"), "Edit", "internal/roster/query.go", "Added 9 lines, removed 22 lines"),
  ...call(8, at("@09:38"), "Bash", "go vet ./...", "vet: unused variable", true),
  { kind: "notice", time: at("@09:39"), notice: "task", text: "Task 3 of 4 is complete: one query per week instead of one per shift." },
  { kind: "notice", time: at("@09:39:20"), notice: "teammate", text: "reviewer joined to read the diff." },
  { kind: "notice", time: at("@09:39:40"), notice: "error", error: true, text: "The API refused the request: overloaded. Retrying in 8 s." },
  { kind: "agent_finished", time: at("@09:40"), id: "toolu_agent_explore", tool: "Explore", text: "Find every caller of keeperFor", notice: "done" },
  { kind: "agent_finished", time: at("@09:40:20"), id: "toolu_agent_review", tool: "reviewer", text: "Review the query change", notice: "failed" },
  { kind: "agent_finished", time: at("@09:40:40"), id: "toolu_agent_check", tool: "general-purpose", text: "Check the week's totals against the fixture", notice: "stopped" },
  { kind: "interrupted", time: at("@09:41") },
  { kind: "compaction", time: at("@09:42"), trigger: "manual", tokens: 188000n,
    text: "The roster page was slow because each shift row loaded its keeper separately. The handler now loads a week in one query. One vet warning is open." },
  prompt(at("@09:42:10"), "brief", "Implement the week query. Own `internal/roster/query.go` and its test. Do not change `go.mod`."),
  prompt(at("@09:42:30"), "engine", "Round 2: the owned-files check failed. Revert go.mod.\nThe other checks passed."),
  reply(at("@09:44"), "One query per week is in. The slow test now passes in 31 ms."),
];

// pane is a terminal pane of halyard, with the Claude session the conversation streams from.
function convoPane(id: string, session: string, cwd: string, summary: string, state: string, changed: bigint, seen = changed): Pane {
  return create(PaneSchema, {
    id, host: "halyard", session, pane: Number(id.slice(1)), cwd, command: "claude", state, summary, label: "",
    changed, seen, question: "", context: "", activity: changed, tool: "",
    claudeSession: `5e0e0000-0000-4000-8000-${id.slice(1).padStart(12, "0")}`, questionHash: "",
    newestReply: changed, lastTurnEnd: changed, orreryId: "",
  });
}

// stream is the Panes.WatchConversation answer of pane, holding items and the unfinished blocks given.
function stream(pane: Pane, items: ItemInit[], unfinished: MessageInitShape<typeof WatchConversationResponseSchema>["unfinished"] = []): WatchConversationResponse {
  return create(WatchConversationResponseSchema, {
    transcript: `${pane.id}.jsonl`, seq: 1n, reset: true,
    items: items.map((init: ItemInit): ConversationItem => create(ConversationItemSchema, init)),
    unfinished, state: { pane },
  });
}

const blocksPane = convoPane("%25", "lighthouse", "/home/owner/code/lighthouse", "Planned the rota export, and steers its epic", "done", at("@14:24"));
const linesPane = convoPane("%26", "tidepool", "/home/owner/code/tidepool", "Why the roster page is slow past forty shifts", "done", at("@09:44"), at("@09:43"));
const emptyPane = convoPane("%27", "tidepool", "/home/owner/code/tidepool", "New session", "", at("@14:41"));
const loadingPane = convoPane("%28", "tidepool", "/home/owner/code/tidepool", "Tide table cache warm-up", "done", at("@14:37"));

// convoSlices is the shipped slices with the four panes of these scenarios listed and their streams served.
export const convoSlices: Slices = {
  ...shippedSlices,
  panes: create(PanesListResponseSchema, { panes: [...shippedSlices.panes.panes, blocksPane, linesPane, emptyPane, loadingPane] }),
  paneStreams: new Map([
    ...shippedSlices.paneStreams,
    ["%25", stream(blocksPane, blocksItems)],
    ["%26", stream(linesPane, linesItems, [{
      messageId: "msg_roster_think", index: 0, parentToolUseId: "", kind: "thinking",
      text: "The page renders one row per shift and each row asks for its keeper. That would be one query per row. Check the handler before guessing.",
    }])],
    ["%27", stream(emptyPane, [])],
    ["%28", stream(loadingPane, [])],
  ]),
};

// convoLoading is convoSlices with the conversation call held, so a page that opens pane %28 waits for its first answer.
export const convoLoading: Slices = { ...convoSlices, hold: new Set([conversationCall]) };

// convoWorkingSlices is convoSlices' shipped slices with pane %11 last seen at 14:31:30, so the reader draws the since-you-looked
// rule after the 14:31 prompt and before the tool calls that follow it. The pane's list row and its stream's state both hold it.
const workingSeen = at("@14:31:30");

// withSeen is a copy of pane that was last seen at seen.
function withSeen(pane: Pane, seen: bigint): Pane {
  const copy = clone(PaneSchema, pane);
  copy.seen = seen;
  return copy;
}

// workingStream is pane %11's shipped stream with its state's pane last seen at workingSeen.
function workingStream(): WatchConversationResponse {
  const stream = shippedSlices.paneStreams.get("%11");
  const state = stream?.state;
  if (stream === undefined || state?.pane === undefined) {
    throw new Error("fixtures: pane %11's stream holds no pane");
  }
  return create(WatchConversationResponseSchema, {
    ...stream,
    state: { ...state, pane: withSeen(state.pane, workingSeen) },
  });
}

export const convoWorkingSlices: Slices = {
  ...shippedSlices,
  panes: create(PanesListResponseSchema, {
    panes: shippedSlices.panes.panes.map((pane) => (pane.id === "%11" ? withSeen(pane, workingSeen) : pane)),
  }),
  paneStreams: new Map(shippedSlices.paneStreams).set("%11", workingStream()),
};

// convoSentWords are the words convo-blocks types into its reply box and sends. The say call is held, so they stay sending.
export const convoSentWords = "Keep the pin and skip the Lisbon case.";

// convoSendingSlices is convoSlices with Control.Say held unanswered, so a message the reply box sends stays sending.
export const convoSendingSlices: Slices = { ...convoSlices, hold: new Set(["orrery.v1.Control/Say"]) };
