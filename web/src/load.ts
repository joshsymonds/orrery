import type { createClients } from "./client";
import { rowsFrom, type Row } from "./rows";

// The fixed message shown when Panes.List fails. It never carries server text.
const panesUnavailable = "panes unavailable";

// loadRows reads the lists at once: the panes, the pane sessions, and the headless sessions orreryd runs, which only
// Sessions.ListHeadless lists. A failed Sessions.List leaves the panes and reports seq 0; a failed
// Sessions.ListHeadless leaves the rest, as a failed Sessions.List does; a failed Panes.List is reported and leaves
// the sessions. seq is the store position Sessions.List reported with its sessions, where a later Events.Watch resumes.
export async function loadRows(
  clients: ReturnType<typeof createClients>,
): Promise<{ rows: Row[]; error: string; seq: bigint }> {
  const [panes, sessions, headless] = await Promise.allSettled([
    clients.panes.list({}),
    clients.sessions.list({}),
    clients.sessions.listHeadless({}),
  ]);
  return {
    rows: rowsFrom(
      panes.status === "fulfilled" ? panes.value.panes : [],
      sessions.status === "fulfilled" ? sessions.value.sessions : [],
      headless.status === "fulfilled" ? headless.value.sessions : [],
    ),
    error: panes.status === "fulfilled" ? "" : panesUnavailable,
    seq: sessions.status === "fulfilled" ? sessions.value.seq : 0n,
  };
}
