import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { EpicsView } from "./epics";
import type { Address } from "./router";
import type { Row } from "./rows";

// listsEpic reports whether the view holds the epic under id in any of its lines, open or shut.
export function listsEpic(view: EpicsView, id: string): boolean {
  const held = [
    ...view.blocks.map((block) => block.epic),
    ...view.waiting,
    ...view.justEnded,
    ...view.endedEarlier,
  ];
  return held.some((item) => item.id === id);
}

// listHolds reports whether the view of address.view holds address.entry, so that opening the address shows its entry.
// An address with no entry always holds. The archived view holds archived rows and archived epics, and no step. The
// inbox holds the rows that are not archived, the epics listsEpic finds, and the steps of the epic blocks. A contract
// holds under every view while the loaded contracts hold its id.
export function listHolds(
  address: Address,
  rows: Row[],
  epics: EpicsView,
  contracts: readonly Contract[] = [],
): boolean {
  const { view, entry } = address;
  if (entry === null) {
    return true;
  }
  const archived = view === "archived";
  switch (entry.kind) {
    case "session":
      return rows.some((row) => row.kind === "session" && row.id === entry.id && row.archived === archived);
    case "pane":
      return rows.some(
        (row) =>
          row.kind === "pane" && row.host === entry.host && row.id === entry.pane && row.archived === archived,
      );
    case "step":
      return !archived && epics.blocks.some((block) => block.steps.some((step) => step.id === entry.id));
    case "epic":
      return archived ? epics.archived.some((item) => item.id === entry.id) : listsEpic(epics, entry.id);
    case "contract":
      return contracts.some((contract) => contract.id === entry.id);
  }
}
