import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { appendFileSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { context } from "./rules";

// The repository root. This file is bundled to web/build/tokens/, three directories below it.
const repo = fileURLToPath(new URL("../../../", import.meta.url));
const tokens = JSON.parse(readFileSync(join(repo, "web/visual/system/tokens.json"), "utf8"));
const kotlinPath = "android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/theme/OrreryTokens.kt";

const read = (rel: string): string => readFileSync(join(repo, rel), "utf8");
const run = (args: string[]) =>
  spawnSync(process.execPath, [join(repo, "web/visual/system/gen-tokens.mjs"), ...args], { cwd: repo, encoding: "utf8" });

// The token a "{a.b}" reference names, read from tokens.json, and the last part of its name.
const refValue = (ref: string): any => ref.slice(1, -1).split(".").reduce((node, key) => node[key], tokens);
const refName = (ref: string): string => ref.slice(1, -1).split(".").slice(-1)[0];
// The Kotlin name of a token: in is a hard keyword, so it is backticked.
const kid = (name: string): string => (name === "in" ? "`in`" : name);

const kotlin = (): string => read(kotlinPath);
// The text between an object's braces; head is its opening, such as "object Ink {".
function bodyOf(source: string, head: string): string {
  const open = source.indexOf(head);
  assert.notEqual(open, -1, `${head} is missing`);
  let depth = 0;
  for (let i = open + head.length - 1; i < source.length; i++) {
    if (source[i] === "{") depth++;
    else if (source[i] === "}" && --depth === 0) return source.slice(open + head.length, i);
  }
  throw new Error(`${head} is not closed`);
}
// The lines of a flat object's body, trimmed and without comments.
const members = (body: string): string[] =>
  body.split("\n").map((line) => line.trim()).filter((line) => line !== "" && !line.startsWith("//"));
// The value of a named argument in a one-line constructor call.
const argument = (line: string, name: string): string | undefined => new RegExp(`\\b${name} = ([^,)]+)`).exec(line)?.[1];
// A #rrggbb colour as Color writes it.
const colour = (hex: string): string => `Color(0xFF${hex.slice(1).toUpperCase()})`;
// The #rrggbb a token's value names: a "{a.b}" reference is followed, through as many as it takes, to the colour it ends at.
const hexOf = (value: string): string => (value.startsWith("{") ? hexOf(refValue(value)) : value);
// The Color a solid colour member holds, written as Color writes it.
const colourOf = (line: string): string => {
  const m = /^val \w+: Color = (Color\(0xFF[0-9A-F]{6}\))$/.exec(line);
  assert.ok(m, `${line} is not a Color`);
  return m[1];
};
// The number a Dp member holds: 8.dp is 8, and (-8).dp is -8.
const dpOf = (line: string): number => {
  const m = /^val \w+: Dp = (\(-\d+\)|\d+)\.dp$/.exec(line);
  assert.ok(m, `${line} is not a Dp length`);
  return Number(m[1].replace(/[()]/g, ""));
};
// The number a Layout ...Ch member holds, which is an Int.
const chOf = (line: string): number => {
  const m = /^val \w+Ch: Int = (\d+)$/.exec(line);
  assert.ok(m, `${line} is not an Int`);
  return Number(m[1]);
};
const numeric = (a: number, b: number): number => a - b;

test("TokenGen: --check on the repository exits 0, so the four committed outputs equal what gen-tokens.mjs writes from tokens.json", () => {
  const result = run(["--check"]);
  assert.equal(result.status, 0, result.stderr);
});

test("TokenGen: --check on a copy with one output altered and another deleted exits non-zero and names each of them", () => {
  const copy = mkdtempSync(join(tmpdir(), "tokengen-"));
  try {
    mkdirSync(join(copy, "web/visual/system"), { recursive: true });
    copyFileSync(join(repo, "web/visual/system/tokens.json"), join(copy, "web/visual/system/tokens.json"));
    assert.equal(run(["--root", copy]).status, 0);
    appendFileSync(join(copy, "web/src/tokens.css"), "/* altered */\n");
    rmSync(join(copy, "web/src/tokens/rules.ts"));
    const result = run(["--root", copy, "--check"]);
    assert.notEqual(result.status, 0, "--check passed a copy with an altered output and a missing one");
    const named = [...result.stderr.matchAll(/^(\S+) differs from tokens\.json or is missing$/gm)].map((m) => m[1]);
    assert.deepEqual(named.sort(), ["web/src/tokens.css", "web/src/tokens/rules.ts"]);
  } finally {
    rmSync(copy, { recursive: true, force: true });
  }
});

// The meta.css checks on a stylesheet at rel: ink.1 is --o-ink-1, each role and series colour has -fill and -line tints,
// and each type is one font shorthand.
function assertFollowsMeta(rel: string): void {
  const css = new Map(
    [...read(rel).matchAll(/(--o-[a-z0-9-]+):\s*([^;]+);/g)].map((m): [string, string] => [m[1], m[2].trim()]),
  );
  assert.ok(css.has("--o-ink-1"), "--o-ink-1 is not defined");
  const percent = (alpha: number): string => `${Math.round(alpha * 100)}%`;
  for (const group of ["role", "series"]) {
    for (const key of Object.keys(tokens[group])) {
      const base = `--o-${group}-${key}`;
      assert.ok(css.has(base), `${base} is not defined`);
      assert.equal(css.get(`${base}-fill`), `color-mix(in srgb, var(${base}) ${percent(tokens.alpha.tintFill)}, transparent)`);
      assert.equal(css.get(`${base}-line`), `color-mix(in srgb, var(${base}) ${percent(tokens.alpha.tintLine)}, transparent)`);
    }
  }
  for (const [name, t] of Object.entries(tokens.type) as [string, any][]) {
    assert.equal(css.get(`--o-type-${name}`), `${refValue(t.weight)} ${t.size}px/${t.line}px var(--o-font-${t.face})`);
  }
}

test("TokenGen: web/src/tokens.css follows meta.css: ink.1 is --o-ink-1, each role and series colour has -fill and -line tints, and --o-type-body is one font shorthand", () => {
  assertFollowsMeta("web/src/tokens.css");
});

test("TokenGen: web/visual/system/tokens.css follows meta.css: ink.1 is --o-ink-1, each role and series colour has -fill and -line tints, and each type is one font shorthand", () => {
  assertFollowsMeta("web/visual/system/tokens.css");
});

test("TokenGen: OrreryTokens has one nested object per top-level group of tokens.json, named for it, and no other at its top level", () => {
  const groups = Object.keys(tokens).filter((key) => key !== "meta").map((key) => key[0].toUpperCase() + key.slice(1));
  const nested = [...kotlin().matchAll(/^ {4}object (\w+) \{$/gm)].map((m) => m[1]);
  assert.deepEqual(nested.sort(), groups.sort());
});

test("TokenGen: OrreryTokens.Ink holds ink.1 as one, every ink as a Color, and no other member", () => {
  const ink = members(bodyOf(kotlin(), "object Ink {"));
  assert.equal(ink.length, Object.keys(tokens.ink).length);
  assert.ok(ink.includes(`val one: Color = ${colour(tokens.palette[refName(tokens.ink["1"])])}`));
  for (const line of ink) assert.match(line, /^val \w+: Color = Color\(0xFF[0-9A-F]{6}\)$/);
});

test("TokenGen: every colour in OrreryTokens is a Color: Palette, Surface, Line, State, Tone, Level, Prose, Syntax and the solid Role and Series colours equal tokens.json's after its references", () => {
  const kt = kotlin();
  for (const [objectName, group] of [
    ["Palette", "palette"],
    ["Surface", "surface"],
    ["Line", "line"],
    ["State", "state"],
    ["Tone", "tone"],
    ["Level", "level"],
    ["Prose", "prose"],
    ["Syntax", "syntax"],
    ["Role", "role"],
    ["Series", "series"],
  ]) {
    const body = members(bodyOf(kt, `object ${objectName} {`));
    // Role and Series also hold a Fill and a Line for each colour; only their solid colours are checked here.
    const solid = group === "role" || group === "series" ? body.filter((line) => !/(Fill|Line): Color/.test(line)) : body;
    assert.equal(solid.length, Object.keys(tokens[group]).length, `${objectName} does not hold one Color per ${group} token`);
    assert.deepEqual(
      solid.map(colourOf).sort(),
      (Object.values(tokens[group]) as string[]).map((value) => colour(hexOf(value))).sort(),
      `${objectName}'s colours are not tokens.json's ${group} after their references`,
    );
  }
});

test("TokenGen: lengths are Dp: OrreryTokens.Space holds space.24 as twentyFour, and each space is a .dp equal to tokens.json's", () => {
  const space = members(bodyOf(kotlin(), "object Space {"));
  assert.ok(space.includes(`val twentyFour: Dp = ${tokens.space["24"]}.dp`));
  const values = space.map((line) => {
    const m = /^val \w+: Dp = (-?\d+)\.dp$/.exec(line);
    assert.ok(m, `${line} is not a Dp length`);
    return Number(m[1]);
  });
  assert.deepEqual(values.sort((a, b) => a - b), (Object.values(tokens.space) as number[]).sort((a, b) => a - b));
});

test("TokenGen: every length in OrreryTokens is Dp: Radius, Size, Layout and Elevation's x, y and blur are .dp equal to tokens.json's, a Layout ...Ch is an Int, and each Z is a Float", () => {
  const kt = kotlin();
  for (const [objectName, group] of [["Radius", "radius"], ["Size", "size"]]) {
    const lengths = members(bodyOf(kt, `object ${objectName} {`)).map(dpOf);
    assert.deepEqual(lengths.sort(numeric), (Object.values(tokens[group]) as number[]).sort(numeric), `${objectName} is not tokens.json's ${group}`);
  }
  const layout = members(bodyOf(kt, "object Layout {"));
  const isCh = (line: string): boolean => /^val \w+Ch: /.test(line);
  const layoutTokens = Object.entries(tokens.layout) as [string, number][];
  assert.deepEqual(
    layout.filter(isCh).map(chOf).sort(numeric),
    layoutTokens.filter(([key]) => key.endsWith("Ch")).map(([, value]) => value).sort(numeric),
    "Layout's ...Ch members are not tokens.json's",
  );
  assert.deepEqual(
    layout.filter((line) => !isCh(line)).map(dpOf).sort(numeric),
    layoutTokens.filter(([key]) => !key.endsWith("Ch")).map(([, value]) => value).sort(numeric),
    "Layout's lengths are not tokens.json's",
  );
  const elevation = bodyOf(kt, "object Elevation {");
  for (const [key, e] of Object.entries(tokens.elevation) as [string, any][]) {
    const lines = members(bodyOf(elevation, `object ${key[0].toUpperCase() + key.slice(1)} {`));
    for (const field of ["x", "y", "blur"]) {
      const line = lines.find((l) => l.startsWith(`val ${field}: `));
      assert.ok(line, `${key} has no ${field}`);
      assert.equal(dpOf(line), e[field], `${key}.${field} is not tokens.json's`);
    }
  }
  const z = members(bodyOf(kt, "object Z {")).map((line) => {
    const m = /^val \w+: Float = (-?\d+(?:\.\d+)?)f$/.exec(line);
    assert.ok(m, `${line} is not a Float`);
    return Number(m[1]);
  });
  assert.deepEqual(z.sort(numeric), (Object.values(tokens.z) as number[]).sort(numeric), "Z is not tokens.json's z");
});

test("TokenGen: each type is a TextStyle in its face, OrreryInter or OrreryMono, with its size and line height in sp, weight and tracking, and its Caps Boolean beside it", () => {
  const lines = members(bodyOf(kotlin(), "object Type {"));
  for (const [name, t] of Object.entries(tokens.type) as [string, any][]) {
    const style = lines.find((line) => line.startsWith(`val ${name}: TextStyle = TextStyle(`));
    assert.ok(style, `${name} is not a TextStyle`);
    assert.equal(argument(style, "fontFamily"), t.face === "inter" ? "OrreryInter" : "OrreryMono");
    assert.equal(argument(style, "fontWeight"), `Font.Weight.${refName(t.weight)}`);
    assert.equal(argument(style, "fontSize"), `${t.size}.sp`);
    assert.equal(argument(style, "lineHeight"), `${t.line}.sp`);
    assert.equal(argument(style, "letterSpacing"), `${t.tracking}f.em`);
    assert.ok(lines.includes(`const val ${name}Caps: Boolean = ${t.caps}`), `${name} has no Caps Boolean beside it`);
  }
});

test("TokenGen: each move is a Timing of its duration in millis and an Easing from the ease points, and each loop is millis", () => {
  const motion = bodyOf(kotlin(), "object Motion {");
  const eases = members(bodyOf(motion, "object Ease {"));
  for (const [name, points] of Object.entries(tokens.motion.ease) as [string, number[]][]) {
    assert.ok(
      eases.includes(`val ${kid(name)}: Easing = CubicBezierEasing(${points.map((p: number) => `${p}f`).join(", ")})`),
      `${name} is not a CubicBezierEasing of its points`,
    );
  }
  const moves = members(bodyOf(motion, "object Move {"));
  for (const [name, move] of Object.entries(tokens.motion.move) as [string, any][]) {
    assert.ok(moves.includes(`val ${name}: Timing = Timing(${move.ms}, Ease.${kid(refName(move.ease))})`), `${name} is not a Timing`);
  }
  const loops = members(bodyOf(motion, "object Loop {"));
  for (const [name, loop] of Object.entries(tokens.motion.loop) as [string, any][]) {
    assert.ok(loops.includes(`val ${name}: Int = ${loop.ms}`), `${name} is not a duration in millis`);
  }
});

test("TokenGen: each motion.delay is an Int of its millis in Motion.Delay and a --o-delay-<name> of its ms in web/src/tokens.css, 0ms in the reduced-motion block; each motion.distance is a Dp in Motion.Distance and a --o-distance-<name> in px", () => {
  const motion = bodyOf(kotlin(), "object Motion {");
  const css = read("web/src/tokens.css");
  const reducedAt = css.indexOf("@media (prefers-reduced-motion: reduce)");
  assert.notEqual(reducedAt, -1, "web/src/tokens.css has no reduced-motion block");
  const normal = css.slice(0, reducedAt);
  const reduced = css.slice(reducedAt);
  const delays = members(bodyOf(motion, "object Delay {"));
  for (const [name, ms] of Object.entries(tokens.motion.delay) as [string, number][]) {
    assert.ok(delays.includes(`val ${name}: Int = ${ms}`), `${name} is not an Int of ${ms} in Motion.Delay`);
    assert.ok(normal.includes(`--o-delay-${name}: ${ms}ms;`), `--o-delay-${name} is not ${ms}ms`);
    assert.ok(reduced.includes(`--o-delay-${name}: 0ms;`), `--o-delay-${name} is not 0ms under reduced motion`);
  }
  const distances = members(bodyOf(motion, "object Distance {"));
  for (const [name, px] of Object.entries(tokens.motion.distance) as [string, number][]) {
    assert.ok(distances.includes(`val ${name}: Dp = ${px}.dp`), `${name} is not a Dp of ${px} in Motion.Distance`);
    assert.ok(css.includes(`--o-distance-${name}: ${px}px;`), `--o-distance-${name} is not ${px}px`);
  }
});

test("TokenGen: each role and series colour has a Fill at alpha.tintFill and a Line at alpha.tintLine, and Alpha holds both", () => {
  const kt = kotlin();
  const alpha = members(bodyOf(kt, "object Alpha {"));
  assert.ok(alpha.includes(`val tintFill: Float = ${tokens.alpha.tintFill}f`));
  assert.ok(alpha.includes(`val tintLine: Float = ${tokens.alpha.tintLine}f`));
  for (const [objectName, group] of [["Role", "role"], ["Series", "series"]]) {
    const body = members(bodyOf(kt, `object ${objectName} {`));
    const solid = body.filter((line) => !/(Fill|Line): Color/.test(line));
    const fills = body.filter((line) => /Fill: Color/.test(line));
    const outlines = body.filter((line) => /Line: Color/.test(line));
    assert.equal(solid.length, Object.keys(tokens[group]).length, `${objectName} holds a colour without its tints`);
    assert.equal(fills.length, solid.length, `${objectName} has a colour without a Fill`);
    assert.equal(outlines.length, solid.length, `${objectName} has a colour without a Line`);
    for (const line of fills) assert.match(line, /^val (\w+)Fill: Color = \1\.copy\(alpha = Alpha\.tintFill\)$/);
    for (const line of outlines) assert.match(line, /^val (\w+)Line: Color = \1\.copy\(alpha = Alpha\.tintLine\)$/);
  }
});

test("TokenGen: rule.context's limits and blocks are the same in rules.ts and in OrreryTokens.Rule, and equal tokens.json's", () => {
  const { limits, blocks } = tokens.rule.context;
  assert.deepEqual([...context.limits], limits);
  assert.equal(context.blocks, blocks);
  const rule = members(bodyOf(bodyOf(kotlin(), "object Rule {"), "object Context {"));
  assert.ok(rule.includes(`val limits: List<Int> = listOf(${limits.join(", ")})`));
  assert.ok(rule.includes(`val blocks: Int = ${blocks}`));
});

test("TokenGen: a type whose face is neither inter nor mono stops the generator and names the type and its face", () => {
  const copy = mkdtempSync(join(tmpdir(), "tokengen-"));
  try {
    mkdirSync(join(copy, "web/visual/system"), { recursive: true });
    const altered = JSON.parse(read("web/visual/system/tokens.json"));
    altered.type.body.face = "serif";
    writeFileSync(join(copy, "web/visual/system/tokens.json"), JSON.stringify(altered));
    const result = run(["--root", copy]);
    assert.notEqual(result.status, 0, "the generator wrote outputs for a face that is neither inter nor mono");
    assert.match(result.stderr, /type\.body's face must be inter or mono, got serif/);
  } finally {
    rmSync(copy, { recursive: true, force: true });
  }
});

// The custom properties of a generated stylesheet at rel, by name.
const cssVars = (rel: string): Map<string, string> =>
  new Map([...read(rel).matchAll(/(--o-[a-z0-9-]+):\s*([^;]+);/g)].map((m): [string, string] => [m[1], m[2].trim()]));
// The first family a font stack names, without its quotes: "Orrery Mono", ui-monospace names Orrery Mono.
const firstFamily = (stack: string): string => stack.split(",")[0].trim().replace(/^"(.*)"$/, "$1");
// The font resources a FontFamily val in Type.kt is built from, such as inter_regular.
function fontsOf(family: string): string[] {
  const m = new RegExp(`val ${family} = FontFamily\\(([\\s\\S]*?)\\n\\)`).exec(read("android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/theme/Type.kt"));
  assert.ok(m, `${family} is not a FontFamily in Type.kt`);
  return [...m[1].matchAll(/R\.font\.(\w+)/g)].map((x) => x[1]);
}
// The rules of style.css with comments removed: each one's selectors, and its declarations by property.
const styleRules = (): { selectors: string[]; declarations: Map<string, string> }[] =>
  [...read("web/src/style.css").replace(/\/\*[\s\S]*?\*\//g, "").matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, selectors = "", body = ""]) => ({
    selectors: selectors.split(",").map((s) => s.trim()),
    declarations: new Map([...body.matchAll(/([a-z-]+)\s*:\s*([^;]+);/g)].map((m): [string, string] => [m[1], m[2].trim()])),
  }));

test("TypeScale: web/src/tokens.css sets label 12/16, small 13/18, body 15/22, title 18/24, display 28/34 and code 13/18, with no tracking but the label's", () => {
  const css = cssVars("web/src/tokens.css");
  assert.equal(css.get("--o-type-label"), "700 12px/16px var(--o-font-mono)");
  assert.equal(css.get("--o-type-small"), "400 13px/18px var(--o-font-inter)");
  assert.equal(css.get("--o-type-body"), "400 15px/22px var(--o-font-inter)");
  assert.equal(css.get("--o-type-title"), "700 18px/24px var(--o-font-inter)");
  assert.equal(css.get("--o-type-display"), "700 28px/34px var(--o-font-inter)");
  assert.equal(css.get("--o-type-code"), "400 13px/18px var(--o-font-mono)");
  assert.deepEqual([...css.keys()].filter((name) => name.endsWith("-tracking")), ["--o-type-label-tracking"]);
});

test("TypeScale: OrreryTokens.Type sets label 12/16, small 13/18, body 15/22, title 18/24, display 28/34 and code 13/18 in sp, with no tracking but the label's, and code in OrreryMono", () => {
  const type = members(bodyOf(kotlin(), "object Type {"));
  const scale: [string, string, string, string, string][] = [
    ["label", "12", "16", "OrreryMono", "0.06f.em"],
    ["small", "13", "18", "OrreryInter", "0f.em"],
    ["body", "15", "22", "OrreryInter", "0f.em"],
    ["title", "18", "24", "OrreryInter", "0f.em"],
    ["display", "28", "34", "OrreryInter", "0f.em"],
    ["code", "13", "18", "OrreryMono", "0f.em"],
  ];
  for (const [name, size, line, family, tracking] of scale) {
    const style = type.find((l) => l.startsWith(`val ${name}: TextStyle = TextStyle(`));
    assert.ok(style, `${name} is not a TextStyle`);
    assert.equal(argument(style, "fontSize"), `${size}.sp`);
    assert.equal(argument(style, "lineHeight"), `${line}.sp`);
    assert.equal(argument(style, "fontFamily"), family);
    assert.equal(argument(style, "letterSpacing"), tracking);
  }
});

test("TypeScale: layout meterCell is 160 and meterCellLine 136, in web/src/tokens.css and in OrreryTokens.Layout", () => {
  const css = cssVars("web/src/tokens.css");
  assert.equal(css.get("--o-layout-meter-cell"), "160px");
  assert.equal(css.get("--o-layout-meter-cell-line"), "136px");
  const layout = members(bodyOf(kotlin(), "object Layout {"));
  assert.ok(layout.includes("val meterCell: Dp = 160.dp"));
  assert.ok(layout.includes("val meterCellLine: Dp = 136.dp"));
});

test("TypeScale: the read measure is 70ch, and .reader .reply, .epic-page .summary-text, .dline, .contract .intent and .contract .approach take it as max-width", () => {
  assert.equal(cssVars("web/src/tokens.css").get("--o-layout-read-measure-ch"), "70ch");
  const rules = styleRules();
  for (const selector of [".reader .reply", ".epic-page .summary-text", ".dline", ".contract .intent", ".contract .approach"]) {
    const widths = rules
      .filter((rule) => rule.selectors.includes(selector) && rule.declarations.has("max-width"))
      .map((rule) => rule.declarations.get("max-width"));
    assert.ok(widths.length > 0, `${selector} sets no max-width`);
    for (const width of widths) assert.equal(width, "var(--o-layout-read-measure-ch)", `${selector} does not take the read measure`);
  }
});

test("TypeFaces: web/src/tokens.css resolves small, body, title and display through their var to the Inter family, and label and code to Orrery Mono", () => {
  const css = cssVars("web/src/tokens.css");
  assert.equal(css.get("--o-font-inter"), '"Inter", system-ui, sans-serif');
  for (const [level, family] of [["small", "Inter"], ["body", "Inter"], ["title", "Inter"], ["display", "Inter"], ["label", "Orrery Mono"], ["code", "Orrery Mono"]]) {
    const face = /var\((--o-font-[a-z]+)\)$/.exec(css.get(`--o-type-${level}`) ?? "");
    assert.ok(face, `--o-type-${level} names no font family`);
    assert.equal(firstFamily(css.get(face[1]) ?? ""), family, `${level} does not resolve to ${family}`);
  }
});

test("TypeFaces: OrreryTokens.Type gives small, body, title and display OrreryInter and label and code OrreryMono, and Type.kt builds each family from its own files", () => {
  const type = members(bodyOf(kotlin(), "object Type {"));
  for (const [level, family, prefix] of [
    ["small", "OrreryInter", "inter_"],
    ["body", "OrreryInter", "inter_"],
    ["title", "OrreryInter", "inter_"],
    ["display", "OrreryInter", "inter_"],
    ["label", "OrreryMono", "orrery_mono_"],
    ["code", "OrreryMono", "orrery_mono_"],
  ]) {
    const style = type.find((l) => l.startsWith(`val ${level}: TextStyle = TextStyle(`));
    assert.ok(style, `${level} is not a TextStyle`);
    assert.equal(argument(style, "fontFamily"), family, `${level} is not in ${family}`);
    const files = fontsOf(family);
    assert.ok(files.length > 0 && files.every((f) => f.startsWith(prefix)), `${family} is not built from ${prefix} files`);
  }
});
