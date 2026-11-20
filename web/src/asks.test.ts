import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  BackgroundTaskSchema,
  ConversationStateSchema,
  HeadlessSessionSchema,
  PaneSchema,
  type BackgroundTask,
  type ConversationState,
} from "./gen/orrery/v1/orrery_pb";
import { EpicSchema, EpicsGetResponseSchema, type Epic, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { type Conversation } from "./conversation";
import { askingWords, askLines, headBadges, ownAsks, steeredEpics, type AskLine } from "./asks";
import { type Row } from "./rows";

function makeRow(init: Partial<Row>): Row {
  return {
    putOff: false,
    tool: "",
    key: "pane:box:%3",
    id: "%3",
    archiveId: "conv-1",
    kind: "pane",
    label: "api",
    project: "/work/api",
    host: "box",
    state: "waiting",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    ...init,
  };
}

type EpicInit = MessageInitShape<typeof EpicSchema>;
type DetailInit = MessageInitShape<typeof EpicsGetResponseSchema>;

// A live, unarchived epic with no steering unless the init says otherwise.
function makeEpic(init: EpicInit): Epic {
  return create(EpicSchema, { repository: "repo-a", live: true, ...init });
}

// A response as serve sends it. What the init leaves out is false, empty or zero.
function makeDetail(init: DetailInit): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// The ask line of one epic, from its detail alone.
function lineFor(epic: Epic, init: DetailInit): AskLine[] {
  return askLines([epic], new Map([[epic.id, makeDetail(init)]]));
}

function makeConversation(state: ConversationState | undefined): Conversation {
  return { transcript: "", seq: 0n, items: [], oldest: 0n, more: false, state, unfinished: [], queued: [], unsent: [] };
}

function makeTask(init: MessageInitShape<typeof BackgroundTaskSchema>): BackgroundTask {
  return create(BackgroundTaskSchema, init);
}

const rotaLine: AskLine = { epic: "repo-a/rota-export", slug: "rota-export", text: "asks: Use Postgres?", way: "Answer" };

test("Band: steeredEpics keeps listing order and only live, unarchived epics whose steering is the id, never another conversation's", () => {
  const listed = [
    makeEpic({ id: "repo-a/zeta", slug: "zeta", steering: "conv-1" }),
    makeEpic({ id: "repo-a/archived", slug: "archived", steering: "conv-1", archived: true }),
    makeEpic({ id: "repo-a/other", slug: "other", steering: "conv-2" }),
    makeEpic({ id: "repo-a/ended", slug: "ended", steering: "conv-1", live: false }),
    makeEpic({ id: "repo-a/alpha", slug: "alpha", steering: "conv-1" }),
  ];
  assert.deepEqual(
    steeredEpics("conv-1", listed).map((epic) => epic.id),
    ["repo-a/zeta", "repo-a/alpha"],
  );
});

test("Band: steeredEpics with an empty id keeps none, even an epic with no steering", () => {
  const listed = [makeEpic({ id: "repo-a/alpha", slug: "alpha", steering: "" })];
  assert.deepEqual(steeredEpics("", listed), []);
});

test("Band: askLines gives a question an Answer line, its text the question with the slug removed", () => {
  const epic = makeEpic({ id: "repo-a/rota-export", slug: "rota-export" });
  assert.deepEqual(lineFor(epic, { questions: [{ id: "Q1", text: "Use Postgres?" }] }), [
    { epic: "repo-a/rota-export", slug: "rota-export", text: "asks: Use Postgres?", way: "Answer" },
  ]);
});

test("Band: askLines gives a park a Decide line, even with a question open", () => {
  const epic = makeEpic({ id: "repo-a/notices", slug: "notices" });
  assert.deepEqual(lineFor(epic, { parked: "R5 is already met at the epic’s base", questions: [{ id: "Q1", text: "Ship?" }] }), [
    { epic: "repo-a/notices", slug: "notices", text: "is parked: R5 is already met at the epic’s base", way: "Decide" },
  ]);
});

test("Band: askLines gives a failed release step an Open line with its text less the slug", () => {
  const epic = makeEpic({ id: "repo-a/orbit", slug: "orbit" });
  assert.deepEqual(
    lineFor(epic, { releaseSteps: [{ position: 2n, step: "CI green on the tag", state: "failed", reason: "tag exists" }] }),
    [{ epic: "repo-a/orbit", slug: "orbit", text: "release step 2 (CI green on the tag) failed: tag exists", way: "Open" }],
  );
});

test("Band: askLines gives another blocker an Open line with its text less the slug", () => {
  const epic = makeEpic({ id: "repo-a/web", slug: "web" });
  assert.deepEqual(lineFor(epic, { steps: [{ id: "s1", task: "web-line", held: true }] }), [
    { epic: "repo-a/web", slug: "web", text: "web-line held", way: "Open" },
  ]);
});

test("Band: askLines gives an epic with no needs no line", () => {
  const epic = makeEpic({ id: "repo-a/quiet", slug: "quiet" });
  assert.deepEqual(lineFor(epic, {}), []);
});

test("PhoneHead: askingWords says nothing when nothing asks", () => {
  assert.equal(askingWords(null, []), "");
});

test("PhoneHead: askingWords names the conversation when only its own question asks", () => {
  assert.equal(askingWords("api", []), "api asks");
});

test("PhoneHead: askingWords names the one epic that asks", () => {
  assert.equal(askingWords(null, [rotaLine]), "rota-export asks");
});

test("PhoneHead: askingWords counts two asks as 2 ask you, the own question included", () => {
  assert.equal(askingWords(null, [rotaLine, { ...rotaLine, slug: "orbit" }]), "2 ask you");
  assert.equal(askingWords("api", [rotaLine]), "2 ask you");
});

test("PhoneHead: ownAsks is true for a waiting row whose pane and session are not put off", () => {
  const row = makeRow({ state: "waiting" });
  assert.equal(ownAsks(row, makeConversation(create(ConversationStateSchema, {}))), true);
});

test("PhoneHead: ownAsks is false when the row is not waiting", () => {
  const row = makeRow({ state: "idle" });
  assert.equal(ownAsks(row, makeConversation(create(ConversationStateSchema, {}))), false);
});

test("PhoneHead: ownAsks is false when the pane's question is put off", () => {
  const row = makeRow({ state: "waiting" });
  const state = create(ConversationStateSchema, { pane: create(PaneSchema, { putOff: true }) });
  assert.equal(ownAsks(row, makeConversation(state)), false);
});

test("PhoneHead: ownAsks is false when the session's question is put off", () => {
  const row = makeRow({ kind: "session", state: "waiting" });
  const state = create(ConversationStateSchema, { session: create(HeadlessSessionSchema, { putOff: true }) });
  assert.equal(ownAsks(row, makeConversation(state)), false);
});

test("PhoneHead: headBadges gives each steered epic a badge, then the running agents and shells", () => {
  const epics = [
    makeEpic({ id: "repo-a/rota-export", slug: "rota-export", steering: "conv-1" }),
    makeEpic({ id: "repo-a/orrery-followups", slug: "orrery-followups", steering: "conv-1" }),
  ];
  const background = [
    makeTask({ id: "a1", type: "agent", status: "running" }),
    makeTask({ id: "a2", type: "agent", status: "done" }),
    makeTask({ id: "s1", type: "shell", status: "running" }),
    makeTask({ id: "s2", type: "shell", status: "running" }),
    makeTask({ id: "a3", type: "agent", status: "running" }),
  ];
  assert.deepEqual(headBadges(epics, [rotaLine], background), [
    { key: "repo-a/rota-export", label: "rota", count: 0, asks: true },
    { key: "repo-a/orrery-followups", label: "orrery", count: 0, asks: false },
    { key: "agents", label: "agents", count: 2, asks: false },
    { key: "running", label: "running", count: 2, asks: false },
  ]);
});

test("PhoneHead: headBadges draws no agents or running badge when no task runs", () => {
  const epics = [makeEpic({ id: "repo-a/rota-export", slug: "rota-export", steering: "conv-1" })];
  const background = [makeTask({ id: "a2", type: "agent", status: "done" })];
  assert.deepEqual(headBadges(epics, [], background), [
    { key: "repo-a/rota-export", label: "rota", count: 0, asks: false },
  ]);
});

test("WaitsOnYou: ownAsks is false for an archived waiting row, so the Archived view's conversation adds no ask", () => {
  const row = makeRow({ state: "waiting", archived: true });
  assert.equal(ownAsks(row, makeConversation(create(ConversationStateSchema, {}))), false);
});

test("WaitsOnYou: ownAsks is false for a waiting row put off on the list", () => {
  const row = makeRow({ state: "waiting", putOff: true });
  assert.equal(ownAsks(row, makeConversation(create(ConversationStateSchema, {}))), false);
});

test("WaitsOnYou: ownAsks is true for a waiting row neither archived nor put off", () => {
  const row = makeRow({ state: "waiting", archived: false, putOff: false });
  assert.equal(ownAsks(row, makeConversation(create(ConversationStateSchema, {}))), true);
});

test("WaitsOnYou: askingWords leaves an archived row's own question out", () => {
  const archived = makeRow({ state: "waiting", archived: true });
  const own = ownAsks(archived, makeConversation(create(ConversationStateSchema, {}))) ? archived.label : null;
  assert.equal(askingWords(own, []), "");
});
