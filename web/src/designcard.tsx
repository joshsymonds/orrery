import type { JSX } from "preact";

import type { Design, DesignContract } from "./gen/orrery/v1/designs_pb";

// DesignCardProps are the design a held card draws, and the press that holds one of its contracts, by the contract's id.
export interface DesignCardProps {
  design: Design;
  onOpenContract: (id: string) => void;
}

// byPosition orders a design's contracts by their place in its start order.
function byPosition(left: DesignContract, right: DesignContract): number {
  if (left.position === right.position) {
    return 0;
  }
  return left.position < right.position ? -1 : 1;
}

// DesignCard draws a design held at the desk: its state phrase as the record gives it, then its contracts in position order,
// each with its title and its state. A press on a contract's row calls onOpenContract with the contract's id. It draws
// nothing the design record does not hold.
export function DesignCard({ design, onOpenContract }: DesignCardProps): JSX.Element {
  const contracts = [...design.contracts].sort(byPosition);
  return (
    <div class="design-card">
      <p class="design-card-state">{design.state}</p>
      <ol class="design-card-contracts" aria-label="contracts">
        {contracts.map((contract) => (
          <li key={contract.id}>
            <button type="button" class="design-card-contract" onClick={() => onOpenContract(contract.id)}>
              <span class="design-card-title">{contract.title}</span>
              <span class="design-card-contract-state">{contract.state}</span>
            </button>
          </li>
        ))}
      </ol>
    </div>
  );
}
