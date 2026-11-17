// The navigation each scenario performs on each side, as web/visual/scenarios.md section 3 gives it. The wireframe opens
// at its hash; the live client loads at the fixture origin and every step is a click or a wait. Each side ends with
// clock.runFor(500), as section 1.1 asks, so the screen is the one the frozen clock shows.

import type { Page } from "playwright-core";

import { type CallRecord, type Fixture, wireframePrefix } from "./fixture";

// Scenario is one entry of web/visual/map.json's scenarios.
export interface Scenario {
  readonly id: string;
  readonly page: string;
  readonly wireframe: string;
  readonly describes: string;
}

// Navigation says whether a side reached its state: completed is false when a step or a wait ran out of time or
// failed, and error then names the step.
export interface Navigation {
  readonly completed: boolean;
  readonly error: string;
}

// stepTimeoutMs bounds each click, each wait for a selector and each wait for a call. The fixture answers in
// milliseconds, so the bound catches a page that never reaches its state.
export const stepTimeoutMs = 10_000;

// settleMs gives the page real time to read the response it was waiting for. The clock is paused, so a render that
// follows a response cannot be timed by the clock; the wait is on the wall clock, not the page's.
const settleMs = 300;

// freezeCss turns off animations, transitions and the caret. The driver installs it in every page before the page loads.
export const freezeCss = "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}";

// wireframeChromeCss hides the wireframe's own page and workflow bars, which the checker never compares.
const wireframeChromeCss = ".pg-bar,.wf-bar{display:none!important}";

// describe keeps the first lines of an error, which name the step and what it waited for, and caps its length.
function describe(error: unknown): string {
  const text = error instanceof Error ? error.message : String(error);
  return text.split("\n").slice(0, 6).join(" | ").slice(0, 800);
}

// navigateWireframe opens the wireframe at the scenario's hash.
export async function navigateWireframe(page: Page, fixture: Fixture, scenario: Scenario): Promise<Navigation> {
  try {
    await page.goto(`${fixture.origin}${wireframePrefix}index.html${scenario.wireframe}`, { waitUntil: "load" });
    await page.addStyleTag({ content: wireframeChromeCss });
    await page.evaluate(() => {
      for (const rows of document.querySelectorAll<HTMLElement>(".b-rows")) {
        rows.scrollTop = 0;
      }
    });
    await page.evaluate(() => document.fonts.ready);
    if (scenario.id === "talk-rota-menu") {
      // The wireframe opens its Talk menu from the keyboard, and it has no hash for the open menu.
      await page.keyboard.press("t");
      await page.clock.runFor(300);
    }
    await page.clock.runFor(500);
    return { completed: true, error: "" };
  } catch (error) {
    return { completed: false, error: describe(error) };
  }
}

type Matcher = (call: CallRecord) => boolean;

// A Step is one action or wait of a live navigation.
type Step =
  // A click is a pointer click on the element's box, or with dispatch, a click event on the element itself: the phase
  // cells are SVG paths whose boxes overlap their neighbours, so a pointer click would land on a neighbour.
  | { readonly kind: "click"; readonly selector: string; readonly text?: RegExp; readonly dispatch?: boolean }
  | { readonly kind: "show"; readonly selector: string }
  | { readonly kind: "call"; readonly label: string; readonly matches: Matcher; readonly count: number };

const clickStep = (selector: string, text?: RegExp): Step => ({ kind: "click", selector, text });
const dispatchStep = (selector: string): Step => ({ kind: "click", selector, dispatch: true });
const showStep = (selector: string): Step => ({ kind: "show", selector });
const callStep = (label: string, matches: Matcher, count = 1): Step => ({ kind: "call", label, matches, count });

// procedureCall matches a call to procedure, and with id, one whose request names that id.
const procedureCall = (procedure: string, id?: string): Matcher => (call) =>
  call.procedure === procedure && (id === undefined || call.body.id === id);
const paneCall = (pane: string): Matcher => (call) =>
  call.procedure === "orrery.v1.Panes/WatchConversation" && call.body.pane === pane;

// calledSince reports whether at least count calls matching matches reached the fixture since the step began at mark.
function calledSince(fixture: Fixture, mark: number, matches: Matcher, count: number): boolean {
  return fixture.calls.slice(mark).filter(matches).length >= count;
}

const rotaLine = '.list .b-row.epic[data-key="epic:lighthouse/rota-export"]';
const talkButton = clickStep("section.page .title-row .acts button.act", /^Talk$/);
const contractSummary = clickStep("section.page button.summary");
const epicsGet = "orrery.v1.Epics/Get";
const epicsHistory = "orrery.v1.Epics/History";

// liveSteps is the live navigation of one scenario, as section 3 lists it.
function liveSteps(scenario: string): Step[] {
  switch (scenario) {
    case "list-home":
      return [
        callStep("Epics/Get for the five live epics", procedureCall(epicsGet), 5),
        callStep("Panes/List", procedureCall("orrery.v1.Panes/List")),
      ];
    case "epic-rota-decision":
      return [
        clickStep(rotaLine),
        callStep("Epics/History of lighthouse/rota-export", procedureCall(epicsHistory, "lighthouse/rota-export")),
      ];
    case "epic-holder-released":
      return [
        clickStep(".list .b-more", /1 ended earlier/),
        clickStep('.list .b-row.epic[data-key="epic:orrery/holder-scopes"]'),
        callStep("Epics/Get of orrery/holder-scopes", procedureCall(epicsGet, "orrery/holder-scopes")),
        callStep("Epics/History of orrery/holder-scopes", procedureCall(epicsHistory, "orrery/holder-scopes")),
      ];
    case "contract-tidealerts-open":
      return [clickStep('[data-key="epic:tidepool/tide-alerts"]'), contractSummary, showStep("article.contract")];
    case "contract-rota-accepted":
      return [clickStep('[data-key="epic:lighthouse/rota-export"]'), contractSummary, showStep("article.contract")];
    case "phase-rota-export-cli-checking":
      return [
        clickStep(rotaLine),
        dispatchStep('section.page svg.orrery path.cell[data-task="T2"][data-phase="checking"]'),
        showStep(".phase-output .phase-row.open"),
      ];
    case "worker-rota-export-cli-implementer":
      return [
        clickStep(rotaLine),
        dispatchStep('path.cell[data-task="T2"][data-phase="implementing"]'),
        clickStep(".phase-output .phase-row.open button.act"),
        callStep("Sessions/WatchConversation of st-rota-t2-impl",
          procedureCall("orrery.v1.Sessions/WatchConversation", "st-rota-t2-impl")),
      ];
    case "talk-rota-rotaplan-docked":
      return [
        clickStep(rotaLine),
        talkButton,
        clickStep("ul.talk li:first-child button"),
        callStep("Panes/WatchConversation of %14", paneCall("%14")),
      ];
    case "talk-rota-menu":
      return [clickStep(rotaLine), talkButton, showStep("ul.talk")];
    case "session-lamp-working":
      return [
        clickStep('.list .b-row[data-key="pane:halyard:%11"]'),
        callStep("Panes/WatchConversation of %11", paneCall("%11")),
      ];
    case "session-beacon-question":
      return [
        clickStep('.list .b-row[data-key="pane:halyard:%12"]'),
        callStep("Panes/WatchConversation of %12", paneCall("%12")),
      ];
    default:
      throw new Error(`no live navigation is defined for ${scenario}`);
  }
}

const pause = (ms: number) => new Promise<void>((resolvePause) => setTimeout(resolvePause, ms));

// advanceUntil advances the clock in 100 ms steps until done() holds, giving the page's effects and the fixture's
// answers a turn between steps. The attempts bound the wait; the clock advances only by the steps it takes.
async function advanceUntil(page: Page, label: string, done: () => Promise<boolean>): Promise<void> {
  for (let attempt = 0; attempt < 30; attempt++) {
    if (await done()) {
      return;
    }
    await page.clock.runFor(100);
    await pause(100);
  }
  throw new Error(`timed out waiting for ${label}`);
}

// navigateLive loads the live client at the fixture origin and performs the scenario's steps. The clock is paused, and
// the page runs its effects on the clock's animation frames, so each step advances the clock until its effects have run:
// a call wait until the fixture has the call, a selector wait until the element is drawn. Every advance is the clock's
// own, so the screen is the same on every run.
export async function navigateLive(page: Page, fixture: Fixture, scenario: Scenario): Promise<Navigation> {
  const mark = fixture.calls.length;
  try {
    await page.goto(`${fixture.origin}/`, { waitUntil: "load" });
    for (const step of liveSteps(scenario.id)) {
      if (step.kind === "click") {
        // Playwright waits for an element's box to hold across two animation frames before it clicks, and the frozen
        // clock runs no frames, so the click is sent without that wait. The wait after each click confirms its effect.
        const locator = page.locator(step.selector, step.text === undefined ? {} : { hasText: step.text }).first();
        if (step.dispatch === true) {
          await locator.dispatchEvent("click", undefined, { timeout: stepTimeoutMs });
        } else {
          await locator.click({ timeout: stepTimeoutMs, force: true });
        }
        await page.clock.runFor(500);
      } else if (step.kind === "show") {
        const locator = page.locator(step.selector).first();
        await advanceUntil(page, step.selector, async () => (await locator.count()) > 0);
      } else {
        await advanceUntil(page, step.label, async () =>
          calledSince(fixture, mark, step.matches, step.count));
      }
    }
    await pause(settleMs);
    await page.clock.runFor(500);
    await pause(settleMs);
    return { completed: true, error: "" };
  } catch (error) {
    return { completed: false, error: describe(error) };
  }
}
