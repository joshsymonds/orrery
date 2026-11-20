import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { lintKotlin } from "./lint-kotlin";

// The repository root. This file is bundled to web/build/tokens/, three directories below it.
const repo = fileURLToPath(new URL("../../../", import.meta.url));
const cli = join(repo, "web/src/tokens/lint-kotlin.ts");

// Fixture paths under the ui, and the theme's files. No test reads the real sources.
const ui = "android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/";
const screen = `${ui}Screen.kt`;
const theme = `${ui}theme/Motion.kt`;
const generated = `${ui}theme/OrreryTokens.kt`;

const fixture = (...lines: string[]): string => lines.join("\n");
// The "path:line" of each problem lintKotlin finds in a fixture.
const where = (path: string, text: string): string[] => lintKotlin(path, text).map((p) => `${p.file}:${p.line}`);

// Runs the CLI from the root of a temporary repository that holds the given Kotlin files under the ui, as the recipes
// run it from the repository root.
function cliIn(files: Record<string, string>, args: string[]) {
  const root = mkdtempSync(join(tmpdir(), "tokenlint-"));
  try {
    mkdirSync(join(root, ui), { recursive: true });
    for (const [name, text] of Object.entries(files)) writeFileSync(join(root, ui, name), text);
    return spawnSync(process.execPath, [cli, ...args], { cwd: root, encoding: "utf8" });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test("TokenLint: a Color( call fails on its line, named by file and line", () => {
  const text = fixture("import androidx.compose.ui.graphics.Color", "val edge = Color(0xFF112233)");
  assert.deepEqual(where(screen, text), [`${screen}:2`]);
});

test("TokenLint: a Mocha. name and a MochaX identifier fail on their lines", () => {
  const text = fixture("Text(color = Mocha.Peach)", "val s = MochaColorScheme");
  assert.deepEqual(where(screen, text), [`${screen}:1`, `${screen}:2`]);
});

test("TokenLint: OrreryTokens.Palette and Palette. fail outside ui/theme, and pass inside it", () => {
  const text = fixture("val red = OrreryTokens.Palette.red", "val pink = Palette.pink");
  assert.deepEqual(where(screen, text), [`${screen}:1`, `${screen}:2`]);
  assert.deepEqual(where(theme, text), []);
});

test("TokenLint: a literal .sp fails on its line: 12.sp, 12f.sp and 1.5.sp", () => {
  const text = fixture("Text(size = 12.sp)", "Text(size = 12f.sp)", "Text(size = 1.5.sp)");
  assert.deepEqual(where(screen, text), [`${screen}:1`, `${screen}:2`, `${screen}:3`]);
});

test("TokenLint: a literal .dp other than 0.dp fails on its line, and 0.dp passes", () => {
  const text = fixture("Modifier.padding(4.dp)", "Modifier.padding(0.dp)", "Modifier.padding(10.dp)");
  assert.deepEqual(where(screen, text), [`${screen}:1`, `${screen}:3`]);
});

test("TokenLint: copy(alpha = 0.5f) fails on its line", () => {
  assert.deepEqual(where(screen, fixture("val faint = color.copy(alpha = 0.5f)")), [`${screen}:1`]);
});

test("TokenLint: alpha = 0.4f and .alpha(0.3f) fail on their lines", () => {
  const text = fixture("Surface(", "    alpha = 0.4f,", ")", "Modifier.alpha(0.3f)");
  assert.deepEqual(where(screen, text), [`${screen}:2`, `${screen}:4`]);
});

test("TokenLint: a const named FOO_ALPHA bound to a number fails on its line, and one bound to a token passes", () => {
  const text = fixture("const val FOO_ALPHA = 0.7f", "val BAR_ALPHA = OrreryTokens.Alpha.tintFill");
  assert.deepEqual(where(screen, text), [`${screen}:1`]);
});

test("TokenLint: durationMillis = 300 fails on its line", () => {
  const text = fixture("Animation(", "    durationMillis = 300,", ")");
  assert.deepEqual(where(screen, text), [`${screen}:2`]);
});

test("TokenLint: tween(300) fails inside ui/theme too, as a literal duration", () => {
  assert.deepEqual(where(theme, fixture("val spec = tween(300)")), [`${theme}:1`]);
});

test("TokenLint: tween( fails outside ui/theme and passes inside ui/theme/Motion.kt", () => {
  const text = fixture("val spec = if (animated) tween(millis, easing = EaseOut) else snap()");
  assert.deepEqual(where(screen, text), [`${screen}:1`]);
  assert.deepEqual(where(theme, text), []);
});

test("TokenLint: inside ui/theme, outside the generated OrreryTokens.kt, a Color( literal, a Mocha name, a literal .sp, a literal .dp, a literal alpha, an ALPHA constant bound to a number, a literal duration and FontFamily.Monospace each fail, in Theme.kt, Type.kt and Motion.kt", () => {
  const text = fixture(
    "val edge = Color(0xFF112233)",
    "val hue = Mocha.Peach",
    "val size = 12.sp",
    "val gap = 4.dp",
    "val faint = color.copy(alpha = 0.5f)",
    "const val FOO_ALPHA = 0.7f",
    "    durationMillis = 300,",
    "val family = FontFamily.Monospace",
  );
  const lines = [1, 2, 3, 4, 5, 6, 7, 8];
  for (const path of [`${ui}theme/Theme.kt`, `${ui}theme/Type.kt`, `${ui}theme/Motion.kt`]) {
    assert.deepEqual(where(path, text), lines.map((line) => `${path}:${line}`));
  }
  assert.deepEqual(where(generated, text), []);
});

test("TokenLint: FontFamily.Monospace and FontFamily.Default fail on their lines", () => {
  const text = fixture("Text(fontFamily = FontFamily.Monospace)", "Text(fontFamily = FontFamily.Default)");
  assert.deepEqual(where(screen, text), [`${screen}:1`, `${screen}:2`]);
});

test("TokenLint: MaterialTheme.typography and MaterialTheme.colorScheme fail outside ui/theme, and pass inside it", () => {
  const text = fixture(
    "Text(style = MaterialTheme.typography.bodyLarge)",
    "Box(color = MaterialTheme.colorScheme.primary)",
  );
  assert.deepEqual(where(screen, text), [`${screen}:1`, `${screen}:2`]);
  assert.deepEqual(where(theme, text), []);
});

test("TokenLint: OrreryTokens.kt passes though it is full of Color( and .dp", () => {
  const text = fixture("val crust: Color = Color(0xFF11111B)", "val gap = 4.dp", "val rim = Color(0xFF181825)");
  assert.deepEqual(where(generated, text), []);
});

test("TokenLint: a fully tokenized fixture passes, and a line wholly a // comment is not code", () => {
  const text = fixture(
    "package io.github.joshsymonds.orrery.ui",
    "",
    "// A Color( or Mocha.Peach in a comment, and a 4.dp, are not code.",
    "@Composable",
    "fun Row(label: String) {",
    "    Text(",
    "        text = label,",
    "        style = OrreryTokens.Type.body,",
    "        color = OrreryTokens.Ink.two,",
    "        modifier = Modifier.padding(OrreryTokens.Space.s8).alpha(OrreryTokens.Alpha.tintFill),",
    "    )",
    "}",
  );
  assert.deepEqual(where(screen, text), []);
});

test("TokenLint: a path outside the Android ui is refused, not passed", () => {
  assert.throws(() => lintKotlin("web/src/Screen.kt", "val c = Color(0xFF112233)"), /not a Kotlin file under the Android ui/);
});

test("TokenLint: the CLI with no files exits 1 on a bad Kotlin file, printing its file and line", () => {
  const result = cliIn(
    { "Bad.kt": fixture("val ok = 1", "val edge = Color(0xFF112233)"), "Clean.kt": fixture("val ok = 1") },
    [],
  );
  assert.equal(result.status, 1, result.stderr);
  const lines = result.stdout.trim().split("\n");
  assert.equal(lines.length, 1, result.stdout);
  assert.ok(lines[0].startsWith(`${ui}Bad.kt:2: `), lines[0]);
});

test("TokenLint: the CLI given a clean file exits 0 with one all-clear line", () => {
  const result = cliIn({ "Clean.kt": fixture("val ok = 1") }, [`${ui}Clean.kt`]);
  assert.equal(result.status, 0, result.stderr);
  const lines = result.stdout.trim().split("\n");
  assert.equal(lines.length, 1, result.stdout);
  assert.match(lines[0], /^all clear: 1 Kotlin file\(s\)/);
});
