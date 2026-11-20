import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";

import { createClients, createTransport } from "./client";
import { ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { changedOnly, type HeldEpic, loadEpics, type Refetch } from "./loadepics";

const listPath = "/orrery.v1.Epics/List";
const getPath = "/orrery.v1.Epics/Get";
const contractsListPath = "/orrery.v1.Contracts/List";

// Synthetic fixtures: three epics in two repositories, no real records.
const liveAlpha = { id: "repo-a/alpha", repository: "repo-a", slug: "alpha", outcome: "", changed: "100", live: true };
const foldedBeta = { id: "repo-b/beta", repository: "repo-b", slug: "beta", outcome: "released", changed: "50", live: false };
const liveGamma = { id: "repo-a/gamma", repository: "repo-a", slug: "gamma", outcome: "", changed: "90", live: true };

// alpha while its one running step runs, and the same alpha after the step's task changes. changed stays the same.
const steppedAlpha = { ...liveAlpha, runningSteps: [{ id: "step-1", task: "draft", agent: "agent-a" }], endedSteps: 0 };
const retaskedAlpha = { ...steppedAlpha, runningSteps: [{ id: "step-1", task: "review", agent: "agent-a" }] };

// Synthetic contracts, listed in an order that is not sorted by id.
const contractBeta = { id: "repo-b/beta-contract", repository: "repo-b", title: "Beta contract", created: "20", changed: "20" };
const contractAlpha = { id: "repo-a/alpha-contract", repository: "repo-a", title: "Alpha contract", created: "10", changed: "10" };
const heldContract = create(ContractSchema, { id: "repo-a/held-contract", repository: "repo-a", title: "Held contract" });

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

// fakeEpics answers Epics.List with list, each Epics.Get with the answer for its id, and Contracts.List with contracts.
// It records every call with its JSON body, so a test sees each path and id that was asked for. A path or id it was not
// given is answered 404, so a Contracts.List the test does not name fails.
function fakeEpics(
  list: Answer,
  gets: Partial<Record<string, Answer>>,
  calls: Call[],
  contracts: Answer = notFound,
): typeof globalThis.fetch {
  return async (input, init) => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const body = (await request.json()) as { id?: string };
    calls.push({ path, body });
    if (path === listPath) return respond(list);
    if (path === contractsListPath) return respond(contracts);
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

// epicsPaths lists the paths of the calls a load makes to the epics service, in order. The Contracts.List call a load
// makes alongside Epics.List is left out.
function epicsPaths(calls: Call[]): string[] {
  return calls.map((call) => call.path).filter((path) => path !== contractsListPath);
}

test("two live epics and one folded: each live epic is asked for by id, the folded one is not, and both details come back", async () => {
  const calls: Call[] = [];
  const clients = clientsFor(
    fakeEpics({ status: 200, body: { epics: listed } }, { [liveAlpha.id]: ok(detailOf(liveAlpha)), [liveGamma.id]: ok(detailOf(liveGamma)) }, calls),
  );

  const result = await loadEpics(clients, new Map(), changedOnly);

  assert.equal(epicsPaths(calls)[0], listPath, "Epics.List is called before any Get");
  assert.deepEqual(epicsPaths(calls), [listPath, getPath, getPath]);
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

  const result = await loadEpics(clients, new Map(), changedOnly);

  assert.deepEqual(epicsPaths(calls), [listPath, getPath, getPath]);
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

  const result = await loadEpics(clients, new Map(), changedOnly);

  assert.deepEqual(epicsPaths(calls), [listPath]);
  assert.deepEqual(result.epics, []);
  assert.equal(result.details.size, 0);
  assert.equal(result.error, "epics unavailable");
  assert.ok(!result.error.includes("server detail"), `error carried server text: ${result.error}`);
});

test("an empty List: no epics, no details, no error, and no Get is asked", async () => {
  const calls: Call[] = [];
  const clients = clientsFor(fakeEpics({ status: 200, body: { epics: [] } }, {}, calls));

  const result = await loadEpics(clients, new Map(), changedOnly);

  assert.deepEqual(epicsPaths(calls), [listPath]);
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
    if (path === contractsListPath) return respond(ok({ contracts: [] }));
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

  const result = await loadEpics(clientsFor(fakeFetch), new Map(), changedOnly);

  assert.equal(result.error, "");
  assert.deepEqual([...result.details.keys()].sort(), [liveAlpha.id, liveGamma.id].sort());
});

test("Epics.List and Contracts.List are in flight at once: the List waits until the other has started", { timeout: 2000 }, async () => {
  let contractsStarted: () => void = () => {};
  const started = new Promise<void>((resolve) => {
    contractsStarted = resolve;
  });
  const fakeFetch: typeof globalThis.fetch = async (input, init) => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (path === listPath) {
      await started;
      return respond(ok({ epics: [] }));
    }
    if (path === contractsListPath) {
      contractsStarted();
      return respond(ok({ contracts: [contractAlpha] }));
    }
    return respond(notFound);
  };

  const result = await loadEpics(clientsFor(fakeFetch), new Map(), changedOnly);

  assert.deepEqual(
    result.contracts.map((contract) => contract.id),
    [contractAlpha.id],
  );
});

test("a load returns Contracts.List's contracts in list order, with no error", async () => {
  const calls: Call[] = [];
  const clients = clientsFor(
    fakeEpics({ status: 200, body: { epics: [] } }, {}, calls, ok({ contracts: [contractBeta, contractAlpha] })),
  );

  const result = await loadEpics(clients, new Map(), changedOnly);

  assert.equal(calls.filter((call) => call.path === contractsListPath).length, 1, "Contracts.List was not asked once");
  assert.deepEqual(
    result.contracts.map((contract) => contract.id),
    [contractBeta.id, contractAlpha.id],
  );
  assert.equal(result.error, "");
});

test("a failed Contracts.List returns the held contracts, adds no error, and still returns the epics", async () => {
  const clients = clientsFor(
    fakeEpics({ status: 200, body: { epics: listed } }, {}, [], serverError),
  );

  const result = await loadEpics(clients, new Map(), changedOnly, [heldContract]);

  assert.deepEqual(
    result.contracts.map((contract) => contract.id),
    [heldContract.id],
  );
  assert.equal(result.error, "");
  assert.ok(!result.error.includes("server detail"), `error carried server text: ${result.error}`);
  assert.deepEqual(
    result.epics.map((epic) => epic.id),
    listed.map((epic) => epic.id),
  );
});

test("a failed Epics.List still returns Contracts.List's contracts, with the epics message", async () => {
  const clients = clientsFor(fakeEpics(serverError, {}, [], ok({ contracts: [contractAlpha] })));

  const result = await loadEpics(clients, new Map(), changedOnly, [heldContract]);

  assert.deepEqual(
    result.contracts.map((contract) => contract.id),
    [contractAlpha.id],
  );
  assert.equal(result.error, "epics unavailable");
  assert.deepEqual(result.epics, []);
});

test("a failed Epics.List and a failed Contracts.List return the held contracts with the epics message", async () => {
  const clients = clientsFor(fakeEpics(serverError, {}, [], serverError));

  const result = await loadEpics(clients, new Map(), changedOnly, [heldContract]);

  assert.deepEqual(
    result.contracts.map((contract) => contract.id),
    [heldContract.id],
  );
  assert.equal(result.error, "epics unavailable");
});

// loadAgainst runs loadEpics against a fake server that lists epics and answers the Gets, recording its calls.
function loadAgainst(
  epics: unknown[],
  gets: Partial<Record<string, Answer>>,
  held: ReadonlyMap<string, HeldEpic>,
  refetch: Refetch,
  calls: Call[],
) {
  return loadEpics(clientsFor(fakeEpics({ status: 200, body: { epics } }, gets, calls)), held, refetch);
}

test("ChangedEpicsOnly: an identical listed epic makes no Get on an event reload", async () => {
  const gets = { [liveAlpha.id]: ok(detailOf(liveAlpha)), [liveGamma.id]: ok(detailOf(liveGamma)) };
  const first = await loadAgainst(listed, gets, new Map(), changedOnly, []);
  const calls: Call[] = [];

  const second = await loadAgainst(listed, {}, first.held, changedOnly, calls);

  assert.deepEqual(epicsPaths(calls), [listPath], "an unchanged epic was asked for");
  assert.equal(second.details.get(liveAlpha.id), first.details.get(liveAlpha.id), "the held alpha detail was not kept");
  assert.equal(second.details.get(liveGamma.id), first.details.get(liveGamma.id), "the held gamma detail was not kept");
});

test("ChangedEpicsOnly: a changed running step with the same changed is fetched", async () => {
  const first = await loadAgainst([steppedAlpha], { [liveAlpha.id]: ok(detailOf(steppedAlpha)) }, new Map(), changedOnly, []);
  const calls: Call[] = [];

  const second = await loadAgainst(
    [retaskedAlpha],
    { [liveAlpha.id]: ok(detailOf(retaskedAlpha)) },
    first.held,
    changedOnly,
    calls,
  );

  assert.deepEqual(idsOf(calls, getPath), [liveAlpha.id]);
  assert.equal(second.details.get(liveAlpha.id)?.epic?.runningSteps[0]?.task, "review", "the detail was not refetched");
});

test("ChangedEpicsOnly: an epic seen for the first time is fetched", async () => {
  const first = await loadAgainst([liveGamma], { [liveGamma.id]: ok(detailOf(liveGamma)) }, new Map(), changedOnly, []);
  const calls: Call[] = [];

  const second = await loadAgainst(
    listed,
    { [liveAlpha.id]: ok(detailOf(liveAlpha)), [liveGamma.id]: ok(detailOf(liveGamma)) },
    first.held,
    changedOnly,
    calls,
  );

  assert.deepEqual(idsOf(calls, getPath), [liveAlpha.id]);
  assert.deepEqual([...second.details.keys()].sort(), [liveAlpha.id, liveGamma.id].sort());
});

test("ChangedEpicsOnly: a load that names an unchanged live epic fetches it and no other unchanged one", async () => {
  const gets = { [liveAlpha.id]: ok(detailOf(liveAlpha)), [liveGamma.id]: ok(detailOf(liveGamma)) };
  const first = await loadAgainst(listed, gets, new Map(), changedOnly, []);
  const calls: Call[] = [];

  const second = await loadAgainst(listed, gets, first.held, new Set([liveGamma.id]), calls);

  assert.deepEqual(idsOf(calls, getPath), [liveGamma.id]);
  assert.equal(second.details.get(liveAlpha.id), first.details.get(liveAlpha.id), "the unnamed alpha was not kept");
  assert.notEqual(second.details.get(liveGamma.id), first.details.get(liveGamma.id), "the named gamma was not refetched");
});

test("ChangedEpicsOnly: a load that names a folded epic does not ask for it", async () => {
  const calls: Call[] = [];

  await loadAgainst(listed, { [liveAlpha.id]: ok(detailOf(liveAlpha)), [liveGamma.id]: ok(detailOf(liveGamma)) }, new Map(), new Set([foldedBeta.id]), calls);

  assert.deepEqual(idsOf(calls, getPath), [liveAlpha.id, liveGamma.id].sort());
});

test("ChangedEpicsOnly: a failed Get keeps the previous detail", async () => {
  const first = await loadAgainst([steppedAlpha], { [liveAlpha.id]: ok(detailOf(steppedAlpha)) }, new Map(), changedOnly, []);

  const failed = await loadAgainst([retaskedAlpha], { [liveAlpha.id]: serverError }, first.held, changedOnly, []);

  assert.equal(failed.error, "");
  assert.equal(failed.details.get(liveAlpha.id), first.details.get(liveAlpha.id), "the held detail was dropped");
  assert.equal(
    failed.held.get(liveAlpha.id)?.listed,
    first.held.get(liveAlpha.id)?.listed,
    "the listed message the detail was fetched against was replaced",
  );

  // The kept listed message still differs from the retasked one, so the next event reload asks again.
  const calls: Call[] = [];
  const retried = await loadAgainst(
    [retaskedAlpha],
    { [liveAlpha.id]: ok(detailOf(retaskedAlpha)) },
    failed.held,
    changedOnly,
    calls,
  );
  assert.deepEqual(idsOf(calls, getPath), [liveAlpha.id]);
  assert.equal(retried.details.get(liveAlpha.id)?.epic?.runningSteps[0]?.task, "review");
});
