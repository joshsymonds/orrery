import assert from "node:assert/strict";
import { test } from "node:test";

import { type Box, type FaultContext, type FaultKind, faultsOf, type SideMeasurement, type TextBox } from "./faults";

const live: FaultContext = { scenario: "epic-holder-released", side: "live", width: 1600 };

// unclipped is a clip that holds the whole test page.
const unclipped: Box = { left: 0, top: 0, width: 10_000, height: 10_000 };

// The ink metrics of a font whose ink fills its content area: the baseline is at the ascent and the whole content area
// is ink. A case that sets these tests the line-box band alone.
const inkFills16 = {
  fontBoundingBoxAscent: 12,
  fontBoundingBoxDescent: 4,
  actualBoundingBoxAscent: 12,
  actualBoundingBoxDescent: 4,
};
const inkFills24 = {
  fontBoundingBoxAscent: 18,
  fontBoundingBoxDescent: 6,
  actualBoundingBoxAscent: 18,
  actualBoundingBoxDescent: 6,
};

// textBox is a visible own-text box of 60 px content width, 10 px '0', a 20 px line-height and a 16 px content area
// whose ink fills it, unless overridden.
function textBox(selector: string, overrides: Partial<TextBox> = {}): TextBox {
  return {
    selector,
    contentWidth: 60,
    zeroWidth: 10,
    lineHeight: 20,
    rects: [],
    clip: unclipped,
    lineTops: [0],
    ancestors: [],
    ...inkFills16,
    ...overrides,
  };
}

// rect is one text rect in CSS pixels.
function rect(left: number, top: number, width: number, height: number): Box {
  return { left, top, width, height };
}

// measured is a side with nothing faulty on it, so a test sets only the part it checks.
function measured(overrides: Partial<SideMeasurement> = {}): SideMeasurement {
  return { boxes: [], headings: [], scrollWidth: 1000, clientWidth: 1000, pastEdge: [], requests: [], ...overrides };
}

// kindsOf lists the fault kinds a measurement raises, one entry per fault.
function kindsOf(side: FaultContext, measurement: SideMeasurement): FaultKind[] {
  return faultsOf(side, measurement).map((fault) => fault.kind);
}

test("narrow-wrap flags a box five '0' wide whose text wraps onto two lines", () => {
  const box = textBox("span.target", {
    contentWidth: 50,
    rects: [rect(0, 0, 40, 16), rect(0, 20, 40, 16)],
    lineTops: [0, 20],
  });
  const faults = faultsOf(live, measured({ boxes: [box] }));
  assert.deepEqual(faults.map((fault) => fault.kind), ["narrow-wrap"]);
  assert.equal(faults[0].selector, "span.target");
  assert.equal(faults[0].reference, false);
});

test("narrow-wrap leaves a box six '0' wide, one glyph short of the rule, alone", () => {
  const box = textBox("span.target", {
    contentWidth: 60,
    rects: [rect(0, 0, 40, 16), rect(0, 20, 40, 16)],
    lineTops: [0, 20],
  });
  assert.deepEqual(kindsOf(live, measured({ boxes: [box] })), []);
});

test("narrow-wrap leaves a narrow box alone when its text is on one line", () => {
  const box = textBox("span.target", { contentWidth: 50, rects: [rect(0, 0, 40, 16)], lineTops: [0] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [box] })), []);
});

test("narrow-wrap flags a 0 px box whose text runs one glyph per line", () => {
  const box = textBox("span.target", {
    contentWidth: 0,
    rects: [rect(0, 0, 9, 16), rect(0, 20, 9, 16), rect(0, 40, 9, 16)],
    lineTops: [0, 20, 40],
  });
  assert.deepEqual(kindsOf(live, measured({ boxes: [box] })), ["narrow-wrap"]);
});

test("overlap flags two boxes whose text bands intersect by more than 1 px on both axes", () => {
  const first = textBox("p.a", { rects: [rect(0, 0, 50, 16)], lineTops: [0] });
  const second = textBox("p.b", { rects: [rect(10, 4, 50, 16)], lineTops: [4] });
  const faults = faultsOf(live, measured({ boxes: [first, second] }));
  assert.deepEqual(faults.map((fault) => fault.kind), ["overlap"]);
  assert.equal(faults[0].selector, "p.a");
  assert.equal(faults[0].other, "p.b");
});

test("overlap leaves an ancestor and its descendant alone even when their text overlaps", () => {
  const ancestor = textBox("div.card", { rects: [rect(0, 0, 50, 16)], lineTops: [0] });
  const descendant = textBox("span.inner", { rects: [rect(0, 2, 50, 16)], lineTops: [2], ancestors: [0] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [ancestor, descendant] })), []);
});

test("overlap flags two boxes whose ink spans intersect, with the overlap size in the detail", () => {
  // Each rect is 16 px with its baseline 12 px down, the ink rising 9 px and falling 3 px from it. The first's ink is
  // [3, 15] and the second's [13, 25]: they share 2 px of height over the same 50 px width.
  const ink = { fontBoundingBoxAscent: 12, fontBoundingBoxDescent: 4, actualBoundingBoxAscent: 9, actualBoundingBoxDescent: 3 };
  const first = textBox("p.a", { ...ink, lineHeight: null, rects: [rect(0, 0, 50, 16)], lineTops: [0] });
  const second = textBox("p.b", { ...ink, lineHeight: null, rects: [rect(0, 10, 50, 16)], lineTops: [10] });
  const faults = faultsOf(live, measured({ boxes: [first, second] }));
  assert.deepEqual(faults.map((fault) => [fault.kind, fault.selector, fault.other]), [["overlap", "p.a", "p.b"]]);
  assert.match(faults[0].detail, /2\.0 px/);
});

test("overlap leaves a large line above a small one alone when their content areas meet but their ink does not", () => {
  // Modelled on the ring's count and its caption at scale 1, both with the ring's computed line-height of 20 px. The
  // 34 px count has a 45 px content area, its baseline 35 px down, and its digits 25 px above the baseline with nothing
  // below it. The 11 px caption's baseline sits 9 px lower, its ascenders reach 8 px above it and its descenders 2 px
  // below. The content areas share 12 px; the ink spans [10, 35] and [36, 46] leave 1 px between them.
  const count = textBox("text.count", {
    lineHeight: 20,
    rects: [rect(0, 0, 120, 45)],
    lineTops: [0],
    fontBoundingBoxAscent: 35,
    fontBoundingBoxDescent: 10,
    actualBoundingBoxAscent: 25,
    actualBoundingBoxDescent: 0,
  });
  const caption = textBox("text.count-of", {
    lineHeight: 20,
    rects: [rect(0, 33, 60, 14)],
    lineTops: [33],
    fontBoundingBoxAscent: 11,
    fontBoundingBoxDescent: 3,
    actualBoundingBoxAscent: 8,
    actualBoundingBoxDescent: 2,
  });
  assert.deepEqual(kindsOf(live, measured({ boxes: [count, caption] })), []);
});

test("overlap leaves two stacked lines alone when their glyph rects overlap but their line bands only touch", () => {
  // Glyph rects are 24 px tall on a 20 px line: the second line's glyphs reach 4 px into the first, but the bands
  // (20 px, centred on each glyph rect) meet at 22 px and share no area.
  const upper = textBox("p.upper", { ...inkFills24, rects: [rect(0, 0, 100, 24)], lineTops: [0] });
  const lower = textBox("p.lower", { ...inkFills24, rects: [rect(0, 20, 100, 24)], lineTops: [20] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [upper, lower] })), []);
});

test("overlap flags stacked lines whose line bands intersect by more than 1 px", () => {
  const upper = textBox("p.upper", { ...inkFills24, rects: [rect(0, 0, 100, 24)], lineTops: [0] });
  const lower = textBox("p.lower", { ...inkFills24, rects: [rect(0, 18, 100, 24)], lineTops: [18] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [upper, lower] })), ["overlap"]);
});

test("overlap leaves bands that intersect by exactly 1 px alone", () => {
  const upper = textBox("p.upper", { ...inkFills24, rects: [rect(0, 0, 100, 24)], lineTops: [0] });
  const lower = textBox("p.lower", { ...inkFills24, rects: [rect(0, 19, 100, 24)], lineTops: [19] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [upper, lower] })), []);
});

test("overlap cuts ink to the line box, so ink beyond a line's box does not count", () => {
  // tall's ink rises 8 px above a 16 px content area: 20 px above the baseline, which is 12 px below the top. Each rect
  // is 16 px tall, so the upper ink is [-8, 16], and the clip, which starts at y = 0, cuts it to [0, 16]. The lower ink
  // is [12, 36]. On the 20 px line-height the line boxes are [-2, 18] and [18, 38], so the lower ink is cut to [18, 36]
  // and the cut inks [0, 16] and [18, 36] share nothing. With lineHeight null nothing is cut: the lower ink [12, 36]
  // meets the upper's [0, 16] over 4 px, which is a fault.
  const tall = {
    fontBoundingBoxAscent: 12,
    fontBoundingBoxDescent: 4,
    actualBoundingBoxAscent: 20,
    actualBoundingBoxDescent: 4,
  };
  const upper = textBox("p.upper", { ...tall, rects: [rect(0, 0, 100, 16)], lineTops: [0] });
  const lower = textBox("p.lower", { ...tall, rects: [rect(0, 20, 100, 16)], lineTops: [20] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [upper, lower] })), []);
  const upperBare = textBox("p.upper", { ...tall, lineHeight: null, rects: [rect(0, 0, 100, 16)], lineTops: [0] });
  const lowerBare = textBox("p.lower", { ...tall, lineHeight: null, rects: [rect(0, 20, 100, 16)], lineTops: [20] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [upperBare, lowerBare] })), ["overlap"]);
});

test("overlap leaves text clipped out of view alone", () => {
  // The second box's overlapping band lies below its scroll container's visible edge, 2 px down from its top.
  const first = textBox("p.a", { rects: [rect(0, 0, 50, 16)], lineTops: [0] });
  const second = textBox("p.b", {
    rects: [rect(10, 4, 50, 16)],
    lineTops: [4],
    clip: { left: 0, top: 0, width: 10_000, height: 2 },
  });
  assert.deepEqual(kindsOf(live, measured({ boxes: [first, second] })), []);
});

test("overlap leaves two boxes side by side on one line alone", () => {
  const left = textBox("span.left", { rects: [rect(0, 0, 50, 16)], lineTops: [0] });
  const right = textBox("span.right", { rects: [rect(60, 0, 50, 16)], lineTops: [0] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [left, right] })), []);
});

test("overlap uses the glyph rect as the band when line-height is normal", () => {
  const first = textBox("p.a", { lineHeight: null, rects: [rect(0, 0, 50, 16)], lineTops: [0] });
  const second = textBox("p.b", { lineHeight: null, rects: [rect(0, 10, 50, 16)], lineTops: [10] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [first, second] })), ["overlap"]);
});

test("overlap measures ink at the rendered size, so SVG text drawn at half its computed size is not drawn larger", () => {
  // The canvas metrics are in the computed size: a 24 px ascent, an 8 px descent, and 16 px of ink above the baseline.
  // The text is drawn at half that, so each 16 px rect has its baseline 12 px down and its ink at [top + 4, top + 12].
  // The rects are 10 px apart, so their ink spans [4, 12] and [14, 22] do not meet. Unscaled, they would: [8, 24] and
  // [18, 34] share 6 px.
  const halfSize = {
    fontBoundingBoxAscent: 24,
    fontBoundingBoxDescent: 8,
    actualBoundingBoxAscent: 16,
    actualBoundingBoxDescent: 0,
    lineHeight: null,
  };
  const first = textBox("text.a", { ...halfSize, rects: [rect(0, 0, 50, 16)], lineTops: [0] });
  const second = textBox("text.b", { ...halfSize, rects: [rect(0, 10, 50, 16)], lineTops: [10] });
  assert.deepEqual(kindsOf(live, measured({ boxes: [first, second] })), []);
});

test("overflow flags a document whose scrollWidth is greater than its clientWidth, naming the outermost element", () => {
  const faults = faultsOf(
    live,
    measured({
      scrollWidth: 1200,
      clientWidth: 1000,
      pastEdge: [{ selector: "main > div.wide", right: 1180 }],
    }),
  );
  assert.deepEqual(faults.map((fault) => [fault.kind, fault.selector]), [["overflow", "main > div.wide"]]);
});

test("overflow names html when the page is wider than the viewport but no element passes its edge", () => {
  const faults = faultsOf(live, measured({ scrollWidth: 1001, clientWidth: 1000 }));
  assert.deepEqual(faults.map((fault) => [fault.kind, fault.selector]), [["overflow", "html"]]);
});

test("overflow leaves a document whose scrollWidth equals its clientWidth alone", () => {
  assert.deepEqual(
    kindsOf(live, measured({ scrollWidth: 1000, clientWidth: 1000, pastEdge: [{ selector: "main", right: 1000 }] })),
    [],
  );
});

test("empty-heading flags a heading with nothing after it, and leaves one followed by text", () => {
  const faults = faultsOf(
    live,
    measured({
      headings: [
        { selector: "section > h2:nth-child(1)", text: "Open items", followed: false },
        { selector: "section > h2:nth-child(3)", text: "Done", followed: true },
      ],
    }),
  );
  assert.deepEqual(faults.map((fault) => [fault.kind, fault.selector]), [["empty-heading", "section > h2:nth-child(1)"]]);
});

test("request-failed names the element whose src or href is the failed URL", () => {
  const url = "https://fonts.googleapis.com/css2?family=Atkinson";
  const faults = faultsOf(
    live,
    measured({
      requests: [{ method: "GET", url, failure: "net::ERR_FAILED", target: 'html > head > link:nth-child(4)' }],
    }),
  );
  assert.deepEqual(faults.map((fault) => [fault.kind, fault.selector]), [["request-failed", "html > head > link:nth-child(4)"]]);
  assert.match(faults[0].detail, /fonts\.googleapis\.com/);
  assert.match(faults[0].detail, /net::ERR_FAILED/);
});

test("request-failed names html when no element's src or href is the failed URL", () => {
  const faults = faultsOf(
    live,
    measured({
      requests: [{ method: "POST", url: "http://127.0.0.1:9/orrery.v1.Epics/Get", failure: "answered HTTP 404", target: null }],
    }),
  );
  assert.deepEqual(faults.map((fault) => [fault.kind, fault.selector]), [["request-failed", "html"]]);
  assert.match(faults[0].detail, /answered HTTP 404/);
});

test("every kind of fault is raised from one measurement, in the order of faultKinds", () => {
  const faults = faultsOf(
    live,
    measured({
      boxes: [
        textBox("span.target", { contentWidth: 0, rects: [rect(0, 0, 9, 16), rect(0, 20, 9, 16)], lineTops: [0, 20] }),
        textBox("p.a", { rects: [rect(100, 100, 50, 16)], lineTops: [100] }),
        textBox("p.b", { rects: [rect(110, 104, 50, 16)], lineTops: [104] }),
      ],
      headings: [{ selector: "h2", text: "Empty", followed: false }],
      scrollWidth: 1200,
      clientWidth: 1000,
      pastEdge: [{ selector: "main", right: 1200 }],
      requests: [{ method: "GET", url: "http://127.0.0.1/x.png", failure: "answered HTTP 500", target: "img" }],
    }),
  );
  assert.deepEqual(faults.map((fault) => fault.kind), ["narrow-wrap", "overlap", "overflow", "empty-heading", "request-failed"]);
});

test("reference is set on the wireframe side only", () => {
  const measurement = measured({
    headings: [{ selector: "h2", text: "Empty", followed: false }],
    requests: [{ method: "GET", url: "https://fonts.googleapis.com/x", failure: "net::ERR_FAILED", target: null }],
  });
  const wireframe = faultsOf({ scenario: "epic-holder-released", side: "wireframe", width: 1600 }, measurement);
  const liveFaults = faultsOf({ scenario: "epic-holder-released", side: "live", width: 1600 }, measurement);
  assert.equal(wireframe.length, 2);
  assert.equal(liveFaults.length, 2);
  assert.ok(wireframe.every((fault) => fault.reference === true && fault.side === "wireframe"));
  assert.ok(liveFaults.every((fault) => fault.reference === false && fault.side === "live"));
});

test("each fault carries its scenario and width", () => {
  const [fault] = faultsOf(
    { scenario: "list-home", side: "live", width: 1000 },
    measured({ headings: [{ selector: "h1", text: "Epics", followed: false }] }),
  );
  assert.deepEqual(
    { scenario: fault.scenario, width: fault.width, kind: fault.kind },
    { scenario: "list-home", width: 1000, kind: "empty-heading" },
  );
});
