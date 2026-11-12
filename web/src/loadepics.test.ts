import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import { loadEpics } from "./loadepics";

const listPath = "/orrery.v1.Epics/List";
const getPath = "/orrery.v1.Epics/Get";

// Synthetic fixtures: three epics in two repositories, no real records.
const liveAlpha = { id: "repo-a/alpha", repository: "repo-a", slug: "alpha", outcome: "", changed: "100", live: true };
const foldedBeta = { id: "repo-b/beta", repository: "repo-b", slug: "beta", outcome: "released", changed: "50", live: false };
const liveGamma = { id: "repo-a/gamma", repository: "repo-a", slug: "gamma", outcome: "", changed: "90", live: true };

const listed = [liveAlpha, foldedBeta, liveGamma];

function detailOf(epic: typeof liveAlpha) {
  return { epic, tasks: [], steps: [] };
}

interface Answer {
  status: number;
  body: unknown;
}

interface Call {
  path: string;
  body: unknown;
}

const ok = (body: unknown): Answer => ({ status: 200, body });
const serverError: Answer = { status: 500, body: { code: "internal", message: "epics server detail" } };
const notFound: Answer = { status: 404, body: { code: "not_found", message: "no such route" } };

function respond(answer: Answer): Response {
  return new Response(JSON.stringify(answer.body), {
    status: answer.status,
    headers: { "content-type": "application/json" },
  });
}

// fakeEpics answers Epics.List with list and each Epics.Get with the answer for its id. It records every call with
// its JSON body, so a test sees each path and id that was asked for. A path or id it was not given is answered 404.
function fakeEpics(list: Answer, gets: Partial<Record<string, Answer>>, calls: Call[]): typeof globalThis.fetch {
  return async (input, init) => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const body = (await request.json()) as { id?: string };
    calls.push({ path, body });
    if (path === listPath) return respond(list);
    if (path === getPath && body.id !== undefined) return respond(gets[body.id] ?? notFound);
    return respond(notFound);
  };
}

function clientsFor(fetch: typeof globalThis.fetch): ReturnType<typeof createClients> {
  return createClients(createTransport({ baseUrl: "https://orrery.test", fetch }));
}

function idsOf(calls: Call[], path: string): string[] {
  return calls
    .filter((call) => call.path === path)
    .map((call) => (call.body as { id: string }).id)
    .sort();
}

test("two live epics and one folded: each live epic is asked for by id, the folded one is not, and both details come back", async () => {
  const calls: Call[] = [];
  const clients = clientsFor(
    fakeEpics({ status: 200, body: { epics: listed } }, { [liveAlpha.id]: ok(detailOf(liveAlpha)), [liveGamma.id]: ok(detailOf(liveGamma)) }, calls),
  );

  const result = await loadEpics(clients);

  assert.equal(calls[0]?.path, listPath, "Epics.List is called before any Get");
  assert.deepEqual(
    calls.map((call) => call.path),
    [listPath, getPath, getPath],
  );
  assert.deepEqual(idsOf(calls, getPath), [liveAlpha.id, liveGamma.id].sort());
  assert.equal(result.error, "");
  assert.deepEqual(
    result.epics.map((epic) => epic.id),
    listed.map((epic) => epic.id),
  );
  assert.deepEqual([...result.details.keys()].sort(), [liveAlpha.id, liveGamma.id].sort());
  assert.equal(result.details.get(liveAlpha.id)?.epic?.slug, "alpha");
  assert.equal(result.details.get(liveGamma.id)?.epic?.slug, "gamma");
});

test("one Get fails: the other epic's detail is kept, the failed epic is left out, and the error stays empty", async () => {
  const calls: Call[] = [];
  const clients = clientsFor(
    fakeEpics(
      { status: 200, body: { epics: [liveAlpha, liveGamma] } },
      { [liveAlpha.id]: serverError, [liveGamma.id]: ok(detailOf(liveGamma)) },
      calls,
    ),
  );

  const result = await loadEpics(clients);

  assert.deepEqual(
    calls.map((call) => call.path),
    [listPath, getPath, getPath],
  );
  assert.equal(result.error, "");
  assert.deepEqual(
    result.epics.map((epic) => epic.id),
    [liveAlpha.id, liveGamma.id],
  );
  assert.deepEqual([...result.details.keys()], [liveGamma.id]);
  assert.equal(result.details.get(liveGamma.id)?.epic?.slug, "gamma");
});

test("Epics.List fails: no epics, no details, the fixed error without server text, and no Get is asked", async () => {
  const calls: Call[] = [];
  const clients = clientsFor(fakeEpics(serverError, {}, calls));

  const result = await loadEpics(clients);

  assert.deepEqual(
    calls.map((call) => call.path),
    [listPath],
  );
  assert.deepEqual(result.epics, []);
  assert.equal(result.details.size, 0);
  assert.equal(result.error, "epics unavailable");
  assert.ok(!result.error.includes("server detail"), `error carried server text: ${result.error}`);
});

test("an empty List: no epics, no details, no error, and no Get is asked", async () => {
  const calls: Call[] = [];
  const clients = clientsFor(fakeEpics({ status: 200, body: { epics: [] } }, {}, calls));

  const result = await loadEpics(clients);

  assert.deepEqual(
    calls.map((call) => call.path),
    [listPath],
  );
  assert.deepEqual(result.epics, []);
  assert.equal(result.details.size, 0);
  assert.equal(result.error, "");
});

test("the live Gets are in flight at once: the first Get waits until the second has started", { timeout: 2000 }, async () => {
  let secondGetStarted: () => void = () => {};
  const secondStarted = new Promise<void>((resolve) => {
    secondGetStarted = resolve;
  });
  const fakeFetch: typeof globalThis.fetch = async (input, init) => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const body = (await request.json()) as { id?: string };
    if (path === listPath) return respond(ok({ epics: [liveAlpha, liveGamma] }));
    if (path === getPath && body.id === liveAlpha.id) {
      await secondStarted;
      return respond(ok(detailOf(liveAlpha)));
    }
    if (path === getPath && body.id === liveGamma.id) {
      secondGetStarted();
      return respond(ok(detailOf(liveGamma)));
    }
    return respond(notFound);
  };

  const result = await loadEpics(clientsFor(fakeFetch));

  assert.equal(result.error, "");
  assert.deepEqual([...result.details.keys()].sort(), [liveAlpha.id, liveGamma.id].sort());
});
