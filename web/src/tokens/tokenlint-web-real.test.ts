import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

// The repository root: this file is bundled to web/build/tokens/, three directories below it.
const repo = fileURLToPath(new URL("../../../", import.meta.url));
const cli = join(repo, "web/src/tokens/lint-web.ts");

test("TokenLint: the real web/src/style.css and every web/src script pass with no problem", () => {
  const result = spawnSync(process.execPath, [cli], { cwd: repo, encoding: "utf8" });
  // A problem is one `file:line: reason` line; on failure those lines say where, and stderr says why the CLI stopped.
  const problems = result.stdout.split("\n").filter((line) => /^\S+:\d+: /.test(line));
  assert.equal(result.status, 0, problems.length > 0 ? problems.join("\n") : result.stderr);
});
