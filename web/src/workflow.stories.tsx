// The stories of the workflow pieces: Workflow and OutputTails. The rota export's evidence runs and gate output come
// from the fixtures. No fixture holds workflow nodes, so the nodes are built here, and their logs read through a fixture
// transport.

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import type { LoadArtifact } from "./evidence";
import { type EpicNode, EpicNodeSchema } from "./gen/orrery/v1/epics_pb";
import { gets, rotaGateFailOutput } from "./fixtures/epics";
import { createFixtureTransport } from "./fixtures/transport";
import * as workflow from "./workflow";

const fixture = createFixtureTransport();
const loadArtifact: LoadArtifact = (run, name, stderr) => fixture.clients.evidence.artifact({ run, name, stderr });

// fixtureOf is the fixture's entry under id. The stories name only held ids, so a missing one is a broken story.
function fixtureOf<Value>(entries: ReadonlyMap<string, Value>, id: string): Value {
  const entry = entries.get(id);
  if (entry === undefined) {
    throw new Error(`fixtures: no entry for ${id}`);
  }
  return entry;
}

// node is one workflow node run as the response carries it.
function node(init: MessageInitShape<typeof EpicNodeSchema>): EpicNode {
  return create(EpicNodeSchema, init);
}

const rotaRuns = fixtureOf(gets, "lighthouse/rota-export").requirements.flatMap((requirement) => requirement.runs);
const checkError = "error: recipe `check` failed with exit code 1";

const planAndBuild: EpicNode[] = [
  node({ path: "1", node: "plan", kind: "plan", state: "ended", outcome: "passed", turns: [1n, 2n, 3n, 4n] }),
  node({ path: "2", node: "build", kind: "build", state: "running", tasks: [1n] }),
];

const evidence: EpicNode[] = [
  node({ path: "2/1", node: "evidence", kind: "evidence", state: "ended", outcome: "passed", runs: rotaRuns }),
];

const failedChecks: EpicNode[] = [
  node({
    path: "2/2",
    node: "gate",
    kind: "gate",
    state: "ended",
    outcome: "failed",
    round: 2n,
    check: { state: "ended", exit: 1, timedOut: false, notRun: "", stdout: rotaGateFailOutput, stderr: checkError },
  }),
  node({
    path: "2/3",
    node: "command",
    kind: "command",
    state: "ended",
    outcome: "failed",
    check: { state: "ended", exit: 124, timedOut: true, notRun: "", stdout: "", stderr: "" },
  }),
  node({
    path: "2/4",
    node: "gate",
    kind: "gate",
    state: "pending",
    check: { state: "pending", exit: 0, timedOut: false, notRun: "the round before failed", stdout: "", stderr: "" },
  }),
];

const loop: EpicNode[] = [
  node({ path: "3", node: "loop", kind: "loop", state: "running", loopRound: 2n }),
  node({
    path: "3/1",
    node: "agent",
    kind: "agent",
    state: "running",
    attempt: 1n,
    step: "st-rota-t2-impl",
    output: "Adding the refuse-on-existing check and its test.",
  }),
];

export const Workflow: Stories = {
  "plan-and-build": () => <workflow.Workflow nodes={planAndBuild} loadArtifact={loadArtifact} />,
  evidence: () => <workflow.Workflow nodes={evidence} loadArtifact={loadArtifact} />,
  "failed-checks": () => <workflow.Workflow nodes={failedChecks} loadArtifact={loadArtifact} />,
  loop: () => <workflow.Workflow nodes={loop} loadArtifact={loadArtifact} />,
};

export const OutputTails: Stories = {
  "both-tails": () => <workflow.OutputTails stdout={rotaGateFailOutput} stderr={checkError} />,
  "stdout-only": () => <workflow.OutputTails stdout={rotaGateFailOutput} stderr="" />,
  "stderr-only": () => <workflow.OutputTails stdout="" stderr={checkError} />,
};
