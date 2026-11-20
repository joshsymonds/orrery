import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

// The repository root. This file is bundled to web/build/tokens/, three directories below it.
const repo = fileURLToPath(new URL("../../../", import.meta.url));
const cli = join(repo, "web/src/tokens/lint-kotlin.ts");

test("TokenLint: the real Kotlin under android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui passes with no problem", () => {
  // The CLI with no arguments lints every Kotlin file under the ui, from the root it is run in.
  const result = spawnSync(process.execPath, [cli], { cwd: repo, encoding: "utf8" });
  assert.equal(result.status, 0, `${result.error ?? ""}${result.stdout}${result.stderr}`);
});
