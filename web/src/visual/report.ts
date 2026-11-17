// The report driver of the visual check. For each scenario of web/visual/map.json at each width it opens the wireframe
// and the live client in Chromium (through playwright-core), navigates each as section 3 gives it, takes a screenshot of
// each side, and records what each side's selectors matched and how their listed properties computed. Nothing here
// judges a result: the report records the facts, and whatever reads them decides.

import { readFile } from "node:fs/promises";
import { join } from "node:path";

import { type Browser, type BrowserContext, chromium, type Page } from "playwright-core";

import { type Fixture, startFixture } from "./fixture";
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

// Side is one of the two renders a scenario is compared between.
export type Side = "wireframe" | "live";

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

// captureSide opens one side of one scenario at one width in a fresh context, navigates it, measures the probes and
// takes its screenshot.
interface SideCapture {
  readonly navigation: Navigation;
  readonly components: readonly Probed[];
  readonly liveOnly: readonly Probed[];
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

    if (options.shot !== undefined) {
      const png = side === "wireframe"
        ? await page.locator(".mock.b").first().screenshot({ animations: "disabled", caret: "hide" }).catch(() =>
          page.screenshot({ animations: "disabled", caret: "hide" }))
        : await page.screenshot({ animations: "disabled", caret: "hide" });
      await options.shot(name, png);
    }
    options.log?.(`${name}: navigation ${navigation.completed ? "completed" : `incomplete (${navigation.error})`}`);
    return { navigation, components: measured, liveOnly };
  } catch (error) {
    // A side that cannot open is recorded as an incomplete navigation whose selectors were not measured, so the other
    // sides of the run still reach the report.
    const message = error instanceof Error ? (error.message.split("\n")[0] ?? "") : String(error);
    options.log?.(`${name}: not captured (${message})`);
    const unmeasured = (matches: number): Probed[] => Array.from({ length: matches }, () => ({ matches: -1, error: message }));
    return {
      navigation: { completed: false, error: message },
      components: unmeasured(components.length),
      liveOnly: unmeasured(liveOnlyEntries.length),
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

  return { version: 1, pages, widths: map.widths, navigation, components, liveOnly };
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
