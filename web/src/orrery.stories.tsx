// The stories of Orrery: the follow-ups epic in flight, the rota export with a held task, the holder epic with every task
// integrated, and round 6's dense mix of 120 tasks over six requirements, in the server states the rim draws. ARC_INNER and
// ARC_OUTER are numbers, so they have no story.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { gets } from "./fixtures/epics";
import { type EpicRequirement, EpicRequirementSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import * as orrery from "./orrery";
import { type Cell, PLACES, type RecordTask, recordTasksOf } from "./record";
import { provenOf, wedgesOf } from "./phases";

// fixtureOf is the fixture's entry under id. The stories name only held ids, so a missing one is a broken story.
function fixtureOf<Value>(entries: ReadonlyMap<string, Value>, id: string): Value {
  const entry = entries.get(id);
  if (entry === undefined) {
    throw new Error(`fixtures: no entry for ${id}`);
  }
  return entry;
}

// provenIdsOf is the requirement ids an epic proves, as the chart's centre counts them.
function provenIdsOf(detail: EpicsGetResponse): Set<string> {
  const wedges = wedgesOf(detail);
  const ids = [...detail.requirements.map((requirement) => requirement.id), ...wedges.flatMap((wedge) => wedge.requirements)];
  return new Set(ids.filter((id) => provenOf(id, wedges, detail.requirements)));
}

const followups = fixtureOf(gets, "orrery/orrery-followups");
const rota = fixtureOf(gets, "lighthouse/rota-export");
const holder = fixtureOf(gets, "orrery/holder-scopes");

// denseRequirements are round 6's six requirements, one in each server state the rim draws: R1 and R2 proven, R3 in flight,
// R4 held, R5 failed and R6 not reached.
const denseRequirements: EpicRequirement[] = [
  ["R1", "proven"],
  ["R2", "proven"],
  ["R3", "in_flight"],
  ["R4", "held"],
  ["R5", "failed"],
  ["R6", "not_reached"],
].map(([id, state]) => create(EpicRequirementSchema, { id, text: `${id} is true`, state }));

// denseTasks is round 6's dense mix: 96 integrated, three in review, five in green, three in implement, three in red and
// ten planned. Task 102 is failing in green, and task 107 is held in implement.
const denseTasks: RecordTask[] = Array.from({ length: 120 }, (_, index) => {
  const placeIndex = index < 96 ? PLACES.length : index < 99 ? 3 : index < 104 ? 2 : index < 107 ? 1 : index < 110 ? 0 : -1;
  const tone = index < 96 ? "ok" : index === 101 ? "bad" : index === 106 ? "mine" : placeIndex === -1 ? "plan" : "run";
  const cells = PLACES.map((_, ring): Cell => (ring < placeIndex ? "ok" : ring === placeIndex ? tone : "none"));
  const place = placeIndex === PLACES.length ? "integrated" : placeIndex === -1 ? "planned" : PLACES[placeIndex];
  return {
    task: `unit-${String(index + 1).padStart(3, "0")}`,
    title: `Unit ${index + 1}`,
    requirements: [`R${(index % 6) + 1}`],
    place,
    tone,
    cells,
    words: place === "integrated" ? "done" : place === "planned" ? "not started" : place,
    since: 0,
  };
});

export const Orrery: Stories = {
  running: () => (
    <orrery.Orrery
      tasks={recordTasksOf(followups)}
      requirements={followups.requirements}
      proven={provenIdsOf(followups)}
    />
  ),
  mine: () => <orrery.Orrery tasks={recordTasksOf(rota)} requirements={rota.requirements} proven={provenIdsOf(rota)} />,
  done: () => <orrery.Orrery tasks={recordTasksOf(holder)} requirements={holder.requirements} proven={provenIdsOf(holder)} />,
  dense: () => <orrery.Orrery tasks={denseTasks} requirements={denseRequirements} proven={new Set(["R1", "R2"])} />,
};
