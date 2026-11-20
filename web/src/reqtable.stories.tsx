// The stories of RequirementTable: the requirements of the rota epic as its table draws them, with every row closed, and
// with R4 open to its evidence runs, read over the fixture transport.

import type { Stories } from "./catalog/story";
import type { LoadArtifact } from "./evidence";
import { gets } from "./fixtures/epics";
import { createFixtureTransport } from "./fixtures/transport";
import * as reqtable from "./reqtable";

const clients = createFixtureTransport().clients;

// loadArtifact reads a run's log or artifact over the fixture clients, as the app's page does.
const loadArtifact: LoadArtifact = (run, name, stderr) => clients.evidence.artifact({ run, name, stderr });

// requirementsOf is the requirements of an epic the fixtures hold a detail for.
function requirementsOf(id: string) {
  const detail = gets.get(id);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no detail for ${id}`);
  }
  return detail.requirements;
}

const rotaRequirements = requirementsOf("lighthouse/rota-export");

export const RequirementTable: Stories = {
  default: () => <reqtable.RequirementTable requirements={rotaRequirements} loadArtifact={loadArtifact} />,
  "with-runs": () => (
    <reqtable.RequirementTable requirements={rotaRequirements} loadArtifact={loadArtifact} initiallyOpen="R4" />
  ),
};
