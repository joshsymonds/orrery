// The stories of the happenings sections: NowRows and NextRows over the rota export's wedges, and JustHappened over the
// rota export's history, which carries a step with failed gate output, and the holder epic's history.

import type { Stories } from "./catalog/story";
import { liveEpoch } from "./fixtures/clock";
import { gets, histories } from "./fixtures/epics";
import * as happenings from "./happenings";
import { wedgesOf } from "./phases";

const noop = () => {};
const now = Date.parse(liveEpoch) / 1000;

// fixtureOf is the fixture's entry under id. The stories name only held ids, so a missing one is a broken story.
function fixtureOf<Value>(entries: ReadonlyMap<string, Value>, id: string): Value {
  const entry = entries.get(id);
  if (entry === undefined) {
    throw new Error(`fixtures: no entry for ${id}`);
  }
  return entry;
}

const rota = fixtureOf(gets, "lighthouse/rota-export");
const rotaWedges = wedgesOf(rota);
const rotaEntries = fixtureOf(histories, "lighthouse/rota-export").entries;
const holder = fixtureOf(gets, "orrery/holder-scopes");
const holderEntries = fixtureOf(histories, "orrery/holder-scopes").entries;

export const NowRows: Stories = {
  "in-flight": () => <happenings.NowRows wedges={rotaWedges} now={now} />,
  "opens-phase": () => <happenings.NowRows wedges={rotaWedges} now={now} onOpenPhase={noop} />,
};

export const NextRows: Stories = {
  planned: () => <happenings.NextRows wedges={rotaWedges} />,
  "opens-phase": () => <happenings.NextRows wedges={rotaWedges} onOpenPhase={noop} />,
};

export const JustHappened: Stories = {
  rota: () => <happenings.JustHappened entries={rotaEntries} steps={rota.steps} />,
  holder: () => <happenings.JustHappened entries={holderEntries} steps={holder.steps} />,
  empty: () => <happenings.JustHappened entries={[]} steps={[]} />,
};
