import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// styleDeclaration is the value of property in style.css's rule for selector, with comments removed, or undefined when
// that rule does not set it. The rule is the one that opens a line with selector; a test fails, named, when there is none.
export function styleDeclaration(selector: string, property: string): string | undefined {
  const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const rule = css.match(new RegExp(`^${escaped} \\{([^}]*)\\}`, "m"));
  assert.ok(rule, `style.css has a ${selector} rule`);
  return rule[1]
    .split(";")
    .map((declaration) => declaration.trim())
    .find((declaration) => declaration.startsWith(`${property}:`))
    ?.slice(property.length + 1)
    .trim();
}
