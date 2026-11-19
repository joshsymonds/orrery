// The stories of WorkerSessionPage: a step's worker session over the rota-export epic's Get answer and the worker's
// conversation stream, drawn while the step runs, while it is held, once it has ended, and while its conversation
// loads. Its nudge, hold and stop calls go to the fixture's Steps, which absorbs them.

import type { JSX } from "preact";

import type { Stories } from "./catalog/story";
import { applyDelta, type Conversation } from "./conversation";
import { gets } from "./fixtures/epics";
import { sessionStreams } from "./fixtures/sessions";
import { createFixtureTransport } from "./fixtures/transport";
import type { EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { stepCalls } from "./readercolumn";
import { WorkerSessionPage as Page } from "./workersession";

const rotaEpic = "lighthouse/rota-export";
const workerStep = "st-rota-t2-impl";

const noop = () => {};

// rota is the Get answer of the rota-export epic, which the fixture holds.
function rota(): EpicsGetResponse {
  const detail = gets.get(rotaEpic);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no Get answer for ${rotaEpic}`);
  }
  return detail;
}

// runningStep is the worker's step, st-rota-t2-impl, which is running.
function runningStep(): EpicStep {
  const step = rota().steps.find((candidate) => candidate.id === workerStep);
  if (step === undefined) {
    throw new Error(`the rota-export Get answer holds no step ${workerStep}`);
  }
  return step;
}

// workerConversation is the worker's stream applied to nothing held, as the page holds it when the stream first answers.
function workerConversation(): Conversation {
  const delta = sessionStreams.get(workerStep);
  if (delta === undefined) {
    throw new Error(`the fixtures hold no conversation stream for ${workerStep}`);
  }
  return applyDelta(null, delta);
}

// sessionPage draws the worker session of step, with the conversation given, which null is still loading.
function sessionPage(conversation: Conversation | null, step: EpicStep): JSX.Element {
  const { clients } = createFixtureTransport();
  const calls = stepCalls(clients, step.id);
  return (
    <Page
      conversation={conversation}
      step={step}
      epic="rota-export"
      task="T2"
      phase="implementing"
      onGoEpic={noop}
      onBack={noop}
      onOlder={noop}
      nudge={calls.nudge}
      hold={calls.hold}
      stop={calls.stop}
    />
  );
}

export const WorkerSessionPage: Stories = {
  // The step runs, so its box offers the nudge that interrupts the turn.
  running: () => sessionPage(workerConversation(), runningStep()),
  // The step is held, so its box offers the nudge that resumes it.
  held: () => sessionPage(workerConversation(), { ...runningStep(), held: true }),
  // The step has ended, so no box shows under the reader.
  ended: () => sessionPage(workerConversation(), { ...runningStep(), end: "done" }),
  // The conversation has not arrived, so the reader shows its loading line.
  loading: () => sessionPage(null, runningStep()),
};
