import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

// Each test's name starts with Shapes. Each reads style.css as text, with comments cut: a rule is its selector list and its
// declarations by property, in source order. A rule inside an @media block is read as its own rule, and rulesUnder returns
// the rules of the block whose query it names, as the desk and the phone read them.

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

// rulesUnder is the rules inside every @media block of the query, such as "(width < 760px)".
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

const sheet = withoutComments(readFileSync(new URL("../src/style.css", import.meta.url), "utf8"));
const rules = rulesOf(sheet);
const desk = rulesUnder(sheet, "(min-width: 760px)");
const phone = rulesUnder(sheet, "(width < 760px)");

// The transcript's shapes are scoped under the reader, and a Block is the element the Block component draws.
const block = '.reader [data-shape="block"]';

// The tone each Block takes, with the rail token it sets and the tint token it sets.
const blockTones: [string, string, string][] = [
  ["you", "var(--o-role-you)", "var(--o-role-you-fill)"],
  ["engine", "var(--o-role-engine)", "var(--o-role-engine-fill)"],
  ["ok", "var(--o-role-ok)", "var(--o-role-ok-fill)"],
  ["bad", "var(--o-role-bad)", "var(--o-role-bad-fill)"],
  ["attention", "var(--o-role-attention)", "var(--o-role-attention-fill)"],
];

test("Shapes: a Block has a 3 px rail in its rail token's colour, and its ground is its tint over the well", () => {
  assert.equal(declaredValue(rules, block, "border-left"), "var(--o-size-rail) solid var(--rail)");
  assert.equal(declaredValue(rules, block, "background"), "linear-gradient(var(--tint), var(--tint)), var(--ground)");
});

test("Shapes: each Block tone sets its rail colour token and its tint token", () => {
  for (const [tone, rail, tint] of blockTones) {
    assert.equal(declaredValue(rules, `${block}.${tone}`, "--rail"), rail, `tone ${tone} rail`);
    assert.equal(declaredValue(rules, `${block}.${tone}`, "--tint"), tint, `tone ${tone} tint`);
  }
  assert.equal(declaredValue(rules, `${block}.quiet`, "--rail"), "var(--o-ink-3)", "tone quiet rail");
});

test("Shapes: a Block's label is upper case in its rail's colour and never wraps inside a word", () => {
  const label = `${block} > .head .label`;
  assert.equal(declaredValue(rules, label, "text-transform"), "uppercase");
  assert.equal(declaredValue(rules, label, "color"), "var(--rail)");
  assert.equal(declaredValue(rules, label, "white-space"), "nowrap");
});

test("Shapes: a pending Block's rail is dashed", () => {
  assert.equal(declaredValue(rules, `${block}.pending`, "border-left-style"), "dashed");
});

test("Shapes: a Block's head is not a toolbar: no hairline under it, and its parts wrap at any width", () => {
  const head = `${block} > .head`;
  assert.equal(declaredValue(rules, head, "border-bottom"), "0");
  assert.equal(declaredValue(rules, head, "white-space"), "normal");
  assert.equal(declaredValue(rules, head, "flex-wrap"), "wrap");
});

test("Shapes: at the desk a Block's head hides its time, which the gutter draws", () => {
  assert.equal(declaredValue(desk, `${block} > .head .at`, "display"), "none");
  assert.equal(declaredValue(desk, ".reader .t > .gut", "display"), undefined);
});

test("Shapes: at phone width the gutter hides, and a Block's head draws its time", () => {
  assert.equal(declaredValue(phone, ".reader .t > .gut", "display"), "none");
  assert.equal(declaredValue(phone, `${block} > .head .at`, "display"), "inline");
});

test("Shapes: at phone width the head time follows the words, and only the head's end is pushed right", () => {
  assert.equal(declaredValue(phone, `${block} > .head .at`, "margin-left"), undefined);
  assert.equal(declaredValue(rules, `${block} > .head .end`, "margin-left"), "auto");
});

test("Shapes: every Line hangs on a 1 px hairline thread in the firm line colour, with no rail", () => {
  assert.equal(declaredValue(rules, ".reader .line", "border-left"), "var(--o-size-hairline) solid var(--o-line-firm)");
});

test("Shapes: a running line's thread is the live tone, drawn as a hairline", () => {
  assert.equal(declaredValue(rules, ".reader .line.running", "border-left"), "var(--o-size-hairline) solid var(--o-role-live)");
  assert.equal(
    declaredValue(rules, ".reader .line.run:has(> .fold.running)", "border-left"),
    "var(--o-size-hairline) solid var(--o-role-live)",
  );
});

test("Shapes: lines in a run touch: a line's row has no bottom padding before the next line", () => {
  assert.equal(declaredValue(rules, ".reader .t:has(> .body > .line):has(+ .t > .body > .line) > .body", "padding-bottom"), "0");
});

// The transcript's rules are the reader's, so a rule outside it, such as the notice stack's row, is no line or rule here.
test("Shapes: no .line or .rule rule sets a 3 px border or shadow, only a Block's rail is 3 px", () => {
  const lineOrRule = /\.(line|rule|call|fold|thinking|notice|task|compaction)(?![\w-])/;
  const railed = rules.filter(
    (rule) =>
      rule.selectors.some((selector) => selector.startsWith(".reader") && lineOrRule.test(selector)) &&
      ["border", "border-left", "box-shadow"].some((property) =>
        (rule.declarations.get(property) ?? "").includes("--o-size-rail"),
      ),
  );
  assert.deepEqual(
    railed.map((rule) => rule.selectors),
    [],
  );
});

test("Shapes: a quote's bar in a reply or a Block is 3 px in the prose marker, as round 6 draws it", () => {
  assert.equal(declaredValue(rules, ".reader .reply blockquote", "border-left"), "var(--o-size-rail) solid var(--o-prose-marker)");
  assert.equal(declaredValue(rules, `${block} blockquote`, "border-left"), "var(--o-size-rail) solid var(--o-prose-marker)");
});

test("Shapes: a Rule's label is upper case between two hairlines that span the rule", () => {
  assert.equal(declaredValue(rules, ".reader .rule .label", "text-transform"), "uppercase");
  assert.equal(declaredValue(rules, ".reader .rule::before", "background"), "currentColor");
  assert.equal(declaredValue(rules, ".reader .rule::after", "background"), "currentColor");
});

test("Shapes: a compaction or a day Rule draws its hairlines in the soft line, as round 6's plain rule does", () => {
  for (const kind of ["compaction", "day"]) {
    assert.equal(declaredValue(rules, `.reader .rule.${kind}::before`, "background"), "var(--o-line-soft)", `${kind} before`);
    assert.equal(declaredValue(rules, `.reader .rule.${kind}::after`, "background"), "var(--o-line-soft)", `${kind} after`);
  }
});

test("Shapes: a Rule's row spans the column with no gutter time, a compaction's row included", () => {
  for (const row of [".reader .t:has(> .body > .rule)", ".reader .t:has(> .body > .compaction > .rule)"]) {
    assert.equal(declaredValue(rules, row, "grid-template-columns"), "minmax(0, 1fr)", `${row} columns`);
    assert.equal(declaredValue(rules, `${row} > .gut`, "display"), "none", `${row} gutter`);
  }
});

test("Shapes: a step end's facts wrap in a row with gaps, +N in the ok tone and −N in the bad tone", () => {
  const facts = ".reader .facts";
  assert.equal(declaredValue(rules, facts, "display"), "flex");
  assert.equal(declaredValue(rules, facts, "flex-wrap"), "wrap");
  assert.equal(declaredValue(rules, facts, "color"), "var(--o-ink-3)");
  assert.equal(declaredValue(rules, `${facts} > span`, "display"), "inline-flex");
  assert.equal(declaredValue(rules, `${facts} .plus`, "color"), "var(--o-role-ok)");
  assert.equal(declaredValue(rules, `${facts} .minus`, "color"), "var(--o-role-bad)");
});

test("Shapes: a chip is neutral unless its tone is ok, bad or quiet, and the quiet tone is ink", () => {
  assert.equal(declaredValue(rules, ".chip", "background"), "var(--o-surface-raised)");
  assert.equal(declaredValue(rules, ".chip", "color"), "var(--o-ink-2)");
  assert.equal(declaredValue(rules, ".reader .chip", "color"), undefined);
  assert.equal(declaredValue(rules, ".reader .chip", "background"), undefined);
  assert.equal(declaredValue(rules, ".reader .chip.ok", "color"), "var(--o-role-ok)");
  assert.equal(declaredValue(rules, ".reader .chip.ok", "background"), "var(--o-role-ok-fill)");
  assert.equal(declaredValue(rules, ".chip.bad", "color"), "var(--o-role-bad)");
  assert.equal(declaredValue(rules, ".reader .chip.quiet", "color"), "var(--o-ink-3)");
});

test("Shapes: a fold resets its whole border, so a run's button draws no border of its own", () => {
  assert.equal(declaredValue(rules, ".reader .fold", "border"), "0");
});

test("Shapes: the Reply is plain, with no ground, border or padding around it", () => {
  assert.equal(declaredValue(rules, ".reader .reply", "background"), undefined);
  assert.equal(declaredValue(rules, ".reader .reply", "border"), undefined);
  assert.equal(declaredValue(rules, ".reader .reply", "padding"), undefined);
});

test("Shapes: a Markdown heading is no larger than the title size", () => {
  const sizes = ["var(--o-type-title)", "var(--o-type-body)"];
  for (const heading of ["h1", "h2", "h3", "h4", "h5", "h6"]) {
    const font = declaredValue(rules, `.reader .reply ${heading}`, "font");
    assert.ok(font !== undefined && sizes.includes(font), `heading ${heading} takes title or body: ${font}`);
  }
});

test("Shapes: a code block and a table scroll inside the column, in a reply and in a Block's body", () => {
  for (const scope of [".reader .reply", block]) {
    assert.equal(declaredValue(rules, `${scope} pre`, "overflow-x"), "auto", `${scope} pre`);
    assert.equal(declaredValue(rules, `${scope} table`, "overflow-x"), "auto", `${scope} table`);
    assert.equal(declaredValue(rules, `${scope} table`, "display"), "block", `${scope} table`);
  }
});

test("Shapes: inline code sits sunk in the well, in a reply and in a Block's body", () => {
  assert.equal(declaredValue(rules, ".reader .reply code", "background"), "var(--o-surface-well)");
  assert.equal(declaredValue(rules, `${block} code`, "background"), "var(--o-surface-well)");
});

test("Shapes: no rule targets a class no markup draws, the transcript's retired shapes are gone", () => {
  const retired = [
    "background-row",
    "bg-status",
    "bg-activity",
    "bg-description",
    "fold-text",
    "thinking-text",
    "cron",
    "chips",
    "card",
    "card-head",
    "card-controls",
    "open-epic",
    "unavailable",
    "track",
    "part",
    "now-sentence",
    "reqs",
    "rid",
    "question",
    "agent-finished",
    "step-agent",
    "step-reason",
    "settled",
    "ring-icon.question",
    "ring-icon.park",
    "ring-icon.release",
    "ring-icon.end",
    "gut.new",
    "gut.sending",
  ];
  const reader = rules.flatMap((rule) => rule.selectors).filter((selector) => /\.reader\b/.test(selector));
  const named = retired.filter((name) => {
    const token = new RegExp(`\\.${name.replace(".", "\\.")}(?![\\w-])`);
    return reader.some((selector) => token.test(selector));
  });
  assert.deepEqual(named, []);
});

test("Shapes: the owner's, the event's and the cron's old blocks are gone, and a wrapper beside a Block draws no rail", () => {
  const selectors = rules.flatMap((rule) => rule.selectors);
  for (const selector of [".reader .you", ".reader .you small", ".reader .event", ".reader .cron", ".reader .card"]) {
    assert.ok(!selectors.includes(selector), `${selector} is retired`);
  }
  for (const selector of [".reader .step-card", ".reader .epic-card"]) {
    assert.ok(
      !(declaredValue(rules, selector, "box-shadow") ?? "").includes("--o-size-rail"),
      `${selector} draws no rail beside its Block`,
    );
  }
});

test("Shapes: ContractBlock keeps its own rail, tint and ground, which no Block rule reaches", () => {
  assert.equal(declaredValue(rules, ".block", "border-left"), "var(--o-size-rail) solid var(--rail)");
  assert.equal(declaredValue(rules, ".block", "background"), "linear-gradient(var(--tint), var(--tint)), var(--ground)");
  assert.equal(declaredValue(rules, ".block.attention", "--rail"), "var(--o-role-attention)");
  assert.equal(declaredValue(rules, ".block.ok", "--rail"), "var(--o-role-ok)");
  assert.equal(declaredValue(rules, ".block.quiet", "--rail"), "var(--o-ink-3)");
});
