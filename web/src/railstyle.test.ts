import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { createElement, render } from "preact";
import { act } from "preact/test-utils";

import { InboxRail } from "./inboxrail";
import type { RailTileModel } from "./railtiles";
import { installTestDom, removeTestDom } from "./testdom";

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

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

test("InboxRail: the rail is 64 wide at the left edge, and at rest the inbox's list is hidden", () => {
  assert.equal(tokenValue("--o-layout-rail-width"), "64px");
  assert.equal(declared(declarationsOf(sheet, ".rail.start"), "width"), "var(--o-layout-rail-width)");
  assert.equal(declared(declarationsOf(mediaBody(wide), ".panes > .rail.start"), "grid-column"), "1");
  assert.equal(declared(declarationsOf(mediaBody(wide), '.panes[data-inbox="rail"] > .list'), "display"), "none");
});

test("InboxRail: out, the rail keeps its 64 wide track at the left edge, and it is still shown", () => {
  const track = declarationsOf(mediaBody(wide), '.panes[data-inbox="rail"], .panes[data-inbox="flyout"]');
  assert.equal(declared(track, "--inbox-track"), "var(--o-layout-rail-width)");
  const shown = declarationsOf(
    mediaBody(wide),
    '.panes[data-inbox="rail"] > .rail.start, .panes[data-inbox="flyout"] > .rail.start',
  );
  assert.equal(declared(shown, "display"), "flex");
});

test("InboxRail: out, the inbox's list lies over the page beside the rail, 340 wide", () => {
  const list = declarationsOf(mediaBody(wide), '.panes[data-inbox="flyout"] > .list');
  assert.equal(declared(list, "position"), "absolute");
  assert.equal(declared(list, "left"), "var(--o-layout-rail-width)");
  assert.equal(declared(list, "width"), "var(--o-layout-inbox-width)");
  assert.equal(tokenValue("--o-layout-inbox-width"), "340px");
});

test("InboxRail: out at 1250 px and under, the inbox's list is 316 wide", () => {
  const list = declarationsOf(mediaBody(narrow), '.panes[data-inbox="flyout"] > .list');
  assert.equal(declared(list, "width"), "var(--o-layout-inbox-width-narrow)");
  assert.equal(tokenValue("--o-layout-inbox-width-narrow"), "316px");
});

test("InboxRail: pinned, the inbox's track is 340 wide, and 316 at 1250 px and under", () => {
  assert.equal(declared(declarationsOf(mediaBody(wide), ".panes"), "--inbox-track"), "var(--o-layout-inbox-width)");
  assert.equal(
    declared(declarationsOf(mediaBody(wide), ".panes.overlay"), "--inbox-track"),
    "var(--o-layout-inbox-width-narrow)",
  );
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

test("InboxRail: at rest and out, the rail's track wins over the narrow pinned track at 1250 px and under", () => {
  // The two rules are equally specific, so the later one wins: the rail's 64 must come after the overlay's 316.
  const body = mediaBody(wide);
  const overlay = body.indexOf(".panes.overlay { --inbox-track");
  const rail = body.indexOf('.panes[data-inbox="rail"], .panes[data-inbox="flyout"] { --inbox-track');
  assert.ok(overlay >= 0 && rail >= 0, "both track rules are in the desk's block");
  assert.ok(rail > overlay, "the rail's track rule follows the overlay's");
});

test("InboxRail: a project tile is the place colour, its live dot yellow and its new dot lavender", () => {
  assert.equal(declared(declarationsOf(sheet, ".tile.place"), "color"), "var(--o-role-place)");
  assert.equal(tokenValue("--o-role-place"), "var(--o-palette-sapphire)");
  assert.equal(declared(declarationsOf(sheet, ".tile .dots i.live"), "background"), "var(--o-role-live)");
  assert.equal(tokenValue("--o-role-live"), "var(--o-palette-yellow)");
  assert.equal(tokenValue("--o-palette-yellow"), "#f9e2af");
  assert.equal(declared(declarationsOf(sheet, ".tile .dots i.new"), "background"), "var(--o-role-new)");
  assert.equal(tokenValue("--o-role-new"), "var(--o-palette-lavender)");
  assert.equal(tokenValue("--o-palette-lavender"), "#b4befe");
});

test("InboxRail: a project tile renders with class place", async () => {
  await withDom((container) => {
    const noop = () => {};
    const tiles: RailTileModel[] = [
      { key: "tidepool", text: "ti", n: 0, live: false, fresh: false, cur: false, title: "tidepool" },
    ];
    act(() => {
      render(
        createElement(InboxRail, {
          tiles,
          onMark: noop,
          onTile: noop,
          onNew: noop,
          onSearch: noop,
          onKeys: noop,
          onEnter: noop,
          onLeave: noop,
        }),
        container,
      );
    });
    const tile = container.querySelector<HTMLElement>('button[aria-label="tidepool"]');
    assert.ok(tile !== null, "the project tile is drawn");
    assert.equal(tile.classList.contains("place"), true);
  });
});
