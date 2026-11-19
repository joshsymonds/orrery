// The stories of Orrery: the rota export epic in flight, the holder epic with every task integrated, and the rota
// export with its phase cells open to a click. ARC_INNER and ARC_OUTER are numbers, so they have no story.

import type { Stories } from "./catalog/story";
import { liveEpoch } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import * as orrery from "./orrery";
import { requirementArcs, wedgesOf } from "./phases";

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
const rotaArcs = requirementArcs(rotaWedges, rota.requirements);
const holder = fixtureOf(gets, "orrery/holder-scopes");
const holderWedges = wedgesOf(holder);
const holderArcs = requirementArcs(holderWedges, holder.requirements);

export const Orrery: Stories = {
  "in-flight": () => <orrery.Orrery wedges={rotaWedges} arcs={rotaArcs} now={now} />,
  integrated: () => <orrery.Orrery wedges={holderWedges} arcs={holderArcs} now={now} />,
  "opens-phase": () => <orrery.Orrery wedges={rotaWedges} arcs={rotaArcs} now={now} onOpenPhase={noop} />,
};
