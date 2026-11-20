import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { type Conversation } from "./conversation";
import { rowFacts, stepFacts } from "./dockfacts";
import { type StepItem } from "./epics";
import { EpicStepSchema } from "./gen/orrery/v1/epics_pb";
import {
  ConversationItemSchema,
  ConversationStateSchema,
  HeadlessSessionSchema,
  OpenRequestSchema,
  PaneSchema,
  type ConversationItem,
  type ConversationState,
  type Pane,
} from "./gen/orrery/v1/orrery_pb";
import { type Row } from "./rows";

// The dock's facts, read from fixed stream fixtures. Each test's name starts with its group token, DockState.

function row(over: Partial<Row> = {}): Row {
  return {
    putOff: false,
    tool: "",
    key: "row",
    id: "%3",
    archiveId: "",
    kind: "pane",
    label: "fixing the build",
    project: "/home/dev/widgets",
    host: "laptop",
    state: "idle",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    ...over,
  };
}

function conversation(over: Partial<Conversation> = {}): Conversation {
  return {
    transcript: "",
    seq: 0n,
    items: [],
    oldest: 0n,
    more: false,
    state: undefined,
    unfinished: [],
    queued: [],
    unsent: [],
    ...over,
  };
}

function state(init: MessageInitShape<typeof ConversationStateSchema> = {}): ConversationState {
  return create(ConversationStateSchema, init);
}

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

function pane(init: MessageInitShape<typeof PaneSchema> = {}): Pane {
  return create(PaneSchema, init);
}

function step(over: Partial<StepItem> = {}): StepItem {
  return {
    kind: "step",
    key: "step-1",
    id: "step-1",
    epic: "orrery-conversation-dock",
    epicSlug: "orrery-conversation-dock",
    label: "deploy",
    doing: "",
    held: false,
    agent: "builder",
    started: 300,
    round: 1,
    pips: [],
    reason: "",
    step: create(EpicStepSchema, { id: "step-1", task: "deploy", agent: "builder", doing: "", held: false }),
    ...over,
  };
}

test("DockState: interrupting comes from the stream when the page's mark is off", () => {
  const facts = rowFacts(row(), conversation({ state: state({ interrupting: true }) }), false);
  assert.equal(facts.interrupting, true);
});

test("DockState: interrupting comes from the page's mark when the stream is not interrupting", () => {
  const facts = rowFacts(row(), conversation({ state: state({ interrupting: false }) }), true);
  assert.equal(facts.interrupting, true);
});

test("DockState: interrupting is false when neither the stream nor the mark says so", () => {
  const facts = rowFacts(row(), conversation({ state: state({ interrupting: false }) }), false);
  assert.equal(facts.interrupting, false);
});

test("DockState: interrupting_since and turn_since are read from the stream as unix seconds", () => {
  const facts = rowFacts(row(), conversation({ state: state({ interruptingSince: 500n, turnSince: 400n }) }), false);
  assert.equal(facts.interruptingSince, 500);
  assert.equal(facts.turnSince, 400);
});

test("DockState: a session's running call is its newest tool item that no later result answers", () => {
  const items = [
    item({ kind: "tool", id: "call-a", tool: "mcp__orrery__step_start", input: "deploy", time: 10n }),
    item({ kind: "tool", id: "call-b", tool: "Bash", input: "ls", time: 20n }),
    item({ kind: "result", id: "call-b", text: "ok" }),
  ];
  const facts = rowFacts(row({ kind: "session" }), conversation({ items }), false);
  assert.deepEqual(facts.tool, { name: "mcp__orrery__step_start", arg: "deploy", start: 10 });
});

test("DockState: a session has no running call once a result follows its tool item", () => {
  const items = [
    item({ kind: "tool", id: "call-a", tool: "Bash", input: "ls", time: 10n }),
    item({ kind: "result", id: "call-a", text: "ok" }),
  ];
  const facts = rowFacts(row({ kind: "session" }), conversation({ items }), false);
  assert.equal(facts.tool, null);
});

test("DockState: a pane's tool is read from its tool JSON", () => {
  const tool = JSON.stringify({ name: "mcp__orrery__step_start", input: "deploy the thing", start: 1234 });
  const facts = rowFacts(row({ kind: "pane" }), conversation({ state: state({ pane: pane({ tool }) }) }), false);
  assert.deepEqual(facts.tool, { name: "mcp__orrery__step_start", arg: "deploy the thing", start: 1234 });
});

test("DockState: a pane's tool has an empty arg when its input is not a string", () => {
  const tool = JSON.stringify({ name: "Bash", input: 7, start: 1234 });
  const facts = rowFacts(row({ kind: "pane" }), conversation({ state: state({ pane: pane({ tool }) }) }), false);
  assert.deepEqual(facts.tool, { name: "Bash", arg: "", start: 1234 });
});

test("DockState: a pane with an empty tool has no tool", () => {
  const facts = rowFacts(row({ kind: "pane" }), conversation({ state: state({ pane: pane({ tool: "" }) }) }), false);
  assert.equal(facts.tool, null);
});

test("DockState: a pane with no Claude session is a shell, and one with a Claude session is a pane", () => {
  const shell = rowFacts(
    row({ kind: "pane" }),
    conversation({ state: state({ pane: pane({ claudeSession: "" }) }) }),
    false,
  );
  const claude = rowFacts(
    row({ kind: "pane" }),
    conversation({ state: state({ pane: pane({ claudeSession: "session-1" }) }) }),
    false,
  );
  assert.equal(shell.kind, "shell");
  assert.equal(claude.kind, "pane");
});

test("DockState: a session row is a session", () => {
  const facts = rowFacts(row({ kind: "session" }), null, false);
  assert.equal(facts.kind, "session");
});

test("DockState: interruptedAt is the newest item's time when that item is interrupted", () => {
  const items = [
    item({ kind: "prompt", text: "go", time: 5n }),
    item({ kind: "interrupted", time: 9n }),
  ];
  const facts = rowFacts(row({ kind: "session" }), conversation({ items }), false);
  assert.equal(facts.interruptedAt, 9);
});

test("DockState: interruptedAt is 0 when an interrupted item is not the newest", () => {
  const items = [
    item({ kind: "interrupted", time: 9n }),
    item({ kind: "assistant", text: "back", time: 10n }),
  ];
  const facts = rowFacts(row({ kind: "session" }), conversation({ items }), false);
  assert.equal(facts.interruptedAt, 0);
});

test("DockState: putOff comes from the stream's request", () => {
  const request = create(OpenRequestSchema, { putOff: true });
  const facts = rowFacts(row(), conversation({ state: state({ request }) }), false);
  assert.equal(facts.putOff, true);
});

test("DockState: putOff comes from the stream's pane", () => {
  const facts = rowFacts(row(), conversation({ state: state({ pane: pane({ putOff: true }) }) }), false);
  assert.equal(facts.putOff, true);
});

test("DockState: reason is the row's summary for a session and empty for a pane", () => {
  const session = rowFacts(row({ kind: "session", summary: "tests fail" }), null, false);
  const paneFacts = rowFacts(row({ kind: "pane", summary: "tests fail" }), null, false);
  assert.equal(session.reason, "tests fail");
  assert.equal(paneFacts.reason, "");
});

test("DockState: name is the project's last element, else the row's label", () => {
  const named = rowFacts(row({ project: "/home/dev/widgets/" }), null, false);
  const unnamed = rowFacts(row({ project: "", label: "fixing the build" }), null, false);
  assert.equal(named.name, "widgets");
  assert.equal(unnamed.name, "fixing the build");
});

test("DockState: state is the stream pane's, then the stream session's, then the row's", () => {
  const waitingSession = create(HeadlessSessionSchema, { state: "waiting" });
  const paneWins = rowFacts(
    row({ state: "done" }),
    conversation({ state: state({ pane: pane({ state: "working" }), session: waitingSession }) }),
    false,
  );
  const sessionNext = rowFacts(
    row({ state: "done" }),
    conversation({ state: state({ session: create(HeadlessSessionSchema, { state: "waiting" }) }) }),
    false,
  );
  const rowOnly = rowFacts(row({ state: "done" }), conversation(), false);
  assert.equal(paneWins.state, "working");
  assert.equal(sessionNext.state, "waiting");
  assert.equal(rowOnly.state, "done");
});

test("DockState: changed is the stream's, else the row's", () => {
  const streamed = rowFacts(
    row({ changed: 600 }),
    conversation({ state: state({ pane: pane({ changed: 700n }) }) }),
    false,
  );
  const fromRow = rowFacts(row({ changed: 600 }), conversation(), false);
  assert.equal(streamed.changed, 700);
  assert.equal(fromRow.changed, 600);
});

test("DockState: a step's facts name the step, say working from its start, and take its running call", () => {
  const items = [item({ kind: "tool", id: "call-a", tool: "mcp__orrery__step_start", input: "deploy", time: 310n })];
  const facts = stepFacts(step({ held: false, started: 300 }), conversation({ items }));
  assert.equal(facts.kind, "step");
  assert.equal(facts.name, "deploy");
  assert.equal(facts.state, "working");
  assert.equal(facts.turnSince, 300);
  assert.deepEqual(facts.step, { label: "deploy", held: false, doing: "" });
  assert.deepEqual(facts.tool, { name: "mcp__orrery__step_start", arg: "deploy", start: 310 });
});

test("DockState: a held step's facts carry the held flag and no running call without a stream", () => {
  const facts = stepFacts(step({ held: true }), null);
  assert.deepEqual(facts.step, { label: "deploy", held: true, doing: "" });
  assert.equal(facts.tool, null);
});

test("DockState: a step's facts carry its doing, what the step is doing or why it is held", () => {
  const facts = stepFacts(step({ held: true, doing: "waits on your answer" }), null);
  assert.deepEqual(facts.step, { label: "deploy", held: true, doing: "waits on your answer" });
});
