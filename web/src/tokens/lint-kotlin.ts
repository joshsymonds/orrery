// Holds Android's ui Kotlin to the design tokens: a component names a role and takes its colour, size, alpha and motion
// from a token, never a hue or a literal. The rules are per line. Run as node's entry script it lints the files given,
// or with none every Kotlin file under the ui, from the repository root it is run in, and exits 1 on a problem.

import { readdirSync, readFileSync, realpathSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export interface Problem {
  file: string;
  line: number;
  reason: string;
}

// The Kotlin linted, repository-relative. theme/ holds the tokens and their motion, so it may name a Palette, a
// MaterialTheme and a tween; the generated OrreryTokens.kt is exempt.
const ui = "android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/";
const theme = `${ui}theme/`;
const generated = `${theme}OrreryTokens.kt`;

const inScope = (path: string): boolean => path.startsWith(ui) && path.endsWith(".kt");

interface Rule {
  // The text a failing line matches; the reason names it.
  pattern: RegExp;
  // What the matched text is, as the reason states it: "Color( is <why>".
  why: string;
  // Whether the rule applies to the file. Every rule applies to every file in scope when this is absent.
  applies?: (path: string) => boolean;
}

const outsideTheme = (path: string): boolean => !path.startsWith(theme);

const rules: Rule[] = [
  { pattern: /\bColor\(/, why: "a colour literal; take the colour from a token" },
  { pattern: /\bMocha\w*/, why: "a hue; a component names a role" },
  {
    pattern: /\bOrreryTokens\.Palette\b|\bPalette\./,
    why: "a hue outside ui/theme; a component names a role",
    applies: outsideTheme,
  },
  { pattern: /\b\d+(?:\.\d+)?f?\.sp\b/, why: "a literal size; take it from a token" },
  // 0.dp is the one literal length; the lookahead skips exactly it, not 10.dp or 0.5.dp.
  { pattern: /\b(?!0\.dp\b)\d+(?:\.\d+)?f?\.dp\b/, why: "a literal length; only 0.dp is literal" },
  { pattern: /(?:\balpha\s*=|\.alpha\()\s*\d[\w.]*/, why: "a literal alpha; take it from a token" },
  {
    pattern: /\bval\s+\w*ALPHA\w*\s*(?::[^=]*)?=\s*\d[\w.]*/,
    why: "an alpha constant bound to a literal; take it from a token",
  },
  { pattern: /\bdurationMillis\s*=\s*\d[\w.]*/, why: "a literal duration; take the timing from Move.spec" },
  { pattern: /\btween\(\s*\d[\w.]*/, why: "a literal duration; take the timing from Move.spec" },
  { pattern: /\btween\(/, why: "outside ui/theme; callers take Move.spec", applies: outsideTheme },
  {
    pattern: /\bFontFamily\.(?:SansSerif|Serif|Cursive|Monospace|Default)\b/,
    why: "a system font family; take the type from a token",
  },
  {
    pattern: /\bFontFamily\(/,
    why: "a font family built outside ui/theme; take Orrery Mono or Inter from the theme",
    applies: outsideTheme,
  },
  {
    pattern: /\bR\.font\.(?!(?:orrery_mono|inter)_)\w+/,
    why: "a font resource other than orrery_mono_* or inter_*; the faces are Orrery Mono and Inter",
  },
  {
    pattern: /\bMaterialTheme\.(?:typography|colorScheme)\b/,
    why: "outside ui/theme; read the tokens, not the theme",
    applies: outsideTheme,
  },
];

// The problems in a Kotlin text at a repository-relative path. A line wholly a // comment is skipped. OrreryTokens.kt
// has none, since it is generated from the tokens. A path outside the ui is refused rather than passed.
export function lintKotlin(path: string, text: string): Problem[] {
  if (!inScope(path)) throw new RangeError(`${path} is not a Kotlin file under the Android ui`);
  if (path === generated) return [];
  const problems: Problem[] = [];
  text.split(/\r?\n/).forEach((line, index) => {
    if (line.trimStart().startsWith("//")) return;
    for (const rule of rules) {
      if (rule.applies !== undefined && !rule.applies(path)) continue;
      const match = rule.pattern.exec(line);
      if (match !== null) problems.push({ file: path, line: index + 1, reason: `${match[0]} is ${rule.why}` });
    }
  });
  return problems;
}

// Every Kotlin file under the ui in root, repository-relative and sorted.
function kotlinUnder(root: string): string[] {
  return readdirSync(join(root, ui), { recursive: true, encoding: "utf8" })
    .filter((name) => name.endsWith(".kt"))
    .map((name) => ui + name)
    .sort();
}

// Lints the files named, or every Kotlin file under the ui when none is. Returns the exit code: 0 clear, 1 a problem,
// 2 a file it cannot lint.
function main(args: string[]): number {
  const root = process.cwd();
  try {
    const files = args.length > 0 ? args.map((arg) => relative(root, resolve(root, arg))) : kotlinUnder(root);
    let problems = 0;
    for (const file of files) {
      for (const problem of lintKotlin(file, readFileSync(join(root, file), "utf8"))) {
        console.log(`${problem.file}:${problem.line}: ${problem.reason}`);
        problems++;
      }
    }
    if (problems > 0) return 1;
    console.log(`all clear: ${files.length} Kotlin file(s) hold to the tokens`);
    return 0;
  } catch (error) {
    console.error(`lint-kotlin: ${error instanceof Error ? error.message : String(error)}`);
    return 2;
  }
}

// This file is node's entry script when argv[1] names it. A test bundles it, and then argv[1] is the test's file, so
// the name check keeps the CLI from running inside the test.
const entry =
  process.argv[1] !== undefined &&
  realpathSync(process.argv[1]) === fileURLToPath(new URL("./lint-kotlin.ts", import.meta.url));
if (entry) process.exitCode = main(process.argv.slice(2));
