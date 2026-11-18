import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { test } from "node:test";

import { options, render } from "preact";
import { useEffect } from "preact/hooks";

import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom } from "./testdom";
import { turn } from "./testwait";

test("a 100 ms timer scheduled after a setTimeout with no delay fires on the tick that reaches it", (t) => {
  mockTimers(t, ["setTimeout"]);
  // Unpatched, the mock schedules the no-delay timer at NaN, and these four timers leave the 100 ms one below the NaN
  // one in the mock's queue, under the 60 s timer, so a tick of 100 ms stops at the 60 s timer and fires nothing.
  const fired: string[] = [];
  setTimeout(() => fired.push("60 s"), 60_000);
  setTimeout(() => fired.push("no delay"));
  setTimeout(() => fired.push("70 s"), 70_000);
  setTimeout(() => fired.push("100 ms"), 100);
  t.mock.timers.tick(100);
  assert.deepEqual(fired, ["no delay", "100 ms"]);
});

test("a setInterval with no delay fires every millisecond, and leaves a later due timer to fire", (t) => {
  mockTimers(t, ["setInterval", "setTimeout"]);
  let ticks = 0;
  setTimeout(() => {}, 60_000);
  setInterval(() => {
    ticks++;
  });
  setTimeout(() => {}, 70_000);
  let fired = false;
  setTimeout(() => {
    fired = true;
  }, 100);
  t.mock.timers.tick(100);
  assert.equal(fired, true, "the 100 ms timer did not fire");
  assert.equal(ticks, 100);
});

test("a component's effect runs while setTimeout is mocked, with no tick of the clock", async (t) => {
  mockTimers(t, ["setTimeout"]);
  let ran = false;
  function Probe() {
    useEffect(() => {
      ran = true;
    }, []);
    return null;
  }
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    // Rendered outside act, the effect waits for Preact's after-paint flush, which mockTimers runs on the real
    // setImmediate: the one turn queued after it lets it run. On the mocked clock it would wait for a tick of 100 ms.
    render(<Probe />, container);
    await turn();
    assert.equal(ran, true, "the effect did not run");
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
});

test("Preact's after-paint scheduling is restored when the test that mocked the timers ends", async (t) => {
  const before = options.requestAnimationFrame;
  await t.test("a test that mocks the timers", (sub) => {
    mockTimers(sub, ["setTimeout"]);
    assert.notEqual(options.requestAnimationFrame, before, "mockTimers left Preact's after-paint scheduling alone");
  });
  assert.equal(options.requestAnimationFrame, before);
});

test("every file under web/src mocks timers through mockTimers", () => {
  const src = new URL("../src/", import.meta.url);
  const direct = readdirSync(src, { recursive: true, encoding: "utf8" })
    .filter((file) => /\.tsx?$/.test(file) && file !== "testclock.ts")
    .filter((file) => /\.timers\.enable\(/.test(readFileSync(new URL(file, src), "utf8")));
  assert.deepEqual(direct, [], "these files enable mock timers directly instead of calling mockTimers");
});
