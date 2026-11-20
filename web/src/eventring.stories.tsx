// The stories of the event pieces: EventRing and StepCard. A ring's question is read through a fixture
// transport, so the rota export's open question draws its form, and the followups epic, which holds no question, draws
// that it is settled.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { type EpicCalls, epicCallsOf } from "./card";
import { noEpics } from "./epics";
import * as eventring from "./eventring";
import type { EpicEvent, StepEvent } from "./eventring";
import { liveEpoch } from "./fixtures/clock";
import { createFixtureTransport } from "./fixtures/transport";
import { StepFactsSchema } from "./gen/orrery/v1/orrery_pb";

const noop = () => {};
const fixture = createFixtureTransport();
// The fixtures' instant, in unix seconds, is the clock the rings' asks age from.
const fixtureNow = Date.parse(liveEpoch) / 1000;
// The calls hold none of the poll's epics, so each ring reads its question's epic through the fixture.
const calls: EpicCalls = epicCallsOf(fixture.clients, noop, noop, fixtureNow, noEpics);

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
  epic: rota,
  task: "",
  brief: [shiftsBrief],
};

// The facts of the three step ends round 6 draws: one round, eight minutes, two files with 96 lines added and 4 removed;
// three rounds, thirteen minutes, four files with 141 added and 12 removed; and one minute, its lines not counted.
const okFacts = create(StepFactsSchema, { rounds: 1, seconds: 480n, counted: true, files: 2n, added: 96n, removed: 4n });
const failedFacts = create(StepFactsSchema, {
  rounds: 3,
  seconds: 780n,
  counted: true,
  files: 4n,
  added: 141n,
  removed: 12n,
});
const stoppedFacts = create(StepFactsSchema, { rounds: 0, seconds: 60n, counted: false });

// stepFailed is a step whose checks failed at the end of its last round.
const stepFailed: StepEvent = {
  step: "st-rota-t3-impl",
  agent: "shift-store",
  end: "checks_failed",
  epic: rota,
  task: "",
  brief: ["Stored shifts per keeper and ISO week."],
};

// stepStopped is a step its owner stopped before its first check round.
const stepStopped: StepEvent = {
  step: "st-rota-t2-impl",
  agent: "audit-log",
  end: "stopped",
  epic: rota,
  task: "",
  brief: ["Stopped by you before its first check round."],
};

// stepReturn is the full return of stepEnded, which the fold pretty-prints once it is opened.
const stepReturn = `{"verdict": "met", "answer": "${shiftsBrief}"}`;
const returningCalls: EpicCalls = { ...calls, stepResult: async () => stepReturn };

// stepLooping is a step whose block names no agent and gives a brief of its own.
const stepLooping: StepEvent = {
  step: "st-rota-t5-try1",
  agent: "",
  end: "looping",
  epic: "",
  task: "",
  brief: ["The monitor saw the same edit three times, so it was nudged, ended and retried on sonnet."],
};

export const EpicEventBlock: Stories = {
  question: () => <eventring.EventRing event={questionEvent} calls={calls} />,
  "question-settled": () => <eventring.EventRing event={settledEvent} calls={calls} />,
  parked: () => <eventring.EventRing event={parkEvent} calls={calls} />,
  released: () => <eventring.EventRing event={releaseEvent} calls={calls} />,
  ended: () => <eventring.EventRing event={endEvent} calls={calls} />,
};

export const StepEndBlock: Stories = {
  ok: () => <eventring.StepCard event={stepEnded} facts={okFacts} calls={returningCalls} />,
  failed: () => <eventring.StepCard event={stepFailed} facts={failedFacts} calls={returningCalls} />,
  stopped: () => <eventring.StepCard event={stepStopped} facts={stoppedFacts} calls={returningCalls} />,
  "without-agent": () => <eventring.StepCard event={stepLooping} />,
};

