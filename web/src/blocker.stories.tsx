// The stories of the blocker pieces: AskFirst, QuestionCard and NowBlock. They draw the rota export epic, whose open
// question Q1 has no choices, whose held step is T1, and a question with choices, which no fixture holds.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import * as blocker from "./blocker";
import { at } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import { type EpicsGetResponse, EpicQuestionSchema } from "./gen/orrery/v1/epics_pb";
import { wedgesOf } from "./phases";
import { blockerOf } from "./status";

const noop = () => {};

// answer lands with no error, as an answer that is sent does.
const answer = async () => "";

// fixtureOf is the fixture's entry under id. The stories name only held ids, so a missing one is a broken story.
function fixtureOf<Value>(entries: ReadonlyMap<string, Value>, id: string): Value {
  const entry = entries.get(id);
  if (entry === undefined) {
    throw new Error(`fixtures: no entry for ${id}`);
  }
  return entry;
}

const rota: EpicsGetResponse = fixtureOf(gets, "lighthouse/rota-export");
const sentence = blocker.nowSentence(wedgesOf(rota));

// heldDetail and parkedDetail are the rota export with its question answered, so that its blocker is its held step, or
// its park.
const heldDetail: EpicsGetResponse = { ...rota, questions: [] };
const parkedDetail: EpicsGetResponse = { ...rota, questions: [], parked: "waiting on the harbour office" };

// recommended is a question with three choices, the first recommended. No fixture holds a question with choices.
const recommended = create(EpicQuestionSchema, {
  id: "Q2",
  text: "Which index should the backfill add?",
  turn: 3n,
  asked: at("@14:12"),
  choices: [
    { id: "composite", does: "Add a composite index on keeper and shift." },
    { id: "partial", does: "Add a partial index on open shifts only." },
    { id: "none", does: "Leave the table as it is." },
  ],
  recommendation: { choice: "composite", why: "open shifts are the only rows the backfill scans", confidence: "high" },
});

export const AskFirst: Stories = {
  idle: () => (
    <blocker.AskFirst
      label="Stop…"
      question="Stop rota-export?"
      confirm="Stop rota-export"
      keep="Keep it running"
      onConfirm={noop}
    />
  ),
};

export const QuestionCard: Stories = {
  "open-question": () => <blocker.QuestionCard question={rota.questions[0]} numberKeys onAnswer={answer} />,
  recommended: () => <blocker.QuestionCard question={recommended} numberKeys onAnswer={answer} />,
};

export const NowBlock: Stories = {
  question: () => (
    <blocker.NowBlock
      sentence={sentence}
      blocker={blockerOf(rota)}
      slug="rota-export"
      numberKeys
      onAnswer={answer}
      onRetry={noop}
      onStop={noop}
    />
  ),
  held: () => (
    <blocker.NowBlock
      sentence={sentence}
      blocker={blockerOf(heldDetail)}
      slug="rota-export"
      numberKeys
      onAnswer={answer}
      onRetry={noop}
      onStop={noop}
    />
  ),
  parked: () => (
    <blocker.NowBlock
      sentence={sentence}
      blocker={blockerOf(parkedDetail)}
      slug="rota-export"
      numberKeys
      onAnswer={answer}
      onRetry={noop}
      onStop={noop}
    />
  ),
  running: () => (
    <blocker.NowBlock
      sentence={sentence}
      blocker={null}
      slug="rota-export"
      numberKeys
      onAnswer={answer}
      onRetry={noop}
      onStop={noop}
    />
  ),
};
