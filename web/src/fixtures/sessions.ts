// The sessions the fixture slices hold: the Sessions.List and Sessions.ListHeadless answers, and the conversation stream
// each session's Sessions.WatchConversation answers with.

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  BackgroundTaskSchema,
  ListHeadlessResponseSchema,
  ListResponseSchema,
  SessionsWatchConversationResponseSchema,
  type SessionsWatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";
import { rotaGateFailOutput } from "./epics";
import { call, type ItemInit, prompt, reply } from "./panes";

const workerBrief = [
  "Brief from planner turn 2.",
  "Goal: R4, export refuses to overwrite an existing file.",
  "Owned files: cli/export.go, cli/export_test.go.",
  "Acceptance: TestExportRefusesOverwrite fails at 3e1f2ab and passes at your commit, and just check exits 0.",
].join("\n");
const workerGateNote =
  "Gate exit 1. Sent back with the gate's output.\n" + rotaGateFailOutput + "\nerror: recipe `check` failed with exit code 1";
const workerFindingNote =
  "Review finding F1, confirmed: export overwrites an existing file without asking. R4 says it must refuse.\n" +
  "cli/export.go:57 · a verifier reproduced it at 14:36 and sent it back into this session.";
const workerLongBash =
  "go test ./cli/... ./internal/rota/... -run 'TestExportRefusesOverwrite|TestExportRefusesSecondRun|TestExportWritesAuditLine' -count=1 -v";

const workerItems: ItemInit[] = [
  prompt(at("@14:07"), "brief", workerBrief),
  ...call(1, at("@14:07:30"), "Read", "cli/export.go"),
  ...call(2, at("@14:08:30"), "Read", "cli/root.go"),
  ...call(3, at("@14:09:30"), "Read", "internal/rota/csv.go"),
  ...call(4, at("@14:10:30"), "Write", "cli/export_test.go"),
  ...call(5, at("@14:12:00"), "Write", "cli/export.go"),
  ...call(6, at("@14:13:30"), "Edit", "cli/export.go"),
  ...call(7, at("@14:15:00"), "Edit", "cli/root.go"),
  ...call(8, at("@14:16:30"), "Bash", "go test ./cli/..."),
  ...call(9, at("@14:18:30"), "Bash", 'git commit -am "export: write a week as CSV"'),
  prompt(at("@14:19"), "engine", workerGateNote, 1),
  ...call(10, at("@14:20"), "Read", "cli/export.go"),
  ...call(11, at("@14:23"), "Edit", "cli/export.go"),
  ...call(12, at("@14:26"), "Edit", "cli/export_test.go"),
  ...call(13, at("@14:30"), "Bash", "just check"),
  prompt(at("@14:36:48"), "engine", workerFindingNote),
  ...call(14, at("@14:38:10"), "Edit", "cli/export.go", "Added 9 lines, removed 1 line"),
  ...call(15, at("@14:40:52"), "Bash", workerLongBash, null),
];

// The headless session that steers tidepool/buoy-feeds, which has two agents and a shell running.
const buoyFeedsId = "6c2f9e14-3a7b-4d58-9b0e-2f7a1c8d4e55";
const buoyFeedsItems: ItemInit[] = [
  prompt(at("@14:20"), "owner", "Serve the buoy feeds on :8710, and check the station list against NOAA's."),
  reply(at("@14:40"),
    "The server is listening on :8710. Two agents are still reading the feeds and the station list, and one has counted the " +
    "stale stations. The log tail has stopped."),
];
const buoyFeedsTasks: MessageInitShape<typeof BackgroundTaskSchema>[] = [
  {
    id: "bg-map-feeds", toolUseId: "toolu_bg_1", description: "Map the buoy feed formats", type: "agent", subagentType: "Explore",
    status: "running", activity: "Read feeds/ndbc.go", started: at("@14:37:58"),
  },
  {
    id: "bg-check-stations", toolUseId: "toolu_bg_2", description: "Check the station list against NOAA's", type: "agent",
    status: "running", activity: "Grep station ids", started: at("@14:39:10"),
  },
  {
    id: "bg-count-stale", toolUseId: "toolu_bg_3", description: "Count the stale stations", type: "agent",
    status: "done", started: at("@14:30"), ended: at("@14:33"),
  },
  {
    id: "bg-serve", toolUseId: "toolu_bg_4", description: "just serve --port 8710", type: "shell",
    status: "running", activity: "listening on :8710", started: at("@14:20"),
  },
  {
    id: "bg-tail-feeds", toolUseId: "toolu_bg_5", description: "tail -f logs/feeds.log", type: "shell",
    status: "stopped", started: at("@14:21"), ended: at("@14:35"),
  },
];

// The headless session of Tide table cache warm-up, which its owner stopped.
const warmupId = "9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90";
const warmupItems: ItemInit[] = [
  prompt(at("@-2d 14:02"), "owner", "Warm the tide table cache for the next 30 days, one port at a time."),
  ...call(1, at("@-2d 14:03"), "Read", "internal/tide/cache.go"),
  ...call(2, at("@-2d 14:04"), "Read", "internal/tide/ports.go"),
  ...call(3, at("@-2d 14:05"), "Bash", "just warm --port lisbon"),
  ...call(4, at("@-2d 14:07"), "Bash", "just warm"),
  reply(at("@-2d 14:09"),
    "The cache is warm for all 14 ports through December 17. Each port took between 2 and 9 seconds; the whole run took 71 seconds.\n\n" +
    "The warm-up is now a `just warm` recipe, and the README says when to run it."),
];

// sessionStreams holds the Sessions.WatchConversation answer of each session that has a conversation, by session id.
export const sessionStreams = new Map<string, SessionsWatchConversationResponse>();
sessionStreams.set("st-rota-t2-impl", create(SessionsWatchConversationResponseSchema, {
  transcript: "st-rota-t2-impl.jsonl",
  seq: 1n,
  reset: true,
  items: workerItems,
  state: {
    goal: "",
    waiting: [],
    context: { tokens: 88000n, window: 200000n },
    session: {
      id: "st-rota-t2-impl", dir: "/home/owner/code/lighthouse", label: "T2", state: "working",
      changed: at("@14:36:48"), seen: 0n, summary: "", question: "", newestReply: at("@14:40:40"),
    },
  },
  unfinished: [{
    messageId: "msg_t2_live", index: 0, parentToolUseId: "", kind: "text",
    text: "Export now checks for the file before it opens it, and returns ErrExists. Writing the test that proves a second export refus",
  }],
}));
sessionStreams.set(warmupId, create(SessionsWatchConversationResponseSchema, {
  transcript: `${warmupId}.jsonl`,
  seq: 1n,
  reset: true,
  items: warmupItems,
  state: {
    goal: "",
    waiting: [],
    context: { tokens: 152000n, window: 1000000n },
    session: {
      id: warmupId, dir: "/home/owner/code/tidepool", label: "Tide table cache warm-up", state: "stopped",
      changed: at("@-2d 14:41"), seen: at("@-2d 14:42"), summary: "You stopped it.", question: "", newestReply: at("@-2d 14:09"),
    },
  },
}));

sessionStreams.set(buoyFeedsId, create(SessionsWatchConversationResponseSchema, {
  transcript: `${buoyFeedsId}.jsonl`,
  seq: 1n,
  reset: true,
  items: buoyFeedsItems,
  state: {
    background: buoyFeedsTasks,
    session: {
      id: buoyFeedsId, dir: "/home/owner/code/tidepool", label: "Steering buoy-feeds", state: "working",
      changed: at("@14:40"), seen: at("@14:40"),
    },
  },
}));

// sessions is the Sessions.List answer: no session is listed, so the rows come from the panes and the headless sessions.
export const sessions = create(ListResponseSchema, { sessions: [], seq: 0n });

// headless is the Sessions.ListHeadless answer: the headless sessions orreryd holds, in id order.
export const headless = create(ListHeadlessResponseSchema, {
  sessions: [
    {
      id: "4be0c6d2-8f3a-4c71-9e25-ephemeris001", dir: "/home/owner/code/almanac",
      label: "The 2027 ephemeris import", headline: "The 2027 ephemeris import",
      state: "interrupted", changed: at("@12:41"), seen: at("@12:42"),
      summary: "Cut off mid-turn when the server restarted.", question: "", newestReply: at("@12:40:30"),
    },
    {
      id: buoyFeedsId, dir: "/home/owner/code/tidepool",
      label: "Steering buoy-feeds", headline: "Steering buoy-feeds",
      state: "working", changed: at("@14:40"), seen: at("@14:40"),
    },
    {
      id: warmupId, dir: "/home/owner/code/tidepool",
      label: "Tide table cache warm-up", headline: "Tide table cache warm-up",
      state: "stopped", changed: at("@-2d 14:41"), seen: at("@-2d 14:42"),
      summary: "You stopped it.", question: "", newestReply: at("@-2d 14:09"),
    },
    {
      id: "e7d3f08a-5b19-4c26-9f0e-1a8c6d2b4f73", dir: "/home/owner",
      label: "Swapping the phone’s font for a subset", headline: "Swapping the phone’s font for a subset",
      state: "waiting", changed: at("@14:33"), seen: at("@14:33:30"),
      summary: "Its turn ended on a question in words", question: "", newestReply: at("@14:33"),
    },
  ],
});
