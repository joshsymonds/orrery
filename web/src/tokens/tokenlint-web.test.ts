import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { LEGACY_BREAKPOINTS, lintCss, lintScript, type Problem } from "./lint-web";

// The repository root: this file is bundled to web/build/tokens/, three directories below it.
const repo = fileURLToPath(new URL("../../../", import.meta.url));
const cli = join(repo, "web/src/tokens/lint-web.ts");

// The file and line of each problem, once per line, so a test names exactly the lines that fail.
const located = (problems: Problem[]): string[] => [...new Set(problems.map((p) => `${p.file}:${p.line}`))];
// The reasons given for one line.
const reasonsAt = (problems: Problem[], line: number): string => problems.filter((p) => p.line === line).map((p) => p.reason).join("\n");

// A sheet that is tokenized throughout, and so passes.
const CLEAN_SHEET = `@font-face { font-family: "Orrery Mono"; src: url("orrery_mono_regular.ttf") format("truetype"); font-weight: 400; }
.card {
  --gap: var(--o-space-8);
  display: grid;
  gap: var(--gap);
  padding: var(--o-space-16);
  border: var(--o-size-hairline) solid var(--o-line-soft);
  border-radius: var(--o-radius-2);
  background: var(--o-role-you-fill);
  color: var(--o-ink-1);
  font: var(--o-type-body);
  font-family: "Orrery Mono";
  letter-spacing: var(--o-type-label-tracking);
  opacity: 1;
}
.card:hover {
  background: var(--o-role-you-line);
}
.card dt {
  color: var(--o-ink-2);
  font-weight: var(--o-font-weight-bold);
}
@keyframes fade {
  0% { opacity: 0; }
  100% { opacity: 1; }
}
@media (min-width: 760px) {
  .card { padding: var(--o-space-24); }
}
@media (max-width: 1249px) {
  .card { gap: var(--o-space-12); }
}
`;

test("TokenLint: a hex colour fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  color: #fff;\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /hex colour/);
});

test("TokenLint: rgb, rgba, hsl and hsla colour functions fail on their lines", () => {
  const problems = lintCss(
    "fixture.css",
    ".a {\n  color: rgb(1 2 3);\n  color: rgba(1, 2, 3, 0.5);\n  color: hsl(120 50% 50%);\n  color: hsla(120, 50%, 50%, 0.5);\n}\n",
  );
  assert.deepEqual(located(problems), ["fixture.css:2", "fixture.css:3", "fixture.css:4", "fixture.css:5"]);
  assert.match(reasonsAt(problems, 5), /colour function/);
});

test("TokenLint: a named colour fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  color: white;\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /named colour/);
});

test("TokenLint: a px length fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  padding: 4px;\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /px length/);
});

test("TokenLint: an em or rem length fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  margin: 1.5em;\n  gap: 2rem;\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2", "fixture.css:3"]);
  assert.match(reasonsAt(problems, 3), /em length/);
});

test("TokenLint: a ms or s duration fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  transition: color 200ms;\n  animation: spin 1.1s linear;\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2", "fixture.css:3"]);
  assert.match(reasonsAt(problems, 3), /duration/);
});

test("TokenLint: font-size fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  font-size: var(--o-type-body-size);\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /font-size/);
});

test("TokenLint: a font-family other than Orrery Mono fails on its line, and Orrery Mono passes", () => {
  const problems = lintCss("fixture.css", ".a {\n  font-family: monospace;\n  font-family: \"Orrery Mono\";\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /font-family/);
});

test("TokenLint: line-height fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  line-height: 1.5;\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /line-height/);
});

test("TokenLint: a raw font-weight fails on its line, and a font-weight token passes", () => {
  const problems = lintCss("fixture.css", ".a {\n  font-weight: 600;\n  font-weight: var(--o-font-weight-bold);\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /font-weight/);
});

test("TokenLint: a raw letter-spacing fails on its line, and a token, 0 and normal pass", () => {
  const problems = lintCss(
    "fixture.css",
    ".a {\n  letter-spacing: 0.04;\n  letter-spacing: var(--o-type-label-tracking);\n  letter-spacing: 0;\n  letter-spacing: normal;\n}\n",
  );
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /letter-spacing/);
});

test("TokenLint: opacity below 1 fails on its line, and an opacity: 0 keyframe passes", () => {
  const problems = lintCss("fixture.css", ".a {\n  opacity: 0.5;\n}\n@keyframes fade {\n  0% { opacity: 0; }\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /opacity/);
});

test("TokenLint: opacity: 0 at the end of a fade passes, in a 100% keyframe and a to keyframe, while opacity: 0.5 beside them fails", () => {
  const problems = lintCss(
    "fixture.css",
    "@keyframes out {\n  from { opacity: 1; }\n  100% { opacity: 0; }\n}\n@keyframes leave {\n  to { opacity: 0; }\n}\n.a {\n  opacity: 0.5;\n}\n",
  );
  assert.deepEqual(located(problems), ["fixture.css:9"]);
  assert.equal(problems.length, 1);
  assert.match(reasonsAt(problems, 9), /opacity/);
});

test("TokenLint: opacity: 0 inside @starting-style passes, as the start of a fade", () => {
  assert.deepEqual(
    lintCss("fixture.css", ".a {\n  transition: opacity var(--o-move-state);\n  @starting-style {\n    opacity: 0;\n  }\n}"),
    [],
  );
  const dimmed = lintCss(
    "fixture.css",
    ".a {\n  transition: opacity var(--o-move-state);\n  @starting-style {\n    opacity: 0.4;\n  }\n}",
  );
  assert.deepEqual(located(dimmed), ["fixture.css:4"]);
});

test("TokenLint: a raw z-index fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  z-index: 30;\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /z-index/);
});

test("TokenLint: a var(--o-...) that tokens.css does not define fails on its line, and a defined one passes", () => {
  const problems = lintCss("fixture.css", ".a {\n  color: var(--o-ink-missing);\n  color: var(--o-ink-1);\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /--o-ink-missing is not a token/);
});

test("TokenLint: a var(--o-palette-...) fails on its line, since a component never names a hue", () => {
  const problems = lintCss("fixture.css", ".a {\n  color: var(--o-palette-red);\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /never names a hue/);
});

test("TokenLint: color-mix( fails on its line, named by file and line", () => {
  const problems = lintCss("fixture.css", ".a {\n  background: color-mix(in srgb, var(--o-ink-1) 10%, transparent);\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /color-mix\(/);
});

test("TokenLint: a local variable read with no fallback and never set fails, and one with a fallback passes", () => {
  const problems = lintCss("fixture.css", ".a {\n  color: var(--tint);\n  padding: var(--gap, var(--o-space-4));\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:2"]);
  assert.match(reasonsAt(problems, 2), /--tint is read and never set/);
});

test("TokenLint: @media on 760, 759 and 1249 passes, each a breakpoint token or one less", () => {
  const problems = lintCss(
    "fixture.css",
    "@media (min-width: 760px) {\n}\n@media (max-width: 759px) {\n}\n@media (max-width: 1249px) {\n}\n",
  );
  assert.deepEqual(problems, []);
});

test("TokenLint: @media on exactly 1100, 1500 and 480 passes, as the listed legacy breakpoints", () => {
  const problems = lintCss(
    "fixture.css",
    "@media (max-width: 1100px) {\n}\n@media (min-width: 1500px) {\n}\n@media (max-width: 480px) {\n}\n",
  );
  assert.deepEqual(problems, []);
});

test("TokenLint: LEGACY_BREAKPOINTS lists 1100 and 480 with F2 and 1500 with F4", () => {
  assert.deepEqual(
    LEGACY_BREAKPOINTS.map((b) => `${b.px} ${b.epic}`),
    ["1100 F2", "1500 F4", "480 F2"],
  );
});

test("TokenLint: @media on 1251 and on 900 fails, each named by its line", () => {
  const problems = lintCss("fixture.css", "@media (min-width: 1251px) {\n}\n@media (max-width: 900px) {\n}\n");
  assert.deepEqual(located(problems), ["fixture.css:1", "fixture.css:3"]);
  assert.match(reasonsAt(problems, 1), /1251px is not a breakpoint token/);
  assert.match(reasonsAt(problems, 3), /900px is not a breakpoint token/);
});

test("TokenLint: @font-face lines are exempt, so a face's raw weight passes", () => {
  const problems = lintCss(
    "fixture.css",
    '@font-face { font-family: "Orrery Mono"; src: url("mono.ttf") format("truetype"); font-weight: 400; }\n',
  );
  assert.deepEqual(problems, []);
});

test("TokenLint: a fully tokenized sheet passes with no problem", () => {
  assert.deepEqual(lintCss("fixture.css", CLEAN_SHEET), []);
});

test("TokenLint: a comment is blanked before matching, so a value named only in one passes", () => {
  const problems = lintCss("fixture.css", "/* color: #fff; padding: 4px; font-size: 12px */\n.a {\n  color: var(--o-ink-1);\n}\n");
  assert.deepEqual(problems, []);
});

test("TokenLint: a hex colour in a script fails on its line, named by file and line", () => {
  const problems = lintScript("card.tsx", 'export const a = 1;\nexport const fill = "#1e1e2e";\n');
  assert.deepEqual(located(problems), ["card.tsx:2"]);
  assert.match(reasonsAt(problems, 2), /hex colour/);
});

test("TokenLint: an inline px in a script fails on its line, in a style object and in a style string", () => {
  const problems = lintScript(
    "card.tsx",
    'export const C = () => (\n  <div style={{ padding: "4px" }}>\n    <i style="margin: 2px" />\n  </div>\n);\n',
  );
  assert.deepEqual(located(problems), ["card.tsx:2", "card.tsx:3"]);
  assert.match(reasonsAt(problems, 3), /px length in an inline style/);
});

test("TokenLint: mark.tsx, the logo, passes the same text that fails in any other script", () => {
  const text = 'export const fill = "#1e1e2e";\nexport const C = () => <i style={{ padding: "4px" }} />;\n';
  assert.equal(lintScript("card.tsx", text).length, 2);
  assert.deepEqual(lintScript("mark.tsx", text), []);
});

test("TokenLint: a comment line in a script is skipped, so a colour named in one passes", () => {
  const problems = lintScript("card.tsx", '// the fill is #1e1e2e\n// style={{ padding: "4px" }}\n');
  assert.deepEqual(problems, []);
});

test("TokenLint: the CLI exits 1 on a bad fixture and prints file:line for each problem", () => {
  const dir = mkdtempSync(join(tmpdir(), "tokenlint-"));
  try {
    const bad = join(dir, "bad.css");
    writeFileSync(bad, ".a {\n  padding: 4px;\n}\n");
    const result = spawnSync(process.execPath, [cli, bad], { encoding: "utf8" });
    assert.equal(result.status, 1, result.stderr);
    assert.ok(result.stdout.includes(`${bad}:2: `), result.stdout);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("TokenLint: the CLI exits 0 on a clean fixture and prints one all-clear line", () => {
  const dir = mkdtempSync(join(tmpdir(), "tokenlint-"));
  try {
    const clean = join(dir, "clean.css");
    writeFileSync(clean, CLEAN_SHEET);
    const result = spawnSync(process.execPath, [cli, clean], { encoding: "utf8" });
    assert.equal(result.status, 0, result.stdout + result.stderr);
    assert.equal(result.stdout.trim().split("\n").length, 1);
    assert.match(result.stdout, /hold to the tokens/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("TokenLint: with no files the CLI lints web/src/style.css and every non-test script under web/src, and skips *.test.ts and *.test.tsx", () => {
  // The same walk the CLI makes over web/src, so the count it prints can be checked against the files on disk.
  const scripts = readdirSync(join(repo, "web/src"), { encoding: "utf8", recursive: true }).filter((rel) => /\.tsx?$/.test(rel));
  const testFiles = scripts.filter((rel) => /\.test\.tsx?$/.test(rel));
  const nonTestFiles = scripts.filter((rel) => !/\.test\.tsx?$/.test(rel));
  assert.ok(testFiles.length >= 1, "web/src holds no *.test.ts or *.test.tsx file to skip");
  const result = spawnSync(process.execPath, [cli], { cwd: repo, encoding: "utf8" });
  assert.equal(result.status, 0, result.stdout + result.stderr);
  // style.css is the one sheet linted, so it is the plus one.
  assert.equal(result.stdout.trim(), `lint-web: ${nonTestFiles.length + 1} files hold to the tokens`);
});
