// The stories of Track: an epic's five stages, drawn from the record of an epic the fixtures hold, or a synthetic one, in
// each state round 6 draws the track in.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { at } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import { type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { trackOf } from "./recordwords";
import { Track as TrackView } from "./track";

const rota = "lighthouse/rota-export";
const holder = "orrery/holder-scopes";

// detailOf is the Epics.Get answer of an epic the fixtures hold.
function detailOf(id: string): EpicsGetResponse {
  const detail = gets.get(id);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no detail for ${id}`);
  }
  return detail;
}

// syntheticOf is an orreryd epic in the given phase, with its build tasks in the given states.
function syntheticOf(outcome: string, tasks: { id: string; status: string; stage: string }[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic: {
      id: "lighthouse/tide-cache",
      repository: "lighthouse",
      slug: "tide-cache",
      outcome,
      source: "orreryd",
      changed: at("@13:50"),
      phaseAt: at("@13:50"),
      seen: at("@13:50"),
      runningSteps: [],
    },
    repository: "/home/owner/code/lighthouse",
    summary: "The tide cache keeps the last tide table per port, so a stale feed still answers.",
    buildTasks: tasks.map((task) => ({
      id: task.id,
      title: `Task ${task.id}`,
      status: task.status,
      stage: task.stage,
      requirements: [],
    })),
  });
}

export const Track: Stories = {
  "asks-you": () => <TrackView stages={trackOf(detailOf(rota))} />,
  building: () =>
    <TrackView
      stages={trackOf(
        syntheticOf("accepted", [
          { id: "T1", status: "done", stage: "" },
          { id: "T2", status: "done", stage: "" },
          { id: "T3", status: "open", stage: "implement" },
          { id: "T4", status: "open", stage: "" },
        ]),
      )}
    />,
  released: () => <TrackView stages={trackOf(detailOf(holder))} />,
  reviewing: () =>
    <TrackView
      stages={trackOf(
        syntheticOf("reviewing", [
          { id: "T1", status: "done", stage: "" },
          { id: "T2", status: "done", stage: "" },
          { id: "T3", status: "open", stage: "review" },
        ]),
      )}
    />,
};
