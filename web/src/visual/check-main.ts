// The entry point of `just visual-check PAGE...`. It runs the report driver over the named pages of web/visual/map.json,
// or over all of them when none is named, writes the screenshots and web/build/visual/report.json the way report-main
// does, and prints one line per failure of the pass/fail decision. It exits 0 when the decision passes and 1 when it
// fails. A name that is not a page of the map exits 1 before any browser or fixture starts, and so does a run that cannot
// start (no browser, no build), with its error.

import { mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { decide, type Failure } from "./decide";
import { pagesOf, runReport } from "./report";

// The recipe runs from the repository root, which holds web/.
const repoRoot = process.cwd();
const outDir = join(repoRoot, "web/build/visual");

// failureLine prints one failure on one line: what failed, where, and why.
function failureLine(failure: Failure): string {
  const where = `${failure.scenario} ${failure.width}px`;
  const reason = failure.reason.replace(/\s+/g, " ").trim();
  if (failure.target === "component") {
    return `FAIL ${where} component "${failure.component}" [${failure.side} ${failure.selector}]: ${reason}`;
  }
  const other = failure.other === undefined ? "" : ` + ${failure.other}`;
  return `FAIL ${where} fault ${failure.fault} [${failure.side} ${failure.selector}${other}]: ${reason}`;
}

async function main(): Promise<number> {
  const known = await pagesOf(repoRoot);
  const named = process.argv.slice(2);
  const unknown = named.filter((page) => !known.includes(page));
  if (unknown.length > 0) {
    for (const page of unknown) {
      console.error(`visual-check: "${page}" is not a page of web/visual/map.json (its pages: ${known.join(", ")})`);
    }
    return 1;
  }
  const pages = named.length === 0 ? known : [...new Set(named)];

  // The directory is rebuilt, so a screenshot from an earlier run cannot stand for one this run did not take.
  await rm(outDir, { recursive: true, force: true });
  await mkdir(outDir, { recursive: true });
  const report = await runReport(pages, {
    repoRoot,
    shot: (name, png) => writeFile(join(outDir, name), png),
    log: (line) => console.log(line),
  });
  await writeFile(join(outDir, "report.json"), `${JSON.stringify(report, null, 2)}\n`);

  const { pass, failures } = decide(report);
  for (const failure of failures) {
    console.log(failureLine(failure));
  }
  console.log(`visual-check: ${failures.length} failures over ${pages.length} pages: ${pass ? "pass" : "fail"}`);
  return pass ? 0 : 1;
}

main().then(
  (code) => process.exit(code),
  (error: unknown) => {
    console.error(error instanceof Error ? (error.stack ?? error.message) : String(error));
    process.exit(1);
  },
);
