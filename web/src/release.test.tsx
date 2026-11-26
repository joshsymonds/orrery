import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import type { JSX } from "preact";

import type { ReleaseView } from "./ending";
import { ReleaseTable } from "./release";
import { installTestDom, removeTestDom } from "./testdom";

async function withRender(node: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(node, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

type Row = ReleaseView["rows"][number];

// row is one step of a release, drawn as the view gives it, with the parts of a step left empty unless given.
function row(overrides: Partial<Row> & Pick<Row, "position" | "step">): Row {
  return { target: "", undo: "rerun", state: "waiting", tone: "none", reason: "", evidence: "", runs: [], ...overrides };
}

// viewOf is a release view of rows, with its Then line when given.
function viewOf(rows: Row[], then = ""): ReleaseView {
  return { words: "", rows, then };
}

// stopped is a release stopped at its second step, which failed.
function stopped(): ReleaseView {
  return viewOf([
    row({
      position: 1,
      step: "Push main and tag v0.4.0, signed",
      target: "github, tag v0.4.0",
      undo: "none once someone has fetched the tag",
      state: "passed",
      tone: "ok",
    }),
    row({
      position: 2,
      step: "CI green on the tag",
      target: "github actions",
      undo: "none",
      state: "failed",
      tone: "bad",
      reason: "CI is red on the tag.",
    }),
    row({
      position: 3,
      step: "Publish the APK to the release page",
      target: "github release page",
      undo: "delete the release asset",
      state: "waiting",
    }),
  ]);
}

// rowOf reads the step row drawn for a position.
function rowOf(container: HTMLElement, position: number): HTMLElement {
  const found = container.querySelector<HTMLElement>(`.tr[data-step="${position}"]`);
  assert.ok(found !== null, `no row for step ${position}`);
  return found;
}

// detailOf reads the detail drawn under a step row, or null when the row draws none.
function detailOf(container: HTMLElement, position: number): HTMLElement | null {
  const next = rowOf(container, position).nextElementSibling;
  return next instanceof HTMLElement && next.classList.contains("tr-detail") ? next : null;
}

// textsOf reads the text of each element matched under root.
function textsOf(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll<HTMLElement>(selector)].map((element) => element.textContent ?? "");
}

// glyphOf reads a cell's glyph, which is empty for a cell with none.
function glyphOf(cell: HTMLElement): string {
  return cell.querySelector('[aria-hidden="true"]')?.textContent ?? "";
}

test("ReleaseTable: each row draws its position, step, target and undo", () =>
  withRender(<ReleaseTable view={stopped()} />, (container) => {
    const first = rowOf(container, 1);
    assert.equal(first.querySelector(".id")?.textContent, "1");
    assert.equal(first.querySelector(".wide .strong")?.textContent, "Push main and tag v0.4.0, signed");
    assert.equal(first.querySelector(".wide .to")?.textContent, " → github, tag v0.4.0");
    assert.equal(first.querySelector(".undo")?.textContent, "none once someone has fetched the tag");
    const third = rowOf(container, 3);
    assert.equal(third.querySelector(".wide .to")?.textContent, " → github release page");
    assert.equal(third.querySelector(".undo")?.textContent, "delete the release asset");
  }));

test("ReleaseTable: a step with no target draws no target", () =>
  withRender(
    <ReleaseTable view={viewOf([row({ position: 1, step: "Merge the epic branch to main", state: "passed", tone: "ok" })])} />,
    (container) => {
      assert.equal(rowOf(container, 1).querySelector(".to"), null);
      assert.equal(rowOf(container, 1).querySelector(".wide .strong")?.textContent, "Merge the epic branch to main");
    },
  ));

test("ReleaseTable: each state's cell draws its word, tone class and glyph", () =>
  withRender(
    <ReleaseTable
      view={viewOf([
        row({ position: 1, step: "a", state: "waiting", tone: "none" }),
        row({ position: 2, step: "b", state: "running", tone: "live" }),
        row({ position: 3, step: "c", state: "passed", tone: "ok" }),
        row({ position: 4, step: "d", state: "failed", tone: "bad" }),
      ])}
    />,
    (container) => {
      const expected: [number, string, string, string][] = [
        [1, "cell none", "waiting", ""],
        [2, "cell live", "running", ""],
        [3, "cell ok", "passed", "✓"],
        [4, "cell bad", "failed", "✗"],
      ];
      for (const [position, cls, word, glyph] of expected) {
        const cell = rowOf(container, position).querySelector<HTMLElement>(".end .cell");
        assert.ok(cell !== null, `no cell for step ${position}`);
        assert.equal(cell.getAttribute("class"), cls);
        assert.equal(glyphOf(cell), glyph);
        assert.equal(cell.textContent, `${glyph}${word}`);
      }
    },
  ));

test("ReleaseTable: a failed step draws its reason, evidence and each run under its row", () =>
  withRender(
    <ReleaseTable
      view={viewOf([
        row({
          position: 2,
          step: "CI green on the tag",
          undo: "rerun",
          state: "failed",
          tone: "bad",
          reason: "CI is red on the tag for the second time.",
          evidence: "run 5519",
          runs: ["attempt 1 · failed · go vet", "attempt 2 · passed"],
        }),
      ])}
    />,
    (container) => {
      const detail = detailOf(container, 2);
      assert.ok(detail !== null, "a step with a reason, evidence and runs draws its detail");
      assert.equal(detail.querySelector("p.inote.error")?.textContent, "CI is red on the tag for the second time.");
      assert.equal(detail.querySelector(".log")?.textContent, "run 5519");
      assert.deepEqual(textsOf(detail, "p.run"), ["attempt 1 · failed · go vet", "attempt 2 · passed"]);
    },
  ));

test("ReleaseTable: a step with no reason, evidence or run draws no detail", () =>
  withRender(
    <ReleaseTable
      view={viewOf([row({ position: 1, step: "Merge the epic branch to main", state: "passed", tone: "ok" })])}
    />,
    (container) => {
      assert.equal(detailOf(container, 1), null);
    },
  ));

test("ReleaseTable: the Then line draws under the table when there is one", async () => {
  const then = "Then halyard serves rota export, and the harbour office gets this week’s file.";
  await withRender(<ReleaseTable view={viewOf([row({ position: 1, step: "a" })], then)} />, (container) => {
    assert.equal(container.querySelector("p.inote.then")?.textContent, then);
  });
  await withRender(<ReleaseTable view={viewOf([row({ position: 1, step: "a" })])} />, (container) => {
    assert.equal(container.querySelector("p.inote.then"), null);
  });
});
