// The stories of the blocker pieces: AskFirst and QuestionCard. They draw the rota export epic's open question Q1, which
// has no choices, and a question with choices, which no fixture holds.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import * as blocker from "./blocker";
import { at } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import { type EpicsGetResponse, EpicQuestionSchema } from "./gen/orrery/v1/epics_pb";

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

export const ConfirmButton: Stories = {
  rest: () => (
    <blocker.AskFirst
      label="Stop…"
      question="Stop rota-export?"
      confirm="Stop rota-export"
      keep="Keep it running"
      onConfirm={noop}
    />
  ),
};

export const Ask: Stories = {
  "open-question": () => <blocker.QuestionCard question={rota.questions[0]} numberKeys onAnswer={answer} />,
  epic: () => <blocker.QuestionCard question={recommended} numberKeys onAnswer={answer} />,
};
