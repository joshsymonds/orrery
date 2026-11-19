// The sessions the fixture slices hold: the Sessions.List and Sessions.ListHeadless answers, and the conversation stream
// each session's Sessions.WatchConversation answers with.

import { create } from "@bufbuild/protobuf";

import {
  ListHeadlessResponseSchema,
  ListResponseSchema,
  SessionsWatchConversationResponseSchema,
  type SessionsWatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";
import { rotaGateFailOutput } from "./epics";
import { call, type ItemInit, prompt } from "./panes";

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

// sessions is the Sessions.List answer: no session is listed, so the rows come from the panes and the headless sessions.
export const sessions = create(ListResponseSchema, { sessions: [], seq: 0n });

// headless is the Sessions.ListHeadless answer: the one headless session orreryd holds.
export const headless = create(ListHeadlessResponseSchema, {
  sessions: [{
    id: "4be0c6d2-8f3a-4c71-9e25-ephemeris001", dir: "/home/owner/code/almanac", label: "ephemeris-import",
    state: "interrupted", changed: at("@12:41"), seen: at("@12:42"),
    summary: "orreryd restarted during the turn, and the turn stopped where it was.", question: "",
    newestReply: at("@12:40:30"),
  }],
});
