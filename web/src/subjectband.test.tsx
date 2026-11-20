import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { type BandLine, type ContractBandLine, SubjectBand } from "./subjectband";
import { assertNone, installTestDom, removeTestDom } from "./testdom";

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

test("OpenOnRight: a line whose epic is not open in the panel offers its way in, a button that opens its epic", async () => {
  const opened: string[] = [];
  await withBand(
    <SubjectBand goal="" lines={threeLines} onOpen={(epic) => opened.push(epic)} here="ep-orbit" />,
    (container) => {
      const rota = container.querySelector<HTMLButtonElement>('button.subj[data-epic="ep-rota"]');
      assert.ok(rota !== null, "the rota-export line is a button");
      assert.equal(textOf(rota, ".end"), "Answer ›");
      rota.click();
      assert.deepEqual(opened, ["ep-rota"]);
    },
  );
});

test("OpenOnRight: the line of the epic open in the panel says it is open on the right, with no button and no way in", async () => {
  const opened: string[] = [];
  await withBand(
    <SubjectBand goal="" lines={threeLines} onOpen={(epic) => opened.push(epic)} here="ep-orbit" />,
    (container) => {
      assertNone(container.querySelector('button[data-epic="ep-orbit"]'));
      const line = container.querySelector<HTMLElement>('div.subj.attention.here[data-epic="ep-orbit"]');
      assert.ok(line !== null, "the open epic's line is drawn");
      assert.equal(textOf(line, ".what b"), "orbit-labels");
      assert.equal(textOf(line, ".end"), "open on the right ›");
      assert.equal(line.textContent?.includes("Decide"), false, "it offers no way in");
      line.click();
      assert.deepEqual(opened, [], "pressing it opens nothing");
    },
  );
});

// contract is a contract line of the fixtures: the contract's id, its slug and the text of what is left to settle.
const contract = (id: string, slug: string, text: string): ContractBandLine => ({ id, slug, text });

const tide = contract("tidepool/tide-alerts", "tide-alerts", "1 decision is yours; read “can’t be undone” before Accept");

test("ContractLines: a contract line draws its slug contract, its text, the doc icon and Read and decide, and a click calls onOpenContract with its id", async () => {
  const opened: string[] = [];
  await withBand(
    <SubjectBand
      goal=""
      lines={[]}
      contracts={[tide]}
      onOpen={() => {}}
      onOpenContract={(id) => {
        opened.push(id);
      }}
    />,
    (container) => {
      const line = container.querySelector<HTMLButtonElement>('button.subj.attention[data-contract="tidepool/tide-alerts"]');
      assert.ok(line !== null, "the contract's line is a button");
      assert.equal(textOf(line, ".what b"), "tide-alerts contract");
      assert.equal(textOf(line, ".st"), tide.text);
      assert.equal(textOf(line, ".end"), "Read and decide ›");
      assert.ok(line.querySelector("svg.icon") !== null, "the line draws the doc icon");
      line.click();
      assert.deepEqual(opened, ["tidepool/tide-alerts"]);
    },
  );
});

test("ContractLines: the contract open on the right draws open on the right with no button and no way in", async () => {
  const opened: string[] = [];
  await withBand(
    <SubjectBand
      goal=""
      lines={[]}
      contracts={[tide]}
      onOpen={() => {}}
      onOpenContract={(id) => {
        opened.push(id);
      }}
      hereContract={tide.id}
    />,
    (container) => {
      assertNone(container.querySelector('button[data-contract="tidepool/tide-alerts"]'));
      const line = container.querySelector<HTMLElement>('div.subj.attention.here[data-contract="tidepool/tide-alerts"]');
      assert.ok(line !== null, "the open contract's line is drawn");
      assert.equal(textOf(line, ".what b"), "tide-alerts contract");
      assert.equal(textOf(line, ".end"), "open on the right ›");
      assert.equal(line.textContent?.includes("Read and decide"), false, "it offers no way in");
      line.click();
      assert.deepEqual(opened, [], "pressing it opens nothing");
    },
  );
});

test("ContractLines: contract lines come before epic lines and count toward the three shown", async () => {
  const labels = contract("tidepool/labels-cache", "labels-cache", "2 decisions are yours");
  await withBand(<SubjectBand goal="" lines={threeLines} contracts={[tide, labels]} onOpen={() => {}} onOpenContract={() => {}} />, (container) => {
    const shown = [...container.querySelectorAll<HTMLElement>("button.subj")];
    assert.deepEqual(
      shown.map((line) => textOf(line, ".what b")),
      ["tide-alerts contract", "labels-cache contract", "rota-export"],
    );
    assert.equal(textOf(container, ".more .what"), "and 2 more ask");
    assert.equal(textOf(container, ".more .st"), "orbit-labels, notices-usable");
  });
});

test("ContractLines: a contract past the third is named in the more line as its slug contract", async () => {
  const labels = contract("tidepool/labels-cache", "labels-cache", "2 decisions are yours");
  const orbit = contract("tidepool/orbit", "orbit", "1 decision is yours");
  const fourth = contract("tidepool/x", "x", "1 decision is yours");
  await withBand(<SubjectBand goal="" lines={[]} contracts={[tide, labels, orbit, fourth]} onOpen={() => {}} onOpenContract={() => {}} />, (container) => {
    assert.equal(countOf(container, "button.subj"), 3);
    assert.equal(textOf(container, ".more .what"), "and 1 more ask");
    assert.equal(textOf(container, ".more .st"), "x contract");
  });
});

test("ContractLines: with only a contract line and no goal the band draws", async () => {
  await withBand(<SubjectBand goal="" lines={[]} contracts={[tide]} onOpen={() => {}} />, (container) => {
    assert.equal(countOf(container, ".subj"), 1);
    assert.ok(container.querySelector('[data-contract="tidepool/tide-alerts"]') !== null, "the contract's line is drawn");
  });
});
