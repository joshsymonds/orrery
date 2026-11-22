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
