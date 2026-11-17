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

// mediaBlock returns the body of the first block with the given header, an @media or a @keyframes, matching its braces.
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

// phaseSection returns the phase output and worker session rules: from the first of them to the flash rule that follows.
function phaseSection(): string {
  const start = css.indexOf(".phase-output {");
  const end = css.indexOf(".flash {");
  assert.notEqual(start, -1, "style.css has the phase output rules");
  assert.ok(end > start, "the phase output rules come before the flash rule");
  return css.slice(start, end);
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

test("from 760px the page fills the window and the list, reader and epic page scroll", () => {
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
  for (const selector of [".list > .rows", ".read > .reader", "section.page"]) {
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

test("from 760px each column keeps its own track in the one row, and the epic page takes the reader's track", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  // One row for every column: a column placed in an earlier track than the one before it must not wrap to a second row.
  for (const selector of [".panes > .list", ".panes > .read", ".panes > section.page"]) {
    assert.match(declarationsFor(wide, selector), /grid-row:\s*1;/, `${selector} sits in the one row`);
  }
  assert.match(declarationsFor(wide, ".panes > .list"), /grid-column:\s*1;/);
  assert.match(declarationsFor(wide, ".panes > .read"), /grid-column:\s*2;/);
  assert.match(declarationsFor(wide, ".panes > .read.pinned"), /grid-column:\s*3;/);
  assert.match(declarationsFor(wide, ".panes > section.page"), /grid-column:\s*2\s*\/\s*-1;/);
});

test("from 760px with no pin, the reader and the epic page run to the last track right of the list", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(
    declarationsFor(wide, ".panes:not(.split) > .read"),
    /grid-column:\s*2\s*\/\s*-1;/,
    "the reader runs to the last line when nothing is pinned",
  );
});

test("from 760px a pinned epic page takes the reader's track, one track wide", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, ".panes.split > section.page"), /grid-column:\s*2;/);
});

test("from 760px the body is base, the list is crust behind surface0, and the epic page is behind --rule", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, "body"), /background:\s*var\(--base\);/);
  assert.match(declarationsFor(wide, ".list"), /background:\s*var\(--crust\);/);
  assert.match(declarationsFor(wide, ".list"), /border-right:\s*1px solid var\(--surface0\);/);
  assert.match(declarationsFor(wide, "section.page"), /border-left:\s*1px solid var\(--rule\);/);
  assert.match(declarationsFor(wide, ".read.pinned"), /border-left:\s*1px solid var\(--rule\);/);
});

test("from 760px the epic page scrolls on its own inside the reader's padding", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  const page = declarationsFor(wide, "section.page");
  assert.match(page, /overflow-y:\s*auto;/);
  assert.match(page, /padding:\s*18px 28px 10px;/);
});

test("a hidden epic page or reader takes no place", () => {
  assert.match(declarationsFor(css, "section.page[hidden]"), /display:\s*none;/);
  assert.match(declarationsFor(css, ".read[hidden]"), /display:\s*none;/);
});

test("from 760px the top, rows, head, conversation and foot keep their padding", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, ".top"), /padding:\s*12px 14px 10px;/);
  assert.match(declarationsFor(wide, ".list > .rows"), /padding:\s*0 6px 10px;/);
  assert.match(declarationsFor(wide, ".head"), /padding:\s*12px 28px 11px;/);
  assert.match(declarationsFor(wide, ".read > .reader"), /padding:\s*18px 28px 10px;/);
  assert.match(declarationsFor(wide, ".send"), /padding:\s*0 28px 14px;/);
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

test("a row's conversation title row takes the head's padding from 760px, and the mono family", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  assert.match(declarationsFor(wide, ".read > .title-row"), /padding:\s*12px 28px 11px;/);
  assert.match(declarationsFor(css, ".read > .title-row"), /font-family:\s*var\(--mono\);/);
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

test("no rule names the send foot's how line, whose markup left the page", () => {
  assert.doesNotMatch(css, /\.how\b/);
});

test("the composer's send button is a 24px mauve square with crust text", () => {
  const go = declarationsFor(css, ".composer .go");
  assert.match(go, /(?<![-\w])width:\s*24px;/);
  assert.match(go, /(?<![-\w])height:\s*24px;/);
  assert.match(go, /background:\s*var\(--mauve\);/);
  assert.match(go, /color:\s*var\(--crust\);/);
});

test("a working composer's edge is a 2px inset line in yellow, and a waiting or held one's is peach", () => {
  assert.match(declarationsFor(css, ".composer.edge-working"), /inset 2px 0 0 var\(--yellow\)/);
  for (const selector of [".composer.edge-waiting", ".composer.edge-held"]) {
    assert.match(declarationsFor(css, selector), /inset 2px 0 0 var\(--peach\)/, selector);
  }
});

test("an idle composer keeps the plain surface1 ring and names no edge colour", () => {
  assert.match(declarationsFor(css, ".composer"), /box-shadow:\s*inset 0 0 0 1px var\(--surface1\);/);
  assert.equal(declarationsFor(css, ".composer.edge-idle"), "");
});

test("the composer's focus ring is a 1px inset mauve ring, and a focused edge keeps its colour under it", () => {
  assert.match(declarationsFor(css, ".composer:focus-within"), /box-shadow:\s*inset 0 0 0 1px var\(--mauve\);/);
  assert.match(
    declarationsFor(css, ".composer.edge-working:focus-within"),
    /box-shadow:\s*inset 2px 0 0 var\(--yellow\), inset 0 0 0 1px var\(--mauve\);/,
  );
  for (const selector of [".composer.edge-waiting:focus-within", ".composer.edge-held:focus-within"]) {
    assert.match(
      declarationsFor(css, selector),
      /box-shadow:\s*inset 2px 0 0 var\(--peach\), inset 0 0 0 1px var\(--mauve\);/,
      selector,
    );
  }
});

test("both composer textareas size to their content, cap at 8 lines of their 1.45 line height, then scroll", () => {
  const box = declarationsFor(css, ".composer textarea");
  assert.match(box, /(?<![-\w])field-sizing:\s*content;/);
  assert.match(box, /font:\s*15\.5px\/1\.45 var\(--mono\);/);
  assert.match(box, /(?<![-\w])max-height:\s*calc\(8 \* 1\.45em\);/);
  assert.match(box, /overflow-y:\s*auto;/);
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

test("GhostQueued: the queued row's rule draws a dashed mauve edge", () => {
  assert.match(declarationsFor(css, ".compose .queued"), /border-left:\s*2px dashed var\(--mauve\);/);
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

test("from 760px to 1500px the list is 400px and the third track 340px", () => {
  const block = mediaBlock(css, "@media (min-width: 760px) and (max-width: 1500px) {");
  assert.match(declarationsFor(block, ".panes"), /grid-template-columns:\s*400px minmax\(0,\s*1fr\) 340px;/);
  assert.match(
    declarationsFor(block, ".panes.split"),
    /grid-template-columns:\s*400px minmax\(0,\s*1fr\) minmax\(0,\s*1fr\);/,
  );
});

test("from 760px to 1100px the list is 380px in two tracks", () => {
  const block = mediaBlock(css, "@media (min-width: 760px) and (max-width: 1100px) {");
  assert.match(declarationsFor(block, ".panes"), /grid-template-columns:\s*380px minmax\(0,\s*1fr\);/);
  assert.match(
    declarationsFor(block, ".panes.split"),
    /grid-template-columns:\s*380px minmax\(0,\s*1fr\) minmax\(0,\s*1fr\);/,
  );
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

test("the 1200px cap is gone, and the reader's gutter and body fill its row with no 68ch track", () => {
  assert.doesNotMatch(css, /max-width:\s*1200px/);
  assert.doesNotMatch(css, /68ch/);
  assert.match(declarationsFor(css, ".reader .t"), /grid-template-columns:\s*5ch minmax\(0,\s*1fr\);/);
  assert.match(declarationsFor(css, ".compose .queued"), /minmax\(0,\s*1fr\)/);
});

test("TurnRhythm: the conversation column's rule sets an 88ch width, centred, at the reading size", () => {
  const column = declarationsFor(css, ".reader .t");
  assert.match(column, /(?<![-\w])width:\s*88ch;/);
  assert.match(column, /(?<![-\w])font-size:\s*14px;/);
  assert.match(column, /margin-inline:\s*auto;/);
});

test("TurnRhythm: the owner's turn's rule sets a 30px top margin, and the first row has none", () => {
  assert.match(declarationsFor(css, ".reader .t.owner-turn"), /margin-top:\s*30px;/);
  assert.match(declarationsFor(css, ".reader .t.owner-turn:first-child"), /margin-top:\s*0(?![\d.])/);
});

test("reading text is 14px with a 1.6 line height", () => {
  assert.match(declarationsFor(css, ".reader .t > .body"), /font:\s*14px\/1\.6 var\(--mono\)/);
  const queued = declarationsFor(css, ".compose .queued > .text");
  assert.match(queued, /font-size:\s*14px/);
  assert.match(queued, /line-height:\s*1\.6(?![\d.])/);
});

test("a sending row is dimmed, its mark is 10px, and the queued words are mono", () => {
  assert.match(declarationsFor(css, ".reader .t.sending-row"), /opacity:\s*\.55;/);
  assert.match(declarationsFor(css, ".reader .t > .gut.sending"), /font-size:\s*10px;/);
  assert.match(declarationsFor(css, ".compose .queued > .words"), /font:\s*400 11px\/1\.45 var\(--mono\);/);
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

test("a run fold's dot trail is pushed to the right, and a failed call's dot is red", () => {
  assert.match(declarationsFor(css, ".reader .fold .trail"), /margin-left:\s*auto;/);
  assert.match(declarationsFor(css, ".reader .fold .dot.failed"), /background:\s*var\(--red\);/);
});

test("a running call's line and a running run's fold draw a yellow rail, and a running dot is yellow", () => {
  assert.match(declarationsFor(css, ".reader .call.running"), /box-shadow:\s*inset 2px 0 0 var\(--yellow\)/);
  assert.match(declarationsFor(css, ".reader .fold.running"), /box-shadow:\s*inset 2px 0 0 var\(--yellow\)/);
  assert.match(declarationsFor(css, ".reader .fold .dot.running"), /background:\s*var\(--yellow\)/);
});

test("CallStatus: each dot in a run's trail takes its status's colour: ok green, failed red, running yellow, none grey", () => {
  assert.match(declarationsFor(css, ".reader .fold .dot.ok"), /background:\s*var\(--green\);/);
  assert.match(declarationsFor(css, ".reader .fold .dot.failed"), /background:\s*var\(--red\);/);
  assert.match(declarationsFor(css, ".reader .fold .dot.running"), /background:\s*var\(--yellow\)/);
  assert.match(declarationsFor(css, ".reader .fold .dot.none"), /background:\s*var\(--overlay1\);/);
});

test("a call line is 12px grey on a hairline, its tool's name the only colour, and a run's opened lines touch", () => {
  const line = declarationsFor(css, ".reader .call");
  assert.match(line, /font:\s*12px\/20px var\(--mono\);/);
  assert.match(line, /(?<![-\w])color:\s*var\(--overlay1\);/);
  assert.match(line, /box-shadow:\s*inset 0 -1px 0 var\(--rule\);/);
  assert.match(line, /(?<![-\w])margin:\s*0;/);
  assert.match(declarationsFor(css, ".reader .call b"), /color:\s*var\(--blue\);/);
  assert.match(declarationsFor(css, ".reader div.calls"), /(?<![-\w])gap:\s*0;/);
  // The fold button is a .calls element too, so a column rule on .reader .calls would stack the folded line.
  assert.doesNotMatch(css, /\.reader \.calls(?![\w-])/);
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

test("the Pause, Stop and Answer commands are mono buttons on surface0, and Answer is the mauve primary", () => {
  assert.match(declarationsFor(css, "button.act"), /background:\s*var\(--surface0\);/);
  const primary = declarationsFor(css, "button.act.primary");
  assert.match(primary, /background:\s*var\(--mauve\);/);
  assert.match(primary, /color:\s*var\(--crust\);/);
});

test("the three moves press, state and layout are 120, 200 and 280 ms, drill is 320 ms ease-out and leave 200 ms ease-in", () => {
  const root = declarationsFor(css, ":root");
  assert.match(root, /--move-press:\s*120ms cubic-bezier\(0\.2, 0, 0, 1\);/);
  assert.match(root, /--move-state:\s*200ms cubic-bezier\(0\.2, 0, 0, 1\);/);
  assert.match(root, /--move-layout:\s*280ms cubic-bezier\(0\.2, 0, 0, 1\);/);
  assert.match(root, /--move-drill:\s*320ms cubic-bezier\(0\.2, 0, 0, 1\);/);
  assert.match(root, /--move-leave:\s*200ms cubic-bezier\(0\.4, 0, 1, 1\);/);
});

test("the palette has the teal the phone draws accepted in", () => {
  assert.match(declarationsFor(css, ":root"), /--teal:\s*#94e2d5;/);
});

test("the three keyframes: a drill and a leave move the page in, and fade changes opacity alone", () => {
  assert.match(css, /@keyframes move-drill\s*\{/);
  assert.match(css, /@keyframes move-leave\s*\{/);
  assert.match(css, /@keyframes move-fade\s*\{/);
  assert.match(mediaBlock(css, "@keyframes move-drill {"), /transform/);
  assert.match(mediaBlock(css, "@keyframes move-leave {"), /transform/);
  const fade = mediaBlock(css, "@keyframes move-fade {");
  assert.match(fade, /opacity/);
  assert.doesNotMatch(fade, /transform/);
});

test("a page moving deeper drills in over the drill move, and a page coming back leaves in over the leave move", () => {
  assert.match(declarationsFor(css, "section.page.drill"), /animation:\s*move-drill var\(--move-drill\);/);
  assert.match(declarationsFor(css, "section.page.leave"), /animation:\s*move-leave var\(--move-leave\);/);
});

test("under reduced motion every move is one 100 ms linear step, and a drilling or leaving page fades", () => {
  const reduced = mediaBlock(css, "@media (prefers-reduced-motion: reduce) {");
  const root = declarationsFor(reduced, ":root");
  for (const name of ["press", "state", "layout", "drill", "leave"]) {
    assert.match(root, new RegExp(`--move-${name}:\\s*100ms linear;`), `--move-${name} is 100ms linear`);
  }
  assert.match(declarationsFor(reduced, "section.page.drill"), /animation:\s*move-fade var\(--move-drill\);/);
  assert.match(declarationsFor(reduced, "section.page.leave"), /animation:\s*move-fade var\(--move-leave\);/);
});

test("buttons transition their transform and background over the press move, and a pressed one shrinks", () => {
  for (const selector of ["button.act", ".back", ".crumb"]) {
    assert.match(
      declarationsFor(css, selector),
      /transition:\s*transform var\(--move-press\), background-color var\(--move-press\);/,
      selector,
    );
  }
  assert.match(declarationsFor(css, "button.act:active"), /transform:\s*scale\(\.97\);/);
});

test("the state colours and the dim move over the state move: the cells, the title row's state and blocker, and the proof sections", () => {
  assert.match(declarationsFor(css, ".orrery .cell"), /transition:\s*fill var\(--move-state\);/);
  assert.match(declarationsFor(css, ".title-row .state"), /transition:\s*color var\(--move-state\);/);
  assert.match(declarationsFor(css, ".title-row .blocker"), /transition:\s*color var\(--move-state\);/);
  assert.match(declarationsFor(css, ".epic-page .proof"), /transition:\s*opacity var\(--move-state\);/);
});

test("the Just happened lines transition over the layout move, and a line that enters starts faded", () => {
  assert.match(
    declarationsFor(css, ".epic-page .happen.just li"),
    /transition:\s*opacity var\(--move-layout\), transform var\(--move-layout\);/,
  );
  assert.match(css, /@starting-style\s*\{\s*\.epic-page \.happen\.just li\s*\{\s*opacity:\s*0;/);
});

test("an epic line's state and blocker phrases enter with move-fade over the state move, so a changed phrase fades in", () => {
  assert.match(declarationsFor(css, ".b-row.epic .words .state"), /animation:\s*move-fade var\(--move-state\);/);
  assert.match(declarationsFor(css, ".b-row.epic .words .blocker"), /animation:\s*move-fade var\(--move-state\);/);
});

test("a row entering Just ended and a new Needs you item enter with move-fade over the layout move", () => {
  assert.match(declarationsFor(css, ".b-row.epic.ended"), /animation:\s*move-fade var\(--move-layout\);/);
  assert.match(declarationsFor(css, ".need-entry"), /animation:\s*move-fade var\(--move-layout\);/);
});

test("the new list entries name their duration only through a --move-* variable", () => {
  for (const selector of [
    ".b-row.epic .words .state",
    ".b-row.epic .words .blocker",
    ".b-row.epic.ended",
    ".need-entry",
  ]) {
    const rule = declarationsFor(css, selector);
    assert.match(rule, /animation:/, `${selector} has an animation`);
    assert.doesNotMatch(rule.replace(/var\(--move-[a-z]+\)/g, ""), /\d(\.\d+)?m?s\b/, selector);
  }
});

test("a running wedge's now cell glows and pulses on 1.8s, and under reduced motion the glow holds still at half strength", () => {
  assert.match(declarationsFor(css, ".orrery .glow"), /animation:\s*orbit-glow 1\.8s ease-in-out infinite;/);
  // The orrery draws no SVG filter, so a glow may name none: a url(#...) here points at nothing.
  assert.doesNotMatch(css, /url\(#/);
  assert.match(css, /@keyframes orbit-glow\s*\{/);
  const reduced = mediaBlock(css, "@media (prefers-reduced-motion: reduce) {");
  assert.match(declarationsFor(reduced, ".orrery .glow"), /animation:\s*none;/);
  assert.match(declarationsFor(reduced, ".orrery .glow"), /opacity:\s*\.5;/);
});

test("the epic page is one column of its sections, with a 12px gap", () => {
  const page = declarationsFor(css, ".epic-page");
  assert.match(page, /display:\s*flex;/);
  assert.match(page, /flex-direction:\s*column;/);
  assert.match(page, /(?<![-\w])gap:\s*12px;/);
});

test("a section heading is 700 10.5px over 14px mono at .13em in overlay1, and its count is 400 at no spacing, 6px in", () => {
  const heading = declarationsFor(css, ".epic-page h3");
  assert.match(heading, /margin:\s*0;/);
  assert.match(heading, /font:\s*700 10\.5px\/14px var\(--mono\);/);
  assert.match(heading, /letter-spacing:\s*\.13em;/);
  assert.match(heading, /color:\s*var\(--overlay1\);/);
  const sub = declarationsFor(css, ".epic-page h3 .sub");
  assert.match(sub, /font-weight:\s*400;/);
  assert.match(sub, /letter-spacing:\s*0;/);
  assert.match(sub, /color:\s*var\(--overlay0\);/);
  assert.match(sub, /margin-left:\s*6px;/);
});

test("the title row lays its back, crumbs, state, blocker and commands on one line, the commands pushed right", () => {
  const row = declarationsFor(css, ".title-row");
  assert.match(row, /display:\s*flex;/);
  assert.match(row, /white-space:\s*nowrap;/);
  assert.match(declarationsFor(css, ".title-row .acts"), /margin-left:\s*auto;/);
});

test("the title row's state is a bold 11.5px mono word, and its blocker is cut to one line in peach", () => {
  assert.match(declarationsFor(css, ".title-row .state"), /font:\s*700 11\.5px\/22px var\(--mono\);/);
  const blocker = declarationsFor(css, ".title-row .blocker");
  assert.match(blocker, /color:\s*var\(--peach\);/);
  assert.match(blocker, /overflow:\s*hidden;/);
  assert.match(blocker, /text-overflow:\s*ellipsis;/);
});

test("the crumb that names the page is bold, and the other crumbs are plain buttons on no background", () => {
  assert.match(declarationsFor(css, ".title-row .crumb.here"), /font-weight:\s*700;/);
  assert.match(declarationsFor(css, ".crumb"), /background:\s*none;/);
  assert.match(declarationsFor(css, ".crumb"), /cursor:\s*pointer;/);
});

test("the now block is a column of its sentence, its blocker's line or form, and Stop", () => {
  const block = declarationsFor(css, ".now-block");
  assert.match(block, /display:\s*flex;/);
  assert.match(block, /flex-direction:\s*column;/);
  assert.match(declarationsFor(css, ".now-block .now"), /color:\s*var\(--text\);/);
  assert.match(declarationsFor(css, ".now-block .blocked"), /color:\s*var\(--peach\);/);
});

test("a question's card is a column with its question in peach, its option cards as key-capped rows, and its text box in a surface1 ring", () => {
  assert.match(declarationsFor(css, ".now-block .answer"), /flex-direction:\s*column;/);
  assert.match(declarationsFor(css, ".now-block .answer .question"), /color:\s*var\(--peach\);/);
  assert.match(declarationsFor(css, ".now-block .answer .opts"), /display:\s*grid;/);
  assert.match(declarationsFor(css, ".now-block .answer .opt"), /grid-template-columns:\s*24px minmax\(0, 1fr\);/);
  assert.match(declarationsFor(css, ".now-block .answer .opt.sel"), /box-shadow:\s*inset 0 0 0 1\.5px var\(--mauve\);/);
  assert.match(declarationsFor(css, ".now-block .answer .opt em"), /background:\s*var\(--green\);/);
  assert.match(
    declarationsFor(css, ".now-block .answer textarea"),
    /box-shadow:\s*inset 0 0 0 1px var\(--surface1\);/,
  );
});

test("the contract summary is a full-width, left-aligned mantle button", () => {
  const summary = declarationsFor(css, ".epic-page .summary");
  assert.match(summary, /(?<![-\w])width:\s*100%;/);
  assert.match(summary, /text-align:\s*left;/);
  assert.match(summary, /background:\s*var\(--mantle\);/);
});

test("the orrery fills its column to at most 404px and sets its text in mono", () => {
  const orrery = declarationsFor(css, ".orrery");
  assert.match(orrery, /(?<![-\w])width:\s*100%;/);
  assert.match(orrery, /max-width:\s*404px;/);
  assert.match(declarationsFor(css, ".orrery text"), /font-family:\s*var\(--mono\);/);
});

test("a passed cell is green, an ahead or planned cell is the track colour, and a now cell takes its tone", () => {
  assert.match(declarationsFor(css, ".orrery .cell.passed"), /fill:\s*var\(--green\);/);
  for (const selector of [".orrery .cell.ahead", ".orrery .cell.planned"]) {
    assert.match(
      declarationsFor(css, selector),
      /fill:\s*color-mix\(in srgb, var\(--surface0\) 60%, var\(--crust\)\);/,
      selector,
    );
  }
  assert.match(declarationsFor(css, ".orrery .cell.now"), /fill:\s*var\(--tone\);/);
});

test("each tone names its colour: run yellow, mine peach, bad red, acc teal and ok green", () => {
  const tones: [string, string][] = [
    ["run", "yellow"],
    ["mine", "peach"],
    ["bad", "red"],
    ["acc", "teal"],
    ["ok", "green"],
  ];
  for (const [tone, colour] of tones) {
    assert.match(
      declarationsFor(css, `.t-${tone}`),
      new RegExp(`--tone:\\s*var\\(--${colour}\\);`),
      `t-${tone} is ${colour}`,
    );
  }
});

test("a proven requirement's arc is green, an open one is an overlay0 wash, and only a phase cell that opens is a pointer", () => {
  assert.match(declarationsFor(css, ".orrery .req.proven"), /fill:\s*var\(--green\);/);
  assert.match(
    declarationsFor(css, ".orrery .req.open"),
    /fill:\s*color-mix\(in srgb, var\(--overlay0\) 45%, transparent\);/,
  );
  assert.match(declarationsFor(css, ".orrery .cell.open"), /cursor:\s*pointer;/);
  assert.doesNotMatch(declarationsFor(css, ".orrery .req.open"), /cursor/);
  assert.equal(declarationsFor(css, ".open"), "", "no rule selects a bare .open");
});

test("the hover card is a mantle box with a surface0 ring, and its task is bold", () => {
  const card = declarationsFor(css, ".epic-page .slice-card");
  assert.match(card, /background:\s*var\(--mantle\);/);
  assert.match(card, /box-shadow:\s*inset 0 0 0 1px var\(--surface0\);/);
  assert.match(declarationsFor(css, ".epic-page .card-task"), /font-weight:\s*700;/);
});

test("a Now row takes its tone's colour, a row that opens its task is a pointer, and +n earlier is a bare overlay0 button", () => {
  assert.match(declarationsFor(css, ".epic-page .happen.now li"), /color:\s*var\(--tone\);/);
  assert.match(declarationsFor(css, ".epic-page .happen li.open"), /cursor:\s*pointer;/);
  const more = declarationsFor(css, ".epic-page .happen .more");
  assert.match(more, /background:\s*none;/);
  assert.match(more, /color:\s*var\(--overlay0\);/);
});

test("a proof section dims to 45% when its phase is not reached", () => {
  assert.match(declarationsFor(css, ".epic-page .proof.dim"), /opacity:\s*\.45;/);
});

test("a proof card is a grid of id, text and kind, its conclusion under them, and an open card is a pointer", () => {
  const card = declarationsFor(css, ".epic-page .proof-card");
  assert.match(card, /grid-template-columns:\s*minmax\(0,\s*auto\) minmax\(0,\s*1fr\) auto;/);
  assert.match(card, /(?<![-\w])gap:\s*7px;/);
  assert.match(declarationsFor(css, ".epic-page .proof-card.open"), /cursor:\s*pointer;/);
  assert.match(declarationsFor(css, ".epic-page .proof-card .conclusion"), /grid-column:\s*2\s*\/\s*-1;/);
});

test("a card's id is bold in subtext1, its text takes the text colour and wraps, and its kind is overlay0", () => {
  const rid = declarationsFor(css, ".epic-page .proof-card .rid");
  assert.match(rid, /font-weight:\s*700;/);
  assert.match(rid, /(?<![-\w])color:\s*var\(--subtext1\);/);
  const text = declarationsFor(css, ".epic-page .proof-card .text");
  assert.match(text, /(?<![-\w])color:\s*var\(--text\);/);
  assert.match(text, /overflow-wrap:\s*anywhere;/);
  assert.doesNotMatch(text, /white-space:\s*nowrap/);
  assert.match(declarationsFor(css, ".epic-page .proof-card .kind"), /(?<![-\w])color:\s*var\(--overlay0\);/);
});

test("a release step is a grid of step, target and undo, and the closing line is 11.5px subtext0", () => {
  const step = declarationsFor(css, ".epic-page .release-step");
  assert.match(step, /grid-template-columns:\s*minmax\(0,\s*auto\) minmax\(0,\s*1fr\) minmax\(0,\s*auto\);/);
  const then = declarationsFor(css, ".epic-page .release .then");
  assert.match(then, /font:\s*11\.5px\/17px var\(--mono\);/);
  assert.match(then, /color:\s*var\(--subtext0\);/);
});

test("the contract's checks keep their line breaks", () => {
  assert.match(declarationsFor(css, ".contract .checks"), /white-space:\s*pre-wrap;/);
});

test("the phase output and the worker session have a rule for each of their classes and each row state", () => {
  const section = phaseSection();
  for (const name of [
    "phase-output",
    "phases",
    "phase-row",
    "phase-head",
    "phase-name",
    "phase-summary",
    "phase-time",
    "phase-body",
    "attempts",
    "rounds",
    "round",
    "checks",
    "gate",
    "gate-stdout",
    "gate-stderr",
    "judge",
    "reviews",
    "findings",
    "reviewers",
    "integration",
    "worker-session",
  ]) {
    assert.match(section, new RegExp(`\\.${name}(?![\\w-])`), `.${name} has a rule`);
  }
  for (const state of ["passed", "now", "ahead", "open"]) {
    assert.match(section, new RegExp(`\\.phase-row\\.${state}(?![\\w-])`), `.phase-row.${state} has a rule`);
  }
});

test("the phase output and the worker session take the mono family and the palette's tokens, never a literal colour", () => {
  const section = phaseSection();
  assert.doesNotMatch(section, /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/);
  const fonts = [...section.matchAll(/(?<![\w-])font(?:-family)?:\s*([^;}]+)/g)].map((match) => match[1].trim());
  assert.ok(fonts.length > 0, "the phase output sets its fonts");
  for (const value of fonts) {
    assert.match(value, /^inherit$|var\(--mono\)/, `font declaration "${value}"`);
  }
});

test("an opening row's body fades in with move-fade over the layout move", () => {
  assert.match(declarationsFor(css, ".phase-output .phase-body"), /animation:\s*move-fade var\(--move-layout\);/);
});

test("a row's state colours transition over the state move", () => {
  assert.match(
    declarationsFor(css, ".phase-output .phase-row"),
    /transition:\s*color var\(--move-state\), background-color var\(--move-state\)/,
  );
});

test("no rule in the phase output or the worker session names a duration other than through a --move-* variable", () => {
  const timed = phaseSection().replace(/var\(--move-[a-z]+\)/g, "");
  assert.doesNotMatch(timed, /\d(\.\d+)?m?s\b/);
});

test("the worker session is a column that fills its track, and its reader alone scrolls", () => {
  const session = declarationsFor(css, ".worker-session");
  assert.match(session, /display:\s*flex;/);
  assert.match(session, /flex-direction:\s*column;/);
  assert.match(session, /height:\s*100%;/);
  const reader = declarationsFor(css, ".worker-session > .reader");
  assert.match(reader, /flex:\s*1 1 auto;/);
  assert.match(reader, /min-height:\s*0(?![\d.])/);
  assert.match(reader, /overflow-y:\s*auto;/);
});

test("the evidence runs are a grid of cards, each run a 4px grid on mantle, padded 6px 8px, with 8px corners at 11.5px over 17px mono", () => {
  assert.match(declarationsFor(css, ".epic-page .runs"), /(?<![-\w])gap:\s*4px;/);
  assert.match(declarationsFor(css, ".epic-page .proof-card .runs"), /grid-column:\s*1 \/ -1;/);
  const run = declarationsFor(css, ".epic-page .run");
  assert.match(run, /display:\s*grid;/);
  assert.match(run, /(?<![-\w])gap:\s*4px;/);
  assert.match(run, /padding:\s*6px 8px;/);
  assert.match(run, /border-radius:\s*8px;/);
  assert.match(run, /background:\s*var\(--mantle\);/);
  assert.match(run, /font:\s*11\.5px\/17px var\(--mono\);/);
});

test("a run's head wraps in a flex row, its phase is bold in subtext1, and a passed result is green", () => {
  const head = declarationsFor(css, ".epic-page .run .run-head");
  assert.match(head, /display:\s*flex;/);
  assert.match(head, /flex-wrap:\s*wrap;/);
  assert.match(head, /gap:\s*0 9px;/);
  assert.match(head, /(?<![-\w])color:\s*var\(--overlay1\);/);
  const phase = declarationsFor(css, ".epic-page .run .phase");
  assert.match(phase, /font-weight:\s*700;/);
  assert.match(phase, /(?<![-\w])color:\s*var\(--subtext1\);/);
  assert.match(declarationsFor(css, ".epic-page .run .pass.ok"), /(?<![-\w])color:\s*var\(--green\);/);
});

test("the log toggle is a bare button in overlay1 mono that starts its grid cell", () => {
  const toggle = declarationsFor(css, ".epic-page .run .log-toggle");
  assert.match(toggle, /justify-self:\s*start;/);
  assert.match(toggle, /background:\s*none;/);
  assert.match(toggle, /(?<![-\w])border:\s*0;/);
  assert.match(toggle, /(?<![-\w])padding:\s*0;/);
  assert.match(toggle, /cursor:\s*pointer;/);
  assert.match(toggle, /font:\s*11px\/17px var\(--mono\);/);
  assert.match(toggle, /(?<![-\w])color:\s*var\(--overlay1\);/);
});

test("a log is a crust block that scrolls, keeps its line breaks and long words, and a tail's label is overlay1", () => {
  const log = declarationsFor(css, ".epic-page .log");
  assert.match(log, /(?<![-\w])margin:\s*0;/);
  assert.match(log, /background:\s*var\(--crust\);/);
  assert.match(log, /max-height:\s*240px;/);
  assert.match(log, /overflow:\s*auto;/);
  assert.match(log, /(?<![-\w])padding:\s*6px 8px;/);
  assert.match(log, /border-radius:\s*6px;/);
  assert.match(log, /font:\s*11px\/16px var\(--mono\);/);
  assert.match(log, /(?<![-\w])color:\s*var\(--subtext1\);/);
  assert.match(log, /white-space:\s*pre-wrap;/);
  assert.match(log, /overflow-wrap:\s*anywhere;/);
  assert.match(declarationsFor(css, ".epic-page .tail-label"), /(?<![-\w])color:\s*var\(--overlay1\);/);
});

test("the loading line is overlay0 and the unavailable line is red", () => {
  assert.match(declarationsFor(css, ".epic-page .run .pending"), /(?<![-\w])color:\s*var\(--overlay0\);/);
  assert.match(declarationsFor(css, ".epic-page .run .unavailable"), /(?<![-\w])color:\s*var\(--red\);/);
});

test("the artifacts are a 4px grid of wrapping rows, a path wraps, and a drawn image fits its column", () => {
  const list = declarationsFor(css, ".epic-page .run .artifacts");
  assert.match(list, /display:\s*grid;/);
  assert.match(list, /(?<![-\w])gap:\s*4px;/);
  const artifact = declarationsFor(css, ".epic-page .artifact");
  assert.match(artifact, /display:\s*flex;/);
  assert.match(artifact, /flex-wrap:\s*wrap;/);
  assert.match(declarationsFor(css, ".epic-page .artifact .path"), /overflow-wrap:\s*anywhere;/);
  const image = declarationsFor(css, ".epic-page .artifact-image");
  assert.match(image, /display:\s*block;/);
  assert.match(image, /max-width:\s*100%;/);
  assert.match(image, /height:\s*auto;/);
});

test("the evidence runs read the same inside the phase output as on the epic page", () => {
  const runRules = [
    ".runs",
    ".run",
    ".run .run-head",
    ".run .phase",
    ".run .pass.ok",
    ".run .log-toggle",
    ".run .pending",
    ".run .unavailable",
    ".run .artifacts",
    ".artifact",
    ".artifact .path",
    ".artifact-image",
    ".log",
  ];
  for (const rule of runRules) {
    const inPhaseOutput = declarationsFor(css, `.phase-output ${rule}`);
    assert.notEqual(inPhaseOutput, "", `.phase-output ${rule} has a rule`);
    assert.equal(inPhaseOutput, declarationsFor(css, `.epic-page ${rule}`), `.phase-output ${rule} matches the epic page`);
  }
});

test("no rule selects the old epic column or the old view", () => {
  assert.doesNotMatch(css, /\.side(?![\w-])/);
  assert.doesNotMatch(css, /\.epic-(view|head)\b/);
});

test("the meters line is one footer line in the footer's type and colour, with no box of its own", () => {
  const line = declarationsFor(css, ".lfoot button.meters");
  assert.match(line, /(?<![-\w])font:\s*inherit;/);
  assert.match(line, /(?<![-\w])color:\s*inherit;/);
  assert.match(line, /(?<![-\w])background:\s*none;/);
  assert.match(line, /(?<![-\w])border:\s*0;/);
});

test("the meters line does not wrap and is cut with an ellipsis when the footer is narrow", () => {
  const line = declarationsFor(css, ".lfoot button.meters");
  assert.match(line, /(?<![-\w])white-space:\s*nowrap;/);
  assert.match(line, /(?<![-\w])overflow:\s*hidden;/);
  assert.match(line, /(?<![-\w])text-overflow:\s*ellipsis;/);
  assert.match(line, /(?<![-\w])min-width:\s*0;/);
});

test("the meters' m cap is the footer's ? cap: no rule restyles a kbd inside the meters", () => {
  assert.doesNotMatch(css, /meters[^{]*\bkbd\b/);
});

test("the meters line spaces its host, stat and key cap apart", () => {
  assert.match(declarationsFor(css, ".lfoot button.meters > * + *"), /margin-left:\s*6px;/);
});

test("a panel row sets its label and value apart, with its bar across the row below them", () => {
  assert.match(declarationsFor(css, ".meter"), /grid-template-columns:\s*minmax\(0,\s*1fr\) auto;/);
  assert.match(declarationsFor(css, ".meter > .bar"), /grid-column:\s*1 \/ -1;/);
});

test("the meters panel is a mantle box with a surface1 ring", () => {
  const panel = declarationsFor(css, ".meters-panel");
  assert.match(panel, /(?<![-\w])background:\s*var\(--mantle\);/);
  assert.match(panel, /box-shadow:\s*0 0 0 1px var\(--surface1\);/);
});

test("from 760px the meters panel lies over the rows' row, so it takes no track and the footer keeps the last row", () => {
  const wide = mediaBlock(css, "@media (min-width: 760px) {");
  const panel = declarationsFor(wide, ".list > .meters-panel");
  assert.match(declarationsFor(wide, ".list"), /(?<![-\w])position:\s*relative;/, "the list holds the panel's grid area");
  assert.match(panel, /(?<![-\w])position:\s*absolute;/);
  assert.match(panel, /grid-row:\s*2;/);
  assert.match(panel, /align-self:\s*end;/);
});

test("a meter bar is a 2px fill: lavender, peach at level-warn and red at level-crit, each by its token", () => {
  assert.match(declarationsFor(css, ".bar"), /(?<![-\w])height:\s*2px;/);
  assert.match(declarationsFor(css, ".bar"), /(?<![-\w])background:\s*var\(--lavender\);/);
  assert.match(declarationsFor(css, ".bar.level-warn"), /(?<![-\w])background:\s*var\(--peach\);/);
  assert.match(declarationsFor(css, ".bar.level-crit"), /(?<![-\w])background:\s*var\(--red\);/);
});

test("the notice stack is absolute, offset from the top and right of its containing block", () => {
  const stack = declarationsFor(css, ".notices");
  assert.match(stack, /(?<![-\w])position:\s*absolute;/);
  assert.match(stack, /(?<![-\w])top:\s*\d+px;/);
  assert.match(stack, /(?<![-\w])right:\s*\d+px;/);
});

test(".panes is position relative, so it is the notice stack's containing block", () => {
  assert.match(declarationsFor(css, ".panes"), /(?<![-\w])position:\s*relative;/);
});

test("each notice row is one line: nowrap, and its title and body are cut with an ellipsis", () => {
  assert.match(declarationsFor(css, ".notices .notice-row"), /white-space:\s*nowrap;/);
  const open = declarationsFor(css, ".notices .notice");
  assert.match(open, /overflow:\s*hidden;/);
  assert.match(open, /white-space:\s*nowrap;/);
  assert.match(open, /text-overflow:\s*ellipsis;/);
});

test("the stack's rules are scoped to .notices, so the reader's own .notice keeps its rules", () => {
  assert.equal(declarationsFor(css, ".notice"), "");
});
