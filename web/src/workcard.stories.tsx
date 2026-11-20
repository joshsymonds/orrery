// The stories of WorkCard: a project page's card in round 6's three states, over the fixture transport's tree. Each card is
// a fixture thing as the tree hangs it, with a no-op opener.

import type { Stories } from "./catalog/story";
import { liveEpoch } from "./fixtures/clock";
import { contracts } from "./fixtures/contracts";
import { epics, gets } from "./fixtures/epics";
import { blockOf } from "./epics";
import type { ContractItem, EpicNode } from "./tree";
import { WorkCard as Card, workCardOf } from "./workcard";

const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

// epicOf hangs the fixture's listed epic under its conversation as the tree does, with its detail from the fixture's Get
// answer.
function epicOf(id: string): EpicNode {
  const listed = epics.epics.find((epic) => epic.id === id);
  if (listed === undefined) {
    throw new Error(`the fixture epics hold no row for ${id}`);
  }
  return { kind: "epic", key: `epic:${id}`, block: blockOf(listed, gets.get(id)), listed, contract: null };
}

// contractItem hangs the fixture's proposed contract under its conversation.
const contract: ContractItem = { kind: "contract", key: `contract:${contracts.contracts[0].id}`, contract: contracts.contracts[0] };

export const WorkCard: Stories = {
  // contract-waiting is a contract proposed and waiting to be read and accepted.
  "contract-waiting": () => <Card card={workCardOf(contract, now)} onOpen={noop} />,
  // epic-asks is an epic with an open question, which the card draws under its track.
  "epic-asks": () => <Card card={workCardOf(epicOf("lighthouse/rota-export"), now)} onOpen={noop} />,
  // epic-building is an epic whose tasks are under way, with nothing asked.
  "epic-building": () => <Card card={workCardOf(epicOf("orrery/orrery-followups"), now)} onOpen={noop} />,
};
