// The stories of EpicPin and ContextBand: the rota-export epic asking, the orbit-labels epic quiet, and the rota-export band
// with its task T2 current, drawn at the fixtures' instant.

import type { Stories } from "./catalog/story";
import { liveEpoch } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import * as band from "./taskband";

const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

// detailOf is the fixtures' Epics.Get answer for an epic id.
function detailOf(id: string): EpicsGetResponse {
  const detail = gets.get(id);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no detail for ${id}`);
  }
  return detail;
}

export const EpicPin: Stories = {
  asking: () => <band.EpicPin detail={detailOf("lighthouse/rota-export")} now={now} onOpen={noop} />,
  quiet: () => <band.EpicPin detail={detailOf("orrery/orbit-labels")} now={now} onOpen={noop} />,
};

export const ContextBand: Stories = {
  default: () => (
    <band.ContextBand
      detail={detailOf("lighthouse/rota-export")}
      now={now}
      current="T2"
      onOpenEpic={noop}
      onOpenTask={noop}
    />
  ),
};
