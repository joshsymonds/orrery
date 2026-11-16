import type { JSX } from "preact";

import { EvidenceRuns, type LoadArtifact } from "./evidence";
import type { EpicContract, EpicRequirement } from "./gen/orrery/v1/epics_pb";

// RedProof is the epic's red proof: a card per requirement with its red runs. The section is reached when a requirement
// has a red run, and dim until then.
export function RedProof(props: { requirements: EpicRequirement[]; loadArtifact: LoadArtifact }): JSX.Element {
  const { requirements, loadArtifact } = props;
  const reached = requirements.some((requirement) => requirement.runs.some((run) => run.phase === "red"));
  return (
    <CardsSection
      heading="Red proof"
      phase="red"
      reached={reached}
      requirements={requirements}
      loadArtifact={loadArtifact}
    />
  );
}

// GreenProof is the epic's green proof: the same cards as the red proof, with each requirement's green runs. The section
// is reached when a green run passed, and dim until then.
export function GreenProof(props: { requirements: EpicRequirement[]; loadArtifact: LoadArtifact }): JSX.Element {
  const { requirements, loadArtifact } = props;
  const reached = requirements.some((requirement) =>
    requirement.runs.some((run) => run.phase === "green" && run.passed),
  );
  return (
    <CardsSection
      heading="Green proof"
      phase="green"
      reached={reached}
      requirements={requirements}
      loadArtifact={loadArtifact}
    />
  );
}

// CardsSection is a proof section of one phase: a card per requirement in contract order, each with its id, text, kind
// when set, conclusion when set, and the runs of its phase, drawn when it has any.
function CardsSection(props: {
  heading: string;
  phase: string;
  reached: boolean;
  requirements: EpicRequirement[];
  loadArtifact: LoadArtifact;
}): JSX.Element {
  const { heading, phase, reached, requirements, loadArtifact } = props;
  return (
    <section class={reached ? `proof ${phase}` : `proof ${phase} dim`}>
      <h3>{heading}</h3>
      {requirements.map((requirement, index) => {
        const runs = requirement.runs.filter((run) => run.phase === phase);
        return (
          <article key={index} class="proof-card">
            <span class="rid">{requirement.id}</span>
            <span class="text">{requirement.text}</span>
            {requirement.kind !== "" && <span class="kind">{requirement.kind}</span>}
            {requirement.conclusion !== "" && <p class="conclusion">{requirement.conclusion}</p>}
            {runs.length > 0 && <EvidenceRuns runs={runs} loadArtifact={loadArtifact} />}
          </article>
        );
      })}
    </section>
  );
}

// ReleaseProof is the epic's release proof: each of the contract's release steps with its target and undo, then the line
// that closes the release. The section is reached when the epic is released. With no contract it is the heading alone.
export function ReleaseProof(props: { contract: EpicContract | undefined; released: boolean }): JSX.Element {
  const { contract, released } = props;
  return (
    <section class={released ? "proof release" : "proof release dim"}>
      <h3>Release proof</h3>
      {contract !== undefined && (
        <>
          <ol>
            {contract.releaseSteps.map((step, index) => (
              <li key={index} class="release-step">
                <span class="step">{step.step}</span>
                <span class="target">{step.target}</span>
                <span class="undo">{step.undo}</span>
              </li>
            ))}
          </ol>
          <p class="then">{contract.releaseThen}</p>
        </>
      )}
    </section>
  );
}

// Report is the epic's report, which is its heading alone and always dim.
export function Report(): JSX.Element {
  return (
    <section class="proof report dim">
      <h3>Report</h3>
    </section>
  );
}
