import assert from "node:assert/strict";
import { test } from "node:test";

import { create, toJsonString } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient, createRouterTransport } from "@connectrpc/connect";

import { AccountSchema, Accounts, Routes, RouteRegistrySchema } from "../gen/orrery/v1/accounts_pb";
import { type Call, accountsServe, accounts as seedAccounts, registry as seedRegistry } from "./accounts";
import { createFixtureTransport } from "./transport";

// served returns the fake from seed, and the Accounts and Routes clients that call it with no network.
function served() {
  const fake = accountsServe({ accounts: seedAccounts, registry: seedRegistry });
  const transport = createRouterTransport((router) => fake.register(router));
  return {
    fake,
    accounts: createClient(Accounts, transport),
    routes: createClient(Routes, transport),
  };
}

// rejection is the ConnectError a call fails with. It fails the test when the call is answered.
async function rejection(call: Promise<unknown>): Promise<ConnectError> {
  try {
    await call;
  } catch (error) {
    assert.ok(error instanceof ConnectError, `the call failed with ${String(error)}`);
    return error;
  }
  assert.fail("the call was answered, not refused");
}

// listed is the account ids the next List answers, in the order it answers them.
async function listedIds(accounts: ReturnType<typeof served>["accounts"]): Promise<string[]> {
  return (await accounts.list({})).accounts.map((account) => account.id);
}

test("AccountsFixture: List answers the invented accounts, created ascending", async () => {
  const { accounts } = served();
  assert.deepEqual((await accounts.list({})).accounts, seedAccounts);
  assert.deepEqual(await listedIds(accounts), ["max-1", "max-2", "max-3", "pro-1"]);
});

test("AccountsFixture: List answers accounts created ascending, then by id, whatever order the seed gives them", async () => {
  const account = (id: string, created: bigint) => create(AccountSchema, { id, name: id, created });
  const fake = accountsServe({
    accounts: [account("b", 2n), account("c", 1n), account("a", 1n)],
    registry: seedRegistry,
  });
  const transport = createRouterTransport((router) => fake.register(router));
  const listed = await createClient(Accounts, transport).list({});
  assert.deepEqual(
    listed.accounts.map((listedAccount) => listedAccount.id),
    ["a", "c", "b"],
  );
});

test("AccountsFixture: Routes.Get answers the invented registry, with personal as the default context", async () => {
  const { routes } = served();
  const { registry } = await routes.get({});
  assert.deepEqual(registry, seedRegistry);
  assert.equal(registry?.defaultContext, "personal");
});

test("AccountsFixture: Add stores an account the next List answers last, with no token in it", async () => {
  const { accounts } = served();
  const token = "sk-ant-oat01-invented-token-value";
  const { account } = await accounts.add({ name: "max-4", provider: "anthropic", plan: "Claude Max", token });
  assert.equal(account?.name, "max-4");
  assert.ok(account !== undefined && !toJsonString(AccountSchema, account).includes(token), "the account holds the token");
  assert.deepEqual((await accounts.list({})).accounts.at(-1), account);
  assert.deepEqual(await listedIds(accounts), ["max-1", "max-2", "max-3", "pro-1", account?.id]);
});

test("AccountsFixture: Rename changes the name the next List answers", async () => {
  const { accounts } = served();
  const { account } = await accounts.rename({ id: "max-2", name: "team-max" });
  assert.equal(account?.name, "team-max");
  assert.equal((await accounts.list({})).accounts.find((listed) => listed.id === "max-2")?.name, "team-max");
});

test("AccountsFixture: SetContextAccount changes the account the next Routes.Get answers for its context", async () => {
  const { routes } = served();
  await routes.setContextAccount({ context: "work", account: "pro-1" });
  const { registry } = await routes.get({});
  assert.equal(registry?.contexts.find((context) => context.id === "work")?.account, "pro-1");
});

test("AccountsFixture: Replace stores the registry given, which the next Routes.Get answers", async () => {
  const { routes } = served();
  const given = create(RouteRegistrySchema, {
    defaultContext: "work",
    contexts: [{ id: "work", defaultSeat: "work-bedrock" }],
  });
  const { registry: stored } = await routes.replace({ registry: given });
  assert.deepEqual(stored, given);
  assert.deepEqual((await routes.get({})).registry, given);
});

test("AccountsFixture: Replace with no registry stores an empty one", async () => {
  const { routes } = served();
  await routes.replace({});
  assert.deepEqual((await routes.get({})).registry, create(RouteRegistrySchema));
});

test("AccountsFixture: Remove deletes an account no context uses, which the next List no longer answers", async () => {
  const { accounts } = served();
  await accounts.remove({ id: "max-3" });
  assert.deepEqual(await listedIds(accounts), ["max-1", "max-2", "pro-1"]);
});

test("AccountsFixture: Remove refuses an account a context uses with FailedPrecondition, naming the context, and changes nothing", async () => {
  const { accounts } = served();
  const error = await rejection(accounts.remove({ id: "max-1" }));
  assert.equal(error.code, Code.FailedPrecondition);
  assert.equal(error.rawMessage, "account: in use by context personal");
  assert.deepEqual(await listedIds(accounts), ["max-1", "max-2", "max-3", "pro-1"]);
});

test("AccountsFixture: Rename, Remove and SetContextAccount refuse an unknown id with NotFound, and change nothing", async () => {
  const { accounts, routes } = served();
  assert.equal((await rejection(accounts.rename({ id: "max-9", name: "x" }))).code, Code.NotFound);
  assert.equal((await rejection(accounts.remove({ id: "max-9" }))).code, Code.NotFound);
  const noContext = await rejection(routes.setContextAccount({ context: "lab", account: "pro-1" }));
  assert.equal(noContext.rawMessage, "context: unknown id");
  const noAccount = await rejection(routes.setContextAccount({ context: "work", account: "max-9" }));
  assert.equal(noAccount.rawMessage, "account: unknown id");
  assert.deepEqual(await listedIds(accounts), ["max-1", "max-2", "max-3", "pro-1"]);
  assert.equal((await routes.get({})).registry?.contexts.find((context) => context.id === "work")?.account, "");
});

test("AccountsFixture: refuseNext refuses each next call of its method in turn, changing nothing, and then serves", async () => {
  const { fake, accounts } = served();
  fake.refuseNext("Accounts.Rename", Code.InvalidArgument, "name: empty");
  fake.refuseNext("Accounts.Rename", Code.FailedPrecondition, "account: busy");
  const first = await rejection(accounts.rename({ id: "max-2", name: "team" }));
  assert.equal(first.code, Code.InvalidArgument);
  assert.equal(first.rawMessage, "name: empty");
  const second = await rejection(accounts.rename({ id: "max-2", name: "team" }));
  assert.equal(second.code, Code.FailedPrecondition);
  assert.equal(second.rawMessage, "account: busy");
  assert.equal((await accounts.list({})).accounts.find((listed) => listed.id === "max-2")?.name, "max-2");
  await accounts.rename({ id: "max-2", name: "team" });
  assert.equal((await accounts.list({})).accounts.find((listed) => listed.id === "max-2")?.name, "team");
});

test("AccountsFixture: dropNext fails the next call of its method as DeadlineExceeded, changing nothing", async () => {
  const { fake, accounts } = served();
  fake.dropNext("Accounts.Add");
  const error = await rejection(accounts.add({ name: "max-4", provider: "anthropic", plan: "Claude Max", token: "t" }));
  assert.equal(error.code, Code.DeadlineExceeded);
  assert.deepEqual(await listedIds(accounts), ["max-1", "max-2", "max-3", "pro-1"]);
});

test("AccountsFixture: calls records each method and request in order, refused and dropped calls included", async () => {
  const { fake, accounts, routes } = served();
  await accounts.list({});
  fake.dropNext("Routes.Get");
  await rejection(routes.get({}));
  await accounts.rename({ id: "pro-1", name: "pro-one" });
  assert.deepEqual(
    fake.calls.map((call) => call.method),
    ["Accounts.List", "Routes.Get", "Accounts.Rename"],
  );
  const rename = fake.calls.find((call): call is Extract<Call, { method: "Accounts.Rename" }> => call.method === "Accounts.Rename");
  assert.deepEqual({ id: rename?.request.id, name: rename?.request.name }, { id: "pro-1", name: "pro-one" });
});

test("AccountsFixture: the fake copies its seed, so a change it makes does not reach the seed", async () => {
  const { accounts } = served();
  await accounts.rename({ id: "max-2", name: "team-max" });
  await accounts.remove({ id: "max-3" });
  assert.equal(seedAccounts.find((account) => account.id === "max-2")?.name, "max-2");
  assert.ok(seedAccounts.some((account) => account.id === "max-3"), "the seed lost max-3");
});

test("AccountsFixture: the fixture transport serves Accounts.List and Routes.Get from the shipped slices, unrefused", async () => {
  const fixture = createFixtureTransport();
  assert.deepEqual((await fixture.clients.accounts.list({})).accounts, seedAccounts);
  assert.deepEqual((await fixture.clients.routes.get({})).registry, seedRegistry);
  assert.deepEqual([...fixture.unserved], [], "the accounts calls were refused");
});
