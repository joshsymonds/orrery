import { createClient, type Interceptor, type Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import { Control, Panes, Sessions } from "./gen/orrery/v1/orrery_pb";

// The header internal/server/server.go requires on every request, so the serve guard admits it. Its value only has
// to be non-empty.
const clientHeader = "X-Orrery-Client";
const clientValue = "orrery web";

const addClientHeader: Interceptor = (next) => (req) => {
  req.header.set(clientHeader, clientValue);
  return next(req);
};

export interface TransportOptions {
  // The server every call goes to. Defaults to the page's own origin.
  baseUrl?: string;
  // Replaces the global fetch, so a test can answer calls without a network.
  fetch?: typeof globalThis.fetch;
}

export function createTransport(options: TransportOptions = {}): Transport {
  return createConnectTransport({
    baseUrl: options.baseUrl ?? window.location.origin,
    fetch: options.fetch,
    interceptors: [addClientHeader],
  });
}

export function createClients(transport: Transport) {
  return {
    panes: createClient(Panes, transport),
    sessions: createClient(Sessions, transport),
    control: createClient(Control, transport),
  };
}
