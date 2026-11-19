// The stories of Handover: an epic's handover drawn from the fixture epics. The holder epic's handover holds its
// epic-stage REVIEW verdicts, the one section any fixture epic's handover draws. No fixture epic holds ledger
// findings or release runs, so the LEDGER and RELEASE sections have no state here.

import type { Stories } from "./catalog/story";
import { gets } from "./fixtures/epics";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { Handover as Block } from "./handover";

const holder = "orrery/holder-scopes";

// detailOf is the Epics.Get answer of an epic the fixtures hold.
function detailOf(id: string): EpicsGetResponse {
  const detail = gets.get(id);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no detail for ${id}`);
  }
  return detail;
}

export const Handover: Stories = {
  // reviews is an epic whose handover holds its REVIEW verdicts alone.
  reviews: () => <Block detail={detailOf(holder)} />,
};
