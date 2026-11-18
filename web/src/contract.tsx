import type { JSX } from "preact";

import { CommitMark } from "./commitmark";
import type { EpicContract, EpicRequirement } from "./gen/orrery/v1/epics_pb";
import { clock } from "./reader";

// ContractBody draws an epic's accepted contract read-only: its title and approved line, then its sections in the
// contract's own order under the contract's own headings, with the epic's requirements in their place among them. Every
// text is a child of its element, so no markup in a field becomes an element, and no field can be edited.
export function ContractBody(props: { contract: EpicContract; requirements: EpicRequirement[] }): JSX.Element {
  const { contract, requirements } = props;
  return (
    <article class="contract">
      <h2>{contract.title}</h2>
      <p class="approved">{contract.approved}</p>

      <section>
        <h3>What you asked for</h3>
        <p class="intent">{contract.intent}</p>
      </section>

      <section>
        <h3>What could go wrong, and how much we care</h3>
        <ul class="failures">
          {contract.failures.map((failure) => (
            <li key={failure.id} class="row">
              <span class="id">{failure.id}</span>
              <span class="text">{failure.text}</span>
              <span class="rating">{failure.rating}</span>
              <span class="response">{failure.response}</span>
            </li>
          ))}
        </ul>
        <p class="care">{contract.care}</p>
      </section>

      <section>
        <h3>Things you did not ask for</h3>
        {contract.additions.length === 0 ? (
          <p class="none">None</p>
        ) : (
          <ul class="additions">
            {contract.additions.map((addition) => (
              <li class="row">
                <span class="location">{addition.location}</span>
                <span class="what">{addition.what}</span>
                <span class="why">{addition.why}</span>
                <span class="cost">{addition.cost}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section>
        <h3>What will be true when done</h3>
        <ul class="requirements">
          {requirements.map((requirement) => (
            <li key={requirement.id} class="row">
              <span class="id">{requirement.id}</span>
              <span class="text">{requirement.text}</span>
              <span class="evidence">{requirement.evidence}</span>
            </li>
          ))}
        </ul>
      </section>

      <section>
        <h3>What I'm assuming</h3>
        <ul class="premises">
          {contract.premises.map((premise) => (
            <li key={premise.id} class="row">
              <span class="id">{premise.id}</span>
              <span class="text">{premise.text}</span>
              <span class="if-wrong">{premise.ifWrong}</span>
            </li>
          ))}
        </ul>
      </section>

      <section>
        <h3>What we won't do</h3>
        <ul class="exclusions">
          {contract.exclusions.map((exclusion) => (
            <li class="row">{exclusion}</li>
          ))}
        </ul>
      </section>

      <section>
        <h3>How, and why not the other ways</h3>
        <p class="approach">{contract.approach}</p>
        <ul class="alternatives">
          {contract.alternatives.map((alternative) => (
            <li class="row">
              <span class="alternative">{alternative.alternative}</span>
              <span class="why-not">{alternative.whyNot}</span>
              <span class="reconsider">{alternative.reconsider}</span>
            </li>
          ))}
        </ul>
      </section>

      <section>
        <h3>What leaves this machine or can't be undone</h3>
        <ul class="release-steps">
          {contract.releaseSteps.map((step) => (
            <li class="row">
              <span class="step">{step.step}</span>
              <span class="target">{step.target}</span>
              <span class="undo">{step.undo}</span>
            </li>
          ))}
        </ul>
        <p class="release-then">{contract.releaseThen}</p>
      </section>

      <section>
        <h3>Checks the machines run</h3>
        <p class="checks">{contract.checks}</p>
      </section>

      <p class="accepted">
        {`accepted ${clock(contract.accepted)}`}
        {contract.base !== "" && (
          <>
            {" · "}
            <CommitMark hash={contract.base} />
          </>
        )}
      </p>
    </article>
  );
}
