import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

// A rule of style.css: the selectors of its list, and its declarations by property, in source order. Comments are cut
// first. A rule inside an @media block is read as its own rule.
interface Rule {
  selectors: string[];
  declarations: Map<string, string>;
}

function withoutComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

function rulesOf(css: string): Rule[] {
  const rules: Rule[] = [];
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = match[1]
      .split(",")
      .map((selector) => selector.trim().replace(/\s+/g, " "))
      .filter((selector) => selector !== "");
    const declarations = new Map<string, string>();
    for (const declaration of match[2].split(";")) {
      const colon = declaration.indexOf(":");
      if (colon > 0) {
        declarations.set(declaration.slice(0, colon).trim(), declaration.slice(colon + 1).trim());
      }
    }
    rules.push({ selectors, declarations });
  }
  return rules;
}

// rulesUnder is the rules inside every @media block of the query, such as "(width < 760px)", as the phone reads them.
function rulesUnder(css: string, query: string): Rule[] {
  const opener = `@media ${query} {`;
  const rules: Rule[] = [];
  let from = css.indexOf(opener);
  while (from !== -1) {
    const start = from + opener.length;
    let depth = 1;
    let end = start;
    while (depth > 0) {
      if (css[end] === "{") {
        depth += 1;
      } else if (css[end] === "}") {
        depth -= 1;
      }
      end += 1;
    }
    rules.push(...rulesOf(css.slice(start, end - 1)));
    from = css.indexOf(opener, end);
  }
  return rules;
}

// declaredValue is the value a property takes from the last rule that names the selector, or undefined when none does.
function declaredValue(rules: Rule[], selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const rule of rules) {
    const declared = rule.declarations.get(property);
    if (declared !== undefined && rule.selectors.includes(selector)) {
      value = declared;
    }
  }
  return value;
}

const css = withoutComments(readFileSync(new URL("../src/style.css", import.meta.url), "utf8"));
const rules = rulesOf(css);
const phone = rulesUnder(css, "(width < 760px)");

// The tones a cell of a task's progress takes, as pips.tsx draws them. A cell not reached (none) takes the base of .pips i.
const pipTones = ["none", "plan", "run", "mine", "limited", "bad", "acc", "ok"];

test("EpicWords: pips' limited tone is grey and differs from mine", () => {
  const limited = declaredValue(rules, '.pips i[data-tone="limited"]', "background");
  const mine = declaredValue(rules, '.pips i[data-tone="mine"]', "background");
  assert.equal(limited, "var(--o-ink-3)");
  assert.notEqual(limited, mine);
});

test("EpicWords: every pip tone has a background", () => {
  for (const tone of pipTones) {
    const selector = tone === "none" ? ".pips i" : `.pips i[data-tone="${tone}"]`;
    assert.notEqual(declaredValue(rules, selector, "background"), undefined, `tone ${tone}`);
  }
});

test("EpicWords: the title row's t-quiet pill is grey", () => {
  assert.equal(declaredValue(rules, ".epic-page .title-row .t-quiet", "color"), "var(--o-ink-3)");
});

test("EpicWords: the chart's hl outline takes no pointer", () => {
  assert.equal(declaredValue(rules, ".orrery .hl", "pointer-events"), "none");
});

test("EpicWords: the chart's limited cells are grey, as the pips' are", () => {
  assert.equal(declaredValue(rules, ".orrery .cell.c-limited", "fill"), "var(--o-ink-3)");
});

test("EpicWords: the chart's limited cells are grey and differ from its mine cells, which take the attention peach", () => {
  const limited = declaredValue(rules, ".orrery .cell.c-limited", "fill");
  assert.equal(limited, "var(--o-ink-3)");
  assert.equal(declaredValue(rules, ".orrery .cell.c-mine", "fill"), "var(--o-tone-mine)");
  assert.notEqual(limited, declaredValue(rules, ".orrery .cell.c-mine", "fill"));
});

test("EpicWords: a task row's limited word is grey and differs from its held word, which takes the attention peach", () => {
  const limited = declaredValue(rules, ".t-limited", "--tone");
  assert.equal(limited, "var(--o-ink-3)");
  assert.equal(declaredValue(rules, ".t-held", "--tone"), "var(--o-role-attention)");
  assert.notEqual(limited, declaredValue(rules, ".t-held", "--tone"));
});

test("EpicRecord: a hidden inbox list is not drawn, as the author rule that sets .list's display would draw it", () => {
  assert.equal(declaredValue(rules, ".list[hidden]", "display"), "none");
});

test("EpicWords: no rule names .proof-card, .release-step or .epic-foot", () => {
  const named = rules.flatMap((rule) => rule.selectors).filter((selector) => /\.(proof-card|release-step|epic-foot)(?![\w-])/.test(selector));
  assert.deepEqual(named, []);
});

test("EpicWords: no .epic-page .now rule can match the track's now stage", () => {
  const named = rules
    .flatMap((rule) => rule.selectors)
    .filter((selector) => /^\.epic-page\s/.test(selector) && /(^|\s)\.now$/.test(selector));
  assert.deepEqual(named, []);
});

test("EpicWords: the Now section, not the track's now stage, takes the now grid", () => {
  assert.equal(declaredValue(rules, ".epic-page section.now", "display"), "grid");
});

test("EpicWords: the question form's option cards take the Now styles under .epic-page .now", () => {
  assert.equal(declaredValue(rules, ".epic-page .now .answer .opt", "display"), "grid");
});

test("EpicWords: a log's label is grey", () => {
  assert.equal(declaredValue(rules, ".epic-page .log .lbl", "color"), "var(--o-ink-3)");
});

test("EpicWords: a log's stderr label takes the halt role", () => {
  assert.equal(declaredValue(rules, ".epic-page .log .lbl.is-halt", "color"), "var(--o-role-halt)");
});

test("EpicWords: at phone width a table stacks to one column", () => {
  assert.equal(declaredValue(phone, ".tbl", "grid-template-columns"), "minmax(0, 1fr)");
});

test("EpicWords: at phone width a table's head row is hidden", () => {
  assert.equal(declaredValue(phone, ".tr.head", "display"), "none");
});

test("EpicWords: at phone width the track hides each stage's words and sets its names small", () => {
  assert.equal(declaredValue(phone, ".epic-page .stage .ds", "display"), "none");
  assert.equal(declaredValue(phone, ".epic-page .stage .nm", "font"), "var(--o-type-small)");
});

test("EpicWords: at phone width the fold line takes the touch height", () => {
  assert.equal(declaredValue(phone, ".fold-line", "min-height"), "var(--o-size-control-touch)");
});

test("EpicWords: at phone width the task table takes round 6's touch columns", () => {
  assert.equal(declaredValue(phone, ".tbl.tasks", "--cols-touch"), "minmax(0, 1fr) auto auto");
});

test("EpicWords: at phone width the requirement table takes round 6's touch columns", () => {
  assert.equal(declaredValue(phone, ".tbl.reqs", "--cols-touch"), "auto auto minmax(0, 1fr)");
});

test("EpicWords: at phone width the running steps table takes round 6's touch columns", () => {
  assert.equal(declaredValue(phone, ".tbl.steps", "--cols-touch"), "auto minmax(0, 1fr) auto");
});

test("EpicWords: at phone width a row's wide cell spans the row, so its pips keep the first line", () => {
  assert.equal(declaredValue(phone, ".tr .wide", "grid-column"), "1 / -1");
});

test("EpicWords: at phone width Just happened sets its time apart from its text", () => {
  assert.equal(declaredValue(phone, ".epic-page .happen .at", "margin-right"), "var(--o-space-12)");
});

test("EpicWords: at phone width the problem line wraps and does not ellipsis", () => {
  assert.equal(declaredValue(phone, ".problem", "white-space"), "normal");
  assert.equal(declaredValue(phone, ".problem", "text-overflow"), "clip");
});

test("EpicWords: at phone width the track's stages take the full column, so a name reads whole", () => {
  assert.equal(declaredValue(phone, ".epic-page .stage", "padding-right"), "0");
});

test("EpicWords: at phone width the track hides a done stage's check, which would cut its name", () => {
  assert.equal(declaredValue(phone, ".epic-page .stage .nm > span", "display"), "none");
});

test("EpicWords: at the desk Just happened sets its clock in a right-aligned gutter, a gap before its words", () => {
  const desk = rulesUnder(css, "(min-width: 760px)");
  assert.equal(declaredValue(desk, ".epic-page .happen li .at", "min-width"), "var(--o-layout-gutter-ch)");
  assert.equal(declaredValue(desk, ".epic-page .happen li .at", "margin-right"), "var(--o-space-16)");
  assert.equal(declaredValue(desk, ".epic-page .happen li .at", "text-align"), "right");
});

test("EpicWords: Resume now sits at its own width under Now's sentence, not across the section", () => {
  assert.equal(declaredValue(rules, ".epic-page section.now > .act", "justify-self"), "start");
});

test("EpicCommands: a confirm sheet's Stop is drawn in the bad role, apart from Keep it running", () => {
  assert.equal(declaredValue(rules, ".sheet .foot .act.danger", "color"), "var(--o-role-bad)");
});

test("EpicRecord: in the desk's panel the epic page's title row stays pinned at the card's top edge, under the panel's controls", () => {
  const desk = rulesUnder(css, "(min-width: 760px)");
  const row = ".rcard > section.page > .epic-page > .title-row";
  assert.equal(declaredValue(desk, row, "position"), "sticky");
  assert.equal(declaredValue(desk, row, "top"), "calc(-1 * var(--o-space-16))");
  assert.equal(declaredValue(desk, row, "padding-top"), "var(--o-space-16)");
  assert.equal(declaredValue(desk, row, "margin-top"), "calc(-1 * var(--o-space-16))");
  assert.equal(declaredValue(desk, row, "z-index"), "var(--o-z-sticky)");
  assert.equal(declaredValue(desk, row, "background"), "var(--o-surface-side)");
  assert.equal(declaredValue(rules, ".panel-ctl", "z-index"), "var(--o-z-flyout)", "the panel's controls sit above its pinned title row");
  assert.equal(declaredValue(desk, ".epic-page > .title-row", "position"), undefined, "no page-wide pin outside the panel");
});

test("EpicRecord: the Tasks section sets the chart beside the task table, wrapping the table under it when narrow", () => {
  assert.equal(declaredValue(rules, ".epic-page section.tasks", "display"), "flex");
  assert.equal(declaredValue(rules, ".epic-page section.tasks", "flex-wrap"), "wrap");
  assert.equal(declaredValue(rules, ".epic-page section.tasks > .sec-head", "flex"), "1 1 100%");
  assert.equal(declaredValue(rules, ".epic-page section.tasks > .orrery-fig", "flex"), "0 0 var(--o-size-orrery-full)");
  assert.equal(declaredValue(rules, ".epic-page section.tasks > .tbl", "flex"), "1 1 56ch");
});

test("EpicRecord: a door's label keeps one line and never shrinks, and no inherited wrap breaks it inside a word", () => {
  assert.equal(declaredValue(rules, ".epic-page .door > span:first-child", "white-space"), "nowrap");
  assert.equal(declaredValue(rules, ".epic-page .door > span:first-child", "flex"), "none");
  assert.equal(declaredValue(rules, ".epic-page .door", "overflow-wrap"), "normal");
});

test("EpicRecord: a door's line takes the rest of its row and ellipsizes on one line, and the door stays within its row", () => {
  assert.equal(declaredValue(rules, ".epic-page .door", "max-width"), "100%");
  assert.equal(declaredValue(rules, ".epic-page .door > span:not(:first-child)", "min-width"), "0");
  assert.equal(declaredValue(rules, ".epic-page .door > span:not(:first-child)", "overflow"), "hidden");
  assert.equal(declaredValue(rules, ".epic-page .door > span:not(:first-child)", "text-overflow"), "ellipsis");
  assert.equal(declaredValue(rules, ".epic-page .door > span:not(:first-child)", "white-space"), "nowrap");
});
