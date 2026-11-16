import type { JSX } from "preact";

import { EvidenceRuns, type LoadArtifact } from "./evidence";
import type { EpicContract, EpicRequirement } from "./gen/orrery/v1/epics_pb";
import type { Phase, Wedge } from "./phases";

type OpenPhase = (task: string, phase: Phase) => void;

// RedProof is the epic's red proof: a card per requirement with its red runs. The section is reached when a requirement
// has a red run, and dim until then. A card opens the first wedge naming its requirement in implementing, when onOpenPhase
// is given; a card no wedge names is not clickable.
export function RedProof(props: {
  requirements: EpicRequirement[];
  wedges: Wedge[];
  loadArtifact: LoadArtifact;
  onOpenPhase?: OpenPhase;
}): JSX.Element {
  const { requirements, wedges, loadArtifact, onOpenPhase } = props;
  const reached = requirements.some((requirement) => requirement.runs.some((run) => run.phase === "red"));
  return (
    <CardsSection
      heading="Red proof"
      phase="red"
      opening="implementing"
      reached={reached}
      requirements={requirements}
      wedges={wedges}
      loadArtifact={loadArtifact}
      onOpenPhase={onOpenPhase}
    />
  );
}

// GreenProof is the epic's green proof: the same cards as the red proof, with each requirement's green runs. The section
// is reached when a green run passed, and dim until then. A card opens the first wedge naming its requirement in
// checking, when onOpenPhase is given; a card no wedge names is not clickable.
export function GreenProof(props: {
  requirements: EpicRequirement[];
  wedges: Wedge[];
  loadArtifact: LoadArtifact;
  onOpenPhase?: OpenPhase;
}): JSX.Element {
  const { requirements, wedges, loadArtifact, onOpenPhase } = props;
  const reached = requirements.some((requirement) =>
    requirement.runs.some((run) => run.phase === "green" && run.passed),
  );
  return (
    <CardsSection
      heading="Green proof"
      phase="green"
      opening="checking"
      reached={reached}
      requirements={requirements}
      wedges={wedges}
      loadArtifact={loadArtifact}
      onOpenPhase={onOpenPhase}
    />
  );
}

// CardsSection is a proof section of one phase: a card per requirement in contract order, each with its id, text, kind
// when set, conclusion when set, and the runs of its phase, drawn when it has any. A card whose requirement a wedge names
// is a button that opens that wedge's task in the opening phase, when onOpenPhase is given.
function CardsSection(props: {
  heading: string;
  phase: string;
  opening: Phase;
  reached: boolean;
  requirements: EpicRequirement[];
  wedges: Wedge[];
  loadArtifact: LoadArtifact;
  onOpenPhase?: OpenPhase;
}): JSX.Element {
  const { heading, phase, opening, reached, requirements, wedges, loadArtifact, onOpenPhase } = props;
  return (
    <section class={reached ? `proof ${phase}` : `proof ${phase} dim`}>
      <h3>{heading}</h3>
      {requirements.map((requirement, index) => {
        const runs = requirement.runs.filter((run) => run.phase === phase);
        const wedge = wedges.find((candidate) => candidate.requirements.includes(requirement.id));
        const open = onOpenPhase !== undefined && wedge !== undefined;
        return (
          <article
            key={index}
            class={open ? "proof-card open" : "proof-card"}
            role={open ? "button" : undefined}
            onClick={open ? () => onOpenPhase(wedge.task, opening) : undefined}
          >
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
