import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import type { ReviewView } from "./ending";
import { EpicReview } from "./review";
import { installTestDom, removeTestDom } from "./testdom";

// The synthetic review: made-up reviewers, findings and locations only.
const view: ReviewView = {
  words: "4 reviewers · 4 findings",
  reviewers: [
    { reviewer: "correctness", reason: "The diff holds to the contract.", verdict: "holds", tone: "ok" },
    { reviewer: "security", reason: "A token reaches the log.", verdict: "fails", tone: "bad" },
    { reviewer: "coverage", reason: "One branch has no test.", verdict: "gap", tone: "attention" },
    { reviewer: "style", reason: "Read in a mixed mode.", verdict: "skipped", tone: "quiet" },
  ],
  findings: [
    {
      id: "F1",
      what: "The token is logged",
      where: "web/src/log.ts:12",
      result: "confirmed",
      tone: "bad",
      reason: "The log line prints it.",
      closures: [
        { text: "round 1 · open", tone: "attention" },
        { text: "round 2 · closed", tone: "ok" },
      ],
    },
    { id: "F2", what: "A name is misspelled", where: "", result: "dropped", tone: "quiet", reason: "", closures: [] },
    {
      id: "F3",
      what: "An edge case is unchecked",
      where: "web/src/x.ts:4",
      result: "being verified",
      tone: "live",
      reason: "",
      closures: [],
    },
    {
      id: "F4",
      what: "The shutdown races",
      where: "",
      result: "left unverified",
      tone: "attention",
      reason: "The verifier stopped.",
      closures: [{ text: "round 1 · other", tone: "attention" }],
    },
  ],
};

// withReview gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withReview(check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the review into container, and flushes the render.
async function draw(container: HTMLElement, reviewView: ReviewView): Promise<void> {
  await act(() => {
    render(<EpicReview view={reviewView} />, container);
  });
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// textOf is the text of the first element under root that matches selector, which must be there.
function textOf(root: ParentNode, selector: string): string {
  return one(root, selector).textContent ?? "";
}

// reviewerRow is the reviewer row of name, which must be drawn.
function reviewerRow(container: HTMLElement, name: string): HTMLElement {
  return one(container, `.tbl.reviews .tr[data-reviewer="${name}"]`);
}

// findingRow is the finding row of id, which must be drawn.
function findingRow(container: HTMLElement, id: string): HTMLElement {
  return one(container, `.tbl.findings .tr[data-finding="${id}"]`);
}

// detailOf is the detail drawn under a finding row, which must be there.
function detailOf(row: HTMLElement): HTMLElement {
  const detail = row.nextElementSibling;
  assert.ok(detail instanceof HTMLElement && detail.classList.contains("tr-detail"), "no detail is drawn under the row");
  return detail;
}

test("EpicReview: the heading and the count words are drawn from the view", () =>
  withReview(async (container) => {
    await draw(container, view);
    const section = one(container, "section.review");
    assert.equal(section.getAttribute("aria-label"), "Review");
    assert.equal(textOf(section, ".sec > .name"), "Review");
    assert.equal(textOf(section, ".sec > .n"), view.words);
  }));

test("EpicReview: each reviewer row draws its name, its reason, and its verdict chip word and tone", () =>
  withReview(async (container) => {
    await draw(container, view);
    assert.equal(container.querySelectorAll(".tbl.reviews .tr[data-reviewer]").length, view.reviewers.length);
    for (const reviewer of view.reviewers) {
      const row = reviewerRow(container, reviewer.reviewer);
      assert.equal(textOf(row, ".strong"), reviewer.reviewer);
      assert.equal(textOf(row, ".wide"), reviewer.reason);
      const chip = one(row, ".end .chip");
      assert.equal(chip.textContent, reviewer.verdict);
      assert.equal(chip.className, `chip ${reviewer.tone}`);
    }
  }));

test("EpicReview: each finding row draws its id, what and where, and its result chip word and tone", () =>
  withReview(async (container) => {
    await draw(container, view);
    assert.equal(container.querySelectorAll(".tbl.findings .tr[data-finding]").length, view.findings.length);
    for (const finding of view.findings) {
      const row = findingRow(container, finding.id);
      assert.equal(textOf(row, ".id"), finding.id);
      const what = finding.where === "" ? finding.what : `${finding.what} · ${finding.where}`;
      assert.equal(textOf(row, ".wide"), what);
      const where = row.querySelector(".wide .where");
      if (finding.where === "") {
        assert.equal(where, null, `${finding.id} has no location to draw`);
      } else {
        assert.equal(where?.textContent, ` · ${finding.where}`);
      }
      const chip = one(row, ".end .chip");
      assert.equal(chip.textContent, finding.result);
      assert.equal(chip.className, `chip ${finding.tone}`);
    }
  }));

test("EpicReview: a finding with a reason or closures draws its detail under its row, the reason first, then its closures in round order", () =>
  withReview(async (container) => {
    await draw(container, view);
    const detail = detailOf(findingRow(container, "F1"));
    assert.equal(textOf(detail, "p.reason"), "The log line prints it.");
    assert.deepEqual(
      [...detail.children].map((child) => child.className),
      ["reason", "closure attention", "closure ok"],
      "the reason first, then the closures",
    );
    const closures = [...detail.querySelectorAll<HTMLElement>("p.closure")];
    assert.deepEqual(
      closures.map((closure) => [closure.textContent, closure.className]),
      [
        ["round 1 · open", "closure attention"],
        ["round 2 · closed", "closure ok"],
      ],
    );
  }));

test("EpicReview: a finding with no reason and no closures draws no detail, and one with only closures draws them alone", () =>
  withReview(async (container) => {
    await draw(container, view);
    assert.equal(container.querySelectorAll(".tr-detail").length, 2, "only F1 and F4 have a detail");
    const bare = findingRow(container, "F2").nextElementSibling;
    assert.ok(bare === null || !bare.classList.contains("tr-detail"), "F2 draws no detail under its row");
    const detail = detailOf(findingRow(container, "F4"));
    assert.equal(detail.querySelector("p.reason")?.textContent, "The verifier stopped.");
    assert.deepEqual(
      [...detail.querySelectorAll<HTMLElement>("p.closure")].map((closure) => closure.textContent),
      ["round 1 · other"],
    );
  }));

test("EpicReview: no reviewer table is drawn with no reviewers", () =>
  withReview(async (container) => {
    await draw(container, { ...view, words: "no verdict yet · 4 findings", reviewers: [] });
    assert.equal(container.querySelector(".tbl.reviews"), null);
    assert.ok(container.querySelector(".tbl.findings") !== null, "the findings table is still drawn");
  }));

test("EpicReview: no findings table is drawn with no findings", () =>
  withReview(async (container) => {
    await draw(container, { ...view, words: "4 reviewers · no findings", findings: [] });
    assert.equal(container.querySelector(".tbl.findings"), null);
    assert.ok(container.querySelector(".tbl.reviews") !== null, "the reviewer table is still drawn");
  }));
