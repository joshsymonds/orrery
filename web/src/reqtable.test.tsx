import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import type { JSX } from "preact";
import { act } from "preact/test-utils";

import type { LoadArtifact } from "./evidence";
import { type EpicRequirement, EpicRequirementSchema, type EvidenceRun, EvidenceRunSchema } from "./gen/orrery/v1/epics_pb";
import { RequirementTable } from "./reqtable";
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

// noLoad answers a log read that no test here opens; a read that happens fails the test.
const noLoad: LoadArtifact = () => Promise.reject(new Error("no log is read in these tests"));

// run is one evidence run of a phase, created at the given time, ended or still going, passed or not, with its exit.
function run(
  phase: string,
  created: bigint,
  passed: boolean,
  state = "ended",
  exit = 0,
): EvidenceRun {
  const init: MessageInitShape<typeof EvidenceRunSchema> = {
    id: `${phase}-${created}`,
    phase,
    source: "captured",
    state,
    passed,
    exit,
    created,
  };
  return create(EvidenceRunSchema, init);
}

// requirement is one requirement of a kind, with its runs in the order given.
function requirement(id: string, kind: string, runs: EvidenceRun[]): EpicRequirement {
  return create(EpicRequirementSchema, { id, text: `${id} says what is true`, kind, runs });
}

// rowsOf reads the requirement rows the table draws, each a button.
function rowsOf(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>("button.tr"));
}

// cellsOf reads a row's red and green proof cells, in that order.
function cellsOf(row: HTMLElement): HTMLElement[] {
  return Array.from(row.querySelectorAll<HTMLElement>("span.cell"));
}

// glyphOf reads a cell's glyph, which is empty for a cell with none.
function glyphOf(cell: HTMLElement): string {
  return cell.querySelector('[aria-hidden="true"]')?.textContent ?? "";
}

// wordOf reads a cell's words, which follow its glyph.
function wordOf(cell: HTMLElement): string {
  return (cell.textContent ?? "").slice(glyphOf(cell).length);
}

// assertCell checks a cell's tone, glyph and words.
function assertCell(cell: HTMLElement, tone: string, glyph: string, word: string): void {
  assert.equal(cell.getAttribute("class"), `cell ${tone}`);
  assert.equal(glyphOf(cell), glyph);
  assert.equal(wordOf(cell), word);
}

// endOf reads a row's proven or open word.
function endOf(row: HTMLElement): string | null | undefined {
  return row.querySelector("span.end")?.textContent;
}

test("EpicProof: each row draws its id, its text and two proof cells", () =>
  withRender(
    <RequirementTable
      requirements={[requirement("R1", "check", []), requirement("R2", "review", [])]}
      loadArtifact={noLoad}
    />,
    (container) => {
      const rows = rowsOf(container);
      assert.equal(rows.length, 2);
      assert.equal(rows[0]?.querySelector("span.id")?.textContent, "R1");
      assert.equal(rows[0]?.querySelector("span.strong")?.textContent, "R1 says what is true");
      assert.equal(cellsOf(rows[0] as HTMLElement).length, 2);
      assert.equal(rows[1]?.querySelector("span.id")?.textContent, "R2");
      assert.equal(rows[1]?.querySelector("span.strong")?.textContent, "R2 says what is true");
      assert.equal(cellsOf(rows[1] as HTMLElement).length, 2);
    },
  ));

test("EpicProof: a check with no run reads no test yet and not yet, a review with no run reads by review in both", () =>
  withRender(
    <RequirementTable
      requirements={[requirement("R1", "check", []), requirement("R2", "review", [])]}
      loadArtifact={noLoad}
    />,
    (container) => {
      const [check, review] = rowsOf(container) as [HTMLElement, HTMLElement];
      const [checkRed, checkGreen] = cellsOf(check);
      assertCell(checkRed as HTMLElement, "none", "", "no test yet");
      assertCell(checkGreen as HTMLElement, "none", "", "not yet");
      const [reviewRed, reviewGreen] = cellsOf(review);
      assertCell(reviewRed as HTMLElement, "none", "", "by review");
      assertCell(reviewGreen as HTMLElement, "none", "", "by review");
    },
  ));

test("EpicProof: a run not yet ended reads running in its phase's cell, live with no glyph", () =>
  withRender(
    <RequirementTable
      requirements={[
        requirement("R1", "check", [
          run("red", 1n, false, "running"),
          run("green", 2n, false, "running"),
        ]),
      ]}
      loadArtifact={noLoad}
    />,
    (container) => {
      const [red, green] = cellsOf(rowsOf(container)[0] as HTMLElement);
      assertCell(red as HTMLElement, "live", "", "running");
      assertCell(green as HTMLElement, "live", "", "running");
    },
  ));

test("EpicProof: an ended red run reads fails at base when it passed and passes at base when it did not", () =>
  withRender(
    <RequirementTable
      requirements={[
        requirement("R1", "check", [run("red", 1n, true)]),
        requirement("R2", "check", [run("red", 1n, false)]),
      ]}
      loadArtifact={noLoad}
    />,
    (container) => {
      const [passed, notPassed] = rowsOf(container);
      assertCell(cellsOf(passed as HTMLElement)[0] as HTMLElement, "ok", "✓", "fails at base");
      assertCell(cellsOf(notPassed as HTMLElement)[0] as HTMLElement, "bad", "✗", "passes at base");
    },
  ));

test("EpicProof: an ended green run reads passes when it passed and fails when it did not", () =>
  withRender(
    <RequirementTable
      requirements={[
        requirement("R1", "check", [run("green", 1n, true)]),
        requirement("R2", "check", [run("green", 1n, false)]),
      ]}
      loadArtifact={noLoad}
    />,
    (container) => {
      const [passed, notPassed] = rowsOf(container);
      assertCell(cellsOf(passed as HTMLElement)[1] as HTMLElement, "ok", "✓", "passes");
      assertCell(cellsOf(notPassed as HTMLElement)[1] as HTMLElement, "bad", "✗", "fails");
    },
  ));

test("EpicProof: the newest run of a phase decides its cell, whatever order the runs are in", () =>
  withRender(
    <RequirementTable
      requirements={[
        requirement("R1", "check", [
          run("red", 2n, false),
          run("red", 1n, true),
          run("green", 3n, true),
          run("green", 4n, false),
        ]),
      ]}
      loadArtifact={noLoad}
    />,
    (container) => {
      const [red, green] = cellsOf(rowsOf(container)[0] as HTMLElement);
      assertCell(red as HTMLElement, "bad", "✗", "passes at base");
      assertCell(green as HTMLElement, "bad", "✗", "fails");
    },
  ));

test("EpicProof: a row is proven when a green run passed, and open when only a red run passed", () =>
  withRender(
    <RequirementTable
      requirements={[
        requirement("R1", "check", [run("green", 1n, true)]),
        requirement("R2", "check", [run("red", 1n, true)]),
      ]}
      loadArtifact={noLoad}
    />,
    (container) => {
      const [proven, open] = rowsOf(container);
      assert.equal(endOf(proven as HTMLElement), "proven");
      assert.equal(endOf(open as HTMLElement), "open");
    },
  ));

test("EpicProof: a row opens its runs red first then green, each phase in created order, and closes again", () =>
  withRender(
    <RequirementTable
      requirements={[
        requirement("R1", "check", [
          run("green", 4n, true, "ended", 14),
          run("red", 2n, true, "ended", 12),
          run("green", 3n, false, "ended", 13),
          run("red", 1n, true, "ended", 11),
        ]),
      ]}
      loadArtifact={noLoad}
    />,
    (container) => {
      const row = rowsOf(container)[0] as HTMLElement;
      assert.equal(row.getAttribute("aria-expanded"), "false");
      assert.equal(container.querySelector(".tr-detail"), null);

      act(() => {
        row.click();
      });
      assert.equal(row.getAttribute("aria-expanded"), "true");
      const detail = container.querySelector(".tr-detail");
      assert.ok(detail, "the row opens its runs under it");
      assert.equal(detail.previousElementSibling, row);
      assert.deepEqual(
        Array.from(detail.querySelectorAll("li.run .sentence"), (sentence) => sentence.textContent),
        [
          "Red run: the test fails before any change, as it should.",
          "Red run: the test fails before any change, as it should.",
          "Green run: the test still fails on the change.",
          "Green run: the test passes on the change.",
        ],
      );
      assert.deepEqual(
        Array.from(detail.querySelectorAll("li.run .meta"), (meta) => meta.textContent),
        ["captured · exit 11", "captured · exit 12", "captured · exit 13", "captured · exit 14"],
      );

      act(() => {
        row.click();
      });
      assert.equal(row.getAttribute("aria-expanded"), "false");
      assert.equal(container.querySelector(".tr-detail"), null);
    },
  ));

test("EpicProof: initiallyOpen draws the named row open and no other", () =>
  withRender(
    <RequirementTable
      requirements={[
        requirement("R1", "check", [run("red", 1n, true)]),
        requirement("R2", "check", [run("red", 1n, true)]),
      ]}
      loadArtifact={noLoad}
      initiallyOpen="R2"
    />,
    (container) => {
      const [first, second] = rowsOf(container);
      assert.equal(first?.getAttribute("aria-expanded"), "false");
      assert.equal(second?.getAttribute("aria-expanded"), "true");
      const details = container.querySelectorAll(".tr-detail");
      assert.equal(details.length, 1);
      assert.equal(details[0]?.previousElementSibling, second);
    },
  ));
