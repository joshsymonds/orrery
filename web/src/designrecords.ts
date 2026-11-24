import type { Conversation } from "./conversation";
import type { createClients } from "./client";
import type { DesignsGetResponse } from "./gen/orrery/v1/designs_pb";

// designUnavailable is the message a design's record keeps when its Designs.Get fails. It never carries server text.
const designUnavailable = "The server could not read this design.";

// DesignRecord is the newest answer to Designs.Get for one design: its response, or the fixed error its read failed with.
// get is null while the record holds an error.
export interface DesignRecord {
  get: DesignsGetResponse | null;
  error: string;
}

// readDesign asks Designs.Get for the design id and resolves its record: the response, or the fixed error when the call
// fails. It never rejects.
async function readDesign(clients: ReturnType<typeof createClients>, id: string): Promise<DesignRecord> {
  try {
    return { get: await clients.designs.get({ id }), error: "" };
  } catch {
    return { get: null, error: designUnavailable };
  }
}

// designCountsOf counts, for each design, the design items the conversations given hold that name it, and a conversation
// that is not drawn is given as null. App reads the record of each design a drawn conversation names, and reads it again
// when that count moves, which is when a new design item for it arrives.
export function designCountsOf(conversations: ReadonlyArray<Conversation | null>): Map<string, number> {
  const counts = new Map<string, number>();
  for (const conversation of conversations) {
    for (const item of conversation?.items ?? []) {
      const design = item.kind === "design" ? (item.design?.design ?? "") : "";
      if (design !== "") {
        counts.set(design, (counts.get(design) ?? 0) + 1);
      }
    }
  }
  return counts;
}

// designRefetch returns the refetch of a design by its id: it reads Designs.Get with clients.designs and hands the record it
// resolves to keep, whether that is the response or the error. A read that a later read of the same id has overtaken is
// dropped when it answers, so the newest read of each id is the one kept, whatever order the answers come in.
export function designRefetch(
  clients: ReturnType<typeof createClients>,
  keep: (id: string, record: DesignRecord) => void,
): (id: string) => Promise<void> {
  const started = new Map<string, number>();
  return async (id) => {
    const read = (started.get(id) ?? 0) + 1;
    started.set(id, read);
    const record = await readDesign(clients, id);
    if (started.get(id) === read) {
      keep(id, record);
    }
  };
}
