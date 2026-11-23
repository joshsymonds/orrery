import { equals } from "@bufbuild/protobuf";

import type { createClients } from "./client";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { Design } from "./gen/orrery/v1/designs_pb";
import { type Epic, EpicSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";

// The fixed message shown when Epics.List fails. It never carries server text.
const epicsUnavailable = "epics unavailable";

// A held detail with the Epics.List message it was fetched against. A later load asks for the epic again only when that
// listed message has changed, or when the load's refetch names the epic.
export interface HeldEpic {
  detail: EpicsGetResponse;
  listed: Epic;
}

interface EpicsLoad {
  epics: Epic[];
  details: Map<string, EpicsGetResponse>;
  held: ReadonlyMap<string, HeldEpic>;
  contracts: Contract[];
  designs: Design[];
  error: string;
}

// Refetch names the live epics a load asks for whatever their listed message.
export type Refetch = ReadonlySet<string>;

// changedOnly names no live epic, so a load asks only for the ones it holds no detail for or whose listed message
// changed.
export const changedOnly: Refetch = new Set<string>();

// loadEpics reads the epic list and the contract list at once, then the detail of each live epic it needs. A load needs
// a live epic when it holds no detail for it, when its listed message differs, in any field, from the one held with it,
// or when refetch names it. A failed Epics.List resolves no epics, asks for no detail, keeps held and reports the fixed
// error. A failed Epics.Get keeps that epic's held detail and listed message, or leaves the epic out of details when it
// held none. A folded epic stays in epics but is never asked for or held, whatever refetch names. A failed
// Contracts.List keeps the contracts the caller holds, and reports no error. A returned contract list is the listed one,
// in list order.
export async function loadEpics(
  clients: ReturnType<typeof createClients>,
  held: ReadonlyMap<string, HeldEpic>,
  refetch: Refetch,
  contracts: readonly Contract[] = [],
): Promise<EpicsLoad> {
  const [epicsAnswer, contractsAnswer] = await Promise.allSettled([
    clients.epics.list({}),
    clients.contracts.list({}),
  ]);
  const listedContracts = contractsAnswer.status === "fulfilled" ? contractsAnswer.value.contracts : [...contracts];
  if (epicsAnswer.status === "rejected") {
    return { epics: [], details: new Map(), held, contracts: listedContracts, designs: [], error: epicsUnavailable };
  }
  const { epics } = epicsAnswer.value;

  const live = epics.filter((epic) => epic.live);
  const asked = live.filter(
    (epic) => refetch.has(epic.id) || !unchanged(held.get(epic.id), epic),
  );
  const answers = await Promise.allSettled(asked.map((epic) => clients.epics.get({ id: epic.id })));
  const fetched = new Map<string, HeldEpic>();
  answers.forEach((answer, index) => {
    if (answer.status === "fulfilled") {
      fetched.set(asked[index].id, { detail: answer.value, listed: asked[index] });
    }
  });

  const next = new Map<string, HeldEpic>();
  const details = new Map<string, EpicsGetResponse>();
  for (const epic of live) {
    const entry = fetched.get(epic.id) ?? held.get(epic.id);
    if (entry !== undefined) {
      next.set(epic.id, entry);
      details.set(epic.id, entry.detail);
    }
  }
  return { epics, details, held: next, contracts: listedContracts, designs: [], error: "" };
}

// unchanged reports whether a live epic holds a detail fetched against a listed message equal to listed.
function unchanged(heldEpic: HeldEpic | undefined, listed: Epic): boolean {
  return heldEpic !== undefined && equals(EpicSchema, heldEpic.listed, listed);
}
