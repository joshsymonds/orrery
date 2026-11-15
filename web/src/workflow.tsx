import type { JSX } from "preact";

import { EvidenceRuns, type LoadArtifact } from "./evidence";
import type { EpicNode, EpicNodeCheck } from "./gen/orrery/v1/epics_pb";

// Workflow is the WORKFLOW section of an orreryd epic's pinned workflow: each node run in the order the response gives,
// with its path, kind, state, outcome, round, and its loop round and attempt when set, then the rows its kind holds. Every
// value is the response's: a field the response leaves unset draws nothing. Every text is a child of its element, so no
// markup in a path, output or tail becomes an element. loadArtifact reads an evidence run's logs and images.
export function Workflow(props: { nodes: EpicNode[]; loadArtifact: LoadArtifact }): JSX.Element {
  const { nodes, loadArtifact } = props;
  return (
    <section>
      <h3 class="eyebrow">
        WORKFLOW<span class="sub">{nodes.length}</span>
      </h3>
      <ol>
        {nodes.map((node) => (
          <li key={node.path}>
            <NodeHead node={node} />
            {node.turns.length > 0 && <SeqRows label="turn" seqs={node.turns} />}
            {node.tasks.length > 0 && <SeqRows label="task" seqs={node.tasks} />}
            {node.runs.length > 0 && <EvidenceRuns runs={node.runs} loadArtifact={loadArtifact} />}
            {node.check !== undefined && <CheckRows check={node.check} />}
            {node.step !== "" && <p>{`step ${node.step}`}</p>}
            {node.output !== "" && <pre class="log">{node.output}</pre>}
          </li>
        ))}
      </ol>
    </section>
  );
}

// NodeHead is a node's head line: its path, kind and state, its outcome once it has one, its round, and its loop round
// and attempt when set. A loop round is set only on a loop, and an attempt is set once the run has one.
function NodeHead(props: { node: EpicNode }): JSX.Element {
  const { node } = props;
  return (
    <div>
      <span class="path">{node.path}</span>
      <span class="kind">{node.kind}</span>
      <span class="state">{node.state}</span>
      {node.outcome !== "" && <span class="end">{node.outcome}</span>}
      <span>{`round ${node.round}`}</span>
      {node.loopRound > 0n && <span>{`loop round ${node.loopRound}`}</span>}
      {node.attempt > 0n && <span>{`attempt ${node.attempt}`}</span>}
    </div>
  );
}

// SeqRows is a plan's turns or tasks, or a build's task: one row per seq, in order, each named by its label.
function SeqRows(props: { label: string; seqs: bigint[] }): JSX.Element {
  const { label, seqs } = props;
  return (
    <ul>
      {seqs.map((seq, index) => (
        <li key={index}>{`${label} ${seq}`}</li>
      ))}
    </ul>
  );
}

// CheckRows is a gate's or command's check: its state, its exit once it has ended without failing to run, whether it was
// killed at its time limit, why it did not run, and its output tails when it has any.
function CheckRows(props: { check: EpicNodeCheck }): JSX.Element {
  const { check } = props;
  return (
    <div>
      <span class="state">{check.state}</span>
      {check.state === "ended" && check.notRun === "" && <span>{`exit ${check.exit}`}</span>}
      {check.timedOut && <span>timed out</span>}
      {check.notRun !== "" && <span>{`not run · ${check.notRun}`}</span>}
      {(check.stdout !== "" || check.stderr !== "") && <OutputTails stdout={check.stdout} stderr={check.stderr} />}
    </div>
  );
}

// OutputTails is a check's stdout and stderr tails: each non-empty tail under its label, preformatted.
export function OutputTails(props: { stdout: string; stderr: string }): JSX.Element {
  const { stdout, stderr } = props;
  return (
    <div class="tails">
      {stdout !== "" && (
        <>
          <span class="tail-label">stdout</span>
          <pre class="log">{stdout}</pre>
        </>
      )}
      {stderr !== "" && (
        <>
          <span class="tail-label">stderr</span>
          <pre class="log">{stderr}</pre>
        </>
      )}
    </div>
  );
}
