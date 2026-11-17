// The entry point of `just visual-selftest`. It serves the synthetic page pairs of selftest.ts from memory on 127.0.0.1 and
// answers 404 for any other path. Each case runs through the report's measurement and through decide(), the decision
// visual-check exits by, and the run prints one line per case: its id, what it expected, the decision and its failures.
// It exits 0 only when every case came out as expected, and 1 otherwise or when the run cannot start (no browser). It
// writes no report.json and no screenshot.

import { createServer, type Server } from "node:http";

import type { Page } from "playwright-core";

import { decide, type Decision, type Failure } from "./decide";
import type { Scenario, Navigation } from "./navigate";
import type { Side } from "./faults";
import { launchChromium, run, type SideSetup } from "./report";
import { judge, type SelfTestCase, selfTestCases, type Verdict } from "./selftest";

// epoch is the instant each side's clock is paused on. The synthetic pages read no time, so it only sets the clock.
const epoch = "2026-11-14T14:41:00Z";

// readyAttempts and readyPollMs bound the wait for a page's #ready element: 60 polls, 50 ms apart.
const readyAttempts = 60;
const readyPollMs = 50;

// Served is the started server: the origin its pages are on, and how to stop it.
interface Served {
  readonly origin: string;
  close(): Promise<void>;
}

// serve answers each case's two pages, /<id>/wireframe and /<id>/live, from memory, and every other path with 404.
async function serve(): Promise<Served> {
  const pages = new Map<string, string>();
  for (const testCase of selfTestCases) {
    pages.set(`/${testCase.id}/wireframe`, testCase.wireframe);
    pages.set(`/${testCase.id}/live`, testCase.live);
  }
  const server: Server = createServer((req, res) => {
    const html = pages.get((req.url ?? "/").split("?")[0] ?? "");
    if (req.method === "GET" && html !== undefined) {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(html);
      return;
    }
    res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
    res.end("not found");
  });
  // A bind failure rejects the start, so main reports it and exits 1 rather than leaving the error unhandled.
  await new Promise<void>((resolveListen, rejectListen) => {
    server.once("error", rejectListen);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", rejectListen);
      resolveListen();
    });
  });
  const address = server.address();
  if (address === null || typeof address === "string") {
    throw new Error("selftest: the server has no TCP address");
  }
  return {
    origin: `http://127.0.0.1:${address.port}`,
    close() {
      // Connections are destroyed before the wait: server.close resolves only once every connection has ended.
      const closed = new Promise<void>((resolveClose) => server.close(() => resolveClose()));
      server.closeAllConnections();
      return closed;
    },
  };
}

// awaitReady polls the page's #ready element until it shows, and throws once the attempts run out. It polls with isVisible,
// as navigate.ts's waits do, so it does not depend on the page's paused clock.
async function awaitReady(page: Page): Promise<void> {
  const ready = page.locator("#ready");
  for (let attempt = 0; attempt < readyAttempts; attempt++) {
    if (await ready.isVisible()) {
      return;
    }
    await new Promise<void>((resolvePoll) => setTimeout(resolvePoll, readyPollMs));
  }
  throw new Error(`#ready did not show within ${readyAttempts * readyPollMs} ms`);
}

// navigateSide is one side's navigation: it opens the case's page on that side and waits for its ready element. The
// scenario's id is the case's id, which names the page.
function navigateSide(origin: string, side: Side): SideSetup["navigate"] {
  return async (page: Page, scenario: Scenario): Promise<Navigation> => {
    try {
      await page.goto(`${origin}/${scenario.id}/${side}`, { waitUntil: "load" });
      await awaitReady(page);
      return { completed: true, error: "" };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return { completed: false, error: message.split("\n")[0] ?? "" };
    }
  };
}

// summaryOf names each distinct failure once, with the widths it failed at: where it is, and why.
function summaryOf(failures: readonly Failure[]): string {
  const groups = new Map<string, { label: string; reason: string; widths: number[] }>();
  for (const failure of failures) {
    const label = failure.target === "component"
      ? `component "${failure.component}" [${failure.side} ${failure.selector}]`
      : `${failure.fault} [${failure.side} ${failure.selector}${failure.other === undefined ? "" : ` + ${failure.other}`}]`;
    const reason = failure.reason.replace(/\s+/g, " ").trim();
    const key = `${label}\u0000${reason}`;
    const group = groups.get(key) ?? { label, reason, widths: [] };
    group.widths.push(failure.width);
    groups.set(key, group);
  }
  return [...groups.values()]
    .map((group) => `${group.label}: ${group.reason} at ${group.widths.map((width) => `${width}px`).join(", ")}`)
    .join("; ");
}

// lineOf is the one line a case prints.
function lineOf(testCase: SelfTestCase, decision: Decision, verdict: Verdict): string {
  const expected = testCase.expected === "pass" ? "pass" : `defect ${testCase.expected}`;
  const decided = decision.pass ? "pass" : "fail";
  const count = decision.failures.length;
  const failures = count === 0 ? "no failures" : `${count} ${count === 1 ? "failure" : "failures"}: ${summaryOf(decision.failures)}`;
  return `${testCase.id}: expected ${expected}; decision ${decided}; ${verdict.right ? "right" : "WRONG"} ` +
    `(${verdict.explanation}); ${failures}`;
}

async function main(): Promise<number> {
  const browser = await launchChromium();
  let served: Served | undefined;
  try {
    served = await serve();
    const origin = served.origin;
    let right = 0;
    for (const testCase of selfTestCases) {
      const report = await run({
        browser,
        map: testCase.map,
        pages: [testCase.id],
        origin,
        sides: {
          wireframe: { epoch, navigate: navigateSide(origin, "wireframe") },
          live: { epoch, navigate: navigateSide(origin, "live") },
        },
      });
      const decision = decide(report);
      const verdict = judge(testCase, decision);
      if (verdict.right) {
        right++;
      }
      console.log(lineOf(testCase, decision, verdict));
    }
    const passed = right === selfTestCases.length;
    console.log(`visual-selftest: ${right} of ${selfTestCases.length} cases came out as expected: ${passed ? "pass" : "fail"}`);
    return passed ? 0 : 1;
  } finally {
    await browser.close();
    await served?.close();
  }
}

main().then(
  (code) => process.exit(code),
  (error: unknown) => {
    console.error(error instanceof Error ? (error.stack ?? error.message) : String(error));
    process.exit(1);
  },
);
