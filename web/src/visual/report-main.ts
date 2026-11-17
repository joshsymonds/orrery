// The entry point of `just visual-report`. It runs the report driver over every page of web/visual/map.json, writes the
// screenshots and web/build/visual/report.json, and exits 0 whatever the comparison finds: a mismatch, an incomplete
// navigation or an unmatched selector is a fact the report records, not a failure of this run. A run that cannot start
// (no browser, no build) exits 1, since then there is no report.

import { readFile, mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { faultKinds } from "./faults";
import { runReport } from "./report";

// The recipe runs from the repository root, which holds web/.
const repoRoot = process.cwd();
const outDir = join(repoRoot, "web/build/visual");

// pagesOf lists the pages the map's scenarios belong to, in the order the map gives them.
async function pagesOf(): Promise<string[]> {
  const map = JSON.parse(await readFile(join(repoRoot, "web/visual/map.json"), "utf8")) as {
    scenarios: { page: string }[];
  };
  return [...new Set(map.scenarios.map((scenario) => scenario.page))];
}

async function main(): Promise<void> {
  const pages = await pagesOf();
  await mkdir(outDir, { recursive: true });
  const report = await runReport(pages, {
    repoRoot,
    shot: (name, png) => writeFile(join(outDir, name), png),
    log: (line) => console.log(line),
  });
  await writeFile(join(outDir, "report.json"), `${JSON.stringify(report, null, 2)}\n`);

  const incomplete = report.navigation.filter((record) => !record.completed);
  const matched = report.components.filter((record) => record.properties !== null).length;
  console.log(
    `visual-report: ${report.components.length} component comparisons over ${pages.length} pages, ` +
      `${matched} with both sides matching one element; ${incomplete.length} navigations incomplete; ` +
      `report at ${join("web/build/visual", "report.json")}`,
  );
  for (const record of incomplete) {
    console.log(`  incomplete: ${record.scenario} ${record.side} ${record.width}: ${record.error}`);
  }
  const live = report.faults.filter((fault) => !fault.reference);
  const byKind = faultKinds.map((kind) => `${kind} ${live.filter((fault) => fault.kind === kind).length}`).join(", ");
  const reference = report.faults.length - live.length;
  console.log(`visual-report: live faults by kind: ${byKind}; ${reference} wireframe reference faults (never counted)`);
}

main().then(
  () => process.exit(0),
  (error: unknown) => {
    console.error(error instanceof Error ? (error.stack ?? error.message) : String(error));
    process.exit(1);
  },
);
