import type { createClients } from "./client";
import type { Pane } from "./gen/orrery/v1/orrery_pb";
import { rowsFrom, type Row } from "./rows";

// The fixed message shown when Panes.List fails. It never carries server text.
const panesUnavailable = "panes unavailable";

// loadRows reads the lists at once: the panes, the pane sessions, and the headless sessions orreryd runs, which only
// Sessions.ListHeadless lists. A failed Sessions.List leaves the panes and reports seq 0; a failed
// Sessions.ListHeadless leaves the rest, as a failed Sessions.List does; a failed Panes.List is reported and leaves
// the sessions. seq is the store position Sessions.List reported with its sessions, where a later Events.Watch resumes.
// panes are the panes the rows were built from, which a later loadConversations rebuilds the rows from.
export async function loadRows(
  clients: ReturnType<typeof createClients>,
): Promise<{ rows: Row[]; error: string; seq: bigint; panes: Pane[] }> {
  const [panes, sessions, headless] = await Promise.allSettled([
    clients.panes.list({}),
    clients.sessions.list({}),
    clients.sessions.listHeadless({}),
  ]);
  const listed = panes.status === "fulfilled" ? panes.value.panes : [];
  return {
    rows: rowsFrom(
      listed,
      sessions.status === "fulfilled" ? sessions.value.sessions : [],
      headless.status === "fulfilled" ? headless.value.sessions : [],
    ),
    error: panes.status === "fulfilled" ? "" : panesUnavailable,
    seq: sessions.status === "fulfilled" ? sessions.value.seq : 0n,
    panes: listed,
  };
}

// loadConversations reads the conversations alone, the pane sessions and the headless sessions, for a tick while the
// event stream is open: it calls Sessions.List and Sessions.ListHeadless, and not Panes.List. It rebuilds the rows from
// panes, the panes of the last full load, and the two answers. A failed call of either leaves the rows as they were, so
// it returns null.
export async function loadConversations(
  clients: ReturnType<typeof createClients>,
  panes: Pane[],
): Promise<Row[] | null> {
  const [sessions, headless] = await Promise.allSettled([clients.sessions.list({}), clients.sessions.listHeadless({})]);
  if (sessions.status !== "fulfilled" || headless.status !== "fulfilled") {
    return null;
  }
  return rowsFrom(panes, sessions.value.sessions, headless.value.sessions);
}
