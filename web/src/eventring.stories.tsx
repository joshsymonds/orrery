// The stories of the event pieces: EventRing, StepCard and WaitingCard. A ring's question is read through a fixture
// transport, so the rota export's open question draws its form, and the followups epic, which holds no question, draws
// that it is settled.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { type EpicCalls, epicCallsOf } from "./card";
import { noEpics } from "./epics";
import * as eventring from "./eventring";
import type { EpicEvent, StepEvent } from "./eventring";
import { createFixtureTransport } from "./fixtures/transport";
import { PendingEventSchema } from "./gen/orrery/v1/orrery_pb";

const noop = () => {};
const fixture = createFixtureTransport();
// The calls hold none of the poll's epics, so each ring reads its question's epic through the fixture.
const calls: EpicCalls = epicCallsOf(fixture.clients, noop, noop, noEpics);

const rota = "lighthouse/rota-export";

const questionEvent: EpicEvent = {
  kind: "question",
  epic: rota,
  tail: "Q1",
  sentence: "Epic lighthouse/rota-export asks a question.",
};
const settledEvent: EpicEvent = {
  kind: "question",
  epic: "orrery/orrery-followups",
  tail: "Q1",
  sentence: "Epic orrery/orrery-followups asks a question.",
};
const parkEvent: EpicEvent = {
  kind: "park",
  epic: rota,
  tail: "Q1",
  sentence: "Epic lighthouse/rota-export is parked.",
};
const releaseEvent: EpicEvent = {
  kind: "release",
  epic: "orrery/holder-scopes",
  tail: "released",
  sentence: "Epic orrery/holder-scopes is released.",
};
const endEvent: EpicEvent = {
  kind: "end",
  epic: "tidepool/offline-tables",
  tail: "done",
  sentence: "Epic tidepool/offline-tables has ended.",
};

// shiftsBrief is the one line the step's brief holds.
const shiftsBrief = "Shifts are stored per keeper and ISO week in the shifts table.";

// stepEnded is a step that ended with a brief, and whose full return the fold reads through stepReturn.
const stepEnded: StepEvent = {
  step: "st-rota-t4-impl",
  agent: "haiku",
  end: "done",
  status: "ended",
  epic: rota,
  task: "",
  brief: [shiftsBrief],
};

// stepReturn is the full return of stepEnded, which the fold pretty-prints once it is opened.
const stepReturn = `{"verdict": "met", "answer": "${shiftsBrief}"}`;
const returningCalls: EpicCalls = { ...calls, stepResult: async () => stepReturn };

// stepLooping is a step whose block names no agent and gives a brief of its own.
const stepLooping: StepEvent = {
  step: "st-rota-t5-try1",
  agent: "",
  end: "looping",
  status: "ended",
  epic: "",
  task: "",
  brief: ["The monitor saw the same edit three times, so it was nudged, ended and retried on sonnet."],
};

const pendingStep = create(PendingEventSchema, {
  id: "step:st-rota-t4-impl",
  kind: "step_end",
  step: "st-rota-t4-impl",
  agent: "haiku",
  end: "done",
  status: "ended",
  brief: `${shiftsBrief}\nMore: step_result id st-rota-t4-impl`,
  epic: rota,
  waits: "speak",
});

const pendingQuestion = create(PendingEventSchema, {
  id: "epic:lighthouse/rota-export:Q1",
  kind: "question",
  epic: rota,
  title: "Epic lighthouse/rota-export asks a question.",
  waits: "free",
});

export const EpicEventBlock: Stories = {
  question: () => <eventring.EventRing event={questionEvent} calls={calls} />,
  "question-settled": () => <eventring.EventRing event={settledEvent} calls={calls} />,
  parked: () => <eventring.EventRing event={parkEvent} calls={calls} />,
  released: () => <eventring.EventRing event={releaseEvent} calls={calls} />,
  ended: () => <eventring.EventRing event={endEvent} calls={calls} />,
};

export const StepEndBlock: Stories = {
  ok: () => <eventring.StepCard event={stepEnded} calls={returningCalls} />,
  "without-agent": () => <eventring.StepCard event={stepLooping} />,
  waiting: () => <eventring.StepCard event={stepEnded} waiting="arrives when you next speak" />,
};

export const WaitingCard: Stories = {
  "step-end-when-spoken": () => <eventring.WaitingCard event={pendingStep} />,
  "question-when-free": () => <eventring.WaitingCard event={pendingQuestion} />,
};
