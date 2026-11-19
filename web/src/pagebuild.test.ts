import assert from "node:assert/strict";
import { test } from "node:test";

import { holdsUnsentText, type TextBoxes, watchPageBuild } from "./pagebuild";

// Page is the state a watch reads: whether the page is hidden and whether it holds unsent text, and how many times it
// was reloaded.
interface Page {
  hidden: boolean;
  unsent: boolean;
  reloads: number;
}

function watchOver(own: string, page: Page) {
  return watchPageBuild(
    own,
    () => page.hidden,
    () => page.unsent,
    () => {
      page.reloads++;
    },
  );
}

test("PageBuild: a served build equal to the page's own never reloads, hidden and empty or not", () => {
  const page: Page = { hidden: true, unsent: false, reloads: 0 };
  const watch = watchOver("build-a", page);
  watch.served("build-a");
  watch.check();
  assert.equal(page.reloads, 0);
});

test("PageBuild: a page or a serve that names no build is never stale", () => {
  for (const [own, served] of [
    ["", "build-b"],
    ["build-a", ""],
  ]) {
    const page: Page = { hidden: true, unsent: false, reloads: 0 };
    const watch = watchOver(own, page);
    watch.served(served);
    watch.check();
    assert.equal(page.reloads, 0, `own ${JSON.stringify(own)} served ${JSON.stringify(served)}`);
  }
});

test("PageBuild: a stale page that is hidden and holds no unsent text reloads at the answer, once", () => {
  const page: Page = { hidden: true, unsent: false, reloads: 0 };
  const watch = watchOver("build-a", page);
  watch.served("build-b");
  assert.equal(page.reloads, 1, "the answer that made it stale");
  watch.served("build-b");
  watch.check();
  assert.equal(page.reloads, 1, "a later answer and check");
});

test("PageBuild: a stale page waits while visible or holding unsent text, and reloads at the first check that finds neither", () => {
  const page: Page = { hidden: false, unsent: true, reloads: 0 };
  const watch = watchOver("build-a", page);
  watch.served("build-b");
  watch.check();
  assert.equal(page.reloads, 0, "visible with a draft");
  page.hidden = true;
  watch.check();
  assert.equal(page.reloads, 0, "hidden with a draft");
  page.hidden = false;
  page.unsent = false;
  watch.check();
  assert.equal(page.reloads, 0, "visible with no draft");
  page.hidden = true;
  watch.check();
  assert.equal(page.reloads, 1, "hidden with no draft");
});

test("PageBuild: a page stays stale when a later answer names its own build again", () => {
  const page: Page = { hidden: false, unsent: false, reloads: 0 };
  const watch = watchOver("build-a", page);
  watch.served("build-b");
  watch.served("build-a");
  page.hidden = true;
  watch.check();
  assert.equal(page.reloads, 1);
});

test("PageBuild: unsent text is any text box holding more than white space", () => {
  const boxes = (...values: string[]): TextBoxes => ({
    querySelectorAll: () => values.map((value) => ({ value })),
  });
  assert.equal(holdsUnsentText(boxes()), false, "no box");
  assert.equal(holdsUnsentText(boxes("", " \n\t")), false, "empty and blank boxes");
  assert.equal(holdsUnsentText(boxes("", "a draft")), true, "a draft");
});
