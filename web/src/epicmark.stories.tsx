// The story of EpicMark: an epic's mark at the gauge's size, one story per centre, each from the Epics.Get detail its
// centre is read from.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import * as epicmark from "./epicmark";
import { EpicRequirementSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";

// epicOf is an epic's detail in centre, with one requirement per state in states, named R1, R2 and so on in order.
function epicOf(centre: string, states: readonly string[]) {
  return create(EpicsGetResponseSchema, {
    centre,
    requirements: states.map((state, i) => create(EpicRequirementSchema, { id: `R${i + 1}`, state })),
  });
}

export const EpicMark: Stories = {
  running: () => <epicmark.EpicMark detail={epicOf("running", ["proven", "proven", "in_flight", "not_reached"])} />,
  asks: () => <epicmark.EpicMark detail={epicOf("asks", ["proven", "held", "not_reached"])} />,
  paused: () => <epicmark.EpicMark detail={epicOf("paused", ["proven", "proven", "not_reached"])} />,
  releasing: () => <epicmark.EpicMark detail={epicOf("releasing", ["proven", "proven", "proven"])} />,
  shipped: () => <epicmark.EpicMark detail={epicOf("shipped", ["proven", "proven"])} />,
  "ended-short": () => <epicmark.EpicMark detail={epicOf("ended_short", ["proven", "failed", "not_reached"])} />,
};
