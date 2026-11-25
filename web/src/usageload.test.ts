import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { Code, createRouterTransport } from "@connectrpc/connect";

import { createClients } from "./client";
import { type AccountsServe, accounts as seedAccounts, accountsServe, registry as seedRegistry } from "./fixtures/accounts";
import { at } from "./fixtures/clock";
import { type UsageServe, usageReadings, usageServe, usageSpend } from "./fixtures/usage";
import { mockTimers } from "./testclock";
import { turn, until } from "./testwait";
import { createUsageReads, type UsageReads } from "./usageload";
import { type UsageData, spanSeconds } from "./usagedata";

// start is the fake clock's first instant, 14:41 UTC on 14 November 2026, in unix seconds.
const start = Number(at("@14:41"));

// Rig is what a test drives: the reads, each read's data as it landed, the two fakes the reads call, and the page's
// hidden state.
interface Rig {
  readonly t: TestContext;
  readonly reads: UsageReads;
  readonly changes: UsageData[];
  readonly accounts: AccountsServe;
  readonly usage: UsageServe;
  readonly page: { hidden: boolean };
}

// rig mocks the clock for the test t, and returns reads over fakes seeded with invented accounts, a registry, readings and
// spend. The page is hidden when hidden is true.
function rig(t: TestContext, hidden = false): Rig {
  mockTimers(t, ["Date"]);
  t.mock.timers.setTime(start * 1000);
  const accounts = accountsServe({ accounts: seedAccounts, registry: seedRegistry });
  const usage = usageServe({ readings: usageReadings, spend: usageSpend });
  const clients = createClients(
    createRouterTransport((router) => {
      accounts.register(router);
      usage.register(router);
    }),
  );
  const changes: UsageData[] = [];
  const page = { hidden };
  const reads = createUsageReads({
    clients,
    hidden: () => page.hidden,
    now: () => Math.floor(Date.now() / 1000),
    onChange: (data) => changes.push(data),
  });
  return { t, reads, changes, accounts, usage, page };
}

// landed waits until n reads have landed, that is until onChange has been called n times.
function landed(r: Rig, n: number): Promise<void> {
  return until(r.t.signal, () => r.changes.length === n, `read ${n} to land`, { step: turn });
}

// calls counts the calls the fakes received for method, refused and dropped calls included.
function calls(r: Rig, method: string): number {
  return [...r.accounts.calls, ...r.usage.calls].filter((call) => call.method === method).length;
}

test("UsageClients: a tick while the page is hidden makes no call, and a tick once shown reads", async (t) => {
  const r = rig(t, true);
  r.reads.tick();
  r.reads.tick();
  await turn();
  assert.equal(r.accounts.calls.length + r.usage.calls.length, 0, "a call was made while the page was hidden");
  assert.equal(r.changes.length, 0, "a read landed while the page was hidden");
  r.page.hidden = false;
  r.reads.tick();
  await landed(r, 1);
  assert.equal(calls(r, "Usage.Readings"), 1, "the tick once shown did not read Usage.Readings");
});

test("UsageClients: each tick while shown reads Accounts.List, Routes.Get and Usage.Readings", async (t) => {
  const r = rig(t);
  assert.equal(r.reads.current().answered, false, "answered was true before any read landed");
  for (let tick = 1; tick <= 3; tick++) {
    r.reads.tick();
    await landed(r, tick);
  }
  assert.equal(calls(r, "Accounts.List"), 3, "Accounts.List was not read on each tick");
  assert.equal(calls(r, "Routes.Get"), 3, "Routes.Get was not read on each tick");
  assert.equal(calls(r, "Usage.Readings"), 3, "Usage.Readings was not read on each tick");
  const data = r.reads.current();
  assert.deepEqual(data.accounts, seedAccounts);
  assert.deepEqual(data.registry, seedRegistry);
  assert.deepEqual(data.readings, usageReadings);
  assert.equal(data.answered, true, "answered was not true once the three reads answered");
  assert.equal(data.failed, false);
});

test("UsageClients: at most one Usage.Spend call a minute across many ticks", async (t) => {
  const r = rig(t);
  for (let tick = 0; tick < 36; tick++) {
    r.reads.tick();
    await landed(r, tick + 1);
    t.mock.timers.tick(5000);
  }
  assert.equal(calls(r, "Usage.Readings"), 36, "Usage.Readings was not read on each tick");
  const spanned = r.usage.calls.flatMap((call) =>
    call.method === "Usage.Spend" ? [[call.request.from, call.request.to]] : [],
  );
  assert.deepEqual(
    spanned,
    [0, 60, 120].map((offset) => [BigInt(start + offset - spanSeconds), BigInt(start + offset)]),
    "Usage.Spend was not called once a minute over the span ending at each call's time",
  );
});

test("UsageClients: a failed read keeps the last data with failed true, and a later success clears it", async (t) => {
  const r = rig(t);
  r.reads.tick();
  await landed(r, 1);
  const held = r.reads.current();
  assert.equal(held.failed, false);
  r.usage.refuseNext("Usage.Readings", Code.Unavailable, "readings: busy");
  t.mock.timers.tick(5000);
  r.reads.tick();
  await landed(r, 2);
  const failed = r.reads.current();
  assert.equal(failed.failed, true, "the failed read did not set failed");
  assert.deepEqual(failed.readings, usageReadings, "the failed read did not keep the readings last read");
  assert.deepEqual(failed.accounts, held.accounts, "the failed read changed the accounts held");
  assert.equal(failed.answered, true, "the failed read changed answered");
  r.reads.tick();
  await landed(r, 3);
  assert.equal(r.reads.current().failed, false, "a later success did not clear failed");
});

test("UsageClients: a tick while a read is in flight makes no new call", async (t) => {
  const r = rig(t);
  r.reads.tick();
  r.reads.tick();
  await landed(r, 1);
  await turn();
  assert.equal(calls(r, "Usage.Readings"), 1, "a tick made a call while the read before it was in flight");
  r.reads.tick();
  await landed(r, 2);
  assert.equal(calls(r, "Usage.Readings"), 2, "the tick after the read landed made no call");
});
