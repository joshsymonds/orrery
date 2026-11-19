// The stories of EvidenceRun: a requirement's evidence runs as its proof draws them, from the fixture epics, with the
// evidence reads of the fixture transport. The states are a captured red and green pair, a run an agent supplied, a run
// still going, and a run with no verdict.

import type { Stories } from "./catalog/story";
import { EvidenceRuns as Runs, type LoadArtifact } from "./evidence";
import { gets } from "./fixtures/epics";
import { createFixtureTransport } from "./fixtures/transport";
import type { EvidenceRun as RunRecord } from "./gen/orrery/v1/epics_pb";

const rota = "lighthouse/rota-export";
const holder = "orrery/holder-scopes";

const clients = createFixtureTransport().clients;

const loadArtifact: LoadArtifact = (run, name, stderr) => clients.evidence.artifact({ run, name, stderr });

// runsOf are the evidence runs of one requirement of an epic, in run order.
function runsOf(epic: string, requirement: string): RunRecord[] {
  const found = gets.get(epic)?.requirements.find((candidate) => candidate.id === requirement);
  if (found === undefined) {
    throw new Error(`the fixtures hold no requirement ${requirement} of ${epic}`);
  }
  return found.runs;
}

export const EvidenceRun: Stories = {
  // captured-red-and-green is a requirement's red run, which failed as it should, and its green run, which passed.
  "captured-red-and-green": () => <Runs runs={runsOf(rota, "R1")} loadArtifact={loadArtifact} />,
  // green-passes is a review requirement's green run, which an agent supplied rather than a captured test.
  "green-passes": () => <Runs runs={runsOf(holder, "R4")} loadArtifact={loadArtifact} />,
  // running is a run that has not ended, so it draws no exit.
  running: () => {
    const [red] = runsOf(rota, "R1");
    return <Runs runs={[{ ...red, state: "running" }]} loadArtifact={loadArtifact} />;
  },
  // no-verdict is a run the judge gave no verdict for, so its reason draws in its place.
  "no-verdict": () => {
    const [red] = runsOf(rota, "R1");
    return (
      <Runs runs={[{ ...red, verdict: "", judgeReason: "the judge timed out" }]} loadArtifact={loadArtifact} />
    );
  },
};
