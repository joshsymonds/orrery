// Screenshots of round 9's accounts wireframe: every section and every figure in it, at 1280 x 900, into a temporary
// directory, or OUT. Fails on a page error or on the page scrolling sideways.
//   PLAYWRIGHT_CORE=<a node_modules directory that holds playwright-core> node shoot.mjs
// CHROMIUM names the browser binary when it is not the system's.
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const page = path.join(here, 'accounts.html');
if (!process.env.PLAYWRIGHT_CORE) { console.error('set PLAYWRIGHT_CORE to a node_modules directory that holds playwright-core'); process.exit(1); }
const require = createRequire(path.join(path.resolve(process.env.PLAYWRIGHT_CORE), '/'));
const { chromium } = require('playwright-core');
import fs from 'node:fs';
import os from 'node:os';
const out = path.resolve(process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), 'orrery-round9-shots-')));
fs.mkdirSync(out, { recursive: true });
const b = await chromium.launch({ executablePath: process.env.CHROMIUM || '/run/current-system/sw/bin/chromium' });
const p = await b.newPage({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 1 });
const errs = [];
p.on('pageerror', e => errs.push(String(e)));
await p.goto('file://' + page);
await p.waitForLoadState('load');
await p.evaluate(() => document.fonts.ready);
const over = await p.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
const secs = await p.$$('main > section');
for (let i = 0; i < secs.length; i++) {
  await secs[i].screenshot({ path: `${out}/s${i + 1}.png` });
  const figs = await secs[i].$$('figure.cell');
  for (let j = 0; j < figs.length; j++) await figs[j].screenshot({ path: `${out}/s${i + 1}-${j + 1}.png` });
}
console.log(JSON.stringify({ out, sections: secs.length, errors: errs, sideways: over }));
await b.close();
if (errs.length || over > 0) process.exit(1);
