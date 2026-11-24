import assert from "node:assert/strict";
import { test } from "node:test";

import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";

import { createClients } from "./client";
import { shippedSlices, type Slices } from "./fixtures/slices";
import { createFixtureTransport } from "./fixtures/transport";
import { usageReadings, usageServe, usageSpend } from "./fixtures/usage";

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

test("UsageClients: createClients(...).usage calls Usage.Readings and Usage.Spend against a fake router transport and gets its answers", async () => {
  const fake = usageServe({ readings: usageReadings, spend: usageSpend });
  const { usage } = createClients(createRouterTransport((router) => fake.register(router)));
  const readings = await usage.readings({});
  const spend = await usage.spend({ from: 0n, to: 604800n });
  assert.deepEqual(readings.readings, usageReadings);
  assert.deepEqual(spend.seats, usageSpend);
  assert.deepEqual(
    fake.calls.map((call) => call.method),
    ["Usage.Readings", "Usage.Spend"],
  );
});

test("UsageClients: the fixture transport serves Usage.Readings and Usage.Spend from the slices, with no unserved entry", async () => {
  const slices: Slices = {
    ...shippedSlices,
    readings: usageReadings.slice(0, 1),
    spend: usageSpend.slice(0, 1),
  };
  const fixture = createFixtureTransport(slices);
  assert.deepEqual((await fixture.clients.usage.readings({})).readings, slices.readings);
  assert.deepEqual((await fixture.clients.usage.spend({ from: 0n, to: 604800n })).seats, slices.spend);
  assert.deepEqual([...fixture.unserved], [], "a Usage call was refused");
});

test("UsageClients: usageServe's refuseNext and dropNext fail the next call of their method, then serve", async () => {
  const fake = usageServe({ readings: usageReadings, spend: usageSpend });
  const { usage } = createClients(createRouterTransport((router) => fake.register(router)));
  fake.refuseNext("Usage.Readings", Code.Unavailable, "readings: busy");
  const refused = await rejection(usage.readings({}));
  assert.equal(refused.code, Code.Unavailable);
  assert.equal(refused.rawMessage, "readings: busy");
  assert.deepEqual((await usage.readings({})).readings, usageReadings);
  fake.dropNext("Usage.Spend");
  const dropped = await rejection(usage.spend({ from: 0n, to: 604800n }));
  assert.equal(dropped.code, Code.DeadlineExceeded);
  assert.deepEqual((await usage.spend({ from: 0n, to: 604800n })).seats, usageSpend);
});
