import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// styleDeclaration is the value of property in style.css's rule for selector, with comments removed, or undefined when
// that rule does not set it. The rule is the one that opens a line with selector; a test fails, named, when there is none.
// The sheet is read from the repository root, where every check runs, since a test bundled elsewhere has no web/ beside it.
export function styleDeclaration(selector: string, property: string): string | undefined {
  const css = readFileSync(join(process.cwd(), "web/src/style.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
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
