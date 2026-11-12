import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";

test("Panes.List goes to the base URL with the orrery client header", async () => {
  const seen: { url?: string; header?: string | null } = {};
  const fakeFetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    seen.url = request.url;
    seen.header = request.headers.get("X-Orrery-Client");
    return new Response("{}", { headers: { "content-type": "application/json" } });
  };

  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch });
  const { panes } = createClients(transport);
  await panes.list({});

  assert.equal(seen.url, "https://orrery.test/orrery.v1.Panes/List");
  assert.ok(seen.header?.trim(), `X-Orrery-Client header was ${JSON.stringify(seen.header)}`);
});
