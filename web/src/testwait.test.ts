import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { turn, until } from "./testwait";

// childEnv marks the run of this file that holds a wait open. Without it the file runs the test that starts that run.
const childEnv = "ORRERY_TESTWAIT_CHILD";

// childTimeoutMs is the --test-timeout of the child run: short, since its one test can only end by timing out.
const childTimeoutMs = 200;

if (process.env[childEnv] === "1") {
  test("a wait whose check never holds", async (t) => {
    await until(t.signal, () => false, "a state that never comes");
  });
} else {
  test("a wait still polling when its test times out ends with the test, and its process exits", async () => {
    // The forced order: the child's only test waits on a check that never holds, so --test-timeout ends the test while
    // the wait polls. The wait must stop with the test: one that polled on would hold the child's event loop open, and
    // the child would never exit, which this test's own --test-timeout would then report.
    // The child is a run of its own, not a file of this run: node:test marks a file it runs with NODE_TEST_CONTEXT,
    // which the child must not inherit, or it would report to a parent that is not reading it.
    const { NODE_TEST_CONTEXT: _, ...env } = process.env;
    const child = spawn(
      process.execPath,
      ["--test", `--test-timeout=${childTimeoutMs}`, fileURLToPath(import.meta.url)],
      { env: { ...env, [childEnv]: "1" }, stdio: ["ignore", "pipe", "pipe"] },
    );
    let output = "";
    child.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
    });
    child.stderr.on("data", (chunk: Buffer) => {
      output += chunk.toString();
    });
    const code = await new Promise<number | null>((resolve) => child.on("close", resolve));
    assert.notEqual(code, 0, "the child run passed a wait that never holds");
    assert.ok(output.includes("a wait whose check never holds"), `the child did not name its test:\n${output}`);
    assert.ok(output.includes("timed out"), `the child's test did not time out:\n${output}`);
  });
}

test("a wait stepped by turn ends while setTimeout is mocked, once a promise chain makes its check hold", async (t) => {
  // Every setTimeout is mocked for this test. A wait that stepped by a timer would never end, so the test ends only if
  // turn needs no timer.
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let landed = false;
  void Promise.resolve()
    .then(() => Promise.resolve())
    .then(() => {
      landed = true;
    });
  await until(t.signal, () => landed, "the promise chain", { step: turn });
  assert.equal(landed, true);
});
