import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const source = readFileSync(new URL("../src/style.css", import.meta.url), "utf8");
// Comments may name the faces the rules must not use, so the rule checks read the stylesheet without them.
const css = source.replace(/\/\*[\s\S]*?\*\//g, "");

// rulesIn splits a stylesheet of flat rules (no nested braces) into each rule's selectors and declarations.
function rulesIn(text: string): { selectors: string[]; declarations: string }[] {
  return [...text.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((match) => ({
    selectors: match[1].split(",").map((selector) => selector.trim()),
    declarations: match[2],
  }));
}

// declarationsFor joins the declarations of every flat rule whose selector list names the selector.
function declarationsFor(text: string, selector: string): string {
  return rulesIn(text)
    .filter((rule) => rule.selectors.includes(selector))
    .map((rule) => rule.declarations)
    .join(";");
}

// mediaBlock returns the body of the first @media block with the given header, matching its braces.
function mediaBlock(text: string, header: string): string {
  const start = text.indexOf(header);
  assert.notEqual(start, -1, `style.css has ${header}`);
  const open = start + header.length - 1;
  let depth = 0;
  for (let index = open; index < text.length; index += 1) {
    if (text[index] === "{") depth += 1;
    if (text[index] === "}") depth -= 1;
    if (depth === 0) return text.slice(open + 1, index);
  }
  assert.fail(`${header} is closed`);
}

test("style.css names no sans-serif, system-ui or --reply face", () => {
  assert.doesNotMatch(source, /sans-serif|system-ui|--reply/);
});

test("every font declaration names the mono variable or inherits", () => {
  const faces = css.replace(/@font-face \{[^}]*\}/g, "");
  const values = [...faces.matchAll(/(?<![\w-])font(?:-family)?:\s*([^;}]+)/g)].map((match) => match[1].trim());
  assert.ok(values.length > 0, "style.css declares fonts");
  for (const value of values) {
    assert.match(value, /^inherit$|var\(--mono\)/, `font declaration "${value}"`);
  }
});

test("form controls take the mono family", () => {
  for (const tag of ["button", "input", "textarea", "select"]) {
    assert.ok(
      rulesIn(css).some((rule) => rule.selectors.includes(tag) && /font-family:\s*var\(--mono\)/.test(rule.declarations)),
      `${tag} has the mono family`,
    );
  }
});

test("the html element takes the mono family", () => {
  assert.match(declarationsFor(css, "html"), /font-family:\s*var\(--mono\)/);
});

test("from 760px the page fills the window and four columns scroll", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, "body"), /height:\s*100dvh/);
  assert.match(declarationsFor(wide, "body"), /overflow:\s*hidden/);
  assert.match(declarationsFor(wide, "#app"), /height:\s*100%/);
  assert.match(declarationsFor(wide, ".app"), /height:\s*100%/);
  assert.match(declarationsFor(wide, ".panes"), /align-items:\s*stretch/);
  // .read > .reader is the scroll box of both the conversation and the pinned conversation.
  for (const selector of [".list", ".read > .reader", ".side"]) {
    assert.match(declarationsFor(wide, selector), /overflow-y:\s*auto/, `${selector} scrolls`);
  }
});

test("the 1200px and 68ch caps are gone and reply text fills the reader", () => {
  assert.doesNotMatch(css, /max-width:\s*1200px|68ch/);
  assert.match(declarationsFor(css, ".reader .t"), /minmax\(0,\s*1fr\)/);
  assert.match(declarationsFor(css, ".reader .queued"), /minmax\(0,\s*1fr\)/);
});

test("reading text is 14px with a 1.6 line height", () => {
  assert.match(declarationsFor(css, ".reader .t > .body"), /font:\s*14px\/1\.6 var\(--mono\)/);
  const queued = declarationsFor(css, ".reader .queued > .body");
  assert.match(queued, /font-size:\s*14px/);
  assert.match(queued, /line-height:\s*1\.6(?![\d.])/);
});

test("plain texts keep their line breaks", () => {
  const selectors = [
    ".reader .compaction .summary",
    ".reader .question",
    ".reader .question-card .question",
    ".reader .notice",
    ".reader .queued > .body",
  ];
  for (const selector of selectors) {
    assert.match(declarationsFor(css, selector), /white-space:\s*pre-(wrap|line)/, selector);
  }
});
