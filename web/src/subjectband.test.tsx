import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { type BandLine, SubjectBand } from "./subjectband";
import { installTestDom, removeTestDom } from "./testdom";

// withBand renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withBand(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function countOf(root: ParentNode, selector: string): number {
  return root.querySelectorAll(selector).length;
}

// line is an ask line of the fixtures: its epic's id, slug, text and way.
const line = (epic: string, slug: string, text: string, way: BandLine["way"]): BandLine => ({ epic, slug, text, way });

const threeLines: BandLine[] = [
  line("ep-rota", "rota-export", "asks: Bump the embedded tz database?", "Answer"),
  line("ep-orbit", "orbit-labels", "is parked: R5 is already met at the epic’s base", "Decide"),
  line("ep-notices", "notices-usable", "release step 2 (CI green on the tag) failed: runner lost", "Open"),
];

test("Band: goal alone draws only the goal line", async () => {
  await withBand(<SubjectBand goal="Every port’s tide table is cached." lines={[]} onOpen={() => {}} />, (container) => {
    assert.equal(countOf(container, ".subj"), 1);
    assert.equal(textOf(container, ".subj.goal .what"), "Goal");
    assert.equal(textOf(container, ".subj.goal .st"), "Every port’s tide table is cached.");
    assert.equal(countOf(container, "button.subj"), 0);
    assert.equal(countOf(container, ".more"), 0);
  });
});

test("Band: no goal and no lines draws nothing", async () => {
  await withBand(<SubjectBand goal="" lines={[]} onOpen={() => {}} />, (container) => {
    assert.equal(container.innerHTML, "");
  });
});

test("Band: each ask line draws its slug, text and way, and a click opens that epic", async () => {
  const opened: string[] = [];
  await withBand(
    <SubjectBand
      goal="Every port’s tide table is cached."
      lines={threeLines}
      onOpen={(epic) => {
        opened.push(epic);
      }}
    />,
    (container) => {
      const buttons = [...container.querySelectorAll<HTMLButtonElement>("button.subj.attention")];
      assert.deepEqual(
        buttons.map((button) => textOf(button, ".what b")),
        ["rota-export", "orbit-labels", "notices-usable"],
      );
      assert.deepEqual(
        buttons.map((button) => textOf(button, ".st")),
        threeLines.map((ask) => ask.text),
      );
      assert.deepEqual(
        buttons.map((button) => textOf(button, ".end")),
        ["Answer ›", "Decide ›", "Open ›"],
      );
      buttons[1]?.click();
      assert.deepEqual(opened, ["ep-orbit"]);
      buttons[0]?.click();
      assert.deepEqual(opened, ["ep-orbit", "ep-rota"]);
    },
  );
});

test("Band: four lines draw three and name the fourth in and 1 more ask", async () => {
  const four = [...threeLines, line("ep-tide", "tide-alerts", "Read and decide", "Answer")];
  await withBand(<SubjectBand goal="" lines={four} onOpen={() => {}} />, (container) => {
    assert.equal(countOf(container, "button.subj"), 3);
    assert.equal(textOf(container, ".more .what"), "and 1 more ask");
    assert.equal(textOf(container, ".more .st"), "tide-alerts");
    assert.equal(container.querySelector('[data-epic="ep-tide"]'), null);
  });
});

test("Band: the more line joins every slug past the third with a comma", async () => {
  const five = [
    ...threeLines,
    line("ep-tide", "tide-alerts", "Read and decide", "Answer"),
    line("ep-labels", "labels-cache", "Open", "Open"),
  ];
  await withBand(<SubjectBand goal="" lines={five} onOpen={() => {}} />, (container) => {
    assert.equal(textOf(container, ".more .what"), "and 2 more ask");
    assert.equal(textOf(container, ".more .st"), "tide-alerts, labels-cache");
  });
});

test("Band: with no goal the lines draw and there is no hide control", async () => {
  await withBand(<SubjectBand goal="" lines={threeLines} onOpen={() => {}} onHide={() => {}} />, (container) => {
    assert.equal(countOf(container, "button.subj"), 3);
    assert.equal(countOf(container, ".subj.goal"), 0);
    assert.equal(container.querySelector('[aria-label="Hide the header"]'), null);
  });
});

test("Band: the hide control is labelled Hide the header and calls onHide", async () => {
  let hides = 0;
  await withBand(
    <SubjectBand
      goal="Every port’s tide table is cached."
      lines={threeLines}
      onOpen={() => {}}
      onHide={() => {
        hides += 1;
      }}
    />,
    (container) => {
      const hide = container.querySelector<HTMLButtonElement>(".subj.goal .fold-head");
      assert.equal(hide?.getAttribute("aria-label") ?? null, "Hide the header");
      assert.equal(hide?.getAttribute("title") ?? null, "Hide the header");
      hide?.click();
      assert.equal(hides, 1);
    },
  );
});

test("Band: the hide control is drawn only when onHide is given", async () => {
  await withBand(<SubjectBand goal="Every port’s tide table is cached." lines={[]} onOpen={() => {}} />, (container) => {
    assert.equal(container.querySelector(".fold-head"), null);
  });
});
