import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";

import { couldNotTell, nothingChanged } from "./accountswrite";
import { createClients } from "./client";
import {
  RouteContextSchema,
  type RouteRegistry,
  RouteRegistrySchema,
  Routes,
  RoutesGetResponseSchema,
  RoutesSetContextAccountResponseSchema,
} from "./gen/orrery/v1/accounts_pb";
import { type Move, moveContexts, undoMoves } from "./usageuse";

// registryOf is the route registry holding each context with the account given for it, in the order given. The seat each
// context names is invented.
function registryOf(accounts: Readonly<Record<string, string>>): RouteRegistry {
  return create(RouteRegistrySchema, {
    contexts: Object.entries(accounts).map(([id, account]) =>
      create(RouteContextSchema, { id, account, defaultSeat: "example-seat" }),
    ),
  });
}

// Behavior is how the fake answers one SetContextAccount call. It applies the call and answers; or it refuses the call and
// changes nothing; or it applies the call and then fails it, as a call does that errors after its write.
type Behavior = "applies" | "refuses" | "appliesThenErrors";

// Fake is a routes service served from the accounts its contexts hold, in memory. accounts is each context's account as the
// service holds it now. calls holds each SetContextAccount call received, in order, whatever it did. behave queues how the
// next calls behave, one each, in order; a call past the queue applies. failGet makes every Routes.Get fail from then on.
interface Fake {
  routes: ReturnType<typeof createClients>["routes"];
  accounts: Map<string, string>;
  calls: { context: string; account: string }[];
  behave(...behaviors: Behavior[]): void;
  failGet(): void;
}

// fakeRoutes returns a Fake that starts from the contexts and accounts of initial.
function fakeRoutes(initial: Readonly<Record<string, string>>): Fake {
  const accounts = new Map(Object.entries(initial));
  const calls: { context: string; account: string }[] = [];
  const queued: Behavior[] = [];
  let getFails = false;
  const transport = createRouterTransport((router) => {
    router.service(Routes, {
      setContextAccount(request) {
        calls.push({ context: request.context, account: request.account });
        const behavior = queued.shift() ?? "applies";
        if (behavior === "refuses") {
          throw new ConnectError("context: unknown id", Code.NotFound);
        }
        accounts.set(request.context, request.account);
        if (behavior === "appliesThenErrors") {
          throw new ConnectError("routes: no answer", Code.Unavailable);
        }
        return create(RoutesSetContextAccountResponseSchema, {
          registry: registryOf(Object.fromEntries(accounts)),
        });
      },
      get() {
        if (getFails) {
          throw new ConnectError("routes: no answer", Code.Unavailable);
        }
        return create(RoutesGetResponseSchema, { registry: registryOf(Object.fromEntries(accounts)) });
      },
    });
  });
  return {
    routes: createClients(transport).routes,
    accounts,
    calls,
    behave: (...behaviors) => {
      queued.push(...behaviors);
    },
    failGet: () => {
      getFails = true;
    },
  };
}

// before is the contexts and accounts the moves start from: three contexts on accounts, and one on the session's own login.
const before = { alpha: "max-1", beta: "max-2", gamma: "max-3", delta: "" };

// plan is the contexts a move names, in the order the page lists them, each with the account it holds in before.
const plan: Move[] = [
  { context: "gamma", from: "max-3" },
  { context: "alpha", from: "max-1" },
  { context: "beta", from: "max-2" },
];

test("UsageUse: moveContexts makes one SetContextAccount per planned context, in plan order, and is done with all of them moved", async () => {
  const fake = fakeRoutes(before);
  const result = await moveContexts(fake.routes, plan, "pro-1", registryOf(before));
  assert.deepEqual(fake.calls, [
    { context: "gamma", account: "pro-1" },
    { context: "alpha", account: "pro-1" },
    { context: "beta", account: "pro-1" },
  ]);
  assert.deepEqual(result, { kind: "done", moved: plan });
  assert.deepEqual(Object.fromEntries(fake.accounts), { alpha: "pro-1", beta: "pro-1", gamma: "pro-1", delta: "" });
});

test("UsageUse: moveContexts makes no call for a context on its own login, or for one changed in latest", async () => {
  const latestAccounts = { alpha: "max-1", delta: "", beta: "max-3" };
  const fake = fakeRoutes(latestAccounts);
  const movePlan: Move[] = [
    { context: "alpha", from: "max-1" },
    { context: "delta", from: "" },
    { context: "beta", from: "max-2" },
  ];
  const result = await moveContexts(fake.routes, movePlan, "pro-1", registryOf(latestAccounts));
  assert.deepEqual(fake.calls, [{ context: "alpha", account: "pro-1" }]);
  assert.deepEqual(result, { kind: "done", moved: [{ context: "alpha", from: "max-1" }] });
  assert.equal(fake.accounts.get("beta"), "max-3", "a context changed in latest is left as it stands");
});

test("UsageUse: moveContexts stops at the first failed call, and Routes.Get says which planned contexts moved", async () => {
  const fake = fakeRoutes(before);
  fake.behave("applies", "refuses");
  const result = await moveContexts(fake.routes, plan, "pro-1", registryOf(before));
  assert.deepEqual(fake.calls, [
    { context: "gamma", account: "pro-1" },
    { context: "alpha", account: "pro-1" },
  ]);
  assert.deepEqual(result, {
    kind: "partial",
    reason: "context: unknown id",
    moved: [plan[0]],
    notMoved: [plan[1], plan[2]],
  });
});

test("UsageUse: moveContexts reports a call that errored after it applied as moved", async () => {
  const fake = fakeRoutes(before);
  fake.behave("applies", "appliesThenErrors");
  const result = await moveContexts(fake.routes, plan, "pro-1", registryOf(before));
  assert.deepEqual(fake.calls, [
    { context: "gamma", account: "pro-1" },
    { context: "alpha", account: "pro-1" },
  ]);
  assert.equal(fake.accounts.get("alpha"), "pro-1");
  assert.deepEqual(result, {
    kind: "partial",
    reason: couldNotTell,
    moved: [plan[0], plan[1]],
    notMoved: [plan[2]],
  });
});

test("UsageUse: moveContexts whose first call is refused moves nothing, and its reason says nothing changed", async () => {
  const fake = fakeRoutes(before);
  fake.behave("refuses");
  const result = await moveContexts(fake.routes, plan, "pro-1", registryOf(before));
  assert.deepEqual(fake.calls, [{ context: "gamma", account: "pro-1" }]);
  assert.deepEqual(result, {
    kind: "partial",
    reason: `context: unknown id ${nothingChanged}`,
    moved: [],
    notMoved: plan,
  });
});

test("UsageUse: moveContexts is unknown, with the could-not-tell text, when the failed call and Routes.Get both fail", async () => {
  const fake = fakeRoutes(before);
  fake.behave("appliesThenErrors");
  fake.failGet();
  const result = await moveContexts(fake.routes, plan, "pro-1", registryOf(before));
  assert.deepEqual(fake.calls, [{ context: "gamma", account: "pro-1" }]);
  assert.deepEqual(result, { kind: "unknown", reason: couldNotTell });
});

test("UsageUse: undoMoves sets each moved context back to its own account, in order, and is done", async () => {
  const fake = fakeRoutes(before);
  const moving = await moveContexts(fake.routes, plan, "pro-1", registryOf(before));
  assert.ok(moving.kind === "done");
  const undone = await undoMoves(fake.routes, moving.moved);
  assert.deepEqual(fake.calls.slice(plan.length), [
    { context: "gamma", account: "max-3" },
    { context: "alpha", account: "max-1" },
    { context: "beta", account: "max-2" },
  ]);
  assert.deepEqual(undone, { kind: "done", moved: plan });
  assert.deepEqual(Object.fromEntries(fake.accounts), before);
});

test("UsageUse: undoMoves stops at the first failed call, and reports which moved contexts are back on their own account", async () => {
  const fake = fakeRoutes(before);
  const moving = await moveContexts(fake.routes, plan, "pro-1", registryOf(before));
  assert.ok(moving.kind === "done");
  fake.behave("applies", "refuses");
  const undone = await undoMoves(fake.routes, moving.moved);
  assert.deepEqual(fake.calls.slice(plan.length), [
    { context: "gamma", account: "max-3" },
    { context: "alpha", account: "max-1" },
  ]);
  assert.deepEqual(undone, {
    kind: "partial",
    reason: "context: unknown id",
    moved: [plan[0]],
    notMoved: [plan[1], plan[2]],
  });
  assert.equal(fake.accounts.get("alpha"), "pro-1");
});

test("UsageUse: undoMoves is unknown, with the could-not-tell text, when the failed undo call and Routes.Get both fail", async () => {
  const fake = fakeRoutes(before);
  const moving = await moveContexts(fake.routes, plan, "pro-1", registryOf(before));
  assert.ok(moving.kind === "done");
  fake.behave("appliesThenErrors");
  fake.failGet();
  const undone = await undoMoves(fake.routes, moving.moved);
  assert.deepEqual(fake.calls.slice(plan.length), [{ context: "gamma", account: "max-3" }]);
  assert.deepEqual(undone, { kind: "unknown", reason: couldNotTell });
});
