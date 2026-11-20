// The shoot driver of just shoot. It serves the catalog build from node:http on 127.0.0.1, opens the catalog's index in
// Chromium to learn the story names, and writes a screenshot of each named story at each width under web/build/shots,
// with motion frozen as the visual check froze its pages. A run overwrites the shots it writes and keeps the others, so
// shoots run one after another leave every shot they wrote. It runs from the repository root as
// web/build/shoot-tool/shoot.mjs, with the catalog directory as its first argument and the arguments of just shoot after
// it. Every failure is printed to stdout, and the run then exits 1.

import { mkdir, readFile } from "node:fs/promises";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { dirname, extname, join, resolve, sep } from "node:path";

import { type Browser, type BrowserContext, chromium, type Page } from "playwright-core";

import { liveEpoch } from "../fixtures/clock";
import { parseShootArgs, resolveNames } from "./args";

// shotsDir is where the screenshots are written, relative to the repository root.
const shotsDir = "web/build/shots";

// indexWidth is the viewport the index is read at. The names it lists do not depend on the width.
const indexWidth = 1600;

// viewportHeight is the second dimension of every viewport.
const viewportHeight = 900;

// settleStepMs is the pause between two reads of a story, and the time the page's clock runs in each round of a settle.
const settleStepMs = 100;

// settleTimeoutMs is how long a story has to draw and settle.
const settleTimeoutMs = 10_000;

// contentTypes are the types the server answers with, by extension. A file of any other extension is not served.
const contentTypes = new Map([
  [".html", "text/html; charset=utf-8"],
  [".js", "text/javascript; charset=utf-8"],
  [".css", "text/css; charset=utf-8"],
  [".ttf", "font/ttf"],
  [".woff2", "font/woff2"],
  [".png", "image/png"],
]);

// freezeCss turns off animations, transitions and the caret, as the visual check froze its pages.
const freezeCss = "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}";

// Draw is what the page shows of its story: the innerHTML of #story once it holds an element, and the text of the first
// failure element drawn, when one is.
interface Draw {
  html: string | null;
  failure: string | null;
}

// Served is the catalog's server: its origin, and the server itself, which closes it.
interface Served {
  origin: string;
  server: Server;
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

const delay = (ms: number): Promise<void> => new Promise((resolveDelay) => setTimeout(resolveDelay, ms));

// fileFor is the file under root that a request's URL names, or null when it names none inside root. The path is decoded
// and resolved, and the file must lie inside root, so a path that climbs out of it names nothing.
function fileFor(root: string, url: string): string | null {
  let path: string;
  try {
    path = decodeURIComponent(new URL(url, "http://127.0.0.1").pathname);
  } catch {
    return null;
  }
  const file = resolve(root, `.${path === "/" ? "/index.html" : path}`);
  return file.startsWith(`${root}${sep}`) ? file : null;
}

function notFound(response: ServerResponse): void {
  response.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
  response.end("not found");
}

// answer sends the file a GET names under root, or a 404 when it names none, or when it cannot be read.
async function answer(root: string, request: IncomingMessage, response: ServerResponse): Promise<void> {
  const file = request.method === "GET" ? fileFor(root, request.url ?? "/") : null;
  const type = file === null ? undefined : contentTypes.get(extname(file));
  if (file === null || type === undefined) {
    notFound(response);
    return;
  }
  try {
    const body = await readFile(file);
    response.writeHead(200, { "content-type": type });
    response.end(body);
  } catch {
    notFound(response);
  }
}

// serve starts a server for the files under root, on 127.0.0.1 at a free port.
async function serve(root: string): Promise<Served> {
  const server = createServer((request, response) => {
    answer(root, request, response).catch(() => response.destroy());
  });
  await new Promise<void>((listening, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => listening());
  });
  const { port } = server.address() as AddressInfo;
  return { origin: `http://127.0.0.1:${port}`, server };
}

function closeServer(server: Server): Promise<void> {
  return new Promise((closed, reject) => {
    server.closeAllConnections();
    server.close((error) => (error === undefined ? closed() : reject(error)));
  });
}

// installFreeze puts the freeze style into the page before its own, so the page never animates or shows a caret.
function installFreeze(css: string): void {
  const install = () => {
    const style = document.createElement("style");
    style.textContent = css;
    document.documentElement.append(style);
  };
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", install, { once: true });
  } else {
    install();
  }
}

// openPage opens a page of the given width in a fresh context, frozen as the visual check froze its pages: the viewport
// is width by viewportHeight, and every request off origin is aborted. Its clock is installed and paused at liveEpoch, the
// instant the fixtures are set at. Each page error is recorded in errors.
async function openPage(
  browser: Browser,
  origin: string,
  width: number,
  errors: string[],
): Promise<{ context: BrowserContext; page: Page }> {
  const context = await browser.newContext({
    viewport: { width, height: viewportHeight },
    deviceScaleFactor: 1,
    timezoneId: "UTC",
    locale: "en-US",
  });
  try {
    await context.route("**/*", (route) =>
      new URL(route.request().url()).origin === origin ? route.continue() : route.abort(),
    );
    const page = await context.newPage();
    page.on("pageerror", (error) => {
      errors.push(error.message);
    });
    await page.addInitScript(installFreeze, freezeCss);
    await page.clock.install({ time: new Date(liveEpoch) });
    await page.clock.pauseAt(new Date(liveEpoch));
    return { context, page };
  } catch (error) {
    await context.close();
    throw error;
  }
}

// indexNames is the name of each story the catalog's index lists, in the order it lists them.
async function indexNames(browser: Browser, origin: string): Promise<string[]> {
  const errors: string[] = [];
  const { context, page } = await openPage(browser, origin, indexWidth, errors);
  try {
    const response = await page.goto(`${origin}/index.html`, { waitUntil: "load" });
    if (response?.ok() !== true) {
      throw new Error(`the catalog has no index.html under ${origin}: run just catalog or just shoot to build it`);
    }
    const names = await page.$$eval("a[data-story]", (links) => links.map((link) => link.getAttribute("data-story") ?? ""));
    if (errors.length > 0) {
      throw new Error(`the catalog index raised a page error: ${errors[0]}`);
    }
    return names;
  } finally {
    await context.close();
  }
}

// readDraw reads what the page shows of its story.
function readDraw(page: Page): Promise<Draw> {
  return page.evaluate(() => {
    const story = document.getElementById("story");
    const failure = document.querySelector("[data-story-failure]");
    return {
      html: story !== null && story.children.length > 0 ? story.innerHTML : null,
      failure: failure === null ? null : (failure.textContent ?? ""),
    };
  });
}

// checkDraw fails the story when the page draws its failure element or has raised a page error.
function checkDraw(draw: Draw, errors: readonly string[], name: string): void {
  if (draw.failure !== null) {
    throw new Error(`story ${name} draws its failure element: ${draw.failure}`);
  }
  if (errors.length > 0) {
    throw new Error(`story ${name} raised a page error: ${errors[0]}`);
  }
}

// settle waits until the story is drawn and its #story stops changing: the same innerHTML on two reads settleStepMs
// apart on the node side. Each round lets the page's clock run settleStepMs, so a timer the page sets fires as it would
// live. It fails once settleTimeoutMs has passed without a settled story.
async function settle(page: Page, errors: readonly string[], name: string): Promise<void> {
  const deadline = Date.now() + settleTimeoutMs;
  let previous: string | null = null;
  for (;;) {
    await page.clock.runFor(settleStepMs);
    const draw = await readDraw(page);
    checkDraw(draw, errors, name);
    if (draw.html !== null && draw.html === previous) {
      return;
    }
    previous = draw.html;
    if (Date.now() >= deadline) {
      throw new Error(`story ${name} did not draw and settle within ${settleTimeoutMs / 1000} s`);
    }
    await delay(settleStepMs);
  }
}

// capture draws the story name at width, writes its full-height screenshot and returns the path it wrote.
async function capture(browser: Browser, origin: string, name: string, width: number): Promise<string> {
  const errors: string[] = [];
  const { context, page } = await openPage(browser, origin, width, errors);
  try {
    await page.goto(`${origin}/index.html?story=${encodeURIComponent(name)}`, { waitUntil: "load" });
    await settle(page, errors, name);
    const path = join(shotsDir, `${name}-${width}.png`);
    const height = await page.evaluate(() => document.documentElement.scrollHeight);
    await mkdir(dirname(path), { recursive: true });
    await page.screenshot({
      path,
      fullPage: true,
      clip: { x: 0, y: 0, width, height },
      animations: "disabled",
      caret: "hide",
    });
    return path;
  } finally {
    await context.close();
  }
}

// run shoots what the arguments ask for, and returns the exit status: 0 when every screenshot is written, 1 otherwise.
// It serves the catalog directory it is given, and it closes the server and the browser in every case.
async function run(argv: readonly string[]): Promise<number> {
  const [catalogDir, ...rest] = argv;
  if (catalogDir === undefined) {
    console.log("shoot: no catalog directory was given");
    return 1;
  }
  const parsed = parseShootArgs(rest);
  if ("error" in parsed) {
    console.log(parsed.error);
    return 1;
  }
  const executablePath = process.env.ORRERY_VISUAL_CHROMIUM ?? "";
  if (executablePath === "") {
    console.log("shoot: ORRERY_VISUAL_CHROMIUM is not set: run shoot under the visual devenv profile, as just shoot does");
    return 1;
  }

  const served = await serve(resolve(catalogDir));
  try {
    const browser = await chromium.launch({ executablePath, args: ["--font-render-hinting=none"] });
    try {
      const resolved = resolveNames(parsed.names, await indexNames(browser, served.origin));
      if ("error" in resolved) {
        console.log(resolved.error);
        return 1;
      }
      for (const name of resolved.names) {
        for (const width of parsed.widths) {
          console.log(await capture(browser, served.origin, name, width));
        }
      }
      return 0;
    } finally {
      await browser.close();
    }
  } finally {
    await closeServer(served.server);
  }
}

run(process.argv.slice(2)).then(
  (status) => {
    process.exitCode = status;
  },
  (error: unknown) => {
    console.log(`shoot: ${messageOf(error)}`);
    process.exitCode = 1;
  },
);
