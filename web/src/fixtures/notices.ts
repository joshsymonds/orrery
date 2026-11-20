// The notice fixtures: the history the Notices service lists and the notices its Watch shows, shaped as the server shapes
// them. A session notice is titled "<dir> · session <first 8 of id>", an epic's by its id, and a usage limit's "usage
// limit" under kind@reset. Every epic notice reports waiting (internal/planner/attention.go attentionNotice); a
// question's body is its Q line and advice lines, a release step's its tell and advice, and a limit's limitBody's. The
// times are the fixtures' instant's, in the browser's UTC zone (clock.ts).

import { create } from "@bufbuild/protobuf";

import { type InAppNotice, InAppNoticeSchema, type ListedNotice, ListedNoticeSchema } from "../gen/orrery/v1/notice_pb";
import { at } from "./clock";

// noticeHistory is the notice history Notices.List answers with, newest first. tidepool's conversation and rota-export
// are unread; the rest are read.
export const noticeHistory: readonly ListedNotice[] = [
  create(ListedNoticeSchema, {
    key: { session: "sess-beacon-12" },
    state: "waiting",
    title: "tidepool · session sess-bea",
    body: "Why the roster page is slow past forty shifts: which fix should I make?",
    time: at("@14:41"),
    read: false,
  }),
  create(ListedNoticeSchema, {
    key: { epic: "lighthouse/rota-export" },
    state: "waiting",
    title: "lighthouse/rota-export",
    body: [
      "Q1: Bump the embedded tz database? The pinned database lacks the 2026 Lisbon rule, so one fixture cannot pass. A bump changes go.mod, which is outside this unit’s owned files, and the contract says no dependency changes.",
      "  Keep the pin: Skip the Lisbon case. The contract holds: R5 is met for every port on today’s rota.",
      "  Allow the bump: A contract change. Adds go.mod to this task and reruns the full gate.",
      "  other: answer in your own words",
    ].join("\n"),
    block: { kind: "question", ref: "Q1" },
    time: at("@14:39"),
    read: false,
  }),
  create(ListedNoticeSchema, {
    key: { epic: "orrery/holder-scopes" },
    state: "waiting",
    title: "orrery/holder-scopes",
    body: "All four requirements proven. No gaps.",
    block: { kind: "report", ref: "" },
    time: at("@09:52"),
    read: true,
  }),
  create(ListedNoticeSchema, {
    key: { epic: "tidepool/offline-tables" },
    state: "waiting",
    title: "tidepool/offline-tables",
    body: ["CI is red on the tag. The step has no undo.", "Q2 release:", "  other: answer in your own words"].join("\n"),
    block: { kind: "release_step", ref: "2" },
    time: at("@09:31"),
    read: true,
  }),
  create(ListedNoticeSchema, {
    key: { limit: `weekly@${at("@15:10")}` },
    state: "limited",
    title: "usage limit",
    body: "Held by the weekly limit, reset 2026-11-14T15:10:00Z: orrery-followups, rota-export",
    time: at("@08:47"),
    read: true,
  }),
  create(ListedNoticeSchema, {
    key: { session: "8b0e5d21-6c4a-4f19-a7e3-2d9c1b5f0a47" },
    state: "done",
    title: "orchard · session 8b0e5d21",
    body: "Parser tests for nested scion grafting: all 41 cases pass.",
    time: at("@-1d 18:20"),
    read: true,
  }),
  create(ListedNoticeSchema, {
    key: { session: "c41f9a07-2e8b-4d63-9a15-7f0b3e6c2d98" },
    state: "failed",
    title: "kelp · session c41f9a07",
    body: "Kelp bed survey importer: the API refused the request.",
    time: at("@-1d 16:02"),
    read: true,
  }),
];

// noticeStack is what Notices.Watch shows at the desk: the five notices that are showing, in the order the stack draws
// them. Each is a history row's own notice, without its time or read mark.
export const noticeStack: readonly InAppNotice[] = noticeHistory.slice(0, 5).map((notice) =>
  create(InAppNoticeSchema, {
    key: notice.key,
    state: notice.state,
    title: notice.title,
    body: notice.body,
    block: notice.block,
  }),
);
