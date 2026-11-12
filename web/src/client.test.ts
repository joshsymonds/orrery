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

test("Epics.List and Steps.Nudge go to the base URL with the orrery client header", async () => {
  const seen: { url?: string; header?: string | null }[] = [];
  const fakeFetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    seen.push({ url: request.url, header: request.headers.get("X-Orrery-Client") });
    return new Response("{}", { headers: { "content-type": "application/json" } });
  };

  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch });
  const { epics, steps } = createClients(transport);
  await epics.list({});
  await steps.nudge({ id: "step-1", text: "keep going" });

  assert.equal(seen.length, 2);
  assert.equal(seen[0].url, "https://orrery.test/orrery.v1.Epics/List");
  assert.equal(seen[1].url, "https://orrery.test/orrery.v1.Steps/Nudge");
  for (const call of seen) {
    assert.ok(call.header?.trim(), `X-Orrery-Client header on ${call.url} was ${JSON.stringify(call.header)}`);
  }
});
