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

test("from 760px the top, rows, head, conversation, foot and epic column keep their padding", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, ".top"), /padding:\s*12px 14px 10px;/);
  assert.match(declarationsFor(wide, ".list > .rows"), /padding:\s*0 6px 10px;/);
  assert.match(declarationsFor(wide, ".head"), /padding:\s*12px 28px 11px;/);
  assert.match(declarationsFor(wide, ".read > .reader"), /padding:\s*18px 28px 10px;/);
  assert.match(declarationsFor(wide, ".send"), /padding:\s*0 28px 14px;/);
  assert.match(declarationsFor(wide, ".side"), /padding:\s*16px 20px 24px;/);
});

test("from 760px the reader is a head, its conversation and a foot, and only the conversation scrolls", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  const read = declarationsFor(wide, ".read");
  assert.match(read, /(?<![-\w])display:\s*grid;/);
  assert.match(read, /grid-template-rows:\s*auto minmax\(0,\s*1fr\) auto;/);
  assert.match(read, /(?<![-\w])gap:\s*0;/);
  assert.doesNotMatch(read, /padding/);
  assert.doesNotMatch(declarationsFor(wide, ".head"), /overflow/);
  assert.doesNotMatch(declarationsFor(wide, ".send"), /overflow/);
  assert.match(declarationsFor(wide, ".read > .reader"), /overflow-y:\s*auto;/);
});

test("the head sets the label at 17px bold and the where line at 12px in overlay1", () => {
  const head = declarationsFor(css, ".head");
  assert.match(head, /display:\s*flex;/);
  assert.match(head, /border-bottom:\s*1px solid var\(--rule\);/);
  assert.match(declarationsFor(css, ".head h1"), /margin:\s*0;/);
  assert.match(declarationsFor(css, ".head h1"), /font:\s*700 17px\/24px var\(--mono\);/);
  assert.match(declarationsFor(css, ".head .where"), /font:\s*12px\/24px var\(--mono\);/);
  assert.match(declarationsFor(css, ".head .where"), /color:\s*var\(--overlay1\);/);
});

test("the pill is a tone's 10% tint with a 30% ring, the glyph is 11px, and each tone sets its colour", () => {
  const pill = declarationsFor(css, ".pill");
  assert.match(pill, /border-radius:\s*99px;/);
  assert.match(pill, /background:\s*color-mix\(in srgb, var\(--tone\) 10%, transparent\);/);
  assert.match(pill, /box-shadow:\s*0 0 0 1px color-mix\(in srgb, var\(--tone\) 30%, transparent\);/);
  assert.match(declarationsFor(css, ".pill .g"), /width:\s*11px;/);
  assert.match(declarationsFor(css, ".pill .g"), /height:\s*11px;/);
  assert.match(declarationsFor(css, ".pill .tool"), /font-weight:\s*400;/);
  assert.match(declarationsFor(css, ".pill .tool"), /color:\s*var\(--blue\);/);
  const tones: [string, string][] = [
    ["working", "yellow"],
    ["waiting", "peach"],
    ["held", "peach"],
    ["done", "green"],
    ["interrupted", "maroon"],
    ["ended", "red"],
  ];
  for (const [tone, colour] of tones) {
    assert.match(
      declarationsFor(css, `.pill.tone-${tone}`),
      new RegExp(`--tone:\\s*var\\(--${colour}\\);`),
      `tone-${tone} is ${colour}`,
    );
  }
  const idle = declarationsFor(css, ".pill.tone-idle");
  assert.match(idle, /color:\s*var\(--overlay1\);/);
  assert.match(idle, /background:\s*var\(--surface0\);/);
  assert.match(idle, /box-shadow:\s*0 0 0 1px color-mix\(in srgb, var\(--overlay1\) 30%, transparent\);/);
});

test("the pill's working glyph turns, and reduced motion holds it still", () => {
  assert.match(declarationsFor(css, ".pill .g-working"), /animation:\s*rot 1\.1s linear infinite;/);
  assert.match(css, /@keyframes rot\s*\{/);
  const reduced = mediaBlock(css, "@media (prefers-reduced-motion: reduce) {");
  assert.match(declarationsFor(reduced, ".pill .g-working"), /animation:\s*none;/);
});

test("the send foot's how line is 11.5px over overlay0", () => {
  assert.match(declarationsFor(css, ".send .how"), /font:\s*11\.5px\/16px var\(--mono\);/);
  assert.match(declarationsFor(css, ".send .how"), /color:\s*var\(--overlay0\);/);
});

test("the composer's send button is a 24px mauve square with crust text", () => {
  const go = declarationsFor(css, ".composer .go");
  assert.match(go, /(?<![-\w])width:\s*24px;/);
  assert.match(go, /(?<![-\w])height:\s*24px;/);
  assert.match(go, /background:\s*var\(--mauve\);/);
  assert.match(go, /color:\s*var\(--crust\);/);
});

test("the queue docks onto the composer: its top corners and the composer's bottom corners are 10px", () => {
  assert.match(declarationsFor(css, ".compose .queue"), /border-radius:\s*10px 10px 0 0;/);
  assert.match(declarationsFor(css, ".compose .queue + .composer"), /border-radius:\s*0 0 10px 10px;/);
});

test("queued text is dim, and a canceled one is struck through with a maroon tag", () => {
  assert.match(declarationsFor(css, ".compose .queued > .text"), /color:\s*var\(--subtext0\);/);
  assert.match(declarationsFor(css, ".compose .queued.canceled > .text"), /text-decoration:\s*line-through;/);
  assert.match(declarationsFor(css, ".compose .queued.canceled > .tag"), /color:\s*var\(--maroon\);/);
});

test("the step foot sets Interrupt as a red chip, the line beside its input, and the more button as a 36px ring", () => {
  assert.match(declarationsFor(css, ".stepbox .line"), /display:\s*flex;/);
  const interrupt = declarationsFor(css, ".stepbox .interrupt");
  assert.match(interrupt, /color:\s*var\(--red\);/);
  assert.match(interrupt, /background:\s*color-mix\(in srgb, var\(--red\) 10%, var\(--mantle\)\);/);
  assert.match(interrupt, /box-shadow:\s*inset 0 0 0 1px color-mix\(in srgb, var\(--red\) 40%, transparent\);/);
  const more = declarationsFor(css, ".stepbox .more");
  assert.match(more, /(?<![-\w])width:\s*36px;/);
  assert.match(more, /border-radius:\s*10px;/);
  assert.match(more, /box-shadow:\s*inset 0 0 0 1px var\(--surface1\);/);
});

test("the step menu's Confirm stop button is red", () => {
  assert.match(declarationsFor(css, ".stepbox-menu button.stop-confirm"), /color:\s*var\(--red\);/);
});

test("markup that left the page has no rules: the pinned head, the work line, the queued rows and the step actions", () => {
  assert.doesNotMatch(css, /pin-head|work-line|work-label|work-tool|work-time|work-note|stepbox-actions|\.reader \.queue/);
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

test("the 1200px cap is gone and the reader's text column stops at 68ch", () => {
  assert.doesNotMatch(css, /max-width:\s*1200px/);
  assert.match(declarationsFor(css, ".reader .t"), /grid-template-columns:\s*5ch minmax\(0,\s*68ch\);/);
  assert.match(declarationsFor(css, ".compose .queued"), /minmax\(0,\s*1fr\)/);
});

test("reading text is 14px with a 1.6 line height", () => {
  assert.match(declarationsFor(css, ".reader .t > .body"), /font:\s*14px\/1\.6 var\(--mono\)/);
  const queued = declarationsFor(css, ".compose .queued > .text");
  assert.match(queued, /font-size:\s*14px/);
  assert.match(queued, /line-height:\s*1\.6(?![\d.])/);
});

test("plain texts keep their line breaks", () => {
  const selectors = [
    ".reader .compaction .summary",
    ".reader .question",
    ".reader .question-card .question",
    ".reader .notice",
    ".compose .queued > .text",
  ];
  for (const selector of selectors) {
    assert.match(declarationsFor(css, selector), /white-space:\s*pre-(wrap|line)/, selector);
  }
});

test("a row's glyph is a 14px disc with a centred 9px mono mark", () => {
  const glyph = declarationsFor(css, ".b-row .g");
  assert.match(glyph, /(?<![-\w])width:\s*14px;/);
  assert.match(glyph, /(?<![-\w])height:\s*14px;/);
  assert.match(glyph, /font:\s*700 9px\/14px var\(--mono\);/);
  assert.match(glyph, /text-align:\s*center;/);
});

test("the waiting, done and interrupted row marks are peach, green and maroon, each with its glyph", () => {
  const waiting = declarationsFor(css, ".b-row .g-waiting");
  assert.match(waiting, /background:\s*var\(--peach\);/);
  assert.match(waiting, /color:\s*var\(--crust\);/);
  assert.match(declarationsFor(css, ".b-row .g-waiting::after"), /content:\s*"\?";/);
  const done = declarationsFor(css, ".b-row .g-done");
  assert.match(done, /box-shadow:\s*inset 0 0 0 1\.5px var\(--green\);/);
  assert.match(done, /color:\s*var\(--green\);/);
  assert.match(declarationsFor(css, ".b-row .g-done::after"), /content:\s*"✓";/);
  assert.match(declarationsFor(css, ".b-row .g-done::after"), /font-size:\s*8px;/);
  const interrupted = declarationsFor(css, ".b-row .g-interrupted");
  assert.match(interrupted, /box-shadow:\s*inset 0 0 0 1\.5px var\(--maroon\);/);
  assert.match(interrupted, /color:\s*var\(--maroon\);/);
  assert.match(declarationsFor(css, ".b-row .g-interrupted::after"), /content:\s*"!";/);
});

test("a row's working glyph turns, and reduced motion holds it still", () => {
  assert.match(declarationsFor(css, ".b-row .g-working"), /animation:\s*rot 1\.1s linear infinite;/);
  const reduced = mediaBlock(css, "@media (prefers-reduced-motion: reduce) {");
  assert.match(declarationsFor(reduced, ".b-row .g-working"), /animation:\s*none;/);
});

test("a row's waiting tag is 9.5px bold peach mono with .1em tracking", () => {
  const tag = declarationsFor(css, ".b-row .ty");
  assert.match(tag, /(?<![-\w])font:\s*700 9\.5px\/20px var\(--mono\);/);
  assert.match(tag, /letter-spacing:\s*\.1em;/);
  assert.match(tag, /color:\s*var\(--peach\);/);
});

test("a row's second line is 12.5px over 19px and clamps to two lines, and .one clamps to one", () => {
  const l2 = declarationsFor(css, ".b-row .l2");
  assert.match(l2, /font:\s*12\.5px\/19px var\(--mono\);/);
  assert.match(l2, /-webkit-line-clamp:\s*2;/);
  assert.match(declarationsFor(css, ".b-row .l2.one"), /-webkit-line-clamp:\s*1;/);
});

test("a section head is a baseline flex line whose title is cut with an ellipsis and whose count is plain overlay0", () => {
  const head = declarationsFor(css, ".b-sec");
  assert.match(head, /display:\s*flex;/);
  assert.match(head, /align-items:\s*baseline;/);
  assert.match(head, /gap:\s*8px;/);
  const title = declarationsFor(css, ".b-sec .title");
  assert.match(title, /min-width:\s*0;/);
  assert.match(title, /overflow:\s*hidden;/);
  assert.match(title, /text-overflow:\s*ellipsis;/);
  const count = declarationsFor(css, ".b-sec i");
  assert.match(count, /font-style:\s*normal;/);
  assert.match(count, /font-weight:\s*400;/);
  assert.match(count, /letter-spacing:\s*0;/);
  assert.match(count, /text-transform:\s*none;/);
  assert.match(count, /color:\s*var\(--overlay0\);/);
});

test("a Needs you head is peach and a New since you looked head is lavender", () => {
  assert.match(declarationsFor(css, ".b-sec.need"), /color:\s*var\(--peach\);/);
  assert.match(declarationsFor(css, ".b-sec.fresh"), /color:\s*var\(--lavender\);/);
});

test("answer chips wrap 5px apart under a 7px margin, each a peach chip with a peach key cap", () => {
  const opts = declarationsFor(css, ".b-row .opts");
  assert.match(opts, /display:\s*flex;/);
  assert.match(opts, /flex-wrap:\s*wrap;/);
  assert.match(opts, /gap:\s*5px;/);
  assert.match(opts, /margin-top:\s*7px;/);
  const opt = declarationsFor(css, ".b-row .opt");
  assert.match(opt, /border-radius:\s*6px;/);
  assert.match(opt, /background:\s*color-mix\(in srgb, var\(--peach\) 12%, transparent\);/);
  assert.match(opt, /box-shadow:\s*0 0 0 1px color-mix\(in srgb, var\(--peach\) 35%, transparent\);/);
  assert.match(opt, /font:\s*11\.5px\/18px var\(--mono\);/);
  const cap = declarationsFor(css, ".b-row .opt kbd");
  assert.match(cap, /background:\s*var\(--peach\);/);
  assert.match(cap, /color:\s*var\(--crust\);/);
});

test("the answered line is 12px over 18px in green, under a 5px margin", () => {
  const ans = declarationsFor(css, ".b-row .ans");
  assert.match(ans, /margin-top:\s*5px;/);
  assert.match(ans, /font:\s*12px\/18px var\(--mono\);/);
  assert.match(ans, /color:\s*var\(--green\);/);
});

test("a run fold's ✓ is green and its ✗ red, both pushed to the right", () => {
  const ok = declarationsFor(css, ".reader .fold .ok");
  assert.match(ok, /margin-left:\s*auto;/);
  assert.match(ok, /color:\s*var\(--green\);/);
  const bad = declarationsFor(css, ".reader .fold .bad");
  assert.match(bad, /margin-left:\s*auto;/);
  assert.match(bad, /color:\s*var\(--red\);/);
});

test("the row's state word and the unused status colours have no rules", () => {
  assert.doesNotMatch(css, /\.b-row \.state|\.st-done|\.st-interrupted|\.st-failed/);
});

test("an epic's main fills its line, and its reading sits at the right in 11.5px subtext0", () => {
  assert.match(declarationsFor(css, ".b-row.epic .main"), /flex:\s*1 1 auto;/);
  const reading = declarationsFor(css, ".b-row.epic .rt");
  assert.match(reading, /(?<![-\w])flex:\s*none;/);
  assert.match(reading, /margin-left:\s*auto;/);
  assert.match(reading, /font:\s*11\.5px\/20px var\(--mono\);/);
  assert.match(reading, /color:\s*var\(--subtext0\);/);
});

test("the task strip is a 3px flex line 2px apart, 6px above and 1px below, and its segments are surface0 with 1.5px corners", () => {
  const strip = declarationsFor(css, ".strip");
  assert.match(strip, /display:\s*flex;/);
  assert.match(strip, /(?<![-\w])gap:\s*2px;/);
  assert.match(strip, /(?<![-\w])height:\s*3px;/);
  assert.match(strip, /margin:\s*6px 0 1px;/);
  const segment = declarationsFor(css, ".strip i");
  assert.match(segment, /flex:\s*1 1 0;/);
  assert.match(segment, /min-width:\s*0;/);
  assert.match(segment, /border-radius:\s*1\.5px;/);
  assert.match(segment, /background:\s*var\(--surface0\);/);
});

test("dense closes the strip's gaps and its segments' corners", () => {
  assert.match(declarationsFor(css, ".strip.dense"), /(?<![-\w])gap:\s*0;/);
  assert.match(declarationsFor(css, ".strip.dense i"), /border-radius:\s*0;/);
});

test("the pips are an inline flex line 2px apart of 9px by 5px surface1 cells with 1px corners", () => {
  const pips = declarationsFor(css, ".pips");
  assert.match(pips, /display:\s*inline-flex;/);
  assert.match(pips, /(?<![-\w])gap:\s*2px;/);
  const cell = declarationsFor(css, ".pips i");
  assert.match(cell, /(?<![-\w])width:\s*9px;/);
  assert.match(cell, /(?<![-\w])height:\s*5px;/);
  assert.match(cell, /border-radius:\s*1px;/);
  assert.match(cell, /background:\s*var\(--surface1\);/);
});

test("the four kinds fill the strip's and the pips' cells: passed green, now yellow, held peach, errored red", () => {
  const kinds: [string, string][] = [
    ["k-passed", "green"],
    ["k-now", "yellow"],
    ["k-held", "peach"],
    ["k-errored", "red"],
  ];
  for (const [kind, colour] of kinds) {
    assert.match(declarationsFor(css, `.${kind}`), new RegExp(`--k:\\s*var\\(--${colour}\\);`), `${kind} is ${colour}`);
    for (const cell of [".strip i", ".pips i"]) {
      assert.match(declarationsFor(css, `${cell}.${kind}`), /background:\s*var\(--k\);/, `${cell}.${kind} is filled`);
    }
  }
});

test("a now cell pulses on a 1.8s glow, and reduced motion holds it still", () => {
  for (const cell of [".strip i.k-now", ".pips i.k-now"]) {
    assert.match(declarationsFor(css, cell), /animation:\s*now-glow 1\.8s ease-in-out infinite;/, cell);
  }
  assert.match(css, /@keyframes now-glow\s*\{/);
  const reduced = mediaBlock(css, "@media (prefers-reduced-motion: reduce) {");
  for (const cell of [".strip i.k-now", ".pips i.k-now"]) {
    assert.match(declarationsFor(reduced, cell), /animation:\s*none;/, `${cell} holds still under reduced motion`);
  }
});

test("a step row is a 14px 13ch auto grid at 12px over 20px, nowrap, indented 33px", () => {
  const step = declarationsFor(css, ".b-row.step");
  assert.match(step, /display:\s*grid;/);
  assert.match(step, /grid-template-columns:\s*14px 13ch auto minmax\(0,\s*1fr\);/);
  assert.match(step, /font:\s*12px\/20px var\(--mono\);/);
  assert.match(step, /white-space:\s*nowrap;/);
  assert.match(step, /margin-left:\s*33px;/);
});

test("a step's mark is yellow, bold and centred; its label is subtext1 and bold when current; its doing is overlay1 cut to one line", () => {
  const mark = declarationsFor(css, ".b-row.step .um");
  assert.match(mark, /text-align:\s*center;/);
  assert.match(mark, /font-weight:\s*700;/);
  assert.match(mark, /color:\s*var\(--yellow\);/);
  const label = declarationsFor(css, ".b-row.step .label");
  assert.match(label, /font:\s*12px\/20px var\(--mono\);/);
  assert.match(label, /overflow:\s*hidden;/);
  assert.match(label, /text-overflow:\s*ellipsis;/);
  assert.match(label, /color:\s*var\(--subtext1\);/);
  const current = declarationsFor(css, ".b-row.step.cur .label");
  assert.match(current, /color:\s*var\(--text\);/);
  assert.match(current, /font-weight:\s*700;/);
  const doing = declarationsFor(css, ".b-row.step .reply");
  assert.match(doing, /overflow:\s*hidden;/);
  assert.match(doing, /text-overflow:\s*ellipsis;/);
  assert.match(doing, /color:\s*var\(--overlay1\);/);
});

test("a held step's mark, label and held text are peach, and the held text is 12px with no capitals", () => {
  for (const selector of [".b-row.step.held .um", ".b-row.step.held .label", ".b-row.step .held"]) {
    assert.match(declarationsFor(css, selector), /color:\s*var\(--peach\);/, selector);
  }
  const held = declarationsFor(css, ".b-row.step .held");
  assert.match(held, /font:\s*12px\/20px var\(--mono\);/);
  assert.doesNotMatch(held, /text-transform|letter-spacing/);
});

test("the epic's count and the capitals held tag have no rules", () => {
  assert.doesNotMatch(css, /\.b-row \.count|\.b-row \.held/);
});

test("the column's eyebrow is 700 10.5px over 14px mono at .13em in overlay1 with no margin, its sub 400 at no spacing in overlay0, 6px in", () => {
  const eyebrow = declarationsFor(css, ".epic-view .eyebrow");
  assert.match(eyebrow, /margin:\s*0;/);
  assert.match(eyebrow, /font:\s*700 10\.5px\/14px var\(--mono\);/);
  assert.match(eyebrow, /letter-spacing:\s*\.13em;/);
  assert.match(eyebrow, /color:\s*var\(--overlay1\);/);
  const sub = declarationsFor(css, ".epic-view .eyebrow .sub");
  assert.match(sub, /font-weight:\s*400;/);
  assert.match(sub, /letter-spacing:\s*0;/);
  assert.match(sub, /color:\s*var\(--overlay0\);/);
  assert.match(sub, /margin-left:\s*6px;/);
});

test("the head line is a baseline flex row 9px apart that does not wrap", () => {
  const line = declarationsFor(css, ".epic-view .head-line");
  assert.match(line, /display:\s*flex;/);
  assert.match(line, /align-items:\s*baseline;/);
  assert.match(line, /(?<![-\w])gap:\s*9px;/);
  assert.match(line, /white-space:\s*nowrap;/);
  assert.match(line, /min-width:\s*0;/);
});

test("the head's slug is 700 14px over 20px mono and does not shrink; its repo is cut to one line in overlay1; its reading sits at the right in 11.5px subtext0", () => {
  const slug = declarationsFor(css, ".epic-view .head-line .slug");
  assert.match(slug, /margin:\s*0;/);
  assert.match(slug, /font:\s*700 14px\/20px var\(--mono\);/);
  assert.match(slug, /(?<![-\w])flex:\s*none;/);
  const repo = declarationsFor(css, ".epic-view .head-line .repo");
  assert.match(repo, /overflow:\s*hidden;/);
  assert.match(repo, /text-overflow:\s*ellipsis;/);
  assert.match(repo, /font:\s*11\.5px\/20px var\(--mono\);/);
  assert.match(repo, /color:\s*var\(--overlay1\);/);
  const reading = declarationsFor(css, ".epic-view .head-line .rt");
  assert.match(reading, /margin-left:\s*auto;/);
  assert.match(reading, /font:\s*11\.5px\/20px var\(--mono\);/);
  assert.match(reading, /color:\s*var\(--subtext0\);/);
  assert.match(reading, /(?<![-\w])flex:\s*none;/);
});

test("a problem is red and flush with the head, and its long text wraps", () => {
  const problem = declarationsFor(css, ".epic-head .problem");
  assert.match(problem, /color:\s*var\(--red\);/);
  assert.match(problem, /margin:\s*0;/);
  assert.match(problem, /overflow-wrap:\s*anywhere;/);
});

test("the column strip is 4px tall under a 9px top margin", () => {
  const strip = declarationsFor(css, ".epic-view .strip");
  assert.match(strip, /(?<![-\w])height:\s*4px;/);
  assert.match(strip, /margin:\s*9px 0 0;/);
});

test("the task rows are a 1px grid pulled 8px into the column, each row a 14px mark, slug, agent and pips grid, its line on the second row", () => {
  const tasks = declarationsFor(css, ".epic-view .tasks");
  assert.match(tasks, /display:\s*grid;/);
  assert.match(tasks, /(?<![-\w])gap:\s*1px;/);
  assert.match(tasks, /margin:\s*0 -8px;/);
  const task = declarationsFor(css, ".epic-view .task");
  assert.match(task, /display:\s*grid;/);
  assert.match(task, /grid-template-columns:\s*14px minmax\(0,\s*auto\) minmax\(0,\s*1fr\) auto;/);
  assert.match(task, /gap:\s*1px 7px;/);
  assert.match(task, /align-items:\s*center;/);
  assert.match(task, /padding:\s*4px 8px 5px;/);
  assert.match(task, /border-radius:\s*8px;/);
  assert.match(task, /font:\s*12px\/18px var\(--mono\);/);
  assert.match(declarationsFor(css, ".epic-view .task .line"), /grid-column:\s*2 \/ -1;/);
});

test("a task's mark is a centred bold colour of its kind; a running mark is a 10px disc that turns, and holds still under reduced motion", () => {
  const mark = declarationsFor(css, ".epic-view .task .um");
  assert.match(mark, /text-align:\s*center;/);
  assert.match(mark, /font-weight:\s*700;/);
  assert.match(mark, /color:\s*var\(--k, var\(--overlay0\)\);/);
  const spinner = declarationsFor(css, ".epic-view .task .um.g-working");
  assert.match(spinner, /(?<![-\w])width:\s*10px;/);
  assert.match(spinner, /(?<![-\w])height:\s*10px;/);
  assert.match(spinner, /justify-self:\s*center;/);
  assert.match(spinner, /border-radius:\s*50%;/);
  assert.match(spinner, /animation:\s*rot 1\.1s linear infinite;/);
  const reduced = mediaBlock(css.slice(css.indexOf(".epic-view .task .um.g-working")), "@media (prefers-reduced-motion: reduce) {");
  assert.match(declarationsFor(reduced, ".epic-view .task .um.g-working"), /animation:\s*none;/);
});

test("a task's slug is subtext1 and its agent overlay0, each cut to one line; its line is 11px over 15px in overlay1", () => {
  for (const selector of [".epic-view .task .slug", ".epic-view .task .agent", ".epic-view .task .line"]) {
    const rule = declarationsFor(css, selector);
    assert.match(rule, /overflow:\s*hidden;/, selector);
    assert.match(rule, /text-overflow:\s*ellipsis;/, selector);
    assert.match(rule, /white-space:\s*nowrap;/, selector);
  }
  assert.match(declarationsFor(css, ".epic-view .task .slug"), /color:\s*var\(--subtext1\);/);
  assert.match(declarationsFor(css, ".epic-view .task .agent"), /color:\s*var\(--overlay0\);/);
  const line = declarationsFor(css, ".epic-view .task .line");
  assert.match(line, /font:\s*11px\/15px var\(--mono\);/);
  assert.match(line, /color:\s*var\(--overlay1\);/);
});

test("the cursor's task row is surface0 with an inset 1px mauve ring and a bold text slug", () => {
  const cur = declarationsFor(css, ".epic-view .task.cur");
  assert.match(cur, /background:\s*var\(--surface0\);/);
  assert.match(cur, /box-shadow:\s*inset 0 0 0 1px var\(--mauve\);/);
  assert.match(declarationsFor(css, ".epic-view .task.cur .slug"), /font-weight:\s*700;/);
  assert.match(declarationsFor(css, ".epic-view .task.cur .slug"), /color:\s*var\(--text\);/);
});

test("a held task's mark and line are peach, and an out task's slug and line are overlay0", () => {
  for (const selector of [".epic-view .task.held .um", ".epic-view .task.held .line"]) {
    assert.match(declarationsFor(css, selector), /color:\s*var\(--peach\);/, selector);
  }
  for (const selector of [".epic-view .task.out .slug", ".epic-view .task.out .line"]) {
    assert.match(declarationsFor(css, selector), /color:\s*var\(--overlay0\);/, selector);
  }
});

test("the now card is a 12px-rounded surface0 ring with 10px 12px 11px padding", () => {
  const card = declarationsFor(css, ".epic-view .now-card");
  assert.match(card, /padding:\s*10px 12px 11px;/);
  assert.match(card, /border-radius:\s*12px;/);
  assert.match(card, /box-shadow:\s*inset 0 0 0 1px var\(--surface0\);/);
});

test("the now card's cap is 700 10px over 16px mono at .12em in overlay1; its doing is 13px over 19px; its time line is 11.5px over 17px in overlay1 under a 7px margin", () => {
  const cap = declarationsFor(css, ".epic-view .now-card .cap");
  assert.match(cap, /font:\s*700 10px\/16px var\(--mono\);/);
  assert.match(cap, /letter-spacing:\s*\.12em;/);
  assert.match(cap, /color:\s*var\(--overlay1\);/);
  assert.match(declarationsFor(css, ".epic-view .now-card p"), /margin:\s*4px 0 0;/);
  assert.match(declarationsFor(css, ".epic-view .now-card .doing"), /font:\s*13px\/19px var\(--mono\);/);
  const time = declarationsFor(css, ".epic-view .now-card .m");
  assert.match(time, /margin-top:\s*7px;/);
  assert.match(time, /font:\s*11\.5px\/17px var\(--mono\);/);
  assert.match(time, /color:\s*var\(--overlay1\);/);
});

test("the steps fold is a full-width, left-aligned mantle button with no border, and its ▸ turns ▾ when expanded", () => {
  const fold = declarationsFor(css, ".epic-view .steps-fold");
  assert.match(fold, /display:\s*block;/);
  assert.match(fold, /(?<![-\w])width:\s*100%;/);
  assert.match(fold, /text-align:\s*left;/);
  assert.match(fold, /padding:\s*5px 10px;/);
  assert.match(fold, /border:\s*0;/);
  assert.match(fold, /border-radius:\s*7px;/);
  assert.match(fold, /background:\s*var\(--mantle\);/);
  assert.match(fold, /font:\s*12px\/20px var\(--mono\);/);
  assert.match(fold, /color:\s*var\(--subtext0\);/);
  const marker = declarationsFor(css, ".epic-view .steps-fold::before");
  assert.match(marker, /content:\s*"▸";/);
  assert.match(marker, /color:\s*var\(--overlay0\);/);
  assert.match(marker, /font-size:\s*10px;/);
  assert.match(declarationsFor(css, '.epic-view .steps-fold[aria-expanded="true"]::before'), /content:\s*"▾";/);
});

test("the step list's pips are a flex row of 4px gaps, scoped to .epic-view .step .pips", () => {
  const pips = declarationsFor(css, ".epic-view .step .pips");
  assert.match(pips, /display:\s*flex;/);
  assert.match(pips, /(?<![-\w])gap:\s*4px;/);
  assert.match(pips, /align-items:\s*center;/);
});

test("the chart keeps the column's width at 100% and at most 404px", () => {
  const chart = declarationsFor(css, ".epic-view .chart");
  assert.match(chart, /(?<![-\w])width:\s*100%;/);
  assert.match(chart, /max-width:\s*404px;/);
});

test("the rules E1-D5 drops have no rules: the task's flex row, its mark and status, the outcome's paragraph, the old slug, the column's bare pips and the tone colours", () => {
  assert.doesNotMatch(css, /\.epic-view \.mark\b/);
  assert.doesNotMatch(css, /\.task \.status\b/);
  assert.doesNotMatch(css, /\.epic-head p\b/);
  assert.doesNotMatch(css, /\.epic-head \.slug\b/);
  assert.doesNotMatch(css, /\.epic-view \.pips\b/);
  assert.doesNotMatch(css, /\.ts-[a-z]/);
  const flexTask = rulesIn(css).filter((rule) => rule.selectors.includes(".epic-view .task") && /display:\s*flex;/.test(rule.declarations));
  assert.deepEqual(flexTask, []);
});
