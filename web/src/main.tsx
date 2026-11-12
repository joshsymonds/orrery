import { render } from "preact";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { loadRows } from "./load";
import type { Row } from "./rows";

const defaultIntervalMs = 5000;

// The element index.html provides for the page to mount into.
const pageRootId = "app";

interface Loaded {
  rows: Row[];
  error: string;
}

// start renders App into root and reloads the rows every intervalMs. A tick that lands while a reload is in flight
// is skipped, so reloads never overlap. The returned function stops reloading and unmounts App.
export function start(
  root: HTMLElement,
  clients: ReturnType<typeof createClients>,
  intervalMs = defaultIntervalMs,
): () => void {
  let loaded: Loaded = { rows: [], error: "" };
  let now = 0;
  let stopped = false;
  let loading = false;

  const draw = () => {
    render(<App rows={loaded.rows} error={loaded.error} now={now} />, root);
  };

  const reload = async () => {
    if (loading) {
      return;
    }
    loading = true;
    try {
      const next = await loadRows(clients);
      if (stopped) {
        return;
      }
      loaded = next;
      now = Math.floor(Date.now() / 1000);
      draw();
    } finally {
      loading = false;
    }
  };

  draw();
  void reload();
  const timer = setInterval(() => void reload(), intervalMs);

  return () => {
    stopped = true;
    clearInterval(timer);
    render(null, root);
  };
}

// Importing this module in a test has no DOM to mount into, so it starts nothing.
const mount = globalThis.document?.getElementById(pageRootId);
if (mount) {
  start(mount, createClients(createTransport()));
}
