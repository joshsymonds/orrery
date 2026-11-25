// The stories of RequirementTable: the requirements of the rota epic as its table draws them, with every row closed, and
// with R4 open to its evidence runs, read over the fixture transport.

import type { Stories } from "./catalog/story";
import type { LoadArtifact } from "./evidence";
import { gets } from "./fixtures/epics";
import { createFixtureTransport } from "./fixtures/transport";
import { provenOf } from "./phases";
import { recordTasksOf } from "./record";
import * as reqtable from "./reqtable";

const clients = createFixtureTransport().clients;

// loadArtifact reads a run's log or artifact over the fixture clients, as the app's page does.
const loadArtifact: LoadArtifact = (run, name, stderr) => clients.evidence.artifact({ run, name, stderr });

// detailOf is the fixture's detail under id. The stories name only held ids, so a missing one is a broken story.
function detailOf(id: string) {
  const detail = gets.get(id);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no detail for ${id}`);
  }
  return detail;
}

const rota = detailOf("lighthouse/rota-export");
const rotaRequirements = rota.requirements;
// rotaProven is the rota epic's proven requirement ids, as the epic page gives its table.
const rotaProven: ReadonlySet<string> = new Set(
  rotaRequirements.map((requirement) => requirement.id).filter((id) => provenOf(id, recordTasksOf(rota), rotaRequirements)),
);

export const RequirementTable: Stories = {
  default: () => (
    <reqtable.RequirementTable requirements={rotaRequirements} loadArtifact={loadArtifact} proven={rotaProven} />
  ),
  "with-runs": () => (
    <reqtable.RequirementTable
      requirements={rotaRequirements}
      loadArtifact={loadArtifact}
      proven={rotaProven}
      initiallyOpen="R4"
    />
  ),
};
