import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

// The bundled test runs from web/build, so these resolve to web/src/style.css and the android font directory.
const styleUrl = new URL("../src/style.css", import.meta.url);
const fontDir = new URL("../../android/app/src/main/res/font/", import.meta.url);

type FontFace = { family: string; weight: string; style: string; display: string; url: string };

// fontFaces is every @font-face rule in style.css, comments removed, with the declarations the test reads.
function fontFaces(): FontFace[] {
  const css = readFileSync(styleUrl, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  return [...css.matchAll(/@font-face\s*\{([^}]*)\}/g)].map(([, body = ""]) => {
    const decls = new Map<string, string>();
    for (const decl of body.split(";")) {
      const colon = decl.indexOf(":");
      if (colon >= 0) decls.set(decl.slice(0, colon).trim(), decl.slice(colon + 1).trim());
    }
    return {
      family: (decls.get("font-family") ?? "").replace(/^"(.*)"$/, "$1"),
      weight: decls.get("font-weight") ?? "",
      style: decls.get("font-style") ?? "",
      display: decls.get("font-display") ?? "",
      url: (decls.get("src") ?? "").match(/url\("([^"]+)"\)/)?.[1] ?? "",
    };
  });
}

// assertFace asserts style.css has the family's face at weight and style, with font-display swap, loading file from the android font directory.
function assertFace(family: string, weight: string, style: string, file: string): void {
  const face = fontFaces().find((f) => f.family === family && f.weight === weight && f.style === style);
  assert.ok(face, `style.css has a ${family} @font-face at ${weight} ${style}`);
  assert.equal(face.display, "swap", `${family} ${weight} ${style} uses font-display swap`);
  assert.equal(new URL(face.url, styleUrl).href, new URL(file, fontDir).href, `${family} ${weight} ${style} loads ${file}`);
  assert.ok(existsSync(fileURLToPath(new URL(file, fontDir))), `${file} exists in android/app/src/main/res/font`);
}

test("TypeFaces: style.css declares Inter at 400 normal, 700 normal and 400 italic, each loading its inter_* file from android/app/src/main/res/font", () => {
  assertFace("Inter", "400", "normal", "inter_regular.ttf");
  assertFace("Inter", "700", "normal", "inter_bold.ttf");
  assertFace("Inter", "400", "italic", "inter_italic.ttf");
});

test("TypeFaces: style.css declares Orrery Mono at 400 normal, 700 normal and 400 italic, each loading its orrery_mono_* file from android/app/src/main/res/font", () => {
  assertFace("Orrery Mono", "400", "normal", "orrery_mono_regular.ttf");
  assertFace("Orrery Mono", "700", "normal", "orrery_mono_bold.ttf");
  assertFace("Orrery Mono", "400", "italic", "orrery_mono_italic.ttf");
});

const systemUrl = new URL("../visual/system/SYSTEM.md", import.meta.url);

// codeLevel is the font code text takes: the --o-type-code token, Orrery Mono at 13/18.
const codeLevel = "var(--o-type-code)";

// styleRules is every rule of style.css with comments removed: its selectors, and its declarations by property.
function styleRules(): { selectors: string[]; declarations: Map<string, string> }[] {
  const css = readFileSync(styleUrl, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  return [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, selectors = "", body = ""]) => {
    const declarations = new Map<string, string>();
    for (const decl of body.split(";")) {
      const colon = decl.indexOf(":");
      if (colon >= 0) declarations.set(decl.slice(0, colon).trim(), decl.slice(colon + 1).trim());
    }
    return { selectors: selectors.split(",").map((s) => s.trim().replace(/\s+/g, " ")), declarations };
  });
}

// fontsOf is the font each rule of style.css that names the selector exactly declares, in source order.
function fontsOf(selector: string): string[] {
  const fonts: string[] = [];
  for (const rule of styleRules()) {
    const font = rule.declarations.get("font");
    if (font !== undefined && rule.selectors.includes(selector)) fonts.push(font);
  }
  return fonts;
}

// assertCodeLevel asserts the selector sets its font, and every rule that sets it sets the code level.
function assertCodeLevel(selector: string): void {
  const fonts = fontsOf(selector);
  assert.ok(fonts.length > 0, `${selector} sets its font`);
  for (const font of fonts) {
    assert.equal(font, codeLevel, `${selector} takes the code level`);
  }
}

test("TypeFaces: a reply's code block and inline code take the code level", () => {
  assertCodeLevel(".reader .reply pre");
  assertCodeLevel(".reader .reply code");
});

test("TypeFaces: a block's code block and inline code take the code level", () => {
  assertCodeLevel('.reader [data-shape="block"] pre');
  assertCodeLevel('.reader [data-shape="block"] code');
});

test("TypeFaces: the Ask's command takes the code level, and the code in it inherits that", () => {
  assertCodeLevel(".ask .cmd pre");
  assert.deepEqual(fontsOf(".ask .cmd code"), ["inherit"]);
});

test("TypeFaces: a tool line's call input takes the code level", () => {
  assertCodeLevel(".reader .call .in");
});

test("TypeFaces: a table row's id takes the code level in every rule that sets it", () => {
  assertCodeLevel(".tr .id");
});

test("TypeFaces: the reader's gutter time takes the code level", () => {
  assertCodeLevel(".reader .t > .gut");
});

// typeSection is section 2, Type, of SYSTEM.md: from its heading up to section 3.
function typeSection(): string {
  const doc = readFileSync(systemUrl, "utf8");
  const start = doc.indexOf("## 2. Type");
  const end = doc.indexOf("## 3. Space", start + 1);
  assert.ok(start >= 0 && end > start, "SYSTEM.md has section 2, Type, ahead of section 3");
  return doc.slice(start, end);
}

// typeRows is each row of the type table keyed by its token, with the cells after the token: the size, the weight and the for-column.
function typeRows(): Map<string, string[]> {
  const rows = new Map<string, string[]>();
  for (const line of typeSection().split("\n")) {
    const cells = line.split("|").map((cell) => cell.trim());
    const token = /^`type\.(\w+)`$/.exec(cells[1] ?? "");
    if (token) rows.set(token[1], cells.slice(2, -1));
  }
  return rows;
}

test("TypeFaces: SYSTEM.md's type section names two faces, Inter for reading text and Orrery Mono for the label and code levels", () => {
  const section = typeSection();
  assert.doesNotMatch(section, /One face/, "the section no longer names one face");
  assert.match(section, /Two faces\. \*\*Inter\*\* sets the reading text: `small`, `body`, `title` and `display`\./);
  assert.match(section, /\*\*Orrery Mono\*\* \(Maple Mono\) sets the label and the code level: `label` and `code`\./);
});

test("TypeFaces: SYSTEM.md's type table sets label 12 / 16, small 13 / 18, code 13 / 18 mono, body 15 / 22, title 18 / 24 and display 28 / 34", () => {
  const rows = typeRows();
  const sizes: [string, string][] = [
    ["label", "12 / 16"],
    ["small", "13 / 18"],
    ["code", "13 / 18 mono"],
    ["body", "15 / 22"],
    ["title", "18 / 24"],
    ["display", "28 / 34"],
  ];
  for (const [token, size] of sizes) {
    assert.equal(rows.get(token)?.[0], size, `type.${token} is ${size}`);
  }
});

test("TypeFaces: SYSTEM.md's code row is for code blocks, inline code, a command, a tool line, an id and a gutter time", () => {
  assert.equal(typeRows().get("code")?.[2], "Code blocks, inline code, a command, a tool line, an id and a gutter time.");
});
