// The stories of EvidenceRun: a requirement's evidence runs as its proof draws them, from the fixture epics, with the
// evidence reads of the fixture transport. The states are round 6's: a red run that holds, and one that met the base, a
// green run an agent supplied, a run a judge is still reading, a run with no verdict, and a run with its log open, or
// with a log that is not available.

import type { Stories } from "./catalog/story";
import { EvidenceRuns as Runs, type LoadArtifact } from "./evidence";
import { gets } from "./fixtures/epics";
import { createFixtureTransport } from "./fixtures/transport";
import type { EvidenceRun as RunRecord } from "./gen/orrery/v1/epics_pb";

const rota = "lighthouse/rota-export";
const holder = "orrery/holder-scopes";

const clients = createFixtureTransport().clients;

const loadArtifact: LoadArtifact = (run, name, stderr) => clients.evidence.artifact({ run, name, stderr });

// runOf is the evidence run of a phase that a requirement of an epic holds in the fixtures.
function runOf(epic: string, requirement: string, phase: string): RunRecord {
  const found = gets.get(epic)?.requirements.find((candidate) => candidate.id === requirement);
  const run = found?.runs.find((candidate) => candidate.phase === phase);
  if (run === undefined) {
    throw new Error(`the fixtures hold no ${phase} run of ${requirement} of ${epic}`);
  }
  return run;
}

const redRun = runOf(rota, "R1", "red");
const greenRun = runOf(rota, "R1", "green");
const capturingRun = runOf(rota, "R4", "green");
const suppliedRun = runOf(holder, "R4", "green");

export const EvidenceRun: Stories = {
  // red-holds is a red run that failed before any change, and whose judge holds the conclusion.
  "red-holds": () => <Runs runs={[{ ...redRun, verdict: "holds" }]} loadArtifact={loadArtifact} />,
  // red-met-at-base is a red run whose test passed before any change, so it should have failed.
  "red-met-at-base": () => (
    <Runs runs={[{ ...redRun, passed: false, verdict: "fails" }]} loadArtifact={loadArtifact} />
  ),
  // green-passes is a green run an agent supplied rather than a captured test, which the judge holds.
  "green-passes": () => <Runs runs={[suppliedRun]} loadArtifact={loadArtifact} />,
  // judging is a green run whose output is captured and a judge is still reading, so it has no verdict yet.
  judging: () => <Runs runs={[{ ...capturingRun, state: "judging" }]} loadArtifact={loadArtifact} />,
  // no-verdict is a green run the judge gave no verdict for, so its reason draws in its place.
  "no-verdict": () => (
    <Runs
      runs={[{ ...greenRun, verdict: "", passed: false, exit: 75, judgeReason: "the judge timed out" }]}
      loadArtifact={loadArtifact}
    />
  ),
  // log-open is a red run with its log open, which the fixture transport answers.
  "log-open": () => <Runs runs={[redRun]} loadArtifact={loadArtifact} initiallyOpen />,
  // log-unavailable is a red run with its log open, whose log the fixture transport does not hold.
  "log-unavailable": () => (
    <Runs runs={[{ ...redRun, id: "ev-log-unavailable" }]} loadArtifact={loadArtifact} initiallyOpen />
  ),
};
