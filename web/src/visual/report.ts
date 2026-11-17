// The report driver of the visual check. For each scenario of web/visual/map.json at each width it opens the wireframe
// and the live client in Chromium (through playwright-core), navigates each as section 3 gives it, takes a screenshot of
// each side, and records what each side's selectors matched and how their listed properties computed. Nothing here
// judges a result: the report records the facts, and whatever reads them decides.

import { readFile } from "node:fs/promises";
import { join } from "node:path";

import { type Browser, type BrowserContext, chromium, type Page } from "playwright-core";

import { type Fixture, startFixture } from "./fixture";
import {
  type FailedRequest,
  type FaultRecord,
  faultsOf,
  type Side,
  type SideMeasurement,
  type TextBox,
  type EdgeMeasure,
  type RequestMeasure,
  type Box,
  type HeadingMeasure,
} from "./faults";
import { freezeCss, type Navigation, navigateLive, navigateWireframe, type Scenario, stepTimeoutMs } from "./navigate";
import { liveEpoch, wireframeEpoch } from "./world";

// The shape of web/visual/map.json that the driver reads.
export interface Component {
  readonly page: string;
  readonly scenario: string;
  readonly name: string;
  readonly grade: string;
  readonly wireframe: string;
  readonly live: string;
  readonly props: readonly string[];
}

export interface LiveOnly {
  readonly page: string;
  readonly live: string;
  readonly note: string;
}

export interface VisualMap {
  readonly version: number;
  readonly widths: readonly number[];
  readonly scenarios: readonly Scenario[];
  readonly components: readonly Component[];
  readonly liveOnly: readonly LiveOnly[];
}

// NavigationRecord is whether one side of one scenario at one width reached its state and its wait.
export interface NavigationRecord {
  readonly scenario: string;
  readonly side: Side;
  readonly width: number;
  readonly completed: boolean;
  readonly error: string;
}

// SideMatch is how many elements one side's selector matched. error is set when the selector would not run.
export interface SideMatch {
  readonly selector: string;
  readonly matches: number;
  readonly error?: string;
}

// PropertyRecord is one listed property's computed value on each side, and whether they are equal.
export interface PropertyRecord {
  readonly name: string;
  readonly wireframe: string;
  readonly live: string;
  readonly equal: boolean;
}

// ComponentRecord is one map component at one width. properties is null unless both sides matched exactly one element.
export interface ComponentRecord {
  readonly scenario: string;
  readonly page: string;
  readonly name: string;
  readonly grade: string;
  readonly width: number;
  readonly wireframe: SideMatch;
  readonly live: SideMatch;
  readonly properties: readonly PropertyRecord[] | null;
}

// LiveCount is one liveOnly selector's live match count on one scenario at one width.
export interface LiveCount {
  readonly scenario: string;
  readonly width: number;
  readonly matches: number;
  readonly error?: string;
}

// LiveOnlyRecord is one liveOnly entry with its live match count on each scenario of its page at each width.
export interface LiveOnlyRecord {
  readonly page: string;
  readonly live: string;
  readonly note: string;
  readonly counts: LiveCount[];
}

// Report is what the driver returns and report-main writes as web/build/visual/report.json.
export interface Report {
  readonly version: 1;
  readonly pages: readonly string[];
  readonly widths: readonly number[];
  readonly navigation: readonly NavigationRecord[];
  readonly components: readonly ComponentRecord[];
  readonly faults: readonly FaultRecord[];
  readonly liveOnly: readonly LiveOnlyRecord[];
}

// RunOptions locate the repository and receive the screenshots and progress lines as they are taken.
export interface RunOptions {
  readonly repoRoot: string;
  readonly shot?: (name: string, png: Uint8Array) => Promise<void>;
  readonly log?: (line: string) => void;
}

// Probe asks one side for a selector's match count and, when it matches one element, the listed computed values.
interface Probe {
  readonly selector: string;
  readonly properties: readonly string[];
}

interface Probed {
  readonly matches: number;
  readonly error?: string;
  readonly values?: readonly string[];
}

// viewportHeight is the second dimension of both viewports, 1600x1000 and 1000x1000.
const viewportHeight = 1000;

// chromiumArgs keep the font rasterisation the same on every run.
const chromiumArgs = ["--font-render-hinting=none"];

// loadMap reads web/visual/map.json under the repository root.
async function loadMap(repoRoot: string): Promise<VisualMap> {
  const text = await readFile(join(repoRoot, "web/visual/map.json"), "utf8");
  return JSON.parse(text) as VisualMap;
}

// pagesOf lists the pages the map's scenarios belong to, in the order the map gives them.
export async function pagesOf(repoRoot: string): Promise<string[]> {
  const map = await loadMap(repoRoot);
  return [...new Set(map.scenarios.map((scenario) => scenario.page))];
}

// probeSide measures probes in the page: each selector's match count, and for one match its computed values.
async function probeSide(page: Page, probes: readonly Probe[]): Promise<Probed[]> {
  return page.evaluate((list: readonly Probe[]) =>
    list.map((probe): Probed => {
      let nodes: NodeListOf<Element>;
      try {
        nodes = document.querySelectorAll(probe.selector);
      } catch (error) {
        return { matches: -1, error: String(error) };
      }
      if (nodes.length !== 1) {
        return { matches: nodes.length };
      }
      const style = getComputedStyle(nodes[0]);
      return { matches: 1, values: probe.properties.map((name) => style.getPropertyValue(name)) };
    }), probes);
}

// measure probes one side, and records a failure of the probe as every selector's error rather than a crash.
async function measure(page: Page, probes: readonly Probe[]): Promise<Probed[]> {
  try {
    return await probeSide(page, probes);
  } catch (error) {
    const message = error instanceof Error ? error.message.split("\n")[0] ?? "" : String(error);
    return probes.map(() => ({ matches: -1, error: message }));
  }
}

// measurePage is the layout measurement of one side. It runs in the page as one self-contained function, so playwright
// sends its source alone and every helper is declared inside it. It returns raw measurements and decides nothing:
// faults.ts reads them. failed lists the requests that failed up to the measurement point; each is resolved to the
// element whose src or href is its URL.
function measurePage(failed: readonly FailedRequest[]): SideMeasurement {
  const root = document.documentElement;

  const numeric = (value: string): number => {
    const parsed = Number.parseFloat(value);
    return Number.isFinite(parsed) ? parsed : 0;
  };

  // hidden is true when the element or an ancestor is display:none or has opacity 0, or the element's text is not
  // visible (visibility is inherited, so this reads the element's own computed value).
  const hidden = (el: Element): boolean => {
    for (let node: Element | null = el; node !== null; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.display === "none" || Number(style.opacity) === 0) {
        return true;
      }
    }
    return getComputedStyle(el).visibility !== "visible";
  };

  const textNodesOf = (el: Element): Text[] =>
    [...el.childNodes].filter((node): node is Text => node.nodeType === Node.TEXT_NODE && /\S/.test(node.textContent ?? ""));

  // glyphRects are the client rects of a node's text that have a non-zero size.
  const glyphRects = (node: Node): Box[] => {
    const range = document.createRange();
    range.selectNodeContents(node);
    return [...range.getClientRects()]
      .filter((rect) => rect.width > 0 && rect.height > 0)
      .map((rect) => ({ left: rect.left, top: rect.top, width: rect.width, height: rect.height }));
  };

  // drawn is true when a node shows on screen: a text node whose text renders under a visible parent, or an element
  // that is not hidden and has a box of non-zero size, or a child that is drawn.
  const drawn = (node: Node): boolean => {
    if (node.nodeType === Node.TEXT_NODE) {
      const parent = node.parentElement;
      return parent !== null && !hidden(parent) && /\S/.test(node.textContent ?? "") && glyphRects(node).length > 0;
    }
    if (!(node instanceof Element)) {
      return false;
    }
    const box = node.getBoundingClientRect();
    if (!hidden(node) && box.width > 0 && box.height > 0) {
      return true;
    }
    return [...node.childNodes].some(drawn);
  };

  // selectorOf is a path from the element up to its nearest unique id, or to body, of tag, classes and :nth-child at
  // each level. It is checked to match exactly that element, and falls back to the path without classes.
  const selectorOf = (el: Element): string => {
    const path = (withClasses: boolean): string => {
      const parts: string[] = [];
      let node: Element | null = el;
      while (node !== null && node !== root) {
        if (node.id !== "") {
          const byId = `#${CSS.escape(node.id)}`;
          if (document.querySelectorAll(byId).length === 1) {
            parts.unshift(byId);
            return parts.join(" > ");
          }
        }
        if (node === document.body) {
          parts.unshift("body");
          return parts.join(" > ");
        }
        const classes = withClasses ? [...node.classList].map((name) => `.${CSS.escape(name)}`).join("") : "";
        const index = node.parentElement === null ? 1 : [...node.parentElement.children].indexOf(node) + 1;
        parts.unshift(`${node.tagName.toLowerCase()}${classes}:nth-child(${index})`);
        node = node.parentElement;
      }
      parts.unshift("html");
      return parts.join(" > ");
    };
    const matchesOnly = (selector: string): boolean => {
      const found = document.querySelectorAll(selector);
      return found.length === 1 && found[0] === el;
    };
    const withClasses = path(true);
    const selector = matchesOnly(withClasses) ? withClasses : path(false);
    if (!matchesOnly(selector)) {
      throw new Error(`no selector matches exactly the ${el.tagName.toLowerCase()} element`);
    }
    return selector;
  };

  // zeroWidths caches the width of '0' per computed font; the probe is an absolutely placed span, removed at once.
  const zeroWidths = new Map<string, number>();
  const fontProps = [
    "font-family",
    "font-size",
    "font-weight",
    "font-style",
    "font-stretch",
    "letter-spacing",
    "font-feature-settings",
    "font-variant-numeric",
    "font-kerning",
  ];
  const zeroWidthOf = (style: CSSStyleDeclaration): number => {
    const key = fontProps.map((prop) => style.getPropertyValue(prop)).join("|");
    const known = zeroWidths.get(key);
    if (known !== undefined) {
      return known;
    }
    const probe = document.createElement("span");
    probe.textContent = "0";
    probe.style.setProperty("position", "absolute");
    probe.style.setProperty("visibility", "hidden");
    probe.style.setProperty("white-space", "pre");
    for (const prop of fontProps) {
      probe.style.setProperty(prop, style.getPropertyValue(prop));
    }
    document.body.appendChild(probe);
    const width = probe.getBoundingClientRect().width;
    probe.remove();
    zeroWidths.set(key, width);
    return width;
  };

  // lineHeightOf is the computed line-height in px, or null when it is normal.
  const lineHeightOf = (style: CSSStyleDeclaration): number | null => {
    const raw = style.lineHeight;
    if (raw.endsWith("px")) {
      return numeric(raw);
    }
    if (/^\d*\.?\d+$/.test(raw)) {
      return numeric(raw) * numeric(style.fontSize);
    }
    return null;
  };

  // inkMetrics are the canvas metrics of an element's own text in its computed font: the font is set from the computed
  // family, style, weight and size, and the text is transformed as CSS transforms it, so the glyphs measured are the
  // glyphs drawn. The same measurement serves HTML and SVG text alike.
  const canvas = document.createElement("canvas").getContext("2d");
  if (canvas === null) {
    throw new Error("no 2D canvas context to measure glyph ink");
  }
  const transformed = (text: string, transform: string): string => {
    switch (transform) {
      case "uppercase":
        return text.toUpperCase();
      case "lowercase":
        return text.toLowerCase();
      case "capitalize":
        return text.replace(/\b\p{L}/gu, (letter) => letter.toUpperCase());
      default:
        return text;
    }
  };
  const inkMetricsOf = (style: CSSStyleDeclaration, texts: readonly Text[]) => {
    canvas.font = `${style.fontStyle} ${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
    const text = transformed(texts.map((node) => node.textContent ?? "").join(""), style.textTransform);
    const metrics = canvas.measureText(text);
    return {
      fontBoundingBoxAscent: metrics.fontBoundingBoxAscent,
      fontBoundingBoxDescent: metrics.fontBoundingBoxDescent,
      actualBoundingBoxAscent: metrics.actualBoundingBoxAscent,
      actualBoundingBoxDescent: metrics.actualBoundingBoxDescent,
    };
  };

  // clipOf is the part of the viewport an element's text can show in: the viewport, cut by the border box of each
  // ancestor, the element included, whose overflow is not visible. Text scrolled or clipped out of view is not shown.
  const clipOf = (el: Element): Box => {
    let left = 0;
    let top = 0;
    let right = root.clientWidth;
    let bottom = window.innerHeight;
    for (let node: Element | null = el; node !== null; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.overflowX !== "visible" || style.overflowY !== "visible") {
        const frame = node.getBoundingClientRect();
        left = Math.max(left, frame.left);
        top = Math.max(top, frame.top);
        right = Math.min(right, frame.right);
        bottom = Math.min(bottom, frame.bottom);
      }
    }
    return { left, top, width: Math.max(0, right - left), height: Math.max(0, bottom - top) };
  };

  const boxes: TextBox[] = [];
  const indexOf = new Map<Element, number>();
  for (const el of document.body.querySelectorAll("*")) {
    const texts = textNodesOf(el);
    if (texts.length === 0 || hidden(el)) {
      continue;
    }
    const rects = texts.flatMap((text) => glyphRects(text));
    if (rects.length === 0) {
      continue;
    }
    const style = getComputedStyle(el);
    const box = el.getBoundingClientRect();
    const ancestors: number[] = [];
    for (let node = el.parentElement; node !== null; node = node.parentElement) {
      const at = indexOf.get(node);
      if (at !== undefined) {
        ancestors.push(at);
      }
    }
    indexOf.set(el, boxes.length);
    boxes.push({
      selector: selectorOf(el),
      contentWidth: Math.max(
        0,
        box.width - numeric(style.paddingLeft) - numeric(style.paddingRight) -
          numeric(style.borderLeftWidth) - numeric(style.borderRightWidth),
      ),
      zeroWidth: zeroWidthOf(style),
      lineHeight: lineHeightOf(style),
      rects,
      clip: clipOf(el),
      lineTops: [...new Set(rects.map((rect) => rect.top))],
      ancestors,
      ...inkMetricsOf(style, texts),
    });
  }

  const headings: HeadingMeasure[] = [...document.body.querySelectorAll("h1,h2,h3,h4,h5,h6")]
    .filter((heading) => !hidden(heading))
    .map((heading) => {
      let followed = false;
      for (let node: Node | null = heading.nextSibling; node !== null && !followed; node = node.nextSibling) {
        followed = drawn(node);
      }
      return {
        selector: selectorOf(heading),
        text: (heading.textContent ?? "").replace(/\s+/g, " ").trim().slice(0, 80),
        followed,
      };
    });

  // past is every rendered element whose right edge passes the viewport's; pastEdge keeps the outermost of them.
  const edge = root.clientWidth;
  const past = new Set<Element>();
  for (const el of document.body.querySelectorAll("*")) {
    if (el.getClientRects().length > 0 && el.getBoundingClientRect().right > edge) {
      past.add(el);
    }
  }
  const pastEdge: EdgeMeasure[] = [];
  for (const el of past) {
    let outermost = true;
    for (let node = el.parentElement; node !== null && outermost; node = node.parentElement) {
      outermost = !past.has(node);
    }
    if (outermost) {
      pastEdge.push({ selector: selectorOf(el), right: el.getBoundingClientRect().right });
    }
  }

  // resolves is true when an attribute value, resolved against the document, is the URL.
  const resolves = (value: string | null, url: string): boolean => {
    if (value === null) {
      return false;
    }
    try {
      return new URL(value, document.baseURI).href === url;
    } catch {
      return false;
    }
  };
  const requests: RequestMeasure[] = failed.map((request) => {
    const owner = [...document.querySelectorAll("[src],[href]")].find((el) =>
      resolves(el.getAttribute("src"), request.url) || resolves(el.getAttribute("href"), request.url));
    return { ...request, target: owner === undefined ? null : selectorOf(owner) };
  });

  return {
    boxes,
    headings,
    scrollWidth: root.scrollWidth,
    clientWidth: edge,
    pastEdge,
    requests,
  };
}

// captureSide opens one side of one scenario at one width in a fresh context, navigates it, measures the probes and
// the layout, and takes its screenshot.
interface SideCapture {
  readonly navigation: Navigation;
  readonly components: readonly Probed[];
  readonly liveOnly: readonly Probed[];
  readonly faults: readonly FaultRecord[];
}

async function captureSide(
  browser: Browser,
  fixture: Fixture,
  map: VisualMap,
  scenario: Scenario,
  width: number,
  side: Side,
  options: RunOptions,
): Promise<SideCapture> {
  const components = map.components.filter((component) => component.scenario === scenario.id);
  const liveOnlyEntries = side === "live" ? map.liveOnly.filter((entry) => entry.page === scenario.page) : [];
  const name = `${scenario.id}-${side}-${width}.png`;
  let context: BrowserContext | undefined;
  try {
    context = await browser.newContext({
      viewport: { width, height: viewportHeight },
      timezoneId: "UTC",
      locale: "en-US",
    });
    // Every request off the fixture origin is aborted: the wireframe's Google Fonts link fails, and its prose face
    // comes from fontconfig.
    const fixtureOrigin = fixture.origin;
    await context.route("**/*", (route) =>
      new URL(route.request().url()).origin === fixtureOrigin ? route.continue() : route.abort(),
    );
    const page = await context.newPage();
    page.setDefaultTimeout(stepTimeoutMs);
    // The requests that fail from here to the measurement point: the page's requestfailed events, which include the
    // off-origin aborts, and the fixture's responses that answer 400 or more.
    const failed: FailedRequest[] = [];
    page.on("requestfailed", (request) => {
      failed.push({ method: request.method(), url: request.url(), failure: request.failure()?.errorText ?? "failed" });
    });
    page.on("response", (response) => {
      if (new URL(response.url()).origin === fixtureOrigin && response.status() >= 400) {
        failed.push({
          method: response.request().method(),
          url: response.url(),
          failure: `answered HTTP ${response.status()}`,
        });
      }
    });
    // Animations, transitions and the caret are off from the page's first style, on both sides alike.
    await page.addInitScript((css: string) => {
      const install = () => {
        const style = document.createElement("style");
        style.textContent = css;
        document.head.appendChild(style);
      };
      if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", install, { once: true });
      } else {
        install();
      }
    }, freezeCss);
    // The clock starts at the scenario's epoch and is paused there, so every Date.now() the page reads is that instant
    // until the navigation advances it.
    const epoch = side === "wireframe" ? wireframeEpoch : liveEpoch;
    await page.clock.install({ time: new Date(epoch) });
    await page.clock.pauseAt(new Date(epoch));

    const navigation = side === "wireframe"
      ? await navigateWireframe(page, fixture, scenario)
      : await navigateLive(page, fixture, scenario);

    const probes: Probe[] = components.map((component) => ({
      selector: side === "wireframe" ? component.wireframe : component.live,
      properties: component.props,
    }));
    const liveOnlyProbes: Probe[] = liveOnlyEntries.map((entry) => ({ selector: entry.live, properties: [] }));
    const measured = await measure(page, probes);
    const liveOnly = await measure(page, liveOnlyProbes);
    const layout = await page.evaluate(measurePage, [...failed]);

    if (options.shot !== undefined) {
      const png = side === "wireframe"
        ? await page.locator(".mock.b").first().screenshot({ animations: "disabled", caret: "hide" }).catch(() =>
          page.screenshot({ animations: "disabled", caret: "hide" }))
        : await page.screenshot({ animations: "disabled", caret: "hide" });
      await options.shot(name, png);
    }
    options.log?.(`${name}: navigation ${navigation.completed ? "completed" : `incomplete (${navigation.error})`}`);
    const faults = faultsOf({ scenario: scenario.id, side, width }, layout);
    return { navigation, components: measured, liveOnly, faults };
  } catch (error) {
    // A side that cannot open is recorded as an incomplete navigation whose selectors were not measured, so the other
    // sides of the run still reach the report. It records no faults: its navigation already says why.
    const message = error instanceof Error ? (error.message.split("\n")[0] ?? "") : String(error);
    options.log?.(`${name}: not captured (${message})`);
    const unmeasured = (matches: number): Probed[] => Array.from({ length: matches }, () => ({ matches: -1, error: message }));
    return {
      navigation: { completed: false, error: message },
      components: unmeasured(components.length),
      liveOnly: unmeasured(liveOnlyEntries.length),
      faults: [],
    };
  } finally {
    await context?.close();
  }
}

// runReport runs the pages' scenarios of the map on both sides at each of the map's widths and returns the report.
export async function runReport(pages: readonly string[], options: RunOptions): Promise<Report> {
  const map = await loadMap(options.repoRoot);
  const scenarios = map.scenarios.filter((scenario) => pages.includes(scenario.page));
  const fixture = await startFixture({
    bundleDir: join(options.repoRoot, "web/build/page"),
    wireframeDir: join(options.repoRoot, "web/visual/wireframe/round4"),
  });
  const executablePath = process.env.ORRERY_VISUAL_CHROMIUM;
  if (executablePath === undefined || executablePath === "") {
    await fixture.close();
    throw new Error("ORRERY_VISUAL_CHROMIUM is not set: run the check under the visual devenv profile");
  }
  const browser = await chromium.launch({ executablePath, args: chromiumArgs });

  const navigation: NavigationRecord[] = [];
  const components: ComponentRecord[] = [];
  const faults: FaultRecord[] = [];
  const liveOnly: LiveOnlyRecord[] = map.liveOnly
    .filter((entry) => pages.includes(entry.page))
    .map((entry) => ({ page: entry.page, live: entry.live, note: entry.note, counts: [] }));
  try {
    for (const scenario of scenarios) {
      for (const width of map.widths) {
        const wireframe = await captureSide(browser, fixture, map, scenario, width, "wireframe", options);
        const live = await captureSide(browser, fixture, map, scenario, width, "live", options);
        navigation.push(
          { scenario: scenario.id, side: "wireframe", width, ...wireframe.navigation },
          { scenario: scenario.id, side: "live", width, ...live.navigation },
        );
        faults.push(...wireframe.faults, ...live.faults);
        const compared = map.components.filter((component) => component.scenario === scenario.id);
        compared.forEach((component, index) => {
          const left = wireframe.components[index];
          const right = live.components[index];
          const properties = bothSingle(left, right)
            ? component.props.map((name, at) => ({
              name,
              wireframe: left.values?.[at] ?? "",
              live: right.values?.[at] ?? "",
              equal: left.values?.[at] === right.values?.[at],
            }))
            : null;
          components.push({
            scenario: scenario.id,
            page: component.page,
            name: component.name,
            grade: component.grade,
            width,
            wireframe: sideMatch(component.wireframe, left),
            live: sideMatch(component.live, right),
            properties,
          });
        });
        const entries = map.liveOnly.filter((entry) => entry.page === scenario.page);
        entries.forEach((entry, index) => {
          const record = liveOnly.find((candidate) => candidate.page === entry.page && candidate.live === entry.live);
          const counted = live.liveOnly[index];
          if (record !== undefined && counted !== undefined) {
            record.counts.push({ scenario: scenario.id, width, matches: counted.matches, ...errorOf(counted) });
          }
        });
      }
    }
  } finally {
    await browser.close();
    await fixture.close();
  }

  return { version: 1, pages, widths: map.widths, navigation, components, faults, liveOnly };
}

// bothSingle is true when both sides matched exactly one element and recorded its values.
function bothSingle(left: Probed | undefined, right: Probed | undefined): boolean {
  return left?.matches === 1 && right?.matches === 1 && left.values !== undefined && right.values !== undefined;
}

function errorOf(probed: Probed): { error?: string } {
  return probed.error === undefined ? {} : { error: probed.error };
}

function sideMatch(selector: string, probed: Probed | undefined): SideMatch {
  return { selector, matches: probed?.matches ?? -1, ...errorOf(probed ?? { matches: -1, error: "not measured" }) };
}
