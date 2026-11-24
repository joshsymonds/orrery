import type { JSX } from "preact";

import { contractSlug } from "./contracts";
import type { ContractRequirementWork } from "./gen/orrery/v1/contracts_pb";
import type { EvidenceRun } from "./gen/orrery/v1/epics_pb";

// ContractWorkEpic is the contract's epic as the work draws it: its id, and what a press on its door does.
export interface ContractWorkEpic {
  id: string;
  onOpen: () => void;
}

// ContractWorkProps are the work of an accepted contract, each requirement in the order serve gives it, and the epic
// whose door opens above them, or null when the contract has none.
export interface ContractWorkProps {
  work: ContractRequirementWork[];
  epic: ContractWorkEpic | null;
}

// stateTones is the tone each requirement state draws in, by serve's word for it. A state serve does not name draws
// none.
const stateTones: Readonly<Record<string, string>> = {
  proven: "ok",
  "being worked": "live",
  failed: "bad",
  "not started": "",
};

// proofOf words a requirement's proof: the phase of its evidence run and whether the run passed, or no proof yet when
// no run proves it.
function proofOf(proof: EvidenceRun | undefined): string {
  if (proof === undefined) {
    return "no proof yet";
  }
  return `${proof.phase} · ${proof.passed ? "passed" : "failed"}`;
}

// ContractWork draws an accepted contract's work by requirement: the epic's door first, then each requirement in the
// order serve gives, with its id, text and state, the tasks working it, and its proof. A press on the door calls the
// epic's onOpen.
export function ContractWork({ work, epic }: ContractWorkProps): JSX.Element {
  return (
    <section class="contract-work" aria-label="the work">
      {epic !== null && (
        <p class="work-epic">
          <button type="button" class="link" onClick={() => epic.onOpen()}>
            {`its epic, ${contractSlug(epic.id)}`}
          </button>
        </p>
      )}
      {work.map((requirement) => (
        <RequirementWork key={requirement.id} requirement={requirement} />
      ))}
    </section>
  );
}

// RequirementWork draws one requirement's work: its id, text and state, then its tasks, then its proof.
function RequirementWork({ requirement }: { requirement: ContractRequirementWork }): JSX.Element {
  const tone = stateTones[requirement.state] ?? "";
  return (
    <article class="work-req" data-requirement={requirement.id}>
      <div class="work-req-head">
        <code>{requirement.id}</code>
        <span class="strong">{requirement.text}</span>
        <span class={tone === "" ? "chip" : `chip ${tone}`}>{requirement.state}</span>
      </div>
      {requirement.tasks.length === 0 ? (
        <p class="inote">
          <span>no task yet</span>
        </p>
      ) : (
        <div class="tbl work-tasks">
          {requirement.tasks.map((task) => (
            <div class="tr" data-task={task.id}>
              <span>
                <code>{task.id}</code>
              </span>
              <span class="strong">{task.title}</span>
              <span>{task.status}</span>
              <span>{task.place}</span>
            </div>
          ))}
        </div>
      )}
      <p class="work-proof">{proofOf(requirement.proof)}</p>
    </article>
  );
}
