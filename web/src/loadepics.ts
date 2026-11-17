import { equals } from "@bufbuild/protobuf";

import type { createClients } from "./client";
import { type Epic, EpicSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";

// The fixed message shown when Epics.List fails. It never carries server text.
const epicsUnavailable = "epics unavailable";

// A held detail with the Epics.List message it was fetched against. A later event reload asks for the epic again only
// when that listed message has changed.
export interface HeldEpic {
  detail: EpicsGetResponse;
  listed: Epic;
}

interface EpicsLoad {
  epics: Epic[];
  details: Map<string, EpicsGetResponse>;
  held: ReadonlyMap<string, HeldEpic>;
  error: string;
}

// loadEpics reads the epic list, then the detail of each live epic it needs, all at once. A tick needs every live epic.
// An event reload needs a live epic only when it holds no detail for it or its listed message differs, in any field,
// from the one held with it. A failed Epics.List resolves no epics, asks for no detail and keeps held. A failed
// Epics.Get keeps that epic's held detail and listed message, or leaves the epic out of details when it held none. A
// folded epic stays in epics but is never asked for or held.
export async function loadEpics(
  clients: ReturnType<typeof createClients>,
  held: ReadonlyMap<string, HeldEpic>,
  tick: boolean,
): Promise<EpicsLoad> {
  let epics: Epic[];
  try {
    ({ epics } = await clients.epics.list({}));
  } catch {
    return { epics: [], details: new Map(), held, error: epicsUnavailable };
  }

  const live = epics.filter((epic) => epic.live);
  const asked = live.filter((epic) => tick || !unchanged(held.get(epic.id), epic));
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
  return { epics, details, held: next, error: "" };
}

// unchanged reports whether a live epic holds a detail fetched against a listed message equal to listed.
function unchanged(heldEpic: HeldEpic | undefined, listed: Epic): boolean {
  return heldEpic !== undefined && equals(EpicSchema, heldEpic.listed, listed);
}
