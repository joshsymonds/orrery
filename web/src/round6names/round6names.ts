// The names driver of just round6-names. It opens the round-6 wireframe's desk page in Chromium to read the scenario
// registry, and its catalogue page to read each cell's name, then writes the names through names.ts. It runs from the
// repository root as web/build/round6names-tool/round6names.mjs, with the wireframe directory as its first argument and
// the output file as its second. Every failure is printed to stdout, and the run then exits 1.

import { writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { type Browser, chromium, type Page } from "playwright-core";

import { round6Names, type Scenario, serializeNames } from "./names";

// deskPage and cataloguePage are the wireframe's pages the names are read from, relative to the wireframe directory.
const deskPage = "desk.html";
const cataloguePage = "catalog.html";

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// readPage opens the wireframe page file in a fresh page, as the wireframe's own shoot script opens its pages: from
// file://, with its scripts run by the load. It returns what read takes from the loaded page. A page error, raised
// while the page loads or while it is read, fails the read, naming the first one.
async function readPage<T>(browser: Browser, file: string, read: (page: Page) => Promise<T>): Promise<T> {
  const errors: string[] = [];
  const page = await browser.newPage();
  try {
    page.on("pageerror", (error) => {
      errors.push(error.message);
    });
    await page.goto(pathToFileURL(file).href, { waitUntil: "load" });
    const result = await read(page);
    if (errors.length > 0) {
      throw new Error(`${file} raised a page error: ${errors[0]}`);
    }
    return result;
  } finally {
    await page.close();
  }
}

// readScenarios reads each scenario the desk page registers, with whether it has a drawing for the desk and for the
// phone. A scenario is registered with desk and phone functions, either one of which may be left out.
function readScenarios(browser: Browser, wireframe: string): Promise<Scenario[]> {
  return readPage(browser, join(wireframe, deskPage), (page) =>
    page.evaluate(() => {
      const registry = (window as Window & { O?: { scenarios?: { id: string; desk?: unknown; phone?: unknown }[] } }).O;
      return (registry?.scenarios ?? []).map((scenario) => ({
        id: scenario.id,
        desk: typeof scenario.desk === "function",
        phone: typeof scenario.phone === "function",
      }));
    }),
  );
}

// readCells reads the name of each cell of the catalogue: the direct child .name of a .cell, which the catalogue draws
// as Component/State. A label inside a cell's drawing is not a direct child, so it is not read.
function readCells(browser: Browser, wireframe: string): Promise<string[]> {
  return readPage(browser, join(wireframe, cataloguePage), (page) =>
    page.$$eval(".cell > .name", (names) => names.map((name) => name.textContent ?? "")),
  );
}

// run writes the names of the wireframe in the directory it is given to the output file, and returns the exit status: 0
// when the file is written, 1 otherwise. It closes the browser in every case.
async function run(argv: readonly string[]): Promise<number> {
  const [wireframeDir, outputPath] = argv;
  if (wireframeDir === undefined || outputPath === undefined) {
    console.log("round6names: usage: round6names WIREFRAME_DIR OUTPUT_FILE");
    return 1;
  }
  const executablePath = process.env.ORRERY_VISUAL_CHROMIUM ?? "";
  if (executablePath === "") {
    console.log("round6names: ORRERY_VISUAL_CHROMIUM is not set: run round6-names under the visual devenv profile, as just round6-names does");
    return 1;
  }

  const wireframe = resolve(wireframeDir);
  const browser = await chromium.launch({ executablePath });
  try {
    const scenarios = await readScenarios(browser, wireframe);
    if (scenarios.length === 0) {
      console.log(`round6names: ${join(wireframe, deskPage)} registers no scenarios`);
      return 1;
    }
    const cells = await readCells(browser, wireframe);
    if (cells.length === 0) {
      console.log(`round6names: ${join(wireframe, cataloguePage)} draws no cells`);
      return 1;
    }
    const names = round6Names(cells, scenarios);
    await writeFile(outputPath, serializeNames(names));
    console.log(
      `round6names: wrote ${outputPath}: ${Object.keys(names.components).length} components, ${scenarios.length} scenarios from ${cells.length} cells`,
    );
    return 0;
  } finally {
    await browser.close();
  }
}

run(process.argv.slice(2)).then(
  (status) => {
    process.exitCode = status;
  },
  (error: unknown) => {
    console.log(`round6names: ${messageOf(error)}`);
    process.exitCode = 1;
  },
);
