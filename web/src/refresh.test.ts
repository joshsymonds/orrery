import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { WatchResponseSchema } from "./gen/orrery/v1/orrery_pb";
import { type Affects, affectsOf, stepSessions } from "./refresh";

// A headless session the page lists as a row, and a step session a loaded epic holds.
const rowSession = "0123456789abcdef0123456789abcdef";
const stepSession = "step-1";
const steps: ReadonlySet<string> = new Set([stepSession]);

type Payload = NonNullable<MessageInitShape<typeof WatchResponseSchema>["event"]>;

function affects(sessionId: string, event: Payload): Affects {
  return affectsOf(create(WatchResponseSchema, { seq: 8n, sessionId, event }), steps);
}

const nothing: Affects = { rows: false, epics: false };
const rowsOnly: Affects = { rows: true, epics: false };
const epicsOnly: Affects = { rows: false, epics: true };
const both: Affects = { rows: true, epics: true };

test("Affects: an engine_text changes nothing, whosever it is", () => {
  const text: Payload = { case: "engineText", value: { messageId: "m1", index: 0, kind: "text", text: "so far" } };
  assert.deepEqual(affects(rowSession, text), nothing);
  assert.deepEqual(affects(stepSession, text), nothing);
});

test("Affects: a background task changes nothing, whosever it is", () => {
  const task: Payload = { case: "engineTask", value: { taskId: "t1", status: "running" } };
  assert.deepEqual(affects(rowSession, task), nothing);
  assert.deepEqual(affects(stepSession, task), nothing);
});

test("Affects: a pane session's events change the rows", () => {
  assert.deepEqual(affects("%1", { case: "sessionAdded", value: { pane: "%1" } }), rowsOnly);
  assert.deepEqual(affects("%1", { case: "sessionChanged", value: {} }), rowsOnly);
  assert.deepEqual(affects("%1", { case: "sessionRemoved", value: {} }), rowsOnly);
});

test("Affects: a headless session's start changes the rows and may be a new step, and a known step's changes the epics", () => {
  const added: Payload = { case: "engineAdded", value: { dir: "/work/agent", state: "working" } };
  assert.deepEqual(affects(rowSession, added), both);
  assert.deepEqual(affects(stepSession, added), epicsOnly);
});

test("Affects: a state change and a prompt change a row's session, and a step's epics", () => {
  const changed: Payload = { case: "engineChanged", value: { state: "idle" } };
  const prompt: Payload = { case: "enginePrompt", value: { text: "go on" } };
  assert.deepEqual(affects(rowSession, changed), rowsOnly);
  assert.deepEqual(affects(rowSession, prompt), rowsOnly);
  assert.deepEqual(affects(stepSession, changed), epicsOnly);
  assert.deepEqual(affects(stepSession, prompt), epicsOnly);
});

test("Affects: only a finished main-conversation reply block changes a row", () => {
  const reply: Payload = { case: "engineBlock", value: { kind: "text", role: "assistant", text: "done" } };
  assert.deepEqual(affects(rowSession, reply), rowsOnly);
  const others: Payload[] = [
    { case: "engineBlock", value: { kind: "tool_call", role: "assistant", name: "Bash" } },
    { case: "engineBlock", value: { kind: "tool_result", role: "user" } },
    { case: "engineBlock", value: { kind: "thinking", role: "assistant" } },
    { case: "engineBlock", value: { kind: "text", role: "assistant", parentToolUseId: "tu1" } },
    { case: "engineBlock", value: { kind: "text", role: "user" } },
  ];
  for (const block of others) {
    assert.deepEqual(affects(rowSession, block), nothing, JSON.stringify(block.value));
  }
  assert.deepEqual(affects(stepSession, reply), nothing, "a step's reply changed something");
});

test("Affects: a request changes a row's newest reply unless it was refused", () => {
  assert.deepEqual(affects(rowSession, { case: "engineRequest", value: { kind: "permission" } }), rowsOnly);
  assert.deepEqual(affects(rowSession, { case: "engineRequest", value: { kind: "permission", refused: true } }), nothing);
  assert.deepEqual(affects(stepSession, { case: "engineRequest", value: { kind: "permission" } }), epicsOnly);
});

test("Affects: a turn's end, an interrupt and a withdrawn request change no row, and hint at a step's epics", () => {
  const payloads: Payload[] = [
    { case: "engineTurnEnded", value: { subtype: "success" } },
    { case: "engineInterrupt", value: { turning: true } },
    { case: "engineRequestCanceled", value: { requestId: "r1" } },
  ];
  for (const payload of payloads) {
    assert.deepEqual(affects(rowSession, payload), nothing, payload.case);
    assert.deepEqual(affects(stepSession, payload), epicsOnly, payload.case);
  }
});

test("Affects: an event of a kind the page does not know changes both", () => {
  assert.deepEqual(affects(rowSession, { case: undefined }), both);
});

test("stepSessions: holds each listed running step and each held detail's step", () => {
  const listed = [
    create(EpicSchema, { id: "repo-a/alpha", runningSteps: [{ id: "step-1" }, { id: "step-2" }] }),
    create(EpicSchema, { id: "repo-a/gamma" }),
  ];
  const details = new Map([
    ["repo-a/gamma", create(EpicsGetResponseSchema, { steps: [{ id: "step-3" }, { id: "step-1" }] })],
  ]);
  assert.deepEqual([...stepSessions(listed, details)].sort(), ["step-1", "step-2", "step-3"]);
});
