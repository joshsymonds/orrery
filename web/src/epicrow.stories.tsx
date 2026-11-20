// The stories of EpicRow: an epic's row in each of round 6's states, and a contract's row. Each row is the fixture epic
// (or contract) as the tree hangs it, with its detail from the fixture's Get answer, and no-op actions.

import type { VNode } from "preact";

import type { Stories } from "./catalog/story";
import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { blockOf } from "./epics";
import { EpicRow as Row } from "./epicrow";
import { liveEpoch } from "./fixtures/clock";
import { contracts } from "./fixtures/contracts";
import { epics, gets } from "./fixtures/epics";
import { inboxColumn } from "./inboxframe";
import type { EpicNode, Hung } from "./tree";

const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

// listedOf is the fixture's Epics.List row of the id, as a copy.
function listedOf(id: string): Epic {
  const found = epics.epics.find((epic) => epic.id === id);
  if (found === undefined) {
    throw new Error(`the fixture epics hold no row for ${id}`);
  }
  return { ...found, runningSteps: [...found.runningSteps] };
}

// epicOf hangs the listed epic under its conversation as the tree does, with its block read from the fixture's detail.
function epicOf(listed: Epic, detail: EpicsGetResponse | undefined = gets.get(listed.id)): EpicNode {
  return { kind: "epic", key: `epic:${listed.id}`, block: blockOf(listed, detail), listed, contract: null };
}

// rowOf draws the row of a hung epic or contract at the fixture's instant, in the inbox column, with no-op actions and no
// cursor on it.
function rowOf(hung: Hung): VNode {
  return inboxColumn(<Row hung={hung} now={now} current={false} onSelect={noop} />);
}

export const EpicRow: Stories = {
  // asking is a building epic with an open question: its blocker leads with a question mark.
  asking: () => rowOf(epicOf(listedOf("lighthouse/rota-export"))),
  // building is an epic whose tasks are under way, with its running steps under it.
  building: () => rowOf(epicOf(listedOf("orrery/orrery-followups"))),
  // contract-proposed is a contract waiting to be read and accepted, drawn with the contract's mark.
  "contract-proposed": () => {
    const contract = contracts.contracts[0];
    return rowOf({ kind: "contract", key: `contract:${contract.id}`, contract });
  },
  // ended-short is an epic that ended otherwise than released, its words standing in for its state.
  "ended-short": () => rowOf(epicOf(listedOf("tidepool/tide-cache"))),
  // parked is an epic the owner parked, which reads its park as its state.
  parked: () => rowOf(epicOf(listedOf("orrery/notices-usable"))),
  // paused is an epic the owner paused.
  paused: () => rowOf(epicOf(listedOf("orrery/harbour-map"))),
  // planning is an epic with no tasks yet and nothing asked, as round 6 draws it: the fixture's buoy-feeds with its
  // question left out.
  planning: () => {
    const listed = listedOf("tidepool/buoy-feeds");
    const detail = gets.get(listed.id);
    return rowOf(epicOf(listed, detail === undefined ? undefined : { ...detail, questions: [] }));
  },
  // release-failed is an epic whose release step failed, which its blocker names.
  "release-failed": () => rowOf(epicOf(listedOf("tidepool/offline-tables"))),
  // releasing is an epic whose tag is being pushed.
  releasing: () => rowOf(epicOf(listedOf("orrery/orbit-labels"))),
  // shipped is an epic that ended released.
  shipped: () => rowOf(epicOf(listedOf("orrery/holder-scopes"))),
};
