// Screenshots of the round-6 wireframe, into a temporary directory: every desk scenario at 1600 and 1000, every phone
// scenario at 412. Nothing is written into the repository; OUT names the directory to use instead.
//   PLAYWRIGHT_CORE=<a node_modules directory that holds playwright-core> node shoot.mjs [scenario-id ...]
// CHROMIUM names the browser binary when it is not the system's.
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';

const here = path.dirname(fileURLToPath(import.meta.url));
if (!process.env.PLAYWRIGHT_CORE) { console.error('shoot: set PLAYWRIGHT_CORE to a node_modules directory that holds playwright-core'); process.exit(1); }
const require = createRequire(path.join(path.resolve(process.env.PLAYWRIGHT_CORE), '/'));
const { chromium } = require('playwright-core');
const out = path.resolve(process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), 'orrery-round6-shots-')));
fs.mkdirSync(out, { recursive: true });

const want = process.argv.slice(2);
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/run/current-system/sw/bin/chromium', headless: true });
const errors = [];

// The scenario list is the wireframe's own: read it from the page, so a new scenario needs no line here.
const lister = await browser.newPage();
await lister.goto(`file://${here}/desk.html`);
const scenarios = await lister.evaluate(() => O.scenarios.map((s) => ({ id: s.id, desk: !!s.desk, phone: !!s.phone, widths: s.widths || [1600, 1000] })));
await lister.close();

const shoot = async (file, id, width, height, name) => {
  const ctx = await browser.newContext({ viewport: { width, height }, timezoneId: 'UTC', deviceScaleFactor: 1, reducedMotion: 'reduce' });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => errors.push(`${name}: ${e.message}`));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(`${name}: ${m.text()}`); });
  await page.goto(`file://${here}/${file}#${id}&bare`);
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(150);
  await page.screenshot({ path: path.join(out, `${name}.png`) });
  // A screen that scrolls sideways is a layout that does not fit its width.
  const wide = await page.evaluate(() => [...document.querySelectorAll('.app, .app *')].filter((el) => el.scrollWidth > el.clientWidth + 1 && getComputedStyle(el).overflowX === 'visible' && el.clientWidth > 0 && !el.closest('.show-tip') && !el.querySelector('.show-tip')).slice(0, 3).map((el) => `${el.className || el.tagName} ${el.scrollWidth}>${el.clientWidth}`));
  if (wide.length) errors.push(`${name}: overflows its box: ${wide.join('; ')}`);
  await ctx.close();
};

let count = 0;
for (const s of scenarios) {
  if (want.length && !want.includes(s.id)) continue;
  if (s.desk) for (const width of s.widths) { await shoot('desk.html', s.id, width, 1000, `${s.id}-${width}`); count += 1; }
  if (s.phone) { await shoot('phone.html', s.id, 412, 892, `${s.id}-412`); count += 1; }
}
await browser.close();
console.log(`${count} shots in ${out}`);
if (errors.length) { console.error(errors.join('\n')); process.exit(1); }
