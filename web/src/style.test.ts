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

// beforeWide returns the stylesheet above the 760px block: the rules that a page under 760px draws.
function beforeWide(text: string): string {
  const header = "@media (min-width: 760px) {";
  const start = text.indexOf(header);
  assert.notEqual(start, -1, `style.css has ${header}`);
  return text.slice(0, start);
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

test("from 760px the page fills the window and the list, reader and epic column scroll", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, "body"), /height:\s*100dvh/);
  assert.match(declarationsFor(wide, "body"), /overflow:\s*hidden/);
  assert.match(declarationsFor(wide, "#app"), /height:\s*100%/);
  assert.match(declarationsFor(wide, ".app"), /height:\s*100%/);
  assert.match(declarationsFor(wide, ".panes"), /align-items:\s*stretch/);
  // The list does not scroll itself: its rows sit between its header and its footer and scroll there.
  assert.match(declarationsFor(wide, ".list"), /grid-template-rows:\s*auto minmax\(0,\s*1fr\) auto;/);
  assert.doesNotMatch(declarationsFor(wide, ".list"), /overflow/);
  // .read > .reader is the scroll box of both the conversation and the pinned conversation.
  for (const selector of [".list > .rows", ".read > .reader", ".side"]) {
    assert.match(declarationsFor(wide, selector), /overflow-y:\s*auto/, `${selector} scrolls`);
    assert.match(declarationsFor(wide, selector), /min-height:\s*0(?![\d.])/, `${selector} can shrink`);
  }
});

test("from 760px the tracks are 440px, fluid and 380px with no gap, and a pin splits the fluid track", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  const panes = declarationsFor(wide, ".panes");
  assert.match(panes, /grid-template-columns:\s*440px minmax\(0,\s*1fr\) 380px;/);
  assert.match(panes, /(?<![-\w])gap:\s*0;/);
  assert.match(
    declarationsFor(wide, ".panes.split"),
    /grid-template-columns:\s*440px minmax\(0,\s*1fr\) minmax\(0,\s*1fr\);/,
  );
});

test("from 760px each column keeps its own track, and to 1100px the epic column takes the reader's", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  // One row for every column: the epic column placed before the pin's track must not wrap to a second row.
  for (const selector of [".panes > .list", ".panes > .read", ".panes > .side"]) {
    assert.match(declarationsFor(wide, selector), /grid-row:\s*1;/, `${selector} sits in the one row`);
  }
  assert.match(declarationsFor(wide, ".panes > .list"), /grid-column:\s*1;/);
  assert.match(declarationsFor(wide, ".panes > .read"), /grid-column:\s*2;/);
  assert.match(declarationsFor(wide, ".panes > .read.pinned"), /grid-column:\s*3;/);
  assert.match(declarationsFor(wide, ".panes > .side"), /grid-column:\s*3;/);
  const narrow = mediaBlock(css, "@media (min-width: 760px) and (max-width: 1100px) {");
  assert.match(declarationsFor(narrow, ".panes > .side"), /grid-column:\s*2;/);
});

test("from 760px a pinned epic column takes the reader's track, and a step hides it", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, ".panes.split .side"), /grid-column:\s*2;/);
  assert.match(declarationsFor(wide, ".panes.split.with-step .side"), /display:\s*none;/);
});

test("from 760px the body is base, the list is crust behind surface0, and the epic column is behind --rule", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, "body"), /background:\s*var\(--base\);/);
  assert.match(declarationsFor(wide, ".list"), /background:\s*var\(--crust\);/);
  assert.match(declarationsFor(wide, ".list"), /border-right:\s*1px solid var\(--surface0\);/);
  assert.match(declarationsFor(wide, ".side"), /border-left:\s*1px solid var\(--rule\);/);
  assert.match(declarationsFor(wide, ".read.pinned"), /border-left:\s*1px solid var\(--rule\);/);
});

test("from 760px the top, rows, reader and epic column keep their padding", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, ".top"), /padding:\s*12px 14px 10px;/);
  assert.match(declarationsFor(wide, ".list > .rows"), /padding:\s*0 6px 10px;/);
  assert.match(declarationsFor(wide, ".read"), /padding:\s*12px 28px 14px;/);
  assert.match(declarationsFor(wide, ".side"), /padding:\s*16px 20px 24px;/);
});

test("under 760px the body is crust and unpadded, and the columns stack behind the side gutter", () => {
  const phone = beforeWide(css);
  assert.match(declarationsFor(phone, "body"), /background:\s*var\(--crust\);/);
  assert.doesNotMatch(declarationsFor(css, "body"), /padding/);
  const panes = declarationsFor(phone, ".panes");
  assert.match(panes, /grid-template-columns:\s*minmax\(0,\s*1fr\);/);
  assert.match(panes, /gap:\s*16px;/);
  assert.match(panes, /padding-left:\s*max\(16px, env\(safe-area-inset-left, 0px\)\);/);
  assert.match(panes, /padding-right:\s*max\(16px, env\(safe-area-inset-right, 0px\)\);/);
});

test("from 760px to 1500px the list is 400px and the epic column 340px", () => {
  const block = mediaBlock(css, "@media (min-width: 760px) and (max-width: 1500px) {");
  assert.match(declarationsFor(block, ".panes"), /grid-template-columns:\s*400px minmax\(0,\s*1fr\) 340px;/);
  assert.match(
    declarationsFor(block, ".panes.split"),
    /grid-template-columns:\s*400px minmax\(0,\s*1fr\) minmax\(0,\s*1fr\);/,
  );
});

test("from 760px to 1100px the list is 380px in two tracks, and a step hides the epic column", () => {
  const block = mediaBlock(css, "@media (min-width: 760px) and (max-width: 1100px) {");
  assert.match(declarationsFor(block, ".panes"), /grid-template-columns:\s*380px minmax\(0,\s*1fr\);/);
  assert.match(
    declarationsFor(block, ".panes.split"),
    /grid-template-columns:\s*380px minmax\(0,\s*1fr\) minmax\(0,\s*1fr\);/,
  );
  assert.match(declarationsFor(block, ".panes.with-step .side"), /display:\s*none;/);
});

test("from 1700px a pinned frame has four tracks, with the epic column in the fourth", () => {
  const block = mediaBlock(css, "@media (min-width: 1700px) {");
  assert.match(
    declarationsFor(block, ".panes.split"),
    /grid-template-columns:\s*440px minmax\(0,\s*1fr\) minmax\(0,\s*1fr\) 380px;/,
  );
  // The shown epic column takes the fourth track; a hidden one keeps display none.
  const side = declarationsFor(block, ".panes.split .side:not([hidden])");
  assert.match(side, /grid-column:\s*4;/);
  assert.match(side, /display:\s*block;/);
});

test("the brand row sets a 22px mark and 11.5px counts, and new counts are lavender", () => {
  assert.match(declarationsFor(css, ".brand"), /display:\s*flex;/);
  assert.match(declarationsFor(css, ".brand .mark"), /width:\s*22px;/);
  assert.match(declarationsFor(css, ".brand .mark"), /height:\s*22px;/);
  assert.match(declarationsFor(css, ".brand .counts"), /font:\s*400 11\.5px\/22px var\(--mono\);/);
  assert.match(declarationsFor(css, ".brand .counts"), /margin-left:\s*auto;/);
  assert.match(declarationsFor(css, ".st-new"), /color:\s*var\(--lavender\);/);
});

test("the wireframe's --rule token is defined", () => {
  assert.match(declarationsFor(css, ":root"), /--rule:\s*#2a2b3c;/);
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
