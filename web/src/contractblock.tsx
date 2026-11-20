import type { JSX } from "preact";

import type { Contract } from "./gen/orrery/v1/contracts_pb";
import { clock } from "./reader";

// ContractLine is what the transcript's contract line says: its tone, its title and the words after it.
interface ContractLine {
  tone: "attention" | "ok" | "quiet";
  title: string;
  rest: string;
}

// contractLine words the line for a contract the transcript names: waiting, accepted, or set aside. A contract the list
// does not hold draws its id alone.
function contractLine(id: string, contract: Contract | undefined): ContractLine {
  if (contract === undefined) {
    return { tone: "quiet", title: id, rest: "" };
  }
  if (contract.state === "proposed") {
    return { tone: "attention", title: contract.title, rest: ` · ${contract.care} care · waits for you` };
  }
  if (contract.state === "accepted") {
    return { tone: "ok", title: contract.title, rest: ` · accepted ${clock(contract.changed)} · ${contract.care} care` };
  }
  return { tone: "quiet", title: contract.title, rest: " · set aside" };
}

// ContractBlock is the transcript's one line where a contract was written: its title and state, and a press on it calls
// onOpen with the contract's id.
export function ContractBlock({
  id,
  contract,
  onOpen,
}: {
  id: string;
  contract: Contract | undefined;
  onOpen: (id: string) => void;
}): JSX.Element {
  const line = contractLine(id, contract);
  return (
    <button type="button" class={`block oneline ${line.tone}`} onClick={() => onOpen(id)}>
      <div class="head">
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor">
          <path d="M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 11h4" />
        </svg>
        <span class="lbl">Contract</span>
      </div>
      <span class="body">
        {line.title}
        {line.rest !== "" && <span class="ink-3">{line.rest}</span>}
      </span>
      <span class="end">
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor">
          <path d="M6 3l5 5-5 5" />
        </svg>
      </span>
    </button>
  );
}
