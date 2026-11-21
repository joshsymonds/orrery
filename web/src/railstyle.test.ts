import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

// Each test's name starts with InboxRail. Each reads the declarations of style.css and tokens.css, as railaddress.test.tsx
// does: a rule by its exact selector with comments removed, a rule inside its @media block where a selector repeats, and a
// token by its one declaration.

const sheet = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const tokens = readFileSync(new URL("../src/tokens.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

// mediaBody is the body of the first @media block whose header is header, with its braces matched.
function mediaBody(header: string): string {
  const start = sheet.indexOf(`${header} {`);
  assert.ok(start >= 0, `style.css has a ${header} block`);
  const open = sheet.indexOf("{", start);
  let depth = 0;
  for (let i = open; i < sheet.length; i++) {
    if (sheet[i] === "{") {
      depth++;
    } else if (sheet[i] === "}") {
      depth--;
      if (depth === 0) {
        return sheet.slice(open + 1, i);
      }
    }
  }
  assert.fail(`style.css's ${header} block is not closed`);
}

// declarationsOf is the declarations of the one rule in scope whose selector is selector.
function declarationsOf(scope: string, selector: string): string {
  const rules = [...scope.matchAll(/([^{}]+)\{([^{}]*)\}/g)].filter(([, selectors]) => selectors.trim() === selector);
  assert.equal(rules.length, 1, `style.css has one ${selector} rule`);
  return rules[0][2];
}

// declared is the value of property in a rule's declarations, or undefined when the rule does not set it.
function declared(declarations: string, property: string): string | undefined {
  return declarations
    .split(";")
    .map((declaration) => declaration.trim())
    .find((declaration) => declaration.startsWith(`${property}:`))
    ?.slice(property.length + 1)
    .trim();
}

// tokenValue is the value of the token name in tokens.css, which declares each token once.
function tokenValue(name: string): string {
  const matches = [...tokens.matchAll(new RegExp(`^\\s*${name}:\\s*([^;]+);`, "gm"))];
  assert.equal(matches.length, 1, `tokens.css declares ${name} once`);
  return matches[0][1].trim();
}

const wide = "@media (min-width: 760px)";
const narrow = "@media (min-width: 760px) and (max-width: 1250px)";

test("InboxRail: the rail is 64 wide at the left edge, and collapsed the inbox's list is not drawn", () => {
  assert.equal(tokenValue("--o-layout-rail-width"), "64px");
  assert.equal(declared(declarationsOf(sheet, ".rail.start"), "width"), "var(--o-layout-rail-width)");
  assert.equal(declared(declarationsOf(mediaBody(wide), ".panes > .rail.start"), "grid-column"), "1");
  assert.equal(declared(declarationsOf(mediaBody(wide), '.panes[data-inbox="collapsed"] > .list'), "display"), "none");
});

test("InboxRail: collapsed, the inbox's track is the rail's 64, and the rail is shown in it", () => {
  const track = declarationsOf(mediaBody(wide), '.panes[data-inbox="collapsed"]');
  assert.equal(declared(track, "--inbox-track"), "var(--o-layout-rail-width)");
  assert.equal(declared(declarationsOf(mediaBody(wide), '.panes[data-inbox="collapsed"] > .rail.start'), "display"), "flex");
});

test("InboxRail: expanded, the inbox's track is 340 wide, and 316 at 1250 px and under", () => {
  assert.equal(declared(declarationsOf(mediaBody(wide), ".panes"), "--inbox-track"), "var(--o-layout-inbox-width)");
  assert.equal(tokenValue("--o-layout-inbox-width"), "340px");
  assert.equal(
    declared(declarationsOf(mediaBody(wide), ".panes.overlay"), "--inbox-track"),
    "var(--o-layout-inbox-width-narrow)",
  );
  assert.equal(tokenValue("--o-layout-inbox-width-narrow"), "316px");
});

test("InboxRail: no rule serves a retired inbox state, and nothing lays the inbox's list over the page", () => {
  const retired = [...sheet.matchAll(/data-inbox="([a-z]+)"/g)].map((match) => match[1]).filter((state) => state !== "collapsed");
  assert.deepEqual(retired, [], "only the collapsed state has rules of its own");
  const floating = [...sheet.matchAll(/([^{}]+)\{([^{}]*)\}/g)].filter(
    ([, selectors, body]) => /> \.list\b/.test(selectors) && declared(body, "position") === "absolute",
  );
  assert.deepEqual(floating.map(([, selectors]) => selectors.trim()), [], "no rule lays the list out of the grid");
});

test("InboxRail: every layout of the desk's columns takes the inbox's track as its first column", () => {
  // The track widths reach the page only through grid-template-columns, so each .panes layout must start with the track.
  const layouts = [...mediaBody(wide).matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter(([, selectors]) => selectors.trim().startsWith(".panes"))
    .map(([, selectors, body]) => ({ selector: selectors.trim(), columns: declared(body, "grid-template-columns") }))
    .filter((layout) => layout.columns !== undefined);
  assert.ok(layouts.some((layout) => layout.selector === ".panes"), "the desk's base layout sets its columns");
  for (const layout of layouts) {
    assert.ok(
      layout.columns?.startsWith("var(--inbox-track) "),
      `${layout.selector} starts its columns with the inbox's track: ${layout.columns}`,
    );
  }
});

test("InboxRail: collapsed, the rail's track wins over the narrow expanded track at 1250 px and under", () => {
  // The two rules are equally specific, so the later one wins: the rail's 64 must come after the overlay's 316.
  const body = mediaBody(wide);
  const overlay = body.indexOf(".panes.overlay { --inbox-track");
  const rail = body.indexOf('.panes[data-inbox="collapsed"] { --inbox-track');
  assert.ok(overlay >= 0 && rail >= 0, "both track rules are in the desk's block");
  assert.ok(rail > overlay, "the rail's track rule follows the overlay's");
});

test("InboxRail: the project tiles scroll in their own area between the mark and the spring, so the page never does", () => {
  const tiles = declarationsOf(sheet, ".rail-tiles");
  assert.equal(declared(tiles, "overflow-y"), "auto");
  assert.equal(declared(tiles, "min-height"), "0");
  assert.equal(declared(tiles, "flex"), "0 1 auto");
});

test("InboxRail: a project tile sets its name sideways in the place colour, ending in an ellipsis past its height", () => {
  const tile = declarationsOf(sheet, ".vtile");
  assert.equal(declared(tile, "color"), "var(--o-role-place)");
  assert.equal(declared(tile, "flex-direction"), "column");
  assert.equal(declared(tile, "width"), "calc(var(--o-space-32) + var(--o-space-8))");
  assert.equal(tokenValue("--o-role-place"), "var(--o-palette-sapphire)");
  const name = declarationsOf(sheet, ".vtile .vn");
  assert.equal(declared(name, "writing-mode"), "vertical-rl");
  assert.equal(declared(name, "transform"), "rotate(180deg)");
  assert.equal(declared(name, "max-height"), "calc(var(--o-space-48) * 2 + var(--o-space-16))");
  assert.equal(declared(name, "text-overflow"), "ellipsis");
  assert.equal(declared(name, "overflow"), "hidden");
});

test("InboxRail: a quiet project tile has no ground, a hairline outline, and its name in ink.3 at regular weight", () => {
  const quiet = declarationsOf(sheet, ".vtile.quiet");
  assert.equal(declared(quiet, "background"), "none");
  assert.equal(declared(quiet, "box-shadow"), "inset 0 0 0 var(--o-size-hairline) var(--o-line-soft)");
  assert.equal(declared(quiet, "color"), "var(--o-ink-3)");
  assert.equal(declared(quiet, "font-weight"), "var(--o-font-weight-regular)");
});

test("InboxRail: a project tile's dots stand in a column under its name, its live dot yellow and its new dot lavender", () => {
  assert.equal(declared(declarationsOf(sheet, ".vtile .vdots"), "flex-direction"), "column");
  assert.equal(declared(declarationsOf(sheet, ".vtile .vdots i.live"), "background"), "var(--o-role-live)");
  assert.equal(tokenValue("--o-role-live"), "var(--o-palette-yellow)");
  assert.equal(tokenValue("--o-palette-yellow"), "#f9e2af");
  assert.equal(declared(declarationsOf(sheet, ".vtile .vdots i.new"), "background"), "var(--o-role-new)");
  assert.equal(tokenValue("--o-role-new"), "var(--o-palette-lavender)");
  assert.equal(tokenValue("--o-palette-lavender"), "#b4befe");
});

test("ProjectFlyout lies beside the rail over the page, twelve 32s wide, and its rows scroll when they are long", () => {
  assert.equal(declared(declarationsOf(sheet, ".rail.start"), "position"), "relative");
  const fly = declarationsOf(sheet, ".pfly");
  assert.equal(declared(fly, "position"), "absolute");
  assert.equal(declared(fly, "left"), "calc(var(--o-layout-rail-width) + var(--o-space-8))");
  assert.equal(declared(fly, "width"), "calc(var(--o-space-32) * 12)");
  assert.equal(declared(fly, "max-height"), "100%");
  assert.equal(declared(fly, "z-index"), "var(--o-z-menu)");
  assert.equal(declared(declarationsOf(sheet, ".pf-rows"), "overflow-y"), "auto");
  assert.equal(declared(declarationsOf(sheet, ".pf-row .s.asks"), "color"), "var(--o-role-attention)");
});
