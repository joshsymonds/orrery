// Holds the web's stylesheet and scripts to the tokens. A component names a token from web/src/tokens.css, which
// tokens.json generates; it never writes a colour, length, duration, font or palette hue of its own.
//   node web/src/tokens/lint-web.ts [file ...]
// With no files it lints web/src/style.css and every non-test script under web/src. A .css file is linted as a
// stylesheet, a .ts or .tsx file as a script. It prints one `file:line: reason` per problem and exits 1, or prints an
// all-clear and exits 0. It exits 2 on a file it cannot read or one that is neither a stylesheet nor a script.
import { readFileSync, readdirSync } from "node:fs";
import { basename, join } from "node:path";
import { fileURLToPath } from "node:url";

export interface Problem {
  file: string;
  line: number;
  reason: string;
}

// The media queries that name a number no token holds. Each is today's layout, and goes when the epic that redraws it
// lands.
export const LEGACY_BREAKPOINTS: readonly { readonly px: number; readonly layout: string; readonly epic: string }[] = [
  { px: 1100, layout: "the inbox", epic: "F2" },
  { px: 1500, layout: "the right rail", epic: "F4" },
  { px: 480, layout: "the key list's one-column form", epic: "F2" },
];

// The repository root. This file, and its bundle in web/build/tokens, are both three directories below it.
const repo = fileURLToPath(new URL("../../../", import.meta.url));
const read = (rel: string): string => readFileSync(join(repo, rel), "utf8");

interface TokenSet {
  defined: ReadonlySet<string>;
  breakpoints: ReadonlySet<number>;
}

let tokenSet: TokenSet | undefined;
// The --o- names tokens.css defines, and the two breakpoints tokens.json's layout names.
function tokens(): TokenSet {
  if (tokenSet === undefined) {
    const { layout } = JSON.parse(read("web/visual/system/tokens.json")) as {
      layout: { breakpointPhone: number; breakpointNarrow: number };
    };
    tokenSet = {
      defined: new Set([...read("web/src/tokens.css").matchAll(/(--o-[a-z0-9-]+)\s*:/g)].map((m) => m[1])),
      breakpoints: new Set([layout.breakpointPhone, layout.breakpointNarrow]),
    };
  }
  return tokenSet;
}

// What a sheet may not hold, each with the reason printed beside the line.
const STRICT: readonly (readonly [RegExp, string])[] = [
  [/#[0-9a-fA-F]{3,8}\b/, "a hex colour: name a role, an ink, a surface or a line"],
  [/\b(rgb|rgba|hsl|hsla)\(/, "a colour function: name a token"],
  [/(?<![\w-])-?\d*\.?\d+px\b/, "a px length: use a space, size, radius or layout token"],
  [/(?<![\w-])\d*\.?\d+r?em\b/, "an em or rem length: use a token"],
  [/(?<![\w-])\d*\.?\d+m?s\b/, "a duration: use a move or loop token"],
  [/\bfont-size\s*:/, "font-size: set font to a type token"],
  [/\bfont-family\s*:(?!\s*"Orrery Mono")/, "font-family: set font to a type token"],
  [/\bline-height\s*:/, "line-height: the type token carries it"],
  [/\bfont-weight\s*:(?!\s*var\()/, "a raw font-weight: use --o-font-weight-*"],
  [/\bletter-spacing\s*:(?!\s*(var\(|0(?![.\d])|normal))/, "raw letter-spacing: use the label's tracking"],
  [/\bopacity\s*:\s*0?\.\d/, "dimming with opacity: draw it in a lower ink instead"],
  [/\bz-index\s*:(?!\s*var\()/, "a raw z-index: use a layer token"],
  [/\bcolor-mix\(/, "color-mix(: a tint is a -fill or -line token, a shadow an elevation token"],
];
// A colour name as a value, not a class, a selector or a token's name (which has a hyphen before it).
const NAMED = /:[^;{}]*(?<![\w.#-])(white|black|red|green|blue|yellow|orange|purple|grey|gray|silver)(?![\w-])/;

// Comments are blanked to spaces, so a line keeps its number and a comment matches nothing.
const blankComments = (css: string): string => css.replace(/\/\*[\s\S]*?\*\//g, (c) => c.replace(/[^\n]/g, " "));

export function lintCss(file: string, css: string): Problem[] {
  const { defined, breakpoints } = tokens();
  const sheet = blankComments(css);
  const problems: Problem[] = [];
  const fail = (line: number, reason: string): void => {
    problems.push({ file, line, reason });
  };

  sheet.split("\n").forEach((text, i) => {
    const line = i + 1;
    // A face declares its own family and weight; those are not component values.
    if (/^\s*@font-face/.test(text)) return;
    // A media query cannot read a variable, so each number it names is a breakpoint token, or one less for max-width.
    if (/^\s*@media/.test(text)) {
      for (const [, digits] of text.matchAll(/(\d+)px/g)) {
        const px = Number(digits);
        if (breakpoints.has(px) || breakpoints.has(px + 1) || LEGACY_BREAKPOINTS.some((b) => b.px === px)) continue;
        fail(line, `${digits}px is not a breakpoint token: use breakpointPhone or breakpointNarrow, or one less`);
      }
      return;
    }
    for (const [pattern, reason] of STRICT) if (pattern.test(text)) fail(line, reason);
    if (NAMED.test(text)) fail(line, "a named colour: name a token");
    for (const [, name] of text.matchAll(/var\((--o-[a-z0-9-]+)/g)) {
      if (name.startsWith("--o-palette-")) fail(line, `${name}: a component never names a hue, only a role, an ink or a line`);
      else if (!defined.has(name)) fail(line, `${name} is not a token`);
    }
    for (const [, name, comma] of text.matchAll(/var\((--(?!o-)[a-z0-9-]+)(\s*,)?/g)) {
      // A local variable is set in this sheet, or read with a fallback (the markup sets it).
      if (!comma && !new RegExp(`(?<![\\w-])${name}\\s*:`).test(sheet)) fail(line, `${name} is read and never set`);
    }
  });
  return problems;
}

// A script's comment line, which is skipped: a line comment, or a line within a block comment.
const COMMENT_LINE = /^\s*(\/\/|\/\*|\*)/;
const SCRIPT_HEX = /(?<![\w.])#[0-9a-fA-F]{3,8}\b/;
// An inline style: preact's style={{ ... }}, or a style string in either quote.
const INLINE_STYLE = /\bstyle=(?:\{\{([^}]*)\}\}|"([^"]*)"|'([^']*)')/g;
const PX = /(?<![\w-])-?\d*\.?\d+px\b/;

export function lintScript(file: string, text: string): Problem[] {
  // The logo draws its own colours.
  if (basename(file) === "mark.tsx") return [];
  const body = text
    .split("\n")
    .map((line) => (COMMENT_LINE.test(line) ? "" : line))
    .join("\n");
  const problems: Problem[] = [];
  body.split("\n").forEach((line, i) => {
    if (SCRIPT_HEX.test(line)) {
      problems.push({ file, line: i + 1, reason: "a hex colour in a script: name a token and draw it with a class" });
    }
  });
  for (const match of body.matchAll(INLINE_STYLE)) {
    const style = match[1] ?? match[2] ?? match[3];
    if (PX.test(style)) {
      const line = body.slice(0, match.index).split("\n").length;
      problems.push({ file, line, reason: "a px length in an inline style: use a class" });
    }
  }
  return problems;
}

const KINDS = /\.(css|tsx?)$/;

function lintFile(name: string, text: string): Problem[] {
  return name.endsWith(".css") ? lintCss(name, text) : lintScript(name, text);
}

interface Target {
  name: string;
  path: string;
}

// With no arguments: the sheet, and every non-test script under web/src, named by their repository paths.
function defaultTargets(): Target[] {
  const scripts = readdirSync(join(repo, "web/src"), { encoding: "utf8", recursive: true })
    .filter((rel) => /\.tsx?$/.test(rel) && !/\.test\.tsx?$/.test(rel))
    .sort()
    .map((rel) => `web/src/${rel}`);
  return ["web/src/style.css", ...scripts].map((name) => ({ name, path: join(repo, name) }));
}

function cli(args: string[]): number {
  try {
    const targets: Target[] = args.length > 0 ? args.map((name) => ({ name, path: name })) : defaultTargets();
    const problems: Problem[] = [];
    for (const { name, path } of targets) {
      if (!KINDS.test(name)) throw new Error(`${name} is not a .css, .ts or .tsx file`);
      problems.push(...lintFile(name, readFileSync(path, "utf8")));
    }
    for (const p of problems) console.log(`${p.file}:${p.line}: ${p.reason}`);
    if (problems.length > 0) return 1;
    console.log(`lint-web: ${targets.length} files hold to the tokens`);
    return 0;
  } catch (error) {
    console.error(`lint-web: ${error instanceof Error ? error.message : String(error)}`);
    return 2;
  }
}

// The CLI runs when this file is node's entry script. The tests bundle this module into a file of their own, which is
// the entry there, so the entry's name is what tells the two apart.
if (process.argv[1] !== undefined && basename(process.argv[1]) === "lint-web.ts") {
  process.exitCode = cli(process.argv.slice(2));
}
