import type { createClients } from "./client";
import { rowsFrom, type Row } from "./rows";

// The fixed message shown when Panes.List fails. It never carries server text.
const panesUnavailable = "panes unavailable";

// loadRows reads both lists at once. A failed Sessions.List leaves the panes; a failed Panes.List is reported
// and leaves the sessions.
export async function loadRows(
  clients: ReturnType<typeof createClients>,
): Promise<{ rows: Row[]; error: string }> {
  const [panes, sessions] = await Promise.allSettled([clients.panes.list({}), clients.sessions.list({})]);
  return {
    rows: rowsFrom(
      panes.status === "fulfilled" ? panes.value.panes : [],
      sessions.status === "fulfilled" ? sessions.value.sessions : [],
    ),
    error: panes.status === "fulfilled" ? "" : panesUnavailable,
  };
}
