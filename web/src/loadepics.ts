import type { createClients } from "./client";
import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";

// The fixed message shown when Epics.List fails. It never carries server text.
const epicsUnavailable = "epics unavailable";

// loadEpics reads the epic list, then the detail of every live epic at once. A failed Epics.List resolves no epics
// and asks for no detail. A failed Epics.Get leaves that epic out of details. A folded epic stays in epics but is
// never asked for.
export async function loadEpics(
  clients: ReturnType<typeof createClients>,
): Promise<{ epics: Epic[]; details: Map<string, EpicsGetResponse>; error: string }> {
  let epics: Epic[];
  try {
    ({ epics } = await clients.epics.list({}));
  } catch {
    return { epics: [], details: new Map(), error: epicsUnavailable };
  }

  const live = epics.filter((epic) => epic.live);
  const answers = await Promise.allSettled(live.map((epic) => clients.epics.get({ id: epic.id })));
  const details = new Map<string, EpicsGetResponse>();
  answers.forEach((answer, index) => {
    if (answer.status === "fulfilled") {
      details.set(live[index].id, answer.value);
    }
  });
  return { epics, details, error: "" };
}
