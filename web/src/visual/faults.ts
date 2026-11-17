// The layout-fault detectors of the visual check. The page measures and this module decides: report.ts runs one
// self-contained function in each side's page that returns the raw measurements typed here, and faultsOf turns them
// into fault records. Nothing here touches the DOM or playwright-core, so web-check unit-tests the rules without a
// browser.

// Side is one of the two renders a scenario is compared between.
export type Side = "wireframe" | "live";

// FaultKind names the five layout faults. narrow-wrap is a text box narrower than six '0' glyphs whose text wraps;
// overlap is two text boxes whose text visibly overlaps; overflow is content wider than the page; empty-heading is a
// heading with nothing visible after it; request-failed is a request that failed or answered 400 or more.
export type FaultKind = "narrow-wrap" | "overlap" | "overflow" | "empty-heading" | "request-failed";

// faultKinds lists the kinds in the order faultsOf reports them.
export const faultKinds: readonly FaultKind[] = ["narrow-wrap", "overlap", "overflow", "empty-heading", "request-failed"];

// FaultRecord is one fault on one side of one scenario at one width. other names the second element of an overlap.
// reference is true exactly when side is wireframe: the wireframe's faults are reported and never counted.
export interface FaultRecord {
  readonly scenario: string;
  readonly side: Side;
  readonly width: number;
  readonly kind: FaultKind;
  readonly selector: string;
  readonly other?: string;
  readonly detail: string;
  readonly reference: boolean;
}

// FaultContext says which side, scenario and width a measurement came from.
export interface FaultContext {
  readonly scenario: string;
  readonly side: Side;
  readonly width: number;
}

// Box is a rectangle in CSS pixels, in the form getClientRects reports one.
export interface Box {
  readonly left: number;
  readonly top: number;
  readonly width: number;
  readonly height: number;
}

// TextBox is one visible own-text box: an element with a non-whitespace text-node child whose text renders. selector
// matches exactly that element. contentWidth is the content-box width, 0 included. zeroWidth is the width of '0' in
// the element's computed font. lineHeight is the computed line-height in px, or null when it is normal. rects are the client rects of the element's own text. clip is the part of the
// viewport the text can show in: the viewport cut by the overflow clip of each ancestor. lineTops are the distinct tops
// of those rects. ancestors are the indices, in the measured boxes, of the boxes that contain this one.
// The four font metrics come from a canvas 2D context set to the computed font, with the element's own text measured.
// fontBoundingBoxAscent and fontBoundingBoxDescent are the font's content area above and below the baseline;
// actualBoundingBoxAscent and actualBoundingBoxDescent are the ink of the text above and below it. They are in the
// computed font size, which for SVG text is not the rendered one: inkSpan scales them to the rendered size.
export interface TextBox {
  readonly selector: string;
  readonly contentWidth: number;
  readonly zeroWidth: number;
  readonly lineHeight: number | null;
  readonly rects: readonly Box[];
  readonly clip: Box;
  readonly lineTops: readonly number[];
  readonly ancestors: readonly number[];
  readonly fontBoundingBoxAscent: number;
  readonly fontBoundingBoxDescent: number;
  readonly actualBoundingBoxAscent: number;
  readonly actualBoundingBoxDescent: number;
}

// HeadingMeasure is one visible h1-h6. followed is true when anything visible follows it within its parent.
export interface HeadingMeasure {
  readonly selector: string;
  readonly text: string;
  readonly followed: boolean;
}

// EdgeMeasure is one outermost element whose right edge passes the viewport's right edge, in CSS pixels.
export interface EdgeMeasure {
  readonly selector: string;
  readonly right: number;
}

// FailedRequest is one request that failed, or one fixture-origin response that answered 400 or more.
export interface FailedRequest {
  readonly method: string;
  readonly url: string;
  readonly failure: string;
}

// RequestMeasure is a FailedRequest with the selector of the element whose src or href is its URL, or null when none is.
export interface RequestMeasure extends FailedRequest {
  readonly target: string | null;
}

// SideMeasurement is everything the page function measures on one side at the measurement point.
export interface SideMeasurement {
  readonly boxes: readonly TextBox[];
  readonly headings: readonly HeadingMeasure[];
  readonly scrollWidth: number;
  readonly clientWidth: number;
  readonly pastEdge: readonly EdgeMeasure[];
  readonly requests: readonly RequestMeasure[];
}

// faultsOf turns one side's measurements into its fault records, grouped in the order of faultKinds.
export function faultsOf(context: FaultContext, measured: SideMeasurement): FaultRecord[] {
  return [
    ...narrowWrapFaults(context, measured.boxes),
    ...overlapFaults(context, measured.boxes),
    ...overflowFaults(context, measured),
    ...emptyHeadingFaults(context, measured.headings),
    ...requestFaults(context, measured.requests),
  ];
}

// narrowWrapFaults flags a box whose content is narrower than six '0' glyphs (a content width of 0 always counts) and
// whose own text spans more than one line.
export function narrowWrapFaults(context: FaultContext, boxes: readonly TextBox[]): FaultRecord[] {
  return boxes.flatMap((box) => {
    const limit = 6 * box.zeroWidth;
    const narrow = box.contentWidth === 0 || box.contentWidth < limit;
    if (!narrow || box.lineTops.length < 2) {
      return [];
    }
    return [
      record(
        context,
        "narrow-wrap",
        box.selector,
        `content box ${box.contentWidth.toFixed(1)} px wide, under six '0' (${limit.toFixed(1)} px); ` +
          `its text wraps onto ${box.lineTops.length} lines`,
      ),
    ];
  });
}

// overlapFaults flags each pair of boxes, neither containing the other, whose visible ink spans intersect by more than
// 1 px on both axes. An ink span is the vertical extent of a text rect's glyphs (inkSpan), cut to the line-box band of
// the rect where a line-height applies, and to the box's clip, so text scrolled or clipped out of view does not count.
// The line box is line-height tall and centred on the glyph rect; without a line-height the ink span is not cut. A pair
// yields one fault, naming both selectors in document order.
export function overlapFaults(context: FaultContext, boxes: readonly TextBox[]): FaultRecord[] {
  const inks = boxes.map(inksOf);
  const faults: FaultRecord[] = [];
  for (let first = 0; first < boxes.length; first++) {
    for (let second = first + 1; second < boxes.length; second++) {
      if (boxes[second].ancestors.includes(first) || boxes[first].ancestors.includes(second)) {
        continue;
      }
      const shared = sharedSpan(inks[first], inks[second]);
      if (shared !== null) {
        faults.push(
          record(
            context,
            "overlap",
            boxes[first].selector,
            `glyph ink overlaps by ${shared.width.toFixed(1)} × ${shared.height.toFixed(1)} px`,
            boxes[second].selector,
          ),
        );
      }
    }
  }
  return faults;
}

// inkSpan is the glyph ink of one text rect, as a box whose width is the rect's. The rect is the glyph content area, so
// its height over the font's content area at the computed size is the scale the text is rendered at: 1 for HTML, and
// the screen scale for SVG text, whose computed font size is in user units. The baseline sits fontBoundingBoxAscent
// below the rect's top; the ink runs actualBoundingBoxAscent above the baseline and actualBoundingBoxDescent below it.
function inkSpan(rect: Box, box: TextBox): Box {
  const scale = rect.height / (box.fontBoundingBoxAscent + box.fontBoundingBoxDescent);
  const baseline = rect.top + box.fontBoundingBoxAscent * scale;
  return {
    left: rect.left,
    width: rect.width,
    top: baseline - box.actualBoundingBoxAscent * scale,
    height: (box.actualBoundingBoxAscent + box.actualBoundingBoxDescent) * scale,
  };
}

// inksOf returns each text rect's ink span, cut to its line-box band where a line-height applies and to the box's
// clip; a span cut away entirely is dropped.
function inksOf(box: TextBox): Box[] {
  return box.rects.flatMap((rect) => {
    const ink = inkSpan(rect, box);
    const inBand = box.lineHeight === null
      ? ink
      : intersection(ink, {
        left: rect.left,
        width: rect.width,
        top: rect.top + rect.height / 2 - box.lineHeight / 2,
        height: box.lineHeight,
      });
    const shown = inBand === null ? null : intersection(inBand, box.clip);
    return shown === null ? [] : [shown];
  });
}

// intersection is the part of a rectangle inside a clip, or null when none of it is.
function intersection(rect: Box, clip: Box): Box | null {
  const left = Math.max(rect.left, clip.left);
  const top = Math.max(rect.top, clip.top);
  const right = Math.min(rect.left + rect.width, clip.left + clip.width);
  const bottom = Math.min(rect.top + rect.height, clip.top + clip.height);
  return right > left && bottom > top ? { left, top, width: right - left, height: bottom - top } : null;
}

// sharedSpan returns the first pair of spans, one from each list, that intersect by more than 1 px on both axes.
function sharedSpan(left: readonly Box[], right: readonly Box[]): { width: number; height: number } | null {
  for (const a of left) {
    for (const b of right) {
      const width = Math.min(a.left + a.width, b.left + b.width) - Math.max(a.left, b.left);
      const height = Math.min(a.top + a.height, b.top + b.height) - Math.max(a.top, b.top);
      if (width > 1 && height > 1) {
        return { width, height };
      }
    }
  }
  return null;
}

// overflowFaults flags a document whose scrollWidth is greater than its clientWidth. It names each outermost element
// past the right edge, or html when no element is.
export function overflowFaults(
  context: FaultContext,
  measured: Pick<SideMeasurement, "scrollWidth" | "clientWidth" | "pastEdge">,
): FaultRecord[] {
  if (measured.scrollWidth <= measured.clientWidth) {
    return [];
  }
  const measure = `scrollWidth ${measured.scrollWidth} px over clientWidth ${measured.clientWidth} px`;
  if (measured.pastEdge.length === 0) {
    return [record(context, "overflow", "html", `${measure}; no element's right edge passes the viewport`)];
  }
  return measured.pastEdge.map((edge) =>
    record(context, "overflow", edge.selector, `${measure}; right edge at ${edge.right.toFixed(1)} px`));
}

// emptyHeadingFaults flags each heading with nothing visible after it within its parent.
export function emptyHeadingFaults(context: FaultContext, headings: readonly HeadingMeasure[]): FaultRecord[] {
  return headings
    .filter((heading) => !heading.followed)
    .map((heading) =>
      record(context, "empty-heading", heading.selector, `heading "${heading.text}" has nothing visible after it`));
}

// requestFaults names each failed request by the element whose src or href is its URL, or html when there is none.
export function requestFaults(context: FaultContext, requests: readonly RequestMeasure[]): FaultRecord[] {
  return requests.map((request) =>
    record(context, "request-failed", request.target ?? "html", `${request.method} ${request.url} failed: ${request.failure}`));
}

// record builds one fault record, with reference set on the wireframe side only.
function record(context: FaultContext, kind: FaultKind, selector: string, detail: string, other?: string): FaultRecord {
  return {
    scenario: context.scenario,
    side: context.side,
    width: context.width,
    kind,
    selector,
    ...(other === undefined ? {} : { other }),
    detail,
    reference: context.side === "wireframe",
  };
}
