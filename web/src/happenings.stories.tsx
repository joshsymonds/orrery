// The stories of the happenings sections: JustHappened over the rota export's history, which carries a step with failed
// gate output, and the holder epic's history, and OutputTails over the rota export's gate output.

import type { Stories } from "./catalog/story";
import { gets, histories, rotaGateFailOutput } from "./fixtures/epics";
import * as happenings from "./happenings";

const checkError = "error: recipe `check` failed with exit code 1";

// noop is the step door of the stories: it opens nothing, since a story draws the page without one.
const noop = () => {};

// fixtureOf is the fixture's entry under id. The stories name only held ids, so a missing one is a broken story.
function fixtureOf<Value>(entries: ReadonlyMap<string, Value>, id: string): Value {
  const entry = entries.get(id);
  if (entry === undefined) {
    throw new Error(`fixtures: no entry for ${id}`);
  }
  return entry;
}

const rota = fixtureOf(gets, "lighthouse/rota-export");
const rotaEntries = fixtureOf(histories, "lighthouse/rota-export").entries;
const holder = fixtureOf(gets, "orrery/holder-scopes");
const holderEntries = fixtureOf(histories, "orrery/holder-scopes").entries;

export const HappeningList: Stories = {
  default: () => <happenings.JustHappened entries={rotaEntries} steps={rota.steps} onOpenStep={noop} />,
  holder: () => <happenings.JustHappened entries={holderEntries} steps={holder.steps} onOpenStep={noop} />,
  empty: () => <happenings.JustHappened entries={[]} steps={[]} />,
};

export const OutputTails: Stories = {
  "both-tails": () => <happenings.OutputTails stdout={rotaGateFailOutput} stderr={checkError} />,
  "stdout-only": () => <happenings.OutputTails stdout={rotaGateFailOutput} stderr="" />,
  "stderr-only": () => <happenings.OutputTails stdout="" stderr={checkError} />,
};
