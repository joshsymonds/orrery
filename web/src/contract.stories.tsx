// The stories of ContractBody: an epic's accepted contract, read-only, with its requirements, from the fixture epics
// that hold a contract. The states differ in what the contract fills: additions, none at all, and no base commit.

import type { Stories } from "./catalog/story";
import { ContractBody as Body } from "./contract";
import { gets } from "./fixtures/epics";
import type { EpicContract, EpicRequirement } from "./gen/orrery/v1/epics_pb";

const rota = "lighthouse/rota-export";
const holder = "orrery/holder-scopes";
const tide = "tidepool/tide-alerts";

// contractOf is the accepted contract of an epic the fixtures hold one for.
function contractOf(id: string): EpicContract {
  const contract = gets.get(id)?.contract;
  if (contract === undefined) {
    throw new Error(`the fixtures hold no contract for ${id}`);
  }
  return contract;
}

// requirementsOf are an epic's requirements in contract order.
function requirementsOf(id: string): EpicRequirement[] {
  return gets.get(id)?.requirements ?? [];
}

export const ContractBody: Stories = {
  // rota-accepted is a contract with additions, failures, alternatives and release steps, and a base commit.
  "rota-accepted": () => <Body contract={contractOf(rota)} requirements={requirementsOf(rota)} />,
  // holder-no-additions is a contract that names no addition, so its section reads None.
  "holder-no-additions": () => <Body contract={contractOf(holder)} requirements={requirementsOf(holder)} />,
  // tide-alerts-no-base is a contract with no base commit, so its accepted line names no commit.
  "tide-alerts-no-base": () => <Body contract={contractOf(tide)} requirements={requirementsOf(tide)} />,
};
