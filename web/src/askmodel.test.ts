import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import type { Conversation } from "./conversation";
import {
  EpicQuestionChoiceSchema,
  EpicQuestionSchema,
  EpicRecommendationSchema,
  EpicSchema,
  type Epic,
  type EpicQuestion,
} from "./gen/orrery/v1/epics_pb";
import {
  ConversationStateSchema,
  HeadlessSessionSchema,
  OpenRequestSchema,
  PaneSchema,
  type ConversationState,
  type OpenRequest,
  type Pane,
  type HeadlessSession,
} from "./gen/orrery/v1/orrery_pb";
import { epicAsk, conversationAsk, parkAsk, staleWords } from "./askmodel";
import { rowsFrom, type Row } from "./rows";

const secondsPerDay = 86400;

function makePane(init: MessageInitShape<typeof PaneSchema>): Pane {
  return create(PaneSchema, { host: "box", session: "work", cwd: "/work/api", ...init });
}

function makeHeadless(init: MessageInitShape<typeof HeadlessSessionSchema>): HeadlessSession {
  return create(HeadlessSessionSchema, init);
}

function paneRow(init: MessageInitShape<typeof PaneSchema>): Row {
  const rows = rowsFrom([makePane({ id: "%3", state: "waiting", summary: "Plan the import", changed: 1794700000n, ...init })], []);
  return rows[0];
}

function headlessRow(init: MessageInitShape<typeof HeadlessSessionSchema>): Row {
  const rows = rowsFrom([], [], [makeHeadless({ id: "a1b2c3d4e5f60718293a4b5c6d7e8f90", dir: "/work/tidepool", headline: "Why the roster page is slow", state: "waiting", changed: 1794700100n, ...init })]);
  return rows[0];
}

function conversation(state: ConversationState): Conversation {
  return { transcript: "", seq: 0n, items: [], oldest: 0n, more: false, state, unfinished: [], queued: [], unsent: [] };
}

function requestOf(init: MessageInitShape<typeof OpenRequestSchema>): OpenRequest {
  return create(OpenRequestSchema, init);
}

const permissionOptions = [
  { n: 1, name: "Yes", does: "", recommended: false, why: "", answerable: true },
  { n: 2, name: "Yes, and don’t ask again", does: "for commands like this one in this session", recommended: false, why: "", answerable: true },
  { n: 3, name: "No", does: "and say what to do instead, below", recommended: false, why: "", answerable: true },
];

test("Ask: a pane AskUserQuestion draws its options with descriptions and question 1 of 2 for two questions", () => {
  const stored = JSON.stringify({
    questions: [
      {
        question: "Which database should the sample use?",
        options: [
          { label: "SQLite", description: "Embedded, one file" },
          { label: "Postgres", description: "" },
        ],
      },
      { question: "A second question", options: [{ label: "Ignored", description: "" }] },
    ],
  });

  const model = conversationAsk(paneRow({ id: "%3", orreryId: "orr-1", question: stored, questionHash: "h1" }), null);

  assert.deepEqual(model, {
    kind: "pane",
    who: "api · Plan the import · question 1 of 2",
    asked: 1794700000,
    question: "Which database should the sample use?",
    command: null,
    options: [
      { n: 1, name: "SQLite", does: "Embedded, one file", recommended: false, why: "", answerable: true },
      { n: 2, name: "Postgres", does: "", recommended: false, why: "", answerable: true },
    ],
    putOff: false,
    archive: { kind: "session", id: "orr-1" },
    target: { kind: "pane", id: "%3", questionHash: "h1", changed: 1794700000 },
    epic: null,
  });
});

test("Ask: a pane Bash permission reads its question and command with the three permission options in the design's words", () => {
  const stored = JSON.stringify({ tool: "Bash", input: { command: "make test" } });

  const model = conversationAsk(paneRow({ id: "%3", orreryId: "orr-1", question: stored, questionHash: "h2" }), null);

  assert.deepEqual(model, {
    kind: "permission",
    who: "api · Plan the import",
    asked: 1794700000,
    question: "Run this command?",
    command: { tool: "Bash", text: "make test" },
    options: permissionOptions,
    putOff: false,
    archive: { kind: "session", id: "orr-1" },
    target: { kind: "pane", id: "%3", questionHash: "h2", changed: 1794700000 },
    epic: null,
  });
});

test("Ask: a session question and a session permission draw their served options and target the open request", () => {
  const row = headlessRow({});
  const question = conversation(
    create(ConversationStateSchema, {
      request: requestOf({ id: "req-1", kind: "question", question: "Which fix should I make?", options: ["One query per week", "Cache keepers"] }),
    }),
  );
  const permission = conversation(
    create(ConversationStateSchema, {
      request: requestOf({ id: "req-2", kind: "permission", question: "Bash", options: ["allow", "deny"] }),
    }),
  );

  assert.deepEqual(conversationAsk(row, question), {
    kind: "session",
    who: "tidepool · Why the roster page is slow",
    asked: 1794700100,
    question: "Which fix should I make?",
    command: null,
    options: [
      { n: 1, name: "One query per week", does: "", recommended: false, why: "", answerable: true },
      { n: 2, name: "Cache keepers", does: "", recommended: false, why: "", answerable: true },
    ],
    putOff: false,
    archive: { kind: "headless", id: row.id },
    target: { kind: "headless", id: row.id, requestId: "req-1", options: ["One query per week", "Cache keepers"] },
    epic: null,
  });

  assert.deepEqual(conversationAsk(row, permission), {
    kind: "permission",
    who: "tidepool · Why the roster page is slow",
    asked: 1794700100,
    question: "Use Bash?",
    command: null,
    options: [
      { n: 1, name: "allow", does: "", recommended: false, why: "", answerable: true },
      { n: 2, name: "deny", does: "", recommended: false, why: "", answerable: true },
    ],
    putOff: false,
    archive: { kind: "headless", id: row.id },
    target: { kind: "headless", id: row.id, requestId: "req-2", options: ["allow", "deny"] },
    epic: null,
  });
});

test("Ask: a session request the phone cannot answer draws no options", () => {
  const session = conversation(
    create(ConversationStateSchema, { request: requestOf({ id: "req-3", kind: "question", question: "Paste the token", options: [] }) }),
  );
  const sessionModel = conversationAsk(headlessRow({}), session);
  assert.ok(sessionModel !== null);
  assert.deepEqual(sessionModel.options, []);
});

test("Ask: a pane's plain-text question is answered by the three permission answers, as today", () => {
  const model = conversationAsk(paneRow({ id: "%4", question: "Proceed with the rename?" }), null);

  assert.equal(model?.kind, "permission");
  assert.equal(model?.command, null);
  assert.deepEqual(
    model?.options.map((option) => [option.n, option.name]),
    [[1, "Yes"], [2, "Yes, and don’t ask again"], [3, "No"]],
  );
});

test("Ask: a pane AskUserQuestion option ending (Recommended) is named without the suffix and marked recommended", () => {
  const stored = JSON.stringify({
    questions: [
      {
        question: "Which database should the sample use?",
        options: [
          { label: "SQLite (Recommended)", description: "Embedded, one file" },
          { label: "Postgres", description: "" },
        ],
      },
    ],
  });

  const model = conversationAsk(paneRow({ id: "%3", orreryId: "orr-1", question: stored, questionHash: "h1" }), null);

  assert.deepEqual(model?.options, [
    { n: 1, name: "SQLite", does: "Embedded, one file", recommended: true, why: "", answerable: true },
    { n: 2, name: "Postgres", does: "", recommended: false, why: "", answerable: true },
  ]);
  assert.deepEqual(model?.target, { kind: "pane", id: "%3", questionHash: "h1", changed: 1794700000 });
});

test("Ask: a session option ending (Recommended) is named without the suffix and marked, and its target keeps the full served label", () => {
  const served = ["One query per week (Recommended)", "Cache keepers"];
  const row = headlessRow({});

  const model = conversationAsk(
    row,
    conversation(create(ConversationStateSchema, { request: requestOf({ id: "req-7", kind: "question", question: "Which fix?", options: served }) })),
  );

  assert.deepEqual(model?.options, [
    { n: 1, name: "One query per week", does: "", recommended: true, why: "", answerable: true },
    { n: 2, name: "Cache keepers", does: "", recommended: false, why: "", answerable: true },
  ]);
  assert.deepEqual(model?.target, { kind: "headless", id: row.id, requestId: "req-7", options: served });
});

test("Ask: putOff comes from the stream's open request or from its pane", () => {
  const putOffRequest = conversation(
    create(ConversationStateSchema, {
      request: requestOf({ id: "req-4", kind: "question", question: "Which?", options: ["A"], putOff: true }),
    }),
  );
  const putOffPane = conversation(
    create(ConversationStateSchema, { pane: makePane({ id: "%3", putOff: true }) }),
  );
  const awake = conversation(
    create(ConversationStateSchema, { request: requestOf({ id: "req-5", kind: "question", question: "Which?", options: ["A"] }) }),
  );
  const pane = paneRow({ id: "%3", question: JSON.stringify({ tool: "Bash", input: { command: "ls" } }), questionHash: "h3" });

  assert.equal(conversationAsk(headlessRow({}), putOffRequest)?.putOff, true);
  assert.equal(conversationAsk(pane, putOffPane)?.putOff, true);
  assert.equal(conversationAsk(headlessRow({}), awake)?.putOff, false);
  assert.equal(conversationAsk(pane, null)?.putOff, false);
});

test("Ask: the archive target is the row's session, and none for a pane with no orrery id", () => {
  const withId = conversationAsk(paneRow({ id: "%3", orreryId: "orr-9", question: "{}" }), null);
  const withoutId = conversationAsk(paneRow({ id: "%5", orreryId: "", question: JSON.stringify({ tool: "Bash", input: { command: "ls" } }) }), null);

  assert.deepEqual(withId?.archive, { kind: "session", id: "orr-9" });
  assert.equal(withoutId?.archive, null);
});

test("Ask: an option past the ninth is numbered but not answerable", () => {
  const options = Array.from({ length: 10 }, (_, index) => `Option ${index + 1}`);
  const model = conversationAsk(
    headlessRow({}),
    conversation(create(ConversationStateSchema, { request: requestOf({ id: "req-6", kind: "question", question: "Many?", options }) })),
  );

  assert.equal(model?.options[8].n, 9);
  assert.equal(model?.options[8].answerable, true);
  assert.equal(model?.options[9].n, 10);
  assert.equal(model?.options[9].answerable, false);
});

const epic: Epic = create(EpicSchema, {
  id: "harbour-rota/rota-export",
  repository: "harbour-rota",
  path: "/home/owner/Code/harbour",
  slug: "rota-export",
  phaseAt: 1794700000n,
  parked: "tz-fixtures failed twice",
});

test("Ask: an epic question draws its choices, with the recommended one's why, and a park draws Retry", () => {
  const question: EpicQuestion = create(EpicQuestionSchema, {
    id: "Q4",
    text: "Bump the embedded tz database?",
    asked: 1794700000n,
    choices: [
      create(EpicQuestionChoiceSchema, { id: "keep-pin", does: "Skip the Lisbon case." }),
      create(EpicQuestionChoiceSchema, { id: "allow-bump", does: "Adds go.mod to this task." }),
    ],
    recommendation: create(EpicRecommendationSchema, { choice: "keep-pin", why: "Lisbon is not on any rota until March.", confidence: "high" }),
  });

  assert.deepEqual(epicAsk(epic, question), {
    kind: "epic",
    who: "harbour · rota-export",
    asked: 1794700000,
    question: "Bump the embedded tz database?",
    command: null,
    options: [
      { n: 1, name: "keep-pin", does: "Skip the Lisbon case.", recommended: true, why: "Lisbon is not on any rota until March.", answerable: true },
      { n: 2, name: "allow-bump", does: "Adds go.mod to this task.", recommended: false, why: "", answerable: true },
    ],
    putOff: false,
    archive: null,
    target: null,
    epic: { id: "harbour-rota/rota-export", questionId: "Q4" },
  });

  assert.deepEqual(parkAsk(epic, "tz-fixtures failed twice"), {
    kind: "park",
    who: "harbour · rota-export",
    asked: 1794700000,
    question: "tz-fixtures failed twice",
    command: null,
    options: [{ n: 1, name: "Retry", does: "Run the parked work again", recommended: false, why: "", answerable: true }],
    putOff: false,
    archive: null,
    target: null,
    epic: { id: "harbour-rota/rota-export", questionId: "" },
  });
});

test("Ask: staleWords is null before a full day and reads the days from a day on", () => {
  const now = 1794700000;

  assert.equal(staleWords(now - (23 * 3600 + 59 * 60), now), null);
  assert.equal(staleWords(now - secondsPerDay, now), "1 day");
  assert.equal(staleWords(now - 5 * secondsPerDay, now), "5 days");
});
